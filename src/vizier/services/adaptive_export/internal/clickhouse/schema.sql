-- Forensic SOC ClickHouse schema (adaptive-write feature, design rev 2)
-- ----------------------------------------------------------------------
-- Pixie type map (PixieTypeToClickHouseType):
--   TIME64NS → DateTime64(9); event_time is nanosecond-consistent → DateTime64(9)
--   INT64 → Int64 | FLOAT64 → Float64 | STRING → String
--   BOOLEAN → UInt8 | UINT128 → String
-- Pixie's retention plugin adds: hostname String, event_time DateTime64(9)
-- (nanoseconds everywhere: kubescape_logs.event_time is UInt64 unix-ns; protocol
--  tables' event_time is DateTime64(9) derived from time_; see soc clickhouse-lab).
-- We add: namespace String, pod String  (used by adaptive_attribution JOINs).
--
-- Engine convention for pixie observation tables:
--   ENGINE = MergeTree()
--   PARTITION BY toYYYYMM(event_time)
--   ORDER BY (hostname, event_time)
--
-- The hash IS NOT stored on pixie observation rows. Attribution is via JOIN
-- against forensic_db.adaptive_attribution on (hostname, namespace, pod, time_).
-- See the adaptive_attribution definition at the bottom of this file.

CREATE DATABASE IF NOT EXISTS forensic_db;

-- Kubescape alerts (Vector kubescape_to_alerts sink, unchanged).
CREATE TABLE IF NOT EXISTS forensic_db.alerts (
    timestamp       DateTime64(3),
    ingest_time     DateTime64(3) DEFAULT now64(3),
    rule_id         LowCardinality(String),
    alert_name      LowCardinality(String),
    severity        UInt8,
    unique_id       String,
    cluster_name    LowCardinality(String),
    namespace       LowCardinality(String),
    pod_name        String,
    container_name  LowCardinality(String),
    container_id    String,
    workload_name   LowCardinality(String),
    workload_kind   LowCardinality(String),
    image           LowCardinality(String),
    infected_pid    UInt32,
    process_name    LowCardinality(String),
    process_cmdline String,
    message         String,
    raw_event       String
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(timestamp)
  ORDER BY (timestamp, severity, namespace, rule_id)
  TTL toDateTime(timestamp) + INTERVAL 90 DAY DELETE
  SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- Kubescape raw logs — Vector kubescape_enrich sink writes here, the operator's
-- trigger reads it. anomaly_hash column kept here as DEFAULT '' for backwards
-- compat with any existing Vector pipeline that already populates it; the
-- operator does not depend on it being non-empty.
CREATE TABLE IF NOT EXISTS forensic_db.kubescape_logs (
    BaseRuntimeMetadata   String,
    CloudMetadata         String,
    RuleID                String,
    RuntimeK8sDetails     String,
    RuntimeProcessDetails String,
    event                 String,
    event_time            UInt64,   -- unix epoch NANOSECONDS (Vector kubescape_enrich emits ns)
    hostname              String,
    level                 String DEFAULT '',
    message               String DEFAULT '',
    msg                   String DEFAULT '',
    processtree_depth     String DEFAULT '',
    anomaly_hash          String DEFAULT ''
) ENGINE = MergeTree()
  ORDER BY (event_time, hostname)
  -- event_time is unix-epoch NANOSECONDS; convert with fromUnixTimestamp64Nano.
  -- Plain toDateTime() would read ns as seconds (year ~58e9) → broken partitions/TTL.
  -- toYYYYMM accepts DateTime64 directly; TTL must wrap in toDateTime().
  PARTITION BY toYYYYMM(fromUnixTimestamp64Nano(event_time))
  TTL toDateTime(fromUnixTimestamp64Nano(event_time)) + INTERVAL 30 DAY DELETE
  SETTINGS index_granularity = 8192;

-- ============================================================================
-- 12 Pixie socket_tracer tables — strongly predefined, namespace + pod added.
-- The retention scripts (PxL, user-defined or shipped defaults) MUST populate
-- namespace + pod via px.upid_to_namespace / px.upid_to_pod_name.
-- ============================================================================

-- http_events — pixie/src/stirling/source_connectors/socket_tracer/http_table.h
CREATE TABLE IF NOT EXISTS forensic_db.http_events (
    time_          DateTime64(9, 'UTC'),
    upid           String,
    namespace      String,
    pod            String,
    remote_addr    String,
    remote_port    Int64,
    local_addr     String,
    local_port     Int64,
    trace_role     Int64,
    encrypted      UInt8,
    major_version  Int64,
    minor_version  Int64,
    content_type   Int64,
    req_headers    String,
    req_method     String,
    req_path       String,
    req_body       String,
    req_body_size  Int64,
    resp_headers   String,
    resp_status    Int64,
    resp_message   String,
    resp_body      String,
    resp_body_size Int64,
    latency        Int64,
    hostname       String,
    event_time     DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9),
    unique_id      String DEFAULT ''
) ENGINE = ReplacingMergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time, time_, upid, trace_role, remote_port, local_port, latency, req_method, req_path);

-- http2_messages.beta — http2_messages_table.h
CREATE TABLE IF NOT EXISTS forensic_db.`http2_messages.beta` (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    stream_id   Int64,
    headers     String,
    body        String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9)
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- dns_events — dns_table.h
CREATE TABLE IF NOT EXISTS forensic_db.dns_events (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    req_header  String,
    req_body    String,
    resp_header String,
    resp_body   String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9),
    unique_id   String DEFAULT ''
) ENGINE = ReplacingMergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time, time_, upid, trace_role, remote_port, local_port, latency, req_body);

