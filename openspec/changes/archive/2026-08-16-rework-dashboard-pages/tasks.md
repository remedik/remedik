## 1. The defect that hid the others

- [x] 1.1 The refresh reloads the page when the asset fingerprint changes
- [x] 1.2 A test that a changed fingerprint is detectable in the served HTML

## 2. Filtering without state

- [x] 2.1 Filter options render as links, each toggling one clause
- [x] 2.2 The active filter is stated, each clause removable
- [x] 2.3 No form, no Apply, no JavaScript on the filtering path

## 3. The pages

- [x] 3.1 `/remediations`: the full list, the filters and the counts
- [x] 3.2 `/` rebuilt as panels: posture, attention, activity, where
- [x] 3.3 Every panel that counts something links to the list that shows it
- [x] 3.4 Navigation, breadcrumbs and empty states across four pages

## 4. Proof

- [x] 4.1 Tests for each panel builder and for the filter links
- [x] 4.2 e2e: the four pages render, filtering by link works, still GET-only
- [x] 4.3 Architecture and CHANGELOG
