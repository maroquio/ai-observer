package httpproxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/httpcapture"
	"github.com/tobilg/ai-observer/internal/redact"
	"github.com/tobilg/ai-observer/internal/storage"
)

// fake builds credential-shaped test values at runtime, so the source holds
// nothing that secret scanners report as a leaked key
func fake(prefix string, n int) string {
	return prefix + strings.Repeat("Ab3x", n/4+1)[:n]
}

const testModel = "claude-sonnet-4-5-20250929"

func sse(events ...string) []string { return events }

func sseEvent(typ, data string) string {
	return "event: " + typ + "\ndata: " + data + "\n\n"
}

// streamEvents is a reply with a text block and a tool use
func streamEvents(msgID, toolID string) []string {
	return sse(
		sseEvent("message_start", `{"type":"message_start","message":{"id":"`+msgID+`","type":"message","role":"assistant","model":"`+testModel+`","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":100,"cache_read_input_tokens":2000,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":0},"output_tokens":1}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sseEvent("ping", `{"type": "ping"}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Vou ler o arquivo."}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"`+toolID+`","name":"Read","input":{}}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"/tmp/a.txt\"}"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":1}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":42}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	)
}

// upstream is a fake Anthropic API
type upstream struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	headers []http.Header
	bodies  [][]byte
	// barrier: the stream waits after its first event until released
	barrier chan struct{}
	streams int
	handler func(w http.ResponseWriter, r *http.Request, body []byte) bool
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{t: t}
	u.srv = httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.headers = append(u.headers, r.Header.Clone())
	u.bodies = append(u.bodies, body)
	h := u.handler
	barrier := u.barrier
	if r.URL.Path == "/v1/messages" {
		u.streams++
	}
	n := u.streams
	u.mu.Unlock()
	if h != nil && h(w, r, body) {
		return
	}
	switch r.URL.Path {
	case "/v1/messages/count_tokens":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Request-Id", "req_count_1")
		fmt.Fprint(w, `{"input_tokens":2112}`)
	case "/v1/messages":
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Request-Id", "req_stream_1")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		events := streamEvents(fmt.Sprintf("msg_%02d", n), fmt.Sprintf("toolu_%02d", n))
		for i, ev := range events {
			io.WriteString(w, ev)
			fl.Flush()
			if i == 0 && barrier != nil {
				select {
				case <-barrier:
				case <-time.After(10 * time.Second):
					u.t.Error("stream barrier was not released: the proxy buffered the first event")
					return
				}
			}
		}
	default:
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}
}

type harness struct {
	t        *testing.T
	dir      string
	store    *storage.DuckDBStore
	blobs    *httpcapture.BlobStore
	capturer *httpcapture.Capturer
	manager  *Manager
	up       *upstream
	runID    string
	proxyURL string
}

type harnessOpts struct {
	mode    string
	maxBody int64
	// wrap replaces the store the capture writes to (failure injection)
	wrap func(httpcapture.RunStore) httpcapture.RunStore
}

func newHarness(t *testing.T, mode string) *harness {
	return newHarnessWith(t, harnessOpts{mode: mode})
}

func newHarnessWith(t *testing.T, o harnessOpts) *harness {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.NewDuckDBStore(filepath.Join(dir, "test.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := httpcapture.OpenBlobStore(filepath.Join(dir, "captures"))
	if err != nil {
		t.Fatal(err)
	}
	up := newUpstream(t)
	upURL, err := ValidateUpstream(up.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var rs httpcapture.RunStore = store
	if o.wrap != nil {
		rs = o.wrap(store)
	}
	capturer := httpcapture.New(httpcapture.Config{Mode: o.mode, Upstream: upURL.String(), MaxBodyBytes: o.maxBody}, rs, blobs, redact.New(redact.Secrets), nil)
	m, err := NewManager(Config{Host: "127.0.0.1", Upstream: upURL}, capturer)
	if err != nil {
		t.Fatal(err)
	}
	run, proxyURL, err := m.StartLauncherRun(context.Background(), httpcapture.RunOptions{Name: "teste"})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, dir: dir, store: store, blobs: blobs, capturer: capturer, manager: m, up: up, runID: run.ID, proxyURL: proxyURL}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.Shutdown(ctx)
		blobs.Close()
		store.Close()
	})
	return h
}

// flush finishes the run and waits until everything captured is stored
func (h *harness) flush() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.manager.Shutdown(ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) exchanges() []api.HTTPExchange {
	h.t.Helper()
	res, err := h.store.HTTPExchanges(context.Background(), storage.HTTPExchangeFilter{RunID: h.runID}, storage.Page{Limit: 100})
	if err != nil {
		h.t.Fatal(err)
	}
	return res.Exchanges
}

func requestBody(sessionID string, stream bool, extraMessages ...string) string {
	msgs := `{"role":"user","content":[{"type":"text","text":"leia /tmp/a.txt"}]}`
	for _, m := range extraMessages {
		msgs += "," + m
	}
	return `{"model":"` + testModel + `","max_tokens":32000,"stream":` + fmt.Sprint(stream) +
		`,"system":[{"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"}}]` +
		`,"tools":[{"name":"Read","description":"Read a file","input_schema":{"type":"object"}}]` +
		`,"metadata":{"user_id":"{\"device_id\":\"dev1\",\"account_uuid\":\"\",\"session_id\":\"` + sessionID + `\"}"}` +
		`,"messages":[` + msgs + `]}`
}

