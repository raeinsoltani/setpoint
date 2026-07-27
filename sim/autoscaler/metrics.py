"""Metric collectors.

A collector returns a single float: the current value of the scaling signal
(for example requests-per-second per ready replica, or average latency).

Two implementations are provided:

* ``PrometheusCollector`` -- queries a real Prometheus server over its HTTP API
  using a PromQL expression (uses only the standard library).
* ``StaticCollector`` -- returns a value held in memory; used by tests and by
  the closed-loop simulator, where the metric is produced by the simulated
  world rather than scraped.
"""

from __future__ import annotations

import json
import urllib.parse
import urllib.request
from typing import Optional


class Collector:
    """Interface for metric collectors."""

    def read(self) -> float:  # pragma: no cover - interface
        raise NotImplementedError


class StaticCollector(Collector):
    """Returns a value that can be updated externally (tests / simulation)."""

    def __init__(self, value: float = 0.0):
        self.value = value

    def set(self, value: float) -> None:
        self.value = value

    def read(self) -> float:
        return float(self.value)


class PrometheusCollector(Collector):
    """Reads a scalar scaling signal from Prometheus via PromQL.

    The query is expected to evaluate to a single scalar or a one-element
    vector, e.g.::

        sum(rate(http_requests_total{app="myapp"}[1m]))
          / count(up{app="myapp"} == 1)
    """

    def __init__(self, query: str, base_url: str = "http://localhost:9090", timeout: float = 5.0):
        self.query = query
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout

    def read(self) -> float:
        url = f"{self.base_url}/api/v1/query?" + urllib.parse.urlencode({"query": self.query})
        with urllib.request.urlopen(url, timeout=self.timeout) as resp:
            payload = json.loads(resp.read().decode("utf-8"))

        if payload.get("status") != "success":
            raise RuntimeError(f"Prometheus query failed: {payload}")

        data = payload["data"]
        result_type = data["resultType"]
        result = data["result"]

        if result_type == "scalar":
            return float(result[1])
        if result_type == "vector":
            if not result:
                return 0.0
            return float(result[0]["value"][1])
        raise RuntimeError(f"Unsupported Prometheus resultType: {result_type}")
