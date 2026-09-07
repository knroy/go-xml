package relaxng

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

func itoa(i int) string { return strconv.Itoa(i) }

// compileSrcNoFatal compiles without a *testing.T, so it can run on a
// goroutine that the test is timing.
func compileSrcNoFatal(src string) (*Schema, error) {
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	return Compile(tree.Root)
}

// §7.3's second clause requires an <attribute> with an open name class —
// anyName or nsName, which admit unboundedly many names — to sit under a
// repetition, so that a document carrying two such attributes has an
// unambiguous reading.
//
// The spec writes that as a <oneOrMore> ancestor and says §7 applies to the
// simplified grammar. §4.20 rewrites <zeroOrMore>p as choice(oneOrMore p,
// empty), so a <zeroOrMore> supplies the ancestor the rule asks for. This
// package checks §7 before compiling, on the tree as written, where that
// rewrite has not yet happened — so <zeroOrMore> has to be accepted here in
// its own right. Requiring the literal <oneOrMore> refused DocBook 5.1,
// XSpec and the SVRL schema, all of which write <zeroOrMore>.
func TestOpenNameAttributeRepetition(t *testing.T) {
	const wrap = `<element name="e" xmlns="http://relaxng.org/ns/structure/1.0">%s</element>`

	accept := []struct{ name, body string }{
		{"oneOrMore/anyName", `<oneOrMore><attribute><anyName/></attribute></oneOrMore>`},
		{"zeroOrMore/anyName", `<zeroOrMore><attribute><anyName/></attribute></zeroOrMore>`},
		{"zeroOrMore/nsName", `<zeroOrMore><attribute><nsName ns="urn:x"/></attribute></zeroOrMore>`},
		{"zeroOrMore/choice-with-anyName", `<zeroOrMore><attribute><choice><anyName/><name>a</name></choice></attribute></zeroOrMore>`},
		{"zeroOrMore/anyName-except", `<zeroOrMore><attribute><anyName><except><name>a</name></except></anyName></attribute></zeroOrMore>`},
		// The repetition need not be the immediate parent.
		{"zeroOrMore//group/anyName", `<zeroOrMore><group><attribute><anyName/></attribute><text/></group></zeroOrMore>`},
	}
	for _, c := range accept {
		if _, err := compileSrc(t, strings.Replace(wrap, "%s", c.body, 1)); err != nil {
			t.Errorf("%s: refused, want accepted: %v", c.name, err)
		}
	}

	// The rule still has to bite. An open name class with no repetition
	// anywhere above it is exactly what §7.3 refuses, and relaxing the
	// ancestor test to admit <zeroOrMore> must not have relaxed that.
	refuse := []struct{ name, body string }{
		{"bare/anyName", `<attribute><anyName/></attribute>`},
		{"bare/nsName", `<attribute><nsName ns="urn:x"/></attribute>`},
		{"bare/choice-with-anyName", `<attribute><choice><anyName/><name>a</name></choice></attribute>`},
		{"group/two-bare-anyName", `<group><attribute><anyName/></attribute><attribute><anyName/></attribute></group>`},
		// <optional> is choice(p, empty) after §4.20 — no oneOrMore in it.
		{"optional/anyName", `<optional><attribute><anyName/></attribute></optional>`},
		// An <element> is a barrier: the outer repetition does not reach in.
		{"zeroOrMore//element/anyName", `<zeroOrMore><element name="f"><attribute><anyName/></attribute></element></zeroOrMore>`},
	}
	for _, c := range refuse {
		_, err := compileSrc(t, strings.Replace(wrap, "%s", c.body, 1))
		if err == nil {
			t.Errorf("%s: accepted, want refused", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "section 7.3") {
			t.Errorf("%s: refused for the wrong reason: %v", c.name, err)
		}
	}

	// A name class with no open component is not this rule's business at all.
	if _, err := compileSrc(t, strings.Replace(wrap,
		"%s", `<attribute name="a"><text/></attribute>`, 1)); err != nil {
		t.Errorf("named attribute: refused, want accepted: %v", err)
	}
}

// §4.19 expands every <ref> in place and discards the <define>, so a
// definition has no standing of its own in the simplified grammar §7 applies
// to: what encloses the attribute is whatever each <ref> supplies. An
// <attribute> written directly in a <define> body is therefore reached by the
// standalone walk with nothing above it, which is not "no repetition encloses
// it" but "nothing encloses it yet". Judging §7.3's second clause there
// refused every schema that factors an open name class into a definition and
// repeats the <ref> — which is how DocBook, XSpec and SVRL all write it.
func TestOpenNameAttributeInDefine(t *testing.T) {
	const grammar = `<grammar xmlns="http://relaxng.org/ns/structure/1.0">
		<start><element name="e">%s</element></start>
		<define name="anyAttr"><attribute><anyName/></attribute></define>
	</grammar>`

	for _, c := range []struct{ name, body string }{
		{"ref-under-zeroOrMore", `<zeroOrMore><ref name="anyAttr"/></zeroOrMore>`},
		{"ref-under-oneOrMore", `<oneOrMore><ref name="anyAttr"/></oneOrMore>`},
		{"define-never-referenced", `<empty/>`},
	} {
		if _, err := compileSrc(t, strings.Replace(grammar, "%s", c.body, 1)); err != nil {
			t.Errorf("%s: refused, want accepted: %v", c.name, err)
		}
	}
}

// Expanding a <ref> re-compiles the definition's body, and nothing shares
// that work between two <ref>s naming the same definition. A chain of
// definitions each referring to several others therefore costs
// multiplicatively rather than additively: DocBook 5.1 had not compiled after
// 140 s, and a 70-definition schema already expands 14140 times.
//
// maxRefExpansions does not make that cheaper. It makes it bounded, the way
// MaxPatternSize bounds the same shape of blowup during validation — a schema
// that would once have hung now stops with a message that says what happened.
// The chain below is small enough to write down and still doubles at every
// link, so it crosses the budget in well under a second.
func TestRefExpansionIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<grammar xmlns="http://relaxng.org/ns/structure/1.0">` +
		`<start><element name="e"><ref name="d0"/></element></start>`)
	const links = 40
	for i := 0; i < links; i++ {
		b.WriteString(`<define name="d` + itoa(i) + `"><group>` +
			`<ref name="d` + itoa(i+1) + `"/><ref name="d` + itoa(i+1) + `"/>` +
			`</group></define>`)
	}
	b.WriteString(`<define name="d` + itoa(links) + `"><empty/></define></grammar>`)

	done := make(chan error, 1)
	go func() { _, err := compileSrcNoFatal(b.String()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a grammar doubling at each of 40 links compiled; the budget did not bite")
		}
		if !strings.Contains(err.Error(), "<ref> expansions") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("compilation did not finish in 30s; the budget did not bite")
	}
}
