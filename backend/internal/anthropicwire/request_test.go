package anthropicwire

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseRequestFixture(t *testing.T) {
	body := readFixture(t, "request_tools.json")
	orig := bytes.Clone(body)
	s := ParseRequest(body)
	if !bytes.Equal(body, orig) {
		t.Fatal("ParseRequest modified its input")
	}
	if !s.Valid || s.ParseError != "" {
		t.Fatalf("invalid: %s", s.ParseError)
	}
	if s.Model != "claude-sonnet-5-5" || *s.MaxTokens != 32000 || *s.Temperature != 1 || !*s.Stream || s.TopP != nil || s.TopK != nil {
		t.Errorf("params = %q %v %v %v", s.Model, s.MaxTokens, s.Temperature, s.Stream)
	}
	if string(s.Thinking) != `{"type": "enabled", "budget_tokens": 31999}` || s.OutputConfig != nil || s.ToolChoice != nil {
		t.Errorf("raw fields = %s / %s / %s", s.Thinking, s.OutputConfig, s.ToolChoice)
	}
	if !strings.Contains(string(s.ContextManagement), "clear_thinking_20251015") {
		t.Errorf("context_management = %s", s.ContextManagement)
	}
	if s.SessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("session = %q", s.SessionID)
	}
	if !reflect.DeepEqual(s.UnknownFields, []string{"future_param", "another_unknown"}) {
		t.Errorf("unknown = %v", s.UnknownFields)
	}

	sys := s.System
	if !sys.Present || sys.Kind != "blocks" || sys.Blocks != 2 || sys.Bytes == 0 || len(sys.SHA256) != 64 {
		t.Errorf("system = %+v", sys)
	}
	if rawSys := memberRaw(t, body, "system"); sys.SHA256 != sha256Hex(rawSys) || sys.Bytes != len(rawSys) {
		t.Error("system hash is not that of the bytes as sent")
	}

	tools := s.Tools
	if tools.Count != 3 || tools.Deferred != 1 || len(tools.Tools) != 3 || tools.Tools[2].Name != "Bash" ||
		!tools.Tools[2].DeferLoading || tools.Tools[0].DeferLoading {
		t.Errorf("tools = %+v", tools)
	}
	if rawTools := memberRaw(t, body, "tools"); tools.SHA256 != sha256Hex(rawTools) || tools.Bytes != len(rawTools) {
		t.Error("tools hash is not that of the bytes as sent")
	}

	msgs := s.Messages
	if msgs.Count != 5 || len(msgs.Hashes) != 5 || len(msgs.Sizes) != 5 || len(msgs.Hashes[0]) != 16 {
		t.Fatalf("messages = %+v", msgs)
	}
	if !reflect.DeepEqual(msgs.Roles, map[string]int{"user": 3, "assistant": 2}) {
		t.Errorf("roles = %v", msgs.Roles)
	}
	if !reflect.DeepEqual(msgs.Blocks, map[string]int{"text": 3, "tool_use": 2, "tool_result": 2}) {
		t.Errorf("blocks = %v", msgs.Blocks)
	}
	var rawMsgs []json.RawMessage
	if err := json.Unmarshal(memberRaw(t, body, "messages"), &rawMsgs); err != nil {
		t.Fatal(err)
	}
	for i, raw := range rawMsgs {
		sum := sha256.Sum256(raw)
		if msgs.Hashes[i] != hex.EncodeToString(sum[:])[:16] || msgs.Sizes[i] != len(raw) {
			t.Errorf("message %d fingerprint differs from its bytes as sent", i)
		}
	}

	wantResults := []ToolResultRef{
		{ToolUseID: "toolu_01REQHISTORY0000000001", MessageIndex: 2, BlockIndex: 0, ContentBytes: 77,
			Preview: "src/cmd/app/main.go:12:func main() {\nsrc/tools/gen/main.go:8:func main() {"},
		{ToolUseID: "toolu_01REQHISTORY0000000002", IsError: true, MessageIndex: 4, BlockIndex: 0, ContentBytes: 164,
			Preview: "Erro: permissão negada (ação bloqueada). Tente outro caminho."},
	}
	if !reflect.DeepEqual(s.ToolResults, wantResults) {
		t.Errorf("tool results = %+v", s.ToolResults)
	}
	if len(s.ToolUses) != 2 || s.ToolUses[0].Name != "Grep" || s.ToolUses[0].MessageIndex != 1 || s.ToolUses[0].BlockIndex != 1 ||
		string(s.ToolUses[0].Input) != `{"pattern": "func main", "path": "src"}` {
		t.Errorf("tool uses = %+v", s.ToolUses)
	}
	wantBP := []CacheBreakpoint{
		{Location: "system[1]", TTL: "1h", Type: "ephemeral"},
		{Location: "tools[2]", Type: "ephemeral"},
		{Location: "messages[4].content[0].content[1]", Type: "ephemeral"},
	}
	if !reflect.DeepEqual(s.CacheBreakpoints, wantBP) {
		t.Errorf("breakpoints = %+v", s.CacheBreakpoints)
	}
	s.ToolUses[0].Input[0] = 'X'
	if body[0] != '{' || !bytes.Equal(body, orig) {
		t.Error("summary aliases the body")
	}
}

