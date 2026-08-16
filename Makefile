BIN     := remedik
MODULE  := github.com/remedik/remedik
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
GOVULNCHECK_VERSION   := v1.1.4
# kube-prometheus-stack chart version used by the dev cluster.
KPS_CHART_VERSION ?= 88.3.0

TOOLS_BIN := $(CURDIR)/hack/bin
CONTROLLER_GEN := $(TOOLS_BIN)/controller-gen
GOLANGCI  := $(TOOLS_BIN)/golangci-lint
YAMLFMT   := $(TOOLS_BIN)/yamlfmt
HELMDOCS  := $(TOOLS_BIN)/helm-docs
GOVULN    := $(TOOLS_BIN)/govulncheck

KIND_CLUSTER := remedik-dev

.PHONY: all build test vet fmt tidy lint yaml-lint yaml-fix helm-lint helm-docs \
        specs generate manifests verify verify-codegen tools docker-build e2e \
        dev-up dev-down dev-info dev-deploy versions clean help

all: verify build

##@ Build

build: ## Build the remedik binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/$(BIN)

test: ## Run unit tests with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Fail if any file is not gofmt'd (vendored code is not ours to format)
	@out=$$(gofmt -l $$(git ls-files '*.go')); \
		if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi

tidy: ## go mod tidy
	go mod tidy

##@ Code generation

