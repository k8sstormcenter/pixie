// Copyright 2018- The Pixie Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package trigger watches forensic_db.kubescape_logs for new rows and
// pushes parsed kubescape.Event values onto a channel. Polls the
// ClickHouse HTTP interface (default 250ms cadence). Operator runs as
// a DaemonSet — each instance polls only its OWN node's rows via
// `WHERE hostname = '<this-node>'`.
package trigger

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/kubescape"
)

// Config configures the trigger. PollInterval defaults to 250ms.
// Hostname is REQUIRED — it scopes every poll to a single node.
type Config struct {
	Endpoint     string
	Database     string
	Table        string
	Username     string
	Password     string
	Hostname     string
	PollInterval time.Duration

	// InitialWatermark is a fallback used ONLY when Watermark is nil
	// AND the persistent store is also empty. The production wiring
	// always supplies Watermark and leaves this zero.
	InitialWatermark uint64

	// Watermark, when non-nil, makes the trigger persistent across
	// restarts: the first poll loads from the store; successful
	// advances are saved back (throttled by WatermarkSaveInterval).
	// nil → behaves like pre-watermark trigger (in-memory only,
	// starts from InitialWatermark; previously the source of the
	// "infinite full-table replay after OOM" bug).
	Watermark WatermarkStore

	// WatermarkSaveInterval throttles persistent writes — we'd
	// otherwise INSERT every 250ms on a busy node. Default 5s.
	WatermarkSaveInterval time.Duration

	// PollLimit caps rows returned per poll. Bounds catch-up work
	// after a restart so a 10h backlog doesn't translate into a
	// single multi-GiB SELECT the HTTP client times out on; instead
	// it drains in N polls of PollLimit rows.
	// Default: 10000 (also used when caller passes 0). Set explicitly
	// if the default doesn't match your backlog/throughput; "unlimited"
	// is NOT a supported value — every poll always carries a LIMIT.
	// (CodeRabbit r-#68/trigger/clickhouse.go.)
	PollLimit int

	// HTTPTimeout bounds each individual poll. Default 30s; previously
	// hardcoded to 5s, which under any backlog caused every poll to
	// time out mid-stream → watermark never advanced.
	HTTPTimeout time.Duration

	// Lookback (#97 / F8 / AE-9): when > 0, each poll re-scans
	// [watermark-Lookback, ∞) instead of the strict [watermark, ∞) and
	// dedupes re-seen rows by content fingerprint, so an out-of-order /
	// clock-skewed / restart-buried row that lands within the window is
	// still processed EXACTLY ONCE (no drop, no duplicate). Rows below
	// watermark-Lookback stay dropped — the documented bound. 0 keeps
	// the legacy strict high-water-mark behavior (anything below the
	// watermark is dropped forever). Production default is 300s via
	// ADAPTIVE_TRIGGER_LOOKBACK_SEC in cmd/main.go; the zero value here
	// is legacy so existing callers/tests are unchanged.
	Lookback time.Duration

	// MaxSkew is the wall-clock poison clamp (#97): a row whose
	// NORMALIZED event_time is more than MaxSkew past now is still
	// emitted once, but never advances the watermark, so a single
	// corrupted/oversized timestamp (the 1.78e18 leftover of loadtest
	// E8) cannot jump the cursor past all real data and silently halt
	// the trigger. Also applied to the persisted watermark at load, so
	// an ALREADY-poisoned cursor self-recovers on restart without the
	// manual `ALTER TABLE trigger_watermark DELETE`. <=0 → 1h.
	MaxSkew time.Duration

	// DedupMaxEntries caps the lookback dedup set (memory bound). An
	// in-window fingerprint evicted by capacity may re-emit once, so
	// size it >= the max rows expected per lookback window.
	// <=0 → 4*PollLimit.
	DedupMaxEntries int
}

// defaultMaxSkew is the default wall-clock poison-clamp bound (#97):
// an event_time more than this far in the future is implausible.
const defaultMaxSkew = time.Hour

// ClickHouseHTTP polls forensic_db.<table> over the ClickHouse HTTP
// interface, scoped to a single node.
type ClickHouseHTTP struct {
	cfg    Config
	client *http.Client
	// now is the wall clock used by the poison clamp (#97).
	// Injectable for deterministic tests; time.Now in production.
	now func() time.Time
}

