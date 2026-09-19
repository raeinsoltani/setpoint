#!/usr/bin/env python3
"""Persian figures for the thesis, drawn from the runs its tables cite.

analyze.py's figures belong to the repository: English, and all nine arms on one axis,
which on `ramp` lets the collapsed arm bury the other eight. The thesis needs different
ones:

  comparison-<pattern>.pdf   the six arms of the headline table, one figure per workload
  ablation-spike.pdf         the stabilizer ablation as its 2x2 design: signal (total vs
                             per-replica) by stabilizer (on vs off), on the workload the
                             chapter's central claim is measured on

They are also pinned. analyze.py keeps the *latest* run per (pattern, arm), so a rerun
silently changes its figures, and a thesis whose figures come from different runs than
its tables is wrong in a way no reader can detect. Here the cutoff is an argument, and
moving it is a deliberate act that should move the tables with it.

    sim/.venv/bin/python experiments/thesis_figures.py [--as-of 20260814T000000Z]

Needs the B Nazanin font installed, which the thesis itself needs to compile.
"""
from __future__ import annotations

import argparse
import os
import sys
from typing import Dict, List, Sequence, Tuple

import numpy as np
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
from matplotlib import font_manager  # noqa: E402
from matplotlib.legend_handler import HandlerTuple  # noqa: E402
from matplotlib.lines import Line2D  # noqa: E402
from matplotlib.ticker import MaxNLocator  # noqa: E402

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from analyze import (  # noqa: E402
    ARM_COLOR,
    DURATION,
    REPO,
    SLA_LIMIT,
    TARGET,
    Run,
    compute,
    hold,
    load_runs,
    pattern_load,
    required_replicas,
    series_arrays,
)

# The runs Tables 5.1-5.6 were computed from. Everything after this was recorded once
# the chapter was written, and belongs in the figures only when it is in the tables too.
DEFAULT_AS_OF = "20260814T000000Z"

HEADLINE_ARMS = ["static-peak", "static", "hpa-cpu", "hpa-custom", "ours-threshold",
                 "ours-predictive"]

PATTERN_FA = {"diurnal": "روزانه", "ramp": "شیب", "spike": "جهشی", "bursty": "انفجاری"}

# Matplotlib 3.11 shapes and orders Arabic-script text itself (through libraqm), so plain
# Unicode strings are correct. Pre-shaping them with arabic_reshaper + python-bidi, the
# usual recipe for older versions, reverses them a second time.
PERSIAN_DIGITS = str.maketrans("0123456789.", "۰۱۲۳۴۵۶۷۸۹/")

# Text width of the thesis page. Drawing at print size, not scaling a 10-inch figure down,
# is what keeps the labels the size of the body text.
WIDTH_IN = 6.3


def fa_num(x: float, nd: int = 0) -> str:
    """A number as the body text writes it: Persian digits, '/' as the decimal mark."""
    return f"{x:.{nd}f}".translate(PERSIAN_DIGITS)


def fa_tick(v: float, _pos=None) -> str:
    # Never round a tick: a locator that picks 2.5-steps would otherwise print 2, 5, 8.
    return f"{v:g}".translate(PERSIAN_DIGITS)


def use_thesis_font() -> None:
    for path in font_manager.findSystemFonts():
        if "nazanin" in os.path.basename(path).lower():
            font_manager.fontManager.addfont(path)
    names = {f.name for f in font_manager.fontManager.ttflist}
    if "B Nazanin" not in names:
        sys.exit("B Nazanin is not installed; the thesis needs it too (commands.tex)")
    # B Nazanin has no Latin glyphs. Times New Roman is the thesis's Latin face, so an arm
    # name in a legend looks the way \arm{} looks in the text beside it.
    plt.rcParams.update({
        "font.family": ["B Nazanin", "Times New Roman"],
        "font.size": 11,
        "axes.titlesize": 11,
        "legend.fontsize": 10,
        "pdf.fonttype": 42,
    })


def select(runs: Sequence[Run], as_of: str) -> Dict[Tuple[str, str], Run]:
    """Latest valid, full-length run per (pattern, arm) recorded before `as_of`."""
    chosen: Dict[Tuple[str, str], Run] = {}
    for r in runs:
        ts = r.meta.get("timestamp", "")
        if not r.valid or r.meta.get("smoke") or ts >= as_of:
            continue
        key = (r.pattern, r.arm)
        if key not in chosen or ts > chosen[key].meta.get("timestamp", ""):
            chosen[key] = r
    return chosen


