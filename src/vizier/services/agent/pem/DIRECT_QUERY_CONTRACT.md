# PEM direct-query gRPC endpoint — contract (entlein/dx#29)

**Status:** stub PR. dx-agent owns this contract + the dx-side integration; the
**pem-agent** (on a bazel-capable build VM) implements the C++ to make the TDD
targets in `direct_query_server_test.cc` pass.

## Why

Today dx reads evidence by querying the in-cluster **vizier-query-broker** directly
(entlein/dx#28, live). That works and is the MVP path. #29 adds the *durable
per-node* path: make the **normal `vizier-pem`** itself serve `ExecuteScript`
directly over gRPC, so dx on each node can query its node-local PEM with no broker
hop and no cloud dependency.

This is the capability the **experimental `standalone_pem`** already proved
(`src/experimental/standalone_pem/vizier_server.h` — `px::vizier::agent::VizierServer`
implementing `api::vizierpb::VizierService::ExecuteScript` against a local Carnot).
The two differences for the real PEM:

1. **Metadata-connected.** The normal PEM has the metadata service, so per-pod PxL
   filters (`df[df.ctx['pod'] == ...]`) resolve — the gap that made standalone_pem
   return empty per-pod results (old #15). Reuse the PEM's existing Carnot +
   table_store + metadata state; do **not** stand up a second Carnot.
2. **Authenticated.** standalone_pem was insecure (`WithDirectCredsInsecure`). The
   real PEM is in `pl` and must require a **valid cluster service JWT** (the same
   `jwt-signing-key` dx already mints with — see entlein/dx `cmd/dx-daemon/pxbroker.go`).

## The endpoint

- Service: `px.api.vizierpb.VizierService` (the generated gRPC service).
- Method implemented: **`ExecuteScript`** (server-streaming). Mutations/tracepoints
  are **out of scope** for #29 — return `UNIMPLEMENTED` for `req.mutation()==true`.
  (standalone_pem handles mutations; the dx read path never mutates.)
- Transport: gRPC over TLS. TLS may use the in-cluster self-signed CA (dx sets
  `PX_DISABLE_TLS=1` to skip-verify, matching the broker path).

## Config (flags / env) — gated OFF by default

| flag / env                          | default | meaning                                            |
|-------------------------------------|---------|----------------------------------------------------|
| `--direct_query_enabled` / `PL_PEM_DIRECT_QUERY_ENABLED` | `true` (fork) | master switch; when false the port is never opened |
| `--direct_query_port` / `PL_PEM_DIRECT_QUERY_PORT`       | `50305` | gRPC listen port for the direct-query service      |
| `--direct_query_jwt_signing_key` / `PL_JWT_SIGNING_KEY`  | `""`    | HMAC key the bearer JWT must verify against         |

Default-**on** in this k8sstormcenter fork: dx queries the node-local direct-query
server (`DX_BENCH=pemdirect`, :50305) and the vizier deploy templates do not reliably
set `PL_PEM_DIRECT_QUERY_ENABLED` (the `customPEMFlags` Helm path is often left unset),
so a `false` default silently leaves :50305 unbound and dx's pemdirect gets
`connection refused`. Opt out at runtime with `PL_PEM_DIRECT_QUERY_ENABLED=false`, or
compile it out with `--//src/vizier/services/agent/pem:direct_query=disabled`.

## Auth contract

Every `ExecuteScript` call MUST present `authorization: Bearer <jwt>` metadata.
The JWT is verified with `PL_JWT_SIGNING_KEY` and must:
- have a valid signature (HS256) against the signing key,
- be unexpired (`exp` in the future),
- carry a service/audience claim acceptable to vizier (dx mints
  `GenerateJWTForService("dx", "vizier")` — see `src/shared/services/utils`).

Missing/invalid/expired token → `grpc::StatusCode::UNAUTHENTICATED`. No token must
ever fall through to query execution.

## Behavioral contract (the executable spec → `direct_query_server_test.cc`)

1. **flag-off → no listener.** With `direct_query_enabled=false`, nothing listens on
   the port; the PEM starts exactly as today.
2. **flag-on → serves ExecuteScript.** With it enabled + a signing key set, a gRPC
   client with a valid bearer JWT gets a streamed response (status OK) for a trivial
   PxL (e.g. `import px; px.display(px.DataFrame('http_events'))`).
3. **auth required.** Same call with (a) no token, (b) a token signed by the wrong
   key, (c) an expired token → each `UNAUTHENTICATED`, no rows.
4. **metadata-connected filter.** A PxL with a per-pod filter returns only that pod's
   rows (proves the metadata gap #15 is closed on the real PEM). May be an
   integration test tagged `requires_metadata` if a unit Carnot fixture can't supply
   pod context.
5. **mutation rejected.** `req.mutation()==true` → `UNIMPLEMENTED` (scope guard).
6. **no regression.** The existing PEM agent registration / Carnot / Stirling path is
   unchanged when the flag is off (assert via the existing PEM smoke/unit tests).

## dx-side integration (owned by dx-agent — informational)

dx already has the client: `cmd/dx-daemon/pxbroker.go`. Pointing it at a per-PEM
addr is a one-line switch — add `DX_BENCH=pemdirect` selecting
`DX_PEM_DIRECT_ADDR=<HOST_IP>:50305` (HOST_IP via downward API), reusing the exact
JWT mint + `WithBearerAuth` + `WithDisableTLSVerification` path proven against the
broker. dx-agent adds this once #29's endpoint is live; no PEM-side work needed for it.

## Done =

`direct_query_server_test.cc` green under `bazel test`, PEM image builds via the
vizier-release workflow, and a live PG shows dx (`DX_BENCH=pemdirect`) ruling in the
poc e2e off the node-local PEM with the same verdict it gets via the broker.
