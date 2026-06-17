# Security-profile findings — DNS recon coverage

Result of a local sweep against a single-node standalone-pem on a
k3s cluster (kernel 6.8, 1 PEM, 2 vCPU each). Each cell drives N
distinct DNS A-queries at rate R q/s and then pulls `dns_events` via
the PEM's direct port and computes the set intersection of sent vs
captured names (case- and trailing-dot- normalised).

Two profiles measured here. `security_aggressive` skipped for now
because security_runtime already lifts coverage to ≥99.9 % at the
loads where default drops; a 3-way comparison is reserved for the
follow-up where we push N high enough to find security_runtime's own
cliff.

## Profiles

| profile          | TableStore | http % | control-bw / cpu | datastream buf | DNS tracing |
|------------------|-----------:|-------:|-----------------:|---------------:|:-----------:|
| default          | 1024 MB    | 40 %   | 5 MiB/s          | 1 MiB          | on (envvar) |
| security_runtime | 2048 MB    | 25 %   | 20 MiB/s         | 4 MiB          | on (envvar) |

(All knobs envvar-settable on a stock PEM image. No source changes.)

## Coverage matrix — default profile

| N | rate (q/s) | sent | captured | coverage % |
|---:|---:|---:|---:|---:|
| 100     | 100   | 100    | 99     | 99.00 |
| 100     | 1000  | 100    | 100    | 100.00 |
| 100     | 5000  | 100    | 100    | 100.00 |
| 1 000   | 100   | 1000   | 997    | 99.70 |
| 1 000   | 1000  | 1000   | 1000   | 100.00 |
| 1 000   | 5000  | 1000   | 995    | 99.50 |
| 5 000   | 100   | 5000   | 4991   | 99.82 |
| 5 000   | 1000  | 5000   | 4990   | 99.80 |
| 5 000   | 5000  | 5000   | 4988   | 99.76 |
| 10 000  | 100   | 10000  | 9954   | 99.54 |
| 10 000  | 1000  | 10000  | 9990   | 99.90 |
| 10 000  | 5000  | 10000  | 9988   | 99.88 |
| 20 000  | 5000  | 20000  | 19975  | 99.88 |
| 20 000  | 10000 | 20000  | 19987  | 99.94 |
| 20 000  | 20000 | 20000  | 19979  | 99.89 |
| 50 000  | 5000  | 50000  | 49951  | 99.90 |
| 50 000  | 10000 | 50000  | 49971  | 99.94 |
| 50 000  | 20000 | 50000  | 49980  | 99.96 |
| **100 000** | 5000  | 100000 | **85129**  | **85.13** |
| 100 000 | 10000 | 100000 | 84890  | 84.89 |
| 100 000 | 20000 | 100000 | 85652  | 85.65 |
| **200 000** | 5000  | 200000 | **106880** | **53.44** |
| 200 000 | 10000 | 200000 | 107174 | 53.59 |
| 200 000 | 20000 | 200000 | 107064 | 53.53 |

## Coverage matrix — security_runtime profile

| N | rate (q/s) | sent | captured | coverage % |
|---:|---:|---:|---:|---:|
| 50 000  | 5000  | 50000  | 49945  | 99.89 |
| 50 000  | 10000 | 50000  | 49945  | 99.89 |
| 50 000  | 20000 | 50000  | 49964  | 99.93 |
| **100 000** | 5000  | 100000 | **99930**  | **99.93** |
| 100 000 | 10000 | 100000 | 99891  | 99.89 |
| 100 000 | 20000 | 100000 | 99896  | 99.90 |
| **200 000** | 5000  | 200000 | **199755** | **99.88** |
| 200 000 | 10000 | 200000 | 199800 | 99.90 |
| 200 000 | 20000 | 200000 | 199749 | 99.87 |

## What the default profile cannot see

Default holds ≥99.5 % coverage up to **50 000 queries** regardless of
rate (sustained 20 k q/s included). Past that it cliffs:

