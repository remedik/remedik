# action-pvc-expand Specification

## Purpose

Grow a PersistentVolumeClaim that is filling up — but only where growing it
will actually do something.

## Requirements

### Requirement: Refuse a StorageClass that cannot expand

The `pvc.expand` action SHALL read the claim's StorageClass and SHALL refuse
unless it sets `allowVolumeExpansion`.

Without the check the API server accepts the change and nothing happens:
remedik would record a success that did nothing, which is worse than
failing, because nobody goes looking.

#### Scenario: A class that does not allow expansion

- **WHEN** the StorageClass does not set `allowVolumeExpansion`
- **THEN** the step fails naming the class, and the claim is untouched

### Requirement: Growth only, and bounded

The action SHALL accept an absolute size or a relative increase, SHALL
require a ceiling for a relative increase, and SHALL refuse any target at or
below the current request: Kubernetes cannot shrink a volume.

#### Scenario: Shrinking is refused

- **WHEN** the step asks for less than the claim already requests
- **THEN** the step fails saying a volume cannot be shrunk

#### Scenario: A relative increase needs a ceiling

- **WHEN** the step sets a percentage and no maximum
- **THEN** the step fails: growth with no limit is a bill nobody agreed to

### Requirement: Verified as capacity, not as a request

The action SHALL wait for the claim to report the new capacity, and SHALL
fail the step when it does not.

#### Scenario: A resize that never completes

- **WHEN** the claim's status capacity does not reach the new size in time
- **THEN** the step fails, and the message names the usual cause
