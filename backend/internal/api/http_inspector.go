package api

import (
	"time"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
)

// Capture run modes
const (
	// CaptureRunManual groups the exchanges of the shared proxy port
	// (ANTHROPIC_BASE_URL set by hand): several clients may share it
	CaptureRunManual = "manual"
	// CaptureRunLauncher is one process started by ai-observer run, on a
	// port of its own
	CaptureRunLauncher = "launcher"
)

// Capture run states
const (
	CaptureRunActive      = "active"
	CaptureRunFinished    = "finished"
	CaptureRunInterrupted = "interrupted" // the server stopped while it was active
)

// Exchange states: what the proxy observed of an exchange
const (
	ExchangeInProgress = "in_progress"
	ExchangeComplete   = "complete"
	// ExchangePartial: the response ended without its normal end (stream
	// without message_stop, body read error), or the request body was not
	// read whole
	ExchangePartial = "partial"
	// ExchangeAborted: the client went away before the response ended
	ExchangeAborted = "aborted"
	// ExchangeFailed: no upstream response (connection, timeout)
	ExchangeFailed = "failed"
	// ExchangeInterrupted: the server stopped before the exchange ended
	ExchangeInterrupted = "interrupted"
)

// Usage quality of an exchange
const (
	UsageFinal       = "final"
	UsagePartial     = "partial"
	UsageUnavailable = "unavailable"
)

// Cost estimate status
const (
	CostComplete    = "complete"
	CostPartial     = "partial"
	CostUnavailable = "unavailable"
)

// Body variants an exchange stores (HTTPExchangeBody.Variant)
const (
	BodyVariantSafe = "safe" // redacted copy
	BodyVariantRaw  = "raw"  // as observed, local and sensitive (full_raw)
	BodyVariantNone = "none" // not stored (metadata_only, or nothing observed)
)

// Correlation states of a tool use
const (
	ToolLinkMatched    = "matched"
	ToolLinkPending    = "pending"
	ToolLinkUnresolved = "unresolved"
	ToolLinkAmbiguous  = "ambiguous"
)

// HTTPInspectorStatus describes the HTTP capture proxy (GET
// /api/http-inspector/status). It never contains credentials.
type HTTPInspectorStatus struct {
	// Enabled: the proxy runs with this server
	Enabled bool `json:"enabled"`
	// Host and Port of the shared listener (Port 0: none, launcher runs only)
	Host string `json:"host"`
	Port int    `json:"port"`
	// ProxyURL is the value for ANTHROPIC_BASE_URL (empty without a shared
	// listener)
	ProxyURL    string `json:"proxyUrl,omitempty"`
	Upstream    string `json:"upstream"`
	CaptureMode string `json:"captureMode"`
	// CaptureDir holds the stored bodies
	CaptureDir    string `json:"captureDir,omitempty"`
	MaxBodyBytes  int64  `json:"maxBodyBytes"`
	QueueSize     int    `json:"queueSize"`
	RetentionDays int    `json:"retentionDays"`
	StorageLimit  int64  `json:"storageLimit"`
	// ManualRunID is the run of the shared listener
	ManualRunID string `json:"manualRunId,omitempty"`
	ActiveRuns  int    `json:"activeRuns"`
	// Counters since the server started
	Exchanges      int64 `json:"exchanges"`
	InFlight       int64 `json:"inFlight"`
	BytesForwarded int64 `json:"bytesForwarded"`
	BytesStored    int64 `json:"bytesStored"`
	SSEEvents      int64 `json:"sseEvents"`
	// DroppedRecords are capture records lost because the queue was full;
	// LostBodyBytes body bytes not captured because the capture buffer was
	// full; StoreErrors failed writes. Traffic continued in all cases.
	DroppedRecords int64 `json:"droppedRecords"`
	LostBodyBytes  int64 `json:"lostBodyBytes"`
	StoreErrors    int64 `json:"storeErrors"`
	ParseErrors    int64 `json:"parseErrors"`
	// Degraded: some capture was lost since the start
	Degraded bool     `json:"degraded"`
	Warnings []string `json:"warnings"`
}

