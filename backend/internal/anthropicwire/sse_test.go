package anthropicwire

import (
	"bytes"
	"math"
	"math/rand/v2"
	"os"
	"reflect"
	"strings"
	"testing"
)

// readFixture loads a file of testdata
func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// parseChunks feeds chunks to a new parser and returns every event, the
// one returned by Finish last
func parseChunks(limit int, chunks ...[]byte) []SSEEvent {
	p := NewSSEParser(limit)
	var all []SSEEvent
	for _, c := range chunks {
		all = append(all, p.Feed(c)...)
	}
	if ev := p.Finish(); ev != nil {
		all = append(all, *ev)
	}
	return all
}

// splitRandom cuts data into chunks of 1..maxLen bytes
func splitRandom(data []byte, seed uint64, maxLen int) [][]byte {
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	var chunks [][]byte
	for len(data) > 0 {
		n := min(1+rng.IntN(maxLen), len(data))
		chunks = append(chunks, data[:n])
		data = data[n:]
	}
	return chunks
}

func sseStreams(t *testing.T) map[string][]byte {
	tool := readFixture(t, "stream_tool_use.sse")
	text := readFixture(t, "stream_text.sse")
	return map[string][]byte{
		"tool_use_lf":   tool,
		"text_lf":       text,
		"tool_use_crlf": bytes.ReplaceAll(tool, []byte("\n"), []byte("\r\n")),
		"text_cr":       bytes.ReplaceAll(text, []byte("\n"), []byte("\r")),
		"bom_mixed":     []byte("\xEF\xBB\xBFevent: a\r\ndata: ação 🚀\rdata: x\n\r\n: c\nid: 7\nretry: 100\ndata\n\nevent: só\n\n"),
		"cr_at_end":     []byte("data: x\rdata: y\r\r"),
		"unterminated":  []byte("event: message_start\ndata: {\"type\":\"mess"),
	}
}

func TestSSESplitEveryPosition(t *testing.T) {
	for name, data := range sseStreams(t) {
		for _, limit := range []int{0, 24} {
			whole := parseChunks(limit, data)
			if len(whole) == 0 {
				t.Fatalf("%s: no events", name)
			}
			for i := 0; i <= len(data); i++ {
				got := parseChunks(limit, data[:i], data[i:])
				if !reflect.DeepEqual(got, whole) {
					t.Fatalf("%s limit=%d split at %d:\n got %+v\nwant %+v", name, limit, i, got, whole)
				}
			}
			for seed := uint64(1); seed <= 20; seed++ {
				got := parseChunks(limit, splitRandom(data, seed, 7)...)
				if !reflect.DeepEqual(got, whole) {
					t.Fatalf("%s limit=%d seed %d: chunked parse differs", name, limit, seed)
				}
			}
		}
	}
}

func TestSSEByteAccounting(t *testing.T) {
	data := readFixture(t, "stream_tool_use.sse")
	events := parseChunks(0, data)
	if len(events) != 17 {
		t.Fatalf("got %d events, want 17", len(events))
	}
	var next int64
	for i, ev := range events {
		if ev.Seq != i {
			t.Errorf("event %d has Seq %d", i, ev.Seq)
		}
		if ev.ByteOffset != next {
			t.Errorf("event %d offset %d, want %d", i, ev.ByteOffset, next)
		}
		raw := data[ev.ByteOffset : ev.ByteOffset+ev.ByteLen]
		if !bytes.HasSuffix(raw, []byte("\n\n")) || !bytes.Contains(raw, ev.Data) {
			t.Errorf("event %d span %q does not hold its data", i, raw)
		}
		next = ev.ByteOffset + ev.ByteLen
	}
	if next != int64(len(data)) {
		t.Errorf("spans end at %d, stream has %d bytes", next, len(data))
	}
	if events[2].Event != "ping" || string(events[2].Data) != `{"type": "ping"}` {
		t.Errorf("ping event = %+v", events[2])
	}
}

