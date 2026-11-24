package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/config"
	"github.com/tobilg/ai-observer/internal/redact"
	"github.com/tobilg/ai-observer/internal/server"
)

// runArgs are the options of run
type runArgs struct {
	Name   string
	Server string
	Serve  bool
	// Command is the program and its arguments
	Command []string
	DB      string
}

// cmdRun starts a program (Claude Code) with its Anthropic API traffic sent
// through a capture run of its own, and exits with the program's status
func cmdRun(args []string) {
	code, err := runLauncher(context.Background(), args, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "ai-observer run:", err)
		// 127 when the program could not be started, like a shell
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func parseRunArgs(args []string) (*runArgs, error) {
	parsed := &runArgs{}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Var(valueFlag{&parsed.Name, "name"}, "name", "Name of the capture run (default: Execução and the time)")
	fs.Var(valueFlag{&parsed.Server, "url"}, "server", "URL of the running AI Observer (default: the API port of this machine)")
	fs.BoolVar(&parsed.Serve, "serve", false, "Start AI Observer (with the HTTP capture proxy) for the program's lifetime when none runs")
	registerDBFlag(fs, &parsed.DB)
	fs.Usage = func() {
		fmt.Print(`Run a program, such as Claude Code, with its Anthropic API traffic captured

The program gets ANTHROPIC_BASE_URL pointing at a proxy port opened for this
run alone, so its requests are told apart from any other client's. Nothing
else of its environment changes: its login (claude.ai subscription or
ANTHROPIC_API_KEY) is passed through as it is, and no Claude Code setting is
written. Standard input, output, error and the exit status are the
program's. The run ends when the program exits.

Usage: ai-observer run [options] -- <program> [arguments...]

Requires a running AI Observer with the HTTP capture proxy
(ai-observer serve --http-proxy), or --serve to start one for the run.

Examples:
  ai-observer run -- claude
  ai-observer run --name "Aula 3" -- claude -p "Explique este repositório"
  ai-observer run --serve -- claude

Options:
`)
		printFlags(fs)
	}
	// Options end at the first argument that is no option (or at --): the
	// program's own options are never read
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	parsed.Command = fs.Args()
	if len(parsed.Command) == 0 {
		fs.Usage()
		return nil, errors.New("missing program: ai-observer run -- claude")
	}
	return parsed, nil
}

// launcherClient talks to the AI Observer API
type launcherClient struct {
	base  string
	token string
	http  *http.Client
}

func newLauncherClient(base, token string) *launcherClient {
	return &launcherClient{base: strings.TrimSuffix(base, "/"), token: token, http: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			// The certificate names the server's public host, not loopback
			TLSClientConfig: &tls.Config{InsecureSkipVerify: isLoopbackURL(base)},
		},
	}}
}

func isLoopbackURL(raw string) bool {
	host := raw
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *launcherClient) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e api.ErrorResponse
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			return fmt.Errorf("%s: %s", resp.Status, e.Message)
		}
		return fmt.Errorf("%s", resp.Status)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ready reports whether the server answers
func (c *launcherClient) ready(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.do(ctx, http.MethodGet, "/ready", nil, nil) == nil
}

// apiBaseURL is the API of a server with cfg on this machine
func apiBaseURL(cfg *config.Config) string {
	scheme := "http"
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.APIPort))
}

