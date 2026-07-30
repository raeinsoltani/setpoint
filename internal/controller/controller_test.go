package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/raeinsoltani/setpoint/internal/metrics"
	"github.com/raeinsoltani/setpoint/internal/policy"
	"github.com/raeinsoltani/setpoint/internal/scaler"
)

var errBoom = errors.New("boom")

// failingCollector fails a fixed number of times, then serves a value. It models
// a Prometheus that is briefly unavailable rather than permanently broken.
type failingCollector struct {
	failures int
	value    float64
	calls    int
}

func (c *failingCollector) Read(context.Context) (float64, error) {
	c.calls++
	if c.calls <= c.failures {
		return 0, errBoom
	}
	return c.value, nil
}

type failingScaler struct {
	scaler.Scaler
	failSet bool
	setCall int
}

func (s *failingScaler) Set(ctx context.Context, replicas int32) error {
	s.setCall++
	if s.failSet {
		return errBoom
	}
	return s.Scaler.Set(ctx, replicas)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func thresholdPolicy(target float64) policy.Policy {
	return policy.NewThreshold(policy.Options{
		Target: target, Tolerance: 0.10, MinReplicas: 1, MaxReplicas: 10,
	})
}

func newTestController(t *testing.T, opts Options) *Controller {
	t.Helper()
	if opts.Interval == 0 {
		opts.Interval = time.Second
	}
	if opts.Log == nil {
		opts.Log = discardLogger()
	}
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// The scaling formula must use ready replicas, not requested ones. This is the
// correctness fix from Phase 1 item 4 and the reason scaler.Scale has two fields:
// during a scale-up, spec runs ahead of ready, and using spec as the base makes
// the autoscaler over-scale precisely when it is already adding capacity.
func TestFormulaBaseIsReadyNotSpec(t *testing.T) {
	// 4 requested, only 2 serving. At 200 req/s/replica against a target of 100,
	// the correct answer is ceil(2 * 2.0) = 4 — the capacity already on its way.
	// Using spec as the base would give ceil(4 * 2.0) = 8.
	sc := scaler.NewInMemory(2)
	if err := sc.Set(context.Background(), 4); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := sc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec != 4 || got.Ready != 2 {
		t.Fatalf("fixture wrong: spec=%d ready=%d, want 4 and 2", got.Spec, got.Ready)
	}

	c := newTestController(t, Options{
		Collector: metrics.NewStatic(200),
		Policy:    thresholdPolicy(100),
		Scaler:    sc,
	})

	decision, err := c.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if decision.ReadyReplicas != 2 {
		t.Errorf("policy base = %d, want the ready count 2", decision.ReadyReplicas)
	}
	if decision.Desired != 4 {
		t.Errorf("desired = %d, want 4 (ceil(ready 2 x 2.0)); 8 means spec was used as the base", decision.Desired)
	}
}

// Scale-up is not re-issued while pods are still starting. The comparison that
// decides whether to call the API is against spec, so a fleet that has already
// been asked for 4 replicas is left alone until they become ready.
func TestNoRedundantScaleWhilePodsStart(t *testing.T) {
	inner := scaler.NewInMemory(2)
	if err := inner.Set(context.Background(), 4); err != nil {
		t.Fatalf("Set: %v", err)
	}
	sc := &failingScaler{Scaler: inner}

	c := newTestController(t, Options{
		Collector: metrics.NewStatic(200),
		Policy:    thresholdPolicy(100),
		Scaler:    sc,
	})

	if _, err := c.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if sc.setCall != 0 {
		t.Errorf("Set called %d times; spec already equals the decision, so the API should not be touched", sc.setCall)
	}
}

// A metric read failure must leave the replica count exactly where it was.
// A missing metric is not evidence of a load change in either direction.
func TestCollectorErrorHoldsReplicas(t *testing.T) {
	sc := scaler.NewInMemory(3)
	c := newTestController(t, Options{
		Collector: &failingCollector{failures: 1, value: 500},
		Policy:    thresholdPolicy(100),
		Scaler:    sc,
	})

	if _, err := c.Reconcile(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("Reconcile error = %v, want it to wrap errBoom", err)
	}
	got, _ := sc.Get(context.Background())
	if got.Spec != 3 {
		t.Errorf("replicas = %d after a failed read, want them held at 3", got.Spec)
	}
}

// The loop survives transient errors: a collector that fails once and then
// recovers must still produce a scaling decision on the next iteration.
func TestLoopSurvivesTransientCollectorError(t *testing.T) {
	sc := scaler.NewInMemory(1)
	collector := &failingCollector{failures: 1, value: 500}
	c := newTestController(t, Options{
		Collector: collector,
		Policy:    thresholdPolicy(100),
		Scaler:    sc,
		Interval:  time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run returned %v; the loop must not die on a transient error", err)
	}

	if collector.calls < 2 {
		t.Fatalf("collector called %d times; the loop stopped after the first failure", collector.calls)
	}
	got, _ := sc.Get(context.Background())
	if got.Spec != 5 {
		t.Errorf("replicas = %d, want 5 (ceil(1 x 5.0)) once the collector recovered", got.Spec)
	}
}

// A scale write failure surfaces as an error but still returns the decision, so
// the caller can log what was attempted.
func TestScalerErrorIsReported(t *testing.T) {
	sc := &failingScaler{Scaler: scaler.NewInMemory(1), failSet: true}
	c := newTestController(t, Options{
		Collector: metrics.NewStatic(500),
		Policy:    thresholdPolicy(100),
		Scaler:    sc,
	})

	decision, err := c.Reconcile(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("Reconcile error = %v, want it to wrap errBoom", err)
	}
	if decision.Desired != 5 {
		t.Errorf("decision.Desired = %d, want 5 reported even though applying it failed", decision.Desired)
	}
}

// Dry-run decides and logs but never touches the scale target — the mode that
// makes it safe to point the autoscaler at a production Deployment to see what
// it would do.
func TestDryRunDoesNotScale(t *testing.T) {
	sc := &failingScaler{Scaler: scaler.NewInMemory(1)}
	c := newTestController(t, Options{
		Collector: metrics.NewStatic(500),
		Policy:    thresholdPolicy(100),
		Scaler:    sc,
		DryRun:    true,
	})

	decision, err := c.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if decision.Desired != 5 {
		t.Errorf("decision.Desired = %d, want 5 — dry-run must still decide", decision.Desired)
	}
	if sc.setCall != 0 {
		t.Errorf("Set called %d times in dry-run, want 0", sc.setCall)
	}
	if got, _ := sc.Get(context.Background()); got.Spec != 1 {
		t.Errorf("replicas = %d, want them untouched at 1", got.Spec)
	}
}

// Run must return promptly when its context is cancelled, so SIGTERM during a
// long reconcile interval does not stall pod termination.
func TestRunStopsOnContextCancel(t *testing.T) {
	c := newTestController(t, Options{
		Collector: metrics.NewStatic(100),
		Policy:    thresholdPolicy(100),
		Scaler:    scaler.NewInMemory(1),
		Interval:  time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	// Let the first reconcile complete, then cancel mid-wait.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on clean shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancellation; it is waiting out the full interval")
	}
}

func TestNewRejectsIncompleteOptions(t *testing.T) {
	full := Options{
		Collector: metrics.NewStatic(1),
		Policy:    thresholdPolicy(100),
		Scaler:    scaler.NewInMemory(1),
		Interval:  time.Second,
	}
	tests := []struct {
		name  string
		mutate func(*Options)
	}{
		{"no collector", func(o *Options) { o.Collector = nil }},
		{"no policy", func(o *Options) { o.Policy = nil }},
		{"no scaler", func(o *Options) { o.Scaler = nil }},
		{"zero interval", func(o *Options) { o.Interval = 0 }},
		{"negative interval", func(o *Options) { o.Interval = -time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := full
			tt.mutate(&opts)
			if _, err := New(opts); err == nil {
				t.Error("New accepted an incomplete configuration")
			}
		})
	}
}
