// Package anthropicwire interprets Anthropic Messages API traffic observed by
// the HTTP capture proxy (internal/httpproxy): endpoint classification, an
// incremental Server-Sent Events parser that accepts arbitrary byte
// fragments, reconstruction of streamed messages, usage normalization with
// nullable fields, request summaries (system prompt, tools, messages,
// tool results, cache breakpoints), tool use/result extraction, request
// diffs, body decoding and evidence-based didactic descriptions.
//
// The package is pure: it never reads files, the network or storage, and
// never changes the bytes it is given. Unknown fields and event types are
// preserved as raw JSON so newer API versions stay inspectable.
package anthropicwire

import "encoding/json"

// ExchangeKind is what an observed HTTP exchange was, by its endpoint
type ExchangeKind string

const (
	// KindModelInference is POST /v1/messages: the model generated a reply
	KindModelInference ExchangeKind = "model_inference"
	// KindTokenCount is POST /v1/messages/count_tokens: an estimate of the
	// input tokens of a request, no generation
	KindTokenCount ExchangeKind = "token_count"
	// KindQuotaProbe is a POST /v1/messages that is Claude Code's quota
	// probe (IsQuotaProbe): an auxiliary call, not a turn of the
	// conversation. Known only once the request body was read, so an
	// exchange in progress is still KindModelInference.
	KindQuotaProbe ExchangeKind = "quota_probe"
	// KindOtherAPI is any other path that went through the proxy
	KindOtherAPI ExchangeKind = "other_api"
	// KindTransportError is an exchange that got no upstream response
	KindTransportError ExchangeKind = "transport_error"
)

// SSEEvent is one Server-Sent Event as framed on the wire (WHATWG HTML
// "event stream interpretation"), before its data is interpreted
type SSEEvent struct {
	// Seq is the 0-based position of the event in the stream
	Seq int
	// Event is the value of the event: field; empty when the event had none
	Event string
	// Data is the data: lines joined with "\n" (without the final newline)
	Data []byte
	// ID is the value of the last id: field of the event, when HasID
	ID    string
	HasID bool
	// Retry is the value of a valid retry: field (milliseconds)
	Retry *int64
	// ByteOffset is the offset in the stream of the event's first line;
	// ByteLen the bytes of the event including its terminating blank line
	// (comments and lines without data in between are included)
	ByteOffset int64
	ByteLen    int64
	// Truncated is set when the data exceeded the parser's limit; Data then
	// holds its first bytes
	Truncated bool
}

// Parse status of a stream event's data (StreamEvent.ParseStatus)
const (
	ParseOK          = "ok"
	ParseInvalidJSON = "invalid_json"
	ParseEmpty       = "empty"     // no data
	ParseTruncated   = "truncated" // data cut by the parser's limit
	ParseMismatch    = "mismatch"  // event: field differs from the JSON "type"
	ParseUnexpected  = "unexpected"
)

// StreamEvent is what Reconstructor.Add read from one SSE event
type StreamEvent struct {
	Seq int
	// Type is the JSON "type" of the data, or the event: field when the
	// data is not a JSON object with a type
	Type string
	// Index is the content block index of content_block_* events
	Index *int
	// DeltaType is delta.type of content_block_delta (text_delta,
	// input_json_delta, thinking_delta, signature_delta, citations_delta, ...)
	DeltaType string
	// ParseStatus is one of the Parse* constants
	ParseStatus string
	// Note explains a status other than ParseOK
	Note string
}

// Usage is a usage object of the Messages API, normalized. A nil field was
// not reported: never treat it as 0.
type Usage struct {
	InputTokens              *int64 `json:"inputTokens,omitempty"`
	OutputTokens             *int64 `json:"outputTokens,omitempty"`
	CacheReadInputTokens     *int64 `json:"cacheReadInputTokens,omitempty"`
	CacheCreationInputTokens *int64 `json:"cacheCreationInputTokens,omitempty"`
	// CacheCreation5m and CacheCreation1h are usage.cache_creation.
	// ephemeral_5m_input_tokens / ephemeral_1h_input_tokens: parts of
	// CacheCreationInputTokens, never added to it again
	CacheCreation5m *int64 `json:"cacheCreation5m,omitempty"`
	CacheCreation1h *int64 `json:"cacheCreation1h,omitempty"`
	// ServiceTier is usage.service_tier, when present
	ServiceTier string `json:"serviceTier,omitempty"`
	// Raw is the usage object as received (for a stream, the last one
	// observed, see Message.UsageSnapshots)
	Raw json.RawMessage `json:"raw,omitempty"`
}

