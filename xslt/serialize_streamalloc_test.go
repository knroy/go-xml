package xslt

import (
	"bytes"
	"io"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// streamDoc is n copies of an element holding text, a comment and a PI, so
// that every path below has a node per copy to allocate for.
func streamDoc(t *testing.T, n int) xdm.Sequence {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<r>`)
	for range n {
		b.WriteString(`<a>x ]]&gt; y &#xA0; z<!--c--><?p d?></a>`)
	}
	b.WriteString(`</r>`)
	tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return xdm.One(tree.Root)
}

// The cdata-section-elements path built each section in a builder, a
// character map allocated a segment slice per text node, and comments and PIs
// were concatenated before being written. Each cost an allocation per node,
// so the count must not grow with the number of nodes.
func TestSerializeStreamAllocsFlat(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under -race")
	}
	for _, tc := range []struct {
		name string
		opts OutputSettings
		cmap map[rune]string
	}{
		{"cdata", OutputSettings{Method: "xml", CDataElements: []xdm.QName{{Local: "a"}}}, nil},
		{"charmap", OutputSettings{Method: "xml"}, map[rune]string{' ': "&nbsp;"}},
		{"comment-pi", OutputSettings{Method: "xml"}, nil},
	} {
		count := func(seq xdm.Sequence) float64 {
			return testing.AllocsPerRun(10, func() {
				if err := Serialize(io.Discard, seq, tc.opts, tc.cmap); err != nil {
					t.Fatal(err)
				}
			})
		}
		few, many := count(streamDoc(t, 5)), count(streamDoc(t, 500))
		if many > few+5 {
			t.Errorf("%s: 500 nodes %.0f allocations, 5 nodes %.0f; want the same", tc.name, many, few)
		}
	}
}

// The text method concatenated the whole string value before writing it, a
// copy of the output and its builder's growth on top. It now writes each text
// node as it comes, so what it allocates must not grow with the output.
func TestSerializeTextMethodStreams(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under -race")
	}
	seq := streamDoc(t, 20000)
	var out bytes.Buffer
	if err := Serialize(&out, seq, OutputSettings{Method: "text"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := allocBytes(func() {
		if err := Serialize(io.Discard, seq, OutputSettings{Method: "text"}, nil); err != nil {
			t.Fatal(err)
		}
	}); got > uint64(out.Len())/4 {
		t.Errorf("text method allocated %d bytes for %d bytes of output", got, out.Len())
	}
}

// allocBytes is the heap f allocates, the least of three runs so that a
// collection landing inside one does not count against it.
func allocBytes(f func()) uint64 {
	best := ^uint64(0)
	for range 3 {
		var a, b goruntime.MemStats
		goruntime.ReadMemStats(&a)
		f()
		goruntime.ReadMemStats(&b)
		best = min(best, b.TotalAlloc-a.TotalAlloc)
	}
	return best
}
