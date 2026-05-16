#!/usr/bin/env bash
# protocol-sweep-test.sh — bash test suite for the e2e probe.
# Verifies probe_e2e correctly handles:
#   T1: normal flow — all tables grow → PASS
#   T2: kubescape_logs TTL-pruning — rows insert but absolute count drops
#       → INS captures the inserts, verdict still PASS
#   T3: no-operator case — operator absent, kubescape flows → PASS w/ note
#   T4: dead pipeline — kubescape FLAT, vector errors → FAIL
#   T5: operator deployed but pixie returns 0 rows → FAIL
#
# Each test mocks ch_count + vector_err_count + operator_ready and asserts
# on INS[t] values plus return code.
#
# Run:  ./protocol-sweep-test.sh
# Exit: 0 if all pass, 1 if any fail.

set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib-probe.sh

PROBE_TABLES=(kubescape_logs http_events redis_events pgsql_events adaptive_attribution)
PROBE_INTERVAL_S=1        # speed up tests
WARMUP_S=3                # 3 samples per probe
OUT=/tmp                  # suppress sweep.log writes
NS=px-protocol-loadtest

PASS=0
FAIL=0
FAIL_NAMES=()

assert_eq() {
  local desc="$1" actual="$2" expected="$3"
  if [ "$actual" = "$expected" ]; then
    return 0
  else
    echo "  FAIL: $desc — expected '$expected' got '$actual'"
    return 1
  fi
}

assert_ge() {
  local desc="$1" actual="$2" min="$3"
  if [ "$actual" -ge "$min" ] 2>/dev/null; then
    return 0
  else
    echo "  FAIL: $desc — expected >= $min got '$actual'"
    return 1
  fi
}

run_test() {
  local name="$1"
  shift
  echo "=== $name ==="
  if "$@"; then
    PASS=$((PASS+1))
    echo "  PASS"
  else
    FAIL=$((FAIL+1))
    FAIL_NAMES+=("$name")
    echo "  TEST FAIL"
  fi
}

# --- mock scaffolding --------------------------------------------------
# State stored in files because ch_count() is invoked via $(...) subshell —
# any in-memory state increment in the function body is lost when the
# subshell exits. File-based counters survive.
MOCK_DIR=$(mktemp -d -t probe-mock-XXXXXX)
trap "rm -rf $MOCK_DIR" EXIT

VECTOR_ERR=0
OP_READY=1

# set_table_seq <table> <v0> <v1> <v2> <v3> ...
# Sets the sequence of values ch_count(<table>) returns on successive calls.
set_table_seq() {
  local t="$1"; shift
  printf '%s\n' "$@" > "$MOCK_DIR/seq-$t"
  echo 0 > "$MOCK_DIR/idx-$t"
}

ch_count() {
  local t="$1"
  local idx_file="$MOCK_DIR/idx-$t"
  local seq_file="$MOCK_DIR/seq-$t"
  if [ ! -f "$seq_file" ]; then echo 0; return; fi
  local idx
  idx=$(cat "$idx_file")
  local v
  v=$(awk -v n="$idx" 'NR==n+1{print; exit}' "$seq_file")
  v=${v:-0}
  echo $((idx + 1)) > "$idx_file"
  echo "$v"
}

vector_err_count() { echo "$VECTOR_ERR"; }
operator_ready() { echo "$OP_READY"; }

reset_mocks() {
  rm -f "$MOCK_DIR"/seq-* "$MOCK_DIR"/idx-*
  VECTOR_ERR=0
  OP_READY=1
  unset INS
}

# --- T1: normal flow ---------------------------------------------------
test_normal_flow() {
  reset_mocks
  # 4 samples (t0 + 3 ticks); each tick adds rows to all tables
  set_table_seq kubescape_logs       100 150 200 250
  set_table_seq http_events         1000 1200 1400 1600
  set_table_seq redis_events         500 600 700 800
  set_table_seq pgsql_events         700 850 1000 1150
  set_table_seq adaptive_attribution  10 12 14 16
  probe_e2e
  local rc=$?
  assert_eq "return code" "$rc" "0" || return 1
  assert_eq "kubescape INS" "${INS[kubescape_logs]}" "150" || return 1
  assert_eq "http INS" "${INS[http_events]}" "600" || return 1
  assert_eq "redis INS" "${INS[redis_events]}" "300" || return 1
  assert_eq "pgsql INS" "${INS[pgsql_events]}" "450" || return 1
  assert_eq "attrib INS" "${INS[adaptive_attribution]}" "6" || return 1
}

