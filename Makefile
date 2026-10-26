.DEFAULT_GOAL := help

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
NS := app-plat
ISTIO_NS := istio-system
REGISTRY := app-plat
GATEWAY_PORT ?= 30080
ADMIN_EMAIL ?= admin@app-plat.local
ADMIN_PASSWORD ?= app-plat-admin
OPENROUTER_ENV ?= $(ROOT)/../goodknight/.env

BIN_DIR := $(ROOT)/bin
CMDS := control-plane blob-gateway admin user llm-gateway app-plat

.PHONY: help tools tidy build test images namespace istio-up deps-up idp-up platform-up up down status admin-credentials e2e logs port-forward

help: ## Show targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Check local tools
	@command -v go >/dev/null || (echo "go missing" && exit 1)
	@command -v docker >/dev/null || (echo "docker missing" && exit 1)
	@command -v kubectl >/dev/null || (echo "kubectl missing" && exit 1)
	@command -v helm >/dev/null || (echo "helm missing" && exit 1)
	@echo "tools ok: $$(go version)"

tidy: ## go mod tidy
	cd $(ROOT) && go mod tidy

build: ## Build all binaries into bin/
	mkdir -p $(BIN_DIR)
	@for cmd in $(CMDS); do \
		echo "building $$cmd"; \
		cd $(ROOT) && CGO_ENABLED=0 go build -o $(BIN_DIR)/$$cmd ./cmd/$$cmd; \
	done

test: ## Targeted tests
	cd $(ROOT) && go test ./pkg/... ./internal/...

images: ## Build platform container images into the local docker engine
	docker build -t $(REGISTRY)/control-plane:dev -f deploy/docker/control-plane.Dockerfile $(ROOT)
	docker build -t $(REGISTRY)/blob-gateway:dev -f deploy/docker/service.Dockerfile --build-arg CMD=blob-gateway $(ROOT)
	docker build -t $(REGISTRY)/admin:dev -f deploy/docker/service.Dockerfile --build-arg CMD=admin $(ROOT)
	docker build -t $(REGISTRY)/user:dev -f deploy/docker/service.Dockerfile --build-arg CMD=user $(ROOT)
	docker build -t $(REGISTRY)/llm-gateway:dev -f deploy/docker/service.Dockerfile --build-arg CMD=llm-gateway $(ROOT)
	docker build -t $(REGISTRY)/pgbouncer:dev -f deploy/docker/pgbouncer.Dockerfile $(ROOT)

namespace: ## Create the platform namespace
	kubectl create ns $(NS) --dry-run=client -o yaml | kubectl apply -f -
	kubectl label ns $(NS) istio-injection=enabled app-plat.io/name=app-plat --overwrite

istio-up: namespace ## Install Istio and apply platform mesh policy
	helm repo add istio https://istio-release.storage.googleapis.com/charts >/dev/null 2>&1 || true
	helm repo update istio
	kubectl get ns $(ISTIO_NS) >/dev/null 2>&1 || kubectl create ns $(ISTIO_NS)
	helm upgrade --install istio-base istio/base -n $(ISTIO_NS) --wait
	helm upgrade --install istiod istio/istiod -n $(ISTIO_NS) --wait \
		--set pilot.resources.requests.cpu=100m \
		--set pilot.resources.requests.memory=256Mi
	helm upgrade --install istio-ingress istio/gateway -n $(ISTIO_NS) --wait
	kubectl -n $(ISTIO_NS) get svc istio-ingress -o json | python3 -c 'import json,sys; svc=json.load(sys.stdin); svc["spec"]["type"]="NodePort"; \
[p.update(nodePort=$(GATEWAY_PORT)) for p in svc["spec"]["ports"] if p.get("port")==80]; json.dump(svc, sys.stdout)' | kubectl apply -f -
	kubectl apply -f deploy/istio/

