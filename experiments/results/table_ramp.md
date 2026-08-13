# `ramp` — cluster results

Measurement window only; warmup and settle excluded. Required replicas are
`ceil(pattern(t) / 100)` from the workload definition, not from delivered load.

| Arm | SLA violations | Replica-seconds | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 latency |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `static-peak` | 0.0% | 21,720 | 0 | 8,450 | 2,640.8 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `static` | 35.6% | 14,480 | 2,420 | 3,630 | 2,631.2 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `hpa-cpu` | 0.0% | 12,990 | 605 | 325 | 2,586.0 | 70.0 s | 8/0 | 0 | 2.4 ms |
| `hpa-custom` | 4.7% | 12,040 | 1,230 | 0 | 2,671.8 | 127.5 s | 9/0 | 0 | 2.4 ms |
| `ours-threshold` | 4.1% | 11,810 | 1,460 | 0 | 2,476.7 | 110.0 s | 8/0 | 0 | 2.5 ms |
| `ours-predictive` | 0.0% | 13,515 | 270 | 575 | 2,585.1 | 30.0 s | 10/0 | 0 | 2.4 ms |
| `ours-predictive-per-replica` | 0.0% | 12,900 | 885 | 515 | 2,475.9 | 87.5 s | 11/3 | 5 | 2.5 ms |
| `ours-predictive-nostab` | 0.0% | 13,395 | 200 | 325 | 2,482.3 | 20.0 s | 16/7 | 12 | 2.5 ms |
| `ours-predictive-per-replica-nostab` | 39.5% | 7,360 | 6,540 | 630 | 1,496.6 | 10.0 s | 60/60 | 119 | 2.5 ms |

Notes:

- `static`: never reached 9 ready replicas after the step at t=1025s
- `static`: never reached 10 ready replicas after the step at t=1145s
- `static`: never reached 11 ready replicas after the step at t=1265s
- `static`: never reached 12 ready replicas after the step at t=1385s
- `ours-threshold`: never reached 12 ready replicas after the step at t=1385s
- `ours-predictive-per-replica-nostab`: never reached 10 ready replicas after the step at t=1145s
- `ours-predictive-per-replica-nostab`: never reached 11 ready replicas after the step at t=1265s
- `ours-predictive-per-replica-nostab`: never reached 12 ready replicas after the step at t=1385s
- `ours-predictive-per-replica-nostab`: per_replica_rps and autoscaler_metric_value differ by 88% (median)