// HTTPCaptureRun is a capture run: the exchanges of one launched process,
// or of the shared manual port during one server start
type HTTPCaptureRun struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Note        string `json:"note,omitempty"`
	Mode        string `json:"mode"`
	State       string `json:"state"`
	CaptureMode string `json:"captureMode"`
	Port        int    `json:"port"`
	Upstream    string `json:"upstream"`
	// Cwd, Command and PID of a launched process
	Cwd       string     `json:"cwd,omitempty"`
	Command   string     `json:"command,omitempty"`
	PID       int        `json:"pid,omitempty"`
	ExitCode  *int       `json:"exitCode,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	// Exchanges observed, and the last one's time
	Exchanges      int64      `json:"exchanges"`
	LastExchangeAt *time.Time `json:"lastExchangeAt,omitempty"`
	// SessionIDs are the Claude Code session IDs the exchanges carried
	// (X-Claude-Code-Session-Id or metadata.user_id)
	SessionIDs []string `json:"sessionIds"`
	// Unattributed counts exchanges without a session ID
	Unattributed int64 `json:"unattributed"`
	// StoredBytes of the run's bodies
	StoredBytes int64 `json:"storedBytes"`
}

// HTTPCaptureRunsResponse is a page of capture runs, newest first
type HTTPCaptureRunsResponse struct {
	Runs       []HTTPCaptureRun `json:"runs"`
	Total      int64            `json:"total"`
	HasMore    bool             `json:"hasMore"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// CreateHTTPCaptureRunRequest starts a launcher run (ai-observer run): the
// server opens a proxy port for it alone
type CreateHTTPCaptureRunRequest struct {
	Name    string `json:"name,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Command string `json:"command,omitempty"`
}

// HTTPCaptureRunCreated is the started run and its proxy URL
type HTTPCaptureRunCreated struct {
	Run      HTTPCaptureRun `json:"run"`
	ProxyURL string         `json:"proxyUrl"`
}

// FinishHTTPCaptureRunRequest ends a launcher run
type FinishHTTPCaptureRunRequest struct {
	ExitCode *int `json:"exitCode,omitempty"`
	PID      int  `json:"pid,omitempty"`
}

// UpdateHTTPCaptureRunRequest renames a run or sets its note; omitted
// fields stay
type UpdateHTTPCaptureRunRequest struct {
	Name *string `json:"name,omitempty"`
	Note *string `json:"note,omitempty"`
}

// HTTPUsage is the token usage of an exchange: the API's usage object,
// normalized (nil: not reported, never 0), with derived totals and the
// estimated API-equivalent cost
type HTTPUsage struct {
	Usage  anthropicwire.Usage       `json:"usage"`
	Totals anthropicwire.UsageTotals `json:"totals"`
	// Quality: final (complete response), partial (interrupted: the last
	// cumulative values observed), unavailable (no usage)
	Quality string `json:"quality"`
	// CountTokens is the input_tokens of a count_tokens response: an
	// estimate, not inference usage
	CountTokens *int64 `json:"countTokens,omitempty"`
	// CostUSD is the API-equivalent cost estimated from the price catalog
	// (not a subscription charge); nil when not estimable
	CostUSD    *float64 `json:"costUsd,omitempty"`
	CostStatus string   `json:"costStatus"`
	CostReason string   `json:"costReason,omitempty"`
	// PricedModel is the catalog model the cost used
	PricedModel string `json:"pricedModel,omitempty"`
	// Source is always http_proxy: captured on the wire
	Source string `json:"source"`
}

// HTTPExchange is a row of the timeline: one request and its response
type HTTPExchange struct {
	ID    string `json:"id"`
	RunID string `json:"runId"`
	// Seq is the exchange's position in its run (1-based, by arrival)
	Seq int64 `json:"seq"`
	// SessionID and the evidence it came from (header or body field)
	SessionID       string `json:"sessionId,omitempty"`
	SessionEvidence string `json:"sessionEvidence,omitempty"`
	// RequestID is the upstream's request-id response header
	RequestID string `json:"requestId,omitempty"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Query     string `json:"query,omitempty"`
	// Kind: model_inference, token_count, quota_probe (Claude Code's quota
	// probe, an auxiliary call), other_api, transport_error
	Kind  string `json:"kind"`
	State string `json:"state"`
	// Status is the HTTP status the client received (0: none)
	Status         int    `json:"status"`
	RequestedModel string `json:"requestedModel,omitempty"`
	ResponseModel  string `json:"responseModel,omitempty"`
	Stream         bool   `json:"stream"`
	StopReason     string `json:"stopReason,omitempty"`
	MessageID      string `json:"messageId,omitempty"`
	// RetryCount is the client's X-Stainless-Retry-Count header
	RetryCount int       `json:"retryCount"`
	StartedAt  time.Time `json:"startedAt"`
	// Durations from the start in milliseconds (nil: not reached)
	DurationMs   *int64 `json:"durationMs,omitempty"`
	TTFBMs       *int64 `json:"ttfbMs,omitempty"`
	FirstEventMs *int64 `json:"firstEventMs,omitempty"`
	// Body sizes as observed on the wire
	RequestBytes  int64 `json:"requestBytes"`
	ResponseBytes int64 `json:"responseBytes"`
	SSEEvents     int64 `json:"sseEvents"`
	// Headline is a one-sentence, evidence-based description
	Headline string `json:"headline"`
	// ToolUses names the tools the reply requested; ToolResults counts
	// tool results the request carried for the first time
	ToolUses    []string `json:"toolUses"`
	ToolResults int      `json:"toolResults"`
	ErrorType   string   `json:"errorType,omitempty"`
	Error       string   `json:"error,omitempty"`
	// Degraded: part of this exchange was not captured (traffic was not
	// affected)
	Degraded    bool       `json:"degraded"`
	CaptureMode string     `json:"captureMode"`
	Usage       *HTTPUsage `json:"usage,omitempty"`
}

