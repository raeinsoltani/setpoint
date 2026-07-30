// Workload shapes, transliterated 1:1 from sim/demo/simulate.py.
//
// They live here as functions of time rather than as hand-written k6 stage arrays
// so that the simulated and real workloads cannot drift apart. The Phase 6 validity
// argument depends on comparing like with like: if the cluster runs a different
// curve than the simulator, agreement between them proves nothing and disagreement
// diagnoses nothing.
//
// Keep these in lockstep with sim/demo/simulate.py. If one changes, change both.

export const DURATION = 1800; // seconds, matching the simulator
export const TARGET_RPS_PER_REPLICA = 100; // policy.target in config.yaml

// Sudden 4x step for ten minutes. The workload the proposal's motivation is written
// around — and, per the Phase 6 framing note, the one where prediction provably
// cannot help, because no forecaster anticipates an instantaneous step.
export function spike(t) {
  if (t >= 600 && t < 1200) return 1300;
  return 300;
}

// Smooth rise and fall, like a daily traffic curve. Prediction's best case.
export function diurnal(t) {
  const s = Math.sin((Math.PI * t) / DURATION);
  return 300 + 900 * s * s;
}

// Three bursts, each followed by a short trough. Stresses anti-flapping: the trough
// is an invitation to scale down just before the next burst arrives.
export function bursty(t) {
  for (const start of [300, 800, 1300]) {
    if (t >= start && t < start + 120) return 1200;
    if (t >= start + 120 && t < start + 180) return 200;
  }
  return 400;
}

// Added for this project — the workload prediction actually exists for. A single
// sustained trend, where a forecaster has something real to extrapolate and pods
// have time to become ready before the load that needs them arrives.
export function ramp(t) {
  if (t < 300) return 200;
  if (t < 1500) return 200 + (1000 * (t - 300)) / 1200;
  return 1200;
}

// stagesFrom samples a pattern into k6 ramping-arrival-rate stages.
//
// Every sample becomes a zero-duration jump followed by a hold, i.e. a staircase
// rather than linear interpolation. Uniform treatment matters more than smoothness
// here: `spike` and `bursty` are genuine step functions that linear interpolation
// would round off, softening exactly the transient the experiment is measuring. At
// a 30s step against a 5s scrape interval and a 1m rate() window, the staircase is
// indistinguishable from the smooth curve in what Prometheus reports for `diurnal`
// and `ramp` anyway.
export function stagesFrom(pattern, stepSeconds = 30) {
  const stages = [];
  for (let t = 0; t < DURATION; t += stepSeconds) {
    const rps = Math.round(pattern(t));
    stages.push({ target: rps, duration: '0s' });
    stages.push({ target: rps, duration: `${stepSeconds}s` });
  }
  return stages;
}

// scenario builds the k6 scenario for a pattern.
//
// An open model (arrival rate) rather than a closed one (fixed VUs) is required, not
// stylistic. Under a closed model, when the service slows down the load generator
// automatically sends less — offered load would fall exactly when the autoscaler is
// being tested on its response to high load, which flatters every arm and hides the
// under-provisioning the evaluation is supposed to measure.
export function scenario(pattern, stepSeconds = 30) {
  const stages = stagesFrom(pattern, stepSeconds);
  const peak = Math.max(...stages.map((s) => s.target));

  return {
    executor: 'ramping-arrival-rate',
    startRate: stages[0].target,
    timeUnit: '1s',
    stages,
    // Sized for the peak rate at a pessimistic 250ms of latency. Too few VUs and k6
    // reports "insufficient VUs" and silently delivers less load than requested,
    // which looks like the service coping.
    preAllocatedVUs: Math.ceil(peak * 0.25),
    maxVUs: Math.ceil(peak * 1.0),
  };
}

// Common thresholds. These are recorded as part of the run rather than used to gate
// it: a threshold breach under a deliberately overloaded arm is a *result*, not a
// test failure, so k6 must still exit successfully and write its summary.
export const thresholds = {
  'http_req_failed{expected_response:true}': [
    { threshold: 'rate<0.05', abortOnFail: false },
  ],
  http_req_duration: [
    { threshold: 'p(95)<500', abortOnFail: false },
    { threshold: 'p(99)<1000', abortOnFail: false },
  ],
};