-- redis_events — redis_table.h
CREATE TABLE IF NOT EXISTS forensic_db.redis_events (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    req_cmd     String,
    req_args    String,
    resp        String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9),
    unique_id   String DEFAULT ''
) ENGINE = ReplacingMergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time, time_, upid, trace_role, remote_port, local_port, latency, req_cmd);

-- mysql_events — mysql_table.h
CREATE TABLE IF NOT EXISTS forensic_db.mysql_events (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    req_cmd     Int64,
    req_body    String,
    resp_status Int64,
    resp_body   String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9),
    unique_id   String DEFAULT ''
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- pgsql_events — pgsql_table.h
CREATE TABLE IF NOT EXISTS forensic_db.pgsql_events (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    req         String,
    resp        String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9),
    unique_id   String DEFAULT ''
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- cql_events — cass_table.h
CREATE TABLE IF NOT EXISTS forensic_db.cql_events (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    req_op      Int64,
    req_body    String,
    resp_op     Int64,
    resp_body   String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9)
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- mongodb_events — mongodb_table.h
CREATE TABLE IF NOT EXISTS forensic_db.mongodb_events (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    req_cmd     String,
    req_body    String,
    resp_status String,
    resp_body   String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9)
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- kafka_events.beta — kafka_table.h
CREATE TABLE IF NOT EXISTS forensic_db.`kafka_events.beta` (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    req_cmd     Int64,
    client_id   String,
    req_body    String,
    resp        String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9)
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- amqp_events — amqp_table.h
CREATE TABLE IF NOT EXISTS forensic_db.amqp_events (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    frame_type  Int64,
    channel     Int64,
    method      String,
    payload     String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9)
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- mux_events — mux_table.h
CREATE TABLE IF NOT EXISTS forensic_db.mux_events (
    time_       DateTime64(9, 'UTC'),
    upid        String,
    namespace   String,
    pod         String,
    remote_addr String,
    remote_port Int64,
    local_addr  String,
    local_port  Int64,
    trace_role  Int64,
    encrypted   UInt8,
    req_type    Int64,
    req         String,
    resp        String,
    latency     Int64,
    hostname    String,
    event_time  DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9)
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- tls_events — tls_table.h
CREATE TABLE IF NOT EXISTS forensic_db.tls_events (
    time_         DateTime64(9, 'UTC'),
    upid          String,
    namespace     String,
    pod           String,
    remote_addr   String,
    remote_port   Int64,
    local_addr    String,
    local_port    Int64,
    version       Int64,
    content_type  Int64,
    handshake     String,
    latency       Int64,
    hostname      String,
    event_time    DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9)
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

-- conn_stats — conn_stats_table.h
-- Connection-level statistics (open/close/active counters + bytes_sent/recv +
-- protocol/ssl). Re-added to the rev-2 schema so the
-- adaptive_export retention scripts can persist it. local_addr/local_port are
-- intentionally absent — the pixie kConnStatsElements set carries only
-- remote_addr/remote_port (the connection is identified by the local upid +
-- the remote tuple). Counters are MERGEd by ClickHouse over the (hostname,
-- event_time) order; no aggregating engine because each retention-script
-- pull is a discrete snapshot row.
CREATE TABLE IF NOT EXISTS forensic_db.conn_stats (
    time_         DateTime64(9, 'UTC'),
    upid          String,
    namespace     String,
    pod           String,
    remote_addr   String,
    remote_port   Int64,
    trace_role    Int64,
    addr_family   Int64,
    protocol      Int64,
    ssl           UInt8,
    conn_open     Int64,
    conn_close    Int64,
    conn_active   Int64,
    bytes_sent    Int64,
    bytes_recv    Int64,
    hostname      String,
    event_time    DateTime64(9, 'UTC') DEFAULT toDateTime64(time_, 9),
    unique_id     String DEFAULT ''
) ENGINE = ReplacingMergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time, time_, upid, remote_addr, remote_port, trace_role);

-- ============================================================================
-- adaptive_attribution — operator's only write target in ClickHouse.
--
-- One row per active anomaly hash per node. The operator inserts one row
-- per arriving kubescape_log on its node. ReplacingMergeTree(t_end) collapses
-- re-inserts to the row with the largest t_end — so each fresh anomaly with
-- the same hash extends the active window automatically; stale rows merge
-- away.
--
-- Analyst joins:
--
--   SELECT he.*, attr.anomaly_hash
--   FROM forensic_db.http_events he
--   ASOF INNER JOIN forensic_db.adaptive_attribution attr
--     ON  he.hostname = attr.hostname
--     AND he.namespace = attr.namespace
--     AND he.pod = attr.pod
--     AND he.time_ >= attr.t_start
--   WHERE he.time_ <= attr.t_end
--     AND attr.anomaly_hash = '<hash>';
--
-- Boot-time rehydration of the operator's in-memory active set:
--
--   SELECT * FROM forensic_db.adaptive_attribution FINAL
--   WHERE hostname = '<node>' AND t_end > now64(9);
--
-- DateTime64(9, 'UTC') — pin tz so bare-string serialization is
-- unambiguous; without it, CH parses incoming timestamps in the
-- server-session timezone and silently shifts values on non-UTC hosts.
-- ============================================================================
CREATE TABLE IF NOT EXISTS forensic_db.adaptive_attribution (
    anomaly_hash String,
    namespace    String,
    pod          String,
    comm         String,
    pid          UInt64,
    hostname     String,
    t_start      DateTime64(9, 'UTC'),
    t_end        DateTime64(9, 'UTC'),
    last_seen    DateTime64(9, 'UTC'),
    last_rule_id String,
    n_anomalies  UInt64
) ENGINE = ReplacingMergeTree(t_end)
  PARTITION BY toYYYYMM(t_start)
  ORDER BY (hostname, anomaly_hash);

