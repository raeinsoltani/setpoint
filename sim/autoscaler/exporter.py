"""Minimal Prometheus-format metrics exporter for the autoscaler itself.

Exposes the autoscaler's internal state (current/desired replicas, observed
metric, target) on an HTTP endpoint so it can be scraped by Prometheus and
visualised in Grafana. Uses only the standard library.
"""

from __future__ import annotations

import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Dict


class MetricsExporter:
    def __init__(self, port: int = 8000):
        self.port = port
        self._values: Dict[str, float] = {
            "autoscaler_current_replicas": 0.0,
            "autoscaler_desired_replicas": 0.0,
            "autoscaler_metric_value": 0.0,
            "autoscaler_metric_target": 0.0,
            "autoscaler_predicted_value": 0.0,
        }
        self._lock = threading.Lock()
        self._server: HTTPServer | None = None
        self._thread: threading.Thread | None = None

    def set(self, **kwargs: float) -> None:
        with self._lock:
            for key, value in kwargs.items():
                if value is not None:
                    self._values[key] = float(value)

    def _render(self) -> bytes:
        with self._lock:
            lines = []
            for key, value in self._values.items():
                lines.append(f"# TYPE {key} gauge")
                lines.append(f"{key} {value}")
        return ("\n".join(lines) + "\n").encode("utf-8")

    def start(self) -> None:
        exporter = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):  # noqa: N802
                if self.path.rstrip("/") in ("", "/metrics"):
                    body = exporter._render()
                    self.send_response(200)
                    self.send_header("Content-Type", "text/plain; version=0.0.4")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                else:
                    self.send_response(404)
                    self.end_headers()

            def log_message(self, *args):  # silence request logging
                pass

        self._server = HTTPServer(("0.0.0.0", self.port), Handler)
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    def stop(self) -> None:
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
