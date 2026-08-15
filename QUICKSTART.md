# Quickstart

> **Status: pre-alpha.** remedik is not installable yet. Section 1 works
> today (developer setup); section 2 documents the target UX that ships with
> v0.1.0 and is kept in sync with the specs in [`openspec/`](openspec/).

## 1. Developer quickstart (works today)

Prerequisites: Go ≥ 1.24 (older Go ≥ 1.21 auto-downloads the right
toolchain), GNU make, git.

```bash
git clone https://github.com/ratyx/remedik.git
cd remedik
make verify        # gofmt check, go vet, unit tests (race detector)
make build         # produces ./bin/remedik
./bin/remedik --version
./bin/remedik      # serves /healthz and /readyz on :8081
```

Optional, used from v0.1.0 on: [kind](https://kind.sigs.k8s.io/) and
[helm](https://helm.sh/) for the local cluster workflow.

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
