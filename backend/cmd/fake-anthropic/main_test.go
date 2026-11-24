package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
)

func post(t *testing.T, url, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

// The simulated conversation: a request asking about auth gets a Read
// tool_use, the request carrying its result gets the final reply
func TestSimulatedToolUseConversation(t *testing.T) {
	srv := httptest.NewServer(&server{file: "/demo/src/auth.ts", quiet: true})
	defer srv.Close()
	tools := `"tools":[{"name":"Read","input_schema":{"type":"object"}}]`

	resp, data := post(t, srv.URL+"/v1/messages", `{"model":"claude-sonnet-4-5","stream":true,`+tools+`,"messages":[{"role":"user","content":"Revise src/auth.ts"}]}`)
	if resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Request-Id") == "" {
		t.Fatalf("headers %v", resp.Header)
	}
	r := anthropicwire.NewReconstructor()
	p := anthropicwire.NewSSEParser(0)
	for _, ev := range p.Feed(data) {
		r.Add(ev)
	}
	m := r.Message()
	if m.StopReason == nil || *m.StopReason != "tool_use" || !m.Complete || len(anthropicwire.ToolUses(m)) != 1 || m.Usage.CacheCreation5m == nil || *m.Usage.CacheCreation5m != 3000 {
		t.Fatalf("first reply %+v", m)
	}
	use := anthropicwire.ToolUses(m)[0]
	if use.Name != "Read" || !strings.Contains(string(use.Input), "/demo/src/auth.ts") {
		t.Fatalf("tool use %+v", use)
	}

	_, data = post(t, srv.URL+"/v1/messages", `{"model":"claude-sonnet-4-5",`+tools+`,"messages":[{"role":"user","content":"Revise src/auth.ts"},`+
		`{"role":"assistant","content":[{"type":"tool_use","id":"`+use.ID+`","name":"Read","input":{}}]},`+
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"`+use.ID+`","content":"x"}]}]}`)
	final, err := anthropicwire.ParseMessageJSON(data)
	if err != nil || final.StopReason == nil || *final.StopReason != "end_turn" || final.Usage.CacheReadInputTokens == nil || *final.Usage.CacheReadInputTokens != 3000 {
		t.Fatalf("final reply %+v, %v", final, err)
	}

	_, data = post(t, srv.URL+"/v1/messages/count_tokens", `{"model":"m","messages":[]}`)
	var count map[string]int
	if json.Unmarshal(data, &count) != nil || count["input_tokens"] <= 0 {
		t.Fatalf("count_tokens %s", data)
	}
	resp, _ = post(t, srv.URL+"/v1/models", `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("other path %d", resp.StatusCode)
	}
}
