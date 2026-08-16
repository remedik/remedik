## Why

remedik ships one action. The catalogue it needs — restarting any workload,
evicting a pod, rolling back a deploy, cordoning and draining a node, running
a Job — is roughly a dozen more, and every one of them will inherit whatever
the contract looks like when they are written.

Right now the contract has three gaps that would be reproduced twelve times:

1. **A step reports what it called, not whether it worked.** `Execute`
   returning without error means the API accepted the patch. It does not
   mean the rollout completed, or that the pods came back. An operator
   reading "restarted deployment payments/api" cannot tell whether the
   remediation helped.
2. **Nothing appears on the object that was remediated.** Events are
   published on the *strategy*, and only for guard rejections. Someone
   running `kubectl describe deployment payments/api` after an unexplained
   restart sees nothing — they have to already know remedik exists and go
   looking for it.
3. **A step's outcome is two strings.** There is nowhere to put the exit
   code, the number of pods evicted, the revision rolled back to, or the
   replica count before and after. Every action that has something specific
   to say has to bury it in prose.

The cost of fixing this grows with each action added, and it is the
difference between a tool that reports what it did and one that can be
trusted to act unattended.

## What Changes

- **`Result` replaces the string return** on `Plan` and `Execute`. It
  carries the one-line summary as before, plus `Outputs` for structured
  detail and `Kubectl` for the equivalent command a human would have typed.
- **`Verify`, an optional post-condition.** Actions that can check their own
  work implement `action.Verifier`; the engine calls it after `Execute` and
  records the result. Read-only by construction, and a step whose verify
  fails is a failed step.
- **Events on the remediated object.** The reconciler publishes
  `Remediating`, `Remediated` and `RemediationFailed` on the target, so
  `kubectl describe` explains the change where the reader is already looking.
- **`StepStatus` gains `kubectl`, `outputs` and `verified`.** The dashboard
  shows all three.
- `deployment.restart` adopts all of the above, as the reference
  implementation for the actions that follow.

## Non-goals

- New actions. This change is the contract they will be written against.
- Making `Verify` mandatory. An action that cannot check its own work
  should say so by not implementing the interface, not by implementing a
  check that always passes.
- Retrying a failed verify inside the step beyond its own timeout. Retrying
  the whole attempt is what `onFailure.retries` is for, and it already
  exists.

## Capabilities

### Modified Capabilities

- `remediation-execution`
- `action-deployment-restart`

## Impact

- `api/v1alpha1`: three fields on `StepStatus`; `make generate manifests`.
- `internal/action`: `Result`, `Verifier`; the `Action` interface changes
  shape, which is a source-breaking change for out-of-tree actions. There
  are none, and the API is alpha.
- `internal/engine`: `StepRunner` records the new fields and calls `Verify`;
  the reconciler gains an event recorder and a RESTMapper.
- `internal/dashboard`: the step timeline shows the kubectl equivalent, the
  outputs and the verification.
- The chart grants no new permission: publishing events is a permission the
  operator already holds.
