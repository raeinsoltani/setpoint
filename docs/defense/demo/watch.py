#!/usr/bin/env python3
"""One line per control cycle: what the controller saw, what it decided, what users felt.

    python3 docs/defense/demo/watch.py

This is the controller explaining itself (lab-notebook §7.1), read from its own
exporter rather than from Prometheus, so a line appears the moment a reconcile
finishes instead of a scrape later. The exporter is reached through the API
server's service proxy, which follows the Service across controller restarts —
every act restarts the controller, and a port-forward would die with the old pod.

The fleet bar is the decision:

    █  ready and kept          ░  requested, still starting
    ×  ready, being removed    ┃  the raw recommendation, where the stabilizer
                                  overrode it

Each act ends with a scorecard. Reversals are counted exactly as
experiments/analyze.py counts them: a sign change between consecutive moves of
.spec.replicas.

Standard library only, so it runs on any machine with python3 and kubectl.
"""

from __future__ import annotations

import argparse
import json
import math
import re
import signal
import subprocess
import sys
import time
import urllib.request
from dataclasses import dataclass, field

NS = "default"
EXPORTER = f"/api/v1/namespaces/{NS}/services/setpoint:metrics/proxy/metrics"
K6_API = "http://localhost:6565/v1/metrics/"

# 256-colour codes rather than 24-bit: macOS Terminal.app does not render truecolor.
BLUE, RED, AMBER, GREEN, DIM, BOLD, RESET = (
    "\033[38;5;33m", "\033[38;5;203m", "\033[38;5;214m", "\033[38;5;77m",
    "\033[38;5;245m", "\033[1m", "\033[0m")


