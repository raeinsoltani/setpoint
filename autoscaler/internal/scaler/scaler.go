// Package scaler reads and writes the replica count of a scale target.
package scaler

import "context"

// Scale is the observed state of the target workload.
//
// Spec and Ready are deliberately separate. The Python prototype collapsed them
// (sim/autoscaler/controller.py:36 passed spec.replicas straight into the HPA
// formula), but they diverge for as long as new pods take to become ready — and
// during exactly that window the scaling signal is measured per *ready* replica.
// Using Spec as the formula base while the metric is per-Ready double-counts the
// pods that are still starting and makes the autoscaler over-scale during a ramp.
type Scale struct {
	// Spec is .spec.replicas: how many replicas are requested.
	Spec int32
	// Ready is .status.readyReplicas: how many are actually serving traffic.
	Ready int32
}

// Scaler reads the current scale of a target workload and applies a new one.
type Scaler interface {
	// Get returns the target's current spec and ready replica counts.
	Get(ctx context.Context) (Scale, error)
	// Set requests replicas for the target.
	Set(ctx context.Context, replicas int32) error
}

// InMemory is a Scaler backed by local state, for tests and the simulator.
//
// It models the startup delay that makes reactive scaling hurt: replicas
// requested via Set are not immediately Ready. Call MarkReady to promote them.
type InMemory struct {
	spec  int32
	ready int32
}

// NewInMemory returns an InMemory scaler with spec and ready both set to replicas.
func NewInMemory(replicas int32) *InMemory {
	return &InMemory{spec: replicas, ready: replicas}
}

// Get implements Scaler.
func (m *InMemory) Get(context.Context) (Scale, error) {
	return Scale{Spec: m.spec, Ready: m.ready}, nil
}

// Set implements Scaler. Scaling down takes effect immediately (pods are killed);
// scaling up only moves Spec, leaving Ready to catch up via MarkReady.
func (m *InMemory) Set(_ context.Context, replicas int32) error {
	m.spec = replicas
	if replicas < m.ready {
		m.ready = replicas
	}
	return nil
}

// MarkReady promotes pending replicas to ready, simulating pod start-up
// completing. Ready never exceeds Spec.
func (m *InMemory) MarkReady(n int32) {
	m.ready += n
	if m.ready > m.spec {
		m.ready = m.spec
	}
}
