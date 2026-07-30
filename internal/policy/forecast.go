package policy

import "math"

// Forecaster predicts a signal some number of steps ahead.
//
// Update feeds one observation and returns the prediction for the configured
// horizon. Implementations are stateful and are called once per control interval,
// so they must be cheap.
//
// The interface exists so the forecasting method is a swappable component: the
// proposal's related work cites ARIMA and LSTM, and either can be dropped in
// behind this interface without touching the control loop.
//
// Whatever the implementation, it must be fed an *exogenous* signal — one the
// autoscaler's own decisions do not change. See PredictiveTotalLoad.
type Forecaster interface {
	Update(value float64) float64
	// Reset clears accumulated state, so one instance can be reused across runs.
	Reset()
}

// EWMATrend smooths with an exponentially weighted moving average and
// extrapolates the resulting trend linearly. Ported from the Python prototype
// (sim/autoscaler/policy.py:67-92).
type EWMATrend struct {
	horizon  int
	alpha    float64
	ewma     float64
	prevEWMA float64
	seeded   bool
}

// NewEWMATrend returns a forecaster predicting horizon steps ahead with smoothing
// factor alpha in (0, 1]. Larger alpha tracks recent values more closely.
func NewEWMATrend(horizon int, alpha float64) *EWMATrend {
	return &EWMATrend{horizon: horizon, alpha: alpha}
}

// Update implements Forecaster.
func (f *EWMATrend) Update(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return f.ewma
	}
	if !f.seeded {
		f.ewma, f.prevEWMA, f.seeded = value, value, true
	} else {
		f.prevEWMA = f.ewma
		f.ewma = f.alpha*value + (1-f.alpha)*f.ewma
	}
	trend := f.ewma - f.prevEWMA
	return math.Max(0, f.ewma+trend*float64(f.horizon))
}

// Reset implements Forecaster.
func (f *EWMATrend) Reset() { f.ewma, f.prevEWMA, f.seeded = 0, 0, false }

// Holt is double exponential smoothing: it tracks level and trend with separate
// smoothing factors, so a persistent trend is not damped the way EWMATrend's
// single-difference estimate damps it.
//
// It is a second implementation of Forecaster purely to demonstrate that the
// forecasting method is genuinely pluggable.
type Holt struct {
	horizon int
	alpha   float64 // level smoothing
	beta    float64 // trend smoothing
	level   float64
	trend   float64
	seeded  bool
	count   int
}

// NewHolt returns a Holt forecaster. alpha smooths the level, beta the trend.
func NewHolt(horizon int, alpha, beta float64) *Holt {
	return &Holt{horizon: horizon, alpha: alpha, beta: beta}
}

// Update implements Forecaster.
func (f *Holt) Update(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		// Same non-negative floor as the seeded path below. Without it a metric
		// gap arriving during a steep decline returns a negative load, which
		// FleetFor would silently floor to min_replicas — a real scale-down
		// caused by a missing scrape rather than by absent load.
		return math.Max(0, f.level+f.trend*float64(f.horizon))
	}
	switch {
	case !f.seeded:
		f.level, f.trend, f.seeded, f.count = value, 0, true, 1
	case f.count == 1:
		// The second observation is the first evidence of a trend.
		f.trend = value - f.level
		f.level = value
		f.count++
	default:
		prevLevel := f.level
		f.level = f.alpha*value + (1-f.alpha)*(f.level+f.trend)
		f.trend = f.beta*(f.level-prevLevel) + (1-f.beta)*f.trend
	}
	return math.Max(0, f.level+f.trend*float64(f.horizon))
}

// Reset implements Forecaster.
func (f *Holt) Reset() { f.level, f.trend, f.seeded, f.count = 0, 0, false, 0 }
