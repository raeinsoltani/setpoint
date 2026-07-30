package observability

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func newTestExporter() *Exporter {
	// Port 0 skips the HTTP server; the registry is what the assertions need.
	return New(Options{Port: 0})
}

// The Grafana dashboard panels were built against the Python prototype's metric
// names (sim/autoscaler/exporter.py:19-25). Renaming any of them breaks every
// panel and every figure in the evaluation chapter, so the names are pinned here
// rather than left to convention.
func TestPrototypeMetricNamesArePreserved(t *testing.T) {
	e := newTestExporter()
	e.Observe(Observation{
		SpecReplicas: 3, ReadyReplicas: 2, DesiredReplicas: 4,
		MetricValue: 120, MetricTarget: 100, PredictedValue: 150,
	})

	want := []string{
		"autoscaler_current_replicas",
		"autoscaler_desired_replicas",
		"autoscaler_metric_value",
		"autoscaler_metric_target",
		"autoscaler_predicted_value",
	}
	names := gatheredNames(t, e)
	for _, name := range want {
		if !names[name] {
			t.Errorf("metric %q is missing; Grafana panels built on it would go blank", name)
		}
	}
}

func TestObservePublishesValues(t *testing.T) {
	e := newTestExporter()
	e.Observe(Observation{
		SpecReplicas: 3, ReadyReplicas: 2, DesiredReplicas: 4, RawRecommend: 6,
		MetricValue: 120.5, MetricTarget: 100, PredictedValue: 150,
		Duration: 12 * time.Millisecond,
	})

	tests := []struct {
		name  string
		gauge prometheus.Gauge
		want  float64
	}{
		{"autoscaler_current_replicas", e.currentReplicas, 3},
		{"autoscaler_ready_replicas", e.readyReplicas, 2},
		{"autoscaler_desired_replicas", e.desiredReplicas, 4},
		{"autoscaler_raw_recommendation", e.rawRecommend, 6},
		{"autoscaler_metric_value", e.metricValue, 120.5},
		{"autoscaler_metric_target", e.metricTarget, 100},
		{"autoscaler_predicted_value", e.predictedValue, 150},
	}
	for _, tt := range tests {
		if got := testutil.ToFloat64(tt.gauge); got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Ready and spec replicas must be published separately. Collapsing them on the
// dashboard would hide the start-up gap, which is the whole visual point of the
// predictive-versus-reactive comparison.
func TestReadyAndSpecAreDistinctSeries(t *testing.T) {
	e := newTestExporter()
	e.Observe(Observation{SpecReplicas: 8, ReadyReplicas: 3})

	if got := testutil.ToFloat64(e.currentReplicas); got != 8 {
		t.Errorf("current_replicas = %v, want 8", got)
	}
	if got := testutil.ToFloat64(e.readyReplicas); got != 3 {
		t.Errorf("ready_replicas = %v, want 3", got)
	}
}

// Scale-action counts are the quantitative evidence for the anti-flapping
// requirement, and the metric that exposed the per-replica feedback bug.
func TestRecordScaleCountsByDirection(t *testing.T) {
	e := newTestExporter()
	e.RecordScale(2, 5) // up
	e.RecordScale(5, 9) // up
	e.RecordScale(9, 4) // down
	e.RecordScale(4, 4) // unchanged: not an action

	if got := testutil.ToFloat64(e.scaleActions.WithLabelValues("up")); got != 2 {
		t.Errorf("scale-ups = %v, want 2", got)
	}
	if got := testutil.ToFloat64(e.scaleActions.WithLabelValues("down")); got != 1 {
		t.Errorf("scale-downs = %v, want 1", got)
	}
}

// A run with no scale-downs must still emit the series as zero. An absent series
// and a zero one look very different on a chart, and "never scaled down" is a
// result worth being able to state.
func TestBothScaleDirectionsExistBeforeAnyAction(t *testing.T) {
	e := newTestExporter()

	families, err := e.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	seen := map[string]bool{}
	for _, f := range families {
		if f.GetName() != "autoscaler_scale_actions_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "direction" {
					seen[label.GetValue()] = true
				}
			}
		}
	}

	for _, direction := range []string{"up", "down"} {
		if !seen[direction] {
			t.Errorf("autoscaler_scale_actions_total is missing direction=%q before any scaling occurred", direction)
		}
	}
}

func TestErrorCountersIncrement(t *testing.T) {
	e := newTestExporter()
	e.ReconcileStarted()
	e.ReconcileStarted()
	e.CollectorFailed()
	e.ScalerFailed()

	if got := testutil.ToFloat64(e.reconcileTotal); got != 2 {
		t.Errorf("reconcile_total = %v, want 2", got)
	}
	if got := testutil.ToFloat64(e.collectorErrors); got != 1 {
		t.Errorf("collector_errors_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(e.scalerErrors); got != 1 {
		t.Errorf("scaler_errors_total = %v, want 1", got)
	}
}

func TestReconcileDurationIsObserved(t *testing.T) {
	e := newTestExporter()
	e.Observe(Observation{Duration: 30 * time.Millisecond})

	if got := testutil.CollectAndCount(e.reconcileTime); got != 1 {
		t.Errorf("reconcile duration metric count = %d, want 1", got)
	}
	if !strings.Contains(gatheredText(t, e), "autoscaler_reconcile_duration_seconds") {
		t.Error("reconcile duration histogram was not registered")
	}
}

func TestAllMetricsAreRegistered(t *testing.T) {
	names := gatheredNames(t, newTestExporter())
	want := []string{
		"autoscaler_current_replicas", "autoscaler_desired_replicas",
		"autoscaler_metric_value", "autoscaler_metric_target", "autoscaler_predicted_value",
		"autoscaler_ready_replicas", "autoscaler_raw_recommendation",
		"autoscaler_reconcile_duration_seconds", "autoscaler_scale_actions_total",
		"autoscaler_collector_errors_total", "autoscaler_scaler_errors_total",
		"autoscaler_reconcile_total", "autoscaler_last_success_timestamp_seconds",
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("metric %q is not registered", name)
		}
	}
}

func gatheredNames(t *testing.T, e *Exporter) map[string]bool {
	t.Helper()
	families, err := e.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	names := make(map[string]bool, len(families))
	for _, f := range families {
		names[f.GetName()] = true
	}
	return names
}

func gatheredText(t *testing.T, e *Exporter) string {
	t.Helper()
	families, err := e.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var sb strings.Builder
	for _, f := range families {
		sb.WriteString(f.String())
		sb.WriteString("\n")
	}
	return sb.String()
}
