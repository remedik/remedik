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
#   3. real remediation    — a matching alert restarts the Deployment, the object
#                            carries events explaining it, and the record confirms
#                            the rollout completed
#   4. cooldown            — an immediate repeat is refused by the guard
#   5. no match            — an unrelated alert is accepted and ignored
#   6. restart safety      — the cooldown still holds after the operator restarts
#   7. dashboard           — every page renders, read-only, and the dry-run
#                            report names what would have happened
#   8. workload actions    — a StatefulSet is restarted, an owned pod is
#                            evicted, and a pod nothing owns is refused
#   9. guards and nodes    — blastRadius refuses a workload it cannot protect,
#                            and a node is cordoned and then uncordoned
#
# Usage:  make e2e            (add KEEP_CLUSTER=1 to inspect afterwards)
set -euo pipefail

CLUSTER="${CLUSTER:-remedik-e2e}"
NAMESPACE="${NAMESPACE:-remedik}"
IMAGE="${IMAGE:-remedik:e2e}"
TOKEN="${TOKEN:-e2e-token}"
DASHBOARD_TOKEN="${DASHBOARD_TOKEN:-e2e-dashboard-token}"
DASHBOARD_PORT="${DASHBOARD_PORT:-18082}"
METRICS_PORT="${METRICS_PORT:-18080}"
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
DASHBOARD_FORWARD_PID=""
METRICS_FORWARD_PID=""

