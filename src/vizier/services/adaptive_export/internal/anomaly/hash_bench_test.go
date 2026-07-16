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

package anomaly

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// anomaly.Hash sits on the HOTTEST path in AE: it runs for every
// kubescape event the trigger fans into the controller. At ~1k
// events/sec on a busy cluster, that's 1k Hash() calls/sec PLUS the
// kubescape extraction allocations on each upstream Row.
//
// These benchmarks establish the per-call cost. The fields are sized
// to match real workloads: Pod is the standard 51-char k8s name,
// Namespace ~20 chars, Comm 16 chars (max kernel limit).

func benchTarget(i int) Target {
	return Target{
		PID:       uint64(1000 + i),
		Comm:      "java",
		Pod:       "backend-vulnerable-779cd9d765-mxr8t-replica-shard-9",
		Namespace: "svc-poc-production",
	}
}

func BenchmarkHash(b *testing.B) {
	t := benchTarget(0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Hash(t)
	}
}

// BenchmarkHash_Unique varies the PID each iteration. Establishes
// what the hash costs when the inputs aren't shared across calls (so
// no CPU caching shortcut on the input bytes).
func BenchmarkHash_Unique(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Hash(benchTarget(i))
	}
}

// BenchmarkHash_LongNamespace pumps the fields to their realistic
// upper bound (256-char Pod, 63-char namespace per k8s DNS limits).
// Shows whether the SHA-256 step or the writeLenPrefixed allocations
// dominate.
func BenchmarkHash_LongFields(b *testing.B) {
	t := Target{
		PID:       12345,
		Comm:      "very-long-process-name-near-kernel-limit-16chrs!",
		Pod:       "extremely-long-statefulset-pod-name-with-replica-suffix-and-shard-suffix-pushing-the-k8s-253-char-dns-limit-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Namespace: "production-tenant-namespace-63-chars-aaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Hash(t)
	}
}

// BenchmarkHash_Parallel measures contention under GOMAXPROCS
// goroutines computing hashes in parallel. AE on a busy cluster has
// 11 BatchWriter + 11 TableScanner streaming goroutines plus the
// controller fan-out; if Hash's sha256.New() or its hex.EncodeToString
// hit a shared allocator pool, parallel speedup will collapse.
func BenchmarkHash_Parallel(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	var i atomic.Uint64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = Hash(benchTarget(int(i.Add(1))))
		}
	})
}

// BenchmarkHash_KubescapeReplay simulates the trigger-controller
// fan-out: drain a batch of 10k events (the configured PollLimit
// default) by hashing each one's target. Measures the per-batch
// hash cost — call once per trigger poll on a busy cluster.
func BenchmarkHash_KubescapeReplay(b *testing.B) {
	const batch = 10_000
	targets := make([]Target, batch)
	for i := range targets {
		targets[i] = Target{
			PID:       uint64(1000 + i),
			Comm:      fmt.Sprintf("proc-%d", i%64),
			Pod:       fmt.Sprintf("backend-%d-7bdf99c466-replica-%d", i%32, i%4),
			Namespace: fmt.Sprintf("ns-%d", i%8),
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		for j := range targets {
			_ = Hash(targets[j])
		}
	}
}
