#!/usr/bin/env bash
#
# Stamps each generated CRD with a hash of itself.
#
# `helm upgrade` does not upgrade CRDs. Helm installs everything in a chart's
# crds/ directory once, on first install, and never touches it again — a
# deliberate choice, because a CRD replaced carelessly takes every custom
# resource in the cluster with it. The consequence is that a new field is
# silently absent after an upgrade: the API server prunes what its schema does
# not know, so a strategy asking for approval simply loses the field and
# remediates unattended.
#
# That is not a hypothetical either. It is how `execution.approvalTimeout`
# turned out to be missing from a cluster running today's operator: the chart
# had it, the cluster's CRD did not, and nothing anywhere said so.
#
# So the chart checks. templates/crd-guard.yaml compares this stamp against the
# CRD in the cluster and refuses the upgrade with the command that fixes it. A
# hash of the whole file rather than of the schema alone, deliberately: the file
# is generated, so anything that changed in it is a reason to apply it.
#
# Run by `make manifests`; `make verify-codegen` then holds the stamp current,
# because a stale stamp is a diff in a generated file.
set -euo pipefail

cd "$(dirname "$0")/.."

KEY="remedik.dev/schema-hash"
ANCHOR="controller-gen.kubebuilder.io/version:"

for crd in charts/remedik/crds/*.yaml; do
	# The stamp cannot be part of what it stamps.
	stripped=$(grep -v "^ *${KEY}:" "$crd")
	hash=$(printf '%s\n' "$stripped" | sha256sum | cut -c1-16)

	if ! grep -q "^ *${ANCHOR}" "$crd"; then
		echo "$crd has no controller-gen annotation to stamp beside" >&2
		exit 1
	fi

	# Rewritten from the stripped copy, so running this twice is a no-op.
	printf '%s\n' "$stripped" \
		| sed "s|^\( *\)${ANCHOR}\(.*\)$|\1${ANCHOR}\2\n\1${KEY}: ${hash}|" \
		> "${crd}.stamped"
	mv "${crd}.stamped" "$crd"

	printf '%s  %s\n' "$hash" "$(basename "$crd")"
done