// memberRaw returns the raw bytes of a top-level member as encoding/json
// sees them
func memberRaw(t *testing.T, body []byte, key string) []byte {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	return top[key]
}

func TestParseRequestShapes(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		check func(s RequestSummary) bool
	}{
		{"invalid JSON", `{"model":`, func(s RequestSummary) bool { return !s.Valid && s.ParseError == "invalid JSON" }},
		{"array body", `[1]`, func(s RequestSummary) bool { return !s.Valid && s.ParseError != "" }},
		{"empty body", ``, func(s RequestSummary) bool { return !s.Valid }},
		{"empty object", `{}`, func(s RequestSummary) bool { return s.Valid && !s.System.Present && s.Messages.Count == 0 }},
		{"string system and content", `{"system":"Seja breve.","messages":[{"role":"user","content":"oi","cache_control":{"type":"ephemeral"}}]}`,
			func(s RequestSummary) bool {
				return s.System.Kind == "string" && s.System.Blocks == 1 && s.Messages.Blocks["text"] == 1 && len(s.CacheBreakpoints) == 0
			}},
		{"null system", `{"system":null}`, func(s RequestSummary) bool { return !s.System.Present }},
		{"numbers kept", `{"max_tokens":1e3,"temperature":0.70,"top_p":1,"top_k":5,"stream":"yes"}`, func(s RequestSummary) bool {
			return s.MaxTokens == nil && *s.Temperature == 0.7 && *s.TopP == 1 && *s.TopK == 5 && s.Stream == nil
		}},
		{"duplicate keys: last wins once", `{"messages":[{"role":"user","content":"a"}],"messages":[{"role":"user","content":"b"},{"role":"assistant","content":"c"}],"x":1,"x":2}`,
			func(s RequestSummary) bool {
				return s.Messages.Count == 2 && reflect.DeepEqual(s.UnknownFields, []string{"x"})
			}},
		{"escaped keys", `{"model":"claude-sonnet-5-5","ç":1}`, func(s RequestSummary) bool {
			return s.Model == "claude-sonnet-5-5" && reflect.DeepEqual(s.UnknownFields, []string{"ç"})
		}},
		{"legacy user_id", `{"metadata":{"user_id":"user_abc123_account_0b1c2d3e-0000-4000-8000-000000000001_session_8f9e0d1c-2b3a-4c5d-9e8f-7a6b5c4d3e2f"}}`,
			func(s RequestSummary) bool { return s.SessionID == "8f9e0d1c-2b3a-4c5d-9e8f-7a6b5c4d3e2f" }},
		{"odd blocks", `{"messages":[{"content":[1,{"text":"x"},{"type":"image","source":{}}]},"x"]}`, func(s RequestSummary) bool {
			return s.Messages.Count == 2 && s.Messages.Blocks["unknown"] == 2 && s.Messages.Blocks["image"] == 1 && s.Messages.Roles["unknown"] == 2
		}},
		{"tool_result without content", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_9"}]}]}`,
			func(s RequestSummary) bool {
				return len(s.ToolResults) == 1 && s.ToolResults[0].ContentBytes == 0 && s.ToolResults[0].Preview == ""
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ParseRequest([]byte(tt.body))
			if !tt.check(s) {
				t.Errorf("summary = %+v", s)
			}
			if s.ToolResults == nil || s.ToolUses == nil || s.CacheBreakpoints == nil || s.UnknownFields == nil ||
				s.Tools.Tools == nil || s.Messages.Hashes == nil || s.Messages.Sizes == nil ||
				s.Messages.Roles == nil || s.Messages.Blocks == nil {
				t.Errorf("nil list field in %+v", s)
			}
		})
	}
}

