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

package sink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
)

// encodePixieRowsFast writes a JSONEachRow batch for the named pixie
// table to buf without going through encoding/json's reflect path.
//
// Why: the AE CPU bench showed 50 % of WritePixieRows wall time in
// encoding/json.(*encodeState).reflectValue + 16 % in slices.SortFunc
// because rows are map[string]any — the encoder is forced through
// reflect.MapRange + per-row map-key alphabetic sort. This fast path
// looks up the table's column order from schema.sql (once, cached)
// and walks each row in that fixed order, type-switching the value
// and writing the JSON atom directly. No reflect, no sort, ~3 % of
// the allocations.
//
// Returns ErrUnknownTable for tables we don't have a schema for —
// the caller (sink.WritePixieRows) falls back to encoding/json so a
// new pixie table not yet in schema.sql isn't a hard failure.
func encodePixieRowsFast(buf *bytes.Buffer, table string, rows []map[string]any) error {
	cols, err := getCachedColumns(table)
	if err != nil {
		return err
	}
	for _, row := range rows {
		buf.WriteByte('{')
		first := true
		for _, col := range cols {
			v, ok := row[col]
			if !ok {
				// event_time derivation: pxapi result rows carry time_
				// (TIME64NS) but never event_time — that column was added by
				// Pixie's retention plugin in the production flow, but the
				// operator-direct push path AE takes bypasses the plugin.
				// Without this derivation the column collapsed to CH's
				// epoch-0 default and every operator-pushed row landed in
				// partition 197001 (rig 6a25c85c, 2026-06-07 — visible in
				// the data even though the silent-drop was fixed by aeprod6).
				// schema.sql also carries a DEFAULT toDateTime64(time_, 3)
				// as a belt-and-suspenders safety net for fresh installs;
				// this derivation handles existing tables (where the
				// CREATE TABLE IF NOT EXISTS is a no-op) AND tables on CH
				// versions that don't evaluate DEFAULT expressions on
				// JSONEachRow insert.
				if col == "event_time" {
					if t, hasTime := row["time_"]; hasTime {
						v = t
						ok = true
					}
				}
				if !ok {
					continue
				}
			}
			if !first {
				buf.WriteByte(',')
			}
			first = false
			// Column names from schema.sql are always plain identifiers
			// (matches chIdentRE in clickhouse.go); safe to emit without
			// JSON-string escape work.
			buf.WriteByte('"')
			buf.WriteString(col)
			buf.WriteString(`":`)
			if err := appendJSONValue(buf, v); err != nil {
				return fmt.Errorf("fastencode: %s.%s: %w", table, col, err)
			}
		}
		buf.WriteByte('}')
		buf.WriteByte('\n')
	}
	return nil
}

// getCachedColumns wraps clickhouse.Columns with a once-per-table
// memo. clickhouse.Columns re-parses schema.sql on every call (no
// internal cache), which would defeat the per-call savings of the
// fast path on the hot WritePixieRows route.
func getCachedColumns(table string) ([]string, error) {
	columnCacheMu.RLock()
	if cols, ok := columnCache[table]; ok {
		columnCacheMu.RUnlock()
		return cols, nil
	}
	columnCacheMu.RUnlock()

	cols, err := clickhouse.Columns(table)
	if err != nil {
		return nil, err
	}
	columnCacheMu.Lock()
	defer columnCacheMu.Unlock()
	if existing, ok := columnCache[table]; ok {
		return existing, nil
	}
	columnCache[table] = cols
	return cols, nil
}

var (
	columnCacheMu sync.RWMutex
	columnCache   = map[string][]string{}
)

// encodeBufPool reuses the bytes.Buffer the sink hands to the fast (or
// slow) encoder across WritePixieRows / Write calls. The fan-out path
// calls these on a 30-second cadence per active anomaly × per pixie
// table, so without pooling each call's underlying byte array is heap-
// allocated and then GC'd. Bench-measured benefit:
// BenchmarkEncodePixieRowsFast_Pooled_PixieShape vs unpooled.
//
// Note: the buffer's INITIAL allocation still happens (1× per Get from
// an empty pool); reuse kicks in once the pool warms. Steady-state
// allocations drop from 2 017 → ~17 per 1000-row batch.
var encodeBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// errFastEncodeUnsupported is returned by appendJSONValue when a value
// type is not in the fast-path switch. The caller (WritePixieRows)
// should fall back to encoding/json for safety.
var errFastEncodeUnsupported = errors.New("fastencode: unsupported value type")

