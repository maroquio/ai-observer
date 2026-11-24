package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/httpturns"
)

// turnColumns are the request summary fields turn grouping reads
// (httpturns.Request), in scanTurnRequest's order
const turnColumns = `e.request_summary IS NOT NULL,
	COALESCE(TRY_CAST(json_extract(e.request_summary, '$.tools.count') AS INTEGER), 0),
	COALESCE(json_extract_string(e.request_summary, '$.system.sha256'), ''),
	COALESCE(TRY_CAST(json_extract(e.request_summary, '$.messages.count') AS INTEGER), 0),
	COALESCE(TRY_CAST(json_extract(e.request_summary, '$.lastUserIndex') AS INTEGER), -1),
	CAST(to_json(json_extract(e.request_summary, '$.toolResults[*].messageIndex')) AS VARCHAR),
	COALESCE(e.first_message_hash, ''),
	COALESCE(json_extract_string(e.request_summary, '$.messages.hashes[#-1]'), ''),
	COALESCE(json_extract_string(e.request_summary, '$.prompt'), '')`

// turnScan holds the scanned turnColumns
type turnScan struct {
	known                bool
	tools, count, lastIx int
	system, first, hash  string
	prompt               string
	results              sql.NullString
}

func (t *turnScan) dest() []any {
	return []any{&t.known, &t.tools, &t.system, &t.count, &t.lastIx, &t.results, &t.first, &t.hash, &t.prompt}
}

func (t *turnScan) request() httpturns.Request {
	r := httpturns.Request{
		Known: t.known, Tools: t.tools, SystemSHA: t.system, MessageCount: t.count,
		LastUserIndex: t.lastIx, FirstMessageHash: t.first, LastMessageHash: t.hash, Prompt: t.prompt,
	}
	if t.results.Valid {
		_ = json.Unmarshal([]byte(t.results.String), &r.ToolResultIndexes)
	}
	return r
}

