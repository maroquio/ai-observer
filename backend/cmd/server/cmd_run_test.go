package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tobilg/ai-observer/internal/api"
)

// The launcher's program: when the test binary runs with this variable it
// acts as a client of the Anthropic API (keyless), then exits with 3
func TestMain(m *testing.M) {
	if os.Getenv("AI_OBSERVER_TEST_CHILD") == "1" {
		os.Exit(fakeClaude())
	}
	os.Exit(m.Run())
}

func fakeClaude() int {
	base := os.Getenv("ANTHROPIC_BASE_URL")
	prompt := os.Getenv("AI_OBSERVER_TEST_CHILD_PROMPT")
	if prompt == "" {
		prompt = "oi"
	}
	body, _ := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5-20250929", "max_tokens": 5,
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/messages?beta=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("X-Claude-Code-Session-Id", "launcher-session")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 9
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return 3
}

func TestRunLauncherCapturesTheProgram(t *testing.T) {
	_, apiPort, _ := setupRunEnv(t)
	var mu sync.Mutex
	var keys []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("X-Api-Key"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_l","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"oi"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
	}))
	defer up.Close()
	t.Setenv("AI_OBSERVER_HTTP_PROXY_UPSTREAM", up.URL)
	t.Setenv("AI_OBSERVER_HTTP_PROXY_PORT", "off")
	t.Setenv("AI_OBSERVER_HTTP_CAPTURE_DIR", t.TempDir())
	// The program's login passes through unchanged
	key := "sk-ant-api03-" + strings.Repeat("Lw9k", 12)
	t.Setenv("ANTHROPIC_API_KEY", key)
	t.Setenv("ANTHROPIC_BASE_URL", "http://example.invalid")
	t.Setenv("AI_OBSERVER_TEST_CHILD", "1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runServer(ctx, []string{"--http-proxy"}) }()
	waitHealthy(t, apiPort, done)

	var stderr bytes.Buffer
	code, err := runLauncher(ctx, []string{"--name", "Teste do launcher", "--", os.Args[0], "-test.run=^$"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Fatalf("exit status %d, want the program's 3; %s", code, stderr.String())
	}
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("the program's key did not reach the upstream: %v", keys)
	}
	if !strings.Contains(stderr.String(), "1 trocas capturadas") {
		t.Fatalf("stderr: %s", stderr.String())
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/http-inspector/runs", apiPort))
	if err != nil {
		t.Fatal(err)
	}
	var runs api.HTTPCaptureRunsResponse
	json.NewDecoder(resp.Body).Decode(&runs)
	resp.Body.Close()
	if len(runs.Runs) != 1 {
		t.Fatalf("runs %+v", runs)
	}
	r := runs.Runs[0]
	if r.Name != "Teste do launcher" || r.Mode != api.CaptureRunLauncher || r.State != api.CaptureRunFinished ||
		r.ExitCode == nil || *r.ExitCode != 3 || r.PID == 0 || r.Exchanges != 1 || len(r.SessionIDs) != 1 || r.SessionIDs[0] != "launcher-session" {
		t.Fatalf("run %+v", r)
	}
	if strings.Contains(r.Command, key) {
		t.Fatal("command line stored with a secret")
	}
	cancel()
	waitStopped(t, done)
}

// --serve starts a server for the run alone: the capture is stored redacted
// and priced like with `serve`, and is there for the next server
func TestRunLauncherServe(t *testing.T) {
	dbPath, apiPort, _ := setupRunEnv(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Request-Id", "req_serve_1")
		fmt.Fprint(w, `{"id":"msg_s","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1000,"output_tokens":100}}`)
	}))
	defer up.Close()
	captureDir := t.TempDir()
	t.Setenv("AI_OBSERVER_HTTP_PROXY_UPSTREAM", up.URL)
	t.Setenv("AI_OBSERVER_HTTP_CAPTURE_DIR", captureDir)
	secret := "sk-ant-api03-" + strings.Repeat("Qz7x", 12)
	t.Setenv("AI_OBSERVER_TEST_CHILD_PROMPT", "use a chave "+secret+" no deploy")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("AI_OBSERVER_TEST_CHILD", "1")
	t.Setenv("AI_OBSERVER_LOG_LEVEL", "INFO")

	var stderr bytes.Buffer
	code, err := runLauncher(context.Background(), []string{"--serve", "--", os.Args[0], "-test.run=^$"}, &stderr)
	if err != nil || code != 3 {
		t.Fatalf("code %d, err %v; %s", code, err, stderr.String())
	}
	logPath := filepath.Join(filepath.Dir(dbPath), "ai-observer-run.log")
	if !strings.Contains(stderr.String(), logPath) || !strings.Contains(stderr.String(), "1 trocas capturadas") {
		t.Fatalf("stderr: %s", stderr.String())
	}
	if info, err := os.Stat(logPath); err != nil || info.Size() == 0 {
		t.Fatalf("server log %v", err)
	}
	// Back to standard output for the server started next
	t.Setenv("AI_OBSERVER_LOG_LEVEL", "ERROR")
	initLogging()

	// Nothing on disk holds the secret
	filepath.WalkDir(captureDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if data, _ := os.ReadFile(path); bytes.Contains(data, []byte(secret)) {
				t.Errorf("secret stored in %s", path)
			}
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Setenv("AI_OBSERVER_TEST_CHILD", "")
	done := make(chan error, 1)
	go func() { done <- runServer(ctx, nil) }()
	waitHealthy(t, apiPort, done)
	get := func(path string, out any) {
		t.Helper()
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", apiPort, path))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %s", path, resp.Status)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	var runs api.HTTPCaptureRunsResponse
	get("/api/http-inspector/runs", &runs)
	if len(runs.Runs) != 1 || runs.Runs[0].State != api.CaptureRunFinished || runs.Runs[0].Exchanges != 1 {
		t.Fatalf("runs %+v", runs)
	}
	var page api.HTTPExchangesResponse
	get("/api/http-inspector/runs/"+runs.Runs[0].ID+"/exchanges", &page)
	if len(page.Exchanges) != 1 {
		t.Fatalf("exchanges %+v", page)
	}
	ex := page.Exchanges[0]
	if ex.Usage == nil || ex.Usage.CostUSD == nil || *ex.Usage.CostUSD <= 0 || ex.Usage.CostStatus != api.CostComplete {
		t.Fatalf("usage %+v", ex.Usage)
	}
	var body api.HTTPExchangeBody
	get("/api/http-inspector/exchanges/"+ex.ID+"/request", &body)
	if strings.Contains(body.Body, secret) || !strings.Contains(body.Body, "no deploy") {
		t.Fatalf("request body %q", body.Body)
	}
	cancel()
	waitStopped(t, done)
}

func TestRunLauncherWithoutServer(t *testing.T) {
	setupRunEnv(t)
	_, err := runLauncher(context.Background(), []string{"--", "true"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ai-observer serve --http-proxy") {
		t.Fatalf("error %v", err)
	}
}

func TestChildEnvReplacesBaseURL(t *testing.T) {
	env := childEnv([]string{"A=1", "ANTHROPIC_BASE_URL=http://old", "ANTHROPIC_API_KEY=k"}, "http://127.0.0.1:9")
	got := strings.Join(env, ";")
	if got != "A=1;ANTHROPIC_API_KEY=k;ANTHROPIC_BASE_URL=http://127.0.0.1:9" {
		t.Fatalf("env %s", got)
	}
}
