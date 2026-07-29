package scaler

import (
	"context"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Kubernetes scales a Deployment through the apps/v1 scale subresource.
//
// The scale subresource is used rather than patching the Deployment itself
// because it is the same narrow surface HPA uses: it needs only
// get/update on deployments/scale, so the autoscaler's RBAC cannot be used to
// rewrite a pod spec.
type Kubernetes struct {
	client     kubernetes.Interface
	name       string
	namespace  string
	timeout    time.Duration
	fieldOwner string
}

// KubernetesOptions configures a Kubernetes scaler.
type KubernetesOptions struct {
	// Deployment is the name of the target Deployment.
	Deployment string
	// Namespace is the target's namespace.
	Namespace string
	// Kubeconfig is an explicit kubeconfig path. Empty means try in-cluster
	// config first, then the ambient kubeconfig.
	Kubeconfig string
	// Timeout bounds each API call.
	Timeout time.Duration
}

// NewKubernetes returns a scaler bound to a Deployment, building a client from
// in-cluster credentials or a kubeconfig.
func NewKubernetes(opts KubernetesOptions) (*Kubernetes, error) {
	if opts.Deployment == "" {
		return nil, errors.New("scaler: kubernetes scaler requires a deployment name")
	}
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	cfg, err := restConfig(opts.Kubeconfig)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("scaler: building kubernetes client: %w", err)
	}
	return NewKubernetesWithClient(client, opts), nil
}

// NewKubernetesWithClient returns a scaler using a caller-supplied client, so
// tests can inject a fake clientset instead of reaching a cluster.
func NewKubernetesWithClient(client kubernetes.Interface, opts KubernetesOptions) *Kubernetes {
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	return &Kubernetes{
		client:     client,
		name:       opts.Deployment,
		namespace:  opts.Namespace,
		timeout:    opts.Timeout,
		fieldOwner: "prom-autoscaler",
	}
}

// restConfig resolves credentials: an explicit kubeconfig if given, otherwise
// in-cluster (the deployed case), falling back to the ambient kubeconfig (the
// developer's laptop).
func restConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("scaler: loading kubeconfig %s: %w", kubeconfig, err)
		}
		return cfg, nil
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("scaler: no in-cluster config and no usable kubeconfig: %w", err)
	}
	return cfg, nil
}

// Get implements Scaler.
//
// One Deployment read supplies both numbers: .spec.replicas and
// .status.readyReplicas. The scale subresource is not used on the read path
// because it cannot answer the second question — its status reports total
// replicas, not ready ones — and the ready count is what the policy needs as its
// formula base, since the metric is measured per serving pod. Reading the
// Deployment alone therefore halves the API calls per reconcile.
func (k *Kubernetes) Get(ctx context.Context) (Scale, error) {
	ctx, cancel := context.WithTimeout(ctx, k.timeout)
	defer cancel()

	deploy, err := k.client.AppsV1().Deployments(k.namespace).
		Get(ctx, k.name, metav1.GetOptions{})
	if err != nil {
		return Scale{}, fmt.Errorf("scaler: reading deployment %s/%s: %w", k.namespace, k.name, err)
	}

	// .spec.replicas is a pointer because it is optional; unset means the
	// API server's default of 1.
	spec := int32(1)
	if deploy.Spec.Replicas != nil {
		spec = *deploy.Spec.Replicas
	}
	return Scale{Spec: spec, Ready: deploy.Status.ReadyReplicas}, nil
}

// Set implements Scaler by updating the scale subresource.
//
// Read-modify-write on the object just fetched keeps the resourceVersion, so a
// concurrent edit by another controller causes a conflict error rather than a
// silent overwrite. The controller holds its replica count on error, and the
// next reconcile retries with fresh state.
func (k *Kubernetes) Set(ctx context.Context, replicas int32) error {
	ctx, cancel := context.WithTimeout(ctx, k.timeout)
	defer cancel()

	deployments := k.client.AppsV1().Deployments(k.namespace)
	scale, err := deployments.GetScale(ctx, k.name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("scaler: reading scale of deployment %s/%s: %w", k.namespace, k.name, err)
	}
	if scale.Spec.Replicas == replicas {
		return nil
	}
	scale.Spec.Replicas = replicas

	if _, err := deployments.UpdateScale(ctx, k.name, scale,
		metav1.UpdateOptions{FieldManager: k.fieldOwner}); err != nil {
		return fmt.Errorf("scaler: scaling deployment %s/%s to %d: %w", k.namespace, k.name, replicas, err)
	}
	return nil
}

// Target describes the scale target, for logs and metric labels.
func (k *Kubernetes) Target() string { return k.namespace + "/" + k.name }
