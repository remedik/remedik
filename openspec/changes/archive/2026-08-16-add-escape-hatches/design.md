## Context

Three actions that reach outside remedik. They are the largest trust surface
in the project, and every decision below is about bounding them: what they
can reach, whose authority they run with, and what a mistake costs.

## Decisions

1. **`action.Request` replaces the parameter list.** `Resolve` received the
   alert's labels and `Plan`/`Execute` did not, which was fine while every
   verb restarted something it had already resolved. A verb whose entire job
   is handing the incident to something else is useless without them. It is
   a struct for the same reason `Result` is: the next action to need
   something can add a field without touching the seven before it.

2. **Jobs run in remedik's own namespace, always.** A namespace parameter
   would mean `create` on jobs cluster-wide — the permission to start a
   container anywhere, held permanently, so that a strategy can occasionally
   start one somewhere. A Job that must act elsewhere does so through the
   Kubernetes API using its ServiceAccount, which is where that authority
   belongs and where somebody granted it deliberately.

3. **The Job's ServiceAccount is named by the step, defaults to `default`,
   and may never be remedik's.** Defaulting to remedik's own would mean a
   strategy author inheriting every permission the operator holds by
   omission; `default` has no permissions, so the failure mode of forgetting
   is a Job that cannot do anything rather than one that can do everything.
   Naming remedik's own is refused explicitly, with a message that says why.

4. **The command is a JSON array, not a string.** A string needs quoting
   rules, and quoting rules invented for a YAML field are how a remediation
   ends up running something nobody wrote. `["/bin/sh","-c","…"]` is
   explicit about the shell being asked for.

5. **`script.run` reads its ConfigMap from remedik's namespace only.**
   Reading one from the namespace an alert names would mean anyone with
   write access to any namespace could get arbitrary code executed by the
   operator: a privilege escalation dressed up as a feature. The same
   reasoning applies to `webhook.call` and its Secret — a label must not
   decide which credential remedik hands out.

6. **Execute creates the Job; Verify waits for it.** A script can run for
   minutes and executions are serialised, so waiting inside Execute would
   hold the reconcile worker for as long as somebody's script felt like.
   Verification is already bounded by the step's `verifyTimeout`, so
   reusing it means the strategy states how long it is willing to wait, in
   the same field it uses for everything else.

7. **A non-2xx webhook response fails the step.** A pipeline that answered
   500 did not run, and a `Succeeded` record beside it would be a lie the
   audit trail tells for ever.

8. **Alert labels reach the container as `REMEDIK_ALERT_<LABEL>`.** The
   prefix exists so a label called `PATH` cannot replace the container's,
   which is the sort of thing that happens once and is never diagnosed. The
   labels also arrive whole as JSON, for a script that would rather parse
   them than guess at the naming.

## Risks / Trade-offs

- **`job.run` is the widest action in the catalogue.** It runs code somebody
  else wrote, with an identity somebody else granted. That is the point of
  an escape hatch, and the bounds above are what make it a hatch rather than
  a hole. It is off by default, and its documentation says plainly what
  enabling it means.
- **Logs on the record.** Only the tail, capped, because a Remediation lives
  in etcd and etcd is not a log store. The Job is kept for an hour so the
  rest is one `kubectl logs` away.
- **A second Kubernetes client.** Pod logs are a subresource the
  controller-runtime client does not model, so the Job actions hold a
  clientset for that alone. Worth it: the last thing a script printed before
  it stopped is most of what makes this better than starting a Job by hand.

## Open Questions

None blocking. Whether a Job should be able to run in the target's namespace
is worth revisiting if somebody has a case the ServiceAccount route cannot
serve — but it would need `create` on jobs cluster-wide, and that is a
different conversation about a different permission.
