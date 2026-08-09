# Lab notebook

Every measurement taken, every finding that does not survive in the code, and the reasoning
behind decisions a reader of the source could not reconstruct. Kept so the thesis can be
written from records rather than from memory.

**Append, never rewrite.** A number that later turns out wrong gets a correction next to it,
with both values and the date — a notebook that silently updates itself is not evidence.

Conventions: dates are Gregorian. "req/s" is always *total* offered load unless the line says
per-replica. All Go coverage figures are `go test -cover` statement coverage.

---

## 1. Environment of record

For the reproducibility section. Captured 2026-07-27, re-verified 2026-07-30.

| Component | Version |
|---|---|
| Go | 1.26.4 |
| kubectl | v1.36.1 |
| Kubernetes (Docker Desktop) | v1.36.1, single node `desktop-control-plane` |
| Kubernetes (kind) | v1.36.1, 3 nodes |
| Helm | v4.2.0 |
| kind | v0.32.0 |
| k6 | v2.1.0 |
| Python (simulator) | 3.14.5 (venv in `sim/` is 3.11.15) |
| git | 2.50.1 |
| kube-prometheus-stack | prometheus-operator v0.92.1 |
| Base images | `golang:1.26-alpine` build, `gcr.io/distroless/static-debian12:nonroot` runtime |

Host: macOS (Darwin 25.5.0), arm64. Docker Desktop VM: **14 CPU, 7.749 GiB RAM, 110 pod
slots**, ~6% CPU requested at rest.

Built image sizes: `setpoint:dev` 40.6 MB, `sample-app:dev` 17.6 MB.

### Controller configuration used for every run so far

```
interval_seconds              15     (matches HPA's sync period)
policy.target                 100.0  (req/s per ready replica)
policy.tolerance              0.10   (10% dead-band, as HPA)
policy.min_replicas           1
policy.max_replicas           20     (was 10 — see §6.1)
forecaster.type               ewma
forecaster.horizon            3      (3 x 15s = 45s of lead time)
forecaster.alpha              0.5
stabilization_window_seconds  90
cooldown_seconds              0      (disabled)
max_step                      0      (disabled)
```

Prometheus: 15s global scrape interval, **5s** for the two ServiceMonitors that matter,
`rate(...[1m])` window. Retention 6h.

---

## 2. Node capacity probe — 2026-07-27

Retired a risk the plan had listed as a likely blocker.

- Node at rest: 6% CPU requested, 110 pod slots free.
- Probe: 10 replicas at `50m` CPU request → **10/10 Ready**, node reached **10% CPU**.

**Conclusion:** a single-node Docker Desktop cluster comfortably hosts the fleet.
`max_replicas` can exceed 10, and the sample app can carry realistic CPU requests rather
than artificially tiny ones. The plan's "laptop cannot sustain 10 replicas" risk was retired
on this evidence.

Later confirmed at scale: **20 replicas request 4 of the node's 14 CPU**, and a live run
reached 16 replicas without memory pressure on any node.

---

## 3. The closed-loop feedback discovery — 2026-07-27

The project's central result. Found by measurement, not by reading.

### 3.1 The mechanism

The prototype forecast **per-replica** load. That signal is `total_load / ready_replicas`,
and `ready_replicas` is the quantity the autoscaler *sets*. So the forecaster extrapolates a
signal its own actions move, and the loop has positive gain:

```
scale up → per-replica load falls → forecast trends down → scale down
         → per-replica load rises → forecast trends up   → scale up
```

Total arrival rate is **exogenous** — the autoscaler cannot change how many requests
arrive — so forecasting it has structurally zero gain on that path: `ceil(predicted_total/T)`
has no dependence on the current replica count.

### 3.2 Simulator measurements (Python, `sim/demo/simulate.py`)

Spike workload, per-replica forecasting vs threshold:

