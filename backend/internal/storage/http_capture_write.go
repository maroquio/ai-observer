package storage

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tobilg/ai-observer/internal/api"
)

// HTTPCaptureRunRecord is a capture run as stored
type HTTPCaptureRunRecord struct {
	ID            string
	Name          string
	Mode          string
	State         string
	CaptureMode   string
	Host          string
	Port          int
	Upstream      string
	Cwd           string
	Command       string
	PID           int
	StartedAt     time.Time
	ServerVersion string
}

// HTTPExchangeRecord is an exchange as stored. A started exchange is
// inserted with the request's metadata (Started); a finished one replaces
// it with everything observed (Finished).
type HTTPExchangeRecord struct {
	Exchange api.HTTPExchange

	DegradedReasons []string
	RequestHeaders  []api.HTTPHeader
	ResponseHeaders []api.HTTPHeader

	RequestContentType      string
	RequestContentEncoding  string
	ResponseContentType     string
	ResponseContentEncoding string

	Lifecycle []api.HTTPLifecycleEvent
	// Observations, RequestSummary and ResponseMessage are JSON documents
	// (anthropicwire types), stored as they are
	Observations     json.RawMessage
	RequestSummary   json.RawMessage
	ResponseMessage  json.RawMessage
	FirstMessageHash string
}

// HTTPUsageRecord is the usage of a finished exchange
type HTTPUsageRecord struct {
	ExchangeID string
	RunID      string
	SessionID  string
	Kind       string
	Model      string
	StartedAt  time.Time
	Detail     api.HTTPUsageDetail
}

// HTTPPayloadRecord references a stored body file
type HTTPPayloadRecord struct {
	ExchangeID string
	// Part is request or response; Variant safe or raw
	Part    string
	Variant string
	// RelPath is the file relative to the capture directory
	RelPath         string
	ObservedBytes   int64
	ObservedSHA256  string
	StoredBytes     int64
	StoredSHA256    string
	ContentEncoding string
	DecodeStatus    string
	Truncated       bool
	Sanitized       bool
	Redactions      int
}

// HTTPToolUseRecord is a tool_use block of a reply
type HTTPToolUseRecord struct {
	ToolUseID string
	Name      string
	// Input is the arguments' JSON as stored (redacted, or empty)
	Input string
}

// HTTPToolResultRecord is a tool_result block a request carried for the
// first time in its run
type HTTPToolResultRecord struct {
	ToolUseID string
	IsError   bool
	Preview   string
	Bytes     int64
}

// HTTPExchangeFinish is everything stored when an exchange ends
type HTTPExchangeFinish struct {
	Record      HTTPExchangeRecord
	Events      []api.HTTPSSEEvent
	Usage       *HTTPUsageRecord
	Payloads    []HTTPPayloadRecord
	ToolUses    []HTTPToolUseRecord
	ToolResults []HTTPToolResultRecord
}

// HTTPCaptureBatch is a group of capture writes stored in one transaction,
// in order
type HTTPCaptureBatch struct {
	Started  []HTTPExchangeRecord
	Finished []HTTPExchangeFinish
}

