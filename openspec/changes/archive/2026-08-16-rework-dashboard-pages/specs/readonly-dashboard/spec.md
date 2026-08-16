## ADDED Requirements

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
