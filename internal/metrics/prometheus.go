package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// ErrAmbiguousResult is returned when a query yields more than one series. The
// scaling signal must be a single number, so a query returning several series is
// a configuration mistake — silently taking the first would scale on an
// arbitrary pod and be very hard to notice.
var ErrAmbiguousResult = errors.New("metrics: PromQL query returned multiple series; aggregate it to a single value")

// Prometheus reads the scaling signal from a Prometheus server via PromQL.
//
// The query is expected to evaluate to a scalar or a one-element vector, e.g.
//
//	sum(rate(http_requests_total{app="sample"}[1m]))
//	  / count(up{app="sample"} == 1)
//
// This uses the official client rather than the prototype's hand-rolled
// urllib call (sim/autoscaler/metrics.py:58), which brings retry-aware
// transport, correct content negotiation and typed results for free.
type Prometheus struct {
	api     promv1.API
	query   string
	timeout time.Duration
	// retries is the number of extra attempts after the first failure.
	retries int
	log     *slog.Logger
}

// PrometheusOptions configures a Prometheus collector.
type PrometheusOptions struct {
	// URL is the Prometheus server base URL, e.g. http://localhost:9090.
	URL string
	// Query is the PromQL expression producing the scaling signal.
	Query string
	// Timeout bounds a single query attempt.
	Timeout time.Duration
	// Retries is how many times to retry a failed query within one Read.
	// Zero means a single attempt.
	Retries int
	// Log receives retry warnings. Defaults to slog.Default().
	Log *slog.Logger
}

// NewPrometheus returns a collector querying the given server.
func NewPrometheus(opts PrometheusOptions) (*Prometheus, error) {
	if opts.Query == "" {
		return nil, errors.New("metrics: prometheus collector requires a query")
	}
	client, err := promapi.NewClient(promapi.Config{Address: opts.URL})
	if err != nil {
		return nil, fmt.Errorf("metrics: building prometheus client for %q: %w", opts.URL, err)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Prometheus{
		api:     promv1.NewAPI(client),
		query:   opts.Query,
		timeout: opts.Timeout,
		retries: opts.Retries,
		log:     opts.Log,
	}, nil
}

// Read implements Collector.
//
// Each attempt is bounded by the configured timeout, and the whole Read is
// bounded by ctx, so a slow Prometheus cannot stall the control loop past its
// reconcile interval.
func (p *Prometheus) Read(ctx context.Context) (float64, error) {
	var lastErr error
	for attempt := 0; attempt <= p.retries; attempt++ {
		if attempt > 0 {
			// A short fixed backoff: the reconcile interval is the real budget,
			// so there is no room for exponential growth here.
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		value, err := p.queryOnce(ctx)
		if err == nil {
			return value, nil
		}
		if errors.Is(err, ErrAmbiguousResult) || ctx.Err() != nil {
			// A malformed query will fail identically every time, and a cancelled
			// context will not recover. Neither is worth retrying.
			return 0, err
		}
		lastErr = err
		p.log.Warn("prometheus query failed, retrying",
			"attempt", attempt+1, "of", p.retries+1, "error", err)
	}
	return 0, fmt.Errorf("metrics: query %q failed after %d attempts: %w", p.query, p.retries+1, lastErr)
}

func (p *Prometheus) queryOnce(ctx context.Context) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	result, warnings, err := p.api.Query(ctx, p.query, time.Now())
	if err != nil {
		return 0, err
	}
	for _, w := range warnings {
		p.log.Warn("prometheus query warning", "query", p.query, "warning", w)
	}
	return scalarFrom(result)
}

// scalarFrom reduces a PromQL result to the single float the policy needs,
// handling the same three cases as the prototype (sim/autoscaler/metrics.py:65-76).
func scalarFrom(result model.Value) (float64, error) {
	switch v := result.(type) {
	case *model.Scalar:
		return float64(v.Value), nil
	case model.Vector:
		switch len(v) {
		case 0:
			// An empty vector means the target is not reporting — typically no
			// pods are up yet. Zero load is the correct reading, not an error.
			return 0, nil
		case 1:
			return float64(v[0].Value), nil
		default:
			return 0, fmt.Errorf("%w: got %d series", ErrAmbiguousResult, len(v))
		}
	default:
		return 0, fmt.Errorf("metrics: unsupported PromQL result type %T (expected scalar or vector)", result)
	}
}
