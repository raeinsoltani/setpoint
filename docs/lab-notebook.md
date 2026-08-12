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

**Update, 2026-08-09:** metrics-server is installed, so `hpa-cpu` is in the sweep below and
its gate gets its first real test. `hpa-custom` remains unrun — prometheus-adapter is still
not installed.

### 11.5 The first `ramp` sweep died of an unattended-run problem, not a cluster one

The first attempt at `make sweep PATTERN=ramp` at `TIME_SCALE=1` produced **nothing**. The
`static` arm's directory held `k6-stdout.log` (1.3 MB, so the load really was driven for the
full 30 minutes) and no `run.json`, `series.json`, or `k6-summary.json` — it was killed
between k6 finishing and the capture phase. The directory was deleted rather than kept:
without series it cannot be rescored, so it is not evidence of anything.

The cause was **Docker Desktop having gone away**, not the harness. `kubectl` came back with
`connection refused` on the API server and `docker version` could not reach the daemon.
On restart, metrics-server crashlooped twice with

```
panic: unable to load configmap based request-header-client-ca-file: ...
       dial tcp 10.96.0.1:443: connect: connection refused
```

which is a **boot race, not a fault** — metrics-server starting before the apiserver is
serving. It went Ready on its own within 10 seconds of the third start. Worth recognising on
sight: it looks alarming and needs no action.

Two changes so a multi-hour sweep survives:

1. **The Prometheus port-forward is now supervised.** `kubectl port-forward` was spawned once
   at preflight and never checked again. A run queries Prometheus during warmup and then not
   again until capture ~30 minutes later, so a forward dropping anywhere inside that gap
   destroys the whole run — and the failure only surfaces at the very end, after the load has
   been driven. It now respawns from a supervisor loop until a sentinel file is removed on
   exit, and capture waits up to 60s for the tunnel instead of assuming it survived.
   Prometheus keeps scraping regardless, so the data is never actually lost — only the
   tunnel to it. Verified by killing the child mid-flight: the forward came back in under 8s
   and cleanup left no orphans.
2. **The sweep runs under `caffeinate -i`**, detached with `nohup`. An unattended 3-hour run
   has to outlive the display going to sleep.

The general lesson, which belongs in the reproducibility section: at `TIME_SCALE=1` a single
arm is ~34 minutes and a sweep is ~3 hours, which is long enough that *the laptop* becomes
part of the experimental apparatus. Anything that can interrupt it — sleep, a Docker Desktop
restart, a dropped tunnel — is a failure mode of the measurement, and each one costs half an
hour of wall clock that cannot be compressed away (§11.3).

**Correction, 2026-08-10:** point 2 above was wrong, and the second sweep proved it. See §11.7.

### 11.6 First real numbers, and the static baseline is not what it claimed to be

The 2026-08-09 sweep produced two valid `ramp` runs at `TIME_SCALE=1` before the host
started sleeping — the first evaluation-grade measurements in the project.

| Arm | SLA violations | Replica-s | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `static` (8) | 35.5% | 14,440 | 2,400 | 3,630 | 2,624.9 | — | 0/0 | 0 | 2.4 ms |
| `ours-threshold` | 5.2% | 12,050 | 1,220 | 0 | 2,479.0 | 125.0 s | 9/0 | 0 | 2.5 ms |

The reactive policy beat the static baseline on **SLA and cost simultaneously** — 5.2% vs
35.5% violations while using *fewer* replica-seconds. That is not a plausible tradeoff
curve, and the reason is that the baseline was mis-specified, not that the policy is
miraculous.

`static` was pinned at 8 replicas, inherited from `sim/demo/simulate.py` where the comment
read "a fixed, generously-provisioned baseline". It is nothing of the kind. Every pattern
peaks well above it:

| Pattern | Peak req/s | Replicas required at peak | At mean |
|---|---:|---:|---:|
| `spike` | 1300 | 13 | 6.3 |
| `diurnal` | 1200 | 12 | 7.5 |
| `bursty` | 1200 | 12 | 5.4 |
| `ramp` | 1200 | 12 | 7.0 |

