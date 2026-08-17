## 1. The sweeper

- [ ] 1.1 A leader-only runnable applying retention on a schedule
- [ ] 1.2 Reclaims records whose strategy is quiet, disabled, renamed or gone
- [ ] 1.3 Deletes at a bounded rate

## 2. The age limit

- [ ] 2.1 `history.maxAge`, unset meaning today's behaviour
- [ ] 2.2 Measured from completion; non-terminal records are never candidates
- [ ] 2.3 The guard floor, and a log line when it overrides

## 3. Observability

- [ ] 3.1 Metrics for records swept, records held back, and the last sweep
- [ ] 3.2 The dashboard says what retention is in force

## 4. Proof

- [ ] 4.1 Unit tests, including the floor and the orphan case
- [ ] 4.2 e2e: a record past its age is reclaimed; one inside the floor is not
- [ ] 4.3 Architecture, chart README and CHANGELOG
