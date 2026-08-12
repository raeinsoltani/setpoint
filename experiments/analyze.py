"""Turn raw experiment captures into the evaluation chapter's tables and figures.

Reads every run directory under ``experiments/results/raw/`` — each one produced by
``experiments/run.sh`` — and writes:

* ``metrics.csv``               one row per run, every derived number
* ``table_<pattern>.md``        the per-workload comparison table
* ``comparison_<pattern>.png``  three-panel figure, same shape as the simulator's
* ``summary.md``               headline table plus the simulator-vs-cluster check

Nothing is computed inside run.sh, and nothing is measured here. The split matters:
a metric definition that turns out to be wrong can be corrected and every past run
re-analysed, because the raw series are kept.

Two definitions carry most of the weight and are worth stating plainly.

*Required replicas* is ``ceil(pattern(t) / target)`` — derived from the workload
definition, not from the measured request rate. The pattern is what the system was
asked to serve; measured throughput is partly an *outcome* of the policy under test,
so scoring a policy against its own delivered load would let an arm look adequate
precisely by failing to serve traffic.

*SLA violation* is the fraction of measured samples whose per-ready-replica load
exceeds ``target * (1 + tolerance)``, matching the simulator exactly so the two sets
of numbers can be compared rather than merely presented side by side.

Usage:
    python3 experiments/analyze.py [--raw DIR] [--out DIR] [--include-smoke]
"""

from __future__ import annotations

import argparse
import csv
import json
import math
import os
import sys
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Sequence, Tuple

import numpy as np

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))

# The workload definitions live in the simulator and are mirrored into
# test/load/lib/patterns.js. Importing them here rather than restating them means the
# analysis cannot disagree with the curve that was actually driven at the cluster —
# which is the whole basis of the simulator-vs-cluster comparison below.
sys.path.insert(0, os.path.join(REPO, "sim", "demo"))
try:
    from simulate import (  # type: ignore  # noqa: E402
        DURATION,
        PATTERNS,
        TARGET,
        TOLERANCE,
        simulate,
    )

    SIM_AVAILABLE = True
except Exception as exc:  # pragma: no cover - environment-dependent
    print(f"warning: simulator not importable ({exc}); "
          f"falling back to local constants and skipping the ordering check")
    DURATION, TARGET, TOLERANCE = 1800, 100.0, 0.10
    PATTERNS = {}
    SIM_AVAILABLE = False

SLA_LIMIT = TARGET * (1 + TOLERANCE)

# Arm order is presentation order everywhere: baselines first, ours after, the
# deliberately-unstable variant last.
ARM_ORDER = [
    "static-peak",
    "static",
    "hpa-cpu",
    "hpa-custom",
    "ours-threshold",
    "ours-predictive",
    "ours-predictive-per-replica",
    # Phase 7.1 stabilizer ablation. Listed last so they sit apart from the arms the
    # evaluation chapter compares against each other.
    "ours-predictive-nostab",
    "ours-predictive-per-replica-nostab",
]
ARM_COLOR = {
    "static-peak": "#4d4d4d",
    "static": "#888888",
    "hpa-cpu": "#b3446c",
    "hpa-custom": "#7a5195",
    "ours-threshold": "#e08a00",          # matches the simulator's `threshold`
    "ours-predictive": "#2a6099",         # matches the simulator's `predictive`
    "ours-predictive-per-replica": "#c0392b",
    # Same hues as the arms they ablate, so a figure reads as "this policy, undamped".
    "ours-predictive-nostab": "#7fb3d5",
    "ours-predictive-per-replica-nostab": "#e8887c",
}
# Which cluster arm corresponds to which simulator strategy, for the ordering check.
# `static-peak` has no simulator equivalent: the simulator's static arm is hardcoded to
# 8 replicas (sim/demo/simulate.py, ReplicaPool.ready), which is the `static` arm here.
SIM_EQUIVALENT = {
    "static": "static",
    "ours-threshold": "threshold",
    "ours-predictive": "predictive",
}


