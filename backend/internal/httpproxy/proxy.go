// Package httpproxy is the HTTP capture proxy: a reverse proxy from a local
// port to one fixed upstream (the Anthropic API by default) that hands what
// it forwards to internal/httpcapture.
//
// It is not an open proxy: every request goes to the configured upstream,
// whatever its Host, absolute-form URI or path say; CONNECT and protocol
// upgrades are refused. Requests from web pages (with an Origin header) and
// with Host names that are no IP or localhost are refused, so other sites
// cannot use a user's credentials through it (CSRF, DNS rebinding).
//
// The proxy forwards requests and responses unchanged apart from hop-by-hop
// headers, Host and the URL; compressed bodies stay compressed. It adds no
// retries or timeouts of its own to streaming responses.
package httpproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tobilg/ai-observer/internal/httpcapture"
	"github.com/tobilg/ai-observer/internal/logger"
)

// ValidateUpstream parses the upstream base URL: https, or http to a
// loopback address (tests, local gateways); no credentials, path, query or
// fragment
func ValidateUpstream(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL: %w", err)
	}
	if u.User != nil {
		return nil, errors.New("upstream URL must not contain credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("upstream URL must not have a query or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("upstream URL must not have a path")
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("upstream URL needs a host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(host) {
			return nil, errors.New("upstream URL must use https (http only to a loopback address)")
		}
	default:
		return nil, fmt.Errorf("upstream URL scheme %q is not supported (https)", u.Scheme)
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

func isLoopback(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hostAccepted reports whether a request's Host header may reach the
// proxy: IPs, localhost and the names in allowed
func hostAccepted(hostport string, allowed map[string]bool) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	switch {
	case host == "":
		return true
	case host == "localhost" || strings.HasSuffix(host, ".localhost"):
		return true
	case net.ParseIP(host) != nil:
		return true
	}
	return allowed[host]
}

// NewTransport returns the transport to the upstream: no transparent
// decompression (bodies pass as sent), HTTP/2 when offered, the
// environment's proxy settings, no response timeout (long streams)
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	t.ForceAttemptHTTP2 = true
	t.MaxIdleConnsPerHost = 16
	t.ResponseHeaderTimeout = 0
	return t
}

// Handler forwards requests to the upstream and records them in the run
// returned by run (none when it returns nil)
type Handler struct {
	upstream *url.URL
	proxy    *httputil.ReverseProxy
	run      func() *httpcapture.Run
	allowed  map[string]bool
}

// NewHandler returns a proxy handler for upstream
func NewHandler(upstream *url.URL, transport http.RoundTripper, allowedHosts []string, run func() *httpcapture.Run) *Handler {
	h := &Handler{upstream: upstream, run: run, allowed: map[string]bool{}}
	for _, name := range allowedHosts {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" && name != "*" {
			h.allowed[name] = true
		}
	}
	h.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			// SetURL joins the upstream's (empty) path with the request's; the
			// Host header names the upstream. X-Forwarded-* are not added.
			pr.Out.Host = upstream.Host
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorLog:      nil,
		ModifyResponse: func(resp *http.Response) error {
			if ex := exchangeOf(resp.Request.Context()); ex != nil {
				ex.ResponseHeaders(resp)
			}
			return nil
		},
		ErrorHandler: h.upstreamError,
	}
	return h
}

type exchangeKey struct{}

func exchangeOf(ctx context.Context) *httpcapture.Exchange {
	ex, _ := ctx.Value(exchangeKey{}).(*httpcapture.Exchange)
	return ex
}

// anthropicError writes an error in the Anthropic API's format
func anthropicError(w http.ResponseWriter, status int, typ, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-AI-Observer-Proxy", "error")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": typ, "message": message},
	})
}

// ServeHTTP forwards a request
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		anthropicError(w, http.StatusForbidden, "permission_error", "AI Observer HTTP proxy: requests from web pages (Origin header) are not accepted")
		return
	}
	if !hostAccepted(r.Host, h.allowed) {
		logger.Warn("HTTP proxy: rejected request with disallowed Host header", "host", r.Host)
		anthropicError(w, http.StatusForbidden, "permission_error", "AI Observer HTTP proxy: host not allowed")
		return
	}
	if r.Method == http.MethodConnect || (r.URL.IsAbs() && r.URL.Host != "") || !strings.HasPrefix(r.URL.Path, "/") {
		anthropicError(w, http.StatusBadRequest, "invalid_request_error", "AI Observer HTTP proxy: not a forward proxy; set ANTHROPIC_BASE_URL to this address")
		return
	}

	var ex *httpcapture.Exchange
	if run := h.run(); run != nil {
		ex = run.Begin(r)
	}
	if isUpgrade(r) {
		if ex != nil {
			ex.Unsupported(http.StatusNotImplemented, "protocol upgrade ("+r.Header.Get("Upgrade")+") is not supported by the capture proxy")
			ex.End(false)
		}
		anthropicError(w, http.StatusNotImplemented, "api_error", "AI Observer HTTP proxy: protocol upgrades (WebSocket) are not supported")
		return
	}
	if ex == nil {
		h.proxy.ServeHTTP(w, r)
		return
	}

	cw := &clientWriter{ResponseWriter: w}
	ctx := context.WithValue(r.Context(), exchangeKey{}, ex)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { ex.UpstreamConnected(info.Reused) },
	})
	r = r.WithContext(ctx)
	if r.Body != nil {
		r.Body = ex.WrapRequestBody(r.Body)
	} else {
		ex.WrapRequestBody(nil)
	}
	defer func() {
		// ReverseProxy aborts the handler (http.ErrAbortHandler) when copying
		// a streamed response fails: the exchange still ends and is stored
		rec := recover()
		ex.End(cw.failed() || r.Context().Err() != nil)
		if rec != nil {
			panic(rec)
		}
	}()
	h.proxy.ServeHTTP(cw, r)
}

// upstreamError answers a request the upstream did not answer: 504 on
// timeouts, 502 otherwise; nothing when the client went away
func (h *Handler) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	ex := exchangeOf(r.Context())
	if r.Context().Err() != nil {
		if ex != nil {
			ex.TransportError(err, 0)
		}
		return
	}
	status := http.StatusBadGateway
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		status = http.StatusGatewayTimeout
	}
	if ex != nil {
		ex.TransportError(err, status)
	}
	logger.Warn("HTTP proxy: upstream request failed", "upstream", h.upstream.Host, "path", r.URL.Path, "error", err)
	anthropicError(w, status, "api_error", "AI Observer HTTP proxy: upstream "+h.upstream.Host+" unreachable: "+err.Error())
}

func isUpgrade(r *http.Request) bool {
	if r.Header.Get("Upgrade") == "" {
		return false
	}
	for _, v := range r.Header.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
				return true
			}
		}
	}
	return false
}

// clientWriter remembers whether writing to the client failed (the client
// went away); Flush and the rest reach the connection through Unwrap
type clientWriter struct {
	http.ResponseWriter
	mu  sync.Mutex
	err error
}

func (c *clientWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	if err != nil {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
	}
	return n, err
}

func (c *clientWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *clientWriter) failed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err != nil
}

// newServer returns the HTTP server of a proxy listener: no write timeout
// (streams last minutes), header and idle timeouts
func newServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       5 * time.Minute,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          nil,
	}
}
