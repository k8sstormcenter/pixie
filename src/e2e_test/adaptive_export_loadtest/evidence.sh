#!/usr/bin/env bash
# Copyright 2018- The Pixie Authors.
# SPDX-License-Identifier: Apache-2.0
#
# evidence.sh — capture SOC-stack integration evidence on a live rig:
#   (1) pipeline components are Running (kubescape → vector → node-agent →
#       ClickHouse → adaptive_export)
#   (2) EVERY forensic_db table is created, with the correct engine
#   (3) event_time is unix NANOSECONDS end-to-end (kubescape_logs UInt64 via
#       fromUnixTimestamp64Nano; AE-owned protocol tables DateTime64(9))
#   (4) the pipeline is flowing (kubescape_logs fresh + growing, attribution
#       written, Pixie data-plane rows captured)
#   (5) the AE control-plane loadtest (reproducibility) passes  [AELOAD_RUN_SUITE=1]
#
# Sandbox-safe: NO shell `sleep` (blocked); ClickHouse via `kubectl exec`.
#
# Usage:
#   KUBECONFIG=/path/kubeconfig ./evidence.sh
#   AELOAD_RUN_SUITE=1 SUITE_BIN=/tmp/aeloadsuite.test ... ./evidence.sh
set -uo pipefail

: "${KUBECONFIG:?set KUBECONFIG to the rig kubeconfig}"
CH_NS="${CH_NS:-clickhouse}"
AE_NS="${AE_NS:-pl}"
SOC_NS="${SOC_NS:-honey}"
DB="${DB:-forensic_db}"
EVID="${EVID:-/tmp/evidence_$(kubectl config current-context 2>/dev/null | tr -c 'A-Za-z0-9' _)}"
mkdir -p "$EVID"

K(){ kubectl "$@"; }
CHPOD="$(K -n "$CH_NS" get pods --no-headers 2>/dev/null | awk '/^chi-/{print $1;exit}')"
chq(){ K -n "$CH_NS" exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }

pass=0; fail=0
ok(){   printf '  \033[32mPASS\033[0m %s\n' "$*"; pass=$((pass+1)); }
no(){   printf '  \033[31mFAIL\033[0m %s\n' "$*"; fail=$((fail+1)); }
warn(){ printf '  \033[33mWARN\033[0m %s\n' "$*"; }
hdr(){  printf '\n=== %s ===\n' "$*"; }

# The AE-owned tables (must be created by AE on boot) + soc-owned inputs.
AE_TABLES="http_events http2_messages.beta dns_events redis_events mysql_events pgsql_events cql_events mongodb_events kafka_events.beta amqp_events mux_events tls_events conn_stats adaptive_attribution trigger_watermark ae_reconcile"
SOC_TABLES="kubescape_logs alerts"

# ---------------------------------------------------------------- 1. components
hdr "1. pipeline components Running"
comp_check(){ # ns selector-substr label
  local ns="$1" want="$2"
  local line; line="$(K -n "$ns" get pods --no-headers 2>/dev/null | grep -iE "$want" | head -1)"
  if [ -n "$line" ] && echo "$line" | grep -qE 'Running|Completed'; then ok "$want ($ns): $(echo "$line"|awk '{print $1,$3}')"
  else no "$want ($ns): not Running"; fi
}
comp_check "$SOC_NS" kubescape
comp_check "$SOC_NS" vector
comp_check "$SOC_NS" node-agent
comp_check "$CH_NS"  'chi-.*(server|forensic|cluster)'
comp_check "$CH_NS"  'chk-.*keeper'
comp_check "$AE_NS"  adaptive-export
K -n "$AE_NS" get ds adaptive-export -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null | tee "$EVID/ae_image.txt" | sed 's/^/  AE image: /'; echo

# ---------------------------------------------------------------- 2. tables
hdr "2. all forensic_db tables created"
chq "SELECT name, engine FROM system.tables WHERE database='$DB' ORDER BY name" > "$EVID/tables.txt"
have="$(cut -f1 "$EVID/tables.txt")"
for t in $AE_TABLES $SOC_TABLES; do
  if grep -qxF "$t" <<<"$have"; then ok "table $t"; else no "table $t MISSING"; fi
done

# ---------------------------------------------------------------- 3. ns units
hdr "3. event_time is NANOSECONDS end-to-end"
kdll="$(chq "SHOW CREATE TABLE $DB.kubescape_logs")"
echo "$kdll" > "$EVID/kubescape_logs_ddl.sql"
if grep -q 'event_time.*UInt64' <<<"$kdll" && grep -q 'fromUnixTimestamp64Nano' <<<"$kdll"; then
  ok "kubescape_logs.event_time = UInt64 + fromUnixTimestamp64Nano (ns)"