deps-up: namespace ## Install postgres, pgbouncer, valkey, rustfs, registry
	docker build -t $(REGISTRY)/pgbouncer:dev -f deploy/docker/pgbouncer.Dockerfile $(ROOT)
	kubectl apply -f deploy/k8s/postgres.yaml
	kubectl apply -f deploy/k8s/pgbouncer.yaml
	kubectl apply -f deploy/k8s/valkey.yaml
	kubectl apply -f deploy/k8s/rustfs.yaml
	kubectl apply -f deploy/k8s/registry.yaml
	kubectl -n $(NS) rollout status deploy/postgres --timeout=180s
	kubectl -n $(NS) rollout status deploy/pgbouncer --timeout=180s
	kubectl -n $(NS) rollout status deploy/valkey --timeout=180s
	kubectl -n $(NS) rollout status deploy/rustfs --timeout=180s
	kubectl -n $(NS) rollout status deploy/registry --timeout=180s

idp-up: namespace ## Install Kratos and Hydra
	kubectl apply -f deploy/k8s/rbac.yaml
	kubectl apply -f deploy/idp/idp.yaml
	kubectl -n $(NS) rollout status deploy/kratos --timeout=240s
	kubectl -n $(NS) rollout status deploy/hydra --timeout=240s

platform-up: images namespace ## Deploy control plane, gateways, and UIs
	@if [ -f $(OPENROUTER_ENV) ]; then \
		set -a; . $(OPENROUTER_ENV); set +a; \
		kubectl -n $(NS) create secret generic app-plat-openrouter \
			--from-literal=OPENROUTER_API_KEY="$$OPENROUTER_API_KEY" \
			--dry-run=client -o yaml | kubectl apply -f -; \
	else \
		echo "warning: $(OPENROUTER_ENV) missing; LLM proxy will have no upstream key"; \
		kubectl -n $(NS) create secret generic app-plat-openrouter \
			--from-literal=OPENROUTER_API_KEY="" \
			--dry-run=client -o yaml | kubectl apply -f -; \
	fi
	kubectl apply -f deploy/k8s/rbac.yaml
	kubectl apply -f deploy/k8s/platform.yaml
	kubectl -n $(NS) rollout status deploy/control-plane --timeout=240s
	kubectl -n $(NS) rollout status deploy/blob-gateway --timeout=180s
	kubectl -n $(NS) rollout status deploy/llm-gateway --timeout=180s
	kubectl -n $(NS) rollout status deploy/admin --timeout=180s
	kubectl -n $(NS) rollout status deploy/user --timeout=180s

up: tools istio-up deps-up idp-up platform-up ## Install and deploy the full platform
	@$(MAKE) admin-credentials
	@echo ""
	@echo "User UI:  http://app.127.0.0.1.nip.io:$(GATEWAY_PORT)/"
	@echo "Admin UI: http://admin.127.0.0.1.nip.io:$(GATEWAY_PORT)/"
	@echo "Login:    $(ADMIN_EMAIL) / $(ADMIN_PASSWORD)"

down: ## Delete the app-plat namespace
	kubectl delete ns $(NS) --wait=false || true

status: ## Show namespace workloads
	kubectl -n $(NS) get pods,svc,gateway,virtualservice,authorizationpolicy,networkpolicy

admin-credentials: ## Print the bootstrap admin login
	@echo "email:    $(ADMIN_EMAIL)"
	@echo "password: $(ADMIN_PASSWORD)"
	@echo "user ui:  http://app.127.0.0.1.nip.io:$(GATEWAY_PORT)/"
	@echo "admin ui: http://admin.127.0.0.1.nip.io:$(GATEWAY_PORT)/"

logs: ## Tail control-plane logs
	kubectl -n $(NS) logs deploy/control-plane --tail=200 -f

e2e: ## Validate the running platform end to end
	bash $(ROOT)/scripts/e2e.sh

port-forward: ## Fallback access if the gateway NodePort is unreachable
	kubectl -n $(NS) port-forward svc/user 8082:8082
