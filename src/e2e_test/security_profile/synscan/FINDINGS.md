# SYN-scan detection gap — empirical results

Two nmap scans against an in-cluster target (`coredns` at `10.42.0.7`,
port 53 open, ports 20–100 mostly closed), driven from a scanner pod
(`synscanner`, `10.42.0.237`) in the same node-local network as the
PEM. After each scan I queried every Stirling table that could
conceivably carry signal.

## Scans

| profile | nmap flags | what it does on the wire |
|---|---|---|
| SYN scan | `-sS` | scanner SYN; target SYN-ACK (open) or RST (closed); scanner RST. **No syscall fires** on either side for closed ports. |
| TCP-connect scan | `-sT` | scanner `connect()` → kernel completes the handshake → `close()`. **No bytes exchanged.** |

Each scan ran in ~0.1 s against ports 20-100 + port 53. nmap reported
"80 closed (reset/conn-refused) + 1 open (53/domain)" both times.

## Pixie query — scan window

PxL filter: rows whose `remote_addr == 10.42.0.7` (target IP) in the
last 240 s.

| Table | Source connector | Rows matching scan ports | Rows matching port 53 (open) |
|---|---|---:|---:|
| `conn_stats` | socket_tracer | **0** | **0** |
| `tcp_stats_events` | tcp_stats | n/a (source not deployed) | n/a |
| `network_stats` | network_stats | **0** | **0** |
| `http_events` | socket_tracer | 0 (expected — not HTTP) | 0 |
| `dns_events` | socket_tracer | 0 (expected — not DNS) | 0 |

For context, `conn_stats` contained 44 rows for the same target over
the same window — but **every single row was from the kubelet probe
loop** (`User-Agent: kube-probe/1.35` to `/ready` and `/health`).
Identifiable by `upid=...0360 (= the kubelet)`, `protocol=1 (HTTP)`,
ports 8080/8181, non-zero `bytes_sent`/`bytes_recv`.

**Not a single row attributable to the SYN scan or to the TCP-connect
scan landed in any table.** The scanner pod's own UPID does not appear
as a source anywhere.

## Why

- **`-sS` (SYN scan)**: never gets past the kernel TCP state machine.
  No syscall fires on either side. Stirling hooks `connect`,
  `accept`, `sendto`, `recvfrom`, `sendmsg`, `recvmsg` — all
  userspace; the SYN→RST handshake leaves no userspace trace.
- **`-sT` (TCP-connect scan)**: nmap calls `connect()`, which Stirling
  *does* hook. But `conn_stats` requires a protocol parser to
  classify the connection (`protocol != kUnknown`) before it emits a
  row. With zero bytes exchanged the parser never runs; the
  connection ends as `protocol=0`, which `conn_tracker.cc` filters
  out before the table push.
- **`network_stats`**: aggregates `/proc/<pid>/net/dev`-style byte
  counters per pod. SYN frames are tiny (74 B each), and on this
  standalone-pem the source connector isn't producing rows at all
  (likely missing k8s metadata for the scanner pod). Even when it
  does work, the granularity is per-pod totals, not per-(remote IP,
  port).

## Implication

Realistic recon — `nmap -sS -p 1-65535 <victim-cluster-pod>` from a
foothold pod — leaves zero rows in any Stirling table. Detection
of port scanning today is **not a tuning problem and not a query
problem; the events aren't being captured.**

## Candidate fixes (in priority order)

1. **New Stirling probe: TCP control-plane events.** Hook
   `tcp_v4_rcv` / `tcp_v6_rcv` (or `inet_csk_accept` for the open
   case + a kretprobe on `tcp_v4_conn_request` for the listen-side
   SYN) and emit a per-event record:
   `(timestamp, src_upid, src_addr, src_port, dst_addr, dst_port, flags)`.
   New `tcp_control_events` table. Scanner-side records `flags=SYN`;
   target-side records `flags=SYN-ACK` for open and `flags=RST` for
   closed. This is the source-of-truth fix.
2. **Loosen `conn_stats` filter.** Stop dropping `protocol=kUnknown`
   rows; let them through with `bytes=0`. Captures `-sT` cleanly,
   does nothing for `-sS`. One-line change in `conn_tracker.cc`,
   useful as a stop-gap.
3. **Use `network_stats` rx_packets delta as a recon signal.** Detect
   "many small packets to many ports" by watching `rx_packets`
   jumping by ≥ K per second per pod. Cheap, alertable, false-positive
   prone. Only useful as an early-warning until #1 lands.

## Next experiment (planned)

* Patch Stirling per (2) — flip the kUnknown drop — and re-run this
  exact test bed. Expected outcome: `-sT` shows up, `-sS` still does
  not. Quantifies the cheap stop-gap.
* Sketch (1) as a follow-up PR. Even without merging, getting the
  probe working in a side-loaded image and running this same test
  bed would prove out the full detection path.

## Reproducing

```bash
# 1. Deploy scanner pod (needs NET_RAW for nmap -sS)
kubectl apply -f src/e2e_test/security_profile/synscan/k8s/scanner-pod.yaml

# 2. Run paired scans
kubectl -n default exec synscanner -- nmap -sS -p 20-100 -n -Pn <target_pod_ip>
sleep 10
kubectl -n default exec synscanner -- nmap -sT -p 20-100 -n -Pn <target_pod_ip>

# 3. Query Pixie tables
go run src/e2e_test/security_profile/synscan/tools/stats_check/main.go \
    -addr <pem-host>:12345 -scanner_ip <scanner_pod_ip> -target_ip <target_pod_ip>
```
