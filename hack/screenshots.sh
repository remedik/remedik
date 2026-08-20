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

# TARGET points this at a dashboard that is already reachable and needs none of
# the cluster choreography below -- `make dev-dashboard`, or somebody else's
# port-forward. The pictures are the same pictures; what changes is that taking
# them stops needing fifteen minutes of kind.
TARGET="${TARGET:-}"
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
	[ -n "$TARGET" ] && return 0
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

if [ -n "$TARGET" ]; then
	PORT="${TARGET##*:}"
	echo "==> Shooting ${TARGET}, which is already serving"
else
echo "==> Serving the dashboard without authentication, briefly"
helm upgrade remedik charts/remedik --namespace "$NAMESPACE" --reuse-values \
	--set dashboard.auth.disabled=true --set dashboard.auth.token="" \
	--wait --timeout 3m >/dev/null
kubectl -n "$NAMESPACE" rollout status deploy/remedik --timeout=120s >/dev/null

kubectl -n "$NAMESPACE" port-forward --address 0.0.0.0 "svc/remedik-dashboard" "${PORT}:${PORT}" >/dev/null 2>&1 &
FORWARD=$!
fi

# Waiting until *Chrome* can reach it, not until curl can.
#
# These are two different questions on WSL, and the difference put a screenshot
# of "This page isn't working — ERR_EMPTY_RESPONSE" into the README as the first
# image anybody sees. curl runs inside WSL and reaches the port-forward the
# moment it binds; Chrome is the Windows binary and arrives through Windows'
# localhost forwarding, which can still refuse the connection a second or two
# later. So the readiness check is made by the same browser that takes the
# pictures, through the same path, and it checks for content rather than for a
# connection.
echo "==> Waiting for the browser to be able to reach it"
ready=""
for _ in $(seq 1 40); do
	if "$CHROME" --headless=new --disable-gpu --dump-dom \
		--user-data-dir="${STAGE_WIN}\\profile-probe" \
		"http://localhost:${PORT}/" 2>/dev/null | grep -q "remedik"; then
		ready=yes
		break
	fi
	sleep 2
done
[ -n "$ready" ] || {
	echo "the dashboard never answered the browser on port ${PORT}" >&2
	exit 1
}

# Which records to photograph.
#
# Against a dashboard that is simply reachable, the pages are their own index:
# the list can already be asked for "failed, and nobody was told", which is
# exactly the record worth a picture. So choosing one needs no cluster access
# at all.
pick_from_page() {
	curl -fsS "$1" 2>/dev/null |
		grep -oE 'href="/remediations/[a-z0-9][a-z0-9.-]*"' |
		head -1 | sed -E 's|.*/remediations/||; s|"$||' || true
}

if [ -n "$TARGET" ]; then
	DETAIL="$(pick_from_page "${TARGET}/remediations?state=Failed&escalation=failed")"
	[ -n "$DETAIL" ] || DETAIL="$(pick_from_page "${TARGET}/remediations?state=Failed")"
	WAITING="$(pick_from_page "${TARGET}/approvals")"
	[ -n "$DETAIL" ] || {
		echo "no remediation to photograph at ${TARGET}" >&2
		exit 1
	}
else

# A failed remediation makes the detail page worth looking at: it is the one
# that carries the plan, the failure and the escalation.
#
# Preferring one whose times make sense. `make dev-seed` writes status
# timestamps in the past, but the API server stamps creation as now, so a seeded
# record reads "started eleven days before it was created" — true of the fixture
# and nonsense to a reader, in a picture whose whole job is to be believed.
DETAIL="$(kubectl -n "$NAMESPACE" get remediations -o json | python3 -c '
import json, sys

fallback = ""
best = ("", "")
for item in json.load(sys.stdin).get("items", []):
    status = item.get("status") or {}
    if status.get("state") != "Failed":
        continue
    name = item["metadata"]["name"]
    created = item["metadata"].get("creationTimestamp", "")
    fallback = fallback or name
    started = status.get("startedAt", "")
    # Coherent, and with an escalation to show: the two things that make this
    # page worth a screenshot. Newest wins, so the picture is of recent state
    # rather than of whatever sorts first.
    if started and created and started >= created and status.get("escalation"):
        if created > best[0]:
            best = (created, name)
