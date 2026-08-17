#!/usr/bin/env bash
#
# Fills the dev cluster with a cluster's worth of history.
#
# Why this exists: every screenshot and every manual check was made against
# nine records in three namespaces, and a dashboard that reads well at nine
# rows tells you nothing about how it reads at two thousand. The filters, the
# ordering, the paging and the "where is this going badly" page are all
# claims about scale, so they need a cluster at scale to be tested against.
#
# The records are written with their status already terminal, and the
# operator is stopped while that happens. Two reasons, both deliberate:
#
#   * A record created without a status is one the reconciler will pick up
#     and execute. Two thousand of those would be two thousand real actions
#     against targets that do not exist.
#   * A terminal record is a no-op on reconcile, so when the operator comes
#     back it replays them into the guards — which is the realistic thing —
#     and then leaves them alone.
#
# It is idempotent in the sense that it can be run again; records accumulate
# rather than being replaced, so `--reset` is there when that is not wanted.
#
# Usage:
#   hack/dev-seed.sh                     # 150 namespaces, ~1400 records
#   hack/dev-seed.sh --namespaces 40     # smaller
#   hack/dev-seed.sh --reset             # delete existing records first
#
# One limitation, deliberately not hidden: the API server sets
# creationTimestamp itself, so every seeded record is as old as the run. The
# volume, the spread across namespaces and the mix of outcomes are real; the
# ages are not, so the activity panel will show one spike rather than a week.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

CONTEXT="${CONTEXT:-kind-remedik-dev}"
NAMESPACE="${NAMESPACE:-remedik}"
NS_COUNT=150
RESET=0
PARALLEL="${PARALLEL:-16}"

while [ $# -gt 0 ]; do
	case "$1" in
		--namespaces) NS_COUNT="$2"; shift 2 ;;
		--reset)      RESET=1; shift ;;
		-h|--help)    sed -n '2,27p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*)            echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done

k() { kubectl --context "$CONTEXT" "$@"; }

k version --request-timeout=5s >/dev/null 2>&1 || {
	echo "cannot reach $CONTEXT; bring the cluster up with: make dev-up && make dev-deploy" >&2
	exit 1
}

