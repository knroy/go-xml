package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestJSONToXMLValidateNeedsAValidator pins the behaviour of this package on
// its own, with no host to supply the schema layer.
//
// F&O 3.1 §17.5.3: "An error is raised [err:FOJS0004] if the value of the
// validate option is true and the processor does not support schema
// validation or typed data." Returning an untyped tree instead would be worse
// than the error: every assertion the caller then makes about the annotations
// would answer false, with nothing to say why.
func TestJSONToXMLValidateNeedsAValidator(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	if ctx.Validator != nil {
		t.Fatal("a bare context should have no tree validator")
	}
	_, err := Eval(`json-to-xml('{}', map{'validate': true()})`, ctx, nil)
	if err == nil {
		t.Fatal("validate=true with no validator should be an error")
	}
	if !strings.Contains(err.Error(), "FOJS0004") {
		t.Errorf("want FOJS0004, got %v", err)
	}
}

// TestJSONToXMLValidateUsesTheValidator checks the hook is reached, and
// reached with the document node rather than with its element child: the
// schema layer validates from a root, and handing it the element would skip
// the document-level assessment.
func TestJSONToXMLValidateUsesTheValidator(t *testing.T) {
	var seen *xdm.Node
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	ctx.Validator = validatorFunc(func(doc *xdm.Node) error {
		seen = doc
		return nil
	})
	if _, err := Eval(`json-to-xml('{}', map{'validate': true()})`, ctx, nil); err != nil {
		t.Fatalf("validate=true with a validator should succeed: %v", err)
	}
	if seen == nil {
		t.Fatal("the validator was not called")
	}
	if seen.Kind != xdm.KindDocument {
		t.Errorf("the validator was handed a %v, want a document node", seen.Kind)
	}
}

// TestJSONToXMLWithoutValidateSkipsTheValidator is the converse: validation
// is what the option asks for, not something the function does whenever it
// can. The default for the option is implementation-defined and this
// processor's is false, so a plain call must not annotate.
func TestJSONToXMLWithoutValidateSkipsTheValidator(t *testing.T) {
	called := false
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	ctx.Validator = validatorFunc(func(*xdm.Node) error {
		called = true
		return nil
	})
	if _, err := Eval(`json-to-xml('{}')`, ctx, nil); err != nil {
		t.Fatalf("json-to-xml: %v", err)
	}
	if called {
		t.Error("json-to-xml without validate=true should not validate")
	}
}

type validatorFunc func(*xdm.Node) error

func (f validatorFunc) ValidateJSONTree(doc *xdm.Node) error { return f(doc) }