-- ============================================================================
-- trigger_watermark — persistent cursor for the kubescape_logs trigger.
--
-- Per node, per source-table. The operator advances the row's `watermark`
-- (UInt64 event_time, ns) every time it successfully drains a batch of
-- kubescape rows. On restart it reads the row back and resumes from there
-- instead of replaying the full table from event_time=0 (which, on a busy
-- cluster, produces multi-GiB single-shot SELECTs that the HTTP client
-- times out on, never advancing → infinite stuck loop).
--
-- ReplacingMergeTree(updated_at) collapses re-inserts to the newest, so
-- the operator can INSERT cheaply without bothering with UPDATE
-- semantics. Reads use FINAL — cheap because cardinality is one row per
-- (hostname, table_name).
--
-- This is the operator's second write target alongside adaptive_attribution.
-- ============================================================================
CREATE TABLE IF NOT EXISTS forensic_db.trigger_watermark (
    hostname    String,
    table_name  String,
    watermark   UInt64,
    updated_at  DateTime64(9, 'UTC')
) ENGINE = ReplacingMergeTree(updated_at)
  PARTITION BY hostname
  ORDER BY (hostname, table_name);

-- ============================================================================
-- ae_reconcile — per-pull write-fidelity instrument (gated by ADAPTIVE_RECONCILE).
--
-- One row per data-plane pull: how many rows AE READ back from Pixie for a
-- (table, pod, window) vs how many it WROTE to ClickHouse. Lets a reconcile
-- run localize any loss to a single hop:
--   read  < px-direct PEM count  → query/window/filter miss (R5)
--   wrote < read                 → sink/batch drop          (R6)
--   CH distinct > read           → re-pull duplication       (C8)
-- Plain MergeTree (append-only debug log). NOT a pixie observation table and
-- NOT in PixieTables(); the operator creates it so a reconcile run has a
-- target without manual DDL.
-- ============================================================================
CREATE TABLE IF NOT EXISTS forensic_db.ae_reconcile (
    ts          DateTime64(9, 'UTC'),
    mode        String,
    table_name  String,
    namespace   String,
    pod         String,
    win_start   DateTime64(9, 'UTC'),
    win_end     DateTime64(9, 'UTC'),
    read_count  Int64,
    wrote_count Int64,
    write_err   String,
    hostname    String
) ENGINE = MergeTree
  PARTITION BY toYYYYMMDD(ts)
  ORDER BY (table_name, ts)
  -- append-only debug log; cap growth so long reconcile runs don't accumulate
  -- unbounded storage (CodeRabbit). 30d matches the pixie observation tables.
  TTL toDateTime(ts) + INTERVAL 30 DAY DELETE;