// HTTPExchangesResponse is a page of exchanges in run order
type HTTPExchangesResponse struct {
	Exchanges  []HTTPExchange `json:"exchanges"`
	Total      int64          `json:"total"`
	HasMore    bool           `json:"hasMore"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

// HTTPLifecycleEvent is a transport event of an exchange
type HTTPLifecycleEvent struct {
	// Event: request_started, request_body_observed, upstream_connected,
	// response_headers_received, response_streaming, completed,
	// upstream_error, client_aborted, upstream_aborted, capture_degraded,
	// stream_parse_error
	Event  string    `json:"event"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"`
}

// HTTPExchangeDetail is an exchange with its transport lifecycle and
// interpretation (GET /api/http-inspector/exchanges/{id})
type HTTPExchangeDetail struct {
	Exchange     HTTPExchange                `json:"exchange"`
	Lifecycle    []HTTPLifecycleEvent        `json:"lifecycle"`
	Observations []anthropicwire.Observation `json:"observations"`
	// DegradedReasons explain what was not captured
	DegradedReasons []string `json:"degradedReasons"`
	// Previous and next exchange of the run, for navigation
	PrevID string `json:"prevId,omitempty"`
	NextID string `json:"nextId,omitempty"`
	// Run is the exchange's run
	Run HTTPCaptureRun `json:"run"`
}

// HTTPHeader is a header as stored: secret values are never stored
type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Redacted: the value was replaced (credentials, cookies)
	Redacted bool `json:"redacted"`
}

// HTTPExchangeBody is one side of an exchange: headers and body
type HTTPExchangeBody struct {
	ExchangeID string `json:"exchangeId"`
	// Part is request or response
	Part    string       `json:"part"`
	Headers []HTTPHeader `json:"headers"`
	// Variant of the body returned: safe (redacted), raw (as observed,
	// full_raw only) or none
	Variant string `json:"variant"`
	// Available lists the stored variants
	Available []string `json:"available"`
	// ContentType and ContentEncoding headers; DecodeStatus says how the
	// body was decoded for display (identity, decoded, unsupported, error)
	ContentType     string `json:"contentType,omitempty"`
	ContentEncoding string `json:"contentEncoding,omitempty"`
	DecodeStatus    string `json:"decodeStatus,omitempty"`
	// ObservedBytes and ObservedSHA256 describe the bytes on the wire
	// (encoded); the SHA-256 is empty when bytes were lost
	ObservedBytes  int64  `json:"observedBytes"`
	ObservedSHA256 string `json:"observedSha256,omitempty"`
	// StoredBytes and StoredSHA256 describe the returned variant (decoded)
	StoredBytes  int64  `json:"storedBytes"`
	StoredSHA256 string `json:"storedSha256,omitempty"`
	// Truncated: the body exceeded the storage limit; only its start is
	// stored (the traffic was forwarded whole)
	Truncated bool `json:"truncated"`
	// Sanitized: redaction changed the stored safe variant
	Sanitized bool `json:"sanitized"`
	// Redactions counts the replaced values
	Redactions int `json:"redactions"`
	// BodyEncoding is utf8 or base64 (bytes that are no valid UTF-8)
	BodyEncoding string `json:"bodyEncoding,omitempty"`
	Body         string `json:"body,omitempty"`
	// Request is the interpreted request (part request, Messages API)
	Request *anthropicwire.RequestSummary `json:"request,omitempty"`
	// Message is the interpreted reply (part response, Messages API)
	Message *anthropicwire.Message `json:"message,omitempty"`
	// Unavailable explains a missing body (metadata_only, not captured)
	Unavailable string `json:"unavailable,omitempty"`
}