| Metric | `PredictivePerReplica` | `Threshold` |
|---|---|---|
| Replica changes under constant load | **17** | 4 |
| Cost (replica-seconds) | +23% | baseline |
| SLA outcome | identical | identical |

Forecasting total load instead:

| Metric | `PredictiveTotalLoad` | `Threshold` | `PredictivePerReplica` |
|---|---|---|---|
| Replica changes (spike) | **3** | 4 | 17 |
| Cost | = threshold | baseline | +23% |
| SLA violations (diurnal) | **0%** | 6.7% | 1.7% |

23% more cost for *no* SLA benefit is the sharpest way to state the per-replica variant's
failure: it is strictly worse on both axes.

### 3.3 Unit-test measurements (Go, `internal/policy`) — 2026-07-30

Same phenomenon, isolated. `TestPerReplicaForecastingOscillatesUnderConstantLoad`: closed
loop, constant total load **500 req/s**, target **100**, EWMA(horizon 3, α 0.5), 40 steps,
first 10 discarded as the initial climb, **no stabilizer configured**.

| Policy | Replica changes over 30 settled steps | Fleet |
|---|---|---|
| `PredictiveTotalLoad` | **0** | settles on 5 (=500/100) and never moves |
| `PredictivePerReplica` | **29** | oscillates between **1 and 8** |

The stabilizer is deliberately absent. It is a damper, and damping the symptom hides the
property under test, which lives in the policy's own dynamics. **These numbers and the
simulator's are not interchangeable** — the simulator's are what the shipped configuration
does, the unit test's are what the policy does alone. Both belong in the evaluation, labelled.

**Open question for Phase 7.1:** how much of the oscillation does the stabilizer mask, and at
what cost in over-provisioning? Not measured yet. Do not guess — a 90s window over
alternating high/low recommendations may suppress the visible flapping while leaving the
fleet parked high, which would be a *different* failure (cost, not instability) and is worth
its own figure.

---

## 4. Correctness findings in the control path

### 4.1 `spec.replicas` vs `status.readyReplicas` — 2026-07-27 (Phase 1)

The Python prototype passed `scaler.get_replicas()`, i.e. **`.spec.replicas`**, as the HPA
formula base (`sim/autoscaler/controller.py:36-37`) while the metric is measured *per ready
replica*. During pod startup the two diverge and the autoscaler over-scales: it divides
observed load by the pods it has *asked for* rather than the pods actually serving.

Worked example, now `TestFormulaBaseIsReadyNotSpec`: spec=4, ready=2, metric=200, target=100.
Correct answer is 4 (`ceil(2 × 2.0)`). Using spec gives **8** — double.

The Go `Scaler` returns both numbers; the policy uses ready. Documented as a deliberate
improvement over the prototype, not a port.

But the *apply* decision compares against `.spec.replicas`, not ready — the question there is
"has the cluster already been asked for this many", and pods still starting have been asked
for. Comparing against ready would re-issue the same scale-up every interval until the pods
came up.

### 4.2 Three defects found by writing the policy tests — 2026-07-30

Each was found by asserting intended behaviour and watching the assertion fail. None was
visible by reading the code.

**(a) `Holt.Update` returned a negative prediction on the NaN path.** The early return for
NaN/Inf skipped the `math.Max(0, ...)` floor the normal path applies. Measured **-450** after
feeding 500, 400, 100, 10 then NaN with horizon 3, α 0.5, β 0.5. `FleetFor` would silently
floor that to `min_replicas` — **a scale-down caused by a missing scrape rather than by
absent load**, and one that looks perfectly normal in the logs.

**(b) The cooldown was global, not per-direction.** It contradicted its own doc comment
("scale-up is applied immediately") *and* the plan, which specifies a minimum interval
between actions **in the same direction**. A scale-down followed by load returning blocked
the recovery scale-up for the full cooldown — precisely the case where waiting costs an SLA
breach. Now keyed on direction.