func post(t *testing.T, base, path, body string, header http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestValidateUpstream(t *testing.T) {
	ok := []string{"https://api.anthropic.com", "https://api.anthropic.com/", "http://127.0.0.1:9999", "http://localhost:1"}
	for _, s := range ok {
		if _, err := ValidateUpstream(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	bad := []string{"http://api.anthropic.com", "https://user:pw@api.anthropic.com", "https://api.anthropic.com/v1",
		"https://api.anthropic.com?x=1", "ftp://x", "https://", "file:///etc/passwd"}
	for _, s := range bad {
		if _, err := ValidateUpstream(s); err == nil {
			t.Errorf("%s: accepted", s)
		}
	}
}

func TestStreamingIsNotBuffered(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	h.up.barrier = make(chan struct{})
	key := fake("sk-ant-api03-", 48)
	resp := post(t, h.proxyURL, "/v1/messages?beta=true", requestBody("11111111-2222-3333-4444-555555555555", true), http.Header{
		"X-Api-Key":                {key},
		"Anthropic-Version":        {"2023-06-01"},
		"Anthropic-Beta":           {"claude-code-20250219,interleaved-thinking-2025-05-14"},
		"X-Claude-Code-Session-Id": {"11111111-2222-3333-4444-555555555555"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// The first event must reach the client while the upstream still holds
	// the rest of the stream
	r := bufio.NewReader(resp.Body)
	first, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "event: message_start") {
		t.Fatalf("first line %q", first)
	}
	close(h.up.barrier)
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rest), "message_stop") {
		t.Fatal("stream incomplete")
	}
	// Credentials and protocol headers reach the upstream unchanged
	got := h.up.headers[0]
	if got.Get("X-Api-Key") != key || got.Get("Anthropic-Beta") != "claude-code-20250219,interleaved-thinking-2025-05-14" {
		t.Fatalf("headers not forwarded: %v", got)
	}
	if got.Get("X-Forwarded-For") != "" {
		t.Fatal("X-Forwarded-For added")
	}
	h.flush()

	ex := h.exchanges()
	if len(ex) != 1 {
		t.Fatalf("%d exchanges", len(ex))
	}
	e := ex[0]
	if e.State != api.ExchangeComplete || e.Kind != "model_inference" || !e.Stream || e.Status != 200 {
		t.Fatalf("exchange %+v", e)
	}
	if e.SessionID != "11111111-2222-3333-4444-555555555555" || e.SessionEvidence != "header:X-Claude-Code-Session-Id" {
		t.Fatalf("session %q %q", e.SessionID, e.SessionEvidence)
	}
	if e.RequestID != "req_stream_1" || e.StopReason != "tool_use" || e.MessageID != "msg_01" || e.ResponseModel != testModel {
		t.Fatalf("response fields %+v", e)
	}
	if e.SSEEvents != 11 || len(e.ToolUses) != 1 || e.ToolUses[0] != "Read" {
		t.Fatalf("events %d tools %v", e.SSEEvents, e.ToolUses)
	}
	if e.Usage == nil || e.Usage.Quality != api.UsageFinal || *e.Usage.Usage.OutputTokens != 42 || *e.Usage.Usage.CacheReadInputTokens != 2000 {
		t.Fatalf("usage %+v", e.Usage)
	}
	if e.Usage.CostStatus != api.CostComplete || e.Usage.CostUSD == nil || *e.Usage.CostUSD <= 0 {
		t.Fatalf("cost %+v", e.Usage)
	}

	// The stored record never holds the key
	stored, err := h.store.HTTPExchange(context.Background(), e.ID)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := json.Marshal(stored)
	if bytes.Contains(all, []byte(key)) {
		t.Fatal("credential stored")
	}
	for _, hd := range stored.Record.RequestHeaders {
		if strings.EqualFold(hd.Name, "X-Api-Key") && (!hd.Redacted || hd.Value != "[REDACTED]") {
			t.Fatalf("x-api-key header %+v", hd)
		}
	}
	events, err := h.store.HTTPSSEEvents(context.Background(), e.ID, "", storage.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Events) != 11 || events.Events[0].Type != "message_start" || events.Types["content_block_delta"] != 3 {
		t.Fatalf("events %+v", events)
	}
	// The barrier separates the first event from the rest in time
	if events.Events[0].OffsetMs > events.Events[1].OffsetMs {
		t.Fatal("event offsets out of order")
	}
}

func TestCountTokensAndToolLinks(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	hdr := http.Header{"X-Claude-Code-Session-Id": {sid}}
	resp := post(t, h.proxyURL, "/v1/messages/count_tokens?beta=true", requestBody(sid, false), hdr)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	resp = post(t, h.proxyURL, "/v1/messages?beta=true", requestBody(sid, true), hdr)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	// The next request carries the tool result
	follow := requestBody(sid, true,
		`{"role":"assistant","content":[{"type":"text","text":"Vou ler o arquivo."},{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"/tmp/a.txt"}}]}`,
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"conteúdo do arquivo"}]}`)
	resp = post(t, h.proxyURL, "/v1/messages?beta=true", follow, hdr)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	h.flush()

	ex := h.exchanges()
	if len(ex) != 3 {
		t.Fatalf("%d exchanges", len(ex))
	}
	if ex[0].Kind != "token_count" || ex[0].Usage == nil || ex[0].Usage.CountTokens == nil || *ex[0].Usage.CountTokens != 2112 {
		t.Fatalf("count_tokens %+v", ex[0])
	}
	if ex[0].Usage.CostUSD != nil {
		t.Fatal("count_tokens priced")
	}
	if ex[2].ToolResults != 1 {
		t.Fatalf("tool results %d", ex[2].ToolResults)
	}
	links, err := h.store.HTTPToolLinks(context.Background(), h.runID, "")
	if err != nil {
		t.Fatal(err)
	}
	// toolu_01 (reply of exchange 2) got its result in exchange 3; toolu_02
	// (reply of exchange 3) has none: unresolved, since the run ended
	if len(links) != 2 || links[0].ToolUseID != "toolu_01" || links[0].Status != api.ToolLinkMatched || links[0].UseSeq != 2 || links[0].ResultSeq != 3 ||
		links[1].ToolUseID != "toolu_02" || links[1].Status != api.ToolLinkUnresolved {
		t.Fatalf("links %+v", links)
	}
	sum, err := h.store.HTTPTrafficSummary(context.Background(), h.runID, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Exchanges != 3 || sum.Inference != 2 || sum.TokenCounts != 1 || sum.CountedTokens != 2112 || sum.Tokens.Output != 84 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestUpstreamErrorsPassThrough(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	statuses := []int{401, 403, 429, 500, 529}
	h.up.handler = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		var code int
		fmt.Sscan(r.URL.Query().Get("code"), &code)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"type":"error","error":{"type":"overloaded_error","message":"falha %d"}}`, code)
		return true
	}
	for _, code := range statuses {
		resp := post(t, h.proxyURL, fmt.Sprintf("/v1/messages?code=%d", code), requestBody("s1", true), nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != code || !strings.Contains(string(body), fmt.Sprintf("falha %d", code)) || resp.Header.Get("Retry-After") != "3" {
			t.Fatalf("%d: got %d %s", code, resp.StatusCode, body)
		}
	}
	h.flush()
	ex := h.exchanges()
	if len(ex) != len(statuses) {
		t.Fatalf("%d exchanges", len(ex))
	}
	for i, e := range ex {
		if e.Status != statuses[i] || e.ErrorType != "overloaded_error" || e.State != api.ExchangeComplete {
			t.Fatalf("exchange %d: %+v", i, e)
		}
	}
}

