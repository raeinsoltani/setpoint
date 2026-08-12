# `diurnal` — cluster results

Measurement window only; warmup and settle excluded. Required replicas are
`ceil(pattern(t) / 100)` from the workload definition, not from delivered load.

| Arm | SLA violations | Replica-seconds | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 latency |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `static-peak` | 0.0% | 21,720 | 0 | 7,290 | 2,907.8 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `static` | 40.3% | 14,480 | 2,490 | 2,540 | 2,895.9 | 0.0 s | 0/0 | 0 | 2.4 ms |
| `ours-threshold` | 3.6% | 14,545 | 1,025 | 1,140 | 2,854.7 | 107.5 s | 8/8 | 1 | 2.4 ms |

Notes:

- `static`: never reached 9 ready replicas after the step at t=485s
- `static`: never reached 10 ready replicas after the step at t=550s
- `static`: never reached 11 ready replicas after the step at t=620s
- `static`: never reached 12 ready replicas after the step at t=710s
