package anthropicwire

import (
	"encoding/json"
	"math"
	"testing"
)

func TestParseUsageZeroVersusAbsent(t *testing.T) {
	fields := []struct {
		key string
		get func(Usage) *int64
	}{
		{"input_tokens", func(u Usage) *int64 { return u.InputTokens }},
		{"output_tokens", func(u Usage) *int64 { return u.OutputTokens }},
		{"cache_read_input_tokens", func(u Usage) *int64 { return u.CacheReadInputTokens }},
		{"cache_creation_input_tokens", func(u Usage) *int64 { return u.CacheCreationInputTokens }},
		{"ephemeral_5m_input_tokens", func(u Usage) *int64 { return u.CacheCreation5m }},
		{"ephemeral_1h_input_tokens", func(u Usage) *int64 { return u.CacheCreation1h }},
	}
	wrap := func(key, value string) string {
		if key == "ephemeral_5m_input_tokens" || key == "ephemeral_1h_input_tokens" {
			return `{"cache_creation":{"` + key + `":` + value + `}}`
		}
		return `{"` + key + `":` + value + `}`
	}
	for _, f := range fields {
		for _, tt := range []struct {
			value string
			want  *int64
		}{
			{"0", i64(0)}, {"17", i64(17)}, {"null", nil}, {`"5"`, nil}, {"1.5", nil}, {"true", nil},
			{"99999999999999999999", nil},
		} {
			raw := wrap(f.key, tt.value)
			got := f.get(ParseUsage(json.RawMessage(raw)))
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("%s: got %v, want %v", raw, got, tt.want)
			}
		}
		if got := f.get(ParseUsage(json.RawMessage(`{}`))); got != nil {
			t.Errorf("%s absent: got %d", f.key, *got)
		}
	}
	if u := ParseUsage(json.RawMessage(`{"cache_creation":null}`)); u.CacheCreation5m != nil || u.CacheCreation1h != nil {
		t.Error("null cache_creation yields values")
	}
}

func TestParseUsageRawAndNonObject(t *testing.T) {
	raw := json.RawMessage(`{"input_tokens":1,"service_tier":"priority","server_tool_use":{"web_search_requests":2}}`)
	u := ParseUsage(raw)
	if u.ServiceTier != "priority" || string(u.Raw) != string(raw) {
		t.Errorf("usage = %+v", u)
	}
	raw[2] = 'X'
	if u.Raw[2] == 'X' {
		t.Error("Raw aliases the input")
	}
	for _, in := range []string{`[1,2]`, `"x"`, `null`, `{bad`} {
		u := ParseUsage(json.RawMessage(in))
		if u.InputTokens != nil || u.OutputTokens != nil || string(u.Raw) != in {
			t.Errorf("%s: %+v", in, u)
		}
	}
	if u := ParseUsage(nil); u.Raw != nil {
		t.Errorf("nil raw: %+v", u)
	}
}

func TestUsageMerge(t *testing.T) {
	start := ParseUsage(json.RawMessage(`{"input_tokens":100,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":300},"output_tokens":1,"service_tier":"standard"}`))
	final := ParseUsage(json.RawMessage(`{"output_tokens":40}`))
	m := start.Merge(final)
	if *m.InputTokens != 100 || *m.CacheReadInputTokens != 200 || *m.CacheCreationInputTokens != 300 ||
		*m.CacheCreation5m != 0 || *m.CacheCreation1h != 300 || *m.OutputTokens != 40 || m.ServiceTier != "standard" {
		t.Errorf("merged = %+v", m)
	}
	if string(m.Raw) != `{"output_tokens":40}` {
		t.Errorf("raw = %s", m.Raw)
	}
	// newer values replace, never add
	again := m.Merge(ParseUsage(json.RawMessage(`{"input_tokens":100,"output_tokens":41,"cache_creation":{"ephemeral_5m_input_tokens":5}}`)))
	if *again.InputTokens != 100 || *again.OutputTokens != 41 || *again.CacheCreation5m != 5 || *again.CacheCreation1h != 300 {
		t.Errorf("merged again = %+v", again)
	}
	*again.InputTokens = 1
	if *m.InputTokens != 100 || *start.InputTokens != 100 {
		t.Error("Merge shares pointers with its inputs")
	}
	if kept := m.Merge(Usage{}); string(kept.Raw) != `{"output_tokens":40}` {
		t.Errorf("empty next replaced Raw: %s", kept.Raw)
	}
}

