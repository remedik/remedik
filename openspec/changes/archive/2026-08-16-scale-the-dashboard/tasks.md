## 1. The arithmetic

- [x] 1.1 Count each dimension in one pass, not one pass per option
- [x] 1.2 A benchmark, and a test that the counts are unchanged

## 2. The controls

- [x] 2.1 Pills below the threshold, a select with quick-picks above it
- [x] 2.2 The select submits a GET landing on the same URL a pill would
- [x] 2.3 Both forms carry counts

## 3. The live region

- [x] 3.1 The refresh swaps the live region when a page marks one
- [x] 3.2 The list marks its rows and counts, leaving the controls alone
- [x] 3.3 A test that the controls are outside it

## 4. Paging

- [x] 4.1 `?page=N`, composing with the filters
- [x] 4.2 Prev, next, and where the reader is
- [x] 4.3 An out-of-range page is not an error

## 5. Proof

- [x] 5.1 e2e: paging and the select both filter
- [x] 5.2 Architecture, CHANGELOG, and the measured limit
