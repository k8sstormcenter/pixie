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

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAuthURLHost: the browser login goes to the cloud's "work." subdomain once,
// whatever form the cloud address takes; a self-hosted address that already
// names it opened https://work.work.<cloud>/login.
func TestAuthURLHost(t *testing.T) {
	for _, tc := range []struct{ cloudAddr, want string }{
		{"withpixie.ai:443", "https://work.withpixie.ai:443/login?local_mode=true"},
		{"work.soc.example.com:443", "https://work.soc.example.com:443/login?local_mode=true"},
		{"https://work.soc.example.com", "https://work.soc.example.com/login?local_mode=true"},
	} {
		p := &PixieCloudLogin{CloudAddr: tc.cloudAddr}
		assert.Equal(t, tc.want, p.getAuthURL().String(), tc.cloudAddr)
	}
}
