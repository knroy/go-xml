package c14n

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Namespace nodes as node-set members (NamespaceSet, FromXPathFilter). The
// expected outputs are worked from C14N 1.0 §2.3 and Exclusive C14N §3; the
// comment on each case says which rule decides it. Every input here is also
// in tests/c14n's xmlsec1 differential, which agrees except where
// docs/c14n.md, "Where xmlsec1 differs", says why it does not.

func canonFilter(t *testing.T, src, filter string, ns map[string]string, alg Algorithm, prefixes ...string) string {
	t.Helper()
	set, err := FromXPathFilter(parse(t, src), filter, ns)
	if err != nil {
		t.Fatal(err)
	}
	return canonSet(t, set, alg, prefixes...)
}

func TestNamespaceSetElementDroppedNodesKept(t *testing.T) {
	src := `<e1 xmlns="a:b"><e2 xmlns:foo="urn:foo"><e3/></e2></e1>`
	ns := map[string]string{"ab": "a:b"}
	for _, tc := range []struct {
		alg      Algorithm
		prefixes []string
		want     string
	}{
		// §2.3: e2 is outside the set, but its namespace axis is still
		// processed, so its in-set foo node renders into e1's content. e3's
		// foo renders too: its nearest ancestor in the set, e1, has none.
		{Inclusive10, nil, `<e1 xmlns="a:b"> xmlns:foo="urn:foo"<e3 xmlns:foo="urn:foo"></e3></e1>`},
		{Inclusive11, nil, `<e1 xmlns="a:b"> xmlns:foo="urn:foo"<e3 xmlns:foo="urn:foo"></e3></e1>`},
		// Exclusive §3: e2's nodes need e2 in the set; e3 uses only the
		// default namespace, which e1 already rendered.
		{Exclusive10, nil, `<e1 xmlns="a:b"><e3></e3></e1>`},
		// A listed prefix follows Canonical XML's rule, e2 included.
		{Exclusive10, []string{"foo"}, `<e1 xmlns="a:b"> xmlns:foo="urn:foo"<e3 xmlns:foo="urn:foo"></e3></e1>`},
	} {
		if got := canonFilter(t, src, `not(self::ab:e2)`, ns, tc.alg, tc.prefixes...); got != tc.want {
			t.Errorf("%s %v:\n got %s\nwant %s", tc.alg, tc.prefixes, got, tc.want)
		}
	}
}

func TestNamespaceSetUtilisedNodeOutsideSet(t *testing.T) {
	src := `<r xmlns:foo="urn:foo" xmlns:bar="urn:bar"><foo:a bar:x="1"><b/></foo:a></r>`
	filter := `not(name()='bar')` // every bar namespace node, and nothing else
	for _, tc := range []struct {
		alg      Algorithm
		prefixes []string
		want     string
	}{
		// Canonical XML renders only nodes in the set: no bar anywhere, so
		// the output does not declare the prefix bar:x uses. That is the
		// node set's doing, and the specification's result.
		{Inclusive10, nil, `<r xmlns:foo="urn:foo"><foo:a bar:x="1"><b></b></foo:a></r>`},
		// Exclusive C14N too: a node outside the set is not rendered
		// "even if its parent node is included" (§1.1), utilised or not.
		{Exclusive10, nil, `<r><foo:a xmlns:foo="urn:foo" bar:x="1"><b></b></foo:a></r>`},
		// Listed, bar goes back to Canonical XML's rule.
		{Exclusive10, []string{"bar"}, `<r><foo:a xmlns:foo="urn:foo" bar:x="1"><b></b></foo:a></r>`},
	} {
		if got := canonFilter(t, src, filter, nil, tc.alg, tc.prefixes...); got != tc.want {
			t.Errorf("%s %v:\n got %s\nwant %s", tc.alg, tc.prefixes, got, tc.want)
		}
	}
}

