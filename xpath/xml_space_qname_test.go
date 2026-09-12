package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// fn:QName, fn:resolve-QName and fn:normalize-unicode all reduce to XML S, and
// all three reached it through strings.TrimSpace, which is Go's whole Unicode
// White_Space set.
//
// For the two QName functions the basis is F&O 10.1.1 and 10.1.2: FOCA0002
// unless the argument "does not have the correct lexical form for an instance
// of xs:QName", and xs:QName has whiteSpace="collapse", whose whitespace is
// exactly #x20 #x9 #xD #xA. A no-break space is an ordinary character there,
// so a name spelled with one has no valid lexical form and must be refused.
// isNCName already refuses it -- the trim was the whole admission path.
//
// For fn:normalize-unicode the basis is F&O 5.4.6, which defines the effective
// value of $normalizationForm as fn:upper-case(fn:normalize-space($form)).
// fn:normalize-space is XML S only, so an NBSP survives into the form name,
// which is then not one of the recognised forms and must raise FOCH0003.
//
// Each case pairs the NBSP spelling with the XML-whitespace one: the fix must
// reject the first WITHOUT rejecting the second, which is what distinguishes a
// correct whitespace set from simply deleting the trim.
func TestQNameFunctionsUseXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"

	doc, err := xdm.ParseString(`<p xmlns:eg="http://example.com/eg">x</p>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// fn:resolve-QName resolves against the namespace bindings of its element
	// argument, so the focus has to be the element that carries xmlns:eg and
	// not the document node above it -- otherwise "eg" is unbound and the
	// FONS0004 that follows would be mistaken for a whitespace refusal.
	var el *xdm.Node
	for _, k := range doc.Root.Children {
		if k.Kind == xdm.KindElement {
			el = k
			break
		}
	}
	if el == nil {
		t.Fatal("no document element")
	}

	eval := func(expr string) error {
		t.Helper()
		ctx := NewContext(el, Builtins())
		ctx.Version = XPath31
		ctx.LibraryVersion = XPath31
		_, err := Eval(expr, ctx, cardinalityNS{})
		return err
	}

	tests := []struct {
		name    string
		expr    string
		wantErr string // "" means the call must succeed
	}{
		{
			name:    "fn:QName leading NBSP",
			expr:    `fn:QName("http://example.com/eg", "` + nbsp + `eg:a")`,
			wantErr: "FOCA0002",
		},
		{
			name:    "fn:QName trailing NBSP",
			expr:    `fn:QName("http://example.com/eg", "eg:a` + nbsp + `")`,
			wantErr: "FOCA0002",
		},
		{
			name: "fn:QName leading and trailing XML space still accepted",
			expr: "fn:QName(\"http://example.com/eg\", \"  eg:a\t\")",
		},
		{
			name:    "fn:resolve-QName leading NBSP",
			expr:    `fn:resolve-QName("` + nbsp + `eg:a", .)`,
			wantErr: "FOCA0002",
		},
		{
			name:    "fn:resolve-QName trailing NBSP",
			expr:    `fn:resolve-QName("eg:a` + nbsp + `", .)`,
			wantErr: "FOCA0002",
		},
		{
			name: "fn:resolve-QName XML space still accepted",
			expr: "fn:resolve-QName(\" eg:a\n\", .)",
		},
		{
			name:    "normalize-unicode form padded with NBSP",
			expr:    `fn:normalize-unicode("a", "` + nbsp + `NFC` + nbsp + `")`,
			wantErr: "FOCH0003",
		},
		{
			name: "normalize-unicode form padded with XML space still accepted",
			expr: "fn:normalize-unicode(\"a\", \" NFC\t\")",
		},
		{
			// The effective value is a collapse, not merely a trim, so an
			// interior run of XML S is removed too -- but "N F C" collapses to
			// "N F C", which is still not a recognised form.
			name:    "normalize-unicode form with interior space is unrecognised",
			expr:    `fn:normalize-unicode("a", "N F C")`,
			wantErr: "FOCH0003",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := eval(tc.expr)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("%s: %v; XML whitespace is permitted here and must "+
					"not be refused", tc.expr, err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("%s was accepted; a no-break space is an ordinary "+
					"character in this lexical form, so %s is required",
					tc.expr, tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("%s: got %v, want %s", tc.expr, err, tc.wantErr)
			}
		})
	}
}

// An NBSP inside the name, rather than around it, was always refused: only the
// trim admitted it. This pins that the fix did not widen anything -- the
// interior case must still fail, and for the same reason.
func TestQNameInteriorNBSPStillRefused(t *testing.T) {
	const nbsp = "\u00a0"
	doc, err := xdm.ParseString(`<p>x</p>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := NewContext(doc.Root, Builtins())
	ctx.Version = XPath31
	ctx.LibraryVersion = XPath31
	expr := `fn:QName("http://example.com/eg", "a` + nbsp + `b")`
	if _, err := Eval(expr, ctx, cardinalityNS{}); err == nil {
		t.Errorf("%s was accepted; an interior no-break space is not an "+
			"NCName character, so FOCA0002 is required", expr)
	}
}

