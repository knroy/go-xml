package xpath

import "testing"

// fn:id tokenizes each argument the way an IDREFS value is tokenized, and
// fn:idref tokenizes the IDREFS values it walks over; an ID attribute's own
// value is of a type derived from xs:NCName and so has its whitespace
// collapsed before comparison. Every one of those is the XML Schema
// whiteSpace="collapse" rule, whose separator set is XML S (#x20 #x9 #xD #xA)
// and nothing wider. strings.Fields/strings.TrimSpace used Go's whole Unicode
// White_Space set, which made a no-break space a token boundary on both the
// argument and the value side.
func TestIDUsesXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	doc := `<?xml version="1.0"?>
<!DOCTYPE r [
  <!ATTLIST u id ID #IMPLIED>
  <!ATTLIST w id ID #IMPLIED>
  <!ATTLIST s idrefs IDREFS #IMPLIED>
  <!ATTLIST v idrefs IDREFS #IMPLIED>
]>
<r>
  <x xml:id="a"/>
  <y xml:id="b"/>
  <u id="` + nbsp + `c` + nbsp + `"/>
  <w id=" d "/>
  <s idrefs="a b"/>
  <v idrefs="a` + nbsp + `b"/>
</r>`

	for _, tc := range []struct{ name, expr, want string }{
		// Argument side: an NBSP is not a separator, so this is the single
		// name "a<NBSP>b", which is no NCName and matches nothing.
		{"id arg nbsp is one token", "count(id('a" + nbsp + "b'))", "0"},
		{"id arg xml S separates", "id('a b')/name()", "x,y"},
		{"id arg xml S trims", "id(' a ')/name()", "x"},

		// Value side: an id attribute holding NBSP around "c" collapses only
		// its XML S, so its value stays "<NBSP>c<NBSP>" and "c" misses it.
		{"id value nbsp not trimmed", "count(id('c'))", "0"},
		// The same attribute written with XML S does collapse to "d".
		{"id value xml S trimmed", "id('d')/name()", "w"},

		// idref side: the NBSP inside an IDREFS value is not a separator, so
		// that value holds the one token "a<NBSP>b" and answers to neither
		// "a" nor "b"; only the space-separated value does.
		{"idref nbsp not a separator", "idref('a')/../name()", "s"},
		{"idref xml S separates", "idref('b')/../name()", "s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalStr(t, doc, tc.expr); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}

// F&O §14.5.2 ignores a candidate IDREF that is not an NCName, so an ID
// value that is not one is never found, whether xml:id or a DTD typed it
// ID. The xml:id stays an ID (xml:id §4); it is the candidate that drops out.
func TestIDIgnoresNonNCNameCandidates(t *testing.T) {
	doc := `<?xml version="1.0"?>
<!DOCTYPE r [<!ATTLIST u id ID #IMPLIED>]>
<r><x xml:id="9z"/><u id="7q"/><y xml:id="ok"/></r>`
	for _, tc := range []struct{ expr, want string }{
		{"count(id('9z'))", "0"},
		{"count(id('7q'))", "0"},
		{"id('9z ok 7q')/name()", "y"},
	} {
		if got := evalStr(t, doc, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// F&O 3.1 §14.5.1 (fn:id) and §14.5.2 (fn:element-with-id): "If several
// elements have the same ID value, then E is the one that is first in
// document order." Only the first holder of a duplicated ID is returned;
// fn:idref (§14.5.3) has no such rule and returns every referring node.
func TestIDDuplicateValueReturnsFirst(t *testing.T) {
	doc := `<?xml version="1.0"?>
<!DOCTYPE r [
  <!ATTLIST e id ID #IMPLIED>
  <!ATTLIST s ref IDREF #IMPLIED>
]>
<r>
  <e id="a" n="1"/>
  <x xml:id="b" n="2"/>
  <e id="a" n="3"/>
  <x xml:id="a" n="4"/>
  <e id="b" n="5"/>
  <s ref="a"/>
  <s ref="a"/>
</r>`
	for _, tc := range []struct{ expr, want string }{
		{"id('a')/@n/string()", "1"},
		{"id('a b')/@n/string()", "1,2"},
		{"id(('b', 'a'))/@n/string()", "1,2"},
		{"element-with-id('a')/@n/string()", "1"},
		{"element-with-id('b a')/@n/string()", "1,2"},
		{"count(idref('a'))", "2"},
	} {
		if got := evalStr(t, doc, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}
