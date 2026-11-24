package httpcapture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/selfmetrics"
	"github.com/tobilg/ai-observer/internal/storage"
)

// Transport lifecycle events (api.HTTPLifecycleEvent.Event)
const (
	EventRequestStarted   = "request_started"
	EventRequestBody      = "request_body_observed"
	EventUpstreamConn     = "upstream_connected"
	EventResponseHeaders  = "response_headers_received"
	EventResponseStream   = "response_streaming"
	EventCompleted        = "completed"
	EventUpstreamError    = "upstream_error"
	EventClientAborted    = "client_aborted"
	EventUpstreamAborted  = "upstream_aborted"
	EventCaptureDegraded  = "capture_degraded"
	EventStreamParseError = "stream_parse_error"
)

// SessionHeader is the header Claude Code sends its session ID in
const SessionHeader = "X-Claude-Code-Session-Id"

// sessionIDPattern accepts the session IDs stored (UUIDs and similar)
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// part indexes
const (
	reqPart  = 0
	respPart = 1
)

// chunk is a copy of body bytes as they passed, with their arrival time
type chunk struct {
	part int
	data []byte
	at   time.Time
}

// partState is what the proxy observed of one body
type partState struct {
	started  bool
	done     bool // read to EOF
	err      error
	closed   bool // closed before EOF
	lost     bool // chunks were dropped: the captured body is incomplete
	lostSize int64
	firstAt  time.Time
	doneAt   time.Time
}

// Exchange is one request and its response while the proxy forwards them.
// Its methods are called by the proxy's goroutines; the body chunks are
// processed by a goroutine of its own.
type Exchange struct {
	run       *Run
	c         *Capturer
	id        string
	seq       int64
	startedAt time.Time

	method, path, query string
	kind                anthropicwire.ExchangeKind
	sessionID           string
	sessionEvidence     string
	retryCount          int
	reqHeaders          []api.HTTPHeader
	reqContentType      string
	reqContentEncoding  string

	ch chan chunk

	mu        sync.Mutex
	ended     bool
	parts     [2]partState
	lifecycle []api.HTTPLifecycleEvent
	degraded  []string

	status           int
	respHeaders      []api.HTTPHeader
	respContentType  string
	respContentEnc   string
	requestID        string
	responseAt       time.Time
	firstByteAt      time.Time
	transportErr     string
	transportErrAt   time.Time
	upstreamConnAt   time.Time
	clientAborted    bool
	upstreamAborted  bool
	unsupportedProto string

	// worker state (only touched by the worker goroutine)
	bodies    [2]*bodySink
	sse       *anthropicwire.SSEParser
	recon     *anthropicwire.Reconstructor
	events    []api.HTTPSSEEvent
	eventsCut int
	firstEvt  time.Time
	sseMode   int // how the response is read: sseUnknown, sseIncremental, sseNone, sseEncoded
	// respMarks: where each response chunk ended in the body and when it
	// arrived (compressed event streams only)
	respMarks []chunkMark
}

// How the worker reads the response (Exchange.sseMode)
const (
	sseUnknown     = iota // no response byte processed yet
	sseIncremental        // event stream: parsed as the chunks arrive
	sseNone               // not an event stream
	sseEncoded            // compressed event stream: decoded at the end, each event stamped with its chunk's arrival
)

// chunkMark is the end of a response chunk in the body and its arrival time
type chunkMark struct {
	end int64
	at  time.Time
}

// Begin starts recording an exchange of the run. It returns nil when the
// run has ended (the proxy then forwards without recording).
func (r *Run) Begin(req *http.Request) *Exchange {
	if !r.active.Load() {
		return nil
	}
	c := r.c
	now := time.Now()
	e := &Exchange{
		run:                r,
		c:                  c,
		id:                 NewID(),
		seq:                r.seq.Add(1),
		startedAt:          now,
		method:             req.Method,
		path:               req.URL.Path,
		query:              req.URL.RawQuery,
		kind:               anthropicwire.ClassifyEndpoint(req.Method, req.URL.Path),
		reqHeaders:         c.san.Headers(req.Header),
		reqContentType:     req.Header.Get("Content-Type"),
		reqContentEncoding: req.Header.Get("Content-Encoding"),
		ch:                 make(chan chunk, 512),
	}
	if id := strings.TrimSpace(req.Header.Get(SessionHeader)); sessionIDPattern.MatchString(id) {
		e.sessionID, e.sessionEvidence = id, "header:"+SessionHeader
	}
	if n, err := strconv.Atoi(req.Header.Get("X-Stainless-Retry-Count")); err == nil && n > 0 {
		e.retryCount = n
	}
	e.lifecycle = []api.HTTPLifecycleEvent{{Event: EventRequestStarted, At: now.UTC()}}
	for i := range e.bodies {
		e.bodies[i] = newBodySink(c, i)
	}
	c.stats.exchanges.Add(1)
	c.stats.inFlight.Add(1)
	selfmetrics.HTTPCaptureInFlight.With().Add(1)

	c.pipeline.enqueue(job{started: &storage.HTTPExchangeRecord{
		Exchange:               e.baseExchange(api.ExchangeInProgress),
		RequestHeaders:         e.reqHeaders,
		RequestContentType:     e.reqContentType,
		RequestContentEncoding: e.reqContentEncoding,
		Lifecycle:              append([]api.HTTPLifecycleEvent(nil), e.lifecycle...),
	}})

	c.workers.Add(1)
	go e.work()
	return e
}

