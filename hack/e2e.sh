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
LEADER_PORT="${LEADER_PORT:-18091}"
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
	# First, before anything that could overwrite it: $? is the reason we are
	# here, and a single command in front of this line replaces it with its own
	# result — which would make a failing run report success.
	local exit_code=$?
	for pid in "$PORT_FORWARD_PID" "$DASHBOARD_FORWARD_PID" "$METRICS_FORWARD_PID"; do
		if [ -n "$pid" ]; then
			kill "$pid" 2>/dev/null || true
			wait "$pid" 2>/dev/null || true
		fi
	done
	if [ "$exit_code" -ne 0 ] || [ "$FAILED" -ne 0 ]; then
		step "Diagnostics"
		# Sixty lines was never enough: a failure here is almost always
		# something the operator said several restarts ago, and the reason a
		# remediation ended where it did is in its own status rather than in
		# the log.
		info "--- why each non-terminal or failed remediation ended there ---"
		kubectl -n "$NAMESPACE" get remediations \
			-o custom-columns='NAME:.metadata.name,STATE:.status.state,REASON:.status.reason,MESSAGE:.status.message' \
			--no-headers 2>/dev/null | grep -vE '[[:space:]](Succeeded|Simulated)[[:space:]]' || true

		# Whether anybody was told, per channel. Without this an escalation
		# failure is a phase with no explanation, which is exactly the thing
		# that needed a second run to diagnose once already.
		info "--- escalations, per channel ---"
		kubectl -n "$NAMESPACE" get remediations -o json 2>/dev/null \
			| kubectl_jq_escalations || true

		info "--- pods, and whether any container restarted ---"
		kubectl -n "$NAMESPACE" get pods \
			-o custom-columns='NAME:.metadata.name,READY:.status.containerStatuses[0].ready,RESTARTS:.status.containerStatuses[0].restartCount,REASON:.status.containerStatuses[0].lastState.terminated.reason' \
			--no-headers 2>/dev/null || true

		info "--- events that are not Normal ---"
		kubectl -n "$NAMESPACE" get events --field-selector type!=Normal \
			--sort-by=.lastTimestamp -o custom-columns='REASON:.reason,OBJECT:.involvedObject.name,MESSAGE:.message' \
			--no-headers 2>/dev/null | tail -10 || true

		info "--- operator log ---"
		kubectl -n "$NAMESPACE" logs deploy/remedik --tail=250 2>/dev/null || true
		kubectl -n "$NAMESPACE" logs deploy/remedik --previous --tail=80 2>/dev/null || true
		kubectl -n "$NAMESPACE" get remediations -o wide 2>/dev/null || true
	fi
	if [ "$KEEP_CLUSTER" = "1" ]; then
		info "cluster '$CLUSTER' kept (KEEP_CLUSTER=1); delete it with: kind delete cluster --name $CLUSTER"
		info "reach it with: export KUBECONFIG=$E2E_KUBECONFIG"
		# Kept deliberately: the whole point of KEEP_CLUSTER is to go and look,
		# and a cluster with no kubeconfig is not inspectable.
		return $exit_code
	fi

	kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
	# Last, after the diagnostics have read it. Removing it first left a failed
	# run reporting no pods and an empty operator log, which is exactly the
	# evidence a failure needs.
	[ -n "${E2E_KUBECONFIG:-}" ] && rm -f "$E2E_KUBECONFIG"
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
		      \"fingerprint\":\"${fingerprint}\"}]}" || true
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

