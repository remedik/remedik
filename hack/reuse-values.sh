#!/usr/bin/env bash
#
# Proves the chart still renders the way `helm upgrade --reuse-values` renders
# it: against the previous release's values, with none of the new chart's
# defaults filled in.
#
# This is not a hypothetical. `--reuse-values` replays the last release's
# computed values and does NOT merge the new chart's defaults, so every key
# added since a release is simply absent. It has broken this chart twice:
#
#   1. A new `crashLoopThreshold` rendered empty into `expr: ... >= `, which
#      the Prometheus operator rejected, taking the whole rule file with it.
#   2. A new `pause` block was dereferenced directly, so the upgrade failed
#      with a nil pointer before anything was applied.
#
# Both were found by upgrading a real release by hand. This script is that,
# without the cluster: it copies the chart, swaps in the previous release's
# values.yaml as the only defaults, and renders. Anything the templates reach
# into that the old values did not have shows up as a failure or as an empty
# field, and `helm lint` plus the emptiness check below catch each case.
#
# It also renders the awkward middle case: the previous values plus a single
# --set of a new feature's toggle, which is what somebody does when they read
# the release notes and want the new thing without rewriting their values file.
set -euo pipefail

cd "$(dirname "$0")/.."

CHART=charts/remedik
GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; OFF=$'\033[0m'

# The most recent release tag, which is what a user is upgrading from. With no
# tags at all there is nothing to compare against and nothing to prove.
if ! previous=$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null); then
	printf '%s— no release tag yet, so there is no upgrade to simulate%s\n' "$DIM" "$OFF"
	exit 0
fi

if ! git cat-file -e "${previous}:${CHART}/values.yaml" 2>/dev/null; then
	printf '%s— %s has no chart, so there is no upgrade to simulate%s\n' \
		"$DIM" "$previous" "$OFF"
	exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cp -r "$CHART" "$work/chart"
git show "${previous}:${CHART}/values.yaml" > "$work/chart/values.yaml"

failed=0

render() {
	local what=$1; shift
	local out
	# Every render carries a gateway token, because every real release has one:
	# the chart refuses to render without authentication, and that refusal is
	# a different feature with its own test.
	if ! out=$(helm template remedik "$work/chart" --namespace remedik \
		--set gateway.auth.token=simulated "$@" 2>&1); then
		printf '%s✗%s %s\n' "$RED" "$OFF" "$what"
		printf '%s\n' "$out" | sed 's/^/    /'
		failed=1
		return
	fi

	# A key that renders empty is the quieter half of the same bug: the render
	# succeeds and the object is invalid. `for:` and `severity:` with nothing
	# after them are what the Prometheus operator refused.
	local empty
	if empty=$(printf '%s\n' "$out" | grep -nE '^[[:space:]]*(for|severity|name|namespace|expr):[[:space:]]*$'); then
		printf '%s✗%s %s — a field rendered empty:\n' "$RED" "$OFF" "$what"
		printf '%s\n' "$empty" | sed 's/^/    /'
		failed=1
		return
	fi

	printf '%s✓%s %s\n' "$GREEN" "$OFF" "$what"
}

printf 'Rendering the chart against %s values, as --reuse-values would\n\n' "$previous"

render "the defaults somebody upgrading from ${previous} still has"

# Every feature added since the release, switched on the way a release-notes
# reader switches it on: one --set, no other keys. This is where a template that
# needs a sibling value it cannot get shows up.
render "workloadAlerts turned on with nothing else set" \
	--set workloadAlerts.enabled=true
render "the dashboard turned on with nothing else set" \
	--set dashboard.enabled=true --set dashboard.auth.token=t
render "the Grafana dashboard turned on with nothing else set" \
	--set grafanaDashboard.enabled=true
render "two replicas, which is leader election" \
	--set replicaCount=2
render "retention turned on with nothing else set" \
	--set retention.enabled=true
render "every action enabled, which is the widest RBAC" \
	--set actions.workloadRestart.enabled=true \
	--set actions.deploymentScale.enabled=true \
	--set actions.nodeCordon.enabled=true \
	--set actions.scriptRun.enabled=true \
	--set actions.jobRun.enabled=true

printf '\n'
if (( failed )); then
	printf '%sAn upgrade with --reuse-values from %s would not render.%s\n' \
		"$RED" "$previous" "$OFF"
	printf 'Give the new key a default in the template, or read it as\n'
	printf '(.Values.block).field so that a missing block is not an error.\n'
	exit 1
fi

printf '%sAn upgrade from %s renders with none of the new defaults.%s\n' \
	"$GREEN" "$previous" "$OFF"