// InsertHTTPCaptureRun stores a new capture run
func (s *DuckDBStore) InsertHTTPCaptureRun(ctx context.Context, r HTTPCaptureRunRecord) error {
	return s.inWriteTx(ctx, func(w *writeTx) error {
		_, err := w.tx.ExecContext(ctx, `INSERT INTO http_capture_runs
			(run_id, name, mode, state, capture_mode, host, port, upstream, cwd, command, pid, started_at, server_version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.Name, r.Mode, r.State, r.CaptureMode, nullableString(r.Host), r.Port, nullableString(r.Upstream),
			nullableString(r.Cwd), nullableString(r.Command), nullableInt(r.PID), r.StartedAt.UTC(), nullableString(r.ServerVersion))
		return err
	})
}

// FinishHTTPCaptureRun ends a run: state finished or interrupted, with the
// launched process's exit code when known. A run that ended before keeps
// its end.
func (s *DuckDBStore) FinishHTTPCaptureRun(ctx context.Context, runID, state string, endedAt time.Time, exitCode *int, pid int) error {
	var code any
	if exitCode != nil {
		code = *exitCode
	}
	return s.inWriteTx(ctx, func(w *writeTx) error {
		res, err := w.tx.ExecContext(ctx, `UPDATE http_capture_runs
			SET state = ?, ended_at = ?, exit_code = COALESCE(?, exit_code), pid = COALESCE(?, pid)
			WHERE run_id = ? AND state = ?`,
			state, endedAt.UTC(), code, nullableInt(pid), runID, api.CaptureRunActive)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return s.runExistsTx(ctx, w, runID)
		}
		return nil
	})
}

// UpdateHTTPCaptureRun sets a run's name and note (nil keeps them)
func (s *DuckDBStore) UpdateHTTPCaptureRun(ctx context.Context, runID string, name, note *string) error {
	return s.inWriteTx(ctx, func(w *writeTx) error {
		if err := s.runExistsTx(ctx, w, runID); err != nil {
			return err
		}
		if name != nil {
			if _, err := w.tx.ExecContext(ctx, `UPDATE http_capture_runs SET name = ? WHERE run_id = ?`, *name, runID); err != nil {
				return err
			}
		}
		if note != nil {
			if _, err := w.tx.ExecContext(ctx, `UPDATE http_capture_runs SET note = ? WHERE run_id = ?`, nullableString(*note), runID); err != nil {
				return err
			}
		}
		return nil
	})
}

// runExistsTx returns ErrCaptureNotFound when the run is not stored
func (s *DuckDBStore) runExistsTx(ctx context.Context, w *writeTx, runID string) error {
	var n int
	if err := w.tx.QueryRowContext(ctx, `SELECT count(*) FROM http_capture_runs WHERE run_id = ?`, runID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrCaptureNotFound
	}
	return nil
}

// InterruptActiveHTTPCaptures marks the runs and exchanges left active by a
// server that stopped (or crashed) as interrupted. Servers call it at
// startup, before their proxy opens: no run of an earlier start can still
// receive traffic.
func (s *DuckDBStore) InterruptActiveHTTPCaptures(ctx context.Context, at time.Time) (runs, exchanges int64, err error) {
	err = s.inWriteTx(ctx, func(w *writeTx) error {
		res, err := w.tx.ExecContext(ctx, `UPDATE http_capture_exchanges
			SET state = ?, degraded = true,
			    degraded_reasons = to_json(list_append(COALESCE(CAST(degraded_reasons AS VARCHAR[]), []::VARCHAR[]), ?))
			WHERE state = ?`,
			api.ExchangeInterrupted, "o servidor parou antes do fim desta troca", api.ExchangeInProgress)
		if err != nil {
			return fmt.Errorf("interrupting exchanges: %w", err)
		}
		exchanges, _ = res.RowsAffected()
		res, err = w.tx.ExecContext(ctx, `UPDATE http_capture_runs SET state = ?, ended_at = COALESCE(ended_at, ?) WHERE state = ?`,
			api.CaptureRunInterrupted, at.UTC(), api.CaptureRunActive)
		if err != nil {
			return fmt.Errorf("interrupting runs: %w", err)
		}
		runs, _ = res.RowsAffected()
		return nil
	})
	return runs, exchanges, err
}

// StoreHTTPCaptureBatch stores started and finished exchanges in one
// transaction. A finished exchange replaces its started row (inserting it
// when the start was not stored) and adds its events, usage, payload
// references and tool links. Tool links are keyed by (run, tool use ID):
// a result that arrives before its use (stored out of order) is kept and
// joined when the use arrives; of several requests carrying a result, the
// earliest by sequence wins.
func (s *DuckDBStore) StoreHTTPCaptureBatch(ctx context.Context, b HTTPCaptureBatch) error {
	if len(b.Started) == 0 && len(b.Finished) == 0 {
		return nil
	}
	return s.inWriteTx(ctx, func(w *writeTx) error {
		for i := range b.Started {
			if err := upsertExchangeTx(ctx, w, &b.Started[i]); err != nil {
				return err
			}
		}
		var events []eventRow
		for i := range b.Finished {
			f := &b.Finished[i]
			if err := upsertExchangeTx(ctx, w, &f.Record); err != nil {
				return err
			}
			id := f.Record.Exchange.ID
			// A finish replaces whatever an earlier attempt stored
			for _, table := range []string{"http_capture_sse_events", "http_capture_payloads", "http_capture_usage"} {
				if _, err := w.tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE exchange_id = ?", id); err != nil {
					return fmt.Errorf("replacing %s: %w", table, err)
				}
			}
			for _, e := range f.Events {
				events = append(events, eventRow{id, e})
			}
			if f.Usage != nil {
				if err := insertUsageTx(ctx, w, f.Usage); err != nil {
					return err
				}
			}
			for _, p := range f.Payloads {
				if err := insertPayloadTx(ctx, w, p); err != nil {
					return err
				}
			}
			if err := linkToolsTx(ctx, w, f); err != nil {
				return err
			}
		}
		return w.appendRows(ctx, "http_capture_sse_events",
			[]string{"exchange_id", "seq", "offset_ms", "byte_offset", "byte_len", "event", "type", "block_index",
				"delta_type", "parse_status", "note", "data", "data_bytes", "truncated"},
			len(events), func(i int) []driver.Value {
				e := events[i].event
				var index driver.Value
				if e.Index != nil {
					index = int32(*e.Index)
				}
				return []driver.Value{events[i].exchangeID, int32(e.Seq), e.OffsetMs, e.ByteOffset, e.ByteLen,
					nullableString(e.Event), e.Type, index, nullableString(e.DeltaType), e.ParseStatus,
					nullableString(e.Note), nullableString(e.Data), e.DataBytes, e.Truncated}
			})
	})
}

type eventRow struct {
	exchangeID string
	event      api.HTTPSSEEvent
}

// upsertExchangeTx inserts an exchange, replacing a stored row of the same
// ID
func upsertExchangeTx(ctx context.Context, w *writeTx, r *HTTPExchangeRecord) error {
	e := &r.Exchange
	if _, err := w.tx.ExecContext(ctx, `DELETE FROM http_capture_exchanges WHERE exchange_id = ?`, e.ID); err != nil {
		return fmt.Errorf("replacing exchange: %w", err)
	}
	toolUses := e.ToolUses
	if toolUses == nil {
		toolUses = []string{}
	}
	_, err := w.tx.ExecContext(ctx, `INSERT INTO http_capture_exchanges (
		exchange_id, run_id, seq, session_id, session_evidence, request_id, method, path, query, kind, state, status,
		requested_model, response_model, stream, stop_reason, message_id, retry_count, started_at,
		duration_ms, ttfb_ms, first_event_ms, request_bytes, response_bytes, sse_events, headline,
		tool_uses, tool_results, error_type, error_message, degraded, degraded_reasons, capture_mode,
		request_headers, response_headers, request_content_type, request_content_encoding,
		response_content_type, response_content_encoding, lifecycle, observations, request_summary,
		response_message, first_message_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(?::JSON AS VARCHAR[]), ?, ?, ?, ?, ?::JSON, ?,
		?::JSON, ?::JSON, ?, ?, ?, ?, ?::JSON, ?::JSON, ?::JSON, ?::JSON, ?)`,
		e.ID, e.RunID, e.Seq, nullableString(e.SessionID), nullableString(e.SessionEvidence), nullableString(e.RequestID),
		e.Method, e.Path, nullableString(e.Query), e.Kind, e.State, nullableInt(e.Status),
		nullableString(e.RequestedModel), nullableString(e.ResponseModel), e.Stream, nullableString(e.StopReason),
		nullableString(e.MessageID), e.RetryCount, e.StartedAt.UTC(),
		nullableInt64(e.DurationMs), nullableInt64(e.TTFBMs), nullableInt64(e.FirstEventMs),
		e.RequestBytes, e.ResponseBytes, e.SSEEvents, e.Headline,
		listLiteral(toolUses), e.ToolResults, nullableString(e.ErrorType), nullableString(e.Error), e.Degraded,
		jsonText(nonNilStrings(r.DegradedReasons)), e.CaptureMode,
		jsonText(nonNilHeaders(r.RequestHeaders)), jsonText(nonNilHeaders(r.ResponseHeaders)),
		nullableString(r.RequestContentType), nullableString(r.RequestContentEncoding),
		nullableString(r.ResponseContentType), nullableString(r.ResponseContentEncoding),
		jsonText(nonNilLifecycle(r.Lifecycle)), rawOrNull(r.Observations), rawOrNull(r.RequestSummary),
		rawOrNull(r.ResponseMessage), nullableString(r.FirstMessageHash))
	if err != nil {
		return fmt.Errorf("storing exchange: %w", err)
	}
	return nil
}

func insertUsageTx(ctx context.Context, w *writeTx, u *HTTPUsageRecord) error {
	d := &u.Detail
	_, err := w.tx.ExecContext(ctx, `INSERT INTO http_capture_usage (
		exchange_id, run_id, session_id, kind, model, input_tokens, output_tokens, cache_read_tokens,
		cache_write_tokens, cache_write_5m, cache_write_1h, cache_write_unknown, total_input, total_processed,
		totals_complete, count_tokens, quality, service_tier, raw_usage, snapshots, cost_usd, cost_status,
		cost_reason, priced_model, cost_components, source, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::JSON, ?::JSON, ?, ?, ?, ?, ?::JSON, ?, ?)`,
		u.ExchangeID, u.RunID, nullableString(u.SessionID), u.Kind, nullableString(u.Model),
		ptrValue(d.Usage.InputTokens), ptrValue(d.Usage.OutputTokens), ptrValue(d.Usage.CacheReadInputTokens),
		ptrValue(d.Usage.CacheCreationInputTokens), ptrValue(d.Usage.CacheCreation5m), ptrValue(d.Usage.CacheCreation1h),
		ptrValue(d.Totals.CacheWriteUnknown), ptrValue(d.Totals.TotalInput), ptrValue(d.Totals.TotalProcessed),
		d.Totals.Complete, ptrValue(d.CountTokens), d.Quality, nullableString(d.Usage.ServiceTier),
		rawOrNull(d.Usage.Raw), jsonText(d.Snapshots), ptrValue(d.CostUSD), d.CostStatus,
		nullableString(d.CostReason), nullableString(d.PricedModel), jsonText(d.CostComponents),
		d.Source, u.StartedAt.UTC())
	if err != nil {
		return fmt.Errorf("storing usage: %w", err)
	}
	return nil
}

func insertPayloadTx(ctx context.Context, w *writeTx, p HTTPPayloadRecord) error {
	_, err := w.tx.ExecContext(ctx, `INSERT INTO http_capture_payloads (
		exchange_id, part, variant, rel_path, observed_bytes, observed_sha256, stored_bytes, stored_sha256,
		content_encoding, decode_status, truncated, sanitized, redactions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ExchangeID, p.Part, p.Variant, p.RelPath, p.ObservedBytes, nullableString(p.ObservedSHA256),
		p.StoredBytes, nullableString(p.StoredSHA256), nullableString(p.ContentEncoding),
		nullableString(p.DecodeStatus), p.Truncated, p.Sanitized, p.Redactions)
	if err != nil {
		return fmt.Errorf("storing payload reference: %w", err)
	}
	return nil
}

// linkToolsTx records the tool uses of a finished exchange's reply and the
// tool results its request carried for the first time
func linkToolsTx(ctx context.Context, w *writeTx, f *HTTPExchangeFinish) error {
	e := &f.Record.Exchange
	for _, u := range f.ToolUses {
		_, err := w.tx.ExecContext(ctx, `INSERT INTO http_capture_tool_links
			(run_id, tool_use_id, name, session_id, use_exchange_id, use_seq, use_count, input)
			VALUES (?, ?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT (run_id, tool_use_id) DO UPDATE SET
				use_count = COALESCE(http_capture_tool_links.use_count, 0) + 1,
				name = COALESCE(http_capture_tool_links.name, excluded.name),
				session_id = COALESCE(http_capture_tool_links.session_id, excluded.session_id),
				input = CASE WHEN http_capture_tool_links.use_exchange_id IS NULL OR excluded.use_seq < http_capture_tool_links.use_seq
					THEN excluded.input ELSE http_capture_tool_links.input END,
				use_seq = CASE WHEN http_capture_tool_links.use_exchange_id IS NULL OR excluded.use_seq < http_capture_tool_links.use_seq
					THEN excluded.use_seq ELSE http_capture_tool_links.use_seq END,
				use_exchange_id = CASE WHEN http_capture_tool_links.use_exchange_id IS NULL OR excluded.use_seq < http_capture_tool_links.use_seq
					THEN excluded.use_exchange_id ELSE http_capture_tool_links.use_exchange_id END`,
			e.RunID, u.ToolUseID, nullableString(u.Name), nullableString(e.SessionID), e.ID, e.Seq, nullableString(u.Input))
		if err != nil {
			return fmt.Errorf("storing tool use: %w", err)
		}
	}
	for _, r := range f.ToolResults {
		_, err := w.tx.ExecContext(ctx, `INSERT INTO http_capture_tool_links
			(run_id, tool_use_id, session_id, result_exchange_id, result_seq, result_is_error, result_preview, result_bytes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (run_id, tool_use_id) DO UPDATE SET
				session_id = COALESCE(http_capture_tool_links.session_id, excluded.session_id),
				result_is_error = CASE WHEN http_capture_tool_links.result_seq IS NULL OR excluded.result_seq < http_capture_tool_links.result_seq
					THEN excluded.result_is_error ELSE http_capture_tool_links.result_is_error END,
				result_preview = CASE WHEN http_capture_tool_links.result_seq IS NULL OR excluded.result_seq < http_capture_tool_links.result_seq
					THEN excluded.result_preview ELSE http_capture_tool_links.result_preview END,
				result_bytes = CASE WHEN http_capture_tool_links.result_seq IS NULL OR excluded.result_seq < http_capture_tool_links.result_seq
					THEN excluded.result_bytes ELSE http_capture_tool_links.result_bytes END,
				result_exchange_id = CASE WHEN http_capture_tool_links.result_seq IS NULL OR excluded.result_seq < http_capture_tool_links.result_seq
					THEN excluded.result_exchange_id ELSE http_capture_tool_links.result_exchange_id END,
				result_seq = CASE WHEN http_capture_tool_links.result_seq IS NULL OR excluded.result_seq < http_capture_tool_links.result_seq
					THEN excluded.result_seq ELSE http_capture_tool_links.result_seq END`,
			e.RunID, r.ToolUseID, nullableString(e.SessionID), e.ID, e.Seq, r.IsError, nullableString(r.Preview), r.Bytes)
		if err != nil {
			return fmt.Errorf("storing tool result: %w", err)
		}
	}
	return nil
}

// DeleteHTTPCaptureRun deletes a run and all of its rows. The caller
// removes the run's body files (httpcapture.BlobStore).
func (s *DuckDBStore) DeleteHTTPCaptureRun(ctx context.Context, runID string) error {
	return s.inWriteTx(ctx, func(w *writeTx) error {
		if err := s.runExistsTx(ctx, w, runID); err != nil {
			return err
		}
		const ofRun = `exchange_id IN (SELECT exchange_id FROM http_capture_exchanges WHERE run_id = ?)`
		for _, stmt := range []string{
			`DELETE FROM http_capture_sse_events WHERE ` + ofRun,
			`DELETE FROM http_capture_payloads WHERE ` + ofRun,
			`DELETE FROM http_capture_usage WHERE run_id = ?`,
			`DELETE FROM http_capture_tool_links WHERE run_id = ?`,
			`DELETE FROM http_capture_exchanges WHERE run_id = ?`,
			`DELETE FROM http_capture_runs WHERE run_id = ?`,
		} {
			if _, err := w.tx.ExecContext(ctx, stmt, runID); err != nil {
				return fmt.Errorf("deleting run: %w", err)
			}
		}
		return nil
	})
}

// HTTPCaptureRunsEndedBefore returns the runs that are not active and whose
// last activity (end, or start) is before cutoff, oldest first
func (s *DuckDBStore) HTTPCaptureRunsEndedBefore(ctx context.Context, cutoff time.Time) ([]string, error) {
	return s.queryStrings(ctx, `SELECT run_id FROM http_capture_runs
		WHERE state <> ? AND COALESCE(ended_at, started_at) < ?
		ORDER BY COALESCE(ended_at, started_at), run_id`, api.CaptureRunActive, cutoff.UTC())
}

// HTTPCaptureRunsOldestFirst returns the runs that are not active, oldest
// first, with the bytes of their stored bodies
func (s *DuckDBStore) HTTPCaptureRunsOldestFirst(ctx context.Context) ([]RunSize, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.run_id, COALESCE(sum(p.stored_bytes), 0)
		FROM http_capture_runs r
		LEFT JOIN http_capture_exchanges e ON e.run_id = r.run_id
		LEFT JOIN http_capture_payloads p ON p.exchange_id = e.exchange_id
		WHERE r.state <> ?
		GROUP BY r.run_id, r.started_at
		ORDER BY r.started_at, r.run_id`, api.CaptureRunActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunSize
	for rows.Next() {
		var r RunSize
		if err := rows.Scan(&r.RunID, &r.Bytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunSize is a run and the bytes of its stored bodies
type RunSize struct {
	RunID string
	Bytes int64
}

// HTTPCapturePayloadPaths returns the stored file paths (relative to the
// capture directory) of all runs, for the reconciliation of orphan files
func (s *DuckDBStore) HTTPCapturePayloadPaths(ctx context.Context) (map[string]bool, error) {
	paths, err := s.queryStrings(ctx, `SELECT rel_path FROM http_capture_payloads`)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set, nil
}

// HTTPCaptureRunIDs returns the IDs of all stored runs
func (s *DuckDBStore) HTTPCaptureRunIDs(ctx context.Context) ([]string, error) {
	return s.queryStrings(ctx, `SELECT run_id FROM http_capture_runs ORDER BY run_id`)
}

func (s *DuckDBStore) queryStrings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func nullableInt(v int) driver.Value {
	if v == 0 {
		return nil
	}
	return int64(v)
}

func nullableInt64(v *int64) driver.Value {
	if v == nil {
		return nil
	}
	return *v
}

// ptrValue returns *p, or NULL for nil
func ptrValue[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// jsonText encodes v for a JSON column
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// rawOrNull returns a JSON document, or NULL when empty
func rawOrNull(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// listLiteral formats a list of strings for a ?::VARCHAR[] parameter
func listLiteral(values []string) string {
	b, _ := json.Marshal(values)
	return string(b)
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilHeaders(v []api.HTTPHeader) []api.HTTPHeader {
	if v == nil {
		return []api.HTTPHeader{}
	}
	return v
}

func nonNilLifecycle(v []api.HTTPLifecycleEvent) []api.HTTPLifecycleEvent {
	if v == nil {
		return []api.HTTPLifecycleEvent{}
	}
	return v
}