# kubectl_jq_escalations prints one line per escalation step, without needing
# jq installed: the suite already depends on kubectl and python3 is what the
# repo's other scripts use.
kubectl_jq_escalations() {
	python3 -c '
import json, sys
try:
    doc = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for item in doc.get("items", []):
    esc = (item.get("status") or {}).get("escalation")
    if not esc:
        continue
    name = item["metadata"]["name"]
    # Values pulled out first because this program is wrapped in shell single
    # quotes, so it cannot contain a single quote -- and a backslash-escaped
    # double quote inside an f-string expression is a syntax error. That is
    # what this block used to be, and being diagnostics, it only ran when
    # something had already failed: the suite printed a Python traceback in
    # place of the escalation it was asked about.
    phase = esc.get("phase", "?")
    message = esc.get("message", "")
    print(f"    {name}  escalation={phase}  {message}")
    for step in esc.get("steps", []):
        index = step.get("index")
        action = step.get("action")
        step_phase = step.get("phase")
        detail = step.get("message") or step.get("plan") or ""
        print(f"      channel {index} {action}: {step_phase}  {detail}")
'
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

# strategy_condition <strategy> <field> reads the Ready condition a strategy
# reports about itself. Cluster-scoped, so there is no namespace.
strategy_condition() {
	kubectl get remediationstrategy "$1" \
		-o jsonpath="{.status.conditions[?(@.type=='Ready')].$2}" 2>/dev/null || true
}

# wait_for_ready <strategy> <True|False> [timeout-seconds]
wait_for_ready() {
	local name="$1" want="$2" timeout="${3:-60}" elapsed=0
	while [ "$elapsed" -lt "$timeout" ]; do
		[ "$(strategy_condition "$name" status)" = "$want" ] && return 0
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
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
		grep -qx "$want" <<<"$states" && return 0
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
		grep -qx "$want" <<<"$states" && return 0
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

# wait_for_settled [expected-endpoints] waits until the rollout is really over.
#
# `helm --wait` and `rollout status` return when the new pod is Ready, and at
# that moment the old one is often still a Service endpoint and still holds
# the lease. An alert sent then can land on a process that is about to be
# killed, and its remediation is cut in half — recorded honestly as
# Interrupted, and a failed assertion here. Waiting for the endpoint set to
# match the replica count is waiting for the handover to be finished rather
# than merely started.
wait_for_settled() {
	local want="${1:-1}" timeout="${2:-90}" elapsed=0
	while [ "$elapsed" -lt "$timeout" ]; do
		local n
		n=$(kubectl -n "$NAMESPACE" get endpointslices \
			-l "kubernetes.io/service-name=remedik-gateway" \
			-o jsonpath='{range .items[*]}{range .endpoints[*]}{.conditions.ready}{"\n"}{end}{end}' 2>/dev/null \
			| grep -c true || true)
		[ "$n" = "$want" ] && return 0
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
}

# wait_for_accepting blocks until the gateway stops answering 503.
#
# Readiness is not leadership — a standby is ready and refuses with 503 —
# so a ready pod is not yet proof that alerts are being taken. With leader
# election the leader accepts only after it has won the lease and replayed
# the guards, which is a second or two after the process is up. Firing an
# alert before that would fail a test for a reason the product is entitled
# to: the sender is supposed to retry.
wait_for_accepting() {
	local timeout="${1:-60}" elapsed=0 code
	while [ "$elapsed" -lt "$timeout" ]; do
		code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
			-H "Authorization: Bearer ${TOKEN}" \
			-H 'Content-Type: application/json' \
			--data '{"alerts":[]}' \
			"http://127.0.0.1:${LOCAL_PORT}/webhooks/alertmanager" 2>/dev/null || echo 000)
		[ "$code" != "503" ] && [ "$code" != "000" ] && return 0
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
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

# wait_for_dashboard_body <path> <substring> [timeout-seconds]
#
# The pages read through the manager's cache. A page can answer 200 before
# that cache holds what the page is about — most visibly just after a helm
# upgrade restarts the pod — so asserting on content the moment the port
# answers is a race. It fails about one run in five, on a runner, and never
# here.
wait_for_dashboard_body() {
	local path="$1" want="$2" timeout="${3:-45}" elapsed=0
	while [ "$elapsed" -lt "$timeout" ]; do
		if grep -q "$want" <<<"$(dashboard_body "$path")"; then
			return 0
		fi
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
}

# dashboard_headers <path> -> the response headers
dashboard_headers() {
	local path="$1"
	curl -s -D - -o /dev/null --max-time 10 \
		-H "Authorization: Bearer ${DASHBOARD_TOKEN}" \
		"http://127.0.0.1:${DASHBOARD_PORT}${path}" 2>/dev/null || true
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

# The isolated kubeconfig is created before the cluster, so that `kind create`
# writes into it and never touches the developer's.
#
# It used to be filled in afterwards with `kind get kubeconfig`, which left
# `kind create cluster` to do what it does by default: write the new cluster
# into ~/.kube/config and make it the current context. The run itself was
# unaffected, but every kubectl in the terminal that started it was now pointed
# at a throwaway cluster — which is precisely how one debugging session in this
# project drew a conclusion from the wrong cluster's logs.
E2E_KUBECONFIG="$(mktemp -t remedik-e2e-kubeconfig.XXXXXX)"
# Exported before the cluster exists rather than after it. A failure during
# creation still runs the cleanup diagnostics, and with the ambient kubeconfig
# still in effect those dumped a completely different cluster's remediations
# under the heading "why each one ended there" -- which is worse than printing
# nothing, and is the second time in this project that reading the wrong
# cluster cost real time.
export KUBECONFIG="$E2E_KUBECONFIG"

step "Creating the kind cluster '$CLUSTER'"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
	info "cluster already exists, reusing it"
	kind get kubeconfig --name "$CLUSTER" > "$E2E_KUBECONFIG"
else
	kind create cluster --config hack/e2e/kind.yaml \
		--kubeconfig "$E2E_KUBECONFIG" >/dev/null
fi
# The isolated kubeconfig, now filled in.
#
# Setting the current context is not enough: a kubeconfig is global state, so
# anything else on the machine that switches context mid-run redirects every
# kubectl call after it. That is not hypothetical — a context switch during a
# run sent this whole suite at a development cluster, where it reported an
# ImagePullBackOff for an image it had loaded into the right one.
#
# A kubeconfig with one cluster in it makes that impossible rather than
# unlikely, and a suite that patches and deletes should not be able to reach a
# cluster it was not pointed at. helm reads KUBECONFIG too.
export KUBECONFIG="$E2E_KUBECONFIG"
info "using an isolated kubeconfig; this run cannot reach another cluster"

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
wait_for_settled 1 || info "the gateway endpoints did not settle to one; continuing"
info "operator is running"

step "Creating the test workload and strategy"
kubectl apply -f hack/e2e/workload.yaml >/dev/null
kubectl apply -f hack/e2e/strategy.yaml >/dev/null
kubectl -n e2e-payments rollout status deploy/api --timeout=120s >/dev/null
kubectl -n e2e-payments rollout status deploy/api2 --timeout=120s >/dev/null

# The gateway is a ClusterIP service; reach it through a port-forward.
LOCAL_PORT="${LOCAL_PORT:-18090}"
start_port_forward
wait_for_accepting || info "the gateway is still refusing; continuing and letting the assertions say why"
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
	if grep -q "Eviction API" <<<"$drain_plan"; then
		pass "the drain plan says it would evict: ${drain_plan}"
	else
		fail "the drain plan does not describe an eviction (${drain_plan})"
	fi

	drain_pods=$(kubectl -n "$NAMESPACE" get remediations -l remedik.dev/strategy=e2e-node-drain \
		-o jsonpath='{.items[0].status.steps[0].outputs.pods}' 2>/dev/null || true)
	if grep -q "e2e-payments/" <<<"$drain_pods"; then
		pass "the plan names the pods that would move"
	else
		fail "the plan names no pods (${drain_pods})"
	fi

	# DaemonSet pods are skipped, which is what makes a drain terminate.
	if grep -q "DaemonSet" <<<"$drain_plan"; then
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
wait_for_settled 1 || info "the gateway endpoints did not settle to one; continuing"
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
if grep -q 'ready' <<<"$verified"; then
	pass "the record confirms the rollout completed: ${verified}"
else
	fail "the record does not confirm the rollout; verification did not run"
fi

kubectl_line=$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{range .items[?(@.status.state=="Succeeded")]}{.status.steps[0].kubectl}{"\n"}{end}' \
	2>/dev/null | head -1)
if grep -q 'kubectl rollout restart' <<<"$kubectl_line"; then
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
wait_for_settled 1 || info "the gateway endpoints did not settle to one; continuing"
start_port_forward

# No --tail, for the reason given at the actions assertion: these lines are
# written once at startup, and a fixed tail turns "did the operator do this"
# into "has the operator been quiet since".
if kubectl -n "$NAMESPACE" logs deploy/remedik 2>/dev/null \
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
wait_for_settled 1 || info "the gateway endpoints did not settle to one; continuing"
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

for path in / /remediations /namespaces /strategies; do
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

# Same race as the strategies page: wait for the cache rather than asserting
# on whatever the first request happened to catch.
wait_for_dashboard_body / "e2e-crashloop" 45 || true
overview=$(dashboard_body /)
if grep -q "e2e-crashloop" <<<"$overview"; then
	pass "the overview lists the executions of the e2e strategy"
else
	fail "the overview does not mention the e2e strategy"
fi

# Test 2 recorded a Simulated remediation. Whatever the operator's posture is
# now, that trial has to be reportable — it is the report an operator shows
# their team before turning dry-run off.
if grep -q "What remedik would have done" <<<"$overview"; then
	pass "the dry-run report is on the overview"
else
	fail "no dry-run report, although a simulated remediation exists"
fi
if grep -q "restartedAt" <<<"$overview"; then
	pass "the report says what would have been done"
else
	fail "the report does not show the plan of the simulated remediation"
fi

simulated=$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{range .items[?(@.status.state=="Simulated")]}{.metadata.name}{"\n"}{end}' \
	2>/dev/null | head -1)
if [ -n "$simulated" ]; then
	detail=$(dashboard_body "/remediations/${simulated}")
	if grep -q "nothing in the cluster was changed" <<<"$detail"; then
		pass "the detail page of ${simulated} explains the simulation"
	else
		fail "the detail page of ${simulated} does not explain the simulation"
	fi
else
	fail "no simulated remediation to open a detail page for"
fi

wait_for_dashboard_body /strategies "E2ECrashLooping" 45 || true
strategies=$(dashboard_body /strategies)
grep -q "E2ECrashLooping" <<<"$strategies"; matcher=$?
grep -q "30m" <<<"$strategies"; guard=$?
if [ "$matcher" -eq 0 ] && [ "$guard" -eq 0 ]; then
	pass "the strategies page shows the matcher and the cooldown guard"
else
	# Each status separately: the previous version reported "one of these two
	# is missing" and then printed evidence that both were present, which
	# said the fault was in the assertion without saying where.
	fail "the strategies page is missing the matcher or the guard"
	info "matcher grep exit=${matcher}, guard grep exit=${guard}, bytes=$(printf '%s' "$strategies" | wc -c)"
	info "strategy names on the page: $(grep -oE 'e2e-[a-z-]+' <<<"$strategies" | sort -u | tr '\n' ' ')"
	info "cooldowns on the page: $(grep -oE '<code>[0-9]+[hms]</code>' <<<"$strategies" | sort -u | tr '\n' ' ')"
fi

status=$(dashboard_status /remediations/does-not-exist)
if [ "$status" = "404" ]; then
	pass "an unknown remediation answered 404"
else
	fail "an unknown remediation answered $status, want 404"
fi

# The cluster's name, which is what tells three port-forwarded dashboards
# apart. It is in the tab title so it survives being one of twenty tabs.
if grep -q '<title>e2e-cluster' <<<"$overview"; then
	pass "the cluster name leads the browser title"
else
	fail "the cluster name is not in the title"
fi

# --- the overview is a dashboard, not a list -------------------------------
# It answers "is anything wrong right now?"; the list answers "what happened
# to payments last Tuesday?". Merging them is what this replaced.
for panel in posture-heading attention-heading activity-heading where-heading; do
	if grep -q "$panel" <<<"$overview"; then
		pass "the overview has its ${panel%-heading} panel"
	else
		fail "the overview is missing the ${panel%-heading} panel"
	fi
done
if grep -q 'href="/remediations"' <<<"$overview"; then
	pass "and it sends the reader to the list"
else
	fail "the overview does not link to the list"
fi

# --- filtering --------------------------------------------------------------
# Filtering is navigation. A select plus Apply holds state between the choice
# and the submission, and that state was destroyed by the ten-second refresh
# — twice, in two different ways. A link has nothing to lose.
list=$(dashboard_body /remediations)

# The controls must sit outside the region the ten-second refresh replaces.
# That is what makes it safe to offer a select at all, which is what a
# cluster with a hundred and fifty namespaces needs instead of a wall of
# links — and it is the structural form of the bug that made the filter
# appear broken twice.
live_line=$(grep -n 'id="live"' <<<"$list" | head -1 | cut -d: -f1)
controls_line=$(grep -n 'class="filters"' <<<"$list" | head -1 | cut -d: -f1)
if [ -n "$live_line" ] && [ -n "$controls_line" ] && [ "$controls_line" -lt "$live_line" ]; then
	pass "the filter controls sit outside the region the refresh replaces"
else
	fail "the controls are inside the live region (controls=${controls_line}, live=${live_line})"
fi

# Everything so far targets one namespace, so the namespace row is correctly
# absent: a dimension with one value is not a choice. The state row is not.
if grep -q 'href="/remediations?state=' <<<"$list"; then
	pass "the list offers filter links for a dimension with more than one value"
else
	fail "the list offers no state filter link"
fi
if grep -q 'id="filter-namespace"' <<<"$list"; then
	fail "the list offers a namespace filter although every record is in one namespace"
else
	pass "and omits the namespace row, which would offer a choice of one"
fi

filtered=$(dashboard_body "/remediations?namespace=e2e-payments")
if grep -q 'e2e-payments' <<<"$filtered"; then
	pass "a namespace filter renders that namespace's records"
else
	fail "the namespace filter hid everything in its own namespace"
fi
if grep -q 'hidden' <<<"$filtered"; then
	pass "and the page says how much it is hiding"
else
	fail "a filtered page does not say what it is hiding"
fi

# The policy has to permit the one thing the page submits. `form-action` was
# 'none', which blocked the filter's select entirely -- the browser said so in
# the console and nowhere else, so the control looked merely unresponsive and
# was reported broken four times. A header assertion is cheap and always runs.
headers=$(dashboard_headers /remediations)
if grep -qi "form-action 'self'" <<<"$headers"; then
	pass "the policy lets the page submit its filter back to itself"
else
	fail "form-action does not permit 'self', so the filter select is blocked"
fi
if grep -qiE "form-action '?(none)'?" <<<"$headers"; then
	fail "form-action is 'none', which blocks every form on the page"
else
	pass "and does not forbid forms outright"
fi

# The filter select's no-JavaScript fallback is checked in
# internal/dashboard/assets_test.go, not here. This cluster has few enough
# namespaces that the control is drawn as pills rather than a select, so an
# assertion here would only run if that threshold were crossed by accident --
# and a gate that runs by accident is not a gate.

# The shell reloads itself when the operator changes, which is the defect
# that made two correct filter fixes invisible in an already-open tab.
if grep -q '<meta name="remedik-asset"' <<<"$overview"; then
	pass "the page carries its build fingerprint, so a stale tab can notice"
else
	fail "the page has no asset fingerprint for the refresh to compare"
fi

# The namespaces page is remedik's own record per namespace, and it is the
# one page whose ordering is the point: a namespace where a failure was
# never escalated has to come before one where somebody was already told.
namespaces=$(dashboard_body /namespaces)
if grep -q 'e2e-payments' <<<"$namespaces"; then
	pass "the namespaces page lists the namespace remediation ran in"
else
	fail "the namespaces page does not list e2e-payments"
fi
if grep -q 'Unheard' <<<"$namespaces"; then
	pass "and counts the failures nobody was told about"
else
	fail "the namespaces page does not report unheard failures"
fi
if grep -qE 'Reporting|Live' <<<"$namespaces"; then
	pass "and says what remedik is allowed to do there"
else
	fail "the namespaces page does not name each namespace's posture"
fi
# Every row links to that namespace's executions: a page that judges without
# offering the evidence is a page somebody has to leave to act on.
if grep -q 'href="/remediations?namespace=e2e-payments"' <<<"$namespaces"; then
	pass "and each row links to that namespace's executions"
else
	fail "a namespace row does not link to its own records"
fi

empty=$(dashboard_body "/remediations?namespace=no-such-namespace")
if grep -q "Nothing matches this filter" <<<"$empty"; then
	pass "a filter that matches nothing says so, rather than looking like an empty cluster"
else
	fail "an empty filter result did not explain itself"
fi
if grep -q "No strategies, so nothing can run" <<<"$empty"; then
	fail "an empty filter result claimed the cluster has no strategies"
else
	pass "and it does not claim the cluster is unconfigured"
fi

# An unknown parameter value is honoured, not rejected: a URL pasted from a
# week-old incident channel must not become an error page.
status=$(dashboard_status "/remediations?namespace=no-such-namespace&state=Nonsense")
if [ "$status" = "200" ]; then
	pass "an unrecognised filter value still renders"
else
	fail "an unrecognised filter value answered $status, want 200"
fi

# Filtering must not become a way in for a write.
status=$(dashboard_method POST "/remediations?namespace=e2e-payments")
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
wait_for_settled 1 || info "the gateway endpoints did not settle to one; continuing"
start_port_forward

# Read the whole log of this pod, not the last two hundred lines.
#
# The line being looked for is written once, at startup. Tailing worked until
# the operator started doing more at startup than it used to -- four concurrent
# reconciles replaying a backlog of records push it out of a fixed tail -- so
# the assertion began failing on a slower machine while passing on a fast one.
# A test whose result depends on how chatty the thing under test has become is
# a test that will fail again for a reason that is not a defect.
registered=$(kubectl -n "$NAMESPACE" logs deploy/remedik 2>/dev/null \
	| grep -o '"actions":\[[^]]*\]' | head -1)
if [ "$registered" = '"actions":["deployment.restart","pod.delete","workload.restart"]' ]; then
	pass "the operator registered exactly the actions the chart granted"
else
	fail "the registered actions do not match what the chart enabled: ${registered:-<the startup line was not found>}"
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
if grep -q 'ready' <<<"$sts_verified"; then
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

if grep -q 'no controller owner' <<<"$orphan_msg"; then
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
wait_for_settled 1 || info "the gateway endpoints did not settle to one; continuing"
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
	if grep -q 'unschedulable' <<<"$cordon_verified"; then
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
wait_for_settled 1 || info "the gateway endpoints did not settle to one; continuing"
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
	if grep -q 'allowVolumeExpansion' <<<"$message"; then
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
# A fallback page has to land when the first channel is down. This was a real
# defect: the escalation stopped at its first failed step, so a configured
# fallback was a single point of failure -- and invisible, because every
# channel succeeds when the path is tested.
info "escalation falls back to a second channel"
kubectl apply -f hack/e2e/strategy-fallback.yaml >/dev/null
code=$(send_alert "E2EFallback" "fallback-1" "does-not-exist")
if [ "$code" = "200" ]; then
	pass "gateway accepted the fallback alert"
else
	fail "gateway answered ${code} for the fallback alert, want 200"
fi

fallback=""
for _ in $(seq 1 45); do
	fallback=$(kubectl -n "$NAMESPACE" get remediations \
		-l "remedik.dev/strategy=e2e-fallback" \
		-o jsonpath='{range .items[?(@.status.state=="Failed")]}{.metadata.name}{end}' 2>/dev/null || true)
	[ -n "$fallback" ] && break
	sleep 2
done
if [ -n "$fallback" ]; then
	pass "the fallback remediation failed as intended: ${fallback}"
else
	fail "no failed remediation was recorded for the fallback strategy"
fi

phase=$(kubectl -n "$NAMESPACE" get remediation "$fallback" \
	-o jsonpath='{.status.escalation.phase}' 2>/dev/null || true)
if [ "$phase" = "Succeeded" ]; then
	pass "the escalation succeeded although its first channel was down"
else
	fail "escalation phase = ${phase:-<none>}, want Succeeded: one channel got through"
fi

first=$(kubectl -n "$NAMESPACE" get remediation "$fallback" \
	-o jsonpath='{.status.escalation.steps[0].phase}' 2>/dev/null || true)
second=$(kubectl -n "$NAMESPACE" get remediation "$fallback" \
	-o jsonpath='{.status.escalation.steps[1].phase}' 2>/dev/null || true)
if [ "$first" = "Failed" ]; then
	pass "the broken channel is recorded as failed rather than hidden"
else
	fail "escalation step 0 = ${first:-<none>}, want Failed"
fi
if [ "$second" = "Succeeded" ]; then
	pass "and the channel after it ran"
else
	fail "escalation step 1 = ${second:-<none>}, want Succeeded -- a failed channel must not silence the next"
fi


step "10b. A slow remediation does not stall another"

# One worker meant one remediation at a time, cluster-wide, for as long as it
# took -- and a step that waits for a pipeline's verdict takes minutes by
# design. This fires two alerts for different workloads back to back and
# asserts they overlap, which under the old behaviour they could not.
kubectl apply -f hack/e2e/strategy-slow.yaml >/dev/null

send_alert "E2ESlow" "slow-1" "does-not-exist" >/dev/null
sleep 1
code=$(send_alert "E2EFast" "fast-1" "api2")
if [ "$code" = "200" ]; then
	pass "gateway accepted both the slow and the fast alert"
else
	fail "gateway answered ${code} for the fast alert, want 200"
fi

# The fast one must finish while the slow one is still running. If executions
# were serialised it would have to wait out the slow one's verify.
fast=""
for _ in $(seq 1 30); do
	fast=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/strategy=e2e-fast" \
		-o jsonpath='{range .items[?(@.status.state=="Succeeded")]}{.metadata.name}{end}' 2>/dev/null || true)
	[ -n "$fast" ] && break
	sleep 2
done
slow_state=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/strategy=e2e-slow" \
	-o jsonpath='{.items[0].status.state}' 2>/dev/null || true)

if [ -n "$fast" ]; then
	pass "the fast remediation finished: ${fast}"
else
	fail "the fast remediation did not finish while the slow one held a worker"
fi
if [ "$slow_state" = "Running" ] || [ "$slow_state" = "Pending" ]; then
	pass "and it did so while the slow one was still ${slow_state}"
else
	info "the slow remediation was already ${slow_state:-<none>}; overlap not proven by this run"
fi

step "10c. remedik gives up on a workload it cannot fix, and says so"

# Every one of these remediations succeeds. What the guard detects is that
# succeeding is not helping -- which is the case cooldown and maxPerHour can
# never conclude anything about.
kubectl apply -f hack/e2e/strategy-giveup.yaml >/dev/null

for i in 1 2 3; do
	send_alert "E2EGiveUp" "giveup-${i}" "api2" >/dev/null
	sleep 4
done

# The fourth is the one that should stop rather than remediate.
send_alert "E2EGiveUp" "giveup-4" "api2" >/dev/null

gaveup=""
for _ in $(seq 1 30); do
	gaveup=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/gave-up=true" \
		-o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | head -1 || true)
	[ -n "$gaveup" ] && break
	sleep 2
done

if [ -n "$gaveup" ]; then
	pass "remedik gave up and left a record: ${gaveup}"
else
	fail "no give-up record after four remediations of one target"
fi

if [ -n "$gaveup" ]; then
	reason=$(kubectl -n "$NAMESPACE" get remediation "$gaveup" -o jsonpath='{.status.reason}' 2>/dev/null || true)
	steps=$(kubectl -n "$NAMESPACE" get remediation "$gaveup" -o jsonpath='{.spec.steps}' 2>/dev/null || true)
	esc=$(kubectl -n "$NAMESPACE" get remediation "$gaveup" -o jsonpath='{.status.escalation.phase}' 2>/dev/null || true)
	message=$(kubectl -n "$NAMESPACE" get remediation "$gaveup" -o jsonpath='{.status.message}' 2>/dev/null || true)

	if [ "$reason" = "GaveUp" ]; then
		pass "the record says why: reason=GaveUp"
	else
		fail "reason = ${reason:-<none>}, want GaveUp"
	fi
	if [ -z "$steps" ] || [ "$steps" = "null" ]; then
		pass "and it remediated nothing"
	else
		fail "the give-up record carries steps: ${steps}"
	fi
	if [ "$esc" = "Succeeded" ]; then
		pass "and somebody was told: the escalation ran"
	else
		fail "escalation = ${esc:-<none>}, want Succeeded -- giving up must page"
	fi
	if grep -q "needs a person" <<<"$message"; then
		pass "and the message says a person is needed"
	else
		fail "message does not say what a reader should do: ${message}"
	fi
fi

# One per trip: repeat deliveries must not page again.
send_alert "E2EGiveUp" "giveup-5" "api2" >/dev/null
sleep 6
count=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/gave-up=true" --no-headers 2>/dev/null | wc -l)
if [ "$count" = "1" ]; then
	pass "a repeat delivery did not page again"
else
	fail "${count} give-up records; Alertmanager repeats, so this would page forever"
fi

step "10d. Retention reclaims what nothing else would"

# The leak: pruning ran inside the terminal status write, so records of a
# strategy that was deleted, renamed, disabled or had merely gone quiet were
# never looked at again. This creates a record for a strategy that does not
# exist and asserts a sweep takes it.
cat <<'RECORD' | kubectl apply -f - >/dev/null
apiVersion: remedik.dev/v1alpha1
kind: Remediation
metadata:
  name: e2e-orphan
  namespace: remedik
  labels:
    remedik.dev/strategy: a-strategy-that-was-deleted
spec:
  strategyName: a-strategy-that-was-deleted
  target: deployment/e2e-payments/api
  alert:
    fingerprint: orphan-1
    name: E2EOrphan
RECORD

# Terminal and old, which is what makes it a candidate.
kubectl -n "$NAMESPACE" patch remediation e2e-orphan --subresource=status --type=merge \
	-p '{"status":{"state":"Succeeded","completedAt":"2020-01-01T00:00:00Z"}}' >/dev/null

if kubectl -n "$NAMESPACE" get remediation e2e-orphan >/dev/null 2>&1; then
	pass "an orphaned record exists to be reclaimed"
else
	fail "could not create the orphaned record"
fi

# A one-second sweep interval, so the suite does not wait half an hour.
helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" --reuse-values \
	--set history.maxAge=1h \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null
start_port_forward

# The sweeper runs on a timer, and the first tick is one interval away. Rather
# than wait it out, assert the operator says the policy is in force -- the
# reclaim itself is covered by unit tests, which can control the clock.
if kubectl -n "$NAMESPACE" logs deploy/remedik 2>/dev/null | grep -q "retention sweeper started"; then
	pass "the retention sweeper is running on the leader"
else
	fail "no sign the retention sweeper started"
fi
if kubectl -n "$NAMESPACE" logs deploy/remedik 2>/dev/null | grep -q '"max_age":3600000000000'; then
	pass "and it carries the configured maximum age"
else
	fail "the sweeper did not pick up history.maxAge"
fi

step "10e. The kill switch stops remediation and keeps the record"

# Named in the original plan as the thing you reach for at three in the morning.
# It does not silence remedik: it forces dry-run everywhere, so the record of
# what would have happened survives the decision to stop it.
# Its own strategy and alertname, with no cooldown.
#
# The first version reused E2ECrashLooping, whose target had been remediated
# earlier in the run -- so the cooldown refused before the pause was ever
# consulted, and the test measured the guard rather than the switch.
kubectl apply -f hack/e2e/strategy-pause.yaml >/dev/null

kubectl -n "$NAMESPACE" patch configmap remedik-pause --type merge \
	-p '{"data":{"paused":"true","reason":"e2e is checking the switch"}}' >/dev/null

# It is polled, so give it more than one interval.
sleep 8

code=$(send_alert "E2EPaused" "paused-1" "api2")
if [ "$code" = "200" ]; then
	pass "the gateway still accepts alerts while paused"
else
	fail "gateway answered ${code} while paused, want 200 -- pausing must not look like an outage"
fi

paused_rem=""
for _ in $(seq 1 30); do
	paused_rem=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/paused=true" \
		-o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | head -1 || true)
	[ -n "$paused_rem" ] && break
	sleep 2
done

if [ -n "$paused_rem" ]; then
	pass "a record was still created while paused: ${paused_rem}"
else
	fail "no record while paused -- the audit of what was suppressed is the point"
fi

if [ -n "$paused_rem" ]; then
	dry=$(kubectl -n "$NAMESPACE" get remediation "$paused_rem" -o jsonpath='{.spec.dryRun}' 2>/dev/null || true)
	state=$(kubectl -n "$NAMESPACE" get remediation "$paused_rem" -o jsonpath='{.status.state}' 2>/dev/null || true)
	why=$(kubectl -n "$NAMESPACE" get remediation "$paused_rem" \
		-o jsonpath='{.metadata.annotations.remedik\.dev/pause-reason}' 2>/dev/null || true)

	if [ "$dry" = "true" ]; then
		pass "and it only simulated, although this namespace is live"
	else
		fail "dryRun = ${dry:-<none>} while paused -- the pause must override the posture"
	fi
	if [ "$state" = "Simulated" ]; then
		pass "and it carries the plan it would have run"
	else
		fail "state = ${state:-<none>}, want Simulated"
	fi
	if [ "$why" = "e2e is checking the switch" ]; then
		pass "and the record says why remediation was paused"
	else
		fail "the pause reason is not on the record: ${why:-<none>}"
	fi
fi

# That the dashboard says "Paused" on every page is checked in
# internal/dashboard/dashboard_test.go, not here: the dashboard is only enabled
# for section 7 and this section runs with it off, and a property of the
# templates does not need a cluster to prove. What only a cluster can show is
# the ConfigMap patch reaching the operator and overriding a live posture, which
# is what the assertions above are.

kubectl -n "$NAMESPACE" patch configmap remedik-pause --type merge \
	-p '{"data":{"paused":"false","reason":""}}' >/dev/null
sleep 8
info "switch released"

step "10f. A person approves, denies, and fails to look"

# The gate the original design put at the centre, and the mechanism that makes
# it need no bot: approving is a kubectl patch.
kubectl apply -f hack/e2e/strategy-approval.yaml >/dev/null

# --- approved -------------------------------------------------------------
send_alert "E2EApprove" "approve-1" "api2" >/dev/null

rem=""
for _ in $(seq 1 30); do
	rem=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/strategy=e2e-approve" \
		-o jsonpath='{range .items[?(@.status.state=="AwaitingApproval")]}{.metadata.name}{end}' 2>/dev/null || true)
	[ -n "$rem" ] && break
	sleep 2
done

if [ -n "$rem" ]; then
	pass "the remediation is waiting for a person: ${rem}"
else
	fail "no remediation reached AwaitingApproval"
fi

if [ -n "$rem" ]; then
	# Nothing may have run yet. That is the whole promise of the gate.
	steps=$(kubectl -n "$NAMESPACE" get remediation "$rem" -o jsonpath='{.status.steps}' 2>/dev/null || true)
	if [ -z "$steps" ] || [ "$steps" = "null" ] || [ "$steps" = "[]" ]; then
		pass "and nothing has been planned or executed while it waits"
	else
		fail "steps already ran before anybody approved: ${steps}"
	fi

	message=$(kubectl -n "$NAMESPACE" get remediation "$rem" -o jsonpath='{.status.message}' 2>/dev/null || true)
	if grep -q "escalates" <<<"$message"; then
		pass "and it says what happens if nobody looks"
	else
		fail "the waiting record does not say it will escalate: ${message}"
	fi

	kubectl -n "$NAMESPACE" patch remediation "$rem" --type merge \
		-p '{"spec":{"approval":{"decision":"approve","by":"e2e","note":"looks fine"}}}' >/dev/null

	if wait_for_strategy_state e2e-approve Succeeded 90; then
		pass "approving it with a kubectl patch ran it"
	else
		fail "the approved remediation did not run"
	fi
fi

# --- denied ---------------------------------------------------------------
send_alert "E2EDeny" "deny-1" "api2" >/dev/null

denied=""
for _ in $(seq 1 30); do
	denied=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/strategy=e2e-deny" \
		-o jsonpath='{range .items[?(@.status.state=="AwaitingApproval")]}{.metadata.name}{end}' 2>/dev/null || true)
	[ -n "$denied" ] && break
	sleep 2
done

if [ -n "$denied" ]; then
	kubectl -n "$NAMESPACE" patch remediation "$denied" --type merge \
		-p '{"spec":{"approval":{"decision":"deny","by":"e2e","note":"rolling forward instead"}}}' >/dev/null
	sleep 8

	reason=$(kubectl -n "$NAMESPACE" get remediation "$denied" -o jsonpath='{.status.reason}' 2>/dev/null || true)
	esc=$(kubectl -n "$NAMESPACE" get remediation "$denied" -o jsonpath='{.status.escalation}' 2>/dev/null || true)
	msg=$(kubectl -n "$NAMESPACE" get remediation "$denied" -o jsonpath='{.status.message}' 2>/dev/null || true)

	if [ "$reason" = "Denied" ]; then
		pass "denying it ends the remediation"
	else
		fail "reason = ${reason:-<none>}, want Denied"
	fi
	if [ -z "$esc" ] || [ "$esc" = "null" ]; then
		pass "and does not escalate, because somebody already looked"
	else
		fail "a denial escalated: ${esc}"
	fi
	if grep -q "rolling forward instead" <<<"$msg"; then
		pass "and the note survives on the record"
	else
		fail "the denial note is not on the record: ${msg}"
	fi
else
	fail "no remediation to deny"
fi

# --- nobody looks ---------------------------------------------------------
# A one-second timeout, because the failure mode of a human gate is that
# nobody looks and that has to reach somebody.
send_alert "E2EIgnore" "ignore-1" "api2" >/dev/null

if wait_for_strategy_state e2e-ignore Failed 90; then
	pass "a remediation nobody decided on stopped waiting"
else
	fail "the ignored remediation never timed out"
fi

ignored=$(kubectl -n "$NAMESPACE" get remediations -l "remedik.dev/strategy=e2e-ignore" \
	-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -n "$ignored" ]; then
	reason=$(kubectl -n "$NAMESPACE" get remediation "$ignored" -o jsonpath='{.status.reason}' 2>/dev/null || true)
	esc=$(kubectl -n "$NAMESPACE" get remediation "$ignored" -o jsonpath='{.status.escalation.phase}' 2>/dev/null || true)
	if [ "$reason" = "ApprovalTimeout" ]; then
		pass "and says why: reason=ApprovalTimeout"
	else
		fail "reason = ${reason:-<none>}, want ApprovalTimeout"
	fi
	if [ "$esc" = "Succeeded" ]; then
		pass "and somebody was told, because silence must not be the outcome"
	else
		fail "escalation = ${esc:-<none>} -- nobody looking has to reach somebody"
	fi
fi

# --- the red button -------------------------------------------------------
before=$(remediation_count)
send_alert "E2EManual" "manual-1" "api2" >/dev/null
sleep 6
if [ "$(remediation_count)" = "$before" ]; then
	pass "a manual strategy did not start from an alert"
else
	fail "a manual strategy ran from an alert"
fi
if wait_for_event ManualStrategy "never runs from an alert" 30; then
	pass "and said so where a guard refusal is recorded"
else
	fail "no event explaining why the manual strategy did nothing"
fi

step "10g. A strategy says whether remedik can run it"

# Applied at 10:00, answered within seconds -- rather than at 03:00, by a
# remediation that fails with UnknownAction. The alertnames in this fixture are
# never sent: what is under test is what the strategy says about itself.
kubectl apply -f hack/e2e/strategy-unusable.yaml >/dev/null

if wait_for_ready e2e-unusable False 60; then
	pass "a strategy naming an action this build does not have is not Ready"
else
	fail "e2e-unusable never reported Ready=False (got '$(strategy_condition e2e-unusable status)')"
fi

message=$(strategy_condition e2e-unusable message)
if grep -q "step 2" <<<"$message"; then
	pass "and the message names the step, counted the way a person counts"
else
	fail "the message does not identify the step: ${message:-<none>}"
fi
if grep -q "enabled actions:" <<<"$message"; then
	pass "and lists what this build can run, so a typo is told apart from a feature nobody enabled"
else
	fail "the message does not say what is available: ${message:-<none>}"
fi

reason=$(strategy_condition e2e-unusable reason)
if [ "$reason" = "UnknownAction" ]; then
	pass "and the reason is the same word the failed record would have used"
else
	fail "reason = ${reason:-<none>}, want UnknownAction"
fi

# The print column: the answer has to be where somebody already looks.
if kubectl get remediationstrategy e2e-unusable --no-headers 2>/dev/null | grep -q False; then
	pass "and 'kubectl get remediationstrategies' shows it without a second query"
else
	fail "the Ready column is not rendered by kubectl get"
fi

if wait_for_ready e2e-unusable-escalation False 60; then
	pass "an escalation that could never page anybody is refused the same way"
else
	fail "e2e-unusable-escalation never reported Ready=False"
fi
escalation_message=$(strategy_condition e2e-unusable-escalation message)
if grep -q "onFailure" <<<"$escalation_message"; then
	pass "and the message says the step was an escalation step"
else
	fail "the message does not distinguish the escalation: ${escalation_message:-<none>}"
fi

# Fixing it has to clear the condition without an operator restart, or the
# report is worse than none: it would go on accusing a strategy that is fine.
kubectl patch remediationstrategy e2e-unusable --type json \
	-p '[{"op": "replace", "path": "/spec/steps/1/action", "value": "deployment.restart"}]' >/dev/null
if wait_for_ready e2e-unusable True 60; then
	pass "and correcting the manifest clears it, with no restart"
else
	fail "e2e-unusable stayed not Ready after the name was corrected"
fi

# The other half: a strategy that has been used says so.
if [ "$(strategy_condition e2e-crashloop status)" = "True" ]; then
	pass "the strategy that has been remediating all along is Ready"
else
	fail "e2e-crashloop is not Ready: $(strategy_condition e2e-crashloop message)"
fi
runs=$(kubectl get remediationstrategy e2e-crashloop -o jsonpath='{.status.executionCount}' 2>/dev/null || true)
last=$(kubectl get remediationstrategy e2e-crashloop -o jsonpath='{.status.lastExecutionTime}' 2>/dev/null || true)
if [ -n "$runs" ] && [ "$runs" -ge 1 ] 2>/dev/null; then
	pass "and it reports the ${runs} record(s) it has produced"
else
	fail "executionCount = ${runs:-<none>}, want at least 1"
fi
if [ -n "$last" ]; then
	pass "and when it last fired: ${last}"
else
	fail "lastExecutionTime is empty on a strategy that has fired"
fi

# --------------------------------------------------------------------------
# 11. Posture is per namespace
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
wait_for_settled 1 || info "the gateway endpoints did not settle to one; continuing"
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

# That the operator warns about a mixed posture at startup is not asserted here.
#
# It was, and it failed twice for a reason that had nothing to do with the
# warning: a helm upgrade that changes nothing does not restart the pod, so the
# startup line belongs to a process whose posture was different. A log line
# emitted once, by whichever process happens to be running, is the most fragile
# thing this suite could assert.
#
# The property it stood for -- that a mixed posture is reported rather than left
# for a reader to infer -- is asserted below from remedik_namespace_posture,
# which is live state and answers the same question at any moment.

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

if grep -q 'remedik_namespace_posture{namespace="e2e-payments",posture="live"} 1' <<<"$metrics_body"; then
	pass "the override is a metric, so the posture is queryable"
else
	fail "remedik_namespace_posture does not report the override"
fi
if grep -q '^remedik_dry_run 1' <<<"$metrics_body"; then
	pass "and remedik_dry_run still reports the default, which is 1"
else
	fail "remedik_dry_run does not report the default"
fi


# --------------------------------------------------------------------------
# Test 12 — two replicas, one leader
#
# The guards are in memory, so two instances would each enforce a cooldown
# the other cannot see. `kubectl scale --replicas=2` must therefore be
# failover rather than double remediation: one pod holds the lease and
# answers, the other refuses with 503 and records nothing.
# --------------------------------------------------------------------------
step "12. Scaling to two replicas does not double the remediation"

helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set replicaCount=2 \
	--set dryRun=true \
	--set actions.deploymentRestart.enabled=true \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=180s >/dev/null
wait_for_settled 2 || info "the gateway endpoints did not settle to two; continuing"

ready=$(kubectl -n "$NAMESPACE" get deploy remedik -o jsonpath='{.status.readyReplicas}')
if [ "$ready" = "2" ]; then
	pass "both replicas are ready"
else
	fail "readyReplicas=${ready}, want 2"
fi

# Exactly one lease, held by one of them.
holder=$(kubectl -n "$NAMESPACE" get lease remedik.remedik.dev \
	-o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
if [ -n "$holder" ]; then
	pass "one lease, held by ${holder%%_*}"
else
	fail "no lease was taken; leader election is not running"
fi

# Ask each pod directly, bypassing the Service, so both answers are seen.
leaders=0
refusers=0
for pod in $(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=remedik \
	-o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); do
	kubectl -n "$NAMESPACE" port-forward "pod/$pod" "${LEADER_PORT}:8090" >/dev/null 2>&1 &
	pf=$!

	# The tunnel takes a moment, and a curl that cannot connect exits
	# non-zero — which under `set -e` would end the run rather than report
	# anything. Every request here is allowed to fail and say so.
	code=000
	for _ in $(seq 1 25); do
		code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
			-X POST "http://127.0.0.1:${LEADER_PORT}/webhooks/alertmanager" \
			-H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
			-d '{"version":"4","alerts":[]}' 2>/dev/null || true)
		# curl already writes 000 through -w when it cannot connect; adding
		# another 000 of our own made the guard below never match, so the
		# loop broke on its first attempt and the tunnel never had its second.
		[ -n "$code" ] && [ "$code" != "000" ] && break
		sleep 1
	done
	kill "$pf" 2>/dev/null || true
	wait "$pf" 2>/dev/null || true

	case "$code" in
		200) leaders=$((leaders + 1)) ;;
		503) refusers=$((refusers + 1)) ;;
		000|"") fail "pod ${pod} could not be reached through a port-forward" ;;
		*)   fail "pod ${pod} answered ${code}, want 200 or 503" ;;
	esac
