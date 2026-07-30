package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The prototype's config.yaml must keep loading unchanged — it is checked into
// sim/ and referenced by the thesis, so breaking it would invalidate documented
// behaviour rather than just inconveniencing a user.
func TestParsePrototypeConfigStillLoads(t *testing.T) {
	path := filepath.Join("..", "..", "..", "sim", "config.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prototype config not present at %s: %v", path, err)
	}

	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("prototype config failed to load: %v", err)
	}

	if cfg.IntervalSeconds != 15 {
		t.Errorf("interval_seconds = %v, want 15", cfg.IntervalSeconds)
	}
	if cfg.Collector.Type != CollectorPrometheus {
		t.Errorf("collector.type = %q, want %q", cfg.Collector.Type, CollectorPrometheus)
	}
	if !strings.Contains(cfg.Collector.Query, "http_requests_total") {
		t.Errorf("collector.query did not survive parsing: %q", cfg.Collector.Query)
	}
	// The file sets both `name: predictive` and the deprecated `predictive: true`.
	if cfg.Policy.Name != PolicyPredictive {
		t.Errorf("policy.name = %q, want %q", cfg.Policy.Name, PolicyPredictive)
	}
	if cfg.Policy.Target != 100 {
		t.Errorf("policy.target = %v, want 100", cfg.Policy.Target)
	}
	if cfg.Policy.MaxReplicas != 10 {
		t.Errorf("policy.max_replicas = %d, want 10", cfg.Policy.MaxReplicas)
	}
	if cfg.Policy.StabilizationWindowSeconds != 90 {
		t.Errorf("stabilization_window_seconds = %v, want 90", cfg.Policy.StabilizationWindowSeconds)
	}
	if cfg.Policy.Forecaster.Horizon != 3 || cfg.Policy.Forecaster.Alpha != 0.5 {
		t.Errorf("forecaster = %+v, want horizon 3 alpha 0.5", cfg.Policy.Forecaster)
	}
	// Not specified by the old file, so it must come from defaults.
	if cfg.Policy.Forecaster.Type != ForecasterEWMA {
		t.Errorf("forecaster.type = %q, want default %q", cfg.Policy.Forecaster.Type, ForecasterEWMA)
	}
}

// `predictive: true` predates the named-policy field and must still select the
// corrected total-load policy, not the flawed per-replica one.
func TestDeprecatedPredictiveFlag(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"flag true selects predictive", "policy:\n  target: 100\n  predictive: true\n", PolicyPredictive},
		{"flag false stays threshold", "policy:\n  target: 100\n  predictive: false\n", PolicyThreshold},
		{"absent flag stays threshold", "policy:\n  target: 100\n", PolicyThreshold},
		{"explicit name wins over flag", "policy:\n  target: 100\n  predictive: true\n  name: predictive-per-replica\n", PolicyPredictivePerReplica},
		// A stale `predictive: true` left in an old file must not silently
		// override a deliberate `name: threshold`.
		{"explicit threshold beats flag", "policy:\n  target: 100\n  name: threshold\n  predictive: true\n", PolicyThreshold},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml + staticBackends))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if cfg.Policy.Name != tt.want {
				t.Errorf("policy.name = %q, want %q", cfg.Policy.Name, tt.want)
			}
		})
	}
}

func TestValidateRejectsBadConfigs(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"zero target", "policy:\n  target: 0\n", "policy.target"},
		{"negative target", "policy:\n  target: -5\n", "policy.target"},
		{"min above max", "policy:\n  target: 100\n  min_replicas: 9\n  max_replicas: 3\n", "min_replicas"},
		{"tolerance too large", "policy:\n  target: 100\n  tolerance: 1.5\n", "tolerance"},
		{"unknown policy", "policy:\n  target: 100\n  name: magic\n", "policy.name"},
		{"alpha out of range", "policy:\n  target: 100\n  name: predictive\n  forecaster:\n    alpha: 0\n", "alpha"},
		{"alpha above one", "policy:\n  target: 100\n  name: predictive\n  forecaster:\n    alpha: 1.5\n", "alpha"},
		{"zero horizon", "policy:\n  target: 100\n  name: predictive\n  forecaster:\n    horizon: 0\n", "horizon"},
		{"unknown forecaster", "policy:\n  target: 100\n  name: predictive\n  forecaster:\n    type: lstm\n", "forecaster.type"},
		{"negative cooldown", "policy:\n  target: 100\n  cooldown_seconds: -1\n", "cooldown"},
		{"zero interval", "interval_seconds: 0\npolicy:\n  target: 100\n", "interval_seconds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml + staticBackends))
			if err == nil {
				t.Fatalf("expected an error mentioning %q, got none", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

// Validation reports every problem at once, so a misconfigured file needs one
// fix-and-retry cycle rather than one per mistake.
func TestValidateReportsAllErrors(t *testing.T) {
	_, err := Parse([]byte("interval_seconds: -1\npolicy:\n  target: 0\n  tolerance: 9\n" + staticBackends))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"interval_seconds", "policy.target", "tolerance"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("joined error missing %q; got: %v", want, err)
		}
	}
}

func TestPrometheusCollectorRequiresQuery(t *testing.T) {
	_, err := Parse([]byte("policy:\n  target: 100\nscaler:\n  type: inmemory\n"))
	if err == nil || !strings.Contains(err.Error(), "collector.query") {
		t.Fatalf("expected a missing-query error, got %v", err)
	}
}

func TestKubernetesScalerRequiresDeployment(t *testing.T) {
	_, err := Parse([]byte("policy:\n  target: 100\ncollector:\n  type: static\nscaler:\n  type: kubernetes\n"))
	if err == nil || !strings.Contains(err.Error(), "scaler.deployment") {
		t.Fatalf("expected a missing-deployment error, got %v", err)
	}
}

func TestDurationHelpers(t *testing.T) {
	cfg, err := Parse([]byte("interval_seconds: 20\npolicy:\n  target: 100\n  stabilization_window_seconds: 90\n  cooldown_seconds: 30\n" + staticBackends))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.Interval().Seconds(); got != 20 {
		t.Errorf("Interval() = %vs, want 20s", got)
	}
	if got := cfg.Policy.StabilizationWindow().Seconds(); got != 90 {
		t.Errorf("StabilizationWindow() = %vs, want 90s", got)
	}
	if got := cfg.Policy.Cooldown().Seconds(); got != 30 {
		t.Errorf("Cooldown() = %vs, want 30s", got)
	}
}

// staticBackends supplies collector and scaler sections so policy-focused cases
// fail only on the policy field under test.
const staticBackends = "collector:\n  type: static\nscaler:\n  type: inmemory\n"
