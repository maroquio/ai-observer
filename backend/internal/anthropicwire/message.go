package anthropicwire

import (
	"bytes"
	"encoding/json"
	"errors"
)

// messageHeader is the part of a message object outside its content blocks
type messageHeader struct {
	id, typ, role, model     string
	stopReason, stopSequence *string
	content                  []json.RawMessage
	usage                    json.RawMessage
	extra                    map[string]json.RawMessage
}

// readMessageHeader reads the fields of a message object; keys it does not
// know go to extra
func readMessageHeader(top map[string]json.RawMessage) messageHeader {
	var h messageHeader
	for k, v := range top {
		switch k {
		case "id":
			h.id = rawString(v)
		case "type":
			h.typ = rawString(v)
		case "role":
			h.role = rawString(v)
		case "model":
			h.model = rawString(v)
		case "stop_reason":
			h.stopReason = nullableString(v)
		case "stop_sequence":
			h.stopSequence = nullableString(v)
		case "content":
			if isJSONArray(v) {
				_ = json.Unmarshal(v, &h.content)
			}
		case "usage":
			if !isJSONNull(v) {
				h.usage = v
			}
		default:
			if h.extra == nil {
				h.extra = map[string]json.RawMessage{}
			}
			h.extra[k] = v
		}
	}
	return h
}

// wireBlock is a content block with every value kept raw
type wireBlock struct {
	Type      json.RawMessage `json:"type"`
	Text      json.RawMessage `json:"text"`
	Thinking  json.RawMessage `json:"thinking"`
	Signature json.RawMessage `json:"signature"`
	Data      json.RawMessage `json:"data"`
	ID        json.RawMessage `json:"id"`
	Name      json.RawMessage `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID json.RawMessage `json:"tool_use_id"`
}

// blockFromJSON reads a content block of any type; Raw keeps it whole and
// Input is the raw "input" value when present
func blockFromJSON(index int, raw json.RawMessage) ContentBlock {
	b := ContentBlock{Index: index, Raw: bytes.Clone(raw)}
	var w wireBlock
	if !isJSONObject(raw) || json.Unmarshal(raw, &w) != nil {
		return b
	}
	b.Type = rawString(w.Type)
	b.Text = rawString(w.Text)
	b.Thinking = rawString(w.Thinking)
	b.Signature = rawString(w.Signature)
	b.Data = rawString(w.Data)
	b.ID = rawString(w.ID)
	b.Name = rawString(w.Name)
	b.ToolUseID = rawString(w.ToolUseID)
	if !isJSONNull(w.Input) {
		b.Input = bytes.Clone(w.Input)
	}
	return b
}

// ParseMessageJSON reads a non-streaming reply of POST /v1/messages. A
// message object yields its blocks with Stopped set, tool input as sent
// (InputComplete), and Complete when its type is "message". An error object
// {"type":"error","error":{...}} yields a Message with Error set and Complete
// false. Top-level fields this package does not know go to Extra. A body
// that is not a JSON object is an error.
func ParseMessageJSON(body []byte) (*Message, error) {
	if !json.Valid(body) {
		return nil, errors.New("message: invalid JSON")
	}
	if !isJSONObject(body) {
		return nil, errors.New("message: body is not a JSON object")
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, err
	}
	h := readMessageHeader(top)
	m := &Message{
		ID: h.id, Type: h.typ, Role: h.role, Model: h.model,
		StopReason: h.stopReason, StopSequence: h.stopSequence,
		Content: []ContentBlock{},
		Extra:   h.extra,
	}
	if h.typ == "error" {
		m.Error = parseErrorObject(top["error"])
		delete(m.Extra, "error")
		if len(m.Extra) == 0 {
			m.Extra = nil
		}
		return m, nil
	}
	for i, raw := range h.content {
		b := blockFromJSON(i, raw)
		b.Stopped = true
		b.InputComplete = b.Input != nil
		m.Content = append(m.Content, b)
	}
	if h.usage != nil {
		m.Usage = ParseUsage(h.usage)
	}
	m.Complete = h.typ == "message"
	return m, nil
}

// ParseAPIError reads an error body {"type":"error","error":{...}}: Type and
// Message come from the inner error object, which Raw keeps. It returns nil
// for anything else.
func ParseAPIError(body []byte) *APIError {
	if !json.Valid(body) || !isJSONObject(body) {
		return nil
	}
	var w struct {
		Type  json.RawMessage `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &w) != nil || rawString(w.Type) != "error" {
		return nil
	}
	return parseErrorObject(w.Error)
}

// parseErrorObject reads the inner error object of an error body or event;
// it never returns nil
func parseErrorObject(raw json.RawMessage) *APIError {
	e := &APIError{}
	if isJSONNull(raw) {
		return e
	}
	e.Raw = bytes.Clone(raw)
	var w struct {
		Type    json.RawMessage `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if isJSONObject(raw) && json.Unmarshal(raw, &w) == nil {
		e.Type = rawString(w.Type)
		e.Message = rawString(w.Message)
	}
	return e
}

// isJSONArray tells whether raw starts like a JSON array
func isJSONArray(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '['
}