// HTTPSSEEvent is one Server-Sent Event of a streamed response
type HTTPSSEEvent struct {
	Seq int `json:"seq"`
	// OffsetMs is the time since the request started
	OffsetMs   float64 `json:"offsetMs"`
	ByteOffset int64   `json:"byteOffset"`
	ByteLen    int64   `json:"byteLen"`
	// Event is the event: field; Type the JSON "type"
	Event       string `json:"event,omitempty"`
	Type        string `json:"type"`
	Index       *int   `json:"index,omitempty"`
	DeltaType   string `json:"deltaType,omitempty"`
	ParseStatus string `json:"parseStatus"`
	Note        string `json:"note,omitempty"`
	// Data is the event's data (redacted in safe mode, empty in
	// metadata_only)
	Data      string `json:"data,omitempty"`
	DataBytes int64  `json:"dataBytes"`
	Truncated bool   `json:"truncated"`
}

// HTTPSSEEventsResponse is a page of an exchange's SSE events in order
type HTTPSSEEventsResponse struct {
	Events     []HTTPSSEEvent `json:"events"`
	Total      int64          `json:"total"`
	HasMore    bool           `json:"hasMore"`
	NextCursor string         `json:"nextCursor,omitempty"`
	// Types counts all events of the exchange by type
	Types map[string]int64 `json:"types"`
}

// HTTPUsageDetail is the usage of one exchange with its stream snapshots
type HTTPUsageDetail struct {
	ExchangeID string `json:"exchangeId"`
	Kind       string `json:"kind"`
	Model      string `json:"model,omitempty"`
	HTTPUsage
	// Snapshots are the usage objects of the stream in order (cumulative)
	Snapshots []anthropicwire.UsageSnapshot `json:"snapshots"`
	// CostComponents itemize the estimated cost
	CostComponents []HTTPCostComponent `json:"costComponents"`
}

