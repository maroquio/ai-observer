package httpproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/httpcapture"
	"github.com/tobilg/ai-observer/internal/logger"
)

// Config configures the proxy listeners
type Config struct {
	// Host the listeners bind to (127.0.0.1 by default)
	Host string
	// Port of the shared (manual) listener; 0: none, launcher runs only
	Port int
	// Upstream base URL (ValidateUpstream)
	Upstream *url.URL
	// AllowRemote permits a Host that is no loopback address
	AllowRemote bool
	// AllowedHosts are extra Host names requests may carry
	AllowedHosts []string
	// Transport to the upstream (NewTransport when nil)
	Transport http.RoundTripper
}

// ErrNotLoopback is returned for a non-loopback host without AllowRemote
var ErrNotLoopback = errors.New("the HTTP proxy listens on loopback addresses only; set AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE=true to listen on another interface")

// listener is a proxy port and the server on it
type listener struct {
	ln    net.Listener
	srv   *http.Server
	done  chan struct{}
	runID string // launcher run (empty: shared listener)
}

// Manager runs the shared proxy listener, whose exchanges form a manual run
// created on the first request, and one listener per launcher run
type Manager struct {
	cfg      Config
	capturer *httpcapture.Capturer
	shared   *listener

	mu        sync.Mutex
	manual    *httpcapture.Run
	launchers map[string]*listener
	stopped   bool
}

// NewManager checks cfg and returns a manager (no listener open yet)
func NewManager(cfg Config, capturer *httpcapture.Capturer) (*Manager, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if !cfg.AllowRemote && !isLoopback(cfg.Host) {
		return nil, ErrNotLoopback
	}
	if cfg.Upstream == nil {
		return nil, errors.New("HTTP proxy upstream not set")
	}
	if cfg.Transport == nil {
		cfg.Transport = NewTransport()
	}
	return &Manager{cfg: cfg, capturer: capturer, launchers: map[string]*listener{}}, nil
}

// Capturer returns the capture the proxy records into
func (m *Manager) Capturer() *httpcapture.Capturer { return m.capturer }

// Listen opens the shared listener (when a port is configured); call
// before Serve so a busy port fails startup
func (m *Manager) Listen() error {
	if m.cfg.Port == 0 {
		return nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port)))
	if err != nil {
		return fmt.Errorf("HTTP proxy port %d: %w", m.cfg.Port, err)
	}
	h := NewHandler(m.cfg.Upstream, m.cfg.Transport, m.cfg.AllowedHosts, m.manualRun)
	m.shared = &listener{ln: ln, srv: newServer(h), done: make(chan struct{})}
	return nil
}

// Serve serves the shared listener in the background
func (m *Manager) Serve() {
	if m.shared == nil {
		return
	}
	go serve(m.shared)
	logger.Info("HTTP capture proxy listening", "address", m.ProxyURL(), "upstream", m.cfg.Upstream.String(),
		"capture_mode", m.capturer.Config().Mode)
}

func serve(l *listener) {
	defer close(l.done)
	if err := l.srv.Serve(l.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP capture proxy stopped", "address", l.ln.Addr().String(), "error", err)
	}
}

// ProxyURL is the ANTHROPIC_BASE_URL of the shared listener ("" without)
func (m *Manager) ProxyURL() string {
	if m.shared == nil {
		return ""
	}
	return baseURL(m.shared.ln.Addr())
}

func baseURL(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		host := tcp.IP.String()
		if tcp.IP.IsUnspecified() {
			host = "127.0.0.1"
		}
		return "http://" + net.JoinHostPort(host, strconv.Itoa(tcp.Port))
	}
	return "http://" + addr.String()
}

// manualRun returns the run of the shared listener, creating it on the
// first request; nil when it cannot be stored (the request is forwarded
// without capture)
func (m *Manager) manualRun() *httpcapture.Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil
	}
	if m.manual != nil && m.manual.Active() {
		return m.manual
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run, err := m.capturer.StartRun(ctx, httpcapture.RunOptions{
		Mode: api.CaptureRunManual, Host: m.cfg.Host, Port: m.cfg.Port,
	})
	if err != nil {
		logger.Warn("HTTP proxy: starting the manual capture run failed; forwarding without capture", "error", err)
		return nil
	}
	m.manual = run
	return run
}

