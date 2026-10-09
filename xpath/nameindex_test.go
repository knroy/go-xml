package xpath

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// descendant::name over a parsed document is answered from the tree's
// element-name index; the answer, its order and the predicates applied to it
// are those of the walk, which a clone (not indexed) still takes.
func TestNamedDescendantsFromTheIndex(t *testing.T) {
	tree, err := xdm.ParseString(`<r xmlns:p="urn:p"><p:x n="1"><x n="2"/><p:x n="3"/></p:x><y><p:x n="4"/></y></r>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ns := calendarNS{"q": "urn:p"}
	cases := map[string]string{
		`string-join(//q:x/@n, ',')`:           "1,3,4",
		`string-join((//q:x)[2]/@n, ',')`:      "3",
		`string-join(//q:x[2]/@n, ',')`:        "",
		`string-join(/r/y//q:x/@n, ',')`:       "4",
		`string-join(//x/@n, ',')`:             "2",
		`count(//q:x//q:x)`:                    "1",
		`string-join(//*:x/@n, ',')`:           "1,2,3,4",
		`count(//q:x/descendant-or-self::q:x)`: "3",
	}
	copyRoot := xdm.Copy(tree.Root)
	for src, want := range cases {
		c, err := Compile(src, ns)
		if err != nil {
			t.Fatal(err)
		}
		for _, root := range []*xdm.Node{tree.Root, copyRoot} {
			got, err := c.EvalString(NewContext(root, Builtins()))
			if err != nil || got != want {
				t.Errorf("%s = %q, %v; want %q", src, got, err, want)
			}
		}
	}
}
