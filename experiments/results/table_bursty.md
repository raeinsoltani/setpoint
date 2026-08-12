# `bursty` — cluster results

Measurement window only; warmup and settle excluded. Required replicas are
`ceil(pattern(t) / 100)` from the workload definition, not from delivered load.

| Arm | SLA violations | Replica-seconds | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 latency |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `static-peak` | 0.0% | 21,720 | 0 | 11,960 | 2,094.8 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `static` | 17.4% | 14,480 | 1,440 | 6,160 | 2,091.7 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `hpa-cpu` | 2.5% | 13,880 | 1,200 | 5,320 | 2,085.5 | 30.0 s | 7/6 | 5 | 2.4 ms |
| `hpa-custom` | 1.7% | 18,980 | 340 | 9,560 | 2,079.7 | 0.0 s | 4/0 | 0 | 2.4 ms |
| `ours-threshold` | 3.3% | 12,365 | 1,410 | 4,015 | 2,088.1 | 0.0 s | 9/9 | 5 | 2.4 ms |
| `ours-predictive` | 0.8% | 14,545 | 1,080 | 5,865 | 2,080.9 | 25.0 s | 12/16 | 6 | 2.4 ms |
| `ours-predictive-per-replica` | 2.5% | 13,715 | 1,005 | 4,960 | 2,072.5 | 27.5 s | 9/12 | 10 | 2.4 ms |

Notes:

- `static`: never reached 12 ready replicas after the step at t=300s
- `static`: never reached 12 ready replicas after the step at t=800s
- `static`: never reached 12 ready replicas after the step at t=1300s
- `ours-threshold`: never reached 12 ready replicas after the step at t=300s
- `ours-threshold`: never reached 12 ready replicas after the step at t=800s
- `ours-threshold`: never reached 12 ready replicas after the step at t=1300s
