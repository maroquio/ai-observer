package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/apigen"
	"github.com/tobilg/ai-observer/internal/apigen/spec"
	"github.com/tobilg/ai-observer/internal/config"
	"github.com/tobilg/ai-observer/internal/storage"
)

// fakeKey builds a credential-shaped value at runtime (no literal for
// secret scanners)
func fakeKey() string {
	return "sk-ant-api03-" + strings.Repeat("Zq7w", 12)
}

const e2eModel = "claude-sonnet-4-5-20250929"
const e2eSession = "e2e00000-1111-2222-3333-444444444444"

// fakeAnthropic answers like the Anthropic API, keyless: a greeting, a
// tool use when asked for one, a final reply when a tool result comes back,
// and count_tokens
type fakeAnthropic struct {
	mu   sync.Mutex
	n    int
	keys []string
}

func (f *fakeAnthropic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.n++
	n := f.n
	f.keys = append(f.keys, r.Header.Get("X-Api-Key"))
	f.mu.Unlock()
	w.Header().Set("Request-Id", fmt.Sprintf("req_e2e_%d", n))
	if r.URL.Path == "/v1/messages/count_tokens" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"input_tokens":3000}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	ev := func(typ, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, data)
		w.(http.Flusher).Flush()
	}
	start := func(id, usage string) {
		ev("message_start", `{"type":"message_start","message":{"id":"`+id+`","type":"message","role":"assistant","model":"`+e2eModel+`","content":[],"stop_reason":null,"stop_sequence":null,"usage":`+usage+`}}`)
	}
	text := func(index int, s string) {
		ev("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index))
		for _, part := range strings.SplitAfter(s, " ") {
			b, _ := json.Marshal(part)
			ev("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, index, b))
		}
		ev("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
	}
	stop := func(reason string, output int) {
		ev("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"%s","stop_sequence":null},"usage":{"output_tokens":%d}}`, reason, output))
		ev("message_stop", `{"type":"message_stop"}`)
	}
	switch {
	case bytes.Contains(body, []byte(`"tool_result"`)):
		start("msg_e2e_final", `{"input_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":2000,"output_tokens":1}`)
		text(0, "O arquivo contém uma linha.")
		stop("end_turn", 30)
	case bytes.Contains(body, []byte("use a tool")):
		start("msg_e2e_tool", `{"input_tokens":20,"cache_creation_input_tokens":1500,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":1500,"ephemeral_1h_input_tokens":0},"output_tokens":1}`)
		text(0, "Vou ler o arquivo.")
		ev("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_E2E","name":"Read","input":{}}}`)
		ev("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"/tmp/demo.txt\"}"}}`)
		ev("content_block_stop", `{"type":"content_block_stop","index":1}`)
		stop("tool_use", 40)
	default:
		start("msg_e2e_hello", `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}`)
		text(0, "Olá! Tudo bem?")
		stop("end_turn", 5)
	}
}

func captureConfig(t *testing.T, upstream string, enabled bool) *config.Config {
	cfg := getTestConfig(t)
	cfg.HTTPProxy = config.HTTPProxyConfig{
		Enabled: enabled, Host: "127.0.0.1", Port: 0, Upstream: upstream,
		CaptureMode: config.CaptureSafe, MaxBodyBytes: 32 << 20, QueueSize: 1024, RetentionDays: -1,
	}
	return cfg
}

func e2eBody(stream bool, messages ...string) string {
	return `{"model":"` + e2eModel + `","max_tokens":1000,"stream":` + fmt.Sprint(stream) +
		`,"system":[{"type":"text","text":"Você é um assistente.","cache_control":{"type":"ephemeral"}}]` +
		`,"tools":[{"name":"Read","description":"Lê um arquivo","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}]` +
		`,"metadata":{"user_id":"{\"device_id\":\"d\",\"account_uuid\":\"\",\"session_id\":\"` + e2eSession + `\"}"}` +
		`,"messages":[` + strings.Join(messages, ",") + `]}`
}

func sendThroughProxy(t *testing.T, proxyURL, path, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, proxyURL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", fakeKey())
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("X-Claude-Code-Session-Id", e2eSession)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", path, resp.StatusCode)
	}
}

