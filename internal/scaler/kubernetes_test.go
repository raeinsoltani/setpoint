package scaler

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newFakeDeployment builds a Deployment whose spec and status replica counts
// differ, which is the state that exists throughout every scale-up.
func newFakeDeployment(spec, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: &spec},
		Status:     appsv1.DeploymentStatus{Replicas: spec, ReadyReplicas: ready},
	}
}

func newFakeScaler(t *testing.T, spec, ready int32) (*Kubernetes, *fake.Clientset) {
	t.Helper()
	client := fake.NewSimpleClientset(newFakeDeployment(spec, ready))
	installScaleSubresource(t, client)
	return NewKubernetesWithClient(client, KubernetesOptions{
		Deployment: "sample", Namespace: "default",
	}), client
}

// installScaleSubresource teaches the fake clientset to serve deployments/scale.
//
// The generated fake does not implement it: its object tracker returns the
// Deployment for a "scale" subresource get, and GetScale then type-asserts that
// to *autoscalingv1.Scale and panics. These reactors project the tracked
// Deployment onto a Scale and write updates back, which is what the real API
// server does.
func installScaleSubresource(t *testing.T, client *fake.Clientset) {
	t.Helper()
	tracker := client.Tracker()

	deploymentFor := func(ns, name string) (*appsv1.Deployment, error) {
		obj, err := tracker.Get(appsv1.SchemeGroupVersion.WithResource("deployments"), ns, name)
		if err != nil {
			return nil, err
		}
		return obj.(*appsv1.Deployment), nil
	}

	scaleOf := func(d *appsv1.Deployment) *autoscalingv1.Scale {
		replicas := int32(1)
		if d.Spec.Replicas != nil {
			replicas = *d.Spec.Replicas
		}
		return &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name: d.Name, Namespace: d.Namespace, ResourceVersion: d.ResourceVersion,
			},
			Spec:   autoscalingv1.ScaleSpec{Replicas: replicas},
			Status: autoscalingv1.ScaleStatus{Replicas: d.Status.Replicas},
		}
	}

	client.PrependReactor("get", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		get, ok := action.(k8stesting.GetAction)
		if !ok || action.GetSubresource() != "scale" {
			return false, nil, nil
		}
		d, err := deploymentFor(get.GetNamespace(), get.GetName())
		if err != nil {
			return true, nil, err
		}
		return true, scaleOf(d), nil
	})

	client.PrependReactor("update", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		update, ok := action.(k8stesting.UpdateAction)
		if !ok || action.GetSubresource() != "scale" {
			return false, nil, nil
		}
		scale, ok := update.GetObject().(*autoscalingv1.Scale)
		if !ok {
			return false, nil, nil
		}
		d, err := deploymentFor(scale.Namespace, scale.Name)
		if err != nil {
			return true, nil, err
		}
		d = d.DeepCopy()
		replicas := scale.Spec.Replicas
		d.Spec.Replicas = &replicas
		if err := tracker.Update(appsv1.SchemeGroupVersion.WithResource("deployments"), d, d.Namespace); err != nil {
			return true, nil, err
		}
		return true, scaleOf(d), nil
	})
}

// Get must report spec and ready separately. Collapsing them is the prototype's
// bug (sim/autoscaler/controller.py:36): the scale subresource alone cannot
// answer how many pods are actually serving, which is what the metric is
// measured against.
func TestGetReportsSpecAndReadySeparately(t *testing.T) {
	sc, _ := newFakeScaler(t, 5, 2)

	got, err := sc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec != 5 {
		t.Errorf("Spec = %d, want 5", got.Spec)
	}
	if got.Ready != 2 {
		t.Errorf("Ready = %d, want 2 — reading it from the scale subresource would have given 5", got.Ready)
	}
}

func TestSetUpdatesScaleSubresource(t *testing.T) {
	sc, client := newFakeScaler(t, 2, 2)

	if err := sc.Set(context.Background(), 7); err != nil {
		t.Fatalf("Set: %v", err)
	}

	scale, err := client.AppsV1().Deployments("default").
		GetScale(context.Background(), "sample", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("GetScale: %v", err)
	}
	if scale.Spec.Replicas != 7 {
		t.Errorf("replicas = %d after Set(7), want 7", scale.Spec.Replicas)
	}
}

// Writing a value that is already set would produce a pointless API call and a
// spurious resourceVersion bump on every reconcile that holds.
func TestSetIsANoOpWhenUnchanged(t *testing.T) {
	sc, client := newFakeScaler(t, 3, 3)
	client.ClearActions()

	if err := sc.Set(context.Background(), 3); err != nil {
		t.Fatalf("Set: %v", err)
	}

	for _, action := range client.Actions() {
		if action.GetVerb() == "update" {
			t.Error("Set issued an update for an unchanged replica count")
		}
	}
}

func TestGetOnMissingDeploymentErrors(t *testing.T) {
	sc := NewKubernetesWithClient(fake.NewSimpleClientset(), KubernetesOptions{
		Deployment: "absent", Namespace: "default",
	})
	if _, err := sc.Get(context.Background()); err == nil {
		t.Error("Get succeeded against a Deployment that does not exist")
	}
}

func TestNewKubernetesRequiresDeployment(t *testing.T) {
	if _, err := NewKubernetes(KubernetesOptions{Namespace: "default"}); err == nil {
		t.Error("NewKubernetes accepted an empty deployment name")
	}
}

func TestTargetString(t *testing.T) {
	sc, _ := newFakeScaler(t, 1, 1)
	if got := sc.Target(); got != "default/sample" {
		t.Errorf("Target() = %q, want %q", got, "default/sample")
	}
}

// The in-memory scaler models pod start-up: requesting more replicas moves Spec
// but leaves Ready behind, which is what makes reactive scaling cost latency and
// therefore what the simulator needs in order to be honest.
func TestInMemoryModelsStartupDelay(t *testing.T) {
	m := NewInMemory(2)

	if err := m.Set(context.Background(), 5); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ := m.Get(context.Background())
	if got.Spec != 5 || got.Ready != 2 {
		t.Errorf("after scale-up: spec=%d ready=%d, want 5 and 2", got.Spec, got.Ready)
	}

	m.MarkReady(2)
	got, _ = m.Get(context.Background())
	if got.Ready != 4 {
		t.Errorf("Ready = %d after MarkReady(2), want 4", got.Ready)
	}

	// Ready must never exceed Spec, however many are promoted.
	m.MarkReady(10)
	got, _ = m.Get(context.Background())
	if got.Ready != 5 {
		t.Errorf("Ready = %d, want it capped at Spec 5", got.Ready)
	}
}

// Scaling down takes effect at once: pods are killed immediately, so there is no
// equivalent of start-up delay in that direction.
func TestInMemoryScaleDownIsImmediate(t *testing.T) {
	m := NewInMemory(5)
	if err := m.Set(context.Background(), 2); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ := m.Get(context.Background())
	if got.Spec != 2 || got.Ready != 2 {
		t.Errorf("after scale-down: spec=%d ready=%d, want 2 and 2", got.Spec, got.Ready)
	}
}