func TestStreamErrorEventAndGzip(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	h.up.handler = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		switch r.URL.Query().Get("case") {
		case "sse_error":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			io.WriteString(w, streamEvents("msg_02", "toolu_02")[0])
			io.WriteString(w, sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
		case "gzip":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			io.WriteString(gz, `{"id":"msg_03","type":"message","role":"assistant","model":"`+testModel+`","content":[{"type":"text","text":"oi"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`)
			gz.Close()
		default:
			return false
		}
		return true
	}
	resp := post(t, h.proxyURL, "/v1/messages?case=sse_error", requestBody("s2", true), nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPost, h.proxyURL+"/v1/messages?case=gzip", strings.NewReader(requestBody("s2", false)))
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// the client receives the compressed bytes as the upstream sent them
	if resp.Header.Get("Content-Encoding") != "gzip" || len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Fatalf("body changed: encoding %q", resp.Header.Get("Content-Encoding"))
	}
	h.flush()
	ex := h.exchanges()
	if len(ex) != 2 {
		t.Fatalf("%d exchanges", len(ex))
	}
	if ex[0].ErrorType != "overloaded_error" || ex[0].State == api.ExchangeComplete && ex[0].Usage != nil && ex[0].Usage.Quality == api.UsageFinal {
		t.Fatalf("sse error %+v", ex[0])
	}
	if ex[1].MessageID != "msg_03" || ex[1].Usage == nil || *ex[1].Usage.Usage.OutputTokens != 2 {
		t.Fatalf("gzip %+v", ex[1])
	}
}

// gzipStream answers with streamEvents compressed with gzip, flushed event
// by event with a pause after each (the real API compresses Claude Code's
// streams: it sends Accept-Encoding gzip). With split the fourth event is
// sent in two chunks; with cut the connection closes after five events.
func gzipStream(gap time.Duration, split, cut bool) func(http.ResponseWriter, *http.Request, []byte) bool {
	return func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Request-Id", "req_gzip_stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		gz := gzip.NewWriter(w)
		send := func(s string) {
			io.WriteString(gz, s)
			gz.Flush()
			fl.Flush()
			time.Sleep(gap)
		}
		for i, ev := range streamEvents("msg_gz", "toolu_gz") {
			if cut && i == 5 {
				if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
					conn.Close()
				}
				return true
			}
			if split && i == 3 {
				send(ev[:len(ev)/2])
				ev = ev[len(ev)/2:]
			}
			send(ev)
		}
		gz.Close()
		return true
	}
}

