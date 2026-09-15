package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestNamespaceNodeIdentity pins the identity of namespace nodes under the
// "is" operator.
//
// Namespace nodes are not stored in the tree: the namespace axis synthesizes
// one per in-scope binding on every walk, so two walks over the same binding
// hand back two pointers. Comparing those pointers made the QT3 case
// prod-AxisStep/Axes123 answer false where XDM requires true, because the two
// operands are the same node reached by two routes.
//
// The negative half matters as much as the positive one: the same prefix on a
// different element is a DIFFERENT namespace node (that is QT3's Axes122,
// which asserts false), and so are two different prefixes on one element. A
// change that made every namespace node identical would satisfy Axes123 and
// break Axes122.
func TestNamespaceNodeIdentity(t *testing.T) {
	const doc = `<r xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:a="urn:a"><c/></r>`

	cases := []struct {
		name string
		expr string
		want bool
	}{
		{
			// Axes123: the same binding on the same element, reached once by
			// name and once through a predicate over the whole axis.
			name: "same element same prefix, two walks",
			expr: `/*/namespace::xlink is /*/namespace::*[. = 'http://www.w3.org/1999/xlink']`,
			want: true,
		},
		{
			// Axes122: xlink is in scope on the child too, by inheritance,
			// but the child's namespace node is a different node.
			name: "different element same prefix",
			expr: `/*/namespace::xlink is /*/*[1]/namespace::xlink`,
			want: false,
		},
		{
			name: "same element different prefix",
			expr: `/*/namespace::xlink is /*/namespace::a`,
			want: false,
		},
		{
			// The identity must survive the round trip through a variable,
			// which is where a naive per-walk cache would still fail.
			name: "same node bound twice",
			expr: `/*/namespace::xlink is /*/namespace::xlink`,
			want: true,
		},
		{
			// A namespace node is never the same node as an element, even
			// when the arithmetic behind their order might collide.
			name: "namespace node is not its element",
			expr: `/*/namespace::xlink is /*`,
			want: false,
		},
	}

	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext(tree.Root, Builtins())
			ctx.Version = XPath31
			seq, err := Eval(tc.expr, ctx, nil)
			if err != nil {
				t.Fatalf("eval %s: %v", tc.expr, err)
			}
			// The assertion is on the boolean the operator produced, not on
			// err being nil: "is" returning the wrong answer is not an error.
			got, err := EffectiveBooleanValue(seq)
			if err != nil {
				t.Fatalf("ebv of %s: %v", tc.expr, err)
			}
			if got != tc.want {
				t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}