// checkLexicalQName's own trim is pinned separately because the NBSP cases
// above do not reach it: they are refused by isNCName, which never accepts a
// no-break space, so reverting that one trim alone changes no NBSP answer.
//
// What the trim does decide is the leading/trailing-colon test, and there XML
// whitespace IS load-bearing: " :a" and "a: " are malformed and are caught
// only because the colon is looked for after the XML space is removed. Using
// strings.TrimSpace there would additionally strip a no-break space, so
// "<NBSP>:a" would be read as the well-formed ":a"-without-colon case it is
// not. This keeps the helper honest in both directions.
func TestLexicalQNameColonCheckTrimsXMLSpaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	doc, err := xdm.ParseString(`<p>x</p>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	eval := func(expr string) error {
		ctx := NewContext(doc.Root, Builtins())
		ctx.Version = XPath31
		ctx.LibraryVersion = XPath31
		_, err := Eval(expr, ctx, cardinalityNS{})
		return err
	}
	// A colon with XML whitespace around it is still a malformed QName: the
	// trim has to happen for the colon to be seen at the edge.
	for _, lex := range []string{" :a", "a: ", "\t:a", nbsp + ":a", "a:" + nbsp} {
		expr := `fn:QName("http://example.com/eg", "` + lex + `")`
		if err := eval(expr); err == nil {
			t.Errorf("%s was accepted; a leading or trailing colon is not a "+
				"valid lexical QName, so FOCA0002 is required", expr)
		}
	}
}

// The dynamic xs:QName() constructor, which is the path a COMPUTED argument
// takes.
//
// This test previously asserted the opposite conclusion and was wrong, in a
// way worth recording. It probed with string literals, and
// foldQNameConstructor resolves a literal at parse time into a QName literal
// -- dynamicQName.Eval never runs. So "xs:QName("<NBSP>xs:string")" was
// refused by the FOLDER, the test passed, and three consecutive audits were
// answered "false positive, measured" on evidence that never executed the
// branch under discussion. The fourth report pointed out the folding, which
// is what made the defect visible.
//
// The rule this cost: a probe must be shown to REACH the code it is about.
// Here the proof is the pairing below -- if the two forms ever agree again on
// the NBSP cases, one of them has stopped exercising its own path.
func TestDynamicQNameConstructorUsesXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	if len(nbsp) != 2 {
		t.Fatalf("the NBSP constant is %q, not U+00A0", nbsp)
	}
	ctx := func() *Context {
		c := NewContext(nil, Builtins())
		c.Version = XPath31
		c.LibraryVersion = XPath31
		return c
	}

	// Computed arguments. concat() and a let-bound variable both defeat the
	// constant folder, so each of these executes dynamicQName.Eval.
	computed := []struct {
		expr    string
		wantErr bool
		why     string
	}{
		{`xs:QName(concat("` + nbsp + `", "xs:string"))`, true,
			"a leading no-break space is lexical data, not padding"},
		{`xs:QName(concat("xs:string", "` + nbsp + `"))`, true,
			"a trailing no-break space is lexical data"},
		{`let $s := "` + nbsp + `xs:string" return xs:QName($s)`, true,
			"the same through a variable rather than a call"},
		{`xs:QName(concat(" ", "xs:string", " "))`, false,
			`whiteSpace="collapse" trims XML S, so this is the valid control`},
		{"xs:QName(concat(\"\t\", \"xs:string\"))", false,
			"a tab is XML S and must still be trimmed"},
	}
	for _, tc := range computed {
		_, err := Eval(tc.expr, ctx(), cardinalityNS{})
		if tc.wantErr && err == nil {
			t.Errorf("%s was accepted; %s, so FORG0001 is required",
				tc.expr, tc.why)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: %v; %s", tc.expr, err, tc.why)
		}
	}

	// The literal forms fold at parse time and are refused by
	// foldQNameLiteral instead. They are kept because they are the other half
	// of the surface, and because their agreement with the computed forms
	// above is what says both paths now apply the same whitespace rule.
	for _, lex := range []string{
		nbsp + "xs:string", "xs:string" + nbsp, "xs:" + nbsp + "string",
	} {
		expr := `xs:QName("` + lex + `")`
		if _, err := Eval(expr, ctx(), cardinalityNS{}); err == nil {
			t.Errorf("%s was accepted; a no-break space is not an NCName "+
				"character", expr)
		}
	}
	if _, err := Eval(`xs:QName(" xs:string ")`, ctx(), cardinalityNS{}); err != nil {
		t.Errorf(`xs:QName(" xs:string ") was refused: %v; XML S must be `+
			`trimmed by the collapse facet`, err)
	}
}
