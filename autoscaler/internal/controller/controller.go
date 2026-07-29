// Package controller runs the reconcile loop that ties the collector, policy and
// scaler together.
package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/raeinsoltani/setpoint/internal/metrics"
	"github.com/raeinsoltani/setpoint/internal/observability"
	"github.com/raeinsoltani/setpoint/internal/policy"
	"github.com/raeinsoltani/setpoint/internal/scaler"
)

// Controller is one reconcile loop over one scale target.
type Controller struct {
	collector metrics.Collector
	policy    policy.Policy
	scaler    scaler.Scaler
	exporter  *observability.Exporter
	interval  time.Duration
	dryRun    bool
	log       *slog.Logger

	// now is injectable so tests can drive the stabilizer's clock without
	// sleeping. Defaults to time.Now.
	now func() time.Time
}

// Options configures a Controller.
type Options struct {
	Collector metrics.Collector
	Policy    policy.Policy
	Scaler    scaler.Scaler
	// Exporter is optional; nil disables metric publication.
	Exporter *observability.Exporter
	// Interval is the reconcile period.
	Interval time.Duration
	// DryRun logs decisions without applying them, so the autoscaler can be run
	// against a production target to see what it *would* do.
	DryRun bool
	Log    *slog.Logger
	Now    func() time.Time
}

// New returns a Controller.
func New(opts Options) (*Controller, error) {
	switch {
	case opts.Collector == nil:
		return nil, errors.New("controller: a collector is required")
	case opts.Policy == nil:
		return nil, errors.New("controller: a policy is required")
	case opts.Scaler == nil:
		return nil, errors.New("controller: a scaler is required")
	case opts.Interval <= 0:
		return nil, errors.New("controller: interval must be > 0")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Controller{
		collector: opts.Collector,
		policy:    opts.Policy,
		scaler:    opts.Scaler,
		exporter:  opts.Exporter,
		interval:  opts.Interval,
		dryRun:    opts.DryRun,
		log:       opts.Log,
		now:       opts.Now,
	}, nil
}

// Reconcile runs one iteration: read the metric, read the scale, decide, apply.
//
// Any failure returns without touching the replica count. Holding is the only
// safe response to missing information: a Prometheus outage is not evidence that
// load went up or down, and scaling on a guess is worse than not scaling.
func (c *Controller) Reconcile(ctx context.Context) (policy.Decision, error) {
	start := c.now()
	if c.exporter != nil {
		c.exporter.ReconcileStarted()
	}

	metricValue, err := c.collector.Read(ctx)
	if err != nil {
		if c.exporter != nil {
			c.exporter.CollectorFailed()
		}
		return policy.Decision{}, fmt.Errorf("controller: reading metric: %w", err)
	}

	current, err := c.scaler.Get(ctx)
	if err != nil {
		if c.exporter != nil {
			c.exporter.ScalerFailed()
		}
		return policy.Decision{}, fmt.Errorf("controller: reading scale: %w", err)
	}

	decision, err := c.policy.Decide(current.Ready, metricValue, start)
	if err != nil {
		return policy.Decision{}, fmt.Errorf("controller: deciding: %w", err)
	}

	// The comparison is against .spec.replicas, not ready: the question here is
	// "does the cluster already have this many replicas requested", and pods
	// still starting are already requested. Comparing against ready would re-issue
	// the same scale-up on every interval until the pods came up.
	if decision.Desired != current.Spec {
		if c.dryRun {
			c.log.Info("dry-run: would scale",
				"policy", c.policy.Name(), "from", current.Spec, "to", decision.Desired,
				"ready", current.Ready, "metric", metricValue, "reason", decision.Reason)
		} else {
			if err := c.scaler.Set(ctx, decision.Desired); err != nil {
				if c.exporter != nil {
					c.exporter.ScalerFailed()
				}
				return decision, fmt.Errorf("controller: applying scale %d -> %d: %w",
					current.Spec, decision.Desired, err)
			}
			c.log.Info("scaled",
				"policy", c.policy.Name(), "from", current.Spec, "to", decision.Desired,
				"ready", current.Ready, "metric", metricValue, "target", decision.Target,
				"reason", decision.Reason)
			if c.exporter != nil {
				c.exporter.RecordScale(current.Spec, decision.Desired)
			}
		}
	} else {
		c.log.Debug("hold",
			"policy", c.policy.Name(), "replicas", current.Spec, "ready", current.Ready,
			"metric", metricValue, "reason", decision.Reason)
	}

	if c.exporter != nil {
		var predicted float64
		if decision.Predicted != nil {
			predicted = *decision.Predicted
		}
		c.exporter.Observe(observability.Observation{
			SpecReplicas:    current.Spec,
			ReadyReplicas:   current.Ready,
			DesiredReplicas: decision.Desired,
			RawRecommend:    decision.Raw,
			MetricValue:     metricValue,
			MetricTarget:    decision.Target,
			PredictedValue:  predicted,
			Duration:        c.now().Sub(start),
		})
	}

	return decision, nil
}

// Run reconciles every interval until ctx is cancelled.
//
// A failed iteration is logged and the loop continues — a control loop that dies
// on the first transient API error is useless in a cluster. The wait subtracts
// time already spent, so a slow reconcile does not stretch the effective period.
func (c *Controller) Run(ctx context.Context) error {
	c.log.Info("autoscaler starting",
		"policy", c.policy.Name(), "interval", c.interval, "dry_run", c.dryRun)

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			c.log.Info("autoscaler stopping", "reason", context.Cause(ctx))
			return nil
		case <-timer.C:
		}

		start := time.Now()
		if _, err := c.Reconcile(ctx); err != nil {
			if ctx.Err() != nil {
				// Shutdown raced the reconcile; not a real failure.
				continue
			}
			c.log.Error("reconcile failed, holding replica count", "error", err)
		}

		wait := c.interval - time.Since(start)
		if wait < 0 {
			wait = 0
		}
		timer.Reset(wait)
	}
}
