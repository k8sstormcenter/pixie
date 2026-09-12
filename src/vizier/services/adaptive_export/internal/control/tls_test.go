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

package control

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jwtutils "px.dev/pixie/src/shared/services/utils"
)

// serveTLS starts the control server over TLS on 127.0.0.1:0 using the given
// *tls.Config and returns the base https URL + a shutdown func.
func serveTLS(t *testing.T, cfg *tls.Config, srv *Server) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: srv.Handler(), TLSConfig: cfg}
	go func() { _ = httpSrv.ServeTLS(ln, "", "") }()
	return "https://" + ln.Addr().String(), func() { _ = httpSrv.Close() }
}

func skipVerifyClient() *http.Client {
	return &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // test: dx skip-verifies the in-cluster self-signed cert
	}
}

// TestTLSConfigSelfSigned: with no mounted cert files, TLSConfig self-generates
// an in-memory cert (the default secure path when /certs is absent).
func TestTLSConfigSelfSigned(t *testing.T) {
	cfg, selfSigned, err := TLSConfig("/no/such/cert.crt", "/no/such/key.key", "some-pod")
	if err != nil {
		t.Fatalf("TLSConfig self-gen: %v", err)
	}
	if !selfSigned {
		t.Fatal("expected selfSigned=true when cert files are absent")
	}
	if cfg == nil || len(cfg.Certificates) != 1 {
		t.Fatalf("expected exactly one in-memory certificate, got %+v", cfg)
	}
}

// TestTLSServesHealthz: the server serves TLS by default (self-gen path) and a
// TLS client can reach /healthz. This is T1's "no cleartext by default".
func TestTLSServesHealthz(t *testing.T) {
	cfg, selfSigned, err := TLSConfig("", "", "localhost")
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if !selfSigned {
		t.Fatal("expected self-signed cert")
	}
	base, stop := serveTLS(t, cfg, New(&fakeExporter{}, nil))
	defer stop()

	resp, err := skipVerifyClient().Get(base + "/healthz")
	if err != nil {
		t.Fatalf("TLS GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz over TLS = %d, want 200", resp.StatusCode)
	}
}

// TestTLSRejectsUnauthenticated: over TLS with a signing key configured, an
// unauthenticated control request is rejected (401). This is T1's "requires
// the bearer JWT when a signing key is present" — verified end-to-end on the
// real TLS listener, not just the handler.
func TestTLSRejectsUnauthenticated(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	srv := New(&fakeExporter{}, nil)
	srv.SetAuth(key, "vizier")

	cfg, _, err := TLSConfig("", "", "localhost")
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	base, stop := serveTLS(t, cfg, srv)
	defer stop()
	client := skipVerifyClient()

	// No bearer → 401.
	resp, err := client.Post(base+"/export/start", "application/json", strings.NewReader(`{"pod":"p","t_end":1}`))
	if err != nil {
		t.Fatalf("TLS POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated over TLS = %d, want 401", resp.StatusCode)
	}

	// Valid bearer → not 401.
	good, err := jwtutils.SignJWTClaims(jwtutils.GenerateJWTForService("dx", "vizier"), key)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/export/start", strings.NewReader(`{"namespace":"n","pod":"p","t_end":1}`))
	req.Header.Set("Authorization", "Bearer "+good)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("TLS POST authed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusUnauthorized {
		t.Fatal("valid bearer wrongly rejected over TLS")
	}
}

// TestTLSConfigMountedCert: when cert+key files exist, TLSConfig loads them
// (selfSigned=false) — the /certs/server.{crt,key} shared-cert path.
func TestTLSConfigMountedCert(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	writePEMKeypair(t, certPath, keyPath)

	cfg, selfSigned, err := TLSConfig(certPath, keyPath, "localhost")
	if err != nil {
		t.Fatalf("TLSConfig mounted: %v", err)
	}
	if selfSigned {
		t.Fatal("expected selfSigned=false when cert files exist")
	}
	if cfg == nil || len(cfg.Certificates) != 1 {
		t.Fatalf("expected one loaded certificate, got %+v", cfg)
	}
}

// TestPlaintextPathServes: the CONTROL_INSECURE opt-out serves plain HTTP. This
// mirrors main.go's insecure branch (httpSrv.ListenAndServe with the same
// handler) — a plaintext client reaches /healthz.
func TestPlaintextPathServes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: New(&fakeExporter{}, nil).Handler()}
	go func() { _ = httpSrv.Serve(ln) }()
	defer httpSrv.Close()

	resp, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("plaintext GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plaintext healthz = %d, want 200", resp.StatusCode)
	}
}

// writePEMKeypair mints a self-signed cert via the same helper and writes it as
// PEM cert+key files, so the mounted-cert load path can be exercised.
func writePEMKeypair(t *testing.T, certPath, keyPath string) {
	t.Helper()
	cert, err := selfSignedCert("localhost")
	if err != nil {
		t.Fatalf("selfSignedCert: %v", err)
	}
	certPEM, keyPEM, err := certToPEM(cert)
	if err != nil {
		t.Fatalf("certToPEM: %v", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}
