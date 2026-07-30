package policy

import (
	"errors"
	"testing"
	"time"
)

func baseOptions() Options {
	return Options{Target: 100, Tolerance: 0.10, MinReplicas: 1, MaxReplicas: 10}
}

// recordingForecaster reports back whatever it is fed, and remembers the inputs.
// The identity behaviour keeps the policy tests about the policy: what is being
// asserted is *which signal* reaches the forecaster, not how it smooths.
type recordingForecaster struct{ inputs []float64 }

func (f *recordingForecaster) Update(v float64) float64 {
	f.inputs = append(f.inputs, v)
	return v
}
func (f *recordingForecaster) Reset() { f.inputs = nil }

func TestPolicyNames(t *testing.T) {
	tests := []struct {
		policy Policy
		want   string
	}{
		{NewThreshold(baseOptions()), "threshold"},
		{NewPredictiveTotalLoad(baseOptions(), &recordingForecaster{}), "predictive-total-load"},
		{NewPredictivePerReplica(baseOptions(), &recordingForecaster{}), "predictive-per-replica"},
	}
	for _, tt := range tests {
		if got := tt.policy.Name(); got != tt.want {
			t.Errorf("Name() = %q, want %q", got, tt.want)
		}
	}
}

func TestPoliciesImplementTheInterface(t *testing.T) {
	var _ Policy = (*Threshold)(nil)
	var _ Policy = (*PredictiveTotalLoad)(nil)
	var _ Policy = (*PredictivePerReplica)(nil)
}

func TestThresholdAppliesTheHPAFormula(t *testing.T) {
	p := NewThreshold(baseOptions())

	d, err := p.Decide(2, 200, t0)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if d.Desired != 4 {
		t.Errorf("Desired = %d, want 4", d.Desired)
	}
	if d.Predicted != nil {
		t.Errorf("Predicted = %v, want nil for a reactive policy", *d.Predicted)
	}
}

// The Decision carries the working, not just the answer: the exporter publishes
// these fields and the defense demo narrates them, so an unpopulated field shows up
// as a blank Grafana panel at the worst possible moment.
func TestDecisionCarriesItsWorking(t *testing.T) {
	opts := baseOptions()
	opts.MaxReplicas = 3 // force Raw and Desired apart
	p := NewThreshold(opts)

	d, err := p.Decide(4, 500, t0)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if d.ReadyReplicas != 4 {
		t.Errorf("ReadyReplicas = %d, want 4", d.ReadyReplicas)
	}
	if d.Metric != 500 {
		t.Errorf("Metric = %v, want 500", d.Metric)
	}
	if d.Target != 100 {
		t.Errorf("Target = %v, want 100", d.Target)
	}
	if d.Desired != 3 {
		t.Errorf("Desired = %d, want 3 (clamped to max)", d.Desired)
	}
	if d.Reason == "" {
		t.Error("Reason is empty; it feeds the logs and the demo narrative")
	}
}

func TestPolicyWithoutStabilizerSaysSo(t *testing.T) {
	d, err := NewThreshold(baseOptions()).Decide(2, 200, t0)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if d.Reason != "no stabilizer" {
		t.Errorf("Reason = %q, want %q", d.Reason, "no stabilizer")
	}
}

func TestStabilizerIsAppliedThroughDecide(t *testing.T) {
	opts := baseOptions()
	opts.Stabilizer = NewStabilizer(StabilizerOptions{Window: 90 * time.Second})
	p := NewThreshold(opts)

	// Establish a high recommendation, then drop the load.
	if _, err := p.Decide(5, 500, t0); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	d, err := p.Decide(5, 20, at(10))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if d.Desired != 5 {
		t.Errorf("Desired = %d, want 5 — the stabilizer should hold the fleet", d.Desired)
	}
	if d.Raw != 1 {
		t.Errorf("Raw = %d, want 1 — the pre-stabilization recommendation", d.Raw)
	}
}

func TestPoliciesRejectAnUnusableTarget(t *testing.T) {
	opts := baseOptions()
	opts.Target = 0

	policies := map[string]Policy{
		"threshold":              NewThreshold(opts),
		"predictive-total-load":  NewPredictiveTotalLoad(opts, &recordingForecaster{}),
		"predictive-per-replica": NewPredictivePerReplica(opts, &recordingForecaster{}),
	}
	for name, p := range policies {
		t.Run(name, func(t *testing.T) {
			if _, err := p.Decide(2, 100, t0); !errors.Is(err, ErrInvalidTarget) {
				t.Errorf("err = %v, want ErrInvalidTarget", err)
			}
		})
	}
}

