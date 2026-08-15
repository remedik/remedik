#!/usr/bin/env bash
#
# End-to-end test for remedik on a local kind cluster.
#
# It proves the whole path an operator cares about: an Alertmanager
# delivery reaches the gateway, a strategy matches it, the guards decide,
# and the outcome is visible both as a Remediation resource and as a real
# change (or deliberate non-change) to a workload.
#
#   1. authentication      — an unauthenticated delivery is refused
#   2. dry-run             — a matching alert records Simulated and touches nothing
#   3. real remediation    — a matching alert actually restarts the Deployment
#   4. cooldown            — an immediate repeat is refused by the guard
#   5. no match            — an unrelated alert is accepted and ignored
#   6. restart safety      — the cooldown still holds after the operator restarts
#
# Usage:  make e2e            (add KEEP_CLUSTER=1 to inspect afterwards)
set -euo pipefail

CLUSTER="${CLUSTER:-remedik-e2e}"
NAMESPACE="${NAMESPACE:-remedik}"
IMAGE="${IMAGE:-remedik:e2e}"
TOKEN="${TOKEN:-e2e-token}"
KEEP_CLUSTER="${KEEP_CLUSTER:-0}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --------------------------------------------------------------------------
# Output helpers
# --------------------------------------------------------------------------
if [ -t 1 ]; then
	BOLD=$'\033[1m'; GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; RESET=$'\033[0m'
else
	BOLD=''; GREEN=''; RED=''; DIM=''; RESET=''
fi

step()  { printf '\n%s==> %s%s\n' "$BOLD" "$*" "$RESET"; }
info()  { printf '    %s%s%s\n' "$DIM" "$*" "$RESET"; }
pass()  { printf '    %sPASS%s %s\n' "$GREEN" "$RESET" "$*"; PASSED=$((PASSED + 1)); }
fail()  { printf '    %sFAIL%s %s\n' "$RED" "$RESET" "$*"; FAILED=$((FAILED + 1)); }

PASSED=0
FAILED=0
PORT_FORWARD_PID=""

cleanup() {
	local exit_code=$?
	if [ -n "$PORT_FORWARD_PID" ]; then
		kill "$PORT_FORWARD_PID" 2>/dev/null || true
		wait "$PORT_FORWARD_PID" 2>/dev/null || true
	fi
	if [ "$exit_code" -ne 0 ] || [ "$FAILED" -ne 0 ]; then
		step "Diagnostics"
		kubectl -n "$NAMESPACE" logs deploy/remedik --tail=60 2>/dev/null || true
		kubectl -n "$NAMESPACE" get remediations -o wide 2>/dev/null || true
	fi
	if [ "$KEEP_CLUSTER" = "1" ]; then
		info "cluster '$CLUSTER' kept (KEEP_CLUSTER=1); delete it with: kind delete cluster --name $CLUSTER"
	else
		kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

require() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "$1 is required but not installed. See QUICKSTART.md." >&2
		exit 1
	}
}

# --------------------------------------------------------------------------
# Cluster helpers
# --------------------------------------------------------------------------

# send_alert <alertname> <fingerprint> <deployment> [auth] -> HTTP status
send_alert() {
	local alertname="$1" fingerprint="$2" deployment="$3" auth="${4:-yes}" header=()
	[ "$auth" = "yes" ] && header=(-H "Authorization: Bearer ${TOKEN}")

	curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
		-X POST "http://127.0.0.1:${LOCAL_PORT}/webhooks/alertmanager" \
		"${header[@]}" \
		-H 'Content-Type: application/json' \
		-d "{\"version\":\"4\",\"alerts\":[{
		      \"status\":\"firing\",
		      \"labels\":{\"alertname\":\"${alertname}\",\"namespace\":\"e2e-payments\",\"deployment\":\"${deployment}\"},
		      \"annotations\":{\"summary\":\"e2e\"},
		      \"startsAt\":\"2026-08-15T09:00:00Z\",
		      \"fingerprint\":\"${fingerprint}\"}]}"
}

# wait_for_remediations <expected-count> [timeout-seconds]
wait_for_remediations() {
	local want="$1" timeout="${2:-60}" elapsed=0
	while [ "$elapsed" -lt "$timeout" ]; do
		local got
		got=$(kubectl -n "$NAMESPACE" get remediations --no-headers 2>/dev/null | wc -l | tr -d ' ')
		[ "$got" -ge "$want" ] && return 0
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
}

