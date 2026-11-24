package httpturns

import (
	"fmt"
	"testing"
	"time"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
)

var t0 = time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)

// builder makes exchanges one second apart, in order
type builder struct{ ex []Exchange }

func (b *builder) add(kind anthropicwire.ExchangeKind, r Request) *Exchange {
	n := len(b.ex)
	b.ex = append(b.ex, Exchange{
		HTTPExchange: api.HTTPExchange{ID: fmt.Sprintf("e%02d", n), Kind: string(kind), StartedAt: t0.Add(time.Duration(n) * time.Second), Status: 200},
		Request:      r,
	})
	return &b.ex[n]
}

// request is a request with tools; lastUser and toolResults as Claude
// Code sends them (a system role message after the first prompt)
func (b *builder) request(system, first string, count, lastUser int, hash, prompt string, toolResults ...int) *Exchange {
	return b.add(anthropicwire.KindModelInference, Request{
		Known: true, Tools: 12, SystemSHA: system, FirstMessageHash: first, MessageCount: count, LastUserIndex: lastUser,
		ToolResultIndexes: toolResults, LastMessageHash: hash, Prompt: prompt,
	})
}

func (b *builder) aux(kind anthropicwire.ExchangeKind) *Exchange { return b.add(kind, Request{}) }

func ids(ex []api.HTTPExchange) []string {
	out := make([]string, len(ex))
	for i, e := range ex {
		out[i] = e.ID
	}
	return out
}

func check(t *testing.T, b *builder, wantPreamble []string, want ...[]string) []Turn {
	t.Helper()
	preamble, turns := Group(b.ex)
	if fmt.Sprint(ids(preamble)) != fmt.Sprint(wantPreamble) {
		t.Fatalf("preamble %v, want %v", ids(preamble), wantPreamble)
	}
	if len(turns) != len(want) {
		got := make([][]string, len(turns))
		for i, turn := range turns {
			got[i] = ids(turn.Exchanges)
		}
		t.Fatalf("turns %v, want %v", got, want)
	}
	for i, turn := range turns {
		if fmt.Sprint(ids(turn.Exchanges)) != fmt.Sprint(want[i]) {
			t.Fatalf("turn %d: %v, want %v", i+1, ids(turn.Exchanges), want[i])
		}
	}
	return turns
}

// Claude Code 2.1.331 against the simulated upstream: a prompt, its tool
// loop, then a second prompt (--continue) whose history holds the earlier
// tool result
func TestGroupPromptsAndToolLoop(t *testing.T) {
	b := &builder{}
	b.aux(anthropicwire.KindOtherAPI)                            // e00 HEAD /api/hello
	b.request("sysA", "fA", 2, 0, "h-env", "Revise src/auth.ts") // e01 prompt (role system message last)
	b.request("sysA", "fA", 4, 3, "h-tr1", "", 3)                // e02 tool loop
	b.aux(anthropicwire.KindOtherAPI)                            // e03 HEAD /api/hello
	b.request("sysA", "fA", 6, 5, "h-p2", "Agora resuma", 3)     // e04 second prompt
	turns := check(t, b, []string{"e00"}, []string{"e01", "e02", "e03"}, []string{"e04"})
	if turns[0].Prompt != "Revise src/auth.ts" || turns[1].Prompt != "Agora resuma" {
		t.Fatalf("prompts %q %q", turns[0].Prompt, turns[1].Prompt)
	}
}

// Startup calls (quota probe, a title helper sent without tools) are in
// the preamble or the turn in progress, never a turn of their own
func TestGroupHelpersAndProbe(t *testing.T) {
	helper := Request{Known: true, MessageCount: 1, LastUserIndex: 0, FirstMessageHash: "fH"}
	b := &builder{}
	b.aux(anthropicwire.KindQuotaProbe)             // e00
	b.add(anthropicwire.KindModelInference, helper) // e01 helper, no tools
	b.request("sysA", "fA", 2, 0, "h1", "oi")       // e02
	b.aux(anthropicwire.KindTokenCount)             // e03
	b.add(anthropicwire.KindModelInference, helper) // e04 helper
	b.aux(anthropicwire.KindModelInference)         // e05 in progress: no summary yet
	check(t, b, []string{"e00", "e01"}, []string{"e02", "e03", "e04", "e05"})
}

// Subagents (their own system prompt and first message) start no turn,
// also two of the same type and one that outgrows the main conversation
func TestGroupSubagents(t *testing.T) {
	b := &builder{}
	b.request("sysA", "fA", 2, 0, "m1", "investigue")     // e00 turn 1
	b.request("sysA", "fA", 4, 3, "m2", "", 3)            // e01 main asks for two subagents
	b.request("sysSub", "fS1", 2, 0, "s1", "tarefa 1")    // e02 first subagent
	b.request("sysSub", "fS2", 2, 0, "t1", "tarefa 2")    // e03 second subagent, same type
	b.request("sysSub", "fS1", 6, 5, "s3", "", 3, 5)      // e04
	b.request("sysSub", "fS2", 4, 3, "t2", "", 3)         // e05
	b.request("sysA", "fA", 6, 5, "m3", "", 3, 5)         // e06 main gets the results
	b.request("sysA", "fA", 8, 7, "m4", "e agora?", 3, 5) // e07 turn 2
	check(t, b, nil, []string{"e00", "e01", "e02", "e03", "e04", "e05", "e06"}, []string{"e07"})
}