So 8 is approximately the **mean** requirement, and the arm is under-provisioned at every
peak — hence its 2,400 under-provisioning replica-seconds and the analyzer's four "never
reached N ready replicas" flags. A baseline that loses on both axes is a strawman, and an
examiner is entitled to say so.

Fixed by carrying **two** static arms, which are the two honest ends of the tradeoff:

- `static` — fixed 8, provisioned for the mean. Kept deliberately: capacity-planning to the
  average is a real strategy, and showing it fail under variable load is a result.
- `static-peak` — `ceil(peak(pattern)/target)`, provisioned for the peak. The
  safe-and-expensive reference the evaluation needs. Computed from the pattern definition
  at run time rather than hardcoded, so it cannot drift away from the workload the way the
  8 did.

Worth stating plainly in the evaluation chapter, because it is the more interesting claim:
the useful comparison is not "autoscaling beats static" — that is rigged by choosing a bad
static. It is *where on the cost/SLA curve each policy sits*, with both ends of static
present as the reference.

### 11.7 The host slept, and `caffeinate` does not prevent that

The relaunched sweep lost 3 of 5 arms. `ours-predictive` recorded a measurement window of
**27,577 s against an expected 1,800 s — a 15× stretch**. `hpa-cpu` ran 12 hours before
being killed by hand; `ours-predictive-per-replica` never started.

The host slept. `pmset -g log` showed ~100 sleep/wake cycles, and the two `caffeinate`
assertions had been *held continuously* for 19 h 41 m against 23 h 11 m of wall clock — the
3.5-hour difference is precisely the time the system spent asleep, since assertion timers do
not advance during sleep.

**`caffeinate -i` asserts `PreventUserIdleSystemSleep`, which stops only *idle* sleep.** It
does not stop a lid-close, and neither does `-d`. Adding `-d` was tried and changed nothing.
The only reliable options on this machine are keeping the lid open for the duration, or
`sudo pmset -a disablesleep 1` (system-wide, and must be reverted). This is now a stated
precondition for a sweep, not a detail.

Two harness defects fell out of the wreckage, both fixed:

1. **Restart counting could go negative.** The gate compared `sum(...restarts_total)` at two
   instants, but that sums over the *current* pod set: any arm that scales down deletes pods,
   their counts leave the sum, and the delta goes negative. `ours-predictive` was failed with
   `sample-app restarted -1 time(s)`. This was not cosmetic — it would have produced false
   invalids on every autoscaling arm indefinitely, and it does so *more often the better the
   policy scales down*, which is the worst possible bias. Now counted as
   `sum(increase(...[run]))` per series, which pod churn cannot make negative.
2. **A run destroyed by the host had no way to say so.** The bad run *was* caught, but by the
   dropped-iterations gate, whose message reads "offered load was below the pattern, which
   flatters the arm" — it blames k6. Lost wall clock is now its own named invalid reason,
   triggered when the measured window exceeds the expected one by more than 5%.

The second is the more general lesson and belongs next to §6's silent-failure table: a gate
that fires for the wrong reason is only marginally better than no gate, because it sends the
next person to debug the wrong component. Every check should fail with the name of the thing
that actually broke.

### 11.8 The harness failed its own new arm, and the reason generalises

Before committing 3h40m of cluster time to the fixed sweep, `static-peak` was smoke-tested
alone at `TIME_SCALE=20`. It came back **INVALID**:

```
spec.replicas changed 1 times under the static-peak arm: a controller is still attached
```

No controller was attached. The change was **this script's own `kubectl scale`**. The
capture window reaches back `WARMUP_SECONDS` before measurement starts, which is far enough
to include the moment the arm was applied; the series opens `1 → 12` because the previous
(killed) run had left the fleet at 1. The gate meant to detect a foreign controller detected
the harness setting up.

Plain `static` never tripped this only by luck — it had always run first, against a fleet
already sitting at 8, so its scale was a no-op. In a sweep where `static-peak` follows
`static`, it would have fired on **every** sweep.

