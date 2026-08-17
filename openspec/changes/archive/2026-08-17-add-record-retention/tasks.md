## 1. The sweeper

- [x] 1.1 A leader-only runnable applying retention on a schedule
- [x] 1.2 Reclaims records whose strategy is quiet, disabled, renamed or gone
- [x] 1.3 Deletes at a bounded rate

## 2. The age limit

- [x] 2.1 `history.maxAge`, unset meaning today's behaviour
- [x] 2.2 Measured from completion; non-terminal records are never candidates
- [x] 2.3 The guard floor, and a log line when it overrides

## 3. Observability

- [x] 3.1 Metrics for records swept, records held back, and the last sweep
- [x] 3.2 The dashboard says what retention is in force

## 4. Proof

- [x] 4.1 Unit tests, including the floor and the orphan case
- [x] 4.2 e2e: a record past its age is reclaimed; one inside the floor is not
- [x] 4.3 Architecture, chart README and CHANGELOG