**(c) `int32(math.Ceil(...))` relied on implementation-defined behaviour.** Go specifies that
a float-to-int conversion whose result the target type cannot represent "succeeds but the
result value is implementation-dependent". So a very large recommendation may saturate to
`MaxInt32` on one architecture and wrap **negative** on another — and `clamp` would read a
negative as "below min" and scale the fleet **down** under extreme load. The tests pass on
this arm64 host either way, which is exactly why it needed an explicit guard: the container
is built for a different architecture than it is developed on. `ceilToReplicas` now saturates
deliberately.

### 4.3 The tolerance edge is deliberately unspecified — 2026-07-30

`110/100` evaluates to `1.1000000000000001`, so `|ratio − 1| = 0.10000000000000009 > 0.10`
and the fleet moves. A different metric/target pair with the same nominal ratio can land the
other side and hold. Stock HPA computes the ratio identically and inherits the same fuzziness.

No test asserts the exact edge, because pinning it would pin an artefact of binary floating
point rather than a design choice. **Have this answer ready for the viva** — "what happens at
exactly 10%?" is an obvious question and "it depends on floating-point representation, and
nothing in the design depends on which side it falls" is the correct answer.

---

## 5. The sample app calibration

The load-bearing detail for comparison fairness, and the answer to "how do you know the
comparison is fair?"

### 5.1 The argument

```
target throughput  = 100 req/s per replica      (policy.target)
CPU cost per req   = 2 ms                        (WORK_CPU_MS)
CPU at target      = 100 × 2ms = 200ms/s = 200m
CPU request        = 200m
```

A replica serving exactly its target throughput sits at exactly **100% of its CPU request**.
That single equality makes `hpa-cpu` (HPA on CPU at `averageUtilization: 100`) and
`ours-threshold` (our policy on req/s at target 100) aim at the *same operating point*.
Without it the two arms would scale to different setpoints and any difference between them
would be unattributable. Setting the usual `averageUtilization: 70` would silently break this.

It also requires the work to be genuinely **CPU-bound**. A service that slept instead of
computing would show flat CPU under rising load, `hpa-cpu` would never react, and the
baseline would be a straw man.

### 5.2 Measurements — 2026-07-30

Sequential requests, `GOMAXPROCS=1`, local (`make calibrate`):

| Quantity | Measured | Expected |
|---|---|---|
| Latency per request | **2.096 ms** | 2 ms + HTTP overhead |
| Single-core throughput | **477 req/s** | ~500 |
| Implied CPU at 100 req/s | **210m** | 200m — within 5% |
| `?cpu_ms=8` override | **8.111 ms** | 4× the 2ms case ✓ |

In-cluster, under the k6 spike run: **median latency 2.31 ms**, `min` 2.19 ms. The calibration
holds in the container as well as on the host.

One replica at the original `400m` limit saturated at **192 req/s** measured through a
port-forward — consistent with 400m ÷ 2ms = 200 req/s.

---

## 6. Findings that only running it could produce — 2026-07-30

Every item here would have silently corrupted the Phase 6 comparison rather than failing
loudly. This section is the strongest material for an "engineering challenges" chapter.

### 6.1 `max_replicas: 10` would have destroyed the comparison

All four workloads peak at 1200–1300 req/s. At a target of 100 that needs 12–13 replicas:

| Workload | Range (req/s) | Replicas needed at peak |
|---|---|---|
| `spike` | 300 → 1300 | **13** |
| `diurnal` | 300 → 1200 | 12 |
| `bursty` | 200 → 1200 | 12 |
| `ramp` | 200 → 1200 | 12 |

With a ceiling of 10, **every arm** would clip during peak, saturating at 10 replicas serving
130 req/s each — identically above the SLA line of 110. The SLA-violation metric would then
stop distinguishing the arms exactly where the comparison matters most, and predictive would
appear no better than reactive for a reason having nothing to do with either policy.

