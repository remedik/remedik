## ADDED Requirements

### Requirement: Exactly one instance acts

The operator SHALL hold a lease in its own namespace, and only the instance
holding it SHALL reconcile `Remediation` resources or accept alerts.

The guards keep their state in memory, so two instances would each enforce a
cooldown the other cannot see — the alert storm remedik exists to absorb
would be amplified instead. A replica count above one SHALL therefore be
failover and never additional throughput.

The gateway SHALL keep listening on every replica and answer `503` with a
`Retry-After` when this instance does not hold the lease, rather than
refusing the connection. A Service has one set of endpoints, so a replica
with no listener is indistinguishable from remedik being down — which is
the one thing a gateway must never be mistaken for. Alertmanager retries a
non-2xx and the Service routes the retry, so the alert lands.

Authentication SHALL be checked before leadership, so an unauthenticated
sender cannot learn which replica holds the lease.

#### Scenario: Scaling up does not double the remediation

- **WHEN** the deployment is scaled to two replicas
- **THEN** one lease exists, one replica answers alerts, and the other answers 503 and records nothing

#### Scenario: A standby is not silent

- **WHEN** an alert reaches the replica without the lease
- **THEN** it answers 503 with Retry-After rather than closing the connection, and nothing is recorded

### Requirement: The guards are warmed when the lease is taken

The in-memory guard state SHALL be rebuilt from the existing `Remediation`
resources at the moment this instance becomes the leader, not when the
process starts, and the gateway SHALL NOT accept alerts until that has
completed.

A standby that loaded at boot and took over hours later would enforce
hours-old cooldowns, which is the mistake leader election exists to prevent
arriving through a side door.

An instance that cannot rebuild its guards SHALL stop rather than remediate
without them.

#### Scenario: A late handover does not remediate on stale state

- **WHEN** a standby becomes the leader long after it started
- **THEN** it replays the guard history before accepting anything

#### Scenario: A failed replay is not survivable

- **WHEN** the guard history cannot be read
- **THEN** the operator stops rather than accepting alerts without guards

### Requirement: Readiness is not leadership

The readiness probe SHALL report ready on every replica that is running,
including a standby that holds no lease.

Gating readiness on leadership was tried and rejected: a standby then never
becomes ready, so `helm --wait` and `kubectl rollout status` never complete
on a deployment with more than one replica — the failover this change exists
to allow could not be installed with ordinary tooling.

A standby is doing its job. It waits, and it answers `503` with
`Retry-After` so the sender retries onto the leader. That is where the
contract is enforced, and readiness is not.

A consequence, which callers must expect: a ready replica is not proof that
alerts are being accepted. The leader accepts only once it holds the lease
and has replayed the guards.

#### Scenario: A standby is ready

- **WHEN** a replica is running without the lease
- **THEN** its readiness probe reports ready, and it answers alerts with 503

#### Scenario: More than one replica can be installed

- **WHEN** the chart is installed with two replicas and `--wait`
- **THEN** the install completes
