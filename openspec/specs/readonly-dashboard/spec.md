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

### Requirement: Repeats are counted, not repeated

The list SHALL collapse records that are adjacent in the displayed order and
identical in strategy, target, alert and state into a single row stating how
many records it stands for, the age of the newest and of the oldest.

The count SHALL be a link to the records the group stands for, selected
exactly — by strategy, target, alert and state, on top of whatever the reader
was already looking through.

It SHALL NOT be an expander holding its own open state. The list's rows sit
inside the region the ten-second refresh replaces, so a group opened by a
reader would be closed again by a timer while they read it; showing the
records is navigation for the same reason filtering is.

Collapsing SHALL apply only when the list is in its default time order.
Adjacency carries no meaning in any other order, and a group formed from a
different sort would present an arbitrary subset as a run. When another order
is in force the page SHALL show every row and SHALL say why grouping is not
applied.

The counts stated above the table SHALL continue to count records, never
groups, so the figures a reader is checking against the rows never describe a
different population from the one the rows describe.

#### Scenario: Eight identical failures are one row

- **WHEN** eight consecutive records share a strategy, target, alert and state
- **THEN** the list shows one row saying it stands for eight, with the newest and oldest ages

#### Scenario: The records behind a group are one click away

- **WHEN** a group's count is followed
- **THEN** the list shows exactly those records, and the filter that selected them is stated on the page

#### Scenario: The counts still count records

- **WHEN** grouping collapses 1328 records into 400 rows
- **THEN** the page still states the number of records matched and how many of them this page covers

#### Scenario: Another order turns grouping off

- **WHEN** the list is sorted by duration
- **THEN** every record is its own row and the page states that grouping applies only in time order

### Requirement: The approvals queue has a page, ordered by its deadline

The dashboard SHALL serve `/approvals` listing every remediation awaiting
approval, ordered by how soon it expires, soonest first.

Each entry SHALL state the time remaining, the target, the alert, the strategy,
the steps that would run if it is approved, and the commands that approve or
deny it. A deadline already passed SHALL be shown as expired rather than as a
negative remaining time, because the reconcile that fails the record may not
have run yet.

The navigation entry SHALL carry the number waiting. The page SHALL be served
whether or not anything is waiting, and when nothing is, SHALL distinguish "no
strategy asks for approval" from "everything waiting has been decided".

The page SHALL write nothing: it prints the commands that decide a record, in
the manner of the detail page, and offers no control that acts.

#### Scenario: The one about to expire is first

- **WHEN** one remediation has forty seconds left and another has fourteen minutes
- **THEN** the one with forty seconds is listed first

#### Scenario: An expired approval is not a negative number

- **WHEN** a remediation's approval deadline has passed and it has not yet been reconciled
- **THEN** the entry reads as expired

#### Scenario: An empty queue explains which empty it is

- **WHEN** no remediation is awaiting approval and no strategy uses approval mode
- **THEN** the page says no strategy asks for approval, rather than implying a queue was emptied

#### Scenario: The page still cannot write

- **WHEN** any non-GET request reaches `/approvals`
- **THEN** the response is 405

### Requirement: The overview states impact, derived from the records

The overview SHALL state, for the window it describes: the share of executions
that ran for real and succeeded, the median elapsed time from the alert firing
to the record's terminal state, and each of those against the previous window
of equal length so the figure has a direction.

Simulated records SHALL be excluded from the success share, because nothing
happened. The median SHALL be withheld, with the reason stated, when too few
records fall in the window for it to mean anything.

The panel SHALL state the range it actually covered, which retention may have
made shorter than the range requested.

The dashboard SHALL NOT state any figure it cannot derive from its records. In
particular it SHALL NOT estimate engineer time saved, incidents avoided, or any
other quantity requiring a counterfactual.

#### Scenario: A percentage has a direction

- **WHEN** the last day succeeded at 75% and the day before at 60%
- **THEN** the panel states 75% and that it rose by 15 points

#### Scenario: Two records are not a median

- **WHEN** the window holds too few records to support a median
- **THEN** the panel says so instead of printing one

