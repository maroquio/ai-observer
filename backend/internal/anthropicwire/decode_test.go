package anthropicwire

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// encode compresses data with one coding
func encode(t testing.TB, coding string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	switch coding {
	case "gzip":
		w = gzip.NewWriter(&buf)
	case "zlib":
		w = zlib.NewWriter(&buf)
	case "flate":
		w, _ = flate.NewWriter(&buf, flate.DefaultCompression)
	case "br":
		w = brotli.NewWriter(&buf)
	case "zstd":
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		w = zw
	default:
		t.Fatalf("no encoder for %s", coding)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var decodeSample = []byte(strings.Repeat(`{"type":"content_block_delta","delta":{"text":"ação 🚀"}}`+"\n", 200))

func TestDecodeBodyCodings(t *testing.T) {
	tests := []struct {
		header string
		body   []byte
	}{
		{"gzip", encode(t, "gzip", decodeSample)},
		{"x-gzip", encode(t, "gzip", decodeSample)},
		{"deflate", encode(t, "zlib", decodeSample)},
		{"deflate", encode(t, "flate", decodeSample)},
		{"br", encode(t, "br", decodeSample)},
		{"zstd", encode(t, "zstd", decodeSample)},
		{" GZIP ", encode(t, "gzip", decodeSample)},
		{"identity, gzip", encode(t, "gzip", decodeSample)},
		{"gzip, br", encode(t, "br", encode(t, "gzip", decodeSample))},
		{"deflate,zstd", encode(t, "zstd", encode(t, "zlib", decodeSample))},
	}
	for _, tt := range tests {
		orig := bytes.Clone(tt.body)
		got, status, err := DecodeBody(tt.body, tt.header, 0)
		if err != nil || status != DecodeDecoded || !bytes.Equal(got, decodeSample) {
			t.Errorf("%q: status %s, err %v, %d bytes", tt.header, status, err, len(got))
		}
		if !bytes.Equal(tt.body, orig) {
			t.Errorf("%q: input modified", tt.header)
		}
	}
}

func TestDecodeBodyIdentity(t *testing.T) {
	for _, header := range []string{"", "identity", " Identity , ", ","} {
		got, status, err := DecodeBody(decodeSample, header, 10)
		if err != nil || status != DecodeIdentity || &got[0] != &decodeSample[0] || len(got) != len(decodeSample) {
			t.Errorf("%q: status %s, err %v", header, status, err)
		}
	}
	if got, status, err := DecodeBody(nil, "gzip", 0); err != nil || status != DecodeIdentity || len(got) != 0 {
		t.Errorf("empty gzip body: %s %v", status, err)
	}
}

func TestDecodeBodyFailures(t *testing.T) {
	garbage := []byte("this is not compressed data at all \x00\x01\x02\xff")
	for _, header := range []string{"compress", "gzip, lzma", "x-unknown"} {
		got, status, err := DecodeBody(encode(t, "gzip", decodeSample), header, 0)
		if err == nil || status != DecodeUnsupported || got != nil {
			t.Errorf("%q: %s %v", header, status, err)
		}
	}
	for _, header := range []string{"gzip", "deflate", "br", "zstd"} {
		orig := bytes.Clone(garbage)
		got, status, err := DecodeBody(garbage, header, 0)
		if err == nil || status != DecodeError || got != nil {
			t.Errorf("%q on garbage: %s %v", header, status, err)
		}
		if !bytes.Equal(garbage, orig) {
			t.Errorf("%q: input modified", header)
		}
	}
	// a complete stream cut short is corrupt, not truncated
	cut := encode(t, "gzip", decodeSample)
	if _, status, err := DecodeBody(cut[:len(cut)/2], "gzip", 0); status != DecodeError || err == nil {
		t.Errorf("cut gzip: %s %v", status, err)
	}
}

func TestDecodeBodyTruncation(t *testing.T) {
	for _, coding := range []string{"gzip", "zlib", "flate", "br", "zstd"} {
		header := coding
		if coding == "zlib" || coding == "flate" {
			header = "deflate"
		}
		body := encode(t, coding, decodeSample)
		for _, limit := range []int64{1, 10, 1000} {
			got, status, err := DecodeBody(body, header, limit)
			if err != nil || status != DecodeTruncated || !bytes.Equal(got, decodeSample[:limit]) {
				t.Errorf("%s limit %d: %s %v %q", coding, limit, status, err, got)
			}
		}
		got, status, err := DecodeBody(body, header, int64(len(decodeSample)))
		if err != nil || status != DecodeDecoded || !bytes.Equal(got, decodeSample) {
			t.Errorf("%s at exact limit: %s %v", coding, status, err)
		}
	}
	// truncation inside a chain of codings
	chain := encode(t, "br", encode(t, "gzip", decodeSample))
	got, status, err := DecodeBody(chain, "gzip, br", 100)
	if err != nil || status != DecodeTruncated || len(got) > 100 || !bytes.HasPrefix(decodeSample, got) {
		t.Errorf("chain: %s %v %d bytes", status, err, len(got))
	}
}

func TestDecodeBodyBomb(t *testing.T) {
	zeros := make([]byte, 8<<20)
	for _, coding := range []string{"gzip", "br", "zstd"} {
		bomb := encode(t, coding, zeros)
		got, status, err := DecodeBody(bomb, coding, 1<<20)
		if err != nil || status != DecodeTruncated || len(got) != 1<<20 {
			t.Errorf("%s bomb (%d bytes): %s %v %d", coding, len(bomb), status, err, len(got))
		}
	}
}
