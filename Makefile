BINARY  := bin/setpoint
IMAGE   := setpoint:dev
SAMPLE  := bin/sample-app
SAMPLE_IMAGE := sample-app:dev
PKG     := ./...
MON_NS  := monitoring
PATTERN ?= spike

.PHONY: build build-sample test cover vet lint image sample-image deploy undeploy \
        dry-run clean monitoring servicemonitors dashboard deploy-sample \
        undeploy-sample stack-up stack-down grafana prometheus load calibrate

build:
	go build -o $(BINARY) ./cmd/setpoint

build-sample:
	go build -o $(SAMPLE) ./cmd/sample-app

vet:
	go vet $(PKG)

test:
	go test $(PKG)

# internal/policy is the package defended orally, so its coverage is the number
# that matters; the plan sets a floor of 80%.
cover:
	go test $(PKG) -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -20

image:
	docker build -t $(IMAGE) .

deploy: image
	kubectl apply -f deploy/setpoint/rbac.yaml
	kubectl apply -f deploy/setpoint/configmap.yaml
	kubectl apply -f deploy/setpoint/deployment.yaml
	kubectl rollout status deployment/setpoint --timeout=90s

undeploy:
	kubectl delete -f deploy/setpoint/deployment.yaml --ignore-not-found
	kubectl delete -f deploy/setpoint/configmap.yaml --ignore-not-found
	kubectl delete -f deploy/setpoint/rbac.yaml --ignore-not-found

# Decide and log against the real cluster without touching any replica count.
dry-run: build
	./$(BINARY) --config config.yaml --dry-run --log-level=debug

# ---------------------------------------------------------------------------
# Phase 4 — evaluation stack
# ---------------------------------------------------------------------------

sample-image:
	docker build -t $(SAMPLE_IMAGE) -f deploy/sample-app/Dockerfile .

deploy-sample: sample-image
	kubectl apply -f deploy/sample-app/deployment.yaml
	kubectl rollout status deployment/sample --timeout=90s

undeploy-sample:
	kubectl delete -f deploy/sample-app/deployment.yaml --ignore-not-found

monitoring:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
	helm repo update prometheus-community
	helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
	  -n $(MON_NS) --create-namespace -f deploy/prometheus/values.yaml --wait --timeout 10m

# The dashboard JSON is checked in once, at deploy/grafana/dashboards/, and the
# ConfigMap is generated from it here. Embedding the same JSON in a committed
# ConfigMap would mean two copies that drift.
dashboard:
	kubectl create configmap setpoint-dashboard \
	  --from-file=autoscaler.json=deploy/grafana/dashboards/autoscaler.json \
	  -n $(MON_NS) --dry-run=client -o yaml \
	  | kubectl label --local -f - grafana_dashboard=1 -o yaml \
	  | kubectl apply -f -

# Requires the Prometheus Operator CRDs, so it must follow `make monitoring`.
servicemonitors:
	kubectl apply -f deploy/prometheus/servicemonitors.yaml

stack-up: monitoring dashboard deploy-sample servicemonitors deploy
	@echo
	@echo "Stack up. Next:"
	@echo "  make grafana     # then open http://localhost:3000 (admin/admin)"
	@echo "  make load PATTERN=spike"

stack-down: undeploy undeploy-sample
	kubectl delete -f deploy/prometheus/servicemonitors.yaml --ignore-not-found
	helm uninstall monitoring -n $(MON_NS) || true

grafana:
	@echo "Grafana: http://localhost:3000  (admin/admin)"
	kubectl port-forward -n $(MON_NS) svc/monitoring-grafana 3000:80

prometheus:
	@echo "Prometheus: http://localhost:9090"
	kubectl port-forward -n $(MON_NS) svc/monitoring-kube-prometheus-prometheus 9090:9090

# Drives load at the sample app through a port-forward. Run `make grafana` in
# another shell to watch the replica count move.
#   make load PATTERN=spike|diurnal|bursty|ramp
# No port-forward: svc/sample is a LoadBalancer, so it is reachable on localhost
# and kube-proxy balances across every ready replica. A port-forward would pin the
# load to one pod and cap the run at a single replica's capacity.
load:
	@command -v k6 >/dev/null || { echo "k6 not installed: brew install k6"; exit 1; }
	k6 run test/load/$(PATTERN).js

# Measures the sample app's real capacity per replica, to check the 2ms-per-request
# calibration that makes hpa-cpu and ours-threshold aim at the same setpoint. See
# the note at the top of cmd/sample-app/main.go.
calibrate: build-sample
	@echo "Serving on :8080 with 1 CPU; drive it with k6 and compare req/s to 100."
	GOMAXPROCS=1 ./$(SAMPLE) --addr :8080

clean:
	rm -rf bin coverage.out