Raised to **20**, matching `MAX_R` in the simulator (which had been 20 all along — the Go
config's 10 was an unnoticed inconsistency between the two implementations). Confirmed
empirically: a live spike run reached **13, then 16** replicas.

### 6.2 The observability collapse under overload — **the most interesting finding**

Offered 1300 req/s against a single replica limited to `400m` CPU, the first k6 run **did not
scale at all**. It sat at 1 replica through the entire spike.

Cause chain, verified from kubelet events, Prometheus target state and the controller's logs:

1. `400m` CPU limit ⇒ CFS quota of **40 ms per 100 ms period** ⇒ the container is frozen for
   **60% of wall-clock time** under sustained overload.
2. `/healthz` cannot answer within the readiness probe's timeout ⇒ `readyReplicas` → 0.
3. `/healthz` cannot answer within the liveness probe's timeout ⇒ **kubelet restarted the
   container 3 times**, which made the overload worse and destroyed metric history.
4. `/metrics` cannot answer within the 4s scrape timeout ⇒ target `down`, error
   `context deadline exceeded` ⇒ `rate(http_requests_total[1m])` returns **nothing**.
5. The collector reads the empty result as **zero load** — *correctly*, because an empty
   vector legitimately means no pods are up — and the policy holds the fleet at
   `min_replicas` while load climbs.

> **An autoscaler driven by application metrics goes blind exactly when it is needed most,
> unless the application stays observable under overload.**

This is not a defect in the autoscaler. Its handling of an empty vector is deliberate and
right. It is a property of the *architecture*, and it is adjacent to the thesis contribution:
both are feedback paths that fail while every component behaves as specified. Worth a
paragraph in the evaluation and a slide in the defense.

Three fixes:
- **CPU limit 400m → 1000m.** The one that mattered. Costs nothing in comparison fairness
  because HPA's utilization maths uses the CPU **request**, not the limit — §5.1 is untouched.
  The limit only governs how gracefully a replica degrades while it waits for company.
- **Bounded concurrency (16 in flight) with immediate 503 shedding**, counted in
  `http_requests_total` so offered load stays measurable whether or not a request was served.
  New counter `http_requests_shed_total`.
- **Liveness probe made tolerant** (timeout 5s, failureThreshold 6, period 15s). A liveness
  probe must detect a *deadlocked* process, never a slow one; restarting a busy process is
  almost never the right response.

After the fixes, the identical run delivered the full 1300 req/s with zero restarts (§7).

### 6.3 `kubectl port-forward` cannot drive load at a fleet

`kubectl port-forward svc/sample` resolves **one** endpoint and holds it. Every experiment
would have been capped at a single replica's capacity (~200 req/s) while the fleet scaled up
underneath, unused — and all arms would have looked identical because none was ever really
loaded. `svc/sample` is now a `LoadBalancer` so kube-proxy balances across all ready
endpoints.

### 6.4 Three silent-failure traps in the monitoring stack

- **`targetLabels: [app]` on the ServiceMonitor is mandatory.** The autoscaler's PromQL
  selects on `app="sample"`, but that label lives on the Kubernetes *Service*, not in the
  metrics the app exposes. Without promoting it, the query matches nothing, returns an empty
  vector, and the fleet sits at `min_replicas`. Silent.
- **`serviceMonitorSelectorNilUsesHelmValues` defaults to `true`.** Prometheus then discovers
  only ServiceMonitors carrying the Helm release label, so hand-written ones are ignored with
  no targets and nothing in any log explaining why. Set to `false` (with the three sibling
  selectors) — correct for a dedicated evaluation cluster, wrong for a shared one.
- **`http_requests_total` does not exist until the first request.** An empty query result
  before any traffic is correct behaviour, not a broken pipeline. Cost ~10 minutes of
  misdiagnosis; noted so it costs nobody else any.

### 6.5 Portability problems only the kind run exposed

- **kind does not implement `LoadBalancer` services.** `svc/sample` sits at `<pending>` and
  `localhost:8080` is dead. Fixed by pinning `nodePort: 30080` on the Service and mapping
  host 8080 onto it via `extraPortMappings`, so `make load` is byte-identical on both
  clusters.
- **Both clusters want host port 8080.** kind reports the clash as
  `Bind for 0.0.0.0:8080 failed: port is already allocated` during cluster creation, with
  nothing identifying the sample app as the owner. Tear the Docker Desktop sample app down
  first.
- **`kind load` is mandatory** — kind nodes cannot see the host's Docker images, and both
  manifests use `imagePullPolicy: IfNotPresent` against local tags.

### 6.6 Registry mirror breaks non-Docker-Hub pulls on this machine

kubelet pulls from `quay.io` fail:

```
failed to resolve reference "quay.io/prometheus-operator/prometheus-operator:v0.92.1":
unexpected status from HEAD request to http://registry-mirror:1273/v2/...?ns=quay.io:
500 Internal Server Error
```

`docker pull <image>` on the host succeeds and primes the containerd store that Docker
Desktop's kubelet shares, so the pod starts on its next retry with no manifest change.
Expect it for prometheus-adapter, metrics-server and KEDA. **Belongs in the reproducibility
section**: a reviewer on an unfiltered connection will never see it; one behind the same
mirror hits it immediately.

**Correction, 2026-08-09.** The expectation above was too broad. metrics-server installed
from the chart with **no intervention**: `registry.k8s.io/metrics-server/metrics-server:v0.8.1`
pulled straight through the mirror and the Deployment went Available in ~30s. So the
failure is specific to `quay.io` (and, per the original observation, `ghcr.io`) — not to
non-Docker-Hub registries in general, which is how the earlier note reads.
`registry.k8s.io` is fine. Whether prometheus-adapter and KEDA are affected is still
untested; both are on `ghcr.io`, so the original expectation probably does hold for them.

---

## 7. Live-run results

### 7.1 First end-to-end run, `predictive-total-load` — 2026-07-30

Python concurrent load generator (k6 not yet installed), ~217 req/s sustained.

```
 1 → 2   ready=1  metric= 91.2  scale-up (immediate)
 2 → 3   ready=2  metric= 75.1  scale-up (immediate)
 3 → 4   ready=3  metric= 70.0  scale-up (immediate)
 4 → 3   ready=4  metric= 54.6  scale-down (stabilized)
```

**The observed metric was below target (100) on every scale-up.** Capacity was added because
*predicted total load* was rising — at the last sample, `autoscaler_predicted_value` 221.7
against `autoscaler_metric_value` 54.6. This is the predictive policy leading the reactive
one, visible unprompted in the controller's own structured logs. **Use this as the defense
demo**; it is more convincing than any chart because it is the system explaining itself.

Controller health over the same run: 18 reconciles, `reconcile_duration_seconds_sum` 0.068 s
⇒ **~3.8 ms per reconcile** against a 15 s interval. The control loop is nowhere near being
the bottleneck, which forecloses "is your measured reaction time just your controller being
slow?"

### 7.2 k6 `spike` run — 2026-07-30

`TIME_SCALE=10` (30-minute pattern compressed to 3 minutes), from a cold single replica.

```
requests   : 84,738        (759/s average, peak 1300/s fully delivered and scraped)
median     : 2.31 ms       ← the 2ms/request calibration, in-cluster
p90        : 685.93 ms
p95        : 771.20 ms     ✗ threshold p(95)<500
p99        : 990.12 ms     ✓ threshold p(99)<1000
failed     : 0.00%         (0 of 84,738 — nothing shed, fleet grew fast enough)
checks     : 100.00% (84,738 of 84,738)
VUs        : max 315 of 340 allocated
dropped    : 37 iterations (0.33/s)
restarts   : 0
```

Scale actions: **1 → 2 → 3 → 4 → 5 → 9 → 13 → 16** (7 actions).

The p95 breach is a **result, not a test failure** — it is the latency cost of cold-start
under a 4.3× instantaneous step, which is precisely the SLA figure the evaluation reports.
`abortOnFail: false` on every threshold so runs always complete and write their summary.

The bimodal latency distribution is worth a sentence: median 2.31 ms against p90 686 ms means
most requests were served at full speed and the tail is concentrated entirely in the
under-provisioned window while pods started. That shape *is* the argument for prediction.

**Caveat on this run:** `TIME_SCALE=10` compresses the time axis, which changes the ratio
between how fast load moves and how long a pod takes to become ready — and that ratio is the
phenomenon under study (Phase 7.2 sweeps exactly this variable). **These numbers verify that
the pipeline works. They are not evaluation results and must not be reported as such.** All
Phase 6 runs must be at `TIME_SCALE=1`.

### 7.3 kind verification — 2026-07-30

3 nodes Ready from the checked-in config; both images loaded onto all three;
kube-prometheus-stack up; sample app answering HTTP 200 on `localhost:8080` through the port
mapping; `topologySpreadConstraints` spread 4 pods **2/2 across the two workers**; exactly
**one scrape target per replica** with the `app` label promoted (verified at 3 replicas);
autoscaler reading the metric and **reverting a manual `kubectl scale`** — correct behaviour,
and a neat demonstration that it owns the replica count.

This mitigates the proposal's section 6 deviation (it names *k3s or kind*; daily development
is on Docker Desktop) rather than merely recording it.

