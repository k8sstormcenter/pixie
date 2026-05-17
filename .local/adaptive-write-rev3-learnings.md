# Adaptive Write rev-3 — Learnings Log

A running log of reasoning, decisions, dead-ends, and surprises while implementing rev-3. Append-only. Each entry timestamped + scoped.

## 2026-05-17 — Why rev-3 exists

User feedback that crystallized the pivot:
- > "what we have is a clusterfuck that accidentally works, not a fix"
- > "we have way too much network traffic going on. Lets redesign the AW by making it stay local on the node as much as possible"

Rev-2 was symptom-masking: three throttle knobs + a loadtest workaround that got the 4× sweep from "0 rows" to "80k rows" without anyone understanding *why* `per_hash=2, global=10` worked while `per_hash=3, global=20` didn't. The pgsql_events case never recovered (~20 rows). The actual design was wrong: an operator-side periodic fan-out of `N_active_hashes × 10_tables` queries every 30s overloads a vizier-query-broker not built for that traffic pattern.

## Design pivot in one sentence

**Stop sending O(active_hashes × tables) queries to the broker. Send O(tables) queries with a whitelist of pods the operator considers active.**

## Pixie-side constraint discovered while sketching

Pixie's `vz.ExecuteScript` is request-response, not long-lived streaming. `rs.Stream()` blocks until the script finishes; the existing retention plugin model re-runs scripts periodically.

**Consequence**: rev-3 isn't a true "long-lived stream"; it's "ONE shared PxL submission per table per refresh interval, with an embedded whitelist." Logically equivalent for our purposes — the operator decides when streaming starts/stops by including/excluding pods from the whitelist; if the whitelist is empty for a table, we don't even submit.

This is still 10–100× less broker pressure than rev-2 because the multiplication-by-active-hashes goes away.

## Decisions (open and closed)

| Decision | Choice | Status |
|---|---|---|
| Whitelist key | `namespace/pod` string (matches `px.upid_to_pod_name` output) | tentative — may need pod UID for recreation safety |
| Re-submit cadence | filter-change-driven, with debounce; periodic re-submit every 30 s as freshness floor | tentative |
| Debounce interval | 1 second | starting value, may tune |
| Whitelist size cap | 500 pods | starting value; beyond → switch to no-filter mode |
| Per-table CH batch | 10 k rows OR 5 s | starting value |
| Per-table goroutine | 1 stream goroutine + 1 writer goroutine = 2 per table = 20 per node | confirmed |
| Backoff on broker error | 1 s → 2 s → 5 s → 10 s (cap) | tentative |
| Feature flag | `ADAPTIVE_WRITE_MODE=streaming` vs `pull` (default pull = rev-2) | confirmed |

## Smallest viable slice (defined here so it's reviewable)

1. `internal/activeset/ActiveSet` — pod-keyed map, version counter, delta chan.
2. `internal/filterupdater/FilterUpdater` — debouncer + size cap on top of ActiveSet.
3. `internal/streaming/TableScanner` — periodic-PxL-per-table with whitelist.
4. `internal/streaming/CHBatchWriter` — bounded buffer + per-table batching.
5. `internal/streaming/Supervisor` — owns N scanners; restarts on errors.
6. Wiring in `main.go` behind the env flag; ATTRIBUTION sink stays as-is.

Slice 1: implement + run streaming mode for **ALL 10 tables** (one-shot, not table-by-table — easier to reason about than mixed mode).

## Validation plan

A/B 4× sweep:
- **rev-2 baseline** (with current throttle knobs applied, the "manifest-defaults" config): ~80–135 k rows total, pgsql ~20.
- **rev-3 streaming**: same workload, expect comparable or better; pgsql should fill since it's no longer starved behind other tables' fan-outs.

Metrics:
- successful pushes per table
- DeadlineExceeded errors per table (rev-3 expectation: zero)
- CH fresh rows per table in last 5 min

## Learnings (appended as work progresses)

### 2026-05-17 — first build + 3 iterations to working PxL

**Slice 1 (`activeset`, `streaming.FilterUpdater`, `streaming.TableScanner`, `streaming.BatchWriter`, `streaming.Supervisor`, env-flag wiring in main.go) built first try.** Total ~600 LOC. All unit tests green on first run.