cleanup() {
	local exit_code=$?
	for pid in "$PORT_FORWARD_PID" "$DASHBOARD_FORWARD_PID" "$METRICS_FORWARD_PID"; do
		if [ -n "$pid" ]; then
			kill "$pid" 2>/dev/null || true
			wait "$pid" 2>/dev/null || true
		fi
	done
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

# send_labeled_alert <alertname> <fingerprint> <labels-json> -> HTTP status
#
# send_alert covers the Deployment-shaped alerts, which is most of them. The
# node, Job, claim and autoscaler actions resolve their target from different
# labels, and testing them through the labels a real alert carries is the
# point — a step that only works when the target is named in `with:` has not
# been tested at all.
send_labeled_alert() {
	local alertname="$1" fingerprint="$2" labels="$3"
	curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
		-X POST "http://127.0.0.1:${LOCAL_PORT}/webhooks/alertmanager" \
		-H "Authorization: Bearer ${TOKEN}" \
		-H 'Content-Type: application/json' \
		-d "{\"version\":\"4\",\"alerts\":[{
		      \"status\":\"firing\",
		      \"labels\":{\"alertname\":\"${alertname}\",${labels}},
		      \"startsAt\":\"2026-08-15T09:00:00Z\",
		      \"fingerprint\":\"${fingerprint}\"}]}"
}

# wait_for_event <reason> <substring> [timeout-seconds]
#
# Events are written after the decision they describe, and how long after
# depends on the cluster's mood. A fixed sleep here is what makes a suite
# flaky, and a suite people learn to re-run is worth less than no suite.
wait_for_event() {
	local reason="$1" want="$2" timeout="${3:-45}" elapsed=0
	while [ "$elapsed" -lt "$timeout" ]; do
		if kubectl get events --all-namespaces --field-selector "reason=${reason}" \
			-o jsonpath='{range .items[*]}{.message}{"\n"}{end}' 2>/dev/null \
			| grep -q "$want"; then
			return 0
		fi
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
}

# wait_for_message <strategy> <substring> [timeout-seconds]
#
# Waits for any of a strategy's records to carry a status message matching
# the substring. Used where the expected outcome is a refusal, which has no
# state of its own to wait for.
wait_for_message() {
	local strategy="$1" want="$2" timeout="${3:-45}" elapsed=0
	while [ "$elapsed" -lt "$timeout" ]; do
		if kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/strategy=${strategy}" \
			-o jsonpath='{range .items[*]}{.status.message}{"\n"}{end}' 2>/dev/null \
			| grep -qi "$want"; then
			return 0
		fi
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
}

# strategy_field <strategy> <jsonpath-after-.items[0]> -> the value
strategy_field() {
	kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/strategy=$1" \
		-o jsonpath="{.items[0]$2}" 2>/dev/null || true
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

# wait_for_strategy_state <strategy> <state> [timeout-seconds]
wait_for_strategy_state() {
	local strategy="$1" want="$2" timeout="${3:-60}" elapsed=0
	while [ "$elapsed" -lt "$timeout" ]; do
		local states
		states=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/strategy=${strategy}" \
			-o jsonpath='{range .items[*]}{.status.state}{"\n"}{end}' 2>/dev/null || true)
		echo "$states" | grep -qx "$want" && return 0
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
}

# restart_annotation <deployment>
restart_annotation() {
	restart_annotation_in e2e-payments "$1"
}

# restart_annotation_in <namespace> <deployment>
restart_annotation_in() {
	kubectl -n "$1" get deploy "$2" \
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

# start_dashboard_forward opens the tunnel to the dashboard service.
start_dashboard_forward() {
	if [ -n "$DASHBOARD_FORWARD_PID" ]; then
		kill "$DASHBOARD_FORWARD_PID" 2>/dev/null || true
		wait "$DASHBOARD_FORWARD_PID" 2>/dev/null || true
	fi
	kubectl -n "$NAMESPACE" port-forward svc/remedik-dashboard \
		"${DASHBOARD_PORT}:${DASHBOARD_PORT}" >/dev/null 2>&1 &
	DASHBOARD_FORWARD_PID=$!
	for _ in $(seq 1 30); do
		curl -s -o /dev/null --max-time 1 "http://127.0.0.1:${DASHBOARD_PORT}/" && return 0
		sleep 1
	done
	echo "the dashboard did not become reachable on 127.0.0.1:${DASHBOARD_PORT}" >&2
	return 1
}

# dashboard_status <path> [auth] -> HTTP status
dashboard_status() {
	local path="$1" auth="${2:-yes}" header=()
	[ "$auth" = "yes" ] && header=(-H "Authorization: Bearer ${DASHBOARD_TOKEN}")
	curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
		"${header[@]}" "http://127.0.0.1:${DASHBOARD_PORT}${path}"
}

# dashboard_body <path> -> the rendered page
dashboard_body() {
	curl -s --max-time 10 -H "Authorization: Bearer ${DASHBOARD_TOKEN}" \
		"http://127.0.0.1:${DASHBOARD_PORT}$1"
}

# dashboard_method <method> <path> -> HTTP status
dashboard_method() {
	curl -s -o /dev/null -w '%{http_code}' --max-time 10 -X "$1" \
		-H "Authorization: Bearer ${DASHBOARD_TOKEN}" \
		"http://127.0.0.1:${DASHBOARD_PORT}$2"
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

# Wait for every node before installing anything. A pod scheduled onto a
# worker whose CNI is not up yet starts, logs, and then blocks on its first
# call to the API server — which looks exactly like a hang in the operator
# and is not one.
kubectl wait --for=condition=Ready nodes --all --timeout=180s >/dev/null
info "$(kubectl get nodes --no-headers | wc -l | tr -d ' ') nodes ready"

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
	--set actions.nodeCordon.enabled=true \
	--set actions.nodeUncordon.enabled=true \
	--set actions.nodeDrain.enabled=true \
	--set guards.blastRadius.enabled=true \
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

# A drain, planned but not performed. This is the only place the plan can be
# checked against a real node: it lists the node's actual pods, decides which
# are evictable, and moves nothing.
NODE=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
status=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
	-X POST "http://127.0.0.1:${LOCAL_PORT}/webhooks/alertmanager" \
	-H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
	-d "{\"version\":\"4\",\"alerts\":[{
	      \"status\":\"firing\",
	      \"labels\":{\"alertname\":\"E2ENodeUnreachable\",\"node\":\"${NODE}\"},
	      \"startsAt\":\"2026-08-15T09:00:00Z\",
	      \"fingerprint\":\"drainplan-1\"}]}")
if [ "$status" = "200" ]; then
	pass "gateway accepted the drain alert"
else
	fail "gateway answered $status for the drain alert"
fi

if wait_for_strategy_state e2e-node-drain Simulated 60; then
	drain_plan=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/strategy=e2e-node-drain \
		-o jsonpath='{.items[0].status.steps[0].plan}' 2>/dev/null || true)
	if echo "$drain_plan" | grep -q "Eviction API"; then
		pass "the drain plan says it would evict: ${drain_plan}"
	else
		fail "the drain plan does not describe an eviction (${drain_plan})"
	fi

	drain_pods=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/strategy=e2e-node-drain \
		-o jsonpath='{.items[0].status.steps[0].outputs.pods}' 2>/dev/null || true)
	if echo "$drain_pods" | grep -q "e2e-payments/"; then
		pass "the plan names the pods that would move"
	else
		fail "the plan names no pods (${drain_pods})"
	fi

	# DaemonSet pods are skipped, which is what makes a drain terminate.
	if echo "$drain_plan" | grep -q "DaemonSet"; then
		pass "the plan says DaemonSet pods are skipped"
	else
		fail "the plan does not mention skipping DaemonSet pods"
	fi
else
	fail "the drain was never planned"
fi

if [ -z "$(kubectl get node "$NODE" -o jsonpath='{.spec.unschedulable}')" ]; then
	pass "planning a drain did not cordon the node"
else
	fail "a dry run cordoned the node"
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

# The explanation has to be where the person is already looking. Somebody
# investigating a restart runs `kubectl describe deployment`, not
# `kubectl get remediations` — they do not necessarily know remedik exists.
if kubectl -n e2e-payments get events --field-selector reason=Remediated \
	-o jsonpath='{range .items[*]}{.involvedObject.kind}/{.involvedObject.name} {.message}{"\n"}{end}' \
	2>/dev/null | grep -q '^Deployment/api2'; then
	pass "the Deployment carries a Remediated event naming what happened"
else
	fail "no Remediated event on the Deployment; kubectl describe would explain nothing"
fi

if kubectl -n e2e-payments get events --field-selector reason=Remediated \
	-o jsonpath='{range .items[*]}{.message}{"\n"}{end}' 2>/dev/null \
	| grep -q 'strategy e2e-crashloop'; then
	pass "the event names the strategy responsible"
else
	fail "the event does not name the strategy, so the reader cannot find the manifest"
fi

# A step that reports the API call succeeded is reporting on the wrong
# event. What matters is whether the workload came back.
verified=$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{range .items[?(@.status.state=="Succeeded")]}{.status.steps[0].verified}{"\n"}{end}' \
	2>/dev/null | head -1)
if echo "$verified" | grep -q 'ready'; then
	pass "the record confirms the rollout completed: ${verified}"
else
	fail "the record does not confirm the rollout; verification did not run"
fi

kubectl_line=$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{range .items[?(@.status.state=="Succeeded")]}{.status.steps[0].kubectl}{"\n"}{end}' \
	2>/dev/null | head -1)
if echo "$kubectl_line" | grep -q 'kubectl rollout restart'; then
	pass "the record carries the equivalent command: ${kubectl_line}"
else
	fail "the record carries no kubectl equivalent"
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

# The refusal has to be visible where an operator looks first.
if wait_for_event GuardRejected 'guard "cooldown"' 45; then
	pass "the refusal is published as an event on the strategy"
else
	fail "no GuardRejected event was published within 45s"
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
# Test 7 — the read-only dashboard
#
# The dashboard is off by default, so enabling it is itself part of the
# test. What matters here is what unit tests cannot prove: that the pages
# render from a real cluster's resources, inside the distroless image, with
# every asset embedded in the binary.
# --------------------------------------------------------------------------
step "7. The dashboard renders, reads only, and reports the dry run"

if kubectl -n "$NAMESPACE" get svc remedik-dashboard >/dev/null 2>&1; then
	fail "the dashboard Service exists before it was enabled"
else
	pass "no dashboard is installed by default"
fi

helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set dryRun=false \
	--set dashboard.enabled=true \
	--set dashboard.port="$DASHBOARD_PORT" \
	--set dashboard.auth.token="$DASHBOARD_TOKEN" \
	--set clusterName=e2e-cluster \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
start_dashboard_forward

# Read-only is the guarantee that has to hold in a real cluster, not just in
# a handler test: nothing here can change anything.
for method in POST PUT PATCH DELETE; do
	status=$(dashboard_method "$method" /)
	if [ "$status" = "405" ]; then
		pass "$method / answered 405"
	else
		fail "$method / answered $status, want 405"
	fi
done

status=$(dashboard_status / no)
if [ "$status" = "401" ]; then
	pass "the dashboard answered 401 without a token"
else
	fail "the dashboard answered $status without a token, want 401"
fi

for path in / /strategies; do
	status=$(dashboard_status "$path")
	if [ "$status" = "200" ]; then
		pass "GET $path rendered"
	else
		fail "GET $path answered $status, want 200"
	fi
done

# The stylesheet is embedded in the binary. A distroless image with no
# filesystem to read from is exactly where a missing go:embed would show up.
status=$(dashboard_status /static/app.css)
if [ "$status" = "200" ]; then
	pass "the embedded stylesheet is served"
else
	fail "the stylesheet answered $status, want 200"
fi

overview=$(dashboard_body /)
if echo "$overview" | grep -q "e2e-crashloop"; then
	pass "the overview lists the executions of the e2e strategy"
else
	fail "the overview does not mention the e2e strategy"
fi

# Test 2 recorded a Simulated remediation. Whatever the operator's posture is
# now, that trial has to be reportable — it is the report an operator shows
# their team before turning dry-run off.
if echo "$overview" | grep -q "What remedik would have done"; then
	pass "the dry-run report is on the overview"
else
	fail "no dry-run report, although a simulated remediation exists"
fi
if echo "$overview" | grep -q "restartedAt"; then
	pass "the report says what would have been done"
else
	fail "the report does not show the plan of the simulated remediation"
fi

simulated=$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{range .items[?(@.status.state=="Simulated")]}{.metadata.name}{"\n"}{end}' \
	2>/dev/null | head -1)
if [ -n "$simulated" ]; then
	detail=$(dashboard_body "/remediations/${simulated}")
	if echo "$detail" | grep -q "nothing in the cluster was changed"; then
		pass "the detail page of ${simulated} explains the simulation"
	else
		fail "the detail page of ${simulated} does not explain the simulation"
	fi
else
	fail "no simulated remediation to open a detail page for"
fi

strategies=$(dashboard_body /strategies)
if echo "$strategies" | grep -q "E2ECrashLooping" && echo "$strategies" | grep -q "30m"; then
	pass "the strategies page shows the matcher and the cooldown guard"
else
	fail "the strategies page is missing the matcher or the guard"
fi

status=$(dashboard_status /remediations/does-not-exist)
if [ "$status" = "404" ]; then
	pass "an unknown remediation answered 404"
else
	fail "an unknown remediation answered $status, want 404"
fi

# The cluster's name, which is what tells three port-forwarded dashboards
# apart. It is in the tab title so it survives being one of twenty tabs.
if echo "$overview" | grep -q '<title>e2e-cluster'; then
	pass "the cluster name leads the browser title"
else
	fail "the cluster name is not in the title"
fi

# --- filtering --------------------------------------------------------------
# The filter is entirely in the URL, which is what makes a narrowed view
# something somebody can paste into an incident channel. Every record so far
# targets e2e-payments, so the assertions are about which of them survive.
filtered=$(dashboard_body "/?namespace=e2e-payments")
if echo "$filtered" | grep -q 'e2e-payments'; then
	pass "a namespace filter renders that namespace's records"
else
	fail "the namespace filter hid everything in its own namespace"
fi

# The controls must be outside <main>, which is what the ten-second refresh
# replaces. Inside it, a selection made and not yet applied is destroyed
# faster than anybody reaches Apply, and the filter appears not to work.
form_line=$(echo "$filtered" | grep -n '<form class="filters"' | head -1 | cut -d: -f1)
main_line=$(echo "$filtered" | grep -n '<main id="content"' | head -1 | cut -d: -f1)
if [ -n "$form_line" ] && [ -n "$main_line" ] && [ "$form_line" -lt "$main_line" ]; then
	pass "the filter controls render outside the region the refresh replaces"
else
	fail "the filter controls are inside <main> (form=${form_line}, main=${main_line})"
fi

# And the applied filter is stated, with each clause removable on its own.
if echo "$filtered" | grep -q 'Filtered by' && echo "$filtered" | grep -q 'chip-active'; then
	pass "the applied filter is stated on the page"
else
	fail "a filtered page does not say what it is filtered by"
fi

empty=$(dashboard_body "/?namespace=no-such-namespace")
if echo "$empty" | grep -q "Nothing matches this filter"; then
	pass "a filter that matches nothing says so, rather than looking like an empty cluster"
else
	fail "an empty filter result did not explain itself"
fi
if echo "$empty" | grep -q "No strategies, so nothing can run"; then
	fail "an empty filter result claimed the cluster has no strategies"
else
	pass "and it does not claim the cluster is unconfigured"
fi

# An unknown parameter value is honoured, not rejected: a URL pasted from a
# week-old incident channel must not become an error page.
status=$(dashboard_status "/?namespace=no-such-namespace&state=Nonsense")
if [ "$status" = "200" ]; then
	pass "an unrecognised filter value still renders"
else
	fail "an unrecognised filter value answered $status, want 200"
fi

# Filtering must not become a way in for a write.
status=$(dashboard_method POST "/?namespace=e2e-payments")
if [ "$status" = "405" ]; then
	pass "a filtered URL is still GET-only"
else
	fail "POST to a filtered URL answered $status, want 405"
fi

# --------------------------------------------------------------------------
# Test 8 — the workload actions
#
# Three things unit tests cannot prove: that workload.restart really drives a
# StatefulSet's rollout to completion, that pod.delete goes through the
# Eviction API against a real API server, and that a pod nothing owns is
# refused rather than removed.
#
# The actions are off by default, so enabling them is part of the test.
# --------------------------------------------------------------------------
step "8. Restarting a StatefulSet, and evicting a pod"

helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set dryRun=false \
	--set dashboard.enabled=true \
	--set dashboard.port="$DASHBOARD_PORT" \
	--set dashboard.auth.token="$DASHBOARD_TOKEN" \
	--set actions.workloadRestart.enabled=true \
	--set actions.podDelete.enabled=true \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
start_port_forward

if kubectl -n "$NAMESPACE" logs deploy/remedik --tail=200 2>/dev/null \
	| grep -q '"actions":\["deployment.restart","pod.delete","workload.restart"\]'; then
	pass "the operator registered exactly the actions the chart granted"
else
	fail "the registered actions do not match what the chart enabled"
fi

# --- a StatefulSet ---------------------------------------------------------
sts_before=$(kubectl -n e2e-payments get statefulset ledger \
	-o jsonpath='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}' 2>/dev/null || true)

send_workload_alert() {
	curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
		-X POST "http://127.0.0.1:${LOCAL_PORT}/webhooks/alertmanager" \
		-H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
		-d "{\"version\":\"4\",\"alerts\":[{
		      \"status\":\"firing\",
		      \"labels\":{\"alertname\":\"$1\",\"namespace\":\"e2e-payments\",\"$2\":\"$3\"},
		      \"startsAt\":\"2026-08-15T09:00:00Z\",
		      \"fingerprint\":\"$4\"}]}"
}

status=$(send_workload_alert E2EStatefulSetStuck statefulset ledger sts-1)
if [ "$status" = "200" ]; then
	pass "gateway accepted the StatefulSet alert"
else
	fail "gateway answered $status for the StatefulSet alert"
fi

if wait_for_strategy_state e2e-statefulset Succeeded 120; then
	sts_after=$(kubectl -n e2e-payments get statefulset ledger \
		-o jsonpath='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}' 2>/dev/null || true)
	if [ -n "$sts_after" ] && [ "$sts_after" != "$sts_before" ]; then
		pass "the StatefulSet was restarted: ${sts_after}"
	else
		fail "the StatefulSet was never restarted"
	fi
else
	fail "the StatefulSet remediation did not succeed within 120s"
fi

sts_verified=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/strategy=e2e-statefulset \
	-o jsonpath='{.items[0].status.steps[0].verified}' 2>/dev/null || true)
