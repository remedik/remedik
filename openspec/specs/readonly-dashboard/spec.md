# readonly-dashboard Specification

## Purpose

A dashboard served by the operator that answers, without kubectl, the two
questions remedik exists to answer: what would this have done during a
dry-run trial, and why did nothing happen during an incident.

## Requirements

### Requirement: Read-only by construction

The dashboard SHALL expose no operation that changes cluster state. It
SHALL serve only GET and HEAD, answering any other method with 405, and it
SHALL be served by a handler that is given a read-only client.

This is a structural guarantee, not a policy: there is no code path from
the dashboard to a write.

#### Scenario: A mutating request is refused

- **WHEN** any POST, PUT, PATCH or DELETE reaches any dashboard path
- **THEN** the response is 405 and nothing in the cluster changes

### Requirement: Overview page

The dashboard SHALL serve an overview at `/` showing: the count of
remediations by terminal state, the number currently in flight, and the
most recent executions with their strategy, target, state, and age.

When the operator is in dry-run, the overview SHALL present the dry-run
summary prominently: how many remediations were simulated, over what
period, and broken down by strategy — the report an operator shows their
team before turning dry-run off.

#### Scenario: A dry-run trial is summarised

- **WHEN** the operator has recorded simulated remediations and dry-run is on
- **THEN** the overview states how many would have run, per strategy, and over what period

#### Scenario: An empty cluster explains itself

- **WHEN** no remediation exists yet
- **THEN** the overview says so and states what to check, rather than rendering an empty table

### Requirement: Remediation detail page

The dashboard SHALL serve a detail page per remediation showing: the
triggering alert's name, fingerprint and labels; the plan; every step with
its phase, plan or message, and timings; the attempt count; and the
terminal state with its reason.

#### Scenario: A failure explains itself

- **WHEN** a remediation failed
- **THEN** its page names the failing step, shows the message the action returned, and shows which steps were skipped

#### Scenario: A simulated remediation shows the plan

- **WHEN** a remediation is in state Simulated
- **THEN** its page shows, per step, what would have been done

### Requirement: Strategies page

The dashboard SHALL list every strategy with its enabled state, matchers,
guards, steps, and the time of its last execution.

#### Scenario: A disabled strategy is visibly disabled

- **WHEN** a strategy has `enabled: false`
- **THEN** the list marks it as disabled rather than showing it like the others

### Requirement: Disabled by default and access-controlled

The dashboard SHALL be disabled unless explicitly enabled. When enabled it
SHALL support an optional token, and a request that does not present it
SHALL be answered 401.

One token, presented either way: as `Authorization: Bearer <token>`, or as
the password of an HTTP Basic request with any username. The second exists
because the documented way to reach the dashboard is a port-forward and a
browser, and a browser cannot be told to send a bearer header. The 401 SHALL
offer a challenge a browser can act on.

The chart SHALL expose the dashboard as a ClusterIP Service and SHALL NOT
create an Ingress: reaching it is a deliberate act by the cluster's owner.

#### Scenario: Not installed unless asked for

- **WHEN** the chart is installed with default values
- **THEN** no dashboard port, Service or handler exists

#### Scenario: Authentication is enforced when configured

- **WHEN** a token is configured and a request arrives without it
- **THEN** the response is 401 and no cluster data is disclosed

#### Scenario: A browser can present the token

- **WHEN** the token is presented as the password of a Basic request
- **THEN** the page is served, whatever the username was

### Requirement: No additional permissions

Enabling the dashboard SHALL NOT require any RBAC rule beyond those the
operator already holds.

#### Scenario: The chart grants nothing extra

- **WHEN** the rendered manifests are compared with the dashboard enabled and disabled
- **THEN** the Role and ClusterRole rules are identical

### Requirement: Serves without external dependencies

Pages SHALL be rendered by the operator and SHALL NOT request scripts,
styles or fonts from outside the cluster: a cluster with no internet egress
must render the dashboard identically.

#### Scenario: An air-gapped cluster renders the same page

- **WHEN** the dashboard is served in a cluster with no outbound internet access
- **THEN** every page renders fully, with no missing styles or failed requests

### Requirement: Filtering the overview

The overview SHALL accept `namespace`, `strategy` and `state` as query
parameters and show only the executions that match all of them. `namespace`
SHALL be matched against the namespace of the object remediated, not
remedik's own.

The summary counts SHALL describe the filtered set, so the figures above the
table always describe the rows in it.

The filter controls SHALL offer every value present in the unfiltered
records, so a selection can always be changed or undone, and SHALL be shown
only when more than one value exists to choose between.

An unrecognised value SHALL be honoured rather than rejected: the answer
"nothing happened there" is information, and a filtered URL must not become
an error page.

A `namespace` filter SHALL exclude records whose target is cluster-scoped,
because a node is in no namespace.

#### Scenario: One namespace during an incident

- **WHEN** the overview is requested with `?namespace=payments`
- **THEN** only executions targeting objects in `payments` are listed, the counts describe those, and the page states how many records the filter is hiding

#### Scenario: The filter is a link

- **WHEN** a filtered page is reloaded, bookmarked or sent to somebody else
- **THEN** it shows the same view, because the filter is entirely in the URL

#### Scenario: A filter that matches nothing is not an empty cluster

- **WHEN** a filter matches no records but records exist
- **THEN** the page says how many exist, names what was filtered for, and offers a link back to everything — rather than showing the "nothing has run yet" state

#### Scenario: The choices do not narrow themselves

- **WHEN** a namespace is selected
- **THEN** the strategy and state controls still offer every value seen in any record

#### Scenario: A node is in no namespace

- **WHEN** a namespace filter is applied and a record targets a node
- **THEN** that record is excluded

### Requirement: The cluster is named, not filtered

The dashboard SHALL show a configured cluster name in its header and browser
title, and SHALL show neither when none is configured.

The dashboard SHALL NOT offer a cluster filter. remedik watches the cluster
it runs in, so a control offering a choice of clusters would offer a choice
of one.

#### Scenario: Three dashboards, three tabs

- **WHEN** three clusters are port-forwarded at once and each operator was given a cluster name
- **THEN** each browser tab is titled with its cluster and each header names it
