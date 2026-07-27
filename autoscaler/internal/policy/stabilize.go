package policy

import "time"

// Stabilizer damps oscillation in the raw recommendation stream.
//
// Scale-up is applied immediately, because being slow to add capacity costs
// latency. Scale-down uses the *maximum* recommendation seen inside the window,
// so a brief dip in load cannot shrink the fleet — this mirrors Kubernetes' own
// downscale stabilization and is the anti-flapping mechanism the proposal names
// as a non-functional requirement.
//
// Ported from sim/autoscaler/policy.py:95-120, plus the cooldown the prototype
// lacks (the proposal calls it «شیب‌گیری (Cooldown)» on page 3).
type Stabilizer struct {
	window   time.Duration
	cooldown time.Duration
	maxStep  int32

	recs        []timedRec
	lastScaleAt time.Time
	hasScaled   bool
}

type timedRec struct {
	at  time.Time
	rec int32
}

// StabilizerOptions configures a Stabilizer.
type StabilizerOptions struct {
	// Window is the downscale stabilization window.
	Window time.Duration
	// Cooldown is the minimum interval between two scale actions. Zero disables it.
	Cooldown time.Duration
	// MaxStep caps how many replicas a single decision may add or remove.
	// Zero means unlimited.
	MaxStep int32
}

// NewStabilizer returns a Stabilizer with the given options.
func NewStabilizer(opts StabilizerOptions) *Stabilizer {
	return &Stabilizer{window: opts.Window, cooldown: opts.Cooldown, maxStep: opts.MaxStep}
}

// Stabilize converts a raw recommendation into the replica count to apply, and
// explains why. current must be the ready replica count.
func (s *Stabilizer) Stabilize(current, recommendation int32, now time.Time) (int32, string) {
	s.recs = append(s.recs, timedRec{at: now, rec: recommendation})
	cutoff := now.Add(-s.window)
	keep := s.recs[:0]
	for _, r := range s.recs {
		if !r.at.Before(cutoff) {
			keep = append(keep, r)
		}
	}
	s.recs = keep

	target, reason := s.direction(current, recommendation)
	if target == current {
		return current, reason
	}

	if s.maxStep > 0 {
		if delta := target - current; delta > s.maxStep {
			target, reason = current+s.maxStep, reason+" (rate-limited)"
		} else if -delta > s.maxStep {
			target, reason = current-s.maxStep, reason+" (rate-limited)"
		}
	}

	if s.cooldown > 0 && s.hasScaled && now.Sub(s.lastScaleAt) < s.cooldown {
		return current, "hold (cooldown)"
	}

	s.lastScaleAt, s.hasScaled = now, true
	return target, reason
}

// direction decides which way to move before rate limiting and cooldown apply.
func (s *Stabilizer) direction(current, recommendation int32) (int32, string) {
	if recommendation > current {
		return recommendation, "scale-up (immediate)"
	}
	windowMax := recommendation
	for _, r := range s.recs {
		if r.rec > windowMax {
			windowMax = r.rec
		}
	}
	if windowMax < current {
		return windowMax, "scale-down (stabilized)"
	}
	return current, "hold (stabilization window)"
}