// HTTPCostComponent is one priced part of an exchange's usage
type HTTPCostComponent struct {
	// Component: input, output, cache_read, cache_write_5m, cache_write_1h,
	// cache_write_unknown
	Component string `json:"component"`
	Tokens    int64  `json:"tokens"`
	// PricePerMTok is the price per million tokens: the catalog's, or
	// derived from it (cache_write_1h: twice the input price, see Note);
	// nil: no price
	PricePerMTok *float64 `json:"pricePerMTok,omitempty"`
	CostUSD      *float64 `json:"costUsd,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// HTTPToolLink is a tool use requested by a reply and the request that
// carried its result
type HTTPToolLink struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	// UseExchangeID is the exchange whose reply requested the tool
	UseExchangeID string `json:"useExchangeId,omitempty"`
	UseSeq        int64  `json:"useSeq,omitempty"`
	// Input is the tool's arguments (redacted; empty in metadata_only)
	Input string `json:"input,omitempty"`
	// ResultExchangeID is the first exchange whose request carried the
	// tool_result with this ID
	ResultExchangeID string `json:"resultExchangeId,omitempty"`
	ResultSeq        int64  `json:"resultSeq,omitempty"`
	ResultIsError    bool   `json:"resultIsError"`
	ResultPreview    string `json:"resultPreview,omitempty"`
	ResultBytes      int64  `json:"resultBytes"`
	// Status: matched, pending, unresolved, ambiguous
	Status string `json:"status"`
	// Evidence names how the link was made
	Evidence string `json:"evidence"`
}

// HTTPToolLinksResponse lists a run's tool uses in order
type HTTPToolLinksResponse struct {
	Tools []HTTPToolLink `json:"tools"`
	// Note reminds that local execution is not observed
	Note string `json:"note"`
}

// HTTPRequestDiffResponse compares a request with an earlier one of the
// same session and kind
type HTTPRequestDiffResponse struct {
	ExchangeID string `json:"exchangeId"`
	// PreviousID is the compared exchange (empty: none, everything is new)
	PreviousID  string `json:"previousId,omitempty"`
	PreviousSeq int64  `json:"previousSeq,omitempty"`
	// ComparedBy says how the previous exchange was chosen:
	// same_conversation (equal first message), same_model,
	// previous_in_session, none
	ComparedBy string                    `json:"comparedBy"`
	Diff       anthropicwire.RequestDiff `json:"diff"`
}

// HTTPTrafficSummary aggregates exchanges (a run, or a session)
type HTTPTrafficSummary struct {
	Exchanges   int64 `json:"exchanges"`
	Inference   int64 `json:"inference"`
	TokenCounts int64 `json:"tokenCounts"`
	// QuotaProbes are Claude Code's quota probes: auxiliary calls, left out
	// of Inference, Errors, the token totals and the comparison
	QuotaProbes int64 `json:"quotaProbes"`
	Other       int64 `json:"other"`
	// Errors: status >= 400 or no response, quota probes left out
	Errors     int64 `json:"errors"`
	Retries    int64 `json:"retries"`
	Partial    int64 `json:"partial"`
	InProgress int64 `json:"inProgress"`
	Streams    int64 `json:"streams"`
	// Tokens of inference exchanges with final usage; PartialTokens of
	// those with partial usage, kept apart
	Tokens        HTTPTokenTotals `json:"tokens"`
	PartialTokens HTTPTokenTotals `json:"partialTokens"`
	// CountedTokens sums count_tokens results (estimates, not usage)
	CountedTokens int64 `json:"countedTokens"`
	// CostUSD is the estimated API-equivalent cost of the priced exchanges;
	// CostStatus is partial when some were not priced
	CostUSD    float64 `json:"costUsd"`
	CostStatus string  `json:"costStatus"`
	// DurationMs from the first request to the last response
	DurationMs int64      `json:"durationMs"`
	FirstAt    *time.Time `json:"firstAt,omitempty"`
	LastAt     *time.Time `json:"lastAt,omitempty"`
	Models     []string   `json:"models"`
	Degraded   int64      `json:"degraded"`
}

// HTTPTokenTotals are summed usage fields; CacheReadShare is cache reads
// over total input tokens
type HTTPTokenTotals struct {
	Input           int64    `json:"input"`
	Output          int64    `json:"output"`
	CacheRead       int64    `json:"cacheRead"`
	CacheWrite      int64    `json:"cacheWrite"`
	CacheWrite5m    int64    `json:"cacheWrite5m"`
	CacheWrite1h    int64    `json:"cacheWrite1h"`
	CacheWriteOther int64    `json:"cacheWriteOther"`
	TotalInput      int64    `json:"totalInput"`
	TotalProcessed  int64    `json:"totalProcessed"`
	CacheReadShare  *float64 `json:"cacheReadShare,omitempty"`
	// Exchanges counted
	Exchanges int64 `json:"exchanges"`
}

// HTTPSourceComparison sets the proxy's usage of a session beside what
// OTLP and session files recorded, without adding them up
type HTTPSourceComparison struct {
	// Selected is the source of the session totals shown elsewhere in AI
	// Observer (otlp/local_jsonl, unchanged by the proxy)
	Selected string `json:"selected"`
	// Telemetry usage of the session (OTLP or session file, deduplicated
	// by AI Observer's existing rules, as the transcript totals it), input
	// counted like ProxyInputTokens (uncached + cache read + cache write);
	// nil when unknown
	TelemetryInputTokens  *int64   `json:"telemetryInputTokens,omitempty"`
	TelemetryOutputTokens *int64   `json:"telemetryOutputTokens,omitempty"`
	TelemetryCostUSD      *float64 `json:"telemetryCostUsd,omitempty"`
	// ProxyInputTokens / ProxyOutputTokens: the proxy's final usage
	// (input = uncached + cache read + cache write)
	ProxyInputTokens  int64 `json:"proxyInputTokens"`
	ProxyOutputTokens int64 `json:"proxyOutputTokens"`
	// MatchedRequests: exchanges whose request-id also appears in the
	// session's telemetry (same model request seen by both)
	MatchedRequests int64 `json:"matchedRequests"`
	// Certainty: exact (every inference exchange matched), partial, none
	Certainty string `json:"certainty"`
	Note      string `json:"note"`
}

// HTTPSessionTrafficResponse is the API traffic of a Claude Code session
// (GET /api/sessions/{sessionId}/http-exchanges)
type HTTPSessionTrafficResponse struct {
	SessionID string             `json:"sessionId"`
	Summary   HTTPTrafficSummary `json:"summary"`
	// Runs that carried the session's exchanges
	Runs       []HTTPCaptureRun      `json:"runs"`
	Comparison *HTTPSourceComparison `json:"comparison,omitempty"`
	HTTPExchangesResponse
}

// HTTPNoSession stands for the exchanges without a session in the HTTP
// Inspector's session paths (/api/http-inspector/sessions/none/turns)
const HTTPNoSession = "none"

// HTTPInspectorSession is a session with captured traffic, as the HTTP
// Inspector lists it (SessionID "" groups the exchanges without a session)
type HTTPInspectorSession struct {
	SessionID string `json:"sessionId"`
	// Title is the typed prompt of the session's first turn (redacted);
	// empty when no turn was found or the capture holds no content
	Title     string    `json:"title,omitempty"`
	FirstAt   time.Time `json:"firstAt"`
	LastAt    time.Time `json:"lastAt"`
	Exchanges int64     `json:"exchanges"`
	Inference int64     `json:"inference"`
	// Turns deduced from the requests (see HTTPSessionTurnsResponse)
	Turns      int64 `json:"turns"`
	Errors     int64 `json:"errors"`
	InProgress int64 `json:"inProgress"`
	// TotalInputTokens and OutputTokens of the model inferences (final and
	// partial usage), CostUSD their estimated API-equivalent cost
	TotalInputTokens int64    `json:"totalInputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	CostUSD          float64  `json:"costUsd"`
	CostStatus       string   `json:"costStatus"`
	Models           []string `json:"models"`
	// Runs is the number of capture runs that carried the session
	Runs int64 `json:"runs"`
}

