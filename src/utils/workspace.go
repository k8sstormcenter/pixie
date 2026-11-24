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

package utils

import "strings"

// WorkspaceHost is the host the Pixie UI is served from: the "work." subdomain
// of the cloud address. The address may carry a scheme or a trailing path, and
// may already name that subdomain (a self-hosted cloud's address often does);
// neither ends up in the host, and the prefix is added only once.
func WorkspaceHost(cloudAddr string) string {
	host := cloudAddr
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+len("://"):]
	}
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if strings.HasPrefix(host, "work.") {
		return host
	}
	return "work." + host
}