-- dx_evidence_graph — dx evidence-graph edge list: one row per directed hop of an
-- investigation (delivery/egress/execution/exfil/pivot), read by the Pixie
-- dx_evidence_graph UI via px.DataFrame(clickhouse_dsn=...). Operator-owned
-- (dx emits the edges, AE persists them); NOT a pixie socket_tracer table.
--
-- event_time (unix NANOSECONDS) + hostname are REQUIRED: Pixie's clickhouse_dsn
-- query template hardcodes `WHERE event_time >= ... AND hostname = ... ORDER BY
-- event_time` — a table without those columns fails with "Unknown identifier
-- event_time". Same convention as kubescape_logs. event_time is nanos, so the
-- partition/TTL use fromUnixTimestamp64Nano (toDateTime would read ns as seconds
-- → year ~58e9 → broken partitions; see the soc#225 fix).
CREATE TABLE IF NOT EXISTS forensic_db.dx_evidence_graph (
    investigation_id  String,
    event_time        UInt64,
    hostname          String,
    requestor_pod     String,
    responder_pod     String,
    requestor_service String,
    responder_service String,
    requestor_ip      String,
    responder_ip      String,
    -- Int64/Float64 ONLY for the numeric columns: Pixie's clickhouse_dsn type
    -- mapper reads UInt8 as BOOLEAN and does not handle UInt16/UInt32/Float32,
    -- so those fail px marshaling with "Column[N] given incorrect type". Int64
    -- + Float64 map cleanly (INT64→Int64, FLOAT64→Float64). event_time stays
    -- UInt64 (same as kubescape_logs, which px reads fine).
    weight            Int64,
    max_severity      Int64,
    confidence        Float64,
    edge_kind         String,
    `condition`       String,
    criteria          String,
    num_findings      Int64
) ENGINE = MergeTree()
  ORDER BY (event_time, hostname)
  PARTITION BY toYYYYMM(fromUnixTimestamp64Nano(event_time))
  TTL toDateTime(fromUnixTimestamp64Nano(event_time)) + INTERVAL 30 DAY DELETE
  SETTINGS index_granularity = 8192;

-- dx_evidence_manifest — the §9 completeness contract: one row per verdict
-- (ruled_in | metastasis), naming the evidence rows dx consulted so the
-- validator can join them against what AE persisted (write⊇read, checkable).
-- Operator-owned (dx emits the manifest via POST /dx/evidence_manifest, AE
-- persists it); NOT a pixie table. Column names are the manifest.Manifest
-- JSON tags (dx internal/manifest). Same event_time (unix NANOSECONDS) +
-- hostname read-path convention as dx_evidence_graph so it is px-readable.
-- The nested collections (case_window/findings/orders/seeds/chain) are stored
-- as JSON text in String columns; the control handler pre-renders them so the
-- JSONEachRow insert is ClickHouse-version independent.
CREATE TABLE IF NOT EXISTS forensic_db.dx_evidence_manifest (
    investigation_id  String,
    event_time        UInt64,
    hostname          String,
    `condition`       String,
    verdict           String,
    confidence        Float64,
    posterior         Float64,
    catalog_version   String,
    case_window       String,
    findings          String,
    orders            String,
    seeds             String,
    chain             String,
    evidence_hash     String
) ENGINE = MergeTree()
  ORDER BY (event_time, hostname)
  PARTITION BY toYYYYMM(fromUnixTimestamp64Nano(event_time))
  TTL toDateTime(fromUnixTimestamp64Nano(event_time)) + INTERVAL 30 DAY DELETE
  SETTINGS index_granularity = 8192;

-- dx_order_seeds — one row per ORDER dx opens (entlein/dx#136 evidence-loss fix).
-- dx owns the order identity: it computes order_id and decides the dedup
-- granularity (1:1 with uniqueID today; finer — per rule/target/event — later).
-- AE only stores and surfaces exactly what dx emits, so the key is order_id and
-- NOTHING here assumes how many orders map to a uniqueID. dx INSERTs (POST-less,
-- direct CH); AE owns the DDL. ReplacingMergeTree ORDER BY (order_id) dedups
-- re-fires of the same order. NOT a pixie table.
CREATE TABLE IF NOT EXISTS forensic_db.dx_order_seeds (
    order_id   String,
    unique_id  String,
    rule_id    String,
    pod        String,
    event_time UInt64,
    hostname   String,
    case_key   String
) ENGINE = ReplacingMergeTree()
  ORDER BY (order_id)
  PARTITION BY toYYYYMM(fromUnixTimestamp64Nano(event_time))
  TTL toDateTime(fromUnixTimestamp64Nano(event_time)) + INTERVAL 30 DAY DELETE
  SETTINGS index_granularity = 8192;

-- dx_order_records — the STAMPED consulted set (entlein/dx#136 stamping model). dx
-- writes one row per (order_id, finding): each record it consulted during the workup
-- for a primary kubescape log, stamped with that log's order_id. The panels read THIS
-- (the exact consulted set) instead of a ±300s time window. event_time is derived from
-- time_ so px can read it (UInt64 + hostname, no Bool cols). AE owns the DDL; dx
-- INSERTs. ReplacingMergeTree collapses re-stamps of the same (order_id,row).
CREATE TABLE IF NOT EXISTS forensic_db.dx_order_records (
    order_id    String,
    unique_id   String,
    src_table   String,
    vector      String,
    source      String,
    time_       Int64,
    pod         String,
    remote_addr String,
    path        String,
    comm        String,
    dns_name    String,
    hostname    String,
    event_time  UInt64 DEFAULT toUInt64(time_)
) ENGINE = ReplacingMergeTree()
  ORDER BY (order_id, src_table, time_, pod, remote_addr, path, comm, dns_name)
  PARTITION BY toYYYYMM(fromUnixTimestamp64Nano(event_time))
  TTL toDateTime(fromUnixTimestamp64Nano(event_time)) + INTERVAL 30 DAY DELETE
  SETTINGS index_granularity = 8192;

-- ── NEW identity model (added ALONGSIDE dx_order_seeds/records, which stay) ───
-- dx_orders — one row per kubescape detection INSTANT. order_id is TRULY unique =
-- hash(uniqueID|Disc|event_time_ns). kubescape_uid/disc are provenance only, NEVER
-- keys. dx INSERTs; AE owns the DDL.
CREATE TABLE IF NOT EXISTS forensic_db.dx_orders (
    order_id      String,
    kubescape_uid String,
    rule_id       String,
    disc          String,
    pod           String,
    event_time    UInt64,
    hostname      String
) ENGINE = ReplacingMergeTree()
  ORDER BY (order_id)
  PARTITION BY toYYYYMM(fromUnixTimestamp64Nano(event_time))
  TTL toDateTime(fromUnixTimestamp64Nano(event_time)) + INTERVAL 30 DAY DELETE
  SETTINGS index_granularity = 8192;

-- dx_order_edges — the identity bridge. One row per (order, consulted pixie row):
-- links order_id to a base-table row via unique_id = the dx-computed content hash
-- of the row's fields (FNV-1a 64, lowercase hex String), the SAME value dx stamps
-- onto that base row's unique_id column — so they match by construction, no
-- CH-side hashing. String (not UInt64): a 64-bit integer does not survive a JSON
-- decode through float64. Many-to-many: a row consulted by N orders → N edges;
-- re-stamps collapse. dx INSERTs.
CREATE TABLE IF NOT EXISTS forensic_db.dx_order_edges (
    order_id   String,
    src_table  String,
    unique_id  String,
    hostname   String,
    event_time UInt64 DEFAULT 0
) ENGINE = ReplacingMergeTree()
  ORDER BY (order_id, src_table, unique_id)
  SETTINGS index_granularity = 8192;

-- dx_ord__conn_stats — join view: conn_stats rows consulted for an order, via the
-- bridge (edge.unique_id = conn_stats.unique_id). Panel filters by order_id.
CREATE VIEW IF NOT EXISTS forensic_db.dx_ord__conn_stats AS
SELECT
    e.order_id AS order_id,
    toString(c.time_) AS ts,
    toInt64(toUnixTimestamp64Nano(c.time_)) AS row_time,
    toUInt64(toUnixTimestamp64Nano(c.event_time)) AS event_time,
    c.namespace AS namespace,
    c.pod AS pod,
    c.remote_addr AS remote_addr,
    c.remote_port AS remote_port,
    c.trace_role AS trace_role,
    c.protocol AS protocol,
    c.conn_open AS conn_open,
    c.conn_close AS conn_close,
    c.conn_active AS conn_active,
    c.bytes_sent AS bytes_sent,
    e.hostname AS hostname
FROM forensic_db.dx_order_edges AS e
INNER JOIN forensic_db.conn_stats AS c ON c.unique_id = e.unique_id
WHERE e.src_table = 'conn_stats';

-- ── dx dark-vector tracepoint tables (entlein/dx#126) ────────────────────────
-- Fed by AE-owned bpftrace UpsertTracepoint probes (constantly enabled, no TTL).
-- Emit raw kernel pid+comm (NOT upid); namespace/pod enriched at pull time via a
-- process_stats join on pid. One column per line (schema-verify parser is line-oriented).
-- (dx_dcsnoop superseded by forensic_db.dc_snoop — canonical DateTime64(9)/Int64
--  schema with full k8s metadata; see the dark-vector section above.)
CREATE TABLE IF NOT EXISTS forensic_db.dx_vfs_events (
  time_ DateTime64(9, 'UTC'),
  pid Int64,
  comm String,
  op String,
  file String,
  namespace String,
  pod String,
  container String,
  hostname String,
  event_time DateTime64(9, 'UTC')
) ENGINE = MergeTree ORDER BY (event_time, pod);

CREATE TABLE IF NOT EXISTS forensic_db.dx_unlink (
  time_ DateTime64(9, 'UTC'),
  pid Int64,
  comm String,
  op String,
  file String,
  namespace String,
  pod String,
  container String,
  hostname String,
  event_time DateTime64(9, 'UTC')
) ENGINE = MergeTree ORDER BY (event_time, pod);

CREATE TABLE IF NOT EXISTS forensic_db.dx_dlookup (
  time_ DateTime64(9, 'UTC'),
  pid Int64,
  comm String,
  file String,
  namespace String,
  pod String,
  container String,
  hostname String,
  event_time DateTime64(9, 'UTC')
) ENGINE = MergeTree ORDER BY (event_time, pod);

CREATE TABLE IF NOT EXISTS forensic_db.dx_mprotect (
  time_ DateTime64(9, 'UTC'),
  pid Int64,
  comm String,
  prot UInt64,
  namespace String,
  pod String,
  container String,
  hostname String,
  event_time DateTime64(9, 'UTC')
) ENGINE = MergeTree ORDER BY (event_time, pod);

-- (dx_creds superseded by forensic_db.creds_change — canonical schema with
--  old_uid/new_uid + full k8s metadata.)
CREATE TABLE IF NOT EXISTS forensic_db.dx_bpf (
  time_ DateTime64(9, 'UTC'),
  pid Int64,
  comm String,
  namespace String,
  pod String,
  container String,
  hostname String,
  event_time DateTime64(9, 'UTC')
) ENGINE = MergeTree ORDER BY (event_time, pod);

CREATE TABLE IF NOT EXISTS forensic_db.dx_ptrace (
  time_ DateTime64(9, 'UTC'),
  pid Int64,
  comm String,
  namespace String,
  pod String,
  container String,
  hostname String,
  event_time DateTime64(9, 'UTC')
) ENGINE = MergeTree ORDER BY (event_time, pod);

-- dc_snoop (dentry cache, V1/V2 process+file) — exported via the OTel/ClickHouse
-- retention plugin (px.export). pid-keyed; t = R (reference) / M (miss).
-- One column per line (schema-verify parser is line-oriented).
CREATE TABLE IF NOT EXISTS forensic_db.dc_snoop (
  time_ DateTime64(9, 'UTC'),
  pid Int64,
  comm String,
  t String,
  file String,
  namespace String,
  pod String,
  container String,
  hostname String,
  event_time DateTime64(9, 'UTC'),
  unique_id String DEFAULT ''
) ENGINE = ReplacingMergeTree ORDER BY (time_, pid, comm, t, file, pod);

-- stack_trace (native continuous profiler stack_traces.beta, V9) — OTel export.
CREATE TABLE IF NOT EXISTS forensic_db.stack_trace (
  time_ DateTime64(9, 'UTC'),
  upid String,
  namespace String,
  pod String,
  container String,
  hostname String,
  stack_trace_id Int64,
  stack_trace String,
  count Int64,
  event_time DateTime64(9, 'UTC'),
  unique_id String DEFAULT ''
) ENGINE = ReplacingMergeTree ORDER BY (time_, upid, stack_trace_id, pod);

-- creds_change (commit_creds privilege-escalation to root, V7) — OTel export.
CREATE TABLE IF NOT EXISTS forensic_db.creds_change (
  time_ DateTime64(9, 'UTC'),
  pid Int64,
  comm String,
  old_uid Int64,
  new_uid Int64,
  namespace String,
  pod String,
  container String,
  hostname String,
  event_time DateTime64(9, 'UTC')
) ENGINE = ReplacingMergeTree ORDER BY (time_, pid, comm, old_uid, new_uid, pod);

-- ── Order-UUID pre-correlation views (entlein/dx#136) ────────────────────────
-- The px/dx_evidence_graph multi-panel dashboard reads these. Each is created on
-- boot AFTER its base table (Apply is fatal on a missing base): all bases are
-- OperatorOwned, and kubescape_logs is ensured in OperatorOwnedTables just before
-- these views. px read contract: expose event_time UInt64 + hostname + NO Bool cols;
-- ts=toString(time_) readable, row_time Int64 ns for the PxL interval-join. Views
-- are not pixie socket_tracer tables → absent from PixieTables().

-- dx_anomaly_orders: ONE row per order dx opened. order_id is dx-assigned and
-- dx owns its granularity (hash(uniqueID) = 1:1 with the log today; finer later),
-- so the view dedups on order_id and makes NO assumption about orders-per-uniqueID.
-- lo/hi are kept for reference (the ±300s span); the CONSULTED records for the
-- order live in dx_order_records, stamped with this order_id.
CREATE VIEW IF NOT EXISTS forensic_db.dx_anomaly_orders AS
SELECT unique_id AS uniqueID, rule_id AS rule, pod,
       toInt64(event_time) - 300000000000 AS lo,
       toInt64(event_time) + 300000000000 AS hi,
       order_id,
       hostname, event_time
FROM forensic_db.dx_order_seeds
LIMIT 1 BY order_id;

-- dx_kubescape_anomalies: L1 kill-chain graph (subject_pod -> target), deduped by uniqueID.
CREATE VIEW IF NOT EXISTS forensic_db.dx_kubescape_anomalies AS
SELECT JSONExtractString(BaseRuntimeMetadata, 'uniqueID') AS uniqueID,
       concat(JSONExtractString(RuntimeK8sDetails, 'podNamespace'), '/', JSONExtractString(RuntimeK8sDetails, 'podName')) AS subject_pod,
       RuleID AS rule,
       JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'process'), 'name') AS process,
       multiIf(JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'dns'), 'domain') != '', JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'dns'), 'domain'), JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'network'), 'dstIP') != '', JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'network'), 'dstIP'), JSONExtractString(JSONExtractRaw(BaseRuntimeMetadata, 'arguments'), 'path') != '', JSONExtractString(JSONExtractRaw(BaseRuntimeMetadata, 'arguments'), 'path'), JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'file'), 'name') != '', concat(JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'file'), 'directory'), '/', JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'file'), 'name')), 'unknown') AS target,
       multiIf(JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'dns'), 'domain') != '', 'domain', JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'network'), 'dstIP') != '', 'endpoint', (JSONExtractString(JSONExtractRaw(BaseRuntimeMetadata, 'arguments'), 'path') != '') OR (JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'file'), 'name') != ''), 'file', 'other') AS target_kind,
       toInt8OrZero(JSONExtractString(BaseRuntimeMetadata, 'severity')) AS severity,
       message AS alert, hostname, event_time
