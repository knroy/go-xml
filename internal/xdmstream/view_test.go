package xdmstream

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/genid"
	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// viewDocs are the shapes views are checked on: doc's (prolog, namespaces,
// a DTD default, xml:base), a 900-deep spine, and siblings enough to fill
// several view batches.
func viewDocs() map[string]string {
	var deep strings.Builder
	deep.WriteString(`<a xmlns:p="urn:p">`)
	for i := range 900 {
		fmt.Fprintf(&deep, `<w d="%d"%s>`, i, map[bool]string{true: ` xmlns:q="urn:q"`, false: ""}[i%100 == 50])
		if i%300 == 7 {
			deep.WriteString(`<r k="x"/><r k="y"><s/></r>`)
		}
	}
	for i := range 200 {
		fmt.Fprintf(&deep, `<r k="%d"><s/></r>`, i)
	}
	deep.WriteString(strings.Repeat("</w>", 900) + "</a>")
	var wide strings.Builder
	wide.WriteString(`<a xml:base="http://ex/w/"><b xmlns="urn:b">`)
	for i := range 6000 {
		fmt.Fprintf(&wide, `<r i="%d" j="v">t<s>u</s></r>`, i)
	}
	wide.WriteString("</b></a>")
	return map[string]string{"doc": doc(40), "deep": deep.String(), "wide": wide.String()}
}

// key is n's generate-id without the tree: a view's or a copy's is its
// original's index in a full parse, so the two must match.
func key(n *xdm.Node) string {
	id := genid.Of(n)
	return id[strings.IndexByte(id, 'x'):]
}

// motionless is what a view of n must answer as n does: its kind, name,
// identity, attributes, namespaces and base URI, its ancestors' too, and
// the document node's URIs (all of that for the ancestors only if all).
func motionless(n *xdm.Node, all bool) string {
	var b strings.Builder
	for c := n; c != nil; c = c.Parent() {
		if !all && c != n {
			// The ancestors' names and identities; the rest of them is read
			// for some elements only, being quadratic in the depth.
			fmt.Fprintf(&b, "^%v %s\n", c.Name(), key(c))
			continue
		}
		fmt.Fprintf(&b, "%v %v %s base=%q doc=%q ns=%v\n", c.Kind(), c.Name(), key(c), c.BaseURI(), c.DocumentURI(), c.InScopeNamespaces())
		for a := range c.Attrs() {
			fmt.Fprintf(&b, "  @%v=%q %s id=%v\n", a.Name(), a.StringValue(), key(a), a.IsID())
		}
		for ns := range c.NamespaceNodes() {
			fmt.Fprintf(&b, "  ns %v=%q %s\n", ns.Name(), ns.StringValue(), key(ns))
		}
	}
	return b.String()
}

// elements is every element of a full parse, in document order.
func elements(t *testing.T, src string, o xdm.ParseOptions) []*xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(src, o)
	if err != nil {
		t.Fatal(err)
	}
	var els []*xdm.Node
	tree.Root.Walk(func(n *xdm.Node) bool {
		if n.Kind() == xdm.KindElement {
			els = append(els, n)
		}
		return true
	})
	return els
}

// A view answers what a motionless construct asks as a full parse does,
// and goes on doing so after the cursor has moved on: every view here is
// read once the document has ended.
func TestViewMatchesTree(t *testing.T) {
	o := opts
	o.AllowDOCTYPE = true
	for name, src := range viewDocs() {
		t.Run(name, func(t *testing.T) {
			before := streamtrace.Snapshot()
			c := Open(strings.NewReader(src), o)
			defer c.Close()
			var views []*xdm.Node
			for {
				e, err := c.Next()
				if err != nil {
					t.Fatal(err)
				}
				if e.Kind == EndDocument {
					break
				}
				if e.Kind != StartElement {
					continue
				}
				v, err := c.View()
				if err != nil {
					t.Fatal(err)
				}
				if again, _ := c.View(); again != v {
					t.Fatal("a second View is another node")
				}
				if len(views)%3 == 0 {
					if _, err := c.Peek(); err != nil {
						t.Fatal(err)
					}
					if again, _ := c.View(); again != v {
						t.Fatal("View after Peek is another node")
					}
				}
				views = append(views, v)
			}
			if _, err := c.View(); err == nil {
				t.Fatal("View at the end of the document")
			}
			els := elements(t, src, o)
			if len(views) != len(els) {
				t.Fatalf("%d views for %d elements", len(views), len(els))
			}
			for i, v := range views {
				if got, want := motionless(v, i%97 == 0), motionless(els[i], i%97 == 0); got != want {
					t.Fatalf("element %d:\nview %s\ntree %s", i, got, want)
				}
			}
			if g := streamtrace.Snapshot().Sub(before).Guards; g != 0 {
				t.Fatalf("%d guard hits", g)
			}
		})
	}
}

