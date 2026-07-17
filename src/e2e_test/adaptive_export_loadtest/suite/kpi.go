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

package aeloadsuite

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The KPIs the suite asserts. Each former measurement script collapses to one
// helper here, so every fixture declares its pass/fail in the same vocabulary.
//
//   Reproducibility  — a metric is identical across all reps (was stats.py std=0)
//   Reconcile        — read == wrote == ClickHouse count (was exp_row_reconcile.sh)
//
// Firehose-vs-steered volume reduction is a measured OUTCOME the data-plane test
// reports, not an asserted threshold: there is no correct fixed percentage, so
// gating on one would be arbitrary.
// NFR and WriteDuration KPIs attach to the data-plane fixtures (§suite_test.go).

// RequireExact asserts a single measured value equals want.
func RequireExact(t *testing.T, label string, got, want int) {
	t.Helper()
	require.Equal(t, want, got, "%s: got %d, want %d", label, got, want)
}

// RequireReproducible asserts every sample is identical (one distinct value),
// which is the std=0 / CV=0 reproducibility criterion. want is the expected
// value; all samples must equal it.
func RequireReproducible(t *testing.T, label string, samples []int, want int) {
	t.Helper()
	require.NotEmpty(t, samples, "%s: no samples", label)
	for rep, got := range samples {
		require.Equalf(t, want, got, "%s: rep %d = %d, want a single distinct value %d", label, rep, got, want)
	}
}

// RequireReconcile asserts the no-loss invariant: everything AE read from Pixie
// it wrote, and everything it wrote is present in ClickHouse.
func RequireReconcile(t *testing.T, table string, read, wrote, ch int) {
	t.Helper()
	require.Equalf(t, read, wrote, "reconcile[%s]: wrote %d != read %d (write path lost rows)", table, wrote, read)
	require.Equalf(t, wrote, ch, "reconcile[%s]: clickhouse %d != wrote %d (sink lost rows)", table, ch, wrote)
}
