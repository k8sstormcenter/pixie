# dx on pure Linux — standalone PEM-direct (no Kubernetes)

*Showcase artifact (fork-only, excluded from copybara — not for upstream). Runs
the **dx active-diagnosis daemon** against a **standalone Pixie PEM** on a single
Linux VM, with **no Kubernetes**, and shows three ways to keep **Pixie Cloud
visualization** working when the PEM has no vizier middleware.*

The companion effort estimate is `dx: docs/DEPLOYMENT_ALTERNATIVES.md` (Q2). This
directory is the buildable/testable realization of it.

---

## 1. What runs, and what each piece is for

```
┌──────────────────────────────  one Linux VM  ─────────────────────────────┐
│                                                                            │
│  standalone_pem   (src/experimental/standalone_pem, systemd, privileged)   │
│    eBPF capture (Stirling) + in-memory table store + ExecuteScript gRPC     │
│    on :12345.  NO cloud-connector, NO query-broker, NO metadata service.    │
│        ▲ pxapi ExecuteScript (node-local gRPC)                              │
│        │                                                                    │
│  dx-daemon        (entlein/dx, systemd)                                      │
│    DX_BENCH=pxdirect  PX_DIRECT_ADDR=127.0.0.1:12345                         │
│    S2 receiver :9099  ← referrals (no kubescape on a VM — see §3)            │
│    triage → workup (queries the PEM) → belief verdict → /metrics :9095       │
│                                                                            │
│  [optional] cloud path for visualization — §4:                             │
│    A) cloud-connector-shim  (this dir, systemd)  → Pixie Cloud vzconn        │
│    B) vizier + kelvin as systemd middleware       → Pixie Cloud vzconn        │
└────────────────────────────────────────────────────────────────────────────┘
```

**Detection needs no cloud.** dx queries the PEM directly over `pxdirect`; the
whole diagnostic loop is node-local. Pixie Cloud is only for the **human
visualization / PxL UI**, and §4 is how you keep it.

---

## 2. Quick start (detection path, no cloud)

### 2a. docker-compose (fastest)
```
cd compose && sudo docker compose up -d
# standalone_pem (privileged, hostPID, /sys + host-root) + dx-daemon on host net
```

### 2b. systemd (the "pure Linux" target)
```
sudo cp systemd/*.service /etc/systemd/system/
sudo cp systemd/dx-standalone.env /etc/dx/            # edit paths/addrs
sudo systemctl daemon-reload
sudo systemctl enable --now standalone-pem dx-daemon
journalctl -u dx-daemon -f | grep 'catalog loaded'   # → 12 conditions
```
Prereqs: a kernel with BTF/CO-RE (or matching headers) for the PEM's eBPF probes;
root/`CAP_SYS_ADMIN`+`CAP_BPF`+`CAP_SYS_PTRACE`; host PID namespace. The
`standalone-pem.service` declares exactly these.

### 2c. verify
```
./inject/inject-referral.sh argocd-render     # a synthetic kubescape finding
curl -s localhost:9095/metrics | grep dx_verdicts_total
journalctl -u dx-daemon | grep ruled_in
```

---

## 3. Referrals without kubescape (the "no k8s" input)

On k8s, dx's referrals are kubescape runtime alerts. On a bare VM there is no
kubescape node-agent, so referrals come from one of:

1. **The injector (`inject/inject-referral.sh`)** — POSTs a synthetic kubescape-
   shaped finding to dx's S2 receiver (`:9099/findings`). Used by the tests + the
   showcase; also the shape any real feed must emit.
2. **A referral synthesizer from the PEM stream** (the recommended production
   path, DEPLOYMENT_ALTERNATIVES §4.4): derive R00xx-equivalent referrals from the
   PEM's own eBPF `process_stats`/`conn_stats`/file events + operator-authored
   baselines — one eBPF agent, no kubescape. Sketched in §6; not built here.

dx carries enough **event-driven evidence in the referral itself** (spawn marker,
file path, the kernel-evidence markers) that a rich referral produces a verdict
**even before the PEM pull** — which is why the smoke test works without eBPF.

---

## 4. Keeping Pixie Cloud visualization — the cloud-auth problem

**The gap.** Pixie Cloud reaches a cluster through the vizier **cloud-connector**,
which opens an mTLS tunnel to the cloud **vzconn** service
(`src/cloud/vzconn`), registered with a **deploy key** → cluster gets an ID + a
cloud-issued cert; the UI's PxL queries are then tunneled cloud → cloud-connector
→ query-broker → PEMs. `standalone_pem` has **none** of cloud-connector,
query-broker, or vzconn — so **Pixie Cloud cannot see it**. Three ways to fix it,
cheapest first:

### Option A — cloud-connector-shim (recommended for a single VM)
A small Go sidecar (`cloudshim/`, systemd `cloud-connector-shim.service`) that:
1. **Authenticates to cloud** exactly as the real cloud-connector does — dial
   `vzconn` with the **deploy key** over mTLS (`vzconn_client.go` flow:
   `RegisterVizier` → receive cluster ID + SSL certs → maintain the tunnel).
2. **Proxies queries** — a cloud-tunneled `ExecuteScript` is forwarded to the
   local `standalone_pem` on `:12345` and the result batches streamed back up the
   tunnel.

