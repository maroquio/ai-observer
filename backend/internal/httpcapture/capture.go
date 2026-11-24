// Package httpcapture records what the HTTP capture proxy (internal/
// httpproxy) forwards between a client such as Claude Code and the
// Anthropic API: requests, responses, Server-Sent Events, usage and tool
// links, interpreted with internal/anthropicwire and stored in DuckDB with
// the bodies in local files.
//
// Capturing is a side copy of the traffic. The proxy hands each body chunk
// to Exchange.observe, which copies it into a bounded buffer and returns
// without waiting; a goroutine per exchange parses and keeps the bodies, and
// a single writer (Pipeline) stores the records. When a buffer or the queue
// is full, capture data is dropped and counted, the exchange is marked
// degraded, and the traffic goes on unchanged.
package httpcapture

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/redact"
	"github.com/tobilg/ai-observer/internal/selfmetrics"
	"github.com/tobilg/ai-observer/internal/storage"
)

// Capture modes (config.Capture*)
const (
	ModeSafe         = "safe"
	ModeMetadataOnly = "metadata_only"
	ModeFullRaw      = "full_raw"
)

// Config configures a Capturer
type Config struct {
	// Mode is ModeSafe, ModeMetadataOnly or ModeFullRaw
	Mode string
	// MaxBodyBytes bounds each kept body (more is forwarded, not kept)
	MaxBodyBytes int64
	// QueueSize bounds the records waiting to be stored
	QueueSize int
	// BufferBytes bounds the body bytes copied from the traffic and not yet
	// processed, over all exchanges (default 256 MiB)
	BufferBytes int64
	// MaxEvents bounds the SSE events stored per exchange (default 200,000)
	MaxEvents int
	// Upstream and ServerVersion are recorded with each run
	Upstream      string
	ServerVersion string
}

// Stats counts what the capture observed and lost since the start
type Stats struct {
	exchanges      atomic.Int64
	inFlight       atomic.Int64
	bytesForwarded atomic.Int64
	bytesStored    atomic.Int64
	sseEvents      atomic.Int64
	droppedRecords atomic.Int64
	lostBodyBytes  atomic.Int64
	storeErrors    atomic.Int64
	parseErrors    atomic.Int64
	blobErrors     atomic.Int64
}

// RunStore is the storage of runs (storage.DuckDBStore)
type RunStore interface {
	Store
	InsertHTTPCaptureRun(ctx context.Context, r storage.HTTPCaptureRunRecord) error
	FinishHTTPCaptureRun(ctx context.Context, runID, state string, endedAt time.Time, exitCode *int, pid int) error
}

// Capturer records exchanges of capture runs
type Capturer struct {
	cfg      Config
	store    RunStore
	pipeline *Pipeline
	blobs    *BlobStore
	san      *Sanitizer
	stats    Stats
	buffered atomic.Int64 // body bytes queued to exchange workers

	mu      sync.Mutex
	runs    map[string]*Run
	workers sync.WaitGroup
	closed  bool
}

// New returns a capturer storing into store, with bodies in blobs (nil in
// metadata_only mode) and text redacted with r
func New(cfg Config, store RunStore, blobs *BlobStore, r *redact.Redactor, notifier Notifier) *Capturer {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 32 << 20
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.BufferBytes <= 0 {
		cfg.BufferBytes = 256 << 20
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 200_000
	}
	switch cfg.Mode {
	case ModeSafe, ModeMetadataOnly, ModeFullRaw:
	default:
		cfg.Mode = ModeSafe
	}
	c := &Capturer{
		cfg:   cfg,
		store: store,
		blobs: blobs,
		san:   NewSanitizer(r),
		runs:  map[string]*Run{},
	}
	c.pipeline = NewPipeline(store, notifier, cfg.QueueSize, &c.stats)
	return c
}

// Config returns the capturer's configuration
func (c *Capturer) Config() Config { return c.cfg }

// Blobs returns the body store (nil without bodies)
func (c *Capturer) Blobs() *BlobStore { return c.blobs }

// Sanitizer returns the redaction of stored text
func (c *Capturer) Sanitizer() *Sanitizer { return c.san }

// StatsSnapshot returns the counters
func (c *Capturer) StatsSnapshot() api.HTTPInspectorStatus {
	s := &c.stats
	out := api.HTTPInspectorStatus{
		Exchanges:      s.exchanges.Load(),
		InFlight:       s.inFlight.Load(),
		BytesForwarded: s.bytesForwarded.Load(),
		BytesStored:    s.bytesStored.Load(),
		SSEEvents:      s.sseEvents.Load(),
		DroppedRecords: s.droppedRecords.Load(),
		LostBodyBytes:  s.lostBodyBytes.Load(),
		StoreErrors:    s.storeErrors.Load(),
		ParseErrors:    s.parseErrors.Load(),
		Warnings:       []string{},
	}
	if out.DroppedRecords > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d registros de captura descartados (fila cheia): o histórico está incompleto", out.DroppedRecords))
	}
	if out.LostBodyBytes > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d bytes de corpo não capturados (buffer cheio): algumas trocas estão incompletas", out.LostBodyBytes))
	}
	if out.StoreErrors > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d registros não puderam ser gravados no banco", out.StoreErrors))
	}
	if n := s.blobErrors.Load(); n > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d corpos não puderam ser gravados no diretório de captura", n))
	}
	out.Degraded = len(out.Warnings) > 0
	c.mu.Lock()
	for _, r := range c.runs {
		if r.active.Load() {
			out.ActiveRuns++
		}
	}
	c.mu.Unlock()
	return out
}