// ID returns the exchange ID
func (e *Exchange) ID() string { return e.id }

// Seq returns the exchange's position in its run
func (e *Exchange) Seq() int64 { return e.seq }

// baseExchange returns the row fields known at the start
func (e *Exchange) baseExchange(state string) api.HTTPExchange {
	headline := "Requisição em andamento"
	if e.kind == anthropicwire.KindModelInference {
		headline = "Inferência em andamento"
	} else if e.kind == anthropicwire.KindTokenCount {
		headline = "Contagem de tokens em andamento"
	}
	return api.HTTPExchange{
		ID:              e.id,
		RunID:           e.run.ID,
		Seq:             e.seq,
		SessionID:       e.sessionID,
		SessionEvidence: e.sessionEvidence,
		Method:          e.method,
		Path:            e.path,
		Query:           e.query,
		Kind:            string(e.kind),
		State:           state,
		RetryCount:      e.retryCount,
		StartedAt:       e.startedAt.UTC(),
		Headline:        headline,
		ToolUses:        []string{},
		CaptureMode:     e.c.cfg.Mode,
	}
}

// note appends a lifecycle event (e.mu held)
func (e *Exchange) note(event, detail string) {
	e.lifecycle = append(e.lifecycle, api.HTTPLifecycleEvent{Event: event, At: time.Now().UTC(), Detail: detail})
}

// observe copies body bytes for the worker. It never blocks: when the
// shared buffer budget or the exchange's queue is full, the rest of that
// body is not captured (the exchange is marked degraded).
func (e *Exchange) observe(part int, p []byte) {
	n := int64(len(p))
	e.c.stats.bytesForwarded.Add(n)
	if part == reqPart {
		selfmetrics.HTTPCaptureBytes.With("request").Add(len(p))
	} else {
		selfmetrics.HTTPCaptureBytes.With("response").Add(len(p))
	}
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	st := &e.parts[part]
	if !st.started {
		st.started = true
		st.firstAt = now
		if part == respPart {
			e.firstByteAt = now
		}
	}
	if e.ended || st.lost {
		st.lostSize += n
		e.c.stats.lostBodyBytes.Add(n)
		return
	}
	if e.c.buffered.Add(n) > e.c.cfg.BufferBytes {
		e.c.buffered.Add(-n)
		e.loseLocked(part, n, "buffer de captura cheio")
		return
	}
	data := make([]byte, len(p))
	copy(data, p)
	select {
	case e.ch <- chunk{part: part, data: data, at: now}:
	default:
		e.c.buffered.Add(-n)
		e.loseLocked(part, n, "fila do exchange cheia")
	}
}

// loseLocked marks the rest of a body as not captured
func (e *Exchange) loseLocked(part int, n int64, why string) {
	st := &e.parts[part]
	st.lost = true
	st.lostSize += n
	e.c.stats.lostBodyBytes.Add(n)
	selfmetrics.HTTPCaptureDropped.With("buffer_full").Inc()
	name := "request"
	if part == respPart {
		name = "response"
	}
	e.degraded = append(e.degraded, "corpo do "+name+" incompleto na captura: "+why)
	e.note(EventCaptureDegraded, name+": "+why)
}

// partEnded records the end of reading a body
func (e *Exchange) partEnded(part int, err error, closed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := &e.parts[part]
	if st.done || st.closed || st.err != nil {
		return
	}
	st.doneAt = time.Now()
	switch {
	case err == nil && !closed:
		st.done = true
		if part == reqPart {
			e.note(EventRequestBody, "")
		}
	case err != nil:
		st.err = err
	default:
		st.closed = true
	}
}

// WrapRequestBody returns body, observed as the proxy reads it
func (e *Exchange) WrapRequestBody(body io.ReadCloser) io.ReadCloser {
	if body == nil || body == http.NoBody {
		e.partEnded(reqPart, nil, false)
		return body
	}
	return &observedBody{e: e, part: reqPart, rc: body}
}

