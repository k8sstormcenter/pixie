// Copyright 2018- The Pixie Authors.
// SPDX-License-Identifier: Apache-2.0

package sink

import (
	"bytes"
	"testing"
	"time"
)

// dcSnoopRow builds a representative dc_snoop event. withParent adds the three
// Int64 fields the ppid change introduces (pid_start/ppid/ppid_start) — everything
// else is the pre-change shape. Parent comm is NOT captured (it was the 9th printf
// arg that exceeded bpftrace's budget and zeroed capture).
func dcSnoopRow(withParent bool) map[string]any {
	r := map[string]any{
		"time_":      time.Unix(0, 1_700_000_000_171_199_174),
		"pid":        int64(159249),
		"comm":       "sh",
		"t":          "R",
		"file":       "opt/bitnami/common/bin/sh",
		"namespace":  "redis",
		"pod":        "redis-master-0",
		"container":  "redis",
		"hostname":   "node-01",
		"event_time": time.Unix(0, 1_700_000_000_171_199_174),
	}
	if withParent {
		r["pid_start"] = int64(1_700_000_000_000_000_000)
		r["ppid"] = int64(90059)
		r["ppid_start"] = int64(1_699_999_999_000_000_000)
	}
	return r
}

// encodeBytesPerRow encodes n identical dc_snoop rows via the same fast path the
// sink uses (encodePixieRowsFast → appendJSONValue) and returns the wire bytes/row.
// cols pins the projection so we can measure the old (10-col) vs new (14-col) shape
// independent of the current schema.
func encodeBytesPerRow(b *testing.B, withParent bool, cols []string) float64 {
	const n = 1000
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = dcSnoopRow(withParent)
	}
	var buf bytes.Buffer
	// warm one pass to get the byte size
	buf.Reset()
	for _, r := range rows {
		buf.WriteByte('{')
		for j, c := range cols {
			if j > 0 {
				buf.WriteByte(',')
			}
			buf.WriteByte('"')
			buf.WriteString(c)
			buf.WriteString(`":`)
			_ = appendJSONValue(&buf, r[c])
		}
		buf.WriteString("}\n")
	}
	bytesPerRow := float64(buf.Len()) / n

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		for _, r := range rows {
			buf.WriteByte('{')
			for j, c := range cols {
				if j > 0 {
					buf.WriteByte(',')
				}
				buf.WriteByte('"')
				buf.WriteString(c)
				buf.WriteString(`":`)
				_ = appendJSONValue(&buf, r[c])
			}
			buf.WriteString("}\n")
		}
	}
	return bytesPerRow
}

var (
	dcSnoopOldCols = []string{"time_", "pid", "comm", "t", "file", "namespace", "pod", "container", "hostname", "event_time"}
	dcSnoopNewCols = []string{"time_", "pid", "pid_start", "ppid", "ppid_start", "comm", "t", "file", "namespace", "pod", "container", "hostname", "event_time"}
)

// BenchmarkDCSnoopEncode_Baseline / _WithParent measure the per-event wire bytes
// before and after the ppid addition. The delta (reported as bytes/row) IS the
// additional data the change loads per dcache event.
func BenchmarkDCSnoopEncode_Baseline(b *testing.B) {
	bpr := encodeBytesPerRow(b, false, dcSnoopOldCols)
	b.ReportMetric(bpr, "bytes/row")
}

func BenchmarkDCSnoopEncode_WithParent(b *testing.B) {
	bpr := encodeBytesPerRow(b, true, dcSnoopNewCols)
	b.ReportMetric(bpr, "bytes/row")
	// Columnar (PEM table-store) cost of the 3 added fields: 3×Int64.
	b.ReportMetric(3*8, "columnar_add_bytes/row")
}

// TestDCSnoopPerEventDataDelta prints the concrete numbers (not just a benchmark
// metric) so the RCA has a hard figure: added wire bytes/row and the columnar
// (PEM) add, plus what that is per 1M dcache events.
func TestDCSnoopPerEventDataDelta(t *testing.T) {
	oldB := sizeOnce(false, dcSnoopOldCols)
	newB := sizeOnce(true, dcSnoopNewCols)
	addWire := newB - oldB
	addColumnar := 3 * 8 // pid_start+ppid+ppid_start (Int64); parent comm not captured
	t.Logf("dc_snoop per-event data delta:")
	t.Logf("  wire (JSON) bytes/row:      old=%d  new=%d  +%d", oldB, newB, addWire)
	t.Logf("  columnar (PEM) bytes/row:   +%d (3xInt64)", addColumnar)
	t.Logf("  per 1,000,000 events:       +%d MB wire, +%d MB columnar", addWire, addColumnar)
	t.Logf("  NOTE: PEM table-store is CAPPED (PL_TABLE_STORE_DATA_LIMIT_MB) — the +40B/event")
	t.Logf("        fills the cap faster (shorter lookback) but does NOT raise peak memory.")
	t.Logf("        collapse also removes ~3.5x rows at export, so net exported data DROPS.")
}

func sizeOnce(withParent bool, cols []string) int {
	r := dcSnoopRow(withParent)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for j, c := range cols {
		if j > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('"')
		buf.WriteString(c)
		buf.WriteString(`":`)
		_ = appendJSONValue(&buf, r[c])
	}
	buf.WriteString("}\n")
	return buf.Len()
}