# --- T2: TTL pruning — rows insert but absolute count collapses --------
test_ttl_pruning() {
  reset_mocks
  # kubescape: 100 → 180 (+80) → 20 (TTL merge dropped 160) → 120 (+100)
  set_table_seq kubescape_logs 100 180 20 120
  set_table_seq http_events 1000 1000 1000 1000
  set_table_seq redis_events 500 500 500 500
  set_table_seq pgsql_events 700 700 700 700
  set_table_seq adaptive_attribution 10 10 10 10
  OP_READY=0
  probe_e2e
  local rc=$?
  assert_eq "TTL: kubescape INS sums positives only" "${INS[kubescape_logs]}" "180" || return 1
  assert_eq "TTL: probe returns PASS (operator absent, kubescape grew)" "$rc" "0" || return 1
}

# --- T3: no-operator case ----------------------------------------------
test_no_operator() {
  reset_mocks
  OP_READY=0
  set_table_seq kubescape_logs 10 15 20 25
  set_table_seq http_events 1000 1000 1000 1000
  set_table_seq redis_events 500 500 500 500
  set_table_seq pgsql_events 700 700 700 700
  set_table_seq adaptive_attribution 0 0 0 0
  probe_e2e
  local rc=$?
  assert_eq "no-op: PASS" "$rc" "0" || return 1
  assert_eq "no-op: kubescape grew" "${INS[kubescape_logs]}" "15" || return 1
  assert_eq "no-op: http flat" "${INS[http_events]}" "0" || return 1
}

# --- T4: dead pipeline -------------------------------------------------
test_dead_pipeline() {
  reset_mocks
  set_table_seq kubescape_logs 100 100 100 100
  set_table_seq http_events 1000 1000 1000 1000
  set_table_seq redis_events 500 500 500 500
  set_table_seq pgsql_events 700 700 700 700
  set_table_seq adaptive_attribution 10 10 10 10
  VECTOR_ERR=12
  probe_e2e
  local rc=$?
  assert_eq "dead: FAIL" "$rc" "1" || return 1
  assert_eq "dead: kubescape INS 0" "${INS[kubescape_logs]}" "0" || return 1
}

# --- T5: operator on, pixie returns 0 ----------------------------------
test_operator_on_pixie_zero() {
  reset_mocks
  OP_READY=1
  set_table_seq kubescape_logs 100 120 140 160
  set_table_seq http_events 1000 1000 1000 1000
  set_table_seq redis_events 500 500 500 500
  set_table_seq pgsql_events 700 700 700 700
  set_table_seq adaptive_attribution 10 10 10 10
  probe_e2e
  local rc=$?
  assert_eq "op+0-pixie: FAIL (op_ready=1, no fan-out)" "$rc" "1" || return 1
  assert_eq "op+0-pixie: kubescape INS 60" "${INS[kubescape_logs]}" "60" || return 1
  assert_eq "op+0-pixie: http INS 0" "${INS[http_events]}" "0" || return 1
}

run_test "T1 normal flow"                    test_normal_flow
run_test "T2 TTL pruning (positive-delta)"   test_ttl_pruning
run_test "T3 no operator deployed"           test_no_operator
run_test "T4 dead SBOB+vector pipeline"      test_dead_pipeline
run_test "T5 operator deployed, pixie empty" test_operator_on_pixie_zero

echo
echo "=========================================="
echo "PASS=$PASS  FAIL=$FAIL"
if [ "$FAIL" -gt 0 ]; then
  echo "Failed: ${FAIL_NAMES[*]}"
  exit 1
fi
echo "all probe tests pass"