step() { printf '\n==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }

# --------------------------------------------------------------------------
# The shape of a realistic cluster
# --------------------------------------------------------------------------
# Namespace names that look like somebody's cluster rather than ns-001..150,
# because the filters are read by eye and "payments-eu-prod" is a different
# test from "ns-042". The list is cycled with a suffix once it runs out.
BASES=(
	payments checkout catalog search inventory shipping billing accounts
	identity notifications media ingest analytics reporting warehouse
	pricing fulfilment returns loyalty support cms cdn edge gateway
	scheduler workers etl datalake ml-serving feature-store
)
ENVS=(prod staging canary)
REGIONS=(eu us apac)

# Strategies, weighted the way a real install is: a few do most of the work.
STRATEGIES=(
	pod-crashloop pod-crashloop pod-crashloop pod-crashloop
	deployment-oom deployment-oom deployment-oom
	statefulset-stuck statefulset-stuck
	job-failed job-failed
	hpa-maxed
	volume-filling
	bad-deploy
	node-pressure
)
ALERTS=(
	KubePodCrashLooping KubeContainerOOMKilled KubeStatefulSetStuck
	KubeJobFailed KubeHPAMaxedOut KubePersistentVolumeFillingUp
	KubeDeploymentReplicasMismatch KubeNodeMemoryPressure
)
WORKLOADS=(api api2 worker web consumer indexer scheduler gc)

if [ "$RESET" = 1 ]; then
	step "Deleting the records that are there"
	k -n "$NAMESPACE" delete remediations --all --wait=false >/dev/null 2>&1 || true
	info "requested"
fi

# --------------------------------------------------------------------------
# Stop the operator while the records are written
# --------------------------------------------------------------------------
step "Stopping the operator"
WAS_REPLICAS="$(k -n "$NAMESPACE" get deploy remedik -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 1)"
[ -n "$WAS_REPLICAS" ] || WAS_REPLICAS=1

restore() {
	printf '\n==> Starting the operator again\n'
	k -n "$NAMESPACE" scale deploy/remedik --replicas="$WAS_REPLICAS" >/dev/null 2>&1 || true
	k -n "$NAMESPACE" rollout status deploy/remedik --timeout=180s >/dev/null 2>&1 || true
	info "back to ${WAS_REPLICAS} replica(s); it will replay this history into the guards"
}
trap restore EXIT

k -n "$NAMESPACE" scale deploy/remedik --replicas=0 >/dev/null
for _ in $(seq 1 60); do
	running="$(k -n "$NAMESPACE" get pods -l app.kubernetes.io/name=remedik \
		--no-headers 2>/dev/null | grep -c . || true)"
	[ "$running" = "0" ] && break
	sleep 1
done
info "stopped, so nothing executes while the history is written"

# --------------------------------------------------------------------------
# Write the specs
# --------------------------------------------------------------------------
# One document per record, applied in a single call. Two thousand kubectl
# invocations would take longer than the rest of this script put together.
step "Writing the records"

SPECS="$(mktemp)"
STATUSES="$(mktemp)"
trap 'rm -f "$SPECS" "$STATUSES"; restore' EXIT

# A deterministic sequence, so two runs of the same size produce the same
# cluster and a screenshot can be reproduced.
#
# It sets R rather than echoing, and that is the whole point: a generator that
# returns through $( ) runs in a subshell, so the parent's state never
# advances. Written that way, the first version produced one identical record
# two thousand times. $RANDOM has the same problem with an extra trap — bash
# reseeds it per subshell from the pid, so the values do vary and the run is
# not reproducible, which is the failure that looks like it works.
#
# Usage:  rnd 10; x=$R
seed=20260817
R=0
rnd() {
	seed=$(( (seed * 1103515245 + 12345) & 0x7fffffff ))
	R=$(( seed % $1 ))
}

total=0
now_epoch="$(date -u +%s)"

for i in $(seq 0 $((NS_COUNT - 1))); do
	# Distinctness matters more than it looks: the first version derived the
	# region from i and the environment from i/30, and 30 bases x 3 regions
	# repeats every 90 — so asking for 150 namespaces produced 90 and the
	# rest silently merged into them.
	#
	# base cycles fastest; the environment and region come from how many
	# times it has wrapped, which gives 30 x 3 x 3 = 270 distinct names.
	base="${BASES[$((i % ${#BASES[@]}))]}"
	wrap=$(( i / ${#BASES[@]} ))
	env="${ENVS[$(( wrap % ${#ENVS[@]} ))]}"
	region="${REGIONS[$(( (wrap / ${#ENVS[@]}) % ${#REGIONS[@]} ))]}"
	ns="${base}-${region}-${env}"

	# How busy this namespace is. A handful carry most of the traffic, which
	# is what makes the "busiest first" ordering worth having.
	rnd 10; busy=$R
	case "$busy" in
		0)   rnd 30; count=$(( 40 + R )) ;;   # a hot spot
		1|2) rnd 12; count=$(( 12 + R )) ;;
		*)   rnd 8;  count=$(( 1 + R )) ;;
	esac

	for _ in $(seq 1 "$count"); do
		rnd ${#STRATEGIES[@]}; strategy="${STRATEGIES[$R]}"
		rnd ${#ALERTS[@]};      alert="${ALERTS[$R]}"
		rnd ${#WORKLOADS[@]};   workload="${WORKLOADS[$R]}"
		name="${strategy}-$(printf '%05d' "$total")"

		# The status timestamps get a spread even though creationTimestamp
		# cannot: the detail page's duration is read from them.
		rnd 1209600; age=$R                # up to two weeks
		rnd 40; took=$(( 1 + R ))
		created="$(date -u -d "@$(( now_epoch - age ))" +%Y-%m-%dT%H:%M:%SZ)"
		finished="$(date -u -d "@$(( now_epoch - age + took ))" +%Y-%m-%dT%H:%M:%SZ)"

		# Outcome. Staging is where things are still being trialled, so it
		# simulates more; prod is live and mostly succeeds.
		rnd 100; roll=$R
		if [ "$env" = "staging" ]; then
			if   [ "$roll" -lt 70 ]; then outcome=Simulated
			elif [ "$roll" -lt 88 ]; then outcome=Succeeded
			else                          outcome=Failed
			fi
		else
			if   [ "$roll" -lt 74 ]; then outcome=Succeeded
			elif [ "$roll" -lt 90 ]; then outcome=Failed
			else                          outcome=Simulated
			fi
		fi

		# Every outcome here is terminal, deliberately. A Pending record is
		# live work: the operator picks it up and runs it for real against a
		# target that does not exist, which fills the log with failures the
		# seeder invented rather than history it recorded.

		dry=false
		[ "$outcome" = "Simulated" ] && dry=true

		cat >>"$SPECS" <<EOF
---
apiVersion: remedik.dev/v1alpha1
kind: Remediation
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
  labels:
    remedik.dev/strategy: ${strategy}
    remedik.dev/fingerprint: fp-${total}
    remedik.dev/seeded: "true"
  creationTimestamp: "${created}"
spec:
  strategyName: ${strategy}
  target: deployment/${ns}/${workload}
  dryRun: ${dry}
  retries: 1
  steps:
    - action: deployment.restart
  alert:
    fingerprint: fp-${total}
    name: ${alert}
    startsAt: "${created}"
    labels:
      namespace: ${ns}
      deployment: ${workload}
      severity: warning
      cluster: dev-kind
EOF

		# The status, written separately because it is a subresource.
		rnd 3; escalation=$R
		printf '%s\t%s\t%s\t%s\t%s\n' \
			"$name" "$outcome" "$created" "$finished" "$escalation" >>"$STATUSES"

		total=$((total + 1))
	done
done

info "$total records across $NS_COUNT namespaces"
k apply -f "$SPECS" >/dev/null
info "specs applied"

# --------------------------------------------------------------------------
# Write the statuses
# --------------------------------------------------------------------------
step "Setting the outcomes"

# Roughly one failure in three is one nobody was told about — either no
# escalation was configured or the escalation itself failed. That ratio is
# what makes the namespaces page's ordering visible.
status_patch() {
	local name="$1" outcome="$2" created="$3" finished="$4"

	case "$outcome" in
		Pending)
			printf '{"status":{"state":"Pending","attempt":1,"reason":"AwaitingRetry","message":"waiting for the retry backoff"}}'
			return ;;
		Succeeded)
			printf '{"status":{"state":"Succeeded","attempt":1,"startedAt":"%s","completedAt":"%s","steps":[{"index":0,"action":"deployment.restart","phase":"Succeeded","plan":"restart the Deployment","verified":"3/3 replicas updated, available and ready","startedAt":"%s","completedAt":"%s"}]}}' \
				"$created" "$finished" "$created" "$finished"
			return ;;
		Simulated)
			printf '{"status":{"state":"Simulated","attempt":1,"startedAt":"%s","completedAt":"%s","steps":[{"index":0,"action":"deployment.restart","phase":"Simulated","plan":"would restart the Deployment, rolling 3 replicas","kubectl":"kubectl rollout restart deployment/api","startedAt":"%s","completedAt":"%s"}]}}' \
				"$created" "$finished" "$created" "$finished"
			return ;;
	esac

	# Failed, with one of three escalation outcomes.
	local esc
	case $(( ${5:-0} % 3 )) in
		0) esc='' ;;                                       # nobody was told
		1) esc=',"escalation":{"phase":"Failed","message":"webhook.call: 502 from the on-call endpoint","completedAt":"'"$finished"'"}' ;;
		*) esc=',"escalation":{"phase":"Succeeded","steps":[{"index":0,"action":"webhook.call","phase":"Succeeded","plan":"POST to the on-call webhook"}],"completedAt":"'"$finished"'"}' ;;
	esac
	printf '{"status":{"state":"Failed","attempt":2,"reason":"StepFailed","message":"execute deployment.restart: deployments.apps \\"%s\\" not found","startedAt":"%s","completedAt":"%s","steps":[{"index":0,"action":"deployment.restart","phase":"Failed","message":"the Deployment does not exist"}]%s}}' \
		"api" "$created" "$finished" "$esc"
}

export -f status_patch k
export CONTEXT NAMESPACE

patch_one() {
	local line="$1"
	IFS=$'\t' read -r name outcome created finished escalation <<<"$line"
	kubectl --context "$CONTEXT" -n "$NAMESPACE" patch remediation "$name" \
		--subresource=status --type=merge \
		-p "$(status_patch "$name" "$outcome" "$created" "$finished" "$escalation")" >/dev/null 2>&1 \
		|| echo "  could not set the status of $name" >&2
}
export -f patch_one

# xargs rather than a loop: a thousand sequential API calls is minutes.
# shellcheck disable=SC2016
< "$STATUSES" xargs -d '\n' -P "$PARALLEL" -I{} bash -c 'patch_one "$@"' _ {}
info "outcomes set"

# --------------------------------------------------------------------------
# What was made
# --------------------------------------------------------------------------
step "Result"
k -n "$NAMESPACE" get remediations --no-headers 2>/dev/null \
	| awk '{print $4}' | sort | uniq -c | sort -rn | sed 's/^/    /'
info "$(k -n "$NAMESPACE" get remediations --no-headers 2>/dev/null | grep -c . || true) records in total"