if echo "$sts_verified" | grep -q 'ready'; then
	pass "the StatefulSet rollout was confirmed: ${sts_verified}"
else
	fail "the StatefulSet rollout was not confirmed (verified=${sts_verified})"
fi

# --- a pod nothing owns ----------------------------------------------------
status=$(send_workload_alert E2EPodStuck pod orphan orphan-1)
if [ "$status" = "200" ]; then
	pass "gateway accepted the bare-pod alert"
else
	fail "gateway answered $status for the bare-pod alert"
fi

for _ in $(seq 1 30); do
	orphan_msg=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/fingerprint=orphan-1 \
		-o jsonpath='{.items[0].status.steps[0].message}' 2>/dev/null || true)
	[ -n "$orphan_msg" ] && break
	sleep 2
done

if echo "$orphan_msg" | grep -q 'no controller owner'; then
	pass "a pod nothing owns was refused, not deleted"
else
	fail "the bare pod was not refused (message: ${orphan_msg})"
fi
if kubectl -n e2e-payments get pod orphan >/dev/null 2>&1; then
	pass "the bare pod is still running"
else
	fail "the bare pod was removed despite the refusal"
fi

# --- an owned pod ----------------------------------------------------------
owned_pod=$(kubectl -n e2e-payments get pods -l app=api2 \
	-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -z "$owned_pod" ]; then
	fail "no owned pod to evict"
