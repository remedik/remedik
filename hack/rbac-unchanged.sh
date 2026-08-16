#!/usr/bin/env bash
#
# Checks the chart's central promise about permissions: remedik holds a
# permission only because a named, enabled feature needs it.
#
# Three things are verified, because each can break independently:
#
#   1. With every action disabled, the ClusterRole grants nothing on any
#      workload. If that ever stops being true, some rule has escaped the
#      catalogue and is being granted unconditionally.
#   2. Enabling one action adds rules, and they are that action's rules.
#   3. The read-only dashboard changes nothing at all.
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

FAILED=0

fail() {
	echo "FAIL: $*" >&2
	FAILED=1
}

# render <extra helm --set flags...> -> the rendered ClusterRole and Role
render() {
	helm template remedik "$CHART" \
		--namespace remedik \
		--set gateway.auth.token=rbac-check \
		--set actions.deploymentRestart.enabled=false \
		--show-only templates/rbac.yaml \
		"$@"
}

# --------------------------------------------------------------------------
# 1. Nothing enabled grants nothing on workloads
# --------------------------------------------------------------------------
render >"$WORK/none.yaml"

for forbidden in deployments statefulsets daemonsets pods jobs secrets configmaps pods/log; do
	if grep -q -- "- $forbidden\$" "$WORK/none.yaml"; then
		fail "with every action disabled, the ClusterRole still grants '$forbidden'"
	fi
done

# --------------------------------------------------------------------------
# 2. Each action grants its own rules, and only when enabled
# --------------------------------------------------------------------------
check_action() {
	local key="$1" resource="$2"
	render --set "actions.${key}.enabled=true" >"$WORK/${key}.yaml"

	if diff -q "$WORK/none.yaml" "$WORK/${key}.yaml" >/dev/null; then
		fail "enabling ${key} granted nothing; its entry in action-rbac.yaml is missing or misnamed"
		return
	fi
	if ! grep -q -- "- ${resource}\$" "$WORK/${key}.yaml"; then
		fail "enabling ${key} did not grant '${resource}'"
		return
	fi
	echo "  ${key} grants ${resource} only when enabled"
}

check_action deploymentRestart deployments
check_action workloadRestart statefulsets
check_action podDelete pods/eviction
check_action jobDelete jobs
check_action webhookCall secrets
check_action jobRun pods/log
check_action scriptRun configmaps

# --------------------------------------------------------------------------
# 3. The dashboard changes nothing
#
# It reads Remediation and RemediationStrategy resources through the
# manager's cache, which the reconciler already watches in order to
# reconcile them. A future change that quietly adds a rule behind
# `if .Values.dashboard.enabled` fails here.
# --------------------------------------------------------------------------
dashboard_off="$WORK/dashboard-off.yaml"
dashboard_on="$WORK/dashboard-on.yaml"

helm template remedik "$CHART" --namespace remedik \
	--set gateway.auth.token=rbac-check \
	--show-only templates/rbac.yaml >"$dashboard_off"
helm template remedik "$CHART" --namespace remedik \
	--set gateway.auth.token=rbac-check \
	--set dashboard.enabled=true --set dashboard.auth.token=rbac-check \
	--show-only templates/rbac.yaml >"$dashboard_on"

if diff -u "$dashboard_off" "$dashboard_on"; then
	echo "  the dashboard grants nothing"
else
	fail "enabling the read-only dashboard changed the rendered RBAC"
fi

# --------------------------------------------------------------------------
if [ "$FAILED" -ne 0 ]; then
	cat >&2 <<'EOF'

A permission is granted by something other than the action that needs it.

Every rule in the ClusterRole should come from charts/remedik/action-rbac.yaml
and appear only when its action is enabled. Either a rule has been added
outside that table, or an action's key does not match the one in values.yaml.
EOF
	exit 1
fi

echo "RBAC follows the enabled actions, and nothing else"
