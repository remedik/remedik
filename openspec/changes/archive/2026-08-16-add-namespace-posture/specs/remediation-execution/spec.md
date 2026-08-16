## MODIFIED Requirements

### Requirement: Global dry-run

Renamed to "Posture", because it is no longer global. The requirement below
replaces it in full.

### Requirement: Posture

Dry-run SHALL be structural: actions implement `Resolve`, `Plan` and
`Execute` separately, and a simulated step calls `Plan`, so the mutating path
is never reached. A step's outcome in dry-run SHALL be `Simulated`, and the
remediation's terminal state SHALL be `Simulated` when every step was.

The posture SHALL be per namespace. The operator takes a default and a set
of overrides keyed by namespace, each `live` or `dryRun`; the posture for a
remediation is the override for the namespace of the object it targets, or
the default when there is none. This is what lets one install act where
remediation has been earned and report everywhere else.

The namespace consulted SHALL be the **target's**, not the operator's own. A
target with no namespace — a node, a webhook, a Job run outside any
workload — SHALL take the default.

The posture SHALL be resolved once, when the `Remediation` is created, and
recorded on it. The reconciler SHALL use the recorded posture and SHALL NOT
consult the operator's current default, because the two can legitimately
disagree and re-reading would silently simulate a namespace somebody
deliberately made live.

An override naming a namespace that does not exist SHALL NOT be an error:
remedik does not watch namespaces, and one can be created after the install.

#### Scenario: One install, two postures

- **WHEN** the default is dry-run and `staging` is overridden to `live`
- **THEN** a remediation targeting `staging` executes and one targeting any other namespace is simulated

#### Scenario: A namespace held back from a live cluster

- **WHEN** the default is live and `prod` is overridden to `dryRun`
- **THEN** a remediation targeting `prod` is simulated and the rest execute

#### Scenario: The target's namespace decides, not remedik's

- **WHEN** remedik runs in `remedik` and that namespace is overridden to `live`, while the default is dry-run
- **THEN** a remediation targeting `payments` is still simulated

#### Scenario: A cluster-scoped target takes the default

- **WHEN** a strategy drains a node, which has no namespace
- **THEN** the default posture applies

#### Scenario: An execution keeps the posture it started with

- **WHEN** a remediation is created live and the operator's default is changed before its retry runs
- **THEN** the retry runs under the posture recorded on the resource

### Requirement: The posture is visible without reading the values file

The operator SHALL log the resolved posture at startup, and SHALL warn when
it is mixed, naming the namespaces that differ.

The chart SHALL print the overrides after an install or upgrade.

The operator SHALL expose `remedik_dry_run` for the default and
`remedik_namespace_posture{namespace,posture}` for each override.

The dashboard SHALL show `Mixed` rather than the default's badge whenever
any namespace differs, and SHALL name those namespaces. Every `Remediation`
already records the posture it ran under.

#### Scenario: The default does not describe the cluster

- **WHEN** the default is dry-run and one namespace is live
- **THEN** the dashboard's badge reads `Mixed` and names that namespace, rather than reading `Dry-run`

#### Scenario: A typo is visible where somebody looks

- **WHEN** an override names a namespace that does not exist
- **THEN** the operator starts, logs the posture it was given, and the dashboard shows the same list
