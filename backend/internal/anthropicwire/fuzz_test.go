package anthropicwire

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"reflect"
	"testing"
)

// fuzzSeeds returns the testdata fixtures plus small edge cases
func fuzzSeeds(t testing.TB) [][]byte {
	seeds := [][]byte{
		nil, []byte("{}"), []byte("[]"), []byte("data:\n\n"), []byte("\r\r\n\n"), []byte("\xEF\xBB\xBF:\n"),
		[]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`),
		[]byte(`{"messages":[{"content":[{"type":"tool_result","content":[{"type":"text","text":"ç"}]}]}]}`),
	}
	for _, name := range []string{"stream_tool_use.sse", "stream_text.sse", "message.json", "error_529.json", "request_tools.json"} {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		seeds = append(seeds, b)
	}
	return seeds
}

// checkStream parses data whole and split at cut, compares both and runs
// the events through a Reconstructor
func checkStream(t *testing.T, data []byte, cut int, limit int) {
	cut = min(max(cut, 0), len(data))
	whole := parseChunks(limit, data)
	split := parseChunks(limit, data[:cut], data[cut:])
	if !reflect.DeepEqual(whole, split) {
		t.Fatalf("split at %d differs:\n%+v\n%+v", cut, whole, split)
	}
	r := NewReconstructor()
	for i, ev := range whole {
		if ev.Seq != i || ev.ByteOffset < 0 || ev.ByteLen <= 0 || ev.ByteOffset+ev.ByteLen > int64(len(data)) {
			t.Fatalf("event %d framing %+v", i, ev)
		}
		if limit > 0 && len(ev.Data) > limit {
			t.Fatalf("event %d data %d bytes over limit %d", i, len(ev.Data), limit)
		}
		r.Add(ev)
	}
	m := r.Message()
	ToolUses(m)
	Describe(DescribeInput{Kind: KindModelInference, Response: m, State: StatePartial})
	Headline(DescribeInput{Kind: KindModelInference, Response: m})
}

func FuzzSSEParser(f *testing.F) {
	for _, s := range fuzzSeeds(f) {
		f.Add(s, len(s)/2)
	}
	f.Fuzz(func(t *testing.T, data []byte, cut int) {
		checkStream(t, data, cut, 0)
		checkStream(t, data, cut, 16)
	})
}

func FuzzParseRequest(f *testing.F) {
	for _, s := range fuzzSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		orig := string(body)
		s := ParseRequest(body)
		if string(body) != orig {
			t.Fatal("input modified")
		}
		if s.Valid != (s.ParseError == "") || s.Messages.Count != len(s.Messages.Hashes) || len(s.Messages.Sizes) != s.Messages.Count ||
			s.ToolResults == nil || s.ToolUses == nil || s.CacheBreakpoints == nil || s.UnknownFields == nil {
			t.Fatalf("inconsistent summary %+v", s)
		}
		for _, r := range s.ToolResults {
			if len(r.Preview) > MaxPreviewBytes {
				t.Fatalf("preview of %d bytes", len(r.Preview))
			}
		}
		d := DiffRequests(&s, &s)
		if d.CommonPrefix != s.Messages.Count || d.CacheChanged || len(d.NewToolResults) != 0 {
			t.Fatalf("self diff %+v", d)
		}
		Describe(DescribeInput{Kind: KindModelInference, Request: &s})
	})
}

func FuzzParseMessageJSON(f *testing.F) {
	for _, s := range fuzzSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		m, err := ParseMessageJSON(body)
		if (m == nil) == (err == nil) {
			t.Fatalf("message %v with error %v", m, err)
		}
		ParseAPIError(body)
		ParseUsage(body).Totals()
		_, _ = ParseCountTokens(body)
		if m != nil {
			Describe(DescribeInput{Kind: KindModelInference, Response: m, Status: 200})
			Headline(DescribeInput{Kind: KindModelInference, Response: m})
		}
	})
}

func FuzzDecodeBody(f *testing.F) {
	f.Add(encode(f, "gzip", decodeSample), "gzip")
	f.Add(encode(f, "zlib", decodeSample), "deflate")
	f.Add(encode(f, "br", decodeSample), "br")
	f.Add(encode(f, "zstd", decodeSample), "zstd")
	f.Add([]byte("plain"), "identity")
	f.Fuzz(func(t *testing.T, body []byte, header string) {
		orig := string(body)
		out, status, err := DecodeBody(body, header, 4096)
		if string(body) != orig {
			t.Fatal("input modified")
		}
		if (err != nil) != (status == DecodeUnsupported || status == DecodeError) || (err != nil && out != nil) {
			t.Fatalf("status %s with error %v", status, err)
		}
		if status != DecodeIdentity && len(out) > 4096 {
			t.Fatalf("%d bytes over the limit", len(out))
		}
	})
}

func FuzzJSONWalk(f *testing.F) {
	for _, s := range fuzzSeeds(f) {
		f.Add(s)
	}
	f.Add([]byte(`{"a\\":"\\\"","b":[{"c":"}]"}]}`))
	f.Add([]byte(` "0"`))
	f.Fuzz(func(t *testing.T, data []byte) {
		checkWalk(t, data)
		if json.Valid(data) {
			var s string
			if json.Unmarshal(data, &s) == nil && stringPrefix(data, 1<<20) != s {
				t.Fatalf("stringPrefix(%q) differs from encoding/json", data)
			}
		}
	})
}

// TestRandomInputRobustness feeds random and mutated bytes to every parser
func TestRandomInputRobustness(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	seeds := fuzzSeeds(t)
	headers := []string{"gzip", "deflate", "br", "zstd", "gzip, br", "identity"}
	for i := range 300 {
		var data []byte
		if i%2 == 0 {
			data = make([]byte, rng.IntN(2048))
			for j := range data {
				data[j] = byte(rng.UintN(256))
			}
		} else {
			data = append([]byte(nil), seeds[rng.IntN(len(seeds))]...)
			for range 1 + rng.IntN(8) {
				if len(data) > 0 {
					data[rng.IntN(len(data))] = byte(rng.UintN(256))
				}
			}
		}
		checkStream(t, data, rng.IntN(len(data)+1), 64)
		ParseRequest(data)
		_, _ = ParseMessageJSON(data)
		ParseUsage(data).Totals()
		_, _, _ = DecodeBody(data, headers[rng.IntN(len(headers))], 1<<16)
		checkWalk(t, data)
	}
}
