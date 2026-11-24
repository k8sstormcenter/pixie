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

package script_test

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"px.dev/pixie/src/api/proto/vispb"
	"px.dev/pixie/src/utils/script"
)

// TestLiveViewLinkHost: the live view is served from the "work." subdomain of the
// cloud. A cloud address that already names that subdomain (as a self-hosted
// cloud's PX_CLOUD_ADDR does: work.soc.example.com:443) or carries a scheme must
// not have the prefix added again: the link printed "https://work.work.soc…".
func TestLiveViewLinkHost(t *testing.T) {
	cluster := "default_ea47190b"
	for _, tc := range []struct {
		cloudAddr, want string
	}{
		{"withpixie.ai:443", "https://work.withpixie.ai:443/live/clusters/default_ea47190b?script=px%2Fcluster"},
		{"dev.withpixie.dev", "https://work.dev.withpixie.dev/live/clusters/default_ea47190b?script=px%2Fcluster"},
		{"work.soc.example.com:443", "https://work.soc.example.com:443/live/clusters/default_ea47190b?script=px%2Fcluster"},
		{"https://work.soc.example.com:443", "https://work.soc.example.com:443/live/clusters/default_ea47190b?script=px%2Fcluster"},
		{"https://withpixie.ai", "https://work.withpixie.ai/live/clusters/default_ea47190b?script=px%2Fcluster"},
		{"https://work.soc.example.com/", "https://work.soc.example.com/live/clusters/default_ea47190b?script=px%2Fcluster"},
		{"workshop.example.com", "https://work.workshop.example.com/live/clusters/default_ea47190b?script=px%2Fcluster"},
	} {
		t.Run(tc.cloudAddr, func(t *testing.T) {
			viper.Set("cloud_addr", tc.cloudAddr)
			defer viper.Set("cloud_addr", "")
			s := script.ExecutableScript{ScriptName: "px/cluster", Vis: &vispb.Vis{}}
			assert.Equal(t, tc.want, s.LiveViewLink(&cluster))
		})
	}
}
