package anthropicwire

import "encoding/json"

// quotaProbeText is the whole prompt of Claude Code's quota probe
const quotaProbeText = "quota"

// IsQuotaProbe tells whether a POST /v1/messages request is Claude Code's
// quota probe: max_tokens 1, a single user message whose content is the
// text "quota" (a string or one text block), no system prompt, no tools and
// no streaming. Claude Code (2.1.x) sends it in interactive sessions logged
// in with a claude.ai subscription, to read the anthropic-ratelimit-unified-*
// headers of the response; it is not a turn of the conversation. sum is
// ParseRequest(body), checked first so that the bodies of ordinary requests
// are not decoded again.
func IsQuotaProbe(sum RequestSummary, body []byte) bool {
	if !sum.Valid || sum.MaxTokens == nil || *sum.MaxTokens != 1 || (sum.Stream != nil && *sum.Stream) ||
		sum.System.Present || sum.Tools.Count > 0 || sum.Messages.Count != 1 || sum.Messages.Roles["user"] != 1 {
		return false
	}
	var r struct {
		Tools    json.RawMessage `json:"tools"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &r) != nil || len(r.Messages) != 1 || !emptyJSON(r.Tools) {
		return false
	}
	return isQuotaProbeContent(r.Messages[0].Content)
}

// isQuotaProbeContent tells whether a message content is the text "quota"
func isQuotaProbeContent(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s == quotaProbeText
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) != 1 {
		return false
	}
	return blocks[0].Type == "text" && blocks[0].Text == quotaProbeText
}

// emptyJSON tells whether a raw JSON value is absent, null or an empty array
func emptyJSON(raw json.RawMessage) bool {
	if isJSONNull(raw) {
		return true
	}
	var list []json.RawMessage
	return json.Unmarshal(raw, &list) == nil && len(list) == 0
}
