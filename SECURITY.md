# Security Policy

remedik's job is to take actions in your cluster, so its own security
posture *is* the product. The commitments below apply from the first public
release (v0.1.0); the threat model is maintained alongside the specs.

## The promises

[docs/invariants.md](docs/invariants.md) is the document to read before granting
remedik write access: what it will never do, and — for each one — whether that
is enforced structurally or is only a convention. A review that cannot tell
those apart is not a review, so the page says which is which.

If one of those promises is not actually kept, that is a security issue and not
a bug, whatever the impact looks like.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting:
**[Report a vulnerability](https://github.com/remedik/remedik/security/advisories/new)**.
It is private, it keeps the report, the discussion and the eventual advisory in
one place, and it is the channel that works if the maintainers change.

It is the only channel, deliberately: an address in a security policy is worth
no more than the mailbox behind it, and a published address nobody reads is
worse than none.

**Please do not describe a suspected vulnerability in a public issue.** If the
form is unavailable to you, an issue asking a maintainer to get in touch — with
nothing about the finding in it — is the way to reach one.

You will get an acknowledgement within 72 hours. Coordinated disclosure is
appreciated; credit is given unless you prefer otherwise.

## Supported versions

Pre-release: only the latest `main` is supported.

## Commitments (from v0.1.0)

- **Release artifacts**: container images signed with cosign, SBOM
  published, SLSA provenance generated in CI.
- **Runtime**: distroless base, non-root, read-only root filesystem, seccomp
  profile.
- **Network**: an opt-in NetworkPolicy in the chart naming who may reach each
  port — the gateway, metrics and the dashboard. It is opt-in rather than
  default because a policy naming the wrong peers stops Alertmanager
  silently, and the chart refuses to render one that would. Ingress only:
  remedik's single outbound call is to the API server, whose address belongs
  to your cluster rather than to this chart.
- **Access**: RBAC generated per enabled feature — never cluster-admin.
- **Data**: secret values are never logged, and never included in LLM
  context. The read-only dashboard shows alert labels, namespaces and
  workload names, which is why it is off by default, served on a ClusterIP
  Service with no Ingress, and authenticated.
- **Dependencies**: scanned in CI (govulncheck) on every PR.

## Threat model

Tracked in [`docs/adr/`](docs/adr/) and expanded with each capability spec.
Core stance: the deterministic engine only ever executes steps from declared
`RemediationStrategy` resources; optional AI components are read-only by
construction.
