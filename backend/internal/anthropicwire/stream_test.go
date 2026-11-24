package anthropicwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// reconstruct parses an SSE stream fed in chunks and rebuilds its message
func reconstruct(chunks ...[]byte) (*Message, []StreamEvent) {
	r := NewReconstructor()
	var events []StreamEvent
	for _, ev := range parseChunks(0, chunks...) {
		events = append(events, r.Add(ev))
	}
	return r.Message(), events
}

// sse builds a stream from event lines: each item is "type|data" (an empty
// type omits the event: field)
func sse(items ...string) []byte {
	var b strings.Builder
	for _, it := range items {
		typ, data, _ := strings.Cut(it, "|")
		if typ != "" {
			b.WriteString("event: " + typ + "\n")
		}
		b.WriteString("data: " + data + "\n\n")
	}
	return []byte(b.String())
}

// ev formats a stream event item for sse with its JSON type as event field
func ev(data string) string {
	var w struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(data), &w)
	return w.Type + "|" + data
}

const (
	evMessageStart = `{"type":"message_start","message":{"id":"msg_01T","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1}}}`
	evMessageStop  = `{"type":"message_stop"}`
)

func i64(n int64) *int64 { return &n }

func TestReconstructToolUseStream(t *testing.T) {
	data := readFixture(t, "stream_tool_use.sse")
	m, events := reconstruct(data)
	for _, se := range events {
		if se.ParseStatus != ParseOK {
			t.Errorf("event %d: %s %s", se.Seq, se.ParseStatus, se.Note)
		}
	}
	if !m.Streamed || !m.SawMessageStart || !m.SawMessageStop || !m.Complete || m.Error != nil {
		t.Fatalf("framing flags wrong: %+v", m)
	}
	if m.ID != "msg_01FakeStreamToolUse0001" || m.Model != "claude-sonnet-5-5" || m.Role != "assistant" || m.Type != "message" {
		t.Errorf("header = %q %q %q %q", m.ID, m.Model, m.Role, m.Type)
	}
	if m.StopReason == nil || *m.StopReason != "tool_use" || m.StopSequence != nil {
		t.Errorf("stop = %v %v", m.StopReason, m.StopSequence)
	}
	if len(m.Content) != 3 {
		t.Fatalf("content = %+v", m.Content)
	}
	if got := m.Content[0].Text; got != "Vou ler o arquivo de configuração e procurar a função principal. 🔍" {
		t.Errorf("text = %q", got)
	}
	read := m.Content[1]
	if read.Type != "tool_use" || read.Name != "Read" || read.ID != "toolu_01ABCDEFGHJKLMNPQRSTUVWX" || !read.Stopped {
		t.Errorf("tool block = %+v", read)
	}
	wantInput := `{"file_path": "/home/dev/projeto/config/ação.yaml", "limit": 200}`
	if string(read.Input) != wantInput || read.PartialInput != wantInput || !read.InputComplete || read.InputError != "" {
		t.Errorf("tool input = %s / %q / %v / %q", read.Input, read.PartialInput, read.InputComplete, read.InputError)
	}
	if string(m.Content[2].Input) != `{"pattern": "func main", "path": "src"}` {
		t.Errorf("grep input = %s", m.Content[2].Input)
	}
	u := m.Usage
	if *u.InputTokens != 12 || *u.CacheCreationInputTokens != 3000 || *u.CacheReadInputTokens != 45000 ||
		*u.OutputTokens != 87 || *u.CacheCreation5m != 0 || *u.CacheCreation1h != 3000 || u.ServiceTier != "standard" {
		t.Errorf("usage = %+v", u)
	}
	if string(u.Raw) != `{"output_tokens":87}` {
		t.Errorf("raw usage = %s", u.Raw)
	}
	if len(m.UsageSnapshots) != 2 || m.UsageSnapshots[0].EventType != "message_start" ||
		m.UsageSnapshots[1].EventType != "message_delta" || m.UsageSnapshots[1].EventSeq != 15 {
		t.Errorf("snapshots = %+v", m.UsageSnapshots)
	}
	want := map[string]int{"message_start": 1, "content_block_start": 3, "ping": 1, "content_block_delta": 7,
		"content_block_stop": 3, "message_delta": 1, "message_stop": 1}
	if !reflect.DeepEqual(m.EventCounts, want) {
		t.Errorf("counts = %v", m.EventCounts)
	}
	refs := ToolUses(m)
	if len(refs) != 2 || refs[0].Name != "Read" || refs[0].MessageIndex != -1 || refs[0].BlockIndex != 1 || string(refs[0].Input) != wantInput {
		t.Errorf("ToolUses = %+v", refs)
	}

	// the same message whatever the chunking
	for i := 0; i <= len(data); i += 7 {
		got, _ := reconstruct(data[:i], data[i:])
		if !reflect.DeepEqual(got, m) {
			t.Fatalf("split at %d gives a different message", i)
		}
	}
	for seed := uint64(1); seed <= 5; seed++ {
		got, _ := reconstruct(splitRandom(data, seed, 7)...)
		if !reflect.DeepEqual(got, m) {
			t.Fatalf("seed %d gives a different message", seed)
		}
	}
}

