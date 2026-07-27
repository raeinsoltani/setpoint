// Package config loads and validates the autoscaler's YAML configuration.
//
// The schema is compatible with the Python prototype's config.yaml (sim/config.yaml)
// so existing files keep working, with additions for the settings the prototype
// lacked: forecaster choice, cooldown and per-interval rate limiting.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Policy names accepted in the `policy.name` field.
const (
	PolicyThreshold = "threshold"
	// PolicyPredictive forecasts total arrival rate. This is the predictive
	// policy you almost certainly want; see policy.PredictiveTotalLoad.
	PolicyPredictive = "predictive"
	// PolicyPredictivePerReplica forecasts the per-replica metric. Retained as an
	// evaluation baseline because it oscillates; not for production use.
	PolicyPredictivePerReplica = "predictive-per-replica"
)

// Forecaster types accepted in the `policy.forecaster.type` field.
const (
	ForecasterEWMA = "ewma"
	ForecasterHolt = "holt"
)

// Collector and scaler types.
const (
	CollectorPrometheus = "prometheus"
	CollectorStatic     = "static"
	ScalerKubernetes    = "kubernetes"
	ScalerInMemory      = "inmemory"
)

// Config is a complete autoscaler configuration.
type Config struct {
	IntervalSeconds float64         `yaml:"interval_seconds"`
	Collector       CollectorConfig `yaml:"collector"`
	Policy          PolicyConfig    `yaml:"policy"`
	Scaler          ScalerConfig    `yaml:"scaler"`
	Exporter        ExporterConfig  `yaml:"exporter"`
}

// CollectorConfig selects and configures the metric source.
type CollectorConfig struct {
	Type           string  `yaml:"type"`
	URL            string  `yaml:"url"`
	Query          string  `yaml:"query"`
	TimeoutSeconds float64 `yaml:"timeout_seconds"`
	// Value seeds the static collector; ignored otherwise.
	Value float64 `yaml:"value"`
}

// PolicyConfig configures the scaling policy and its stabilization.
type PolicyConfig struct {
	Name        string  `yaml:"name"`
	Target      float64 `yaml:"target"`
	Tolerance   float64 `yaml:"tolerance"`
	MinReplicas int32   `yaml:"min_replicas"`
	MaxReplicas int32   `yaml:"max_replicas"`

	// Predictive is the prototype's boolean flag, kept so old config files load.
	// Prefer setting Name. If Name is empty and this is true, Name becomes
	// "predictive".
	Predictive *bool `yaml:"predictive"`

	Forecaster ForecasterConfig `yaml:"forecaster"`

	StabilizationWindowSeconds float64 `yaml:"stabilization_window_seconds"`
	// CooldownSeconds is the minimum gap between scale actions. 0 disables it.
	CooldownSeconds float64 `yaml:"cooldown_seconds"`
	// MaxStep caps replicas added or removed per decision. 0 means unlimited.
	MaxStep int32 `yaml:"max_step"`
}

// ForecasterConfig selects and tunes the forecasting method.
type ForecasterConfig struct {
	Type    string  `yaml:"type"`
	Horizon int     `yaml:"horizon"`
	Alpha   float64 `yaml:"alpha"`
	// Beta is the trend smoothing factor, used by the Holt forecaster only.
	Beta float64 `yaml:"beta"`
}

// ScalerConfig selects and configures the scale target.
type ScalerConfig struct {
	Type       string `yaml:"type"`
	Deployment string `yaml:"deployment"`
	Namespace  string `yaml:"namespace"`
	Kubeconfig string `yaml:"kubeconfig"`
	// Replicas seeds the in-memory scaler; ignored otherwise.
	Replicas int32 `yaml:"replicas"`
}

// ExporterConfig configures the autoscaler's own /metrics endpoint.
type ExporterConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

// Interval returns the reconcile period.
func (c *Config) Interval() time.Duration {
	return time.Duration(c.IntervalSeconds * float64(time.Second))
}

// StabilizationWindow returns the downscale stabilization window.
func (p *PolicyConfig) StabilizationWindow() time.Duration {
	return time.Duration(p.StabilizationWindowSeconds * float64(time.Second))
}

// Cooldown returns the minimum interval between scale actions.
func (p *PolicyConfig) Cooldown() time.Duration {
	return time.Duration(p.CooldownSeconds * float64(time.Second))
}

// Load reads and validates a configuration file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse decodes and validates a configuration from YAML.
func Parse(raw []byte) (*Config, error) {
	cfg := Default()
	// Blank the name so an explicit `name:` can be told apart from the default.
	// applyCompat restores a default below.
	cfg.Policy.Name = ""
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("config: parsing YAML: %w", err)
	}
	cfg.applyCompat()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Default returns a configuration with every optional field populated. Defaults
