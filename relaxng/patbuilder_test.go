package relaxng

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// withMemoAfter runs f with the builder starting at element n.
func withMemoAfter(n int, f func()) {
	old := memoAfter
	memoAfter = n
	defer func() { memoAfter = old }()
	f()
}

// TestLongDocumentRemembersDerivatives: past memoAfter elements a validation
// interns its patterns and remembers its derivatives, so a document that
// keeps returning to one state -- a table's rows -- stops rebuilding the
// derivative for every row. Measured: 4,000 rows cost 47 allocations a row
// without the builder and 12 with it.
func TestLongDocumentRemembersDerivatives(t *testing.T) {
	const xsd = ` datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes"`
	s := compileBoundarySchema(t, `<element name="t"`+rngNS+xsd+`><oneOrMore>
		<element name="row">
			<optional><attribute name="a"><choice><value>x</value><value>y</value></choice></attribute></optional>
			<oneOrMore><element name="entry">
				<optional><attribute name="c"><data type="NMTOKEN"/></attribute></optional>
				<text/></element></oneOrMore></element></oneOrMore></element>`)
	const rows = 4000
	doc, err := xdm.ParseString("<t>"+strings.Repeat(
		`<row a="x"><entry c="c1">1</entry><entry c="c2">2</entry><entry>3</entry></row>`, rows)+"</t>",
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Fatal(err)
	}
	n := testing.AllocsPerRun(3, func() { s.Validate(doc.Root) })
	if n/rows > 20 {
		t.Errorf("%.0f allocations a row; the derivatives are not remembered", n/rows)
	}
}

// TestRememberedAttributeKeepsNamespaceContext: an attribute derivative is
// remembered by state, name and value, not by the namespace bindings, so one
// that consulted them -- a QName value -- must not be remembered: p:a means
// {urn:1}a on the first x and {urn:2}a on the second.
func TestRememberedAttributeKeepsNamespaceContext(t *testing.T) {
	const xsd = ` datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes"`
	s := compileBoundarySchema(t, `<element name="r"`+rngNS+xsd+` xmlns:p="urn:1"><zeroOrMore>
		<element name="x"><attribute name="q"><value type="QName">p:a</value></attribute></element>
		</zeroOrMore></element>`)
	doc, err := xdm.ParseString(`<r><x q="p:a" xmlns:p="urn:1"/><x q="p:a" xmlns:p="urn:1"/>`+
		`<x q="p:a" xmlns:p="urn:2"/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	withMemoAfter(1, func() {
		if err := s.Validate(doc.Root); err == nil {
			t.Error("q=p:a with p bound to urn:2 was accepted")
		}
	})
}

// TestBuilderLeavesPatternSizeAlone: interning must not change what
// MaxPatternSize measures, so the bound fires on the same documents with the
// builder as without it. Merging alternatives on pointer identity would break
// this: patEq never equates patterns holding data, identity would.
func TestBuilderLeavesPatternSizeAlone(t *testing.T) {
	for i, c := range []struct{ schema, doc string }{
		{`<element name="r"` + rngNS + `><oneOrMore><oneOrMore><choice>
			<element name="a"><empty/></element><element name="b"><empty/></element>
			<group><element name="c"><data type="string" datatypeLibrary=""/></element><element name="d"><empty/></element></group>
			</choice></oneOrMore></oneOrMore></element>`,
			`<r><a/><b/><a/><c>1</c><d/><b/><a/><c>2</c><d/><a/></r>`},
		{`<element name="r"` + rngNS + `><oneOrMore><oneOrMore><oneOrMore>
			<element name="a"><attribute name="v"><value>1</value></attribute></element>
			</oneOrMore></oneOrMore></oneOrMore></element>`,
			`<r>` + strings.Repeat(`<a v="1"/>`, 30) + `</r>`},
	} {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s := compileBoundarySchema(t, c.schema)
		var pOff, pOn int
		var eOff, eOn string
		withMemoAfter(1<<30, func() { pOff, eOff = peakSize(t, s, doc.Root) })
		withMemoAfter(1, func() { pOn, eOn = peakSize(t, s, doc.Root) })
		if pOff != pOn || eOff != eOn {
			t.Errorf("case %d: without the builder peak %d, error %q; with it, peak %d, error %q",
				i, pOff, eOff, pOn, eOn)
		}
	}
}
