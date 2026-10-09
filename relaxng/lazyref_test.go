package relaxng

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A recursive definition brought in by <include ns="..."> or <externalRef
// ns="..."> keeps that ns at every level. The recursion is resolved lazily, by
// a sub-compiler, and that sub-compiler once lost the ns: the inner <a> was
// compiled in no namespace, so <a xmlns="urn:x"><a/></a> was refused and one
// whose inner <a> is in no namespace was accepted.
func TestRecursiveRefKeepsTheInheritedNs(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"inc.rng": `<grammar` + rngNS + `><start><ref name="a"/></start>
			<define name="a"><element name="a">
				<optional><ref name="a"/></optional>
			</element></define></grammar>`,
	}}
	for _, schema := range []string{
		`<grammar` + rngNS + `><include href="inc.rng" ns="urn:x"/></grammar>`,
		`<externalRef` + rngNS + ` href="inc.rng" ns="urn:x"/>`,
	} {
		s, err := compileWith(t, schema, Options{Resolver: r})
		if err != nil {
			t.Fatalf("compile %s: %v", schema, err)
		}
		for _, c := range []struct {
			doc   string
			valid bool
		}{
			{`<a xmlns="urn:x"/>`, true},
			{`<a xmlns="urn:x"><a/></a>`, true},
			{`<a xmlns="urn:x"><a><a/></a></a>`, true},
			{`<a xmlns="urn:x"><a xmlns=""/></a>`, false},
		} {
			doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Validate(doc.Root); (err == nil) != c.valid {
				t.Errorf("%s against %s: valid = %v, want %v (err %v)",
					c.doc, schema, err == nil, c.valid, err)
			}
		}
	}
}

// meshGrammar is n definitions, each an element that may hold any of them, so
// every reference after the first to a definition is a recursive one.
func meshGrammar(n int) string {
	var refs, defs strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&refs, `<ref name="d%d"/>`, i)
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(&defs, `<define name="d%d"><element name="e%d">
			<zeroOrMore><choice>%s</choice></zeroOrMore></element></define>`, i, i, refs.String())
	}
	return `<grammar` + rngNS + `><start><ref name="d0"/></start>` + defs.String() + `</grammar>`
}

// A recursive reference resolves to the pattern its definition already
// compiled to, not to a fresh compilation of the same body. Each fresh copy
// recompiled everything the definition reaches and had new pointers, which
// defeated the competition check's memo: DocBook 5.2 compiled its 1,922
// definitions 86,752 times. The allocation bound catches a return to that;
// the mesh below needs about 6.5k allocations shared and 35k not.
func TestLazyRefSharesTheCompiledDefinition(t *testing.T) {
	src := meshGrammar(20)
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// d0's own content refers to d0 lazily; that reference must resolve to
	// the very pattern start holds.
	var lazy *refPat
	var find func(p pattern)
	find = func(p pattern) {
		switch x := p.(type) {
		case *refPat:
			if x.resolve != nil && x.name == "d0" && lazy == nil {
				lazy = x
			}
			if x.resolve == nil {
				find(x.cached)
			}
		case *elementPat:
			find(x.Pattern)
		case *choicePat:
			find(x.Left)
			find(x.Right)
		case *oneOrMorePat:
			find(x.Pattern)
		}
	}
	find(s.start)
	if lazy == nil {
		t.Fatal("no lazy reference to d0 in the compiled schema")
	}
	if got, _ := lazy.get(); got != s.start {
		t.Errorf("the lazy reference to d0 resolved to a fresh copy, not the compiled definition")
	}

	allocs := testing.AllocsPerRun(5, func() {
		if _, err := Compile(tree.Root); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 15_000 {
		t.Errorf("compiling the mesh took %.0f allocations, want at most 15000", allocs)
	}
}
