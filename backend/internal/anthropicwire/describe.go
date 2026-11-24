package anthropicwire

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Sides of an Observation
const (
	SideRequest   = "request"
	SideResponse  = "response"
	SideTransport = "transport"
)

// Capture states of DescribeInput.State that Describe and Headline read
const (
	StatePartial    = "partial"
	StateAborted    = "aborted"
	StateInProgress = "in_progress"
)

// maxHeadlineNames and maxEvidenceItems bound lists quoted in texts
const (
	maxHeadlineNames = 5
	maxEvidenceItems = 5
)

// Describe returns deterministic statements (Brazilian Portuguese) about an
// exchange, each backed by the fields it names in Evidence. They state what
// the wire showed, marking as inferred what follows from it; they never say
// that the client ran a tool or why the model did something. Request
// statements come first, then the reply's blocks in order, its stop
// reason, errors and an interrupted stream.
func Describe(in DescribeInput) []Observation {
	obs := []Observation{}
	if in.transportFailed() {
		text := "O proxy não recebeu resposta do upstream."
		if in.TransportError != "" {
			text = "O proxy não recebeu resposta do upstream: " + strings.TrimSuffix(in.TransportError, ".") + "."
		}
		obs = append(obs, Observation{
			Code: "transport_error", Side: SideTransport, Certainty: CertaintyObserved,
			Text: text, Evidence: "transport_error; status=" + strconv.Itoa(in.Status),
		})
	}
	switch in.endpointKind() {
	case KindTokenCount:
		obs = append(obs, describeTokenCount(in))
	case KindOtherAPI:
		obs = append(obs, Observation{
			Code: "other_api", Side: SideRequest, Certainty: CertaintyObserved,
			Text:     fmt.Sprintf("Chamada auxiliar a %s %s; o conteúdo não foi interpretado como Messages API.", in.Method, in.Path),
			Evidence: fmt.Sprintf("method=%s path=%s", in.Method, in.Path),
		})
	case KindModelInference:
		obs = append(obs, describeRequest(in)...)
		obs = append(obs, describeResponse(in)...)
	}
	if o, ok := describeAPIError(in); ok {
		obs = append(obs, o)
	}
	if o, ok := describePartial(in); ok {
		obs = append(obs, o)
	}
	return obs
}

// Headline returns one short sentence (Brazilian Portuguese) summarizing an
// exchange, such as "Inferência: o modelo solicitou 2 ferramentas (Read,
// Grep)" or "Contagem de tokens: 1.234 tokens de entrada"
func Headline(in DescribeInput) string {
	if in.transportFailed() {
		if in.TransportError == "" {
			return "Falha de transporte: sem resposta do upstream"
		}
		return "Falha de transporte: " + truncateRunes(in.TransportError, 120)
	}
	if e := in.apiError(); e != nil {
		typ := e.Type
		if typ == "" {
			typ = "sem tipo"
		}
		if in.Status >= 400 {
			return fmt.Sprintf("Erro %d da API (%s)", in.Status, typ)
		}
		return fmt.Sprintf("Erro da API (%s)", typ)
	}
	if in.Status >= 400 {
		return fmt.Sprintf("Erro %d da API", in.Status)
	}
	kind := in.endpointKind()
	if in.State == StateInProgress && kind == KindModelInference {
		return "Inferência em andamento"
	}
	switch kind {
	case KindTokenCount:
		if in.CountTokens == nil {
			return "Contagem de tokens"
		}
		return "Contagem de tokens: " + formatInt(*in.CountTokens) + " tokens de entrada"
	case KindModelInference:
		return inferenceHeadline(in)
	}
	return fmt.Sprintf("Chamada auxiliar: %s %s", in.Method, in.Path)
}

// inferenceHeadline summarizes the reply of a model inference
func inferenceHeadline(in DescribeInput) string {
	prefix := "Inferência"
	if in.partial() {
		prefix = "Inferência (parcial)"
	}
	m := in.Response
	if m == nil {
		return prefix + ": resposta não interpretada"
	}
	var names []string
	hasText := false
	for _, b := range m.Content {
		switch b.Type {
		case "tool_use":
			names = append(names, b.Name)
		case "text":
			hasText = true
		}
	}
	stop := ""
	if m.StopReason != nil && *m.StopReason != "" {
		stop = " (" + *m.StopReason + ")"
	}
	switch {
	case len(names) > 0:
		return fmt.Sprintf("%s: o modelo solicitou %s (%s)", prefix,
			plural(len(names), "ferramenta", "ferramentas"), joinLimited(names, maxHeadlineNames))
	case hasText:
		return prefix + ": resposta em texto" + stop
	case len(m.Content) > 0:
		return prefix + ": resposta sem texto" + stop
	}
	return prefix + ": resposta sem conteúdo" + stop
}

