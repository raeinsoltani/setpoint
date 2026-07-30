// Package observability exposes the autoscaler's own state to Prometheus.
//
// This is what makes the project demonstrable: the Grafana dashboard plots
// desired-vs-current replicas against the observed and predicted metric, so the
// difference between reactive and predictive scaling is visible as a lead time
// on a chart rather than asserted in prose.
package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Exporter holds the autoscaler's metrics and serves them over HTTP.
//
// The five gauges the Python prototype exported (sim/autoscaler/exporter.py:19-25)
// keep their exact names, so the Grafana dashboard panels built against the
// prototype remain valid. Everything else is new.
type Exporter struct {
	registry *prometheus.Registry

	currentReplicas prometheus.Gauge
	desiredReplicas prometheus.Gauge
	metricValue     prometheus.Gauge
	metricTarget    prometheus.Gauge
	predictedValue  prometheus.Gauge

	readyReplicas    prometheus.Gauge
	rawRecommend     prometheus.Gauge
	reconcileTime    prometheus.Histogram
	scaleActions     *prometheus.CounterVec
	collectorErrors  prometheus.Counter
	scalerErrors     prometheus.Counter
	reconcileTotal   prometheus.Counter
	lastSuccessUnix  prometheus.Gauge
	server           *http.Server
	log              *slog.Logger
}

// Options configures an Exporter.
type Options struct {
	// Port is the HTTP port for /metrics.
	Port int
	// Log receives serve errors. Defaults to slog.Default().
	Log *slog.Logger
}

// New returns an Exporter with all metrics registered on a private registry.
//
// A private registry rather than the default one keeps the Go runtime and
// process collectors opt-in and guarantees no other package can register a
// colliding name into the autoscaler's scrape output.
func New(opts Options) *Exporter {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	gauge := func(name, help string) prometheus.Gauge {
		return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	}
	counter := func(name, help string) prometheus.Counter {
		return prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	}

	e := &Exporter{
		registry: prometheus.NewRegistry(),
		log:      opts.Log,

		currentReplicas: gauge("autoscaler_current_replicas", "Replicas currently requested on the target (.spec.replicas)."),
		desiredReplicas: gauge("autoscaler_desired_replicas", "Replicas the policy decided on, after stabilization."),
		metricValue:     gauge("autoscaler_metric_value", "Observed value of the scaling signal."),
		metricTarget:    gauge("autoscaler_metric_target", "Per-replica target the policy aims for."),
		predictedValue:  gauge("autoscaler_predicted_value", "Forecast value of the scaling signal, or 0 for reactive policies."),

		readyReplicas: gauge("autoscaler_ready_replicas", "Replicas actually serving traffic (.status.readyReplicas); the base of the scaling formula."),
		rawRecommend:  gauge("autoscaler_raw_recommendation", "Policy recommendation before stabilization and rate limiting."),
		reconcileTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "autoscaler_reconcile_duration_seconds",
			Help: "Wall-clock duration of one reconcile iteration.",
			// The loop must finish well inside a 15s interval, so the buckets
			// are dense in the milliseconds and stop just past that budget.
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15},
		}),
		scaleActions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "autoscaler_scale_actions_total",
			Help: "Replica changes applied, by direction. The anti-flapping evidence.",
		}, []string{"direction"}),
		collectorErrors: counter("autoscaler_collector_errors_total", "Failed metric reads."),
		scalerErrors:    counter("autoscaler_scaler_errors_total", "Failed scale reads or writes."),
		reconcileTotal:  counter("autoscaler_reconcile_total", "Reconcile iterations attempted."),
		lastSuccessUnix: gauge("autoscaler_last_success_timestamp_seconds", "Unix time of the last fully successful reconcile."),
	}

	e.registry.MustRegister(
		e.currentReplicas, e.desiredReplicas, e.metricValue, e.metricTarget, e.predictedValue,
		e.readyReplicas, e.rawRecommend, e.reconcileTime, e.scaleActions,
		e.collectorErrors, e.scalerErrors, e.reconcileTotal, e.lastSuccessUnix,
	)
	// Pre-create both label values so a run with zero scale-downs still emits the
	// series — an absent series and a zero one look very different on a chart.
	e.scaleActions.WithLabelValues("up")
	e.scaleActions.WithLabelValues("down")

	if opts.Port > 0 {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(e.registry, promhttp.HandlerOpts{Registry: e.registry}))
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok")
		})
		e.server = &http.Server{
			Addr:              fmt.Sprintf(":%d", opts.Port),
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}
	return e
}

// Registry exposes the metric registry, so tests can gather without HTTP.
func (e *Exporter) Registry() *prometheus.Registry { return e.registry }

// Start serves /metrics in the background. It is a no-op when Port was 0.
func (e *Exporter) Start() {
	if e.server == nil {
		return
	}
	go func() {
		if err := e.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// A dead exporter loses observability but not scaling, so this is
			// logged rather than fatal.
			e.log.Error("metrics server stopped", "error", err)
		}
	}()
	e.log.Info("metrics server listening", "addr", e.server.Addr, "path", "/metrics")
}

// Stop shuts the HTTP server down, waiting for in-flight scrapes.
func (e *Exporter) Stop(ctx context.Context) error {
	if e.server == nil {
		return nil
	}
	return e.server.Shutdown(ctx)
}

// Observation is one reconcile's worth of state to publish.
type Observation struct {
	SpecReplicas    int32
	ReadyReplicas   int32
	DesiredReplicas int32
	RawRecommend    int32
	MetricValue     float64
	MetricTarget    float64
	PredictedValue  float64
	Duration        time.Duration
}

// Observe publishes one reconcile's state.
func (e *Exporter) Observe(o Observation) {
	e.currentReplicas.Set(float64(o.SpecReplicas))
	e.readyReplicas.Set(float64(o.ReadyReplicas))
	e.desiredReplicas.Set(float64(o.DesiredReplicas))
	e.rawRecommend.Set(float64(o.RawRecommend))
	e.metricValue.Set(o.MetricValue)
	e.metricTarget.Set(o.MetricTarget)
	e.predictedValue.Set(o.PredictedValue)
	e.reconcileTime.Observe(o.Duration.Seconds())
	e.lastSuccessUnix.SetToCurrentTime()
}

// RecordScale counts an applied replica change by direction.
func (e *Exporter) RecordScale(from, to int32) {
	switch {
	case to > from:
		e.scaleActions.WithLabelValues("up").Inc()
	case to < from:
		e.scaleActions.WithLabelValues("down").Inc()
	}
}

// ReconcileStarted counts a reconcile attempt.
func (e *Exporter) ReconcileStarted() { e.reconcileTotal.Inc() }

// CollectorFailed counts a failed metric read.
func (e *Exporter) CollectorFailed() { e.collectorErrors.Inc() }

// ScalerFailed counts a failed scale read or write.
func (e *Exporter) ScalerFailed() { e.scalerErrors.Inc() }