# --------------------------------------------------------------------------- #
# Loading
# --------------------------------------------------------------------------- #
@dataclass
class Run:
    path: str
    meta: dict
    series: dict
    k6: dict

    @property
    def arm(self) -> str:
        return self.meta["arm"]

    @property
    def pattern(self) -> str:
        return self.meta["pattern"]

    @property
    def valid(self) -> bool:
        return bool(self.meta.get("valid", False))


def load_runs(raw_dir: str) -> List[Run]:
    runs: List[Run] = []
    if not os.path.isdir(raw_dir):
        return runs
    for name in sorted(os.listdir(raw_dir)):
        d = os.path.join(raw_dir, name)
        run_json = os.path.join(d, "run.json")
        if not os.path.isfile(run_json):
            continue
        with open(run_json) as fh:
            meta = json.load(fh)
        series = {}
        sp = os.path.join(d, "series.json")
        if os.path.isfile(sp):
            with open(sp) as fh:
                series = json.load(fh)
        k6: dict = {}
        kp = os.path.join(d, "k6-summary.json")
        if os.path.isfile(kp):
            with open(kp) as fh:
                try:
                    k6 = json.load(fh)
                except json.JSONDecodeError:
                    k6 = {}
        runs.append(Run(path=d, meta=meta, series=series, k6=k6))
    return runs


def series_arrays(run: Run, name: str) -> Tuple[np.ndarray, np.ndarray]:
    """Timestamps and values for one captured series, NaNs dropped."""
    entry = run.series.get(name) or {}
    result = entry.get("result") or []
    if not result:
        return np.array([]), np.array([])
    values = result[0].get("values") or []
    if not values:
        return np.array([]), np.array([])
    ts = np.array([float(v[0]) for v in values])
    vs = np.array([float(v[1]) for v in values])
    keep = np.isfinite(vs)
    return ts[keep], vs[keep]


def hold(ts_src: np.ndarray, v_src: np.ndarray, grid: np.ndarray) -> np.ndarray:
    """Sample a gauge onto ``grid`` by holding the last observation.

    Not linear interpolation: replica counts are integers that step, and
    interpolating them invents fractional fleets that then appear in every integral.
    """
    if ts_src.size == 0:
        return np.full(grid.shape, np.nan)
    idx = np.searchsorted(ts_src, grid, side="right") - 1
    out = np.where(idx >= 0, v_src[np.clip(idx, 0, None)], np.nan)
    return out


# --------------------------------------------------------------------------- #
# Metrics
# --------------------------------------------------------------------------- #
@dataclass
class Metrics:
    arm: str
    pattern: str
    valid: bool
    smoke: bool
    timestamp: str
    duration_s: float
    sla_violation_pct: float
    replica_seconds: float
    under_provision_replica_seconds: float
    over_provision_replica_seconds: float
    cpu_core_seconds: float
    scale_ups: int
    scale_downs: int
    reversals: int
    mean_ready: float
    max_ready: float
    p50_ms: float
    p95_ms: float
    p99_ms: float
    delivered_requests: float
    expected_requests: float
    delivery_ratio: float
    shed_requests: float
    reaction_times_s: List[float] = field(default_factory=list)
    notes: List[str] = field(default_factory=list)

    @property
    def median_reaction_s(self) -> float:
        finite = [r for r in self.reaction_times_s if math.isfinite(r)]
        return float(np.median(finite)) if finite else float("nan")


def k6_quantile(k6: dict, key: str) -> float:
    """Pull a latency quantile out of a k6 summary, tolerating schema differences.

    k6 has moved these keys between versions (``p(95)`` vs ``p95``) and nests the
    metric block differently in the v1 and v2 exporters, so read defensively and
    return NaN rather than guessing.
    """
    metrics = k6.get("metrics") or {}
    block = metrics.get("http_req_duration") or {}
    if isinstance(block, dict) and "values" in block and isinstance(block["values"], dict):
        block = block["values"]
    for candidate in (key, key.replace("(", "").replace(")", ""), key.upper()):
        if candidate in block:
            try:
                return float(block[candidate])
            except (TypeError, ValueError):
                pass
    return float("nan")


