package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// evalSerialize evaluates an fn:serialize expression under XPath 3.1 and
// returns the serialized string, or the error the call raised.
func evalSerialize(t *testing.T, expr string) (string, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		return "", err
	}
	return seq[0].(*xdm.Atomic).String(), nil
}

// TestSerializeStandaloneEmptySequenceOmits pins the empty sequence as a legal
// value of the standalone parameter in the map form.
//
// standalone is the one boolean in that form whose declared type is
// xs:boolean? rather than xs:boolean: the empty sequence means "write no
// standalone at all", which the element form spells as value="omit". It was
// read through the shared boolean reader, so map{"standalone":()} came out as
// an XPTY0004 cardinality error rather than a declaration without the
// attribute. QT3 serialize-xml-131 asserts the declaration carries no
// standalone; the suite does not measure it, because the case's environment
// is a schema-validated source this engine skips.
func TestSerializeStandaloneEmptySequenceOmits(t *testing.T) {
	got, err := evalSerialize(t,
		`serialize(parse-xml('<a/>'), map{'omit-xml-declaration':false(),'standalone':()})`)
	if err != nil {
		t.Fatalf("standalone:() raised %v, want a declaration with no standalone", err)
	}
	if want := `<?xml version="1.0" encoding="UTF-8"?>` + "\n<a/>"; got != want {
		t.Errorf("standalone:() = %q, want %q", got, want)
	}

	// The string " omit " stays a type error: that spelling exists because an
	// attribute in the element form can only carry a string, and the map
	// form, being typed, says the same thing with (). serialize-xml-131a.
	if _, err := evalSerialize(t,
		`serialize(parse-xml('<a/>'), map{'omit-xml-declaration':false(),'standalone':' omit '})`); err == nil {
		t.Error(`standalone:" omit " was accepted, want XPTY0004`)
	}
}

// TestSerializeCDATASectionElementsElementForm pins cdata-section-elements in
// the element form of the serialization parameters.
//
// The map form honoured the parameter and the element form sat on the
// accept-and-ignore list, so the same request wrote a CDATA section through
// one spelling and escaped text through the other. The value is a
// space-separated list of lexical QNames whose prefixes resolve against the
// in-scope namespaces of the parameter element itself, the rule XSLT 3.0
// states for the same parameter on xsl:result-document.
func TestSerializeCDATASectionElementsElementForm(t *testing.T) {
	const params = `parse-xml('<output:serialization-parameters ` +
		`xmlns:output="http://www.w3.org/2010/xslt-xquery-serialization" ` +
		`xmlns:p="urn:x"><output:cdata-section-elements value="%s"/>` +
		`</output:serialization-parameters>')/*`

	for _, tc := range []struct{ name, doc, names, want string }{
		{
			name:  "unprefixed",
			doc:   `<a><c>x&lt;y</c></a>`,
			names: "c",
			want:  `<a><c><![CDATA[x<y]]></c></a>`,
		},
		{
			// A prefix is expanded against the parameter document's own
			// bindings, so p:c names the element in urn:x whatever prefix the
			// serialized document happens to write it with.
			name:  "prefixed",
			doc:   `<a xmlns:p="urn:x"><p:c>x&lt;y</p:c></a>`,
			names: "p:c",
			want:  `<a xmlns:p="urn:x"><p:c><![CDATA[x<y]]></p:c></a>`,
		},
		{
			name:  "two names",
			doc:   `<a><c>x&lt;y</c><d>p&amp;q</d></a>`,
			names: "c d",
			want:  `<a><c><![CDATA[x<y]]></c><d><![CDATA[p&q]]></d></a>`,
		},
		{
			// An element not named keeps its escaping, which is what shows
			// the parameter is read rather than applied to everything.
			name:  "unnamed element still escapes",
			doc:   `<a><c>x&lt;y</c><d>p&lt;q</d></a>`,
			names: "c",
			want:  `<a><c><![CDATA[x<y]]></c><d>p&lt;q</d></a>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expr := `serialize(parse-xml('` + tc.doc + `'), ` +
				replaceOnce(params, "%s", tc.names) + `)`
			got, err := evalSerialize(t, expr)
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			if got != tc.want {
				t.Errorf("cdata-section-elements=%q\n got %q\nwant %q",
					tc.names, got, tc.want)
			}
		})
	}

	// An unbound prefix names no element at all, so the parameter document is
	// malformed rather than carrying a name that matches nothing.
	expr := `serialize(parse-xml('<a><c>x</c></a>'), ` +
		replaceOnce(params, "%s", "zz:c") + `)`
	if _, err := evalSerialize(t, expr); err == nil {
		t.Error("an unbound prefix was accepted, want SEPM0017")
	}
}

// replaceOnce substitutes the first occurrence of old in s, keeping the test
// expressions readable without pulling fmt into a table of raw XML.
func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}
