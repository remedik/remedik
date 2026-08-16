## 1. The contract

- [x] 1.1 `action.Request` carries the target, the parameters, the alert's labels and the record's identity
- [x] 1.2 `Plan`, `Execute` and `Verify` take it; every existing action is updated

## 2. webhook.call

- [x] 2.1 POSTs a stable JSON payload; optional credential from a Secret in remedik's namespace only
- [x] 2.2 A non-2xx response fails the step, with the response body on the record
- [x] 2.3 The credential never reaches the record or the kubectl equivalent

## 3. job.run and script.run

- [x] 3.1 Creates a Job in remedik's namespace under a ServiceAccount the step names; remedik's own is refused
- [x] 3.2 The command is a JSON array; alert labels arrive prefixed, and whole as JSON
- [x] 3.3 Verify waits for the Job, records the exit code and the tail of its output
- [x] 3.4 `script.run` mounts a ConfigMap from remedik's namespace only

## 4. Chart and docs

- [x] 4.1 The RBAC table gains a namespaced section; these are the first actions whose permissions belong in the Role
- [x] 4.2 `actions.webhookCall`, `actions.jobRun`, `actions.scriptRun`, all off by default
- [x] 4.3 Tests, including every refusal
- [x] 4.4 Cookbook entries, architecture, CHANGELOG, chart README
