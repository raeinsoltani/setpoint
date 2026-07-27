"""Build a Controller from a YAML/dict configuration."""

from __future__ import annotations

from typing import Any, Dict

from .controller import Controller
from .exporter import MetricsExporter
from .metrics import PrometheusCollector, StaticCollector
from .policy import Forecaster, Policy, Stabilizer
from .scaler import InMemoryScaler, KubernetesScaler


def build_policy(cfg: Dict[str, Any]) -> Policy:
    predictive = bool(cfg.get("predictive", False))
    forecaster = None
    if predictive:
        f = cfg.get("forecaster", {})
        forecaster = Forecaster(horizon=f.get("horizon", 3), alpha=f.get("alpha", 0.5))

    stabilizer = None
    if "stabilization_window_seconds" in cfg:
        stabilizer = Stabilizer(window_seconds=float(cfg["stabilization_window_seconds"]))

    return Policy(
        target=float(cfg["target"]),
        tolerance=float(cfg.get("tolerance", 0.1)),
        min_replicas=int(cfg.get("min_replicas", 1)),
        max_replicas=int(cfg.get("max_replicas", 10)),
        predictive=predictive,
        forecaster=forecaster,
        stabilizer=stabilizer,
        name=cfg.get("name"),
    )


def build_collector(cfg: Dict[str, Any]):
    kind = cfg.get("type", "prometheus")
    if kind == "prometheus":
        return PrometheusCollector(
            query=cfg["query"],
            base_url=cfg.get("url", "http://localhost:9090"),
        )
    if kind == "static":
        return StaticCollector(value=float(cfg.get("value", 0.0)))
    raise ValueError(f"unknown collector type: {kind}")


def build_scaler(cfg: Dict[str, Any]):
    kind = cfg.get("type", "kubernetes")
    if kind == "kubernetes":
        return KubernetesScaler(
            name=cfg["deployment"],
            namespace=cfg.get("namespace", "default"),
            kubeconfig=cfg.get("kubeconfig"),
        )
    if kind == "inmemory":
        return InMemoryScaler(replicas=int(cfg.get("replicas", 1)))
    raise ValueError(f"unknown scaler type: {kind}")


def build_controller(cfg: Dict[str, Any]) -> Controller:
    collector = build_collector(cfg["collector"])
    policy = build_policy(cfg["policy"])
    scaler = build_scaler(cfg["scaler"])
    exporter = None
    if cfg.get("exporter", {}).get("enabled"):
        exporter = MetricsExporter(port=int(cfg["exporter"].get("port", 8000)))
        exporter.start()
    return Controller(
        collector=collector,
        policy=policy,
        scaler=scaler,
        interval=float(cfg.get("interval_seconds", 15)),
        exporter=exporter,
    )


def load_config(path: str) -> Dict[str, Any]:
    import yaml  # imported here so the package works without PyYAML for tests

    with open(path, "r", encoding="utf-8") as fh:
        return yaml.safe_load(fh)
