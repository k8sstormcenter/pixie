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
	// it drains in N polls of PollLimit rows. Default 10000.
	// 0 → unlimited (legacy behavior — NOT recommended in prod).
	PollLimit int

	// HTTPTimeout bounds each individual poll. Default 30s; previously
	// hardcoded to 5s, which under any backlog caused every poll to
	// time out mid-stream → watermark never advanced.
	HTTPTimeout time.Duration
}

// ClickHouseHTTP polls forensic_db.<table> over the ClickHouse HTTP
// interface, scoped to a single node.
type ClickHouseHTTP struct {
	cfg    Config
	client *http.Client
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
	// SELECT in fetchSince cannot be subverted by an attacker-controlled
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
	return &ClickHouseHTTP{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.HTTPTimeout},
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
	// fingerprints already pushed at that exact event_time. This
	// closes the race where two kubescape rows share the same
	// event_time but the second arrives after our previous poll: the
	// query is `event_time >= watermark` (inclusive) and we skip rows
	// whose fingerprint we have already seen at the boundary.
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
	seenAtBoundary := map[string]bool{}
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
		wmBefore := watermark
		rows, maxSeen, err := t.fetchSince(ctx, watermark)
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
		nextSeen := map[string]bool{}
		// Periodic in-loop save: when pollOnce is draining a large
		// initial backlog, the watermark advances long before the
		// loop exits. Calling flushWatermark every N rows means the
		// persistent watermark catches up even mid-drain, so a crash
		// during the drain doesn't replay the whole backlog. Combined
		// with the time-based throttle inside flushWatermark, this
		// produces at most one persistent INSERT per WatermarkSaveInterval.
		const saveEveryN = 256
		for i, row := range rows {
			fp := rowFingerprint(row)
			// Cursor comparisons are in NORMALIZED nanos (F8): the raw
			// event_time unit is not enforced, so compare on the same scale
			// as the SQL filter (chNormEventTimeNanos) and maxSeen.
			evn := normalizeEventTimeNanos(row.EventTime)
			if evn == watermark && seenAtBoundary[fp] {
				continue // already pushed in a prior poll at this exact boundary
			}
			ev, err := kubescape.Extract(row)
			if err != nil {
				log.WithError(err).Debug("trigger: skip incomplete row")
				continue
			}
			// Promote the per-row (normalized) event_time into the watermark
			// immediately so flushWatermark below can persist mid-drain.
			if evn > watermark {
				watermark = evn
				dirty = true
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
			if evn == maxSeen {
				nextSeen[fp] = true
			}
			if i > 0 && i%saveEveryN == 0 {
				flushWatermark()
			}
		}
		if maxSeen > watermark {
			watermark = maxSeen
			seenAtBoundary = nextSeen
			dirty = true
		} else if maxSeen == watermark {
			// no progress this tick — preserve boundary set, optionally extend
			for fp := range nextSeen {
				seenAtBoundary[fp] = true
			}
		}
		// Paging safety: if a full LIMIT-sized batch returned and the watermark
		// still has not advanced past its pre-poll value, every row in the batch
		// shares the same normalized event_time and all were de-duplicated (or
		// failed extraction). ClickHouse ORDER BY + LIMIT N cannot page forward
		// within a single event_time value, so future polls would re-fetch the
		// same batch in the same order and make no progress. Advance by 1 ns to
		// escape the stuck boundary; the next poll picks up rows with strictly
		// larger event_time.
		if len(rows) == t.cfg.PollLimit && watermark == wmBefore && wmBefore > 0 {
			watermark++
			seenAtBoundary = map[string]bool{} // boundary changed; reset dedup set
			dirty = true
			log.WithField("watermark", watermark).
				Debug("trigger: full batch at boundary — bumped watermark by 1 ns to page forward")
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
