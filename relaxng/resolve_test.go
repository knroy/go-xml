package relaxng

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// mapResolver serves schemas from a fixed map, standing in for a caller that
// knows which documents exist. It records what it was asked for, so the
// base-URI contract can be asserted rather than assumed.
type mapResolver struct {
	docs map[string]string
	seen []string
}

func (r *mapResolver) ResolveSchema(href string) (*xdm.Node, error) {
	r.seen = append(r.seen, href)
	src, ok := r.docs[href]
	if !ok {
		return nil, fmt.Errorf("no such schema")
	}
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	return tree.Root, nil
}

// The default must stay closed. An href is an instruction to go and read
// something, and a schema does not get to decide that for the caller.
func TestExternalReferencesNeedAResolver(t *testing.T) {
	for _, src := range []string{
		`<externalRef` + rngNS + ` href="other.rng"/>`,
		`<grammar` + rngNS + `><include href="other.rng"/>
			<start><element name="foo"><empty/></element></start></grammar>`,
	} {
		_, err := compileSrc(t, src)
		if err == nil {
			t.Errorf("an external reference was followed with no Resolver:\n%s", src)
			continue
		}
		if !strings.Contains(err.Error(), "Resolver") {
			t.Errorf("error %q should say a Resolver is needed", err)
		}
	}
}

func compileWith(t *testing.T, src string, opts Options) (*Schema, error) {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return CompileWithOptions(tree.Root, opts)
}

func TestExternalRef(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"other.rng": `<element` + rngNS + ` name="foo"><empty/></element>`,
	}}
	s, err := compileWith(t, `<externalRef`+rngNS+` href="other.rng"/>`,
		Options{Resolver: r})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	doc, err := xdm.ParseString(`<foo/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("the referenced schema should accept <foo/>: %v", err)
	}
}

// xml:base composes outward, so a nested one resolves against the one above.
func TestExternalRefResolvesAgainstBase(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"http://example.com/sub/other.rng": `<element` + rngNS +
			` name="foo"><empty/></element>`,
	}}
	const src = `<div` + rngNS + ` xml:base="sub/">
		<externalRef href="other.rng"/></div>`
	if _, err := compileWith(t, src, Options{
		Resolver: r, BaseURI: "http://example.com/schema.rng",
	}); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(r.seen) != 1 || r.seen[0] != "http://example.com/sub/other.rng" {
		t.Errorf("resolver saw %v, want the base-resolved href", r.seen)
	}
}

// An include merges the named grammar's definitions, and a definition written
// inside the include replaces rather than combines with the one it names.
// That override is the whole point: a schema adopts another and changes the
// parts it needs.
func TestIncludeOverrides(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"base.rng": `<grammar` + rngNS + `>
			<start><element name="root"><ref name="body"/></element></start>
			<define name="body"><element name="original"><empty/></element></define>
		</grammar>`,
	}}
	const src = `<grammar` + rngNS + `>
		<include href="base.rng">
			<define name="body"><element name="replaced"><empty/></element></define>
		</include>
	</grammar>`
	s, err := compileWith(t, src, Options{Resolver: r})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		doc  string
		want bool
	}{
		{`<root><replaced/></root>`, true},
		{`<root><original/></root>`, false},
	}
	for _, c := range cases {
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := s.Validate(doc.Root) == nil
		if got != c.want {
			t.Errorf("%s: valid=%v, want %v", c.doc, got, c.want)
		}
	}
}

// Without an override the included definitions are used as written.
func TestIncludeWithoutOverride(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"base.rng": `<grammar` + rngNS + `>
			<start><element name="root"><ref name="body"/></element></start>
			<define name="body"><element name="original"><empty/></element></define>
		</grammar>`,
	}}
	s, err := compileWith(t, `<grammar`+rngNS+`><include href="base.rng"/></grammar>`,
		Options{Resolver: r})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	doc, err := xdm.ParseString(`<root><original/></root>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("the included definition should apply: %v", err)
	}
}

// A cycle of includes must stop rather than recurse forever. With a resolver
// reading from the network this would be a request loop, not merely a hang.
func TestIncludeCycleTerminates(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"a.rng": `<grammar` + rngNS + `><include href="b.rng"/></grammar>`,
		"b.rng": `<grammar` + rngNS + `><include href="a.rng"/></grammar>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`><include href="a.rng"/></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("a cycle of includes should be refused")
	}
	if !strings.Contains(err.Error(), "deep") {
		t.Errorf("error = %v, want one naming the depth limit", err)
	}
}

// An href names a whole document, so a fragment is refused rather than
// silently ignored — ignoring it would include more than was asked for.
func TestHrefFragmentRefused(t *testing.T) {
	r := &mapResolver{docs: map[string]string{}}
	_, err := compileWith(t, `<externalRef`+rngNS+` href="other.rng#part"/>`,
		Options{Resolver: r})
	if err == nil || !strings.Contains(err.Error(), "fragment") {
		t.Errorf("error = %v, want one naming the fragment", err)
	}
	if len(r.seen) != 0 {
		t.Errorf("the resolver was consulted with %v; it should not have been", r.seen)
	}
}

// parentRef reaches from a nested grammar into the one enclosing it, which is
// the only door between the two scopes.
func TestParentRef(t *testing.T) {
	const src = `<grammar` + rngNS + `>
		<start><element name="root"><ref name="inner"/></element></start>
		<define name="inner">
			<grammar><start><parentRef name="leaf"/></start></grammar>
		</define>
		<define name="leaf"><element name="leaf"><empty/></element></define>
	</grammar>`
	s, err := compileSrc(t, src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	doc, err := xdm.ParseString(`<root><leaf/></root>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("parentRef should reach the outer definition: %v", err)
	}
}

// A parentRef with nothing to reach out to is an error, not an empty pattern.
func TestParentRefNeedsAnEnclosingGrammar(t *testing.T) {
	_, err := compileSrc(t, `<grammar`+rngNS+`>
		<start><parentRef name="x"/></start></grammar>`)
	if err == nil {
		t.Fatal("a parentRef at the outermost grammar should be refused")
	}
}
