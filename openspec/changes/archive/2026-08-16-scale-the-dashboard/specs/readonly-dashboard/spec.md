## ADDED Requirements

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
