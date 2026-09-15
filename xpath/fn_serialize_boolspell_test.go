package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// boolSpellTree builds a nested element carrying a URI-valued attribute.
//
// The nesting is what indent has to act on. The attribute is what
// escape-uri-attributes has to act on, and it has to be a pair the HTML DTD
// declares with type URI -- a/@href is one, see uriAttributes -- holding a
// non-ASCII character, since escapeURIAttribute percent-escapes only runes
// at or above 0x80. An attribute outside that table, or an all-ASCII value,
// would leave the parameter no live path to act on.
func boolSpellTree() *xdm.Node {
	root := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "a"}}
	b := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "b"}, Parent: root}
	c := &xdm.Node{
		Kind:   xdm.KindElement,
		Name:   xdm.QName{Local: "a"},
		Parent: b,
		Attrs: []*xdm.Node{{
			Kind:  xdm.KindAttribute,
			Name:  xdm.QName{Local: "href"},
			Value: "p\u00e9",
		}},
	}
	b.Children = []*xdm.Node{c}
	root.Children = []*xdm.Node{b}
	return root
}

// serializeWithParam serializes boolSpellTree through the element form with
// the named parameter set to the given spelling.
//
// with carries the companion parameters a given boolean needs before it has
// any effect at all: escape-uri-attributes is consulted only for the html and
// xhtml methods, and undeclare-prefixes only when the output version is 1.1.
// Without them the parameter under test would have no live path and the test
// would pass against any implementation, broken or not.
func serializeWithParam(t *testing.T, name, value string, with map[string]string) string {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	params := map[string]string{name: value}
	for k, v := range with {
		params[k] = v
	}
	c := ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(boolSpellTree()))
	c = c.WithVar(xdm.QName{Local: "p"}, xdm.One(paramsElement(params)))
	seq, err := Eval(`serialize($n, $p)`, c, nil)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, value, err)
	}
	return seq[0].(*xdm.Atomic).String()
}

// affirmative and negative are the two halves of the lexical space that
// Serialization 3.1 section 3 gives every boolean-valued serialization
// parameter: "yes, no, true, false, 1 or 0"
// (testdata/xslt30-test/specs/serialization-31.html:1012).
var (
	affirmative = []string{"yes", "true", "1"}
	negative    = []string{"no", "false", "0"}
)

// TestSerializeParamElementBooleanSpellings pins all six spellings of every
// boolean serialization parameter in the element form.
//
// checkYesNo admitted all six and returned only an error, discarding the
// normalisation, while each caller compared the raw text against "yes". So
// "true" and "1" validated and then set the field FALSE -- the affirmative
// silently became the negative. The property that matters is that the three
// affirmative spellings agree with each other and differ from the three
// negative ones; pinning the exact output would pin the formatter instead.
func TestSerializeParamElementBooleanSpellings(t *testing.T) {
	for _, tc := range []struct {
		param string
		with  map[string]string
		// differs reports the observable effect of the parameter, which
		// must hold for the affirmative spellings and not the negative.
		differs func(out string) bool
	}{
		{"indent", nil, func(out string) bool { return strings.Contains(out, "\n") }},
		{"omit-xml-declaration", nil, func(out string) bool {
			return !strings.Contains(out, "<?xml")
		}},
		{"escape-uri-attributes", map[string]string{"method": "html"},
			func(out string) bool { return strings.Contains(out, "p%C3%A9") }},
	} {
		t.Run(tc.param, func(t *testing.T) {
			for _, yes := range affirmative {
				got := serializeWithParam(t, tc.param, yes, tc.with)
				if !tc.differs(got) {
					t.Errorf("%s=%q gave %q, want the same effect as %s=\"yes\"",
						tc.param, yes, got, tc.param)
				}
			}
			for _, no := range negative {
				got := serializeWithParam(t, tc.param, no, tc.with)
				if tc.differs(got) {
					t.Errorf("%s=%q gave %q, want the same effect as %s=\"no\"",
						tc.param, no, got, tc.param)
				}
			}
		})
	}
}

// TestSerializeParamElementBooleanSpellingsAgree pins the two parameters whose
// effect needs a shape this tree does not have -- undeclare-prefixes needs a
// namespace undeclaration and allow-duplicate-names needs a JSON map -- by the
// weaker property that every spelling of one truth value serializes alike.
//
// A parameter that read "true" as false would make the affirmative group
// disagree with itself the moment the parameter had any effect at all.
func TestSerializeParamElementBooleanSpellingsAgree(t *testing.T) {
	for _, tc := range []struct {
		param string
		with  map[string]string
	}{
		{"undeclare-prefixes", map[string]string{"version": "1.1"}},
		{"allow-duplicate-names", nil},
	} {
		param := tc.param
		t.Run(param, func(t *testing.T) {
			for _, group := range [][]string{affirmative, negative} {
				want := serializeWithParam(t, param, group[0], tc.with)
				for _, spelling := range group[1:] {
					if got := serializeWithParam(t, param, spelling, tc.with); got != want {
						t.Errorf("%s=%q gave %q, but %s=%q gave %q",
							param, spelling, got, param, group[0], want)
					}
				}
			}
		})
	}
}

// TestSerializeParamElementStandaloneSpellings pins the standalone parameter
// to the two words an XML declaration may carry.
//
// The element form stored the raw value and wrote it verbatim into the
// declaration, so standalone="true" emitted standalone="true" and
// standalone="1" emitted standalone="1". XML 1.0 section 2.9 admits only
// "yes" or "no" in an SDDecl, so both were malformed XML that no parser
// should accept. "omit" is element-form-only and writes no SDDecl at all
// (Serialization 3.1 section 5.1.6).
func TestSerializeParamElementStandaloneSpellings(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
	}{
		{"yes", ` standalone="yes"`},
		{"true", ` standalone="yes"`},
		{"1", ` standalone="yes"`},
		{"no", ` standalone="no"`},
		{"false", ` standalone="no"`},
		{"0", ` standalone="no"`},
		{"omit", ""},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got := serializeWithParam(t, "standalone", tc.value, nil)
			if tc.want == "" {
				if strings.Contains(got, "standalone") {
					t.Errorf("standalone=%q gave %q, want no SDDecl", tc.value, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("standalone=%q gave %q, want it to contain %q",
					tc.value, got, tc.want)
			}
		})
	}
}

// TestSerializeBooleanSpellingsMatchMapForm is the control.
//
// The map form takes a real xs:boolean and was always correct, so it says what
// the element form's answer should have been. The defect was the two forms
// disagreeing: indent=true() indented and indent="true" did not.
func TestSerializeBooleanSpellingsMatchMapForm(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	c := ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(boolSpellTree()))
	seq, err := Eval(`serialize($n, map{'indent': true()})`, c, nil)
	if err != nil {
		t.Fatalf("map form: %v", err)
	}
	want := seq[0].(*xdm.Atomic).String()

	for _, spelling := range affirmative {
		if got := serializeWithParam(t, "indent", spelling, nil); got != want {
			t.Errorf("element form indent=%q gave %q, map form indent=true() gave %q",
				spelling, got, want)
		}
	}
}
