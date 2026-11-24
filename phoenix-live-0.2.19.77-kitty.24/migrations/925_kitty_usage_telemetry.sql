-- Kitty: admin usage "connection observation" and per-request timing details.
-- Ported from Kitty main site 358_restore_usage_telemetry.sql (request_timing_details
-- and request_connection_traces only; user_traffic_* already exist via 906).
-- Table and column names match the main site so data can be copied verbatim.
-- Additive; older binaries neither read nor write these tables. Retention
-- (30 days) runs in the application.

-- Additive diagnostics storage; old binaries do not read or write this table.
-- No FK: usage_logs can be partitioned; retention also removes orphaned details.
CREATE TABLE IF NOT EXISTS request_timing_details (
    usage_log_id BIGINT NOT NULL,
    trace_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    detail JSONB NOT NULL,
    PRIMARY KEY (usage_log_id, trace_id)
);
CREATE INDEX IF NOT EXISTS request_timing_details_created_at_idx ON request_timing_details(created_at);

-- Diagnostic-only HTTP lifecycle records, including requests without usage.
-- No FK: retention is independent of billing and partitioned usage_logs.
CREATE TABLE IF NOT EXISTS request_connection_traces (
 trace_id UUID PRIMARY KEY,
 source VARCHAR(32) NOT NULL DEFAULT 'application',
 host VARCHAR(255) NOT NULL DEFAULT '',
 scheme VARCHAR(8) NOT NULL DEFAULT '',
 method VARCHAR(16) NOT NULL DEFAULT '',
 http_version VARCHAR(16) NOT NULL DEFAULT '',
 tls_version VARCHAR(32) NOT NULL DEFAULT '',
 upstream_status VARCHAR(64) NOT NULL DEFAULT '',
 network_error VARCHAR(64) NOT NULL DEFAULT '',

 started_at TIMESTAMPTZ NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 client_request_id VARCHAR(128) NOT NULL,
 user_id BIGINT NOT NULL,
 api_key_id BIGINT NOT NULL,
 account_id BIGINT NOT NULL DEFAULT 0,
 group_id BIGINT NOT NULL DEFAULT 0,
 model VARCHAR(256) NOT NULL DEFAULT '',
 path VARCHAR(128) NOT NULL,
 client_ip VARCHAR(64) NOT NULL DEFAULT '',
 user_agent VARCHAR(512) NOT NULL DEFAULT '',
 stream BOOLEAN NOT NULL,
 detail JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS request_connection_traces_time_idx ON request_connection_traces(started_at DESC, trace_id);
CREATE INDEX IF NOT EXISTS request_connection_traces_user_time_idx ON request_connection_traces(user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS request_connection_traces_key_time_idx ON request_connection_traces(api_key_id, started_at DESC);
CREATE INDEX IF NOT EXISTS request_connection_traces_request_idx ON request_connection_traces(request_id);
CREATE INDEX IF NOT EXISTS request_connection_traces_client_request_idx ON request_connection_traces(client_request_id);
CREATE INDEX IF NOT EXISTS request_connection_traces_host_time_idx ON request_connection_traces(host, started_at DESC);