// UsageTotals are the totals derived from a Usage by the documented rules:
//
//	total_input     = input_tokens + cache_read_input_tokens + cache_creation_input_tokens
//	total_processed = total_input + output_tokens
//	cache_read_share = cache_read_input_tokens / total_input (total_input > 0)
//
// A total is nil when its first term (input_tokens, resp. total_input and
// output_tokens) is nil; absent cache fields are not counted, and Complete
// tells whether all three input fields were reported.
type UsageTotals struct {
	TotalInput     *int64   `json:"totalInput,omitempty"`
	TotalProcessed *int64   `json:"totalProcessed,omitempty"`
	CacheReadShare *float64 `json:"cacheReadShare,omitempty"`
	// CacheWriteUnknown is cache_creation_input_tokens minus the 5m and 1h
	// parts, when all three are known and it is above 0: cache writes whose
	// duration the API did not report. When neither part was reported it
	// is all of cache_creation_input_tokens (if above 0).
	CacheWriteUnknown *int64 `json:"cacheWriteUnknown,omitempty"`
	Complete          bool   `json:"complete"`
}

// UsageSnapshot is a usage object observed in a stream, in order
type UsageSnapshot struct {
	// EventSeq is the SSE event the usage came from
	EventSeq int `json:"eventSeq"`
	// EventType is message_start or message_delta (or another type that
	// carried usage)
	EventType string `json:"eventType"`
	// Usage is the cumulative usage after this event (merged snapshot)
	Usage Usage `json:"usage"`
}

// APIError is an error object of the API: {"type":"error","error":{...}}
// as a JSON body or as an SSE error event
type APIError struct {
	Type    string          `json:"type"`
	Message string          `json:"message"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// ContentBlock is a content block of a message, as received or
// reconstructed from content_block_* events
type ContentBlock struct {
	Index int    `json:"index"`
	Type  string `json:"type"`
	// Text of text blocks
	Text string `json:"text,omitempty"`
	// Thinking and Signature of thinking blocks; Data of redacted_thinking
	// blocks (opaque, never interpreted)
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
	// ID, Name, Input of tool_use, server_tool_use and mcp_tool_use blocks.
	// In a stream Input is set only when the concatenated input_json_delta
	// fragments (PartialInput) form valid JSON; InputError says why not.
	ID            string          `json:"id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	PartialInput  string          `json:"partialInput,omitempty"`
	InputComplete bool            `json:"inputComplete,omitempty"`
	InputError    string          `json:"inputError,omitempty"`
	// ToolUseID of result blocks such as web_search_tool_result
	ToolUseID string `json:"toolUseId,omitempty"`
	// Stopped is set when content_block_stop was received (streams), or for
	// blocks of a complete JSON message
	Stopped bool `json:"stopped"`
	// Raw is the block as received (content_block_start's block, or the
	// block of a JSON message), with fields this type does not know
	Raw json.RawMessage `json:"raw,omitempty"`
}

// Message is a reply of POST /v1/messages: parsed from a JSON body, or
// reconstructed from its SSE events
type Message struct {
	ID           string         `json:"id,omitempty"`
	Type         string         `json:"type,omitempty"`
	Role         string         `json:"role,omitempty"`
	Model        string         `json:"model,omitempty"`
	Content      []ContentBlock `json:"content"`
	StopReason   *string        `json:"stopReason,omitempty"`
	StopSequence *string        `json:"stopSequence,omitempty"`
	Usage        Usage          `json:"usage"`
	// UsageSnapshots are the usage objects of a stream in order (cumulative:
	// the last one is the reference, they are never added up)
	UsageSnapshots []UsageSnapshot `json:"usageSnapshots,omitempty"`
	// Error is an error object (JSON error body, or SSE error event)
	Error *APIError `json:"error,omitempty"`
	// Streamed tells whether the message came from SSE events
	Streamed bool `json:"streamed"`
	// SawMessageStart, SawMessageStop: stream framing observed
	SawMessageStart bool `json:"sawMessageStart"`
	SawMessageStop  bool `json:"sawMessageStop"`
	// Complete: a JSON message was parsed whole, or a stream reached
	// message_stop with every started block stopped. An interrupted stream
	// is never complete, even when it has content.
	Complete bool `json:"complete"`
	// EventCounts counts the stream's events by type
	EventCounts map[string]int `json:"eventCounts,omitempty"`
	// Notes are parse problems (invalid JSON, unexpected order, ...)
	Notes []string `json:"notes,omitempty"`
	// Extra holds top-level fields this type does not know
	Extra map[string]json.RawMessage `json:"extra,omitempty"`
}