# wait_for_state <expected-state> [timeout-seconds]
wait_for_state() {
	local want="$1" timeout="${2:-60}" elapsed=0
	while [ "$elapsed" -lt "$timeout" ]; do
		local states
		states=$(kubectl -n "$NAMESPACE" get remediations \
			-o jsonpath='{range .items[*]}{.status.state}{"\n"}{end}' 2>/dev/null || true)
		echo "$states" | grep -qx "$want" && return 0
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
}

# restart_annotation <deployment>
restart_annotation() {
	kubectl -n e2e-payments get deploy "$1" \
		-o jsonpath='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}' 2>/dev/null || true
}

# start_port_forward (re)opens the tunnel to the gateway service.
start_port_forward() {
	if [ -n "$PORT_FORWARD_PID" ]; then
		kill "$PORT_FORWARD_PID" 2>/dev/null || true
		wait "$PORT_FORWARD_PID" 2>/dev/null || true
	fi
	kubectl -n "$NAMESPACE" port-forward svc/remedik-gateway "${LOCAL_PORT}:8090" >/dev/null 2>&1 &
	PORT_FORWARD_PID=$!
	for _ in $(seq 1 30); do
		curl -s -o /dev/null --max-time 1 "http://127.0.0.1:${LOCAL_PORT}/" && return 0
		sleep 1
	done
	echo "the gateway did not become reachable on 127.0.0.1:${LOCAL_PORT}" >&2
	return 1
}

remediation_count() {
	kubectl -n "$NAMESPACE" get remediations --no-headers 2>/dev/null | wc -l | tr -d ' '
}

# --------------------------------------------------------------------------
# Setup
# --------------------------------------------------------------------------
for tool in docker kind kubectl helm; do require "$tool"; done

step "Rendering and linting the chart"
helm lint charts/remedik --set gateway.auth.token=lint >/dev/null
helm template remedik charts/remedik --namespace "$NAMESPACE" \
	--set gateway.auth.token=lint >/dev/null
info "chart renders and lints"

step "Creating the kind cluster '$CLUSTER'"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
	info "cluster already exists, reusing it"
else
	kind create cluster --config hack/e2e/kind.yaml >/dev/null
fi
kubectl config use-context "kind-${CLUSTER}" >/dev/null

step "Building and loading the image"
docker build -q -t "$IMAGE" . >/dev/null
kind load docker-image "$IMAGE" --name "$CLUSTER" >/dev/null
info "$IMAGE loaded into the cluster"

step "Installing remedik (dry-run on)"
helm upgrade --install remedik charts/remedik \
	--namespace "$NAMESPACE" --create-namespace \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set dryRun=true \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
info "operator is running"

step "Creating the test workload and strategy"
kubectl apply -f hack/e2e/workload.yaml >/dev/null
kubectl apply -f hack/e2e/strategy.yaml >/dev/null
kubectl -n e2e-payments rollout status deploy/api --timeout=120s >/dev/null
kubectl -n e2e-payments rollout status deploy/api2 --timeout=120s >/dev/null

# The gateway is a ClusterIP service; reach it through a port-forward.
LOCAL_PORT="${LOCAL_PORT:-18090}"
start_port_forward
info "gateway reachable on 127.0.0.1:${LOCAL_PORT}"

# --------------------------------------------------------------------------
# Test 1 — authentication
# --------------------------------------------------------------------------
step "1. An unauthenticated delivery is refused"
status=$(send_alert E2ECrashLooping unauth-1 api no)
if [ "$status" = "401" ]; then
	pass "gateway answered 401 without a token"
else
	fail "gateway answered $status, want 401"
fi
if [ "$(remediation_count)" = "0" ]; then
	pass "no remediation was created for a rejected delivery"
else
	fail "a rejected delivery still created a remediation"
fi

# --------------------------------------------------------------------------
# Test 2 — dry-run records without acting
# --------------------------------------------------------------------------
step "2. Dry-run records what it would do, and changes nothing"
before_annotation="$(restart_annotation api)"
status=$(send_alert E2ECrashLooping dry-1 api)
if [ "$status" = "200" ]; then
	pass "gateway accepted the delivery"
