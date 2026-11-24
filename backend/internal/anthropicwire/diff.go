package anthropicwire

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
)

// DiffRequests compares cur with the earlier request prev using only their
// summaries (hashes, sizes, tool definitions, cache breakpoints), in time
// linear in their messages and tools. A nil prev is an empty request:
// everything in cur is new. Slices of the result are never nil.
func DiffRequests(prev, cur *RequestSummary) RequestDiff {
	if prev == nil {
		prev = &RequestSummary{}
	}
	if cur == nil {
		cur = &RequestSummary{}
	}
	d := RequestDiff{
		ModelBefore:  prev.Model,
		ModelAfter:   cur.Model,
		ModelChanged: prev.Model != cur.Model,
		ParamChanges: paramChanges(prev, cur),

		SystemChanged:     prev.System.Present != cur.System.Present || prev.System.SHA256 != cur.System.SHA256,
		SystemSHA256After: cur.System.SHA256,
		SystemBytesBefore: prev.System.Bytes,
		SystemBytesAfter:  cur.System.Bytes,

		ToolsBytesBefore: prev.Tools.Bytes,
		ToolsBytesAfter:  cur.Tools.Bytes,
		ToolsCountBefore: prev.Tools.Count,
		ToolsCountAfter:  cur.Tools.Count,

		MessagesBefore: len(prev.Messages.Hashes),
		MessagesAfter:  len(cur.Messages.Hashes),

		CacheBreakpointsBefore: slices.Clone(prev.CacheBreakpoints),
		CacheBreakpointsAfter:  slices.Clone(cur.CacheBreakpoints),
	}
	d.ToolsAdded, d.ToolsRemoved, d.ToolsModified = diffTools(prev.Tools.Tools, cur.Tools.Tools)
	d.ToolsChanged = prev.Tools.SHA256 != cur.Tools.SHA256 || prev.Tools.Count != cur.Tools.Count ||
		len(d.ToolsAdded)+len(d.ToolsRemoved)+len(d.ToolsModified) > 0

	for d.CommonPrefix < d.MessagesBefore && d.CommonPrefix < d.MessagesAfter &&
		prev.Messages.Hashes[d.CommonPrefix] == cur.Messages.Hashes[d.CommonPrefix] {
		d.CommonPrefix++
	}
	d.MessagesAdded = d.MessagesAfter - d.CommonPrefix
	d.MessagesReplaced = d.MessagesBefore - d.CommonPrefix
	for i, size := range cur.Messages.Sizes {
		if i < d.CommonPrefix {
			d.RepeatedBytes += size
		} else {
			d.NewBytes += size
		}
	}
	d.PossibleCompaction = d.MessagesReplaced > 0 && d.MessagesAfter < d.MessagesBefore

	d.NewToolResults = newToolResults(prev.ToolResults, cur.ToolResults)
	d.CacheChanged = !slices.EqualFunc(prev.CacheBreakpoints, cur.CacheBreakpoints,
		func(a, b CacheBreakpoint) bool { return a.Location == b.Location && a.TTL == b.TTL })
	if d.CacheBreakpointsBefore == nil {
		d.CacheBreakpointsBefore = []CacheBreakpoint{}
	}
	if d.CacheBreakpointsAfter == nil {
		d.CacheBreakpointsAfter = []CacheBreakpoint{}
	}
	return d
}

// paramChanges lists the generation parameters that differ, as compact
// JSON, in a fixed order
func paramChanges(prev, cur *RequestSummary) []FieldChange {
	pairs := []struct {
		field         string
		before, after string
	}{
		{"max_tokens", intJSON(prev.MaxTokens), intJSON(cur.MaxTokens)},
		{"temperature", floatJSON(prev.Temperature), floatJSON(cur.Temperature)},
		{"top_p", floatJSON(prev.TopP), floatJSON(cur.TopP)},
		{"top_k", intJSON(prev.TopK), intJSON(cur.TopK)},
		{"stream", boolJSON(prev.Stream), boolJSON(cur.Stream)},
		{"thinking", compactJSON(prev.Thinking), compactJSON(cur.Thinking)},
		{"output_config", compactJSON(prev.OutputConfig), compactJSON(cur.OutputConfig)},
		{"tool_choice", compactJSON(prev.ToolChoice), compactJSON(cur.ToolChoice)},
		{"context_management", compactJSON(prev.ContextManagement), compactJSON(cur.ContextManagement)},
		{"stop_sequences", compactJSON(prev.StopSequences), compactJSON(cur.StopSequences)},
	}
	changes := []FieldChange{}
	for _, p := range pairs {
		if p.before != p.after {
			changes = append(changes, FieldChange{Field: p.field, Before: p.before, After: p.after})
		}
	}
	return changes
}

// diffTools compares tool definitions by name: added, removed and modified
// (same name, different SHA-256) names, each sorted. With repeated names
// the first definition counts.
func diffTools(prev, cur []ToolDef) (added, removed, modified []string) {
	added, removed, modified = []string{}, []string{}, []string{}
	before := make(map[string]string, len(prev))
	for _, t := range prev {
		if _, ok := before[t.Name]; !ok {
			before[t.Name] = t.SHA256
		}
	}
	after := make(map[string]string, len(cur))
	for _, t := range cur {
		if _, ok := after[t.Name]; ok {
			continue
		}
		after[t.Name] = t.SHA256
		switch sha, ok := before[t.Name]; {
		case !ok:
			added = append(added, t.Name)
		case sha != t.SHA256:
			modified = append(modified, t.Name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			removed = append(removed, name)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(modified)
	return added, removed, modified
}

// newToolResults returns the tool_use IDs of cur's tool results that prev
// did not carry, sorted and without repetition
func newToolResults(prev, cur []ToolResultRef) []string {
	seen := make(map[string]bool, len(prev))
	for _, r := range prev {
		seen[r.ToolUseID] = true
	}
	ids := []string{}
	for _, r := range cur {
		if !seen[r.ToolUseID] {
			seen[r.ToolUseID] = true
			ids = append(ids, r.ToolUseID)
		}
	}
	slices.Sort(ids)
	return ids
}

// compactJSON returns raw without insignificant whitespace ("" when
// absent; raw as is when it is not valid JSON)
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

// intJSON formats an optional integer ("" when nil)
func intJSON(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

// floatJSON formats an optional number ("" when nil)
func floatJSON(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'g', -1, 64)
}

// boolJSON formats an optional boolean ("" when nil)
func boolJSON(v *bool) string {
	if v == nil {
		return ""
	}
	return strconv.FormatBool(*v)
}
