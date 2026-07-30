BINARY  := bin/setpoint
IMAGE   := setpoint:dev
PKG     := ./...

.PHONY: build test cover vet lint image deploy undeploy dry-run clean

build:
	go build -o $(BINARY) ./cmd/setpoint

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

clean:
	rm -rf bin coverage.out