---

## 8. Test suite state — 2026-07-30

| Package | Coverage | Note |
|---|---|---|
| `internal/policy` | **99.1%** | 45 tests, 82 with subtests. The package examined orally |
| `internal/metrics` | 85.4% | |
| `internal/config` | 78.4% | |
| `internal/controller` | 74.6% | |
| `internal/observability` | 65.1% | |
| `internal/scaler` | 59.3% | Gap is `restConfig`, which needs a live cluster |
| `cmd/*` | 0% | Wiring only |

Plan floor for `internal/policy` was ≥80%. The only uncovered branch in that package is
`ceilToReplicas`'s negative-saturation case, unreachable through the public API because both
callers filter NaN and negative input first.

The policy suite is written as a **specification**, not as regression guards: each case names
the property it pins down, and the cases a judge is likely to probe — ceiling vs rounding,
the dead-band, zero ready replicas, the 90s window boundary — are explicit and findable.

---

## 9. Framing decisions the code cannot show

### 9.1 The thesis question was sharpened in week 1

Not "I built a custom autoscaler" — that is a solved problem (KEDA is a graduated CNCF
project; `prometheus-adapter` + HPA covers custom metrics; AWS has shipped predictive scaling
for years), and claiming novelty there invites a defense question the related-work section
cannot answer. The question is:

> **When does predictive autoscaling become unstable, and how must it be designed so it
> doesn't?**

The autoscaler is the *instrument*; the stability result is the contribution. The gap is real:
the predictive-scaling literature, including the papers the proposal cites, evaluates
forecasters in isolation and largely ignores closed-loop stability — which is exactly the hole
the week-1 measurement fell into.

**This framing is not in the signed proposal.** The proposal supports it only obliquely, via
the anti-flapping non-functional requirement on page 3. Raise it with Dr. Javadi in August
rather than September.

### 9.2 An honest negative result belongs in the evaluation

The proposal's motivation claims reactive scaling fails «هنگام جهش‌های ناگهانی ترافیک» —
during *sudden* traffic jumps. **Measurement contradicts this.** No forecaster can anticipate
an instantaneous step, and predictive reacts at the same moment as threshold on both `spike`
and `bursty`.

Prediction wins only where load *trends*: `diurnal`, and the `ramp` pattern added for this
project. Write the evaluation to that shape — **prediction helps on trending and periodic
workloads and provably cannot help on unforeseeable step changes** — and state the limitation
directly. It is stronger and more defensible than a claim the data does not support, and a
judge is likely to probe exactly this point.

### 9.3 Why `ramp` was added

The three original patterns do not contain the workload predictive scaling actually exists
for. `spike` and `bursty` are step functions; `diurnal` trends but also turns. `ramp` is a
single sustained trend (200 req/s rising linearly to 1200 over 20 minutes), so a forecaster
has something clean to extrapolate. **If predictive does not win on `ramp`, it does not win.**