// The central design claim of the project, asserted directly: the forecaster is fed
// total arrival rate, which this controller cannot influence. Replicas change
// between the two calls while total load does not, and the forecaster must see a
// flat series — if it saw anything else, the signal would be endogenous and the
// feedback loop would be back.
func TestPredictiveTotalLoadForecastsAnExogenousSignal(t *testing.T) {
	f := &recordingForecaster{}
	p := NewPredictiveTotalLoad(baseOptions(), f)

	if _, err := p.Decide(5, 100, t0); err != nil { // 5 x 100 = 500
		t.Fatalf("Decide() error = %v", err)
	}
	if _, err := p.Decide(4, 125, at(15)); err != nil { // 4 x 125 = 500
		t.Fatalf("Decide() error = %v", err)
	}
	if _, err := p.Decide(10, 50, at(30)); err != nil { // 10 x 50 = 500
		t.Fatalf("Decide() error = %v", err)
	}

	for i, got := range f.inputs {
		if got != 500 {
			t.Errorf("forecaster input %d = %v, want 500 — total load is constant here", i, got)
		}
	}
	if len(f.inputs) != 3 {
		t.Fatalf("forecaster saw %d observations, want 3", len(f.inputs))
	}
}

// The contrasting half: the flawed variant feeds the forecaster a signal that moved
// only because the controller moved the replica count. Same three states, same
// constant load, and the forecaster sees a series swinging by 2.5x.
func TestPredictivePerReplicaForecastsASignalItControls(t *testing.T) {
	f := &recordingForecaster{}
	p := NewPredictivePerReplica(baseOptions(), f)

	for i, s := range []struct {
		ready  int32
		metric float64
	}{{5, 100}, {4, 125}, {10, 50}} {
		if _, err := p.Decide(s.ready, s.metric, at(i*15)); err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
	}

	want := []float64{100, 125, 50}
	for i, w := range want {
		if f.inputs[i] != w {
			t.Errorf("forecaster input %d = %v, want %v", i, f.inputs[i], w)
		}
	}
}

func TestPredictiveTotalLoadHandlesZeroReadyReplicas(t *testing.T) {
	f := &recordingForecaster{}
	p := NewPredictiveTotalLoad(baseOptions(), f)

	d, err := p.Decide(0, 500, t0)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	// With no ready pods the metric is all the information available, so it is
	// treated as the load of a single replica rather than multiplied away to zero.
	if f.inputs[0] != 500 {
		t.Errorf("forecaster input = %v, want 500", f.inputs[0])
	}
	if d.Desired != 5 {
		t.Errorf("Desired = %d, want 5", d.Desired)
	}
}

func TestPredictiveRespectsReplicaBounds(t *testing.T) {
	p := NewPredictiveTotalLoad(baseOptions(), &recordingForecaster{})

	t.Run("floor", func(t *testing.T) {
		d, err := p.Decide(5, 0, t0)
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		if d.Desired != 1 {
			t.Errorf("Desired = %d, want 1 (min_replicas) under no load", d.Desired)
		}
	})

	t.Run("ceiling", func(t *testing.T) {
		d, err := p.Decide(5, 100000, at(15))
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		if d.Desired != 10 {
			t.Errorf("Desired = %d, want 10 (max_replicas) under extreme load", d.Desired)
		}
	})
}

// Prediction has to buy lead time or the project has no thesis. On a rising ramp
// the predictive policy must ask for capacity no later than the reactive one, and
// strictly earlier at some point in the ramp.
func TestPredictiveLeadsThresholdOnARamp(t *testing.T) {
	reactive := NewThreshold(baseOptions())
	predictive := NewPredictiveTotalLoad(baseOptions(), NewEWMATrend(3, 0.5))

	const ready int32 = 4
	leadFound := false

	for i, metric := range []float64{100, 110, 120, 130, 140, 150} {
		r, err := reactive.Decide(ready, metric, at(i*15))
		if err != nil {
			t.Fatalf("threshold Decide() error = %v", err)
		}
		p, err := predictive.Decide(ready, metric, at(i*15))
		if err != nil {
			t.Fatalf("predictive Decide() error = %v", err)
		}

		if p.Desired < r.Desired {
			t.Fatalf("step %d (metric %v): predictive asked for %d, reactive for %d — "+
				"predictive must never lag", i, metric, p.Desired, r.Desired)
		}
		if p.Desired > r.Desired {
			leadFound = true
		}
	}

	if !leadFound {
		t.Error("predictive never asked for capacity ahead of reactive on a rising ramp")
	}
}

