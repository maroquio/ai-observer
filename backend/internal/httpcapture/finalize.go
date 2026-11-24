package httpcapture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/logger"
	"github.com/tobilg/ai-observer/internal/pricing"
	"github.com/tobilg/ai-observer/internal/selfmetrics"
	"github.com/tobilg/ai-observer/internal/storage"
)

// Text bounds of what is stored in the database (bodies are in files)
const (
	maxToolInputBytes  = 16 << 10
	maxPreviewBytes    = 2 << 10
	maxErrorBytes      = 4 << 10
	maxSummaryJSONSize = 4 << 20
)

// snapshot is the exchange's transport state when it ended
type snapshot struct {
	parts            [2]partState
	status           int
	respHeaders      []api.HTTPHeader
	respContentType  string
	respContentEnc   string
	requestID        string
	responseAt       time.Time
	firstByteAt      time.Time
	transportErr     string
	clientAborted    bool
	upstreamAborted  bool
	unsupportedProto string
	lifecycle        []api.HTTPLifecycleEvent
	degraded         []string
}

func (e *Exchange) snapshot() snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return snapshot{
		parts:            e.parts,
		status:           e.status,
		respHeaders:      e.respHeaders,
		respContentType:  e.respContentType,
		respContentEnc:   e.respContentEnc,
		requestID:        e.requestID,
		responseAt:       e.responseAt,
		firstByteAt:      e.firstByteAt,
		transportErr:     e.transportErr,
		clientAborted:    e.clientAborted,
		upstreamAborted:  e.upstreamAborted,
		unsupportedProto: e.unsupportedProto,
		lifecycle:        append([]api.HTTPLifecycleEvent(nil), e.lifecycle...),
		degraded:         append([]string(nil), e.degraded...),
	}
}

// finish interprets what was observed and queues the finished record. A
// panic while interpreting still stores the transport facts.
func (e *Exchange) finish(ctx context.Context) {
	snap := e.snapshot()
	var fin *storage.HTTPExchangeFinish
	func() {
		defer func() {
			if r := recover(); r != nil {
				e.c.stats.parseErrors.Add(1)
				selfmetrics.HTTPCaptureErrors.With("parse").Inc()
				logger.Warn("HTTP capture: interpreting an exchange failed", "exchange", e.id, "panic", fmt.Sprint(r))
				fin = e.fallback(snap, fmt.Sprint(r))
			}
		}()
		fin = e.build(ctx, snap)
	}()
	for i := range e.bodies {
		e.bodies[i].discard()
	}
	ex := fin.Record.Exchange
	selfmetrics.HTTPCaptureExchanges.With(ex.Kind, ex.State).Inc()
	if !snap.responseAt.IsZero() {
		selfmetrics.HTTPCaptureFirstByteSeconds.With().Observe(snap.responseAt.Sub(e.startedAt).Seconds())
	}
	if ex.DurationMs != nil {
		selfmetrics.HTTPCaptureUpstreamSeconds.With().Observe(float64(*ex.DurationMs) / 1000)
	}
	e.c.pipeline.enqueue(job{finished: fin})
}

// fallback is the record of an exchange whose interpretation failed
func (e *Exchange) fallback(snap snapshot, why string) *storage.HTTPExchangeFinish {
	ex := e.baseExchange(transportState(snap))
	ex.Status = snap.status
	ex.RequestID = snap.requestID
	ex.Degraded = true
	ex.Headline = "Troca registrada sem interpretação (falha ao interpretar)"
	ex.RequestBytes = snap.parts[reqPart].lostSize + e.bodies[reqPart].size
	ex.ResponseBytes = snap.parts[respPart].lostSize + e.bodies[respPart].size
	return &storage.HTTPExchangeFinish{Record: storage.HTTPExchangeRecord{
		Exchange:                ex,
		DegradedReasons:         append(snap.degraded, "falha ao interpretar a troca: "+truncateText(why, 200)),
		RequestHeaders:          e.reqHeaders,
		ResponseHeaders:         nonNilHeaders(snap.respHeaders),
		RequestContentType:      e.reqContentType,
		RequestContentEncoding:  e.reqContentEncoding,
		ResponseContentType:     snap.respContentType,
		ResponseContentEncoding: snap.respContentEnc,
		Lifecycle:               snap.lifecycle,
	}}
}

