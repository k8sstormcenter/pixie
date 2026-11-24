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

package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"px.dev/pixie/src/utils"
)

// TestWorkspaceHost: the UI is served from the "work." subdomain of the cloud
// address. The address may carry a scheme or a path and may already name that
// subdomain (a self-hosted cloud's PX_CLOUD_ADDR does); the prefix is added once.
func TestWorkspaceHost(t *testing.T) {
	for _, tc := range []struct{ cloudAddr, want string }{
		{"withpixie.ai:443", "work.withpixie.ai:443"},
		{"dev.withpixie.dev", "work.dev.withpixie.dev"},
		{"work.soc.example.com:443", "work.soc.example.com:443"},
		{"https://work.soc.example.com:443", "work.soc.example.com:443"},
		{"https://withpixie.ai", "work.withpixie.ai"},
		{"https://work.soc.example.com/", "work.soc.example.com"},
		{"workshop.example.com", "work.workshop.example.com"},
	} {
		assert.Equal(t, tc.want, utils.WorkspaceHost(tc.cloudAddr), tc.cloudAddr)
	}
}
