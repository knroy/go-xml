package relaxng

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// compileRefNamed bounded its <ref> descent with a count:
//
//	const maxRefDepth = 500
//	if c.depth >= maxRefDepth { return nil, fmt.Errorf(... "recurses more than 500 deep") }
//
// The count could never do the job it was named for. Immediately above it,
// c.expanding — the set of definitions currently being compiled, entered on
// descent and deleted on unwind — already catches every re-entry into a
// definition still on the stack, and hands it to lazyRef or to the §4.19
// refusal. A recursive grammar therefore never reached the count. Nor did the
// count bound runtime recursion, which unfolds through lazyRef's resolve, and
// that builds a fresh compiler with depth 0 each time.
//
// What the count could reach was the acyclic case: 501 distinct definitions
// each <ref>ing the next is a legal, entirely non-recursive grammar, and it
// was refused with `definition "D500" recurses more than 500 deep`. Truncation
// returned a hard refusal, so a valid schema became uncompilable at a sharp
// cliff between 500 and 501.
//
// The count is gone; c.expanding is the whole termination argument. These
// tests pin both sides.
var refDepths = []int{1, 2, 63, 64, 65, 128, 256, 499, 500, 501, 512, 1024, 4096}

// refChain builds start -> element doc { ref D0 }, then n definitions where
// D{i} refs D{i+1} and the last holds <text/>. No definition reaches itself,
// and no <element> is crossed inside the chain, so the whole thing is expanded
// eagerly at compile time — the descent the counter was watching.
func refChain(n int) string {
	var b strings.Builder
	b.WriteString(`<grammar xmlns="http://relaxng.org/ns/structure/1.0">`)
	b.WriteString(`<start><element name="doc"><ref name="D0"/></element></start>`)
	for i := 0; i < n; i++ {
		if i == n-1 {
			fmt.Fprintf(&b, `<define name="D%d"><text/></define>`, i)
		} else {
			fmt.Fprintf(&b, `<define name="D%d"><ref name="D%d"/></define>`, i, i+1)
		}
	}
	b.WriteString(`</grammar>`)
	return b.String()
}

func compileString(t *testing.T, src string) (*Schema, error) {
	t.Helper()
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return Compile(st.Root)
}

// TestRefChainCompilesAtEveryDepth is the regression guard: a legal acyclic
// chain must compile, at 499 and at 501 alike.
func TestRefChainCompilesAtEveryDepth(t *testing.T) {
	for _, n := range refDepths {
		if _, err := compileString(t, refChain(n)); err != nil {
			t.Errorf("depth %d: legal acyclic ref chain refused: %v", n, err)
		}
	}
}

// TestRefChainStillValidates asserts the semantic property rather than that
// compilation returned non-nil: the grammar reached through the deep chain
// still accepts the document it describes and still rejects one it does not.
//
// The shallow case is checked by the same assertions at n=2, so a "correct"
// deep result cannot be correct for a reason that would also make an empty
// pattern look right.
func TestRefChainStillValidates(t *testing.T) {
	for _, n := range refDepths {
		s, err := compileString(t, refChain(n))
		if err != nil {
			t.Errorf("depth %d: compile: %v", n, err)
			continue
		}
		good, err := xdm.ParseString(`<doc>hello</doc>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(good.Root); err != nil {
			t.Errorf("depth %d: <doc>hello</doc> rejected: %v", n, err)
		}
		// The chain ends in <text/>, so an element child is not allowed.
		// If the deep chain had collapsed to "anything goes", this passes
		// where it should not.
		bad, err := xdm.ParseString(`<doc><nope/></doc>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(bad.Root); err == nil {
			t.Errorf("depth %d: <doc><nope/></doc> accepted; the chain's "+
				"<text/> is not being enforced", n)
		}
	}
}

// TestRefRecursionTerminates is what the bound was nominally there for. A
// definition that reaches itself across an <element> boundary is legal and
// describes arbitrarily deep nesting; it must compile, terminate, and then
// validate a document at whatever depth that document actually has.
func TestRefRecursionTerminates(t *testing.T) {
	src := `<grammar xmlns="http://relaxng.org/ns/structure/1.0">` +
		`<start><ref name="box"/></start>` +
		`<define name="box"><element name="box">` +
		`<choice><ref name="box"/><text/></choice>` +
		`</element></define></grammar>`
	s, err := compileString(t, src)
	if err != nil {
		t.Fatalf("a legal recursive grammar failed to compile: %v", err)
	}
	for _, depth := range []int{1, 2, 64, 500, 501, 1000} {
		doc := "x"
		for i := 0; i < depth; i++ {
			doc = "<box>" + doc + "</box>"
		}
		d, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("depth %d: parse doc: %v", depth, err)
		}
		if err := s.Validate(d.Root); err != nil {
			t.Errorf("nesting %d: rejected by a grammar that describes "+
				"arbitrary nesting: %v", depth, err)
		}
	}
}

// TestRefSelfRecursionWithoutElementRefused is the cyclic input the removed
// count was the only visible defence against, and it must still terminate and
// still be refused — by c.expanding, per section 4.19. A definition reaching
// itself with no <element> in between describes no finite document.
func TestRefSelfRecursionWithoutElementRefused(t *testing.T) {
	cases := map[string]string{
		"direct": `<define name="a"><ref name="a"/></define>`,
		"mutual": `<define name="a"><ref name="b"/></define>` +
			`<define name="b"><ref name="a"/></define>`,
	}
	for name, defs := range cases {
		src := `<grammar xmlns="http://relaxng.org/ns/structure/1.0">` +
			`<start><element name="doc"><ref name="a"/></element></start>` +
			defs + `</grammar>`
		_, err := compileString(t, src)
		if err == nil {
			t.Errorf("%s: a definition reaching itself with no intervening "+
				"<element> was accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), "4.19") {
			t.Errorf("%s: refused, but not as a 4.19 violation: %v", name, err)
		}
	}
}

// TestRefChainThroughElementsCompiles crosses an <element> at every link, so
// each definition is expanded under a deeper elementDepth. It is still
// acyclic, and still must compile past the old bound.
func TestRefChainThroughElementsCompiles(t *testing.T) {
	// Capped below the XML parser's own MaxDepth of 1000 nesting levels —
	// a deliberate resource bound on the document, not on the grammar.
	for _, n := range []int{2, 499, 501, 900} {
		var b strings.Builder
		b.WriteString(`<grammar xmlns="http://relaxng.org/ns/structure/1.0">`)
		b.WriteString(`<start><ref name="D0"/></start>`)
		for i := 0; i < n; i++ {
			if i == n-1 {
				fmt.Fprintf(&b, `<define name="D%d"><element name="e%d">`+
					`<text/></element></define>`, i, i)
			} else {
				fmt.Fprintf(&b, `<define name="D%d"><element name="e%d">`+
					`<ref name="D%d"/></element></define>`, i, i, i+1)
			}
		}
		b.WriteString(`</grammar>`)
		s, err := compileString(t, b.String())
		if err != nil {
			t.Errorf("depth %d: refused: %v", n, err)
			continue
		}
		doc := "x"
		for i := n - 1; i >= 0; i-- {
			doc = fmt.Sprintf("<e%d>%s</e%d>", i, doc, i)
		}
		d, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("depth %d: parse doc: %v", n, err)
		}
		if err := s.Validate(d.Root); err != nil {
			t.Errorf("depth %d: the document this grammar describes was "+
				"rejected: %v", n, err)
		}
	}
}