func TestUsageTotals(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		name  string
		usage string
		want  UsageTotals
	}{
		{"documented example", `{"input_tokens":100,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"output_tokens":40}`,
			UsageTotals{TotalInput: i64(600), TotalProcessed: i64(640), CacheReadShare: f(200.0 / 600), CacheWriteUnknown: i64(300), Complete: true}},
		{"split not added again", `{"input_tokens":100,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":200},"output_tokens":40}`,
			UsageTotals{TotalInput: i64(600), TotalProcessed: i64(640), CacheReadShare: f(200.0 / 600), Complete: true}},
		{"split below total", `{"input_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":50,"ephemeral_1h_input_tokens":200},"output_tokens":0}`,
			UsageTotals{TotalInput: i64(301), TotalProcessed: i64(301), CacheReadShare: f(0), CacheWriteUnknown: i64(50), Complete: true}},
		{"split above total", `{"input_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":80,"ephemeral_1h_input_tokens":80}}`,
			UsageTotals{TotalInput: i64(101), CacheReadShare: f(0), Complete: true}},
		{"one part of the split", `{"input_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":60}}`,
			UsageTotals{TotalInput: i64(101), CacheReadShare: f(0), Complete: true}},
		{"no creation written", `{"input_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":2}`,
			UsageTotals{TotalInput: i64(5), TotalProcessed: i64(7), CacheReadShare: f(0), Complete: true}},
		{"cache fields absent", `{"input_tokens":5,"output_tokens":2}`,
			UsageTotals{TotalInput: i64(5), TotalProcessed: i64(7)}},
		{"input absent", `{"cache_read_input_tokens":10,"output_tokens":2}`, UsageTotals{}},
		{"output absent", `{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`,
			UsageTotals{TotalInput: i64(0), Complete: true}},
		{"negative", `{"input_tokens":-10,"output_tokens":2}`, UsageTotals{}},
		{"overflow", `{"input_tokens":9223372036854775807,"cache_read_input_tokens":1,"output_tokens":1}`, UsageTotals{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseUsage(json.RawMessage(tt.usage)).Totals()
			if !eqPtr(got.TotalInput, tt.want.TotalInput) || !eqPtr(got.TotalProcessed, tt.want.TotalProcessed) ||
				!eqPtr(got.CacheWriteUnknown, tt.want.CacheWriteUnknown) || got.Complete != tt.want.Complete ||
				!eqFloat(got.CacheReadShare, tt.want.CacheReadShare) {
				t.Errorf("got %s, want %s", fmtTotals(got), fmtTotals(tt.want))
			}
		})
	}
}

func eqPtr(a, b *int64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func eqFloat(a, b *float64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && math.Abs(*a-*b) < 1e-12)
}

func fmtTotals(t UsageTotals) string {
	b, _ := json.Marshal(t)
	return string(b)
}

func TestParseCountTokens(t *testing.T) {
	tests := []struct {
		body    string
		want    *int64
		wantErr bool
	}{
		{`{"input_tokens":1234}`, i64(1234), false},
		{`{"input_tokens":0}`, i64(0), false},
		{`{"other":1}`, nil, false},
		{`{"input_tokens":null}`, nil, false},
		{`{"input_tokens":"12"}`, nil, true},
		{`{"input_tokens":`, nil, true},
		{`[]`, nil, true},
		{``, nil, true},
	}
	for _, tt := range tests {
		got, err := ParseCountTokens([]byte(tt.body))
		if (err != nil) != tt.wantErr || !eqPtr(got, tt.want) {
			t.Errorf("%s: got %v, %v", tt.body, got, err)
		}
	}
}
