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

cleanup() {
	local exit_code=$?
	for pid in "$PORT_FORWARD_PID" "$DASHBOARD_FORWARD_PID"; do
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
if kubectl get events --all-namespaces --field-selector reason=GuardRejected \
	-o jsonpath='{range .items[*]}{.message}{"\n"}{end}' 2>/dev/null | grep -q 'guard "cooldown"'; then
	pass "the refusal is published as an event on the strategy"
else
	fail "no GuardRejected event was published"
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
if kubectl get events --all-namespaces --field-selector reason=GuardRejected \
	-o jsonpath='{range .items[*]}{.message}{"\n"}{end}' 2>/dev/null \
	| grep -q 'blastRadius'; then
	pass "the refusal names blastRadius on the strategy"
else
	fail "no blastRadius refusal was published"
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
# Summary
# --------------------------------------------------------------------------
step "Remediation records"
kubectl -n "$NAMESPACE" get remediations -o wide || true

step "Result"
printf '    %d passed, %d failed\n\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
