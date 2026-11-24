package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
)

// ErrCaptureNotFound is returned for unknown capture runs and exchanges
var ErrCaptureNotFound = errors.New("not found")

// HTTPRunFilter selects capture runs
type HTTPRunFilter struct {
	// Mode (manual, launcher) and State (active, finished, interrupted);
	// empty: all
	Mode  string
	State string
}

// HTTPExchangeFilter selects exchanges: of a run (in run order) or of a
// session (by start time, across runs). Empty fields do not filter.
type HTTPExchangeFilter struct {
	RunID     string
	SessionID string
	Kind      string
	State     string
	// Status: "error" (status >= 400 or no response), "ok" (< 400), or an
	// HTTP status code
	Status string
	Model  string
	// Stream: "true" or "false"
	Stream string
	// Tool is a tool name the reply requested
	Tool string
	// Search matches path, headline, models, session and request IDs
	// (case-insensitive substring)
	Search string
	// MinTokens keeps exchanges whose total processed tokens are at least
	// this many
	MinTokens int64
	From, To  time.Time
}

// HTTPStoredExchange is an exchange with what is stored beside its row
type HTTPStoredExchange struct {
	Record HTTPExchangeRecord
	Usage  *api.HTTPUsageDetail
}

const exchangeColumns = `e.exchange_id, e.run_id, e.seq, e.session_id, e.session_evidence, e.request_id, e.method, e.path,
	e.query, e.kind, e.state, e.status, e.requested_model, e.response_model, e.stream, e.stop_reason, e.message_id,
	e.retry_count, e.started_at, e.duration_ms, e.ttfb_ms, e.first_event_ms, e.request_bytes, e.response_bytes,
	e.sse_events, e.headline, CAST(to_json(COALESCE(e.tool_uses, []::VARCHAR[])) AS VARCHAR), e.tool_results,
	e.error_type, e.error_message, e.degraded, e.capture_mode`

const usageColumns = `u.exchange_id, u.input_tokens, u.output_tokens, u.cache_read_tokens, u.cache_write_tokens,
	u.cache_write_5m, u.cache_write_1h, u.cache_write_unknown, u.total_input, u.total_processed, u.totals_complete,
	u.count_tokens, u.quality, u.service_tier, CAST(u.raw_usage AS VARCHAR), u.cost_usd, u.cost_status, u.cost_reason,
	u.priced_model, u.source`

// scanExchange reads exchangeColumns followed by usageColumns
func scanExchange(scan func(dest ...any) error) (api.HTTPExchange, error) {
	var e api.HTTPExchange
	var sessionID, evidence, requestID, query, reqModel, respModel, stopReason, messageID, headline, errType, errMsg sql.NullString
	var status, retry, toolResults sql.NullInt64
	var stream, degraded sql.NullBool
	var duration, ttfb, firstEvent, reqBytes, respBytes, sse sql.NullInt64
	var toolUses string
	var u usageScan
	err := scan(&e.ID, &e.RunID, &e.Seq, &sessionID, &evidence, &requestID, &e.Method, &e.Path, &query, &e.Kind,
		&e.State, &status, &reqModel, &respModel, &stream, &stopReason, &messageID, &retry, &e.StartedAt,
		&duration, &ttfb, &firstEvent, &reqBytes, &respBytes, &sse, &headline, &toolUses, &toolResults,
		&errType, &errMsg, &degraded, &e.CaptureMode,
		&u.exchangeID, &u.input, &u.output, &u.cacheRead, &u.cacheWrite, &u.cache5m, &u.cache1h, &u.cacheUnknown,
		&u.totalInput, &u.totalProcessed, &u.complete, &u.countTokens, &u.quality, &u.serviceTier, &u.raw,
		&u.cost, &u.costStatus, &u.costReason, &u.pricedModel, &u.source)
	if err != nil {
		return e, err
	}
	e.StartedAt = e.StartedAt.UTC()
	e.SessionID, e.SessionEvidence, e.RequestID, e.Query = sessionID.String, evidence.String, requestID.String, query.String
	e.RequestedModel, e.ResponseModel, e.StopReason, e.MessageID = reqModel.String, respModel.String, stopReason.String, messageID.String
	e.Headline, e.ErrorType, e.Error = headline.String, errType.String, errMsg.String
	e.Status, e.RetryCount, e.ToolResults = int(status.Int64), int(retry.Int64), int(toolResults.Int64)
	e.Stream, e.Degraded = stream.Bool, degraded.Bool
	e.DurationMs, e.TTFBMs, e.FirstEventMs = nullInt(duration), nullInt(ttfb), nullInt(firstEvent)
	e.RequestBytes, e.ResponseBytes, e.SSEEvents = reqBytes.Int64, respBytes.Int64, sse.Int64
	e.ToolUses = []string{}
	if toolUses != "" {
		_ = json.Unmarshal([]byte(toolUses), &e.ToolUses)
	}
	if u.exchangeID.Valid {
		usage := u.toAPI()
		e.Usage = &usage
	}
	return e, nil
}

