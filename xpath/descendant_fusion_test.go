package xpath

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// "//T" with no predicate on either step is evaluated as descendant::T. Every
// expression below must answer what its unfused spelling answers (the
// explicit "descendant-or-self::node()/child::" form with a no-op predicate
// is never fused), in the same order, with the same error; and the fused path
// must not materialise the descendant-or-self sequence.
func TestDescendantFusion(t *testing.T) {
	tree, err := xdm.ParseString(
		`<a xmlns:p="urn:p" id="0"><b id="1"><c id="2"/><b id="3"><c id="4">t</c></b></b><c id="5"/>t<!--k--><?pi x?><p:c id="6"/></a>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	unfused := func(e string) string {
		return strings.ReplaceAll(e, "//", "/descendant-or-self::node()[true()]/")
	}
	show := func(e string) string {
		v, err := MustCompile(e, nil).Eval(NewContext(tree.Root, Builtins()))
		if err != nil {
			return "ERROR " + xdm.ErrorCode(err)
		}
		var got []string
		for _, it := range v {
			switch x := it.(type) {
			case *xdm.Node:
				// Namespace nodes are made afresh per step; identity is
				// compared for the others.
				if x.Kind() == xdm.KindNamespace {
					got = append(got, "ns:"+x.StringValue())
				} else {
					got = append(got, fmt.Sprintf("%s:%s@%p", x.Kind(), x.StringValue(), x))
				}
			case *xdm.Atomic:
				got = append(got, x.String())
			}
		}
		return strings.Join(got, " ")
	}
	for _, e := range []string{
		"//c", "//*", "//node()", "//text()", "//comment()", "//processing-instruction()",
		"//attribute()", "//namespace-node()", "//document-node()", "//element(c)",
		"//*:c", "/a//c", "/a//b//c", "//b//c", "//c[1]", "(//c)[2]",
		"//b/c", "//c/@id", "//@id", "//c//.", "//c/..", "/a/@id//node()",
		"for $b in //b return count($b//c)", "//b/(c)", "//b//string(@id)",
		"(/a, 1)//c", "(1, 2)//c", "//b//(c, 1)", "//c/(., 1)",
	} {
		if got, want := show(e), show(unfused(e)); got != want {
			t.Errorf("%s = %q,\n  unfused %q", e, got, want)
		}
	}
	// The errors the steps raise, by code.
	for e, code := range map[string]string{
		"(/a, 1)//c":  "XPTY0019",
		"//b//(c, 1)": "XPTY0018",
	} {
		if got := show(e); got != "ERROR "+code {
			t.Errorf("%s = %q, want %s", e, got, code)
		}
	}

	// Unfused, the descendant-or-self step held all 2,001 nodes and the child
	// step allocated a result for each <p> it visited.
	var sb strings.Builder
	sb.WriteString("<r>")
	for i := 0; i < 1000; i++ {
		sb.WriteString("<p><c/></p>")
	}
	sb.WriteString("</r>")
	big, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := MustCompile("//c", nil)
	ctx := NewContext(big.Root, Builtins())
	allocs := testing.AllocsPerRun(10, func() {
		if v, err := c.Eval(ctx); err != nil || len(v) != 1000 {
			t.Fatalf("//c: %d nodes, %v", len(v), err)
		}
	})
	t.Logf("//c over 1,000 parents: %.0f allocations", allocs)
	if allocs > 100 {
		t.Errorf("//c over 1,000 parents allocated %.0f times, want at most 100", allocs)
	}
}
