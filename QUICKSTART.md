# Quickstart

Two paths: install remedik into a cluster, or work on remedik itself.

---

## Install it

### Prerequisites

A Kubernetes cluster (1.30+), [helm](https://helm.sh/docs/intro/install/),
and Prometheus with Alertmanager. remedik reads alerts from Alertmanager; it
does not scrape anything itself.

### 1. Install the chart

**Dry-run is the default.** remedik will match alerts, evaluate guards and
record what it would have done, changing nothing.

```bash
helm install remedik oci://ghcr.io/ratyx/charts/remedik \
  --namespace remedik --create-namespace \
  --set gateway.auth.token="$(openssl rand -hex 24)"
```

The token is what stops anything else in the cluster from asking remedik to
act. Keep the value: Alertmanager needs it in step 2. In production, manage
the Secret yourself and use `--set gateway.auth.existingSecret=<name>`.

> While no image has been published, build and load one yourself — see
> "Work on it" below, or run `make dev-deploy` against a kind cluster.

### 2. Point Alertmanager at the gateway

`helm install` prints the exact snippet. It looks like this:

```yaml
receivers:
  - name: remedik
    webhook_configs:
      - url: http://remedik-gateway.remedik.svc:8090/webhooks/alertmanager
        http_config:
          authorization:
            type: Bearer
            credentials: <the token from step 1>
```

Then route the alerts you want remediated to that receiver.

### 3. Add a strategy

Start from the cookbook in [`examples/strategies/`](examples/strategies/):

```bash
kubectl apply -f examples/strategies/pod-crashloop.yaml
kubectl get remediationstrategies
```

### 4. Read the dry-run reports

```bash
kubectl get remediations -n remedik
kubectl describe remediation -n remedik <name>
```

Every record will be `Simulated`, carrying the plan it would have run. Give
it a week of real alerts. When the plans look right:

```bash
helm upgrade remedik ... --set dryRun=false
```

Guards and the audit trail work identically either way.

---

## Work on it

### Prerequisites

**Go ≥ 1.26**, GNU make, git, **helm v3.21.4**, **yamllint 1.38.0**
(`sudo apt install yamllint`). Go-based tools (golangci-lint, controller-gen,
yamlfmt, helm-docs) install themselves, pinned, into `hack/bin/` on first
use — or all at once with `make tools`.

For the cluster workflows also: Docker (Docker Desktop with WSL integration,
or docker-ce), **kind v0.32.0**, kubectl.

`make versions` shows every pinned version against the latest upstream
release. `make help` lists every target.

### Build and test

```bash
git clone https://github.com/ratyx/remedik.git
cd remedik
make verify   # gofmt, vet, golangci-lint, yamllint, helm lint, race tests
make build    # ./bin/remedik
```

### End-to-end on kind

```bash
make e2e                  # throwaway cluster, then cleans up
KEEP_CLUSTER=1 make e2e    # keep it to inspect afterwards
```

It asserts the five behaviours that matter: an unauthenticated delivery is
refused, dry-run records a plan without touching anything, turning dry-run
off actually restarts the Deployment, the cooldown refuses an immediate
repeat, and an unmatched alert is accepted and ignored.

### A cluster to play in

```bash
make dev-up       # kind + kube-prometheus-stack (Grafana, Alertmanager)
make dev-deploy   # build, load and install remedik in dry-run
make dev-info     # how to reach the UIs
make dev-down
```

Send it an alert by hand:

```bash
kubectl -n remedik port-forward svc/remedik-gateway 8090:8090 &

curl -sS -X POST http://localhost:8090/webhooks/alertmanager \
  -H "Authorization: Bearer dev-token" -H "Content-Type: application/json" \
  -d '{"version":"4","alerts":[
        {"status":"firing",
         "labels":{"alertname":"KubePodCrashLooping","namespace":"payments","deployment":"api"},
         "startsAt":"2026-08-15T09:00:00Z","fingerprint":"demo-1"}]}'

kubectl -n remedik get remediations
```

Without a token the gateway answers 401; a malformed payload gets 400;
anything understood gets 200, even when nothing matches — Alertmanager
retries non-2xx responses, so "nothing matched" must not look like a
failure.

### Changing the API types

```bash
make generate manifests   # DeepCopy + CRDs, with a pinned controller-gen
```

CI fails if the committed output is stale (`make verify-codegen`), so commit
whatever they change.

### Adding a feature

remedik is developed spec-first; see [CONTRIBUTING.md](CONTRIBUTING.md).
Short version: propose the change in `openspec/`, get the spec approved,
then implement it.