func TestToolResultPreview(t *testing.T) {
	long := strings.Repeat("é", 400) // 800 bytes
	escaped := strings.Repeat(`ç`, 1000)
	tests := []struct {
		name, content, want string
	}{
		{"cut at rune boundary", `"` + long + `"`, strings.Repeat("é", 150)},
		{"odd boundary", `"a` + long + `"`, "a" + strings.Repeat("é", 149)},
		{"escapes decoded", `"` + escaped + `"`, strings.Repeat("ç", 150)},
		{"text blocks concatenated", `[{"type":"text","text":"um "},{"type":"image","source":{"data":"AAAA"}},{"type":"text","text":"dois"}]`, "um dois"},
		{"emoji not split", `"` + strings.Repeat("x", 298) + `🚀"`, strings.Repeat("x", 298)},
	}
	for _, tt := range tests {
		body := `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":` + tt.content + `}]}]}`
		s := ParseRequest([]byte(body))
		if len(s.ToolResults) != 1 {
			t.Fatalf("%s: %+v", tt.name, s)
		}
		if got := s.ToolResults[0].Preview; got != tt.want {
			t.Errorf("%s: preview %q (%d bytes), want %d bytes", tt.name, got, len(got), len(tt.want))
		}
		if s.ToolResults[0].ContentBytes != len(tt.content) {
			t.Errorf("%s: content bytes %d, want %d", tt.name, s.ToolResults[0].ContentBytes, len(tt.content))
		}
	}
}