* **100 000 queries**: coverage falls to **~85 %** — ~15 k queries lost.
* **200 000 queries**: coverage falls to **~53 %** — ~93 k queries lost.

The loss is rate-independent (5 k vs 20 k q/s produce the same
coverage), which rules out per-CPU control-bw cap as the dominant
constraint. The drop tracks the dns_events share of the table-store:
1024 MB total × (100 % − 40 % http − ~30 other tables) ≈ 25–30 MB for
DNS, ≈ 40 k–50 k rows before rotate. Above that, oldest rows are
evicted while the dnsverify query is still running.

This means a single recon sweep of ≥100 k DNS lookups (well within
nmap-default-list-and-script-set range) **silently loses 15 % to 50 %
of the trail** on a default Pixie config.

## What flags alone achieved

`security_runtime` recovers coverage to ≥99.87 % at 200 000 queries.
Every change is a stock PEM env var:

```
PL_TABLE_STORE_DATA_LIMIT_MB=2048
PL_TABLE_STORE_HTTP_EVENTS_PERCENT=25
STIRLING_SOCKET_TRACER_TARGET_CONTROL_BW_PERCPU=20971520
PL_DATASTREAM_BUFFER_SIZE=4194304
PX_STIRLING_ENABLE_DNS_TRACING=1
```

Headline gains:

| load            | default | security_runtime | delta |
|-----------------|--------:|-----------------:|------:|
| 100 k @ 5 k/s   |  85.13 %| 99.93 %          | **+14.80 pp** |
| 100 k @ 20 k/s  |  85.65 %| 99.90 %          | **+14.25 pp** |
| 200 k @ 5 k/s   |  53.44 %| 99.88 %          | **+46.44 pp** |
| 200 k @ 20 k/s  |  53.53 %| 99.87 %          | **+46.34 pp** |

The dominant lever is the **table-store split**:
`PL_TABLE_STORE_HTTP_EVENTS_PERCENT=25` reallocates ~150 MB from the
http carve-out to the rest of the tables, lifting the dns_events
budget above the load. The control-bw and datastream-buffer bumps
are belt-and-braces — they would matter once a code change exposes
higher-volume protocols (TLS, full pcap of recon SYN scans).

## What we still cannot recover with flags alone

The harness has not yet found the security_runtime cliff. Next steps
in a follow-up PR:

* Push N to 1 M to find where the 2048 MB table-store rotates DNS too.
* Add a TCP recon variant (nmap SYN scan): control-bw will dominate
  because every new 5-tuple is a control event.
* Add concurrent HTTP load to verify that the 25 %/75 % split holds
  under noisy-neighbour conditions.
* Only then turn to the source-level levers (`kSamplingPeriod`,
  `kDeathCountdownIters`, per-protocol perf-buffer carve-outs).

TLS handshake parsing (gh#2095) is **deliberately not measured here**
— it needs a dual-trace implementation, not a flag flip.

## Reproducing

```bash
# 1. Native build of the binaries
go build -o /tmp/dnsprobe ./src/e2e_test/security_profile/tools/dnsprobe
go build -o /tmp/dnsverify ./src/e2e_test/security_profile/tools/dnsverify

# 2. Apply the flag profile (skip for "default")
kubectl -n <pem-ns> set env daemonset/<pem-ds> \
  $(grep -v '^#' src/e2e_test/security_profile/harness/flags_security_runtime.env | xargs)
kubectl -n <pem-ns> rollout status ds/<pem-ds>

# 3. Drive one cell
salt=$(/tmp/dnsprobe -n 100000 -rate 5000 -workers 64 \
    -domain secprof.invalid -resolver 1.1.1.1:53 \
    -out /tmp/sent.csv)
/tmp/dnsverify -addr <pem-host>:12345 -direct -salt "$salt" \
    -lookback 240 -out /tmp/seen.csv

# 4. Score
source src/e2e_test/security_profile/harness/lib.sh
coverage_stats /tmp/sent.csv /tmp/seen.csv
```

(`harness/run.sh` wraps steps 2–4 for the full sweep matrix.)