#### Scenario: Retention shortened the window

- **WHEN** a seven-day window is requested and the oldest record kept is three days old
- **THEN** the panel states the range it actually covered

### Requirement: Activity carries outcome and range

The activity panel SHALL show each interval as segments by outcome —
succeeded, failed and simulated — in the colours those states carry elsewhere
on the pages, and SHALL offer at least a one-day and a seven-day range as
links.

Each interval SHALL carry its exact counts in a form available without
JavaScript and reachable from a keyboard.

The panel SHALL keep a floor on its scale, so a quiet period does not render
as full-height bars.

#### Scenario: A quiet day and a bad day look different

- **WHEN** an interval holds one failure and another holds twenty successes
- **THEN** the two bars differ in both height and colour

#### Scenario: The range is a link

- **WHEN** the seven-day range is chosen
- **THEN** the page is a URL that shows the same view when reloaded or sent to somebody

### Requirement: A remediation has a shape in time

The detail page SHALL present the record as one ordered sequence: the alert
firing, the record's creation, any wait for approval, each attempt, each step
within it, and the escalation — with the time elapsed between entries.

A proportional bar SHALL be drawn only for entries lasting at least one
second. Timestamps have second granularity, so drawing sub-second durations to
scale would imply a precision the data does not carry; where every entry is
instant, the page SHALL say the whole record completed within a second.

The page SHALL also show the recent outcomes for the same target, each linking
to its record, so a remediation that keeps being needed is visible from the
one that is being read.

#### Scenario: The order is the order it happened in

- **WHEN** a remediation waited for approval, ran two steps and escalated
- **THEN** the page shows those five moments in the order they occurred, with the elapsed time between them

#### Scenario: A sub-second record is not drawn as a bar

- **WHEN** every timestamp on a record falls in the same second
- **THEN** the page states that it completed within a second rather than drawing a bar of arbitrary length

#### Scenario: The target has been here before

- **WHEN** the same target has earlier records
- **THEN** the page lists their outcomes and ages, each linking to its record

### Requirement: A failure is explained, not only quoted

Where a record's reason, failing step, returned message and the history of its
target identify a known cause, the page SHALL state that cause as a sentence,
name the fields it read to reach it, and give the next command where one
exists.

The explanation SHALL be a pure function of the record and the time. It SHALL
NOT replace the message the action returned, which stays on the page in full.

Where no rule recognises the record, the page SHALL say nothing rather than
speculate.

No explanation SHALL be produced by a language model or by any request leaving
the operator. The binary's only outbound connection remains the API server.

#### Scenario: A missing object is explained as one

- **WHEN** a step failed because the object named by the alert does not exist
- **THEN** the page says the target no longer exists or was never in that namespace, names the labels it read, and shows the raw message unchanged

#### Scenario: An unrecognised failure is not guessed at

- **WHEN** no rule matches the record
- **THEN** the page shows the message it always showed and offers no explanation

#### Scenario: The explainer needs nothing but the record

- **WHEN** the explainer is called in a test with a record and a time
- **THEN** it returns its sentence without a client, a network call or a global

### Requirement: The pages are reachable without the mouse

The dashboard SHALL offer a palette, opened with `Ctrl+K` or `Cmd+K`, listing
the pages, the namespaces, the strategies, the alert names and the most recent
records, narrowing as the reader types, and navigating to the chosen entry.

The dashboard SHALL offer single-key navigation to each page and a key that
lists every shortcut.

All of this SHALL be an enhancement. With JavaScript disabled every page SHALL
render, navigate and read exactly as before, and no function SHALL be reachable
only by a shortcut. Choosing an entry SHALL navigate, so the result is a URL
like any other.

The data the palette lists SHALL be fetched from the same origin, under the
same authentication as the pages, by a GET route disclosing nothing the pages
do not already show.

#### Scenario: A namespace is three keystrokes away

- **WHEN** a reader opens the palette and types part of a namespace name
- **THEN** the matching namespaces are offered and choosing one navigates to that namespace's records

#### Scenario: JavaScript off loses nothing

