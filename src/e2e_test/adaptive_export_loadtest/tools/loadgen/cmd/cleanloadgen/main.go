// cleanloadgen — a deterministic, "clean-cut" traffic generator for the
// adaptive_export (AE) live data-plane load-tests.
//
// It is the OPPOSITE of a fuzzer: its only job is to emit an EXACTLY known
// number of HTTP, DNS and PostgreSQL operations against fixed sinks, inside a
// single sealed time band [B0,B1], and emit nothing else over the network. The
// counts it prints are the ground-truth oracle the AE assertions compare
// forensic_db row deltas against — no fabricated numbers anywhere.
//
// Determinism rules baked in (see the load-test design notes):
//   - HTTP: one NEW TCP connection per request (DisableKeepAlives) so both
//     http_events AND conn_stats counts are a function of HTTP_N. Every request
//     MUST return 2xx or the process exits non-zero (the rep is discarded, not
//     silently mis-counted).
//   - DNS: exactly ONE A-query per name via LookupNetIP(ip4) on a FQDN with a
//     trailing dot (suppresses /etc/resolv.conf search-domain expansion under
//     ndots:5) → dns_events == DNS_N. Names need not resolve; an NXDOMAIN is
//     still one captured query/response, so NXDOMAIN is not treated as failure.
//   - PGSQL: a single connection runs PGSQL_N separate `SELECT 1` statements →
//     pgsql_events == PGSQL_N.
//   - HTTP/PG endpoints are passed as IP:port (HTTP_ADDR / PG_ADDR), never DNS
//     names, so resolving the sinks themselves cannot pollute the DNS count.
//
// After firing, the process prints a one-line JSON manifest, emits the sentinel
// AELOAD_FIRED, then HOLDS (sleeps until SIGTERM). Holding keeps the pod — and
// therefore its upid — alive so Pixie's upid_to_pod_name can still resolve it
// when AE queries the window AFTER the kubescape fixture is injected. The
// harness deletes the pod once the rep is measured.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

type manifest struct {
	HTTP       int    `json:"http"`         // http_events expected
	DNS        int    `json:"dns"`          // dns_events expected (A queries)
	PGSQL      int    `json:"pgsql"`        // pgsql_events expected
	ConnTCPEst int    `json:"conn_tcp_est"` // conn_stats TCP rows expected (tolerance gate)
	B0         int64  `json:"b0"`           // band start, unix nanos (node clock == Pixie time_)
	B1         int64  `json:"b1"`           // band end, unix nanos
	B0ISO      string `json:"b0_iso"`
	B1ISO      string `json:"b1_iso"`
	Pod        string `json:"pod"`
	Namespace  string `json:"namespace"`
	Node       string `json:"node"`
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		fatalf("env %s=%q is not an integer", k, v)
	}
	return def
}

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "cleanloadgen: "+format+"\n", a...)
	os.Exit(1)
}

func mustIPPort(k string) string {
	v := os.Getenv(k)
	if v == "" {
		fatalf("%s is required (IP:port, never a DNS name — see design)", k)
	}
	host, _, err := net.SplitHostPort(v)
	if err != nil {
		fatalf("%s=%q is not host:port: %v", k, v, err)
	}
	if net.ParseIP(host) == nil {
		fatalf("%s host %q must be a literal IP, not a name, so it cannot add DNS events", k, host)
	}
	return v
}

