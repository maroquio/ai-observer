package anthropicwire

import (
	"bytes"
	"math"
	"strconv"
)

// DefaultMaxSSEEventBytes is the data limit of an SSE event when
// NewSSEParser gets maxEventBytes <= 0
const DefaultMaxSSEEventBytes = 16 << 20

// sseLineSlack is how far a line may exceed the event data limit before the
// parser stops buffering it: room for the field name and the colon
const sseLineSlack = 64

var utf8BOM = []byte("\xEF\xBB\xBF")

// SSEParser frames a Server-Sent Events stream fed in arbitrary fragments,
// following the WHATWG event stream interpretation. Lines end with "\r\n",
// "\n" or "\r"; a UTF-8 BOM is skipped at the start of the stream. Work is
// linear in the input and memory is bounded by the per-event limit: data
// beyond it (and the part of any line beyond it) is dropped and the event
// flagged Truncated, and parsing of the following events is unaffected.
//
// Each blank line ends an event span: the next event's ByteOffset is the
// first byte after it, so the bytes of a block that dispatches nothing (only
// comments or retry, for instance) belong to no event.
//
// An SSEParser is not safe for concurrent use.
type SSEParser struct {
	max     int
	offset  int64 // bytes fed so far
	lines   int64 // lines processed, to find the first one (BOM)
	nextSeq int

	// partial line carried over between chunks (lineOpen), starting at
	// stream offset lineStart, capped at max+sseLineSlack
	line         []byte
	lineOpen     bool
	lineStart    int64
	lineOverflow bool
	// crPending: the last chunk ended with "\r", whose line waits for the
	// next byte to know whether "\n" completes the terminator
	crPending bool

	// event being built
	inEvent   bool
	evStart   int64
	data      []byte
	sawData   bool
	event     string
	sawEvent  bool
	id        string
	hasID     bool
	retry     *int64
	truncated bool

	comments int
}

// NewSSEParser returns a parser whose events keep at most maxEventBytes of
// data each (DefaultMaxSSEEventBytes when <= 0)
func NewSSEParser(maxEventBytes int) *SSEParser {
	if maxEventBytes <= 0 {
		maxEventBytes = DefaultMaxSSEEventBytes
	}
	// keep max+sseLineSlack from overflowing
	return &SSEParser{max: min(maxEventBytes, math.MaxInt-sseLineSlack)}
}

// Feed parses the next fragment of the stream, which may end anywhere (in a
// field name, inside JSON or a UTF-8 sequence, between "\r" and "\n"), and
// returns the events it completed, in order, with Seq counting from 0 across
// calls. An event whose blank line ends with a "\r" at the end of chunk is
// returned by the next Feed (or Finish), once the following byte is known.
func (p *SSEParser) Feed(chunk []byte) []SSEEvent {
	var out []SSEEvent
	base := p.offset
	p.offset += int64(len(chunk))
	i := 0
	if p.crPending && len(chunk) > 0 {
		p.crPending = false
		if chunk[0] == '\n' {
			i = 1
		}
		out = p.endBufferedLine(base+int64(i), out)
	}
	for i < len(chunk) {
		j := bytes.IndexAny(chunk[i:], "\r\n")
		if j < 0 {
			p.bufferLine(base+int64(i), chunk[i:])
			break
		}
		lineEnd := i + j
		var next int
		switch {
		case chunk[lineEnd] == '\n':
			next = lineEnd + 1
		case lineEnd+1 == len(chunk):
			// "\r" at the end of the chunk: wait for the next byte
			p.bufferLine(base+int64(i), chunk[i:lineEnd])
			p.crPending = true
			return out
		case chunk[lineEnd+1] == '\n':
			next = lineEnd + 2
		default:
			next = lineEnd + 1
		}
		if p.lineOpen {
			p.bufferLine(base+int64(i), chunk[i:lineEnd])
			out = p.endBufferedLine(base+int64(next), out)
		} else {
			line, overflow := p.capLine(chunk[i:lineEnd])
			out = p.processLine(line, overflow, base+int64(i), base+int64(next), out)
		}
		i = next
	}
	return out
}

// Finish ends the stream. A last line without terminator counts as a line.
// If an event has buffered fields (data, event or id) without its blank
// line, it is returned with its Seq, for the caller to treat as incomplete;
// otherwise Finish returns nil. The one exception is a stream whose last
// byte is the "\r" ending a blank line: that complete event is returned here.
func (p *SSEParser) Finish() *SSEEvent {
	var out []SSEEvent
	if p.lineOpen {
		p.crPending = false
		out = p.endBufferedLine(p.offset, out)
	}
	if len(out) == 0 && p.inEvent && (p.sawData || p.sawEvent || p.hasID) {
		out = append(out, p.dispatch(p.offset))
	}
	p.resetEvent()
	if len(out) == 0 {
		return nil
	}
	return &out[0]
}

