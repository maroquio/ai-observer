package handlers

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/httpcapture"
	"github.com/tobilg/ai-observer/internal/logger"
	"github.com/tobilg/ai-observer/internal/storage"
	"github.com/tobilg/ai-observer/internal/version"
)

// HTTPInspectorStore is the storage of the HTTP Inspector endpoints
// (storage.DuckDBStore)
type HTTPInspectorStore interface {
	HTTPCaptureRuns(ctx context.Context, f storage.HTTPRunFilter, page storage.Page) (*api.HTTPCaptureRunsResponse, error)
	HTTPCaptureRun(ctx context.Context, runID string) (*api.HTTPCaptureRun, error)
	UpdateHTTPCaptureRun(ctx context.Context, runID string, name, note *string) error
	DeleteHTTPCaptureRun(ctx context.Context, runID string) error
	HTTPExchanges(ctx context.Context, f storage.HTTPExchangeFilter, page storage.Page) (*api.HTTPExchangesResponse, error)
	HTTPExchange(ctx context.Context, id string) (*storage.HTTPStoredExchange, error)
	HTTPExchangeNeighbors(ctx context.Context, runID string, seq int64) (prev, next string, err error)
	HTTPDiffCandidates(ctx context.Context, runID, kind, sessionID string, beforeSeq int64, limit int) ([]storage.HTTPDiffCandidate, error)
	HTTPSSEEvents(ctx context.Context, exchangeID string, typ string, page storage.Page) (*api.HTTPSSEEventsResponse, error)
	HTTPPayloads(ctx context.Context, exchangeID string) ([]storage.HTTPPayloadRecord, error)
	HTTPToolLinks(ctx context.Context, runID, sessionID string) ([]api.HTTPToolLink, error)
	HTTPTrafficSummary(ctx context.Context, runID, sessionID string) (*api.HTTPTrafficSummary, error)
	HTTPSessionRuns(ctx context.Context, sessionID string) ([]api.HTTPCaptureRun, error)
	HTTPSessionTelemetryRequestIDs(ctx context.Context, sessionID string) (map[string]bool, error)
	HTTPSessionRequestIDs(ctx context.Context, sessionID string) ([]string, error)
}

var _ HTTPInspectorStore = (*storage.DuckDBStore)(nil)

// HTTPProxy is the running capture proxy (httpproxy.Manager)
type HTTPProxy interface {
	Status() api.HTTPInspectorStatus
	StartLauncherRun(ctx context.Context, opts httpcapture.RunOptions) (*httpcapture.Run, string, error)
	FinishRun(ctx context.Context, runID string, exitCode *int, pid int) error
}

// HTTPInspector serves /api/http-inspector/* and
// /api/sessions/{sessionId}/http-exchanges
type HTTPInspector struct {
	store HTTPInspectorStore
	// proxy is nil while the proxy is off (stored captures can still be read)
	proxy HTTPProxy
	// blobs holds the stored bodies; nil without a capture directory
	blobs *httpcapture.BlobStore
	// off describes the proxy while it is off
	off api.HTTPInspectorStatus
}

// NewHTTPInspector returns the HTTP Inspector handlers. proxy and blobs may
// be nil; off is the status reported while proxy is nil.
func NewHTTPInspector(store HTTPInspectorStore, proxy HTTPProxy, blobs *httpcapture.BlobStore, off api.HTTPInspectorStatus) *HTTPInspector {
	off.Enabled = false
	if off.Warnings == nil {
		off.Warnings = []string{}
	}
	return &HTTPInspector{store: store, proxy: proxy, blobs: blobs, off: off}
}

// maxDisplayBody bounds a body returned by the API
const maxDisplayBody = 16 << 20

// toolsNote is shown with tool links
const toolsNote = "O proxy vê apenas o pedido de ferramenta (tool_use) na resposta do modelo e o resultado (tool_result) enviado no request seguinte. A execução local da ferramenta não passa pela rede e não é observada."

