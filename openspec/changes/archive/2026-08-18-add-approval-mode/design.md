## Context

`ExecutionMode` is an enum of one, and the comment on it explains why: widening
it later is safe, while accepting a value that is not implemented would let a
manifest asking for approval remediate without one. That reasoning is why this
change is possible without a compatibility problem — an older remedik rejects
`approval` loudly.

## Decisions

### 1. The gate is a patch, not a bot

Approval is a human decision recorded on the object it is about. `kubectl patch`
is how every other write in this project is made, it is in the cluster's audit
log without remedik doing anything, and it works from a terminal, a runbook, a
GitOps commit or a bot.

Scoping approval to Slack is what kept it unbuilt for so long. Inverting the
dependency — the gate first, transports later — means the safety feature ships
now and the bot becomes a convenience.

### 2. Waiting is a state, and `AwaitingApproval` is not terminal

The record is created, reaches `AwaitingApproval`, and nothing is resolved,
planned or executed. That last part matters: a strategy waiting for approval must
not have already worked out what it would do to a cluster that has since moved
on. Resolution happens after the decision, so the plan is against the cluster as
it is when it is approved.

It is not terminal, so `Running`-means-interrupted is untouched: waiting is a
state the process can legitimately be found in, exactly as `Pending` is.

### 3. The wait is a requeue, and a decision is a watch event

The reconciler requeues until the deadline. It does not poll for the decision,
because the controller already watches `Remediation` — a patch is an event, so
an approval is acted on in about a second rather than at the next tick.

### 4. Silence escalates

No decision by `approvalTimeout` and the remediation fails as `ApprovalTimeout`
and runs `onFailure.steps`. This is the plan's requirement and it is the right
one: the failure mode of a human gate is that nobody looks, and a gate that
quietly drops what nobody looked at is worse than no gate — it converts an alert
into silence.

A denial is different and does **not** escalate. Somebody looked and said no;
telling them again is not information.

### 5. `by` is a claim, and the docs say so

remedik cannot verify who patched an object without an admission webhook. So the
field is recorded as what was claimed and documented as exactly that, with the
cluster's audit log named as the authority.

The alternative — omitting attribution until it can be trusted — is worse: it
leaves the audit trail with no answer at all to "who approved this", when the
answer is available in the audit log and the claim is a useful cross-check.

### 6. `manual` is refused at the gateway, and says so

A manual strategy never starts from an alert. The refusal is recorded as a guard
refusal is: an event on the strategy and a metric, because an operator asking
"why did nothing happen" needs the answer in the same place as every other
version of that question.

### 7. Waiting is visible

A queue nobody can see is a queue nobody empties, and an approval gate that
silently accumulates is worse than none — it looks like remediation is working.
So `AwaitingApproval` records are on the overview's attention panel, ordered
ahead of things somebody has already seen, and carry how long is left.

## Risks / Trade-offs

- **A strategy in approval mode with no `onFailure.steps` and nobody watching
  will time out into silence.** The dashboard shows the queue and the metric
  counts it, but the honest fix is escalation, and the cookbook recipe says so.
- **A long `approvalTimeout` holds a record open**, which costs nothing but does
  mean `kubectl get remediations` shows work that is not progressing. That is
  accurate rather than unfortunate.