// New validates Config and returns a ready trigger.
func New(cfg Config) (*ClickHouseHTTP, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("trigger: empty Endpoint")
	}
	if cfg.Hostname == "" {
		return nil, fmt.Errorf("trigger: empty Hostname (operator must run node-local)")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("trigger: invalid Endpoint %q: %w", cfg.Endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("trigger: Endpoint %q must use http or https scheme", cfg.Endpoint)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("trigger: Endpoint %q has empty host", cfg.Endpoint)
	}
	if cfg.Database == "" {
		cfg.Database = "forensic_db"
	}
	if cfg.Table == "" {
		cfg.Table = "kubescape_logs"
	}
	// Validate Database / Table as plain ClickHouse identifiers
	// (alphanumeric + underscore, not starting with a digit) so the
	// SELECT in fetchSince cannot be subverted by an adversary-controlled
	// Config. Hostname is value-quoted via quoteCH; identifiers cannot
	// be parameterised, hence validation here.
	if !validIdentifier(cfg.Database) {
		return nil, fmt.Errorf("trigger: invalid Database identifier %q (must match [A-Za-z_][A-Za-z0-9_]*)", cfg.Database)
	}
	if !validIdentifier(cfg.Table) {
		return nil, fmt.Errorf("trigger: invalid Table identifier %q (must match [A-Za-z_][A-Za-z0-9_]*)", cfg.Table)
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	if cfg.WatermarkSaveInterval <= 0 {
		cfg.WatermarkSaveInterval = 5 * time.Second
	}
	if cfg.PollLimit < 0 {
		return nil, fmt.Errorf("trigger: PollLimit must be >= 0 (got %d)", cfg.PollLimit)
	}
	if cfg.PollLimit == 0 {
		cfg.PollLimit = 10000
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	if cfg.Lookback < 0 {
		return nil, fmt.Errorf("trigger: Lookback must be >= 0 (got %v)", cfg.Lookback)
	}
	if cfg.MaxSkew <= 0 {
		cfg.MaxSkew = defaultMaxSkew
	}
	if cfg.DedupMaxEntries <= 0 {
		cfg.DedupMaxEntries = 4 * cfg.PollLimit
	}
	return &ClickHouseHTTP{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.HTTPTimeout},
		now:    time.Now,
	}, nil
}

// identifierRE accepts plain ClickHouse identifiers — letters, digits,
// underscores; not starting with a digit. Dotted identifiers (e.g.
// "http2_messages.beta") are deliberately rejected here because the
// trigger only ever queries the kubescape ingest table, not a pixie
// observation table.
var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validIdentifier(s string) bool { return identifierRE.MatchString(s) }

// Subscribe starts the background poll loop. The returned channel
// produces kubescape.Event values until ctx is cancelled, then closes.
func (t *ClickHouseHTTP) Subscribe(ctx context.Context) (<-chan kubescape.Event, error) {
	out := make(chan kubescape.Event, 64)
	go t.run(ctx, out)
	return out, nil
}

func (t *ClickHouseHTTP) run(ctx context.Context, out chan<- kubescape.Event) {
	defer close(out)
	// Watermark uses event_time as the cursor PLUS a set of row
	// fingerprints already pushed. In legacy strict mode (Lookback==0)
	// the query is `event_time >= watermark` (inclusive) and the
	// fingerprint set covers only the exact boundary event_time —
	// closing the race where two kubescape rows share the same
	// event_time but the second arrives after our previous poll. With
	// a bounded lookback (#97, the F8/AE-9 fix) the query starts at
	// max(0, watermark-Lookback) and the fingerprint set is a bounded
	// LRU over the whole re-scanned window, so out-of-order / skewed /
	// restart-buried rows inside the window are captured exactly once.
	//
	// Cold-start order: persistent store > InitialWatermark > 0.
	// The persistent store is the production answer to "operator
	// OOMed, restarts, replays 10h of kubescape_logs from 0, every
	// poll times out, never recovers" — without it any restart on
	// a busy node is permanently stuck.
	watermark := t.cfg.InitialWatermark
	if t.cfg.Watermark != nil {
		// Bound the load with its own context so a flaky CH doesn't
		// block start-up indefinitely. The trigger then falls back
		// to InitialWatermark and we log the failure loudly.
		loadCtx, cancel := context.WithTimeout(ctx, t.cfg.HTTPTimeout)
		wm, ok, err := t.cfg.Watermark.Load(loadCtx, t.cfg.Hostname, t.cfg.Table)
		cancel()
		switch {
		case err != nil:
			log.WithError(err).Warn("trigger: persistent watermark load failed; using InitialWatermark")
		case ok:
			watermark = wm
			log.WithField("watermark", wm).Info("trigger: resumed from persistent watermark")
		default:
			log.WithField("initial", t.cfg.InitialWatermark).
				Info("trigger: no persistent watermark; using InitialWatermark")
		}
	}
	// Cursor is canonical NANOS (F8). Normalize whatever we loaded so a
	// pre-fix persisted seconds watermark (or a non-seconds InitialWatermark)
	// is interpreted on the same scale as chNormEventTimeNanos in the SQL.
	watermark = normalizeEventTimeNanos(watermark)
	maxSkewNS := uint64(t.cfg.MaxSkew.Nanoseconds())
	lookbackNS := uint64(t.cfg.Lookback.Nanoseconds())
	// Self-recovery from an ALREADY-poisoned persisted cursor (#97 T1):
	// a pre-fix deployment could have persisted a far-future watermark
	// (loadtest E8's leftover 1.78e18-style value). Clamp it to
	// wall-clock so fresh rows flow again on restart WITHOUT the manual
	// `ALTER TABLE trigger_watermark DELETE WHERE 1=1` + redeploy.
	if nowNS := uint64(t.now().UnixNano()); watermark > nowNS+maxSkewNS {
		log.WithFields(log.Fields{"watermark": watermark, "clamped_to": nowNS}).
			Warn("trigger: persisted watermark is implausibly far in the future — clamping to wall-clock (poison recovery, #97)")
		watermark = nowNS
	}
	wmGauge := metricWatermarkNS.WithLabelValues(t.cfg.Table, t.cfg.Hostname)
	wmGauge.Set(float64(watermark))
	// Dedup state. Strict mode (Lookback==0) keeps the legacy exact
	// boundary set; lookback mode dedupes the whole re-scanned window
	// with a bounded LRU (#97). rejectedSeen exists only in strict mode:
	// a clamp-rejected row never falls below the cursor, so without a
	// fingerprint record it would re-emit on every poll.
	seenAtBoundary := map[string]bool{}
	var seenInWindow *dedupLRU
	var rejectedSeen *dedupLRU
	if lookbackNS > 0 {
		seenInWindow = newDedupLRU(t.cfg.DedupMaxEntries)
	} else {
		rejectedSeen = newDedupLRU(t.cfg.DedupMaxEntries)
	}
	// catchup lifts a poll's lower bound above the sliding lookback
	// floor while an in-window backlog is wider than PollLimit: without
	// it every poll would re-fetch the same fully-deduped first
	// PollLimit rows and never reach deeper into the window. Cleared as
	// soon as a poll returns under capacity (back to full-window scans).
	var catchup uint64
	ticker := time.NewTicker(t.cfg.PollInterval)
	defer ticker.Stop()

	// Throttle persistent writes: every successful advance is in
	// memory immediately, but only flushed to CH at most every
	// WatermarkSaveInterval. dirty tracks whether the in-memory
	// watermark differs from what was last persisted.
	//
	// The flush is invoked INSIDE pollOnce (not from a ticker case
	// in the for/select), because the initial pollOnce on a busy
	// node can block for tens of seconds while it drains 10k events
	// down a back-pressured channel — during which time the for/
	// select isn't running and a saveTicker.C tick would never be
	// observed. Throttling is done with a time.Time comparison.
	lastSaved := watermark
	var lastSaveTime time.Time
	dirty := false
	flushWatermark := func() {
		if !dirty || t.cfg.Watermark == nil || watermark == lastSaved {
			return
		}
		if !lastSaveTime.IsZero() && time.Since(lastSaveTime) < t.cfg.WatermarkSaveInterval {
			return
		}
		saveCtx, cancel := context.WithTimeout(ctx, t.cfg.HTTPTimeout)
		err := t.cfg.Watermark.Save(saveCtx, t.cfg.Hostname, t.cfg.Table, watermark)
		cancel()
		if err != nil {
			log.WithError(err).WithField("watermark", watermark).
				Warn("trigger: persistent watermark save failed; will retry next interval")
			return
		}
		lastSaved = watermark
		lastSaveTime = time.Now()
		dirty = false
	}
	// Best-effort final flush so a clean shutdown doesn't lose up
	// to WatermarkSaveInterval of progress.
	defer func() {
		if t.cfg.Watermark != nil && dirty {
			saveCtx, cancel := context.WithTimeout(context.Background(), t.cfg.HTTPTimeout)
			defer cancel()
			if err := t.cfg.Watermark.Save(saveCtx, t.cfg.Hostname, t.cfg.Table, watermark); err != nil {
				log.WithError(err).Warn("trigger: shutdown watermark save failed")
			}
		}
	}()

	pollOnce := func() {
		// Bounded lookback (#97): scan from max(0, watermark-Lookback)
		// so rows that landed BELOW the cursor (out-of-order, clock
		// skew, restart burial) are still fetched; the dedup LRU makes
		// re-seen rows exactly-once. Lookback==0 → legacy strict HWM.
		queryFrom := watermark
		if lookbackNS > 0 {
			queryFrom = 0
			if watermark > lookbackNS {
				queryFrom = watermark - lookbackNS
			}
			if catchup > queryFrom {
				queryFrom = catchup
			}
		}
		rows, maxFetched, err := t.fetchSince(ctx, queryFrom)
		// Partial-read tolerance: when the body read is cut short by
		// HTTP timeout / connection reset, fetchSince returns the rows
		// it managed to parse + err. We still process those rows so
		// the watermark advances by what we got; failing to do so was
		// the second half of the "stuck forever" bug.
		if err != nil {
			if len(rows) == 0 {
				log.WithError(err).Warn("trigger: poll failed")
				return
			}
			log.WithError(err).WithField("partial_rows", len(rows)).
				Warn("trigger: poll partial — advancing on what parsed")
		}
		// Wall-clock poison clamp (#97): any normalized event_time past
		// now+MaxSkew must never advance the cursor. acceptedMax is the
		// advancement target — the max normalized event_time among rows
		// that PASS the clamp. With no poison rows it equals maxFetched,
		// so the monotonic happy path is byte-identical to before.
		skewLimit := uint64(t.now().UnixNano()) + maxSkewNS
		acceptedMax := uint64(0)
		for _, row := range rows {
			if evn := normalizeEventTimeNanos(row.EventTime); evn <= skewLimit && evn > acceptedMax {
				acceptedMax = evn
			}
		}
		wmAtPollStart := watermark
		nextSeen := map[string]bool{}
		// Periodic in-loop save: when pollOnce is draining a large
		// initial backlog, the watermark advances long before the
		// loop exits. Calling flushWatermark every N rows means the
		// persistent watermark catches up even mid-drain, so a crash
		// during the drain doesn't replay the whole backlog. Combined
		// with the time-based throttle inside flushWatermark, this
		// produces at most one persistent INSERT per WatermarkSaveInterval.
		const saveEveryN = 256
		skippedSeen := 0
		emitted := 0
		for i, row := range rows {
			fp := rowFingerprint(row)
			// Cursor comparisons are in NORMALIZED nanos (F8): the raw
			// event_time unit is not enforced, so compare on the same scale
			// as the SQL filter (chNormEventTimeNanos) and acceptedMax.
			evn := normalizeEventTimeNanos(row.EventTime)
			if lookbackNS > 0 {
				if seenInWindow.Contains(fp) {
					skippedSeen++
					continue // already pushed in a prior scan of this window
				}
			} else {
				if evn == watermark && seenAtBoundary[fp] {
					skippedSeen++
					continue // already pushed in a prior poll at this exact boundary
				}
				if rejectedSeen.Contains(fp) {
					continue // clamp-rejected row re-fetched (it never sinks below the cursor)
				}
			}
			poison := evn > skewLimit
			ev, exErr := kubescape.Extract(row)
			if exErr != nil {
				log.WithError(exErr).Debug("trigger: skip incomplete row")
				// Register the fingerprint anyway (lookback / poison):
				// the row can never become extractable, and without a
				// record it would be re-fetched + re-logged every poll
				// for as long as it stays above the scan floor.
				if lookbackNS > 0 {
					seenInWindow.Add(fp, evn)
				} else if poison {
					rejectedSeen.Add(fp, evn)
				}
				continue
			}
			if poison {
				// Emit the row once (it may be a real anomaly with a
				// mangled timestamp) but do NOT let it advance the
				// cursor: one 1.78e18 row must not jump the watermark
				// past all real seconds rows (F8 halt).
				metricEventTimeRejected.Inc()
				log.WithFields(log.Fields{
					"event_time": row.EventTime,
					"normalized": evn,
					"skew_limit": skewLimit,
				}).Warn("trigger: event_time beyond wall-clock skew bound — processing row WITHOUT advancing watermark (poison clamp, #97)")
			} else {
				if evn < wmAtPollStart {
					// A row the legacy strict HWM would have dropped —
					// captured via the lookback (T2). Observable proof
					// the fix is doing work (T3).
					metricBelowWatermark.Inc()
				}
				// Promote the per-row (normalized) event_time into the watermark
				// immediately so flushWatermark below can persist mid-drain.
				if evn > watermark {
					watermark = evn
					dirty = true
					wmGauge.Set(float64(watermark))
				}
			}
			if lookbackNS > 0 {
				seenInWindow.Add(fp, evn)
			} else if poison {
				rejectedSeen.Add(fp, evn)
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
			emitted++
			if !poison && evn == acceptedMax {
				nextSeen[fp] = true
			}
			if i > 0 && i%saveEveryN == 0 {
				flushWatermark()
			}
		}
		if lookbackNS > 0 {
			if acceptedMax > watermark {
				watermark = acceptedMax
				dirty = true
				wmGauge.Set(float64(watermark))
			}
			// Paging within the window: a saturated response means the
			// window holds more rows than PollLimit — lift the floor so
			// the next poll pages FORWARD instead of re-fetching the
			// same deduped prefix forever.
			if len(rows) >= t.cfg.PollLimit {
				if emitted == 0 && skippedSeen == len(rows) {
					// Every row in the saturated page was already seen —
					// step past the page entirely (lookback analog of the
					// legacy 1ns boundary escape).
					catchup = maxFetched + 1
				} else if acceptedMax > catchup {
					catchup = acceptedMax
				}
			} else {
				catchup = 0
			}
			// Entries below the sliding floor can never be re-fetched;
			// evict them so the LRU stays at ~window size.
			floor := uint64(0)
			if watermark > lookbackNS {
				floor = watermark - lookbackNS
			}
			seenInWindow.EvictBelow(floor)
		} else if acceptedMax > watermark {
			watermark = acceptedMax
			seenAtBoundary = nextSeen
			dirty = true
			wmGauge.Set(float64(watermark))
		} else if acceptedMax == watermark {
			// no progress this tick — preserve boundary set, optionally extend
			for fp := range nextSeen {
				seenAtBoundary[fp] = true
			}
			// Paging escape: if every row returned was a boundary-skip AND
			// the response was at PollLimit capacity, there may be additional
			// rows at the same normalized event_time that we will never reach
			// (the SQL ORDER BY has no secondary key, so LIMIT always returns
			// the same PollLimit rows from the boundary). Advance the watermark
			// by 1 nanosecond to escape the boundary. In practice this means
			// at most one nanosecond's worth of events are not re-delivered on
			// the next poll, which is acceptable: the fingerprint dedup already
			// tolerates boundary overlap, and we prefer forward progress over
			// an infinite loop.
			if skippedSeen > 0 && len(nextSeen) == 0 && len(rows) >= t.cfg.PollLimit {
				watermark++
				seenAtBoundary = map[string]bool{}
				dirty = true
				wmGauge.Set(float64(watermark))
				log.WithField("watermark", watermark).
					Warn("trigger: boundary paging escape — advanced watermark by 1ns to unblock poll")
			}
		}
		// Final flush at end of pollOnce — also throttled.
		flushWatermark()
	}

	pollOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollOnce()
		}
	}
}