func TestSSEFraming(t *testing.T) {
	ptr := func(n int64) *int64 { return &n }
	tests := []struct {
		name     string
		input    string
		max      int
		want     []SSEEvent // only Event, Data, ID, HasID, Retry, Truncated compared
		comments int
	}{
		{name: "two events in one chunk", input: "event: a\ndata: 1\n\nevent: b\ndata: 2\n\n",
			want: []SSEEvent{{Event: "a", Data: []byte("1")}, {Event: "b", Data: []byte("2")}}},
		{name: "multiple data lines", input: "data: um\ndata:dois\ndata:  três\n\n",
			want: []SSEEvent{{Data: []byte("um\ndois\n três")}}},
		{name: "empty data line emits empty data", input: "data:\n\n",
			want: []SSEEvent{{Data: []byte{}}}},
		{name: "field without colon", input: "data\ndata\n\n",
			want: []SSEEvent{{Data: []byte("\n")}}},
		{name: "event without data", input: "event: only\n\n",
			want: []SSEEvent{{Event: "only", Data: []byte{}}}},
		{name: "comments only dispatch nothing", input: ": one\n: two\n\n\n\n", comments: 2},
		{name: "retry alone dispatches nothing", input: "retry: 10\n\n"},
		{name: "id and retry", input: "id: 42\nretry: 3000\ndata: x\n\n",
			want: []SSEEvent{{Data: []byte("x"), ID: "42", HasID: true, Retry: ptr(3000)}}},
		{name: "invalid retry and NUL id ignored", input: "id: a\x00b\nretry: 30x\nretry:\ndata: x\n\n",
			want: []SSEEvent{{Data: []byte("x")}}},
		{name: "id alone is emitted", input: "id: 9\n\n",
			want: []SSEEvent{{Data: []byte{}, ID: "9", HasID: true}}},
		{name: "unknown field ignored", input: "foo: bar\ndata: x\n\n",
			want: []SSEEvent{{Data: []byte("x")}}},
		{name: "unknown event type kept", input: "event: future_event\ndata: {\"type\":\"future_event\"}\n\n",
			want: []SSEEvent{{Event: "future_event", Data: []byte(`{"type":"future_event"}`)}}},
		{name: "BOM stripped at start", input: "\xEF\xBB\xBFdata: x\n\n",
			want: []SSEEvent{{Data: []byte("x")}}},
		{name: "BOM not stripped later", input: "data: x\n\n\xEF\xBB\xBFdata: y\n\n",
			want: []SSEEvent{{Data: []byte("x")}}},
		{name: "CRLF", input: "event: a\r\ndata: 1\r\n\r\n",
			want: []SSEEvent{{Event: "a", Data: []byte("1")}}},
		{name: "CR", input: "event: a\rdata: 1\r\r",
			want: []SSEEvent{{Event: "a", Data: []byte("1")}}},
		{name: "truncation keeps later events", max: 10,
			input: "data: 0123456789ABCDEF\ndata: more\n\ndata: ok\n\n",
			want:  []SSEEvent{{Data: []byte("0123456789"), Truncated: true}, {Data: []byte("ok")}}},
		{name: "truncation by joined lines", max: 5, input: "data: abc\ndata: de\n\ndata: abcde\n\n",
			want: []SSEEvent{{Data: []byte("abc\nd"), Truncated: true}, {Data: []byte("abcde")}}},
		{name: "overlong event line", max: 4, input: "event: " + strings.Repeat("e", 200) + "\ndata: ok\n\n",
			want: []SSEEvent{{Event: strings.Repeat("e", 4+sseLineSlack-len("event: ")), Data: []byte("ok"), Truncated: true}}},
		{name: "overlong comment does not truncate", max: 4, input: ":" + strings.Repeat("c", 500) + "\ndata: ok\n\n",
			want: []SSEEvent{{Data: []byte("ok")}}, comments: 1},
		{name: "unterminated event returned by Finish", input: "event: x\ndata: partial",
			want: []SSEEvent{{Event: "x", Data: []byte("partial")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewSSEParser(tt.max)
			got := p.Feed([]byte(tt.input))
			if ev := p.Finish(); ev != nil {
				got = append(got, *ev)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d events %+v, want %d", len(got), got, len(tt.want))
			}
			for i, ev := range got {
				w := tt.want[i]
				if ev.Event != w.Event || !bytes.Equal(ev.Data, w.Data) || ev.ID != w.ID || ev.HasID != w.HasID ||
					!reflect.DeepEqual(ev.Retry, w.Retry) || ev.Truncated != w.Truncated || ev.Data == nil {
					t.Errorf("event %d = %+v (data %q), want %+v (data %q)", i, ev, ev.Data, w, w.Data)
				}
			}
			if p.Comments() != tt.comments {
				t.Errorf("comments = %d, want %d", p.Comments(), tt.comments)
			}
			if p.Offset() != int64(len(tt.input)) {
				t.Errorf("offset = %d, want %d", p.Offset(), len(tt.input))
			}
		})
	}
}

func TestSSEEventAcrossManyChunks(t *testing.T) {
	payload := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` +
		strings.Repeat("ação 🚀 ", 40) + `"}}`
	data := []byte("event: content_block_delta\ndata: " + payload + "\n\n")
	p := NewSSEParser(0)
	var got []SSEEvent
	chunks := 0
	for len(data) > 0 {
		n := min(3, len(data))
		got = append(got, p.Feed(data[:n])...)
		data = data[n:]
		chunks++
	}
	if chunks < 24 || len(got) != 1 || string(got[0].Data) != payload {
		t.Fatalf("after %d chunks got %d events", chunks, len(got))
	}
	if p.Finish() != nil {
		t.Error("Finish returned an event after a complete stream")
	}
}

func TestSSECRAtChunkBoundary(t *testing.T) {
	p := NewSSEParser(0)
	if evs := p.Feed([]byte("data: a\r")); len(evs) != 0 {
		t.Fatalf("events before the blank line: %+v", evs)
	}
	if evs := p.Feed([]byte("\ndata: b\r\n\r")); len(evs) != 0 {
		t.Fatalf("event emitted before knowing the byte after \\r: %+v", evs)
	}
	evs := p.Feed([]byte("\n"))
	if len(evs) != 1 || string(evs[0].Data) != "a\nb" || evs[0].ByteLen != 20 {
		t.Fatalf("got %+v", evs)
	}
	if p.Finish() != nil {
		t.Error("spurious event from the \\r\\n terminator")
	}
}

func TestSSESpanResetAndHugeLimit(t *testing.T) {
	evs := parseChunks(0, []byte(": ping\n\nretry: 5\n\ndata: x\n\n"))
	if len(evs) != 1 || evs[0].ByteOffset != 18 || evs[0].ByteLen != 9 {
		t.Errorf("blank lines must reset the span: %+v", evs)
	}
	evs = parseChunks(math.MaxInt, []byte("data: "+strings.Repeat("y", 300)+"\n\n"))
	if len(evs) != 1 || len(evs[0].Data) != 300 || evs[0].Truncated {
		t.Errorf("unbounded limit: %+v", evs)
	}
}

func TestSSEFinish(t *testing.T) {
	p := NewSSEParser(0)
	p.Feed([]byte("event: message_delta\ndata: {\"type\":\"message_delta\"\ndata: ,\"x\":1}"))
	ev := p.Finish()
	if ev == nil || ev.Seq != 0 || ev.Event != "message_delta" || string(ev.Data) != "{\"type\":\"message_delta\"\n,\"x\":1}" {
		t.Fatalf("Finish = %+v", ev)
	}
	if ev.ByteOffset != 0 || ev.ByteLen != p.Offset() {
		t.Errorf("span %d+%d, want 0+%d", ev.ByteOffset, ev.ByteLen, p.Offset())
	}
	if p.Finish() != nil {
		t.Error("second Finish returned an event")
	}
	q := NewSSEParser(0)
	q.Feed([]byte(": only a comment"))
	if q.Finish() != nil || q.Comments() != 1 {
		t.Error("comment-only stream produced an event")
	}
}