// transportState is the state from transport facts alone
func transportState(snap snapshot) string {
	resp := snap.parts[respPart]
	switch {
	case snap.unsupportedProto != "":
		return api.ExchangeFailed
	case snap.responseAt.IsZero():
		return api.ExchangeFailed
	case snap.clientAborted:
		return api.ExchangeAborted
	case snap.upstreamAborted || resp.err != nil || (resp.closed && !resp.done):
		return api.ExchangePartial
	}
	req := snap.parts[reqPart]
	if req.err != nil || (req.closed && !req.done) {
		return api.ExchangePartial
	}
	return api.ExchangeComplete
}

// decoded is a body ready for interpretation
type decoded struct {
	observed     int64 // bytes on the wire (processed and lost)
	observedHash string
	kept         []byte // encoded bytes kept (up to MaxBodyBytes)
	truncated    bool   // not every observed byte is kept
	lost         bool
	data         []byte // decoded
	decodeStatus string
	decodeErr    error
}

func (e *Exchange) decodeBody(part int, st partState, encoding string) decoded {
	s := e.bodies[part]
	d := decoded{
		observed:  s.size + st.lostSize,
		kept:      s.bytes(),
		truncated: s.truncated() || st.lost,
		lost:      st.lost,
	}
	if !st.lost {
		d.observedHash = s.sum()
	}
	d.data, d.decodeStatus, d.decodeErr = anthropicwire.DecodeBody(d.kept, encoding, e.c.cfg.MaxBodyBytes)
	if d.decodeStatus == anthropicwire.DecodeIdentity && d.truncated {
		// what is kept is the start of a longer body
		d.decodeStatus = anthropicwire.DecodeTruncated
	}
	return d
}

