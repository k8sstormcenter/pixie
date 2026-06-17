# SYN-scan detection gap — empirical test

## What this test answers

Can Pixie see an `nmap -sS` (TCP SYN, "half-open") scan today?

The hypothesis going in: **no**. Stirling's socket_tracer hooks
userspace syscalls (`connect`, `accept`, `sendto`, …). A SYN scan
never gets past kernel TCP — the scanner sends a SYN, the target
returns SYN-ACK or RST, the scanner RSTs back. **No syscall fires on
either side for closed ports**, so socket_tracer produces no row.
For open ports, the target's `accept()` is never called either
(scanner aborts before the 3-way handshake completes), so it's
invisible there too.

The `tcp_stats` source connector tracks `tx/rx/retransmits` via
kprobes on `tcp_sendmsg`/`tcp_cleanup_rbuf` — those also only fire
on established connections. SYN-only flows leave them blank.

The `network_stats` source aggregates `/proc/<pid>/net/dev`-style
byte counts — it sees the SYN frame at the link layer, but not as
a discrete event with the 5-tuple of the recon target.

## Test design

1. Pick a pod inside the cluster as the target — its IP and a known
   open port (e.g. `coredns:53`) plus many closed ports.
2. Run **three** scan profiles from a scanner pod:
   - `-sS` SYN scan: classic recon.
   - `-sT` TCP-connect scan: the same target ports but using the
     full kernel `connect()` syscall — should appear in
     `tcp_stats`/`conn_stats`.
   - Idle wait: a control sample with the scanner pod alive but
     not doing anything, so we can subtract baseline noise.
3. After each scan, query PEM via `pxapi` for every Stirling table
   that could possibly carry the signal:
   - `conn_stats` (socket_tracer)
   - `tcp_stats_events` (tcp_stats source)
   - `network_stats` (network_stats source)
   - `dns_events`, `http_events` (sanity — should be empty for these scans)
4. Score per scan: number of rows whose `remote_addr` matches the
   target IP and whose timestamp falls in the scan window. **Zero
   rows for `-sS`** is the headline finding.

## What we explicitly want to learn

* Confirm the gap (probably trivial — but evidence first).
* Find any *partial* signal Pixie does see (e.g. `network_stats`
  byte counter ticks up by a tiny amount). That might be enough
  for an alert at the source-rate-of-change level even without a
  new probe.
* Quantify what `network_stats` would need to change to give a
  per-flow SYN counter (the candidate one-line BPF probe).