print(best[1] or fallback)
')"
[ -n "$DETAIL" ] || DETAIL="$(kubectl -n "$NAMESPACE" get remediations -o jsonpath='{.items[0].metadata.name}')"

# And a waiting one, if the cluster has any: it is the only page that asks the
# reader for something, and a screenshot of it says more about what this product
# is than any amount of prose about human approval.
WAITING="$(kubectl -n "$NAMESPACE" get remediations \
	-o jsonpath='{range .items[?(@.status.state=="AwaitingApproval")]}{.metadata.name}{"\n"}{end}' | head -1)"
fi

shoot() {
	local name="$1" path="$2" size="$3" floor="${4:-40000}"
	"$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-color-profile=srgb \
		--user-data-dir="${STAGE_WIN}\\profile-${name}" \
		--screenshot="${STAGE_WIN}\\${name}.png" \
		--window-size="$size" "http://localhost:${PORT}${path}" >/dev/null 2>&1
	cp "${STAGE}/${name}.png" "${OUT}/${name}.png"
	chmod 644 "${OUT}/${name}.png"

	# A backstop for the failure this script has actually had: Chrome writes a
	# perfectly valid PNG of its own error page, and it is small. A real page of
	# this dashboard has never been under 80KB and an error page has never been
	# over 25KB, so the floor separates them with room to spare. The size was
	# printed before and read by a human who did not notice; a check does not
	# have that problem.
	local bytes
	bytes="$(wc -c < "${OUT}/${name}.png" | tr -d ' ')"
	if [ "$bytes" -lt "$floor" ]; then
		printf '    %-14s %s bytes — that is an error page, not a screenshot\n' \
			"$name" "$bytes" >&2
		SHOT_FAILED=1
		return
	fi

	# And the other kind of wrong picture, which the size cannot see: a
	# perfectly rendered page of this dashboard saying "No such remediation".
	# It happened -- a record name read from one cluster, photographed against
	# another -- and it weighs the same as a real page, because it carries the
	# same shell.
	if curl -fsS "http://localhost:${PORT}${path}" 2>/dev/null |
		grep -qE '<h1>(No such remediation|Page not found|Cannot read)'; then
		printf '    %-14s photographed an error page: %s\n' "$name" "$path" >&2
		SHOT_FAILED=1
		return
	fi

	printf '    %-14s %s\n' "$name" "$(du -h "${OUT}/${name}.png" | cut -f1)"
}

SHOT_FAILED=0

echo "==> Capturing"
shoot overview     "/"                          "1400,1180"
shoot remediations "/remediations"              "1400,900"
shoot detail       "/remediations/${DETAIL}"    "1400,1250"
shoot namespaces   "/namespaces"                "1400,900"
shoot strategies   "/strategies"                "1400,900"
shoot approvals    "/approvals"                 "1400,1000"
# No phone-width shot here on purpose. Headless Chrome will not open a window
# narrower than about 740 CSS pixels, so --window-size=412 produces a picture of
# the desktop layout with a phone's frame around it -- which is worse than no
# picture, because it is evidence of something that is not true. The card layout
# is checked where it can be: hack/browser-check.mjs drives a real viewport of
# 390 through the DevTools protocol and asserts the page does not scroll
# sideways and that every cell prints its column name.
if [ -n "$WAITING" ]; then
	shoot approval "/remediations/${WAITING}" "1400,1100"
else
	printf '    %-14s %s\n' "approval" "skipped: nothing is awaiting approval"
fi

if [ "$SHOT_FAILED" != "0" ]; then
	echo "" >&2
	echo "At least one screenshot is a browser error page. Nothing was published;" >&2
	echo "the files in docs/screenshots/ are now wrong and should not be committed." >&2
	exit 1
fi

echo "==> Done — docs/screenshots/"
