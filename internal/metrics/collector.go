// Package metrics provides the scaling signal: a single float read from an
// external source, usually Prometheus via PromQL.
package metrics

import "context"

// Collector returns the current value of the scaling signal.
//
// The value is whatever the configured PromQL expression evaluates to — typically
// requests-per-second per ready replica, but latency or queue length work equally
// well, which is the point of driving scaling from application-level metrics
// rather than CPU.
type Collector interface {
	Read(ctx context.Context) (float64, error)
}

// Static returns a value held in memory. Used by unit tests and by the
// controller's smoke mode, where the signal is supplied rather than scraped.
type Static struct {
	value float64
}

// NewStatic returns a Static collector seeded with value.
func NewStatic(value float64) *Static {
	return &Static{value: value}
}

// Set replaces the value returned by subsequent Read calls.
func (s *Static) Set(value float64) {
	s.value = value
}

// Read implements Collector.
func (s *Static) Read(context.Context) (float64, error) {
	return s.value, nil
}
