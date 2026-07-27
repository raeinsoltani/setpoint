"""The control loop that ties collector + policy + scaler + exporter together."""

from __future__ import annotations

import logging
import time
from typing import Optional

from .exporter import MetricsExporter
from .metrics import Collector
from .policy import Policy, ScalingDecision
from .scaler import Scaler

logger = logging.getLogger("autoscaler")


class Controller:
    def __init__(
        self,
        collector: Collector,
        policy: Policy,
        scaler: Scaler,
        interval: float = 15.0,
        exporter: Optional[MetricsExporter] = None,
    ):
        self.collector = collector
        self.policy = policy
        self.scaler = scaler
        self.interval = interval
        self.exporter = exporter

    def reconcile(self, now: Optional[float] = None) -> ScalingDecision:
        """Run one iteration: read metric, decide, apply."""
        now = time.monotonic() if now is None else now
        metric_value = self.collector.read()
        current = self.scaler.get_replicas()
        decision = self.policy.decide(current, metric_value, now=now)

        if decision.desired_replicas != current:
            self.scaler.set_replicas(decision.desired_replicas)
            logger.info(
                "scale %s: %d -> %d (metric=%.2f target=%.2f) [%s]",
                self.policy.name,
                current,
                decision.desired_replicas,
                metric_value,
                self.policy.target,
                decision.reason,
            )
        else:
            logger.debug(
                "hold %s: %d (metric=%.2f) [%s]",
                self.policy.name,
                current,
                metric_value,
                decision.reason,
            )

        if self.exporter is not None:
            self.exporter.set(
                autoscaler_current_replicas=current,
                autoscaler_desired_replicas=decision.desired_replicas,
                autoscaler_metric_value=metric_value,
                autoscaler_metric_target=self.policy.target,
                autoscaler_predicted_value=decision.predicted_value or 0.0,
            )

        return decision

    def run(self, iterations: Optional[int] = None) -> None:
        """Run the control loop forever, or for a fixed number of iterations."""
        count = 0
        while iterations is None or count < iterations:
            start = time.monotonic()
            try:
                self.reconcile()
            except Exception:  # keep the loop alive on transient errors
                logger.exception("reconcile failed")
            count += 1
            if iterations is not None and count >= iterations:
                break
            elapsed = time.monotonic() - start
            time.sleep(max(0.0, self.interval - elapsed))