// HTTPSessionTurnExchanges returns up to limit exchanges of a session (""
// those without one) in start order, with the request fields that turns
// are deduced from; truncated tells whether there were more
func (s *DuckDBStore) HTTPSessionTurnExchanges(ctx context.Context, sessionID string, limit int) (out []httpturns.Exchange, truncated bool, err error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+exchangeColumns+", "+usageColumns+", "+turnColumns+`
		FROM http_capture_exchanges e LEFT JOIN http_capture_usage u ON u.exchange_id = e.exchange_id
		WHERE COALESCE(e.session_id, '') = ?
		ORDER BY e.started_at, e.run_id, e.seq LIMIT ?`, sessionID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("querying session exchanges: %w", err)
	}
	defer rows.Close()
	out = []httpturns.Exchange{}
	for rows.Next() {
		var t turnScan
		e, err := scanExchange(func(dest ...any) error { return rows.Scan(append(dest, t.dest()...)...) })
		if err != nil {
			return nil, false, fmt.Errorf("reading exchange: %w", err)
		}
		out = append(out, httpturns.Exchange{HTTPExchange: e, Request: t.request()})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// HTTPInspectorSessions returns a page of the sessions with captured
// traffic (and one row, session "", for the exchanges without a session),
// the most recently active first, with their turns counted and the first
// turn's prompt as title
func (s *DuckDBStore) HTTPInspectorSessions(ctx context.Context, page Page) (*api.HTTPInspectorSessionsResponse, error) {
	limit, offset, err := pageLimit(page)
	if err != nil {
		return nil, err
	}
	out := &api.HTTPInspectorSessionsResponse{Sessions: []api.HTTPInspectorSession{}}
	if err := s.db.QueryRowContext(ctx, `SELECT count(DISTINCT COALESCE(session_id, '')) FROM http_capture_exchanges`).Scan(&out.Total); err != nil {
		return nil, fmt.Errorf("counting sessions: %w", err)
	}
	having := ""
	var args []any
	if page.Cursor != "" {
		cur, err := decodeCursor(page.Cursor, 1)
		if err != nil {
			return nil, err
		}
		having = ` HAVING max(e.started_at) < ?::TIMESTAMP OR (max(e.started_at) = ?::TIMESTAMP AND sid < ?)`
		args = []any{cur.Time, cur.Time, cur.Keys[0]}
	}
	usage := "u.kind = 'model_inference' AND u.quality IN ('final', 'partial')"
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(e.session_id, '') AS sid,
		min(e.started_at), max(e.started_at), max(e.started_at + to_milliseconds(COALESCE(e.duration_ms, 0))),
		count(*), count(*) FILTER (WHERE e.kind = 'model_inference'),
		count(*) FILTER (WHERE e.kind <> 'quota_probe' AND (COALESCE(e.status, 0) >= 400 OR e.state = 'failed' OR e.error_type IS NOT NULL)),
		count(*) FILTER (WHERE e.state = 'in_progress'),
		COALESCE(sum(u.total_input) FILTER (WHERE `+usage+`), 0), COALESCE(sum(u.output_tokens) FILTER (WHERE `+usage+`), 0),
		COALESCE(sum(u.cost_usd) FILTER (WHERE `+usage+`), 0),
		count(*) FILTER (WHERE `+usage+`), count(*) FILTER (WHERE `+usage+` AND u.cost_status <> 'complete'),
		CAST(to_json(list_sort(list_distinct(list(COALESCE(e.response_model, e.requested_model)) FILTER (WHERE e.kind = 'model_inference')))) AS VARCHAR),
		count(DISTINCT e.run_id)
		FROM http_capture_exchanges e LEFT JOIN http_capture_usage u ON u.exchange_id = e.exchange_id
		GROUP BY sid`+having+` ORDER BY max(e.started_at) DESC, sid DESC LIMIT ? OFFSET ?`, append(args, limit+1, offset)...)
	if err != nil {
		return nil, fmt.Errorf("querying sessions: %w", err)
	}
	defer rows.Close()
	var lastStarts []string
	for rows.Next() {
		var row api.HTTPInspectorSession
		var lastStart, lastEnd sql.NullTime
		var models sql.NullString
		var withUsage, unpriced int64
		if err := rows.Scan(&row.SessionID, &row.FirstAt, &lastStart, &lastEnd, &row.Exchanges, &row.Inference, &row.Errors,
			&row.InProgress, &row.TotalInputTokens, &row.OutputTokens, &row.CostUSD, &withUsage, &unpriced, &models, &row.Runs); err != nil {
			return nil, fmt.Errorf("reading session: %w", err)
		}
		row.FirstAt = row.FirstAt.UTC()
		row.LastAt = lastStart.Time.UTC()
		if lastEnd.Valid && lastEnd.Time.After(row.LastAt) {
			row.LastAt = lastEnd.Time.UTC()
		}
		row.Models = []string{}
		decodeJSON(models, &row.Models)
		if row.Models == nil {
			row.Models = []string{}
		}
		row.CostStatus = api.CostUnavailable
		switch {
		case withUsage > 0 && unpriced == 0:
			row.CostStatus = api.CostComplete
		case withUsage > unpriced || row.CostUSD > 0:
			row.CostStatus = api.CostPartial
		}
		out.Sessions = append(out.Sessions, row)
		lastStarts = append(lastStarts, formatTimeForDB(lastStart.Time))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Sessions) > limit {
		out.Sessions = out.Sessions[:limit]
		out.HasMore = true
		out.NextCursor = encodeCursor(pageCursor{Time: lastStarts[limit-1], Keys: []string{out.Sessions[limit-1].SessionID}})
	}
	if err := s.fillSessionTurns(ctx, out.Sessions); err != nil {
		return nil, err
	}
	return out, nil
}

// fillSessionTurns counts the turns of listed sessions and takes the first
// turn's prompt as their title, from their inferences only (turns start
// at inferences; the other exchanges do not change their number)
func (s *DuckDBStore) fillSessionTurns(ctx context.Context, sessions []api.HTTPInspectorSession) error {
	if len(sessions) == 0 {
		return nil
	}
	ids := make([]any, len(sessions))
	for i, row := range sessions {
		ids[i] = row.SessionID
	}
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(e.session_id, ''), e.kind, COALESCE(e.retry_count, 0), `+turnColumns+`
		FROM http_capture_exchanges e
		WHERE COALESCE(e.session_id, '') IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")+`)
			AND e.kind = 'model_inference' AND e.request_summary IS NOT NULL
		ORDER BY e.started_at, e.run_id, e.seq`, ids...)
	if err != nil {
		return fmt.Errorf("querying session requests: %w", err)
	}
	defer rows.Close()
	bySession := map[string][]httpturns.Exchange{}
	for rows.Next() {
		var sid string
		var e httpturns.Exchange
		var t turnScan
		if err := rows.Scan(append([]any{&sid, &e.Kind, &e.RetryCount}, t.dest()...)...); err != nil {
			return fmt.Errorf("reading session request: %w", err)
		}
		e.Request = t.request()
		bySession[sid] = append(bySession[sid], e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range sessions {
		_, turns := httpturns.Group(bySession[sessions[i].SessionID])
		sessions[i].Turns = int64(len(turns))
		if len(turns) > 0 {
			sessions[i].Title = turns[0].Prompt
		}
	}
	return nil
}