// match the Python prototype so behaviour is unchanged for existing files.
func Default() *Config {
	return &Config{
		IntervalSeconds: 15,
		Collector: CollectorConfig{
			Type:           CollectorPrometheus,
			URL:            "http://localhost:9090",
			TimeoutSeconds: 5,
		},
		Policy: PolicyConfig{
			Name:        PolicyThreshold,
			Tolerance:   0.10,
			MinReplicas: 1,
			MaxReplicas: 10,
			Forecaster: ForecasterConfig{
				Type:    ForecasterEWMA,
				Horizon: 3,
				Alpha:   0.5,
				Beta:    0.3,
			},
			StabilizationWindowSeconds: 90,
		},
		Scaler:   ScalerConfig{Type: ScalerKubernetes, Namespace: "default", Replicas: 1},
		Exporter: ExporterConfig{Port: 8000},
	}
}

// applyCompat resolves the policy name, mapping the prototype's deprecated
// `predictive: true` flag onto it. An explicit `name:` always wins — including an
// explicit "threshold", which must not be overridden by a stale flag left in an
// old config file.
func (c *Config) applyCompat() {
	if c.Policy.Name != "" {
		return
	}
	if c.Policy.Predictive != nil && *c.Policy.Predictive {
		c.Policy.Name = PolicyPredictive
		return
	}
	c.Policy.Name = PolicyThreshold
}

// IsPredictive reports whether the configured policy needs a forecaster.
func (p *PolicyConfig) IsPredictive() bool {
	return p.Name == PolicyPredictive || p.Name == PolicyPredictivePerReplica
}

// Validate checks the configuration is usable. Every constraint the prototype
// enforced ad hoc inside constructors is centralized here, so a bad config fails
// at start-up with one clear message rather than mid-reconcile.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.IntervalSeconds <= 0 {
		add("interval_seconds must be > 0, got %v", c.IntervalSeconds)
	}

	switch c.Collector.Type {
	case CollectorPrometheus:
		if c.Collector.Query == "" {
			add("collector.query is required for the prometheus collector")
		}
		if c.Collector.URL == "" {
			add("collector.url is required for the prometheus collector")
		}
		if c.Collector.TimeoutSeconds <= 0 {
			add("collector.timeout_seconds must be > 0, got %v", c.Collector.TimeoutSeconds)
		}
	case CollectorStatic:
	default:
		add("collector.type must be %q or %q, got %q", CollectorPrometheus, CollectorStatic, c.Collector.Type)
	}

	p := &c.Policy
	switch p.Name {
	case PolicyThreshold, PolicyPredictive, PolicyPredictivePerReplica:
	default:
		add("policy.name must be one of %q, %q, %q; got %q",
			PolicyThreshold, PolicyPredictive, PolicyPredictivePerReplica, p.Name)
	}
	if p.Target <= 0 || math.IsNaN(p.Target) || math.IsInf(p.Target, 0) {
		add("policy.target must be > 0 and finite, got %v", p.Target)
	}
	if p.Tolerance < 0 || p.Tolerance >= 1 {
		add("policy.tolerance must be in [0, 1), got %v", p.Tolerance)
	}
	if p.MinReplicas < 0 {
		add("policy.min_replicas must be >= 0, got %d", p.MinReplicas)
	}
	if p.MaxReplicas < 1 {
		add("policy.max_replicas must be >= 1, got %d", p.MaxReplicas)
	}
	if p.MinReplicas > p.MaxReplicas {
		add("policy.min_replicas (%d) must be <= policy.max_replicas (%d)", p.MinReplicas, p.MaxReplicas)
	}
	if p.StabilizationWindowSeconds < 0 {
		add("policy.stabilization_window_seconds must be >= 0, got %v", p.StabilizationWindowSeconds)
	}
	if p.CooldownSeconds < 0 {
		add("policy.cooldown_seconds must be >= 0, got %v", p.CooldownSeconds)
	}
	if p.MaxStep < 0 {
		add("policy.max_step must be >= 0, got %d", p.MaxStep)
	}

	if p.IsPredictive() {
		f := &p.Forecaster
		switch f.Type {
		case ForecasterEWMA, ForecasterHolt:
		default:
			add("policy.forecaster.type must be %q or %q, got %q", ForecasterEWMA, ForecasterHolt, f.Type)
		}
		if f.Horizon < 1 {
			add("policy.forecaster.horizon must be >= 1, got %d", f.Horizon)
		}
		if f.Alpha <= 0 || f.Alpha > 1 {
			add("policy.forecaster.alpha must be in (0, 1], got %v", f.Alpha)
		}
		if f.Type == ForecasterHolt && (f.Beta <= 0 || f.Beta > 1) {
			add("policy.forecaster.beta must be in (0, 1], got %v", f.Beta)
		}
	}

	switch c.Scaler.Type {
	case ScalerKubernetes:
		if c.Scaler.Deployment == "" {
			add("scaler.deployment is required for the kubernetes scaler")
		}
		if c.Scaler.Namespace == "" {
			add("scaler.namespace is required for the kubernetes scaler")
		}
	case ScalerInMemory:
	default:
		add("scaler.type must be %q or %q, got %q", ScalerKubernetes, ScalerInMemory, c.Scaler.Type)
	}

	if c.Exporter.Enabled && (c.Exporter.Port < 1 || c.Exporter.Port > 65535) {
		add("exporter.port must be in [1, 65535], got %d", c.Exporter.Port)
	}

	return errors.Join(errs...)
}
