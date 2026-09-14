package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Context.MapDuplicateCode selects the error code for a duplicate key in a map
// constructor, and defaults to XQuery's.
//
// The code is the host language's rather than XPath's: XQuery 3.1 section
// 3.11.1 calls it XQDY0137, which the QT3 suite requires, while XSLT 3.0
// section 17.4 gives the very same MapExpr the code XTDE3365. Leaving the
// field unset must therefore keep XQDY0137, so that a host which never heard
// of the knob behaves exactly as it always did.
func TestMapConstructorDuplicateKeyCode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override string
		want     string
	}{
		{"default is the XQuery code", "", "XQDY0137"},
		{"a host may select the XSLT code", "XTDE3365", "XTDE3365"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := CompileVersion(`map{'a':1,'a':2}`, nil, XPath31)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			ctx := NewContext(nil, nil)
			ctx.MapDuplicateCode = tc.override
			if _, err := c.Eval(ctx); err == nil {
				t.Fatal("a map constructor naming a key twice succeeded; " +
					"want " + tc.want)
			} else if code := xdm.ErrorCode(err); code != tc.want {
				t.Errorf("got error code %q (%v), want %q", code, err, tc.want)
			}
		})
	}
}