func TestReconstructThinkingStream(t *testing.T) {
	m, _ := reconstruct(readFixture(t, "stream_text.sse"))
	if !m.Complete || len(m.Content) != 2 {
		t.Fatalf("message = %+v", m)
	}
	th := m.Content[0]
	if th.Type != "thinking" || th.Thinking != "O usuário quer um resumo curto da alteração." ||
		th.Signature != "RmFrZVNpZ25hdHVyZUZvclRlc3RzT25seQ==" || th.Input != nil || th.InputError != "" {
		t.Errorf("thinking = %+v", th)
	}
	if m.Content[1].Text != "A alteração adiciona validação de entrada ✅." {
		t.Errorf("text = %q", m.Content[1].Text)
	}
	if *m.StopReason != "end_turn" || *m.Usage.OutputTokens != 42 || *m.Usage.CacheReadInputTokens != 51234 {
		t.Errorf("stop/usage = %v %+v", *m.StopReason, m.Usage)
	}
	if m.Usage.CacheCreation5m != nil || m.Usage.CacheCreation1h != nil {
		t.Error("cache split invented")
	}
}

func TestReconstructBlockTypes(t *testing.T) {
	tests := []struct {
		block string
		check func(b ContentBlock) bool
	}{
		{`{"type":"text","text":"olá"}`, func(b ContentBlock) bool { return b.Type == "text" && b.Text == "olá" }},
		{`{"type":"tool_use","id":"toolu_01X","name":"Bash","input":{}}`, func(b ContentBlock) bool {
			return b.ID == "toolu_01X" && b.Name == "Bash" && string(b.Input) == "{}" && b.InputComplete
		}},
		{`{"type":"thinking","thinking":"","signature":""}`, func(b ContentBlock) bool { return b.Type == "thinking" && b.Input == nil }},
		{`{"type":"redacted_thinking","data":"EmFakeOpaqueData=="}`, func(b ContentBlock) bool {
			return b.Type == "redacted_thinking" && b.Data == "EmFakeOpaqueData=="
		}},
		{`{"type":"server_tool_use","id":"srvtoolu_01X","name":"web_search","input":{"query":"go 1.26"}}`, func(b ContentBlock) bool {
			return b.Name == "web_search" && string(b.Input) == `{"query":"go 1.26"}` && b.InputComplete
		}},
		{`{"type":"mcp_tool_use","id":"mcptoolu_01X","name":"lookup","server_name":"docs","input":{}}`, func(b ContentBlock) bool {
			return b.Type == "mcp_tool_use" && b.InputComplete && strings.Contains(string(b.Raw), "server_name")
		}},
		{`{"type":"web_search_tool_result","tool_use_id":"srvtoolu_01X","content":[{"type":"web_search_result","url":"https://example.com","title":"Exemplo"}]}`,
			func(b ContentBlock) bool {
				return b.ToolUseID == "srvtoolu_01X" && strings.Contains(string(b.Raw), "example.com") && b.Input == nil
			}},
		{`{"type":"hologram_block","shape":{"sides":7}}`, func(b ContentBlock) bool {
			return b.Type == "hologram_block" && string(b.Raw) == `{"type":"hologram_block","shape":{"sides":7}}`
		}},
	}
	for _, tt := range tests {
		data := sse(ev(evMessageStart),
			ev(`{"type":"content_block_start","index":0,"content_block":`+tt.block+`}`),
			ev(`{"type":"content_block_stop","index":0}`),
			ev(evMessageStop))
		m, events := reconstruct(data)
		if len(m.Content) != 1 || !m.Content[0].Stopped || !tt.check(m.Content[0]) || !m.Complete {
			t.Errorf("block %s reconstructed as %+v", tt.block, m.Content)
		}
		if string(m.Content[0].Raw) != tt.block {
			t.Errorf("raw = %s", m.Content[0].Raw)
		}
		if events[1].Index == nil || *events[1].Index != 0 || events[1].ParseStatus != ParseOK {
			t.Errorf("start event = %+v", events[1])
		}
	}
}

