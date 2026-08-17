#!/usr/bin/env bash
#
# Regenerates docs/screenshots/ from the dev cluster.
#
# A screenshot that no longer matches the product is worse than none, and it
# is the first thing anybody looks at — so this is a script rather than a
# thing somebody did once and cannot repeat.
#
# It also found four defects that no other test could: every bar chart in the
# dashboard rendered at full width for months, because the page's
# Content-Security-Policy drops inline styles and the markup was correct. The
# browser was the only thing that knew.
#
# Needs: a running dev cluster (make dev-cluster && make dev-deploy) and
# Chrome. On WSL it uses the Windows installation, which reaches the
# port-forward through localhost forwarding.
#
# Usage:  hack/screenshots.sh
set -euo pipefail

NAMESPACE="${NAMESPACE:-remedik}"
PORT="${PORT:-8082}"
OUT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/docs/screenshots"

CHROME="${CHROME:-}"
if [ -z "$CHROME" ]; then
	for candidate in \
		"/mnt/c/Program Files/Google/Chrome/Application/chrome.exe" \
		"$(command -v google-chrome || true)" \
		"$(command -v chromium || true)"; do
		[ -n "$candidate" ] && [ -x "$candidate" ] && { CHROME="$candidate"; break; }
	done
fi
[ -n "$CHROME" ] || { echo "no Chrome found; set CHROME=/path/to/chrome" >&2; exit 1; }

# Chrome writes to a path it can see. On WSL that is a Windows path, so the
# shots land in the user's Downloads and are copied back.
if [[ "$CHROME" == /mnt/c/* ]]; then
	STAGE_WIN="C:\\Users\\${WIN_USER:-$USER}\\Downloads\\remedik-shots"
	STAGE="/mnt/c/Users/${WIN_USER:-$USER}/Downloads/remedik-shots"
else
	STAGE_WIN="$(mktemp -d)"
	STAGE="$STAGE_WIN"
fi
mkdir -p "$STAGE" "$OUT"

FORWARD=""
cleanup() {
	[ -n "$FORWARD" ] && { kill "$FORWARD" 2>/dev/null || true; wait "$FORWARD" 2>/dev/null || true; }
	# Put authentication back. The dashboard is served without it only for
	# the length of this script, because headless Chrome cannot be given a
	# bearer header.
	helm upgrade remedik charts/remedik --namespace "$NAMESPACE" --reuse-values \
		--set dashboard.auth.disabled=false --set dashboard.auth.token=dev-token \
		--wait --timeout 3m >/dev/null 2>&1 || true
	echo "    dashboard authentication restored"
}
trap cleanup EXIT

echo "==> Serving the dashboard without authentication, briefly"
helm upgrade remedik charts/remedik --namespace "$NAMESPACE" --reuse-values \
	--set dashboard.auth.disabled=true --set dashboard.auth.token="" \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null

kubectl -n "$NAMESPACE" port-forward --address 0.0.0.0 "svc/remedik-dashboard" "${PORT}:${PORT}" >/dev/null 2>&1 &
FORWARD=$!
for _ in $(seq 1 30); do
	curl -sf -o /dev/null "http://127.0.0.1:${PORT}/" && break
	sleep 1
done

# A failed remediation makes the detail page worth looking at: it is the one
# that carries the plan, the failure and the escalation.
DETAIL="$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{range .items[?(@.status.state=="Failed")]}{.metadata.name}{"\n"}{end}' | head -1)"
[ -n "$DETAIL" ] || DETAIL="$(kubectl -n "$NAMESPACE" get remediations -o jsonpath='{.items[0].metadata.name}')"

# And a waiting one, if the cluster has any: it is the only page that asks the
# reader for something, and a screenshot of it says more about what this product
# is than any amount of prose about human approval.
WAITING="$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{range .items[?(@.status.state=="AwaitingApproval")]}{.metadata.name}{"\n"}{end}' | head -1)"

shoot() {
	local name="$1" path="$2" size="$3"
	"$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-color-profile=srgb \
		--user-data-dir="${STAGE_WIN}\\profile-${name}" \
		--screenshot="${STAGE_WIN}\\${name}.png" \
		--window-size="$size" "http://localhost:${PORT}${path}" >/dev/null 2>&1
	cp "${STAGE}/${name}.png" "${OUT}/${name}.png"
	chmod 644 "${OUT}/${name}.png"
	printf '    %-14s %s\n' "$name" "$(du -h "${OUT}/${name}.png" | cut -f1)"
}

echo "==> Capturing"
shoot overview     "/"                          "1400,1180"
shoot remediations "/remediations"              "1400,900"
shoot detail       "/remediations/${DETAIL}"    "1400,1250"
shoot namespaces   "/namespaces"                "1400,900"
shoot strategies   "/strategies"                "1400,900"
if [ -n "$WAITING" ]; then
	shoot approval "/remediations/${WAITING}" "1400,1100"
else
	printf '    %-14s %s\n' "approval" "skipped: nothing is awaiting approval"
fi

echo "==> Done — docs/screenshots/"
