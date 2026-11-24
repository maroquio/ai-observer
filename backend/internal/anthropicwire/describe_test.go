package anthropicwire

import (
	"strings"
	"testing"
)

// findObs returns the observations with a code
func findObs(obs []Observation, code string) []Observation {
	var out []Observation
	for _, o := range obs {
		if o.Code == code {
			out = append(out, o)
		}
	}
	return out
}

func checkObservations(t *testing.T, obs []Observation) {
	t.Helper()
	for _, o := range obs {
		if o.Code == "" || o.Text == "" || o.Evidence == "" || o.Side == "" ||
			(o.Certainty != CertaintyObserved && o.Certainty != CertaintyInferred) {
			t.Errorf("incomplete observation %+v", o)
		}
		for _, word := range []string{"pensou", "decidiu", "quis", "executou a ferramenta"} {
			if strings.Contains(o.Text, word) {
				t.Errorf("speculative wording %q in %q", word, o.Text)
			}
		}
	}
}

func TestDescribeTokenCount(t *testing.T) {
	in := DescribeInput{Kind: KindTokenCount, Method: "POST", Path: "/v1/messages/count_tokens", Status: 200, CountTokens: i64(1234)}
	obs := Describe(in)
	checkObservations(t, obs)
	if len(obs) != 1 || obs[0].Code != "token_count" || obs[0].Certainty != CertaintyObserved {
		t.Fatalf("obs = %+v", obs)
	}
	want := "Esta chamada apenas contou tokens de entrada (POST /v1/messages/count_tokens); não houve geração de resposta pelo modelo. O endpoint retornou 1.234 tokens de entrada estimados."
	if obs[0].Text != want || !strings.Contains(obs[0].Evidence, "path=/v1/messages/count_tokens") {
		t.Errorf("text = %q evidence = %q", obs[0].Text, obs[0].Evidence)
	}
	if h := Headline(in); h != "Contagem de tokens: 1.234 tokens de entrada" {
		t.Errorf("headline = %q", h)
	}
	in.CountTokens = nil
	if h := Headline(in); h != "Contagem de tokens" {
		t.Errorf("headline = %q", h)
	}
}

func TestDescribeToolUseReply(t *testing.T) {
	m, _ := reconstruct(readFixture(t, "stream_tool_use.sse"))
	req := ParseRequest(readFixture(t, "request_tools.json"))
	in := DescribeInput{Kind: KindModelInference, Method: "POST", Path: "/v1/messages", Status: 200, State: "complete",
		Request: &req, Response: m}
	obs := Describe(in)
	checkObservations(t, obs)
	if got := findObs(obs, "tool_definitions_provided"); len(got) != 1 ||
		got[0].Text != "O request enviou 3 definições de ferramentas (1 com carregamento adiado / defer_loading)." ||
		got[0].Evidence != "request.tools (3)" {
		t.Errorf("tool definitions = %+v", got)
	}
	if got := findObs(obs, "cache_breakpoint"); len(got) != 1 || got[0].Text != "O request marcou 3 pontos de cache (cache_control)." {
		t.Errorf("cache = %+v", got)
	}
	if got := findObs(obs, "assistant_text"); len(got) != 1 ||
		got[0].Text != "A resposta do modelo continha texto (66 caracteres)." || got[0].Evidence != "response.content[0].type=text" {
		t.Errorf("text = %+v", got)
	}
	tools := findObs(obs, "tool_use")
	if len(tools) != 2 {
		t.Fatalf("tool_use = %+v", tools)
	}
	want := "A resposta do modelo solicitou a ferramenta `Read` (ID toolu_01ABCDEFGHJKLMNPQRSTUVWX) com argumentos JSON de 67 bytes."
	if tools[0].Text != want || !strings.Contains(tools[0].Evidence, "response.content[1].type=tool_use") {
		t.Errorf("tool_use = %q / %q", tools[0].Text, tools[0].Evidence)
	}
	if got := findObs(obs, "stop_reason"); len(got) != 1 ||
		got[0].Text != "Motivo de parada: tool_use — o modelo encerrou a resposta para aguardar o resultado de ferramenta." {
		t.Errorf("stop = %+v", got)
	}
	if len(findObs(obs, "stream_partial")) != 0 || len(findObs(obs, "model_fallback")) != 0 || len(findObs(obs, "api_error")) != 0 {
		t.Errorf("unexpected observations: %+v", obs)
	}
	if h := Headline(in); h != "Inferência: o modelo solicitou 2 ferramentas (Read, Grep)" {
		t.Errorf("headline = %q", h)
	}
}

