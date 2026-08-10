# cloud-connector-shim — how the standalone PEM authenticates to Pixie Cloud

The problem (README §4): Pixie Cloud reaches a cluster via the vizier
**cloud-connector** → cloud **vzconn** mTLS tunnel. `standalone_pem` has no
cloud-connector, so cloud cannot see it. This shim is the minimal cloud-connector
for a single PEM.

## Auth flow (what `registerWithCloud` implements)
1. Operator: `px deploy-key create` → a deploy key; mount at `PL_DEPLOY_KEY_FILE`.
2. Shim dials `PL_CLOUD_ADDR` vzconn over **mTLS** and calls **RegisterVizier**
   (`src/cloud/vzconn/vzconnpb`), passing the deploy key as the JWT + a
   `ClusterInfo{ClusterName,…}`.
3. Cloud assigns a **cluster ID** + issues **SSL certs**; the shim persists the ID
   to `PL_CLUSTER_ID_FILE` and keeps the NATS-bridge tunnel open (heartbeats).
   Mirror: `src/vizier/services/cloud_connector/bridge/vzconn_client.go`.
4. Pixie Cloud now lists the cluster; PxL from the UI is tunneled down.

## Query proxy (what `serveProxy` implements)
A cloud-tunneled `ExecuteScript` is run against the local `standalone_pem`
(`PEM_ADDR`, :12345) via `px.dev/pixie/src/api/go/pxapi` (same client dx's
`pxdirect` uses) and the result RowBatches are streamed back up the tunnel. One
agent → no distributed plan / Kelvin needed.

## Build / run
```
go build -o dx-cloud-connector-shim .
PL_DEPLOY_KEY_FILE=/etc/dx/deploy.key PL_CLOUD_ADDR=withpixie.ai:443 \
  PEM_ADDR=127.0.0.1:12345 ./dx-cloud-connector-shim
```

## Status
This is a compiling **skeleton**: config + control loop are wired; the two
integration points (vzconn RegisterVizier, ExecuteScript proxy) are marked
against the real pixie packages and are the follow-up — they can only be
validated against a live Pixie Cloud + the vzconn cert chain (rig-gated). The
metadata caveat (README §4-meta) applies: PxL `upid_to_pod_name` returns empty
off-k8s; the UI shows process/cgroup identity unless a static host→service map is
supplied.