This keeps the PEM binary unchanged and collapses cloud-connector + query-broker
into one shim (single node → no Kelvin fan-out needed). **Cloud shows the cluster
and runs PxL; the dx PoC keeps its own node-local pxdirect path in parallel.**
- *Auth detail:* the deploy key + `PL_CLUSTER_ID` come from cloud
  (`px deploy-key create`); the mTLS handshake is the same brittle step as a
  self-hosted-cloud stand-up (DEPLOYMENT_ALTERNATIVES Q1) — get the DNS/cert chain
  right and it connects.
- *Metadata caveat (below §4-meta) applies:* PxL `upid_to_pod_name` etc. resolve
  to empty; the UI shows process/cgroup identity, not pods.
- Effort: the registration handshake + the ExecuteScript proxy; skeleton +
  precise wiring points are in `cloudshim/` (this dir).

### Option B — run vizier + Kelvin as systemd middleware
Run the real control plane off-k8s as systemd units (`systemd/middleware/`):
`pl-nats`, `pl-etcd`, `vizier-metadata`, `vizier-query-broker`, `vizier-kelvin`,
`vizier-cloud-connector`, with a **real** PEM as the agent (registers via
NATS/metadata). Cloud sees a fully normal vizier; Kelvin gives cross-query exec.
- This is the **fullest** cloud experience and the **heaviest**: vizier's metadata
  service is built to watch the **k8s API** for the upid→pod map
  (`metadata/controllers/k8smeta`). Off k8s you run it in a **degraded metadata**
  mode (static host/cgroup map) — topology columns are synthetic. NATS+etcd+4
  services is a real resident footprint on one VM.
- Unit templates + the metadata caveat are in `systemd/middleware/README.md`.

### Option C — no cloud (detection only)
Skip cloud entirely; visualize from dx's own outputs — the `/metrics` scrape
(Prometheus/Grafana), the structured verdict log, and the evidence manifests.
This is §2 and needs nothing extra. Recommended when the VM has no route to a
Pixie Cloud.

### §4-meta — the metadata degradation (applies to A and B)
`upid_to_pod_name` / `upid_to_namespace` / `upid_to_service_name` are computed by
the metadata service watching the **k8s API**; on a VM there are no pods, so they
return empty. dx already tolerates this (it can key on process/cgroup identity —
DEPLOYMENT_ALTERNATIVES §3.2.3). For the **cloud UI**, PxL scripts that group by
`pod`/`service` show blank; a VM-native PxL uses `upid`, `cmdline`, cgroup path.
A static host→service map (a config file the shim/metadata reads) restores
readable names at lower fidelity.

**Recommendation:** Option A for a single showcase VM (cloud viz + minimal
footprint), Option C when there is no cloud, Option B only if full k8s-style
topology in the UI is a hard requirement.

---

## 5. Tests & NFRs (what runs here vs. on a real kernel)

- `test/smoke.sh` — brings up dx (PEM optional), injects a known attack referral,
  asserts a malignant verdict + the exported metrics. Runs on **any** Linux
  (no eBPF needed — event-driven referral evidence, §3).
- `test/nfr.sh` — the single-VM NFR harness: drives N referrals through dx and
  reports p50/p95 **time-to-verdict** (`dx_time_to_verdict_seconds`), throughput,
  backpressure drops, and **RSS/CPU** (`process_resident_memory_bytes`,
  `process_cpu_seconds_total`) — the pure-Linux equivalent of the k8s report G1/G2.
- The **PEM eBPF path** (real `conn_stats`/`http_events` pulls via pxdirect) needs
  a privileged PEM on a CO-RE kernel; the harness exercises it when a PEM is
  present and degrades to referral-only otherwise (never a silent pass).

### 5a. Validated on a CO-RE VM (2026-07-04, kernel 6.8, BTF present)
Ran the **real** `standalone_pem` image + dx on this host, no k8s/cloud:
- PEM: Stirling attached `socket_tracer` and registered `conn_stats`,
  `http_events`, `dns_events` (+ mysql/redis/mongo/amqp/tls) — **only with
  `/sys` mounted read-write** (read-only blocks kprobe attach:
  `probe cleanup failed: /sys/kernel/debug/tracing/kprobe_events`). The compose +
  `standalone-pem.service` mount `/sys:rw` for this reason.
- dx `pxdirect` executed PxL against the PEM: `dx_bench_errors_total 0`,
  `dx_bench_unavailable 0`, tables resolve (no "Table not found"), a triage pull
  returned in ~12 ms (`"dur_ms":12,"err":false`). Referrals admitted, verdicts
  produced (`generic=MALIGNANT`).
- **The off-k8s caveat, observed directly:** a referral scoped to a synthetic pod
  (`namespace=poc`) pulls **0 rows** because `upid_to_namespace` is empty off-k8s
  and the `namespace=='poc'` filter matches nothing — exactly §4-meta. On a VM,
  omit the pod scope (empty `RuntimeK8sDetails`) so dx queries the PEM unscoped, or
  supply a static metadata map. This is a real behaviour, not a bug.

See `test/README.md` for exact invocations + pass criteria.

---

## 6. Not built here (follow-ups)
- The PEM→referral **synthesizer** (§3.2) — one eBPF agent replacing kubescape.
- A **static metadata** provider (host/cgroup→service) for readable cloud names.
- Full `cloudshim` ExecuteScript proxy hardening + the vzconn cert rotation.

Everything in this directory is **fork-only** and excluded from copybara
(`tools/private/copybara/copy.bara.sky`) — it packages an experimental,
non-upstream deployment mode of the PoC.
