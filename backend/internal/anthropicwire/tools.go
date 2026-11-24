package anthropicwire

// ToolUses returns the tool_use blocks of a reply (type "tool_use" only;
// server and MCP tool calls are executed by the API, not the client), in
// content order, with MessageIndex -1 and BlockIndex the block index. Input
// is the assembled input, nil when it is not complete JSON. The result is
// never nil.
func ToolUses(m *Message) []ToolUseRef {
	refs := []ToolUseRef{}
	if m == nil {
		return refs
	}
	for _, b := range m.Content {
		if b.Type != "tool_use" {
			continue
		}
		ref := ToolUseRef{ID: b.ID, Name: b.Name, MessageIndex: -1, BlockIndex: b.Index}
		if b.InputComplete {
			ref.Input = cloneRaw(b.Input)
		}
		refs = append(refs, ref)
	}
	return refs
}
