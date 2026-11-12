package xdm

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A tag given k attributes by k ATTLIST declarations is matched by lookup,
// not by scanning every attribute for every declaration: 4,000 declarations
// on 20 elements took seconds (fn:parse-xml reaches this with no option set).
func TestAttListManyDeclsLinear(t *testing.T) {
	const k, n = 4000, 20
	var b strings.Builder
	b.WriteString(`<!DOCTYPE r [<!ATTLIST a`)
	for i := range k {
		fmt.Fprintf(&b, ` x%d NMTOKEN " v%d "`, i, i)
	}
	b.WriteString(` id ID #IMPLIED>]><r xmlns:p="urn:p">`)
	b.WriteString(`<a id=" i0 " p:x1=" w " x2="u"/>`)
	b.WriteString(strings.Repeat(`<a/>`, n-1))
	b.WriteString(`</r>`)

	done := make(chan error, 1)
	var tree *Tree
	go func() {
		var err error
		tree, err = ParseString(b.String(), ParseOptions{AllowDOCTYPE: true})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("parse of 4,000 ATTLIST defaults on 20 elements did not finish in 3s")
	}

	a := tree.Root.FirstChild().FirstChild()
	if got := a.NumAttrs(); got != k+2 { // k defaults (x2 given), id, p:x1
		t.Errorf("first <a> has %d attributes, want %d", got, k+2)
	}
	for _, c := range []struct{ uri, local, want, typ string }{
		{"", "x0", "v0", ""},     // a default, collapsed
		{"", "x2", "u", ""},      // given, so not defaulted
		{"urn:p", "x1", "w", ""}, // matched by its local name, collapsed
		{"", "id", "i0", "ID"},   // collapsed and typed
		{"", fmt.Sprint("x", k-1), fmt.Sprint("v", k-1), ""},
	} {
		at := a.Attr(c.uri, c.local)
		if at == nil {
			t.Errorf("no attribute {%s}%s", c.uri, c.local)
			continue
		}
		if at.Value() != c.want || at.TypeAnnotation() != c.typ {
			t.Errorf("{%s}%s = %q typed %q, want %q typed %q", c.uri, c.local, at.Value(), at.TypeAnnotation(), c.want, c.typ)
		}
	}
}