// A retried request is not a new turn
func TestGroupRetries(t *testing.T) {
	b := &builder{}
	b.request("sysA", "fA", 2, 0, "p1", "oi") // e00
	b.request("sysA", "fA", 2, 0, "p1", "oi") // e01 same messages (the first failed)
	r := b.request("sysA", "fA", 2, 0, "p1x", "oi")
	r.RetryCount = 1                               // e02 the SDK's retry header
	b.request("sysA", "fA", 4, 3, "p2", "de novo") // e03 a new prompt
	check(t, b, nil, []string{"e00", "e01", "e02"}, []string{"e03"})
}

// Captures of earlier versions have no lastUserIndex: the last message
func TestGroupWithoutLastUserIndex(t *testing.T) {
	b := &builder{}
	b.request("sysA", "fA", 2, -1, "a", "")    // e00 last message has no tool_result
	b.request("sysA", "fA", 4, -1, "b", "", 3) // e01 tool loop
	b.request("sysA", "fA", 6, -1, "c", "", 3) // e02 old result only: a new prompt
	check(t, b, nil, []string{"e00", "e01"}, []string{"e02"})
}

// The conversation is kept when its system prompt changes (model switch:
// same first message) or its first message does (compaction: same system)
func TestGroupSystemPromptAndCompaction(t *testing.T) {
	b := &builder{}
	b.request("sysA", "fA", 2, 0, "a", "um")        // e00
	b.request("sysB", "fA", 4, 3, "b", "dois")      // e01 model switch
	b.request("sysB", "fA", 6, 5, "c", "", 5)       // e02
	b.request("sysB", "fC", 3, 2, "d", "três")      // e03 after a compaction
	b.request("sysX", "fX", 2, 0, "e", "subtarefa") // e04 neither: a subagent
	check(t, b, nil, []string{"e00"}, []string{"e01", "e02"}, []string{"e03", "e04"})
}

func TestGroupNoTurns(t *testing.T) {
	b := &builder{}
	b.aux(anthropicwire.KindOtherAPI)
	preamble, turns := Group(b.ex)
	if len(preamble) != 1 || turns != nil {
		t.Fatalf("%v %v", preamble, turns)
	}
	if p, turns := Group(nil); p != nil || turns != nil {
		t.Fatal("empty")
	}
}

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }

// Summaries follow the traffic summary's definitions
func TestSummarize(t *testing.T) {
	final := &api.HTTPUsage{
		Usage:   anthropicwire.Usage{InputTokens: i64(10), OutputTokens: i64(5), CacheReadInputTokens: i64(100), CacheCreationInputTokens: i64(20), CacheCreation1h: i64(20)},
		Totals:  anthropicwire.UsageTotals{TotalInput: i64(130), TotalProcessed: i64(135)},
		Quality: api.UsageFinal, CostUSD: f64(0.5), CostStatus: api.CostComplete,
	}
	partial := &api.HTTPUsage{
		Usage:   anthropicwire.Usage{InputTokens: i64(3), OutputTokens: i64(1)},
		Totals:  anthropicwire.UsageTotals{TotalInput: i64(3), TotalProcessed: i64(4)},
		Quality: api.UsagePartial, CostStatus: api.CostPartial,
	}
	d := int64(2500)
	ex := []api.HTTPExchange{
		{Kind: "model_inference", StartedAt: t0, DurationMs: &d, Usage: final, ResponseModel: "claude-opus-5-5", ToolUses: []string{"Read", "Grep"}},
		{Kind: "quota_probe", StartedAt: t0.Add(time.Second), Status: 429, ErrorType: "rate_limit_error"},
		{Kind: "model_inference", StartedAt: t0.Add(2 * time.Second), Status: 500, ErrorType: "api_error", RequestedModel: "claude-haiku-5-5"},
		{Kind: "model_inference", StartedAt: t0.Add(3 * time.Second), Usage: partial, ToolUses: []string{"Read"}},
		{Kind: "token_count", StartedAt: t0.Add(4 * time.Second), Usage: &api.HTTPUsage{Quality: api.UsageUnavailable}},
	}
	s := Summarize(3, "oi", ex)
	if s.Index != 3 || s.Prompt != "oi" || s.Inference != 3 || s.Auxiliary != 2 || s.Errors != 1 ||
		fmt.Sprint(s.ToolUses) != "[Read Grep Read]" || fmt.Sprint(s.Models) != "[claude-haiku-5-5 claude-opus-5-5]" {
		t.Fatalf("counts %+v", s)
	}
	if s.Tokens.Exchanges != 1 || s.Tokens.Output != 5 || s.Tokens.CacheRead != 100 || s.Tokens.CacheWrite1h != 20 || s.Tokens.TotalInput != 130 ||
		s.Tokens.CacheReadShare == nil || s.PartialTokens.Exchanges != 1 || s.PartialTokens.TotalProcessed != 4 {
		t.Fatalf("tokens %+v / %+v", s.Tokens, s.PartialTokens)
	}
	if s.CostUSD != 0.5 || s.CostStatus != api.CostPartial {
		t.Fatalf("cost %v %s", s.CostUSD, s.CostStatus)
	}
	if !s.StartedAt.Equal(t0) || !s.EndedAt.Equal(t0.Add(4*time.Second)) || s.DurationMs != 4000 {
		t.Fatalf("times %v %v %d", s.StartedAt, s.EndedAt, s.DurationMs)
	}
	empty := Summarize(0, "", nil)
	if empty.Exchanges == nil || empty.CostStatus != api.CostUnavailable || empty.DurationMs != 0 {
		t.Fatalf("empty %+v", empty)
	}
}
