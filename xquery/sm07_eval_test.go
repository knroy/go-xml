package xquery

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// charged evaluates src over item and reports the nodes and bytes the
// evaluation charged.
func charged(t *testing.T, src string, item xdm.Item) (xdm.Sequence, int64, int64) {
	t.Helper()
	ctx := xpath.NewContext(item, xpath.Builtins())
	seq, err := Eval(src, ctx, Options{})
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	nodes, bytes := xpathleaf.Charged(ctx)
	return seq, nodes, bytes
}

// An array in element content is flattened under the evaluation's context
// and its leaves are charged: "[$a, $a]" doubled twelve times is 4096 leaves
// for a dozen charged items, and the per-item legacy xdm.Flatten charged none.
func TestSM07ArrayInContentIsCharged(t *testing.T) {
	const q = `let $a := fold-left(1 to 12, [1], function($a, $i) { [$a, $a] })
return count(<e>{$a}</e>/text())`
	if _, err := evalQ(t, q); err != nil {
		t.Fatalf("under the default budget: %v", err)
	}
	ctx := xpath.NewContext(nil, xpath.Builtins()).
		WithEnv(func(e *xpath.Env) { e.MaxItems = 1000 })
	_, err := Eval(q, ctx, Options{})
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("MaxItems 1000 over 4096 flattened leaves: err = %v, want a refusal", err)
	}
	// Still the §3.9.1.3 answer: the members, space-separated, as one text.
	seq, err := evalQ(t, `<a>{['a', ['b', 'c'], 'd']}</a>`)
	if err != nil || seq[0].(*xdm.Node).StringValue() != "a b c d" {
		t.Fatalf("flattened content = %v, %v; want \"a b c d\"", seq, err)
	}
}

// Each copy of a node into element content is charged to the node budget:
// one constructed element placed a thousand times is a thousand subtrees.
func TestSM07EnclosedNodeCopyIsCharged(t *testing.T) {
	_, nodes, _ := charged(t, `let $b := <b><c/><c/><c/></b> return <a>{(1 to 1000) ! $b}</a>`, nil)
	if nodes < 4000 {
		t.Fatalf("1000 copies of a 4-node subtree charged %d nodes, want >= 4000", nodes)
	}
}

// The undeclarations limitInherited writes on each constructed child are
// charged, and written as one shared frame per sibling set.
func TestSM07InheritedUndeclarationsAreCharged(t *testing.T) {
	const k, m = 10, 20
	var decl, attrs, kids strings.Builder
	for i := 0; i < k; i++ {
		fmt.Fprintf(&decl, "declare namespace p%d = 'u%d'; ", i, i)
		fmt.Fprintf(&attrs, " p%d:a=''", i)
	}
	for i := 0; i < m; i++ {
		kids.WriteString("<c/>")
	}
	_, _, base := charged(t, decl.String()+"<e>"+kids.String()+"</e>", nil)
	seq, _, with := charged(t, decl.String()+"<e"+attrs.String()+">"+kids.String()+"</e>", nil)
	if got := with - base; got < k*m*nsBindingBytes {
		t.Fatalf("%d children undeclaring %d fixup bindings charged %d bytes, want >= %d",
			m, k, got, k*m*nsBindingBytes)
	}
	// K2-NameTest-30's rule still holds: a fixup binding does not descend.
	c := seq[0].(*xdm.Node).FirstChild()
	if _, ok := c.LookupPrefix("p0"); ok {
		t.Fatal("a child sees its parent's fixup binding p0")
	}
}

// preserveScope decides each binding against the destination once and
// writes the copy's bindings in one batch, charged to the byte budget.
func TestSM07PreservedScopeIsCharged(t *testing.T) {
	const k, m = 16, 20
	var src strings.Builder
	src.WriteString("<r")
	for i := 0; i < k; i++ {
		fmt.Fprintf(&src, " xmlns:p%d='u%d'", i, i)
	}
	src.WriteString(">")
	for i := 0; i < m; i++ {
		src.WriteString("<e/>")
	}
	src.WriteString("</r>")
	doc, err := xdm.ParseString(src.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seq, _, bytes := charged(t, `<out>{/r/e}</out>`, doc.Root)
	if bytes < k*m*nsBindingBytes {
		t.Fatalf("%d copies preserving %d bindings charged %d bytes, want >= %d",
			m, k, bytes, k*m*nsBindingBytes)
	}
	e := seq[0].(*xdm.Node).LastChild()
	if uri, ok := e.LookupPrefix(fmt.Sprintf("p%d", k-1)); !ok || uri != fmt.Sprintf("u%d", k-1) {
		t.Fatalf("the copy lost a preserved binding: %q, %v", uri, ok)
	}
}
