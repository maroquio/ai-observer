// Package httpturns groups the exchanges the HTTP capture proxy recorded
// for a session into turns: a prompt the user typed and everything that
// followed until the next one (the model's requests of its tool loop,
// subagents, count_tokens, quota probes and other auxiliary calls).
//
// The Messages API has no turn ID, so turns are deduced from the requests
// (Group). The package is pure: it reads only the exchanges it is given.
package httpturns

import (
	"sort"
	"time"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
)

// Note explains how turns are deduced (shown with them)
const Note = "Turnos deduzidos das requisições: a API não tem ID de turno. Um turno começa numa requisição " +
	"da conversa principal (com ferramentas) cuja última mensagem do usuário não traz tool_result, ou seja, " +
	"um prompt digitado; as requisições seguintes do loop de ferramentas, os subagentes e as chamadas " +
	"auxiliares entram no turno em andamento pela hora de início."

// Request is what grouping reads of an exchange's request summary
type Request struct {
	// Known is false when the exchange has no request summary (in
	// progress, other endpoints, a body that could not be read)
	Known bool
	// Tools is the number of tool definitions sent
	Tools int
	// SystemSHA is the SHA-256 of the system field ("" without one)
	SystemSHA string
	// MessageCount is the number of messages
	MessageCount int
	// LastUserIndex is the index of the last user message; -1 when the
	// capture did not record it (earlier versions): the last message
	LastUserIndex int
	// ToolResultIndexes are the message indexes of the tool_result blocks
	ToolResultIndexes []int
	// FirstMessageHash is the hash of the first message, which stays the
	// same while a conversation goes on (until a compaction)
	FirstMessageHash string
	// LastMessageHash is the hash of the last message
	LastMessageHash string
	// Prompt is the typed text of the last user message (redacted)
	Prompt string
}

// Exchange is an exchange with its request summary's fields
type Exchange struct {
	api.HTTPExchange
	Request Request
}

// Turn is a group of exchanges; Prompt is its start's typed text
type Turn struct {
	Prompt    string
	Exchanges []api.HTTPExchange
}

// lastUserHasToolResult tells whether the request's last user message
// carries tool results (the request goes on with a tool loop)
func (r Request) lastUserHasToolResult() bool {
	idx := r.LastUserIndex
	if idx < 0 {
		idx = r.MessageCount - 1
	}
	for _, i := range r.ToolResultIndexes {
		if i == idx {
			return true
		}
	}
	return false
}

// Group splits exchanges, in start order, into the preamble (those before
// the first turn: startup checks, the quota probe) and turns.
//
// A request belongs to the main conversation when it is an inference that
// sends tools and either is the first such request, or has the system
// prompt or the first message of a main request (the first message stays
// when the system prompt changes, after a model switch; the system prompt
// stays after a compaction). Subagents (their own system prompt and first
// message) and helpers (title generation, sent without tools) are not main
// requests. A turn starts at a main request
// whose last user message has no tool_result, unless it repeats the last
// main request (a retry: X-Stainless-Retry-Count > 0, or the same message
// count and last message). Every other exchange joins the turn in progress.
func Group(exchanges []Exchange) (preamble []api.HTTPExchange, turns []Turn) {
	main := conversation{systems: map[string]bool{}, firsts: map[string]bool{}}
	var lastMain *Request
	for i := range exchanges {
		e := &exchanges[i]
		if main.has(e) {
			main.systems[e.Request.SystemSHA] = true
			if e.Request.FirstMessageHash != "" {
				main.firsts[e.Request.FirstMessageHash] = true
			}
			starts := !e.Request.lastUserHasToolResult() && !isRetry(e, lastMain)
			lastMain = &e.Request
			if starts {
				turns = append(turns, Turn{Prompt: e.Request.Prompt})
			}
		}
		if len(turns) == 0 {
			preamble = append(preamble, e.HTTPExchange)
			continue
		}
		t := &turns[len(turns)-1]
		t.Exchanges = append(t.Exchanges, e.HTTPExchange)
	}
	return preamble, turns
}

// conversation is what identifies the main conversation's requests
type conversation struct {
	systems, firsts map[string]bool
}

