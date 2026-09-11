package xquery

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A computed node name and a processing-instruction target are converted
// through XML Schema lexical types -- xs:QName for the first (XQuery 3.1
// 3.9.3.1, where a failed conversion is XQDY0074) and xs:NCName for the second
// (3.9.3.5, XQDY0041). Both datatypes are whiteSpace="collapse", whose
// whitespace is exactly #x20 #x9 #xD #xA.
//
// Both sites trimmed with strings.TrimSpace, which uses unicode.IsSpace and so
// also strips U+00A0. A no-break space is an ordinary NAME character, not
// whitespace, so stripping it silently renamed the node instead of refusing
// it: element {"<NBSP>e"} constructed <e/>, an element whose name is not the
// one the query asked for and whose real name no document can spell.
//
// The NBSP is written as an escape rather than as a literal byte on purpose: a
// raw U+00A0 in source is invisible, and one in this package's tests was
// silently degraded to an ordinary space once already.
//
// Each NBSP case is paired with the XML-whitespace spelling, which must still
// be accepted -- that pairing is what distinguishes using the right whitespace
// set from simply deleting the trim.
func TestComputedNamesUseXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"

	tests := []struct {
		name    string
		query   string
		wantErr string // "" means the query must succeed
		want    string // expected serialization when it succeeds
	}{
		{
			name:    "computed element name with a leading NBSP",
			query:   `serialize(element {"` + nbsp + `e"} {})`,
			wantErr: "XQDY0074",
		},
		{
			name:    "computed element name with a trailing NBSP",
			query:   `serialize(element {"e` + nbsp + `"} {})`,
			wantErr: "XQDY0074",
		},
		{
			name:  "computed element name with XML whitespace is still accepted",
			query: `serialize(element {"  e&#x9;"} {})`,
			want:  "<e/>",
		},
		{
			name:    "computed attribute name with a leading NBSP",
			query:   `serialize(element e {attribute {"` + nbsp + `a"} {"v"}})`,
			wantErr: "XQDY0074",
		},
		{
			name:    "PI target with a leading NBSP",
			query:   `serialize(processing-instruction {"` + nbsp + `t"} {"x"})`,
			wantErr: "XQDY0041",
		},
		{
			name:    "PI target with a trailing NBSP",
			query:   `serialize(processing-instruction {"t` + nbsp + `"} {"x"})`,
			wantErr: "XQDY0041",
		},
		{
			name:  "PI target with XML whitespace is still accepted",
			query: `serialize(processing-instruction {" t "} {"x"})`,
			want:  "<?t x?>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			seq, err := Eval(tc.query, nil, Options{})
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("%s was accepted; a no-break space is a name "+
						"character rather than whitespace, so %s is required "+
						"instead of silently renaming the node",
						tc.query, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("%s: got %v, want %s", tc.query, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: %v; XML whitespace is permitted here and must "+
					"not be refused", tc.query, err)
			}
			if len(seq) != 1 {
				t.Fatalf("%s: got %d items, want 1", tc.query, len(seq))
			}
			a, ok := seq[0].(*xdm.Atomic)
			if !ok {
				t.Fatalf("%s: got %T, want an atomic string", tc.query, seq[0])
			}
			if got := a.String(); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

// The lax-assessment xsi:type annotation path.
//
// XQuery 3.1 3.21 with no imported schema makes "validate lax" a skipped
// assessment, but the built-in xsi:type annotation is still stamped, because
// the data model's ID properties derive from it. The attribute is an
// xs:QName, whiteSpace="collapse", so only XML S surrounds the lexical form.
//
// strings.TrimSpace also strips U+00A0, so xsi:type="<NBSP>xs:integer"
// annotated the element as xs:integer -- a type the document never named, and
// one whose lexical form is invalid. The annotation is observable through the
// typed value, which is what this asserts: with the NBSP present the element
// must stay untyped and atomize as untypedAtomic.
//
// The NBSP is an escape rather than a literal byte on purpose: a raw U+00A0 in
// source is invisible and was silently degraded to an ordinary space in this
// tree once already, which made a probe assert nothing while passing.
func TestLaxXSITypeAnnotationUsesXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	if len(nbsp) != 2 {
		t.Fatalf("the NBSP constant is %q, not U+00A0", nbsp)
	}
	const decl = ` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
		` xmlns:xs="http://www.w3.org/2001/XMLSchema"`

	typeOf := func(attr string) string {
		t.Helper()
		q := `validate lax { <e` + decl + ` xsi:type="` + attr + `">42</e> }`
		seq, err := Eval(`(`+q+`)/data(.) instance of xs:integer`, nil, Options{})
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if len(seq) != 1 {
			t.Fatalf("%s: got %d items, want 1", q, len(seq))
		}
		a, ok := seq[0].(*xdm.Atomic)
		if !ok {
			t.Fatalf("%s: got %T, want xs:boolean", q, seq[0])
		}
		return a.String()
	}

	// The control: XML whitespace around the QName is collapsed, so the
	// annotation is applied and the typed value is an xs:integer.
	if got := typeOf(" xs:integer "); got != "true" {
		t.Errorf(`xsi:type=" xs:integer " did not annotate (instance of `+
			`xs:integer = %s); xs:QName is whiteSpace="collapse", so XML S `+
			`must be trimmed`, got)
	}
	// With a no-break space the lexical form is not a QName, so no annotation
	// is stamped and the element stays untyped.
	if got := typeOf(nbsp + "xs:integer"); got == "true" {
		t.Error(`xsi:type="<NBSP>xs:integer" annotated the element as ` +
			`xs:integer; a no-break space is part of the name, so this names ` +
			`no type and the element must stay untyped`)
	}
}