func gzipRequest(t *testing.T, proxyURL string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, proxyURL+"/v1/messages?beta=true", strings.NewReader(requestBody("s-gz", true)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// A compressed event stream reaches the client as the upstream sent it, and
// every event still gets the arrival time of the chunk that carried it
func TestCompressedStreamEventTimes(t *testing.T) {
	const gap = 30 * time.Millisecond
	h := newHarness(t, httpcapture.ModeSafe)
	h.up.handler = gzipStream(gap, true, false)
	resp := gzipRequest(t, h.proxyURL)
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("encoding %q", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil || string(plain) != strings.Join(streamEvents("msg_gz", "toolu_gz"), "") {
		t.Fatalf("client stream changed: %v", err)
	}
	h.flush()

	ex := h.exchanges()
	if len(ex) != 1 {
		t.Fatalf("%d exchanges", len(ex))
	}
	e := ex[0]
	if e.State != api.ExchangeComplete || !e.Stream || e.Degraded || e.StopReason != "tool_use" || e.SSEEvents != 11 {
		t.Fatalf("exchange %+v", e)
	}
	if e.Usage == nil || e.Usage.Quality != api.UsageFinal || *e.Usage.Usage.OutputTokens != 42 {
		t.Fatalf("usage %+v", e.Usage)
	}
	events, err := h.store.HTTPSSEEvents(context.Background(), e.ID, "", storage.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Events) != 11 {
		t.Fatalf("%d events", len(events.Events))
	}
	// a pause after every chunk: the offsets follow the chunks (in order,
	// mostly distinct, spread over the pauses), never all equal as when the
	// stream was parsed after its end. Loose bounds: a stalled scheduler
	// may join two chunks.
	distinct := map[float64]bool{}
	for i, ev := range events.Events {
		distinct[ev.OffsetMs] = true
		if i > 0 && ev.OffsetMs < events.Events[i-1].OffsetMs {
			t.Fatalf("event %d before event %d: %+v", i, i-1, events.Events)
		}
	}
	span := events.Events[10].OffsetMs - events.Events[0].OffsetMs
	if len(distinct) < 6 || span < float64(5*gap.Milliseconds()) {
		t.Fatalf("%d distinct offsets over %.1f ms: %+v", len(distinct), span, events.Events)
	}
	stored, err := h.store.HTTPExchange(context.Background(), e.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range stored.Record.Lifecycle {
		if l.Event == httpcapture.EventStreamParseError {
			t.Fatalf("lifecycle %+v", l)
		}
	}
}

// A compressed stream cut by the upstream keeps the events decoded before
// the cut, with their times, and says the stream was not decoded whole
func TestCompressedStreamCut(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	h.up.handler = gzipStream(10*time.Millisecond, false, true)
	resp := gzipRequest(t, h.proxyURL)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	h.flush()
	ex := h.exchanges()
	if len(ex) != 1 || ex[0].State != api.ExchangePartial || !ex[0].Degraded || ex[0].SSEEvents != 5 {
		t.Fatalf("%+v", ex)
	}
	stored, err := h.store.HTTPExchange(context.Background(), ex[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(stored.Record.DegradedReasons, " | "), "stream comprimido (gzip) não decodificado por inteiro") {
		t.Fatalf("reasons %v", stored.Record.DegradedReasons)
	}
	events, err := h.store.HTTPSSEEvents(context.Background(), ex[0].ID, "", storage.Page{Limit: 100})
	if err != nil || len(events.Events) != 5 || events.Events[4].OffsetMs <= events.Events[0].OffsetMs {
		t.Fatalf("events %+v %v", events, err)
	}
}

func TestUpstreamUnreachable(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewDuckDBStore(filepath.Join(dir, "t.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// a port with nothing listening
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	up, _ := url.Parse("http://" + addr)
	c := httpcapture.New(httpcapture.Config{Mode: httpcapture.ModeMetadataOnly}, store, nil, redact.New(redact.Secrets), nil)
	m, err := NewManager(Config{Host: "127.0.0.1", Upstream: up}, c)
	if err != nil {
		t.Fatal(err)
	}
	run, proxyURL, err := m.StartLauncherRun(context.Background(), httpcapture.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resp := post(t, proxyURL, "/v1/messages", requestBody("s3", true), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), `"type":"error"`) {
		t.Fatalf("got %d %s", resp.StatusCode, body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := store.HTTPExchanges(context.Background(), storage.HTTPExchangeFilter{RunID: run.ID}, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Exchanges) != 1 || res.Exchanges[0].State != api.ExchangeFailed || res.Exchanges[0].Kind != "transport_error" || res.Exchanges[0].Status != 502 {
		t.Fatalf("%+v", res.Exchanges)
	}
}

func TestRejectsBrowsersAndForeignHosts(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	resp := post(t, h.proxyURL, "/v1/messages", "{}", http.Header{"Origin": {"https://evil.example"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("origin: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, h.proxyURL+"/v1/messages", strings.NewReader("{}"))
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("host: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, h.proxyURL+"/v1/stream", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("upgrade: %d", resp.StatusCode)
	}
	if len(h.up.bodies) != 0 {
		t.Fatal("rejected requests reached the upstream")
	}
}

// AC-01, AC-17: compressed and uncompressed request bodies (with tool
// search payloads) reach the upstream byte for byte
func TestRequestBytesForwardedUnchanged(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	body := `{"model":"` + testModel + `","max_tokens":10,"stream":false,` +
		`"tools":[{"name":"Read","defer_loading":true,"input_schema":{"type":"object"}},{"type":"tool_search_tool_regex_20251119","name":"tool_search"}],` +
		`"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"srv_1","content":[{"type":"tool_reference","tool_name":"Read"}]}]}],` +
		`"extra_number":1.0e2,"unicode":"é é"}`
	resp := post(t, h.proxyURL, "/v1/messages/count_tokens", body, http.Header{"Anthropic-Beta": {"advanced-tool-use-2025-11-20"}})
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(body))
	zw.Close()
	req, _ := http.NewRequest(http.MethodPost, h.proxyURL+"/v1/messages/count_tokens", bytes.NewReader(gz.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if len(h.up.bodies) != 2 || string(h.up.bodies[0]) != body || !bytes.Equal(h.up.bodies[1], gz.Bytes()) {
		t.Fatal("request bodies changed on the way")
	}
	if h.up.headers[0].Get("Anthropic-Beta") != "advanced-tool-use-2025-11-20" || h.up.headers[1].Get("Content-Encoding") != "gzip" {
		t.Fatal("headers changed on the way")
	}
	h.flush()
	ex := h.exchanges()
	if len(ex) != 2 || ex[1].RequestedModel != testModel {
		t.Fatalf("compressed request not interpreted: %+v", ex)
	}
	stored, err := h.store.HTTPExchange(context.Background(), ex[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stored.Record.RequestSummary, []byte(`"deferLoading":true`)) {
		t.Fatalf("defer_loading not recorded: %s", stored.Record.RequestSummary)
	}
}

// Bodies above the storage limit are forwarded whole and stored cut
func TestLargeBodyForwardedWholeStoredCut(t *testing.T) {
	h := newHarnessWith(t, harnessOpts{mode: httpcapture.ModeSafe, maxBody: 1024})
	big := requestBody("s-big", false, `{"role":"user","content":"`+strings.Repeat("x", 64<<10)+`"}`)
	resp := post(t, h.proxyURL, "/v1/messages/count_tokens", big, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if string(h.up.bodies[0]) != big {
		t.Fatal("large body not forwarded whole")
	}
	h.flush()
	ex := h.exchanges()
	if len(ex) != 1 || ex[0].RequestBytes != int64(len(big)) {
		t.Fatalf("%+v", ex)
	}
	pl, err := h.store.HTTPPayloads(context.Background(), ex[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range pl {
		if p.Part == httpcapture.PartRequest {
			found = true
			if !p.Truncated || p.StoredBytes > 1024 || p.ObservedBytes != int64(len(big)) {
				t.Fatalf("payload %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("request body not stored")
	}
}

// Parallel clients of one run get distinct, complete exchanges
func TestParallelClients(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := post(t, h.proxyURL, "/v1/messages", requestBody(fmt.Sprintf("s-%d", i), true), nil)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}(i)
	}
	wg.Wait()
	h.flush()
	ex := h.exchanges()
	if len(ex) != n {
		t.Fatalf("%d exchanges", len(ex))
	}
	seqs := map[int64]bool{}
	for _, e := range ex {
		if e.State != api.ExchangeComplete || seqs[e.Seq] {
			t.Fatalf("exchange %+v", e)
		}
		seqs[e.Seq] = true
	}
}

// A client that goes away mid-stream: the exchange is aborted, with the
// usage observed until then as partial
func TestClientCancelMidStream(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	h.up.barrier = make(chan struct{})
	defer close(h.up.barrier)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.proxyURL+"/v1/messages", strings.NewReader(requestBody("s-c", true)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(resp.Body)
	if _, err := r.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	waitFor(t, func() bool { return h.capturer.StatsSnapshot().InFlight == 0 })
	h.flush()
	ex := h.exchanges()
	if len(ex) != 1 || ex[0].State != api.ExchangeAborted {
		t.Fatalf("%+v", ex)
	}
	if ex[0].Usage == nil || ex[0].Usage.Quality != api.UsagePartial || ex[0].Usage.CostStatus != api.CostPartial {
		t.Fatalf("usage %+v", ex[0].Usage)
	}
}

// The upstream closes the connection mid-stream: the client sees the
// failure and the exchange is partial
func TestUpstreamResetMidStream(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	h.up.handler = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		io.WriteString(w, streamEvents("msg_r", "toolu_r")[0])
		w.(http.Flusher).Flush()
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			conn.Close()
		}
		return true
	}
	resp := post(t, h.proxyURL, "/v1/messages", requestBody("s-r", true), nil)
	_, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err == nil {
		t.Log("client read the cut stream without an error (chunked end not sent)")
	}
	h.flush()
	ex := h.exchanges()
	if len(ex) != 1 || ex[0].State != api.ExchangePartial {
		t.Fatalf("%+v", ex)
	}
}

// failingStore fails every capture write
type failingStore struct{ httpcapture.RunStore }

func (failingStore) StoreHTTPCaptureBatch(context.Context, storage.HTTPCaptureBatch) error {
	return fmt.Errorf("disk full")
}

// AC-11: storage failures never affect the traffic; the status reports them
func TestStoreFailureKeepsTraffic(t *testing.T) {
	h := newHarnessWith(t, harnessOpts{mode: httpcapture.ModeSafe, wrap: func(s httpcapture.RunStore) httpcapture.RunStore {
		return failingStore{s}
	}})
	resp := post(t, h.proxyURL, "/v1/messages", requestBody("s-f", true), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "message_stop") {
		t.Fatalf("traffic affected: %d", resp.StatusCode)
	}
	h.flush()
	st := h.capturer.StatsSnapshot()
	if st.StoreErrors == 0 || !st.Degraded || len(st.Warnings) == 0 {
		t.Fatalf("status %+v", st)
	}
}

// AC-11: a capture directory that cannot be written: traffic continues, the
// exchange is marked degraded
func TestBlobFailureMarksDegraded(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	runs := filepath.Join(h.dir, "captures", "runs")
	if err := os.Chmod(runs, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(runs, 0o700)
	resp := post(t, h.proxyURL, "/v1/messages", requestBody("s-d", true), nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	h.flush()
	ex := h.exchanges()
	if len(ex) != 1 || !ex[0].Degraded || ex[0].State != api.ExchangeComplete {
		t.Fatalf("%+v", ex)
	}
	if h.capturer.StatsSnapshot().Degraded != true {
		t.Fatal("status not degraded")
	}
}

// AC-20: other endpoints are forwarded and recorded as other_api
func TestOtherEndpointsRecorded(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	resp, err := http.Get(h.proxyURL + "/v1/models?limit=5")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	h.flush()
	ex := h.exchanges()
	if len(ex) != 1 || ex[0].Kind != "other_api" || ex[0].Method != "GET" || ex[0].Query != "limit=5" {
		t.Fatalf("%+v", ex)
	}
}

// Claude Code's quota probe (max_tokens 1, the user text "quota") is an
// auxiliary call: neither an error nor an inference, its usage outside the
// totals, the comparison and the Context Diff. A near miss stays an
// inference.
func TestQuotaProbeIsAuxiliary(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	sid := "aaaaaaaa-bbbb-cccc-dddd-000000000001"
	probe := `{"model":"` + testModel + `","max_tokens":1,"messages":[{"role":"user","content":"quota"}],` +
		`"metadata":{"user_id":"{\"session_id\":\"` + sid + `\"}"}}`
	nearMiss := strings.Replace(probe, `"max_tokens":1`, `"max_tokens":2`, 1)
	h.up.handler = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		if r.URL.Path != "/v1/messages" || strings.Contains(string(body), `"stream":true`) {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Query().Get("limited") == "1":
			w.Header().Set("Request-Id", "req_probe_429")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"Error"}}`)
		default:
			w.Header().Set("Request-Id", "req_json_"+r.URL.Query().Get("n"))
			fmt.Fprint(w, `{"id":"msg_p","type":"message","role":"assistant","model":"`+testModel+`",`+
				`"content":[{"type":"text","text":"ok"}],"stop_reason":"max_tokens","usage":{"input_tokens":8,"output_tokens":5}}`)
		}
		return true
	}
	hdr := http.Header{"X-Claude-Code-Session-Id": {sid}}
	for _, req := range []struct{ path, body string }{
		{"/v1/messages?beta=true&limited=1", probe},
		{"/v1/messages?beta=true&n=2", probe},
		{"/v1/messages?beta=true&n=3", nearMiss},
		{"/v1/messages?beta=true", requestBody(sid, true)},
	} {
		resp := post(t, h.proxyURL, req.path, req.body, hdr)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	h.flush()

	ex := h.exchanges()
	if len(ex) != 4 {
		t.Fatalf("%d exchanges", len(ex))
	}
	if ex[0].Kind != "quota_probe" || ex[0].Status != 429 || ex[0].ErrorType != "rate_limit_error" ||
		ex[0].Headline != "Chamada auxiliar: sonda de cota do Claude Code (HTTP 429, rate_limit_error)" {
		t.Fatalf("limited probe %+v", ex[0])
	}
	if ex[1].Kind != "quota_probe" || ex[1].Usage == nil || ex[1].Usage.Usage.OutputTokens == nil || *ex[1].Usage.Usage.OutputTokens != 5 {
		t.Fatalf("probe %+v", ex[1])
	}
	stored, err := h.store.HTTPExchange(context.Background(), ex[1].ID)
	if err != nil || stored.Usage == nil || stored.Usage.Kind != "quota_probe" {
		t.Fatalf("probe usage %+v %v", stored, err)
	}
	if ex[2].Kind != "model_inference" || ex[3].Kind != "model_inference" {
		t.Fatalf("near miss %s, stream %s", ex[2].Kind, ex[3].Kind)
	}

	sum, err := h.store.HTTPTrafficSummary(context.Background(), h.runID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Output: 5 of the near miss and 42 of the stream, none of the probes
	if sum.Exchanges != 4 || sum.Inference != 2 || sum.QuotaProbes != 2 || sum.Other != 0 || sum.Errors != 0 ||
		sum.Tokens.Exchanges != 2 || sum.Tokens.Output != 47 {
		t.Fatalf("summary %+v", sum)
	}
	errs, err := h.store.HTTPExchanges(context.Background(), storage.HTTPExchangeFilter{RunID: h.runID, Status: "error"}, storage.Page{Limit: 10})
	if err != nil || len(errs.Exchanges) != 0 {
		t.Fatalf("error filter %+v %v", errs, err)
	}
	probes, err := h.store.HTTPExchanges(context.Background(), storage.HTTPExchangeFilter{RunID: h.runID, Kind: "quota_probe"}, storage.Page{Limit: 10})
	if err != nil || len(probes.Exchanges) != 2 {
		t.Fatalf("kind filter %+v %v", probes, err)
	}
	ids, err := h.store.HTTPSessionRequestIDs(context.Background(), sid)
	if err != nil || len(ids) != 2 || ids[0] != "req_json_3" {
		t.Fatalf("request IDs %v %v", ids, err)
	}
	// The stream's Context Diff compares with the near miss, never a probe
	cands, err := h.store.HTTPDiffCandidates(context.Background(), h.runID, "model_inference", sid, ex[3].Seq, 10)
	if err != nil || len(cands) != 1 || cands[0].ExchangeID != ex[2].ID {
		t.Fatalf("diff candidates %+v %v", cands, err)
	}
}

// The inspector's session list: sessions by last activity (cursor pages),
// one row for the exchanges without a session, turns and titles from the
// typed prompts, redacted; metadata_only stores no prompt
func TestInspectorSessions(t *testing.T) {
	secret := fake("sk-ant-api03-", 48)
	for _, mode := range []string{httpcapture.ModeSafe, httpcapture.ModeMetadataOnly} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, mode)
			ctx := context.Background()
			sids := []string{"s-1", "s-2", "s-3"}
			for _, sid := range sids {
				body := strings.Replace(requestBody(sid, true), "leia /tmp/a.txt", "leia "+secret, 1)
				resp := post(t, h.proxyURL, "/v1/messages?beta=true", body, http.Header{"X-Claude-Code-Session-Id": {sid}})
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			resp, err := http.Get(h.proxyURL + "/v1/models")
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			h.flush()

			first, err := h.store.HTTPInspectorSessions(ctx, storage.Page{Limit: 3})
			if err != nil {
				t.Fatal(err)
			}
			if first.Total != 4 || len(first.Sessions) != 3 || !first.HasMore || first.Sessions[0].SessionID != "" ||
				first.Sessions[0].Exchanges != 1 || first.Sessions[0].Turns != 0 || first.Sessions[1].SessionID != "s-3" {
				t.Fatalf("first page %+v", first)
			}
			s3 := first.Sessions[1]
			if s3.Turns != 1 || s3.Inference != 1 || s3.OutputTokens != 42 || s3.Runs != 1 || len(s3.Models) != 1 {
				t.Fatalf("session %+v", s3)
			}
			if mode == httpcapture.ModeSafe && (strings.Contains(s3.Title, secret) || !strings.HasPrefix(s3.Title, "leia [REDACTED")) {
				t.Fatalf("title %q", s3.Title)
			}
			if mode == httpcapture.ModeMetadataOnly && s3.Title != "" {
				t.Fatalf("metadata_only title %q", s3.Title)
			}
			next, err := h.store.HTTPInspectorSessions(ctx, storage.Page{Limit: 3, Cursor: first.NextCursor})
			if err != nil || len(next.Sessions) != 1 || next.Sessions[0].SessionID != "s-1" || next.HasMore {
				t.Fatalf("next page %+v %v", next, err)
			}

			none, err := h.store.HTTPSessionTrafficSummary(ctx, "")
			if err != nil || none.Exchanges != 1 || none.Other != 1 {
				t.Fatalf("sessionless summary %+v %v", none, err)
			}
			runs, err := h.store.HTTPSessionRuns(ctx, "")
			if err != nil || len(runs) != 1 {
				t.Fatalf("sessionless runs %+v %v", runs, err)
			}
			rows, truncated, err := h.store.HTTPSessionTurnExchanges(ctx, "s-2", 10)
			if err != nil || truncated || len(rows) != 1 || !rows[0].Request.Known || rows[0].Request.Tools != 1 ||
				rows[0].Request.MessageCount != 1 || rows[0].Request.LastUserIndex != 0 || rows[0].Request.FirstMessageHash == "" ||
				rows[0].Request.LastMessageHash != rows[0].Request.FirstMessageHash || rows[0].Request.SystemSHA == "" {
				t.Fatalf("turn rows %+v %v %v", rows, truncated, err)
			}
		})
	}
}

// A secret that crosses the stored prompt's size limit is redacted whole:
// redaction runs before the prompt is cut, since its rules need the whole
// shape (a PEM key's END line, a JWT's three segments)
func TestPromptRedactedBeforeTruncation(t *testing.T) {
	filler := strings.Repeat("a", anthropicwire.MaxPromptBytes-15) + " "
	h := newHarness(t, httpcapture.ModeSafe)
	ctx := context.Background()
	for sid, secret := range crossingSecrets() {
		text, _ := json.Marshal("leia " + filler + secret)
		body := strings.Replace(requestBody(sid, true), "leia /tmp/a.txt", string(text[1:len(text)-1]), 1)
		resp := post(t, h.proxyURL, "/v1/messages?beta=true", body, http.Header{"X-Claude-Code-Session-Id": {sid}})
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	h.flush()

	sessions, err := h.store.HTTPInspectorSessions(ctx, storage.Page{Limit: 10})
	if err != nil || len(sessions.Sessions) != 2 {
		t.Fatalf("sessions %+v %v", sessions, err)
	}
	for _, s := range sessions.Sessions {
		if len(s.Title) > anthropicwire.MaxPromptBytes || !strings.Contains(s.Title, "[REDACTED") ||
			strings.Contains(s.Title, "eyJ") || strings.Contains(s.Title, "MIIEvQ") {
			t.Fatalf("%s: title %q", s.SessionID, s.Title)
		}
	}
	for _, ex := range h.exchanges() {
		stored, err := h.store.HTTPExchange(ctx, ex.ID)
		if err != nil {
			t.Fatal(err)
		}
		all, _ := json.Marshal(stored)
		if bytes.Contains(all, []byte("eyJ")) || bytes.Contains(all, []byte("MIIEvQ")) {
			t.Fatalf("%s: secret stored: %s", ex.ID, all)
		}
	}
}

// crossingSecrets are credentials the redactor recognizes only whole (a PEM
// key needs its END line, a JWT its three segments), built at runtime; the
// tests place them across a size limit. Their parts "MIIEvQ" and "eyJ" must
// not be stored.
func crossingSecrets() map[string]string {
	keyBody := strings.Repeat("MIIEvQIBADANBgkqhkiG9w0BAQEFAASC", 40)
	return map[string]string{
		"s-pem": "-----" + "BEGIN PRIVATE KEY-----\n" + keyBody + "\n-----" + "END PRIVATE KEY-----",
		"s-jwt": "eyJ" + strings.Repeat("hbGciOiJIUzI1", 3) + ".eyJ" + strings.Repeat("zdWIiOiIxMjM0", 3) + "." + strings.Repeat("SflKxwRJSMeK", 3),
	}
}

// The same for a tool result's preview, stored in the request summary and
// in the tool link
func TestToolResultPreviewRedactedBeforeTruncation(t *testing.T) {
	filler := strings.Repeat("a", anthropicwire.MaxPreviewBytes-10) + " "
	h := newHarness(t, httpcapture.ModeSafe)
	ctx := context.Background()
	n := 0
	for sid, secret := range crossingSecrets() {
		n++
		id := fmt.Sprintf("toolu_%02d", n)
		content, _ := json.Marshal(filler + secret)
		hdr := http.Header{"X-Claude-Code-Session-Id": {sid}}
		resp := post(t, h.proxyURL, "/v1/messages?beta=true", requestBody(sid, true), hdr)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		follow := requestBody(sid, true,
			`{"role":"assistant","content":[{"type":"tool_use","id":"`+id+`","name":"Read","input":{"file_path":"/tmp/a.txt"}}]}`,
			`{"role":"user","content":[{"type":"tool_result","tool_use_id":"`+id+`","content":`+string(content)+`}]}`)
		resp = post(t, h.proxyURL, "/v1/messages?beta=true", follow, hdr)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	h.flush()

	links, err := h.store.HTTPToolLinks(ctx, h.runID, "")
	if err != nil {
		t.Fatal(err)
	}
	previews := 0
	for _, l := range links {
		if l.ResultPreview == "" {
			continue
		}
		previews++
		if len(l.ResultPreview) > anthropicwire.MaxPreviewBytes || !strings.Contains(l.ResultPreview, "[REDACTED") ||
			strings.Contains(l.ResultPreview, "eyJ") || strings.Contains(l.ResultPreview, "MIIEvQ") {
			t.Fatalf("%s: preview %q", l.ToolUseID, l.ResultPreview)
		}
	}
	if previews != 2 {
		t.Fatalf("%d previews in %+v", previews, links)
	}
	for _, ex := range h.exchanges() {
		stored, err := h.store.HTTPExchange(ctx, ex.ID)
		if err != nil {
			t.Fatal(err)
		}
		all, _ := json.Marshal(stored)
		if bytes.Contains(all, []byte("eyJ")) || bytes.Contains(all, []byte("MIIEvQ")) {
			t.Fatalf("%s: secret stored: %s", ex.ID, all)
		}
	}
}

// AC-13: a secret in a body is masked in the safe variant; full_raw keeps
// the raw bytes in a local file only
func TestSecretInBody(t *testing.T) {
	secret := fake("sk-ant-api03-", 48)
	for _, mode := range []string{httpcapture.ModeSafe, httpcapture.ModeFullRaw, httpcapture.ModeMetadataOnly} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, mode)
			body := requestBody("s-secret", false, `{"role":"user","content":"minha chave é `+secret+`"}`)
			resp := post(t, h.proxyURL, "/v1/messages/count_tokens", body, nil)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			h.flush()
			ex := h.exchanges()[0]
			stored, err := h.store.HTTPExchange(context.Background(), ex.ID)
			if err != nil {
				t.Fatal(err)
			}
			all, _ := json.Marshal(stored)
			if bytes.Contains(all, []byte(secret)) {
				t.Fatal("secret in the database record")
			}
			pl, _ := h.store.HTTPPayloads(context.Background(), ex.ID)
			variants := map[string]storage.HTTPPayloadRecord{}
			for _, p := range pl {
				if p.Part == httpcapture.PartRequest {
					variants[p.Variant] = p
				}
			}
			switch mode {
			case httpcapture.ModeMetadataOnly:
				if len(pl) != 0 {
					t.Fatalf("bodies stored in metadata_only: %+v", pl)
				}
				return
			case httpcapture.ModeFullRaw:
				raw, ok := variants[api.BodyVariantRaw]
				if !ok {
					t.Fatal("no raw variant")
				}
				data, _, err := h.blobs.ReadFile(raw.RelPath, 1<<20)
				if err != nil || !bytes.Contains(data, []byte(secret)) {
					t.Fatal("raw variant is not the observed body")
				}
			default:
				if _, ok := variants[api.BodyVariantRaw]; ok {
					t.Fatal("raw variant in safe mode")
				}
			}
			safe, ok := variants[api.BodyVariantSafe]
			if !ok || !safe.Sanitized || safe.Redactions == 0 {
				t.Fatalf("safe variant %+v", safe)
			}
			data, _, err := h.blobs.ReadFile(safe.RelPath, 1<<20)
			if err != nil || bytes.Contains(data, []byte(secret)) || !bytes.Contains(data, []byte("[REDACTED")) {
				t.Fatalf("safe variant not redacted: %s", data)
			}
			if !json.Valid(data) {
				t.Fatal("safe variant is no valid JSON")
			}
		})
	}
}

// AC-14: a model without a price: no cost, never a made-up zero
func TestUnpricedModel(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	h.up.handler = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_u","type":"message","role":"assistant","model":"claude-imaginary-9","content":[{"type":"text","text":"oi"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`)
		return true
	}
	resp := post(t, h.proxyURL, "/v1/messages", requestBody("s-u", false), nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	h.flush()
	ex := h.exchanges()
	if len(ex) != 1 || ex[0].Usage == nil || ex[0].Usage.CostUSD != nil || ex[0].Usage.CostStatus != api.CostUnavailable || ex[0].Usage.CostReason == "" {
		t.Fatalf("%+v", ex[0].Usage)
	}
}

// AC-07: two launcher runs side by side never mix their exchanges
func TestParallelLaunchers(t *testing.T) {
	h := newHarness(t, httpcapture.ModeSafe)
	run2, url2, err := h.manager.StartLauncherRun(context.Background(), httpcapture.RunOptions{Name: "segundo"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp := post(t, h.proxyURL, "/v1/messages", requestBody("sessao-a", true), http.Header{"X-Claude-Code-Session-Id": {"sessao-a"}})
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
		go func() {
			defer wg.Done()
			resp := post(t, url2, "/v1/messages", requestBody("sessao-b", true), http.Header{"X-Claude-Code-Session-Id": {"sessao-b"}})
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
	}
	wg.Wait()
	h.flush()
	for runID, want := range map[string]string{h.runID: "sessao-a", run2.ID: "sessao-b"} {
		res, err := h.store.HTTPExchanges(context.Background(), storage.HTTPExchangeFilter{RunID: runID}, storage.Page{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Exchanges) != 10 {
			t.Fatalf("run %s: %d exchanges", runID, len(res.Exchanges))
		}
		for _, e := range res.Exchanges {
			if e.SessionID != want {
				t.Fatalf("run %s has an exchange of %s", runID, e.SessionID)
			}
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
