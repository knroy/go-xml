package relaxng

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// stripMemo undoes addMemoPoints in place, on a schema compiled for this
// purpose alone (its refPats are its own).
func stripMemo(p pattern, refs map[*refPat]bool) pattern {
	switch t := p.(type) {
	case *refPat:
		if t.static != nil {
			return stripMemo(t.cached, refs)
		}
		if !refs[t] {
			refs[t] = true
			if t.done && t.err == nil {
				t.cached = stripMemo(t.cached, refs)
			}
		}
		return t
	case elementPat:
		return elementPat{t.Name, stripMemo(t.Pattern, refs)}
	case choicePat:
		return choicePat{stripMemo(t.Left, refs), stripMemo(t.Right, refs)}
	case groupPat:
		return groupPat{stripMemo(t.Left, refs), stripMemo(t.Right, refs)}
	case interleavePat:
		return interleavePat{stripMemo(t.Left, refs), stripMemo(t.Right, refs)}
	case oneOrMorePat:
		return oneOrMorePat{stripMemo(t.Pattern, refs)}
	}
	return p
}

// peakSize is the smallest MaxPatternSize the document validates under,
// which is the largest derivative the validation carried, and the error at
// the default limit.
func peakSize(t *testing.T, s *Schema, doc *xdm.Node) (int, string) {
	t.Helper()
	msg := ""
	if err := s.Validate(doc); err != nil {
		msg = err.Error()
	}
	fires := func(n int) bool {
		err := s.ValidateWithOptions(doc, ValidateOptions{MaxPatternSize: n})
		return err != nil && strings.Contains(err.Error(), "derivative pattern exceeds")
	}
	lo, hi := 1, DefaultMaxPatternSize
	for lo < hi {
		mid := (lo + hi) / 2
		if fires(mid) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, msg
}

// TestMemoPointsAreInvisible: the static wrappers memo.go adds must not change
// a verdict, an error, or the size MaxPatternSize measures, and they must be
// used: after one validation the wrapper around r's content remembers the
// derivative for the element it opened.
func TestMemoPointsAreInvisible(t *testing.T) {
	alts := ""
	for i := 0; i < 8; i++ {
		alts += fmt.Sprintf(`<element name="e%d"><optional><attribute name="x"/></optional><text/></element>`, i)
	}
	cases := []struct{ schema, doc string }{
		// A choice of elements under oneOrMore: the DocBook shape.
		{`<element name="r" xmlns="http://relaxng.org/ns/structure/1.0"><zeroOrMore><choice>` + alts +
			`</choice></zeroOrMore></element>`,
			`<r><e1>a</e1><e3 x="1"/><e1/><e7>b</e7></r>`},
		{`<element name="r" xmlns="http://relaxng.org/ns/structure/1.0"><zeroOrMore><choice>` + alts +
			`</choice></zeroOrMore></element>`,
			`<r><e1>a</e1><e3 y="1"/></r>`},
		// Nested oneOrMore, whose derivative grows with every child: the
		// shape patternSize exists for.
		{`<element name="r" xmlns="http://relaxng.org/ns/structure/1.0"><oneOrMore><oneOrMore><choice>
			<element name="a"><empty/></element><element name="b"><empty/></element>
			<group><element name="c"><data type="string" datatypeLibrary=""/></element><element name="d"><empty/></element></group>
			</choice></oneOrMore></oneOrMore></element>`,
			`<r><a/><b/><a/><c>1</c><d/><b/><a/><c>2</c><d/><a/></r>`},
		{`<element name="r" xmlns="http://relaxng.org/ns/structure/1.0"><interleave><oneOrMore><choice>
			<element name="a"><empty/></element><element name="b"><empty/></element></choice></oneOrMore>
			<optional><element name="c"><value>v</value></element></optional><text/></interleave></element>`,
			`<r><a/>t<c>v</c><b/><a/></r>`},
	}
	for i, c := range cases {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		memo := compileBoundarySchema(t, c.schema)
		plain := compileBoundarySchema(t, c.schema)
		plain.start = stripMemo(plain.start, map[*refPat]bool{})

		pm, em := peakSize(t, memo, doc.Root)
		pp, ep := peakSize(t, plain, doc.Root)
		if pm != pp || em != ep {
			t.Errorf("case %d: with memo points peak %d, error %q; without, peak %d, error %q",
				i, pm, em, pp, ep)
		}
	}

	s := compileBoundarySchema(t, cases[0].schema)
	content := s.start.(elementPat).Pattern
	w, ok := content.(*refPat)
	if !ok || w.static == nil {
		t.Fatalf("r's content is %T, want a static memo point", content)
	}
	doc, _ := xdm.ParseString(cases[0].doc, xdm.ParseOptions{})
	if err := s.Validate(doc.Root); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.open.Load(xdm.QName{Local: "e1"}); !ok {
		t.Error("the memo point did not remember startTagOpenDeriv for e1")
	}
	if !patEq(w, w.cached) || patternSize(w, 1<<30) != patternSize(w.cached, 1<<30) {
		t.Error("a memo point must compare and measure as the subtree it holds")
	}
}

// TestAttDerivMemoOnlyWhereTheValueCannotMatter: a memo point remembers
// attDeriv by attribute name only where every attribute pattern the name
// reaches takes any value. x is an xs:int, so <a x="z"/> must still fail
// after <a x="1"/> has been validated; y is text, so its derivative is kept.
func TestAttDerivMemoOnlyWhereTheValueCannotMatter(t *testing.T) {
	s := compileBoundarySchema(t, `<element name="r" xmlns="http://relaxng.org/ns/structure/1.0"
		datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes"><zeroOrMore>
		<element name="a"><interleave>
			<optional><attribute name="x"><data type="int"/></attribute></optional>
			<optional><attribute name="y"><text/></attribute></optional>
			<optional><attribute name="z"><text/></attribute></optional>
		</interleave></element></zeroOrMore></element>`)
	for _, c := range []struct {
		doc   string
		valid bool
	}{
		{`<r><a x="1" y="q"/><a y="p" z="o"/></r>`, true},
		{`<r><a x="1" y="q"/><a x="z" y="q"/></r>`, false},
		{`<r><a y="q"/><a x="z"/></r>`, false},
	} {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(doc.Root); (err == nil) != c.valid {
			t.Errorf("%s: got %v, want valid=%v", c.doc, err, c.valid)
		}
	}
	a := s.start.(elementPat).Pattern.(*refPat).cached
	for {
		if o, ok := unstatic(a).(oneOrMorePat); ok {
			a = o.Pattern
			break
		}
		a = unstatic(a).(choicePat).Left
	}
	w, ok := a.(elementPat).Pattern.(*refPat)
	if !ok || w.static == nil {
		t.Fatalf("a's content is %T, want a memo point", a.(elementPat).Pattern)
	}
	if b, ok := w.static.att.Load(xdm.QName{Local: "y"}); !ok || b.(*patBox).p == nil {
		t.Error("attDeriv for the text attribute y was not remembered")
	}
	if b, ok := w.static.att.Load(xdm.QName{Local: "x"}); ok && b.(*patBox).p != nil {
		t.Error("attDeriv for the typed attribute x was remembered")
	}
}