// pns binds the prefix p.
type pns struct{}

func (pns) ResolvePrefix(p string) (string, bool) { return "urn:p", p == "p" }
func (pns) DefaultElementNamespace() string       { return "" }
func (pns) DefaultFunctionNamespace() string      { return xdm.NSFN }

// fault runs f and reports whether it raised the posture guard, counted.
func fault(t *testing.T, what string, f func()) {
	t.Helper()
	before := streamtrace.Snapshot().Guards
	var p any
	func() {
		defer func() { p = recover() }()
		f()
	}()
	var pe *streamtrace.PostureError
	if e, ok := p.(error); !ok || !errors.As(e, &pe) {
		t.Errorf("%s: no posture fault (recovered %v)", what, p)
		return
	}
	if streamtrace.Snapshot().Guards != before+1 {
		t.Errorf("%s: the guard was not counted", what)
	}
}

// viewAt returns the view of the first element named local in src, and its
// cursor, left at its start tag.
func viewAt(t *testing.T, src, local string) (*xdm.Node, *Cursor) {
	t.Helper()
	c := Open(strings.NewReader(src), opts)
	for {
		e, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == StartElement && e.Node.Name().Local == local {
			v, err := c.View()
			if err != nil {
				t.Fatal(err)
			}
			return v, c
		}
	}
}

// What a view has no content for is a posture fault, never an empty
// answer: on the element, on an ancestor's copy and on the document node's,
// through xdm and through XPath. Its motionless properties raise nothing.
func TestViewGuard(t *testing.T) {
	src := `<a xmlns:p="urn:p" x="1"><z/>text<b i="7" j="8" p:k="9">content<c/></b><d/></a>`
	v, c := viewAt(t, src, "b")
	defer c.Close()
	for _, n := range []*xdm.Node{v, v.Parent(), v.Parent().Parent()} {
		what := n.Kind().String()
		fault(t, what+" Children", func() {
			for range n.Children() {
			}
		})
		fault(t, what+" FirstChild", func() { n.FirstChild() })
		fault(t, what+" LastChild", func() { n.LastChild() })
		fault(t, what+" NumChildren", func() { n.NumChildren() })
		fault(t, what+" ChildElements", func() { n.ChildElements() })
		fault(t, what+" Descendants", func() {
			for range n.Descendants() {
			}
		})
		fault(t, what+" Walk", func() { n.Walk(func(*xdm.Node) bool { return true }) })
		fault(t, what+" StringValue", func() { n.StringValue() })
		fault(t, what+" Atomize", func() { n.Atomize() })
		fault(t, what+" SubtreeEnd", func() { n.SubtreeEnd() })
		fault(t, what+" DescendantsInheritBase", func() { n.DescendantsInheritBase() })
		fault(t, what+" Copy", func() { xdm.Copy(n) })
		if n.Kind() == xdm.KindElement {
			fault(t, what+" NextSibling", func() { n.NextSibling() })
			fault(t, what+" PrevSibling", func() { n.PrevSibling() })
		}
	}
	ctx := xpath.NewContext(v, xpath.Builtins())
	eval := func(expr string) (xdm.Sequence, error) {
		e, err := xpath.CompileWith(expr, xpath.CompileOptions{Version: xpath.XPath31, Namespaces: pns{}})
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		return e.Eval(ctx)
	}
	for _, expr := range []string{
		"string(.)", "data(.)", ". = 'x'", "node()", "*", "text()", "count(*)", "exists(c)", "has-children(.)",
		"descendant::c", ".//c", "..//c", "following-sibling::*", "preceding-sibling::*",
		"following::*", "preceding::*", "string(..)", "/*/*", "//c", "deep-equal(., .)",
		"string-length()", "normalize-space()", "number(.)", "../d", "root()/descendant::*",
	} {
		fault(t, "xpath "+expr, func() { eval(expr) })
	}
	before := streamtrace.Snapshot().Guards
	for expr, want := range map[string]string{
		"name()":                             "b",
		"node-name(.)":                       "b",
		"local-name(..)":                     "a",
		"string(@i)":                         "7",
		"count(@*)":                          "3",
		"string-join(@* ! string(), ',')":    "7,8,9",
		"@p:k = '9'":                         "true",
		"count(@i | @j | @i)":                "2",
		"@i << @j":                           "true",
		". is ..":                            "false",
		".. << .":                            "true",
		"count(ancestor::*)":                 "1",
		"count(ancestor-or-self::node())":    "3",
		"string(../@x)":                      "1",
		"count(namespace::*)":                "2",
		"sort(in-scope-prefixes(.))":         "p xml",
		"namespace-uri-for-prefix('p', .)":   "urn:p",
		"base-uri(.)":                        "http://ex/d.xml",
		"document-uri(/)":                    "http://ex/d.xml",
		"root() instance of document-node()": "true",
		"generate-id(.) = generate-id(.)":    "true",
		"generate-id(..) != generate-id(.)":  "true",
		"exists(parent::a)":                  "true",
		"self::b instance of element(b)":     "true",
	} {
		got, err := eval("string-join((" + expr + ") ! string(), ' ')")
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if s := got[0].(*xdm.Atomic).Str(); s != want {
			t.Errorf("%s = %q, want %q", expr, s, want)
		}
	}
	if g := streamtrace.Snapshot().Guards - before; g != 0 {
		t.Errorf("motionless expressions hit the guard %d times", g)
	}
	// An attribute of a view, and a grounded copy, are not shallow.
	if got := v.Attr("", "i").StringValue(); got != "7" {
		t.Errorf("@i = %q", got)
	}
	g, err := c.Ground()
	if err != nil {
		t.Fatal(err)
	}
	if got := g.StringValue(); got != "content" {
		t.Errorf("the grounded copy reads %q", got)
	}
}