// ManualRunID returns the current manual run ("" before the first request)
func (m *Manager) ManualRunID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.manual != nil && m.manual.Active() {
		return m.manual.ID
	}
	return ""
}

// StartLauncherRun starts a run with a listener of its own on a free port
// and returns the run and its proxy URL
func (m *Manager) StartLauncherRun(ctx context.Context, opts httpcapture.RunOptions) (*httpcapture.Run, string, error) {
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return nil, "", errors.New("the server is shutting down")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(m.cfg.Host, "0"))
	if err != nil {
		return nil, "", fmt.Errorf("opening a proxy port: %w", err)
	}
	opts.Mode = api.CaptureRunLauncher
	opts.Host = m.cfg.Host
	opts.Port = ln.Addr().(*net.TCPAddr).Port
	run, err := m.capturer.StartRun(ctx, opts)
	if err != nil {
		ln.Close()
		return nil, "", err
	}
	h := NewHandler(m.cfg.Upstream, m.cfg.Transport, m.cfg.AllowedHosts, func() *httpcapture.Run {
		if run.Active() {
			return run
		}
		return nil
	})
	l := &listener{ln: ln, srv: newServer(h), done: make(chan struct{}), runID: run.ID}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		ln.Close()
		_ = m.capturer.FinishRun(context.WithoutCancel(ctx), run.ID, api.CaptureRunInterrupted, nil, 0)
		return nil, "", errors.New("the server is shutting down")
	}
	m.launchers[run.ID] = l
	m.mu.Unlock()
	go serve(l)
	url := baseURL(ln.Addr())
	logger.Info("HTTP capture run started", "run", run.ID, "proxy", url)
	return run, url, nil
}

// FinishRun ends a run: a launcher run's listener stops accepting requests
// and its open requests may finish (until ctx ends); the run is then stored
// as finished
func (m *Manager) FinishRun(ctx context.Context, runID string, exitCode *int, pid int) error {
	m.mu.Lock()
	l := m.launchers[runID]
	delete(m.launchers, runID)
	if m.manual != nil && m.manual.ID == runID {
		m.manual = nil
	}
	m.mu.Unlock()
	if l != nil {
		shutdownListener(ctx, l)
	}
	return m.capturer.FinishRun(ctx, runID, api.CaptureRunFinished, exitCode, pid)
}

// shutdownListener stops a listener gracefully until ctx ends, then closes
// its connections
func shutdownListener(ctx context.Context, l *listener) {
	if err := l.srv.Shutdown(ctx); err != nil {
		l.srv.Close()
	}
	<-l.done
}

// LauncherRuns returns the IDs of the launcher runs with a listener
func (m *Manager) LauncherRuns() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.launchers))
	for id := range m.launchers {
		ids = append(ids, id)
	}
	return ids
}

// Shutdown stops all listeners (open requests may finish until ctx ends,
// then their connections are closed), then stores what was captured
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.stopped = true
	ls := make([]*listener, 0, len(m.launchers)+1)
	for _, l := range m.launchers {
		ls = append(ls, l)
	}
	m.launchers = map[string]*listener{}
	if m.shared != nil {
		ls = append(ls, m.shared)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, l := range ls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			shutdownListener(ctx, l)
		}()
	}
	wg.Wait()
	return m.capturer.Close(ctx)
}

// Status describes the proxy (without credentials)
func (m *Manager) Status() api.HTTPInspectorStatus {
	st := m.capturer.StatsSnapshot()
	cfg := m.capturer.Config()
	st.Enabled = true
	st.Host = m.cfg.Host
	st.Port = m.cfg.Port
	st.ProxyURL = m.ProxyURL()
	st.Upstream = m.cfg.Upstream.String()
	st.CaptureMode = cfg.Mode
	st.MaxBodyBytes = cfg.MaxBodyBytes
	st.QueueSize = cfg.QueueSize
	if b := m.capturer.Blobs(); b != nil {
		st.CaptureDir = b.Dir()
	}
	st.ManualRunID = m.ManualRunID()
	return st
}