// describeTokenCount states what a count_tokens exchange was
func describeTokenCount(in DescribeInput) Observation {
	o := Observation{
		Code: "token_count", Side: SideRequest, Certainty: CertaintyObserved,
		Text:     "Esta chamada apenas contou tokens de entrada (POST /v1/messages/count_tokens); não houve geração de resposta pelo modelo.",
		Evidence: "path=" + in.Path,
	}
	if in.CountTokens != nil {
		o.Text += " O endpoint retornou " + formatInt(*in.CountTokens) + " tokens de entrada estimados."
		o.Evidence += "; response.input_tokens=" + strconv.FormatInt(*in.CountTokens, 10)
	}
	return o
}

// describeRequest states what a Messages API request carried
func describeRequest(in DescribeInput) []Observation {
	var obs []Observation
	req := in.Request
	if req != nil && req.Valid {
		if n := req.Tools.Count; n > 0 {
			obs = append(obs, Observation{
				Code: "tool_definitions_provided", Side: SideRequest, Certainty: CertaintyObserved,
				Text: fmt.Sprintf("O request enviou %s (%d com carregamento adiado / defer_loading).",
					plural(n, "definição de ferramenta", "definições de ferramentas"), req.Tools.Deferred),
				Evidence: fmt.Sprintf("request.tools (%d)", n),
			})
		}
		if n := len(req.CacheBreakpoints); n > 0 {
			locs := make([]string, n)
			for i, bp := range req.CacheBreakpoints {
				locs[i] = bp.Location
			}
			obs = append(obs, Observation{
				Code: "cache_breakpoint", Side: SideRequest, Certainty: CertaintyObserved,
				Text:     fmt.Sprintf("O request marcou %s (cache_control).", plural(n, "ponto de cache", "pontos de cache")),
				Evidence: "request.cache_control: " + joinLimited(locs, maxEvidenceItems),
			})
		}
	}
	ids := slices.Sorted(maps.Keys(in.SubmittedResults))
	for _, id := range ids {
		text := fmt.Sprintf("O request enviou o resultado (tool_result) da ferramenta cujo ID %s", id)
		if seq := in.SubmittedResults[id]; seq != 0 {
			text += fmt.Sprintf(" foi solicitado pelo exchange #%d.", seq)
		} else {
			text += " foi solicitado."
		}
		obs = append(obs, Observation{
			Code: "tool_result_submitted", Side: SideRequest, Certainty: CertaintyObserved,
			Text: text, Evidence: toolResultEvidence(req, id),
		})
	}
	if len(ids) > 0 {
		obs = append(obs, Observation{
			Code: "tool_result_submitted", Side: SideRequest, Certainty: CertaintyInferred,
			Text:     "A execução local da ferramenta não foi observada pelo proxy; ela é inferida pela presença do resultado neste request.",
			Evidence: fmt.Sprintf("request.messages[].content[].type=tool_result (%d)", len(ids)),
		})
	}
	return obs
}

// toolResultEvidence locates the tool_result block of a tool_use ID
func toolResultEvidence(req *RequestSummary, id string) string {
	if req != nil {
		for _, r := range req.ToolResults {
			if r.ToolUseID == id {
				return fmt.Sprintf("request.messages[%d].content[%d].type=tool_result tool_use_id=%s",
					r.MessageIndex, r.BlockIndex, id)
			}
		}
	}
	return "submittedResults tool_use_id=" + id
}