// Offset returns the number of bytes fed so far
func (p *SSEParser) Offset() int64 { return p.offset }

// Comments returns the number of comment lines (starting with ":") seen
func (p *SSEParser) Comments() int { return p.comments }

// capLine limits a line to the event limit plus room for its field name
func (p *SSEParser) capLine(line []byte) ([]byte, bool) {
	if limit := p.max + sseLineSlack; len(line) > limit {
		return line[:limit], true
	}
	return line, false
}

// bufferLine appends part of a line, which starts at stream offset start
// when it is the first part, to the partial line, up to its cap
func (p *SSEParser) bufferLine(start int64, part []byte) {
	if !p.lineOpen {
		p.lineOpen = true
		p.lineStart = start
	}
	room := p.max + sseLineSlack - len(p.line)
	if len(part) > room {
		part = part[:max(room, 0)]
		p.lineOverflow = true
	}
	p.line = append(p.line, part...)
}

// endBufferedLine processes the partial line; end is the stream offset
// after its terminator
func (p *SSEParser) endBufferedLine(end int64, out []SSEEvent) []SSEEvent {
	out = p.processLine(p.line, p.lineOverflow, p.lineStart, end, out)
	if cap(p.line) > 64<<10 {
		p.line = nil // do not keep a huge buffer for short lines
	} else {
		p.line = p.line[:0]
	}
	p.lineOpen = false
	p.lineOverflow = false
	return out
}

// processLine interprets one line. start is the stream offset of its first
// byte, end the offset after its terminator.
func (p *SSEParser) processLine(line []byte, overflow bool, start, end int64, out []SSEEvent) []SSEEvent {
	if p.lines == 0 {
		line = bytes.TrimPrefix(line, utf8BOM)
		start = 0
	}
	p.lines++
	if len(line) == 0 && !overflow {
		if p.inEvent && (p.sawData || p.sawEvent || p.hasID) {
			out = append(out, p.dispatch(end))
		}
		p.resetEvent()
		return out
	}
	if !p.inEvent {
		p.inEvent = true
		p.evStart = start
	}
	if line[0] == ':' {
		p.comments++
		return out
	}
	name, value := line, []byte(nil)
	if c := bytes.IndexByte(line, ':'); c >= 0 {
		name, value = line[:c], line[c+1:]
		value = bytes.TrimPrefix(value, []byte(" "))
	}
	switch string(name) {
	case "data":
		p.appendData(value)
	case "event":
		p.event = string(value)
		p.sawEvent = true
	case "id":
		if bytes.IndexByte(value, 0) < 0 {
			p.id = string(value)
			p.hasID = true
		}
	case "retry":
		if isASCIIDigits(value) {
			if n, err := strconv.ParseInt(string(value), 10, 64); err == nil {
				p.retry = &n
			}
		}
	default:
		return out
	}
	if overflow {
		p.truncated = true
	}
	return out
}

// appendData adds a data line to the data buffer, joined to the previous
// ones with "\n", keeping the buffer within the event limit
func (p *SSEParser) appendData(value []byte) {
	if p.sawData {
		if len(p.data) >= p.max {
			p.truncated = true
			return
		}
		p.data = append(p.data, '\n')
	}
	p.sawData = true
	if room := p.max - len(p.data); len(value) > room {
		value = value[:room]
		p.truncated = true
	}
	p.data = append(p.data, value...)
}

// dispatch builds the current event, which ends at stream offset end
func (p *SSEParser) dispatch(end int64) SSEEvent {
	data := p.data[:len(p.data):len(p.data)]
	if data == nil {
		data = []byte{}
	}
	ev := SSEEvent{
		Seq:        p.nextSeq,
		Event:      p.event,
		Data:       data,
		ID:         p.id,
		HasID:      p.hasID,
		Retry:      p.retry,
		ByteOffset: p.evStart,
		ByteLen:    end - p.evStart,
		Truncated:  p.truncated,
	}
	p.nextSeq++
	p.data = nil
	return ev
}

// resetEvent forgets the event being built
func (p *SSEParser) resetEvent() {
	p.inEvent = false
	p.data = nil
	p.sawData = false
	p.event = ""
	p.sawEvent = false
	p.id = ""
	p.hasID = false
	p.retry = nil
	p.truncated = false
}

// isASCIIDigits tells whether b is a non-empty run of ASCII digits
func isASCIIDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