else
	status=$(send_workload_alert E2EPodStuck pod "$owned_pod" "evict-1")
	if [ "$status" = "200" ]; then
		pass "gateway accepted the eviction alert"
	else
		fail "gateway answered $status for the eviction alert"
	fi

	evicted=""
	for _ in $(seq 1 45); do
		evicted=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/fingerprint=evict-1 \
			-o jsonpath='{.items[0].status.steps[0].verified}' 2>/dev/null || true)
		[ -n "$evicted" ] && break
		sleep 2
	done
	if [ -n "$evicted" ]; then
		pass "the pod was evicted and confirmed gone: ${evicted}"
	else
		fail "the eviction was never confirmed"
	fi
fi

# --------------------------------------------------------------------------
# Test 9 — the guards and the node actions
#
# These are the highest-risk verbs in the catalogue, and the two things that
# cannot be tested without a cluster are exactly the interesting ones: that
# the blastRadius guard refuses against a real workload's status, and that a
# drain actually empties a real node through the Eviction API.
#
# The worker is used throughout, never the control plane: draining that would
# leave the test with nowhere to put anything.
# --------------------------------------------------------------------------
step "9. Guards refuse, and a node is cordoned then uncordoned"

helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set dryRun=false \
	--set actions.workloadRestart.enabled=true \
	--set actions.podDelete.enabled=true \
	--set actions.nodeCordon.enabled=true \
	--set actions.nodeUncordon.enabled=true \
	--set actions.nodeDrain.enabled=true \
	--set guards.blastRadius.enabled=true \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