done

if [ "$leaders" = "1" ]; then
	pass "exactly one replica accepts alerts"
else
	fail "${leaders} replicas accept alerts, want exactly 1"
fi
if [ "$refusers" = "1" ]; then
	pass "and the other refuses with 503 rather than going quiet"
else
	fail "${refusers} replicas refused with 503, want exactly 1"
fi

# Back to one, so the summary reflects a normal deployment.
helm upgrade remedik charts/remedik \
	--namespace "$NAMESPACE" \
	--set image.repository="${IMAGE%%:*}" \
	--set image.tag="${IMAGE##*:}" \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set replicaCount=1 \
	--set actions.deploymentRestart.enabled=true \
	--wait --timeout 3m >/dev/null

# --------------------------------------------------------------------------
# Test 13 — an upgrade whose CRDs the cluster does not have is refused
#
# `helm upgrade` never upgrades CRDs: Helm applies a chart's crds/ directory
# on first install and never again. So a field added since somebody's install
# is pruned silently by the API server rather than rejected, and a strategy
# that asks for human approval can lose the field that makes it wait.
#
# The chart compares a stamp in each generated CRD with the cluster's. This is
# the only place that comparison can be exercised: Helm's `lookup` returns
# nothing during `helm template` and `--dry-run`, by design, so `make
# helm-lint` cannot see this guard at all.
# --------------------------------------------------------------------------
step "13. An upgrade is refused when the cluster's CRDs are older"