func main() {
	var (
		httpN     = envInt("HTTP_N", 100)
		dnsN      = envInt("DNS_N", 100)
		pgN       = envInt("PGSQL_N", 100)
		httpAddr  = mustIPPort("HTTP_ADDR") // e.g. 10.43.0.10:8080
		httpPath  = envStr("HTTP_PATH", "/ping")
		dnsBase   = envStr("DNS_BASE", "t-%d.aeload.svc.cluster.local.") // trailing dot = FQDN
		settlePre = time.Duration(envInt("SETTLE_PRE_MS", 1500)) * time.Millisecond
	)
	// PG is optional (PGSQL_N may be 0 or PG_ADDR unset).
	pgAddr := os.Getenv("PG_ADDR")
	if pgN > 0 {
		pgAddr = mustIPPort("PG_ADDR")
	}

	// Let the pod's networking settle and the upid register before the band
	// opens, so no stray startup traffic lands inside [B0,B1].
	time.Sleep(settlePre)

	b0 := time.Now()

	// ---- HTTP: HTTP_N requests, new connection each ----
	for i := 0; i < httpN; i++ {
		// Fresh transport per request guarantees a new TCP connection.
		tr := &http.Transport{DisableKeepAlives: true}
		cl := &http.Client{Transport: tr, Timeout: 5 * time.Second}
		url := "http://" + httpAddr + httpPath
		resp, err := cl.Get(url)
		if err != nil {
			fatalf("http request %d/%d to %s failed: %v", i+1, httpN, url, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			fatalf("http request %d/%d to %s: status %d (need 2xx)", i+1, httpN, url, resp.StatusCode)
		}
		tr.CloseIdleConnections()
	}

	// ---- DNS: DNS_N distinct names, exactly one A query each ----
	res := &net.Resolver{PreferGo: true}
	for i := 0; i < dnsN; i++ {
		name := fmt.Sprintf(dnsBase, i)
		if !strings.HasSuffix(name, ".") {
			fatalf("DNS_BASE must yield an FQDN ending in '.' to suppress search expansion; got %q", name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// ip4 → a single A query. NXDOMAIN is fine: the query/response is still
		// one captured dns_event. Any OTHER error (timeout) means the query may
		// not have completed deterministically → fail the rep.
		_, err := res.LookupNetIP(ctx, "ip4", name)
		cancel()
		if err != nil && !isNXDomain(err) {
			fatalf("dns lookup %d/%d for %s failed non-NXDOMAIN: %v", i+1, dnsN, name, err)
		}
	}

	// ---- PGSQL: PGSQL_N statements over one connection ----
	if pgN > 0 {
		host, port, _ := net.SplitHostPort(pgAddr)
		dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=5",
			host, port,
			envStr("PG_USER", "postgres"), envStr("PG_PASSWORD", "postgres"), envStr("PG_DB", "postgres"))
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			fatalf("pg open: %v", err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		for i := 0; i < pgN; i++ {
			var one int
			if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
				fatalf("pg query %d/%d failed: %v", i+1, pgN, err)
			}
		}
		db.Close()
	}

	b1 := time.Now()

	m := manifest{
		HTTP:       httpN,
		DNS:        dnsN,
		PGSQL:      pgN,
		ConnTCPEst: httpN + boolToInt(pgN > 0), // HTTP_N new conns + 1 pg conn
		B0:         b0.UnixNano(),
		B1:         b1.UnixNano(),
		B0ISO:      b0.UTC().Format(time.RFC3339Nano),
		B1ISO:      b1.UTC().Format(time.RFC3339Nano),
		Pod:        envStr("POD_NAME", os.Getenv("HOSTNAME")),
		Namespace:  envStr("POD_NAMESPACE", "aeload"),
		Node:       envStr("NODE_NAME", ""),
	}
	out, _ := json.Marshal(m)
	fmt.Printf("AELOAD_MANIFEST %s\n", out)
	fmt.Println("AELOAD_FIRED")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	// SUSTAIN: after the exact counted band, optionally keep a low continuous
	// HTTP trickle for SUSTAIN_SEC. A FRESH pod's traffic is often missed because
	// Pixie/Stirling's eBPF attaches to the new process only after a scan cycle —
	// so a one-shot band fires before capture begins (the "0 for freshly-flagged
	// pods" symptom). A trickle keeps the pod observable for the whole window, so
	// Pixie captures it once attached. Used by the sustained / "does AE keep
	// writing until t_end" RCA (E8-data). For exact-count tests (E5) leave
	// SUSTAIN_SEC=0 and instead pre-warm via SETTLE_PRE_MS so Stirling is already
	// attached when the exact band fires.
	if sustainSec := envInt("SUSTAIN_SEC", 0); sustainSec > 0 {
		deadline := time.Now().Add(time.Duration(sustainSec) * time.Second)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		// Trickle DISTINCT DNS lookups (one A-query each) — a protocol Pixie
		// reliably traces — so every AE re-pull pass sees NEW rows and we can
		// observe the C15 "keep writing until t_end" contract. (HTTP trickle was
		// invisible on rigs where Pixie isn't tracing HTTP.)
		sres := &net.Resolver{PreferGo: true}
		si := dnsN
		for time.Now().Before(deadline) {
			select {
			case <-sig:
				return
			case <-ticker.C:
				sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, _ = sres.LookupNetIP(sctx, "ip4", fmt.Sprintf(dnsBase, si))
				scancel()
				si++
			}
		}
	}

	// HOLD: keep the pod (and its upid) alive so Pixie metadata still resolves
	// upid_to_pod_name when AE queries the window. Harness deletes us when done.
	<-sig
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isNXDomain reports whether err is a "no such host" DNS error (the expected,
// fully-deterministic outcome for synthetic names) rather than a transport
// failure that would make the query count non-deterministic.
func isNXDomain(err error) bool {
	var de *net.DNSError
	if errors.As(err, &de) {
		return de.IsNotFound
	}
	return false
}
