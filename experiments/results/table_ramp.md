# `ramp` — cluster results

Measurement window only; warmup and settle excluded. Required replicas are
`ceil(pattern(t) / 100)` from the workload definition, not from delivered load.

| Arm | SLA violations | Replica-seconds | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 latency |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `static` | 35.5% | 14,440 | 2,400 | 3,630 | 2,624.9 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `ours-threshold` | 5.2% | 12,050 | 1,220 | 0 | 2,479.0 | 125.0 s | 9/0 | 0 | 2.5 ms |

Notes:

- `static`: never reached 9 ready replicas after the step at t=1025s
- `static`: never reached 10 ready replicas after the step at t=1145s
- `static`: never reached 11 ready replicas after the step at t=1265s
- `static`: never reached 12 ready replicas after the step at t=1385s
