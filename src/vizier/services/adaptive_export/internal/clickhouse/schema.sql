-- Forensic SOC ClickHouse schema (adaptive-write feature, design rev 2)
-- ----------------------------------------------------------------------
-- Pixie type map (PixieTypeToClickHouseType):
--   TIME64NS → DateTime64(9), except event_time → DateTime64(3)
--   INT64 → Int64 | FLOAT64 → Float64 | STRING → String
--   BOOLEAN → UInt8 | UINT128 → String
-- Pixie's retention plugin adds: hostname String, event_time DateTime64(3)
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
    event_time            UInt64,
    hostname              String,
    level                 String DEFAULT '',
    message               String DEFAULT '',
    msg                   String DEFAULT '',
    processtree_depth     String DEFAULT '',
    anomaly_hash          String DEFAULT ''
) ENGINE = MergeTree()
  ORDER BY (event_time, hostname)
  PARTITION BY toYYYYMM(toDateTime(event_time))
  TTL toDateTime(event_time) + INTERVAL 30 DAY DELETE
  SETTINGS index_granularity = 8192;

-- ============================================================================
-- 13 Pixie socket_tracer tables — strongly predefined, namespace + pod added.
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
    event_time     DateTime64(3, 'UTC')
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

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
    event_time  DateTime64(3, 'UTC')
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
    event_time  DateTime64(3, 'UTC')
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

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
    event_time  DateTime64(3, 'UTC')
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

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
    event_time  DateTime64(3, 'UTC')
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
    event_time  DateTime64(3, 'UTC')
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
    event_time  DateTime64(3, 'UTC')
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
    event_time  DateTime64(3, 'UTC')
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
    event_time  DateTime64(3, 'UTC')
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
    event_time  DateTime64(3, 'UTC')
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
    event_time  DateTime64(3, 'UTC')
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
    event_time    DateTime64(3, 'UTC')
) ENGINE = MergeTree()
  PARTITION BY toYYYYMM(event_time)
  ORDER BY (hostname, event_time);

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
