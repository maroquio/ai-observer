package anthropicwire

import (
	"testing"
)

func TestParseMessageJSON(t *testing.T) {
	m, err := ParseMessageJSON(readFixture(t, "message.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "msg_01FakeJsonReply00000003" || m.Type != "message" || m.Model != "claude-sonnet-5-5" ||
		!m.Complete || m.Streamed || m.Error != nil {
		t.Errorf("message = %+v", m)
	}
	if m.StopReason == nil || *m.StopReason != "tool_use" || m.StopSequence != nil {
		t.Errorf("stop = %v / %v", m.StopReason, m.StopSequence)
	}
	if len(m.Content) != 3 {
		t.Fatalf("content = %+v", m.Content)
	}
	for i, b := range m.Content {
		if b.Index != i || !b.Stopped || len(b.Raw) == 0 {
			t.Errorf("block %d = %+v", i, b)
		}
	}
	tool := m.Content[1]
	if tool.Name != "Grep" || string(tool.Input) != `{"pattern": "type Config struct", "path": "internal", "-n": true}` || !tool.InputComplete {
		t.Errorf("tool = %+v", tool)
	}
	if m.Content[2].Type != "future_block_kind" || string(m.Content[2].Raw) != `{"type": "future_block_kind", "payload": {"x": 1}}` {
		t.Errorf("unknown block = %+v", m.Content[2])
	}
	if string(m.Extra["future_top_level"]) != `{"enabled": true}` || string(m.Extra["container"]) != "null" || len(m.Extra) != 2 {
		t.Errorf("extra = %v", m.Extra)
	}
	tot := m.Usage.Totals()
	if *tot.TotalInput != 600 || *tot.TotalProcessed != 640 || m.Usage.ServiceTier != "standard" {
		t.Errorf("usage = %+v", m.Usage)
	}
	if refs := ToolUses(m); len(refs) != 1 || refs[0].ID != "toolu_01JSONREPLYTOOLUSE000001" || refs[0].BlockIndex != 1 {
		t.Errorf("ToolUses = %+v", refs)
	}
}

func TestParseMessageJSONError(t *testing.T) {
	body := readFixture(t, "error_529.json")
	m, err := ParseMessageJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if m.Error == nil || m.Error.Type != "overloaded_error" || m.Error.Message != "Overloaded" || m.Complete {
		t.Errorf("message = %+v", m)
	}
	if string(m.Extra["request_id"]) != `"req_011FakeRequestIdForTests"` || m.Extra["error"] != nil {
		t.Errorf("extra = %v", m.Extra)
	}
	if len(m.Content) != 0 || m.Content == nil {
		t.Errorf("content = %#v", m.Content)
	}
	e := ParseAPIError(body)
	if e == nil || e.Type != "overloaded_error" || string(e.Raw) != `{"type":"overloaded_error","message":"Overloaded"}` {
		t.Errorf("ParseAPIError = %+v", e)
	}
}

func TestParseMessageJSONInvalid(t *testing.T) {
	for _, body := range []string{``, `{`, `[]`, `"message"`, `null`} {
		if m, err := ParseMessageJSON([]byte(body)); err == nil || m != nil {
			t.Errorf("%q: %+v, %v", body, m, err)
		}
	}
	m, err := ParseMessageJSON([]byte(`{"type":"message","content":[{"type":"tool_use","id":"toolu_1","name":"Read"}],"usage":{"input_tokens":0}}`))
	if err != nil || m.Content[0].InputComplete || m.Content[0].Input != nil || *m.Usage.InputTokens != 0 || m.Usage.OutputTokens != nil {
		t.Errorf("tool without input: %+v, %v", m, err)
	}
	if m, _ := ParseMessageJSON([]byte(`{"type":"message_batch"}`)); m.Complete {
		t.Error("only type message is complete")
	}
}

func TestParseAPIError(t *testing.T) {
	tests := []struct {
		body string
		ok   bool
		typ  string
	}{
		{`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: too large"}}`, true, "invalid_request_error"},
		{`{"type":"error"}`, true, ""},
		{`{"type":"message","error":{"type":"x"}}`, false, ""},
		{`{"error":{"type":"x"}}`, false, ""},
		{`not json`, false, ""},
		{`[{"type":"error"}]`, false, ""},
	}
	for _, tt := range tests {
		e := ParseAPIError([]byte(tt.body))
		if (e != nil) != tt.ok || (e != nil && e.Type != tt.typ) {
			t.Errorf("%s: %+v", tt.body, e)
		}
	}
}

func TestToolUsesNil(t *testing.T) {
	if refs := ToolUses(nil); refs == nil || len(refs) != 0 {
		t.Errorf("ToolUses(nil) = %#v", refs)
	}
	m := &Message{Content: []ContentBlock{{Type: "server_tool_use", Name: "web_search"}, {Type: "text"}}}
	if refs := ToolUses(m); refs == nil || len(refs) != 0 {
		t.Errorf("server tool counted: %#v", refs)
	}
}

func TestClassifyEndpoint(t *testing.T) {
	tests := []struct {
		method, path string
		want         ExchangeKind
	}{
		{"POST", "/v1/messages", KindModelInference},
		{"post", "/v1/messages/", KindModelInference},
		{"POST", "/v1/messages/count_tokens", KindTokenCount},
		{"POST", "/v1/messages/count_tokens/", KindTokenCount},
		{"POST", "/v1/messages//", KindOtherAPI},
		{"POST", "/v1/messages/batches", KindOtherAPI},
		{"GET", "/v1/models", KindOtherAPI},
		{"GET", "/v1/messages", KindOtherAPI},
		{"POST", "/v1/messages?beta=true", KindOtherAPI}, // the caller strips the query
		{"POST", "/", KindOtherAPI},
		{"", "", KindOtherAPI},
	}
	for _, tt := range tests {
		if got := ClassifyEndpoint(tt.method, tt.path); got != tt.want {
			t.Errorf("%s %s = %s, want %s", tt.method, tt.path, got, tt.want)
		}
	}
}
