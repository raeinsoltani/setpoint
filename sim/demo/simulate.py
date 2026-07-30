"""Closed-loop simulation comparing scaling strategies.

The simulator models a service whose observed per-replica load is
``total_load / ready_replicas``. Newly requested replicas become *ready* only
after a start-up delay (this is what makes reactive scaling suffer during
spikes and what a predictive policy can hide). Each strategy is driven on the
identical workload, and we report two headline numbers:

* SLA violation ratio -- fraction of time the ready replicas were overloaded
  (per-replica load above ``target * (1 + tolerance)``), a proxy for latency
  breaches.
* Replica-seconds -- integral of ready replicas over time, a proxy for cost.

Run:  python demo/simulate.py
Output: comparison_<pattern>.png  and a printed summary table.
"""

from __future__ import annotations

import math
import os
import sys
from dataclasses import dataclass, field
from typing import Callable, Dict, List

import numpy as np

# make the package importable when run as a script
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from autoscaler.policy import Forecaster, Policy, Stabilizer  # noqa: E402

TARGET = 100.0          # target requests/sec per replica
TOLERANCE = 0.10
MIN_R, MAX_R = 1, 20
CONTROL_INTERVAL = 15   # seconds between reconciles (like HPA)
STARTUP_DELAY = 30      # seconds for a new replica to become ready
DURATION = 1800         # total simulated seconds (30 min)


# --------------------------------------------------------------------------- #
# Workload patterns: total requests/sec over time
# --------------------------------------------------------------------------- #
def pattern_spike(t: float) -> float:
    base = 300.0
    if 600 <= t < 1200:          # sudden 4x spike for 10 minutes
        return 1300.0
    return base


def pattern_diurnal(t: float) -> float:
    # smooth rise and fall, like a daily traffic curve
    return 300.0 + 900.0 * math.sin(math.pi * t / DURATION) ** 2


def pattern_bursty(t: float) -> float:
    base = 400.0
    for start in (300, 800, 1300):
        if start <= t < start + 120:
            return 1200.0
        if start + 120 <= t < start + 180:  # short trough after each burst
            return 200.0
    return base


def pattern_ramp(t: float) -> float:
    """A single sustained trend: 200 rps, rising linearly to 1200 over 20 minutes.

    Added because the three patterns above do not contain the workload predictive
    scaling actually exists for. ``spike`` and ``bursty`` are step functions no
    forecaster can anticipate, and ``diurnal`` trends but also turns. This one gives
    a forecaster a clean trend to extrapolate, so it is where the predictive policy
    should show its largest advantage -- and if it does not win here, it does not win.

    Mirrored exactly by ``ramp`` in test/load/lib/patterns.js. Change both together.
    """
    if t < 300:
        return 200.0
    if t < 1500:
        return 200.0 + 1000.0 * (t - 300.0) / 1200.0
    return 1200.0


PATTERNS: Dict[str, Callable[[float], float]] = {
    "spike": pattern_spike,
    "diurnal": pattern_diurnal,
    "bursty": pattern_bursty,
    "ramp": pattern_ramp,
}


@dataclass
class ReplicaPool:
    """Tracks ready replicas and pending (starting) replicas."""

    ready: int = 1
    _pending: List[float] = field(default_factory=list)  # ready-at timestamps

    def desired(self, target: int, now: float) -> None:
        current_total = self.ready + len(self._pending)
        if target > current_total:                       # scale up: queue start-ups
            for _ in range(target - current_total):
                self._pending.append(now + STARTUP_DELAY)
        elif target < self.ready:                        # scale down: drop immediately
            self.ready = target
            self._pending.clear()
        elif target <= current_total:                    # cancel surplus pending
            drop = current_total - target
            for _ in range(min(drop, len(self._pending))):
                self._pending.pop()

    def tick(self, now: float) -> None:
        still_pending = []
        for ready_at in self._pending:
            if now >= ready_at:
                self.ready += 1
            else:
                still_pending.append(ready_at)
        self._pending = still_pending

    @property
    def total(self) -> int:
        return self.ready + len(self._pending)


@dataclass
class Result:
    name: str
    time: List[float]
    load: List[float]
    ready: List[int]
    per_replica: List[float]
    violations: int
    replica_seconds: float