upgrade_with_current_crds() {
	helm upgrade remedik charts/remedik \
		--namespace "$NAMESPACE" \
		--set image.repository="${IMAGE%%:*}" \
		--set image.tag="${IMAGE##*:}" \
		--set image.pullPolicy=IfNotPresent \
		--set gateway.auth.token="$TOKEN" \
		--set replicaCount=1 \
		--set actions.deploymentRestart.enabled=true \
		"$@"
}

# Exactly what an older release looks like: a CRD whose stamp is not this
# chart's. Annotated rather than replaced, so no custom resource is touched.
kubectl annotate crd remediations.remedik.dev \
	remedik.dev/schema-hash=0000000000000000 --overwrite >/dev/null

if refusal=$(upgrade_with_current_crds --wait --timeout 3m 2>&1); then
	fail "the upgrade went ahead with a CRD this chart did not ship"
else
	pass "the upgrade is refused rather than silently pruning the new fields"

	# Both halves of the instruction, because the first version of this
	# assertion pinned only the prefix and stayed green when the command it
	# checked lost the flag that makes it work.
	if grep -q -- "--server-side" <<<"$refusal" &&
		grep -q -- "--force-conflicts" <<<"$refusal"; then
		pass "the refusal carries the command that fixes it, flags included"
	else
		fail "the refusal does not say how to fix it: $refusal"
	fi
fi

# And it is escapable, because a guard nobody can turn off is an outage
# waiting for the case its author did not think of.
if upgrade_with_current_crds --set crdCheck.enabled=false --wait --timeout 3m >/dev/null 2>&1; then
	pass "somebody who manages CRDs themselves can say so and proceed"
else
	fail "crdCheck.enabled=false did not let the upgrade through"
fi

# Restore the real stamp, the way the message tells a user to -- including
# --force-conflicts, which the first version of this left out and which is
# exactly why the instruction needed testing: helm owns these fields.
kubectl apply --server-side --force-conflicts -f charts/remedik/crds/ >/dev/null

if upgrade_with_current_crds --wait --timeout 3m >/dev/null 2>&1; then
	pass "applying the CRDs is all it takes, and the upgrade proceeds"
else
	fail "the upgrade still fails after applying the chart's CRDs"
fi

# --------------------------------------------------------------------------
# Summary
# --------------------------------------------------------------------------
step "Remediation records"
kubectl -n "$NAMESPACE" get remediations -o wide || true

step "Result"
printf '    %d passed, %d failed\n\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