def k6_count(k6: dict, metric: str, field_name: str = "count") -> float:
    metrics = k6.get("metrics") or {}
    block = metrics.get(metric) or {}
    if isinstance(block, dict) and "values" in block and isinstance(block["values"], dict):
        block = block["values"]
    try:
        return float(block.get(field_name, float("nan")))
    except (TypeError, ValueError):
        return float("nan")


def latency_ms(run: Run, key: str, fallback: float) -> float:
    """Window-wide latency quantile in milliseconds, from run.json if present."""
    block = run.meta.get("latency_window_seconds") or {}
    value = block.get(key)
    if value is None:
        return fallback
    try:
        seconds = float(value)
    except (TypeError, ValueError):
        return fallback
    return seconds * 1000.0 if math.isfinite(seconds) else fallback


def required_replicas(pattern_name: str, pattern_t: np.ndarray) -> np.ndarray:
    """Replicas the workload demands at each point, from the pattern definition."""
    fn = PATTERNS.get(pattern_name)
    if fn is None:
        return np.full(pattern_t.shape, np.nan)
    load = np.array([fn(float(t)) for t in pattern_t])
    return np.ceil(load / TARGET)


def pattern_load(pattern_name: str, pattern_t: np.ndarray) -> np.ndarray:
    fn = PATTERNS.get(pattern_name)
    if fn is None:
        return np.full(pattern_t.shape, np.nan)
    return np.array([fn(float(t)) for t in pattern_t])