// has tells whether an exchange is a request of the main conversation
func (c conversation) has(e *Exchange) bool {
	r := e.Request
	if e.Kind != string(anthropicwire.KindModelInference) || !r.Known || r.Tools == 0 {
		return false
	}
	return len(c.systems) == 0 || c.systems[r.SystemSHA] || (r.FirstMessageHash != "" && c.firsts[r.FirstMessageHash])
}

// isRetry tells whether a main request repeats the previous one
func isRetry(e *Exchange, lastMain *Request) bool {
	if e.RetryCount > 0 {
		return true
	}
	return lastMain != nil && e.Request.LastMessageHash != "" &&
		e.Request.MessageCount == lastMain.MessageCount && e.Request.LastMessageHash == lastMain.LastMessageHash
}

// Summarize describes a group of exchanges (index 0: the preamble), with
// the definitions of the traffic summary (storage.HTTPTrafficSummary):
// errors leave out quota probes, tokens and cost come from model
// inferences only, final usage apart from partial
func Summarize(index int, prompt string, exchanges []api.HTTPExchange) api.HTTPTurn {
	t := api.HTTPTurn{
		Index: index, Prompt: prompt, Exchanges: exchanges, ToolUses: []string{}, Models: []string{},
		CostStatus: api.CostUnavailable,
	}
	if t.Exchanges == nil {
		t.Exchanges = []api.HTTPExchange{}
	}
	models := map[string]bool{}
	var priced, unpriced int64
	for i, e := range exchanges {
		if i == 0 || e.StartedAt.Before(t.StartedAt) {
			t.StartedAt = e.StartedAt
		}
		end := e.StartedAt
		if e.DurationMs != nil {
			end = end.Add(time.Duration(*e.DurationMs) * time.Millisecond)
		}
		if end.After(t.EndedAt) {
			t.EndedAt = end
		}
		if e.Kind != string(anthropicwire.KindQuotaProbe) && (e.Status >= 400 || e.State == api.ExchangeFailed || e.ErrorType != "") {
			t.Errors++
		}
		if e.Kind != string(anthropicwire.KindModelInference) {
			t.Auxiliary++
			continue
		}
		t.Inference++
		t.ToolUses = append(t.ToolUses, e.ToolUses...)
		if m := e.ResponseModel; m != "" {
			models[m] = true
		} else if e.RequestedModel != "" {
			models[e.RequestedModel] = true
		}
		u := e.Usage
		if u == nil || (u.Quality != api.UsageFinal && u.Quality != api.UsagePartial) {
			continue
		}
		totals := &t.Tokens
		if u.Quality == api.UsagePartial {
			totals = &t.PartialTokens
		}
		addUsage(totals, u)
		if u.CostUSD != nil {
			t.CostUSD += *u.CostUSD
		}
		if u.CostStatus == api.CostComplete {
			priced++
		} else {
			unpriced++
		}
	}
	if !t.EndedAt.IsZero() {
		t.DurationMs = t.EndedAt.Sub(t.StartedAt).Milliseconds()
	}
	for _, totals := range []*api.HTTPTokenTotals{&t.Tokens, &t.PartialTokens} {
		if totals.TotalInput > 0 {
			share := float64(totals.CacheRead) / float64(totals.TotalInput)
			totals.CacheReadShare = &share
		}
	}
	switch {
	case priced > 0 && unpriced == 0:
		t.CostStatus = api.CostComplete
	case priced > 0 || t.CostUSD > 0:
		t.CostStatus = api.CostPartial
	}
	for m := range models {
		t.Models = append(t.Models, m)
	}
	sort.Strings(t.Models)
	return t
}

// addUsage adds an exchange's usage to totals, field by field as the
// traffic summary sums the usage table's columns
func addUsage(t *api.HTTPTokenTotals, u *api.HTTPUsage) {
	t.Exchanges++
	t.Input += val(u.Usage.InputTokens)
	t.Output += val(u.Usage.OutputTokens)
	t.CacheRead += val(u.Usage.CacheReadInputTokens)
	t.CacheWrite += val(u.Usage.CacheCreationInputTokens)
	t.CacheWrite5m += val(u.Usage.CacheCreation5m)
	t.CacheWrite1h += val(u.Usage.CacheCreation1h)
	t.CacheWriteOther += val(u.Totals.CacheWriteUnknown)
	t.TotalInput += val(u.Totals.TotalInput)
	t.TotalProcessed += val(u.Totals.TotalProcessed)
}

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