else no "kubescape_logs DDL is not nanos (expected UInt64 + fromUnixTimestamp64Nano)"; fi
# sample magnitude: 19 digits ⇒ ns
dig="$(chq "SELECT length(toString(max(event_time))) FROM $DB.kubescape_logs")"
[ "${dig:-0}" -ge 19 ] && ok "kubescape_logs.event_time magnitude = ${dig} digits (nanoseconds)" \
                        || no "kubescape_logs.event_time is ${dig} digits (expected ~19 = ns)"
# AE-owned protocol tables must be DateTime64(9)
chq "SELECT table, type FROM system.columns WHERE database='$DB' AND name='event_time' AND table IN ('http_events','conn_stats','dns_events','pgsql_events') ORDER BY table" > "$EVID/protocol_event_time_types.txt"
if grep -qE 'DateTime64\(3' "$EVID/protocol_event_time_types.txt"; then
  no "protocol event_time is DateTime64(3) (MILLISECONDS) — AE tables predate the ns image; DROP + recreate under the ns AE to get DateTime64(9)"
  sed 's/^/    /' "$EVID/protocol_event_time_types.txt"
else ok "protocol event_time = DateTime64(9) (nanoseconds)"; fi

# ---------------------------------------------------------------- 4. flow
hdr "4. pipeline is flowing"
chq "SELECT count() FROM $DB.kubescape_logs" | { read -r n; [ "${n:-0}" -gt 0 ] && ok "kubescape_logs has $n rows (kubescape→vector→CH)" || no "kubescape_logs empty"; }
# freshness: newest kubescape_logs within 15 min of now
fresh="$(chq "SELECT fromUnixTimestamp64Nano(max(event_time)) > now()-900 FROM $DB.kubescape_logs")"
[ "${fresh:-0}" = "1" ] && ok "kubescape_logs is FRESH (newest < 15m old — pipeline live)" || warn "kubescape_logs newest is >15m old (pipeline may be idle)"
chq "SELECT count() FROM $DB.adaptive_attribution" | { read -r n; [ "${n:-0}" -gt 0 ] && ok "adaptive_attribution has $n rows (AE control plane)" || warn "adaptive_attribution empty (no anomalies steered yet)"; }
for t in http_events conn_stats; do
  chq "SELECT count() FROM $DB.$t" | { read -r n; [ "${n:-0}" -gt 0 ] && ok "$t has $n rows (Pixie data plane)" || warn "$t empty"; }
done
chq "SELECT name, total_rows FROM system.tables WHERE database='$DB' ORDER BY name" | tee "$EVID/table_rowcounts.txt" >/dev/null

# ---------------------------------------------------------------- 5. loadtest
hdr "5. AE control-plane loadtest (reproducibility)"
if [ "${AELOAD_RUN_SUITE:-0}" = "1" ] && [ -x "${SUITE_BIN:-/tmp/aeloadsuite.test}" ]; then
  K -n "$CH_NS" port-forward "svc/$(K -n "$CH_NS" get svc -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.spec.ports[*].port}{"\n"}{end}' | awk '/8123/{print $1;exit}')" 8123:8123 >/tmp/evpf.log 2>&1 &
  PF=$!
  if curl --retry 30 --retry-delay 1 --retry-connrefused -fsS http://127.0.0.1:8123/ping >/dev/null 2>&1; then
    AELOAD_LIVE=1 AELOAD_CH_URL=http://127.0.0.1:8123 \
    AELOAD_CH_WUSER="${AELOAD_CH_WUSER:-ingest_writer}" AELOAD_CH_WPASS="${AELOAD_CH_WPASS:-changeme-ingest}" \
    AELOAD_REPS="${AELOAD_REPS:-10}" \
    "${SUITE_BIN:-/tmp/aeloadsuite.test}" -test.v -test.run "${SUITE_RUN:-TestControlPlaneReproducibility/single-anomaly}" -test.timeout 20m 2>&1 | tee "$EVID/loadtest.txt"
    grep -q -- '--- PASS' "$EVID/loadtest.txt" && ok "loadtest PASS (see $EVID/loadtest.txt)" || no "loadtest did not pass"
  else no "could not port-forward ClickHouse for the loadtest"; fi
  kill $PF 2>/dev/null || true
else
  warn "loadtest skipped (set AELOAD_RUN_SUITE=1 + SUITE_BIN=/tmp/aeloadsuite.test)"
fi

# ---------------------------------------------------------------- summary
hdr "SUMMARY"
printf '  PASS=%d  FAIL=%d  → evidence in %s\n' "$pass" "$fail" "$EVID"
[ "$fail" -eq 0 ]
