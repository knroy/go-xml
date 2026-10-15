package c14n

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// writeEscaped escapes through two 256-entry tables. Every byte is checked
// against C14N 1.0 §2.3, written out independently here: text escapes & < >
// and CR; attribute values escape & < " TAB LF and CR; nothing else changes.
func TestWriteEscapedEveryByte(t *testing.T) {
	text := map[byte]string{'&': "&amp;", '<': "&lt;", '>': "&gt;", '\r': "&#xD;"}
	attr := map[byte]string{'&': "&amp;", '<': "&lt;", '"': "&quot;", '\t': "&#x9;", '\n': "&#xA;", '\r': "&#xD;"}
	for _, tc := range []struct {
		attr bool
		want map[byte]string
	}{{false, text}, {true, attr}} {
		for b := 0; b < 256; b++ {
			var sb strings.Builder
			w := &outBuf{w: &sb}
			writeEscaped(w, "a"+string([]byte{byte(b)})+"z", tc.attr)
			w.flush()
			want, ok := tc.want[byte(b)]
			if !ok {
				want = string([]byte{byte(b)})
			}
			if got := sb.String(); got != "a"+want+"z" {
				t.Errorf("attr=%v byte %#02x: got %q, want %q", tc.attr, b, got, "a"+want+"z")
			}
		}
	}
}

// writeCounter counts the writes that reach it.
type writeCounter struct{ n, bytes int }

func (w *writeCounter) Write(p []byte) (int, error) {
	w.n++
	w.bytes += len(p)
	return len(p), nil
}

// The output buffer is 64 KiB, so this document's 850 KB of canonical
// output reaches the writer in 14 writes; with bufio's default 4 KiB it took
// 209.
func TestOutputReachesWriterInLargeChunks(t *testing.T) {
	doc, err := xdm.ParseString("<r>"+strings.Repeat("<a b='&amp;'>text &lt;</a>", 1<<15)+"</r>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var w writeCounter
	if err := Write(&w, doc.Root, Options{Algorithm: Inclusive10}); err != nil {
		t.Fatal(err)
	}
	if max := w.bytes/outBufSize + 1; w.n > max || outBufSize < 64<<10 {
		t.Errorf("%d bytes reached the writer in %d writes, want at most %d with a buffer of at least 64 KiB", w.bytes, w.n, max)
	}
}

// maxWrite records the largest write that reaches it.
type maxWrite struct {
	strings.Builder
	max int
}

func (w *maxWrite) Write(p []byte) (int, error) {
	w.max = max(w.max, len(p))
	return w.Builder.Write(p)
}

// A text node larger than the output buffer is escaped a buffer's worth at a
// time, so the buffer does not grow to the node's size. Escapes fall on the
// chunk boundaries too, and the output is the same as escaping it whole.
func TestLargeTextIsWrittenInChunks(t *testing.T) {
	text := strings.Repeat("a<b&", 3*outBufSize/4+1) // 3 buffers and a bit
	doc, err := xdm.ParseString("<r>"+strings.NewReplacer("<", "&lt;", "&", "&amp;").Replace(text)+"</r>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var w maxWrite
	if err := Write(&w, doc.Root, Options{Algorithm: Inclusive10}); err != nil {
		t.Fatal(err)
	}
	want := "<r>" + strings.NewReplacer("<", "&lt;", "&", "&amp;").Replace(text) + "</r>"
	if w.String() != want {
		t.Fatalf("output differs from escaping the text whole (%d bytes, want %d)", w.Len(), len(want))
	}
	// One chunk escapes to at most five times its size ("&amp;").
	if limit := outBufSize + 5*outBufSize; w.max > limit {
		t.Errorf("largest write %d bytes, want at most %d", w.max, limit)
	}
}
