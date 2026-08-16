## ADDED Requirements

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