type usageScan struct {
	exchangeID                                                             sql.NullString
	input, output, cacheRead, cacheWrite, cache5m, cache1h, cacheUnknown   sql.NullInt64
	totalInput, totalProcessed, countTokens                                sql.NullInt64
	complete                                                               sql.NullBool
	quality, serviceTier, raw, costStatus, costReason, pricedModel, source sql.NullString
	cost                                                                   sql.NullFloat64
}

func (u *usageScan) toAPI() api.HTTPUsage {
	out := api.HTTPUsage{
		Usage: anthropicwire.Usage{
			InputTokens:              nullInt(u.input),
			OutputTokens:             nullInt(u.output),
			CacheReadInputTokens:     nullInt(u.cacheRead),
			CacheCreationInputTokens: nullInt(u.cacheWrite),
			CacheCreation5m:          nullInt(u.cache5m),
			CacheCreation1h:          nullInt(u.cache1h),
			ServiceTier:              u.serviceTier.String,
		},
		Totals: anthropicwire.UsageTotals{
			TotalInput:        nullInt(u.totalInput),
			TotalProcessed:    nullInt(u.totalProcessed),
			CacheWriteUnknown: nullInt(u.cacheUnknown),
			Complete:          u.complete.Bool,
		},
		Quality:     u.quality.String,
		CountTokens: nullInt(u.countTokens),
		CostStatus:  u.costStatus.String,
		CostReason:  u.costReason.String,
		PricedModel: u.pricedModel.String,
		Source:      u.source.String,
	}
	if u.raw.Valid && u.raw.String != "" {
		out.Usage.Raw = json.RawMessage(u.raw.String)
	}
	if u.cost.Valid {
		c := u.cost.Float64
		out.CostUSD = &c
	}
	if t := out.Totals.TotalInput; t != nil && *t > 0 && out.Usage.CacheReadInputTokens != nil {
		share := float64(*out.Usage.CacheReadInputTokens) / float64(*t)
		out.Totals.CacheReadShare = &share
	}
	return out
}

// seqCursor is the cursor of pages in run order
type seqCursor struct {
	Seq int64 `json:"s"`
}

