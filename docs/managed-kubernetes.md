# Running remedik on EKS, GKE and AKS

Short version: it installs the same way, and it needs no cloud credentials. The
differences are about *alerts* and *nodes*, not about the operator.

This page separates what is structurally true from what has not been tested on a
real managed cluster, because the difference matters and a page that blurs it is
worse than no page.

---

## What is the same everywhere

remedik talks to **one** thing: your cluster's API server.

- **No cloud API calls, so no cloud identity.** No IRSA, no GKE Workload
  Identity, no AKS managed identity, no access keys. This is structural rather
  than a configuration choice: the only outbound connection in the binary is to
  the API server, which is why the chart's NetworkPolicy is ingress-only.
- **No node access, no privileged pods, no host mounts.** The image is
  distroless, non-root and read-only, and remediation runs through the API.
- **RBAC is generated from the actions you enable**, so what remedik can do on a
  managed cluster is the same list as anywhere else, and `helm template` with
  your values is the complete answer.
- **The gateway is a ClusterIP Service**, reached by whatever Alertmanager you
  already run. Nothing needs a load balancer or a public address.

So "does it work on managed Kubernetes" is not really a question about the cloud
— it is a question about whether you have Prometheus and Alertmanager in the
cluster, which is the same question as anywhere.

## What differs: the alerts

Managed offerings each ship their own rule sets, and the alert *names* are what
your strategies match on.

| | |
| --- | --- |
| **Amazon EKS** | Amazon Managed Service for Prometheus, or your own kube-prometheus-stack. Alertmanager can be AMP's or your own; remedik only needs it to POST to the gateway |
| **Google GKE** | Google Cloud Managed Service for Prometheus, with its own managed rule packages, or your own stack |
| **Azure AKS** | Azure Monitor managed Prometheus with its own recommended alert rules, or your own stack |

Two practical consequences:

1. **Check the label names, not just the alert names.** A managed scrape often
   relabels the *scraped* namespace onto `exported_namespace` and uses
   `namespace` for the scrape's own. A strategy matching `namespace: payments`
   then matches nothing, and `--set logLevel=debug` says exactly that — see
   [troubleshooting](troubleshooting.md#2-did-a-strategy-match).
2. **If the rules you need are not there, the chart ships them.**
   `workloadAlerts.enabled=true` adds crash-loop and OOM rules built for
   remediation: they resolve the *workload* from `kube_pod_owner` rather than
   naming a pod, because a pod name is gone by the time anybody acts on it.

Alerts about the control plane — API server latency, etcd, scheduler — mostly do
not exist on a managed cluster, because the control plane is not yours. Strategies
written against them belong to self-managed clusters.

## What differs: nodes

`node.cordon`, `node.uncordon` and `node.drain` work through the API, so they
work on a managed node group. What they do *not* do is replace the machine:

- A drained node in an EKS managed node group, a GKE node pool or an AKS
  agent pool stays cordoned and empty until something removes the instance. The
  node group's own health checks or the cluster autoscaler may do that, on their
  own schedule, or may not.
- remedik deliberately does not terminate instances. `node.replace` is
  [planned as a cloud pack](advanced-setup.md#cloud-packs-i-dont-have-a-pipeline)
  and is not shipped, because terminating a VM needs cloud credentials — which is
  the one thing this operator does not have today, and not by accident.
- If you already have a pipeline that can replace a node, `webhook.call` in
  `onFailure.steps` reaches it, and the strategy YAML is the same either way.

So a useful shape on managed Kubernetes is: **cordon automatically, drain with
approval, and escalate the replacement to whatever owns your nodes.**
[examples/strategies/ask-before-draining.yaml](../examples/strategies/ask-before-draining.yaml)
is that strategy.

## Not tested, and said so

The list above follows from how remedik is built, and the parts that would break
are the parts a cluster decides rather than the operator:

- **GKE Autopilot.** remedik's own pod fits Autopilot's constraints on paper
  (non-root, read-only root filesystem, no host access, resource requests set),
  but `job.run` and `script.run` create pods from *your* image with *your*
  ServiceAccount, and Autopilot will apply its own rules to those. Untested.
- **Pod Security admission** in `restricted` mode: the operator's pod satisfies
  it; a Job you supply is your manifest's problem.
- **Private clusters / restricted egress.** Nothing here reaches the internet,
  which makes this easier rather than harder, but the Alertmanager → gateway path
  is one your network policy has to allow.
- **The managed alert rule packages themselves** change independently of this
  project, so their names are worth checking against your own cluster rather than
  against this page.

If you run remedik on one of these, saying what you found is worth more than most
pull requests — it is what turns this section from "not tested" into
documentation.
