package xpath

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// testCollections is a CollectionResolver over a fixed map, standing in for a
// caller that knows which collections exist. The empty key is the default
// collection.
type testCollections struct {
	docs map[string][]string // uri -> document sources
	// lastBase records what the engine passed, so the base-URI contract can
	// be asserted rather than assumed.
	lastURI, lastBase string
}

func (c *testCollections) ResolveCollection(uri, base string) (xdm.Sequence, error) {
	c.lastURI, c.lastBase = uri, base
	srcs, ok := c.docs[uri]
	if !ok {
		return nil, fmt.Errorf("no such collection")
	}
	var out xdm.Sequence
	for _, s := range srcs {
		tree, err := xdm.Parse(strings.NewReader(s), xdm.ParseOptions{})
		if err != nil {
			return nil, err
		}
		out = append(out, tree.Root)
	}
	return out, nil
}

// nopDocs is a DocumentResolver that resolves nothing. It exists only to make
// Docs non-nil, so that "fn:doc is enabled" can be distinguished from
// "fn:collection is enabled".
type nopDocs struct{}

func (nopDocs) ResolveDocument(string, string) (*xdm.Tree, error) {
	return nil, fmt.Errorf("no documents")
}

func collCtx(t *testing.T, r CollectionResolver) *Context {
	t.Helper()
	ctx := NewContext(mustParse(t, testDoc), Builtins())
	ctx.Collections = r
	return ctx
}

// A collection is only reachable when the caller configures one. This is the
// security-relevant half: the default must stay closed.
func TestCollectionRequiresResolver(t *testing.T) {
	for _, expr := range []string{`collection()`, `collection('books')`} {
		err := evalErr(t, testDoc, expr)
		if !strings.Contains(err.Error(), "FODC0002") {
			t.Errorf("%s = %v, want FODC0002", expr, err)
		}
	}
}

func TestCollectionResolves(t *testing.T) {
	r := &testCollections{docs: map[string][]string{
		"books":  {`<b>one</b>`, `<b>two</b>`},
		"":       {`<d>default</d>`},
		"single": {`<s>x</s>`},
	}}

	cases := []struct{ expr, want string }{
		// The named collection returns every document in it.
		{`count(collection('books'))`, "2"},
		{`string-join(collection('books')/b, ',')`, "one,two"},
		// No argument is the default collection, which the resolver sees as
		// the empty URI.
		{`string-join(collection()/d, ',')`, "default"},
		// An empty sequence argument means the default collection too, not a
		// type error: the parameter is declared xs:string?.
		{`string-join(collection(())/d, ',')`, "default"},
		// A collection composes with the rest of the language rather than
		// being a special form.
		{`count(collection('books')/b[. = 'two'])`, "1"},
	}
	for _, c := range cases {
		got := mustEval(t, collCtx(t, r), c.expr)
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A resolver that fails must surface as FODC0002 and name the URI, not panic
// or return an empty sequence that looks like a legitimate empty collection.
func TestCollectionResolverError(t *testing.T) {
	r := &testCollections{docs: map[string][]string{}}
	_, err := Eval(`collection('nonesuch')`, collCtx(t, r), testNS{})
	if err == nil {
		t.Fatal("a failing resolver should be an error")
	}
	if !strings.Contains(err.Error(), "FODC0002") ||
		!strings.Contains(err.Error(), "nonesuch") {
		t.Errorf("error = %v, want FODC0002 naming the URI", err)
	}
}

// An unusable URI is rejected before the resolver is consulted, matching
// fn:doc: whether a collection is configured does not come into it.
func TestCollectionInvalidURI(t *testing.T) {
	r := &testCollections{docs: map[string][]string{}}
	_, err := Eval(`collection(':/')`, collCtx(t, r), testNS{})
	if err == nil {
		t.Fatal("an invalid collection URI should be an error")
	}
	if !strings.Contains(err.Error(), "FODC0004") {
		t.Errorf("error = %v, want FODC0004", err)
	}
	if r.lastURI != "" {
		t.Errorf("resolver was consulted with %q; it should not have been called", r.lastURI)
	}
}

// The argument is xs:string?, so a non-string is a type error rather than a
// URI to stringify — the same rule fn:doc follows. Checked because the
// alternative, silently accepting xs:integer(2) as the URI "2", would turn a
// mistake into a lookup.
func TestCollectionNonStringArgument(t *testing.T) {
	r := &testCollections{docs: map[string][]string{"2": {`<a/>`}}}
	_, err := Eval(`collection(xs:integer(2))`, collCtx(t, r), testNS{})
	if err == nil {
		t.Fatal("a non-string collection URI should be a type error")
	}
	if !strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("error = %v, want XPTY0004", err)
	}
	if r.lastURI != "" {
		t.Errorf("resolver was consulted with %q; it should not have been called", r.lastURI)
	}
}

// A relative URI resolves against the static base URI — the base of the
// expression — not against the context item's document. Resolving is the
// resolver's job; what is checked here is which base the engine hands it.
func TestCollectionUsesStaticBaseURI(t *testing.T) {
	r := &testCollections{docs: map[string][]string{"books": {`<b>one</b>`}}}
	ctx := collCtx(t, r)
	ctx.StaticBaseURI = "http://example.com/base/"
	if _, err := Eval(`collection('books')`, ctx, testNS{}); err != nil {
		t.Fatalf("collection: %v", err)
	}
	if r.lastBase != "http://example.com/base/" {
		t.Errorf("resolver got base %q, want the static base URI", r.lastBase)
	}
}

// With no static base URI the context item's document base is the fallback, so
// a caller who set neither is not left with nothing to resolve against.
func TestCollectionFallsBackToItemBase(t *testing.T) {
	r := &testCollections{docs: map[string][]string{"books": {`<b>one</b>`}}}
	tree, err := xdm.ParseString(`<catalog/>`,
		xdm.ParseOptions{BaseURI: "http://example.com/doc.xml"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := NewContext(tree.Root, Builtins())
	ctx.Collections = r
	if _, err := Eval(`collection('books')`, ctx, testNS{}); err != nil {
		t.Fatalf("collection: %v", err)
	}
	if r.lastBase != "http://example.com/doc.xml" {
		t.Errorf("resolver got base %q, want the document base URI", r.lastBase)
	}
}

// Setting Docs must not enable collections, and vice versa. The two are
// separate switches on purpose.
func TestCollectionIndependentOfDocs(t *testing.T) {
	ctx := NewContext(mustParse(t, testDoc), Builtins())
	ctx.Docs = nopDocs{} // fn:doc enabled, fn:collection not
	_, err := Eval(`collection('books')`, ctx, testNS{})
	if err == nil || !strings.Contains(err.Error(), "FODC0002") {
		t.Errorf("collection() = %v, want FODC0002 with only Docs set", err)
	}
}

// The resolver must still be reachable from a nested scope. Context is copied
// by value on every focus change, so a field that is not carried across would
// work at the top level and fail inside a predicate or a for-expression.
func TestCollectionSurvivesScopeChange(t *testing.T) {
	r := &testCollections{docs: map[string][]string{"books": {`<b>one</b>`}}}
	const expr = `count(/catalog/book[count(collection('books')) = 1])`
	if got := mustEval(t, collCtx(t, r), expr); got != "3" {
		t.Errorf("%s = %q, want 3", expr, got)
	}
	if got := mustEval(t, collCtx(t, r), `count(for $x in 1 to 2 return collection('books'))`); got != "2" {
		t.Errorf("collection inside a for-expression = %q, want 2", got)
	}
}
