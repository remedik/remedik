#!/usr/bin/env bash
# Compare the versions pinned in this repo against the latest upstream
# releases. Read-only: it never changes anything. Run with `make versions`.
set -euo pipefail

cd "$(dirname "$0")/.."

green() { printf '\033[32m%s\033[0m' "$1"; }
yellow() { printf '\033[33m%s\033[0m' "$1"; }

# latest_tag <github-owner/repo> <tag-prefix-regex>
latest_tag() {
	git ls-remote --tags "https://github.com/$1" 2>/dev/null |
		grep -oE "refs/tags/$2$" |
		sed 's|refs/tags/||' |
		grep -vE '(rc|alpha|beta)' |
		sort -V | tail -1
}

row() { # row <name> <pinned> <latest>
	local status
	if [ -z "$3" ]; then
		status="$(yellow '? upstream unreachable')"
	elif [ "$2" = "$3" ]; then
		status="$(green 'up to date')"
	else
		status="$(yellow "-> $3")"
	fi
	printf '  %-24s %-14s %s\n' "$1" "$2" "$status"
}

echo "Pinned versions vs. latest upstream:"
echo

# The go directive is a MINIMUM version, so only the major.minor series is
# compared: `go 1.26.0` is current while Go 1.26.6 is the newest patch.
go_series() { echo "$1" | cut -d. -f1,2; }
go_pinned="$(grep -oE '^go [0-9.]+' go.mod | awk '{print $2}')"
go_latest="$(latest_tag golang/go 'go[0-9.]+' | sed 's/^go//')"
if [ -z "$go_latest" ]; then
	go_want=""
elif [ "$(go_series "$go_pinned")" = "$(go_series "$go_latest")" ]; then
	go_want="$go_pinned"
else
	go_want="$(go_series "$go_latest").0"
fi
row "go (min, go.mod)" "$go_pinned" "$go_want"

row "controller-gen" \
	"$(grep -oE 'CONTROLLER_GEN_VERSION := \S+' Makefile | awk '{print $3}')" \
	"$(latest_tag kubernetes-sigs/controller-tools 'v0[0-9.]+')"

row "controller-runtime" \
	"$(grep -oE 'sigs.k8s.io/controller-runtime \S+' go.mod | awk '{print $2}')" \
	"$(latest_tag kubernetes-sigs/controller-runtime 'v0[0-9.]+')"

row "golangci-lint" \
	"$(grep -oE 'GOLANGCI_LINT_VERSION := \S+' Makefile | awk '{print $3}')" \
	"$(latest_tag golangci/golangci-lint 'v2[0-9.]+')"

row "yamlfmt" \
	"$(grep -oE 'YAMLFMT_VERSION\s+:= \S+' Makefile | awk '{print $3}')" \
	"$(latest_tag google/yamlfmt 'v0[0-9.]+')"

row "helm-docs" \
	"$(grep -oE 'HELM_DOCS_VERSION\s+:= \S+' Makefile | awk '{print $3}')" \
	"$(latest_tag norwoodj/helm-docs 'v1[0-9.]+')"

row "helm" \
	"$(grep -oE 'HELM_VERSION: \S+' .github/workflows/ci.yml | awk '{print $2}')" \
	"$(latest_tag helm/helm 'v3[0-9.]+')"

row "yamllint" \
	"$(grep -oE 'YAMLLINT_VERSION: \S+' .github/workflows/ci.yml | awk '{print $2}')" \
	"$(latest_tag adrienverge/yamllint 'v[0-9.]+' | sed 's/^v//')"

row "kind (docs)" \
	"$(grep -oE 'kind v[0-9.]+' QUICKSTART.md | head -1 | awk '{print $2}')" \
	"$(latest_tag kubernetes-sigs/kind 'v0[0-9.]+')"

row "actions/checkout" \
	"$(grep -oE 'actions/checkout@\S+' .github/workflows/ci.yml | head -1 | cut -d@ -f2)" \
	"$(latest_tag actions/checkout 'v[0-9]+' )"

row "actions/setup-go" \
	"$(grep -oE 'actions/setup-go@\S+' .github/workflows/ci.yml | head -1 | cut -d@ -f2)" \
	"$(latest_tag actions/setup-go 'v[0-9]+')"

row "azure/setup-helm" \
	"$(grep -oE 'azure/setup-helm@\S+' .github/workflows/ci.yml | head -1 | cut -d@ -f2)" \
	"$(latest_tag Azure/setup-helm 'v[0-9]+')"

kps_pinned="$(grep -oE 'KPS_CHART_VERSION \?= \S+' Makefile | awk '{print $3}')"
kps_latest="$(curl -fsSL --max-time 15 \
	https://raw.githubusercontent.com/prometheus-community/helm-charts/main/charts/kube-prometheus-stack/Chart.yaml 2>/dev/null |
	grep -oE '^version: \S+' | awk '{print $2}' || true)"
row "kube-prometheus-stack" "$kps_pinned" "$kps_latest"

echo
echo "Major-version bumps for GitHub Actions and Helm charts can carry breaking"
echo "changes — read the release notes before bumping."