Mirrored exactly in `sim/demo/simulate.py:pattern_ramp` and
`test/load/lib/patterns.js:ramp` — the Phase 6 validity argument depends on the simulated and
real workloads being the same curve.

### 9.4 Why the load model is open, not closed

k6 uses `ramping-arrival-rate` (offered load), not a fixed VU count. Under a closed model,
when the service slows down the generator automatically sends less — offered load would fall
exactly when the autoscaler is being tested on its response to high load. That flatters every
arm equally and hides the under-provisioning the evaluation exists to measure.

### 9.5 Why the per-replica variant is kept

`PredictivePerReplica` is not dead code and not an oversight. The before/after contrast is a
substantive evaluation section showing a real closed-loop feedback problem found and fixed,
which is stronger than presenting only the design that works. `cmd/setpoint` emits a
`slog.Warn` when it is selected, so it cannot be run as production policy by accident.

### 9.6 Metric names deliberately keep the `autoscaler_` prefix

The project was renamed to `setpoint`, but the five original prototype gauges kept their
names. Renaming a gauge invalidates every Grafana panel and every figure in the evaluation
chapter. `TestPrototypeMetricNamesArePreserved` enforces it.

### 9.7 `Scaler.Get` does not use the scale subresource

One Deployment read supplies both `.spec.replicas` and `.status.readyReplicas`. The scale
subresource cannot answer the second question — its status reports *total* replicas, not
ready ones — and the ready count is what the formula needs. Reading the Deployment alone
therefore halves the API calls per reconcile. `Set` still uses `GetScale`/`UpdateScale` for a
proper read-modify-write on `resourceVersion`.

---

## 10. Open questions, carried forward

1. **How much oscillation does the stabilizer mask, and at what cost?** (§3.3) Unmeasured.
   Phase 7.1.
2. **`PredictiveTotalLoad` has no tolerance dead-band**, because `FleetFor` takes no
   tolerance — anti-flapping on the predictive path rests entirely on the stabilizer.
   Arguably correct, since reacting sooner is the point, but asymmetric with `Threshold`.
   Settle it with the 7.1 analysis rather than by changing it untested.
3. **Every live number so far is from a `TIME_SCALE=10` or ad-hoc run.** No evaluation-grade
   result exists yet. Phase 6.
4. **`hpa-cpu` and `hpa-custom` arms are unrun** — metrics-server and prometheus-adapter are
   not installed.
5. **Only `predictive-total-load` has run on the cluster.** `threshold` and
   `predictive-per-replica` have not.
6. **Does the real cluster reproduce the simulator's *ordering*** (predictive < threshold <
   static on SLA violations, predictive costing more replica-seconds than threshold)?
   Directional agreement is a strong validity argument; a mismatch means the model or the
   deployment is wrong and must be investigated, not glossed over.
   *Now checked automatically* — `experiments/analyze.py` writes the comparison into
   `summary.md` as soon as two comparable arms have run. See §11.

---

## 11. The Phase 6 experiment harness — 2026-08-09

Built before running anything, deliberately. The alternative — run twenty experiments by
hand and post-process them afterwards — is how one discovers on the twentieth that the
third was invalid, with no way to tell which of the others share the defect.

