# Option B — vizier + Kelvin as systemd (full Pixie Cloud visualization off k8s)

Run the control plane as systemd units so Pixie Cloud sees a normal vizier and
Kelvin gives cross-query exec. Heaviest option; use only if k8s-style topology in
the UI is required. Units are TEMPLATES — the images/binaries + config come from
the pixie build.

Services (start in order): pl-etcd → pl-nats → vizier-metadata →
vizier-query-broker → vizier-kelvin → vizier-cloud-connector, with a real PEM
(not standalone_pem) as the agent so it registers via NATS/metadata.

THE CAVEAT: vizier's metadata service (src/vizier/services/metadata/controllers/
k8smeta) is built to watch the k8s API for the upid->pod/service/namespace map.
Off k8s there are no pods, so you run metadata in a DEGRADED mode backed by a
STATIC host/cgroup->service file (metadata-static.json). PxL topology columns
(pod, service, node) are then synthetic; group-by-pod in the UI shows the static
names, not real k8s objects. This is the price of dropping k8s (see the dx repo
docs/DEPLOYMENT_ALTERNATIVES.md §3.4).

Each unit: Environment=PL_CLOUD_ADDR/PL_DEPLOY_KEY, After= the previous, Restart=
on-failure, MemoryMax set (etcd/nats/metadata are the stateful footprint). Wire
the cloud-connector's vzconn deploy-key registration the same way as the shim
(cloudshim/README.md).
