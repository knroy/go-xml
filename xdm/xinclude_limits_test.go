package xdm

import (
	"fmt"
	"strings"
	"testing"
)

// The loop rule and the two resource bounds are different failures, and a
// caller has to be able to tell them apart: a loop is a defect in the
// document, which no amount of patience would fix, while a bound is this
// processor declining to spend more on a document that is merely large.
//
// The loop rule is already the URI stack — an inclusion whose URI is one of
// its own ancestors — and these tests hold that apart from the bounds rather
// than letting either stand in for the other.

// A chain of inclusions longer than the nesting bound repeats no URI: every
// link is a distinct file and the far end terminates. It is refused, but as a
// resource limit, and the message must not call it a loop.
func TestXIncludeDeepAcyclicChainIsAResourceLimit(t *testing.T) {
	files := map[string]string{}
	const chain = maxIncludeDepth + 1
	for i := 0; i < chain; i++ {
		files[fmt.Sprintf("mem:///d/g%d.xml", i)] = fmt.Sprintf(
			`<g`+xiNS+`><xi:include href="g%d.xml"/></g>`, i+1)
	}
	files[fmt.Sprintf("mem:///d/g%d.xml", chain)] = `<leaf/>`

	_, _, err := run(t, "mem:///d/main.xml",
		`<root`+xiNS+`><xi:include href="g0.xml"/></root>`, files)
	if err == nil {
		t.Fatal("a chain past the nesting bound should be refused")
	}
	if !strings.Contains(err.Error(), "resource limit exceeded") {
		t.Errorf("error = %v, want one naming a resource limit", err)
	}
	if strings.Contains(err.Error(), "circular") || strings.Contains(err.Error(), "loop") {
		t.Errorf("an acyclic chain must not be reported as a loop: %v", err)
	}
}

// The other side of the same bound: a chain that fits must be included, so
// that the ceiling is a ceiling and not a suspicion of depth as such.
func TestXIncludeChainInsideTheBoundSucceeds(t *testing.T) {
	files := map[string]string{}
	const chain = maxIncludeDepth - 2
	for i := 0; i < chain; i++ {
		files[fmt.Sprintf("mem:///d/g%d.xml", i)] = fmt.Sprintf(
			`<g`+xiNS+`><xi:include href="g%d.xml"/></g>`, i+1)
	}
	files[fmt.Sprintf("mem:///d/g%d.xml", chain)] = `<leaf/>`

	tree, _, err := run(t, "mem:///d/main.xml",
		`<root`+xiNS+`><xi:include href="g0.xml"/></root>`, files)
	if err != nil {
		t.Fatalf("a legal %d-deep chain was refused: %v", chain, err)
	}
	if n := countElements(tree.Root, "leaf"); n != 1 {
		t.Errorf("found %d <leaf> elements at the end of the chain, want 1", n)
	}
}

// A direct self-inclusion is circular at depth one, and must be reported as
// circular there rather than surviving 40 levels to be called a depth
// overrun. This is the case a depth counter alone gets wrong twice: it takes
// forty reads to notice, and then names the wrong resource.
func TestXIncludeDirectSelfIncludeIsCircularAtDepthOne(t *testing.T) {
	_, res, err := run(t, "mem:///d/main.xml",
		`<root`+xiNS+`><xi:include href="a.xml"/></root>`,
		map[string]string{
			"mem:///d/a.xml": `<a` + xiNS + `><xi:include href="a.xml"/></a>`,
		})
	if err == nil {
		t.Fatal("a self-including resource should be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one saying circular", err)
	}
	if !strings.Contains(err.Error(), "a.xml") {
		t.Errorf("error = %v, should name the URI that closed the loop", err)
	}
	if strings.Contains(err.Error(), "resource limit") {
		t.Errorf("a loop must not be reported as a resource limit: %v", err)
	}
	// Caught on the path, not by exhausting a budget.
	if len(res.reads) > 2 {
		t.Errorf("the resolver was consulted %d times (%v); a loop of one "+
			"should be caught at once", len(res.reads), res.reads)
	}
}