`experiments/run.sh` performs one run; `experiments/analyze.py` derives every number from
the captures. **No metric is computed during a run and nothing is measured during
analysis.** That split is what makes a metric definition correctable: the raw series are
kept, so a definition that turns out to be wrong can be fixed and every past run rescored
without going back to the cluster.

### 11.1 A run is not `k6 run`

```
teardown → apply exactly one arm → reset fleet → warm up → measure → settle
         → capture 17 series → check validity → write run.json
```

Every step other than "measure" is a §6 finding turned into a gate. The two that would
have cost the most:

- **Verifying the live ConfigMap after switching policy.** A ConfigMap edit does not
  restart the pod, and a mounted ConfigMap updates only on the kubelet's sync period.
  Without an explicit `rollout restart` plus a read-back, the *previous* arm's policy
  keeps running under the new arm's name. Every number would be attributed to the wrong
  policy, and nothing anywhere would look wrong.
- **The warmup, at the pattern's own `t=0` rate.** `http_requests_total` does not exist
  until the first request (§6.4) and `rate(...[1m])` needs a full minute before it means
  anything, so a run starting at t=0 spends its first minute measuring the metric
  pipeline filling up. It also lets each arm enter the measured window at *its own*
  equilibrium rather than adding an identical cold-start climb to the front of every
  trace.

Validity is recorded per run in `run.json`, so the analysis excludes broken runs
automatically instead of relying on someone remembering which afternoon's were bad.

**`valid` and `smoke` are separate axes**, and conflating them was the first design error
(caught while testing): `valid: false` means the mechanics broke and nothing is
recoverable; `smoke: true` means `TIME_SCALE != 1`, where the mechanics may be perfect
and the numbers still belong nowhere near the evaluation chapter. `run.sh` now refuses
`--time-scale != 1` without an explicit `--smoke`.

### 11.2 k6 does not export a p99

`--summary-export` carries `p(90)` and `p(95)` only. It *evaluates* the `p(99)<1000`
threshold and reports pass/fail, but discards the value — so the p99 in §7.2 could not
have been reproduced from that run's artefacts.

The evaluation's latency figures therefore come from the sample app's own
`http_request_duration_seconds` histogram, as one `histogram_quantile` over a `rate()`
window equal to the whole measurement window. k6's client-side view is kept as a
cross-check; it includes network time the histogram does not, so the two should agree
closely and a large gap is worth chasing.

### 11.3 Compression confirmed harmful, with numbers

§7.2 argued from reasoning that `TIME_SCALE != 1` runs cannot be reported. A harness
smoke run at `TIME_SCALE=20` on `ramp` shows it directly:

| | Observed |
|---|---|
| Fleet at the end of the ramp | **11 ready**, never reached the required 12 |
| Measured `rate(...[1m])` at peak | **~910 req/s** against an offered 1200 |
| Requests delivered | 62,204, **0 dropped**, p95 2.45 ms |

Both distortions are pure artefacts of the compressed axis. A 1-minute `rate()` window
covers 1200 *pattern* seconds at this scale, so the load signal is smeared beyond
recognition; and pods take the same 30s to start while the workload finishes 20× sooner,
so the fleet cannot possibly track it. Neither says anything about the policy. This is
the concrete form of the §7.2 caveat, and a compact way to justify the 30-minute run
length to a judge who asks why the experiments take so long.

Worth noting separately: the load generator delivered 1200 req/s with **zero dropped
iterations at 3–4 VUs**, so k6 is not a bottleneck at these rates. Offered-vs-delivered
is now measured on every run against the pattern's own integral, and a run below 95% is
marked invalid — an arm that was never really loaded looks adequate for a reason having
nothing to do with its policy (§6.3).

### 11.4 Not yet exercised

The `hpa-cpu` and `hpa-custom` paths in `run.sh` are written but **unrun** — metrics-server
and prometheus-adapter are still not installed, so the gate that waits for the HPA to stop
reporting `<unknown>` has never fired in anger. First real use will be the first test of it.
