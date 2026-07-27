"""Scaler backends: read the current replica count and apply a new one.

* ``InMemoryScaler`` -- keeps replicas in a Python variable; used by tests and
  the simulator.
* ``KubernetesScaler`` -- patches the ``/scale`` subresource of a Deployment on
  a real cluster. The ``kubernetes`` client is imported lazily so the rest of
  the prototype runs without it installed.
"""

from __future__ import annotations


class Scaler:
    """Interface for scaler backends."""

    def get_replicas(self) -> int:  # pragma: no cover - interface
        raise NotImplementedError

    def set_replicas(self, replicas: int) -> None:  # pragma: no cover - interface
        raise NotImplementedError


class InMemoryScaler(Scaler):
    """A scaler backed by a local integer (simulation / testing)."""

    def __init__(self, replicas: int = 1):
        self._replicas = replicas

    def get_replicas(self) -> int:
        return self._replicas

    def set_replicas(self, replicas: int) -> None:
        self._replicas = int(replicas)


class KubernetesScaler(Scaler):
    """Scales a Kubernetes Deployment via the apps/v1 scale subresource."""

    def __init__(self, name: str, namespace: str = "default", kubeconfig: str = None):
        try:
            from kubernetes import client, config  # type: ignore
        except ImportError as exc:  # pragma: no cover - depends on environment
            raise ImportError(
                "The 'kubernetes' package is required for KubernetesScaler. "
                "Install it with: pip install kubernetes"
            ) from exc

        try:
            if kubeconfig:
                config.load_kube_config(config_file=kubeconfig)
            else:
                config.load_incluster_config()
        except Exception:
            config.load_kube_config()

        self.name = name
        self.namespace = namespace
        self._apps = client.AppsV1Api()

    def get_replicas(self) -> int:
        scale = self._apps.read_namespaced_deployment_scale(self.name, self.namespace)
        return int(scale.spec.replicas or 0)

    def set_replicas(self, replicas: int) -> None:
        body = {"spec": {"replicas": int(replicas)}}
        self._apps.patch_namespaced_deployment_scale(self.name, self.namespace, body)
