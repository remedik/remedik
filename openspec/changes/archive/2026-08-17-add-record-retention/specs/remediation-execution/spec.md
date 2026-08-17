## ADDED Requirements

### Requirement: Retention is applied on a schedule, not only on completion

remedik SHALL apply its retention policy periodically, independently of
whether any remediation is completing.

Pruning ran inside the terminal status write, so it only ever reclaimed
records for the strategy that had just finished one. A strategy that was
disabled, renamed, deleted, or had simply gone quiet kept every record it had
ever made, for ever. Over the life of a cluster, strategies are added and
removed, and each departure left a permanent deposit — a leak rather than a
policy.

The sweep SHALL run only on the instance holding the lease, and SHALL delete
at a bounded rate.

#### Scenario: A deleted strategy's records are reclaimed

- **WHEN** a strategy no longer exists
- **AND** its records are outside the retention
- **THEN** a sweep deletes them

#### Scenario: A quiet strategy's records are reclaimed

- **WHEN** a strategy has completed nothing for longer than the retention
- **THEN** its records outside the retention are deleted without it running
  again

### Requirement: Records can be retained by age

The operator SHALL support a maximum age for terminal records, applied
regardless of how many there are.

Retention is expressed in time — in a data policy, an audit requirement, or a
conversation with whoever owns etcd. A count per strategy may be a week for one
and three years for another.

Age SHALL be measured from completion. A record that has not reached a
terminal state SHALL never be a candidate: it is work in flight, not history.

An unset maximum age SHALL mean today's behaviour exactly, so that an upgrade
cannot delete anybody's history because a default looked reasonable.

#### Scenario: An old record is reclaimed

- **WHEN** a terminal record completed longer ago than the maximum age
- **THEN** a sweep deletes it

#### Scenario: Work in flight is never swept

- **WHEN** a record is Pending or Running, however old
- **THEN** no sweep deletes it

### Requirement: Retention never deletes what the guards are relying on

A sweep SHALL NOT delete a record newer than the longest guard window
currently configured across all strategies, whatever the maximum age says.

Guard state is rebuilt from existing records at startup, so a record inside a
strategy's cooldown or give-up window is not history — it is the reason remedik
will refuse to act again. Deleting it means that after the next restart remedik
remediates something it had correctly refused.

When the floor overrides the configured age, the operator SHALL say so, because
a retention policy that is quietly not being applied is worse than one that is
refused.

#### Scenario: A cooldown outlives the retention

- **WHEN** the maximum age is shorter than a strategy's cooldown
- **THEN** records inside that cooldown are kept, and the operator logs that
  the floor is in force

#### Scenario: The floor follows the strategies

- **WHEN** a strategy's cooldown is lengthened
- **THEN** the floor grows with it, without a restart