def kubectl(*args: str, timeout: float = 4) -> str | None:
    try:
        out = subprocess.run(["kubectl", *args], capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        return None
    return out.stdout if out.returncode == 0 else None


def scrape() -> dict[str, float] | None:
    text = kubectl("get", "--raw", EXPORTER)
    if text is None:
        return None
    values = {}
    for line in text.splitlines():
        m = re.match(r"^(autoscaler_[a-z_]+)(\{[^}]*\})?\s+(\S+)$", line)
        if m:
            name = m.group(1) + (m.group(2) or "")
            values[name] = float(m.group(3))
    return values


def live_config() -> tuple[str, float]:
    """Policy name and stabilization window from the live ConfigMap.

    The controller reads its config once, at start, and every act changes the
    ConfigMap and restarts it — the same contract experiments/run.sh verifies. So
    the ConfigMap read at the moment a restart is seen is the config that pod runs.
    """
    raw = kubectl("get", "cm", "setpoint-config", "-n", NS, "-o", "jsonpath={.data.config\\.yaml}") or ""
    policy, window, in_policy = "?", float("nan"), False
    for line in raw.splitlines():
        if line.startswith("policy:"):
            in_policy = True
        elif in_policy and re.match(r"^\S", line):
            in_policy = False
        if in_policy and policy == "?" and (m := re.match(r"^\s+name:\s*([a-z-]+)", line)):
            policy = m.group(1)
        if m := re.match(r"^\s+stabilization_window_seconds:\s*([0-9.]+)", line):
            window = float(m.group(1))
    return policy, window


def k6_totals() -> tuple[float, float] | None:
    """Cumulative (requests, failed requests) from k6's REST API, or None if k6 is not running."""
    try:
        with urllib.request.urlopen(K6_API + "http_reqs", timeout=1) as r:
            count = json.load(r)["data"]["attributes"]["sample"]["count"]
        with urllib.request.urlopen(K6_API + "http_req_failed", timeout=1) as r:
            rate = json.load(r)["data"]["attributes"]["sample"]["rate"]
    except Exception:
        return None
    # The API publishes the failure *rate* since k6 started, not a count; the count
    # is recovered so that each cycle can report its own failures, not a running mean.
    return float(count), rate * count


@dataclass
class Act:
    policy: str
    window: float
    cycles: int = 0
    moves: list[int] = field(default_factory=list)
    ready_sum: float = 0
    reqs: float = 0
    failed: float = 0

    @property
    def broken(self) -> bool:
        return self.policy == "predictive-per-replica"

    @property
    def colour(self) -> str:
        if not self.broken:
            return BLUE
        return AMBER if self.window > 0 else RED

    def title(self) -> str:
        signal_ = {
            "predictive": "forecasting TOTAL load        λ̂ = forecast(m · r)",
            "predictive-per-replica": "forecasting PER-POD load      m̂ = forecast(m)",
            "threshold": "reactive threshold            no forecast",
        }.get(self.policy, self.policy)
        stab = "stabilizer OFF" if self.window == 0 else f"stabilizer ON ({self.window:.0f} s)"
        return f"{signal_}   ·   {stab}"

    def reversals(self) -> int:
        signs = [1 if d > 0 else -1 for d in self.moves]
        return sum(1 for a, b in zip(signs, signs[1:]) if a != b)


def bar(ready: int, desired: int, raw: int, width: int, base: str) -> str:
    cells = [" "] * width
    colours = [base] * width
    for i in range(min(width, max(ready, desired))):
        if i < min(ready, desired):
            cells[i] = "█"
        elif i < desired:
            cells[i] = "░"
        else:
            cells[i], colours[i] = "×", RED
    if raw != desired and 0 < raw <= width:
        cells[raw - 1], colours[raw - 1] = "┃", AMBER + BOLD
    return "".join(f"{RESET}{c}{s}" for s, c in zip(cells, colours)) + RESET


HEADER = (f"{DIM}  time       ready → desired   fleet                    "
          f"load per pod   forecast        users failed{RESET}")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--width", type=int, default=20, help="fleet bar width; max_replicas (default 20)")
    ap.add_argument("--log", help="also append plain lines to this file (for rehearsal records)")
    args = ap.parse_args()

    log = open(args.log, "a", encoding="utf-8") if args.log else None
    strip = re.compile(r"\033\[[0-9;]*m")

    def out(s: str = "") -> None:
        print(s, flush=True)
        if log:
            log.write(strip.sub("", s) + "\n")
            log.flush()

    act: Act | None = None
    last_success = last_total = last_desired = None
    k6_prev = None
    restarting = False

    def scorecard() -> None:
        if not act or act.cycles == 0:
            return
        failed = f"{100 * act.failed / act.reqs:.1f}%" if act.reqs else "–"
        out(f"{act.colour}  └─ {act.cycles} cycles · {len(act.moves)} moves · "
            f"{act.reversals()} reversals · mean fleet {act.ready_sum / act.cycles:.1f} · "
            f"users failed {failed}{RESET}")

    def finish(*_):
        scorecard()
        sys.exit(0)

    signal.signal(signal.SIGINT, finish)
    signal.signal(signal.SIGTERM, finish)

    while True:
        m = scrape()
        if m is None:
            if not restarting and act:
                out(f"{DIM}  · controller restarting — no decisions until it is back{RESET}")
            restarting = True
            time.sleep(1)
            continue

        total = m.get("autoscaler_reconcile_total", 0)
        success = m.get("autoscaler_last_success_timestamp_seconds", 0)

        # A new process starts its counter again, so a smaller total is a restart:
        # a new act, with a new config, beginning with this cycle.
        if act is None or (last_total is not None and total < last_total):
            scorecard()
            act = Act(*live_config())
            out()
            out(f"{act.colour}{BOLD}━━ {act.title()} {RESET}")
            out(HEADER)
            last_success = last_desired = None
        restarting = False
        last_total = total

        if success and success != last_success:
            last_success = success
            ready = int(m.get("autoscaler_ready_replicas", 0))
            spec = int(m.get("autoscaler_current_replicas", 0))
            desired = int(m.get("autoscaler_desired_replicas", 0))
            raw = int(m.get("autoscaler_raw_recommendation", 0))
            metric = m.get("autoscaler_metric_value", math.nan)
            predicted = m.get("autoscaler_predicted_value", math.nan)

            # .spec.replicas differing from what this controller last set means
            # someone else wrote it: the kick. Said out loud, and kept out of the
            # move count, because a disturbance is not a decision.
            if last_desired is not None and spec != last_desired:
                out(f"{BOLD}  ⚡ fleet knocked from {last_desired} to {spec} pods by hand"
                    f" · traffic unchanged{RESET}")
            last_desired = desired

            act.cycles += 1
            act.ready_sum += ready
            if desired != spec:
                act.moves.append(desired - spec)

            k6_now = k6_totals()
            failed = "     –"
            if k6_now and k6_prev and k6_now[0] > k6_prev[0]:
                d_req, d_fail = k6_now[0] - k6_prev[0], max(0.0, k6_now[1] - k6_prev[1])
                act.reqs += d_req
                act.failed += d_fail
                pct = 100 * d_fail / d_req
                failed = f"{RED if pct >= 1 else ''}{pct:5.1f}%{RESET}"
            k6_prev = k6_now

            arrow = (f"{GREEN}▲{RESET}" if desired > spec else
                     f"{RED}▼{RESET}" if desired < spec else f"{DIM}·{RESET}")
            unit = "/pod " if act.broken else "total"
            held = f"  {AMBER}held · raw {raw}{RESET}" if raw != desired else ""
            # The reconcile's own time, not the time this poll saw it: just after a
            # restart the Service answers a few seconds late, and wall-clock stamps
            # would bunch the first cycles together as if the interval had shrunk.
            stamp = time.strftime("%H:%M:%S", time.localtime(success))
            out(f"  {stamp}  {arrow}  {ready:>2} → {desired:<2}       "
                f"{bar(ready, desired, raw, args.width, act.colour)}   "
                f"{metric:6.1f} req/s   {predicted:6.0f} {unit}   {failed}{held}")

        time.sleep(1)


if __name__ == "__main__":
    sys.exit(main())
