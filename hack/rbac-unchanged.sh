#!/usr/bin/env bash
#
# Proves that enabling the dashboard grants remedik nothing.
#
# The chart's rule is that a permission exists only because a named, enabled
# feature needs it. The dashboard needs none: it reads Remediation and
# RemediationStrategy resources through the manager's cache, which the
# operator already watches in order to reconcile them.
#
# That is a claim about rendered manifests, so it is checked as one: the
# Role and ClusterRole must come out byte-identical with the dashboard on
# and off. A future change that quietly adds a rule behind
# `if .Values.dashboard.enabled` fails here.
#
# Usage:  hack/rbac-unchanged.sh   (run by `make helm-lint`)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART="$REPO_ROOT/charts/remedik"

command -v helm >/dev/null || {
	echo "helm not found — install: https://helm.sh/docs/intro/install/" >&2
	exit 1
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

render() {
	helm template remedik "$CHART" \
		--namespace remedik \
		--set gateway.auth.token=rbac-check \
		--show-only templates/rbac.yaml \
		"$@"
}

render >"$WORK/dashboard-off.yaml"
render --set dashboard.enabled=true --set dashboard.auth.token=rbac-check >"$WORK/dashboard-on.yaml"

if diff -u "$WORK/dashboard-off.yaml" "$WORK/dashboard-on.yaml"; then
	echo "RBAC is identical with the dashboard enabled and disabled"
	exit 0
fi

cat >&2 <<'EOF'

The dashboard changed the rendered RBAC.

Enabling a read-only page must not widen what remedik may do in a cluster.
Either the new rule belongs to a feature that is not the dashboard, or the
dashboard is reading something it should not.
EOF
exit 1
