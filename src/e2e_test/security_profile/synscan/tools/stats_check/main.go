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

// stats_check — queries every Stirling table that could carry a
// SYN-scan signal and prints row counts plus the first few rows.
// Use it post-scan to score whether Pixie saw anything.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/api/go/pxapi/types"
)

type mux struct {
	out  *os.File
	name string
	n    int
}

func (m *mux) AcceptTable(_ context.Context, md types.TableMetadata) (pxapi.TableRecordHandler, error) {
	fmt.Fprintf(m.out, "== %s ==\n", m.name)
	return m, nil
}
func (m *mux) HandleInit(_ context.Context, _ types.TableMetadata) error { return nil }
func (m *mux) HandleRecord(_ context.Context, r *types.Record) error {
	if m.n < 10 {
		s := ""
		for _, c := range r.TableMetadata.ColInfo {
			d := r.GetDatum(c.Name)
			if d != nil {
				s += c.Name + "=" + d.String() + " "
			}
		}
		fmt.Fprintln(m.out, s)
	}
	m.n++
	return nil
}

func (m *mux) HandleDone(_ context.Context) error {
	fmt.Fprintf(m.out, "  total_rows=%d\n\n", m.n)
	return nil
}

func main() {
	var (
		addr      = flag.String("addr", "127.0.0.1:12345", "PEM direct addr")
		startSec  = flag.Int("start", -240, "start_time seconds before now")
		scannerIP = flag.String("scanner_ip", "", "scanner pod IP (filters rows whose remote_addr matches it — captures inbound-to-scanner)")
		targetIP  = flag.String("target_ip", "", "scan target IP")
		portMin   = flag.Int("port_min", 20, "lower port of scan range")
		portMax   = flag.Int("port_max", 100, "upper port of scan range")
	)
	flag.Parse()
	if *targetIP == "" {
		die("-target_ip required")
	}
	ctx := context.Background()
	c, err := pxapi.NewClient(ctx, pxapi.WithDirectAddr(*addr), pxapi.WithDirectCredsInsecure())
	if err != nil {
		die("NewClient: %v", err)
	}
	v, err := c.NewVizierClient(ctx, "")
	if err != nil {
		die("NewVizierClient: %v", err)
	}

	queries := []struct{ name, pxl string }{
		{
			"conn_stats anything to target",
			fmt.Sprintf(`import px
df = px.DataFrame(table='conn_stats', start_time='%ds')
df = df[df.remote_addr == '%s']
px.display(df[['time_','upid','remote_addr','remote_port','protocol','ssl','conn_open','conn_close','bytes_sent','bytes_recv']], 'conn_stats')
`, *startSec, *targetIP),
		},
		{
			"conn_stats scan-port range only",
			fmt.Sprintf(`import px
df = px.DataFrame(table='conn_stats', start_time='%ds')
df = df[df.remote_addr == '%s']
df = df[df.remote_port >= %d]
df = df[df.remote_port <= %d]
px.display(df[['time_','upid','remote_addr','remote_port','protocol','conn_open','conn_close']], 'conn_stats')
`, *startSec, *targetIP, *portMin, *portMax),
		},
		{
			"http_events to target",
			fmt.Sprintf(`import px
df = px.DataFrame(table='http_events', start_time='%ds')
df = df[df.remote_addr == '%s']
px.display(df.head(20), 'http_events')
`, *startSec, *targetIP),
		},
		{
			"dns_events to target",
			fmt.Sprintf(`import px
df = px.DataFrame(table='dns_events', start_time='%ds')
df = df[df.remote_addr == '%s']
px.display(df.head(20), 'dns_events')
`, *startSec, *targetIP),
		},
		{
			"network_stats first 30",
			fmt.Sprintf(`import px
df = px.DataFrame(table='network_stats', start_time='%ds')
px.display(df.head(30), 'network_stats')
`, *startSec),
		},
	}
	if *scannerIP != "" {
		queries = append(queries, struct{ name, pxl string }{
			"conn_stats remote_addr == scanner_ip (inbound-to-scanner traffic)",
			fmt.Sprintf(`import px
df = px.DataFrame(table='conn_stats', start_time='%ds')
df = df[df.remote_addr == '%s']
px.display(df.head(30), 'conn_stats')
`, *startSec, *scannerIP),
		})
	}
	for _, q := range queries {
		m := &mux{out: os.Stdout, name: q.name}
		rs, err := v.ExecuteScript(ctx, q.pxl, m)
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "%s: %v\n", q.name, err)
			continue
		}
		if rs != nil {
			_ = rs.Stream()
			_ = rs.Close()
		}
	}
}
func die(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }
