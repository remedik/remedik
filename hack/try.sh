#!/usr/bin/env bash
#
# Runs the whole loop on your laptop, with nothing simulated.
#
# This exists because "install an operator that has write access to your
# cluster" is a big first step, and reading a README is not evidence. So: a
# throwaway cluster, a real Prometheus, a workload that really crash-loops, a
# real alert routed through a real Alertmanager, and remedik recording what it
# would do about it. Then one flag lets it act, and you watch that too.
#
# Everything here is the same chart and the same values a real install uses.
# Nothing is stubbed, and no alert is posted by hand.
#
# Needs: docker, kind, kubectl, helm. Takes about ten minutes, most of it
# waiting for Prometheus to decide the workload is really broken.
#
#   ./hack/try.sh          set it up and walk through it
#   ./hack/try.sh --clean  delete the cluster
set -euo pipefail

cd "$(dirname "$0")/.."

CLUSTER="${CLUSTER:-remedik-try}"
NAMESPACE=remedik
DEMO_NS=payments
TOKEN=dev-token   # the token hack/dev/monitoring-values.yaml already carries

if [ -t 1 ]; then
	B=$'\033[1m'; DIM=$'\033[2m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; OFF=$'\033[0m'
else
	B=''; DIM=''; GREEN=''; YELLOW=''; OFF=''
fi

step() { printf '\n%s==> %s%s\n' "$B" "$*" "$OFF"; }
info() { printf '    %s\n' "$*"; }
dim()  { printf '    %s%s%s\n' "$DIM" "$*" "$OFF"; }
ok()   { printf '    %s✓%s %s\n' "$GREEN" "$OFF" "$*"; }

if [ "${1:-}" = "--clean" ]; then
	step "Deleting the cluster '$CLUSTER'"
	kind delete cluster --name "$CLUSTER"
	exit 0
fi

for tool in docker kind kubectl helm; do
	command -v "$tool" >/dev/null || {
		echo "$tool not found. This needs docker, kind, kubectl and helm." >&2
		echo "  kind:    https://kind.sigs.k8s.io/docs/user/quick-start/#installation" >&2
		echo "  helm:    https://helm.sh/docs/intro/install/" >&2
		exit 1
	}
done

# Its own kubeconfig, so this cannot touch the cluster you actually work with
# and cannot change your current context.
KUBECONFIG_FILE="$(mktemp -t remedik-try-kubeconfig.XXXXXX)"
export KUBECONFIG="$KUBECONFIG_FILE"

step "1/6  A throwaway cluster"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
	kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG_FILE"
	info "reusing the existing '$CLUSTER'"
else
	kind create cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG_FILE" >/dev/null
	ok "created '$CLUSTER' — your own kubectl context was not touched"
fi

step "2/6  Prometheus, Alertmanager and Grafana"
dim "kube-prometheus-stack, with Alertmanager already routing to remedik."
dim "This is the slow part: a few minutes."
KPS_VERSION="$(sed -n 's/^KPS_CHART_VERSION *?*= *//p' Makefile | head -1)"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
helm repo update prometheus-community >/dev/null
helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
	--namespace monitoring --create-namespace \
	${KPS_VERSION:+--version "$KPS_VERSION"} \
	-f hack/dev/monitoring-values.yaml \
	--wait --timeout 12m >/dev/null
ok "monitoring is up, and Alertmanager has a receiver pointing at remedik"

step "3/6  remedik, in dry-run — which is the install default"
docker build -q -t remedik:try . >/dev/null
kind load docker-image remedik:try --name "$CLUSTER" >/dev/null
kubectl apply --server-side -f charts/remedik/crds/ >/dev/null
helm upgrade --install remedik charts/remedik \
	--namespace "$NAMESPACE" --create-namespace \
	--set image.repository=remedik --set image.tag=try \
	--set image.pullPolicy=IfNotPresent \
	--set gateway.auth.token="$TOKEN" \
	--set clusterName=laptop \
	--set dashboard.enabled=true --set dashboard.auth.token="$TOKEN" \
	--set actions.workloadRestart.enabled=true \
	--set workloadAlerts.enabled=true \
	--set workloadAlerts.crashLoopFor=1m \
	--set workloadAlerts.crashLoopWindow=5m \
	--set workloadAlerts.crashLoopThreshold=1 \
	--set serviceMonitor.enabled=true \
	--set serviceMonitor.additionalLabels.release=monitoring \
	--set prometheusRule.enabled=true \
	--set prometheusRule.additionalLabels.release=monitoring \
	--set grafanaDashboard.enabled=true \
	--set grafanaDashboard.namespace=monitoring \
	--wait --timeout 5m >/dev/null
ok "installed. It can restart a Deployment, and nothing else."
dim "The crash-loop rule is tightened for the demo -- one restart inside five"
dim "minutes, holding for one. The defaults are wider on purpose: a workload that"
dim "recovers on its own should not be remediated at all."

step "4/6  A workload that really is broken, and a strategy for it"
kubectl create namespace "$DEMO_NS" >/dev/null 2>&1 || true
kubectl apply -n "$DEMO_NS" -f - >/dev/null <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  labels: {app: api}
spec:
  replicas: 2
  selector: {matchLabels: {app: api}}
  template:
    metadata:
      labels: {app: api}
    spec:
      containers:
        # Runs for a while, then exits non-zero. A pod that keeps dying is
        # what CrashLoopBackOff is, and this is the honest way to have one.
        - name: app
          image: busybox:1.36
          command: ["sh", "-c", "echo starting; sleep 25; echo 'cannot reach the database'; exit 1"]
          resources:
            requests: {cpu: 10m, memory: 16Mi}
YAML
kubectl apply -f - >/dev/null <<'YAML'
apiVersion: remedik.dev/v1alpha1
kind: RemediationStrategy
metadata:
  name: restart-crashlooping-workload
spec:
  enabled: true
  trigger:
    match:
      alertname: RemedikWorkloadCrashLooping
  guards:
    # Do not restart the same workload again too soon, and never more than four
    # times an hour, whatever the alert does. Five minutes rather than the
    # fifteen you would use in production, so that this demo can show the guard
    # refusing *and* the remediation that follows it.
    cooldown: 5m
    maxPerHour: 4
  steps:
    - action: deployment.restart
YAML
ok "payments/api is crash-looping, and one strategy says what to do about it"

step "5/6  Waiting for Prometheus to raise the alert"
dim "kube-state-metrics has to notice, Prometheus has to evaluate the rule for a"
dim "minute, and Alertmanager groups before it delivers. Two to four minutes."
deadline=$((SECONDS + 420))
while [ "$SECONDS" -lt "$deadline" ]; do
	count="$(kubectl -n "$NAMESPACE" get remediations --no-headers 2>/dev/null | wc -l | tr -d ' ')"
	if [ "$count" != "0" ]; then break; fi
	printf '    %s…%s\r' "$DIM" "$OFF"
	sleep 10
done

if [ "${count:-0}" = "0" ]; then
	printf '\n'
	info "${YELLOW}No remediation yet.${OFF} Nothing is broken about that — it can just be slow."
	info "Watch it arrive with:"
	info "  export KUBECONFIG=$KUBECONFIG_FILE"
	info "  kubectl -n $NAMESPACE get remediations -w"
	info "Or find out where it stopped: docs/troubleshooting.md"
	exit 0
fi

printf '\n'
kubectl -n "$NAMESPACE" get remediations -o wide
ok "an alert became a decision, and the decision is a Kubernetes object"

step "6/6  What it would have done"
name="$(kubectl -n "$NAMESPACE" get remediations -o jsonpath='{.items[0].metadata.name}')"
kubectl -n "$NAMESPACE" get remediation "$name" \
	-o jsonpath='{range .status.steps[*]}    {.action}: {.plan}{"\n"}{end}' 2>/dev/null || true
echo
info "${B}Nothing in the cluster was changed.${OFF} That is the default, and the"
info "point: run it like this for a week and read what it would have done."

cat <<EOF

$B  Where to look$OFF
    export KUBECONFIG=$KUBECONFIG_FILE

    kubectl -n $NAMESPACE get remediations
    kubectl -n $NAMESPACE describe remediation $name

    The dashboard, which cannot write anything:
      kubectl -n $NAMESPACE port-forward svc/remedik-dashboard 8082:8082
      http://127.0.0.1:8082  — leave the username empty, password: $TOKEN

$B  When you want it to act$OFF
    helm upgrade remedik charts/remedik -n $NAMESPACE --reuse-values \\
      --set namespacePosture.$DEMO_NS=live

    Per namespace, so going live is not all-or-nothing. The next alert for
    payments restarts the Deployment for real; every other namespace still
    only reports.

    If nothing happens for a few minutes, that is the cooldown guard refusing
    to touch a workload it just decided about — which is the guard working.
    It says so:

      kubectl -n $NAMESPACE logs deploy/remedik | grep "guard rejected"
      kubectl get events --field-selector reason=GuardRejected

$B  How to stop it, which is worth trying once$OFF
    kubectl -n $NAMESPACE patch configmap remedik-pause --type merge \\
      -p '{"data":{"paused":"true","reason":"just checking"}}'

    Every replica is dry-run within seconds, with no restart. Records keep
    appearing so you can see what was suppressed.

$B  When you are done$OFF
    ./hack/try.sh --clean

EOF