else
	fail "gateway answered $status, want 200"
fi

if wait_for_state Simulated 60; then
	pass "remediation reached state Simulated"
else
	fail "no remediation reached Simulated within 60s"
fi

plan=$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{.items[0].status.steps[0].plan}' 2>/dev/null || true)
if [ -n "$plan" ]; then
	pass "the record carries the plan it would have run: ${plan}"
else
	fail "the simulated record has no plan"
fi

if [ "$(restart_annotation api)" = "$before_annotation" ]; then
	pass "the Deployment was not touched"
else
	fail "dry-run modified the Deployment"
fi

# --------------------------------------------------------------------------
# Test 3 — a real remediation
#
# Against a second Deployment: the dry run above recorded a cooldown for
# its own target, faithfully, so reusing it here would be refused by the
# guard rather than remediated.
# --------------------------------------------------------------------------
step "3. With dry-run off, the Deployment is actually restarted"
helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set dryRun=false \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
start_port_forward

status=$(send_alert E2ECrashLooping real-1 api2)
if [ "$status" = "200" ]; then
	pass "gateway accepted the delivery"
else
	fail "gateway answered $status, want 200"
fi

if wait_for_state Succeeded 90; then
	pass "remediation reached state Succeeded"
else
	fail "no remediation reached Succeeded within 90s"
fi

if [ -n "$(restart_annotation api2)" ]; then
	pass "the Deployment was restarted: $(restart_annotation api2)"
else
	fail "the Deployment was never restarted"
fi

# --------------------------------------------------------------------------
# Test 4 — the cooldown guard
# --------------------------------------------------------------------------
step "4. An immediate repeat is refused by the cooldown guard"
count_before=$(remediation_count)
status=$(send_alert E2ECrashLooping real-2 api2)
if [ "$status" = "200" ]; then
	pass "gateway accepted the delivery (a refused guard is not an error)"
else
	fail "gateway answered $status, want 200"
fi
sleep 8
count_after=$(remediation_count)
if [ "$count_after" = "$count_before" ]; then
	pass "no new remediation was created inside the cooldown"
else
	fail "the cooldown did not stop a repeat: $count_before -> $count_after"
fi

# --------------------------------------------------------------------------
# Test 5 — an alert nothing matches
# --------------------------------------------------------------------------
step "5. An unmatched alert is accepted and ignored"
count_before=$(remediation_count)
status=$(send_alert SomethingElseEntirely other-1 api2)
if [ "$status" = "200" ]; then
	pass "gateway answered 200 (Alertmanager must not retry a normal no-match)"
else
	fail "gateway answered $status, want 200"
fi
sleep 5
if [ "$(remediation_count)" = "$count_before" ]; then
	pass "no remediation was created"
else
	fail "an unmatched alert produced a remediation"
fi

# --------------------------------------------------------------------------
# Test 6 — guards survive a restart
#
# Cooldowns live in memory. If they were not rebuilt from the Remediation
# resources at startup, the first alert after any restart would be acted on
# however recently the same thing had already been tried — a guard that
# evaporates is worse than no guard, because it is one people rely on.
# --------------------------------------------------------------------------
step "6. The cooldown still holds after the operator restarts"
kubectl -n "$NAMESPACE" rollout restart deploy/remedik >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
start_port_forward

if kubectl -n "$NAMESPACE" logs deploy/remedik --tail=200 2>/dev/null \
	| grep -q "guard history rebuilt"; then
	pass "the operator rebuilt its guard history on startup"
else
	fail "no sign that the guard history was rebuilt"
fi

count_before=$(remediation_count)
status=$(send_alert E2ECrashLooping real-3 api2)
if [ "$status" = "200" ]; then
	pass "gateway accepted the delivery"
else
	fail "gateway answered $status, want 200"
fi
sleep 8
if [ "$(remediation_count)" = "$count_before" ]; then
	pass "the cooldown was still in force after the restart"
else
	fail "the cooldown was forgotten across the restart"
fi

# --------------------------------------------------------------------------
# Summary
# --------------------------------------------------------------------------
step "Remediation records"
kubectl -n "$NAMESPACE" get remediations -o wide || true

step "Result"
printf '    %d passed, %d failed\n\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
