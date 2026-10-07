# The export path: what is defined where

Two fork-only components move query results off a cluster: `adaptive-export` in the `pl`
namespace, and `dx-daemon` in the `honey` namespace. This file is the map, so nothing has
to be remembered twice.

## One installer

```
skaffold deploy -f skaffold/skaffold_export.yaml
```

That is the whole install, on an empty cluster or on top of a running one. It is
idempotent: a second run with no change touches nothing and restarts nothing. A pod is
replaced only when an image pin changes. The file holds two Skaffold configs,
`adaptive-export` and `dx-daemon`, and `dx-daemon` declares `requires: adaptive-export`,
so the order is declared rather than remembered. Select one with `-m <config>`.

## Where each thing lives

| Thing | Defined in |
| --- | --- |
| adaptive-export DaemonSet and Service | `k8s/vizier/adaptive_export/daemonset.yaml` |
| adaptive-export ServiceAccount, Role, ClusterRole and bindings | `k8s/vizier/adaptive_export/rbac.yaml` |
| adaptive-export image pin | `images:` in `k8s/vizier/adaptive_export/kustomization.yaml` |
| dx-daemon DaemonSet | `k8s/vizier/dx/dx-daemon.yaml` |
| dx-daemon image pin | `images:` in `k8s/vizier/dx/kustomization.yaml` |
| every secret and ConfigMap fix-up | the `before` hooks in `skaffold/skaffold_export.yaml` |

Each component is self-contained. The AE kustomization does not reach into `../bootstrap`,
which is what used to render the same two files twice under two different label sets and
leave `adaptive-export-control` with no endpoints.

## Not part of a vizier release

The release bundle is built by the `kustomize_build` targets in `k8s/vizier/BUILD.bazel`,
which glob `base/`, `bootstrap/`, `pem/` and the two metadata overlays. Neither
`adaptive_export/` nor `dx/` is in any of those globs, so a vizier release installs
neither, and this path is rolled on its own with the command above.

## Inputs the installer reads

| Variable | Used for | If unset |
| --- | --- | --- |
| `PIXIE_API_KEY`, or `PX_API_KEY` | the `pixie-api-key` in `pl-adaptive-export-secrets` | a live secret is left alone; on a cluster with no secret the install stops |
| `AE_CH_DSN` | the `clickhouse-dsn` in the same secret | the in-cluster forensic database default |
| `AE_PULL_CONFIG`, or `DOCKER_CONFIG` | the `adaptive-export-pull` image pull secret | an existing pull secret is reused; otherwise the install stops |
| `DX_PULL_CONFIG`, or `DOCKER_CONFIG` | the `dx-pull` image pull secret | a default config location is tried, and the install stops if nothing there is readable |
| `DX_CH_HTTP_URL` | dx's database endpoint | the in-cluster default |

Secrets are only ever seeded, never overwritten with a placeholder. A key is written only
when one is supplied.
