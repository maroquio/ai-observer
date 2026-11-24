package anthropicwire

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// KnownRequestFields are the top-level request keys ParseRequest knows;
// the others are listed in RequestSummary.UnknownFields
var KnownRequestFields = []string{
	"model", "max_tokens", "temperature", "top_p", "top_k", "stream",
	"system", "messages", "tools", "tool_choice", "thinking",
	"output_config", "metadata", "context_management", "stop_sequences",
	"mcp_servers", "container", "service_tier",
}

// MaxPreviewBytes is the size limit of a stored tool result preview
// (TruncatePreview)
const MaxPreviewBytes = 300

// MaxPromptBytes is the size limit of a stored prompt (TruncatePrompt)
const MaxPromptBytes = 1000

// The parser keeps more of the prompt and of each tool result's text than is
// stored, so that redaction, which runs before they are cut, sees a secret
// crossing the stored limit whole (its rules need the whole shape: a PEM
// key's END line, a JWT's three segments)
const (
	maxPromptSourceBytes  = 16 << 10
	maxPreviewSourceBytes = 8 << 10
)

// TruncatePrompt cuts a redacted prompt to MaxPromptBytes
func TruncatePrompt(p string) string {
	return truncateUTF8(p, MaxPromptBytes)
}

// TruncatePreview cuts a redacted tool result preview to MaxPreviewBytes
func TruncatePreview(p string) string {
	return truncateUTF8(p, MaxPreviewBytes)
}

// injectedTextPrefix starts the text blocks Claude Code adds to user
// messages (context, reminders), which are not what the user typed
const injectedTextPrefix = "<system-reminder>"

// previewRawBytes is how much of an encoded string is decoded to build a
// preview: enough for maxPreviewSourceBytes even when every character is
// escaped (\uXXXX)
const previewRawBytes = maxPreviewSourceBytes * 6

// ParseRequest summarizes a POST /v1/messages (or count_tokens) body in one
// pass over it. Sizes and hashes are those of each value's bytes as sent:
// numbers and strings are never reformatted. A body that is not a JSON
// object yields Valid false and ParseError. List fields are never nil.
func ParseRequest(body []byte) RequestSummary {
	s := RequestSummary{
		Tools:            ToolsSummary{Tools: []ToolDef{}},
		Messages:         MessagesSummary{Roles: map[string]int{}, Blocks: map[string]int{}, Hashes: []string{}, Sizes: []int{}},
		ToolResults:      []ToolResultRef{},
		ToolUses:         []ToolUseRef{},
		CacheBreakpoints: []CacheBreakpoint{},
		UnknownFields:    []string{},
	}
	if !json.Valid(body) {
		s.ParseError = "invalid JSON"
		return s
	}
	// the last occurrence of a key wins, as with encoding/json
	known := map[string][]byte{}
	unknown := map[string]bool{}
	ok := forEachMember(body, func(key string, val []byte) {
		if slices.Contains(KnownRequestFields, key) {
			known[key] = val
		} else if !unknown[key] {
			unknown[key] = true
			s.UnknownFields = append(s.UnknownFields, key)
		}
	})
	if !ok {
		s.ParseError = "body is not a JSON object"
		return s
	}
	s.Valid = true
	s.Model = rawString(known["model"])
	s.MaxTokens = rawInt(known["max_tokens"])
	s.Temperature = rawFloat(known["temperature"])
	s.TopP = rawFloat(known["top_p"])
	s.TopK = rawInt(known["top_k"])
	s.Stream = rawBool(known["stream"])
	s.Thinking = cloneRaw(known["thinking"])
	s.OutputConfig = cloneRaw(known["output_config"])
	s.ToolChoice = cloneRaw(known["tool_choice"])
	s.Metadata = cloneRaw(known["metadata"])
	s.ContextManagement = cloneRaw(known["context_management"])
	s.StopSequences = cloneRaw(known["stop_sequences"])
	s.readSystem(known["system"])
	s.readTools(known["tools"])
	s.readMessages(known["messages"])
	s.SessionID = sessionIDFromMetadata(known["metadata"])
	return s
}

// readSystem summarizes the system field
func (s *RequestSummary) readSystem(raw []byte) {
	if isJSONNull(raw) {
		return
	}
	s.System.Present = true
	s.System.Bytes = len(raw)
	s.System.SHA256 = sha256Hex(raw)
	switch raw[0] {
	case '"':
		s.System.Kind = "string"
		s.System.Blocks = 1
	case '[':
		s.System.Kind = "blocks"
		forEachElem(raw, func(i int, block []byte) {
			s.System.Blocks++
			forEachMember(block, func(key string, val []byte) {
				if key == "cache_control" {
					s.addBreakpoint(val, "system[", i, "]")
				}
			})
		})
	}
}