// CacheBreakpoint is a cache_control marker in a request
type CacheBreakpoint struct {
	// Location such as "system[1]", "tools[12]", "messages[4].content[0]"
	Location string `json:"location"`
	// TTL is cache_control.ttl ("5m", "1h"), empty when not given
	TTL  string `json:"ttl,omitempty"`
	Type string `json:"type,omitempty"`
}

// SystemSummary describes the system field of a request
type SystemSummary struct {
	Present bool `json:"present"`
	// Kind is "string" or "blocks"
	Kind   string `json:"kind,omitempty"`
	Blocks int    `json:"blocks"`
	// Bytes of the system field's JSON, SHA256 of those bytes (hex)
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256,omitempty"`
}

// ToolDef is one entry of a request's tools array
type ToolDef struct {
	Name string `json:"name"`
	// Type is set for server/versioned tools (e.g. web_search_20250305)
	Type         string `json:"type,omitempty"`
	DeferLoading bool   `json:"deferLoading,omitempty"`
	Bytes        int    `json:"bytes"`
	SHA256       string `json:"sha256"`
}

// ToolsSummary describes the tools array of a request
type ToolsSummary struct {
	Count    int       `json:"count"`
	Deferred int       `json:"deferred"`
	Bytes    int       `json:"bytes"`
	SHA256   string    `json:"sha256,omitempty"`
	Tools    []ToolDef `json:"tools"`
}

// MessagesSummary describes the messages array of a request
type MessagesSummary struct {
	Count int `json:"count"`
	// Roles counts messages by role; Blocks counts content blocks by type
	// (a string content counts as one "text" block)
	Roles  map[string]int `json:"roles"`
	Blocks map[string]int `json:"blocks"`
	Bytes  int            `json:"bytes"`
	// Hashes are the SHA-256 (hex, first 16 characters) of each message's
	// JSON bytes as sent without its cache_control members, in order, for
	// prefix comparison (DiffRequests): clients move the cache breakpoint
	// to the newest message, which must not make older messages differ
	Hashes []string `json:"hashes"`
	// Sizes are the JSON bytes of each message
	Sizes []int `json:"sizes"`
}

// ToolResultRef is a tool_result block of a request
type ToolResultRef struct {
	ToolUseID    string `json:"toolUseId"`
	IsError      bool   `json:"isError"`
	MessageIndex int    `json:"messageIndex"`
	BlockIndex   int    `json:"blockIndex"`
	// ContentBytes is the size of the block's content JSON
	ContentBytes int `json:"contentBytes"`
	// Preview is the start of the result's text (at most 300 bytes, cut at
	// a UTF-8 boundary); callers sanitize it before storing
	Preview string `json:"preview,omitempty"`
}

// ToolUseRef is a tool_use block found in a request's history or a reply
type ToolUseRef struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Input        json.RawMessage `json:"input,omitempty"`
	MessageIndex int             `json:"messageIndex"`
	BlockIndex   int             `json:"blockIndex"`
}

// RequestSummary is what ParseRequest reads from a Messages API request
// body (also count_tokens requests, which share the shape)
type RequestSummary struct {
	// Valid is false when the body is not a JSON object; ParseError says why
	Valid      bool   `json:"valid"`
	ParseError string `json:"parseError,omitempty"`

	Model       string   `json:"model,omitempty"`
	MaxTokens   *int64   `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
	TopK        *int64   `json:"topK,omitempty"`
	Stream      *bool    `json:"stream,omitempty"`
	// Raw JSON of fields shown as they are
	Thinking          json.RawMessage `json:"thinking,omitempty"`
	OutputConfig      json.RawMessage `json:"outputConfig,omitempty"`
	ToolChoice        json.RawMessage `json:"toolChoice,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	ContextManagement json.RawMessage `json:"contextManagement,omitempty"`
	StopSequences     json.RawMessage `json:"stopSequences,omitempty"`

	System   SystemSummary   `json:"system"`
	Tools    ToolsSummary    `json:"tools"`
	Messages MessagesSummary `json:"messages"`

	ToolResults []ToolResultRef `json:"toolResults"`
	// ToolUses are the tool_use blocks in the request's history
	ToolUses []ToolUseRef `json:"toolUses"`

	CacheBreakpoints []CacheBreakpoint `json:"cacheBreakpoints"`

	// SessionID is the session_id in metadata.user_id (a JSON string with
	// "session_id", or the legacy "..._session_<id>" form); empty otherwise
	SessionID string `json:"sessionId,omitempty"`

	// UnknownFields are the top-level keys not in KnownRequestFields, in
	// order of appearance
	UnknownFields []string `json:"unknownFields"`
}

// Certainty of an observation: what the wire showed, or a conclusion drawn
// from it
const (
	CertaintyObserved = "observed"
	CertaintyInferred = "inferred"
)

