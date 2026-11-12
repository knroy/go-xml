package xslt

import (
	"context"
	goruntime "runtime"
	"testing"
)

// liveMarkWriter counts the bytes written and, as the count passes each of
// marks, collects garbage and records the live heap.
type liveMarkWriter struct {
	n     int
	marks []int
	live  []uint64
}

func (w *liveMarkWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	for len(w.marks) > 0 && w.n >= w.marks[0] {
		w.marks = w.marks[1:]
		goruntime.GC()
		var m goruntime.MemStats
		goruntime.ReadMemStats(&m)
		w.live = append(w.live, m.HeapAlloc)
	}
	return len(p), nil
}

// TestStreamOutLiveHeapFlat: streamed Output holds what is open, not what
// was written. 200,000 elements (the generator two small ranges, so it holds
// nothing that grows) are written while the live heap, measured after a
// collection at a tenth and at nine tenths of the output, stays level (0.2
// MB). A sink that kept the text of what it wrote (trim not compacting the
// text store) grows by about 3 MB between the two.
//
// The html case is the same under method="html", whose default is HTML5:
// its doctype waits only for the html element, and the prefixed svg:svg in
// each record is normalised as it is written (Serialization 3.1 §7.1), where
// a result held whole for both is the whole tree's size while it is
// written. indent="no", as html's default indent="yes" holds an element
// whose children are all elements as bytes until it ends (streamout.go).
func TestStreamOutLiveHeapFlat(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("measures the live heap")
	}
	for _, c := range []struct{ name, output, open, close, rec string }{
		{"xml", ``, `<out>`, `</out>`, `<r n="{.}"><xsl:value-of select="'item number', . * 2"/></r>`},
		{"html", `<xsl:output method="html" indent="no"/>`, `<html><body>`, `</body></html>`,
			`<p n="{.}"><xsl:value-of select="'item number', . * 2"/><svg:svg/></p>`},
	} {
		s := compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:svg="http://www.w3.org/2000/svg">`+
			c.output+`<xsl:template name="main">`+c.open+`<xsl:for-each select="1 to 200"><xsl:for-each select="1 to 1000">`+
			c.rec+`</xsl:for-each></xsl:for-each>`+c.close+`</xsl:template></xsl:stylesheet>`)
		run := func(w *liveMarkWriter) {
			if _, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Output: w}); err != nil {
				t.Fatal(err)
			}
		}
		size := &liveMarkWriter{}
		run(size)
		w := &liveMarkWriter{marks: []int{size.n / 10, size.n * 9 / 10}}
		run(w)
		if len(w.live) != 2 {
			t.Fatalf("%s: measured %d of 2 marks (output %d bytes)", c.name, len(w.live), size.n)
		}
		// The same result held as a tree and then serialized: a level heap
		// alone would not tell a streamed result from one written at End.
		held := func() uint64 {
			res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main"})
			if err != nil {
				t.Fatal(err)
			}
			h := &liveMarkWriter{marks: []int{size.n * 9 / 10}}
			if err := res.Serialize(h); err != nil {
				t.Fatal(err)
			}
			return h.live[0]
		}()
		grew := int64(w.live[1]) - int64(w.live[0])
		t.Logf("%s: live heap %.2f MB at 10%%, %.2f MB at 90%% of %d bytes; held %.2f MB",
			c.name, float64(w.live[0])/1e6, float64(w.live[1])/1e6, size.n, float64(held)/1e6)
		if grew > 1<<20 {
			t.Errorf("%s: live heap grew %.2f MB over 80%% of the output (160,000 elements)", c.name, float64(grew)/1e6)
		}
		if w.live[1] > held/2 {
			t.Errorf("%s: live heap %.2f MB at 90%%, the held result's %.2f MB: not streamed",
				c.name, float64(w.live[1])/1e6, float64(held)/1e6)
		}
	}
}