// streamed is every node a run over src hands out or copies, read once the
// document has ended: the view of each element, its ancestors' copies, its
// attributes and namespace nodes; and the elements grounded at random (now
// or later), whole, with theirs.
func streamed(t *testing.T, src string, o xdm.ParseOptions, rnd *rand.Rand) []*xdm.Node {
	t.Helper()
	var out []*xdm.Node
	add := func(n *xdm.Node, deep bool) {
		for c := n; c != nil; c = c.Parent() {
			out = append(out, c)
			for a := range c.Attrs() {
				out = append(out, a)
			}
			for ns := range c.NamespaceNodes() {
				out = append(out, ns)
			}
		}
		if deep {
			for d := range n.Descendants() {
				out = append(out, d)
				for a := range d.Attrs() {
					out = append(out, a)
				}
			}
		}
	}
	c := Open(strings.NewReader(src), o)
	defer c.Close()
	for {
		e, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == EndDocument {
			break
		}
		if e.Kind != StartElement {
			continue
		}
		if rnd.Intn(4) != 0 {
			v, err := c.View()
			if err != nil {
				t.Fatal(err)
			}
			add(v, false)
		}
		switch rnd.Intn(16) {
		case 0:
			g, err := c.Ground()
			if err != nil {
				t.Fatal(err)
			}
			add(g, true)
		case 1:
			if err := c.GroundLater(func(g *xdm.Node) error { add(g, true); return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out
}

// original maps a key to the full parse's node of that key.
func original(t *testing.T, src string, o xdm.ParseOptions) map[string]*xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(src, o)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*xdm.Node{key(tree.Root): tree.Root}
	tree.Root.Walk(func(n *xdm.Node) bool {
		for d := range n.Children() {
			m[key(d)] = d
		}
		for a := range n.Attrs() {
			m[key(a)] = a
		}
		for ns := range n.NamespaceNodes() {
			m[key(ns)] = ns
		}
		return true
	})
	return m
}

// keys is the keys of a node sequence.
func keys(s xdm.Sequence) []string {
	out := make([]string, len(s))
	for i, it := range s {
		out[i] = key(it.(*xdm.Node))
	}
	return out
}

// Views and grounded copies are their originals to identity and order:
// "is", "<<", Identity, generate-id, union, intersect, except, path-result
// sorting and dedup, innermost and outermost agree with a full parse over
// copies of the same nodes made at different times, in different trees
// and different batches.
func TestViewIdentityAndOrder(t *testing.T) {
	o := opts
	o.AllowDOCTYPE = true
	for name, src := range viewDocs() {
		t.Run(name, func(t *testing.T) {
			rnd := rand.New(rand.NewSource(int64(len(src))))
			nodes := streamed(t, src, o, rnd)
			orig := original(t, src, o)
			tree := func(n *xdm.Node) *xdm.Node {
				x := orig[key(n)]
				if x == nil {
					t.Fatalf("no original for %v %v %s", n.Kind(), n.Name(), key(n))
				}
				if x.Kind() != n.Kind() || x.Name() != n.Name() {
					t.Fatalf("%v %v is a copy of %v %v", n.Kind(), n.Name(), x.Kind(), x.Name())
				}
				return x
			}
			for range 20000 {
				a, b := nodes[rnd.Intn(len(nodes))], nodes[rnd.Intn(len(nodes))]
				ta, tb := tree(a), tree(b)
				if a.Is(b) != ta.Is(tb) || (a.Identity() == b.Identity()) != ta.Is(tb) {
					t.Fatalf("%v %s is %v %s: %v, tree %v", a.Name(), key(a), b.Name(), key(b), a.Is(b), ta.Is(tb))
				}
				if a.Compare(b) != ta.Compare(tb) {
					t.Fatalf("%v %s vs %v %s: %d, tree %d", a.Name(), key(a), b.Name(), key(b), a.Compare(b), ta.Compare(tb))
				}
				if (genid.Of(a) == genid.Of(b)) != ta.Is(tb) {
					t.Fatalf("generate-id of %s and %s", key(a), key(b))
				}
			}
			pick := func() (xdm.Sequence, xdm.Sequence) {
				var s, ts xdm.Sequence
				for range 1 + rnd.Intn(60) {
					n := nodes[rnd.Intn(len(nodes))]
					s, ts = append(s, n), append(ts, tree(n))
				}
				return s, ts
			}
			va, vb := xdm.QName{Local: "a"}, xdm.QName{Local: "b"}
			exprs := []string{"$a | $b", "$a intersect $b", "$a except $b", "$a/..", "$a/ancestor::*",
				"$a/(@* | ..)", "outermost($a)", "innermost($a)", "$a ! generate-id()", "$a[1] is $b[1]",
				"$a[1] << $b[1]", "count($a | $b)", "distinct-values(($a | $b) ! generate-id())",
				"for $x in $a return count($b[. is $x])"}
			for range 400 {
				sa, ta := pick()
				sb, tb := pick()
				for _, s := range [][2]xdm.Sequence{{sa, ta}, {sb, tb}} {
					if got, want := keys(xdm.SortDocumentOrder(s[0])), keys(xdm.SortDocumentOrder(s[1])); !slices.Equal(got, want) {
						t.Fatalf("sorted:\n%v\n%v", got, want)
					}
				}
				for _, op := range []func(a, b xdm.Sequence) xdm.Sequence{xdm.Union, xdm.Intersect, xdm.Except} {
					if got, want := keys(op(sa, sb)), keys(op(ta, tb)); !slices.Equal(got, want) {
						t.Fatalf("set operator:\n%v\n%v", got, want)
					}
				}
				for _, expr := range exprs {
					e, err := xpath.CompileWith(expr, xpath.CompileOptions{Version: xpath.XPath31})
					if err != nil {
						t.Fatal(err)
					}
					run := func(a, b xdm.Sequence) []string {
						r, err := e.Eval(xpath.NewContext(nil, xpath.Builtins()).WithVar(va, a).WithVar(vb, b))
						if err != nil {
							t.Fatalf("%s: %v", expr, err)
						}
						var out []string
						for _, it := range r {
							if n, ok := it.(*xdm.Node); ok {
								out = append(out, key(n))
							} else if s := it.(*xdm.Atomic).Str(); strings.HasPrefix(s, "N") {
								out = append(out, s[strings.IndexByte(s, 'x'):])
							} else {
								out = append(out, s)
							}
						}
						return out
					}
					if got, want := run(sa, sb), run(ta, tb); !slices.Equal(got, want) {
						t.Fatalf("%s:\nstreamed %v\ntree     %v", expr, got, want)
					}
				}
			}
		})
	}
}
