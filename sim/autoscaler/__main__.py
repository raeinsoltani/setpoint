"""Entry point: run the autoscaler against a real (or in-memory) target.

Usage:
    python -m autoscaler --config config.yaml
    python -m autoscaler --smoke        # in-memory smoke run, no cluster needed
"""

from __future__ import annotations

import argparse
import logging

from .config import build_controller, load_config


def smoke_config() -> dict:
    """A self-contained config that needs no Prometheus or cluster."""
    return {
        "interval_seconds": 0.2,
        "collector": {"type": "static", "value": 250.0},
        "scaler": {"type": "inmemory", "replicas": 1},
        "policy": {
            "name": "threshold",
            "target": 100.0,
            "tolerance": 0.1,
            "min_replicas": 1,
            "max_replicas": 10,
            "stabilization_window_seconds": 1.0,
        },
    }


def main() -> None:
    parser = argparse.ArgumentParser(description="Prometheus-driven custom autoscaler")
    parser.add_argument("--config", help="path to YAML config")
    parser.add_argument("--smoke", action="store_true", help="run a short in-memory smoke test")
    parser.add_argument("--iterations", type=int, default=None, help="stop after N iterations")
    parser.add_argument("-v", "--verbose", action="store_true")
    args = parser.parse_args()

    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )

    if args.smoke:
        cfg = smoke_config()
        controller = build_controller(cfg)
        controller.run(iterations=args.iterations or 10)
        print("final replicas:", controller.scaler.get_replicas())
        return

    if not args.config:
        parser.error("either --config or --smoke is required")

    cfg = load_config(args.config)
    controller = build_controller(cfg)
    controller.run(iterations=args.iterations)


if __name__ == "__main__":
    main()
