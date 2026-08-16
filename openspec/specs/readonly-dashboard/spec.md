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

The controls SHALL hold no state between a choice and its application — see
"Filtering is navigation" below, which supersedes the form this requirement
originally described.

An active filter SHALL be stated on the page, and each clause SHALL be
liftable on its own without disturbing the others.

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

#### Scenario: The auto-refresh cannot eat a selection

- **WHEN** a reader chooses a value and the page's ten-second refresh fires
- **THEN** nothing is lost, because a choice is a link and applies as it is made

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

### Requirement: The shell reloads when the operator changes

The dashboard SHALL carry its asset fingerprint in the page, and the
auto-refresh SHALL compare the fingerprint it fetches with the running one
and reload the whole page when they differ.

Only the content region is replaced by a refresh, so without this a tab left
open across an operator upgrade keeps the old stylesheet, the old script and
the old markup indefinitely while its data stays current — which is the most
convincing way for a page to be wrong, and which made two correct fixes
invisible to the person who reported the bug.

#### Scenario: An upgrade does not leave a tab on last week's page

- **WHEN** the operator is upgraded while a dashboard tab is open
- **THEN** the next refresh reloads the page rather than rendering new data through the old shell

### Requirement: Filtering is navigation

Every filter choice SHALL be a link. The filtering path SHALL contain no
form, no input and no JavaScript, so no state exists between choosing a
value and applying it.

Choosing the value already in force SHALL remove it, so the same control
both narrows and widens.

Each control SHALL show how many records the choice would yield, counted
without that dimension's own clause, so switching between values is possible
without trying each one.

#### Scenario: A choice cannot be lost

- **WHEN** a reader clicks a filter value
- **THEN** the page is filtered immediately, with nothing held between the click and the result for a refresh to destroy

#### Scenario: The same control widens

- **WHEN** a filter value is in force and the reader clicks it again
- **THEN** that clause is removed and the other clauses stay

### Requirement: The overview is a dashboard, the list is a list

The overview SHALL answer "is anything wrong right now?" as panels: the
posture, what needs attention, activity over the last day, and where
remediation is happening. Every panel that counts something SHALL link to
the filtered list showing it.

The overview SHALL show only a short tail of recent executions and link to
the full list. `/remediations` SHALL be the list, carrying the filters and
the counts.

The "needs attention" panel SHALL order its entries by how much silence each
represents, so a failed escalation — a remediation that failed with nobody
told — is listed above a failure that was reported.

#### Scenario: The front page answers the front-page question

- **WHEN** an escalation has failed
- **THEN** the overview leads its attention panel with it, saying nobody may know, and links to those records

#### Scenario: The list is one click away

- **WHEN** a reader wants the whole history
- **THEN** the overview links to `/remediations`, which lists it with the filters

#### Scenario: Both spellings of the list path work

- **WHEN** `/remediations` or `/remediations/` is requested
- **THEN** the list is served, without a redirect

### Requirement: The dashboard stays usable at any cluster size

The cost of building a page SHALL be linear in the number of records, not a
product of records and filter values. Counting a dimension's values SHALL
take one pass over the records rather than one pass per value.

Filter controls SHALL adapt to how many values a dimension has. A handful
SHALL render as links, so a choice is one click. Above that threshold the
dimension SHALL render as a select carrying every value with its count, plus
the busiest few as links — the browser's own keyboard type-ahead is then the
search, and no JavaScript is required to get it.

A select's form SHALL carry the other clauses, so choosing a value in one
dimension does not silently clear another.

The repository SHALL hold benchmarks at a stated size, so a performance
claim is a measurement rather than an impression.

#### Scenario: A hundred and fifty namespaces

- **WHEN** the records span more namespaces than the threshold
- **THEN** the namespace control is a select listing all of them with counts, beside links for the busiest few, rather than a wall of links

#### Scenario: A handful of states stays one click

- **WHEN** a dimension has only a few values
- **THEN** it renders as links, because a menu to open would be slower than what it replaced

#### Scenario: Choosing one dimension keeps the others

- **WHEN** a state is already filtered and a namespace is chosen from the select
- **THEN** both clauses are in force

### Requirement: The list pages

The list SHALL draw a bounded page of executions and SHALL offer links to
the pages either side, stating which rows are shown and how many pages there
are. Paging SHALL preserve the filter.

A page number beyond the end SHALL be clamped rather than refused: history
is pruned, so a bookmarked page may no longer exist, and that is not an
error.

#### Scenario: Ten thousand records are a list, not a truncation

- **WHEN** more executions match than fit on a page
- **THEN** the page states which rows it is showing, out of how many, and links to the next

#### Scenario: Paging and filtering compose

- **WHEN** a reader filters by namespace and turns the page
- **THEN** the filter is still in force and every row is still in that namespace

#### Scenario: A bookmarked page that no longer exists

- **WHEN** `?page=99` is requested on a list with three pages
- **THEN** the last page is shown

### Requirement: The refresh replaces only the live region

A page MAY mark a region as the only part the auto-refresh replaces. The
list SHALL mark its rows and counts, keeping its filter controls outside, so
a value chosen or typed and not yet applied cannot be destroyed by a
refresh.

#### Scenario: A select survives the refresh

- **WHEN** a reader opens the namespace select and the ten-second refresh fires
- **THEN** the control is untouched, because only the rows beneath it were replaced
