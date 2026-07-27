"""A Prometheus-driven custom autoscaler (prototype).

Public API re-exported for convenience.
"""

from .controller import Controller
from .exporter import MetricsExporter
from .metrics import Collector, PrometheusCollector, StaticCollector
from .policy import Forecaster, Policy, ScalingDecision, Stabilizer, hpa_desired
from .scaler import InMemoryScaler, KubernetesScaler, Scaler

__all__ = [
    "Controller",
    "MetricsExporter",
    "Collector",
    "PrometheusCollector",
    "StaticCollector",
    "Policy",
    "Forecaster",
    "Stabilizer",
    "ScalingDecision",
    "hpa_desired",
    "Scaler",
    "InMemoryScaler",
    "KubernetesScaler",
]

__version__ = "0.1.0"
