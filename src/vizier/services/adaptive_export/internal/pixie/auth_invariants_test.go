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

package pixie

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"
)

// TestCloudClientAuthIsPixieAPIKeyHeader pins the ONE cloud-auth mechanism: the
// plugin client authenticates via the canonical "pixie-api-key" gRPC metadata
// header — what the pixie cloud api-server trusts for external clients — and
// never a bearer/JWT or a hand-rolled scheme. (Cluster service JWTs are rejected
// by the cloud api for this surface; see NewClient's doc comment.) This must not
// drift into a second mechanism.
func TestCloudClientAuthIsPixieAPIKeyHeader(t *testing.T) {
	c, err := NewClient(context.Background(), "test-key-123", "cloud.example.org:443")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	md, ok := metadata.FromOutgoingContext(c.ctx)
	if !ok {
		t.Fatal("NewClient attached no outgoing gRPC metadata — no auth header set")
	}
	if got := md.Get("pixie-api-key"); len(got) != 1 || got[0] != "test-key-123" {
		t.Errorf(`cloud auth header pixie-api-key = %v, want ["test-key-123"]`, got)
	}
	// The cloud client must NOT use the in-cluster bearer/JWT header — that path
	// is a different, non-interchangeable mechanism (jwtutils service JWT).
	if got := md.Get("authorization"); len(got) != 0 {
		t.Errorf("cloud client must not set an authorization/bearer header (that is the in-cluster JWT path), got %v", got)
	}
}

// TestCloudClientRejectsEmptyKey — no silent unauthenticated fallback: an empty
// key is a hard error, never a proceed-anyway.
func TestCloudClientRejectsEmptyKey(t *testing.T) {
	if _, err := NewClient(context.Background(), "", "cloud.example.org:443"); err == nil {
		t.Fatal("NewClient(empty key) must error, not proceed unauthenticated")
	}
}

// TestNoAuthReinvention walks the whole adaptive_export source tree and enforces
// "one auth method per context, no wheels reinvented":
//
//   - Every JWT is minted/verified through the SHARED pixie lib
//     px.dev/pixie/src/shared/services/utils (jwtutils) — never a hand-rolled
//     golang-jwt/dgrijalva/jwt.New/SignedString/jwt.Parse.
//   - The "pixie-api-key" cloud header lives ONLY in internal/pixie (the single
//     cloud plugin client), never sprinkled across packages.
//
// This is the guardrail behind the recurring "use the API-based auth, don't
// reinvent it" rule: the AE has exactly two auth surfaces — cloud=pixie-api-key,
// in-cluster=jwtutils service JWT — and both go through canonical code.
func TestNoAuthReinvention(t *testing.T) {
	root := aeSourceRoot(t)

	// Hand-rolled JWT crypto is banned — jwtutils is the only sanctioned path.
	bannedJWT := []string{"golang-jwt", "dgrijalva/jwt", "jwt.New(", ".SignedString(", "jwt.Parse("}

	apiKeyHeaderIn := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		rel, _ := filepath.Rel(root, path)
		for _, banned := range bannedJWT {
			if strings.Contains(src, banned) {
				t.Errorf("%s uses %q — JWTs must go through the shared jwtutils lib (GenerateJWTForService / SignJWTClaims / ParseToken), no hand-rolled crypto", rel, banned)
			}
		}
		// Match the header string literal only (not doc-comment prose).
		if strings.Contains(src, `"pixie-api-key"`) {
			apiKeyHeaderIn[rel] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk AE source: %v", err)
	}

	for f := range apiKeyHeaderIn {
		if filepath.Dir(f) != filepath.Join("internal", "pixie") {
			t.Errorf(`the "pixie-api-key" cloud auth header appears in %s — it must be centralized in internal/pixie (one cloud auth surface)`, f)
		}
	}
}

// aeSourceRoot returns the adaptive_export package root (the dir containing this
// test's package), resolved from the compiled test's own file path.
func aeSourceRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller unavailable — cannot locate source tree")
	}
	// .../adaptive_export/internal/pixie/auth_invariants_test.go -> .../adaptive_export
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	// In a sandboxed build (bazel) the source tree is not laid out on disk at this
	// path — the source-walk guard is a `go test` check; skip it there rather than
	// fail. The behavioral auth tests above still run everywhere.
	if filepath.Base(root) != "adaptive_export" {
		t.Skipf("source tree not at %q (sandboxed build) — skipping source-walk guard", root)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "main.go")); err != nil {
		t.Skipf("AE source tree not readable at %q (sandboxed build) — skipping source-walk guard", root)
	}
	return root
}
