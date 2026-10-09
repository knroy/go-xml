package relaxng

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
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

// Each definition is compiled once and shared by every <ref> naming it. Before
// that, a <ref> re-compiled the definition's body, so a chain of definitions
// each referring to the next twice cost 2^links expansions: DocBook 5.2 hit
// maxRefExpansions, and this 40-link chain was refused by it. Shared, the
// chain compiles in one expansion per definition.
func TestRefExpansionIsShared(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<grammar xmlns="http://relaxng.org/ns/structure/1.0">` +
		`<start><element name="e"><ref name="d0"/></element></start>`)
	const links = 40
	for i := 0; i < links; i++ {
		b.WriteString(`<define name="d` + itoa(i) + `"><group>` +
			`<ref name="d` + itoa(i+1) + `"/><ref name="d` + itoa(i+1) + `"/>` +
			`</group></define>`)
	}
	b.WriteString(`<define name="d` + itoa(links) + `"><element name="x"><empty/></element></define></grammar>`)

	done := make(chan error, 1)
	go func() { _, err := compileSrcNoFatal(b.String()); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a chain of 40 shared definitions was refused: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("compilation did not finish in 30s")
	}
}

// Sharing a compiled definition must not hide a section 4.19 violation. Here
// c is first compiled under the <element>, where its way back to d is legal,
// and then reused by d's second branch, where d reaches itself through c with
// no <element> between.
func TestSharedDefinitionKeeps419(t *testing.T) {
	src := `<grammar xmlns="http://relaxng.org/ns/structure/1.0">
		<start><element name="r"><ref name="d"/></element></start>
		<define name="d"><choice>
			<element name="e"><ref name="c"/></element>
			<ref name="c"/>
		</choice></define>
		<define name="c"><ref name="d"/></define>
	</grammar>`
	_, err := compileSrcNoFatal(src)
	if err == nil || !strings.Contains(err.Error(), "4.19") {
		t.Fatalf("got %v, want a section 4.19 refusal", err)
	}
}

// The same, with the way back to d running through two definitions, b and c,
// that began at the same depth: the name d is noted in b, the innermost, and
// reaches c's shared set only when b finishes and hands its set up.
func TestSharedDefinitionKeeps419ThroughAChain(t *testing.T) {
	src := `<grammar xmlns="http://relaxng.org/ns/structure/1.0">
		<start><element name="r"><ref name="d"/></element></start>
		<define name="d"><choice>
			<element name="e"><ref name="c"/></element>
			<ref name="c"/>
		</choice></define>
		<define name="c"><ref name="b"/></define>
		<define name="b"><ref name="d"/></define>
	</grammar>`
	_, err := compileSrcNoFatal(src)
	if err == nil || !strings.Contains(err.Error(), "4.19") {
		t.Fatalf("got %v, want a section 4.19 refusal", err)
	}
}

// A shared definition reached twice is two occurrences for sections 7.2 and
// 7.4, not one: the checks over the compiled pattern must not treat the
// second as already accounted for.
func TestSharedDefinitionCountsTwice(t *testing.T) {
	for _, c := range []struct{ combinator, body, section string }{
		{"interleave", `<group><element name="x"><empty/></element><empty/></group>`, "7.4"},
		{"group", `<group><data type="string"/><empty/></group>`, "7.2"},
	} {
		src := `<grammar xmlns="http://relaxng.org/ns/structure/1.0"
			datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes">
			<start><element name="r"><` + c.combinator + `>
				<ref name="a"/><ref name="a"/>
			</` + c.combinator + `></element></start>
			<define name="a">` + c.body + `</define>
		</grammar>`
		_, err := compileSrcNoFatal(src)
		if err == nil || !strings.Contains(err.Error(), c.section) {
			t.Errorf("%s: got %v, want a section %s refusal", c.combinator, err, c.section)
		}
	}
}
