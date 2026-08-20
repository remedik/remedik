#!/usr/bin/env bash
#
# A chart value must not become a second argument.
#
# It could. Every flag the deployment builds from values.yaml was rendered
# unquoted, so a value carrying a newline ended its own YAML list item and
# began another one:
#
#     - --dashboard-link=Evil
#     - --actions=job.run=https://y.test
#
# That second line is not a broken link. It is a flag on the operator's command
# line, chosen by whoever wrote the values file — and `--actions` decides what
# remedik is allowed to run at all. The person writing values is trusted with
# the cluster; the rendering is still not trusted, for the same reason the
# scheme of a link is checked rather than assumed.
#
# Every such argument is quoted now. This is what keeps it that way.
#
# Usage:  hack/no-arg-injection.sh   (run by `make helm-lint`)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

VALUES="$(mktemp)"
trap 'rm -f "$VALUES"' EXIT

# Every value that reaches an argument, each carrying a newline and a flag the
# operator would never have been given.
cat >"$VALUES" <<'YAML'
gateway:
  auth:
    disabled: true
clusterName: "evil\n            - --actions=job.run"
logLevel: "info\n            - --dry-run=false"
namespacePosture:
  "payments\n            - --concurrency=999": live
dashboard:
  enabled: true
  auth:
    disabled: true
  links:
    - name: "Evil\n            - --actions=job.run"
      url: "https://x.test"
YAML

rendered="$(helm template remedik charts/remedik -f "$VALUES")"

# The injected flags, as they would appear if any value escaped its argument.
smuggled=0
while read -r flag; do
	[ -n "$flag" ] || continue
	if grep -qE "^ +- ${flag}\$" <<<"$rendered"; then
		echo "FAIL: a chart value injected '${flag}' into the operator's arguments" >&2
		smuggled=1
	fi
done <<'FLAGS'
--actions=job.run
--dry-run=false
--concurrency=999
FLAGS

if [ "$smuggled" -ne 0 ]; then
	cat >&2 <<'MSG'

Every argument built from a value must be quoted:

    - {{ printf "--flag=%s" .Values.thing | quote }}

Unquoted, a value with a newline ends its list item and starts another, which
is an extra flag rather than a malformed one.
MSG
	exit 1
fi

echo "chart values cannot inject arguments"
