package xpath_test

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// A program that links xpath but not xslt has no processor registered, so
// fn:transform declines with FOXT0004, the code F&O 3.1 gives an
// implementation that cannot run the transformation. This package's tests do
// not link xslt, which is what makes the case testable here.
func TestTransformWithoutProcessorIsFOXT0004(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx = ctx.WithVersion(xpath.XPath31)
	_, err := xpath.Eval(`transform(map{'stylesheet-text': '<x/>'})`, ctx, nil)
	if code := xdm.ErrorCode(err); code != "FOXT0004" {
		t.Errorf("code = %q, want FOXT0004 (error: %v)", code, err)
	}
}
