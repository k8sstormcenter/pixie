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

// dnsprobe — fires N DNS A-lookups at a controlled rate against a fixed
// resolver and writes a CSV manifest of (ts_ns, query_name) tuples to
// -out. Each name carries a per-run random salt so the harness can
// match captured rows back to this exact run with zero risk of
// cross-talk between sweeps.
//
// One lookup per name, using net.LookupNetIP(ip4) on the FQDN with a
// trailing dot — same shape `cleanloadgen` in adaptive_export_loadtest
// uses, so behaviour is consistent across the e2e tests:
//   - one A-query per name
//   - search-domain expansion suppressed by the trailing dot
//   - NXDOMAIN counts as a captured exchange (one question + one
//     answer over the wire), so the resolver doesn't need to "find" the
//     names — it just needs to answer them.
//
// Workers default to 32 so the harness can drive bursts faster than a
// single goroutine's blocking lookup would allow.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	var (
		n        = flag.Int("n", 1000, "number of DNS queries to fire")
		ratePS   = flag.Int("rate", 1000, "queries per second (steady)")
		workers  = flag.Int("workers", 32, "concurrent lookup goroutines")
		domain   = flag.String("domain", "secprof.invalid", "parent zone — names are <salt>-<i>.<domain>.")
		resolver = flag.String("resolver", "1.1.1.1:53", "UDP DNS resolver to direct queries at")
		out      = flag.String("out", "/tmp/dnsprobe-sent.csv", "CSV manifest output")
	)
	flag.Parse()

	salt := mustSalt()
	fmt.Fprintf(os.Stderr, "dnsprobe: n=%d rate=%d/s workers=%d resolver=%s salt=%s\n",
		*n, *ratePS, *workers, *resolver, salt)

	f, err := os.Create(*out)
	if err != nil {
		die("create -out: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, "ts_ns,query_name,err")

	// Pin the resolver — bypasses /etc/resolv.conf so the cluster's
	// nameservice doesn't perturb the result.
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, "udp", *resolver)
		},
	}

	jobs := make(chan int, *n)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var posted atomic.Int64

	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				name := fmt.Sprintf("%s-%d.%s.", salt, i, *domain)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				ts := time.Now().UnixNano()
				_, lerr := r.LookupNetIP(ctx, "ip4", name)
				cancel()
				errStr := ""
				// NXDOMAIN is the EXPECTED outcome: it still produces
				// one captured DNS query+response on the wire, which
				// is what we want to count.
				if lerr != nil && !isExpectedNXDomain(lerr) {
					errStr = lerr.Error()
				}
				mu.Lock()
				_, _ = fmt.Fprintf(f, "%d,%s,%s\n", ts, name, errStr)
				mu.Unlock()
				posted.Add(1)
			}
		}()
	}

	// Steady-rate pacer — emit a job every 1/rate seconds.
	tickEvery := time.Second / time.Duration(*ratePS)
	if tickEvery <= 0 {
		tickEvery = time.Microsecond
	}
	start := time.Now()
	for i := 0; i < *n; i++ {
		jobs <- i
		target := start.Add(time.Duration(i+1) * tickEvery)
		if d := time.Until(target); d > 0 {
			time.Sleep(d)
		}
	}
	close(jobs)
	wg.Wait()

	dur := time.Since(start)
	fmt.Fprintf(os.Stderr, "dnsprobe: posted=%d wall=%.2fs rate=%.0f/s csv=%s salt=%s\n",
		posted.Load(), dur.Seconds(), float64(posted.Load())/dur.Seconds(), *out, salt)
	// Salt on stdout (not stderr) so the harness can capture it without
	// muddying the logs.
	fmt.Println(salt)
}

func mustSalt() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		die("salt: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func isExpectedNXDomain(err error) bool {
	// net.DNSError carries IsNotFound for NXDOMAIN.
	if e, ok := err.(*net.DNSError); ok && e.IsNotFound {
		return true
	}
	return false
}

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "dnsprobe: "+f+"\n", a...)
	os.Exit(1)
}