FROM forensic_db.kubescape_logs
WHERE RuleID != '' AND JSONExtractString(BaseRuntimeMetadata, 'uniqueID') != ''
LIMIT 1 BY uniqueID;

-- dx_src__kubescape_logs: anomaly detail (process tree comm/cmdline/pcomm) per panel.
CREATE VIEW IF NOT EXISTS forensic_db.dx_src__kubescape_logs AS
SELECT toString(fromUnixTimestamp64Nano(toInt64(event_time))) AS ts, toInt64(event_time) AS row_time, event_time,
       RuleID, JSONExtractString(BaseRuntimeMetadata, 'uniqueID') AS uniqueID,
       JSONExtractString(JSONExtractRaw(RuntimeProcessDetails, 'processTree'), 'comm') AS comm,
       JSONExtractString(JSONExtractRaw(RuntimeProcessDetails, 'processTree'), 'pcomm') AS parent,
       JSONExtractString(JSONExtractRaw(RuntimeProcessDetails, 'processTree'), 'cmdline') AS cmdline,
       message AS alert,
       concat(JSONExtractString(RuntimeK8sDetails, 'podNamespace'), '/', JSONExtractString(RuntimeK8sDetails, 'podName')) AS pod, hostname
FROM forensic_db.kubescape_logs WHERE RuleID != '';