// simulateConstantLoad closes the loop: the metric each step is derived from the
// replica count the policy chose on the previous step, exactly as it is in a real
// cluster where per-replica load is total load divided by the serving pods.
//
// Total arrival rate is held constant throughout. Any movement in the replica count
// after the fleet has settled is therefore generated by the controller itself, not
// by the workload.
func simulateConstantLoad(t *testing.T, p Policy, totalLoad float64, steps int) []int32 {
	t.Helper()

	ready := int32(1)
	history := make([]int32, 0, steps)

	for i := range steps {
		metric := totalLoad / float64(ready)
		d, err := p.Decide(ready, metric, at(i*15))
		if err != nil {
			t.Fatalf("step %d: Decide() error = %v", i, err)
		}
		ready = d.Desired
		history = append(history, ready)
	}
	return history
}

func countChanges(history []int32) int {
	changes := 0
	for i := 1; i < len(history); i++ {
		if history[i] != history[i-1] {
			changes++
		}
	}
	return changes
}

// This is the project's headline result, written as a test so it cannot quietly
// stop being true.
//
// Both policies see the same constant total load of 500 against a target of 100, so
// the correct answer is five replicas, forever. PredictiveTotalLoad finds it and
// stays there. PredictivePerReplica cannot settle: it forecasts per-replica load,
// which is total/ready, and `ready` is the quantity it sets — so it extrapolates a
// signal its own output moves, and the loop has positive gain:
//
//	scale up → per-replica load falls → forecast trends down → scale down
//	         → per-replica load rises → forecast trends up   → scale up
//
// No stabilizer is configured, deliberately. The stabilizer is a damper, and
// damping the symptom would hide the property under test — which lives in the
// policy's own dynamics. Quantifying how much of this the stabilizer masks, and
// where the oscillation boundary sits as a function of horizon and alpha, is the
// stability analysis in Phase 7.1.
func TestPerReplicaForecastingOscillatesUnderConstantLoad(t *testing.T) {
	const (
		totalLoad = 500.0
		steps     = 40
		settled   = 10 // ignore the initial climb from one replica
	)

	stable := simulateConstantLoad(t,
		NewPredictiveTotalLoad(baseOptions(), NewEWMATrend(3, 0.5)), totalLoad, steps)
	unstable := simulateConstantLoad(t,
		NewPredictivePerReplica(baseOptions(), NewEWMATrend(3, 0.5)), totalLoad, steps)

	stableChanges := countChanges(stable[settled:])
	unstableChanges := countChanges(unstable[settled:])

	if stableChanges != 0 {
		t.Errorf("PredictiveTotalLoad moved the fleet %d times under constant load "+
			"(should have converged): %v", stableChanges, stable[settled:])
	}
	for _, r := range stable[settled:] {
		if r != 5 {
			t.Errorf("PredictiveTotalLoad settled on %d replicas, want 5 (500/100): %v",
				r, stable[settled:])
			break
		}
	}

	if unstableChanges < 10 {
		t.Errorf("PredictivePerReplica moved the fleet only %d times in %d steps; "+
			"the evaluation baseline is supposed to oscillate: %v",
			unstableChanges, steps-settled, unstable[settled:])
	}

	t.Logf("replica changes under constant load: total-load=%d, per-replica=%d",
		stableChanges, unstableChanges)
}

// The same contrast stated as cost. Oscillation is not merely untidy: swinging
// between the floor and the ceiling both over-provisions and under-serves, so the
// unstable policy's fleet size is a poor description of the actual demand.
func TestOscillationSpansTheReplicaRange(t *testing.T) {
	unstable := simulateConstantLoad(t,
		NewPredictivePerReplica(baseOptions(), NewEWMATrend(3, 0.5)), 500, 40)

	lo, hi := unstable[10], unstable[10]
	for _, r := range unstable[10:] {
		lo, hi = min(lo, r), max(hi, r)
	}

	if hi-lo < 4 {
		t.Errorf("per-replica predictive spanned %d..%d replicas; expected a wide swing "+
			"around the correct answer of 5", lo, hi)
	}
	t.Logf("per-replica predictive oscillated between %d and %d replicas "+
		"while the correct answer was a constant 5", lo, hi)
}