Fixed by asking the question over the *measured* window rather than the whole capture, which
is the window validity is actually a claim about. Replayed against all three stored runs, the
verdicts are unchanged where they should be: `static` 0 → 0 changes, `ours-threshold` 12 → 9
(the three dropped were in warmup and settle), `static-peak` 1 → 0. A second check was added
at the same time — a static arm must hold spec.replicas *at the value it pinned*, since
constant-at-the-wrong-value means the fleet under test was not the fleet the arm claims.

The generalisable point, and the reason the smoke test was worth the six minutes: **the
harness is an instrument, and an instrument that has never been read against a known input
has not been calibrated.** Three of the defects in §11.7–11.8 were in the checking code, not
the system under test, and all three biased toward *rejecting good runs* — the failure mode
that is invisible until it has quietly eaten a day of cluster time. Test a new arm at
`TIME_SCALE=20` before spending 34 minutes on it.

### 11.9 The first complete arm set: six-for-six on `ramp` — 2026-08-11

Relaunched on AC power with the lid open. **All six arms valid**, every measured window
1800–1802 s against an expected 1800 — no lost wall clock, so §11.7's gate has now been read
against a clean input as well as a broken one.

| Arm | SLA violations | Replica-s | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `static-peak` (12) | 0.0% | 21,720 | 0 | 8,450 | 2,640.8 | — | 0/0 | 0 | 2.4 ms |
| `static` (8) | 35.6% | 14,480 | 2,420 | 3,630 | 2,631.2 | — | 0/0 | 0 | 2.4 ms |
| `hpa-cpu` | 0.0% | 12,990 | 605 | 325 | 2,586.0 | 70.0 s | 8/0 | 0 | 2.4 ms |
| `ours-threshold` | 4.1% | 11,810 | 1,460 | 0 | 2,476.7 | 110.0 s | 8/0 | 0 | 2.5 ms |
| `ours-predictive` | 0.0% | 13,515 | 270 | 575 | 2,585.1 | 30.0 s | 10/0 | 0 | 2.4 ms |
| `ours-predictive-per-replica` | 0.0% | 12,900 | 885 | 515 | 2,475.9 | 87.5 s | 11/3 | **5** | 2.5 ms |

**The SLA-violation column does not discriminate on this workload.** Four of six arms sit at
0.0%. `ramp` rises by 1000 req/s over 20 minutes — about 0.83 req/s per second, or roughly
one additional replica every two minutes — which is slow enough that a 15-second reconcile
loop and a 30-second pod start-up can track it reactively. The headline metric saturates,
and reporting only that column would say the arms are indistinguishable when they are not.
The columns that separate them here are **under-provisioning replica-seconds** and **median
reaction time**. Note this in the evaluation chapter rather than quietly picking the
flattering column.

What the numbers actually support:

1. **`static-peak` is the cost argument, and it is a large one.** Same 0.0% SLA as
   `ours-predictive`, for 21,720 replica-seconds against 13,515 — **61% more capacity for
   identical SLA outcomes**. This is the comparison the evaluation chapter was missing while
   `static` was the only baseline, and it is the one that does not look rigged.
2. **`ours-predictive` buys responsiveness, not SLA.** Against `hpa-cpu` it cuts median
   reaction from 70 s to **30 s** and under-provisioning from 605 to **270 replica-seconds**
   — less than half — while costing 4% more replica-seconds (13,515 vs 12,990). On `ramp`
   that is the honest claim. It is *not* "predictive beats HPA on SLA": both are at zero.
3. **`hpa-cpu` performs well and should be reported saying so.** §9.2 already commits to
   publishing negative results; a slow monotonic ramp is a workload stock HPA handles, and
   the thesis is stronger for saying it than for burying it.
4. **`ours-threshold` is the cheapest arm and the only `ours-*` one that violates.** 11,810
   replica-seconds, 4.1% violations, and the analyzer flags it never reaching 12 ready
   replicas after the final step — it lags the top of the ramp and never catches up before
   the window closes. Cheap because it under-provisions, which is the expected reactive
   tradeoff and lands it in the right place on the curve.