// Status returns the proxy's state
func (h *HTTPInspector) Status() api.HTTPInspectorStatus {
	if h.proxy == nil {
		return h.off
	}
	return h.proxy.Status()
}

// GetStatus answers GET /api/http-inspector/status
func (h *HTTPInspector) GetStatus(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, h.Status())
}

func (h *HTTPInspector) notFound(w http.ResponseWriter, r *http.Request, what string, err error) {
	if errors.Is(err, storage.ErrCaptureNotFound) {
		api.WriteError(w, http.StatusNotFound, what+" not found")
		return
	}
	writeQueryError(w, r, "failed to load "+what, err)
}

// ListRuns answers GET /api/http-inspector/runs
func (h *HTTPInspector) ListRuns(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	f := storage.HTTPRunFilter{
		Mode:  p.Enum("mode", "", "", api.CaptureRunManual, api.CaptureRunLauncher),
		State: p.Enum("state", "", "", api.CaptureRunActive, api.CaptureRunFinished, api.CaptureRunInterrupted),
	}
	page := p.Page()
	if !p.Valid(w) {
		return
	}
	resp, err := h.store.HTTPCaptureRuns(r.Context(), f, page)
	if err != nil {
		writeQueryError(w, r, "failed to load capture runs", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, resp)
}

// CreateRun answers POST /api/http-inspector/runs: a launcher run with a
// proxy port of its own
func (h *HTTPInspector) CreateRun(w http.ResponseWriter, r *http.Request) {
	if h.proxy == nil {
		api.WriteError(w, http.StatusConflict, "the HTTP capture proxy is not enabled: start the server with --http-proxy (or AI_OBSERVER_HTTP_PROXY_ENABLED=true)")
		return
	}
	var req api.CreateHTTPCaptureRunRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Name = truncateRunText(req.Name, 200)
	run, proxyURL, err := h.proxy.StartLauncherRun(r.Context(), httpcapture.RunOptions{
		Name: req.Name, Cwd: truncateRunText(req.Cwd, 4096), Command: truncateRunText(req.Command, 4096),
	})
	if err != nil {
		api.WriteInternalError(w, "failed to start capture run", err)
		return
	}
	stored, err := h.store.HTTPCaptureRun(r.Context(), run.ID)
	if err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, api.HTTPCaptureRunCreated{Run: *stored, ProxyURL: proxyURL})
}

func truncateRunText(s string, n int) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// GetRun answers GET /api/http-inspector/runs/{runId}
func (h *HTTPInspector) GetRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "runId")
	run, err := h.store.HTTPCaptureRun(r.Context(), id)
	if err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	sum, err := h.store.HTTPTrafficSummary(r.Context(), id, "")
	if err != nil {
		writeQueryError(w, r, "failed to summarize capture run", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, api.HTTPRunDetail{Run: *run, Summary: *sum})
}

// UpdateRun answers PUT /api/http-inspector/runs/{runId}: name and note
func (h *HTTPInspector) UpdateRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "runId")
	var req api.UpdateHTTPCaptureRunRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Name != nil {
		name := truncateRunText(*req.Name, 200)
		if name == "" {
			api.WriteError(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		req.Name = &name
	}
	if req.Note != nil {
		note := truncateRunText(*req.Note, 4000)
		req.Note = &note
	}
	if err := h.store.UpdateHTTPCaptureRun(r.Context(), id, req.Name, req.Note); err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	run, err := h.store.HTTPCaptureRun(r.Context(), id)
	if err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, run)
}

// FinishRun answers POST /api/http-inspector/runs/{runId}/finish
func (h *HTTPInspector) FinishRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "runId")
	var req api.FinishHTTPCaptureRunRequest
	if r.ContentLength != 0 && !decodeBody(w, r, &req) {
		return
	}
	if _, err := h.store.HTTPCaptureRun(r.Context(), id); err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	if h.proxy == nil {
		api.WriteError(w, http.StatusConflict, "the HTTP capture proxy is not enabled")
		return
	}
	// Give open requests of the run a moment to finish
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	if err := h.proxy.FinishRun(ctx, id, req.ExitCode, req.PID); err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	run, err := h.store.HTTPCaptureRun(r.Context(), id)
	if err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, run)
}