def compute(run: Run) -> Optional[Metrics]:
    window = run.meta["window"]
    t0, t1 = float(window["measure_start"]), float(window["measure_end"])
    step = float(str(window.get("step", "5s")).rstrip("s"))
    scale = float(run.meta.get("time_scale", 1) or 1)

    ready_ts, ready_v = series_arrays(run, "ready_replicas")
    if ready_ts.size == 0:
        print(f"  skipping {os.path.basename(run.path)}: no ready_replicas series")
        return None

    grid = np.arange(t0, t1 + step, step)
    if grid.size < 2:
        print(f"  skipping {os.path.basename(run.path)}: measurement window too short")
        return None

    # Pattern time. A smoke run compresses the axis, so wall-clock seconds map onto
    # pattern seconds through TIME_SCALE; at TIME_SCALE=1 this is the identity.
    pattern_t = (grid - t0) * scale

    ready = hold(ready_ts, ready_v, grid)
    spec = hold(*series_arrays(run, "spec_replicas"), grid)
    per_replica = hold(*series_arrays(run, "per_replica_rps"), grid)
    cpu = hold(*series_arrays(run, "cpu_cores"), grid)
    total_rps = hold(*series_arrays(run, "total_rps"), grid)
    shed_rps = hold(*series_arrays(run, "shed_rps"), grid)

    needed = required_replicas(run.pattern, pattern_t)
    offered = pattern_load(run.pattern, pattern_t)

    notes: List[str] = []

    # SLA violations, defined exactly as the simulator defines them.
    pr = per_replica[np.isfinite(per_replica)]
    sla_pct = 100.0 * float(np.count_nonzero(pr > SLA_LIMIT)) / pr.size if pr.size else float("nan")

    ready_f = np.nan_to_num(ready, nan=0.0)
    replica_seconds = float(np.sum(ready_f) * step)
    under = float(np.sum(np.clip(needed - ready_f, 0, None)) * step)
    over = float(np.sum(np.clip(ready_f - needed, 0, None)) * step)
    cpu_seconds = float(np.nansum(cpu) * step)

    # Scale actions are counted on spec.replicas, the quantity a controller actually
    # writes. Counting them on ready would also count pods finishing start-up, which
    # is the cluster reacting, not the policy deciding.
    spec_ts, spec_v = series_arrays(run, "spec_replicas")
    in_window = (spec_ts >= t0) & (spec_ts <= t1)
    sv = spec_v[in_window]
    deltas = np.diff(sv) if sv.size > 1 else np.array([])
    moves = deltas[deltas != 0]
    scale_ups = int(np.count_nonzero(moves > 0))
    scale_downs = int(np.count_nonzero(moves < 0))
    signs = np.sign(moves)
    reversals = int(np.count_nonzero(np.diff(signs) != 0)) if signs.size > 1 else 0

    # Reaction time: for each upward step in demand, how long until the fleet has
    # enough *ready* replicas to serve it. Undefined for smooth patterns, which have
    # no step to react to — their equivalent measure is under-provisioning above.
    reactions: List[float] = []
    if needed.size > 1:
        jumps = np.where(np.diff(needed) > 0)[0] + 1
        for j in jumps:
            target_n = needed[j]
            after = np.where((np.arange(grid.size) >= j) & (ready_f >= target_n))[0]
            if after.size:
                reactions.append(float((grid[after[0]] - grid[j])))
            else:
                reactions.append(float("inf"))
                notes.append(f"never reached {int(target_n)} ready replicas after the step at t={pattern_t[j]:.0f}s")

    delivered = k6_count(run.k6, "http_reqs")
    # Integrate the pattern itself on a dense grid rather than the capture grid: the
    # capture is 5s-spaced (50s in pattern time on a compressed run) and can run a few
    # samples past the pattern's end, both of which inflate the expected count enough
    # to trip the under-delivery check on a run that delivered everything asked of it.
    # A compressed run spends TIME_SCALE times fewer wall-clock seconds at each rate,
    # so the integral is divided to match what k6 actually had time to send.
    span = min(float(pattern_t[-1]), float(DURATION))
    dense = np.arange(0.0, span, 1.0)
    expected = (float(np.trapezoid(pattern_load(run.pattern, dense), dense)) / scale
                if dense.size > 1 and run.pattern in PATTERNS else float("nan"))
    ratio = delivered / expected if expected and math.isfinite(expected) and expected > 0 else float("nan")
    if math.isfinite(ratio) and ratio < 0.95:
        notes.append(f"offered load under-delivered: k6 sent {ratio * 100:.1f}% of the pattern's request integral")

    shed_total = float(np.nansum(shed_rps) * step)

    # Cross-check: for our arms, the independently-computed per-replica series should
    # track what the policy itself reported. A divergence means the analysis is not
    # looking at the signal the policy acted on, which would invalidate every number
    # derived from it.
    if run.arm.startswith("ours-"):
        seen = hold(*series_arrays(run, "autoscaler_metric_value"), grid)
        both = np.isfinite(seen) & np.isfinite(per_replica)
        if np.count_nonzero(both) > 10:
            denom = np.maximum(np.abs(per_replica[both]), 1.0)
            rel = float(np.median(np.abs(seen[both] - per_replica[both]) / denom))
            if rel > 0.15:
                notes.append(f"per_replica_rps and autoscaler_metric_value differ by {rel * 100:.0f}% (median)")

    return Metrics(
        arm=run.arm,
        pattern=run.pattern,
        valid=run.valid,
        smoke=bool(run.meta.get("smoke", False)),
        timestamp=run.meta.get("timestamp", ""),
        duration_s=float(t1 - t0),
        sla_violation_pct=sla_pct,
        replica_seconds=replica_seconds,
        under_provision_replica_seconds=under,
        over_provision_replica_seconds=over,
        cpu_core_seconds=cpu_seconds,
        scale_ups=scale_ups,
        scale_downs=scale_downs,
        reversals=reversals,
        mean_ready=float(np.mean(ready_f)),
        max_ready=float(np.max(ready_f)),
        # Prefer the application histogram's window-wide quantiles, captured by
        # run.sh; k6's summary export carries no p99 at all. k6 remains the fallback
        # and is the client-side view, which includes network time the histogram does
        # not — they should agree closely here, and a large gap is worth chasing.
        p50_ms=latency_ms(run, "p50", k6_quantile(run.k6, "med")),
        p95_ms=latency_ms(run, "p95", k6_quantile(run.k6, "p(95)")),
        p99_ms=latency_ms(run, "p99", k6_quantile(run.k6, "p(99)")),
        delivered_requests=delivered,
        expected_requests=expected,
        delivery_ratio=ratio,
        shed_requests=shed_total,
        reaction_times_s=reactions,
        notes=notes,
    )


