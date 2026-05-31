/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package exporter

import (
	"context"

	"github.com/gofrs/uuid"

	"px.dev/pixie/src/e2e_test/perf_tool/pkg/metrics"
)

// Exporter handles exporting experiment results and specs to a storage backend.
type Exporter interface {
	// ExportResults consumes metrics from resultCh until it closes, then flushes.
	ExportResults(ctx context.Context, expID uuid.UUID, resultCh <-chan *metrics.ResultRow) error
	// ExportSpec writes the experiment spec for a successful experiment.
	ExportSpec(ctx context.Context, expID uuid.UUID, encodedSpec string, commitTopoOrder int) error
	// Close releases any resources held by the exporter.
	Close() error
}