func TestSessionIDFromUserID(t *testing.T) {
	tests := []struct{ in, want string }{
		{`{"device_id":"0f1e2d","account_uuid":"","session_id":"11111111-2222-4333-8444-555555555555"}`, "11111111-2222-4333-8444-555555555555"},
		{` {"session_id":"abc.DEF_1:2-3"} `, "abc.DEF_1:2-3"},
		{`user_0f1e_account_1111_session_2222-3333`, "2222-3333"},
		{`user_x_session_a_session_b`, "b"},
		{`{"session_id":""}`, ""},
		{`{"session_id":"has space"}`, ""},
		{`{"session_id":123}`, ""},
		{`{"session_id":"` + strings.Repeat("a", 129) + `"}`, ""},
		{`{"session_id":"` + strings.Repeat("a", 128) + `"}`, strings.Repeat("a", 128)},
		{`{broken`, ""},
		{`user_abc_session_`, ""},
		{`user_abc_session_x/y`, ""},
		{`plain-user`, ""},
		{``, ""},
	}
	for _, tt := range tests {
		if got := SessionIDFromUserID(tt.in); got != tt.want {
			t.Errorf("%q: got %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestJSONWalkMatchesEncodingJSON checks the walker against encoding/json:
// same member keys and raw values, same array elements
func TestJSONWalkMatchesEncodingJSON(t *testing.T) {
	docs := []string{
		`{}`, `[]`, `{"a":1}`, ` { "a" : [ 1 , 2 ] , "b" : { "c" : "d\"}" } } `,
		`{"s":"a\\\\\"b\\","t":"\\\\","u":"ç🚀","n":-1.5e+3,"l":[true,false,null]}`,
		`[{"x":[{"y":[[[]]]}]},"]",  "}" ,0]`,
		`{"k":1,"k":2}`, `{"":""}`,
		`["\"", "\\", "\\\"", "a\\\\\\\"b"]`,
	}
	for _, doc := range docs {
		checkWalk(t, []byte(doc))
	}
}

// checkWalk compares forEachMember/forEachElem with encoding/json on data
func checkWalk(t *testing.T, data []byte) {
	t.Helper()
	if !json.Valid(data) {
		return
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) == nil {
		got := map[string]json.RawMessage{}
		if !forEachMember(data, func(k string, v []byte) { got[k] = v }) {
			t.Fatalf("%q: forEachMember failed", data)
		}
		if len(got) != len(obj) {
			t.Fatalf("%q: members %v, want %v", data, got, obj)
		}
		for k, v := range obj {
			if !bytes.Equal(got[k], v) {
				t.Fatalf("%q: member %q = %q, want %q", data, k, got[k], v)
			}
		}
		return
	}
	var arr []json.RawMessage
	if json.Unmarshal(data, &arr) == nil {
		var got []json.RawMessage
		if !forEachElem(data, func(_ int, v []byte) { got = append(got, v) }) {
			t.Fatalf("%q: forEachElem failed", data)
		}
		if len(got) != len(arr) {
			t.Fatalf("%q: elems %q, want %q", data, got, arr)
		}
		for i := range arr {
			if !bytes.Equal(got[i], arr[i]) {
				t.Fatalf("%q: elem %d = %q, want %q", data, i, got[i], arr[i])
			}
		}
		return
	}
	if forEachMember(data, func(string, []byte) {}) || forEachElem(data, func(int, []byte) {}) {
		t.Fatalf("%q: walker accepted a scalar", data)
	}
}

// syntheticRequest builds a Claude Code-like request of about size bytes
func syntheticRequest(size int) []byte {
	var b bytes.Buffer
	b.WriteString(`{"model":"claude-sonnet-5-5","max_tokens":32000,"stream":true,"system":[{"type":"text","text":"Fictional system prompt.","cache_control":{"type":"ephemeral","ttl":"1h"}}],"tools":[`)
	for i := range 40 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"name":"Tool%d","description":"%s","input_schema":{"type":"object","properties":{"arg":{"type":"string"}}}}`, i, strings.Repeat("descrição ", 50))
	}
	b.WriteString(`],"messages":[`)
	payload := strings.Repeat(`linha de saída com \"aspas\" e acentuação: ação\n`, 400)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		if i%2 == 0 {
			fmt.Fprintf(&b, `{"role":"assistant","content":[{"type":"text","text":"Vou executar."},{"type":"tool_use","id":"toolu_%06d","name":"Bash","input":{"command":"ls -la"}}]}`, i)
		} else {
			fmt.Fprintf(&b, `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%06d","content":"%s"}]}`, i-1, payload)
		}
	}
	b.WriteString(`],"metadata":{"user_id":"{\"session_id\":\"11111111-2222-4333-8444-555555555555\"}"}}`)
	return b.Bytes()
}

func TestParseRequestLargeBody(t *testing.T) {
	body := syntheticRequest(10 << 20)
	s := ParseRequest(body)
	if !s.Valid || s.Messages.Count < 200 || len(s.ToolResults) != s.Messages.Count/2 || s.Tools.Count != 40 ||
		s.SessionID == "" || len(s.ToolResults[0].Preview) > MaxPreviewBytes {
		t.Fatalf("summary of %d bytes: valid=%v messages=%d results=%d", len(body), s.Valid, s.Messages.Count, len(s.ToolResults))
	}
}

func BenchmarkParseRequest10MB(b *testing.B) {
	body := syntheticRequest(10 << 20)
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		ParseRequest(body)
	}
}