// parseEncodedStream decodes a compressed event stream and parses its
// events, each stamped with the arrival of the chunk it was decoded from;
// decoded output stops at the body limit. It returns the decoding error,
// if any (the events before it are kept).
func (e *Exchange) parseEncodedStream(data []byte, encoding string, end time.Time) error {
	e.sse = anthropicwire.NewSSEParser(0)
	e.recon = anthropicwire.NewReconstructor()
	tr := &timedReader{data: data, marks: e.respMarks, at: end}
	dec, release, err := anthropicwire.NewStreamDecoder(tr, encoding)
	if err != nil {
		return err
	}
	defer release()
	limit := e.c.cfg.MaxBodyBytes
	if limit <= 0 {
		limit = anthropicwire.DefaultMaxDecodedBytes
	}
	buf := make([]byte, 32<<10)
	for decoded := int64(0); decoded < limit; {
		n, err := dec.Read(buf[:min(int64(len(buf)), limit-decoded)])
		if n > 0 {
			decoded += int64(n)
			for _, ev := range e.sse.Feed(buf[:n]) {
				e.addEvent(ev, tr.at)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// build interprets the exchange
func (e *Exchange) build(_ context.Context, snap snapshot) *storage.HTTPExchangeFinish {
	c := e.c
	state := transportState(snap)
	ex := e.baseExchange(state)
	ex.Status = snap.status
	ex.RequestID = snap.requestID
	degraded := snap.degraded
	lifecycle := snap.lifecycle

	end := snap.parts[respPart].doneAt
	if end.IsZero() {
		end = time.Now()
	}
	ex.DurationMs = msPtr(end.Sub(e.startedAt))
	if !snap.responseAt.IsZero() {
		ex.TTFBMs = msPtr(snap.responseAt.Sub(e.startedAt))
	} else if snap.unsupportedProto == "" {
		ex.Kind = string(anthropicwire.KindTransportError)
	}

	// Request
	req := e.decodeBody(reqPart, snap.parts[reqPart], e.reqContentEncoding)
	ex.RequestBytes = req.observed
	var reqSum *anthropicwire.RequestSummary
	if e.kind == anthropicwire.KindModelInference || e.kind == anthropicwire.KindTokenCount {
		if req.decodeErr == nil && len(req.data) > 0 {
			s := anthropicwire.ParseRequest(req.data)
			reqSum = &s
			ex.RequestedModel = s.Model
			if s.Stream != nil {
				ex.Stream = *s.Stream
			}
			if ex.SessionID == "" && s.SessionID != "" {
				ex.SessionID, ex.SessionEvidence = s.SessionID, "body:metadata.user_id"
			}
			if !s.Valid && s.ParseError != "" {
				degraded = append(degraded, "corpo da requisição não interpretado: "+truncateText(s.ParseError, 200))
			}
			// Claude Code's quota probe is an auxiliary call: only the row's
			// kind changes, e.kind keeps the response parsed as an inference
			// (an exchange without response stays a transport error)
			if e.kind == anthropicwire.KindModelInference && ex.Kind == string(e.kind) && anthropicwire.IsQuotaProbe(s, req.data) {
				ex.Kind = string(anthropicwire.KindQuotaProbe)
			}
		} else if req.decodeErr != nil {
			degraded = append(degraded, "corpo da requisição não decodificado ("+req.decodeStatus+")")
		}
	}

	// Response
	resp := e.decodeBody(respPart, snap.parts[respPart], snap.respContentEnc)
	ex.ResponseBytes = resp.observed
	var msg *anthropicwire.Message
	var countTokens *int64
	stream := isEventStream(snap.respContentType)
	if stream {
		ex.Stream = true
		if e.sseMode == sseEncoded {
			if err := e.parseEncodedStream(resp.kept, snap.respContentEnc, end); err != nil && !resp.truncated {
				degraded = append(degraded, "stream comprimido ("+snap.respContentEnc+") não decodificado por inteiro: "+truncateText(err.Error(), 200))
				lifecycle = append(lifecycle, api.HTTPLifecycleEvent{Event: EventStreamParseError, At: time.Now().UTC(),
					Detail: "stream comprimido (" + snap.respContentEnc + "): " + truncateText(err.Error(), 200)})
			}
		}
		if e.sse != nil {
			if ev := e.sse.Finish(); ev != nil {
				// an event without its terminating blank line: incomplete
				e.addEvent(*ev, end)
				degraded = append(degraded, "stream terminou no meio de um evento SSE")
			}
			msg = e.recon.Message()
		}
		if e.eventsCut > 0 {
			degraded = append(degraded, fmt.Sprintf("%d eventos SSE além do limite de %d não foram armazenados (contados e interpretados)", e.eventsCut, c.cfg.MaxEvents))
		}
		if resp.truncated && !resp.lost {
			degraded = append(degraded, "corpo do stream maior que o limite de armazenamento: apenas o início foi guardado")
		}
	} else if resp.decodeErr == nil && len(resp.data) > 0 && looksJSON(resp.data) {
		switch {
		case snap.status >= 400:
			if apiErr := anthropicwire.ParseAPIError(resp.data); apiErr != nil {
				msg = &anthropicwire.Message{Error: apiErr, Content: []anthropicwire.ContentBlock{}}
			}
		case e.kind == anthropicwire.KindTokenCount:
			if n, err := anthropicwire.ParseCountTokens(resp.data); err == nil {
				countTokens = n
			}
		case e.kind == anthropicwire.KindModelInference:
			if m, err := anthropicwire.ParseMessageJSON(resp.data); err == nil {
				msg = m
			} else {
				degraded = append(degraded, "resposta JSON não interpretada: "+truncateText(err.Error(), 200))
			}
		}
	} else if resp.decodeErr != nil && resp.observed > 0 {
		degraded = append(degraded, "corpo da resposta não decodificado ("+resp.decodeStatus+")")
	}
	ex.SSEEvents = int64(len(e.events) + e.eventsCut)
	if !e.firstEvt.IsZero() {
		ex.FirstEventMs = msPtr(e.firstEvt.Sub(e.startedAt))
	}

	if msg != nil {
		ex.ResponseModel = msg.Model
		ex.MessageID = msg.ID
		if msg.StopReason != nil {
			ex.StopReason = *msg.StopReason
		}
		if msg.Error != nil {
			ex.ErrorType = msg.Error.Type
			ex.Error = truncateText(c.san.String(msg.Error.Message), maxErrorBytes)
		}
		// A stream that ended without message_stop is partial even when the
		// connection closed normally
		if stream && state == api.ExchangeComplete && msg.SawMessageStart && !msg.SawMessageStop && msg.Error == nil {
			state = api.ExchangePartial
			degraded = append(degraded, "stream sem message_stop: a resposta pode estar incompleta")
		}
	}
	if snap.transportErr != "" && ex.Error == "" {
		ex.ErrorType = "transport_error"
		ex.Error = truncateText(c.san.String(snap.transportErr), maxErrorBytes)
	}
	if snap.unsupportedProto != "" && ex.Error == "" {
		ex.ErrorType = "unsupported"
		ex.Error = snap.unsupportedProto
	}
	if ex.Error == "" && snap.status >= 400 {
		ex.ErrorType = fmt.Sprintf("http_%d", snap.status)
	}
	ex.State = state
	if state == api.ExchangeComplete {
		lifecycle = append(lifecycle, api.HTTPLifecycleEvent{Event: EventCompleted, At: end.UTC()})
	}

	// Usage and cost
	var usageRec *storage.HTTPUsageRecord
	model := ex.ResponseModel
	if model == "" {
		model = ex.RequestedModel
	}
	if detail := e.usage(ex.Kind, msg, countTokens, model, state, stream); detail != nil {
		ex.Usage = &detail.HTTPUsage
		usageRec = &storage.HTTPUsageRecord{
			ExchangeID: e.id, RunID: e.run.ID, SessionID: ex.SessionID, Kind: ex.Kind,
			Model: model, StartedAt: e.startedAt.UTC(), Detail: *detail,
		}
	}

	// Tool uses and the results submitted for the first time
	var toolUses []storage.HTTPToolUseRecord
	if msg != nil {
		refs := anthropicwire.ToolUses(msg)
		ids := make([]string, 0, len(refs))
		for _, u := range refs {
			if u.ID == "" {
				continue
			}
			ids = append(ids, u.ID)
			ex.ToolUses = append(ex.ToolUses, u.Name)
			input := ""
			if c.cfg.Mode != ModeMetadataOnly && len(u.Input) > 0 {
				input = truncateText(e.safeJSON(u.Input), maxToolInputBytes)
			}
			toolUses = append(toolUses, storage.HTTPToolUseRecord{ToolUseID: u.ID, Name: u.Name, Input: input})
		}
		e.run.noteToolUses(e.seq, ids)
	}
	var submitted map[string]int
	var toolResults []storage.HTTPToolResultRecord
	if reqSum != nil && e.kind == anthropicwire.KindModelInference && len(reqSum.ToolResults) > 0 {
		ids := make([]string, 0, len(reqSum.ToolResults))
		for _, r := range reqSum.ToolResults {
			if r.ToolUseID != "" {
				ids = append(ids, r.ToolUseID)
			}
		}
		submitted = e.run.newResults(e.seq, ids)
		for _, r := range reqSum.ToolResults {
			if _, ok := submitted[r.ToolUseID]; !ok {
				continue
			}
			preview := ""
			if c.cfg.Mode != ModeMetadataOnly {
				preview = truncateText(c.san.String(r.Preview), maxPreviewBytes)
			}
			toolResults = append(toolResults, storage.HTTPToolResultRecord{
				ToolUseID: r.ToolUseID, IsError: r.IsError, Preview: preview, Bytes: int64(r.ContentBytes),
			})
		}
		ex.ToolResults = len(toolResults)
	}

	// Description
	in := anthropicwire.DescribeInput{
		Kind: anthropicwire.ExchangeKind(ex.Kind), Method: e.method, Path: e.path, Status: snap.status,
		State: state, Request: reqSum, Response: msg, CountTokens: countTokens,
		SubmittedResults: submitted,
	}
	if snap.transportErr != "" {
		in.TransportError = truncateText(c.san.String(snap.transportErr), maxErrorBytes)
	}
	ex.Headline = anthropicwire.Headline(in)
	observations := anthropicwire.Describe(in)

	// What is stored of the interpretation
	firstHash := ""
	if reqSum != nil && len(reqSum.Messages.Hashes) > 0 {
		firstHash = reqSum.Messages.Hashes[0]
	}
	rec := storage.HTTPExchangeRecord{
		Exchange:                ex,
		RequestHeaders:          e.reqHeaders,
		ResponseHeaders:         nonNilHeaders(snap.respHeaders),
		RequestContentType:      e.reqContentType,
		RequestContentEncoding:  e.reqContentEncoding,
		ResponseContentType:     snap.respContentType,
		ResponseContentEncoding: snap.respContentEnc,
		Lifecycle:               lifecycle,
		Observations:            e.storedJSON(observations, false),
		FirstMessageHash:        firstHash,
	}
	if reqSum != nil {
		rec.RequestSummary = e.storedJSON(e.storedRequest(*reqSum), true)
	}
	if msg != nil {
		rec.ResponseMessage = e.storedJSON(e.storedMessage(msg), true)
	}

	// Bodies
	payloads, perr := e.storeBodies(req, resp, snap)
	if perr != "" {
		degraded = append(degraded, perr)
	}
	rec.DegradedReasons = nonNilStrings(degraded)
	rec.Exchange.Degraded = len(degraded) > 0

	events := e.events
	if events == nil {
		events = []api.HTTPSSEEvent{}
	}
	return &storage.HTTPExchangeFinish{
		Record:      rec,
		Events:      events,
		Usage:       usageRec,
		Payloads:    payloads,
		ToolUses:    toolUses,
		ToolResults: toolResults,
	}
}

// usage builds an exchange's usage and estimated cost (nil: nothing to
// record, such as an error response); kind is the row's kind, which a
// quota probe changes
func (e *Exchange) usage(kind string, msg *anthropicwire.Message, countTokens *int64, model, state string, stream bool) *api.HTTPUsageDetail {
	d := &api.HTTPUsageDetail{
		ExchangeID: e.id,
		Kind:       kind,
		Model:      model,
		Snapshots:  []anthropicwire.UsageSnapshot{},
		HTTPUsage:  api.HTTPUsage{Source: "http_proxy"},
	}
	d.CostComponents = []api.HTTPCostComponent{}
	if countTokens != nil {
		d.CountTokens = countTokens
		d.Quality = api.UsageUnavailable
		d.CostStatus = api.CostUnavailable
		d.CostReason = "count_tokens devolve uma estimativa do prompt; não há consumo de inferência"
		return d
	}
	if msg == nil || e.kind != anthropicwire.KindModelInference {
		return nil
	}
	u := msg.Usage
	d.Usage = u
	d.Totals = u.Totals()
	if msg.UsageSnapshots != nil {
		d.Snapshots = msg.UsageSnapshots
	}
	hasUsage := u.InputTokens != nil || u.OutputTokens != nil
	switch {
	case !hasUsage:
		d.Quality = api.UsageUnavailable
	case state == api.ExchangeComplete && (!stream || msg.SawMessageStop):
		d.Quality = api.UsageFinal
	default:
		d.Quality = api.UsagePartial
	}
	if !hasUsage {
		if msg.Error == nil {
			d.CostStatus = api.CostUnavailable
			d.CostReason = "a resposta não informou uso de tokens"
			return d
		}
		return nil
	}
	priceUsage(d, model)
	return d
}

// cacheWrite1hInputMultiplier is the price of a 1-hour cache write token
// relative to an input token of the same model and context tier
// (Anthropic's prompt caching pricing; docs/http-inspector/adr/ADR-006)
const cacheWrite1hInputMultiplier = 2

// priceUsage estimates the API-equivalent cost of d's usage from the price
// catalog. Nothing is priced at 0 for lack of a price: unpriced parts make
// the status partial (or unavailable) with the reason.
func priceUsage(d *api.HTTPUsageDetail, model string) {
	u := d.Usage
	val := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return max(0, *p)
	}
	input, output := val(u.InputTokens), val(u.OutputTokens)
	cacheRead, cacheWrite := val(u.CacheReadInputTokens), val(u.CacheCreationInputTokens)
	p := pricing.ClaudePricingForPrompt(model, input+cacheRead+cacheWrite)
	if p == nil {
		d.CostStatus = api.CostUnavailable
		if model == "" {
			d.CostReason = "modelo desconhecido: sem preço no catálogo"
		} else {
			d.CostReason = "o modelo " + model + " não tem preço no catálogo"
		}
		return
	}
	d.PricedModel = pricing.NormalizeClaudeModel(model)
	var total float64
	var missing []string
	add := func(component string, tokens int64, perToken *float64, note string) {
		c := api.HTTPCostComponent{Component: component, Tokens: tokens, Note: note}
		if perToken != nil {
			perM := *perToken * 1e6
			cost := float64(tokens) * *perToken
			c.PricePerMTok, c.CostUSD = &perM, &cost
			total += cost
		} else if tokens > 0 {
			missing = append(missing, component)
		}
		d.CostComponents = append(d.CostComponents, c)
	}
	add("input", input, &p.InputCostPerToken, "")
	add("output", output, &p.OutputCostPerToken, "")
	if cacheRead > 0 || u.CacheReadInputTokens != nil {
		add("cache_read", cacheRead, &p.CacheReadCostPerToken, "")
	}
	write5m, write1h := val(u.CacheCreation5m), val(u.CacheCreation1h)
	if write5m > 0 {
		add("cache_write_5m", write5m, &p.CacheWriteCostPerToken, "")
	}
	if write1h > 0 {
		// The catalog (models.dev) has no 1-hour write price; Anthropic prices
		// 1-hour cache writes at twice the base input price (5-minute ones at
		// 1.25x), the rule Claude Code's own cost metric follows
		write1hPrice := cacheWrite1hInputMultiplier * p.InputCostPerToken
		add("cache_write_1h", write1h, &write1hPrice, "derivado: 2 × o preço de input do catálogo (regra de preços da Anthropic para escrita de cache de 1 hora)")
	}
	if unknown := val(d.Totals.CacheWriteUnknown); unknown > 0 {
		add("cache_write_unknown", unknown, &p.CacheWriteCostPerToken, "TTL não informado: estimado com o preço de escrita de cache do catálogo (5 minutos)")
	}
	d.CostUSD = &total
	switch {
	case len(missing) > 0:
		d.CostStatus = api.CostPartial
		d.CostReason = "sem preço para " + strings.Join(missing, ", ") + ": o custo mostrado não inclui esses tokens"
	case d.Quality == api.UsagePartial:
		d.CostStatus = api.CostPartial
		d.CostReason = "uso parcial (resposta interrompida): custo dos tokens observados até o fim"
	default:
		d.CostStatus = api.CostComplete
	}
}

// storeBodies writes the body files of the mode and returns their rows
func (e *Exchange) storeBodies(req, resp decoded, snap snapshot) ([]storage.HTTPPayloadRecord, string) {
	c := e.c
	if c.cfg.Mode == ModeMetadataOnly || c.blobs == nil {
		return []storage.HTTPPayloadRecord{}, ""
	}
	var out []storage.HTTPPayloadRecord
	var problems []string
	for part, d := range map[int]decoded{reqPart: req, respPart: resp} {
		name, enc := PartRequest, e.reqContentEncoding
		if part == respPart {
			name, enc = PartResponse, snap.respContentEnc
		}
		if d.observed == 0 && len(d.kept) == 0 {
			continue
		}
		// safe: the decoded body, redacted
		if d.decodeErr == nil {
			safe, n, ok := c.san.Body(d.data)
			if ok {
				rel, err := BodyPath(e.run.ID, e.id, name, api.BodyVariantSafe)
				if err == nil {
					err = c.blobs.WriteFile(rel, safe)
				}
				if err != nil {
					c.stats.blobErrors.Add(1)
					selfmetrics.HTTPCaptureErrors.With("blob").Inc()
					problems = append(problems, "corpo seguro do "+name+" não gravado: "+err.Error())
				} else {
					sum := sha256.Sum256(safe)
					out = append(out, storage.HTTPPayloadRecord{
						ExchangeID: e.id, Part: name, Variant: api.BodyVariantSafe, RelPath: rel,
						ObservedBytes: d.observed, ObservedSHA256: d.observedHash,
						StoredBytes: int64(len(safe)), StoredSHA256: hex.EncodeToString(sum[:]),
						ContentEncoding: enc, DecodeStatus: d.decodeStatus, Truncated: d.truncated,
						Sanitized: n > 0, Redactions: n,
					})
					c.stats.bytesStored.Add(int64(len(safe)))
					selfmetrics.HTTPCaptureStoredBytes.With().Add(len(safe))
				}
			} else {
				problems = append(problems, "corpo do "+name+" não é texto UTF-8: a variante segura não foi gravada")
			}
		}
		// raw: the bytes as observed (full_raw only), never headers
		if c.cfg.Mode == ModeFullRaw {
			s := e.bodies[part]
			rel, err := BodyPath(e.run.ID, e.id, name, api.BodyVariantRaw)
			if err == nil {
				if s.spool != nil && s.spoolErr == nil {
					err = s.spool.Keep(rel)
					s.spool = nil
				} else {
					err = c.blobs.WriteFile(rel, d.kept)
				}
			}
			if err != nil {
				c.stats.blobErrors.Add(1)
				selfmetrics.HTTPCaptureErrors.With("blob").Inc()
				problems = append(problems, "corpo bruto do "+name+" não gravado: "+err.Error())
				continue
			}
			sum := sha256.Sum256(d.kept)
			out = append(out, storage.HTTPPayloadRecord{
				ExchangeID: e.id, Part: name, Variant: api.BodyVariantRaw, RelPath: rel,
				ObservedBytes: d.observed, ObservedSHA256: d.observedHash,
				StoredBytes: int64(len(d.kept)), StoredSHA256: hex.EncodeToString(sum[:]),
				ContentEncoding: enc, DecodeStatus: anthropicwire.DecodeIdentity, Truncated: d.truncated,
			})
			c.stats.bytesStored.Add(int64(len(d.kept)))
			selfmetrics.HTTPCaptureStoredBytes.With().Add(len(d.kept))
		}
	}
	if out == nil {
		out = []storage.HTTPPayloadRecord{}
	}
	return out, strings.Join(problems, "; ")
}

// safeJSON returns a JSON value redacted (or a note when it is invalid)
func (e *Exchange) safeJSON(raw []byte) string {
	out, _, err := e.c.san.JSON(raw)
	if err != nil {
		return ""
	}
	return string(out)
}

// storedJSON marshals v for the database: redacted, and bounded
func (e *Exchange) storedJSON(v any, redact bool) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	if redact {
		if out, _, err := e.c.san.JSON(b); err == nil {
			b = out
		}
	}
	if len(b) > maxSummaryJSONSize {
		return json.RawMessage(`{"omitted":"too large"}`)
	}
	return b
}

// storedRequest is the request summary as stored: in metadata_only mode
// without content (previews, tool arguments, metadata values)
func (e *Exchange) storedRequest(s anthropicwire.RequestSummary) anthropicwire.RequestSummary {
	if e.c.cfg.Mode != ModeMetadataOnly {
		return s
	}
	s.Metadata = nil
	s.StopSequences = nil
	results := make([]anthropicwire.ToolResultRef, len(s.ToolResults))
	for i, r := range s.ToolResults {
		r.Preview = ""
		results[i] = r
	}
	s.ToolResults = results
	uses := make([]anthropicwire.ToolUseRef, len(s.ToolUses))
	for i, u := range s.ToolUses {
		u.Input = nil
		uses[i] = u
	}
	s.ToolUses = uses
	return s
}

// storedMessage is the reply as stored: in metadata_only mode only its
// structure (block types, tool names and IDs, usage, stop reason)
func (e *Exchange) storedMessage(m *anthropicwire.Message) *anthropicwire.Message {
	if e.c.cfg.Mode != ModeMetadataOnly {
		return m
	}
	cp := *m
	cp.Content = make([]anthropicwire.ContentBlock, len(m.Content))
	for i, b := range m.Content {
		cp.Content[i] = anthropicwire.ContentBlock{
			Index: b.Index, Type: b.Type, ID: b.ID, Name: b.Name, ToolUseID: b.ToolUseID,
			InputComplete: b.InputComplete, Stopped: b.Stopped,
		}
	}
	cp.Extra = nil
	if cp.Error != nil {
		cp.Error = &anthropicwire.APIError{Type: cp.Error.Type}
	}
	return &cp
}

func msPtr(d time.Duration) *int64 {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return &ms
}

func looksJSON(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

func nonNilHeaders(h []api.HTTPHeader) []api.HTTPHeader {
	if h == nil {
		return []api.HTTPHeader{}
	}
	return h
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
