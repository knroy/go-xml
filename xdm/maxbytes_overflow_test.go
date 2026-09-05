package xdm

import (
	"math"
	"runtime"
	"strings"
	"testing"
)

// The largest byte limit a caller can name must not refuse every document.
//
// The reader is wrapped in io.LimitReader(r, maxBytes+1) so that hitting the
// limit is distinguishable from a document exactly at it. At math.MaxInt64
// that addition overflows to math.MinInt64, io.LimitReader reads a negative
// limit as "nothing left", and every document failed with "no root element" --
// the one setting a caller would choose to mean "do not limit me" was the one
// setting that rejected valid input.
func TestMaxBytesAtMaxInt64(t *testing.T) {
	for _, mb := range []int64{math.MaxInt64, math.MaxInt64 - 1, 1 << 40} {
		if _, err := ParseString(`<r/>`, ParseOptions{MaxBytes: mb}); err != nil {
			t.Errorf("MaxBytes=%d: %v", mb, err)
		}
	}
	// The limit must still be enforced where a document really does exceed it.
	if _, err := ParseString(`<r/>`, ParseOptions{MaxBytes: 3}); err == nil {
		t.Error("MaxBytes=3 should refuse a 4-byte document")
	}
}

// MaxBytes is documented as bounding the source document, and the code says
// the limit "wraps the reader, so it bounds what is read rather than what a
// caller remembered to check". For UTF-16 it bounded neither.
//
// decodeReader ran BEFORE the io.LimitReader/countingReader wrap, and
// utf16Reader.fill calls io.ReadAll on the raw source — it has to, because the
// encoding declaration it rewrites sits at the front of text a streaming
// decoder would already have handed on. So the whole document was pulled in
// and decoded to UTF-8 before a single byte was counted, and the limit only
// described the refusal, not its cost: measured, 8 MB of UTF-16 allocated
// 138 MB against a MaxBytes of 1024. fill's own comment concedes the whole-
// input read and justifies it by "schema and instance documents are small
// enough", which is not a property attacker-supplied input has.
func TestMaxBytesBoundsUTF16Input(t *testing.T) {
	utf16LE := func(s string) string {
		b := []byte{0xFF, 0xFE}
		for _, r := range s {
			b = append(b, byte(r), byte(r>>8))
		}
		return string(b)
	}

	big := utf16LE("<r>" + strings.Repeat("x", 4<<20) + "</r>")
	src := strings.NewReader(big)

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	_, err := Parse(src, ParseOptions{MaxBytes: 1024})
	runtime.ReadMemStats(&m1)

	if err == nil {
		t.Fatalf("%d bytes of UTF-16 accepted against a MaxBytes of 1024", len(big))
	}
	if !strings.Contains(err.Error(), "exceeds 1024 bytes") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
	// The refusal must cost about the limit, not about the input. A generous
	// ceiling: decodeReader's own 4 KB bufio buffer and the decoder's are
	// fixed costs, but nothing proportional to the 8 MB source may appear.
	if got := m1.TotalAlloc - m0.TotalAlloc; got > 1<<20 {
		t.Fatalf("refusing a 1024-byte-limited parse allocated %d bytes "+
			"from a %d byte source; the limit bounds nothing", got, len(big))
	}

	// A UTF-16 document inside the limit still parses: the bound must not be
	// achieved by breaking UTF-16 support, which XML 1.0 §4.3.3 makes
	// mandatory.
	tr, err := Parse(strings.NewReader(utf16LE(`<r>hello</r>`)), ParseOptions{MaxBytes: 1024})
	if err != nil {
		t.Fatalf("a small UTF-16 document was refused: %v", err)
	}
	if got := tr.Root.StringValue(); got != "hello" {
		t.Fatalf("UTF-16 decoded to %q, want %q", got, "hello")
	}
}