func TestDescribeToolResultSubmission(t *testing.T) {
	req := ParseRequest(readFixture(t, "request_tools.json"))
	in := DescribeInput{Kind: KindModelInference, Method: "POST", Path: "/v1/messages", Request: &req,
		SubmittedResults: map[string]int{"toolu_01REQHISTORY0000000002": 41, "toolu_01REQHISTORY0000000001": 0}}
	obs := findObs(Describe(in), "tool_result_submitted")
	checkObservations(t, obs)
	if len(obs) != 3 {
		t.Fatalf("obs = %+v", obs)
	}
	if obs[0].Text != "O request enviou o resultado (tool_result) da ferramenta cujo ID toolu_01REQHISTORY0000000001 foi solicitado." ||
		obs[0].Certainty != CertaintyObserved || obs[0].Evidence != "request.messages[2].content[0].type=tool_result tool_use_id=toolu_01REQHISTORY0000000001" {
		t.Errorf("first = %+v", obs[0])
	}
	if obs[1].Text != "O request enviou o resultado (tool_result) da ferramenta cujo ID toolu_01REQHISTORY0000000002 foi solicitado pelo exchange #41." {
		t.Errorf("second = %+v", obs[1])
	}
	if obs[2].Certainty != CertaintyInferred ||
		obs[2].Text != "A execução local da ferramenta não foi observada pelo proxy; ela é inferida pela presença do resultado neste request." {
		t.Errorf("inferred = %+v", obs[2])
	}
}

func TestDescribeTransportAndOther(t *testing.T) {
	in := DescribeInput{Kind: KindTransportError, Method: "POST", Path: "/v1/messages", TransportError: "dial tcp: connection refused"}
	obs := Describe(in)
	checkObservations(t, obs)
	if len(obs) != 1 || obs[0].Code != "transport_error" || obs[0].Side != SideTransport ||
		obs[0].Text != "O proxy não recebeu resposta do upstream: dial tcp: connection refused." {
		t.Errorf("obs = %+v", obs)
	}
	if h := Headline(in); h != "Falha de transporte: dial tcp: connection refused" {
		t.Errorf("headline = %q", h)
	}
	other := DescribeInput{Kind: KindOtherAPI, Method: "GET", Path: "/v1/models", Status: 200}
	obs = Describe(other)
	if len(obs) != 1 || obs[0].Text != "Chamada auxiliar a GET /v1/models; o conteúdo não foi interpretado como Messages API." {
		t.Errorf("other = %+v", obs)
	}
	if h := Headline(other); h != "Chamada auxiliar: GET /v1/models" {
		t.Errorf("headline = %q", h)
	}
}

