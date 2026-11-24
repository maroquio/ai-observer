package anthropicwire

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The typed prompt and the last user message, as Claude Code 2.1.331 sends
// them: reminders as text blocks before the prompt, a role system message
// after the first user message, tool results in later user messages
func TestRequestPrompt(t *testing.T) {
	reminder := `{"type":"text","text":"<system-reminder>\nContexto injetado.\n</system-reminder>"}`
	first := `{"role":"user","content":[` + reminder + `,{"type":"text","text":"Revise src/auth.ts","cache_control":{"type":"ephemeral"}}]}`
	env := `{"role":"system","content":"# Environment"}`
	reply := `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]}`
	result := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"},` + reminder + `]}`
	final := `{"role":"assistant","content":[{"type":"text","text":"Pronto."}]}`
	next := `{"role":"user","content":"  Agora resuma  "}`
	body := func(msgs ...string) []byte {
		return []byte(`{"model":"m","max_tokens":10,"messages":[` + strings.Join(msgs, ",") + `]}`)
	}
	cases := []struct {
		name     string
		body     []byte
		lastUser int
		prompt   string
	}{
		{"first prompt, system message last", body(first, env), 0, "Revise src/auth.ts"},
		{"tool loop", body(first, env, reply, result), 3, ""},
		{"next prompt as a string", body(first, env, reply, result, final, next), 5, "Agora resuma"},
		{"only reminders", body(`{"role":"user","content":[` + reminder + `]}`), 0, ""},
		{"two typed blocks", body(`{"role":"user","content":[{"type":"text","text":"um"},{"type":"image","source":{}},{"type":"text","text":"dois"}]}`), 0, "um\n\ndois"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := ParseRequest(c.body)
			if s.LastUserIndex == nil || *s.LastUserIndex != c.lastUser || s.Prompt != c.prompt {
				t.Fatalf("last user %v, prompt %q", s.LastUserIndex, s.Prompt)
			}
		})
	}
	if s := ParseRequest(body(final)); s.LastUserIndex != nil || s.Prompt != "" {
		t.Fatalf("no user message: %v %q", s.LastUserIndex, s.Prompt)
	}
	long := ParseRequest(body(`{"role":"user","content":"` + strings.Repeat("é", maxPromptSourceBytes) + `"}`))
	if len(long.Prompt) > maxPromptSourceBytes || len(long.Prompt) <= MaxPromptBytes || !strings.HasPrefix(long.Prompt, "éé") {
		t.Fatalf("long prompt %d bytes", len(long.Prompt))
	}
	if cut := TruncatePrompt(long.Prompt); len(cut) > MaxPromptBytes || len(cut) < MaxPromptBytes-1 || !utf8.ValidString(cut) {
		t.Fatalf("truncated prompt %d bytes", len(cut))
	}
}