func TestReconstructDeltas(t *testing.T) {
	data := sse(ev(evMessageStart),
		ev(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"A"}}`),
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"B"}}`),
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"char_location","cited_text":"x"}}}`),
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"sparkle_delta","sparkle":1}}`),
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"sparkle_delta","sparkle":2}}`),
		ev(`{"type":"content_block_stop","index":0}`),
		ev(`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`),
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"pensando"}}`),
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"sig1"}}`),
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"sig2"}}`),
		ev(`{"type":"content_block_stop","index":1}`),
		ev(evMessageStop))
	m, events := reconstruct(data)
	if m.Content[0].Text != "AB" || m.Content[1].Thinking != "pensando" || m.Content[1].Signature != "sig1sig2" {
		t.Errorf("content = %+v", m.Content)
	}
	if events[3].DeltaType != "citations_delta" || events[4].DeltaType != "sparkle_delta" || events[4].ParseStatus != ParseOK {
		t.Errorf("delta events = %+v %+v", events[3], events[4])
	}
	if m.EventCounts["content_block_delta"] != 7 {
		t.Errorf("counts = %v", m.EventCounts)
	}
	want := "content index 0: deltas not interpreted: 1 citations_delta, 2 sparkle_delta"
	if len(m.Notes) != 1 || m.Notes[0] != want {
		t.Errorf("notes = %q", m.Notes)
	}
	if !m.Complete {
		t.Error("unknown deltas made the message incomplete")
	}
}