func TestDescribePartialStream(t *testing.T) {
	data := readFixture(t, "stream_text.sse")
	m, _ := reconstruct(data[:len(data)*2/3])
	in := DescribeInput{Kind: KindModelInference, Method: "POST", Path: "/v1/messages", Status: 200, State: StateAborted, Response: m}
	obs := Describe(in)
	checkObservations(t, obs)
	got := findObs(obs, "stream_partial")
	if len(got) != 1 || got[0].Text != "O stream foi interrompido antes de message_stop; conteúdo e uso de tokens são parciais." ||
		!strings.Contains(got[0].Evidence, "state=aborted") {
		t.Errorf("partial = %+v", got)
	}
	if th := findObs(obs, "assistant_thinking_available"); len(th) != 1 ||
		th[0].Text != "A resposta incluiu um bloco de thinking transmitido pela API (44 caracteres)." {
		t.Errorf("thinking = %+v", th)
	}
	if h := Headline(in); !strings.HasPrefix(h, "Inferência (parcial): ") {
		t.Errorf("headline = %q", h)
	}

	// still in progress: no interruption claimed
	in.State = StateInProgress
	if len(findObs(Describe(in), "stream_partial")) != 0 {
		t.Error("in-progress stream described as interrupted")
	}
	if h := Headline(in); h != "Inferência em andamento" {
		t.Errorf("headline = %q", h)
	}

	// interrupted tool arguments
	cut := sse(ev(evMessageStart),
		ev(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01C","name":"Read","input":{}}}`),
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_pa"}}`))
	m, _ = reconstruct(cut)
	obs = Describe(DescribeInput{Kind: KindModelInference, Response: m, State: StatePartial})
	if tu := findObs(obs, "tool_use"); len(tu) != 1 ||
		tu[0].Text != "A resposta do modelo solicitou a ferramenta `Read` (ID toolu_01C), mas os argumentos JSON estão incompletos (9 bytes recebidos)." {
		t.Errorf("tool_use = %+v", tu)
	}
}

func TestDescribeErrorsAndFallback(t *testing.T) {
	m, err := ParseMessageJSON(readFixture(t, "error_529.json"))
	if err != nil {
		t.Fatal(err)
	}
	in := DescribeInput{Kind: KindModelInference, Method: "POST", Path: "/v1/messages", Status: 529, Response: m}
	obs := Describe(in)
	checkObservations(t, obs)
	if got := findObs(obs, "api_error"); len(got) != 1 || got[0].Text != "A API retornou erro overloaded_error: Overloaded." ||
		got[0].Evidence != "response.error.type=overloaded_error; status=529" {
		t.Errorf("api_error = %+v", got)
	}
	if h := Headline(in); h != "Erro 529 da API (overloaded_error)" {
		t.Errorf("headline = %q", h)
	}
	bare := DescribeInput{Kind: KindModelInference, Method: "POST", Path: "/v1/messages", Status: 500}
	if got := findObs(Describe(bare), "api_error"); len(got) != 1 || got[0].Text != "Status HTTP 500 recebido do upstream." {
		t.Errorf("status error = %+v", got)
	}
	if h := Headline(bare); h != "Erro 500 da API" {
		t.Errorf("headline = %q", h)
	}

	reply, _ := ParseMessageJSON([]byte(`{"type":"message","model":"claude-haiku-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	req := ParseRequest([]byte(`{"model":"claude-sonnet-5-5"}`))
	fb := DescribeInput{Kind: KindModelInference, Request: &req, Response: reply, Status: 200}
	if got := findObs(Describe(fb), "model_fallback"); len(got) != 1 ||
		got[0].Text != "O modelo que respondeu (claude-haiku-5) difere do solicitado (claude-sonnet-5-5)." ||
		got[0].Certainty != CertaintyObserved {
		t.Errorf("fallback = %+v", got)
	}
	if h := Headline(fb); h != "Inferência: resposta em texto (end_turn)" {
		t.Errorf("headline = %q", h)
	}
}

func TestDescribeStopReasonsAndBlocks(t *testing.T) {
	reasons := map[string]string{
		"end_turn":      "Motivo de parada: end_turn — o modelo concluiu o turno de resposta.",
		"max_tokens":    "Motivo de parada: max_tokens — a resposta atingiu o limite de tokens de saída definido no request (max_tokens).",
		"stop_sequence": "Motivo de parada: stop_sequence — a geração parou na sequência de parada `FIM`.",
		"pause_turn":    "Motivo de parada: pause_turn — a API pausou um turno longo; a continuação depende de um novo request.",
		"refusal":       "Motivo de parada: refusal — a API encerrou a resposta sinalizando recusa.",
		"warp_drive":    "Motivo de parada: warp_drive (valor não reconhecido por esta versão).",
	}
	for reason, want := range reasons {
		r := reason
		seq := "FIM"
		m := &Message{StopReason: &r, StopSequence: &seq}
		got := findObs(Describe(DescribeInput{Kind: KindModelInference, Response: m}), "stop_reason")
		if len(got) != 1 || got[0].Text != want || got[0].Evidence != "response.stop_reason="+reason {
			t.Errorf("%s: %+v", reason, got)
		}
	}
	m, _ := ParseMessageJSON([]byte(`{"type":"message","content":[
		{"type":"redacted_thinking","data":"opaque"},
		{"type":"server_tool_use","id":"srvtoolu_01W","name":"web_search","input":{"query":"x"}},
		{"type":"web_search_tool_result","tool_use_id":"srvtoolu_01W","content":[]},
		{"type":"hologram"}],"stop_reason":"end_turn"}`))
	obs := Describe(DescribeInput{Kind: KindModelInference, Response: m})
	checkObservations(t, obs)
	if got := findObs(obs, "assistant_thinking_available"); len(got) != 1 ||
		got[0].Text != "A resposta incluiu thinking redigido (redacted_thinking); o conteúdo é opaco e não pode ser lido." {
		t.Errorf("redacted = %+v", got)
	}
	if got := findObs(obs, "server_tool_event"); len(got) != 2 || !strings.Contains(got[1].Text, "web_search_tool_result") ||
		got[1].Evidence != "response.content[2].type=web_search_tool_result" {
		t.Errorf("server tools = %+v", got)
	}
	if got := findObs(obs, "unknown"); len(got) != 1 || !strings.Contains(got[0].Text, "`hologram`") {
		t.Errorf("unknown = %+v", got)
	}
	if h := Headline(DescribeInput{Kind: KindModelInference, Response: m}); h != "Inferência: resposta sem texto (end_turn)" {
		t.Errorf("headline = %q", h)
	}
}

func TestFormatInt(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 7: "7", 999: "999", 1000: "1.000", 1234: "1.234",
		1234567: "1.234.567", -1234: "-1.234", 100000: "100.000"} {
		if got := formatInt(n); got != want {
			t.Errorf("formatInt(%d) = %q, want %q", n, got, want)
		}
	}
}
