package c14n

import (
	"bufio"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
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
			w := bufio.NewWriter(&sb)
			writeEscaped(w, "a"+string([]byte{byte(b)})+"z", tc.attr)
			_ = w.Flush()
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