// UpstreamConnected records the connection to the upstream
func (e *Exchange) UpstreamConnected(reused bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.upstreamConnAt.IsZero() {
		return
	}
	e.upstreamConnAt = time.Now()
	detail := "nova conexão"
	if reused {
		detail = "conexão reutilizada"
	}
	e.note(EventUpstreamConn, detail)
}

// ResponseHeaders records the upstream's response head and returns its
// body, observed as the proxy copies it to the client
func (e *Exchange) ResponseHeaders(resp *http.Response) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = resp.StatusCode
	e.respHeaders = e.c.san.Headers(resp.Header)
	e.respContentType = resp.Header.Get("Content-Type")
	e.respContentEnc = resp.Header.Get("Content-Encoding")
	e.requestID = resp.Header.Get("Request-Id")
	if e.requestID == "" {
		e.requestID = resp.Header.Get("X-Request-Id")
	}
	if len(e.requestID) > 200 {
		e.requestID = e.requestID[:200]
	}
	e.responseAt = time.Now()
	e.note(EventResponseHeaders, strconv.Itoa(resp.StatusCode))
	if isEventStream(e.respContentType) {
		e.note(EventResponseStream, "text/event-stream")
	}
	if resp.Body != nil && resp.Body != http.NoBody {
		resp.Body = &observedBody{e: e, part: respPart, rc: resp.Body}
	} else {
		e.parts[respPart].done = true
	}
}

// TransportError records a failure to get a response from the upstream
func (e *Exchange) TransportError(err error, status int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.transportErr = err.Error()
	e.transportErrAt = time.Now()
	if e.status == 0 {
		e.status = status
	}
	e.note(EventUpstreamError, err.Error())
}

// Unsupported records a request the proxy refused (such as a WebSocket
// upgrade), answered with status
func (e *Exchange) Unsupported(status int, why string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = status
	e.unsupportedProto = why
	e.note(EventUpstreamError, why)
}

// End finishes the exchange after the proxy has answered (or failed):
// clientGone tells whether the client went away. The record is finished
// and stored in the background.
func (e *Exchange) End(clientGone bool) {
	e.mu.Lock()
	if e.ended {
		e.mu.Unlock()
		return
	}
	e.ended = true
	resp := &e.parts[respPart]
	if clientGone && !resp.done {
		e.clientAborted = true
		e.note(EventClientAborted, "o cliente encerrou a conexão antes do fim da resposta")
	} else if resp.err != nil && !resp.done {
		e.upstreamAborted = true
		e.note(EventUpstreamAborted, resp.err.Error())
	}
	close(e.ch)
	e.mu.Unlock()
	e.c.stats.inFlight.Add(-1)
	selfmetrics.HTTPCaptureInFlight.With().Add(-1)
}

// observedBody passes a body through, observing what is read
type observedBody struct {
	e    *Exchange
	part int
	rc   io.ReadCloser
	once sync.Once
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.e.observe(b.part, p[:n])
	}
	if err != nil {
		b.once.Do(func() {
			if errors.Is(err, io.EOF) {
				b.e.partEnded(b.part, nil, false)
			} else {
				b.e.partEnded(b.part, err, false)
			}
		})
	}
	return n, err
}

func (b *observedBody) Close() error {
	b.once.Do(func() { b.e.partEnded(b.part, nil, true) })
	return b.rc.Close()
}

// bodySink keeps a body: in memory (safe and metadata_only modes, so
// unredacted bytes never reach the disk), or streamed to a spool file
// (full_raw), up to MaxBodyBytes; the SHA-256 covers every observed byte
type bodySink struct {
	c        *Capturer
	max      int64
	mem      bytes.Buffer
	spool    *Spool
	hash     hash.Hash
	size     int64 // bytes observed (processed)
	kept     int64
	spoolErr error
}

func newBodySink(c *Capturer, part int) *bodySink {
	return &bodySink{c: c, max: c.cfg.MaxBodyBytes, hash: sha256.New()}
}

func (s *bodySink) write(p []byte) {
	s.size += int64(len(p))
	s.hash.Write(p)
	room := s.max - s.kept
	if room <= 0 {
		return
	}
	if int64(len(p)) > room {
		p = p[:room]
	}
	if s.c.cfg.Mode == ModeFullRaw && s.c.blobs != nil && s.spoolErr == nil {
		if s.spool == nil {
			s.spool, s.spoolErr = s.c.blobs.CreateSpool()
		}
		if s.spoolErr == nil {
			if _, err := s.spool.Write(p); err != nil {
				s.spoolErr = err
			} else {
				s.kept += int64(len(p))
				return
			}
		}
	}
	s.mem.Write(p)
	s.kept += int64(len(p))
}