// waitExchanges waits until a run has n finished exchanges
func waitExchanges(t *testing.T, s *Server, runID string, n int) []api.HTTPExchange {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := s.storage.HTTPExchanges(context.Background(), storage.HTTPExchangeFilter{RunID: runID}, storage.Page{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		done := 0
		for _, e := range res.Exchanges {
			if e.State != api.ExchangeInProgress {
				done++
			}
		}
		if done == n && len(res.Exchanges) == n {
			return res.Exchanges
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s: %d of %d exchanges finished", runID, done, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func shutdown(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// E2E A (hello), B (tool use and result) and C (count_tokens, cache write
// and read) through a real server and proxy with a fake upstream, keyless;
// every endpoint's response is checked against the generated types; then
// the server restarts and the capture reopens (AC-09), a run is deleted
// without touching another (AC-18)
func TestHTTPInspectorEndToEnd(t *testing.T) {
	model, err := apigen.Build(spec.REST(), ".")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeAnthropic{}
	up := httptest.NewServer(fake)
	defer up.Close()
	cfg := captureConfig(t, up.URL, true)
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := apiCheck{t: t, s: s, model: model}

	// AC-08: what the proxy captures never enters the telemetry counts
	// (/api/stats without a range is cached: ask for a range)
	statsURL := "/api/stats?from=" + time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) + "&to=" + time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	statsBefore := decode[api.StatsResponse](t, c.get(statsURL, "StatsResponse"))

	status := decode[api.HTTPInspectorStatus](t, c.get("/api/http-inspector/status", "HTTPInspectorStatus"))
	if !status.Enabled || status.CaptureMode != config.CaptureSafe {
		t.Fatalf("status %+v", status)
	}
	created := decode[api.HTTPCaptureRunCreated](t, c.do(http.MethodPost, "/api/http-inspector/runs",
		api.CreateHTTPCaptureRunRequest{Name: "Aula E2E", Cwd: "/tmp/demo", Command: "claude -p"}, http.StatusCreated, "HTTPCaptureRunCreated"))
	runID := created.Run.ID
	if created.Run.Mode != api.CaptureRunLauncher || created.Run.State != api.CaptureRunActive || !strings.HasPrefix(created.ProxyURL, "http://127.0.0.1:") {
		t.Fatalf("created %+v", created)
	}

	user := func(text string) string { return `{"role":"user","content":[{"type":"text","text":"` + text + `"}]}` }
	// A: hello
	sendThroughProxy(t, created.ProxyURL, "/v1/messages?beta=true", e2eBody(true, user("olá")))
	// C: count_tokens first
	sendThroughProxy(t, created.ProxyURL, "/v1/messages/count_tokens?beta=true", e2eBody(false, user("use a tool")))
	// B + C: a tool use (cache write), then its result (cache read)
	sendThroughProxy(t, created.ProxyURL, "/v1/messages?beta=true", e2eBody(true, user("use a tool")))
	sendThroughProxy(t, created.ProxyURL, "/v1/messages?beta=true", e2eBody(true, user("use a tool"),
		`{"role":"assistant","content":[{"type":"text","text":"Vou ler o arquivo."},{"type":"tool_use","id":"toolu_E2E","name":"Read","input":{"file_path":"/tmp/demo.txt"}}]}`,
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_E2E","content":"uma linha"}]}`))
	ex := waitExchanges(t, s, runID, 4)

	// The upstream received the credential; nothing stored has it (AC-12)
	for _, k := range fake.keys {
		if k != fakeKey() {
			t.Fatal("credential not forwarded")
		}
	}

	// A
	hello := ex[0]
	if hello.Kind != "model_inference" || hello.State != api.ExchangeComplete || hello.StopReason != "end_turn" || hello.RequestID != "req_e2e_1" ||
		hello.SessionID != e2eSession || hello.Usage == nil || hello.Usage.Quality != api.UsageFinal || *hello.Usage.Usage.OutputTokens != 5 {
		t.Fatalf("hello %+v", hello)
	}
	resp := decode[api.HTTPExchangeBody](t, c.get("/api/http-inspector/exchanges/"+hello.ID+"/response", "HTTPExchangeBody"))
	if resp.Message == nil || len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "Olá! Tudo bem?" || resp.Variant != api.BodyVariantSafe {
		t.Fatalf("hello response %+v", resp.Message)
	}

	// C: count_tokens is in the timeline but not inference usage (AC-05)
	count := ex[1]
	if count.Kind != "token_count" || count.Usage == nil || count.Usage.CountTokens == nil || *count.Usage.CountTokens != 3000 || count.Usage.CostUSD != nil {
		t.Fatalf("count %+v", count)
	}
	write := decode[api.HTTPUsageDetail](t, c.get("/api/http-inspector/exchanges/"+ex[2].ID+"/usage", "HTTPUsageDetail"))
	if *write.Usage.CacheCreation5m != 1500 || *write.Totals.TotalInput != 20+1500 || *write.Totals.TotalProcessed != 20+1500+40 {
		t.Fatalf("cache write usage %+v %+v", write.Usage, write.Totals)
	}
	var sawWrite bool
	for _, comp := range write.CostComponents {
		if comp.Component == "cache_write_5m" && comp.Tokens == 1500 && comp.CostUSD != nil {
			sawWrite = true
		}
	}
	if !sawWrite || write.CostStatus != api.CostComplete {
		t.Fatalf("cost components %+v", write.CostComponents)
	}
	read := decode[api.HTTPUsageDetail](t, c.get("/api/http-inspector/exchanges/"+ex[3].ID+"/usage", "HTTPUsageDetail"))
	if *read.Usage.CacheReadInputTokens != 2000 || *read.Totals.TotalInput != 2050 || read.Totals.CacheReadShare == nil {
		t.Fatalf("cache read usage %+v", read.Totals)
	}
	if len(read.Snapshots) < 2 {
		t.Fatalf("snapshots %+v", read.Snapshots)
	}

	// B: tool use linked to the request carrying its result (AC-06)
	tools := decode[api.HTTPToolLinksResponse](t, c.get("/api/http-inspector/runs/"+runID+"/tools", "HTTPToolLinksResponse"))
	if len(tools.Tools) != 1 || tools.Tools[0].ToolUseID != "toolu_E2E" || tools.Tools[0].Status != api.ToolLinkMatched ||
		tools.Tools[0].UseExchangeID != ex[2].ID || tools.Tools[0].ResultExchangeID != ex[3].ID || tools.Note == "" {
		t.Fatalf("tools %+v", tools)
	}
	detail := decode[api.HTTPExchangeDetail](t, c.get("/api/http-inspector/exchanges/"+ex[3].ID, "HTTPExchangeDetail"))
	var inferred bool
	for _, o := range detail.Observations {
		if o.Certainty == anthropicwire.CertaintyInferred {
			inferred = true
		}
	}
	if !inferred || detail.PrevID != ex[2].ID || detail.Run.ID != runID || len(detail.Lifecycle) < 3 {
		t.Fatalf("detail %+v", detail)
	}
	diff := decode[api.HTTPRequestDiffResponse](t, c.get("/api/http-inspector/exchanges/"+ex[3].ID+"/diff", "HTTPRequestDiffResponse"))
	if diff.PreviousID != ex[2].ID || diff.ComparedBy != "same_conversation" || diff.Diff.MessagesAdded != 2 || len(diff.Diff.NewToolResults) != 1 {
		t.Fatalf("diff %+v", diff)
	}
	c.get("/api/http-inspector/exchanges/"+ex[0].ID+"/diff", "HTTPRequestDiffResponse")

	// Every endpoint, checked against the generated types
	c.get("/api/http-inspector/runs", "HTTPCaptureRunsResponse")
	c.get("/api/http-inspector/runs?mode=launcher&state=active", "HTTPCaptureRunsResponse")
	run := decode[api.HTTPRunDetail](t, c.get("/api/http-inspector/runs/"+runID, "HTTPRunDetail"))
	sum := run.Summary
	if sum.Exchanges != 4 || sum.Inference != 3 || sum.TokenCounts != 1 || sum.CountedTokens != 3000 ||
		sum.Tokens.Output != 5+40+30 || sum.Tokens.Exchanges != 3 || sum.Tokens.CacheRead != 2000 || sum.Tokens.CacheWrite5m != 1500 {
		t.Fatalf("summary %+v", sum)
	}
	for q, want := range map[string]int{"kind=token_count": 1, "kind=model_inference": 3, "tool=Read": 1, "status=ok": 4, "status=error": 0, "stream=false": 1} {
		res := decode[api.HTTPExchangesResponse](t, c.get("/api/http-inspector/runs/"+runID+"/exchanges?"+q, "HTTPExchangesResponse"))
		if len(res.Exchanges) != want {
			t.Errorf("filter %s: %d exchanges, want %d", q, len(res.Exchanges), want)
		}
	}
	page := decode[api.HTTPExchangesResponse](t, c.get("/api/http-inspector/runs/"+runID+"/exchanges?limit=3", "HTTPExchangesResponse"))
	if !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page %+v", page)
	}
	next := decode[api.HTTPExchangesResponse](t, c.get("/api/http-inspector/runs/"+runID+"/exchanges?limit=3&cursor="+page.NextCursor, "HTTPExchangesResponse"))
	if len(next.Exchanges) != 1 || next.Exchanges[0].ID != ex[3].ID {
		t.Fatalf("next page %+v", next)
	}
	for _, e := range ex {
		c.get("/api/http-inspector/exchanges/"+e.ID, "HTTPExchangeDetail")
		c.get("/api/http-inspector/exchanges/"+e.ID+"/request", "HTTPExchangeBody")
		c.get("/api/http-inspector/exchanges/"+e.ID+"/response", "HTTPExchangeBody")
		raw := decode[api.HTTPExchangeBody](t, c.get("/api/http-inspector/exchanges/"+e.ID+"/request?variant=raw", "HTTPExchangeBody"))
		if raw.Variant != api.BodyVariantNone || raw.Body != "" || raw.Unavailable == "" {
			t.Fatalf("raw variant in safe mode: %+v", raw)
		}
		c.get("/api/http-inspector/exchanges/"+e.ID+"/events", "HTTPSSEEventsResponse")
		c.get("/api/http-inspector/exchanges/"+e.ID+"/usage", "HTTPUsageDetail")
	}
	events := decode[api.HTTPSSEEventsResponse](t, c.get("/api/http-inspector/exchanges/"+ex[2].ID+"/events?type=content_block_delta", "HTTPSSEEventsResponse"))
	if len(events.Events) == 0 || events.Types["message_start"] != 1 {
		t.Fatalf("events %+v", events)
	}
	c.do(http.MethodGet, "/api/http-inspector/exchanges/nope", nil, http.StatusNotFound, "ErrorResponse")
	c.do(http.MethodGet, "/api/http-inspector/runs/nope", nil, http.StatusNotFound, "ErrorResponse")
	c.do(http.MethodGet, "/api/http-inspector/runs/"+runID+"/exchanges?kind=bogus", nil, http.StatusBadRequest, "ErrorResponse")
	c.do(http.MethodDelete, "/api/http-inspector/runs/"+runID, nil, http.StatusConflict, "ErrorResponse")

	// The session's traffic, compared with its telemetry (AC-08): the same
	// request seen by OTLP is matched, never added up
	if err := s.storage.InsertTelemetry(context.Background(), []api.LogRecord{{
		Timestamp: time.Now().UTC(), ServiceName: "claude-code", SeverityText: "INFO", Body: "claude_code.api_request",
		LogAttributes: map[string]string{"session.id": e2eSession, "event.name": "api_request", "request_id": "req_e2e_1",
			"model": e2eModel, "input_tokens": "10", "output_tokens": "5", "cost_usd": "0.0001"},
	}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	session := decode[api.HTTPSessionTrafficResponse](t, c.get("/api/sessions/"+e2eSession+"/http-exchanges", "HTTPSessionTrafficResponse"))
	if len(session.Exchanges) != 4 || len(session.Runs) != 1 || session.Comparison == nil ||
		session.Comparison.MatchedRequests != 1 || session.Comparison.Certainty != "partial" {
		t.Fatalf("session traffic %+v", session.Comparison)
	}
	c.get("/api/sessions/unknown/http-exchanges", "HTTPSessionTrafficResponse")
	statsAfter := decode[api.StatsResponse](t, c.get(statsURL, "StatsResponse"))
	if statsAfter.SpanCount != statsBefore.SpanCount || statsAfter.MetricCount != statsBefore.MetricCount || statsAfter.LogCount != statsBefore.LogCount+1 {
		t.Fatalf("telemetry counts changed by the capture: %+v -> %+v", statsBefore, statsAfter)
	}

	// Rename, export (sanitized: no credential, no raw bodies)
	renamed := decode[api.HTTPCaptureRun](t, c.do(http.MethodPut, "/api/http-inspector/runs/"+runID,
		map[string]string{"name": "Aula renomeada", "note": "nota"}, http.StatusOK, "HTTPCaptureRun"))
	if renamed.Name != "Aula renomeada" || renamed.Note != "nota" {
		t.Fatalf("renamed %+v", renamed)
	}
	zipped := c.do(http.MethodGet, "/api/http-inspector/runs/"+runID+"/export", nil, http.StatusOK, "")
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		if bytes.Contains(data, []byte(fakeKey())) {
			t.Fatalf("export %s holds the credential", f.Name)
		}
	}
	if !names["manifest.json"] || !names["run.json"] || !names["tools.json"] || len(zr.File) < 3+4 {
		t.Fatalf("export files %v", names)
	}
	// Nothing on disk holds the credential
	filepath.WalkDir(cfg.HTTPCaptureDir(), func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if data, _ := os.ReadFile(path); bytes.Contains(data, []byte(fakeKey())) {
				t.Errorf("%s holds the credential", path)
			}
		}
		return nil
	})

	finished := decode[api.HTTPCaptureRun](t, c.do(http.MethodPost, "/api/http-inspector/runs/"+runID+"/finish",
		map[string]any{"exitCode": 0, "pid": 4242}, http.StatusOK, "HTTPCaptureRun"))
	if finished.State != api.CaptureRunFinished || finished.ExitCode == nil || *finished.ExitCode != 0 || finished.PID != 4242 || finished.Exchanges != 4 {
		t.Fatalf("finished %+v", finished)
	}

	// AC-09: after a restart the capture reopens whole
	shutdown(t, s)
	s2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, s2)
	c2 := apiCheck{t: t, s: s2, model: model}
	again := decode[api.HTTPExchangeBody](t, c2.get("/api/http-inspector/exchanges/"+hello.ID+"/response", "HTTPExchangeBody"))
	if again.Message == nil || again.Message.Content[0].Text != "Olá! Tudo bem?" || again.Body == "" || again.Body != resp.Body {
		t.Fatal("response not the same after restart")
	}
	req := decode[api.HTTPExchangeBody](t, c2.get("/api/http-inspector/exchanges/"+ex[3].ID+"/request", "HTTPExchangeBody"))
	if req.Request == nil || req.Request.Model != e2eModel || len(req.Request.ToolResults) != 1 || !strings.Contains(req.Body, "toolu_E2E") {
		t.Fatalf("request after restart %+v", req.Request)
	}
	ev2 := decode[api.HTTPSSEEventsResponse](t, c2.get("/api/http-inspector/exchanges/"+ex[2].ID+"/events", "HTTPSSEEventsResponse"))
	if int64(len(ev2.Events)) != ex[2].SSEEvents {
		t.Fatalf("%d events after restart, want %d", len(ev2.Events), ex[2].SSEEvents)
	}

	// AC-18: deleting a run removes only its data
	other := decode[api.HTTPCaptureRunCreated](t, c2.do(http.MethodPost, "/api/http-inspector/runs",
		api.CreateHTTPCaptureRunRequest{Name: "Outra"}, http.StatusCreated, "HTTPCaptureRunCreated"))
	sendThroughProxy(t, other.ProxyURL, "/v1/messages", e2eBody(true, user("olá")))
	waitExchanges(t, s2, other.Run.ID, 1)
	c2.do(http.MethodPost, "/api/http-inspector/runs/"+other.Run.ID+"/finish", nil, http.StatusOK, "HTTPCaptureRun")
	c2.do(http.MethodDelete, "/api/http-inspector/runs/"+runID, nil, http.StatusNoContent, "")
	c2.do(http.MethodGet, "/api/http-inspector/runs/"+runID, nil, http.StatusNotFound, "ErrorResponse")
	c2.do(http.MethodGet, "/api/http-inspector/exchanges/"+hello.ID, nil, http.StatusNotFound, "ErrorResponse")
	if _, err := os.Stat(filepath.Join(cfg.HTTPCaptureDir(), "runs", runID)); !os.IsNotExist(err) {
		t.Fatal("bodies of the deleted run remain")
	}
	left := decode[api.HTTPExchangesResponse](t, c2.get("/api/http-inspector/runs/"+other.Run.ID+"/exchanges", "HTTPExchangesResponse"))
	if len(left.Exchanges) != 1 {
		t.Fatal("the other run lost its exchanges")
	}
	if _, err := os.Stat(filepath.Join(cfg.HTTPCaptureDir(), "runs", other.Run.ID)); err != nil {
		t.Fatal("the other run lost its bodies")
	}
}

// AC-16: with the proxy off nothing listens and nothing changes; stored
// captures stay readable
func TestHTTPInspectorOff(t *testing.T) {
	model, err := apigen.Build(spec.REST(), ".")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(captureConfig(t, "https://api.anthropic.com", false))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, s)
	if s.httpProxy != nil {
		t.Fatal("proxy created while disabled")
	}
	c := apiCheck{t: t, s: s, model: model}
	st := decode[api.HTTPInspectorStatus](t, c.get("/api/http-inspector/status", "HTTPInspectorStatus"))
	if st.Enabled || st.ProxyURL != "" {
		t.Fatalf("status %+v", st)
	}
	status := decode[api.StatusResponse](t, c.get("/api/status", "StatusResponse"))
	if status.HTTPInspector.Enabled {
		t.Fatal("status reports the proxy enabled")
	}
	c.do(http.MethodPost, "/api/http-inspector/runs", api.CreateHTTPCaptureRunRequest{}, http.StatusConflict, "ErrorResponse")
	c.get("/api/http-inspector/runs", "HTTPCaptureRunsResponse")
}

// An invalid upstream fails startup instead of forwarding anywhere
func TestHTTPInspectorRejectsInvalidUpstream(t *testing.T) {
	for _, u := range []string{"http://api.anthropic.com", "https://x.example/v1", "https://user:pw@api.anthropic.com"} {
		if s, err := New(captureConfig(t, u, true)); err == nil {
			shutdown(t, s)
			t.Errorf("%s accepted", u)
		}
	}
}
