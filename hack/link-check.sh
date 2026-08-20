#!/usr/bin/env bash
#
# Every link in this repository that points at this repository resolves.
#
# It exists because removing one page broke a link on the landing page, which
# is the first thing anybody sees, and nothing said so. Markdown links between
# documents, and the blob links the website uses to reach files here, are the
# two kinds that can be checked without a network — so they are the two kinds
# that are checked. External URLs are not: a gate that depends on somebody
# else's uptime is a gate people learn to skip.
#
# Usage:  hack/link-check.sh   (run by `make verify`)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

python3 - <<'PY'
import pathlib
import re
import subprocess
import sys

markdown = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")
blob = re.compile(r"https://github\.com/remedik/remedik/blob/main/([^\"'\s>)]+)")

files = subprocess.run(
    ["git", "ls-files", "*.md", "website/*.html"],
    capture_output=True, text=True, check=True,
).stdout.split()

broken, checked = [], 0

for name in files:
    path = pathlib.Path(name)
    text = path.read_text(errors="replace")

    targets = [(t, "md") for t in markdown.findall(text)] if path.suffix == ".md" else []
    targets += [(t, "blob") for t in blob.findall(text)]

    for target, kind in targets:
        if kind == "md":
            if target.startswith(("http://", "https://", "mailto:", "#")):
                continue
            target = target.split("#")[0]
            if not target:
                continue
            resolved = path.parent / target
        else:
            resolved = pathlib.Path(target.split("#")[0])

        checked += 1
        if not resolved.exists():
            broken.append(f"{name} -> {target}")

for entry in broken:
    print(f"FAIL: broken link: {entry}", file=sys.stderr)

if broken:
    print(f"\n{len(broken)} of {checked} internal links point at a file that "
          "is not there.", file=sys.stderr)
    sys.exit(1)

print(f"{checked} internal links resolve")
PY