func encodeSeqCursor(seq int64) string {
	b, _ := json.Marshal(seqCursor{seq})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeSeqCursor(s string) (int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	var c seqCursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.Seq < 0 {
		return 0, ErrInvalidCursor
	}
	return c.Seq, nil
}

// exchangeConditions builds the WHERE clause of an exchange filter
func exchangeConditions(f HTTPExchangeFilter) (conditions, error) {
	var c conditions
	if f.RunID != "" {
		c.add("e.run_id = ?", f.RunID)
	}
	if f.SessionID != "" {
		c.add("e.session_id = ?", f.SessionID)
	}
	if f.Kind != "" {
		c.add("e.kind = ?", f.Kind)
	}
	if f.State != "" {
		c.add("e.state = ?", f.State)
	}
	switch f.Status {
	case "":
	case "error":
		c.add("(COALESCE(e.status, 0) >= 400 OR e.status IS NULL AND e.state IN ('failed', 'aborted', 'partial', 'interrupted') OR e.error_type IS NOT NULL)")
	case "ok":
		c.add("e.status BETWEEN 1 AND 399")
	default:
		var code int
		if _, err := fmt.Sscanf(f.Status, "%d", &code); err != nil || code < 100 || code > 599 {
			return c, fmt.Errorf("invalid status filter %q", f.Status)
		}
		c.add("e.status = ?", code)
	}
	if f.Model != "" {
		c.add("(e.requested_model = ? OR e.response_model = ?)", f.Model, f.Model)
	}
	switch f.Stream {
	case "true":
		c.add("e.stream")
	case "false":
		c.add("NOT COALESCE(e.stream, false)")
	}
	if f.Tool != "" {
		c.add("list_contains(e.tool_uses, ?)", f.Tool)
	}
	if f.Search != "" {
		term := "%" + escapeLike(strings.ToLower(f.Search)) + "%"
		c.add(`(lower(e.path) LIKE ? ESCAPE '\' OR lower(COALESCE(e.headline, '')) LIKE ? ESCAPE '\'
			OR lower(COALESCE(e.requested_model, '')) LIKE ? ESCAPE '\' OR lower(COALESCE(e.response_model, '')) LIKE ? ESCAPE '\'
			OR lower(COALESCE(e.session_id, '')) LIKE ? ESCAPE '\' OR lower(COALESCE(e.request_id, '')) LIKE ? ESCAPE '\'
			OR lower(COALESCE(e.error_message, '')) LIKE ? ESCAPE '\' OR lower(array_to_string(COALESCE(e.tool_uses, []::VARCHAR[]), ' ')) LIKE ? ESCAPE '\')`,
			term, term, term, term, term, term, term, term)
	}
	if f.MinTokens > 0 {
		c.add("u.total_processed >= ?", f.MinTokens)
	}
	if !f.From.IsZero() {
		c.add("e.started_at >= ?::TIMESTAMP", formatTimeForDB(f.From))
	}
	if !f.To.IsZero() {
		c.add("e.started_at <= ?::TIMESTAMP", formatTimeForDB(f.To))
	}
	return c, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// HTTPExchanges returns a page of exchanges: of a run in run order (cursor
// on seq), otherwise by start time (cursor on start, run and seq)
func (s *DuckDBStore) HTTPExchanges(ctx context.Context, f HTTPExchangeFilter, page Page) (*api.HTTPExchangesResponse, error) {
	limit, offset, err := pageLimit(page)
	if err != nil {
		return nil, err
	}
	conds, err := exchangeConditions(f)
	if err != nil {
		return nil, err
	}
	where, whereArgs := conds.where()
	from := ` FROM http_capture_exchanges e LEFT JOIN http_capture_usage u ON u.exchange_id = e.exchange_id`

	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT count(*)"+from+where, whereArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("counting exchanges: %w", err)
	}

	pageConds := conds.and("true")
	order := " ORDER BY e.started_at, e.run_id, e.seq"
	byRun := f.RunID != ""
	if byRun {
		order = " ORDER BY e.seq"
	}
	if page.Cursor != "" {
		if byRun {
			seq, err := decodeSeqCursor(page.Cursor)
			if err != nil {
				return nil, err
			}
			pageConds.add("e.seq > ?", seq)
		} else {
			c, err := decodeCursor(page.Cursor, 2)
			if err != nil {
				return nil, err
			}
			pageConds.add(`(e.started_at > ?::TIMESTAMP OR (e.started_at = ?::TIMESTAMP AND (e.run_id > ? OR (e.run_id = ? AND e.seq > ?))))`,
				c.Time, c.Time, c.Keys[0], c.Keys[0], c.Keys[1])
		}
	}
	pageWhere, pageArgs := pageConds.where()
	query := "SELECT " + exchangeColumns + ", " + usageColumns + from + pageWhere + order + " LIMIT ? OFFSET ?"
	rows, err := s.db.QueryContext(ctx, query, append(pageArgs, limit+1, offset)...)
	if err != nil {
		return nil, fmt.Errorf("querying exchanges: %w", err)
	}
	defer rows.Close()
	out := &api.HTTPExchangesResponse{Exchanges: []api.HTTPExchange{}, Total: total}
	for rows.Next() {
		e, err := scanExchange(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("reading exchange: %w", err)
		}
		out.Exchanges = append(out.Exchanges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Exchanges) > limit {
		out.Exchanges = out.Exchanges[:limit]
		out.HasMore = true
		last := out.Exchanges[limit-1]
		if byRun {
			out.NextCursor = encodeSeqCursor(last.Seq)
		} else {
			out.NextCursor = encodeCursor(pageCursor{Time: formatTimeForDB(last.StartedAt), Keys: []string{last.RunID, fmt.Sprint(last.Seq)}})
		}
	}
	return out, nil
}

// HTTPExchange returns an exchange with everything stored beside its row
func (s *DuckDBStore) HTTPExchange(ctx context.Context, id string) (*HTTPStoredExchange, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+exchangeColumns+", "+usageColumns+`,
		CAST(e.degraded_reasons AS VARCHAR), CAST(e.request_headers AS VARCHAR), CAST(e.response_headers AS VARCHAR),
		e.request_content_type, e.request_content_encoding, e.response_content_type, e.response_content_encoding,
		CAST(e.lifecycle AS VARCHAR), CAST(e.observations AS VARCHAR), CAST(e.request_summary AS VARCHAR),
		CAST(e.response_message AS VARCHAR), e.first_message_hash,
		CAST(u.snapshots AS VARCHAR), CAST(u.cost_components AS VARCHAR), u.model
		FROM http_capture_exchanges e LEFT JOIN http_capture_usage u ON u.exchange_id = e.exchange_id
		WHERE e.exchange_id = ?`, id)
	var reasons, reqHeaders, respHeaders, reqCT, reqCE, respCT, respCE, lifecycle, observations, summary, message, hash sql.NullString
	var snapshots, components, model sql.NullString
	exchange, err := scanExchange(func(dest ...any) error {
		return row.Scan(append(dest, &reasons, &reqHeaders, &respHeaders, &reqCT, &reqCE, &respCT, &respCE,
			&lifecycle, &observations, &summary, &message, &hash, &snapshots, &components, &model)...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCaptureNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading exchange: %w", err)
	}
	out := &HTTPStoredExchange{Record: HTTPExchangeRecord{
		Exchange:                exchange,
		RequestContentType:      reqCT.String,
		RequestContentEncoding:  reqCE.String,
		ResponseContentType:     respCT.String,
		ResponseContentEncoding: respCE.String,
		FirstMessageHash:        hash.String,
	}}
	r := &out.Record
	decodeJSON(reasons, &r.DegradedReasons)
	decodeJSON(reqHeaders, &r.RequestHeaders)
	decodeJSON(respHeaders, &r.ResponseHeaders)
	decodeJSON(lifecycle, &r.Lifecycle)
	r.Observations = rawJSONColumn(observations)
	r.RequestSummary = rawJSONColumn(summary)
	r.ResponseMessage = rawJSONColumn(message)
	if r.DegradedReasons == nil {
		r.DegradedReasons = []string{}
	}
	if r.RequestHeaders == nil {
		r.RequestHeaders = []api.HTTPHeader{}
	}
	if r.ResponseHeaders == nil {
		r.ResponseHeaders = []api.HTTPHeader{}
	}
	if r.Lifecycle == nil {
		r.Lifecycle = []api.HTTPLifecycleEvent{}
	}
	if exchange.Usage != nil {
		detail := &api.HTTPUsageDetail{
			ExchangeID:     exchange.ID,
			Kind:           exchange.Kind,
			Model:          model.String,
			HTTPUsage:      *exchange.Usage,
			Snapshots:      []anthropicwire.UsageSnapshot{},
			CostComponents: []api.HTTPCostComponent{},
		}
		decodeJSON(snapshots, &detail.Snapshots)
		decodeJSON(components, &detail.CostComponents)
		if detail.Snapshots == nil {
			detail.Snapshots = []anthropicwire.UsageSnapshot{}
		}
		if detail.CostComponents == nil {
			detail.CostComponents = []api.HTTPCostComponent{}
		}
		out.Usage = detail
	}
	return out, nil
}

func decodeJSON(v sql.NullString, dest any) {
	if v.Valid && v.String != "" && v.String != "null" {
		_ = json.Unmarshal([]byte(v.String), dest)
	}
}

func rawJSONColumn(v sql.NullString) json.RawMessage {
	if !v.Valid || v.String == "" || v.String == "null" {
		return nil
	}
	return json.RawMessage(v.String)
}

// HTTPExchangeNeighbors returns the IDs of the exchanges before and after
// seq in a run
func (s *DuckDBStore) HTTPExchangeNeighbors(ctx context.Context, runID string, seq int64) (prev, next string, err error) {
	var p, n sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT exchange_id FROM http_capture_exchanges WHERE run_id = ? AND seq < ? ORDER BY seq DESC LIMIT 1),
		(SELECT exchange_id FROM http_capture_exchanges WHERE run_id = ? AND seq > ? ORDER BY seq LIMIT 1)`,
		runID, seq, runID, seq).Scan(&p, &n)
	return p.String, n.String, err
}

// HTTPDiffCandidate is an earlier request a diff may compare with
type HTTPDiffCandidate struct {
	ExchangeID       string
	Seq              int64
	Model            string
	FirstMessageHash string
	Summary          json.RawMessage
}

// HTTPDiffCandidates returns up to limit earlier exchanges of the same run,
// kind and session (sessionless exchanges compare with sessionless ones),
// newest first
func (s *DuckDBStore) HTTPDiffCandidates(ctx context.Context, runID, kind, sessionID string, beforeSeq int64, limit int) ([]HTTPDiffCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exchange_id, seq, COALESCE(requested_model, ''), COALESCE(first_message_hash, ''),
		CAST(request_summary AS VARCHAR)
		FROM http_capture_exchanges
		WHERE run_id = ? AND kind = ? AND COALESCE(session_id, '') = ? AND seq < ? AND request_summary IS NOT NULL
		ORDER BY seq DESC LIMIT ?`, runID, kind, sessionID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HTTPDiffCandidate
	for rows.Next() {
		var c HTTPDiffCandidate
		var summary sql.NullString
		if err := rows.Scan(&c.ExchangeID, &c.Seq, &c.Model, &c.FirstMessageHash, &summary); err != nil {
			return nil, err
		}
		c.Summary = rawJSONColumn(summary)
		out = append(out, c)
	}
	return out, rows.Err()
}

// HTTPSSEEvents returns a page of an exchange's SSE events in order, after
// the event with sequence afterSeq (-1: from the start), optionally of one
// type, with the counts of all of its events by type
func (s *DuckDBStore) HTTPSSEEvents(ctx context.Context, exchangeID string, typ string, page Page) (*api.HTTPSSEEventsResponse, error) {
	limit, offset, err := pageLimit(page)
	if err != nil {
		return nil, err
	}
	var c conditions
	c.add("exchange_id = ?", exchangeID)
	if typ != "" {
		c.add("type = ?", typ)
	}
	where, args := c.where()
	out := &api.HTTPSSEEventsResponse{Events: []api.HTTPSSEEvent{}, Types: map[string]int64{}}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM http_capture_sse_events"+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	typeRows, err := s.db.QueryContext(ctx, `SELECT COALESCE(type, ''), count(*) FROM http_capture_sse_events WHERE exchange_id = ? GROUP BY 1`, exchangeID)
	if err != nil {
		return nil, err
	}
	for typeRows.Next() {
		var t string
		var n int64
		if err := typeRows.Scan(&t, &n); err != nil {
			typeRows.Close()
			return nil, err
		}
		out.Types[t] = n
	}
	typeRows.Close()
	if page.Cursor != "" {
		seq, err := decodeSeqCursor(page.Cursor)
		if err != nil {
			return nil, err
		}
		c.add("seq > ?", seq)
	}
	where, args = c.where()
	rows, err := s.db.QueryContext(ctx, `SELECT seq, offset_ms, byte_offset, byte_len, event, type, block_index, delta_type,
		parse_status, note, data, data_bytes, truncated FROM http_capture_sse_events`+where+` ORDER BY seq LIMIT ? OFFSET ?`,
		append(args, limit+1, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e api.HTTPSSEEvent
		var offsetMs sql.NullFloat64
		var byteOffset, byteLen, dataBytes, index sql.NullInt64
		var event, typ, delta, status, note, data sql.NullString
		var truncated sql.NullBool
		if err := rows.Scan(&e.Seq, &offsetMs, &byteOffset, &byteLen, &event, &typ, &index, &delta, &status, &note, &data, &dataBytes, &truncated); err != nil {
			return nil, err
		}
		e.OffsetMs, e.ByteOffset, e.ByteLen, e.DataBytes = offsetMs.Float64, byteOffset.Int64, byteLen.Int64, dataBytes.Int64
		e.Event, e.Type, e.DeltaType, e.ParseStatus, e.Note, e.Data = event.String, typ.String, delta.String, status.String, note.String, data.String
		e.Truncated = truncated.Bool
		if index.Valid {
			i := int(index.Int64)
			e.Index = &i
		}
		out.Events = append(out.Events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Events) > limit {
		out.Events = out.Events[:limit]
		out.HasMore = true
		out.NextCursor = encodeSeqCursor(int64(out.Events[limit-1].Seq))
	}
	return out, nil
}

// HTTPPayloads returns the stored body references of an exchange
func (s *DuckDBStore) HTTPPayloads(ctx context.Context, exchangeID string) ([]HTTPPayloadRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exchange_id, part, variant, rel_path, COALESCE(observed_bytes, 0),
		COALESCE(observed_sha256, ''), COALESCE(stored_bytes, 0), COALESCE(stored_sha256, ''), COALESCE(content_encoding, ''),
		COALESCE(decode_status, ''), COALESCE(truncated, false), COALESCE(sanitized, false), COALESCE(redactions, 0)
		FROM http_capture_payloads WHERE exchange_id = ? ORDER BY part, variant`, exchangeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HTTPPayloadRecord{}
	for rows.Next() {
		var p HTTPPayloadRecord
		if err := rows.Scan(&p.ExchangeID, &p.Part, &p.Variant, &p.RelPath, &p.ObservedBytes, &p.ObservedSHA256,
			&p.StoredBytes, &p.StoredSHA256, &p.ContentEncoding, &p.DecodeStatus, &p.Truncated, &p.Sanitized, &p.Redactions); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const runColumns = `r.run_id, r.name, r.note, r.mode, r.state, r.capture_mode, r.port, r.upstream, r.cwd, r.command,
	r.pid, r.exit_code, r.started_at, r.ended_at,
	(SELECT count(*) FROM http_capture_exchanges e WHERE e.run_id = r.run_id),
	(SELECT max(e.started_at) FROM http_capture_exchanges e WHERE e.run_id = r.run_id),
	(SELECT CAST(to_json(COALESCE(list_sort(list_distinct(list(e.session_id))), []::VARCHAR[])) AS VARCHAR)
		FROM http_capture_exchanges e WHERE e.run_id = r.run_id AND e.session_id IS NOT NULL),
	(SELECT count(*) FROM http_capture_exchanges e WHERE e.run_id = r.run_id AND e.session_id IS NULL),
	(SELECT COALESCE(sum(p.stored_bytes), 0) FROM http_capture_payloads p
		JOIN http_capture_exchanges e ON e.exchange_id = p.exchange_id WHERE e.run_id = r.run_id)`

func scanRun(scan func(dest ...any) error) (api.HTTPCaptureRun, error) {
	var r api.HTTPCaptureRun
	var note, upstream, cwd, command, sessions sql.NullString
	var port, pid, exitCode sql.NullInt64
	var ended, last sql.NullTime
	err := scan(&r.ID, &r.Name, &note, &r.Mode, &r.State, &r.CaptureMode, &port, &upstream, &cwd, &command,
		&pid, &exitCode, &r.StartedAt, &ended, &r.Exchanges, &last, &sessions, &r.Unattributed, &r.StoredBytes)
	if err != nil {
		return r, err
	}
	r.StartedAt = r.StartedAt.UTC()
	r.Note, r.Upstream, r.Cwd, r.Command = note.String, upstream.String, cwd.String, command.String
	r.Port, r.PID = int(port.Int64), int(pid.Int64)
	if exitCode.Valid {
		c := int(exitCode.Int64)
		r.ExitCode = &c
	}
	if ended.Valid {
		t := ended.Time.UTC()
		r.EndedAt = &t
	}
	if last.Valid {
		t := last.Time.UTC()
		r.LastExchangeAt = &t
	}
	r.SessionIDs = []string{}
	decodeJSON(sessions, &r.SessionIDs)
	if r.SessionIDs == nil {
		r.SessionIDs = []string{}
	}
	return r, nil
}

// HTTPCaptureRun returns a run
func (s *DuckDBStore) HTTPCaptureRun(ctx context.Context, runID string) (*api.HTTPCaptureRun, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM http_capture_runs r WHERE r.run_id = ?", runID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCaptureNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading run: %w", err)
	}
	return &r, nil
}

// HTTPCaptureRuns returns a page of runs, newest first
func (s *DuckDBStore) HTTPCaptureRuns(ctx context.Context, f HTTPRunFilter, page Page) (*api.HTTPCaptureRunsResponse, error) {
	limit, offset, err := pageLimit(page)
	if err != nil {
		return nil, err
	}
	var c conditions
	if f.Mode != "" {
		c.add("r.mode = ?", f.Mode)
	}
	if f.State != "" {
		c.add("r.state = ?", f.State)
	}
	where, args := c.where()
	out := &api.HTTPCaptureRunsResponse{Runs: []api.HTTPCaptureRun{}}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM http_capture_runs r"+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	if page.Cursor != "" {
		cur, err := decodeCursor(page.Cursor, 1)
		if err != nil {
			return nil, err
		}
		cond, condArgs := newestFirstAfter("r.started_at", []string{"r.run_id"}, cur.Time, []any{cur.Keys[0]})
		c.add(cond, condArgs...)
	}
	where, args = c.where()
	rows, err := s.db.QueryContext(ctx, "SELECT "+runColumns+" FROM http_capture_runs r"+where+
		" ORDER BY r.started_at DESC, r.run_id DESC LIMIT ? OFFSET ?", append(args, limit+1, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		out.Runs = append(out.Runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Runs) > limit {
		out.Runs = out.Runs[:limit]
		out.HasMore = true
		last := out.Runs[limit-1]
		out.NextCursor = encodeCursor(pageCursor{Time: formatTimeForDB(last.StartedAt), Keys: []string{last.ID}})
	}
	return out, nil
}

// HTTPSessionRuns returns the runs that carried exchanges of a session
func (s *DuckDBStore) HTTPSessionRuns(ctx context.Context, sessionID string) ([]api.HTTPCaptureRun, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+runColumns+` FROM http_capture_runs r
		WHERE r.run_id IN (SELECT DISTINCT run_id FROM http_capture_exchanges WHERE session_id = ?)
		ORDER BY r.started_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.HTTPCaptureRun{}
	for rows.Next() {
		r, err := scanRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HTTPToolLinks returns the tool links of a run or a session, by the
// sequence of their use (results without an observed use last). runActive
// tells whether uses without result are pending or unresolved.
func (s *DuckDBStore) HTTPToolLinks(ctx context.Context, runID, sessionID string) ([]api.HTTPToolLink, error) {
	var c conditions
	if runID != "" {
		c.add("t.run_id = ?", runID)
	}
	if sessionID != "" {
		c.add("t.session_id = ?", sessionID)
	}
	where, args := c.where()
	rows, err := s.db.QueryContext(ctx, `SELECT t.tool_use_id, t.name, t.session_id, t.use_exchange_id, t.use_seq, t.use_count,
		t.input, t.result_exchange_id, t.result_seq, t.result_is_error, t.result_preview, t.result_bytes, r.state
		FROM http_capture_tool_links t JOIN http_capture_runs r ON r.run_id = t.run_id`+where+`
		ORDER BY t.use_seq NULLS LAST, t.result_seq, t.tool_use_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.HTTPToolLink{}
	for rows.Next() {
		var l api.HTTPToolLink
		var name, session, useEx, input, resEx, preview, runState sql.NullString
		var useSeq, useCount, resSeq, resBytes sql.NullInt64
		var isErr sql.NullBool
		if err := rows.Scan(&l.ToolUseID, &name, &session, &useEx, &useSeq, &useCount, &input, &resEx, &resSeq, &isErr, &preview, &resBytes, &runState); err != nil {
			return nil, err
		}
		l.Name, l.SessionID, l.UseExchangeID, l.Input = name.String, session.String, useEx.String, input.String
		l.ResultExchangeID, l.ResultPreview = resEx.String, preview.String
		l.UseSeq, l.ResultSeq, l.ResultBytes, l.ResultIsError = useSeq.Int64, resSeq.Int64, resBytes.Int64, isErr.Bool
		l.Status, l.Evidence = toolLinkStatus(useEx.Valid, resEx.Valid, useCount.Int64, runState.String)
		out = append(out, l)
	}
	return out, rows.Err()
}

// toolLinkStatus classifies a tool link by what was observed
func toolLinkStatus(use, result bool, useCount int64, runState string) (status, evidence string) {
	switch {
	case use && useCount > 1:
		return api.ToolLinkAmbiguous, "o mesmo tool_use.id apareceu em mais de uma resposta"
	case use && result:
		return api.ToolLinkMatched, "tool_use.id da resposta = tool_result.tool_use_id de um request posterior (mesmo run)"
	case use && runState == api.CaptureRunActive:
		return api.ToolLinkPending, "tool_use observado; nenhum request posterior trouxe o resultado ainda"
	case use:
		return api.ToolLinkUnresolved, "tool_use observado; nenhum request posterior capturado trouxe o resultado"
	default:
		return api.ToolLinkUnresolved, "tool_result observado sem o tool_use correspondente neste run (anterior ao início da captura?)"
	}
}

// HTTPTrafficSummary aggregates the exchanges of a run or a session
func (s *DuckDBStore) HTTPTrafficSummary(ctx context.Context, runID, sessionID string) (*api.HTTPTrafficSummary, error) {
	var c conditions
	if runID != "" {
		c.add("e.run_id = ?", runID)
	}
	if sessionID != "" {
		c.add("e.session_id = ?", sessionID)
	}
	where, args := c.where()
	out := &api.HTTPTrafficSummary{Models: []string{}, CostStatus: api.CostUnavailable}
	var first, last sql.NullTime
	var lastEnd sql.NullTime
	var models sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT count(*),
		count(*) FILTER (WHERE e.kind = 'model_inference'),
		count(*) FILTER (WHERE e.kind = 'token_count'),
		count(*) FILTER (WHERE e.kind NOT IN ('model_inference', 'token_count')),
		count(*) FILTER (WHERE COALESCE(e.status, 0) >= 400 OR e.state = 'failed' OR e.error_type IS NOT NULL),
		count(*) FILTER (WHERE COALESCE(e.retry_count, 0) > 0),
		count(*) FILTER (WHERE e.state IN ('partial', 'aborted', 'interrupted')),
		count(*) FILTER (WHERE e.state = 'in_progress'),
		count(*) FILTER (WHERE e.stream),
		count(*) FILTER (WHERE e.degraded),
		min(e.started_at), max(e.started_at),
		max(e.started_at + to_milliseconds(COALESCE(e.duration_ms, 0))),
		CAST(to_json(list_sort(list_distinct(list(COALESCE(e.response_model, e.requested_model))))) AS VARCHAR)
		FROM http_capture_exchanges e`+where, args...).Scan(
		&out.Exchanges, &out.Inference, &out.TokenCounts, &out.Other, &out.Errors, &out.Retries, &out.Partial,
		&out.InProgress, &out.Streams, &out.Degraded, &first, &last, &lastEnd, &models)
	if err != nil {
		return nil, fmt.Errorf("summarizing exchanges: %w", err)
	}
	if first.Valid {
		f := first.Time.UTC()
		out.FirstAt = &f
		end := last.Time.UTC()
		if lastEnd.Valid && lastEnd.Time.After(end) {
			end = lastEnd.Time.UTC()
		}
		out.LastAt = &end
		out.DurationMs = end.Sub(f).Milliseconds()
	}
	decodeJSON(models, &out.Models)
	if out.Models == nil {
		out.Models = []string{}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT u.quality,
		COALESCE(sum(u.input_tokens), 0), COALESCE(sum(u.output_tokens), 0), COALESCE(sum(u.cache_read_tokens), 0),
		COALESCE(sum(u.cache_write_tokens), 0), COALESCE(sum(u.cache_write_5m), 0), COALESCE(sum(u.cache_write_1h), 0),
		COALESCE(sum(u.cache_write_unknown), 0), COALESCE(sum(u.total_input), 0), COALESCE(sum(u.total_processed), 0),
		count(*), COALESCE(sum(u.cost_usd), 0),
		count(*) FILTER (WHERE u.cost_status <> 'complete')
		FROM http_capture_usage u JOIN http_capture_exchanges e ON e.exchange_id = u.exchange_id`+
		where+andCond(where, "u.kind = 'model_inference' AND u.quality IN ('final', 'partial')")+` GROUP BY u.quality`, args...)
	if err != nil {
		return nil, fmt.Errorf("summarizing usage: %w", err)
	}
	defer rows.Close()
	var priced, unpriced int64
	for rows.Next() {
		var quality string
		var t api.HTTPTokenTotals
		var cost float64
		var notComplete int64
		if err := rows.Scan(&quality, &t.Input, &t.Output, &t.CacheRead, &t.CacheWrite, &t.CacheWrite5m, &t.CacheWrite1h,
			&t.CacheWriteOther, &t.TotalInput, &t.TotalProcessed, &t.Exchanges, &cost, &notComplete); err != nil {
			return nil, err
		}
		if t.TotalInput > 0 {
			share := float64(t.CacheRead) / float64(t.TotalInput)
			t.CacheReadShare = &share
		}
		if quality == api.UsageFinal {
			out.Tokens = t
		} else {
			out.PartialTokens = t
		}
		out.CostUSD += cost
		priced += t.Exchanges - notComplete
		unpriced += notComplete
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch {
	case priced > 0 && unpriced == 0:
		out.CostStatus = api.CostComplete
	case priced > 0 || out.CostUSD > 0:
		out.CostStatus = api.CostPartial
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(sum(u.count_tokens), 0)
		FROM http_capture_usage u JOIN http_capture_exchanges e ON e.exchange_id = u.exchange_id`+
		where+andCond(where, "u.kind = 'token_count'"), args...).Scan(&out.CountedTokens); err != nil {
		return nil, err
	}
	return out, nil
}

// where returns the WHERE clause of c and its arguments
func (c conditions) where() (string, []any) {
	return c.clause("WHERE"), c.args
}

// andCond appends a condition to a WHERE clause (or starts one)
func andCond(where, cond string) string {
	if where == "" {
		return " WHERE " + cond
	}
	return " AND " + cond
}

// HTTPSessionTelemetryUsage returns what AI Observer's telemetry recorded
// of a session's model requests: the request IDs of its usage events
// (OTLP api_request logs and session file usage records), to compare with
// the proxy's exchanges
func (s *DuckDBStore) HTTPSessionTelemetryRequestIDs(ctx context.Context, sessionID string) (map[string]bool, error) {
	ids, err := s.queryStrings(ctx, `SELECT DISTINCT json_extract_string(LogAttributes, '$.request_id')
		FROM otel_logs WHERE SessionId = ? AND json_extract_string(LogAttributes, '$.request_id') IS NOT NULL`, sessionID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

// HTTPSessionRequestIDs returns the request IDs of a session's inference
// exchanges
func (s *DuckDBStore) HTTPSessionRequestIDs(ctx context.Context, sessionID string) ([]string, error) {
	return s.queryStrings(ctx, `SELECT request_id FROM http_capture_exchanges
		WHERE session_id = ? AND kind = 'model_inference' AND request_id IS NOT NULL ORDER BY started_at, seq`, sessionID)
}

// HTTPExchangeCountsBySession returns the number of captured exchanges of
// each of the given sessions (sessions without any are left out)
func (s *DuckDBStore) HTTPExchangeCountsBySession(ctx context.Context, sessionIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(sessionIDs) == 0 {
		return out, nil
	}
	ids := append([]string(nil), sessionIDs...)
	sort.Strings(ids)
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, count(*) FROM http_capture_exchanges
		WHERE session_id IN (`+placeholders+`) GROUP BY session_id`, stringArgs(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// HTTPCaptureCounts returns the stored runs and exchanges, for the status
func (s *DuckDBStore) HTTPCaptureCounts(ctx context.Context) (runs, exchanges int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM http_capture_runs), (SELECT count(*) FROM http_capture_exchanges)`).Scan(&runs, &exchanges)
	return runs, exchanges, err
}

// fillSessionHTTPExchanges sets the number of captured API exchanges of
// listed sessions (the HTTP Inspector's, kept apart from their telemetry)
func (s *DuckDBStore) fillSessionHTTPExchanges(ctx context.Context, sessions []api.Session) error {
	if len(sessions) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(sessions))
	var marks []string
	var args []any
	for _, session := range sessions {
		if !seen[session.SessionID] {
			seen[session.SessionID] = true
			marks = append(marks, "?")
			args = append(args, session.SessionID)
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, COUNT(*) FROM http_capture_exchanges
		WHERE session_id IN (`+strings.Join(marks, ", ")+`) GROUP BY session_id`, args...)
	if err != nil {
		return fmt.Errorf("counting sessions' captured exchanges: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int, len(marks))
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return fmt.Errorf("scanning captured exchange count: %w", err)
		}
		counts[id] = n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range sessions {
		sessions[i].HTTPExchanges = counts[sessions[i].SessionID]
	}
	return nil
}
