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

// Package anomaly defines the source-agnostic identity of one anomaly
// observation: a four-field Target and the deterministic AnomalyHash
// derived from it.
//
// AnomalyHash is the join key written by the operator into
// forensic_db.adaptive_attribution and joined against pixie observation
// tables on (hostname, namespace, pod, time_).
//
// The hash is workload-identity, NOT event-identity: it carries no
// timestamp and no rule id. The same workload firing N anomalies
// produces N kubescape rows, all collapsing to the same hash. This
// makes the hash a meaningful partition / join key.
package anomaly

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// AnomalyHash is the 32-hex-character (16-byte) join key derived from
// a Target. Same Target → same AnomalyHash, every time.
type AnomalyHash string

// Target is the workload-identity used for hashing. Pod and Namespace
// MAY be empty (host-pid processes outside any pod). PID + Comm are
// always required by the producer; the hash function does not enforce
// that — extraction is the place to enforce.
//
// Note: timestamp and rule id deliberately not in the hash. Different
// rule firings on the same workload share the same hash; the time
// dimension is carried separately in the attribution row's
// (t_start, t_end) interval.
type Target struct {
	PID       uint64
	Comm      string
	Pod       string // may be empty
	Namespace string // may be empty
}

// Hash returns the deterministic 32-hex-character AnomalyHash for the
// given Target. SHA-256 over a length-prefixed canonical encoding of
// the four identity fields, truncated to the leading 16 bytes
// (32 hex chars). 128 collision bits suffice for the workload
// cardinality envelope.
//
// The encoding is: PID as big-endian uint64, followed by each string
// as uint32-LE length || bytes. Length prefixing is collision-safe
// across delimiter-bearing or empty inputs (a plain ":"-join is not —
// e.g. {Pod:"a:b", NS:""} would collide with {Pod:"a", NS:"b:"}).
func Hash(t Target) AnomalyHash {
	h := sha256.New()
	var pidBuf [8]byte
	binary.BigEndian.PutUint64(pidBuf[:], t.PID)
	h.Write(pidBuf[:])
	writeLenPrefixed(h, t.Comm)
	writeLenPrefixed(h, t.Pod)
	writeLenPrefixed(h, t.Namespace)
	sum := h.Sum(nil)
	return AnomalyHash(hex.EncodeToString(sum[:16]))
}

// writeLenPrefixed writes uint32-LE length followed by the raw bytes.
// 4 GiB per field is well above any realistic Pod/Namespace/Comm size.
func writeLenPrefixed(h interface{ Write([]byte) (int, error) }, s string) {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(s)))
	_, _ = h.Write(lenBuf[:])
	_, _ = h.Write([]byte(s))
}