- **WHEN** the dashboard is used with JavaScript disabled
- **THEN** every page renders and every function remains reachable by link

#### Scenario: The palette route is read-only and authenticated

- **WHEN** the palette's route is requested without the configured token, or with a method other than GET or HEAD
- **THEN** the response is 401 or 405 respectively

### Requirement: Links out of the dashboard are configured and validated

The chart SHALL allow links to external systems to be configured as a name and
a URL template, substituting the record's namespace, target, name, alert,
fingerprint and a time window around it.

Substituted values SHALL be percent-encoded. A template whose scheme is
anything but `http` or `https` SHALL be rejected at startup, with a log line
naming it, rather than rendered into a page.

No configured link SHALL be required for any page to render.

#### Scenario: A record links to its dashboards

- **WHEN** a link is configured with a template naming the alert and a time window
- **THEN** the record's page offers that link with its own alert and window substituted and encoded

#### Scenario: A hostile template does not reach the page

- **WHEN** a configured link uses a `javascript:` scheme
- **THEN** it is rejected at startup and no page renders it

### Requirement: The pages are usable on a phone

Below a stated narrow width each table row SHALL render as a card carrying its
column names, from the same markup the wide layout uses, with no horizontal
scrolling of the page and no JavaScript deciding the layout.

The header SHALL keep the posture and the cluster readable at that width,
because "is it live" is the question somebody woken up asks first.

#### Scenario: A table becomes cards

- **WHEN** a list page is rendered at 390 CSS pixels wide
- **THEN** each record is a card labelled with its columns and the page does not scroll sideways

#### Scenario: The posture survives the narrow header

- **WHEN** the header is rendered at 390 CSS pixels wide
- **THEN** the posture and any pause reason are still legible

### Requirement: Ordering the list

Every column header on the list SHALL be a link that orders by that column,
and SHALL reverse when it is already in force. The order SHALL be part of the
URL, and SHALL compose with the filter and with paging.

No control on the ordering path SHALL hold state between the choice and its
application, because the headers sit inside the region the ten-second refresh
replaces.

An unrecognised sort key SHALL fall back to the default order rather than
refusing the page.

#### Scenario: Which of these took ten minutes

- **WHEN** the duration header is chosen
- **THEN** the rows are ordered by how long each took, and the page is a URL that shows the same

#### Scenario: Ordering survives the refresh

- **WHEN** an order is in force and the ten-second refresh fires
- **THEN** the order is unchanged, because it is in the URL and not in a control

### Requirement: Filtering by escalation, target and alert

The list SHALL accept `escalation`, `target` and `alert` as filter clauses
alongside the existing ones. `escalation` SHALL select over failures by whether
telling somebody succeeded, failed, or was never attempted.

A target and an alert shown on any row SHALL be a link to the records that
share it.

A clause with no control of its own SHALL be stated on the page as a removable
chip, so a page is never narrowed by something it does not otherwise show.

Every panel that counts something SHALL link to the set it counted, and the
count on the destination SHALL equal the count on the panel.

#### Scenario: What has remedik done to this deployment

- **WHEN** a target on any row is followed
- **THEN** the list shows the records for that target, across strategies

#### Scenario: A count links to what it counted

- **WHEN** the overview states how many escalations failed and that figure is followed
- **THEN** the list shows exactly that many records

### Requirement: A printed command can be copied

Where a page prints a command — the patch that decides an approval, or the
`kubectl` a step is equivalent to — it SHALL offer a control that copies it.

The control SHALL be created by the page's script rather than written into the
markup, so it cannot appear where the clipboard is unavailable: a button that
silently does nothing is worse than no button.

#### Scenario: Nobody retypes a patch at three in the morning

- **WHEN** a page prints the approval patch in a secure context
- **THEN** a copy control is offered and copies the command exactly

#### Scenario: No control where it could not work

- **WHEN** the page is served over a context where the clipboard is unavailable
- **THEN** no copy control is rendered and the command is still shown in full

#### Scenario: Every page that prints one, not only some

- **WHEN** a page that carries no filter control prints a command
- **THEN** the copy control is offered there too
