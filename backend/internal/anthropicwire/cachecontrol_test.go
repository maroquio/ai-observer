package anthropicwire

import (
	"encoding/json"
	"testing"
)

func TestWithoutCacheControl(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"role":"user","content":"oi"}`, `{"role":"user","content":"oi"}`},
		{`{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}`, `{"type":"text","text":"a"}`},
		{`{"cache_control":{"type":"ephemeral","ttl":"1h"},"type":"text","text":"a"}`, `{"type":"text","text":"a"}`},
		{`{"cache_control": {"type":"ephemeral"} , "type":"text"}`, `{"type":"text"}`},
		{`{"cache_control":{"type":"ephemeral"}}`, `{}`},
		{`{"role":"user","content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}},{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"y","cache_control":{"type":"ephemeral"}}]}]}`,
			`{"role":"user","content":[{"type":"text","text":"x"},{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"y"}]}]}`},
		// In a string the key's quotes are escaped: kept
		{`{"type":"text","text":"{\"cache_control\":{}}"}`, `{"type":"text","text":"{\"cache_control\":{}}"}`},
		{`{"type":"text","text":"cache_control"}`, `{"type":"text","text":"cache_control"}`},
	}
	for _, c := range cases {
		got := string(withoutCacheControl([]byte(c.in)))
		if got != c.want {
			t.Errorf("withoutCacheControl(%s) = %s, want %s", c.in, got, c.want)
		}
		if !json.Valid([]byte(got)) {
			t.Errorf("invalid JSON %s", got)
		}
	}
}

// Claude Code moves the cache breakpoint to the newest message: the earlier
// messages still form a common prefix and the first message's hash is
// stable across the conversation
func TestMessageHashesIgnoreMovedBreakpoint(t *testing.T) {
	first := `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"Revise auth.ts","cache_control":{"type":"ephemeral"}}]}]}`
	second := `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"Revise auth.ts"}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"x","cache_control":{"type":"ephemeral"}}]}]}`
	a, b := ParseRequest([]byte(first)), ParseRequest([]byte(second))
	if a.Messages.Hashes[0] != b.Messages.Hashes[0] {
		t.Fatal("first message hash changed with the breakpoint")
	}
	d := DiffRequests(&a, &b)
	if d.CommonPrefix != 1 || d.MessagesAdded != 2 || d.MessagesReplaced != 0 {
		t.Fatalf("diff %+v", d)
	}
}
