package xpath_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// NewContext takes a nil FunctionLibrary, and a call with one correctly
// reports XPST0017. A named function reference reached the library through a
// different path that did not test for nil, so "fn:count#1" panicked where
// "fn:count(...)" returned an error — a nil dereference inside a library call,
// which takes down the caller's goroutine rather than failing the request.
func TestNamedFunctionRefWithNoLibrary(t *testing.T) {
	tree, err := xdm.ParseString("<r/>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := xpath.NewContext(tree.Root, nil)

	for _, expr := range []string{"fn:count#1", "count#1", "concat#3"} {
		c, err := xpath.CompileVersion(expr, nil, xpath.XPath30)
		if err != nil {
			t.Fatalf("%s: compile: %v", expr, err)
		}
		if _, err := c.Eval(ctx); err == nil {
			t.Errorf("%s: accepted with no library", expr)
		} else if !strings.Contains(err.Error(), "XPST0017") {
			t.Errorf("%s: wrong error: %v", expr, err)
		}
	}
}