// Observation is one evidence-based statement about an exchange, for the
// didactic view. Text is Brazilian Portuguese; it states what the wire
// showed, never what the model "thought" or what the client did locally.
type Observation struct {
	// Code is a stable identifier: assistant_text, tool_use,
	// assistant_thinking_available, server_tool_event, stop_reason,
	// api_error, tool_result_submitted, context_change, cache_breakpoint,
	// tool_definitions_provided, token_count, other_api, transport_error,
	// stream_partial, possible_compaction, unknown
	Code string `json:"code"`
	// Side is "request", "response" or "transport"
	Side      string `json:"side"`
	Certainty string `json:"certainty"`
	Text      string `json:"text"`
	// Evidence names the fields the statement is based on, such as
	// "response.content[1].type=tool_use"
	Evidence string `json:"evidence"`
}

// Body decoding status (DecodeBody)
const (
	DecodeIdentity    = "identity"    // no content coding: the bytes as they are
	DecodeDecoded     = "decoded"     // decoded from the listed codings
	DecodeUnsupported = "unsupported" // a coding this package cannot decode
	DecodeError       = "error"       // corrupt data
	DecodeTruncated   = "truncated"   // decoded output exceeded the limit
)

// FieldChange is a request parameter that differs between two requests
// (JSON values; empty when absent)
type FieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// RequestDiff compares a request with an earlier one (DiffRequests). It
// works on the summaries' fingerprints, never on the bodies, so its cost
// does not grow with the size of the conversation.
type RequestDiff struct {
	ModelBefore  string        `json:"modelBefore,omitempty"`
	ModelAfter   string        `json:"modelAfter,omitempty"`
	ModelChanged bool          `json:"modelChanged"`
	ParamChanges []FieldChange `json:"paramChanges"`

	SystemChanged     bool   `json:"systemChanged"`
	SystemSHA256After string `json:"systemSha256After,omitempty"`
	SystemBytesBefore int    `json:"systemBytesBefore"`
	SystemBytesAfter  int    `json:"systemBytesAfter"`

	ToolsChanged     bool     `json:"toolsChanged"`
	ToolsBytesBefore int      `json:"toolsBytesBefore"`
	ToolsBytesAfter  int      `json:"toolsBytesAfter"`
	ToolsAdded       []string `json:"toolsAdded"`
	ToolsRemoved     []string `json:"toolsRemoved"`
	ToolsModified    []string `json:"toolsModified"`
	ToolsCountBefore int      `json:"toolsCountBefore"`
	ToolsCountAfter  int      `json:"toolsCountAfter"`

	MessagesBefore int `json:"messagesBefore"`
	MessagesAfter  int `json:"messagesAfter"`
	// CommonPrefix is the number of leading messages with equal hashes
	CommonPrefix int `json:"commonPrefix"`
	// MessagesAdded are the messages after the common prefix in the later
	// request; MessagesReplaced the earlier request's messages after it
	// (history rewritten, e.g. by compaction)
	MessagesAdded    int `json:"messagesAdded"`
	MessagesReplaced int `json:"messagesReplaced"`
	// NewBytes are the JSON bytes of the added messages, RepeatedBytes those
	// of the common prefix (history sent again)
	NewBytes      int `json:"newBytes"`
	RepeatedBytes int `json:"repeatedBytes"`

	// NewToolResults are tool_use IDs of tool_result blocks that the earlier
	// request did not carry
	NewToolResults []string `json:"newToolResults"`

	CacheBreakpointsBefore []CacheBreakpoint `json:"cacheBreakpointsBefore"`
	CacheBreakpointsAfter  []CacheBreakpoint `json:"cacheBreakpointsAfter"`
	CacheChanged           bool              `json:"cacheChanged"`

	// PossibleCompaction: the history was rewritten (common prefix shorter
	// than the earlier request) and the message count dropped. A hint, not
	// a certainty.
	PossibleCompaction bool `json:"possibleCompaction"`
}

// DescribeInput is what Describe and Headline base their statements on
type DescribeInput struct {
	Kind   ExchangeKind
	Method string
	Path   string
	// Status is the HTTP status (0 when no response was received)
	Status int
	// State is the capture state: complete, partial, aborted, failed,
	// in_progress
	State string
	// TransportError describes a failure without upstream response
	TransportError string
	Request        *RequestSummary
	Response       *Message
	// CountTokens is the input_tokens a count_tokens response returned
	CountTokens *int64
	// SubmittedResults maps tool_use IDs whose tool_result this request
	// carries for the first time to the sequence number of the exchange
	// whose reply requested them (0 when unknown)
	SubmittedResults map[string]int
}
