# `spike` — cluster results

Measurement window only; warmup and settle excluded. Required replicas are
`ceil(pattern(t) / 100)` from the workload definition, not from delivered load.

| Arm | SLA violations | Replica-seconds | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 latency |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `static-peak` | 0.0% | 23,530 | 0 | 12,100 | 2,464.3 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `static` | 32.9% | 14,480 | 3,000 | 6,050 | 2,449.5 | — s | 0/0 | 0 | 2.4 ms |
| `hpa-cpu` | 1.7% | 13,915 | 480 | 2,965 | 2,409.9 | 60.0 s | 3/4 | 1 | 2.4 ms |
| `hpa-custom` | 2.2% | 12,255 | 475 | 1,300 | 2,506.2 | 70.0 s | 4/5 | 1 | 2.4 ms |
| `ours-threshold` | 2.5% | 11,925 | 1,095 | 1,590 | 2,404.5 | — s | 3/4 | 1 | 2.4 ms |
| `ours-predictive` | 0.6% | 14,280 | 270 | 3,120 | 2,440.2 | 35.0 s | 7/9 | 7 | 2.4 ms |
| `ours-predictive-per-replica` | 0.6% | 13,900 | 390 | 2,860 | 2,419.9 | 60.0 s | 9/9 | 14 | 2.4 ms |
| `ours-predictive-nostab` | 4.1% | 12,355 | 465 | 1,390 | 2,365.3 | 60.0 s | 28/28 | 43 | 2.4 ms |
| `ours-predictive-per-replica-nostab` | 50.8% | 1,810 | 9,620 | 0 | 1,356.9 | — s | 60/60 | 119 | 2.5 ms |

Notes:

- `static`: never reached 13 ready replicas after the step at t=600s
- `ours-threshold`: never reached 13 ready replicas after the step at t=600s
- `ours-predictive-per-replica-nostab`: never reached 13 ready replicas after the step at t=600s
- `ours-predictive-per-replica-nostab`: per_replica_rps and autoscaler_metric_value differ by 135% (median)