// rowFingerprint hashes the row's content so we can dedupe at the
// watermark boundary without trusting kubescape to give us a unique row id.
func rowFingerprint(r kubescape.Row) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00%s",
		r.EventTime, r.RuleID, r.Hostname, r.K8sDetails, r.ProcessDetails)
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeEventTimeNanos maps a raw kubescape event_time (UInt64, whose unit
// the pipeline does not enforce) to canonical UNIX NANOSECONDS using the same
// magnitude thresholds as controller.eventTimeToTime. This is the fix for the
// watermark-poison bug (FINDINGS_AND_BACKLOG F8): the trigger's cursor is a
// monotonic high-water-mark, so without a single canonical unit a stray row in
// a larger unit (e.g. one nanos row, ~1.78e18) drives the watermark past every
// real seconds row (~1.78e9) and AE silently stops processing forever. The
// cursor + the SQL filter both operate on the normalized value so units are
// always comparable.
func normalizeEventTimeNanos(et uint64) uint64 {
	switch {
	case et < 1e10:
		return et * 1_000_000_000 // seconds → nanos
	case et < 1e13:
		return et * 1_000_000 // millis → nanos
	default:
		return et // already nanos
	}
}

// chNormEventTimeNanos is the ClickHouse expression equivalent of
// normalizeEventTimeNanos — used in the trigger SELECT so the >= watermark
// filter and ORDER BY are unit-agnostic server-side. (UInt64 headroom: the
// largest pre-normalization input that hits the *1e9 branch is <1e10, so the
// product is <1e19 < 2^64.)
const chNormEventTimeNanos = "multiIf(event_time < 10000000000, event_time * 1000000000, " +
	"event_time < 10000000000000, event_time * 1000000, event_time)"

func (t *ClickHouseHTTP) fetchSince(ctx context.Context, watermark uint64) ([]kubescape.Row, uint64, error) {
	q := url.Values{}
	// LIMIT bounds per-poll work. ORDER BY event_time + LIMIT N means
	// catch-up from a stale watermark drains in ceil(backlog/N) polls
	// of small responses instead of one giant scan. Without this, an
	// operator that restarted into a multi-hour backlog could never
	// recover — every unbounded query exceeded HTTPTimeout.
	// Filter + order on the NORMALIZED (nanos) event_time so the watermark
	// cursor is unit-agnostic (F8 fix). watermark is already in nanos.
	q.Set("query", fmt.Sprintf(
		"SELECT RuleID, RuntimeK8sDetails, RuntimeProcessDetails, event_time, hostname "+
			"FROM %s.%s "+
			"WHERE hostname = %s AND %s >= %d "+
			"ORDER BY %s LIMIT %d FORMAT JSONEachRow",
		t.cfg.Database, t.cfg.Table, quoteCH(t.cfg.Hostname),
		chNormEventTimeNanos, watermark, chNormEventTimeNanos, t.cfg.PollLimit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		t.cfg.Endpoint+"/?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	if t.cfg.Username != "" {
		req.SetBasicAuth(t.cfg.Username, t.cfg.Password)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseJSONEachRow(resp.Body)
}

// parseJSONEachRow streams JSONEachRow output line-by-line from r.
// Streaming (vs io.ReadAll into a []byte) bounds memory at one row
// regardless of how large the ClickHouse result set is.
//
// Malformed rows are LOGGED + SKIPPED, never fatal: a single bad line
// must not block watermark advancement and re-pin the bad row on every
// subsequent poll. Only an unrecoverable scanner error (e.g. line
// exceeds the 16 MiB buffer) fails the call.
func parseJSONEachRow(r io.Reader) ([]kubescape.Row, uint64, error) {
	type rawRow struct {
		RuleID                string          `json:"RuleID"`
		RuntimeK8sDetails     string          `json:"RuntimeK8sDetails"`
		RuntimeProcessDetails string          `json:"RuntimeProcessDetails"`
		EventTime             json.RawMessage `json:"event_time"`
		Hostname              string          `json:"hostname"`
	}
	var (
		rows    []kubescape.Row
		maxSeen uint64
	)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rr rawRow
		if err := json.Unmarshal(line, &rr); err != nil {
			log.WithError(err).Debug("trigger: skip malformed JSON row")
			continue
		}
		ev, err := parseUint64Loose(rr.EventTime)
		if err != nil {
			log.WithError(err).Debug("trigger: skip row with bad event_time")
			continue
		}
		rows = append(rows, kubescape.Row{
			EventTime:      ev,
			RuleID:         rr.RuleID,
			Hostname:       rr.Hostname,
			K8sDetails:     rr.RuntimeK8sDetails,
			ProcessDetails: rr.RuntimeProcessDetails,
		})
		// maxSeen is the cursor max in NORMALIZED nanos (F8): with an
		// unenforced unit the raw max is not necessarily the time-max.
		if n := normalizeEventTimeNanos(ev); n > maxSeen {
			maxSeen = n
		}
	}
	if err := scanner.Err(); err != nil {
		// Partial-read tolerance: return whatever parsed cleanly along
		// with the error so the caller can still advance the watermark.
		// Without this, an HTTP body read cut off mid-stream (the
		// classic 5s-timeout-on-2GB-response failure mode) discarded
		// ~all parsed rows and pinned the watermark in place.
		return rows, maxSeen, err
	}
	return rows, maxSeen, nil
}

func parseUint64Loose(raw json.RawMessage) (uint64, error) {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	return strconv.ParseUint(s, 10, 64)
}

// chLiteralEscaper — hoisted to a package-level var so we don't allocate
// a Replacer per call (quoteCH is hot in rowFingerprint).
var chLiteralEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func quoteCH(s string) string {
	return "'" + chLiteralEscaper.Replace(s) + "'"
}
