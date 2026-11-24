package storage

// The HTTP Inspector's tables (internal/httpproxy, internal/httpcapture).
// They hold what the capture proxy observed between a client and the
// Anthropic API: runs, exchanges, SSE events, usage, tool links and the
// references to stored bodies (files in the capture directory, never in the
// database). They are separate from the telemetry tables, so capture data
// never adds to OTLP or session file metrics.
//
// They are part of the base schema (CREATE ... IF NOT EXISTS, run on every
// start): new tables need no one-time migration, and the schema version
// stays the upstream's. Columns added later go into a migration.
//
// Only primary keys are indexed: exchanges are looked up by ID, runs by
// ID, tool links by (run, tool use ID). SSE events have no index: they are
// appended per exchange in order, so DuckDB's zonemaps on exchange_id serve
// the per-exchange reads; an ART index on millions of event rows would
// slow inserts for no measured gain (see the telemetry tables).
const schemaHTTPCapture = `
CREATE TABLE IF NOT EXISTS http_capture_runs (
    run_id          VARCHAR PRIMARY KEY,
    name            VARCHAR NOT NULL,
    note            VARCHAR,
    mode            VARCHAR NOT NULL,
    state           VARCHAR NOT NULL,
    capture_mode    VARCHAR NOT NULL,
    host            VARCHAR,
    port            INTEGER,
    upstream        VARCHAR,
    cwd             VARCHAR,
    command         VARCHAR,
    pid             INTEGER,
    exit_code       INTEGER,
    started_at      TIMESTAMP NOT NULL,
    ended_at        TIMESTAMP,
    server_version  VARCHAR
);

CREATE TABLE IF NOT EXISTS http_capture_exchanges (
    exchange_id               VARCHAR PRIMARY KEY,
    run_id                    VARCHAR NOT NULL,
    seq                       BIGINT NOT NULL,
    session_id                VARCHAR,
    session_evidence          VARCHAR,
    request_id                VARCHAR,
    method                    VARCHAR NOT NULL,
    path                      VARCHAR NOT NULL,
    query                     VARCHAR,
    kind                      VARCHAR NOT NULL,
    state                     VARCHAR NOT NULL,
    status                    INTEGER,
    requested_model           VARCHAR,
    response_model            VARCHAR,
    stream                    BOOLEAN,
    stop_reason               VARCHAR,
    message_id                VARCHAR,
    retry_count               INTEGER,
    started_at                TIMESTAMP NOT NULL,
    duration_ms               BIGINT,
    ttfb_ms                   BIGINT,
    first_event_ms            BIGINT,
    request_bytes             BIGINT,
    response_bytes            BIGINT,
    sse_events                BIGINT,
    headline                  VARCHAR,
    tool_uses                 VARCHAR[],
    tool_results              INTEGER,
    error_type                VARCHAR,
    error_message             VARCHAR,
    degraded                  BOOLEAN,
    degraded_reasons          JSON,
    capture_mode              VARCHAR NOT NULL,
    request_headers           JSON,
    response_headers          JSON,
    request_content_type      VARCHAR,
    request_content_encoding  VARCHAR,
    response_content_type     VARCHAR,
    response_content_encoding VARCHAR,
    lifecycle                 JSON,
    observations              JSON,
    request_summary           JSON,
    response_message          JSON,
    first_message_hash        VARCHAR
);

CREATE TABLE IF NOT EXISTS http_capture_sse_events (
    exchange_id   VARCHAR NOT NULL,
    seq           INTEGER NOT NULL,
    offset_ms     DOUBLE,
    byte_offset   BIGINT,
    byte_len      BIGINT,
    event         VARCHAR,
    type          VARCHAR,
    block_index   INTEGER,
    delta_type    VARCHAR,
    parse_status  VARCHAR,
    note          VARCHAR,
    data          VARCHAR,
    data_bytes    BIGINT,
    truncated     BOOLEAN
);

CREATE TABLE IF NOT EXISTS http_capture_usage (
    exchange_id          VARCHAR PRIMARY KEY,
    run_id               VARCHAR NOT NULL,
    session_id           VARCHAR,
    kind                 VARCHAR NOT NULL,
    model                VARCHAR,
    input_tokens         BIGINT,
    output_tokens        BIGINT,
    cache_read_tokens    BIGINT,
    cache_write_tokens   BIGINT,
    cache_write_5m       BIGINT,
    cache_write_1h       BIGINT,
    cache_write_unknown  BIGINT,
    total_input          BIGINT,
    total_processed      BIGINT,
    totals_complete      BOOLEAN,
    count_tokens         BIGINT,
    quality              VARCHAR NOT NULL,
    service_tier         VARCHAR,
    raw_usage            JSON,
    snapshots            JSON,
    cost_usd             DOUBLE,
    cost_status          VARCHAR NOT NULL,
    cost_reason          VARCHAR,
    priced_model         VARCHAR,
    cost_components      JSON,
    source               VARCHAR NOT NULL,
    started_at           TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS http_capture_tool_links (
    run_id              VARCHAR NOT NULL,
    tool_use_id         VARCHAR NOT NULL,
    name                VARCHAR,
    session_id          VARCHAR,
    use_exchange_id     VARCHAR,
    use_seq             BIGINT,
    use_count           INTEGER,
    input               VARCHAR,
    result_exchange_id  VARCHAR,
    result_seq          BIGINT,
    result_is_error     BOOLEAN,
    result_preview      VARCHAR,
    result_bytes        BIGINT,
    PRIMARY KEY (run_id, tool_use_id)
);

CREATE TABLE IF NOT EXISTS http_capture_payloads (
    exchange_id      VARCHAR NOT NULL,
    part             VARCHAR NOT NULL,
    variant          VARCHAR NOT NULL,
    rel_path         VARCHAR NOT NULL,
    observed_bytes   BIGINT,
    observed_sha256  VARCHAR,
    stored_bytes     BIGINT,
    stored_sha256    VARCHAR,
    content_encoding VARCHAR,
    decode_status    VARCHAR,
    truncated        BOOLEAN,
    sanitized        BOOLEAN,
    redactions       INTEGER,
    PRIMARY KEY (exchange_id, part, variant)
);
`

// HTTPCaptureTables lists the HTTP Inspector's tables, children first (the
// order deletions use)
var HTTPCaptureTables = []string{
	"http_capture_sse_events",
	"http_capture_payloads",
	"http_capture_usage",
	"http_capture_tool_links",
	"http_capture_exchanges",
	"http_capture_runs",
}