// DeleteRun answers DELETE /api/http-inspector/runs/{runId}: the run's
// records and stored bodies; an active run is refused
func (h *HTTPInspector) DeleteRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "runId")
	run, err := h.store.HTTPCaptureRun(r.Context(), id)
	if err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	if run.State == api.CaptureRunActive {
		api.WriteError(w, http.StatusConflict, "the run is active: finish it first")
		return
	}
	if err := h.store.DeleteHTTPCaptureRun(r.Context(), id); err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	if h.blobs != nil {
		if err := h.blobs.RemoveRun(id); err != nil {
			logger.Warn("HTTP Inspector: removing the run's bodies failed", "run", id, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// exchangeFilter reads the exchange list filters
func exchangeFilter(p *params) storage.HTTPExchangeFilter {
	f := storage.HTTPExchangeFilter{
		Kind: p.Enum("kind", "", "", string(anthropicwire.KindModelInference), string(anthropicwire.KindTokenCount),
			string(anthropicwire.KindOtherAPI), string(anthropicwire.KindTransportError)),
		State: p.Enum("state", "", "", api.ExchangeInProgress, api.ExchangeComplete, api.ExchangePartial,
			api.ExchangeAborted, api.ExchangeFailed, api.ExchangeInterrupted),
		Model:     p.Str("model"),
		Stream:    p.Enum("stream", "", "", "true", "false"),
		Tool:      p.Str("tool"),
		Search:    p.Str("search"),
		MinTokens: p.Int64("minTokens", 0, 0, 1<<50),
	}
	if s := p.Str("status"); s != "" {
		if s == "error" || s == "ok" {
			f.Status = s
		} else if n, err := strconv.Atoi(s); err == nil && n >= 100 && n <= 599 {
			f.Status = s
		} else {
			p.Invalid("status", "error, ok or an HTTP status code")
		}
	}
	tr := p.OptionalTimeRange()
	f.From, f.To = tr.From, tr.To
	return f
}

// RunExchanges answers GET /api/http-inspector/runs/{runId}/exchanges
func (h *HTTPInspector) RunExchanges(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "runId")
	p := newParams(r)
	f := exchangeFilter(p)
	f.RunID = id
	page := p.PageOf(100, 500)
	if !p.Valid(w) {
		return
	}
	if _, err := h.store.HTTPCaptureRun(r.Context(), id); err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	resp, err := h.store.HTTPExchanges(r.Context(), f, page)
	if err != nil {
		writeQueryError(w, r, "failed to load exchanges", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, resp)
}

// RunTools answers GET /api/http-inspector/runs/{runId}/tools
func (h *HTTPInspector) RunTools(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "runId")
	if _, err := h.store.HTTPCaptureRun(r.Context(), id); err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	links, err := h.store.HTTPToolLinks(r.Context(), id, "")
	if err != nil {
		writeQueryError(w, r, "failed to load tool links", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, api.HTTPToolLinksResponse{Tools: links, Note: toolsNote})
}

// SessionExchanges answers GET /api/sessions/{sessionId}/http-exchanges
func (h *HTTPInspector) SessionExchanges(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionId")
	p := newParams(r)
	f := exchangeFilter(p)
	f.SessionID = sid
	page := p.PageOf(100, 500)
	if !p.Valid(w) {
		return
	}
	ctx := r.Context()
	list, err := h.store.HTTPExchanges(ctx, f, page)
	if err != nil {
		writeQueryError(w, r, "failed to load exchanges", err)
		return
	}
	sum, err := h.store.HTTPTrafficSummary(ctx, "", sid)
	if err != nil {
		writeQueryError(w, r, "failed to summarize exchanges", err)
		return
	}
	runs, err := h.store.HTTPSessionRuns(ctx, sid)
	if err != nil {
		writeQueryError(w, r, "failed to load capture runs", err)
		return
	}
	resp := api.HTTPSessionTrafficResponse{SessionID: sid, Summary: *sum, Runs: runs, HTTPExchangesResponse: *list}
	if sum.Inference > 0 {
		cmp, err := h.comparison(ctx, sid, sum)
		if err != nil {
			writeQueryError(w, r, "failed to compare sources", err)
			return
		}
		resp.Comparison = cmp
	}
	api.WriteJSON(w, http.StatusOK, resp)
}

// comparison sets the proxy's usage beside the session's telemetry: the
// same model requests are matched by request ID, never added up
func (h *HTTPInspector) comparison(ctx context.Context, sid string, sum *api.HTTPTrafficSummary) (*api.HTTPSourceComparison, error) {
	telemetry, err := h.store.HTTPSessionTelemetryRequestIDs(ctx, sid)
	if err != nil {
		return nil, err
	}
	proxyIDs, err := h.store.HTTPSessionRequestIDs(ctx, sid)
	if err != nil {
		return nil, err
	}
	c := &api.HTTPSourceComparison{
		Selected:          "telemetry",
		ProxyInputTokens:  sum.Tokens.TotalInput,
		ProxyOutputTokens: sum.Tokens.Output,
	}
	for _, id := range proxyIDs {
		if telemetry[id] {
			c.MatchedRequests++
		}
	}
	switch {
	case len(telemetry) == 0:
		c.Certainty = "none"
		c.Note = "A sessão não tem telemetria (OTLP ou arquivo) com request_id: os números do proxy aparecem só aqui e não entram nos totais do AI Observer."
	case c.MatchedRequests == int64(len(proxyIDs)) && c.MatchedRequests > 0:
		c.Certainty = "exact"
		c.Note = "Cada requisição de inferência capturada também está na telemetria (mesmo request_id): é o mesmo consumo visto por duas fontes, não somado."
	case c.MatchedRequests > 0:
		c.Certainty = "partial"
		c.Note = "Parte das requisições capturadas também está na telemetria (mesmo request_id). Os totais do AI Observer continuam vindo da telemetria; o proxy não é somado."
	default:
		c.Certainty = "none"
		c.Note = "Nenhum request_id capturado aparece na telemetria da sessão: as fontes não puderam ser casadas. Nada é somado."
	}
	return c, nil
}

// exchangeOr404 loads an exchange
func (h *HTTPInspector) exchangeOr404(w http.ResponseWriter, r *http.Request) *storage.HTTPStoredExchange {
	ex, err := h.store.HTTPExchange(r.Context(), chi.URLParam(r, "exchangeId"))
	if err != nil {
		h.notFound(w, r, "exchange", err)
		return nil
	}
	return ex
}

// GetExchange answers GET /api/http-inspector/exchanges/{exchangeId}
func (h *HTTPInspector) GetExchange(w http.ResponseWriter, r *http.Request) {
	ex := h.exchangeOr404(w, r)
	if ex == nil {
		return
	}
	rec := ex.Record
	run, err := h.store.HTTPCaptureRun(r.Context(), rec.Exchange.RunID)
	if err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	prev, next, err := h.store.HTTPExchangeNeighbors(r.Context(), rec.Exchange.RunID, rec.Exchange.Seq)
	if err != nil {
		writeQueryError(w, r, "failed to load exchange", err)
		return
	}
	obs := []anthropicwire.Observation{}
	if len(rec.Observations) > 0 {
		if err := json.Unmarshal(rec.Observations, &obs); err != nil || obs == nil {
			obs = []anthropicwire.Observation{}
		}
	}
	detail := api.HTTPExchangeDetail{
		Exchange:        rec.Exchange,
		Lifecycle:       nonNil(rec.Lifecycle),
		Observations:    obs,
		DegradedReasons: nonNil(rec.DegradedReasons),
		PrevID:          prev,
		NextID:          next,
		Run:             *run,
	}
	if ex.Usage != nil {
		detail.Exchange.Usage = &ex.Usage.HTTPUsage
	}
	api.WriteJSON(w, http.StatusOK, detail)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// GetRequest answers GET /api/http-inspector/exchanges/{exchangeId}/request
func (h *HTTPInspector) GetRequest(w http.ResponseWriter, r *http.Request) {
	h.body(w, r, httpcapture.PartRequest)
}

// GetResponse answers GET /api/http-inspector/exchanges/{exchangeId}/response
func (h *HTTPInspector) GetResponse(w http.ResponseWriter, r *http.Request) {
	h.body(w, r, httpcapture.PartResponse)
}

// body returns one side of an exchange: headers, the stored body variant
// (safe by default, raw when asked and stored) and its interpretation
func (h *HTTPInspector) body(w http.ResponseWriter, r *http.Request, part string) {
	p := newParams(r)
	variant := p.Enum("variant", api.BodyVariantSafe, api.BodyVariantSafe, api.BodyVariantRaw)
	if !p.Valid(w) {
		return
	}
	ex := h.exchangeOr404(w, r)
	if ex == nil {
		return
	}
	payloads, err := h.store.HTTPPayloads(r.Context(), ex.Record.Exchange.ID)
	if err != nil {
		writeQueryError(w, r, "failed to load exchange body", err)
		return
	}
	rec := ex.Record
	out := api.HTTPExchangeBody{
		ExchangeID: rec.Exchange.ID,
		Part:       part,
		Variant:    api.BodyVariantNone,
		Available:  []string{},
	}
	if part == httpcapture.PartRequest {
		out.Headers = nonNil(rec.RequestHeaders)
		out.ContentType, out.ContentEncoding = rec.RequestContentType, rec.RequestContentEncoding
		out.ObservedBytes = rec.Exchange.RequestBytes
		if len(rec.RequestSummary) > 0 {
			var s anthropicwire.RequestSummary
			if json.Unmarshal(rec.RequestSummary, &s) == nil {
				out.Request = &s
			}
		}
	} else {
		out.Headers = nonNil(rec.ResponseHeaders)
		out.ContentType, out.ContentEncoding = rec.ResponseContentType, rec.ResponseContentEncoding
		out.ObservedBytes = rec.Exchange.ResponseBytes
		if len(rec.ResponseMessage) > 0 {
			var m anthropicwire.Message
			if json.Unmarshal(rec.ResponseMessage, &m) == nil {
				out.Message = &m
			}
		}
	}
	var chosen *storage.HTTPPayloadRecord
	for i := range payloads {
		pl := &payloads[i]
		if pl.Part != part {
			continue
		}
		out.Available = append(out.Available, pl.Variant)
		if pl.Variant == variant {
			chosen = pl
		}
	}
	switch {
	case chosen == nil && rec.Exchange.CaptureMode == httpcapture.ModeMetadataOnly:
		out.Unavailable = "modo metadata_only: o corpo não é armazenado, apenas tamanhos, hashes e estrutura"
	case chosen == nil && variant == api.BodyVariantRaw:
		out.Unavailable = "a variante bruta só é gravada no modo full_raw"
	case chosen == nil && out.ObservedBytes == 0:
		out.Unavailable = "sem corpo"
	case chosen == nil:
		out.Unavailable = "o corpo não foi armazenado (veja os motivos de captura degradada)"
	case h.blobs == nil:
		out.Unavailable = "o diretório de captura não está disponível neste servidor"
	}
	if chosen != nil && h.blobs != nil {
		data, cut, err := h.blobs.ReadFile(chosen.RelPath, maxDisplayBody)
		if err != nil {
			out.Unavailable = "o arquivo do corpo não pôde ser lido (removido ou corrompido)"
		} else {
			out.Variant = chosen.Variant
			out.DecodeStatus = chosen.DecodeStatus
			out.ObservedBytes = chosen.ObservedBytes
			out.ObservedSHA256 = chosen.ObservedSHA256
			out.StoredBytes = chosen.StoredBytes
			out.StoredSHA256 = chosen.StoredSHA256
			out.Truncated = chosen.Truncated || cut
			out.Sanitized = chosen.Sanitized
			out.Redactions = chosen.Redactions
			if utf8.Valid(data) {
				out.BodyEncoding, out.Body = "utf8", string(data)
			} else {
				out.BodyEncoding, out.Body = "base64", base64.StdEncoding.EncodeToString(data)
			}
		}
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// GetEvents answers GET /api/http-inspector/exchanges/{exchangeId}/events
func (h *HTTPInspector) GetEvents(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	typ := p.Str("type")
	page := p.PageOf(500, 2000)
	if !p.Valid(w) {
		return
	}
	ex := h.exchangeOr404(w, r)
	if ex == nil {
		return
	}
	resp, err := h.store.HTTPSSEEvents(r.Context(), ex.Record.Exchange.ID, typ, page)
	if err != nil {
		writeQueryError(w, r, "failed to load events", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, resp)
}

// GetUsage answers GET /api/http-inspector/exchanges/{exchangeId}/usage
func (h *HTTPInspector) GetUsage(w http.ResponseWriter, r *http.Request) {
	ex := h.exchangeOr404(w, r)
	if ex == nil {
		return
	}
	if ex.Usage == nil {
		api.WriteError(w, http.StatusNotFound, "the exchange has no usage (no model reply)")
		return
	}
	api.WriteJSON(w, http.StatusOK, ex.Usage)
}

// GetDiff answers GET /api/http-inspector/exchanges/{exchangeId}/diff: the
// request compared with the previous one of the same run, kind and session,
// preferably of the same conversation (equal first message)
func (h *HTTPInspector) GetDiff(w http.ResponseWriter, r *http.Request) {
	ex := h.exchangeOr404(w, r)
	if ex == nil {
		return
	}
	rec := ex.Record
	resp := api.HTTPRequestDiffResponse{ExchangeID: rec.Exchange.ID, ComparedBy: "none"}
	var cur anthropicwire.RequestSummary
	if len(rec.RequestSummary) == 0 || json.Unmarshal(rec.RequestSummary, &cur) != nil {
		resp.Diff = anthropicwire.DiffRequests(nil, &anthropicwire.RequestSummary{})
		api.WriteJSON(w, http.StatusOK, resp)
		return
	}
	cands, err := h.store.HTTPDiffCandidates(r.Context(), rec.Exchange.RunID, rec.Exchange.Kind, rec.Exchange.SessionID, rec.Exchange.Seq, 50)
	if err != nil {
		writeQueryError(w, r, "failed to load earlier requests", err)
		return
	}
	pick := func(match func(c storage.HTTPDiffCandidate) bool) *storage.HTTPDiffCandidate {
		for i := range cands {
			if match(cands[i]) {
				return &cands[i]
			}
		}
		return nil
	}
	var chosen *storage.HTTPDiffCandidate
	if rec.FirstMessageHash != "" {
		if c := pick(func(c storage.HTTPDiffCandidate) bool { return c.FirstMessageHash == rec.FirstMessageHash }); c != nil {
			chosen, resp.ComparedBy = c, "same_conversation"
		}
	}
	if chosen == nil && cur.Model != "" {
		if c := pick(func(c storage.HTTPDiffCandidate) bool { return c.Model == cur.Model }); c != nil {
			chosen, resp.ComparedBy = c, "same_model"
		}
	}
	if chosen == nil && len(cands) > 0 {
		chosen, resp.ComparedBy = &cands[0], "previous_in_session"
	}
	var prev *anthropicwire.RequestSummary
	if chosen != nil {
		var s anthropicwire.RequestSummary
		if json.Unmarshal(chosen.Summary, &s) == nil {
			prev = &s
			resp.PreviousID, resp.PreviousSeq = chosen.ExchangeID, chosen.Seq
		} else {
			resp.ComparedBy = "none"
		}
	}
	resp.Diff = anthropicwire.DiffRequests(prev, &cur)
	api.WriteJSON(w, http.StatusOK, resp)
}

// ExportRun answers GET /api/http-inspector/runs/{runId}/export: a zip of
// the run's records and safe (redacted) bodies. Raw bodies and secret
// headers are never exported.
func (h *HTTPInspector) ExportRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "runId")
	ctx := r.Context()
	run, err := h.store.HTTPCaptureRun(ctx, id)
	if err != nil {
		h.notFound(w, r, "capture run", err)
		return
	}
	sum, err := h.store.HTTPTrafficSummary(ctx, id, "")
	if err != nil {
		writeQueryError(w, r, "failed to summarize capture run", err)
		return
	}
	links, err := h.store.HTTPToolLinks(ctx, id, "")
	if err != nil {
		writeQueryError(w, r, "failed to load tool links", err)
		return
	}
	var exchanges []api.HTTPExchange
	page := storage.Page{Limit: 500}
	for {
		res, err := h.store.HTTPExchanges(ctx, storage.HTTPExchangeFilter{RunID: id}, page)
		if err != nil {
			writeQueryError(w, r, "failed to load exchanges", err)
			return
		}
		exchanges = append(exchanges, res.Exchanges...)
		if !res.HasMore || res.NextCursor == "" {
			break
		}
		page.Cursor = res.NextCursor
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="http-capture-%s.zip"`, id))
	zw := zip.NewWriter(w)
	defer zw.Close()
	writeJSONFile := func(name string, v any) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	manifest := map[string]any{
		"format":     1,
		"exportedAt": time.Now().UTC(),
		"version":    version.Version,
		"runId":      id,
		"note":       "Exportação sanitizada: somente as variantes seguras (redigidas) dos corpos; headers de credencial nunca são gravados. Revise antes de compartilhar.",
	}
	if err := writeJSONFile("manifest.json", manifest); err != nil {
		return
	}
	if err := writeJSONFile("run.json", api.HTTPRunDetail{Run: *run, Summary: *sum}); err != nil {
		return
	}
	if err := writeJSONFile("tools.json", api.HTTPToolLinksResponse{Tools: links, Note: toolsNote}); err != nil {
		return
	}
	for _, e := range exchanges {
		stored, err := h.store.HTTPExchange(ctx, e.ID)
		if err != nil {
			logger.Warn("HTTP Inspector export: exchange not readable", "exchange", e.ID, "error", err)
			continue
		}
		rec := stored.Record
		doc := map[string]any{
			"exchange":        rec.Exchange,
			"usage":           stored.Usage,
			"requestHeaders":  nonNil(rec.RequestHeaders),
			"responseHeaders": nonNil(rec.ResponseHeaders),
			"lifecycle":       nonNil(rec.Lifecycle),
			"degradedReasons": nonNil(rec.DegradedReasons),
			"observations":    rawOrNull(rec.Observations),
			"request":         rawOrNull(rec.RequestSummary),
			"response":        rawOrNull(rec.ResponseMessage),
		}
		base := fmt.Sprintf("exchanges/%04d-%s", e.Seq, e.ID)
		if err := writeJSONFile(base+".json", doc); err != nil {
			return
		}
		if h.blobs == nil {
			continue
		}
		payloads, err := h.store.HTTPPayloads(ctx, e.ID)
		if err != nil {
			continue
		}
		for _, pl := range payloads {
			if pl.Variant != api.BodyVariantSafe {
				continue
			}
			data, _, err := h.blobs.ReadFile(pl.RelPath, 64<<20)
			if err != nil {
				continue
			}
			f, err := zw.Create(base + "." + pl.Part + ".body")
			if err != nil {
				return
			}
			if _, err := f.Write(data); err != nil {
				return
			}
		}
	}
}

func rawOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
