package anthropicwire

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// The functions below walk JSON that json.Valid accepted, handing out the
// members and elements of objects and arrays as sub-slices of the original
// bytes: nothing is reformatted or copied, and each level is scanned once.
// Skipping is iterative, so nesting depth costs no stack. On malformed
// input they stop and report false; they never index out of range.

// jsonSpace tells whether c is JSON whitespace
func jsonSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// skipSpace returns the offset of the first non-space byte at or after i
func skipSpace(data []byte, i int) int {
	for i < len(data) && jsonSpace(data[i]) {
		i++
	}
	return i
}

// jsonValueEnd returns the offset just past the value starting at data[i],
// or -1 when the data ends first
func jsonValueEnd(data []byte, i int) int {
	if i < 0 || i >= len(data) {
		return -1
	}
	switch data[i] {
	case '"':
		return jsonStringEnd(data, i)
	case '{', '[':
		depth := 0
		for i < len(data) {
			switch data[i] {
			case '"':
				end := jsonStringEnd(data, i)
				if end < 0 {
					return -1
				}
				i = end
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return -1
	}
	j := i
	for j < len(data) && !jsonDelim(data[j]) {
		j++
	}
	if j == i {
		return -1
	}
	return j
}

// jsonDelim tells whether c ends a number or literal
func jsonDelim(c byte) bool {
	switch c {
	case ',', ':', '{', '}', '[', ']', '"':
		return true
	}
	return jsonSpace(c)
}

// jsonStringEnd returns the offset just past the string whose opening quote
// is data[i], or -1. A quote preceded by an odd run of backslashes is
// escaped; each run is counted once, so the scan stays linear.
func jsonStringEnd(data []byte, i int) int {
	j := i + 1
	for j <= len(data) {
		k := bytes.IndexByte(data[j:], '"')
		if k < 0 {
			return -1
		}
		q := j + k
		backslashes := 0
		for p := q - 1; p > i && data[p] == '\\'; p-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return q + 1
		}
		j = q + 1
	}
	return -1
}

// forEachMember calls fn with each member of the object obj, in order: the
// decoded key and the raw value. It reports false when obj is not an object.
func forEachMember(obj []byte, fn func(key string, val []byte)) bool {
	i := skipSpace(obj, 0)
	if i >= len(obj) || obj[i] != '{' {
		return false
	}
	i = skipSpace(obj, i+1)
	if i < len(obj) && obj[i] == '}' {
		return true
	}
	for i < len(obj) && obj[i] == '"' {
		keyEnd := jsonStringEnd(obj, i)
		if keyEnd < 0 {
			return false
		}
		key := obj[i:keyEnd]
		i = skipSpace(obj, keyEnd)
		if i >= len(obj) || obj[i] != ':' {
			return false
		}
		i = skipSpace(obj, i+1)
		end := jsonValueEnd(obj, i)
		if end < 0 {
			return false
		}
		fn(jsonKey(key), obj[i:end])
		i = skipSpace(obj, end)
		if i >= len(obj) {
			return false
		}
		switch obj[i] {
		case '}':
			return true
		case ',':
			i = skipSpace(obj, i+1)
		default:
			return false
		}
	}
	return false
}

// forEachElem calls fn with each element of the array arr, in order. It
// reports false when arr is not an array.
func forEachElem(arr []byte, fn func(i int, val []byte)) bool {
	i := skipSpace(arr, 0)
	if i >= len(arr) || arr[i] != '[' {
		return false
	}
	i = skipSpace(arr, i+1)
	if i < len(arr) && arr[i] == ']' {
		return true
	}
	for n := 0; i < len(arr); n++ {
		end := jsonValueEnd(arr, i)
		if end < 0 {
			return false
		}
		fn(n, arr[i:end])
		i = skipSpace(arr, end)
		if i >= len(arr) {
			return false
		}
		switch arr[i] {
		case ']':
			return true
		case ',':
			i = skipSpace(arr, i+1)
		default:
			return false
		}
	}
	return false
}

// jsonKey decodes a raw JSON string token as encoding/json does
func jsonKey(raw []byte) string {
	if len(raw) >= 2 {
		inner := raw[1 : len(raw)-1]
		if bytes.IndexByte(inner, '\\') < 0 && utf8.Valid(inner) {
			return string(inner)
		}
	}
	return rawString(raw)
}

// stringPrefix decodes the start of a raw JSON string: at least the first
// maxRaw bytes of its encoded content, cut after a whole escape sequence. It
// returns "" for anything that is not a string.
func stringPrefix(raw []byte, maxRaw int) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '"' {
		return ""
	}
	inner := raw[1 : len(raw)-1]
	if len(inner) <= maxRaw {
		return rawString(raw)
	}
	cut := 0
	for cut < maxRaw && cut < len(inner) {
		switch {
		case inner[cut] != '\\':
			cut++
		case cut+1 < len(inner) && inner[cut+1] == 'u':
			cut += 6
		default:
			cut += 2
		}
	}
	cut = min(cut, len(inner))
	buf := make([]byte, 0, cut+2)
	buf = append(buf, '"')
	buf = append(buf, inner[:cut]...)
	buf = append(buf, '"')
	return rawString(buf)
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// rawBool reads a raw JSON boolean; anything else is nil
func rawBool(raw json.RawMessage) *bool {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		v := true
		return &v
	case "false":
		v := false
		return &v
	}
	return nil
}

var cacheControlKey = []byte(`"cache_control"`)

// withoutCacheControl returns the JSON value raw without its
// "cache_control" members, at any depth, leaving every other byte as it
// is; raw itself when it has none. A key is recognized by what surrounds
// it ('{' or ',' before, ':' after), which a quote inside a string (always
// escaped) cannot have.
func withoutCacheControl(raw []byte) []byte {
	if !bytes.Contains(raw, cacheControlKey) {
		return raw
	}
	out := make([]byte, 0, len(raw))
	i := 0 // raw[:i] is decided
	for {
		j := bytes.Index(raw[i:], cacheControlKey)
		if j < 0 {
			break
		}
		j += i
		keyEnd := j + len(cacheControlKey)
		p := j - 1
		for p >= i && jsonSpace(raw[p]) {
			p--
		}
		colon := skipSpace(raw, keyEnd)
		if p < i || (raw[p] != '{' && raw[p] != ',') || colon >= len(raw) || raw[colon] != ':' {
			out = append(out, raw[i:keyEnd]...)
			i = keyEnd
			continue
		}
		end := jsonValueEnd(raw, skipSpace(raw, colon+1))
		if end < 0 {
			return raw
		}
		if raw[p] == ',' {
			// ...,"cache_control":{...}  → drop the comma before it
			out = append(out, raw[i:p]...)
			i = end
		} else {
			// {"cache_control":{...},... → drop the comma after it
			out = append(out, raw[i:j]...)
			next := skipSpace(raw, end)
			if next < len(raw) && raw[next] == ',' {
				next = skipSpace(raw, next+1)
			}
			i = next
		}
	}
	return append(out, raw[i:]...)
}
