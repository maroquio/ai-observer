// Command fake-anthropic is a simulated Anthropic Messages API for keyless
// demonstrations and tests of the HTTP Inspector. It never calls the real
// API and accepts any credential.
//
// It answers deterministically:
//
//   - POST /v1/messages/count_tokens: an estimate (body size / 4)
//   - POST /v1/messages whose last user message carries a tool_result: a
//     final text reply that reads from the cache
//   - POST /v1/messages offering a Read tool and mentioning "auth": a
//     tool_use asking to read the --file path (5-minute cache write)
//   - any other POST /v1/messages: a short text reply
//   - other paths: 404 in the Anthropic error format
//
// Streaming requests get server-sent events with small delays between
// deltas, so their timing is visible in the inspector. Replies name the
// requested model, so the price catalog prices them as it would real ones.
//
// Usage:
//
//	go run ./cmd/fake-anthropic --listen 127.0.0.1:9787 --file "$PWD/src/auth.ts"
//	ai-observer serve --http-proxy   # with AI_OBSERVER_HTTP_PROXY_UPSTREAM=http://127.0.0.1:9787
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type server struct {
	file  string
	delay time.Duration
	seq   atomic.Int64
	quiet bool
}

func main() {
	listen := flag.String("listen", "127.0.0.1:9787", "Address to listen on (keep it on loopback)")
	file := flag.String("file", "src/auth.ts", "File the simulated model asks Claude Code to read (made absolute)")
	delay := flag.Duration("delay", 40*time.Millisecond, "Pause between streamed events")
	quiet := flag.Bool("quiet", false, "Do not log requests")
	flag.Parse()
	abs, err := filepath.Abs(*file)
	if err != nil {
		log.Fatal(err)
	}
	s := &server{file: abs, delay: *delay, quiet: *quiet}
	fmt.Fprintf(os.Stderr, "fake-anthropic: API simulada em http://%s (nenhuma chamada à API real; arquivo da demo: %s)\n", *listen, abs)
	log.Fatal(http.ListenAndServe(*listen, s))
}

// request is what the simulation reads of a Messages API request
type request struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Name string `json:"name"`
	} `json:"tools"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := s.seq.Add(1)
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if !s.quiet {
		log.Printf("#%d %s %s (%d bytes)", n, r.Method, r.URL.Path, len(body))
	}
	w.Header().Set("Request-Id", fmt.Sprintf("req_sim_%04d", n))
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages/count_tokens":
		writeJSON(w, http.StatusOK, map[string]any{"input_tokens": len(body)/4 + 1})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		s.messages(w, n, body)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"type": "error",
			"error": map[string]string{"type": "not_found_error", "message": "rota não simulada: " + r.URL.Path}})
	}
}

func (s *server) messages(w http.ResponseWriter, n int64, body []byte) {
	var req request
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"type": "error",
			"error": map[string]string{"type": "invalid_request_error", "message": "JSON inválido ou sem model"}})
		return
	}
	lastText, hasResult := lastUserMessage(req)
	hasRead := false
	for _, t := range req.Tools {
		hasRead = hasRead || t.Name == "Read"
	}
	id := fmt.Sprintf("msg_sim_%04d", n)
	var content []block
	stop := "end_turn"
	usage := map[string]any{"input_tokens": 24, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0, "output_tokens": 0}
	switch {
	case hasResult:
		content = []block{{Type: "text", Text: "Li o arquivo. A função de login compara a senha em texto puro; " +
			"use uma comparação de hash com tempo constante e adicione um teste para senha incorreta. (resposta simulada)"}}
		usage["input_tokens"], usage["cache_read_input_tokens"] = 180, 3000
		usage["cache_creation_input_tokens"] = 420
		usage["cache_creation"] = map[string]int{"ephemeral_5m_input_tokens": 420, "ephemeral_1h_input_tokens": 0}
	case hasRead && strings.Contains(strings.ToLower(lastText), "auth"):
		input, _ := json.Marshal(map[string]string{"file_path": s.file})
		content = []block{
			{Type: "text", Text: "Vou ler o arquivo de autenticação."},
			{Type: "tool_use", ID: fmt.Sprintf("toolu_sim_%04d", n), Name: "Read", Input: input},
		}
		stop = "tool_use"
		usage["cache_creation_input_tokens"] = 3000
		usage["cache_creation"] = map[string]int{"ephemeral_5m_input_tokens": 3000, "ephemeral_1h_input_tokens": 0}
	default:
		content = []block{{Type: "text", Text: "Olá! Esta é uma resposta simulada do upstream de demonstração."}}
	}
	output := 0
	for _, b := range content {
		output += len(b.Text)/4 + len(b.Input)/4 + 1
	}
	usage["output_tokens"] = output
	if !req.Stream {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model,
			"content": content, "stop_reason": stop, "stop_sequence": nil, "usage": usage})
		return
	}
	s.stream(w, id, req.Model, content, stop, usage)
}

// lastUserMessage returns the text of the last user message and whether it
// carries a tool result
func lastUserMessage(req request) (string, bool) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != "user" {
			continue
		}
		var text string
		if json.Unmarshal(m.Content, &text) == nil {
			return text, false
		}
		var blocks []block
		_ = json.Unmarshal(m.Content, &blocks)
		var parts []string
		result := false
		for _, b := range blocks {
			result = result || b.Type == "tool_result"
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n"), result
	}
	return "", false
}

func (s *server) stream(w http.ResponseWriter, id, model string, content []block, stop string, usage map[string]any) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(event string, data any) {
		payload, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(s.delay)
	}
	start := map[string]any{}
	for k, v := range usage {
		start[k] = v
	}
	start["output_tokens"] = 1
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": start}})
	send("ping", map[string]string{"type": "ping"})
	for i, b := range content {
		switch b.Type {
		case "text":
			send("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]string{"type": "text", "text": ""}})
			for _, part := range chunks(b.Text, 24) {
				send("content_block_delta", map[string]any{"type": "content_block_delta", "index": i,
					"delta": map[string]string{"type": "text_delta", "text": part}})
			}
		case "tool_use":
			send("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "tool_use", "id": b.ID, "name": b.Name, "input": map[string]any{}}})
			for _, part := range chunks(string(b.Input), 16) {
				send("content_block_delta", map[string]any{"type": "content_block_delta", "index": i,
					"delta": map[string]string{"type": "input_json_delta", "partial_json": part}})
			}
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	send("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": usage["output_tokens"]}})
	send("message_stop", map[string]string{"type": "message_stop"})
}

// chunks cuts s into pieces of about size runes
func chunks(s string, size int) []string {
	r := []rune(s)
	var out []string
	for len(r) > size {
		out = append(out, string(r[:size]))
		r = r[size:]
	}
	return append(out, string(r))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