start_port_forward

# --- blastRadius ----------------------------------------------------------
# The strategy demands five available replicas of a Deployment that has one,
# so the guard must refuse. This is the property unit tests cannot check: the
# numbers come from a real workload's status.
count_before=$(remediation_count)
status=$(send_alert E2EBlastRadius blast-1 api)
if [ "$status" = "200" ]; then
	pass "gateway accepted the blastRadius alert"
else
	fail "gateway answered $status for the blastRadius alert"
fi
sleep 8

if [ "$(remediation_count)" = "$count_before" ]; then
	pass "blastRadius refused a workload it could not protect"
else
	fail "blastRadius let a remediation through"
fi
if wait_for_event GuardRejected blastRadius 45; then
	pass "the refusal names blastRadius on the strategy"
else
	fail "no blastRadius refusal was published within 45s"
fi

# --- cordon, then put it back ---------------------------------------------
# Cordoning is safe even here: nothing moves and nothing restarts, only new
# scheduling stops. It is undone immediately afterwards.
NODE=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')

send_node_alert() {
	curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
		-X POST "http://127.0.0.1:${LOCAL_PORT}/webhooks/alertmanager" \
		-H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
		-d "{\"version\":\"4\",\"alerts\":[{
		      \"status\":\"firing\",
		      \"labels\":{\"alertname\":\"$1\",\"node\":\"${NODE}\"},
		      \"startsAt\":\"2026-08-15T09:00:00Z\",
		      \"fingerprint\":\"$2\"}]}"
}

unschedulable() {
	kubectl get node "$NODE" -o jsonpath='{.spec.unschedulable}' 2>/dev/null
}

status=$(send_node_alert E2ENodeNotReady cordon-1)
if [ "$status" = "200" ]; then
	pass "gateway accepted the cordon alert"
else
	fail "gateway answered $status for the cordon alert"
fi

if wait_for_strategy_state e2e-node-cordon Succeeded 90; then
	if [ "$(unschedulable)" = "true" ]; then
		pass "the node was cordoned"
	else
		fail "the node was not cordoned"
	fi
	cordon_verified=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/strategy=e2e-node-cordon \
		-o jsonpath='{.items[0].status.steps[0].verified}' 2>/dev/null || true)
	if echo "$cordon_verified" | grep -q 'unschedulable'; then
		pass "the record confirms it: ${cordon_verified}"
	else
		fail "the record does not confirm the cordon (${cordon_verified})"
	fi
else
	fail "the cordon remediation did not succeed"
fi

status=$(send_node_alert E2ENodeSchedulable uncordon-1)
if wait_for_strategy_state e2e-node-uncordon Succeeded 90; then
	if [ -z "$(unschedulable)" ] || [ "$(unschedulable)" = "false" ]; then
		pass "the node was uncordoned again"
	else
		fail "the node was left cordoned"
	fi
else
	fail "the uncordon remediation did not succeed (status ${status})"
