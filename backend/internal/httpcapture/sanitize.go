package httpcapture

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/redact"
)

// sensitiveKeys are JSON object keys whose string values are replaced
// whole, whatever they look like
var sensitiveKeys = map[string]bool{
	"authorization": true, "proxy-authorization": true, "api_key": true, "apikey": true, "api-key": true,
	"x-api-key": true, "x-goog-api-key": true, "password": true, "passwd": true, "secret": true,
	"client_secret": true, "access_token": true, "refresh_token": true, "id_token": true, "token": true,
	"session_token": true, "private_key": true, "cookie": true, "set-cookie": true,
}

// Sanitizer redacts secrets (and, in redaction mode "all", personal data)
// in captured bodies before they are stored in the safe variant. It works
// on decoded JSON string values, never on the serialized text, so JSON
// stays valid and key order is kept.
type Sanitizer struct {
	r *redact.Redactor
}

// NewSanitizer returns a sanitizer with the store's redactor. A redactor
// that is off still masks values of sensitive keys and headers.
func NewSanitizer(r *redact.Redactor) *Sanitizer {
	return &Sanitizer{r: r}
}

// String redacts a text value
func (s *Sanitizer) String(v string) string {
	if s == nil || !s.r.Enabled() {
		return v
	}
	return s.r.String(v)
}

// JSON returns body with the secrets in its string values redacted, and the
// number of values replaced. When nothing is replaced it returns body
// itself (the bytes as observed). Numbers are kept as written.
func (s *Sanitizer) JSON(body []byte) ([]byte, int, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var out bytes.Buffer
	out.Grow(len(body))
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)

	type frame struct {
		object bool
		count  int  // elements (array) or keys+values (object) written
		key    bool // the next string of an object is a key
		last   string
	}
	var stack []*frame
	redactions := 0
	writeString := func(v string) {
		enc.Encode(v)
		out.Truncate(out.Len() - 1) // Encode adds a newline
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if d, ok := tok.(json.Delim); ok && (d == '}' || d == ']') {
			out.WriteByte(byte(d))
			stack = stack[:len(stack)-1]
			if len(stack) > 0 {
				stack[len(stack)-1].count++
			}
			continue
		}
		// separators before a key, an array element or a value
		if top != nil {
			switch {
			case top.object && top.key:
				if top.count > 0 {
					out.WriteByte(',')
				}
			case top.object:
				out.WriteByte(':')
			case top.count > 0:
				out.WriteByte(',')
			}
		}
		isKey := top != nil && top.object && top.key
		switch v := tok.(type) {
		case json.Delim:
			out.WriteByte(byte(v))
			stack = append(stack, &frame{object: v == '{', key: v == '{'})
			if top != nil && top.object {
				top.key = true
			}
			continue
		case string:
			if isKey {
				writeString(v)
				top.last = v
				top.key = false
				continue
			}
			var replaced string
			if top != nil && top.object && sensitiveKeys[strings.ToLower(top.last)] && v != "" {
				replaced = "[REDACTED:" + strings.ToLower(top.last) + "]"
			} else {
				replaced = s.String(v)
			}
			if replaced != v {
				redactions++
			}
			writeString(replaced)
		case json.Number:
			out.WriteString(v.String())
		case bool:
			if v {
				out.WriteString("true")
			} else {
				out.WriteString("false")
			}
		case nil:
			out.WriteString("null")
		}
		if top != nil {
			if top.object {
				top.key = true
				top.count++
			} else {
				top.count++
			}
		}
	}
	if redactions == 0 {
		return body, 0, nil
	}
	return out.Bytes(), redactions, nil
}

// Body sanitizes a decoded body: JSON by its string values, other UTF-8
// text as a whole. Bytes that are no valid UTF-8 cannot be sanitized:
// stored reports false and the safe variant is not stored.
func (s *Sanitizer) Body(body []byte) (out []byte, redactions int, stored bool) {
	if len(body) == 0 {
		return body, 0, true
	}
	if json.Valid(body) {
		if out, n, err := s.JSON(body); err == nil {
			return out, n, true
		}
	}
	if !utf8.Valid(body) {
		return nil, 0, false
	}
	text := string(body)
	if red := s.String(text); red != text {
		return []byte(red), strings.Count(red, "[REDACTED"), true
	}
	return body, 0, true
}

// sensitiveHeader reports whether a header's value is a credential or
// session secret, never stored even in full_raw mode
func sensitiveHeader(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "authorization", "proxy-authorization", "x-api-key", "api-key", "x-goog-api-key", "cookie", "set-cookie",
		"anthropic-admin-api-key", "x-amz-security-token":
		return true
	}
	return strings.Contains(n, "token") || strings.Contains(n, "secret") || strings.Contains(n, "password") ||
		strings.Contains(n, "api-key") || strings.Contains(n, "apikey") || strings.HasSuffix(n, "-auth")
}

// Headers returns the headers as stored: sorted by name, credentials
// replaced (keeping only an authorization scheme), other values redacted
// like bodies
func (s *Sanitizer) Headers(h http.Header) []api.HTTPHeader {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]api.HTTPHeader, 0, len(h))
	for _, name := range names {
		for _, value := range h[name] {
			if sensitiveHeader(name) {
				masked := "[REDACTED]"
				if scheme, _, ok := strings.Cut(value, " "); ok && strings.EqualFold(name, "authorization") && len(scheme) < 16 {
					masked = scheme + " [REDACTED]"
				}
				out = append(out, api.HTTPHeader{Name: name, Value: masked, Redacted: true})
				continue
			}
			red := s.String(value)
			out = append(out, api.HTTPHeader{Name: name, Value: red, Redacted: red != value})
		}
	}
	return out
}
