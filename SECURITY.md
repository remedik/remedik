# Security Policy

remedik's job is to take actions in your cluster, so its own security
posture *is* the product. The commitments below apply from the first public
release (v0.1.0); the threat model is maintained alongside the specs.

## Reporting a vulnerability

Email **daniel.vechiu1@gmail.com** with details and reproduction steps. You
will get an acknowledgement within 72 hours. Please do not open public
issues for suspected vulnerabilities. Coordinated disclosure is appreciated;
credit is given unless you prefer otherwise.

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