// describeResponse states what the reply contained, block by block
func describeResponse(in DescribeInput) []Observation {
	m := in.Response
	if m == nil {
		return nil
	}
	var obs []Observation
	if in.Request != nil && in.Request.Model != "" && m.Model != "" && in.Request.Model != m.Model {
		obs = append(obs, Observation{
			Code: "model_fallback", Side: SideResponse, Certainty: CertaintyObserved,
			Text:     fmt.Sprintf("O modelo que respondeu (%s) difere do solicitado (%s).", m.Model, in.Request.Model),
			Evidence: fmt.Sprintf("request.model=%s; response.model=%s", in.Request.Model, m.Model),
		})
	}
	textChars := 0
	var textEvidence []string
	for _, b := range m.Content {
		if b.Type == "text" {
			textChars += utf8.RuneCountInString(b.Text)
			textEvidence = append(textEvidence, blockEvidence(b))
		}
	}
	textDone := false
	for _, b := range m.Content {
		if b.Type == "text" {
			if !textDone {
				textDone = true
				obs = append(obs, Observation{
					Code: "assistant_text", Side: SideResponse, Certainty: CertaintyObserved,
					Text:     fmt.Sprintf("A resposta do modelo continha texto (%s).", plural(textChars, "caractere", "caracteres")),
					Evidence: joinLimited(textEvidence, maxEvidenceItems),
				})
			}
			continue
		}
		obs = append(obs, describeBlock(b))
	}
	if m.StopReason != nil && *m.StopReason != "" {
		obs = append(obs, Observation{
			Code: "stop_reason", Side: SideResponse, Certainty: CertaintyObserved,
			Text:     stopReasonText(*m.StopReason, m.StopSequence),
			Evidence: "response.stop_reason=" + *m.StopReason,
		})
	}
	return obs
}

// describeBlock states what a non-text block of the reply was
func describeBlock(b ContentBlock) Observation {
	o := Observation{Side: SideResponse, Certainty: CertaintyObserved, Evidence: blockEvidence(b)}
	switch {
	case b.Type == "tool_use":
		o.Code = "tool_use"
		o.Evidence += fmt.Sprintf(", response.content[%d].name=%s", b.Index, b.Name)
		switch {
		case b.InputComplete:
			o.Text = fmt.Sprintf("A resposta do modelo solicitou a ferramenta `%s` (ID %s) com argumentos JSON de %s.",
				b.Name, b.ID, plural(len(b.Input), "byte", "bytes"))
		case b.Stopped:
			o.Text = fmt.Sprintf("A resposta do modelo solicitou a ferramenta `%s` (ID %s), mas os argumentos recebidos não formam JSON válido (%s).",
				b.Name, b.ID, plural(len(b.PartialInput), "byte", "bytes"))
		default:
			o.Text = fmt.Sprintf("A resposta do modelo solicitou a ferramenta `%s` (ID %s), mas os argumentos JSON estão incompletos (%s recebidos).",
				b.Name, b.ID, plural(len(b.PartialInput), "byte", "bytes"))
		}
	case b.Type == "thinking":
		o.Code = "assistant_thinking_available"
		o.Text = "A resposta incluiu um bloco de thinking sem texto transmitido pela API."
		if b.Thinking != "" {
			o.Text = fmt.Sprintf("A resposta incluiu um bloco de thinking transmitido pela API (%s).",
				plural(utf8.RuneCountInString(b.Thinking), "caractere", "caracteres"))
		}
	case b.Type == "redacted_thinking":
		o.Code = "assistant_thinking_available"
		o.Text = "A resposta incluiu thinking redigido (redacted_thinking); o conteúdo é opaco e não pode ser lido."
	case b.Type == "server_tool_use":
		o.Code = "server_tool_event"
		o.Text = fmt.Sprintf("A resposta incluiu um bloco server_tool_use (ferramenta de servidor `%s`, ID %s).", b.Name, b.ID)
	case b.Type == "mcp_tool_use":
		o.Code = "server_tool_event"
		o.Text = fmt.Sprintf("A resposta incluiu um bloco mcp_tool_use (ferramenta MCP `%s`, ID %s).", b.Name, b.ID)
	case strings.HasSuffix(b.Type, "_tool_result"):
		o.Code = "server_tool_event"
		o.Text = fmt.Sprintf("A resposta incluiu um bloco %s (resultado de ferramenta de servidor", b.Type)
		if b.ToolUseID != "" {
			o.Text += ", ID " + b.ToolUseID
		}
		o.Text += ")."
	default:
		o.Code = "unknown"
		if b.Type == "" {
			o.Text = "A resposta incluiu um bloco sem tipo, preservado sem interpretação."
		} else {
			o.Text = fmt.Sprintf("A resposta incluiu um bloco do tipo `%s`, preservado sem interpretação.", b.Type)
		}
	}
	return o
}

