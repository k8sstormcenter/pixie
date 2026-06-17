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

// dnsverify — pulls dns_events from a Pixie PEM via pxapi and emits a
// CSV of (ts_ns, query, upid) tuples for any row whose query name
// carries the run-salt printed by dnsprobe. The harness joins the
// dnsprobe -out manifest against this CSV to compute coverage.
//
// Direct mode is the default: it talks to a standalone_pem (or pemdq8)
// directly on its query port. Use -direct=false to go through the
// vizier broker via WithCloudAddr.

package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/api/go/pxapi/types"
)

func main() {
	var (
		addr     = flag.String("addr", "127.0.0.1:12345", "PEM gRPC endpoint (HOST_IP:port for standalone_pem)")
		direct   = flag.Bool("direct", true, "use WithDirectAddr instead of WithCloudAddr")
		jwt      = flag.String("jwt", "", "bearer JWT to attach (if blank, no auth)")
		cluster  = flag.String("cluster", "", "cluster UUID for WithCloudAddr")
		salt     = flag.String("salt", "", "run-salt printed by dnsprobe (required)")
		lookback = flag.Int("lookback", 120, "PxL start_time lookback in seconds")
		out      = flag.String("out", "/tmp/dnsverify-seen.csv", "CSV output")
	)
	flag.Parse()
	if *salt == "" {
		die("-salt is required")
	}
	f, err := os.Create(*out)
	if err != nil {
		die("create -out: %v", err)
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"ts_ns", "query_name", "upid"})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := []pxapi.ClientOption{}
	if *direct {
		// Standalone_pem listens with self-signed cert; PX_DISABLE_TLS=1
		// flips insecureSkipVerify for cluster.local-style addrs, which
		// is what WithDirectCredsInsecure exposes for plain-text dial.
		opts = append(opts, pxapi.WithDirectAddr(*addr), pxapi.WithDirectCredsInsecure())
	} else {
		opts = append(opts,
			pxapi.WithCloudAddr(*addr),
			pxapi.WithDisableTLSVerification(*addr),
		)
	}
	if *jwt != "" {
		opts = append(opts, pxapi.WithBearerAuth(*jwt))
	}
	client, err := pxapi.NewClient(ctx, opts...)
	if err != nil {
		die("NewClient: %v", err)
	}
	vz, err := client.NewVizierClient(ctx, *cluster)
	if err != nil {
		die("NewVizierClient: %v", err)
	}

	pxl := fmt.Sprintf(`import px
df = px.DataFrame(table='dns_events', start_time='-%ds')
df.ts_ns = df.time_
df.upid = px.upid_to_string(df.upid)
df = df[px.contains(df.req_body, '%s')]
px.display(df[['ts_ns','req_body','upid']], 'dns_events')
`, *lookback, *salt)

	mux := &dnsMux{out: w}
	rs, err := vz.ExecuteScript(ctx, pxl, mux)
	if err != nil && err != io.EOF {
		die("ExecuteScript: %v", err)
	}
	defer func() { _ = rs.Close() }()
	if err := rs.Stream(); err != nil && err != io.EOF {
		die("Stream: %v", err)
	}
	w.Flush()
	fmt.Fprintf(os.Stderr, "dnsverify: salt=%s captured=%d csv=%s\n", *salt, mux.handler.n, *out)
}

type dnsMux struct {
	out     *csv.Writer
	handler dnsHandler
}

func (m *dnsMux) AcceptTable(_ context.Context, md types.TableMetadata) (pxapi.TableRecordHandler, error) {
	m.handler.md = &md
	m.handler.out = m.out
	return &m.handler, nil
}

type dnsHandler struct {
	mu  sync.Mutex
	md  *types.TableMetadata
	out *csv.Writer
	n   int
}

func (h *dnsHandler) HandleInit(_ context.Context, _ types.TableMetadata) error { return nil }

func (h *dnsHandler) HandleRecord(_ context.Context, r *types.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	get := func(name string) string {
		d := r.GetDatum(name)
		if d == nil {
			return ""
		}
		return strings.Trim(d.String(), `"`)
	}
	_ = h.out.Write([]string{get("ts_ns"), get("req_body"), get("upid")})
	h.n++
	return nil
}

func (h *dnsHandler) HandleDone(_ context.Context) error { return nil }

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "dnsverify: "+f+"\n", a...)
	os.Exit(1)
}
