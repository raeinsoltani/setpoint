// Command setpoint runs the Prometheus-driven autoscaler against a Kubernetes
// Deployment.
//
//	setpoint --config config.yaml
//	setpoint --config config.yaml --dry-run   # decide, log, apply nothing
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/raeinsoltani/setpoint/internal/config"
	"github.com/raeinsoltani/setpoint/internal/controller"
	"github.com/raeinsoltani/setpoint/internal/metrics"
	"github.com/raeinsoltani/setpoint/internal/observability"
	"github.com/raeinsoltani/setpoint/internal/policy"
	"github.com/raeinsoltani/setpoint/internal/scaler"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath = flag.String("config", "config.yaml", "path to the YAML configuration file")
		dryRun     = flag.Bool("dry-run", false, "log scaling decisions without applying them")
		logLevel   = flag.String("log-level", "info", "debug, info, warn or error")
		validate   = flag.Bool("validate", false, "load and validate the config, then exit")
	)
	flag.Parse()

	level, err := parseLevel(*logLevel)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *validate {
		fmt.Printf("%s is valid (policy=%s, target=%.2f, replicas=%d-%d)\n",
			*configPath, cfg.Policy.Name, cfg.Policy.Target, cfg.Policy.MinReplicas, cfg.Policy.MaxReplicas)
		return nil
	}

	collector, err := buildCollector(cfg, log)
	if err != nil {
		return err
	}
	scl, err := buildScaler(cfg)
	if err != nil {
		return err
	}
	pol, err := buildPolicy(cfg)
	if err != nil {
		return err
	}

	var exporter *observability.Exporter
	if cfg.Exporter.Enabled {
		exporter = observability.New(observability.Options{Port: cfg.Exporter.Port, Log: log})
		exporter.Start()
	}

	ctrl, err := controller.New(controller.Options{
		Collector: collector,
		Policy:    pol,
		Scaler:    scl,
		Exporter:  exporter,
		Interval:  cfg.Interval(),
		DryRun:    *dryRun,
		Log:       log,
	})
	if err != nil {
		return err
	}

	// SIGTERM is what Kubernetes sends on pod deletion, so honouring it is what
	// makes a rolling update of the autoscaler itself clean.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runErr := ctrl.Run(ctx)

	if exporter != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := exporter.Stop(shutdownCtx); err != nil {
			log.Warn("metrics server shutdown", "error", err)
		}
	}
	return runErr
}

func buildCollector(cfg *config.Config, log *slog.Logger) (metrics.Collector, error) {
	switch cfg.Collector.Type {
	case config.CollectorStatic:
		return metrics.NewStatic(cfg.Collector.Value), nil
	case config.CollectorPrometheus:
		return metrics.NewPrometheus(metrics.PrometheusOptions{
			URL:     cfg.Collector.URL,
			Query:   cfg.Collector.Query,
			Timeout: time.Duration(cfg.Collector.TimeoutSeconds * float64(time.Second)),
			Retries: 2,
			Log:     log,
		})
	default:
		return nil, fmt.Errorf("unknown collector type %q", cfg.Collector.Type)
	}
}

func buildScaler(cfg *config.Config) (scaler.Scaler, error) {
	switch cfg.Scaler.Type {
	case config.ScalerInMemory:
		return scaler.NewInMemory(cfg.Scaler.Replicas), nil
	case config.ScalerKubernetes:
		return scaler.NewKubernetes(scaler.KubernetesOptions{
			Deployment: cfg.Scaler.Deployment,
			Namespace:  cfg.Scaler.Namespace,
			Kubeconfig: cfg.Scaler.Kubeconfig,
		})
	default:
		return nil, fmt.Errorf("unknown scaler type %q", cfg.Scaler.Type)
	}
}

func buildPolicy(cfg *config.Config) (policy.Policy, error) {
	p := &cfg.Policy
	opts := policy.Options{
		Target:      p.Target,
		Tolerance:   p.Tolerance,
		MinReplicas: p.MinReplicas,
		MaxReplicas: p.MaxReplicas,
		Stabilizer: policy.NewStabilizer(policy.StabilizerOptions{
			Window:   p.StabilizationWindow(),
			Cooldown: p.Cooldown(),
			MaxStep:  p.MaxStep,
		}),
	}

	switch p.Name {
	case config.PolicyThreshold:
		return policy.NewThreshold(opts), nil
	case config.PolicyPredictive:
		return policy.NewPredictiveTotalLoad(opts, buildForecaster(p)), nil
	case config.PolicyPredictivePerReplica:
		// Deliberately available, deliberately noisy: this policy exists as the
		// evaluation baseline that oscillates, not as something to run for real.
		slog.Warn("policy predictive-per-replica forecasts a signal the autoscaler controls; " +
			"it oscillates and is intended only as an evaluation baseline")
		return policy.NewPredictivePerReplica(opts, buildForecaster(p)), nil
	default:
		return nil, fmt.Errorf("unknown policy %q", p.Name)
	}
}

func buildForecaster(p *config.PolicyConfig) policy.Forecaster {
	f := &p.Forecaster
	if f.Type == config.ForecasterHolt {
		return policy.NewHolt(f.Horizon, f.Alpha, f.Beta)
	}
	return policy.NewEWMATrend(f.Horizon, f.Alpha)
}

func parseLevel(s string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("invalid --log-level %q: want debug, info, warn or error", s)
	}
	return level, nil
}
