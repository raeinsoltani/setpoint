# `spike` — cluster results

Measurement window only; warmup and settle excluded. Required replicas are
`ceil(pattern(t) / 100)` from the workload definition, not from delivered load.

| Arm | SLA violations | Replica-seconds | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 latency |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `static-peak` | 0.0% | 23,530 | 0 | 12,100 | 2,464.3 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `static` | 32.9% | 14,480 | 3,000 | 6,050 | 2,449.5 | — s | 0/0 | 0 | 2.4 ms |
| `hpa-cpu` | 1.7% | 13,915 | 480 | 2,965 | 2,409.9 | 60.0 s | 3/4 | 1 | 2.4 ms |
| `hpa-custom` | 2.8% | 17,105 | 425 | 6,100 | 2,399.7 | 65.0 s | 4/0 | 0 | 2.4 ms |
| `ours-threshold` | 2.5% | 11,925 | 1,095 | 1,590 | 2,404.5 | — s | 3/4 | 1 | 2.4 ms |
| `ours-predictive` | 0.3% | 14,085 | 405 | 3,060 | 2,438.6 | 65.0 s | 5/7 | 3 | 2.4 ms |
| `ours-predictive-per-replica` | 0.8% | 13,900 | 390 | 2,860 | 2,407.6 | 60.0 s | 9/9 | 14 | 2.4 ms |

Notes:

- `static`: never reached 13 ready replicas after the step at t=600s
- `ours-threshold`: never reached 13 ready replicas after the step at t=600s
