# setpoint

Predictive Kubernetes autoscaling driven by application metrics — and the conditions
under which it goes unstable.

A BSc project (Amirkabir University of Technology, Computer Engineering) implementing a
custom Prometheus-driven autoscaler in Go, and measuring it against stock HPA.

## What this is actually about

Building a Prometheus-driven autoscaler is a solved problem — KEDA is a graduated CNCF
project and `prometheus-adapter` + HPA already covers custom metrics. So the autoscaler
here is the *instrument*, not the contribution. The question is:

> **When does predictive autoscaling become unstable, and how must it be designed so it
> doesn't?**

The short version of the answer, which the code and its tests demonstrate:

A predictive autoscaler must forecast **total load**, not **per-replica load**.
Per-replica load is `total / ready_replicas`, and `ready_replicas` is the quantity the
autoscaler *sets* — so forecasting it means extrapolating a signal your own actions move,
and the loop has positive gain:

```
scale up → per-replica load falls → forecast trends down → scale down
         → per-replica load rises → forecast trends up   → scale up
```

Total arrival rate is exogenous: the autoscaler cannot change how many requests arrive.
Forecasting it is stable. Under a constant total load of 500 req/s against a target of
100, with no stabilizer configured:

| Policy | Replica changes over 30 settled steps | Fleet |
|---|---|---|
| `PredictiveTotalLoad` | **0** | settles on 5, correct, forever |
| `PredictivePerReplica` | **29** | oscillates between 1 and 8 |

Both policies ship. The unstable one is not dead code — it is the evaluation baseline
that makes the result visible. See
`TestPerReplicaForecastingOscillatesUnderConstantLoad` in `internal/policy`.

## Layout

```
cmd/setpoint/          the autoscaler
cmd/sample-app/        the calibrated workload it scales
internal/policy/       HPA formula, forecasters, stabilizer, policies  ← the interesting part
internal/metrics/      Prometheus collector
internal/scaler/       Kubernetes scale writer
internal/controller/   reconcile loop
internal/observability/ promhttp exporter
deploy/                manifests: sample-app, setpoint, prometheus, grafana, hpa, kind
test/load/             k6 workloads: spike, diurnal, bursty, ramp
sim/                   Python closed-loop simulator (design reference + fallback figures)
docs/                  architecture and report
```

## Running it

Needs a Kubernetes cluster, Helm, and [k6](https://k6.io) (`brew install k6`).

```bash
make stack-up                      # monitoring + dashboard + sample app + autoscaler
make grafana                       # http://localhost:3000 (admin/admin)
make load PATTERN=spike            # spike | diurnal | bursty | ramp
```

The sample app is a `LoadBalancer` service on `localhost:8080`, so load runs need no
port-forward. This matters: `kubectl port-forward` resolves a *single* pod and holds it,
which would cap every run at one replica's capacity and make all comparison arms look
identical because none of them was ever really loaded.

Watch the replica count move on the dashboard before the per-replica metric crosses the
SLA line under the predictive policy, and after it under `hpa-cpu`. That contrast is the
headline result.

Switch policies by editing `policy.name` in `deploy/setpoint/configmap.yaml`
(`threshold` | `predictive` | `predictive-per-replica`) and restarting the Deployment.

`make stack-down` removes everything.

### Cluster options

Daily development uses **Docker Desktop Kubernetes**. The proposal names *k3s or kind*,
so a working kind config is checked in and verified, which keeps the repository
reproducible for a reviewer who does not have Docker Desktop:

```bash
kind create cluster --config deploy/kind/cluster.yaml
kind load docker-image sample-app:dev setpoint:dev --name setpoint
```

`kind load` is not optional — kind nodes cannot see the host's Docker images, and both
manifests use `imagePullPolicy: IfNotPresent` against local tags.

## Development

```bash
go build ./... && go vet ./... && go test ./... -cover
make cover                        # coverage by function
make dry-run                      # decide and log against a real cluster, change nothing
```

`internal/policy` is at 99.1% coverage. Its tests are written as a specification rather
than as regression guards — each case names the property it pins down.

## Calibration, and why it matters

The sample app burns a known 2ms of CPU per request. At the target of 100 req/s that is
exactly 200m, which is exactly its CPU request. So "100% of CPU request" and "at the
per-replica target" are the *same operating point*, which is what lets HPA-on-CPU and our
threshold policy be compared at all — otherwise the two arms aim at different setpoints
and any difference between them is unattributable.

Verify it with `make calibrate`.

## Metric names

The exported gauges keep their original `autoscaler_` prefix despite the project being
named `setpoint`. Renaming a gauge invalidates every Grafana panel and every figure in
the evaluation chapter; a test enforces this.

## License

MIT — see [LICENSE](LICENSE).
