package anthropicwire

import (
	"reflect"
	"testing"
)

// conversation builds a request body from its messages (raw JSON) and
// extra top-level members
func conversation(extra string, messages ...string) RequestSummary {
	body := `{"model":"claude-sonnet-5-5"` + extra + `,"messages":[`
	for i, m := range messages {
		if i > 0 {
			body += ","
		}
		body += m
	}
	return ParseRequest([]byte(body + "]}"))
}

const (
	msgUser      = `{"role":"user","content":"Liste os arquivos."}`
	msgToolUse   = `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01A","name":"Bash","input":{"command":"ls"}}]}`
	msgResult    = `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01A","content":"a.go\nb.go","cache_control":{"type":"ephemeral"}}]}`
	msgAnswer    = `{"role":"assistant","content":"Há dois arquivos."}`
	msgSummary   = `{"role":"user","content":"Resumo da conversa anterior: listamos arquivos."}`
	toolsV1      = `,"tools":[{"name":"Read","input_schema":{}},{"name":"Grep","input_schema":{}}]`
	toolsV2      = `,"tools":[{"name":"Read","input_schema":{"type":"object"}},{"name":"Bash","input_schema":{}}]`
	paramsBefore = `,"max_tokens":32000,"thinking":{"type": "enabled",  "budget_tokens": 1024},"temperature":1`
	paramsAfter  = `,"max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":1024},"temperature":0.5,"tool_choice":{"type":"auto"}`
)

func TestDiffRequestsGrowingConversation(t *testing.T) {
	prev := conversation(toolsV1+paramsBefore, msgUser, msgToolUse)
	cur := conversation(toolsV2+paramsAfter, msgUser, msgToolUse, msgResult, msgAnswer)
	d := DiffRequests(&prev, &cur)
	if d.CommonPrefix != 2 || d.MessagesAdded != 2 || d.MessagesReplaced != 0 || d.PossibleCompaction {
		t.Errorf("prefix = %+v", d)
	}
	if d.RepeatedBytes != len(msgUser)+len(msgToolUse) || d.NewBytes != len(msgResult)+len(msgAnswer) {
		t.Errorf("bytes: repeated %d new %d", d.RepeatedBytes, d.NewBytes)
	}
	if !reflect.DeepEqual(d.NewToolResults, []string{"toolu_01A"}) {
		t.Errorf("new tool results = %v", d.NewToolResults)
	}
	if !d.ToolsChanged || !reflect.DeepEqual(d.ToolsAdded, []string{"Bash"}) || !reflect.DeepEqual(d.ToolsRemoved, []string{"Grep"}) ||
		!reflect.DeepEqual(d.ToolsModified, []string{"Read"}) || d.ToolsCountBefore != 2 || d.ToolsCountAfter != 2 {
		t.Errorf("tools = %+v", d)
	}
	want := []FieldChange{
		{Field: "temperature", Before: "1", After: "0.5"},
		{Field: "tool_choice", After: `{"type":"auto"}`},
	}
	if !reflect.DeepEqual(d.ParamChanges, want) {
		t.Errorf("param changes = %+v", d.ParamChanges)
	}
	if !d.CacheChanged || len(d.CacheBreakpointsBefore) != 0 || len(d.CacheBreakpointsAfter) != 1 {
		t.Errorf("cache = %+v", d)
	}
	if d.ModelChanged || d.SystemChanged {
		t.Errorf("model/system changed: %+v", d)
	}
}

func TestDiffRequestsCompaction(t *testing.T) {
	prev := conversation("", msgUser, msgToolUse, msgResult, msgAnswer)
	cur := conversation(`,"system":"novo"`, msgSummary, msgUser)
	d := DiffRequests(&prev, &cur)
	if d.CommonPrefix != 0 || d.MessagesReplaced != 4 || d.MessagesAdded != 2 || !d.PossibleCompaction {
		t.Errorf("diff = %+v", d)
	}
	if !d.SystemChanged || d.SystemSHA256After != cur.System.SHA256 || d.SystemBytesBefore != 0 || d.SystemBytesAfter != len(`"novo"`) {
		t.Errorf("system = %+v", d)
	}
	if len(d.NewToolResults) != 0 || d.NewToolResults == nil {
		t.Errorf("new tool results = %#v", d.NewToolResults)
	}
	if !d.CacheChanged {
		t.Error("breakpoint removed but CacheChanged false")
	}

	// rewritten tail, same length: replaced, not a compaction
	edited := conversation("", msgUser, msgToolUse, msgResult, msgSummary)
	d = DiffRequests(&prev, &edited)
	if d.CommonPrefix != 3 || d.MessagesReplaced != 1 || d.MessagesAdded != 1 || d.PossibleCompaction {
		t.Errorf("edited = %+v", d)
	}
}

func TestDiffRequestsNilAndIdentical(t *testing.T) {
	cur := conversation(toolsV1+paramsBefore, msgUser, msgToolUse, msgResult)
	d := DiffRequests(nil, &cur)
	if d.MessagesBefore != 0 || d.MessagesAdded != 3 || d.CommonPrefix != 0 || d.NewBytes != len(msgUser)+len(msgToolUse)+len(msgResult) ||
		!reflect.DeepEqual(d.ToolsAdded, []string{"Grep", "Read"}) || !d.ModelChanged || len(d.ParamChanges) != 3 ||
		!reflect.DeepEqual(d.NewToolResults, []string{"toolu_01A"}) || d.PossibleCompaction {
		t.Errorf("nil prev = %+v", d)
	}
	same := DiffRequests(&cur, &cur)
	if same.CommonPrefix != 3 || same.MessagesAdded != 0 || same.ToolsChanged || same.CacheChanged || same.SystemChanged ||
		len(same.ParamChanges) != 0 || len(same.NewToolResults) != 0 {
		t.Errorf("identical = %+v", same)
	}
	empty := DiffRequests(nil, nil)
	for name, s := range map[string]any{
		"ParamChanges": empty.ParamChanges, "ToolsAdded": empty.ToolsAdded, "ToolsRemoved": empty.ToolsRemoved,
		"ToolsModified": empty.ToolsModified, "NewToolResults": empty.NewToolResults,
		"CacheBreakpointsBefore": empty.CacheBreakpointsBefore, "CacheBreakpointsAfter": empty.CacheBreakpointsAfter,
	} {
		if reflect.ValueOf(s).IsNil() {
			t.Errorf("%s is nil", name)
		}
	}
}

func TestDiffRequestsCacheTTL(t *testing.T) {
	a := ParseRequest([]byte(`{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}]}`))
	b := ParseRequest([]byte(`{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`))
	if d := DiffRequests(&a, &b); !d.CacheChanged || !d.SystemChanged {
		t.Errorf("ttl change not seen: %+v", d)
	}
}