// readTools summarizes the tools array
func (s *RequestSummary) readTools(raw []byte) {
	if isJSONNull(raw) {
		return
	}
	s.Tools.Bytes = len(raw)
	s.Tools.SHA256 = sha256Hex(raw)
	forEachElem(raw, func(i int, tool []byte) {
		def := ToolDef{Bytes: len(tool), SHA256: sha256Hex(tool)}
		forEachMember(tool, func(key string, val []byte) {
			switch key {
			case "name":
				def.Name = rawString(val)
			case "type":
				def.Type = rawString(val)
			case "defer_loading":
				def.DeferLoading = isTrue(val)
			case "cache_control":
				s.addBreakpoint(val, "tools[", i, "]")
			}
		})
		if def.DeferLoading {
			s.Tools.Deferred++
		}
		s.Tools.Tools = append(s.Tools.Tools, def)
	})
	s.Tools.Count = len(s.Tools.Tools)
}

// readMessages summarizes the messages array and the blocks inside it
func (s *RequestSummary) readMessages(raw []byte) {
	if isJSONNull(raw) {
		return
	}
	s.Messages.Bytes = len(raw)
	lastUser := -1
	var lastUserContent []byte
	forEachElem(raw, func(i int, msg []byte) {
		sum := sha256.Sum256(withoutCacheControl(msg))
		s.Messages.Hashes = append(s.Messages.Hashes, hex.EncodeToString(sum[:8]))
		s.Messages.Sizes = append(s.Messages.Sizes, len(msg))
		role := ""
		var content []byte
		forEachMember(msg, func(key string, val []byte) {
			switch key {
			case "role":
				role = rawString(val)
			case "content":
				content = val
			}
		})
		s.Messages.Roles[countKey(role)]++
		if role == "user" {
			lastUser, lastUserContent = i, content
		}
		switch {
		case len(content) > 0 && content[0] == '"':
			s.Messages.Blocks["text"]++
		case len(content) > 0 && content[0] == '[':
			forEachElem(content, func(j int, block []byte) {
				s.readContentBlock(i, j, block)
			})
		}
	})
	s.Messages.Count = len(s.Messages.Hashes)
	if lastUser >= 0 {
		s.LastUserIndex = &lastUser
		if !s.hasToolResultIn(lastUser) {
			s.Prompt = promptText(lastUserContent)
		}
	}
}

// hasToolResultIn tells whether message i has a tool_result block
func (s *RequestSummary) hasToolResultIn(i int) bool {
	for _, r := range s.ToolResults {
		if r.MessageIndex == i {
			return true
		}
	}
	return false
}

// promptText is the typed text of a user message's content (a string, or
// its text blocks except injected ones, joined by blank lines), at most
// maxPromptSourceBytes
func promptText(content []byte) string {
	const rawLimit = maxPromptSourceBytes * 6
	if len(content) == 0 {
		return ""
	}
	if content[0] == '"' {
		return truncateUTF8(strings.TrimSpace(stringPrefix(content, rawLimit)), maxPromptSourceBytes)
	}
	var parts []string
	size := 0
	forEachElem(content, func(_ int, block []byte) {
		if size > maxPromptSourceBytes {
			return
		}
		var typ string
		var text []byte
		forEachMember(block, func(key string, val []byte) {
			switch key {
			case "type":
				typ = rawString(val)
			case "text":
				text = val
			}
		})
		if typ != "text" {
			return
		}
		t := strings.TrimSpace(stringPrefix(text, rawLimit))
		if t == "" || strings.HasPrefix(t, injectedTextPrefix) {
			return
		}
		parts = append(parts, t)
		size += len(t)
	})
	return truncateUTF8(strings.Join(parts, "\n\n"), maxPromptSourceBytes)
}