fi


# --------------------------------------------------------------------------
# Test 10 — the rest of the catalogue, and the escalation path
#
# Groups 1-9 cover six of the fourteen actions. The other eight are the ones
# whose failure modes are invisible without a cluster: a rollback needs real
# revision history, a scale needs a real HPA to refuse, an expansion needs a
# real StorageClass, and a webhook needs something at the other end.
#
# That last one is answered with remedik itself. Its gateway accepts POST,
# requires a bearer token from a Secret, and answers 200 to a body it
# understood — so the whole outbound path is proven without the test needing
# an endpoint outside the cluster, which it would not have.
# --------------------------------------------------------------------------
step "10. The remaining actions, and escalation"

# The endpoint credentials. The right token proves the success path; a real
# Secret holding the wrong one proves the failure path honestly, rather than
# by pointing at a URL that does not resolve.
kubectl -n "$NAMESPACE" create secret generic e2e-wrong-token \
	--from-literal=token=not-the-gateway-token \
	--dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$NAMESPACE" create serviceaccount e2e-runbook \
	--dry-run=client -o yaml | kubectl apply -f - >/dev/null

helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set dryRun=false \
	--set actions.deploymentRestart.enabled=true \
	--set actions.jobDelete.enabled=true \
	--set actions.deploymentRollback.enabled=true \
	--set actions.deploymentScale.enabled=true \
	--set actions.hpaScale.enabled=true \
	--set actions.pvcExpand.enabled=true \
	--set actions.webhookCall.enabled=true \
	--set actions.jobRun.enabled=true \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
start_port_forward

# --- job.delete -----------------------------------------------------------
# Resolved from `job_name`, not `job`: in Prometheus `job` is the scrape job,
# and an action that read it would resolve to kube-state-metrics.
status=$(send_labeled_alert E2EJobFailed job-1 \
	'"namespace":"e2e-payments","job_name":"nightly-billing"')
if [ "$status" = "200" ]; then
	pass "gateway accepted the failed-Job alert"
else
	fail "gateway answered $status for the failed-Job alert"
fi

if wait_for_strategy_state e2e-job-delete Succeeded 90; then
	if kubectl -n e2e-payments get job nightly-billing >/dev/null 2>&1; then
		fail "the Job is still there"
	else
		pass "the Job was deleted, so its CronJob can schedule again"
	fi
else
	fail "the job.delete remediation did not succeed"
fi

# --- deployment.rollback --------------------------------------------------
# Give it a second revision, then ask remedik to put the first one back. The
# assertion is on the pod template, because that is what a rollback restores;
# the revision number only moves forward.
kubectl -n e2e-payments patch deploy shipped --type=merge \
	-p '{"spec":{"template":{"metadata":{"labels":{"release":"bad"}}}}}' >/dev/null
kubectl -n e2e-payments rollout status deploy/shipped --timeout=90s >/dev/null 2>&1 || true

if [ "$(kubectl -n e2e-payments get deploy shipped -o jsonpath='{.spec.template.metadata.labels.release}')" = "bad" ]; then
	pass "the bad revision is live, so there is something to roll back"
else
	fail "the second revision did not take"
fi

status=$(send_alert E2EBadDeploy rollback-1 shipped)
if wait_for_strategy_state e2e-rollback Succeeded 120; then
	live=$(kubectl -n e2e-payments get deploy shipped \
		-o jsonpath='{.spec.template.metadata.labels.release}')
	if [ "$live" = "good" ]; then
		pass "the previous revision is back"
	else
		fail "the deployment still runs the bad revision (release=${live})"
	fi
	rolled=$(strategy_field e2e-rollback '.status.steps[0].outputs.rolledBackTo')
	if [ -n "$rolled" ]; then
		pass "the record names the revision it went back to: ${rolled}"
	else
		fail "the record does not name the revision"
	fi
else
	fail "the rollback remediation did not succeed (gateway answered ${status})"
fi

# --- deployment.scale -----------------------------------------------------
status=$(send_alert E2ENeedsCapacity scale-1 capacity)
if wait_for_strategy_state e2e-scale Succeeded 120; then
	replicas=$(kubectl -n e2e-payments get deploy capacity -o jsonpath='{.spec.replicas}')
	if [ "$replicas" = "3" ]; then
		pass "the deployment was scaled from 1 to 3"
	else
		fail "the deployment has ${replicas} replicas, want 3"
	fi
else
	fail "the scale remediation did not succeed (gateway answered ${status})"
fi

# api2 has an HPA, so scaling it directly must be refused: two controllers
# fighting over one replica count is worse than not scaling at all.
status=$(send_alert E2ENeedsCapacity scale-2 api2)
if wait_for_message e2e-scale horizontalpodautoscaler 60; then
	pass "scaling a Deployment an HPA owns was refused"
else
	fail "scaling under an HPA was not refused (gateway answered ${status})"
fi

# --- hpa.scale ------------------------------------------------------------
status=$(send_labeled_alert E2EHpaMaxed hpa-1 \
	'"namespace":"e2e-payments","horizontalpodautoscaler":"api2"')
if wait_for_strategy_state e2e-hpa-scale Succeeded 120; then
	ceiling=$(kubectl -n e2e-payments get hpa api2 -o jsonpath='{.spec.maxReplicas}')
	if [ "$ceiling" = "5" ]; then
		pass "the autoscaler's ceiling was raised from 2 to 5"
	else
		fail "maxReplicas is ${ceiling}, want 5"
	fi
