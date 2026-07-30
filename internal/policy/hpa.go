package policy

import (
	"errors"
	"math"
)

// ErrInvalidTarget is returned when a target value is not usable as a divisor.
var ErrInvalidTarget = errors.New("policy: target must be > 0 and finite")

// DesiredReplicas returns the replica count Kubernetes' HPA would choose:
//
//	desired = ceil(base * metric/target)
//
// unless metric/target is within tolerance of 1.0, in which case the count is
// held. The result is clamped to [minReplicas, maxReplicas].
//
// Matching HPA's formula exactly is deliberate: it makes our threshold policy a
// faithful stand-in for stock HPA, so the evaluation compares predictive-vs-reactive
// rather than accidentally comparing two different threshold implementations.
//
// base is the *ready* replica count, not the requested one — see scaler.Scale.
func DesiredReplicas(base int32, metric, target, tolerance float64, minReplicas, maxReplicas int32) (int32, error) {
	if target <= 0 || math.IsNaN(target) || math.IsInf(target, 0) {
		return 0, ErrInvalidTarget
	}
	if math.IsNaN(metric) || math.IsInf(metric, 0) {
		// A missing or broken metric must never move the fleet. Holding is the
		// safe action: Prometheus being briefly unavailable is not evidence of
		// a load change in either direction.
		return clamp(base, minReplicas, maxReplicas), nil
	}

	ratio := metric / target
	var desired int32
	if math.Abs(ratio-1.0) <= tolerance {
		desired = base
	} else {
		// Scale from at least one replica so a fleet that has scaled to zero
		// ready pods can still recover.
		b := base
		if b < 1 {
			b = 1
		}
		desired = ceilToReplicas(float64(b) * ratio)
	}

	return clamp(desired, minReplicas, maxReplicas), nil
}

// FleetFor returns the replicas needed to serve totalLoad at target load each:
//
//	ceil(totalLoad / target)
//
// This is the total-load form used by the predictive policy. Unlike the HPA ratio
// formula it does not depend on the current replica count, which is what makes it
// immune to the feedback loop documented in PredictiveTotalLoad.
func FleetFor(totalLoad, target float64, minReplicas, maxReplicas int32) (int32, error) {
	if target <= 0 || math.IsNaN(target) || math.IsInf(target, 0) {
		return 0, ErrInvalidTarget
	}
	if math.IsNaN(totalLoad) || math.IsInf(totalLoad, 0) || totalLoad < 0 {
		return clamp(minReplicas, minReplicas, maxReplicas), nil
	}
	return clamp(ceilToReplicas(totalLoad/target), minReplicas, maxReplicas), nil
}

// ceilToReplicas rounds a fractional replica demand up to a whole replica count,
// saturating rather than overflowing.
//
// The explicit range check is load-bearing. Go specifies that a float-to-int
// conversion whose result the target type cannot represent "succeeds but the result
// value is implementation-dependent" — so int32(math.Ceil(1e18)) may saturate to
// MaxInt32 on one architecture and wrap to a negative number on another. A negative
// value would then be read by clamp as "below min" and scale the fleet *down* under
// extreme load: the worst possible response, arrived at by accident, and only on
// some build targets. Saturating here makes the behaviour the same everywhere.
func ceilToReplicas(v float64) int32 {
	c := math.Ceil(v)
	switch {
	case c >= math.MaxInt32:
		return math.MaxInt32
	case c <= math.MinInt32:
		return math.MinInt32
	default:
		return int32(c)
	}
}

func clamp(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
