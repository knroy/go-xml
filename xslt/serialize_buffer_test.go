package xslt

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

type countingWriter struct {
	buf    bytes.Buffer
	writes int
	failAt int // fail the nth write when > 0
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	if c.failAt > 0 && c.writes >= c.failAt {
		return 0, errors.New("disk full")
	}
	return c.buf.Write(p)
}

// Serialize buffers a writer that is not in memory: a file got one system
// call per token. The bytes are those written to a bytes.Buffer, and a write
// error is still returned.
func TestSerializeBuffersWriter(t *testing.T) {
	src := "<r>" + strings.Repeat(`<a b="1">x &amp; y</a>`, 2000) + "</r>"
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seq := xdm.One(tree.Root)
	var want bytes.Buffer
	if err := Serialize(&want, seq, OutputSettings{Encoding: "UTF-8"}, nil); err != nil {
		t.Fatal(err)
	}
	var cw countingWriter
	if err := Serialize(&cw, seq, OutputSettings{Encoding: "UTF-8"}, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cw.buf.Bytes(), want.Bytes()) {
		t.Fatal("buffered output differs from the in-memory output")
	}
	if cw.writes > 10 {
		t.Errorf("%d writes for %d bytes, want at most 10", cw.writes, want.Len())
	}
	fw := countingWriter{failAt: 1}
	if err := Serialize(&fw, seq, OutputSettings{Encoding: "UTF-8"}, nil); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("write error: got %v, want disk full", err)
	}
}
