package anthropicwire

import (
	"strings"
	"testing"
)

// probeBody is the quota probe as Claude Code 2.1.331 sent it (metadata
// shortened)
const probeBody = `{"model":"claude-haiku-5-5","max_tokens":1,"messages":[{"role":"user","content":"quota"}],"metadata":{"user_id":"{\"session_id\":\"s1\"}"}}`

func TestIsQuotaProbe(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"probe", probeBody, true},
		{"text block", `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"quota"}]}]}`, true},
		{"stream false, empty tools, extra fields", `{"model":"m","max_tokens":1,"stream":false,"tools":[],"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"quota"}]}`, true},
		{"max_tokens 2", strings.Replace(probeBody, `"max_tokens":1`, `"max_tokens":2`, 1), false},
		{"no max_tokens", strings.Replace(probeBody, `"max_tokens":1,`, ``, 1), false},
		{"stream", strings.Replace(probeBody, `"max_tokens":1`, `"max_tokens":1,"stream":true`, 1), false},
		{"system", strings.Replace(probeBody, `"max_tokens":1`, `"max_tokens":1,"system":"x"`, 1), false},
		{"tools", strings.Replace(probeBody, `"max_tokens":1`, `"max_tokens":1,"tools":[{"name":"Read","input_schema":{}}]`, 1), false},
		{"other text", strings.Replace(probeBody, `"quota"`, `"quota?"`, 1), false},
		{"capitalized", strings.Replace(probeBody, `"quota"`, `"Quota"`, 1), false},
		{"assistant", strings.Replace(probeBody, `"role":"user"`, `"role":"assistant"`, 1), false},
		{"two messages", strings.Replace(probeBody, `"content":"quota"}`, `"content":"quota"},{"role":"assistant","content":"ok"}`, 1), false},
		{"two blocks", `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"quota"},{"type":"text","text":"x"}]}]}`, false},
		{"image block", `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","text":"quota"}]}]}`, false},
		{"not JSON", `quota`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := []byte(c.body)
			if got := IsQuotaProbe(ParseRequest(body), body); got != c.want {
				t.Fatalf("IsQuotaProbe = %v, want %v", got, c.want)
			}
		})
	}
}

func TestQuotaProbeDescription(t *testing.T) {
	sum := ParseRequest([]byte(probeBody))
	limited := DescribeInput{
		Kind: KindQuotaProbe, Method: "POST", Path: "/v1/messages", Status: 429, Request: &sum,
		Response: &Message{Error: &APIError{Type: "rate_limit_error", Message: "Error"}},
	}
	if got := Headline(limited); got != "Chamada auxiliar: sonda de cota do Claude Code (HTTP 429, rate_limit_error)" {
		t.Fatalf("headline %q", got)
	}
	obs := Describe(limited)
	if len(obs) != 2 || obs[0].Code != "quota_probe" || obs[0].Certainty != CertaintyInferred || obs[1].Code != "api_error" {
		t.Fatalf("observations %+v", obs)
	}
	ok := limited
	ok.Status, ok.Response = 200, &Message{}
	if got := Headline(ok); got != "Chamada auxiliar: sonda de cota do Claude Code (HTTP 200)" {
		t.Fatalf("headline %q", got)
	}
	// A probe that got no response is a transport failure
	failed := DescribeInput{Kind: KindTransportError, Method: "POST", Path: "/v1/messages", TransportError: "connection refused"}
	if got := Headline(failed); !strings.HasPrefix(got, "Falha de transporte") {
		t.Fatalf("headline %q", got)
	}
}
