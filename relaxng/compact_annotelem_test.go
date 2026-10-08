package relaxng

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestCompactGrammarAnnotationElements pins the compact grammar's
// annotationElementNotKeyword member: a foreign element written among a
// grammar's definitions, as DocBook 5 opens its schema with
// `s:ns [ prefix = "db" uri = "..." ]`. It was read as the start of a datatype
// name and refused with "the datatype prefix "s" is not bound". A malformed one
// is still an error.
func TestCompactGrammarAnnotationElements(t *testing.T) {
	src := `namespace s = "http://purl.oclc.org/dsdl/schematron"
s:ns [ prefix = "a" uri = "urn:a" ]
note [ "free text" ]
start = element r { text }
s:pattern [ s:rule [ context = "r" ] ]
`
	s, err := CompileCompact(src, Options{})
	if err != nil {
		t.Fatalf("CompileCompact: %v", err)
	}
	doc, _ := xdm.ParseString(`<r>x</r>`, xdm.ParseOptions{})
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("valid document refused: %v", err)
	}
	if _, err := CompileCompact("s:ns [ = ]\nstart = element r { text }\n", Options{}); err == nil ||
		!strings.Contains(err.Error(), "annotation") {
		t.Errorf("a malformed annotation element: err = %v, want an annotation error", err)
	}
}