func TestNamespaceSetDefaultNodeDropped(t *testing.T) {
	// a's default namespace node is dropped; b's and c's are kept.
	filter := `not(count(.|../namespace::*)=count(../namespace::*) and name()='' and local-name(..)='a')`
	src := `<r xmlns="urn:d"><a><b/></a><c/></r>`
	// Canonical XML: a has no default node in the set and r does, so
	// xmlns=""; b's node differs from a's (none), so it renders; c's equals
	// r's. Exclusive §3's xmlns="" conditions hold for a as well, and b's
	// nearest utilising output ancestor, a, has no node in the set.
	want := `<r xmlns="urn:d"><a xmlns=""><b xmlns="urn:d"></b></a><c></c></r>`
	for _, alg := range []Algorithm{Inclusive10, Inclusive11, Exclusive10} {
		if got := canonFilter(t, src, filter, nil, alg); got != want {
			t.Errorf("%s:\n got %s\nwant %s", alg, got, want)
		}
	}
	if got := canonFilter(t, src, filter, nil, Exclusive10, ""); got != want {
		t.Errorf("exclusive, #default listed:\n got %s\nwant %s", got, want)
	}

	// The dropped node binds a different URI from the ancestor's. It is
	// outside the set, so it does not render (§1.1); a has no default node
	// in the set and r has one, so xmlns=""; b's renders because a has none.
	src = `<r xmlns="urn:1"><a xmlns="urn:2"><b/></a></r>`
	want = `<r xmlns="urn:1"><a xmlns=""><b xmlns="urn:2"></b></a></r>`
	if got := canonFilter(t, src, filter, nil, Exclusive10); got != want {
		t.Errorf("rebound, exclusive:\n got %s\nwant %s", got, want)
	}
}

func TestNamespaceSetUndeclaringElementDropped(t *testing.T) {
	// e2 undeclares the default namespace; XPath 1.0 gives that no
	// namespace node, so e2, dropped, renders only foo. e3 has no default
	// node and its nearest ancestor in the set, e1, has one: xmlns="".
	src := `<e1 xmlns="a:b"><e2 xmlns="" xmlns:foo="urn:foo"><e3/></e2></e1>`
	want := `<e1 xmlns="a:b"> xmlns:foo="urn:foo"<e3 xmlns="" xmlns:foo="urn:foo"></e3></e1>`
	if got := canonFilter(t, src, `not(self::e2)`, nil, Inclusive10); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

// TestNamespaceSetAllNodesEqualsDocument: a filter that keeps every node,
// namespace nodes included, is the whole document, so the NamespaceSet path
// must produce exactly what the default path does, for every algorithm and
// with and without a PrefixList.
func TestNamespaceSetAllNodesEqualsDocument(t *testing.T) {
	for _, src := range []string{
		`<r xmlns="urn:d" xmlns:p="urn:p" xmlns:u="urn:u"><p:a p:x="1"><b/><c xmlns=""/></p:a><!--c--><?pi d?></r>`,
		`<r xmlns:p="urn:1"><p:a><p:b xmlns:p="urn:2"><p:c/></p:b></p:a></r>`,
		`<r xmlns="urn:d"><a xmlns=""><b xmlns="urn:e"/></a></r>`,
		`<r xml:lang="en" xmlns:q="urn:q"><q:m xml:space="preserve">t</q:m></r>`,
	} {
		doc := parse(t, src)
		set, err := FromXPathFilter(doc, "true()", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, alg := range allAlgorithms {
			for _, prefixes := range [][]string{nil, {"", "p", "q", "u"}} {
				opts := Options{Algorithm: alg, InclusiveNamespacePrefixes: prefixes}
				want, err := Bytes(doc, opts)
				if err != nil {
					t.Fatal(err)
				}
				got, err := BytesNodeSet(set, opts)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != string(want) {
					t.Errorf("%s %v on %s:\n got %s\nwant %s", alg, prefixes, src, got, want)
				}
			}
		}
	}
}

func TestFromXPathFilterErrors(t *testing.T) {
	doc := parse(t, `<a/>`)
	if _, err := FromXPathFilter(doc, "self::", nil); err == nil {
		t.Error("a filter that does not compile: no error")
	}
	// count(.) is 1 for every node, so this divides by zero only when run.
	if _, err := FromXPathFilter(doc, "1 idiv (count(.) - 1) = 0", nil); err == nil || !strings.Contains(err.Error(), "FOAR0001") {
		t.Errorf("a filter that fails at evaluation: err %v, want FOAR0001", err)
	}
	// "//" needs a tree rooted at a document node; a detached element has
	// none, so collecting the input node-set fails (XPDY0050).
	detached := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "d"}}
	if _, err := FromXPathFilter(detached, "true()", nil); err == nil {
		t.Error("a tree with no document node: no error")
	}
	set, err := FromXPathFilter(doc, "self::a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if set.Root() != doc || !set.Contains(doc.Children[0]) || set.Contains(doc) {
		t.Error("membership of self::a")
	}
	if ns, ok := set.(NamespaceSet); !ok || ns.ContainsNamespace(doc.Children[0], "xml") {
		t.Error("FromXPathFilter must be a NamespaceSet; self::a keeps no namespace node")
	}
}
