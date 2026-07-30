package policy

import (
	"math"
	"testing"
)

// Both forecasters must satisfy the same contract, so the shared properties are
// asserted against the interface rather than against either implementation.
func forecasters() map[string]Forecaster {
	return map[string]Forecaster{
		"ewma": NewEWMATrend(3, 0.5),
		"holt": NewHolt(3, 0.5, 0.5),
	}
}

// With no history there is no trend to extrapolate, so the first prediction can
// only be the observation itself. This matters in practice: it is what the
// autoscaler does on its very first reconcile after a restart, and a forecaster
// that guessed here would move the fleet on no evidence.
func TestFirstObservationPredictsItself(t *testing.T) {
	for name, f := range forecasters() {
		t.Run(name, func(t *testing.T) {
			if got := f.Update(100); got != 100 {
				t.Errorf("first Update(100) = %v, want 100", got)
			}
		})
	}
}

func TestFlatSeriesPredictsTheSameValue(t *testing.T) {
	for name, f := range forecasters() {
		t.Run(name, func(t *testing.T) {
			var got float64
			for range 20 {
				got = f.Update(200)
			}
			if math.Abs(got-200) > 1e-6 {
				t.Errorf("after a flat series, prediction = %v, want 200", got)
			}
		})
	}
}

// The whole point of forecasting: on a rising series the prediction must lead the
// observation, or the predictive policy has no advantage over the reactive one.
func TestRisingTrendPredictsAboveTheLastObservation(t *testing.T) {
	for name, f := range forecasters() {
		t.Run(name, func(t *testing.T) {
			var got float64
			for v := 100.0; v <= 200; v += 20 {
				got = f.Update(v)
			}
			if got <= 200 {
				t.Errorf("prediction = %v, want > 200 (the last observation)", got)
			}
		})
	}
}

// A prediction is a load, and negative load is meaningless — it would flow into
// FleetFor and be silently floored there, hiding the nonsense. Every path out of
// Update must be non-negative, including the ones taken on bad input.
func TestPredictionIsNeverNegative(t *testing.T) {
	for name, f := range forecasters() {
		t.Run(name, func(t *testing.T) {
			// A steep collapse drives the trend estimate strongly negative, so
			// level + trend x horizon goes below zero.
			for _, v := range []float64{500, 400, 100, 10} {
				if got := f.Update(v); got < 0 {
					t.Fatalf("Update(%v) = %v, want >= 0", v, got)
				}
			}
			// Then a gap in the metric, which takes the early-return path.
			if got := f.Update(math.NaN()); got < 0 {
				t.Errorf("Update(NaN) after a collapse = %v, want >= 0", got)
			}
		})
	}
}

// A scrape gap must not be treated as a reading of zero: that would look like load
// vanishing and drag the forecast — and the fleet — down. The property asserted is
// the strong one: feeding unusable observations is a complete no-op on state, so a
// forecaster that saw a gap ends up indistinguishable from one that did not.
func TestUnusableObservationDoesNotDisturbState(t *testing.T) {
	clean, gapped := forecasters(), forecasters()

	for name := range clean {
		t.Run(name, func(t *testing.T) {
			c, g := clean[name], gapped[name]

			var cleanPred, gappedPred float64
			for _, v := range []float64{100, 120, 140, 160} {
				cleanPred = c.Update(v)

				gappedPred = g.Update(v)
				for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
					g.Update(bad)
				}
			}

			if math.Abs(cleanPred-gappedPred) > 1e-9 {
				t.Errorf("prediction with metric gaps = %v, without = %v; want identical",
					gappedPred, cleanPred)
			}
		})
	}
}

func TestResetClearsState(t *testing.T) {
	for name, f := range forecasters() {
		t.Run(name, func(t *testing.T) {
			for _, v := range []float64{100, 200, 300} {
				f.Update(v)
			}
			f.Reset()
			if got := f.Update(50); got != 50 {
				t.Errorf("first Update after Reset = %v, want 50 (seeding behaviour)", got)
			}
		})
	}
}

// Horizon is the lead time in control intervals. At horizon 0 the forecaster is
// asked for "now", which is the smoothed level with no extrapolation — this is the
// knob the stability sweep in Phase 7.1 turns, so its endpoint must be exact.
func TestZeroHorizonDoesNotExtrapolate(t *testing.T) {
	f := NewEWMATrend(0, 0.5)
	for _, v := range []float64{100, 200, 300} {
		f.Update(v)
	}
	// alpha=0.5 over 100, 200, 300 gives a level of 225.
	if got := f.Update(300); math.Abs(got-262.5) > 1e-9 {
		t.Errorf("horizon-0 prediction = %v, want the smoothed level 262.5", got)
	}
}

// Alpha controls how fast the level tracks recent observations. A high alpha must
// react faster than a low one, or the parameter is not doing what the thesis says.
func TestHigherAlphaTracksFaster(t *testing.T) {
	slow, fast := NewEWMATrend(0, 0.1), NewEWMATrend(0, 0.9)
	slow.Update(100)
	fast.Update(100)

	slowPred, fastPred := slow.Update(500), fast.Update(500)
	if fastPred <= slowPred {
		t.Errorf("alpha=0.9 predicted %v, alpha=0.1 predicted %v; want the faster alpha higher",
			fastPred, slowPred)
	}
}

// Holt separates level and trend smoothing, so a *persistent* trend keeps
// accumulating instead of being re-estimated from a single difference each step.
// On a sustained ramp it should therefore lead EWMATrend. This is the concrete
// justification for having two Forecaster implementations rather than one.
func TestHoltLeadsEWMAOnASustainedRamp(t *testing.T) {
	ewma, holt := NewEWMATrend(3, 0.5), NewHolt(3, 0.5, 0.5)

	var ewmaPred, holtPred float64
	for v := 100.0; v <= 400; v += 25 {
		ewmaPred, holtPred = ewma.Update(v), holt.Update(v)
	}

	if holtPred <= ewmaPred {
		t.Errorf("Holt predicted %v, EWMATrend predicted %v; want Holt to lead on a sustained ramp",
			holtPred, ewmaPred)
	}
}

func TestForecastersImplementTheInterface(t *testing.T) {
	var _ Forecaster = (*EWMATrend)(nil)
	var _ Forecaster = (*Holt)(nil)
}
