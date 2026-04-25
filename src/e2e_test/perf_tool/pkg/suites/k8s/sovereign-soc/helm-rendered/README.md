# Helm-rendered Kubescape + Vector manifests for the sovereign-soc suite

`PrerenderedDeploy` only applies static YAML; it does not invoke helm at
runtime. So the Kubescape and Vector charts used by the Sovereign SOC demo
are pre-rendered once and committed here. The source values files that
went in are also committed so the render is reproducible.

Sources:

- `kubescape-values.yaml` — copied verbatim from
  [`k8sstormcenter/soc@main:tree/kubescape/values.yaml`](https://github.com/k8sstormcenter/soc/blob/main/tree/kubescape/values.yaml).
- `kubescape-default-rules.yaml` — copied verbatim from
  [`k8sstormcenter/soc@main:tree/kubescape/default-rules.yaml`](https://github.com/k8sstormcenter/soc/blob/main/tree/kubescape/default-rules.yaml).
- `vector-values.yaml` — based on
  [`k8sstormcenter/soc@main:tree/vector-lab/values.yaml`](https://github.com/k8sstormcenter/soc/blob/main/tree/vector-lab/values.yaml)
  with the ClickHouse sink `endpoint:` rewritten to the external forensic
  endpoint (`http://clickhouse.forensic.austrianopencloudcommunity.org:8123`)
  so Vector can write to CH from any experiment cluster, not just the
  forensic cluster's in-cluster DNS.

## How to re-render

From inside the dev docker container, with its helm in `$PATH`:

```sh
helm repo add kubescape https://kubescape.github.io/helm-charts/
helm repo add vector    https://helm.vector.dev
helm repo update

# Kubescape operator (pinned to the version used by soc/Makefile).
helm template kubescape kubescape/kubescape-operator \
  --version 1.30.2 \
  --namespace honey --create-namespace \
  --values src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc/helm-rendered/kubescape-values.yaml \
  > src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc/helm-rendered/kubescape.rendered.yaml

# Split the kube-system-namespaced RoleBinding (storage-auth-reader) into
# its own file, because PrerenderedDeploy only tolerates a single namespace
# per step.
python3 - <<'PY'
import yaml, os
base = "src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc/helm-rendered"
with open(f"{base}/kubescape.rendered.yaml") as f:
    docs = list(yaml.safe_load_all(f))
main, ks = [], []
for d in docs:
    if d is None: continue
    ns = (d.get("metadata") or {}).get("namespace")
    (ks if ns == "kube-system" else main).append(d)
with open(f"{base}/kubescape.rendered.yaml", "w") as f:
    yaml.safe_dump_all(main, f, sort_keys=False)
with open(f"{base}/kubescape.rendered.kube-system.yaml", "w") as f:
    yaml.safe_dump_all(ks, f, sort_keys=False)
PY

# Vector (version pinned to whatever's current on the vector repo).
helm template vector vector/vector \
  --namespace honey --create-namespace \
  --values src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc/helm-rendered/vector-values.yaml \
  > src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc/helm-rendered/vector.rendered.yaml
```

## Why the kube-system split

The kubescape-operator chart includes a single `RoleBinding` in
`kube-system` — `storage-auth-reader` — that delegates auth checking to
the kube-apiserver's `extension-apiserver-authentication-reader` Role
(required for the storage APIService aggregation to work; without it the
`ApplicationProfile` CRD can't be read, which means node-agent can't
compare workload behavior against the pre-populated redis profile).

`RoleBinding` objects must reside in the same namespace as the Role they
reference, so we can't rewrite it into `honey`. And
`PrerenderedDeploy.getNamespace()` errors if a single concatenated YAML
touches more than one namespace. We split it into its own step and flag
it `skip_namespace_delete: true` on the proto spec so teardown never
tries to `kubectl delete ns kube-system`.