else
	fail "the hpa.scale remediation did not succeed (gateway answered ${status})"
fi

# --- pvc.expand -----------------------------------------------------------
# The refusal is the feature. kind's "standard" class does not set
# allowVolumeExpansion, so the API server would accept the patch and nothing
# would happen — and remedik would have recorded a success that did nothing.
status=$(send_labeled_alert E2EVolumeFilling pvc-1 \
	'"namespace":"e2e-payments","persistentvolumeclaim":"ledger-data"')
if wait_for_strategy_state e2e-pvc-expand Failed 120; then
	message=$(strategy_field e2e-pvc-expand '.status.message')
	if echo "$message" | grep -q 'allowVolumeExpansion'; then
		pass "the expansion was refused, naming the StorageClass"
	else
		fail "the refusal does not explain itself: ${message}"
	fi
	size=$(kubectl -n e2e-payments get pvc ledger-data \
		-o jsonpath='{.spec.resources.requests.storage}')
	if [ "$size" = "1Gi" ]; then
		pass "the claim was left alone"
	else
		fail "the claim was patched anyway (${size})"
	fi
else
	fail "the pvc.expand remediation did not fail as it should (gateway answered ${status})"
fi

# --- webhook.call ---------------------------------------------------------
status=$(send_alert E2EWebhook webhook-1 api)
if wait_for_strategy_state e2e-webhook Succeeded 120; then
	pass "the webhook reached its endpoint with the credential from a Secret"
	code=$(strategy_field e2e-webhook '.status.steps[0].outputs.status')
	if [ "$code" = "200" ]; then
		pass "the record keeps the response code: ${code}"
	else
		fail "the record's status output is '${code}', want 200"
	fi
	if strategy_field e2e-webhook '.status.steps[0]' | grep -qF "$TOKEN"; then
		fail "the credential leaked into the record"
	else
		pass "the credential is not in the record, only where it came from"
	fi
else
	fail "the webhook remediation did not succeed (gateway answered ${status})"
fi

# --- job.run --------------------------------------------------------------
# The Job runs in remedik's namespace under a ServiceAccount the operator
# names and never its own, and the step waits for the exit code rather than
# reporting success once the Job is created.
status=$(send_alert E2ERunbook runbook-1 api)
if wait_for_strategy_state e2e-job-run Succeeded 180; then
	pass "the runbook Job ran to completion and its exit code was checked"
	sa=$(strategy_field e2e-job-run '.status.steps[0].outputs.serviceAccount')
	if [ "$sa" = "e2e-runbook" ]; then
		pass "it ran as ${sa}, not as remedik"
	else
		fail "it ran as '${sa}', want e2e-runbook"
	fi
else
	fail "the job.run remediation did not succeed (gateway answered ${status})"
	kubectl -n "$NAMESPACE" get jobs 2>/dev/null || true
fi

# --- escalation -----------------------------------------------------------
# The loop the project exists to close: the remediation could not work, and
# somebody was told.
status=$(send_alert E2EEscalate escalate-1 does-not-exist)
if wait_for_strategy_state e2e-escalation Failed 120; then
	pass "the remediation failed, as it must"

	phase=$(strategy_field e2e-escalation '.status.escalation.phase')
	if [ "$phase" = "Succeeded" ]; then
		pass "the escalation was sent"
	else
		fail "the escalation's phase is '${phase}', want Succeeded"
	fi

	# Escalating is not succeeding.
	state=$(strategy_field e2e-escalation '.status.state')
	if [ "$state" = "Failed" ]; then
		pass "the record is still Failed: a page is not a fix"
	else
		fail "escalating changed the outcome to ${state}"
	fi

	# The remediation's own verdict survived the escalation.
	if strategy_field e2e-escalation '.status.message' | grep -q 'does-not-exist'; then
		pass "the record still explains why the remediation failed"
	else
		fail "the escalation overwrote the remediation's own message"
	fi

	# The escalation's steps are its own, not appended to the remediation's.
	own=$(strategy_field e2e-escalation '.status.steps[0].action')
	esc=$(strategy_field e2e-escalation '.status.escalation.steps[0].action')
	if [ "$own" = "deployment.restart" ] && [ "$esc" = "webhook.call" ]; then
		pass "the page is recorded apart from the remediation's steps"
	else
		fail "steps are '${own}' and '${esc}', want deployment.restart and webhook.call"
	fi
else
	fail "the escalating remediation did not reach Failed (gateway answered ${status})"
fi

# --- an escalation that could not be sent ---------------------------------
# The case that matters most: it did not work, and nobody knows.
status=$(send_alert E2EEscalateBadly escalate-2 does-not-exist)
if wait_for_strategy_state e2e-escalation-fails Failed 120; then
	phase=$(strategy_field e2e-escalation-fails '.status.escalation.phase')
	if [ "$phase" = "Failed" ]; then
		pass "a page that could not be sent is recorded as failed"
	else
		fail "the failed escalation's phase is '${phase}', want Failed"
	fi
	message=$(strategy_field e2e-escalation-fails '.status.escalation.message')
	if [ -n "$message" ]; then
		pass "and it says why: ${message}"
	else
		fail "the failed escalation does not say why"
	fi
	state=$(strategy_field e2e-escalation-fails '.status.state')
	if [ "$state" = "Failed" ]; then
		pass "the remediation is Failed, not something worse"
	else
		fail "a failed page changed the state to ${state}"
	fi
