# PEM direct-query — signing-key security contract (entlein/dx#29)

This document is the **authoritative spec for how the JWT signing key flows
through the direct-query endpoint**, what it protects, what it does NOT
protect, the threat model, and the unit-test coverage that locks the contract
down. Pair with [DIRECT_QUERY_CONTRACT.md](DIRECT_QUERY_CONTRACT.md) which
covers the gRPC/PxL surface; this doc covers crypto + key handling only.

## TL;DR

- One HMAC-SHA256 key, source `secret/pl-cluster-secrets/jwt-signing-key`,
  shared by `kelvin`, `query-broker`, `metadata-server`, and (now) the PEM's
  direct-query verifier.
- Mounted into the PEM via `PL_JWT_SIGNING_KEY` env (the existing
  `k8s/vizier/pem/base/pem_daemonset.yaml` `secretKeyRef`).
- Used in both directions: the agent's outgoing service-token mint
  (`manager.cc:GenerateServiceToken`) AND direct-query's incoming
  `verifyHs256Jwt` (`direct_query_server.cc:133`).
- **Compromise of the key = compromise of every in-cluster service-to-service
  auth path.** Direct-query is no more or less protected than kelvin or
  query-broker.

## Key flow

```
                            pl-cluster-secrets/jwt-signing-key
                                          │
       ┌──────────────────────┬───────────┼─────────────┬───────────────────┐
       ▼                      ▼           ▼             ▼                   ▼
   kelvin                query-broker  metadata    vizier-pem            (any future
   (env PL_JWT_…)       (env PL_JWT_…)  (env …)   (env PL_JWT_…)         in-cluster
                                                    │                     consumer)
                                                    │
                              ┌─────────────────────┴─────────────────────┐
                              ▼                                           ▼
                  GenerateServiceToken                    direct-query verifier
                  (outgoing mint to                       (incoming token check
                   kelvin/MDS, manager.cc:434)            from dx_daemon,
                                                          direct_query_server.cc:133)
```

- **Single source of truth:** the `jwt-signing-key` data field in the
  `pl-cluster-secrets` Secret. RBAC restricts read access to the four
  service-account identities above.
- **Two pixie flag pegs reading the same env:**
  - `FLAGS_jwt_signing_key` — DEFINEd in
    `src/vizier/services/agent/shared/manager/manager.cc:60`, owns outgoing
    mint.
  - `FLAGS_direct_query_jwt_signing_key` — DEFINEd in
    `src/vizier/services/agent/pem/pem_manager.cc:39`, owns direct-query
    verification.
