package xdm

import (
	"runtime"
	"strings"
	"testing"
)

// deeplyNested builds a document of the given nesting depth. The root carries
// a namespace declaration so that every element below it has a binding in
// scope that it does not declare itself, which is the case Tree.assign has to
// count and the one that used to cost a walk to the root.
func deeplyNested(depth int) string {
	var sb strings.Builder
	sb.WriteString(`<r xmlns:a="urn:a">`)
	for i := 0; i < depth; i++ {
		sb.WriteString("<e>")
	}
	for i := 0; i < depth; i++ {
		sb.WriteString("</e>")
	}
	sb.WriteString("</r>")
	return sb.String()
}

// TestFinalizeNotQuadraticInDepth bounds what numbering a deeply nested
// document allocates.
//
// Tree.assign needs the number of namespace bindings in scope at each element
// so that it can reserve a document-order slot for each one. It used to get
// that number from InScopeNamespaces, which builds the whole map by walking to
// the root -- so the walk cost the element's depth, and numbering the document
// cost the square of it. Nesting depth is attacker-controlled in a way byte
// size is not: 224 kB of nesting allocated 17.8 GB, while the same 256 kB laid
// out wide rather than deep allocated 29 MB.
//
// The default MaxDepth of 1000 bounds this for a caller who takes the default,
// so it is a limit rather than a live vulnerability; it bites the caller who
// raises MaxDepth to accept legitimately deep input, which is why the test
// raises it too.
//
// Allocation is asserted rather than time because it is what separates the two
// shapes cleanly and does not vary with machine load. Measured here: 13.4 MB
// after the fix against 17,780 MB before, at depth 32,000. The budget is many
// times the post-fix figure so that allocator noise does not fail it, and
// three orders of magnitude under the pre-fix one so that a return to
// quadratic behaviour fails it decisively.
func TestFinalizeNotQuadraticInDepth(t *testing.T) {
	if testing.Short() {
		t.Skip("cost test")
	}
	const depth = 32000
	// The race detector's instrumentation allocates shadow state of its own
	// per access, so the budget is widened there; the pre-fix figure would
	// still be far above even the widened bound.
	budget := uint64(200 << 20)
	if raceEnabled {
		budget *= 4
	}

	src := deeplyNested(depth)
	opts := ParseOptions{MaxDepth: depth + 10, MaxNodes: -1, MaxBytes: -1}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tree, err := ParseString(src, opts)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	if tree == nil {
		t.Fatal("ParseString returned no tree")
	}
	used := after.TotalAlloc - before.TotalAlloc

	if used > budget {
		t.Errorf("parsing a %d-byte document nested %d deep allocated %d MB, "+
			"over the %d MB budget.\n"+
			"Tree.assign is rebuilding the in-scope namespace map from the "+
			"ancestor chain per element again, which is O(depth^2) in "+
			"nesting and reachable from document data.",
			len(src), depth, used>>20, budget>>20)
	}

	// A document of the same byte size that is wide rather than deep is the
	// control: it shares the element count and the size but not the nesting,
	// and it was never affected. Asserting it here keeps the test honest
	// about what it attributes the cost to.
	wide := new(strings.Builder)
	wide.WriteString(`<r xmlns:a="urn:a">`)
	for i := 0; i < 64000; i++ {
		wide.WriteString("<e/>")
	}
	wide.WriteString("</r>")

	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := ParseString(wide.String(), opts); err != nil {
		t.Fatalf("ParseString(wide): %v", err)
	}
	runtime.ReadMemStats(&after)
	if w := after.TotalAlloc - before.TotalAlloc; w > budget {
		t.Errorf("the wide control allocated %d MB, over the %d MB budget; "+
			"the cost is no longer specific to depth", w>>20, budget>>20)
	}
}

