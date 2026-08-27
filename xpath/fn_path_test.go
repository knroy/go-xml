package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func TestFnPath(t *testing.T) {
	src := `<p xmlns="http://example.com/one" xml:lang="de" author="Schiller">a<br/>b<br/>c</p>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ expr, want string }{
		{`path(.)`, "/"},
		{`path(/*:p)`, "/Q{http://example.com/one}p[1]"},
		{`path(/*:p/@xml:lang)`, "/Q{http://example.com/one}p[1]/@Q{http://www.w3.org/XML/1998/namespace}lang"},
		{`path(/*:p/@author)`, "/Q{http://example.com/one}p[1]/@author"},
		{`path(/*:p/*:br[2])`, "/Q{http://example.com/one}p[1]/Q{http://example.com/one}br[2]"},
	} {
		ctx := NewContext(tree.Root, Builtins())
		ctx.Version = XPath31
		got, err := Eval(tc.expr, ctx, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if s := got[0].(*xdm.Atomic).String(); s != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.expr, s, tc.want)
		}
	}
}
