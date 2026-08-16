#!/usr/bin/env bash
#
# Checks that the spec-first workflow was actually followed.
#
# CONTRIBUTING.md says a change is proposed, specified and approved before it
# is written, and that `openspec/specs/` is the current contract. Both are
# claims about files, so they are checked as claims about files — here,
# rather than by trusting that everyone remembered.
#
# Deliberately dependency-free: the openspec CLI is a convenience, and a gate
# that only runs where somebody has installed a tool is not a gate.
#
# Usage:  hack/openspec-check.sh   (run by `make verify`)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

FAILED=0

fail() {
	echo "FAIL: $*" >&2
	FAILED=1
}

# --------------------------------------------------------------------------
# Every change — open or archived — carries its reasoning
# --------------------------------------------------------------------------
for change in openspec/changes/*/ openspec/changes/archive/*/; do
	[ -d "$change" ] || continue
	[ "$(basename "$change")" = "archive" ] && continue

	for required in proposal.md design.md tasks.md; do
		[ -f "${change}${required}" ] || fail "${change} has no ${required}"
	done

	# A proposal that does not say why is a description, not a proposal.
	if [ -f "${change}proposal.md" ] && ! grep -q '^## Why' "${change}proposal.md"; then
		fail "${change}proposal.md has no '## Why' section"
	fi

	# An archived change should have nothing left undone; an open one may.
	case "$change" in
	openspec/changes/archive/*)
		if grep -q '^- \[ \]' "${change}tasks.md" 2>/dev/null; then
			fail "${change} is archived with unfinished tasks"
		fi
		;;
	esac
done

# --------------------------------------------------------------------------
# Every capability spec is a contract, not prose
# --------------------------------------------------------------------------
for spec in openspec/specs/*/spec.md; do
	[ -f "$spec" ] || continue

	grep -q '^## Purpose' "$spec" || fail "$spec has no '## Purpose'"
	grep -q '^## Requirements' "$spec" || fail "$spec has no '## Requirements'"

	# A requirement with no scenario cannot be checked by anyone but its
	# author, which is the state this workflow exists to avoid.
	requirements=$(grep -c '^### Requirement:' "$spec" || true)
	scenarios=$(grep -c '^#### Scenario:' "$spec" || true)

	if [ "$requirements" -eq 0 ]; then
		fail "$spec declares no requirements"
	elif [ "$scenarios" -lt "$requirements" ]; then
		fail "$spec has $requirements requirements but only $scenarios scenarios"
	fi

	# SHALL is the word that separates a contract from an intention.
	grep -q 'SHALL' "$spec" || fail "$spec states no SHALL requirements"
done

# --------------------------------------------------------------------------
# An archived change's capabilities reached the specs
# --------------------------------------------------------------------------
for delta in openspec/changes/archive/*/specs/*/; do
	[ -d "$delta" ] || continue
	capability="$(basename "$delta")"
	if [ ! -f "openspec/specs/${capability}/spec.md" ]; then
		fail "archived change delivered '${capability}' but openspec/specs/${capability}/ does not exist"
	fi
done

if [ "$FAILED" -ne 0 ]; then
	cat >&2 <<'EOF'

The spec-first workflow was not followed for something above.

CONTRIBUTING.md is the long version. The short one: a change is proposed with
its reasoning, specified with requirements that have scenarios, and archived
only once its tasks are done and its capabilities are in openspec/specs/.
EOF
	exit 1
fi

changes=$(find openspec/changes -maxdepth 3 -name proposal.md | wc -l | tr -d ' ')
specs=$(find openspec/specs -name spec.md | wc -l | tr -d ' ')
echo "spec-first workflow intact: ${changes} changes, ${specs} capabilities"