def make_policy(kind: str) -> Policy:
    stab = Stabilizer(window_seconds=90.0)
    if kind == "threshold":
        return Policy(TARGET, TOLERANCE, MIN_R, MAX_R, predictive=False,
                      stabilizer=stab, name="threshold")
    if kind == "predictive":
        return Policy(TARGET, TOLERANCE, MIN_R, MAX_R, predictive=True,
                      forecaster=Forecaster(horizon=3, alpha=0.5),
                      stabilizer=stab, name="predictive")
    raise ValueError(kind)


def simulate(pattern: Callable[[float], float], strategy: str) -> Result:
    """Run one strategy on one workload. ``strategy`` in {static, threshold, predictive}."""
    pool = ReplicaPool(ready=3)
    policy = None if strategy == "static" else make_policy(strategy)
    if strategy == "static":
        pool.ready = 8                     # a fixed, generously-provisioned baseline

    times, loads, readys, per_rep = [], [], [], []
    violations = 0
    replica_seconds = 0.0
    next_control = 0.0
    threshold = TARGET * (1 + TOLERANCE)

    for t in range(DURATION):
        pool.tick(float(t))
        total_load = pattern(float(t))
        ready = max(pool.ready, 1)
        observed = total_load / ready       # per-ready-replica load (what Prometheus sees)

        if policy is not None and t >= next_control:
            decision = policy.decide(pool.total, observed, now=float(t))
            pool.desired(decision.desired_replicas, float(t))
            next_control = t + CONTROL_INTERVAL

        if observed > threshold:
            violations += 1
        replica_seconds += pool.ready

        times.append(t)
        loads.append(total_load)
        readys.append(pool.ready)
        per_rep.append(observed)

    return Result(
        name=strategy,
        time=times,
        load=loads,
        ready=readys,
        per_replica=per_rep,
        violations=violations,
        replica_seconds=replica_seconds,
    )


def plot(pattern_name: str, results: List[Result], outdir: str) -> str:
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    colors = {"static": "#888888", "threshold": "#e08a00", "predictive": "#2a6099"}
    fig, (ax1, ax2, ax3) = plt.subplots(3, 1, figsize=(10, 9), sharex=True)

    ax1.plot(results[0].time, results[0].load, color="#333", lw=1.6)
    ax1.set_ylabel("Total load\n(req/s)")
    ax1.set_title(f"Autoscaling comparison — '{pattern_name}' workload")
    ax1.grid(alpha=0.3)

    for r in results:
        c = colors.get(r.name, None)
        ax2.step(r.time, r.ready, where="post", label=r.name, color=c, lw=1.6)
    ax2.set_ylabel("Ready\nreplicas")
    ax2.legend(loc="upper right", ncol=3, fontsize=9)
    ax2.grid(alpha=0.3)

    for r in results:
        c = colors.get(r.name, None)
        ax3.plot(r.time, r.per_replica, label=r.name, color=c, lw=1.2)
    ax3.axhline(TARGET, color="green", ls="--", lw=1, label="target")
    ax3.axhline(TARGET * (1 + TOLERANCE), color="red", ls=":", lw=1, label="SLA limit")
    ax3.set_ylabel("Per-replica\nload (req/s)")
    ax3.set_xlabel("time (s)")
    ax3.legend(loc="upper right", ncol=5, fontsize=8)
    ax3.grid(alpha=0.3)
    ax3.set_ylim(0, TARGET * 3)

    fig.tight_layout()
    path = os.path.join(outdir, f"comparison_{pattern_name}.png")
    fig.savefig(path, dpi=130)
    plt.close(fig)
    return path


def main() -> None:
    outdir = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "results"))
    os.makedirs(outdir, exist_ok=True)

    print(f"{'workload':<10} {'strategy':<12} {'SLA violation %':>16} {'replica-seconds':>16}")
    print("-" * 58)
    for pname, pfun in PATTERNS.items():
        results = [simulate(pfun, s) for s in ("static", "threshold", "predictive")]
        for r in results:
            viol_pct = 100.0 * r.violations / DURATION
            print(f"{pname:<10} {r.name:<12} {viol_pct:>15.1f}% {r.replica_seconds:>16.0f}")
        path = plot(pname, results, outdir)
        print(f"{'':<10} -> saved {os.path.basename(path)}")
        print("-" * 58)


if __name__ == "__main__":
    main()
