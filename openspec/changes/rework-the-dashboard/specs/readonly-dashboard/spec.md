## ADDED Requirements

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