-- dx_src__stack_trace: original schema + ts/row_time/event_time.
CREATE VIEW IF NOT EXISTS forensic_db.dx_src__stack_trace AS
SELECT toString(time_) AS ts, toInt64(toUnixTimestamp64Nano(time_)) AS row_time, toUInt64(toUnixTimestamp64Nano(event_time)) AS event_time,
       namespace, pod, container, stack_trace_id, stack_trace, count, hostname
FROM forensic_db.stack_trace;

CREATE VIEW IF NOT EXISTS forensic_db.dx_ord__redis_events AS
SELECT
    e.order_id AS order_id,
    toString(c.time_) AS ts,
    toInt64(toUnixTimestamp64Nano(c.time_)) AS row_time,
    toUInt64(toUnixTimestamp64Nano(c.event_time)) AS event_time,
    c.namespace AS namespace,
    c.pod AS pod,
    c.remote_addr AS remote_addr,
    c.remote_port AS remote_port,
    c.trace_role AS trace_role,
    c.req_cmd AS req_cmd,
    c.req_args AS req_args,
    c.resp AS resp,
    c.latency AS latency,
    e.hostname AS hostname
FROM forensic_db.dx_order_edges AS e
INNER JOIN forensic_db.redis_events AS c ON c.unique_id = e.unique_id
WHERE e.src_table = 'redis_events';