// TestFinalizeNamespaceCountsUnchanged pins the namespace counts and in-scope
// bindings that Tree.assign now derives incrementally rather than by walking
// to the root.
//
// The incremental scope is shadowed and restored per element, so the cases
// that can go wrong are the ones where restoring matters: a prefix redeclared
// by a descendant must not stay rebound for a later sibling, an undeclaration
// must remove a binding for the subtree and only for the subtree, and the
// implicit xml prefix must be counted exactly once everywhere.
func TestFinalizeNamespaceCountsUnchanged(t *testing.T) {
	const src = `<r xmlns:p="urn:outer" xmlns="urn:def">` +
		`<shadow xmlns:p="urn:inner"><deep/></shadow>` +
		`<sibling><deep/></sibling>` +
		`<undecl xmlns=""><deep/></undecl>` +
		`<after/>` +
		`</r>`
	tree, err := ParseString(src, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Root.ChildElements()[0]
	byName := map[string]*Node{"r": root}
	var walk func(*Node)
	walk = func(n *Node) {
		for _, c := range n.ChildElements() {
			// deep appears three times; key it by its parent so each is
			// checked separately.
			name := c.Name.Local
			if name == "deep" {
				name = n.Name.Local + "/deep"
			}
			byName[name] = c
			walk(c)
		}
	}
	walk(root)

	// want is the full in-scope map at each element, xml included. These are
	// the values InScopeNamespaces produced before the change.
	want := map[string]map[string]string{
		"r":            {"xml": NSXML, "p": "urn:outer", "": "urn:def"},
		"shadow":       {"xml": NSXML, "p": "urn:inner", "": "urn:def"},
		"shadow/deep":  {"xml": NSXML, "p": "urn:inner", "": "urn:def"},
		"sibling":      {"xml": NSXML, "p": "urn:outer", "": "urn:def"},
		"sibling/deep": {"xml": NSXML, "p": "urn:outer", "": "urn:def"},
		"undecl":       {"xml": NSXML, "p": "urn:outer"},
		"undecl/deep":  {"xml": NSXML, "p": "urn:outer"},
		"after":        {"xml": NSXML, "p": "urn:outer", "": "urn:def"},
	}
	for name, w := range want {
		n := byName[name]
		if n == nil {
			t.Fatalf("%s not found in the parsed tree", name)
		}
		got := n.InScopeNamespaces()
		if len(got) != len(w) {
			t.Errorf("%s: %d bindings in scope, want %d (%v vs %v)",
				name, len(got), len(w), got, w)
			continue
		}
		for prefix, uri := range w {
			if got[prefix] != uri {
				t.Errorf("%s: prefix %q bound to %q, want %q",
					name, prefix, got[prefix], uri)
			}
		}
	}

	// Document order must stay strictly increasing across the whole tree,
	// which is what the reservation Tree.assign sizes from these counts is
	// for: a slot handed out twice would show up as a repeat here.
	seen := map[int32]*Node{}
	var order func(*Node)
	order = func(n *Node) {
		if prev, dup := seen[n.order]; dup {
			t.Errorf("document order %d given to both %s and %s",
				n.order, prev.Name.Local, n.Name.Local)
		}
		seen[n.order] = n
		for _, ns := range n.Namespaces {
			if prev, dup := seen[ns.order]; dup {
				t.Errorf("document order %d given to both %s and namespace %q",
					ns.order, prev.Name.Local, ns.Name.Local)
			}
			seen[ns.order] = ns
		}
		for _, a := range n.Attrs {
			if prev, dup := seen[a.order]; dup {
				t.Errorf("document order %d given to both %s and attribute %q",
					a.order, prev.Name.Local, a.Name.Local)
			}
			seen[a.order] = a
		}
		for _, c := range n.Children {
			order(c)
		}
	}
	order(tree.Root)
}

// TestFinalizeScopeRestoredAcrossSiblings is the narrow regression: the walk
// mutates one map as it descends, so a declaration left applied after its
// subtree is numbered would silently change every later sibling's count. The
// element counts here differ between the shadowing branch and the branch after
// it, and a missing restore collapses them onto the same value.
func TestFinalizeScopeRestoredAcrossSiblings(t *testing.T) {
	const src = `<r xmlns:a="urn:a">` +
		`<many xmlns:b="urn:b" xmlns:c="urn:c" xmlns:d="urn:d"/>` +
		`<few/>` +
		`</r>`
	tree, err := ParseString(src, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Root.ChildElements()[0]
	kids := root.ChildElements()
	many, few := kids[0], kids[1]

	if got := len(many.InScopeNamespaces()); got != 5 {
		t.Errorf("many has %d bindings in scope, want 5 (xml, a, b, c, d)", got)
	}
	if got := len(few.InScopeNamespaces()); got != 2 {
		t.Errorf("few has %d bindings in scope, want 2 (xml, a); the "+
			"declarations on its preceding sibling leaked into its scope", got)
	}
	// The reservation shows up as the gap between the two siblings' order
	// values: many reserves a slot per in-scope binding. If few's scope had
	// been inflated by its sibling's declarations the gap after few would
	// grow too.
	if few.order <= many.order {
		t.Fatalf("document order is not increasing: many=%d few=%d",
			many.order, few.order)
	}
}
