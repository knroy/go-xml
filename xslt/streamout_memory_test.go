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
func TestStreamOutLiveHeapFlat(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("measures the live heap")
	}
	s := compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		`<xsl:template name="main"><out><xsl:for-each select="1 to 200"><xsl:for-each select="1 to 1000">`+
		`<r n="{.}"><xsl:value-of select="'item number', . * 2"/></r></xsl:for-each></xsl:for-each></out></xsl:template></xsl:stylesheet>`)
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
		t.Fatalf("measured %d of 2 marks (output %d bytes)", len(w.live), size.n)
	}
	grew := int64(w.live[1]) - int64(w.live[0])
	t.Logf("live heap %.2f MB at 10%%, %.2f MB at 90%% of %d bytes", float64(w.live[0])/1e6, float64(w.live[1])/1e6, size.n)
	if grew > 1<<20 {
		t.Errorf("live heap grew %.2f MB over 80%% of the output (160,000 elements)", float64(grew)/1e6)
	}
}
