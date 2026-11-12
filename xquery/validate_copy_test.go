package xquery_test

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xquery"
)

// TestValidateLaxWithoutSchemaCopiesOperand pins §3.21's "the validate
// expression returns a new copy of its operand" on the path with no imported
// schema. That path stamped the xsi:type annotation onto the caller's own
// tree and returned the operand itself, so one evaluation changed what every
// later query over the same document saw -- an unsynchronised write into a
// tree a concurrently used Query only reads. Both node kinds are covered: the
// operand is the element, then the document node.
func TestValidateLaxWithoutSchemaCopiesOperand(t *testing.T) {
	for _, path := range []string{"/a", "/"} {
		doc, err := xdm.ParseString(
			`<a xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`+
				` xmlns:xs="http://www.w3.org/2001/XMLSchema"`+
				` xsi:type="xs:integer">5</a>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		el := doc.Root.ChildElements()[0]
		eval := func(q string) xdm.Sequence {
			t.Helper()
			seq, err := xquery.Eval(q,
				xpath.NewContext(doc.Root, xpath.Builtins()), xquery.Options{})
			if err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			return seq
		}
		probe := `data(/a) instance of xs:integer`
		got := eval(fmt.Sprintf(`validate lax { %s }`, path))
		if len(got) != 1 {
			t.Fatalf("%s: want one node, got %v", path, got)
		}
		if n, _ := got[0].(*xdm.Node); n == el || n == doc.Root {
			t.Errorf("%s: validate returned its operand, not a copy", path)
		}
		if a := el.TypeAnnotation(); a != "" {
			t.Errorf("%s: the input element was annotated %q", path, a)
		}
		if p := eval(probe); fmt.Sprint(p) != "[false]" {
			t.Errorf("%s: %s is %v after validate, want [false]", path, probe, p)
		}
		// The copy still carries the annotation: that half is the point.
		typed := fmt.Sprintf(`data((validate lax { %s })`+
			`/descendant-or-self::a) instance of xs:integer`, path)
		if p := eval(typed); fmt.Sprint(p) != "[true]" {
			t.Errorf("%s: %s is %v, want [true]", path, typed, p)
		}
	}
}
