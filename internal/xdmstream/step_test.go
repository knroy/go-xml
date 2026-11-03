package xdmstream

import (
	"runtime"
	"strings"
	"testing"
)

// TestCursorParsesOnTheReader: the parse runs in steps on the reader's
// goroutine (xdmburst.Steps), not as a coroutine of its own: reading starts
// no goroutine, and the events are those of the tree (a prolog, an entity
// restart, leaves after the document element: every place a step pauses).
func TestCursorParsesOnTheReader(t *testing.T) {
	src := `<!--a--><?p x?><!DOCTYPE r [<!ENTITY m "<b>m</b>">]><r>t&m;<!--c--><s/>u</r><!--z-->`
	before := runtime.NumGoroutine()
	c := Open(strings.NewReader(src), opts)
	if _, err := c.Next(); err != nil {
		t.Fatal(err)
	}
	if n := runtime.NumGoroutine(); n != before {
		t.Fatalf("%d goroutines while reading, %d before", n, before)
	}
	c.Close()
	c = Open(strings.NewReader(src), opts)
	defer c.Close()
	same(t, drain(t, c, nil), treeEvents(t, src, opts, nil))
}