func TestReconstructPartialToolJSON(t *testing.T) {
	input := `{"command": "grep -rn \"ação\" src | head -n 20", "timeout": 120000, "description": "Busca 🔎"}`
	items := []string{ev(evMessageStart),
		ev(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01P","name":"Bash","input":{}}}`)}
	// fragments are JSON strings: they split the text between runes
	runes := []rune(input)
	for i := 0; i < len(runes); i += 3 {
		frag, _ := json.Marshal(string(runes[i:min(i+3, len(runes))]))
		items = append(items, ev(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+string(frag)+`}}`))
	}
	full := append(items, ev(`{"type":"content_block_stop","index":0}`), ev(evMessageStop))
	m, _ := reconstruct(sse(full...))
	if b := m.Content[0]; string(b.Input) != input || !b.InputComplete || !m.Complete {
		t.Fatalf("assembled input = %s (%v)", b.Input, b.InputComplete)
	}

	// cut in the middle of the arguments: kept, flagged incomplete
	cut := items[:len(items)/2]
	m, _ = reconstruct(sse(cut...))
	b := m.Content[0]
	if b.Input != nil || b.InputComplete || b.InputError != "incomplete" || b.PartialInput == "" || !strings.HasPrefix(input, b.PartialInput) {
		t.Errorf("interrupted tool block = %+v", b)
	}
	if m.Complete || m.SawMessageStop || len(ToolUses(m)) != 1 || ToolUses(m)[0].Input != nil {
		t.Errorf("interrupted message = %+v", m)
	}

	// stopped with invalid arguments
	bad := append(cut[:len(cut):len(cut)], ev(`{"type":"content_block_stop","index":0}`), ev(evMessageStop))
	m, _ = reconstruct(sse(bad...))
	if b := m.Content[0]; b.InputError != "invalid JSON" || b.Input != nil || b.PartialInput == "" || !b.Stopped {
		t.Errorf("invalid tool block = %+v", b)
	}
}

func TestReconstructCumulativeUsage(t *testing.T) {
	data := sse(ev(`{"type":"message_start","message":{"id":"msg_01U","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":100,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":200},"output_tokens":1}}}`),
		ev(`{"type":"message_delta","delta":{},"usage":{"output_tokens":10}}`),
		ev(`{"type":"message_delta","delta":{},"usage":{"output_tokens":20}}`),
		ev(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":31}}`),
		ev(evMessageStop))
	m, _ := reconstruct(data)
	u := m.Usage
	if *u.OutputTokens != 31 {
		t.Errorf("output tokens = %d, want 31", *u.OutputTokens)
	}
	if *u.InputTokens != 100 || *u.CacheReadInputTokens != 200 || *u.CacheCreationInputTokens != 300 ||
		*u.CacheCreation5m != 100 || *u.CacheCreation1h != 200 {
		t.Errorf("input/cache fields lost: %+v", u)
	}
	if len(m.UsageSnapshots) != 4 {
		t.Fatalf("snapshots = %d, want 4", len(m.UsageSnapshots))
	}
	for i, want := range []int64{1, 10, 20, 31} {
		s := m.UsageSnapshots[i]
		if *s.Usage.OutputTokens != want || *s.Usage.InputTokens != 100 {
			t.Errorf("snapshot %d = %+v", i, s.Usage)
		}
	}
	tot := u.Totals()
	if *tot.TotalInput != 600 || *tot.TotalProcessed != 631 || tot.CacheWriteUnknown != nil {
		t.Errorf("totals = %+v", tot)
	}
}

func TestReconstructInterruptions(t *testing.T) {
	start := ev(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	delta := ev(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Parcial"}}`)
	stop := ev(`{"type":"content_block_stop","index":0}`)
	overloaded := ev(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)

	t.Run("no message_stop", func(t *testing.T) {
		m, _ := reconstruct(sse(ev(evMessageStart), start, delta, stop))
		if m.Complete || m.SawMessageStop || m.Content[0].Text != "Parcial" {
			t.Errorf("message = %+v", m)
		}
	})
	t.Run("cut mid-event", func(t *testing.T) {
		data := append(sse(ev(evMessageStart), start, delta), []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"del")...)
		m, events := reconstruct(data)
		last := events[len(events)-1]
		if last.ParseStatus != ParseInvalidJSON || last.Type != "content_block_delta" {
			t.Errorf("pending event = %+v", last)
		}
		if m.Content[0].Text != "Parcial" || m.Content[0].Stopped || m.Complete || len(m.Notes) != 1 {
			t.Errorf("message = %+v", m)
		}
	})
	t.Run("error after text", func(t *testing.T) {
		m, _ := reconstruct(sse(ev(evMessageStart), start, delta, stop, overloaded))
		if m.Error == nil || m.Error.Type != "overloaded_error" || m.Error.Message != "Overloaded" ||
			string(m.Error.Raw) != `{"type":"overloaded_error","message":"Overloaded"}` {
			t.Errorf("error = %+v", m.Error)
		}
		if m.Complete || m.Content[0].Text != "Parcial" {
			t.Errorf("message = %+v", m)
		}
	})
	t.Run("error in the middle", func(t *testing.T) {
		m, _ := reconstruct(sse(ev(evMessageStart), start, overloaded, delta, stop, ev(evMessageStop)))
		if m.Error == nil || m.Complete || !m.SawMessageStop || m.Content[0].Text != "Parcial" || m.EventCounts["error"] != 1 {
			t.Errorf("message = %+v", m)
		}
	})
}

func TestReconstructParseStatuses(t *testing.T) {
	tests := []struct {
		name   string
		event  SSEEvent
		status string
		typ    string
	}{
		{"ok", SSEEvent{Event: "ping", Data: []byte(`{"type":"ping"}`)}, ParseOK, "ping"},
		{"mismatch", SSEEvent{Event: "ping", Data: []byte(`{"type":"message_stop"}`)}, ParseMismatch, "message_stop"},
		{"invalid json", SSEEvent{Event: "content_block_delta", Data: []byte(`{"type":`)}, ParseInvalidJSON, "content_block_delta"},
		{"empty", SSEEvent{Event: "message_stop", Data: []byte{}}, ParseEmpty, "message_stop"},
		{"truncated", SSEEvent{Event: "content_block_delta", Data: []byte(`{"type":"content_blo`), Truncated: true}, ParseTruncated, "content_block_delta"},
		{"not an object", SSEEvent{Event: "x", Data: []byte(`[1,2]`)}, ParseUnexpected, "x"},
		{"no type", SSEEvent{Event: "x", Data: []byte(`{"type":5}`)}, ParseUnexpected, "x"},
		{"unknown type", SSEEvent{Data: []byte(`{"type":"future_event","x":1}`)}, ParseOK, "future_event"},
		{"fractional index", SSEEvent{Data: []byte(`{"type":"content_block_stop","index":1.5}`)}, ParseUnexpected, "content_block_stop"},
		{"delta before start", SSEEvent{Data: []byte(`{"type":"content_block_delta","index":4,"delta":{"type":"text_delta","text":"x"}}`)}, ParseUnexpected, "content_block_delta"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReconstructor()
			se := r.Add(tt.event)
			if se.ParseStatus != tt.status || se.Type != tt.typ {
				t.Errorf("got %s/%q (%s), want %s/%q", se.ParseStatus, se.Type, se.Note, tt.status, tt.typ)
			}
			if (se.ParseStatus == ParseOK) != (se.Note == "") {
				t.Errorf("note %q with status %s", se.Note, se.ParseStatus)
			}
			if r.Message().EventCounts[tt.typ] != 1 {
				t.Errorf("counts = %v", r.Message().EventCounts)
			}
		})
	}
}

func TestReconstructIndicesAndPlaceholders(t *testing.T) {
	data := sse(ev(evMessageStart),
		ev(`{"type":"content_block_start","index":5,"content_block":{"type":"text","text":""}}`),
		ev(`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`),
		ev(`{"type":"content_block_delta","index":7,"delta":{"type":"text_delta","text":"órfão"}}`),
		ev(`{"type":"content_block_stop","index":5}`),
		ev(`{"type":"content_block_stop","index":2}`),
		ev(`{"type":"content_block_stop","index":7}`),
		ev(evMessageStop))
	m, events := reconstruct(data)
	var idx []int
	for _, b := range m.Content {
		idx = append(idx, b.Index)
	}
	if !reflect.DeepEqual(idx, []int{2, 5, 7}) {
		t.Errorf("indices = %v", idx)
	}
	if m.Content[2].Text != "órfão" || m.Content[2].Type != "" || events[3].ParseStatus != ParseUnexpected {
		t.Errorf("placeholder = %+v / %+v", m.Content[2], events[3])
	}
	if !m.Complete {
		t.Error("all blocks stopped and message_stop seen: should be complete")
	}
}

func TestReconstructExtraAndSnapshotIsolation(t *testing.T) {
	data := sse(`message_start|{"type":"message_start","message":{"id":"msg_01E","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":3,"output_tokens":1},"container":{"id":"cnt_01"}}}`,
		ev(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"x"}}`))
	r := NewReconstructor()
	for _, e := range parseChunks(0, data) {
		r.Add(e)
	}
	m := r.Message()
	if string(m.Extra["container"]) != `{"id":"cnt_01"}` {
		t.Errorf("extra = %v", m.Extra)
	}
	*m.Usage.InputTokens = 999
	m.UsageSnapshots[0].Usage.Raw[0] = 'X'
	m.EventCounts["message_start"] = 50
	m.Content[0].Raw[0] = 'X'
	m.Extra["container"][0] = 'X'
	again := r.Message()
	if *again.Usage.InputTokens != 3 || again.UsageSnapshots[0].Usage.Raw[0] != '{' ||
		again.EventCounts["message_start"] != 1 || again.Content[0].Raw[0] != '{' || again.Extra["container"][0] != '{' {
		t.Errorf("snapshot shares state: %+v", again)
	}
}

func TestReconstructNotesBounded(t *testing.T) {
	r := NewReconstructor()
	for i := range 1000 {
		r.Add(SSEEvent{Seq: i, Data: []byte("not json")})
	}
	m := r.Message()
	if len(m.Notes) != maxNotes+1 || !strings.HasPrefix(m.Notes[maxNotes], "800 more") {
		t.Errorf("notes = %d, last %q", len(m.Notes), m.Notes[len(m.Notes)-1])
	}
	if m.EventCounts["unknown"] != 1000 {
		t.Errorf("counts = %v", m.EventCounts)
	}
}

func TestReconstructRepeatedStartAndStartContent(t *testing.T) {
	data := sse(`message_start|{"type":"message_start","message":{"id":"msg_01S","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[{"type":"text","text":"pré"}],"usage":{"input_tokens":1}}}`,
		ev(`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"a"}}`),
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"b"}}`),
		ev(`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"x"}}`),
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"c"}}`),
		ev(`{"type":"content_block_stop","index":1}`),
		ev(`{"type":"content_block_stop","index":1}`),
		ev(evMessageStop))
	m, events := reconstruct(data)
	if len(m.Content) != 2 || m.Content[0].Text != "pré" || !m.Content[0].Stopped || m.Content[1].Text != "abc" {
		t.Errorf("content = %+v", m.Content)
	}
	if events[3].ParseStatus != ParseUnexpected || events[6].ParseStatus != ParseUnexpected || !m.Complete {
		t.Errorf("statuses %+v %+v complete %v", events[3], events[6], m.Complete)
	}
}