// stopReasonText explains a stop_reason
func stopReasonText(reason string, sequence *string) string {
	switch reason {
	case "end_turn":
		return "Motivo de parada: end_turn — o modelo concluiu o turno de resposta."
	case "tool_use":
		return "Motivo de parada: tool_use — o modelo encerrou a resposta para aguardar o resultado de ferramenta."
	case "max_tokens":
		return "Motivo de parada: max_tokens — a resposta atingiu o limite de tokens de saída definido no request (max_tokens)."
	case "stop_sequence":
		if sequence != nil && *sequence != "" {
			return fmt.Sprintf("Motivo de parada: stop_sequence — a geração parou na sequência de parada `%s`.", *sequence)
		}
		return "Motivo de parada: stop_sequence — a geração parou em uma sequência de parada."
	case "pause_turn":
		return "Motivo de parada: pause_turn — a API pausou um turno longo; a continuação depende de um novo request."
	case "refusal":
		return "Motivo de parada: refusal — a API encerrou a resposta sinalizando recusa."
	case "model_context_window_exceeded":
		return "Motivo de parada: model_context_window_exceeded — a resposta atingiu o limite da janela de contexto do modelo."
	}
	return fmt.Sprintf("Motivo de parada: %s (valor não reconhecido por esta versão).", reason)
}

// describeAPIError states an error object or an HTTP error status
func describeAPIError(in DescribeInput) (Observation, bool) {
	o := Observation{Code: "api_error", Side: SideResponse, Certainty: CertaintyObserved}
	if e := in.apiError(); e != nil {
		typ := e.Type
		if typ == "" {
			typ = "sem tipo"
		}
		if e.Message != "" {
			o.Text = fmt.Sprintf("A API retornou erro %s: %s.", typ, strings.TrimSuffix(e.Message, "."))
		} else {
			o.Text = fmt.Sprintf("A API retornou erro %s.", typ)
		}
		o.Evidence = "response.error.type=" + e.Type
		if in.Status > 0 {
			o.Evidence += "; status=" + strconv.Itoa(in.Status)
		}
		return o, true
	}
	if in.Status >= 400 {
		o.Text = fmt.Sprintf("Status HTTP %d recebido do upstream.", in.Status)
		o.Evidence = "status=" + strconv.Itoa(in.Status)
		return o, true
	}
	return o, false
}

// describePartial states that the reply was cut before its end
func describePartial(in DescribeInput) (Observation, bool) {
	m := in.Response
	if m == nil || !in.partial() {
		return Observation{}, false
	}
	var evidence []string
	if in.State == StatePartial || in.State == StateAborted {
		evidence = append(evidence, "state="+in.State)
	}
	o := Observation{Code: "stream_partial", Side: SideResponse, Certainty: CertaintyObserved}
	if m.Streamed {
		o.Text = "O stream foi interrompido antes de message_stop; conteúdo e uso de tokens são parciais."
		if !m.SawMessageStop {
			evidence = append(evidence, "response.sawMessageStop=false")
		} else {
			evidence = append(evidence, "response.complete=false")
		}
	} else {
		o.Text = "A resposta foi recebida apenas em parte; conteúdo e uso de tokens podem estar incompletos."
	}
	o.Evidence = strings.Join(evidence, "; ")
	return o, true
}

// transportFailed tells whether the exchange got no upstream response
func (in DescribeInput) transportFailed() bool {
	return in.Kind == KindTransportError || in.TransportError != ""
}

// endpointKind is the kind of endpoint, also for transport failures
func (in DescribeInput) endpointKind() ExchangeKind {
	if in.Kind == "" || in.Kind == KindTransportError {
		return ClassifyEndpoint(in.Method, in.Path)
	}
	return in.Kind
}

// apiError is the error object of the reply, if any
func (in DescribeInput) apiError() *APIError {
	if in.Response == nil {
		return nil
	}
	return in.Response.Error
}

// partial tells whether the reply is known to be cut: the capture state says
// so, or a stream did not complete. A capture still in progress is not.
func (in DescribeInput) partial() bool {
	if in.State == StateInProgress || in.Response == nil {
		return false
	}
	if in.State == StatePartial || in.State == StateAborted {
		return true
	}
	return in.Response.Streamed && !in.Response.Complete
}

// blockEvidence names a reply block by index and type
func blockEvidence(b ContentBlock) string {
	return fmt.Sprintf("response.content[%d].type=%s", b.Index, b.Type)
}

// plural formats a count with the singular or plural noun
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return "1 " + singular
	}
	return formatInt(int64(n)) + " " + pluralForm
}

// joinLimited joins at most limit items, noting how many were left out
func joinLimited(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:limit], ", ") + fmt.Sprintf(" (+%d)", len(items)-limit)
}

// truncateRunes cuts s to at most n runes, marking the cut with "…"
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}

// formatInt formats n with "." as thousands separator (1.234.567)
func formatInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	head := len(s) % 3
	if head == 0 {
		head = 3
	}
	var b strings.Builder
	b.WriteString(sign)
	b.WriteString(s[:head])
	for i := head; i < len(s); i += 3 {
		b.WriteByte('.')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
