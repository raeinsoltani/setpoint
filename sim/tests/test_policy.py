"""Unit tests for the policy engine — the core logic defended in the project."""

import os
import sys

import pytest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from autoscaler.policy import Forecaster, Policy, Stabilizer, hpa_desired  # noqa: E402


# --------------------------- hpa_desired ---------------------------------- #
def test_hpa_scale_up_doubles_when_load_doubles():
    # ratio = 200/100 = 2 -> ceil(2 * 2) = 4
    assert hpa_desired(2, 200, 100, tolerance=0.1) == 4


def test_hpa_scale_down():
    # ratio = 50/100 = 0.5 -> ceil(4 * 0.5) = 2
    assert hpa_desired(4, 50, 100, tolerance=0.1) == 2


def test_hpa_within_tolerance_holds():
    # ratio = 1.05 is inside the 10% band -> no change
    assert hpa_desired(3, 105, 100, tolerance=0.1) == 3


def test_hpa_clamps_to_max():
    assert hpa_desired(1, 5000, 100, tolerance=0.1, max_replicas=5) == 5


def test_hpa_clamps_to_min():
    assert hpa_desired(10, 0.0, 100, tolerance=0.1, min_replicas=2) == 2


def test_hpa_rejects_bad_target():
    with pytest.raises(ValueError):
        hpa_desired(1, 100, 0)


# --------------------------- Forecaster ----------------------------------- #
def test_forecaster_extrapolates_rising_trend():
    f = Forecaster(horizon=3, alpha=0.5)
    last = 0.0
    for v in [100, 200, 300, 400, 500]:
        last = f.update(v)
    # rising series -> forecast should exceed the most recent observation
    assert last > 500


def test_forecaster_first_value_is_identity():
    f = Forecaster()
    assert f.update(42.0) == pytest.approx(42.0)


# --------------------------- Stabilizer ----------------------------------- #
def test_stabilizer_scales_up_immediately():
    s = Stabilizer(window_seconds=60)
    desired, reason = s.stabilize(current_replicas=2, recommendation=5, now=0)
    assert desired == 5
    assert "scale-up" in reason


def test_stabilizer_delays_scale_down_using_window_max():
    s = Stabilizer(window_seconds=60)
    # a brief high recommendation stays "remembered" and blocks premature downscale
    s.stabilize(current_replicas=5, recommendation=5, now=0)
    desired, reason = s.stabilize(current_replicas=5, recommendation=2, now=30)
    assert desired == 5  # held because 5 is still within the window
    # after the window passes, downscale is allowed
    desired, reason = s.stabilize(current_replicas=5, recommendation=2, now=200)
    assert desired == 2
    assert "scale-down" in reason


# --------------------------- Policy end-to-end ---------------------------- #
def test_policy_threshold_scale_up():
    p = Policy(target=100, tolerance=0.1, min_replicas=1, max_replicas=10)
    d = p.decide(current_replicas=2, metric_value=300, now=0)
    assert d.desired_replicas == 6  # ceil(2 * 3)
    assert d.predicted_value is None


def test_policy_predictive_reacts_earlier_than_threshold():
    thr = Policy(target=100, max_replicas=50, predictive=False)
    pred = Policy(target=100, max_replicas=50, predictive=True,
                  forecaster=Forecaster(horizon=3, alpha=0.5))
    # feed an accelerating series and compare the final recommendation
    series = [100, 140, 200, 280]
    dt = dp = None
    for t, v in enumerate(series):
        dt = thr.decide(current_replicas=3, metric_value=v, now=t)
        dp = pred.decide(current_replicas=3, metric_value=v, now=t)
    assert dp.desired_replicas >= dt.desired_replicas
    assert dp.predicted_value is not None


def test_policy_respects_min_replicas():
    p = Policy(target=100, min_replicas=2, max_replicas=10)
    d = p.decide(current_replicas=5, metric_value=0.0, now=0)
    assert d.desired_replicas >= 2