def time_axis(run: Run) -> Tuple[np.ndarray, np.ndarray, float, float]:
    """The measurement grid in pattern seconds, plus the warmup and settle extents."""
    w = run.meta["window"]
    t0, t1 = float(w["measure_start"]), float(w["measure_end"])
    step = float(str(w.get("step", "5s")).rstrip("s"))
    grid = np.arange(t0, t1 + step, step)
    return grid, grid - t0, float(w.get("warmup_seconds", 120)), float(w.get("settle_seconds", 120))


def shade_outside_window(ax, warmup: float, settle: float) -> None:
    # Every number in the tables comes from the unshaded region only.
    ax.axvspan(-warmup, 0, color="#000000", alpha=0.06, lw=0)
    ax.axvspan(DURATION, DURATION + settle, color="#000000", alpha=0.06, lw=0)
    ax.set_xlim(-warmup, DURATION + settle)
    ax.set_xticks(np.arange(0, DURATION + 1, 300))
    ax.xaxis.set_major_formatter(fa_tick)
    ax.yaxis.set_major_formatter(fa_tick)
    ax.grid(alpha=0.3)


def rtl_legend(fig, handles: List, labels: List[str], ncol: int, **kw) -> None:
    """A figure legend that reads right to left, like the page around it.

    Matplotlib fills legend columns left to right, so the first entry lands at the
    left edge. Padding to a full grid and reversing the column order puts it at the
    right, where a Persian reader starts.
    """
    per_col = -(-len(handles) // ncol)
    pad = per_col * ncol - len(handles)
    handles = list(handles) + [Line2D([], [], alpha=0)] * pad
    labels = list(labels) + [""] * pad
    cols = [list(range(c * per_col, (c + 1) * per_col)) for c in range(ncol)]
    order = [i for col in reversed(cols) for i in col]
    fig.legend([handles[i] for i in order], [labels[i] for i in order], ncol=ncol,
               markerfirst=False, frameon=False, **kw)


def plot_comparison(pattern: str, chosen: Dict[Tuple[str, str], Run], path: str) -> None:
    runs = [chosen[(pattern, a)] for a in HEADLINE_ARMS if (pattern, a) in chosen]
    fig, (ax1, ax2, ax3) = plt.subplots(3, 1, figsize=(WIDTH_IN, 7.4), sharex=True,
                                        gridspec_kw={"height_ratios": [1, 1.25, 1.25]})
    handles, labels = [], []

    x_full = np.arange(0, DURATION, 5.0)
    h, = ax1.plot(x_full, pattern_load(pattern, x_full), color="#222", lw=1.5)
    handles.append(h); labels.append("بار عرضه‌شده (الگو)")
    # The measured curve is taken from static-peak: its fleet is never short of capacity,
    # so what Prometheus sees there is the load delivered, not the load a starved fleet
    # managed to serve.
    ref = chosen.get((pattern, "static-peak"), runs[0])
    grid, x, warmup, settle = time_axis(ref)
    td, tv = series_arrays(ref, "total_rps")
    h, = ax1.plot(td - grid[0], tv, color="#999", lw=1.0, ls="--")
    handles.append(h); labels.append("بار اندازه‌گیری‌شده")
    ax1.set_ylabel("بار کل\n(درخواست بر ثانیه)")

    for run in runs:
        grid, x, _, _ = time_axis(run)
        color = ARM_COLOR[run.arm]
        h, = ax2.step(x, hold(*series_arrays(run, "ready_replicas"), grid), where="post",
                      color=color, lw=1.4)
        ax3.plot(x, hold(*series_arrays(run, "per_replica_rps"), grid), color=color, lw=1.1)
        handles.append(h); labels.append(run.arm)

    h, = ax2.plot(x_full, required_replicas(pattern, x_full), color="#000", lw=1.0, ls=":")
    handles.append(h); labels.append("نیاز مرجع")
    ax2.set_ylabel("نمونه‌های\nآماده")
    ax2.yaxis.set_major_locator(MaxNLocator(integer=True, steps=[1, 2, 5, 10]))

    h = ax3.axhline(TARGET, color="green", ls="--", lw=1)
    handles.append(h); labels.append("هدف")
    h = ax3.axhline(SLA_LIMIT, color="red", ls=":", lw=1.2)
    handles.append(h); labels.append("حد کیفیت خدمت")
    ax3.set_ylabel("بار هر نمونه\n(درخواست بر ثانیه)")
    ax3.set_ylim(bottom=0)
    ax3.set_xlabel("زمان (ثانیه)")

    for ax in (ax1, ax2, ax3):
        shade_outside_window(ax, warmup, settle)

    rtl_legend(fig, handles, labels, ncol=4, loc="upper center", bbox_to_anchor=(0.5, 1.0))
    fig.tight_layout(rect=(0, 0, 1, 0.9))
    fig.savefig(path)
    plt.close(fig)


# The ablation as its design: rows are the forecast signal, columns the stabilizer.
# The stabilized column is on the right because a Persian reader starts there, and the
# stabilized arms are the baseline the unstabilized ones are read against.
ABLATION = [
    ("پیش‌بینی بار کل", "ours-predictive-nostab", "ours-predictive"),
    ("پیش‌بینی بار هر نمونه", "ours-predictive-per-replica-nostab", "ours-predictive-per-replica"),
]


def plot_ablation(pattern: str, chosen: Dict[Tuple[str, str], Run], path: str) -> None:
    fig, axs = plt.subplots(2, 2, figsize=(WIDTH_IN, 5.6), sharex=True, sharey=True)
    x_full = np.arange(0, DURATION, 5.0)
    needed = required_replicas(pattern, x_full)
    warmup = settle = 120.0

    for r, (row_label, arm_off, arm_on) in enumerate(ABLATION):
        for c, arm in ((0, arm_off), (1, arm_on)):
            ax = axs[r][c]
            run = chosen[(pattern, arm)]
            grid, x, warmup, settle = time_axis(run)
            color = ARM_COLOR[arm]
            # spec.replicas is what the controller asked for; ready is what it got. On
            # the broken arm the gap between them is the whole result: it asks for 11
            # and is back down to 1 before any new pod can finish starting.
            ax.step(x, hold(*series_arrays(run, "spec_replicas"), grid), where="post",
                    color="#555", lw=0.6, alpha=0.8)
            ax.step(x, hold(*series_arrays(run, "ready_replicas"), grid), where="post",
                    color=color, lw=1.6)
            ax.plot(x_full, needed, color="#000", lw=1.0, ls=":")
            m = compute(run)
            head = ("با پنجره‌ی پایدارسازی ۹۰ ثانیه‌ای", "بدون میراگر")[c == 0]
            ax.set_title(f"{head}\n{arm}" if r == 0 else arm)
            ax.text(0.97, 0.95,
                    f"نقض کیفیت خدمت {fa_num(m.sla_violation_pct, 1)}٪\n"
                    f"تغییر جهت {fa_num(m.reversals)}",
                    transform=ax.transAxes, ha="right", va="top", fontsize=10,
                    bbox=dict(facecolor="white", edgecolor="#bbb", lw=0.5, alpha=0.9))
            shade_outside_window(ax, warmup, settle)
            if r == 1:
                ax.set_xlabel("زمان (ثانیه)")
        axs[r][0].set_ylabel("تعداد نمونه")
        axs[r][1].yaxis.set_label_position("right")
        axs[r][1].set_ylabel(row_label)

    axs[0][0].set_ylim(0, 22)
    axs[0][0].yaxis.set_major_locator(MaxNLocator(integer=True, steps=[1, 2, 5, 10]))

    # Ready is drawn in each arm's own colour, so its legend key shows all four.
    ready_key = tuple(Line2D([], [], color=ARM_COLOR[a], lw=1.6)
                      for _, off, on in ABLATION for a in (on, off))
    handles = [Line2D([], [], color="#555", lw=0.8), ready_key,
               Line2D([], [], color="#000", lw=1.0, ls=":")]
    labels = ["تعداد درخواستی کنترل‌گر", "نمونه‌های آماده", "نیاز مرجع"]
    rtl_legend(fig, handles, labels, ncol=3, loc="upper center", bbox_to_anchor=(0.5, 1.0),
               handler_map={tuple: HandlerTuple(ndivide=None, pad=0)}, handlelength=4)
    fig.tight_layout(rect=(0, 0, 1, 0.93))
    fig.savefig(path)
    plt.close(fig)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--raw", default=os.path.join(REPO, "experiments", "results", "raw"))
    ap.add_argument("--out", default=os.path.join(REPO, "docs", "thesis", "latex", "figures"))
    ap.add_argument("--as-of", default=DEFAULT_AS_OF,
                    help="ignore runs recorded at or after this timestamp (default: %(default)s)")
    args = ap.parse_args()

    use_thesis_font()
    chosen = select(load_runs(args.raw), args.as_of)
    os.makedirs(args.out, exist_ok=True)

    for (pattern, arm), run in sorted(chosen.items()):
        print(f"  {pattern:8s} {arm:36s} {run.meta.get('timestamp')}")

    for pattern in PATTERN_FA:
        path = os.path.join(args.out, f"comparison-{pattern}.pdf")
        plot_comparison(pattern, chosen, path)
        print(f"wrote {os.path.relpath(path, REPO)}")
    path = os.path.join(args.out, "ablation-spike.pdf")
    plot_ablation("spike", chosen, path)
    print(f"wrote {os.path.relpath(path, REPO)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