**Iteration loop on the deployed binary uncovered three PxL surprises I'd not have caught in tests:**

1. **`or` between Series comparisons is rejected.** First PxL emitted `df = df[(df.pod == 'a') or (df.pod == 'b')]`. Compilation error: `Expected two arguments to 'or'`. PxL parses Python's short-circuit `or` differently from element-wise truth-tests on Series.

2. **`|` is also rejected.** Switched to `(df.pod == 'a') | (df.pod == 'b')` (pandas idiom). New error: `Operator '|' not handled`. PxL has no element-wise bitwise OR on Series.

3. **`px.contains` is substring, not regex.** Tried `px.contains(df.pod, '^(p1|p2|...)$')` → script compiled but matched zero rows (substring search for the literal `^(...)$` text in pod names). Real regex UDF is `px.regex_match(pattern, input)` registered in `carnot/funcs/builtins/regex_ops.cc`.

**Resolution**: `df = df[px.regex_match('^(p1|p2|...)$', df.pod)]` with full regex escaping of pod names defensively (k8s DNS-1123 doesn't admit regex metachars but a future rename rule might).

**Throwaway-test count to discover this**: 3 deploys, ~10 min of iteration. Cheaper than debugging in tests because the failure mode is "pixie compiler rejects" — purely an integration surface.

### 2026-05-17 — first successful 4× streaming sweep

| Table | queries (5 min) | rows from pixie | flushes to CH | CH delta |
|---|---|---|---|---|
| http_events | 8 | 70,000 | 7 | 80,000 |
| redis_events | 8 | 70,000 | 7 | 80,000 |
| pgsql_events | 8 | 50,178 | 6 | 60,178 |
| dns_events | 8 | 1,706 | 4 | 1,706 |
| amqp/cql/mongo/mux/mysql/tls | 8-9 each | 0 | 0 | 0 |
| **Total** | **83** | **191,884** | **24** | **221,884** |

**0 errors, 0 DeadlineExceeded.**

vs rev-2 with manifest throttle defaults at the same 4× load:
- 78 fan-outs, 6-15 successful pushes
- pgsql_events: 20 rows in CH (the chronic starvation case)
- Total ~135k rows

**Rev-3 delivered ~3000× more pgsql data than rev-2 with 1/10th the broker query count.**

### Confirmed design wins

1. **Even per-table workload distribution.** Each table got ~8 queries in 5 min — no table starved by others' larger payloads (the rev-2 pgsql failure mode).
2. **Empty tables are nearly free.** amqp/cql/etc. ran 8-9 queries each, all returning 0 rows. The cost is one network roundtrip per refresh; total wall budget for the 6 empty tables = trivial.
3. **No throttle knobs needed.** The bound IS the design: N tables × 1 query per refresh = O(N) broker concurrency = 10. The rev-2 knobs (per_hash, global, empty_skip) are completely unnecessary in this model.
4. **One ActiveSet seed worked.** Rehydrate-on-boot populated the streaming set from CH without race issues.

### Remaining work (not in this slice)

- Make `OnAttribution` non-blocking (today it's synchronous from controller.handle; if ActiveSet.Upsert blocks, it would back-pressure the controller). Not observed in the sweep, but a contention hazard.
- Wire pruner: PruneExpired ⇒ ActiveSet.Remove. Today the rev-3 ActiveSet only shrinks if Remove is called explicitly; the controller's OnPrune callback IS hooked up but the prune-grace timing means active pods linger past their nominal t_end for `2 * After` (10 min default). Probably fine, but worth measuring under longer load.
- Decide on the operational defaults for `ADAPTIVE_STREAM_REFRESH_SEC`, `ADAPTIVE_STREAM_BATCH_EVERY_SEC`, `ADAPTIVE_STREAM_MAX_WHITELIST`. Current sweep used 30 / 5 / 500 — all worked at this scale; need stress test to find limits.
- Delete rev-2 push path. The throttle knobs in `controller.Config` + `pushPixieRows` + the `inFlight` map are now dead code when streaming mode is on; cleanup once we're sure rev-3 holds up.

### Decisions revisited

- Whitelist key (`namespace/pod` string) → **kept**. regex_match with the rendered key worked first try.
- Re-submit cadence (30 s default + filter-change-driven) → **kept**. Filter coalescing reduced re-submissions to ~1 per ActiveSet change, regardless of the 12k anomalies/sec workload.
- Whitelist size cap (500) → **untested at scale**; sweep had only 6 pods. Future work.

### 2026-05-17 — slice 2 (AttributionNotifier + TDD discipline)

**New rule adopted**: TDD from now on, with unit tests as primary feedback and the 4× sweep as the integration gate. Memory entry: [TDD discipline](feedback_tdd.md). Catalyst: the rev-3 slice-1 work cost 3 redeploys (~30 min) discovering three independent PxL syntax errors that integration testing would have caught once.

**Slice 2 scope**: a non-blocking `AttributionNotifier` between controller callbacks and the ActiveSet. Without it, a slow ActiveSet writer could pin `controller.handle` and back-pressure the trigger.

**TDD process (round 1 — Notifier)**:
1. Wrote 7 unit tests first → red (undefined symbols).
2. Wrote `notifier.go` (~140 LOC) → green except for one test asserting "0 drops on 50 events in 32-buffer" which was over-strict (producer outraced consumer on first burst).
3. Relaxed the test to use buffer >> burst + inter-submit yield — passes.

**Net cost vs slice-1's "deploy + observe" loop**: ~5 min for the whole cycle vs 30 min for slice-1 — and the tests stay as regression coverage.

**TDD process (round 2 — controller callbacks)**:
1. Added 4 new tests for `OnAttribution` + `OnPrune` behavior:
   - `TestController_OnAttribution_FiresPerEvent`
   - `TestController_OnAttribution_NilIsNoop`
   - `TestController_OnPrune_FiresWithKeyDetails`
   - `TestController_OnPrune_NilIsNoop`
   - `TestController_OnPrune_DoesNotHoldMutex` ← caught a real concern: callback under lock would deadlock
2. All 5 passed first run — the earlier refactor of `PruneExpired` to collect-under-lock-then-fire-after-release was already correct.

**TDD process (round 3 — end-to-end integration)**:
1. Added 3 integration tests against fake querier + fake sink:
   - `TestIntegration_NotifierToScannerWhitelistFlow` — green first try.
   - `TestIntegration_EmptyActiveSetSkipsAllQueries` — green first try.
   - `TestIntegration_PrunePropagatesToScannerWhitelist` — RED first try because my assertion was wrong (looking at q.all()'s last entry, which stays stale when scanner correctly skips on empty whitelist). Fixed assertion: count post-Remove queries containing the pod (must be 0). Green.

**Notable test discovery**: the "PrunePropagates" assertion bug taught me that the scanner's empty-whitelist short-circuit is *invisible* in q.all() — assertions on streams of side effects need to count NEW occurrences, not check the latest entry.

### 2026-05-17 — slice 2 4× sweep result

Same workload as slice 1. Comparable throughput:

| Table | queries (5min) | rows from pixie | CH fresh rows |
|---|---|---|---|
| http_events | 7 | 70,000 | 80,000 |
| redis_events | 7 | 70,000 | 90,000 |
| pgsql_events | 7 | 50,000 | 50,000 |
| dns_events | 7 | 1,490 | 1,490 |
| 6 quiet tables | 6-7 each | 0 | 0 |
| **Total** | **69** | **191,490** | **221,490** |

**0 errors. 0 DeadlineExceeded. 23 batched CH writes (was N×10×per_hash×per_pass in rev-2).**

No regression vs slice 1; the Notifier is essentially zero-overhead at this load.

### TDD insights this session

- Unit tests turned around in **seconds** vs the deploy-loop's **minutes**. The notifier was production-ready in ~5 min of test-first work; slice 1's PxL discovery cost 30 min of deploy-loop work.
- The "OnPrune doesn't hold mutex" test required some thought to write but **prevents an entire class of future deadlocks** under load.
- The "PrunePropagates" failure was an *assertion bug*, not a *code bug* — but it forced me to articulate the actual invariant precisely ("no NEW queries containing the pod after Remove"), which is sharper than "last query shouldn't have it".
- I should write more tests like `TestController_OnPrune_DoesNotHoldMutex` — concurrency-discipline assertions that are nearly impossible to debug post-hoc.
