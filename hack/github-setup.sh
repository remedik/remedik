#!/usr/bin/env bash
#
# Applies this repository's GitHub configuration.
#
# Settings clicked into a web form are configuration nobody reviews and
# nobody can diff. Everything here is idempotent, so running it twice is
# safe and running it after somebody changed something by hand puts it back.
#
# Several features are gated by GitHub on visibility and plan. Rather than
# failing on those, this reports them, because "not available yet" and
# "misconfigured" need different responses and the difference is invisible
# in a settings page.
#
# Usage:  hack/github-setup.sh [owner/repo]
set -uo pipefail

REPO="${1:-remedik/remedik}"

if [ -t 1 ]; then
	BOLD=$'\033[1m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; DIM=$'\033[2m'; RESET=$'\033[0m'
else
	BOLD=''; GREEN=''; YELLOW=''; RED=''; DIM=''; RESET=''
fi

step() { printf '\n%s==> %s%s\n' "$BOLD" "$*" "$RESET"; }
ok()   { printf '    %s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
skip() { printf '    %s–%s %s\n' "$YELLOW" "$RESET" "$*"; }
bad()  { printf '    %s✗%s %s\n' "$RED" "$RESET" "$*"; FAILED=1; }
note() { printf '      %s%s%s\n' "$DIM" "$*" "$RESET"; }

FAILED=0
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

command -v gh >/dev/null 2>&1 || {
	echo "gh is required: https://cli.github.com" >&2
	exit 1
}
gh auth status >/dev/null 2>&1 || {
	echo "not logged in: gh auth login" >&2
	exit 1
}

ORG="${REPO%%/*}"
VISIBILITY="$(gh api "repos/${REPO}" --jq '.visibility' 2>/dev/null || echo unknown)"
step "Configuring ${REPO} (${VISIBILITY})"

# --------------------------------------------------------------------------
# The organisation
#
# These are defaults for every repository created here from now on, which is
# the only moment they are free to get right.
# --------------------------------------------------------------------------
step "Organisation defaults"
if gh api -X PATCH "orgs/${ORG}" \
	-F dependabot_alerts_enabled_for_new_repositories=true \
	-F dependabot_security_updates_enabled_for_new_repositories=true \
	-F dependency_graph_enabled_for_new_repositories=true \
	-F secret_scanning_enabled_for_new_repositories=true \
	-F secret_scanning_push_protection_enabled_for_new_repositories=true \
	-F web_commit_signoff_required=true >/dev/null 2>&1; then
	ok "new repositories get alerts, security updates and scanning by default"
else
	bad "could not set the organisation defaults"
fi

# Two-factor is refused while any member is without it, because turning it
# on would lock them out. That is the right behaviour and a useful check.
gh api -X PATCH "orgs/${ORG}" -F two_factor_requirement_enabled=true >/dev/null 2>&1
if [ "$(gh api "orgs/${ORG}" --jq '.two_factor_requirement_enabled')" = "true" ]; then
	ok "two-factor authentication required"
else
	without="$(gh api "orgs/${ORG}/members?filter=2fa_disabled" --jq 'map(.login)|join(", ")' 2>/dev/null)"
	bad "two-factor authentication is NOT required"
	if [ -n "$without" ]; then
		note "these members have no 2FA, and enabling it would lock them out: ${without}"
		note "each of them: https://github.com/settings/security — then run this again"
	fi
fi

# --------------------------------------------------------------------------
# Merge behaviour
#
# Squash only, so one change is one commit whose message is the pull
# request's body. This repository's history is the argument for its code;
# merge commits and rebase chains bury it.
# --------------------------------------------------------------------------
step "Merge behaviour"
if gh api -X PATCH "repos/${REPO}" \
	-F allow_squash_merge=true \
	-F allow_merge_commit=false \
	-F allow_rebase_merge=false \
	-F delete_branch_on_merge=true \
	-F allow_update_branch=true \
	-F squash_merge_commit_title=PR_TITLE \
	-F squash_merge_commit_message=PR_BODY \
	-F has_wiki=false \
	-F has_projects=false \
	-F has_issues=true >/dev/null 2>&1; then
	ok "squash-only, branches deleted on merge, wiki and projects off"
else
	bad "could not set the merge behaviour"
fi

# Auto-merge lets a pull request land the moment its checks pass, which is
# what makes Dependabot's updates cost nothing to keep up with.
if gh api -X PATCH "repos/${REPO}" -F allow_auto_merge=true >/dev/null 2>&1 &&
	[ "$(gh api "repos/${REPO}" --jq '.allow_auto_merge')" = "true" ]; then
	ok "auto-merge available"
else
	skip "auto-merge is not available"
	note "private repositories need GitHub Team; it turns on by itself when this goes public"
fi

# --------------------------------------------------------------------------
# Dependency security
#
# These two are available everywhere, and they are the ones that matter most
# for a project whose whole dependency tree runs with write access to
# somebody's cluster.
# --------------------------------------------------------------------------
step "Dependency security"
if gh api -X PUT "repos/${REPO}/vulnerability-alerts" >/dev/null 2>&1; then
	ok "Dependabot alerts"
else
	bad "could not enable Dependabot alerts"
fi
if gh api -X PUT "repos/${REPO}/automated-security-fixes" >/dev/null 2>&1; then
	ok "Dependabot security updates"
else
	bad "could not enable Dependabot security updates"
fi

# --------------------------------------------------------------------------
# Secret scanning
#
# Push protection is the one worth having: it refuses the push rather than
# telling you afterwards that a credential is now in the history and has to
# be rotated.
# --------------------------------------------------------------------------
step "Secret scanning"
for feature in secret_scanning secret_scanning_push_protection; do
	gh api -X PATCH "repos/${REPO}" \
		-f "security_and_analysis[${feature}][status]=enabled" >/dev/null 2>&1
	state="$(gh api "repos/${REPO}" --jq ".security_and_analysis.${feature}.status // \"unavailable\"" 2>/dev/null)"
	if [ "$state" = "enabled" ]; then
		ok "${feature}"
	else
		skip "${feature} is not available"
	fi
done
if [ "$VISIBILITY" != "public" ]; then
	note "private repositories need GitHub Advanced Security; both are free once this is public"
fi

# --------------------------------------------------------------------------
# Branch protection
#
# The ruleset is a file, reviewed like the code it protects. See
# .github/rulesets/README.md for what each rule is for.
# --------------------------------------------------------------------------
step "Branch protection"
RULESET="${ROOT}/.github/rulesets/main.json"
if [ ! -f "$RULESET" ]; then
	bad "no ruleset at ${RULESET}"
else
	name="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["name"])' "$RULESET")"

	# Guarded: listing is refused on a plan without rulesets, and an
	# unchecked capture would put the 403 body into the URL below — which
	# reports an unsupported protocol scheme and hides the real reason.
	if ! existing="$(gh api "repos/${REPO}/rulesets" --jq ".[] | select(.name==\"${name}\") | .id" 2>/dev/null | head -1)"; then
		existing=""
	fi

	if [ -n "$existing" ]; then
		method=PUT; url="repos/${REPO}/rulesets/${existing}"
	else
		method=POST; url="repos/${REPO}/rulesets"
	fi

	if response="$(gh api -X "$method" "$url" --input "$RULESET" 2>&1)"; then
		ok "ruleset '${name}' applied: PR required, linear history, verify + vuln + e2e must pass"
	elif printf '%s' "$response" | grep -q "Upgrade to GitHub Pro or make this repository public"; then
		skip "rulesets are not available"
		note "private repositories need GitHub Pro; run this again once public and main is protected"
	else
		bad "could not apply the ruleset"
		note "$(printf '%s' "$response" | head -2)"
	fi
fi

# --------------------------------------------------------------------------
# Discoverability
# --------------------------------------------------------------------------
step "Description and topics"
if gh api -X PATCH "repos/${REPO}" \
	-f description="Turn Alertmanager alerts into safe, audited Kubernetes remediation. Dry-run by default, guards before every action, and an audit trail that explains itself." \
	-f homepage="https://github.com/${REPO}" >/dev/null 2>&1; then
	ok "description and homepage"
else
	bad "could not set the description"
fi

if gh api -X PUT "repos/${REPO}/topics" \
	-f 'names[]=kubernetes' -f 'names[]=kubernetes-operator' -f 'names[]=sre' \
	-f 'names[]=site-reliability-engineering' -f 'names[]=alertmanager' \
	-f 'names[]=auto-remediation' -f 'names[]=incident-response' \
	-f 'names[]=prometheus' -f 'names[]=golang' -f 'names[]=operator' >/dev/null 2>&1; then
	ok "topics"
else
	bad "could not set the topics"
fi

step "Result"
if [ "$FAILED" -eq 0 ]; then
	printf '    everything this repository can have, it has.\n'
	if [ "$VISIBILITY" != "public" ]; then
		printf '    %sRun this again after making it public: rulesets and secret scanning are waiting on that.%s\n' "$DIM" "$RESET"
	fi
	printf '\n'
else
	printf '    something did not apply; read the ✗ lines above.\n\n'
	exit 1
fi