CREATE VIEW IF NOT EXISTS forensic_db.dx_ord__http_events AS
SELECT
    e.order_id AS order_id,
    toString(c.time_) AS ts,
    toInt64(toUnixTimestamp64Nano(c.time_)) AS row_time,
    toUInt64(toUnixTimestamp64Nano(c.event_time)) AS event_time,
    c.namespace AS namespace,
    c.pod AS pod,
    c.remote_addr AS remote_addr,
    c.remote_port AS remote_port,
    c.req_method AS req_method,
    c.req_path AS req_path,
    c.req_body AS req_body,
    c.resp_status AS resp_status,
    c.resp_body AS resp_body,
    c.latency AS latency,
    e.hostname AS hostname
FROM forensic_db.dx_order_edges AS e
INNER JOIN forensic_db.http_events AS c ON c.unique_id = e.unique_id
WHERE e.src_table = 'http_events';

CREATE VIEW IF NOT EXISTS forensic_db.dx_ord__dns_events AS
SELECT
    e.order_id AS order_id,
    toString(c.time_) AS ts,
    toInt64(toUnixTimestamp64Nano(c.time_)) AS row_time,
    toUInt64(toUnixTimestamp64Nano(c.event_time)) AS event_time,
    c.namespace AS namespace,
    c.pod AS pod,
    c.remote_addr AS remote_addr,
    c.remote_port AS remote_port,
    c.req_body AS req_body,
    c.resp_body AS resp_body,
    c.latency AS latency,
    e.hostname AS hostname
FROM forensic_db.dx_order_edges AS e
INNER JOIN forensic_db.dns_events AS c ON c.unique_id = e.unique_id
WHERE e.src_table = 'dns_events';

CREATE VIEW IF NOT EXISTS forensic_db.dx_ord__pgsql_events AS
SELECT
    e.order_id AS order_id,
    toString(c.time_) AS ts,
    toInt64(toUnixTimestamp64Nano(c.time_)) AS row_time,
    toUInt64(toUnixTimestamp64Nano(c.event_time)) AS event_time,
    c.namespace AS namespace,
    c.pod AS pod,
    c.remote_addr AS remote_addr,
    c.remote_port AS remote_port,
    c.req AS req,
    c.resp AS resp,
    c.latency AS latency,
    e.hostname AS hostname
FROM forensic_db.dx_order_edges AS e
INNER JOIN forensic_db.pgsql_events AS c ON c.unique_id = e.unique_id
WHERE e.src_table = 'pgsql_events';

CREATE VIEW IF NOT EXISTS forensic_db.dx_ord__mysql_events AS
SELECT
    e.order_id AS order_id,
    toString(c.time_) AS ts,
    toInt64(toUnixTimestamp64Nano(c.time_)) AS row_time,
    toUInt64(toUnixTimestamp64Nano(c.event_time)) AS event_time,
    c.namespace AS namespace,
    c.pod AS pod,
    c.remote_addr AS remote_addr,
    c.remote_port AS remote_port,
    c.req_cmd AS req_cmd,
    c.req_body AS req_body,
    c.resp_status AS resp_status,
    c.resp_body AS resp_body,
    c.latency AS latency,
    e.hostname AS hostname
FROM forensic_db.dx_order_edges AS e
INNER JOIN forensic_db.mysql_events AS c ON c.unique_id = e.unique_id
WHERE e.src_table = 'mysql_events';

CREATE VIEW IF NOT EXISTS forensic_db.dx_ord__dc_snoop AS
SELECT
    e.order_id AS order_id,
    toString(c.time_) AS ts,
    toInt64(toUnixTimestamp64Nano(c.time_)) AS row_time,
    toUInt64(toUnixTimestamp64Nano(c.event_time)) AS event_time,
    c.pid AS pid,
    c.comm AS comm,
    c.t AS t,
    c.file AS file,
    c.namespace AS namespace,
    c.pod AS pod,
    c.container AS container,
    e.hostname AS hostname
FROM forensic_db.dx_order_edges AS e
INNER JOIN forensic_db.dc_snoop AS c ON c.unique_id = e.unique_id
WHERE e.src_table = 'dc_snoop';

CREATE VIEW IF NOT EXISTS forensic_db.dx_ord__stack_trace AS
SELECT
    e.order_id AS order_id,
    toString(c.time_) AS ts,
    toInt64(toUnixTimestamp64Nano(c.time_)) AS row_time,
    toUInt64(toUnixTimestamp64Nano(c.event_time)) AS event_time,
    c.namespace AS namespace,
    c.pod AS pod,
    c.container AS container,
    c.stack_trace_id AS stack_trace_id,
    c.stack_trace AS stack_trace,
    c.count AS count,
    e.hostname AS hostname
FROM forensic_db.dx_order_edges AS e
INNER JOIN forensic_db.stack_trace AS c ON c.unique_id = e.unique_id
WHERE e.src_table = 'stack_trace';

-- ── MITRE ATT&CK enrichment over kubescape_logs (px/dx_evidence_graph) ────────
-- dx_kubescape_mitre: L1 graph source — one row per (uniqueID, rule) so orders
-- keyed on (uniqueID, RuleID) all join; MITRE + resolved target from BaseRuntimeMetadata.
CREATE VIEW IF NOT EXISTS forensic_db.dx_kubescape_mitre AS
SELECT JSONExtractString(BaseRuntimeMetadata, 'uniqueID') AS uniqueID,
       concat(JSONExtractString(RuntimeK8sDetails, 'podNamespace'), '/', JSONExtractString(RuntimeK8sDetails, 'podName')) AS subject_pod,
       RuleID AS rule,
       JSONExtractString(BaseRuntimeMetadata, 'mitreTactic') AS mitre_tactic,
       JSONExtractString(BaseRuntimeMetadata, 'mitreTechnique') AS mitre_technique,
       concat(RuleID, ' · ', JSONExtractString(BaseRuntimeMetadata, 'mitreTechnique')) AS rule_mitre,
       JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'process'), 'name') AS process,
       multiIf(JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'dns'), 'domain') != '',
JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'dns'), 'domain'),
JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'network'), 'dstIP') != '',
JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'network'), 'dstIP'), JSONExtractString(JSONExtractRaw(BaseRuntimeMetadata,
'arguments'), 'path') != '', JSONExtractString(JSONExtractRaw(BaseRuntimeMetadata, 'arguments'), 'path'),
JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'file'), 'name') != '',
concat(JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'file'), 'directory'), '/',
JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'file'), 'name')), 'unknown') AS target,
       multiIf(JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'dns'), 'domain') != '', 'domain',
JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata, 'identifiers'), 'network'), 'dstIP') != '', 'endpoint',
(JSONExtractString(JSONExtractRaw(BaseRuntimeMetadata, 'arguments'), 'path') != '') OR (JSONExtractString(JSONExtractRaw(JSONExtractRaw(BaseRuntimeMetadata,
'identifiers'), 'file'), 'name') != ''), 'file', 'other') AS target_kind,
       toInt8OrZero(JSONExtractString(BaseRuntimeMetadata, 'severity')) AS severity,
       message AS alert, hostname, event_time
FROM forensic_db.kubescape_logs
WHERE RuleID != '' AND JSONExtractString(BaseRuntimeMetadata, 'uniqueID') != ''
LIMIT 1 BY uniqueID, rule;

-- dx_src__kubescape_mitre: kubescape detail panel — MITRE cols after RuleID, plus
-- process tree (comm/pcomm/cmdline). ts/row_time/event_time px-connector convention.
CREATE VIEW IF NOT EXISTS forensic_db.dx_src__kubescape_mitre AS
SELECT toString(fromUnixTimestamp64Nano(toInt64(event_time))) AS ts, toInt64(event_time) AS row_time, event_time,
       RuleID,
       JSONExtractString(BaseRuntimeMetadata, 'mitreTactic') AS mitre_tactic,
       JSONExtractString(BaseRuntimeMetadata, 'mitreTechnique') AS mitre_technique,
       JSONExtractString(BaseRuntimeMetadata, 'uniqueID') AS uniqueID,
       JSONExtractString(JSONExtractRaw(RuntimeProcessDetails, 'processTree'), 'comm') AS comm,
       JSONExtractInt(JSONExtractRaw(RuntimeProcessDetails, 'processTree'), 'pid') AS pid,
       JSONExtractString(JSONExtractRaw(RuntimeProcessDetails, 'processTree'), 'pcomm') AS parent,
       JSONExtractInt(JSONExtractRaw(RuntimeProcessDetails, 'processTree'), 'ppid') AS ppid,
       JSONExtractString(JSONExtractRaw(RuntimeProcessDetails, 'processTree'), 'cmdline') AS cmdline,
       message AS alert,
       concat(JSONExtractString(RuntimeK8sDetails, 'podNamespace'), '/', JSONExtractString(RuntimeK8sDetails, 'podName')) AS pod, hostname
FROM forensic_db.kubescape_logs WHERE RuleID != '';

-- dx_orders_win: per-order ±300s baseline/attack window for the differential
-- flamegraph (stack_diff). hostname carried so the px node-shard resolves.
CREATE VIEW IF NOT EXISTS forensic_db.dx_orders_win AS
SELECT order_id, pod,
       toInt64(event_time) - 300000000000 AS lo,
       toInt64(event_time) + 300000000000 AS hi,
       event_time, hostname
FROM forensic_db.dx_orders;

-- dx_dns_resolve: DNS resolution edges exploded from dns_events resp_body
-- (querier->resolver, then the answer tree name->CNAME / name->A). Read by the
-- dx/dns_resolve UI, time-windowed via dx_orders_win. Column named event_time
-- (int64 ns) because the px ClickHouse connector defaults its cursor there.
CREATE VIEW IF NOT EXISTS forensic_db.dx_dns_resolve AS
SELECT toInt64(toUnixTimestamp64Nano(dns_events.event_time)) AS event_time,
       toString(dns_events.event_time) AS ts, hostname,
       if(pod != '', pod, if(local_addr != '', concat('client:', local_addr), 'client')) AS from_node,
       concat(remote_addr, ':', toString(remote_port)) AS to_node,
       JSONExtractString(JSONExtractArrayRaw(req_body, 'queries')[1], 'name') AS edge_label,
       'query' AS kind
FROM forensic_db.dns_events
WHERE resp_body != '' AND resp_body != '{}'
UNION ALL
SELECT toInt64(toUnixTimestamp64Nano(dns_events.event_time)) AS event_time,
       toString(dns_events.event_time) AS ts, hostname,
       JSONExtractString(ans, 'name') AS from_node,
       concat(JSONExtractString(ans, 'cname'), JSONExtractString(ans, 'addr')) AS to_node,
       JSONExtractString(ans, 'type') AS edge_label,
       lower(JSONExtractString(ans, 'type')) AS kind
FROM forensic_db.dns_events
ARRAY JOIN JSONExtractArrayRaw(resp_body, 'answers') AS ans
WHERE resp_body != '' AND JSONExtractString(ans, 'type') != ''
  AND concat(JSONExtractString(ans, 'cname'), JSONExtractString(ans, 'addr')) NOT IN ('', '-');
