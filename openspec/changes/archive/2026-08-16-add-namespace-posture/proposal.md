## Why

`dryRun` is one flag on the operator, so a cluster is either all simulation
or all action. That is not how anybody adopts a tool that holds write
access. The real shape is: live in `staging`, reporting only in `prod`,
until the reports have earned the change — and today that needs two
installs.

It is also the difference between a trial that ends and one that never
does. An operator who cannot turn remediation on for one namespace turns it
on for none, and the dry-run report stays a curiosity.

## What Changes

- **`namespacePosture`** — a map from namespace to `live` or `dryRun`,
  overriding the cluster-wide default for the namespace the remediation
  targets.
- The posture is resolved **once, when the record is created**, from the
  target's namespace, and recorded on it. This is the rule `steps` and
  `retries` already follow: an execution keeps the behaviour it started
  with, and the record explains itself without the reader having to know
  what the chart said at the time.
- A target with no namespace — a node, a webhook — uses the default. Stated
  rather than left to be discovered, and safe by construction because the
  default ships as dry-run.
- The dashboard stops claiming one posture for the whole cluster. It says
  what the default is and which namespaces differ, and every record already
  carries the posture it ran under.
- `remedik_namespace_posture{namespace,posture}` joins `remedik_dry_run`,
  so the answer to "what is this cluster actually allowed to do?" is one
  query rather than an inspection of the values file.

## Non-goals

- **A per-namespace kill switch.** Kubernetes already has two better ones:
  `kubectl scale deploy/remedik --replicas=0`, and `enabled: false` on a
  strategy. Both are instant and neither needs a chart upgrade, which is
  what matters at the moment somebody wants everything to stop.
- **Letting a namespace choose its own posture** through a label. Posture is
  a cluster-operator decision. A namespace that can promote itself to live
  is a namespace that can grant itself remediation the cluster operator
  never reviewed — and the RBAC that makes it possible was granted for a
  different reason. See the design.
- **Per-namespace RBAC.** The chart's permissions stay cluster-wide;
  posture decides whether remedik acts, not whether it could.

## Capabilities

### Modified Capabilities

- `remediation-execution`

## Impact

- `internal/engine`: a `Posture` resolver the sink consults; the reconciler
  stops second-guessing the record.
- `cmd/remedik`: a `--namespace-posture` flag.
- `charts/remedik`: the `namespacePosture` map, and a rendered summary in
  `helm install`'s notes so the posture is visible at the moment it is set.
- `internal/dashboard`: an honest posture badge, and the posture on every
  record.