// RunOptions describe a new run
type RunOptions struct {
	Name    string
	Mode    string // api.CaptureRunManual or api.CaptureRunLauncher
	Host    string
	Port    int
	Cwd     string
	Command string
}

// StartRun stores a new active run
func (c *Capturer) StartRun(ctx context.Context, opts RunOptions) (*Run, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("capture is shutting down")
	}
	run := &Run{
		ID:        NewID(),
		Name:      opts.Name,
		Mode:      opts.Mode,
		Port:      opts.Port,
		StartedAt: time.Now().UTC(),
		c:         c,
		uses:      map[string]int64{},
		results:   map[string]int64{},
	}
	if run.Name == "" {
		run.Name = defaultRunName(opts.Mode, run.StartedAt)
	}
	err := c.store.InsertHTTPCaptureRun(ctx, storage.HTTPCaptureRunRecord{
		ID:            run.ID,
		Name:          run.Name,
		Mode:          opts.Mode,
		State:         api.CaptureRunActive,
		CaptureMode:   c.cfg.Mode,
		Host:          opts.Host,
		Port:          opts.Port,
		Upstream:      c.cfg.Upstream,
		Cwd:           opts.Cwd,
		Command:       opts.Command,
		StartedAt:     run.StartedAt,
		ServerVersion: c.cfg.ServerVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("storing capture run: %w", err)
	}
	run.active.Store(true)
	c.mu.Lock()
	c.runs[run.ID] = run
	c.mu.Unlock()
	selfmetrics.HTTPCaptureActiveRuns.With().Add(1)
	return run, nil
}

func defaultRunName(mode string, t time.Time) string {
	local := t.Local().Format("2006-01-02 15:04:05")
	if mode == api.CaptureRunLauncher {
		return "Execução " + local
	}
	return "Captura manual " + local
}

// Run returns an active run by ID
func (c *Capturer) Run(id string) *Run {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.runs[id]; r != nil && r.active.Load() {
		return r
	}
	return nil
}

// FinishRun ends a run: no further exchanges are attributed to it. Its
// exchanges still in flight finish and are stored.
func (c *Capturer) FinishRun(ctx context.Context, id string, state string, exitCode *int, pid int) error {
	c.mu.Lock()
	r := c.runs[id]
	delete(c.runs, id)
	c.mu.Unlock()
	if r != nil && r.active.Swap(false) {
		selfmetrics.HTTPCaptureActiveRuns.With().Add(-1)
	}
	return c.store.FinishHTTPCaptureRun(ctx, id, state, time.Now().UTC(), exitCode, pid)
}

// Close finishes the active runs (state finished), waits for the exchanges
// in flight to be processed and stores the queued records, until ctx ends
func (c *Capturer) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	var ids []string
	for id := range c.runs {
		ids = append(ids, id)
	}
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		c.workers.Wait()
		close(done)
	}()
	var errs []error
	select {
	case <-done:
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("waiting for capture workers: %w", ctx.Err()))
	}
	if err := c.pipeline.Close(ctx); err != nil {
		errs = append(errs, fmt.Errorf("storing queued capture records: %w", err))
	}
	for _, id := range ids {
		if err := c.FinishRun(context.WithoutCancel(ctx), id, api.CaptureRunFinished, nil, 0); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Run is a capture run: exchanges are numbered in arrival order and tool
// uses and results are linked within it
type Run struct {
	ID        string
	Name      string
	Mode      string
	Port      int
	StartedAt time.Time

	c      *Capturer
	seq    atomic.Int64
	active atomic.Bool

	mu sync.Mutex
	// uses maps tool_use IDs to the seq of the exchange whose reply
	// requested them; results to the seq of the first request that carried
	// their result
	uses    map[string]int64
	results map[string]int64
}

// Active reports whether the run still records exchanges
func (r *Run) Active() bool { return r.active.Load() }

// noteToolUses records the tool uses of a reply
func (r *Run) noteToolUses(seq int64, ids []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if prev, ok := r.uses[id]; !ok || seq < prev {
			r.uses[id] = seq
		}
	}
}

// newResults returns which tool results of a request are carried for the
// first time in the run (by seq: a request processed out of order still
// counts as first when its seq is lower), mapped to the seq of the exchange
// that requested them (0: unknown)
func (r *Run) newResults(seq int64, ids []string) map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, id := range ids {
		if prev, ok := r.results[id]; ok && prev <= seq {
			continue
		}
		r.results[id] = seq
		out[id] = int(r.uses[id])
	}
	return out
}

// truncateText cuts s to at most n bytes at a rune boundary
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return strings.ToValidUTF8(s[:cut], "") + "…"
}
