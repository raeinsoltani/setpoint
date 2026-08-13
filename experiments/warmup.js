// Constant-rate warmup, held at the measured pattern's own starting rate.
//
// Driven by experiments/run.sh before every measured run. It exists for two reasons
// that both produce a silently wrong run if skipped:
//
//   1. `http_requests_total` does not exist until the first request is served, and
//      `rate(...[1m])` needs a full minute of samples before it reports anything
//      meaningful. A measurement that starts at t=0 spends its first minute
//      measuring the metric pipeline filling up rather than the policy.
//   2. Each arm should enter the measured window at *its own* equilibrium for the
//      starting load. Starting every arm cold adds an identical climb to the front
//      of every trace, which dilutes exactly the contrast the experiment is for.
//
// The rate is `pattern(0)` read from the same module the measured run uses, so the
// warmup cannot drift away from the workload it is warming up for.
import http from 'k6/http';
import { check } from 'k6';
import * as patterns from '../test/load/lib/patterns.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const NAME = __ENV.PATTERN;
const SECONDS = Number(__ENV.WARMUP_SECONDS || 120);

const pattern = patterns[NAME];
if (typeof pattern !== 'function') {
  throw new Error(`unknown pattern '${NAME}'; expected one of spike, diurnal, bursty, ramp`);
}
const RATE = Math.round(pattern(0));

export const options = {
  scenarios: {
    warmup: {
      // Open model, as in the measured run — see the note on `scenario` in
      // test/load/lib/patterns.js. A closed model would send less load exactly when
      // the service is slow, which is the state the warmup needs to establish.
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: `${SECONDS}s`,
      preAllocatedVUs: Math.ceil(RATE * 0.25),
      maxVUs: Math.ceil(RATE * 1.0),
    },
  },
  // No thresholds. A warmup cannot fail the experiment: it is establishing the
  // starting state, and whatever latency it sees is part of that state, not a result.
  //
  // The warmup must use the same connection model as the measured run (§11.13), or it
  // establishes an equilibrium on a fleet that is not actually sharing the load, and
  // the measured window starts by unwinding that instead of measuring the policy.
  ...patterns.CONNECTION_OPTIONS,
};

export default function () {
  const res = http.get(`${BASE_URL}/`);
  check(res, { 'status is 200': (r) => r.status === 200 });
}