- **Fallback:** when `FLAGS_direct_query_jwt_signing_key` is empty,
  `MaybeStartDirectQueryServer` falls back to `FLAGS_jwt_signing_key`. This
  avoids the split-brain where a CLI override of only one flag silently
  disables direct-query auth (CodeRabbit catch on PR #49).
- **Empty-key guard at Init:** `Manager::Init`
  (`shared/manager/manager.cc:140`) refuses to start if
  `FLAGS_jwt_signing_key` is empty. Without this guard, `GenerateServiceToken`
  would throw an uncaught `jwt::SigningError("key not provided")` from
  cpp_jwt's `obj.signature()` on the first outgoing call — observed in the
  field on the stock fork 0.14.17 PEM as a 23-restart CrashLoopBackOff.

## Threat model

### What the signing key protects

| Threat | Mitigation |
|---|---|
| **Unauthenticated direct-query call.** Anyone with network reach to `:50305` opens an ExecuteScript stream. | Bearer JWT required. No token → `UNAUTHENTICATED` at `AuthenticateRequest`. |
| **Wrong-key token (different cluster, stale leak from a rotated secret).** | HMAC verification compares against `effective_signing_key`; mismatch → `UNAUTHENTICATED`. |
| **Expired token replay.** | `exp` claim required; verifier rejects `now ≥ exp_secs`. |
| **alg:none forgery (RFC 8725 §3.1).** Attacker submits unsigned token with `"alg":"none"`. | Verifier requires `alg == "HS256"` in the header; any other value → `UNAUTHENTICATED`. |
| **Wrong-audience token (e.g. one meant for a different service).** | `aud` claim required; verifier rejects anything but `"vizier"` (string) or array-containing `"vizier"`. |
| **Tampered signature / payload / header.** | HMAC-SHA256 verifies the `header.payload` signing input against the supplied signature. Any byte flip in any segment → `UNAUTHENTICATED`. |
| **Token under non-Bearer scheme** (e.g. `Token <jwt>`, `Basic …`). | `stripBearerPrefix` requires case-insensitive `"bearer "`; anything else → token treated as empty → `UNAUTHENTICATED`. |

### What it does NOT protect

| Out of scope | Rationale |
|---|---|
| **Key compromise.** If `pl-cluster-secrets/jwt-signing-key` leaks, an attacker can mint valid tokens for kelvin / query-broker / direct-query at will. | Same threat model as the entire in-cluster service mesh. Out-of-band protections (RBAC on the Secret, sealed-secrets/SOPS at rest, key rotation) are the appropriate controls. |
| **Token replay within the validity window.** A captured valid token is replayable until `exp`. | The `jti` claim is minted by `GenerateServiceToken` but the direct-query verifier does NOT track it (no nonce store). Defensive choice: tokens are short-lived (60s in `GenerateServiceToken`) and in-cluster traffic is TLS-encapsulated, so capture surface is small. If/when we need anti-replay, add a sliding `jti` LRU. |
| **Confidentiality of the PxL query body.** The JWT only authenticates the caller; the gRPC channel itself carries the script. | Channel TLS is the PixieTLS in `k8s/vizier/base/*` manifests; direct-query inherits it via the `pem_daemonset.yaml` cert mounts (or `InsecureServerCredentials` when explicitly disabled for soak/dev — never in production). |
| **PEM-level authorization (who can run what PxL).** Any valid token can run any read-only PxL. | Mutations are rejected at the scope guard (`req.mutation() == true → UNIMPLEMENTED`). For read-only queries, the contract is "anyone the cluster trusts to mint a JWT can read PEM data" — same as kelvin's contract. |
| **Cross-tenant isolation in a multi-cluster cloud.** | Out of scope: this is a per-cluster service-to-service token; cross-cluster auth is the cloud's job. |
| **Network-level access control to `:50305`.** | `NetworkPolicy` in the manifest is the right place (out-of-scope for this PR; tracked as a future hardening). |

### Tampering scenarios — unit-test coverage

The cases below are explicit tests in
`src/vizier/services/agent/pem/direct_query_server_test.cc`. Each guards a
specific tampering surface; together they lock the verifier's behaviour
against drift.

| Scenario | Test name | Assertion |
|---|---|---|
| No `authorization` header | `NoToken_Unauthenticated` | `UNAUTHENTICATED` |
| Bearer header with empty token | `BearerEmptyToken_Unauthenticated` | `UNAUTHENTICATED` |
| Lowercase `bearer ` scheme | `ValidToken_LowercaseBearerPrefix_Authenticated` | not `UNAUTHENTICATED` |
| Wrong scheme (`Token <jwt>`) | `WrongAuthScheme_Unauthenticated` | `UNAUTHENTICATED` |
| Garbage non-JWT string | `GarbageBearer_Unauthenticated` | `UNAUTHENTICATED` |
| `alg:none` forgery (RFC 8725) | `AlgNoneToken_Unauthenticated` | `UNAUTHENTICATED` |
| Wrong signing key | `WrongKey_Unauthenticated` | `UNAUTHENTICATED` |
| Expired token | `ExpiredToken_Unauthenticated` | `UNAUTHENTICATED` |
| Missing `aud` claim | `MissingAud_Unauthenticated` | `UNAUTHENTICATED` |
| Wrong `aud` value | `WrongAud_Unauthenticated` | `UNAUTHENTICATED` |
| `aud` as a string (RFC 7519 backwards-compat) | `ValidToken_AudAsString_Authenticated` | not `UNAUTHENTICATED` |
| Missing `exp` claim | `MissingExp_Unauthenticated` | `UNAUTHENTICATED` |
| Single-byte signature flip | `TamperedSignatureByte_Unauthenticated` | `UNAUTHENTICATED` |
| Single-byte payload flip (signature now mismatches) | `TamperedPayloadByte_Unauthenticated` | `UNAUTHENTICATED` |
| Single-byte header flip (signature now mismatches) | `TamperedHeaderByte_Unauthenticated` | `UNAUTHENTICATED` |
| Truncated token (last 10 chars removed) | `TruncatedToken_Unauthenticated` | `UNAUTHENTICATED` |
| Two tokens concatenated (`tok1.tok2`) | `ConcatenatedTokens_Unauthenticated` | `UNAUTHENTICATED` |
| `alg:HS384` confusion (HS256 sig under HS384 header) | `AlgConfusion_HS384_Unauthenticated` | `UNAUTHENTICATED` |
| Mutation flag set | `ValidToken_Mutation_Unimplemented` | `UNIMPLEMENTED` |

The fixture (`DirectQueryServerTest`) hosts an in-process gRPC server backed
by the real `DirectQueryServer`, so the authentication metadata flow is
end-to-end — gRPC client → metadata API → `AuthenticateRequest` → `verifyHs256Jwt`.
There is no mock layer between the test and the verifier.

## Key rotation

Rotation = update the Secret + restart the consumers (kelvin, query-broker,
metadata-server, vizier-pem). dx_daemon picks up the new key on its next
mint. There is **no overlap window** — both old and new tokens would have to
verify simultaneously, which the current verifier doesn't support (no
multi-key). For a zero-downtime rotation, the contract would need to:

1. Hold two valid keys at once (`PL_JWT_SIGNING_KEY_PREV` + `_NEW`).
2. Mint with `_NEW`; verify against either.
3. After `max(exp_window)`, drop `_PREV`.

Not implemented today; out of scope for #29 (the demo's rotation cadence is
manual). Track as a hardening follow-up if/when key rotation becomes
operational.

## Logging / observability

- The verifier's specific failure reason (e.g. "signature mismatch", "wrong
  audience") is `VLOG(1)`'d in `direct_query_server.cc:243` but **collapsed
  on the wire** to a generic `"direct-query: invalid bearer token"`. Peers
  cannot probe which check failed; operators can flip
  `--v=1` to see the diagnostic in PEM stderr.
- `MaybeStartDirectQueryServer`'s breadcrumbs (`step 1/6 … 6/6 READY`) name
  the exact step on init failure — but they do NOT log the key value. The
  empty-key guard logs `"signing key is empty"`, not the key.
- **Never** include the signing key in PEM stderr, logs, or status messages.
  No `LOG(*) << FLAGS_jwt_signing_key` anywhere; reviewers should reject any
  patch that adds one.

## Cross-references

- `src/vizier/services/agent/pem/direct_query_server.cc:133` —
  `verifyHs256Jwt` implementation.
- `src/vizier/services/agent/pem/pem_manager.cc:39, :115` — flag DEFINE +
  effective-key fallback.
- `src/vizier/services/agent/shared/manager/manager.cc:60, :140, :423` —
  shared flag DEFINE + empty-key guard + outgoing-mint
  `GenerateServiceToken`.
- `k8s/vizier/pem/base/pem_daemonset.yaml:98-102` — `PL_JWT_SIGNING_KEY`
  `secretKeyRef` mount.
- `k8s/vizier/base/{kelvin,query_broker}_deployment.yaml` — sibling consumers
  of the same secret key.
- `src/vizier/services/agent/pem/DIRECT_QUERY_CONTRACT.md` — sibling doc on
  the gRPC/PxL contract.