**The per-replica variant's pathology is real but invisible in the headline columns.** It
scored 0.0% SLA and 12,900 replica-seconds — on those two numbers alone it looks *better*
than the correct predictive policy. The instability shows up in exactly one place: **5
direction reversals and 3 scale-downs, against 0 and 0 for every other arm.** A monotonically
rising workload gives positive feedback little room to run: the forecast may be wrong, but
the correction is almost always in the same direction, so the oscillation is damped by the
workload rather than by the policy. §9.5 keeps this arm to demonstrate the failure mode, and
`ramp` is the workload least able to demonstrate it. **`diurnal` and `bursty` are where the
claim has to be made** — `diurnal` turns, and `bursty` has troughs after each burst, and both
give the loop room to overshoot in both directions. If the reversal count does not blow up
there, §3's central argument needs restating in terms of reversals rather than SLA cost.

**Informal repeatability.** Two arms now have two independent `TIME_SCALE=1` runs on `ramp`
(§11.6 and here): `static` 35.5% → 35.6% violations and 14,440 → 14,480 replica-seconds;
`ours-threshold` 5.2% → 4.1% and 12,050 → 11,810. The no-controller arm reproduces to
~0.1%, the autoscaled one varies by ~1 percentage point. That is the difference between
measuring an open loop and a closed one, and it sets the resolution: **differences below
about 1.5 points of SLA violation between autoscaled arms are not distinguishable at n=1.**
Every gap being leaned on above is far wider than that, but any future claim resting on a
sub-1.5-point difference needs repeats before it can be made.

**Open question 6 is answered for `ramp`.** Simulator and cluster agree on the ordering of
all three simulator-comparable arms, on *both* headline metrics:

| Metric | Simulator order | Cluster order |
|---|---|---|
| SLA violations | predictive < threshold < static | predictive < threshold < static |
| Replica-seconds | threshold < predictive < static | threshold < predictive < static |

The simulator's absolute magnitudes were never expected to match — it models a 30 s start-up
and no network — but ordering agreement is what licenses using it to reason about
configurations too expensive to run at the cluster. One pattern is not enough to close the
question; it stays open until the remaining three are in.

### 11.10 `hpa-custom` runs at last, and a clamshell costs the diurnal forecaster — 2026-08-12

The unattended chain launched on the evening of the 11th survived something it was designed
to survive: **the agent session driving it was reaped at 23:47** (`ptyhost_orphan_watchdog`,
the terminal host going away), and the work continued for another seven hours without it.
The chain was deliberately started detached under its own `caffeinate -dis` rather than as a
child of the session. That detail is the only reason there is anything to write here. Record
it as harness practice: **an unattended run must not be a child of the thing watching it.**

`pmset -g log` is clean from the 17:26 wake straight through to 06:39 — thirteen hours with
no sleep entry at all. §11.7's mitigation holds when the lid stays open.

