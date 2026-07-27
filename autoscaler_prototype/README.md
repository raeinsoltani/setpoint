# Prometheus-Driven Custom Autoscaler — Prototype

A working prototype of a custom horizontal autoscaler that scales a service on
**application-level metrics from Prometheus** instead of raw CPU, and can scale
**predictively** (before a spike) rather than only reactively. It is the Python
proof-of-concept for the bachelor's project *"Design and Implementation of a
Custom Prometheus-Driven Autoscaler for Cloud-Native Microservices."*

> The thesis targets Go for the production implementation; this Python prototype
> validates the control logic and generates the evaluation experiments quickly.

## Architecture

```
Prometheus ──(PromQL)──> Metric Collector ─┐
                                           ▼
                                     Policy Engine  (threshold / predictive
                                           │         + anti-flapping)
                                           ▼
                                        Scaler ────> Kubernetes / k3s API
                                           │
                                           └───────> /metrics ──> Grafana
```

Each stage is a small, replaceable component (see `autoscaler/`):

| File | Responsibility |
|------|----------------|
| `policy.py` | HPA replica formula, EWMA+trend forecaster, stabilization window |
| `metrics.py` | `PrometheusCollector` (PromQL) and `StaticCollector` |
| `scaler.py` | `KubernetesScaler` (scale subresource) and `InMemoryScaler` |
| `exporter.py` | Exposes the autoscaler's own state as Prometheus metrics |
| `controller.py` | The reconcile loop tying it all together |
| `config.py` | Builds a `Controller` from `config.yaml` |

## Quick start

```bash
pip install -r requirements.txt

# 1) Run the unit tests (policy engine)
python -m pytest tests -q

# 2) Run the evaluation simulation (no cluster needed) -> results/*.png
python demo/simulate.py

# 3) Smoke-run the control loop in memory (no Prometheus, no cluster)
python -m autoscaler --smoke

# 4) Run against a real Prometheus + Kubernetes target
python -m autoscaler --config config.yaml
```

## The simulation (evaluation)

`demo/simulate.py` runs a **closed loop**: observed per-replica load is
`total_load / ready_replicas`, and newly requested replicas only become ready
after a start-up delay — which is exactly why a reactive policy drops requests
during a spike and a predictive one can avoid it.

It compares three strategies (static baseline, threshold, predictive) on three
workloads (spike, diurnal, bursty) and reports:

* **SLA violation %** — time spent overloaded (per-replica load above the SLA
  limit); a proxy for latency breaches.
* **Replica-seconds** — integral of running replicas; a proxy for cost.

Each run writes a three-panel chart (`results/comparison_<workload>.png`)
showing load, replica counts, and per-replica load vs. the target/SLA lines.
These plots and the summary table drop straight into the thesis evaluation
chapter.

## How the policy stays faithful to Kubernetes HPA

* **Formula:** `desired = ceil(current * metric / target)` with a 10% tolerance
  dead-band — identical to HPA.
* **Anti-flapping:** scale-up is immediate; scale-down uses the maximum
  recommendation over a stabilization window — identical to HPA's downscale
  stabilization.
* **Prediction:** the predictive policy applies the same formula to a forecast
  of the metric (EWMA + linear trend), which is the project's contribution over
  stock HPA. The forecaster is a drop-in interface, so ARIMA/LSTM can replace it
  later.