// runLauncher runs the program and returns its exit status
func runLauncher(ctx context.Context, args []string, stderr io.Writer) (int, error) {
	parsed, err := parseRunArgs(args)
	if err != nil {
		return 0, err
	}
	cfg := config.Load()
	if parsed.DB != "" {
		cfg.DatabasePath = parsed.DB
	}
	base := parsed.Server
	if base == "" {
		base = apiBaseURL(cfg)
	}
	client := newLauncherClient(base, cfg.AuthToken)

	embedded := false
	if !client.ready(ctx) {
		if !parsed.Serve {
			return 0, fmt.Errorf("no AI Observer answers at %s: start it with `ai-observer serve --http-proxy` (or add --serve to start one for this run)", base)
		}
		if parsed.Server != "" {
			return 0, errors.New("--serve starts a server on this machine: leave out --server")
		}
		// The terminal belongs to the program: the server logs to a file
		// next to the database
		logPath := filepath.Join(filepath.Dir(cfg.DatabasePath), "ai-observer-run.log")
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return 0, err
		}
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, fmt.Errorf("opening the server log: %w", err)
		}
		defer logFile.Close()
		initLoggingTo(logFile)
		fmt.Fprintf(stderr, "AI Observer: servidor iniciado para esta execução (log em %s)\n", logPath)
		embedded = true
		cfg.HTTPProxy.Enabled = true
		// Only this run's port: no shared port to conflict with
		cfg.HTTPProxy.Port = 0
		srv, err := server.New(cfg)
		if err != nil {
			return 0, fmt.Errorf("starting AI Observer: %w", err)
		}
		served := make(chan error, 1)
		go func() { served <- srv.ListenAndServe() }()
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			srv.Shutdown(sctx)
		}()
		deadline := time.Now().Add(15 * time.Second)
		for !client.ready(ctx) {
			select {
			case err := <-served:
				return 0, fmt.Errorf("starting AI Observer: %w", err)
			default:
			}
			if time.Now().After(deadline) {
				return 0, errors.New("AI Observer did not become ready")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	cwd, _ := os.Getwd()
	// The command line is stored with the run: redact secrets it may carry
	commandLine := redact.New(redact.Secrets).String(strings.Join(parsed.Command, " "))
	var created api.HTTPCaptureRunCreated
	if err := client.do(ctx, http.MethodPost, "/api/http-inspector/runs",
		api.CreateHTTPCaptureRunRequest{Name: parsed.Name, Cwd: cwd, Command: commandLine}, &created); err != nil {
		return 0, fmt.Errorf("starting the capture run: %w", err)
	}
	runID := created.Run.ID
	fmt.Fprintf(stderr, "AI Observer: captura %q, proxy %s\n", created.Run.Name, created.ProxyURL)
	if prev := os.Getenv("ANTHROPIC_BASE_URL"); prev != "" && prev != created.ProxyURL {
		fmt.Fprintf(stderr, "AI Observer: ANTHROPIC_BASE_URL (%s) substituída para este processo\n", prev)
	}

	code, pid, runErr := runChild(parsed.Command, created.ProxyURL)

	// The run ends even when ctx was cancelled
	fctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	finish := api.FinishHTTPCaptureRunRequest{PID: pid}
	if runErr == nil {
		finish.ExitCode = &code
	}
	var finished api.HTTPCaptureRun
	if err := client.do(fctx, http.MethodPost, "/api/http-inspector/runs/"+runID+"/finish", finish, &finished); err != nil {
		fmt.Fprintf(stderr, "AI Observer: não foi possível encerrar a captura %s: %v\n", runID, err)
	} else {
		fmt.Fprintf(stderr, "AI Observer: %d trocas capturadas — %s/http-inspector/runs/%s\n", finished.Exchanges, dashboardBase(base), runID)
		if embedded {
			fmt.Fprintln(stderr, "AI Observer: o servidor desta execução foi encerrado; para ver a captura, inicie `ai-observer serve` e abra o link acima")
		}
	}
	if runErr != nil {
		return 127, runErr
	}
	return code, nil
}

// dashboardBase is the dashboard's URL for links printed to the user
func dashboardBase(apiBase string) string {
	return strings.TrimSuffix(apiBase, "/")
}

// runChild runs the program with the proxy URL as ANTHROPIC_BASE_URL and
// the terminal of this process; it returns the exit status and PID
func runChild(command []string, proxyURL string) (code, pid int, err error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = childEnv(os.Environ(), proxyURL)

	// The terminal sends Ctrl+C (SIGINT) to the whole foreground process
	// group, the program included: this process ignores it and waits for the
	// program to decide. Signals sent to this process alone (SIGTERM,
	// SIGHUP) are passed on.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return 0, 0, fmt.Errorf("starting %s: %w", command[0], err)
	}
	pid = cmd.Process.Pid
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if s != os.Interrupt {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()
	err = cmd.Wait()
	close(done)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitStatus(exitErr.ProcessState), pid, nil
		}
		return 0, pid, err
	}
	return 0, pid, nil
}

// childEnv is env with ANTHROPIC_BASE_URL set to proxyURL (replacing any)
func childEnv(env []string, proxyURL string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_BASE_URL=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "ANTHROPIC_BASE_URL="+proxyURL)
}