// readContentBlock reads block j of message i
func (s *RequestSummary) readContentBlock(i, j int, block []byte) {
	var typ, id, name, toolUseID string
	var input, content, cacheControl []byte
	isError := false
	forEachMember(block, func(key string, val []byte) {
		switch key {
		case "type":
			typ = rawString(val)
		case "id":
			id = rawString(val)
		case "name":
			name = rawString(val)
		case "input":
			input = val
		case "tool_use_id":
			toolUseID = rawString(val)
		case "is_error":
			isError = isTrue(val)
		case "content":
			content = val
		case "cache_control":
			cacheControl = val
		}
	})
	s.Messages.Blocks[countKey(typ)]++
	loc := "messages[" + strconv.Itoa(i) + "].content["
	if cacheControl != nil {
		s.addBreakpoint(cacheControl, loc, j, "]")
	}
	switch typ {
	case "tool_use":
		s.ToolUses = append(s.ToolUses, ToolUseRef{
			ID: id, Name: name, Input: cloneRaw(input), MessageIndex: i, BlockIndex: j,
		})
	case "tool_result":
		s.ToolResults = append(s.ToolResults, ToolResultRef{
			ToolUseID: toolUseID, IsError: isError, MessageIndex: i, BlockIndex: j,
			ContentBytes: len(content),
			Preview:      s.readToolResultContent(content, loc+strconv.Itoa(j)+"].content["),
		})
	}
}

// readToolResultContent returns the preview of a tool_result's content (a
// string, or the text of its text blocks) and records the cache breakpoints
// of its blocks
func (s *RequestSummary) readToolResultContent(content []byte, loc string) string {
	if len(content) == 0 {
		return ""
	}
	var text strings.Builder
	switch content[0] {
	case '"':
		text.WriteString(stringPrefix(content, previewRawBytes))
	case '[':
		forEachElem(content, func(k int, block []byte) {
			var typ string
			var blockText []byte
			forEachMember(block, func(key string, val []byte) {
				switch key {
				case "type":
					typ = rawString(val)
				case "text":
					blockText = val
				case "cache_control":
					s.addBreakpoint(val, loc, k, "]")
				}
			})
			if typ == "text" && text.Len() <= maxPreviewSourceBytes {
				text.WriteString(stringPrefix(blockText, previewRawBytes))
			}
		})
	}
	return truncateUTF8(text.String(), maxPreviewSourceBytes)
}

// addBreakpoint records a cache_control value (an object) found at the
// location prefix+index+suffix
func (s *RequestSummary) addBreakpoint(cacheControl []byte, prefix string, index int, suffix string) {
	if !isJSONObject(cacheControl) {
		return
	}
	var w struct {
		Type json.RawMessage `json:"type"`
		TTL  json.RawMessage `json:"ttl"`
	}
	_ = json.Unmarshal(cacheControl, &w)
	s.CacheBreakpoints = append(s.CacheBreakpoints, CacheBreakpoint{
		Location: prefix + strconv.Itoa(index) + suffix,
		TTL:      rawString(w.TTL),
		Type:     rawString(w.Type),
	})
}

// sessionIDFromMetadata reads metadata.user_id
func sessionIDFromMetadata(metadata []byte) string {
	id := ""
	forEachMember(metadata, func(key string, val []byte) {
		if key == "user_id" {
			id = SessionIDFromUserID(rawString(val))
		}
	})
	return id
}

// SessionIDFromUserID extracts the session ID from metadata.user_id. Claude
// Code 2.1.x sends a JSON object encoded in the string
// ({"device_id":...,"account_uuid":...,"session_id":"<uuid>"}); older
// versions send "user_<hex>_account_<uuid>_session_<uuid>", whose part after
// the last "_session_" is taken. The result must be 1 to 128 characters of
// [A-Za-z0-9._:-]; otherwise "" is returned.
func SessionIDFromUserID(userID string) string {
	userID = strings.TrimSpace(userID)
	if strings.HasPrefix(userID, "{") {
		var w struct {
			SessionID json.RawMessage `json:"session_id"`
		}
		if json.Unmarshal([]byte(userID), &w) != nil {
			return ""
		}
		return validSessionID(rawString(w.SessionID))
	}
	if i := strings.LastIndex(userID, "_session_"); i >= 0 {
		return validSessionID(userID[i+len("_session_"):])
	}
	return ""
}

// validSessionID returns id when it looks like an identifier, else ""
func validSessionID(id string) string {
	if len(id) == 0 || len(id) > 128 {
		return ""
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == ':' || c == '-'
		if !ok {
			return ""
		}
	}
	return id
}

// sha256Hex is the hex SHA-256 of b
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cloneRaw copies a raw JSON value; nil when absent
func cloneRaw(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return bytes.Clone(raw)
}

// isTrue tells whether raw is the JSON literal true
func isTrue(raw []byte) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("true"))
}
