package anthropicwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// wireUsage is a usage object with every value kept raw, so that a value of
// the wrong type reads as absent instead of 0
type wireUsage struct {
	InputTokens              json.RawMessage `json:"input_tokens"`
	OutputTokens             json.RawMessage `json:"output_tokens"`
	CacheReadInputTokens     json.RawMessage `json:"cache_read_input_tokens"`
	CacheCreationInputTokens json.RawMessage `json:"cache_creation_input_tokens"`
	CacheCreation            json.RawMessage `json:"cache_creation"`
	ServiceTier              json.RawMessage `json:"service_tier"`
}

// wireCacheCreation is usage.cache_creation
type wireCacheCreation struct {
	Ephemeral5m json.RawMessage `json:"ephemeral_5m_input_tokens"`
	Ephemeral1h json.RawMessage `json:"ephemeral_1h_input_tokens"`
}

// ParseUsage reads a usage object. A field that is missing, null or not an
// integer is nil; an explicit 0 is a pointer to 0. Raw keeps a copy of raw.
// A value that is not a JSON object yields a Usage with only Raw set.
func ParseUsage(raw json.RawMessage) Usage {
	u := Usage{}
	if len(raw) > 0 {
		u.Raw = bytes.Clone(raw)
	}
	var w wireUsage
	if !isJSONObject(raw) || json.Unmarshal(raw, &w) != nil {
		return u
	}
	u.InputTokens = rawInt(w.InputTokens)
	u.OutputTokens = rawInt(w.OutputTokens)
	u.CacheReadInputTokens = rawInt(w.CacheReadInputTokens)
	u.CacheCreationInputTokens = rawInt(w.CacheCreationInputTokens)
	if isJSONObject(w.CacheCreation) {
		var cc wireCacheCreation
		if json.Unmarshal(w.CacheCreation, &cc) == nil {
			u.CacheCreation5m = rawInt(cc.Ephemeral5m)
			u.CacheCreation1h = rawInt(cc.Ephemeral1h)
		}
	}
	u.ServiceTier = rawString(w.ServiceTier)
	return u
}

// Merge returns the usage after next, a later snapshot of the same reply:
// fields present in next replace those of u, absent ones keep u's values
// (the API reports cumulative counts, they are never added). Raw is next.Raw
// when not empty.
func (u Usage) Merge(next Usage) Usage {
	out := u.clone()
	pick := func(dst **int64, v *int64) {
		if v != nil {
			*dst = clonePtr(v)
		}
	}
	pick(&out.InputTokens, next.InputTokens)
	pick(&out.OutputTokens, next.OutputTokens)
	pick(&out.CacheReadInputTokens, next.CacheReadInputTokens)
	pick(&out.CacheCreationInputTokens, next.CacheCreationInputTokens)
	pick(&out.CacheCreation5m, next.CacheCreation5m)
	pick(&out.CacheCreation1h, next.CacheCreation1h)
	if next.ServiceTier != "" {
		out.ServiceTier = next.ServiceTier
	}
	if len(next.Raw) > 0 {
		out.Raw = bytes.Clone(next.Raw)
	}
	return out
}

// Totals derives the totals documented on UsageTotals. The 5m/1h parts are
// never added to the input again. CacheWriteUnknown is
// cache_creation_input_tokens minus both parts when all three are known, or
// all of cache_creation_input_tokens when the API reported no split at all
// (the duration of those writes was not reported); it is nil when only one
// part is known, when the parts account for everything, and when the result
// is not above 0. A result that is negative or overflows is nil.
func (u Usage) Totals() UsageTotals {
	t := UsageTotals{
		Complete: u.InputTokens != nil && u.CacheReadInputTokens != nil && u.CacheCreationInputTokens != nil,
	}
	if u.InputTokens != nil {
		total, ok := *u.InputTokens, true
		for _, part := range []*int64{u.CacheReadInputTokens, u.CacheCreationInputTokens} {
			if part != nil && ok {
				total, ok = addInt64(total, *part)
			}
		}
		if ok && total >= 0 {
			t.TotalInput = &total
		}
	}
	if t.TotalInput != nil && u.OutputTokens != nil {
		if sum, ok := addInt64(*t.TotalInput, *u.OutputTokens); ok && sum >= 0 {
			t.TotalProcessed = &sum
		}
	}
	if t.TotalInput != nil && *t.TotalInput > 0 && u.CacheReadInputTokens != nil && *u.CacheReadInputTokens >= 0 {
		share := float64(*u.CacheReadInputTokens) / float64(*t.TotalInput)
		t.CacheReadShare = &share
	}
	t.CacheWriteUnknown = u.cacheWriteUnknown()
	return t
}

// cacheWriteUnknown implements the CacheWriteUnknown rule of Totals
func (u Usage) cacheWriteUnknown() *int64 {
	if u.CacheCreationInputTokens == nil {
		return nil
	}
	unknown := *u.CacheCreationInputTokens
	switch {
	case u.CacheCreation5m == nil && u.CacheCreation1h == nil:
	case u.CacheCreation5m != nil && u.CacheCreation1h != nil:
		split, ok := addInt64(*u.CacheCreation5m, *u.CacheCreation1h)
		if !ok || split < 0 {
			return nil
		}
		unknown -= split
	default:
		return nil
	}
	if unknown <= 0 {
		return nil
	}
	return &unknown
}

// ParseCountTokens reads the reply of POST /v1/messages/count_tokens,
// {"input_tokens": N}. It returns nil without error when input_tokens is
// missing or null, and an error when the body is not a JSON object or the
// value is not an integer.
func ParseCountTokens(body []byte) (*int64, error) {
	if !json.Valid(body) {
		return nil, errors.New("count_tokens: invalid JSON")
	}
	if !isJSONObject(body) {
		return nil, errors.New("count_tokens: body is not a JSON object")
	}
	var w struct {
		InputTokens json.RawMessage `json:"input_tokens"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, err
	}
	if isJSONNull(w.InputTokens) {
		return nil, nil
	}
	n := rawInt(w.InputTokens)
	if n == nil {
		return nil, errors.New("count_tokens: input_tokens is not an integer")
	}
	return n, nil
}

// clone returns u with its own copies of every pointer and of Raw
func (u Usage) clone() Usage {
	return Usage{
		InputTokens:              clonePtr(u.InputTokens),
		OutputTokens:             clonePtr(u.OutputTokens),
		CacheReadInputTokens:     clonePtr(u.CacheReadInputTokens),
		CacheCreationInputTokens: clonePtr(u.CacheCreationInputTokens),
		CacheCreation5m:          clonePtr(u.CacheCreation5m),
		CacheCreation1h:          clonePtr(u.CacheCreation1h),
		ServiceTier:              u.ServiceTier,
		Raw:                      bytes.Clone(u.Raw),
	}
}

// clonePtr returns a new pointer to *p, or nil
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// addInt64 adds a and b, reporting false on overflow
func addInt64(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, false
	}
	return a + b, true
}

// rawInt reads a raw JSON value as an integer; anything else (missing,
// null, fraction, string, out of range) is nil
func rawInt(raw json.RawMessage) *int64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return nil
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// rawFloat reads a raw JSON number as a float; anything else is nil
func rawFloat(raw json.RawMessage) *float64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return nil
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return nil
	}
	return &f
}

// rawString decodes a raw JSON string; anything else is ""
func rawString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '"' {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// isJSONObject tells whether raw starts like a JSON object
func isJSONObject(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}

// isJSONNull tells whether raw is absent or the JSON null literal
func isJSONNull(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}
