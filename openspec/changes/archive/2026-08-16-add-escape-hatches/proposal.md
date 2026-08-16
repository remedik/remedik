## Why

Every built-in action remedik will ever have covers a fraction of what
people need to do at 3am. The rest — call the pipeline that reprovisions a
node, run the runbook script somebody already wrote, poke an internal
service — is unreachable, and "remedik cannot do X" is a reason not to
install it at all.

Comparable tools solve this with one primitive, and it is consistently the
one people use most: run a container, hand it the alert, tell me what it
said. remedik has no equivalent, so today the answer to anything outside
four verbs is a fork.

There is also a gap in the contract that only becomes obvious here.
`Resolve` receives the alert's labels; `Plan` and `Execute` do not. A verb
that restarts a Deployment does not need them once the target is resolved.
A verb whose entire job is *handing the alert to something else* is useless
without them.

## What Changes

- **`action.Request`** replaces the widening parameter list on `Plan`,
  `Execute` and `Verify`. It carries the target and the step's parameters as
  before, plus the alert's labels and the names of the remediation and
  strategy responsible — everything an action might need to explain itself
  to something outside the cluster.
- **`webhook.call`** — POSTs the alert, the strategy and the plan to a URL,
  with an optional bearer token from a Secret. The cheapest way to reach
  anything remedik will never implement, and it moves the blast radius
  outside the cluster where somebody else's controls apply.
- **`job.run`** — runs an image as a Job in the operator's namespace, with
  the alert's labels as environment variables, under a ServiceAccount the
  step names. Verification waits for the Job and records its exit code and
  the last lines of its output.
- **`script.run`** — the same, with the script taken from a ConfigMap
  instead of baked into an image, so a runbook can be edited without a
  rebuild.

## Non-goals

- Running Jobs in arbitrary namespaces. They run where remedik runs, so the
  permission stays namespaced. A Job that must act elsewhere does so through
  its own ServiceAccount, which is the thing that should carry that
  authority.
- A shell. `command` is a JSON array, so there are no quoting rules to
  invent and no place for word-splitting to surprise anyone.
- Streaming logs. The last lines of output, on the record, is what an
  operator reads; anything more belongs in the cluster's logging.

## Capabilities

### New Capabilities

- `action-webhook-call`
- `action-job-run`

### Modified Capabilities

- `remediation-execution`

## Impact

- `internal/action`: `Request`; the three methods change shape, which is
  source-breaking for out-of-tree actions. There are none.
- New permissions, each tied to an action that must be enabled: `get` on
  secrets (webhook.call) and on configmaps (script.run), both namespaced;
  `create`, `get` and `delete` on jobs plus `get`/`list` on pods and
  `pods/log`, namespaced, for the Job runners.
- The chart's RBAC table grows a namespaced section, because these are the
  first actions whose permissions belong in the Role rather than the
  ClusterRole.
