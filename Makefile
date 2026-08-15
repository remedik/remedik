BIN     := remedik
MODULE  := github.com/ratyx/remedik
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE)/internal/version.version=$(VERSION)

# ---------------------------------------------------------------------------
# Pinned tool versions — keep in sync with .github/workflows/ci.yml.
# External prerequisites (install yourself; see QUICKSTART.md):
#   helm v3.21.4 · kind v0.32.0 · yamllint 1.38.0 · kubectl (stable)
# Check for newer releases with: make versions
# ---------------------------------------------------------------------------
CONTROLLER_GEN_VERSION := v0.21.0
GOLANGCI_LINT_VERSION := v2.12.2
YAMLFMT_VERSION       := v0.21.0
HELM_DOCS_VERSION     := v1.14.2
# kube-prometheus-stack chart version used by the dev cluster.
KPS_CHART_VERSION ?= 88.3.0

TOOLS_BIN := $(CURDIR)/hack/bin
CONTROLLER_GEN := $(TOOLS_BIN)/controller-gen
GOLANGCI  := $(TOOLS_BIN)/golangci-lint
YAMLFMT   := $(TOOLS_BIN)/yamlfmt
HELMDOCS  := $(TOOLS_BIN)/helm-docs

KIND_CLUSTER := remedik-dev

.PHONY: all build test vet fmt tidy lint yaml-lint yaml-fix helm-lint helm-docs \
        generate manifests verify verify-codegen tools dev-up dev-down dev-info versions clean help

all: verify build

##@ Build

build: ## Build the remedik binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/$(BIN)

test: ## Run unit tests with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Fail if any file is not gofmt'd
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi

tidy: ## go mod tidy
	go mod tidy

##@ Code generation

generate: $(CONTROLLER_GEN) ## Regenerate DeepCopy methods for the API types
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

manifests: $(CONTROLLER_GEN) ## Regenerate CRD manifests into the chart
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=charts/remedik/crds

##@ Lint & docs

lint: $(GOLANGCI) ## Run golangci-lint
	$(GOLANGCI) run

yaml-lint: ## Lint YAML files (requires yamllint: sudo apt install yamllint, or pip install yamllint)
	@command -v yamllint >/dev/null || { echo "yamllint not found — install with: sudo apt install yamllint  (or: pip install yamllint)"; exit 1; }
	yamllint .

yaml-fix: $(YAMLFMT) ## Auto-format YAML files in place
	$(YAMLFMT) .

helm-lint: ## Lint the Helm chart (requires helm)
	@command -v helm >/dev/null || { echo "helm not found — install: https://helm.sh/docs/intro/install/"; exit 1; }
	helm lint charts/remedik

helm-docs: $(HELMDOCS) ## Regenerate chart README.md from values.yaml annotations
	$(HELMDOCS) --chart-search-root charts

verify: fmt vet lint yaml-lint helm-lint test ## Everything CI runs

verify-codegen: generate manifests ## Fail if generated code or CRDs are stale
	@git diff --exit-code api/ charts/remedik/crds/ \
		|| { echo "generated files are stale — run 'make generate manifests' and commit"; exit 1; }

##@ Tools (installed pinned, into hack/bin)

tools: $(CONTROLLER_GEN) $(GOLANGCI) $(YAMLFMT) $(HELMDOCS) ## Install all pinned dev tools

$(CONTROLLER_GEN):
	@mkdir -p $(TOOLS_BIN)
	GOBIN=$(TOOLS_BIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

$(GOLANGCI):
	@mkdir -p $(TOOLS_BIN)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
		| sh -s -- -b $(TOOLS_BIN) $(GOLANGCI_LINT_VERSION)

$(YAMLFMT):
	@mkdir -p $(TOOLS_BIN)
	GOBIN=$(TOOLS_BIN) go install github.com/google/yamlfmt/cmd/yamlfmt@$(YAMLFMT_VERSION)

$(HELMDOCS):
	@mkdir -p $(TOOLS_BIN)
	GOBIN=$(TOOLS_BIN) go install github.com/norwoodj/helm-docs/cmd/helm-docs@$(HELM_DOCS_VERSION)

##@ Dev cluster (kind + Prometheus/Alertmanager/Grafana)

dev-up: ## Create the kind dev cluster and install the monitoring stack
	@command -v docker >/dev/null || { echo "docker not found — dev cluster needs Docker (Docker Desktop with WSL integration, or docker-ce in WSL)"; exit 1; }
	@command -v kind >/dev/null || { echo "kind not found — install: https://kind.sigs.k8s.io/docs/user/quick-start/#installation"; exit 1; }
	@command -v kubectl >/dev/null || { echo "kubectl not found — install: https://kubernetes.io/docs/tasks/tools/"; exit 1; }
	@command -v helm >/dev/null || { echo "helm not found — install: https://helm.sh/docs/intro/install/"; exit 1; }
	@kind get clusters 2>/dev/null | grep -qx $(KIND_CLUSTER) \
		|| kind create cluster --config hack/dev/kind.yaml
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
	helm repo update prometheus-community >/dev/null
	helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
		--namespace monitoring --create-namespace \
		--version $(KPS_CHART_VERSION) \
		-f hack/dev/monitoring-values.yaml --wait --timeout 10m
	@$(MAKE) --no-print-directory dev-info

dev-info: ## Show how to reach the dev cluster UIs
	@echo ""
	@echo "Dev cluster '$(KIND_CLUSTER)' — UIs (each port-forward in its own terminal):"
	@echo "  Grafana:      kubectl port-forward -n monitoring svc/monitoring-grafana 3000:80                          -> http://localhost:3000 (admin / remedik-dev)"
	@echo "  Prometheus:   kubectl port-forward -n monitoring svc/monitoring-kube-prometheus-prometheus 9090:9090     -> http://localhost:9090"
	@echo "  Alertmanager: kubectl port-forward -n monitoring svc/monitoring-kube-prometheus-alertmanager 9093:9093   -> http://localhost:9093"
	@echo "  (service names may vary — check with: kubectl get svc -n monitoring)"
	@echo ""
	@echo "remedik itself deploys here once 'add-mvp-core' ships (make dev-deploy will appear then)."

dev-down: ## Delete the kind dev cluster
	kind delete cluster --name $(KIND_CLUSTER)

##@ Misc

versions: ## Show pinned vs. latest upstream versions of every tool
	@hack/versions.sh

clean: ## Remove build output
	rm -rf bin

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^##@/ {printf "\n%s\n", substr($$0, 5)} /^[a-zA-Z_-]+:.*?##/ {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