# --------------------------------------------------------------------------- #
# Output
# --------------------------------------------------------------------------- #
CSV_FIELDS = [
    "pattern", "arm", "timestamp", "valid", "smoke", "duration_s",
    "sla_violation_pct", "replica_seconds", "under_provision_replica_seconds",
    "over_provision_replica_seconds", "cpu_core_seconds", "median_reaction_s",
    "scale_ups", "scale_downs", "reversals", "mean_ready", "max_ready",
    "p50_ms", "p95_ms", "p99_ms", "delivered_requests", "expected_requests",
    "delivery_ratio", "shed_requests", "notes",
]


def write_csv(rows: Sequence[Metrics], path: str) -> None:
    with open(path, "w", newline="") as fh:
        w = csv.DictWriter(fh, fieldnames=CSV_FIELDS)
        w.writeheader()
        for m in rows:
            w.writerow({
                "pattern": m.pattern, "arm": m.arm, "timestamp": m.timestamp,
                "valid": m.valid, "smoke": m.smoke, "duration_s": f"{m.duration_s:.0f}",
                "sla_violation_pct": f"{m.sla_violation_pct:.2f}",
                "replica_seconds": f"{m.replica_seconds:.0f}",
                "under_provision_replica_seconds": f"{m.under_provision_replica_seconds:.0f}",
                "over_provision_replica_seconds": f"{m.over_provision_replica_seconds:.0f}",
                "cpu_core_seconds": f"{m.cpu_core_seconds:.1f}",
                "median_reaction_s": f"{m.median_reaction_s:.1f}",
                "scale_ups": m.scale_ups, "scale_downs": m.scale_downs,
                "reversals": m.reversals,
                "mean_ready": f"{m.mean_ready:.2f}", "max_ready": f"{m.max_ready:.0f}",
                "p50_ms": f"{m.p50_ms:.2f}", "p95_ms": f"{m.p95_ms:.2f}",
                "p99_ms": f"{m.p99_ms:.2f}",
                "delivered_requests": f"{m.delivered_requests:.0f}",
                "expected_requests": f"{m.expected_requests:.0f}",
                "delivery_ratio": f"{m.delivery_ratio:.3f}",
                "shed_requests": f"{m.shed_requests:.0f}",
                "notes": "; ".join(m.notes),
            })


def fmt(v: float, nd: int = 1) -> str:
    if v is None or (isinstance(v, float) and not math.isfinite(v)):
        return "—"
    return f"{v:,.{nd}f}"


