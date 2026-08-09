# Phase 6 — measured experiments

The evaluation chapter is generated from here. `make load` drives traffic and tells
you nothing afterwards; this directory turns "drive traffic at the cluster" into a
*run*: a controlled, recorded, checkable unit of evidence.

```bash
make experiment ARM=ours-predictive PATTERN=ramp   # one run
make sweep PATTERN=ramp                            # every arm on one workload
make analyze                                       # tables + figures from all runs
```

## What a run is

```
teardown → apply exactly one arm → reset fleet → warm up → measure → settle
         → capture series → check validity → write run.json
```

Only the "measure" step is `k6 run`. Everything around it exists because of something
in `docs/lab-notebook.md` §6 that failed **silently** — producing a plausible-looking
run whose numbers meant something other than what they appeared to mean. Specifically:

| Step | The silent failure it prevents |
|---|---|
| teardown | Two controllers writing `.spec.replicas` fight; the replica trace is uninterpretable but looks normal |
| verify live ConfigMap | A ConfigMap edit does not restart the pod, so the *previous* arm's policy keeps running under the new arm's name |
| warmup | `http_requests_total` does not exist until the first request, and `rate(...[1m])` needs a full minute before it means anything (§6.4) |
| metric-pipeline gate | Three separate ways the query returns an empty vector while every component reports healthy (§6.4) |
| HPA-metric gate | An HPA reading `<unknown>` sits at `minReplicas` and looks exactly like a policy that chose not to act |
| restart check | A restarted sample-app destroys metric history and was serving degraded (§6.2) |
| delivery check | k6 under-delivering makes an arm look adequate because it was never really loaded (§6.3) |

A run that fails a validity check is not a bad result — it is **not a result**. The
reasons are recorded in `run.json` so `analyze.py` excludes it automatically, rather
than relying on someone remembering which afternoon's runs were the broken ones.

## `valid` and `smoke` are different axes

`valid: false` means the run mechanics broke; nothing can be recovered from it.

`smoke: true` means `TIME_SCALE != 1`. The mechanics may be perfect and the numbers
still belong nowhere near the evaluation chapter, because compressing the time axis
changes the ratio between how fast load moves and how long a pod takes to become
ready — and *that ratio is the phenomenon under study* (§7.2, and Phase 7.2 sweeps
exactly this variable). `run.sh` refuses `--time-scale != 1` unless `--smoke` is
given, and `analyze.py` excludes smoke runs unless `--include-smoke` is passed.

**Every Phase 6 run must be at `TIME_SCALE=1`**, i.e. 30 minutes of load per run.

## Arms

| Arm | What it is | Needs |
|---|---|---|
| `static` | fixed 8 replicas, no controller — the generously-provisioned baseline | — |
| `hpa-cpu` | stock HPA on CPU at 100% of request | metrics-server |
| `hpa-custom` | stock HPA on *our* metric via prometheus-adapter | prometheus-adapter |
| `ours-threshold` | our reactive policy | — |
| `ours-predictive` | our predictive policy, forecasting total load | — |
| `ours-predictive-per-replica` | the deliberately unstable variant (§3, §9.5) | — |

`hpa-custom` is the arm that isolates the actual claim — it differs from
`ours-predictive` only in the policy, since the signal and setpoint are identical. It
is also the first thing to cut if time runs short; the core claim survives without it.

The starting fleet (3 replicas, and 8 for `static`) matches the simulator's
`ReplicaPool(ready=3)` and its static baseline, so a cluster run and a simulator run
of the same arm can be laid side by side.

## What a run leaves behind

```
experiments/results/raw/<pattern>__<arm>__<timestamp>/
  run.json               metadata, window bounds, validity, window-wide latency
  series.json            17 Prometheus range queries over the whole capture
  k6-summary.json        k6's own aggregate view
  k6-stdout.log          the run as it appeared
  k6-warmup.log
  setpoint.log           the autoscaler's structured decisions (ours-* arms)
  configmap-applied.yaml the exact config that was live
  events.txt
```

`raw/` is gitignored; the derived tables and figures next to it are tracked.

`setpoint.log` is worth keeping per run rather than per project: §7.1 identifies it as
the best defense demo material, because it shows capacity being added while the
observed metric is still *below* target — the system explaining its own prediction.

## Two definitions that carry the weight

**Required replicas** is `ceil(pattern(t) / target)` — computed from the workload
definition, not from the measured request rate. The pattern is what the system was
asked to serve; measured throughput is partly an *outcome* of the policy under test,
so scoring a policy against its own delivered load would let an arm look adequate
precisely by failing to serve traffic.

**SLA violation** is the fraction of samples whose per-ready-replica load exceeds
`target × (1 + tolerance)` — defined exactly as the simulator defines it, so the two
sets of numbers can be *compared* rather than merely presented side by side.

`analyze.py` imports the pattern definitions from `sim/demo/simulate.py` rather than
restating them, so the analysis cannot disagree with the curve that was driven at the
cluster. That import is also what makes the simulator-vs-cluster ordering check in
`summary.md` possible — lab notebook open question 6.

## Outputs

| File | Contents |
|---|---|
| `results/metrics.csv` | one row per run, every derived number |
| `results/table_<pattern>.md` | per-workload comparison table |
| `results/comparison_<pattern>.png` | three-panel figure, same shape as the simulator's |
| `results/summary.md` | headline table, ordering check, excluded runs |

Latency quantiles come from the application's histogram over the whole measurement
window, not from k6: k6's `--summary-export` carries `p(90)` and `p(95)` only, and
evaluates a `p(99)` threshold without exporting its value.

## Requirements

`kubectl`, `k6`, `jq`, `curl`, a running stack (`make stack-up`), and for the
analysis, Python with numpy and matplotlib — the simulator's venv already has both,
and `make analyze` prefers it automatically.

Re-analysis is free and non-destructive: the raw series are kept, so a metric
definition that turns out to be wrong can be corrected and every past run rescored
without re-running anything at the cluster.