// HTTPInspectorSessionsResponse is a page of the sessions with captured
// traffic, the most recently active first
type HTTPInspectorSessionsResponse struct {
	Sessions   []HTTPInspectorSession `json:"sessions"`
	Total      int64                  `json:"total"`
	HasMore    bool                   `json:"hasMore"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

// HTTPTurn is a turn of a session's captured traffic: a typed prompt and
// the exchanges until the next one (the tool loop's requests, subagents,
// count_tokens and other auxiliary calls, by start time). Numbers follow
// HTTPTrafficSummary: errors leave out quota probes, tokens and cost come
// from model inferences, final usage apart from partial.
type HTTPTurn struct {
	// Index is 1-based; 0 is the preamble (exchanges before the first turn)
	Index int `json:"index"`
	// Prompt is the start of the typed text (redacted); empty for the
	// preamble and when the capture holds no content (metadata_only, or
	// captured by an earlier version)
	Prompt     string         `json:"prompt,omitempty"`
	StartedAt  time.Time      `json:"startedAt"`
	EndedAt    time.Time      `json:"endedAt"`
	DurationMs int64          `json:"durationMs"`
	Exchanges  []HTTPExchange `json:"exchanges"`
	// Inference counts the model inferences, Auxiliary the other exchanges
	Inference int64 `json:"inference"`
	Auxiliary int64 `json:"auxiliary"`
	Errors    int64 `json:"errors"`
	// ToolUses are the tools the model requested, in order
	ToolUses      []string        `json:"toolUses"`
	Tokens        HTTPTokenTotals `json:"tokens"`
	PartialTokens HTTPTokenTotals `json:"partialTokens"`
	CostUSD       float64         `json:"costUsd"`
	CostStatus    string          `json:"costStatus"`
	Models        []string        `json:"models"`
}

// HTTPSessionTurnsResponse is a session's captured traffic grouped into
// turns, deduced from the requests (the API has no turn ID; Note says how)
type HTTPSessionTurnsResponse struct {
	// SessionID is "" for the exchanges without a session
	SessionID string             `json:"sessionId"`
	Summary   HTTPTrafficSummary `json:"summary"`
	// Runs that carried the session's exchanges
	Runs       []HTTPCaptureRun      `json:"runs"`
	Comparison *HTTPSourceComparison `json:"comparison,omitempty"`
	// Preamble holds the exchanges before the first turn (startup checks,
	// the quota probe); nil when there are none
	Preamble *HTTPTurn  `json:"preamble,omitempty"`
	Turns    []HTTPTurn `json:"turns"`
	// Truncated is true when the session has more exchanges than are
	// grouped (the newest are left out; Summary counts all of them)
	Truncated bool   `json:"truncated"`
	Note      string `json:"note"`
}

// HTTPRunDetail is a run with its summary
type HTTPRunDetail struct {
	Run     HTTPCaptureRun     `json:"run"`
	Summary HTTPTrafficSummary `json:"summary"`
}
