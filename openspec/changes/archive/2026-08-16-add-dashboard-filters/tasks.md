## 1. The filter

- [x] 1.1 Parse namespace, strategy and state from the query string
- [x] 1.2 Match records, with a namespace filter excluding cluster-scoped targets
- [x] 1.3 Collect the options from every record, not the filtered ones

## 2. The page

- [x] 2.1 A GET form above the table, shown only when there is a choice
- [x] 2.2 The counts follow the filter, and the page says what is hidden
- [x] 2.3 An empty result explains itself rather than looking like an empty cluster
- [x] 2.4 `clusterName` in the header and the browser tab

## 3. Proof

- [x] 3.1 Tests: parsing, matching, options, and that the stats follow the filter
- [x] 3.2 e2e: a filtered page returns only its namespace's records
- [x] 3.3 README, architecture, chart values
