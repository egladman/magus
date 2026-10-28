---
title: Run the daemon on Kubernetes for development
description: A kustomize base that runs the magus daemon under the restricted pod security profile, a dev overlay, kubeconform validation against vendored schemas, and a figure read from the same YAML.
tags: [kubernetes, kustomize, kubeconform, daemon, figure, security]
---

# Run the daemon in a development cluster

`deploy/kustomize/base` runs `magus server start --foreground` as one pod. It is
for development: the daemon serves its console and MCP on loopback, and you reach
it with `kubectl port-forward`. `deploy/kustomize/overlays/dev` puts it in the
`magus-dev` namespace.

```sh
kubectl apply -k deploy/kustomize/overlays/dev
kubectl -n magus-dev port-forward svc/magus 7391
```

The figure below is drawn from the overlay's YAML on every docs build, so it cannot
describe a manifest that no longer exists.

<!--diagram:kustomize-architecture-->

## What the base refuses

Each default is the strict one. Loosen it in an overlay, where the change is visible.

| Object                | Setting                                                                                                                                         |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Namespace (overlay)   | Pod Security Admission enforces, audits and warns at `restricted`, pinned to v1.35                                                              |
| Pod                   | `runAsNonRoot`, uid and gid 65532, seccomp `RuntimeDefault`, no host namespaces, no service links                                               |
| Container             | read-only root filesystem, `allowPrivilegeEscalation: false`, every capability dropped, requests and limits for CPU, memory and scratch storage |
| ServiceAccount        | no RBAC binding, `automountServiceAccountToken: false` on the account and the pod                                                               |
| PersistentVolumeClaim | `ReadWriteOnce` for XDG state and data; the Deployment's `Recreate` strategy keeps one writer                                                   |
| emptyDir volumes      | cache, `/tmp` and an in-memory runtime dir, each with a `sizeLimit`                                                                             |
| NetworkPolicy         | `default-deny` shuts ingress and egress; `allow-dns` opens cluster DNS; the overlay's `allow-https` opens 443 except the metadata endpoint      |
| Probes                | exec `magus server status`: a kubelet HTTP probe cannot reach a loopback listener                                                               |

The listener stays on `127.0.0.1:7391` because it serves bearer tokens over
plaintext HTTP, and magus refuses a non-loopback bind without `insecure_bind`.
`kubectl port-forward` connects inside the pod's network namespace, so it reaches
loopback; nothing else in the cluster does. The Service exists to give
port-forward a stable name.

## Validation

`magus run lint libs/kustomize` builds the base and the overlay and validates
each against the Kubernetes v1.35.0 JSON schemas in `libs/kustomize/schemas/`,
with `kubeconform -strict`, so an unknown field fails. The schemas are vendored,
so validation runs offline. kubeconform is pinned in `mise.toml`.

`magus run test libs/kustomize` needs `lint`, so the figure is never laid out from
a manifest kubeconform rejects.

## How the figure reads the YAML

`libs/kustomize/kustomize.buzz` builds a kustomization in Buzz, without the
kustomize binary, in the order `kustomize build` applies its transformers. It reads
`resources` (recursively), `namespace`, `namePrefix`, `nameSuffix`, `labels`,
`commonLabels`, `commonAnnotations`, the ConfigMap and Secret generators,
`images`, and JSON6902 patches. A field it does not read raises, naming it.

A strategic-merge patch is applied only when it touches nothing the figure draws:
names, selectors, pod labels, volumes, env references, the service account, Service
ports, or a NetworkPolicy's spec. Its list semantics are kustomize's own, so the
reader refuses the patch, naming it, rather than draw a guess. Write such a change
as a JSON6902 patch.

A generated ConfigMap or Secret is drawn under its plain name. `kustomize build`
appends a content hash, which would move the figure on every edit to the data.

Each object is a `figure\external` actor, joined by `flowAcross` edges:

- a Service to each workload its selector picks;
- a workload to its ServiceAccount, claims, ConfigMaps and Secrets;
- each NetworkPolicy allow rule between a workload and its peer, labeled with the
  ports;
- a policy type with no rules as the policy denying the workloads it selects.

An object a pod names that the build does not hold, such as a Secret created out of
band, is drawn as named but not built. No directory holds any of it, so the figure
is `.unscoped(why:)`.

To draw another kustomization, call `kustomize\figureOf(kustomize\build(dir), id:,
title:)` from a figure file and register it in `docs/site/diagrams/all.buzz`. See
[Writing diagrams](diagrams.md).
