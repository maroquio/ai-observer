package anthropicwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
)

// maxNotes bounds Message.Notes: an adversarial stream cannot make them
// grow faster than a fixed amount; the rest is only counted
const maxNotes = 200

// wireEvent is the envelope of a Messages API stream event
type wireEvent struct {
	Type         json.RawMessage `json:"type"`
	Index        json.RawMessage `json:"index"`
	Message      json.RawMessage `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        json.RawMessage `json:"delta"`
	Usage        json.RawMessage `json:"usage"`
	Error        json.RawMessage `json:"error"`
}

// wireDelta is the delta of content_block_delta and message_delta events
type wireDelta struct {
	Type         json.RawMessage `json:"type"`
	Text         json.RawMessage `json:"text"`
	Thinking     json.RawMessage `json:"thinking"`
	Signature    json.RawMessage `json:"signature"`
	PartialJSON  json.RawMessage `json:"partial_json"`
	StopReason   json.RawMessage `json:"stop_reason"`
	StopSequence json.RawMessage `json:"stop_sequence"`
}

// blockState is a content block being reconstructed
type blockState struct {
	block      ContentBlock // metadata; text fields come from the buffers
	started    bool         // content_block_start was seen
	startInput json.RawMessage
	text       []byte
	thinking   []byte
	signature  []byte
	partial    []byte
	// otherDeltas counts deltas whose type is not interpreted
	otherDeltas map[string]int
}

// Reconstructor rebuilds a Messages API reply from its SSE events, in the
// order they were received. Message can be called at any time, including
// after an interrupted stream: it returns every fragment received so far.
// A Reconstructor is not safe for concurrent use.
type Reconstructor struct {
	msg          Message
	blocks       map[int]*blockState
	notesDropped int
}

// NewReconstructor returns an empty Reconstructor
func NewReconstructor() *Reconstructor {
	return &Reconstructor{
		msg:    Message{Streamed: true, EventCounts: map[string]int{}},
		blocks: map[int]*blockState{},
	}
}

// Add interprets one SSE event and tells what it was. The JSON "type" of
// the data decides how the event is processed; an event: field that differs
// is reported as ParseMismatch. Invalid, empty and truncated data is counted
// and noted, never interpreted.
func (r *Reconstructor) Add(ev SSEEvent) StreamEvent {
	se := StreamEvent{Seq: ev.Seq, Type: ev.Event, ParseStatus: ParseOK}
	switch {
	case ev.Truncated:
		r.flag(&se, ParseTruncated, "data truncated by the SSE event limit")
	case len(bytes.TrimSpace(ev.Data)) == 0:
		r.flag(&se, ParseEmpty, "event without data")
	case !json.Valid(ev.Data):
		r.flag(&se, ParseInvalidJSON, "data is not valid JSON")
	default:
		r.interpret(ev, &se)
	}
	r.msg.EventCounts[countKey(se.Type)]++
	return se
}

// interpret processes an event whose data is valid JSON
func (r *Reconstructor) interpret(ev SSEEvent, se *StreamEvent) {
	var w wireEvent
	if !isJSONObject(ev.Data) || json.Unmarshal(ev.Data, &w) != nil {
		r.flag(se, ParseUnexpected, "data is not a JSON object")
		return
	}
	typ := rawString(w.Type)
	if typ == "" {
		r.flag(se, ParseUnexpected, "data has no JSON type")
		return
	}
	se.Type = typ
	if ev.Event != "" && ev.Event != typ {
		r.flag(se, ParseMismatch, fmt.Sprintf("event field %q differs from JSON type %q", ev.Event, typ))
	}
	if n := rawInt(w.Index); n != nil && *n >= 0 && *n <= math.MaxInt {
		idx := int(*n)
		se.Index = &idx
	}
	switch typ {
	case "message_start":
		r.messageStart(w, se)
	case "content_block_start":
		r.blockStart(w, se)
	case "content_block_delta":
		r.blockDelta(w, se)
	case "content_block_stop":
		r.blockStop(se)
	case "message_delta":
		r.messageDelta(w)
	case "message_stop":
		r.msg.SawMessageStop = true
	case "error":
		r.msg.Error = parseErrorObject(w.Error)
	}
	if typ != "message_start" && !isJSONNull(w.Usage) {
		r.addUsage(ev.Seq, typ, w.Usage)
	}
}

// messageStart reads the message object of message_start
func (r *Reconstructor) messageStart(w wireEvent, se *StreamEvent) {
	if r.msg.SawMessageStart {
		r.flag(se, ParseUnexpected, "repeated message_start")
	}
	r.msg.SawMessageStart = true
	if !isJSONObject(w.Message) {
		r.flag(se, ParseUnexpected, "message_start without message object")
		return
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(w.Message, &top) != nil {
		return
	}
	h := readMessageHeader(top)
	r.msg.ID, r.msg.Type, r.msg.Role, r.msg.Model = h.id, h.typ, h.role, h.model
	r.msg.StopReason, r.msg.StopSequence = h.stopReason, h.stopSequence
	for k, v := range h.extra {
		if r.msg.Extra == nil {
			r.msg.Extra = map[string]json.RawMessage{}
		}
		r.msg.Extra[k] = v
	}
	// content is normally empty here; blocks it carries are taken as given
	for i, raw := range h.content {
		b := &blockState{}
		b.setStart(blockFromJSON(i, raw))
		b.block.Stopped = true
		r.blocks[i] = b
	}
	if h.usage != nil {
		r.addUsage(se.Seq, "message_start", h.usage)
	}
}

// blockStart reads content_block_start
func (r *Reconstructor) blockStart(w wireEvent, se *StreamEvent) {
	if se.Index == nil {
		r.flag(se, ParseUnexpected, "content_block_start without valid index")
		return
	}
	if !isJSONObject(w.ContentBlock) {
		r.flag(se, ParseUnexpected, "content_block_start without content_block object")
		return
	}
	b, seen := r.blocks[*se.Index]
	switch {
	case !seen:
		b = &blockState{}
		r.blocks[*se.Index] = b
	case b.started:
		r.flag(se, ParseUnexpected, fmt.Sprintf("repeated content_block_start for index %d", *se.Index))
	}
	b.setStart(blockFromJSON(*se.Index, w.ContentBlock))
}

// blockDelta reads content_block_delta
func (r *Reconstructor) blockDelta(w wireEvent, se *StreamEvent) {
	if se.Index == nil {
		r.flag(se, ParseUnexpected, "content_block_delta without valid index")
		return
	}
	var d wireDelta
	if !isJSONObject(w.Delta) || json.Unmarshal(w.Delta, &d) != nil {
		r.flag(se, ParseUnexpected, "content_block_delta without delta object")
		return
	}
	se.DeltaType = rawString(d.Type)
	b := r.blocks[*se.Index]
	switch {
	case b == nil:
		b = &blockState{block: ContentBlock{Index: *se.Index}}
		r.blocks[*se.Index] = b
		r.flag(se, ParseUnexpected, fmt.Sprintf("delta for index %d before content_block_start (placeholder block)", *se.Index))
	case b.block.Stopped:
		r.flag(se, ParseUnexpected, fmt.Sprintf("delta for index %d after content_block_stop", *se.Index))
	}
	switch se.DeltaType {
	case "text_delta":
		b.text = append(b.text, rawString(d.Text)...)
	case "thinking_delta":
		b.thinking = append(b.thinking, rawString(d.Thinking)...)
	case "signature_delta":
		b.signature = append(b.signature, rawString(d.Signature)...)
	case "input_json_delta":
		b.partial = append(b.partial, rawString(d.PartialJSON)...)
	default:
		if b.otherDeltas == nil {
			b.otherDeltas = map[string]int{}
		}
		b.otherDeltas[countKey(se.DeltaType)]++
	}
}

// blockStop reads content_block_stop
func (r *Reconstructor) blockStop(se *StreamEvent) {
	if se.Index == nil {
		r.flag(se, ParseUnexpected, "content_block_stop without valid index")
		return
	}
	b := r.blocks[*se.Index]
	switch {
	case b == nil:
		r.flag(se, ParseUnexpected, fmt.Sprintf("content_block_stop for unknown index %d", *se.Index))
	case b.block.Stopped:
		r.flag(se, ParseUnexpected, fmt.Sprintf("repeated content_block_stop for index %d", *se.Index))
	default:
		b.block.Stopped = true
	}
}

// messageDelta reads the delta of message_delta (its usage is read by
// interpret, like that of any event)
func (r *Reconstructor) messageDelta(w wireEvent) {
	var d wireDelta
	if !isJSONObject(w.Delta) || json.Unmarshal(w.Delta, &d) != nil {
		return
	}
	if len(d.StopReason) > 0 {
		r.msg.StopReason = nullableString(d.StopReason)
	}
	if len(d.StopSequence) > 0 {
		r.msg.StopSequence = nullableString(d.StopSequence)
	}
}

// addUsage merges a usage object into the cumulative usage and records the
// snapshot
func (r *Reconstructor) addUsage(seq int, typ string, raw json.RawMessage) {
	r.msg.Usage = r.msg.Usage.Merge(ParseUsage(raw))
	r.msg.UsageSnapshots = append(r.msg.UsageSnapshots, UsageSnapshot{
		EventSeq: seq, EventType: typ, Usage: r.msg.Usage.clone(),
	})
}

// flag sets the status of an event (the first problem wins) and notes it
func (r *Reconstructor) flag(se *StreamEvent, status, note string) {
	if se.ParseStatus == ParseOK {
		se.ParseStatus = status
	}
	if se.Note == "" {
		se.Note = note
	} else {
		se.Note += "; " + note
	}
	r.note(fmt.Sprintf("event %d (%s): %s", se.Seq, countKey(se.Type), note))
}

// note appends to Message.Notes within maxNotes
func (r *Reconstructor) note(s string) {
	if len(r.msg.Notes) < maxNotes {
		r.msg.Notes = append(r.msg.Notes, s)
		return
	}
	r.notesDropped++
}

// Message returns a snapshot of the reply as reconstructed so far: a fresh
// copy, which later events do not change. Blocks are ordered by index.
// Complete is set when message_stop was seen, every block was stopped and
// no error event arrived. Tool input is set only when its fragments form
// valid JSON; InputError says "invalid JSON" for a stopped block and
// "incomplete" for one the stream did not finish.
func (r *Reconstructor) Message() *Message {
	m := r.msg
	m.Usage = r.msg.Usage.clone()
	m.StopReason = clonePtr(r.msg.StopReason)
	m.StopSequence = clonePtr(r.msg.StopSequence)
	m.Error = cloneAPIError(r.msg.Error)
	m.EventCounts = maps.Clone(r.msg.EventCounts)
	m.Extra = cloneExtra(r.msg.Extra)
	m.Notes = slices.Clone(r.msg.Notes)
	m.UsageSnapshots = make([]UsageSnapshot, len(r.msg.UsageSnapshots))
	for i, s := range r.msg.UsageSnapshots {
		s.Usage = s.Usage.clone()
		m.UsageSnapshots[i] = s
	}
	indexes := slices.Sorted(maps.Keys(r.blocks))
	m.Content = make([]ContentBlock, 0, len(indexes))
	allStopped := true
	for _, idx := range indexes {
		b := r.blocks[idx]
		m.Content = append(m.Content, b.snapshot())
		allStopped = allStopped && b.block.Stopped
		if len(b.otherDeltas) > 0 {
			m.Notes = append(m.Notes, otherDeltasNote(idx, b.otherDeltas))
		}
	}
	if r.notesDropped > 0 {
		m.Notes = append(m.Notes, fmt.Sprintf("%d more notes omitted", r.notesDropped))
	}
	if len(m.Notes) == 0 {
		m.Notes = nil
	}
	m.Complete = m.SawMessageStop && allStopped && m.Error == nil
	return &m
}

// setStart takes the fields of content_block_start's block. Its initial
// text goes before text already received through deltas of a placeholder
// block; a repeated start only replaces the metadata, keeping the text.
func (b *blockState) setStart(start ContentBlock) {
	stopped := b.block.Stopped
	if !b.started {
		b.started = true
		b.text = append([]byte(start.Text), b.text...)
		b.thinking = append([]byte(start.Thinking), b.thinking...)
		b.signature = append([]byte(start.Signature), b.signature...)
	}
	b.startInput = start.Input
	start.Text, start.Thinking, start.Signature, start.Input = "", "", "", nil
	start.InputComplete = false
	b.block = start
	b.block.Stopped = stopped
}

// snapshot materializes the block from its buffers
func (b *blockState) snapshot() ContentBlock {
	c := b.block
	c.Raw = bytes.Clone(c.Raw)
	c.Text = string(b.text)
	c.Thinking = string(b.thinking)
	c.Signature = string(b.signature)
	c.PartialInput = string(b.partial)
	if !b.toolLike() {
		return c
	}
	switch {
	case json.Valid(b.partial):
		c.Input = bytes.Clone(b.partial)
		c.InputComplete = true
	case c.Stopped && len(b.partial) == 0 && isJSONObject(b.startInput):
		c.Input = bytes.Clone(b.startInput)
		c.InputComplete = true
	case c.Stopped:
		c.InputError = "invalid JSON"
	default:
		c.InputError = "incomplete"
	}
	return c
}

// toolLike tells whether the block carries tool input
func (b *blockState) toolLike() bool {
	return isToolUseType(b.block.Type) || len(b.startInput) > 0 || len(b.partial) > 0
}

// isToolUseType tells whether a block type is a tool call with an input
func isToolUseType(t string) bool {
	return t == "tool_use" || t == "server_tool_use" || t == "mcp_tool_use"
}

// otherDeltasNote describes the uninterpreted deltas of a block
func otherDeltasNote(idx int, counts map[string]int) string {
	types := slices.Sorted(maps.Keys(counts))
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = fmt.Sprintf("%d %s", counts[t], t)
	}
	return fmt.Sprintf("content index %d: deltas not interpreted: %s", idx, strings.Join(parts, ", "))
}

// countKey is the EventCounts key of a type ("unknown" when empty)
func countKey(t string) string {
	if t == "" {
		return "unknown"
	}
	return t
}

// nullableString reads a raw JSON string or null
func nullableString(raw json.RawMessage) *string {
	if isJSONNull(raw) {
		return nil
	}
	s := rawString(raw)
	return &s
}

// cloneExtra copies a map of raw values and the values themselves
func cloneExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	if extra == nil {
		return nil
	}
	c := make(map[string]json.RawMessage, len(extra))
	for k, v := range extra {
		c[k] = bytes.Clone(v)
	}
	return c
}

// cloneAPIError copies an APIError
func cloneAPIError(e *APIError) *APIError {
	if e == nil {
		return nil
	}
	c := *e
	c.Raw = bytes.Clone(e.Raw)
	return &c
}
