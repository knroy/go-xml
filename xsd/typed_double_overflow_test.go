package xsd

import (
	"math"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestTypedDoubleOverflowKeepsItsType covers an xs:double whose magnitude no
// double can hold. "1e400" is a valid xs:double lexical form and the schema
// validates it, so the node carries a "double" annotation -- but building the
// typed value from that annotation went through strconv.ParseFloat, which
// reports the magnitude as strconv.ErrRange while still returning the right
// value. Reading the error as "not a lexical form of this type" returned no
// atomic at all, the caller fell back to xs:untypedAtomic, and a validated
// xs:double silently stopped being a double.
//
// The assertion is on the RESULT, not on an error: this path produces none.
// F&O 3.0 §4.2 lets an overflow yield ±INF, which is what casting and JSON
// parsing already do, so the typed value has to agree with them.
func TestTypedDoubleOverflowKeepsItsType(t *testing.T) {
	s := load11(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="d" type="xs:double"/>
	  <xs:element name="f" type="xs:float"/>
	</xs:schema>`)

	cases := []struct {
		el   string
		lex  string
		want float64
		typ  xdm.TypeCode
	}{
		{"d", "1e400", math.Inf(1), xdm.TypeDouble},
		{"d", "-1e400", math.Inf(-1), xdm.TypeDouble},
		{"d", "1e-400", 0, xdm.TypeDouble},
		{"d", "1.5", 1.5, xdm.TypeDouble},
		{"d", "INF", math.Inf(1), xdm.TypeDouble},
		// float64 holds 1e40 exactly well enough; xs:float cannot, and
		// NewFloat narrows it to +Inf. The point is that it stays a float.
		{"f", "1e400", math.Inf(1), xdm.TypeFloat},
		{"f", "2.5", 2.5, xdm.TypeFloat},
	}

	for _, c := range cases {
		tree, err := xdm.ParseString("<"+c.el+">"+c.lex+"</"+c.el+">", xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s=%s: %v", c.el, c.lex, err)
		}
		if err := s.Validate(tree.Root, ValidateOptions{Annotate: true}); err != nil {
			t.Fatalf("%s=%s: the schema accepts this lexical form: %v", c.el, c.lex, err)
		}
		a := tree.Root.ChildElements()[0].Atomize()
		if a == nil {
			t.Errorf("%s=%s: atomised to nothing", c.el, c.lex)
			continue
		}
		if a.Type != c.typ {
			t.Errorf("%s=%s: atomised as %v, want %v", c.el, c.lex, a.Type, c.typ)
		}
		if got := a.Float64(); got != c.want {
			t.Errorf("%s=%s: value %v, want %v", c.el, c.lex, got, c.want)
		}
	}
}