// bytes returns the kept body
func (s *bodySink) bytes() []byte {
	if s.spool != nil && s.spoolErr == nil {
		b, err := s.spool.ReadAll(s.max)
		if err == nil {
			return b
		}
		s.spoolErr = err
	}
	return s.mem.Bytes()
}

func (s *bodySink) sum() string { return hex.EncodeToString(s.hash.Sum(nil)) }

func (s *bodySink) truncated() bool { return s.size > s.kept }

// discard removes a spool file that was not kept
func (s *bodySink) discard() {
	if s.spool != nil {
		s.spool.Discard()
		s.spool = nil
	}
}

func isEventStream(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && strings.EqualFold(mt, "text/event-stream")
}

// work processes the body chunks, then finishes the record
func (e *Exchange) work() {
	defer e.c.workers.Done()
	for ch := range e.ch {
		e.process(ch)
		e.c.buffered.Add(-int64(len(ch.data)))
	}
	defer func() {
		if r := recover(); r != nil {
			e.c.stats.parseErrors.Add(1)
			selfmetrics.HTTPCaptureErrors.With("parse").Inc()
		}
	}()
	e.finish(context.Background())
}

// process handles one chunk in the worker
func (e *Exchange) process(ch chunk) {
	e.bodies[ch.part].write(ch.data)
	if ch.part != respPart {
		return
	}
	if e.sseMode == sseUnknown {
		e.mu.Lock()
		ct, enc := e.respContentType, e.respContentEnc
		e.mu.Unlock()
		switch {
		case !isEventStream(ct):
			e.sseMode = sseNone
		case enc == "" || strings.EqualFold(enc, "identity"):
			e.sseMode = sseIncremental
			e.sse = anthropicwire.NewSSEParser(0)
			e.recon = anthropicwire.NewReconstructor()
		default:
			e.sseMode = sseEncoded
		}
	}
	switch e.sseMode {
	case sseIncremental:
		for _, ev := range e.sse.Feed(ch.data) {
			e.addEvent(ev, ch.at)
		}
	case sseEncoded:
		e.respMarks = append(e.respMarks, chunkMark{end: e.bodies[respPart].size, at: ch.at})
	}
}

// timedReader reads a kept response body one arrived chunk per Read and
// remembers when the chunk it read last arrived: decoded bytes come out of
// a decoder only after the chunk holding them was read (see
// anthropicwire.NewStreamDecoder), so that time is their arrival
type timedReader struct {
	data  []byte
	marks []chunkMark
	pos   int64
	i     int
	at    time.Time
}

func (r *timedReader) Read(p []byte) (int, error) {
	if r.pos >= int64(len(r.data)) {
		return 0, io.EOF
	}
	for r.i < len(r.marks)-1 && r.marks[r.i].end <= r.pos {
		r.i++
	}
	end := int64(len(r.data))
	if r.i < len(r.marks) {
		end = min(end, r.marks[r.i].end)
		r.at = r.marks[r.i].at
	}
	n := copy(p, r.data[r.pos:end])
	r.pos += int64(n)
	return n, nil
}

// addEvent interprets an SSE event that arrived at "at"
func (e *Exchange) addEvent(ev anthropicwire.SSEEvent, at time.Time) {
	se := e.recon.Add(ev)
	e.c.stats.sseEvents.Add(1)
	selfmetrics.HTTPCaptureSSEEvents.With().Inc()
	if e.firstEvt.IsZero() {
		e.firstEvt = at
	}
	if len(e.events) >= e.c.cfg.MaxEvents {
		e.eventsCut++
		if e.eventsCut == 1 {
			selfmetrics.HTTPCaptureDropped.With("event_limit").Inc()
		}
		return
	}
	data := ""
	if e.c.cfg.Mode != ModeMetadataOnly {
		if out, _, ok := e.c.san.Body(ev.Data); ok {
			data = string(out)
		}
	}
	e.events = append(e.events, api.HTTPSSEEvent{
		Seq:         se.Seq,
		OffsetMs:    float64(at.Sub(e.startedAt).Microseconds()) / 1000,
		ByteOffset:  ev.ByteOffset,
		ByteLen:     ev.ByteLen,
		Event:       ev.Event,
		Type:        se.Type,
		Index:       se.Index,
		DeltaType:   se.DeltaType,
		ParseStatus: se.ParseStatus,
		Note:        se.Note,
		Data:        data,
		DataBytes:   int64(len(ev.Data)),
		Truncated:   ev.Truncated,
	})
}