def write_pattern_table(pattern: str, rows: Sequence[Metrics], path: str) -> None:
    lines = [
        f"# `{pattern}` — cluster results",
        "",
        "Measurement window only; warmup and settle excluded. Required replicas are",
        "`ceil(pattern(t) / 100)` from the workload definition, not from delivered load.",
        "",
        "| Arm | SLA violations | Replica-seconds | Under-prov. | Over-prov. | CPU core-s | Reaction (median) | Scale ↑/↓ | Reversals | p95 latency |",
        "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for m in rows:
        reaction = "n/a" if not m.reaction_times_s else fmt(m.median_reaction_s) + " s"
        lines.append(
            f"| `{m.arm}` | {fmt(m.sla_violation_pct)}% | {fmt(m.replica_seconds, 0)} | "
            f"{fmt(m.under_provision_replica_seconds, 0)} | {fmt(m.over_provision_replica_seconds, 0)} | "
            f"{fmt(m.cpu_core_seconds)} | {reaction} | {m.scale_ups}/{m.scale_downs} | "
            f"{m.reversals} | {fmt(m.p95_ms)} ms |"
        )
    notes = [(m.arm, n) for m in rows for n in m.notes]
    if notes:
        lines += ["", "Notes:", ""]
        lines += [f"- `{arm}`: {n}" for arm, n in notes]
    lines.append("")
    with open(path, "w") as fh:
        fh.write("\n".join(lines))


def plot_pattern(pattern: str, runs: Sequence[Run], path: str) -> None:
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    fig, (ax1, ax2, ax3) = plt.subplots(3, 1, figsize=(10, 9), sharex=True)

    drew_load = False
    for run in runs:
        window = run.meta["window"]
        t0, t1 = float(window["measure_start"]), float(window["measure_end"])
        step = float(str(window.get("step", "5s")).rstrip("s"))
        scale = float(run.meta.get("time_scale", 1) or 1)
        grid = np.arange(t0, t1 + step, step)
        x = (grid - t0) * scale
        color = ARM_COLOR.get(run.arm)

        if not drew_load:
            ax1.plot(x, pattern_load(pattern, x), color="#333", lw=1.6, label="offered (pattern)")
            td, tv = series_arrays(run, "total_rps")
            ax1.plot((td - t0) * scale, tv, color="#999", lw=1.0, ls="--", label="measured")
            ax1.set_ylabel("Total load\n(req/s)")
            ax1.set_title(f"Autoscaling comparison — '{pattern}' workload (cluster)")
            ax1.legend(loc="upper right", fontsize=8)
            ax1.grid(alpha=0.3)
            drew_load = True

        ready = hold(*series_arrays(run, "ready_replicas"), grid)
        ax2.step(x, ready, where="post", label=run.arm, color=color, lw=1.6)

        pr = hold(*series_arrays(run, "per_replica_rps"), grid)
        ax3.plot(x, pr, label=run.arm, color=color, lw=1.2)

    needed = required_replicas(pattern, np.arange(0, DURATION, 5.0))
    ax2.plot(np.arange(0, DURATION, 5.0), needed, color="#000", lw=1.0, ls=":", label="required")
    ax2.set_ylabel("Ready\nreplicas")
    ax2.legend(loc="upper right", ncol=3, fontsize=8)
    ax2.grid(alpha=0.3)

    # Shade warmup and settle. Every number in the tables comes from the unshaded
    # region only, and a trace that looks alarming in the shaded parts (a fleet still
    # draining, a rate() window still filling) is not a result — saying so on the
    # figure is cheaper than explaining it once per reader.
    if runs:
        w = runs[0].meta["window"]
        sc = float(runs[0].meta.get("time_scale", 1) or 1)
        measure_end = (float(w["measure_end"]) - float(w["measure_start"])) * sc
        for ax in (ax1, ax2, ax3):
            lo, hi = ax.get_xlim()
            ax.axvspan(lo, 0, color="#000000", alpha=0.05, lw=0)
            ax.axvspan(measure_end, hi, color="#000000", alpha=0.05, lw=0)
            ax.set_xlim(lo, hi)

    ax3.axhline(TARGET, color="green", ls="--", lw=1, label="target")
    ax3.axhline(SLA_LIMIT, color="red", ls=":", lw=1, label="SLA limit")
    ax3.set_ylabel("Per-replica\nload (req/s)")
    ax3.set_xlabel("time (s)")
    ax3.legend(loc="upper right", ncol=4, fontsize=8)
    ax3.grid(alpha=0.3)
    ax3.set_ylim(0, TARGET * 3)

    fig.tight_layout()
    fig.savefig(path, dpi=130)
    plt.close(fig)


def ordering_check(by_pattern: Dict[str, List[Metrics]]) -> List[str]:
    """Does the cluster reproduce the simulator's *ordering* of the arms?

    Open question 6 in the lab notebook. Directional agreement is a validity argument
    for both artefacts; disagreement means one of them is modelling something the
    other is not, and has to be investigated rather than presented.
    """
    if not SIM_AVAILABLE:
        return ["Simulator not importable in this environment; ordering check skipped."]

    lines = [
        "| Workload | Metric | Simulator order | Cluster order | Agrees |",
        "|---|---|---|---|---|",
    ]
    for pattern in sorted(by_pattern):
        rows = [m for m in by_pattern[pattern] if m.arm in SIM_EQUIVALENT]
        if len(rows) < 2 or pattern not in PATTERNS:
            continue
        sim_results = {r.name: r for r in
                       (simulate(PATTERNS[pattern], s) for s in ("static", "threshold", "predictive"))}

        for label, sim_key, cluster_key in (
            ("SLA violations", "sla", "sla_violation_pct"),
            ("Replica-seconds", "cost", "replica_seconds"),
        ):
            def sim_value(arm: str) -> float:
                r = sim_results[SIM_EQUIVALENT[arm]]
                if sim_key == "sla":
                    return 100.0 * r.violations / DURATION
                return r.replica_seconds

            sim_order = sorted((m.arm for m in rows), key=sim_value)
            cluster_order = sorted(rows, key=lambda m: getattr(m, cluster_key))
            cluster_names = [m.arm for m in cluster_order]
            agrees = "yes" if sim_order == cluster_names else "**no**"
            lines.append(
                f"| `{pattern}` | {label} | {' < '.join(sim_order)} | "
                f"{' < '.join(cluster_names)} | {agrees} |"
            )
    if len(lines) == 2:
        lines.append("| — | — | — | — | no comparable runs yet |")
    return lines


def _spread(values: List[float], fmt: str = "{:.1f}") -> str:
    """Every value, then the max-min gap. At n=2-3 a mean hides more than it shows."""
    finite = [v for v in values if v is not None and math.isfinite(v)]
    if not finite:
        return "—"
    shown = ", ".join(fmt.format(v) for v in finite)
    if len(finite) < 2:
        return shown
    return f"{shown}  (Δ{fmt.format(max(finite) - min(finite))})"


def write_summary(by_pattern: Dict[str, List[Metrics]], excluded: List[Run], path: str,
                  repeats: Optional[Dict[Tuple[str, str], List[Metrics]]] = None) -> None:
    lines = [
        "# Cluster evaluation summary",
        "",
        "Generated by `experiments/analyze.py` from the raw captures in",
        "`experiments/results/raw/`. Regenerate rather than edit.",
        "",
        "## Headline",
        "",
        "| Workload | Arm | SLA violations | Replica-seconds | Under-prov. | p95 latency |",
        "|---|---|---:|---:|---:|---:|",
    ]
    for pattern in sorted(by_pattern):
        for m in by_pattern[pattern]:
            lines.append(
                f"| `{pattern}` | `{m.arm}` | {fmt(m.sla_violation_pct)}% | "
                f"{fmt(m.replica_seconds, 0)} | {fmt(m.under_provision_replica_seconds, 0)} | "
                f"{fmt(m.p95_ms)} ms |"
            )
    if not by_pattern:
        lines.append("| — | — | — | — | — | no valid runs yet |")

    lines += [
        "",
        "## Simulator vs cluster — ordering agreement",
        "",
        "Lab notebook open question 6. Compares the *order* of the arms, not their",
        "absolute values: the simulator models a 30s startup delay and no network,",
        "so its magnitudes are not expected to match the cluster's.",
        "",
    ]
    lines += ordering_check(by_pattern)

    multi = {k: v for k, v in (repeats or {}).items() if len(v) > 1}
    if multi:
        lines += [
            "",
            "## Repeatability",
            "",
            "Independent `TIME_SCALE=1` runs of the same arm on the same workload. The",
            "tables above report the **latest** run per arm; this reports every one.",
            "",
            "This section sets the resolution of every comparison made elsewhere: a",
            "difference between two arms that is smaller than the spread of one arm",
            "against itself is not a result. Values are listed individually rather than",
            "averaged, because at n=2-3 a mean hides more than it shows.",
            "",
            "| Workload | Arm | n | SLA violations % | Replica-seconds | Reversals | Reaction (median) s |",
            "|---|---|---:|---|---|---|---|",
        ]
        for (pattern, arm) in sorted(multi):
            ms = multi[(pattern, arm)]
            lines.append(
                f"| `{pattern}` | `{arm}` | {len(ms)} "
                f"| {_spread([m.sla_violation_pct for m in ms])} "
                f"| {_spread([m.replica_seconds for m in ms], '{:,.0f}')} "
                f"| {_spread([float(m.reversals) for m in ms], '{:.0f}')} "
                f"| {_spread([m.median_reaction_s for m in ms])} |"
            )

    if excluded:
        lines += ["", "## Excluded runs", "",
                  "| Run | Reasons |", "|---|---|"]
        for run in excluded:
            reasons = "; ".join(run.meta.get("invalid_reasons") or ["(none recorded)"])
            lines.append(f"| `{os.path.basename(run.path)}` | {reasons} |")

    lines.append("")
    with open(path, "w") as fh:
        fh.write("\n".join(lines))


# --------------------------------------------------------------------------- #
def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--raw", default=os.path.join(REPO, "experiments", "results", "raw"))
    ap.add_argument("--out", default=os.path.join(REPO, "experiments", "results"))
    ap.add_argument("--include-smoke", action="store_true",
                    help="include TIME_SCALE != 1 runs; they are not evaluation results")
    ap.add_argument("--include-invalid", action="store_true",
                    help="include runs run.sh marked invalid")
    args = ap.parse_args()

    os.makedirs(args.out, exist_ok=True)

    runs = load_runs(args.raw)
    if not runs:
        print(f"no runs found under {args.raw}")
        print("run one with:  ./experiments/run.sh --arm ours-predictive --pattern ramp")
        return 1

    kept, excluded = [], []
    for r in runs:
        if r.meta.get("smoke") and not args.include_smoke:
            excluded.append(r)
        elif not r.valid and not args.include_invalid:
            excluded.append(r)
        else:
            kept.append(r)

    print(f"{len(runs)} run(s) found; {len(kept)} included, {len(excluded)} excluded")

    # Latest run wins per (pattern, arm) — reruns are the normal way to replace a run
    # that was invalidated by something in the environment.
    latest: Dict[Tuple[str, str], Run] = {}
    for r in kept:
        key = (r.pattern, r.arm)
        if key not in latest or r.meta.get("timestamp", "") > latest[key].meta.get("timestamp", ""):
            latest[key] = r

    # Score *every* kept run, not just the latest. Deliberate repeats are how the
    # measurement resolution gets established (§11.9), and collapsing them to one row
    # would silently discard precisely the runs that were paid for to quantify spread.
    metrics_by_path: Dict[str, Metrics] = {}
    repeats: Dict[Tuple[str, str], List[Metrics]] = {}
    for r in kept:
        m = compute(r)
        if m is None:
            continue
        metrics_by_path[r.path] = m
        repeats.setdefault((r.pattern, r.arm), []).append(m)
    for ms in repeats.values():
        ms.sort(key=lambda m: m.timestamp)

    all_metrics: List[Metrics] = []
    runs_by_pattern: Dict[str, List[Run]] = {}
    for (pattern, arm), run in sorted(latest.items()):
        m = metrics_by_path.get(run.path)
        if m is None:
            continue
        all_metrics.append(m)
        runs_by_pattern.setdefault(pattern, []).append(run)

    if not all_metrics:
        print("nothing analysable")
        return 1

    order = {a: i for i, a in enumerate(ARM_ORDER)}
    by_pattern: Dict[str, List[Metrics]] = {}
    for m in all_metrics:
        by_pattern.setdefault(m.pattern, []).append(m)
    for pattern in by_pattern:
        by_pattern[pattern].sort(key=lambda m: order.get(m.arm, 99))
        runs_by_pattern[pattern].sort(key=lambda r: order.get(r.arm, 99))

    csv_path = os.path.join(args.out, "metrics.csv")
    write_csv(sorted(all_metrics, key=lambda m: (m.pattern, order.get(m.arm, 99))), csv_path)
    print(f"  wrote {os.path.relpath(csv_path, REPO)}")

    for pattern, rows in sorted(by_pattern.items()):
        tp = os.path.join(args.out, f"table_{pattern}.md")
        write_pattern_table(pattern, rows, tp)
        print(f"  wrote {os.path.relpath(tp, REPO)}")
        try:
            fp = os.path.join(args.out, f"comparison_{pattern}.png")
            plot_pattern(pattern, runs_by_pattern[pattern], fp)
            print(f"  wrote {os.path.relpath(fp, REPO)}")
        except ImportError:
            print("  matplotlib unavailable; figures skipped")

    sp = os.path.join(args.out, "summary.md")
    write_summary(by_pattern, excluded, sp, repeats)
    print(f"  wrote {os.path.relpath(sp, REPO)}")

    flagged = [(m, n) for m in all_metrics for n in m.notes]
    if flagged:
        print("\nflags:")
        for m, n in flagged:
            print(f"  {m.pattern}/{m.arm}: {n}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
