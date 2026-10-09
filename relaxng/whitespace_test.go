package relaxng

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Section 6.2.7: whitespace-only text among element children is stripped, and
// a lone whitespace-only text child matches either as text or as nothing. It
// used to be matched as text whenever the pattern allowed text, which chose
// the text branch of a choice between text and elements on the first newline
// and left nothing for the element that followed.
func TestWhitespaceTextBesideElementsIsStripped(t *testing.T) {
	s, err := compileString(t, `<element name="p"`+rngNS+`><choice>
		<zeroOrMore><text/></zeroOrMore>
		<oneOrMore><element name="f"><text/></element></oneOrMore>
	</choice></element>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, c := range []struct {
		doc   string
		valid bool
	}{
		{"<p><f>a</f></p>", true},
		{"<p>\n <f>a</f>\n <f>b</f>\n</p>", true},
		{"<p>\n</p>", true},
		{"<p>x</p>", true},
		{"<p>x<f>a</f></p>", false},
	} {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(doc.Root); (err == nil) != c.valid {
			t.Errorf("%q: valid = %v, want %v (err %v)", c.doc, err == nil, c.valid, err)
		}
	}
}

// A lone whitespace-only child may also match as nothing, so an element whose
// content must be empty, or must be one value, still accepts it.
func TestLoneWhitespaceTextMayMatchNothing(t *testing.T) {
	s, err := compileString(t, `<element name="p"`+rngNS+`><choice>
		<empty/>
		<group><text/><element name="f"><empty/></element></group>
	</choice></element>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, d := range []string{"<p/>", "<p> </p>", "<p>\n\t</p>"} {
		doc, err := xdm.ParseString(d, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(doc.Root); err != nil {
			t.Errorf("%q: %v", d, err)
		}
	}
}

// Section 6.2.7 lets a lone whitespace-only child match as nothing, but it is
// still a string to <value>, <data> and <list>, so the derivative over it must
// be kept, not dropped. Beside an element it is stripped even when a data
// alternative could have taken it. Every verdict here matches Jing 20241231.
func TestLoneWhitespaceTextStillReachesValues(t *testing.T) {
	const xsd = ` datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes"`
	for _, c := range []struct {
		schema, doc string
		valid       bool
	}{
		{`<value type="string"> </value>`, "<p> </p>", true},
		{`<value type="string"> </value>`, "<p>  </p>", false},
		{`<data type="string"` + xsd + `><param name="minLength">2</param></data>`, "<p>  </p>", true},
		{`<data type="string"` + xsd + `><param name="minLength">2</param></data>`, "<p> </p>", false},
		{`<list><oneOrMore><value>a</value></oneOrMore></list>`, "<p> a </p>", true},
		{`<list><oneOrMore><value>a</value></oneOrMore></list>`, "<p> </p>", false},
		{`<choice><element name="f"><empty/></element><data type="token"` + xsd + `/></choice>`, "<p> <f/> </p>", true},
	} {
		s, err := compileString(t, `<element name="p"`+rngNS+`>`+c.schema+`</element>`)
		if err != nil {
			t.Fatalf("compile %s: %v", c.schema, err)
		}
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(doc.Root); (err == nil) != c.valid {
			t.Errorf("%q against %s: valid = %v, want %v (err %v)", c.doc, c.schema, err == nil, c.valid, err)
		}
	}
}