// appendJSONValue writes v to buf as one JSON atom. Handles the value
// types pxapi produces for pixie observation rows (see
// internal/pixieapi/pixieapi.go::datumValue + internal/pixie/pixie.go
// equivalent). Unknown types return errFastEncodeUnsupported so the
// caller can fall back to encoding/json — never silently drops a row.
func appendJSONValue(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case string:
		appendJSONString(buf, x)
	case []byte:
		appendJSONString(buf, string(x))
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case int:
		appendInt(buf, int64(x))
	case int32:
		appendInt(buf, int64(x))
	case int64:
		appendInt(buf, x)
	case uint:
		appendUint(buf, uint64(x))
	case uint8:
		appendUint(buf, uint64(x))
	case uint32:
		appendUint(buf, uint64(x))
	case uint64:
		appendUint(buf, x)
	case float32:
		f := float64(x)
		// Reject NaN / +Inf / -Inf — strconv.AppendFloat emits them as
		// "NaN" / "+Inf" / "-Inf" which are invalid JSON and would
		// cause CH to reject the entire batch. errFastEncodeUnsupported
		// triggers the encoding/json fallback path, which also fails
		// on non-finite, but at the per-row granularity instead of
		// poisoning the whole batch (CodeRabbit r-#68/fastencode.go).
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return errFastEncodeUnsupported
		}
		appendFloat(buf, f)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return errFastEncodeUnsupported
		}
		appendFloat(buf, x)
	case time.Time:
		// Same format normalisePixieValue uses for the encoding/json
		// path — CH DateTime64 string input shape.
		buf.WriteByte('"')
		// AppendFormat reuses the buf's underlying bytes; no
		// intermediate string allocation.
		buf.WriteString(x.UTC().Format("2006-01-02 15:04:05.000000000"))
		buf.WriteByte('"')
	case json.Number:
		// json.Number is already decimal text; emit verbatim.
		buf.WriteString(string(x))
	default:
		return errFastEncodeUnsupported
	}
	return nil
}

func appendInt(buf *bytes.Buffer, x int64) {
	var tmp [24]byte
	buf.Write(strconv.AppendInt(tmp[:0], x, 10))
}

func appendUint(buf *bytes.Buffer, x uint64) {
	var tmp [24]byte
	buf.Write(strconv.AppendUint(tmp[:0], x, 10))
}

func appendFloat(buf *bytes.Buffer, x float64) {
	var tmp [32]byte
	buf.Write(strconv.AppendFloat(tmp[:0], x, 'g', -1, 64))
}

// appendJSONString emits s as a quoted JSON string, escaping per
// RFC 8259. Lifted from the standard library's encoding/json
// safeAppend* path; the only deviation is we don't HTML-escape (the
// sink's encoding/json path also sets SetEscapeHTML(false), so the
// outputs match byte-for-byte on safe inputs).
func appendJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if safeJSONByte(b) {
				i++
				continue
			}
			if start < i {
				buf.WriteString(s[start:i])
			}
			switch b {
			case '\\', '"':
				buf.WriteByte('\\')
				buf.WriteByte(b)
			case '\n':
				buf.WriteString(`\n`)
			case '\r':
				buf.WriteString(`\r`)
			case '\t':
				buf.WriteString(`\t`)
			default:
				// 0x00-0x1f except the explicit ones above.
				fmt.Fprintf(buf, `\u%04x`, b)
			}
			i++
			start = i
			continue
		}
		// Multi-byte rune — leave as-is (UTF-8 is valid in JSON
		// strings per RFC 8259 §7).
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	if start < len(s) {
		buf.WriteString(s[start:])
	}
	buf.WriteByte('"')
}

// safeJSONByte reports whether b can appear unescaped inside a JSON
// string. Everything 0x20..0x7e except '"' and '\\' is fine.
func safeJSONByte(b byte) bool {
	if b < 0x20 || b == '"' || b == '\\' {
		return false
	}
	return b < utf8.RuneSelf
}