generate: $(CONTROLLER_GEN) ## Regenerate DeepCopy methods for the API types
	@# Remove stale output first: a malformed generated file would otherwise
	@# break the package load that controller-gen itself needs.
	rm -f api/*/zz_generated.deepcopy.go
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
	@# A token is supplied because the chart refuses to render an
	@# unauthenticated gateway unless that is asked for explicitly.
	helm lint charts/remedik --set gateway.auth.token=lint
	helm template remedik charts/remedik --set gateway.auth.token=lint >/dev/null
	helm template remedik charts/remedik --set gateway.auth.disabled=true >/dev/null
	@# The dashboard is off by default and opt-in either way it is authenticated.
	helm template remedik charts/remedik --set gateway.auth.token=lint \
		--set dashboard.enabled=true --set dashboard.auth.token=lint >/dev/null
	helm template remedik charts/remedik --set gateway.auth.token=lint \
		--set dashboard.enabled=true --set dashboard.auth.disabled=true >/dev/null
	@# The NetworkPolicy renders with peers, and refuses to render without them.
	helm template remedik charts/remedik --set gateway.auth.token=lint \
		--set networkPolicy.enabled=true \
		--set 'networkPolicy.gatewayFrom[0].namespaceSelector.matchLabels.name=monitoring' >/dev/null
	@helm template remedik charts/remedik --set gateway.auth.token=lint \
		--set networkPolicy.enabled=true >/dev/null 2>&1 \
		&& { echo "the chart rendered a NetworkPolicy that would stop Alertmanager"; exit 1; } \
		|| echo "chart refuses a NetworkPolicy with no peers, as intended"
	@# The observability bundle renders on and off.
	helm template remedik charts/remedik --set gateway.auth.token=lint \
		--set serviceMonitor.enabled=true --set prometheusRule.enabled=true \
		--set grafanaDashboard.enabled=true >/dev/null
	@# Enabling it without a way to authenticate must fail, not render.
	@helm template remedik charts/remedik --set gateway.auth.token=lint \
		--set dashboard.enabled=true >/dev/null 2>&1 \
		&& { echo "the chart rendered an unauthenticated dashboard without being asked"; exit 1; } \
		|| echo "chart refuses an unauthenticated dashboard, as intended"
	./hack/rbac-unchanged.sh

helm-docs: $(HELMDOCS) ## Regenerate chart README.md from values.yaml annotations
	$(HELMDOCS) --chart-search-root charts

verify: fmt vet lint yaml-lint helm-lint verify-docs specs vuln test ## Everything CI runs

# Advisories against the toolchain go.mod pins, which is what CI installs.
# It was CI-only, so the eleven standard-library advisories in go1.26.0 were
# invisible here — the local toolchain happened to be newer, which is the
# worst way for a check to pass. `verify` says it is everything CI runs; it
# is only worth saying if it is true.
vuln: $(GOVULN) ## Check for known vulnerabilities
	$(GOVULN) ./...

specs: ## Check that the spec-first workflow was followed
	./hack/openspec-check.sh

verify-codegen: generate manifests ## Fail if generated code or CRDs are stale
	@git diff --exit-code api/ charts/remedik/crds/ \
		|| { echo "generated files are stale — run 'make generate manifests' and commit"; exit 1; }

# The chart README is generated from README.md.gotmpl and values.yaml. Editing
# the generated file works until somebody runs helm-docs, and then the edit is
# gone — silently, and usually in somebody else's commit. CI has always caught
# this; `verify` claims to be everything CI runs, so it catches it too.
#
# It compares regeneration against itself rather than against git, so it says
# the same thing in a clean checkout and in a working tree with uncommitted
# changes. A check that fails on correct-but-unstaged work is a check people
# learn to skip.
verify-docs: $(HELMDOCS) ## Fail if the chart README does not match its template
	@cp charts/remedik/README.md $(TOOLS_BIN)/README.before
	@$(HELMDOCS) --chart-search-root charts
	@diff -u $(TOOLS_BIN)/README.before charts/remedik/README.md \
		|| { cp $(TOOLS_BIN)/README.before charts/remedik/README.md; \
		     echo "charts/remedik/README.md is generated: edit README.md.gotmpl or values.yaml, then run 'make helm-docs'"; \
		     rm -f $(TOOLS_BIN)/README.before; exit 1; }
	@rm -f $(TOOLS_BIN)/README.before
	@echo "chart README matches its template"

##@ Tools (installed pinned, into hack/bin)

tools: $(CONTROLLER_GEN) $(GOLANGCI) $(YAMLFMT) $(HELMDOCS) $(GOVULN) ## Install all pinned dev tools

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

$(GOVULN):
	GOBIN=$(TOOLS_BIN) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

##@ Container image

IMAGE_REPO ?= ghcr.io/remedik/remedik
IMAGE_TAG  ?= $(VERSION)

docker-build: ## Build the container image
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) --build-arg VERSION=$(VERSION) .

##@ Testing

e2e: ## Run the end-to-end test on a throwaway kind cluster
	./hack/e2e.sh

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
	@echo "Deploy remedik into this cluster with: make dev-deploy"

# The action set here is "everything reversible or bounded", plus
# webhook.call — which is an escape hatch and off by default in the chart,
# but is what the escalation path is made of, and a dev cluster where the
# escalation cannot be seen working is a dev cluster that hides the feature
# most worth trying before production. node.drain, job.run and script.run
# stay off: those want a deliberate decision, not a default.
dev-deploy: docker-build ## Build, load and install remedik into the dev cluster
	kind load docker-image $(IMAGE_REPO):$(IMAGE_TAG) --name $(KIND_CLUSTER)
	@# Helm installs the files in crds/ once and never touches them again, so
	@# `helm upgrade` leaves a stale CRD behind and every new API field is
	@# rejected with "unknown field". That is documented for operators in
	@# charts/remedik/README.md; here it is simply done, because a dev loop
	@# that breaks silently on every api/ change is not a dev loop.
	@# --force-conflicts because the initial `helm install` recorded itself as
	@# the field manager, and taking that over is exactly the intent.
	kubectl apply --server-side --force-conflicts -f charts/remedik/crds/
	helm upgrade --install remedik charts/remedik \
		--namespace remedik --create-namespace \
		--set image.repository=$(IMAGE_REPO) --set image.tag=$(IMAGE_TAG) \
		--set image.pullPolicy=IfNotPresent \
		--set gateway.auth.token=dev-token \
		--set clusterName=dev-kind \
		--set namespacePosture.payments=live \
		--set dashboard.enabled=true --set dashboard.auth.token=dev-token \
		--set actions.workloadRestart.enabled=true \
		--set actions.podDelete.enabled=true \
		--set actions.jobDelete.enabled=true \
		--set actions.deploymentRollback.enabled=true \
		--set actions.deploymentScale.enabled=true \
		--set actions.hpaScale.enabled=true \
		--set actions.nodeCordon.enabled=true \
		--set actions.nodeUncordon.enabled=true \
		--set actions.webhookCall.enabled=true \
		--set guards.blastRadius.enabled=true \
		--set serviceMonitor.enabled=true \
		--set serviceMonitor.additionalLabels.release=monitoring \
		--set prometheusRule.enabled=true \
		--set prometheusRule.additionalLabels.release=monitoring \
		--set grafanaDashboard.enabled=true \
		--set grafanaDashboard.namespace=monitoring \
		--wait --timeout 3m
	@# The tag comes from `git describe`, so an uncommitted change rebuilds
	@# the same tag with different contents and helm sees no diff to roll
	@# out. Without this, `make dev-deploy` silently leaves the old binary
	@# running and the next twenty minutes are spent debugging a fix that
	@# was never deployed.
	kubectl -n remedik rollout restart deploy/remedik
	kubectl -n remedik rollout status deploy/remedik --timeout=120s
	@echo ""
	@echo "remedik is installed in dry-run mode. Watch it with:"
	@echo "  kubectl -n remedik get remediations -w"
	@echo "  kubectl -n remedik logs deploy/remedik -f"
	@echo ""
	@echo "Dashboard (read-only): kubectl -n remedik port-forward svc/remedik-dashboard 8082:8082"
	@echo "  -> http://127.0.0.1:8082/  (username blank, password: dev-token)"
	@echo ""
	@echo "Prometheus now scrapes remedik, and Grafana has the 'remedik' dashboard."
	@echo "  Grafana: kubectl port-forward -n monitoring svc/monitoring-grafana 3000:80  (admin / remedik-dev)"

dev-down: ## Delete the kind dev cluster
	kind delete cluster --name $(KIND_CLUSTER)

##@ Misc

versions: ## Show pinned vs. latest upstream versions of every tool
	@hack/versions.sh

clean: ## Remove build output
	rm -rf bin

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^##@/ {printf "\n%s\n", substr($$0, 5)} /^[a-zA-Z0-9_-]+:.*?##/ {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