// A -> B -> C -> A. Nothing repeats until the last link, so this is what an
// active-path check buys over comparing against the immediate parent.
func TestXIncludeIndirectCycleIsCircular(t *testing.T) {
	_, res, err := run(t, "mem:///d/main.xml",
		`<root`+xiNS+`><xi:include href="a.xml"/></root>`,
		map[string]string{
			"mem:///d/a.xml": `<a` + xiNS + `><xi:include href="b.xml"/></a>`,
			"mem:///d/b.xml": `<b` + xiNS + `><xi:include href="c.xml"/></b>`,
			"mem:///d/c.xml": `<c` + xiNS + `><xi:include href="a.xml"/></c>`,
		})
	if err == nil {
		t.Fatal("an A->B->C->A cycle should be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one saying circular", err)
	}
	if strings.Contains(err.Error(), "resource limit") {
		t.Errorf("a loop must not be reported as a resource limit: %v", err)
	}
	if len(res.reads) > 4 {
		t.Errorf("the resolver was consulted %d times (%v); a cycle of three "+
			"should stop on the fourth", len(res.reads), res.reads)
	}
}

// The cycle written through two spellings of one file. c names b as
// "../d/b.xml" while b was itself reached as "b.xml" — different references
// naming one resource. The stack is keyed on the URI the resolver reports, so
// the loop closes at b, which is the resource that is genuinely its own
// ancestor.
func TestXIncludeCycleThroughDifferentRelativePathsIsCaught(t *testing.T) {
	_, res, err := run(t, "mem:///d/main.xml",
		`<root`+xiNS+`><xi:include href="b.xml"/></root>`,
		map[string]string{
			"mem:///d/b.xml": `<b` + xiNS + `><xi:include href="c.xml"/></b>`,
			"mem:///d/c.xml": `<c` + xiNS + `><xi:include href="../d/b.xml"/></c>`,
		})
	if err == nil {
		t.Fatal("a cycle spelled through .. should still be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one saying circular", err)
	}
	// Naming b rather than c is what shows the key is the resolved URI: on
	// the references as written, no string on the path repeats.
	if !strings.Contains(err.Error(), "/d/b.xml") {
		t.Errorf("error = %v, should name /d/b.xml as the resource that is "+
			"its own ancestor", err)
	}
	if len(res.reads) != 3 {
		t.Errorf("the resolver was consulted %d times (%v); want 3 — b, c, "+
			"and the return to b that closes the loop", len(res.reads), res.reads)
	}
}

// A diamond is legal. main includes b and c; both include d. d is reached
// twice by two routes and is an ancestor of neither, so a global "already
// included" set would wrongly refuse this. The stack is popped on the way out,
// which is what makes it an active path rather than a visited set.
func TestXIncludeDiamondIsLegal(t *testing.T) {
	tree, _, err := run(t, "mem:///d/main.xml",
		`<root`+xiNS+`><xi:include href="b.xml"/><xi:include href="c.xml"/></root>`,
		map[string]string{
			"mem:///d/b.xml": `<b` + xiNS + `><xi:include href="d.xml"/></b>`,
			"mem:///d/c.xml": `<c` + xiNS + `><xi:include href="d.xml"/></c>`,
			"mem:///d/d.xml": `<leaf/>`,
		})
	if err != nil {
		t.Fatalf("a diamond of inclusions is legal and was refused: %v", err)
	}
	// Included twice, because it is referenced twice: the resource appears
	// wherever it is named. Refusing the second is the bug this guards.
	if n := countElements(tree.Root, "leaf"); n != 2 {
		t.Errorf("found %d <leaf> elements, want 2 — one per route", n)
	}
}

// countElements counts descendant elements with the given local name.
func countElements(n *Node, local string) int {
	count := 0
	if n.Kind == KindElement && n.Name.Local == local {
		count++
	}
	for _, c := range n.Children {
		count += countElements(c, local)
	}
	return count
}
