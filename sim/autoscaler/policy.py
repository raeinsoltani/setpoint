"""Scaling policy engine.

This is the heart of the autoscaler and the component that is unit-tested and
defended in the project defense. It contains:

* ``hpa_desired`` -- the core Kubernetes-HPA replica formula with a tolerance
  band, so our threshold policy is faithful to real HPA behaviour.
* ``Forecaster`` -- a lightweight EWMA + linear-trend one-step-ahead predictor
  used by the predictive policy to scale *before* a spike arrives.
* ``Stabilizer`` -- a stabilization window that reacts immediately on scale-up
  but is conservative on scale-down (mirrors HPA's downscale-stabilization),
  which prevents flapping/oscillation.
* ``Policy`` -- ties the above together and produces a ``ScalingDecision``.
"""

from __future__ import annotations

import math
from collections import deque
from dataclasses import dataclass
from typing import Deque, Optional, Tuple


@dataclass
class ScalingDecision:
    """Result of a single control-loop evaluation."""

    desired_replicas: int
    raw_recommendation: int
    current_replicas: int
    metric_value: float
    target: float
    reason: str
    predicted_value: Optional[float] = None


def hpa_desired(
    current_replicas: int,
    metric_value: float,
    target: float,
    tolerance: float = 0.1,
    min_replicas: int = 1,
    max_replicas: int = 100,
) -> int:
    """Return the desired replica count using the Kubernetes HPA formula.

    ``desired = ceil(current * (metric / target))`` unless the ratio is within
    ``tolerance`` of 1.0, in which case no change is made. The result is clamped
    to ``[min_replicas, max_replicas]``.
    """
    if target <= 0:
        raise ValueError("target must be > 0")
    if current_replicas < 0:
        raise ValueError("current_replicas must be >= 0")

    ratio = metric_value / target
    if abs(ratio - 1.0) <= tolerance:
        desired = current_replicas
    else:
        # scale from at least 1 running replica to avoid divide-by-zero effects
        base = max(current_replicas, 1)
        desired = math.ceil(base * ratio)

    return max(min_replicas, min(max_replicas, desired))


class Forecaster:
    """EWMA smoother plus a linear-trend extrapolation.

    ``update`` feeds one observation and returns the value predicted
    ``horizon`` steps ahead. It is deliberately simple and cheap so it can run
    every control interval; it can later be swapped for ARIMA/LSTM without
    touching the rest of the code.
    """

    def __init__(self, horizon: int = 3, alpha: float = 0.5):
        if not 0.0 < alpha <= 1.0:
            raise ValueError("alpha must be in (0, 1]")
        self.horizon = horizon
        self.alpha = alpha
        self._ewma: Optional[float] = None
        self._prev_ewma: Optional[float] = None

    def update(self, value: float) -> float:
        if self._ewma is None:
            self._ewma = value
            self._prev_ewma = value
        else:
            self._prev_ewma = self._ewma
            self._ewma = self.alpha * value + (1.0 - self.alpha) * self._ewma
        trend = self._ewma - (self._prev_ewma if self._prev_ewma is not None else self._ewma)
        return max(0.0, self._ewma + trend * self.horizon)


class Stabilizer:
    """Anti-flapping stabilization window.

    Keeps the recent stream of raw recommendations. Scale-up is applied
    immediately (fast reaction), while scale-down uses the *maximum*
    recommendation seen within ``window_seconds`` (conservative), which is
    exactly how Kubernetes' downscale stabilization avoids oscillation.
    """

    def __init__(self, window_seconds: float = 60.0):
        self.window_seconds = window_seconds
        self._recs: Deque[Tuple[float, int]] = deque()

    def stabilize(self, current_replicas: int, recommendation: int, now: float) -> Tuple[int, str]:
        self._recs.append((now, recommendation))
        while self._recs and self._recs[0][0] < now - self.window_seconds:
            self._recs.popleft()

        if recommendation > current_replicas:
            return recommendation, "scale-up (immediate)"

        # scale-down (or hold): take the most conservative recommendation in window
        windowed_max = max(r for _, r in self._recs)
        if windowed_max < current_replicas:
            return windowed_max, "scale-down (stabilized)"
        return current_replicas, "hold (stabilization window)"


class Policy:
    """Configurable scaling policy: threshold or predictive."""

    def __init__(
        self,
        target: float,
        tolerance: float = 0.1,
        min_replicas: int = 1,
        max_replicas: int = 10,
        predictive: bool = False,
        forecaster: Optional[Forecaster] = None,
        stabilizer: Optional[Stabilizer] = None,
        name: Optional[str] = None,
    ):
        self.target = target
        self.tolerance = tolerance
        self.min_replicas = min_replicas
        self.max_replicas = max_replicas
        self.predictive = predictive
        self.forecaster = forecaster or (Forecaster() if predictive else None)
        self.stabilizer = stabilizer
        self.name = name or ("predictive" if predictive else "threshold")

    def decide(self, current_replicas: int, metric_value: float, now: float = 0.0) -> ScalingDecision:
        predicted = None
        signal = metric_value
        if self.predictive and self.forecaster is not None:
            predicted = self.forecaster.update(metric_value)
            signal = predicted

        raw = hpa_desired(
            current_replicas,
            signal,
            self.target,
            self.tolerance,
            self.min_replicas,
            self.max_replicas,
        )

        if self.stabilizer is not None:
            desired, reason = self.stabilizer.stabilize(current_replicas, raw, now)
        else:
            desired, reason = raw, "no stabilizer"

        desired = max(self.min_replicas, min(self.max_replicas, desired))

        return ScalingDecision(
            desired_replicas=desired,
            raw_recommendation=raw,
            current_replicas=current_replicas,
            metric_value=metric_value,
            target=self.target,
            reason=reason,
            predicted_value=predicted,
        )