**`hpa-custom` has now executed.** prometheus-adapter v0.12.0 installed (pre-pulled into the
docker store first, per §11.4's registry-mirror problem), the custom metrics API served
`http_requests_per_second`, the `TIME_SCALE=10` smoke passed, and the `TIME_SCALE=1` backfill
on `ramp` is valid. **`ramp` is seven-for-seven.**

| Arm | SLA | Replica-s | Under-prov. | Reaction | Reversals |
|---|---:|---:|---:|---:|---:|
| `hpa-custom` | 4.7% | 12,025 | 1,245 | 127.5 s | 0 |
| `ours-threshold` | 4.1% | 11,810 | 1,460 | 110.0 s | 0 |
| `ours-predictive` | 0.0% | 13,515 | 270 | 30.0 s | 0 |

Two things fall out of this, and the second was not anticipated.

1. **The ablation is now clean.** `hpa-custom` and `ours-predictive` read the *same signal*
   at the *same setpoint* and differ only in the policy. 4.7% → 0.0% violations, 127.5 s →
   30.0 s median reaction, 1,245 → 270 under-provisioned replica-seconds, for 12% more
   replica-seconds. Every alternative explanation involving the choice of metric is now
   excluded by construction. This is the single most defensible comparison in the project
   and it is the one to build §3's argument on.
2. **`hpa-custom` and `ours-threshold` are near-replicates of each other.** 4.7% vs 4.1%,
   127.5 s vs 110.0 s, 12,025 vs 11,810 replica-seconds — two independently implemented
   reactive controllers on one signal landing within 0.6 points, inside the ~1.5-point
   resolution §11.9 established. That is worth stating explicitly in the evaluation chapter:
   **our reactive baseline behaves like the stock one**, so the predictive gain in (1) is not
   an artifact of having written a weak comparator.

**A clamshell sleep destroyed `diurnal / ours-predictive`.** The lid was closed at 06:41:56,
about 40 s into the measurement window, and the host slept 164 s (`pmset`: wake at 06:44:48).
k6's scenario clock paused while wall clock did not — the offset was measured live at a fixed
2m44s and would have closed the window at ~1964 s against §11.7's 1890 s limit.

The run was killed rather than allowed to finish. **It could not have been re-scored, and the
reason generalises beyond a shifted time base.** The whole VM froze, not just the load
generator: for 164 s Prometheus scraped nothing and the autoscaler reconciled nothing, so on
wake the *forecaster's history buffer had a hole in it* and its horizon-3 extrapolation
continued from stale state against a pattern that had moved on without it. Everything after
the wake is the policy recovering from an input outage, not the policy under test. A constant
time offset is subtractable; a discontinuity injected into the controller's own input is not.
**Sleep during a run is unrecoverable for predictive arms specifically, in a way it is not
for `static`.**

**A harness defect surfaced by the kill, and it would have propagated silently.** Killing a
`run.sh` orphans the `kubectl port-forward` it supervises. `run.sh` then takes its
*"prometheus already reachable on :9090 (reusing)"* branch on every subsequent arm and sets
`PF_PID=""` — so seventeen remaining runs would each have depended on one unsupervised
forward inherited from a killed process, reintroducing exactly the single point of failure
commit `77dec41` was written to remove. The reuse branch is correct by design (it supports an
externally-managed forward) but it cannot tell a *supervised* external forward from an
orphan. Mitigated for this chain by replacing the orphan with a standalone supervised loop;
the 2-second handover was verified against the live `hpa-cpu` run, which passed its
metric-pipeline gate 33 s later reading 299.99 req/s across 6 targets. **If this recurs,
consider having `run.sh` adopt-or-replace rather than blindly reuse.**

**`diurnal`, three arms in, is already doing what `ramp` could not.**

| Arm | SLA | Replica-s | Under-prov. | Over-prov. | Scale ↑/↓ | Reversals |
|---|---:|---:|---:|---:|---:|---:|
| `static-peak` (12) | 0.0% | 21,720 | 0 | 7,290 | 0/0 | 0 |
| `static` (8) | 40.3% | 14,480 | 2,490 | 2,540 | 0/0 | 0 |
| `ours-threshold` | 3.6% | 14,545 | 1,025 | 1,140 | 8/8 | 1 |

`ours-threshold` scales **down eight times and reverses once** here, against 0 and 0 on
`ramp`. The oscillation axis is live on this pattern, which is the precondition §11.9 said
had to hold before `ours-predictive-per-replica` could be falsified at all. Also note
`ours-threshold` costs 14,545 replica-seconds against `static`'s 14,480 — **statistically the
same spend for 3.6% violations instead of 40.3%**, which is a cleaner statement of the value
of scaling at all than anything `ramp` produced.

### 11.11 The per-replica pathology is real, and `diurnal` does not show it — 2026-08-12

`diurnal` and `spike` are both seven-for-seven. Twenty-one of twenty-eight arms are measured,
all valid, no failed runs. `bursty` is the last pattern outstanding.

**§11.9's prediction about where the per-replica variant would break was wrong, and the
correction matters more than the prediction did.** It reasoned that `ramp` hid the pathology
because a monotonic workload gives positive feedback nowhere to run, and that `diurnal` would
expose it because `diurnal` turns. Reversal counts for `ours-predictive-per-replica`:

| Pattern | per-replica | `ours-predictive` | best other arm |
|---|---:|---:|---:|
| `ramp` | 5 | 0 | 0 |
| `diurnal` | **2** | 2 | 0 |
| `spike` | **14** | 3 | 0 |

On `diurnal` it does not discriminate at all — 2 reversals, the same as the correct predictive
policy and the same as `hpa-cpu`. On `spike` it is unmistakable: **14 reversals against 3, and
9/9 scale up/down against 5/7.**

So the driver is not whether the workload *turns*. It is whether it presents a **step large
relative to the control interval**. `diurnal` rises and falls smoothly enough that the loop
settles between 15-second reconciles, so the feedback term never compounds; `spike`'s 4x step
moves the observed per-replica metric so far in one interval that the forecaster extrapolates
its own correction and overshoots, then corrects back. This is a better statement of §3's
mechanism than the one in the proposal: **the instability is excited by input steps, not by
non-monotonicity.** State it that way in the evaluation chapter, and note that a policy
looking stable on a smooth workload is not evidence that it is stable.

**The ablation's strongest form is on `diurnal`.** `ours-predictive` against `hpa-custom` —
identical signal, identical setpoint, policy the only difference:

| | SLA | Replica-s | Reaction |
|---|---:|---:|---:|
| `hpa-custom` | 3.0% | 17,115 | 97.5 s |
| `ours-predictive` | **0.0%** | **15,180** | **27.5 s** |

Better on all three axes simultaneously, which neither `ramp` (where it cost 12% more
capacity) nor `spike` (equal reaction) produced. `ours-predictive` also beats `hpa-cpu` here
on both cost and reaction (15,180 vs 15,570 replica-seconds, 27.5 s vs 55.0 s) at equal 0.0%
SLA — the first pattern where the predictive policy dominates stock HPA outright rather than
trading cost for responsiveness.

**`spike` also produces the first arm that cannot report a reaction time.** `static` and
`ours-threshold` both show `—` for median reaction, and the analyzer flags `ours-threshold` as
never reaching 13 ready replicas after the step at t=600s. It never caught the spike inside
the window at all, so there is no reaction to measure. That is not a missing number; it is the
result, and the table should say so rather than leaving a dash to be misread as "instant".

**`hpa-custom` is consistently the most expensive autoscaled arm** — 17,115 replica-seconds on
`diurnal` and 17,105 on `spike`, against 14,085–15,180 for `ours-predictive` — and it never
scales down on either pattern (8/0 and 4/0). Worth checking whether the adapter's rate window
is simply too long to see the decrease, since a controller that only ratchets upward is a
finding about the configuration and not about custom metrics as an approach.

### 11.12 Phase 6 measurement complete — 2026-08-12

**Twenty-eight of twenty-eight arms measured, every one valid.** Four workloads x seven arms,
all at `TIME_SCALE=1`, every measurement window 1800-1802 s against an expected 1800. The two
invalid runs in `summary.md` are both historical (the 2026-08-09 sleep-destroyed run and the
2026-08-10 smoke that caught the pre-`e443c60` gate bug); nothing from today failed.

**The per-replica pathology across all four workloads.** Direction reversals:

| Pattern | per-replica | `ours-predictive` | `ours-threshold` | `hpa-cpu` | `hpa-custom` |
|---|---:|---:|---:|---:|---:|
| `ramp` | **5** | 0 | 0 | 0 | 0 |
| `diurnal` | 2 | 2 | 1 | 2 | 0 |
| `spike` | **14** | 3 | 1 | 1 | 0 |
| `bursty` | **10** | 6 | 5 | 5 | 0 |

§11.11 called the driver "a step large relative to the control interval". With `bursty` in,
a sharper statement is available and it is the one to defend: **`diurnal` is the only workload
in the set that is smooth, and it is the only one where the pathology does not appear.**
`pattern_diurnal` is `300 + 900 sin^2(pi t / 1800)` — infinitely differentiable. `spike` and
`bursty` are step functions; `ramp` is continuous but has slope discontinuities at t=300 and
t=1500, and shows 5 reversals against 0 for every other arm. The excitation is a discontinuity
in the input *or its derivative*, not non-monotonicity — `diurnal` turns and does not oscillate,
`ramp` never turns and does. Ranked by reversal ratio the ordering is `spike` (4.7x) > `bursty`
(1.7x) > `ramp` (5 vs 0) > `diurnal` (1.0x), which tracks discontinuity severity.

This is a stronger result than the proposal claimed, and it comes with a warning that belongs
in the evaluation chapter: **a per-replica-forecasting policy measured only on a smooth
workload will look stable.** On `diurnal` the broken variant scores 0.0% SLA and is *cheaper*
than the correct one (14,915 vs 15,180 replica-seconds). Anyone validating on a single smooth
curve would ship it.

**`hpa-custom` never scales down, on any workload.** Scale up/down: 9/0 on `ramp`, 8/0 on
`diurnal`, 4/0 on `spike`, 4/0 on `bursty` — against 6/9, 3/4 and 7/6 for `hpa-cpu` on the
three non-monotonic patterns. It is also the most expensive autoscaled arm on all three
(17,115 / 17,105 / 18,980 replica-seconds).

Two explanations are **ruled out**, both checked directly rather than assumed:

- *Not the HPA behaviour config.* `hpa-cpu.yaml` and `hpa-custom.yaml` carry identical
  `behavior` blocks — `scaleUp` stabilization 0, `scaleDown` stabilization 90 s, 100%/15 s
  policies on both. `hpa-cpu` scales down freely under the same settings.
- *Not the adapter's rate window.* `metricsQuery` uses `[1m]`, deliberately matched to the
  autoscaler's own PromQL. An earlier guess in this notebook that the window was "too long to
  see a decrease" is wrong and should not be followed.

The remaining candidate is the HPA's **missing-metrics rule**: when a Pods metric is absent for
some pods, the scale-*down* computation conservatively assumes those pods are at 100% of
target, which suppresses scale-down entirely. `http_requests_per_second` only exists for a pod
once it has served a request inside the `[1m]` window, so any recently-started or idle pod is
missing from the adapter's response. That is a hypothesis, not a finding — it needs a direct
check against `/apis/custom.metrics.k8s.io/.../pods/*/http_requests_per_second` during a
scale-down, compared against the pod list.

**This matters for the headline claim and must not be left implicit.** `hpa-custom` is the arm
that isolates policy from signal, and if it cannot scale down for a configuration reason, then
part of `ours-predictive`'s cost advantage over it is unearned. The SLA and reaction-time
advantages are unaffected — those come from scaling *up* — but the replica-second comparison
against `hpa-custom` should be reported with this caveat until the check is done.

**Open question 6 closes with a qualified answer: 5 of 8 orderings agree.** All three
disagreements are on the step-function workloads, and all three run the same direction:

| Workload | Metric | Simulator | Cluster |
|---|---|---|---|
| `bursty` | SLA | threshold < predictive | **predictive < threshold** |
| `spike` | SLA | threshold < predictive | **predictive < threshold** |
| `spike` | Replica-s | threshold < static < predictive | threshold < predictive < static |

**The simulator understates the predictive policy on step workloads** — it ranks the reactive
policy ahead on SLA where the cluster ranks predictive ahead. The likely cause is that the
simulator scales down instantly (`pool.ready = target`, no drain) and has no measurement lag:
`observed` is `total_load / ready` computed exactly, whereas the cluster pays a scrape interval
plus a 1-minute `rate()` window before a change is visible. Measurement lag penalises a
reactive policy and is precisely what a forecast compensates for, so removing it flatters the
reactive arm. Both smooth-workload patterns agree on both metrics.

The practical consequence for Phase 7: **the simulator can be used to reason about
configurations on smooth workloads, but its ordering on step workloads is not trustworthy and
any sweep result there needs a cluster spot-check.** That is a narrower licence than §11.9
anticipated, and it is better to state it than to have it found.
