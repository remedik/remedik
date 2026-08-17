## ADDED Requirements

### Requirement: Namespaces page

The dashboard SHALL provide a page listing every namespace remedik has
remediated in, with that namespace's posture, its execution outcomes, how
many of its failures nobody was told about, and when it was last active.

Every row SHALL link to that namespace's executions.

The page SHALL describe remedik's own record only. It SHALL NOT present
itself as a measure of the namespace's health: remedik knows the
remediations it ran, not whether the workloads there are well.

#### Scenario: A namespace remedik has never touched does not appear

- **WHEN** the page is rendered
- **THEN** only namespaces with at least one recorded remediation are listed
- **AND** no additional Kubernetes permission is used to discover the rest

#### Scenario: A cluster-scoped remediation is not a namespace

- **WHEN** a remediation targets a node
- **THEN** it is not counted as a namespace row

### Requirement: The namespaces page is ordered by what needs attention

The page SHALL order rows so that the namespaces worth reading come first:
failures nobody was told about, then failures somebody has seen, then
volume, then name.

The order SHALL be stable for the same records, so a page does not
rearrange itself while somebody is reading it.

#### Scenario: An unheard failure outranks a busier namespace

- **WHEN** one namespace has a single failure with no successful escalation
- **AND** another has twenty executions and no failures
- **THEN** the namespace with the unheard failure is listed first

#### Scenario: A failure somebody was told about is not shown as an alarm

- **WHEN** a failed remediation's escalation succeeded
- **THEN** the row is marked as a warning rather than counted as unheard

### Requirement: Each namespace row states its posture

Each row SHALL state whether remedik acts in that namespace or only
reports there, resolved from the operator's posture including any
per-namespace override.

A namespace where nothing ran for real SHALL NOT be shown with a success
rate, because a rate of zero over zero attempts reads as failure.

#### Scenario: A namespace held back from a live default

- **WHEN** the default posture is live and a namespace is overridden to dry-run
- **THEN** that namespace's row reads as reporting and the others as live

#### Scenario: A namespace that has only ever been simulated

- **WHEN** every record in a namespace is Simulated
- **THEN** the row says nothing ran for real rather than showing 0%
- **AND** the namespace is not counted as needing attention
