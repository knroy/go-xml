package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestSerializeVersionReachesDeclaration pins fn:serialize's XML declaration to
// the version parameter.
//
// The declaration hardcoded version="1.0" and the version parameter sat on the
// accept-and-ignore list, so serialize(..., map{"version":"1.1"}) announced 1.0
// over output the caller had asked to be 1.1. xslt/serialize.go carries the
// version through for the XSLT serializer; this is its twin, and QT3
// serialize-xml-036 and -036b assert the declaration reads 1.1.
func TestSerializeVersionReachesDeclaration(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{`serialize($n, map{'method':'xml','omit-xml-declaration':false(),'version':'1.1'})`,
			`version="1.1"`},
		{`serialize($n, map{'method':'xml','omit-xml-declaration':false()})`,
			`version="1.0"`},
		// An unimplemented version is written as 1.0 rather than echoed back:
		// a declaration is a claim a parser acts on.
		{`serialize($n, map{'method':'xml','omit-xml-declaration':false(),'version':'3.7'})`,
			`version="1.0"`},
	} {
		ctx := NewContext(nil, Builtins())
		ctx.Version, ctx.LibraryVersion = XPath31, XPath31
		seq, err := Eval(tc.expr,
			ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(xdm.NewString("x"))), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		got := seq[0].(*xdm.Atomic).String()
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s = %q, want it to contain %s", tc.expr, got, tc.want)
		}
	}
}