else
	fail "the second escalating remediation did not reach Failed (gateway answered ${status})"
fi


# --------------------------------------------------------------------------
# Test 11 — per-namespace posture
#
# The combination is the whole feature: act where remediation has been
# earned, report everywhere else, in one install. One strategy, two
# namespaces, and the only thing that differs is where the alert points.
#
# It cannot be tested without a cluster because the interesting failure is
# not "the flag was misread" — it is the posture and the record disagreeing,
# which needs a real resource to disagree on.
# --------------------------------------------------------------------------
step "11. Live in one namespace, reporting in another"

helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set dryRun=true \
	--set namespacePosture.e2e-payments=live \
	--set actions.deploymentRestart.enabled=true \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
start_port_forward

if kubectl -n "$NAMESPACE" get deploy remedik \
	-o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q 'namespace-posture=e2e-payments=live'; then
	pass "the chart passed the override to the operator"
else
	fail "the operator was not given the namespace override"
fi

# send_posture_alert <namespace> <fingerprint> -> HTTP status
send_posture_alert() {
	send_labeled_alert E2EPosture "$2" "\"namespace\":\"$1\",\"deployment\":\"api\""
}

# The default is dry-run, and this namespace is not overridden.
before_reporting=$(restart_annotation_in e2e-reporting api)
status=$(send_posture_alert e2e-reporting posture-1)
if [ "$status" = "200" ]; then
	pass "gateway accepted the alert for the reporting namespace"
else
	fail "gateway answered $status"
fi

if wait_for_strategy_state e2e-posture Simulated 90; then
	pass "the un-overridden namespace was simulated, as the default says"
	if [ "$(restart_annotation_in e2e-reporting api)" = "$before_reporting" ]; then
		pass "and nothing in it was actually restarted"
	else
		fail "a simulated remediation restarted the deployment"
	fi
else
	fail "the reporting namespace did not produce a Simulated record"
fi

# The same strategy, the same alert name, a namespace that was made live.
before_payments=$(restart_annotation_in e2e-payments api)
status=$(send_posture_alert e2e-payments posture-2)
if wait_for_strategy_state e2e-posture Succeeded 120; then
	pass "the live namespace acted although the default is dry-run"
	after_payments=$(restart_annotation_in e2e-payments api)
	if [ -n "$after_payments" ] && [ "$after_payments" != "$before_payments" ]; then
		pass "and the deployment really was restarted: ${after_payments}"
	else
		fail "the record says Succeeded but nothing was restarted"
	fi
else
	fail "the live namespace did not act (gateway answered ${status})"
fi

# Each record carries the posture it ran under, so neither needs the values
# file to be explained.
simulated_flag=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/strategy=e2e-posture \
	-o jsonpath='{range .items[?(@.status.state=="Simulated")]}{.spec.dryRun}{end}' 2>/dev/null || true)
live_flag=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/strategy=e2e-posture \
	-o jsonpath='{range .items[?(@.status.state=="Succeeded")]}{.spec.dryRun}{end}' 2>/dev/null || true)
if [ "$simulated_flag" = "true" ] && [ "$live_flag" = "false" ]; then
	pass "each record states which posture it ran under, false included"
else
	fail "the records do not carry their posture (simulated=${simulated_flag}, live=${live_flag})"
fi

# And the operator says so where somebody would look for it.
if kubectl -n "$NAMESPACE" logs deploy/remedik --tail=200 2>/dev/null | grep -q 'posture is mixed'; then
	pass "the operator warns that the default does not describe the cluster"
else
	fail "no mixed-posture warning was logged"
fi

# Port-forwarded from the host rather than probed from a pod: pulling a curl
# image would need registry access this cluster does not have.
kubectl -n "$NAMESPACE" port-forward svc/remedik-metrics "${METRICS_PORT}:8080" >/dev/null 2>&1 &
METRICS_FORWARD_PID=$!
metrics_body=""
for _ in $(seq 1 20); do
	metrics_body=$(curl -s --max-time 2 "http://127.0.0.1:${METRICS_PORT}/metrics" || true)
	[ -n "$metrics_body" ] && break
	sleep 1
done
kill "$METRICS_FORWARD_PID" 2>/dev/null || true
wait "$METRICS_FORWARD_PID" 2>/dev/null || true
METRICS_FORWARD_PID=""

if echo "$metrics_body" | grep -q 'remedik_namespace_posture{namespace="e2e-payments",posture="live"} 1'; then
	pass "the override is a metric, so the posture is queryable"
else
	fail "remedik_namespace_posture does not report the override"
fi
if echo "$metrics_body" | grep -q '^remedik_dry_run 1'; then
	pass "and remedik_dry_run still reports the default, which is 1"
else
	fail "remedik_dry_run does not report the default"
fi

# --------------------------------------------------------------------------
# Summary
# --------------------------------------------------------------------------
step "Remediation records"
kubectl -n "$NAMESPACE" get remediations -o wide || true

step "Result"
printf '    %d passed, %d failed\n\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
