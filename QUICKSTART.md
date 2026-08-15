# Quickstart

> **Status: pre-alpha.** remedik is not installable yet. Section 1 works
> today (developer setup); section 2 documents the target UX that ships with
> v0.1.0 and is kept in sync with the specs in [`openspec/`](openspec/).

## 1. Developer quickstart (works today)

Prerequisites: **Go ≥ 1.26** (older toolchains auto-download it), GNU make,
git, **helm v3.21.4** ([install](https://helm.sh/docs/intro/install/)),
**yamllint 1.38.0** (`sudo apt install yamllint`). Go-based lint tools
install themselves, pinned, into `hack/bin/` on first use (`make tools`).

Run `make versions` at any time to see every pinned version next to the
latest upstream release.

```bash
git clone https://github.com/ratyx/remedik.git
cd remedik
make verify        # gofmt, go vet, golangci-lint, yamllint, helm lint, tests
make build         # produces ./bin/remedik
./bin/remedik --version
```

Run it and send it an alert — the gateway is live, the engine is not yet, so
alerts are decoded, validated and logged:

```bash
REMEDIK_GATEWAY_TOKEN=dev-token ./bin/remedik --log-level=debug &

curl -sS -X POST http://localhost:8090/webhooks/alertmanager \
  -H "Authorization: Bearer dev-token" -H "Content-Type: application/json" \
  -d '{"version":"4","alerts":[
        {"status":"firing",
         "labels":{"alertname":"KubePodCrashLooping","namespace":"payments","pod":"api-0"},
         "annotations":{"summary":"crash looping"},
         "startsAt":"2026-08-15T09:00:00Z","fingerprint":"f00d1"}]}'
# -> {"received":1}, and the alert appears in the operator log
```

Health probes live on `:8081` (`/healthz`, `/readyz`). Without a token the
gateway answers 401; a malformed payload gets 400; anything understood gets
200, even when nothing matches — Alertmanager retries non-2xx responses, so
"nothing matched" must not look like a failure.

Other useful targets (`make help` lists everything): `make yaml-fix`
(auto-format YAML), `make helm-docs` (regenerate the chart README from
values annotations), `make lint`.

### Local dev cluster (kind + Prometheus/Alertmanager/Grafana)

Additionally requires: Docker (Docker Desktop with WSL integration, or
docker-ce), **kind v0.32.0**
([install](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)),
[kubectl](https://kubernetes.io/docs/tasks/tools/) (stable). The monitoring
stack is pinned to kube-prometheus-stack 88.3.0.

```bash
make dev-up        # kind cluster + kube-prometheus-stack (monitoring ns)
make dev-info      # how to reach Grafana / Prometheus / Alertmanager UIs
make dev-down      # tear it all down
```

Grafana: `http://localhost:3000` after the printed port-forward
(admin / remedik-dev). remedik itself deploys into this cluster once
`add-mvp-core` ships.

## 2. User quickstart (target UX — v0.1.0)

The steps below are the contract for the first release; this section flips
from "target" to "current" when the `add-mvp-core` change is implemented.

1. Install the chart. **Dry-run is the default** — remedik observes,
   evaluates and reports, but acts on nothing:

   ```bash
   helm repo add remedik https://ratyx.github.io/remedik
   helm install remedik remedik/remedik -n remedik --create-namespace
   ```

2. Point Alertmanager at the gateway — the exact receiver snippet is printed
   by `helm install` (NOTES), roughly:

   ```yaml
   receivers:
     - name: remedik
       webhook_configs:
         - url: http://remedik-gateway.remedik.svc:8090/webhooks/alertmanager
           http_config:
             authorization: {credentials_file: /etc/secrets/remedik-token}
   ```

3. Apply your first strategy from the cookbook (e.g. `pod-crashloop`).

4. Watch what remedik *would* do:

   ```bash
   kubectl get remediations -n remedik
   ```

5. When a week of `Simulated` records looks right, flip `dryRun: false` in
   your values and let it act — guards and audit stay on either way.

Production guidance (modes, guards, hub/spoke, sinks):
[docs/advanced-setup.md](docs/advanced-setup.md).
