package relaxng

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Cycle detection and the resource bound on <include> are two different
// answers to two different questions, and these tests hold them apart.
//
// A depth counter cannot tell a cycle from a long chain: it reports both as
// the same failure, at whichever href happened to be 40 levels down rather
// than at the one that closed the loop. So a cycle is detected by the set of
// hrefs on the *active path* — a schema that is its own ancestor — and the
// depth bound is left to do only what it is for, which is to stop a legal but
// expensive chain of fetches.

// A chain of includes longer than the bound is not circular, and must not be
// described as though it were. It is refused, but for a reason a caller can
// act on differently: the schema is fine, this processor will not spend that
// much on it.
func TestIncludeChainOverBoundIsAResourceLimitNotACycle(t *testing.T) {
	docs := map[string]string{}
	const chain = maxIncludeDepth + 1
	for i := 0; i < chain; i++ {
		docs[fmt.Sprintf("g%d.rng", i)] = fmt.Sprintf(
			`<grammar`+rngNS+`><include href="g%d.rng"/></grammar>`, i+1)
	}
	// The far end terminates, so nothing here is a loop.
	docs[fmt.Sprintf("g%d.rng", chain)] = `<grammar` + rngNS + `>
		<start><element name="doc"><empty/></element></start></grammar>`

	_, err := compileWith(t, `<grammar`+rngNS+`><include href="g0.rng"/></grammar>`,
		Options{Resolver: &mapResolver{docs: docs}})
	if err == nil {
		t.Fatal("a chain past the bound should be refused")
	}
	if !strings.Contains(err.Error(), "resource limit exceeded") {
		t.Errorf("error = %v, want one naming a resource limit", err)
	}
	if strings.Contains(err.Error(), "circular") {
		t.Errorf("an acyclic chain must not be reported as circular: %v", err)
	}
}

// An acyclic chain that fits inside the bound must compile. This is the other
// half of the test above: the bound is a ceiling, not a suspicion.
func TestLegalDeepIncludeChainCompiles(t *testing.T) {
	docs := map[string]string{}
	const chain = maxIncludeDepth - 2
	for i := 0; i < chain; i++ {
		docs[fmt.Sprintf("g%d.rng", i)] = fmt.Sprintf(
			`<grammar`+rngNS+`><include href="g%d.rng"/></grammar>`, i+1)
	}
	docs[fmt.Sprintf("g%d.rng", chain)] = `<grammar` + rngNS + `>
		<start><element name="doc"><empty/></element></start></grammar>`

	s, err := compileWith(t, `<grammar`+rngNS+`><include href="g0.rng"/></grammar>`,
		Options{Resolver: &mapResolver{docs: docs}})
	if err != nil {
		t.Fatalf("a legal %d-deep chain was refused: %v", chain, err)
	}
	doc, err := xdm.ParseString(`<doc/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("the schema at the end of the chain should apply: %v", err)
	}
}

// A schema that includes itself is circular at depth one. It must be caught
// there, on its own terms, rather than 40 fetches later as a depth overrun.
func TestDirectSelfIncludeIsACycleAtDepthOne(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"a.rng": `<grammar` + rngNS + `><include href="a.rng"/></grammar>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`><include href="a.rng"/></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("a self-include should be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one saying circular", err)
	}
	if !strings.Contains(err.Error(), "a.rng") {
		t.Errorf("error = %v, should name the href that closed the loop", err)
	}
	if strings.Contains(err.Error(), "resource limit") {
		t.Errorf("a cycle must not be reported as a resource limit: %v", err)
	}
	// Caught on the path rather than by exhausting a budget: the loop is
	// entered once, not forty times.
	if len(r.seen) > 2 {
		t.Errorf("the resolver was consulted %d times (%v); a cycle of one "+
			"should be caught immediately", len(r.seen), r.seen)
	}
}

// The indirect case. Nothing repeats until the third link closes the loop, so
// this is what distinguishes an active-path set from a check against the
// immediate parent alone.
func TestIndirectIncludeCycleIsReportedAsCircular(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"a.rng": `<grammar` + rngNS + `><include href="b.rng"/></grammar>`,
		"b.rng": `<grammar` + rngNS + `><include href="c.rng"/></grammar>`,
		"c.rng": `<grammar` + rngNS + `><include href="a.rng"/></grammar>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`><include href="a.rng"/></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("an A->B->C->A cycle should be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one saying circular", err)
	}
	if strings.Contains(err.Error(), "resource limit") {
		t.Errorf("a cycle must not be reported as a resource limit: %v", err)
	}
	if len(r.seen) > 4 {
		t.Errorf("the resolver was consulted %d times (%v); a cycle of three "+
			"should stop on the fourth", len(r.seen), r.seen)
	}
}

// A cycle through an <externalRef> is the same cycle. externalRef compiles in
// a compiler of its own, so this is what proves the active path is shared
// across that boundary rather than restarted.
func TestCycleThroughExternalRefIsCircular(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"a.rng": `<grammar` + rngNS + `>
			<start><element name="doc"><externalRef href="b.rng"/></element></start>
			</grammar>`,
		"b.rng": `<element` + rngNS + ` name="inner"><externalRef href="a.rng"/></element>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`>
		<start><externalRef href="a.rng"/></start></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("a cycle through externalRef should be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one saying circular", err)
	}
}

// A diamond is not a cycle. A includes B and C; both include D. D is reached
// twice, by two routes, and neither is an ancestor of the other — so a global
// "already seen" set would wrongly refuse this, and an active-path set must
// accept it. This test is what pins the choice of algorithm.
func TestDiamondIncludeIsLegal(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"b.rng": `<grammar` + rngNS + `><include href="d.rng"/></grammar>`,
		"c.rng": `<grammar` + rngNS + `><include href="d.rng"/></grammar>`,
		// D contributes a definition rather than a <start>: it arrives twice,
		// and section 4.17 forbids two <start>s without combine=. The
		// definition is combined, which is what makes the diamond useful.
		"d.rng": `<grammar` + rngNS + `>
			<define name="leaf" combine="choice">
				<element name="leaf"><empty/></element></define></grammar>`,
	}}
	s, err := compileWith(t, `<grammar`+rngNS+`>
		<include href="b.rng"/><include href="c.rng"/>
		<start><element name="doc"><ref name="leaf"/></element></start>
		</grammar>`,
		Options{Resolver: r})
	if err != nil {
		t.Fatalf("a diamond of includes is legal and was refused: %v", err)
	}
	doc, err := xdm.ParseString(`<doc><leaf/></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("the shared definition should apply: %v", err)
	}
}

// A sibling pair of externalRefs to one schema is the same shape as the
// diamond, on the path that builds a compiler of its own. It must compile,
// which is what makes removing the href from the active path on the way out
// load-bearing rather than decorative.
func TestRepeatedExternalRefToOneSchemaIsLegal(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"leaf.rng": `<element` + rngNS + ` name="leaf"><empty/></element>`,
	}}
	s, err := compileWith(t, `<grammar`+rngNS+`>
		<start><element name="doc">
			<externalRef href="leaf.rng"/><externalRef href="leaf.rng"/>
		</element></start></grammar>`,
		Options{Resolver: r})
	if err != nil {
		t.Fatalf("two references to one schema are legal and were refused: %v", err)
	}
	doc, err := xdm.ParseString(`<doc><leaf/><leaf/></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("both references should apply: %v", err)
	}
}

// fileResolver reads schemas from disk, resolving each href to an absolute
// path. It exists so that a cycle can be written through *different* relative
// spellings of one file, which an in-memory map keyed on the href as written
// cannot express.
type fileResolver struct {
	root  string
	reads []string
}

func (r *fileResolver) ResolveSchema(href string) (*xdm.Node, error) {
	// The href arrives already resolved against the base in force, so it is
	// a path relative to root — possibly with ".." in it, which is exactly
	// what makes two spellings name one file.
	p := filepath.Join(r.root, filepath.FromSlash(href))
	r.reads = append(r.reads, p)
	if len(r.reads) > 200 {
		return nil, fmt.Errorf("runaway: read %d schemas", len(r.reads))
	}
	src, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	tree, err := xdm.ParseString(string(src), xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	return tree.Root, nil
}

// The normalisation test. sub/b.rng includes "../sub/b.rng", which is the file
// it is written in — but spelled differently from the "sub/b.rng" that reached
// it. Keying the active path on the href as written would see two distinct
// strings and miss the loop; keying it on the resolved reference sees one.
func TestCycleThroughDifferentRelativeSpellingsIsCaught(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)),
			[]byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The loop is two links long and each link spells the other file
	// differently from how it was itself reached. b names c as "c.rng";
	// c names b back as "../sub/b.rng". Resolved, those are "sub/c.rng" and
	// "sub/b.rng" — and "sub/b.rng" is already on the path, so the loop
	// closes. Compared as written, no href on the path ever repeats: the
	// outer schema said "sub/b.rng" and c says "../sub/b.rng", which are
	// different strings naming one file.
	write("sub/b.rng", `<grammar`+rngNS+`><include href="c.rng"/></grammar>`)
	write("sub/c.rng", `<grammar`+rngNS+`><include href="../sub/b.rng"/></grammar>`)

	r := &fileResolver{root: root}
	_, err := compileWith(t, `<grammar`+rngNS+`><include href="sub/b.rng"/></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("a cycle spelled through .. should still be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one saying circular", err)
	}
	// The href the message names is what proves the key is the resolved
	// reference. b is the schema that is its own ancestor: the path is
	// b -> c -> b, and the second arrival at b is what closes the loop.
	//
	// Keyed on the href as *written*, the path would hold "sub/b.rng" and
	// "c.rng" — neither of which equals the "../sub/b.rng" that c writes —
	// so the second read of b would be allowed, and the loop would only be
	// noticed on the next lap, blaming "sub/c.rng" for including itself.
	// That is a true refusal of a false statement: c does not include c.
	if !strings.Contains(err.Error(), "sub/b.rng") {
		t.Errorf("error = %v, should name sub/b.rng as the schema that is "+
			"its own ancestor; naming another href means the active path is "+
			"keyed on the raw reference rather than the resolved one", err)
	}
	// And it closes on the first repeat rather than a lap later: b, c, and
	// then the reference back to b, which is refused before it is read.
	if len(r.reads) != 2 {
		t.Errorf("the resolver read %d schemas (%v); want 2 — b and c, with "+
			"the return to b refused before the read", len(r.reads), r.reads)
	}
}

// The same shape, acyclic: two spellings of one file reached from two places
// that are not ancestors of each other. It must compile, so that keying on the
// resolved form does not turn every repeat into a refusal.
func TestTwoSpellingsOfOneSchemaInADiamondCompile(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)),
			[]byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sub/b.rng", `<grammar`+rngNS+`><include href="d.rng"/></grammar>`)
	write("sub/c.rng", `<grammar`+rngNS+`><include href="../sub/d.rng"/></grammar>`)
	write("sub/d.rng", `<grammar`+rngNS+`>
		<define name="leaf" combine="choice">
			<element name="leaf"><empty/></element></define></grammar>`)

	r := &fileResolver{root: root}
	s, err := compileWith(t, `<grammar`+rngNS+`>
		<include href="sub/b.rng"/><include href="sub/c.rng"/>
		<start><element name="doc"><ref name="leaf"/></element></start>
		</grammar>`,
		Options{Resolver: r})
	if err != nil {
		t.Fatalf("one schema reached twice by two spellings is legal: %v", err)
	}
	doc, err := xdm.ParseString(`<doc><leaf/></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Errorf("the shared definition should apply: %v", err)
	}
}

// A schema that includes the file it was itself loaded from is circular, and
// the caller told us where that was. Without seeding the active path with
// Options.BaseURI the loop would only be noticed one level further down, and
// would name the wrong href.
func TestSchemaIncludingItsOwnBaseURIIsCircular(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"main.rng": `<grammar` + rngNS + `><include href="main.rng"/></grammar>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`><include href="main.rng"/></grammar>`,
		Options{Resolver: r, BaseURI: "main.rng"})
	if err == nil {
		t.Fatal("a schema including itself by its own base URI should be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error = %v, want one saying circular", err)
	}
	if len(r.seen) != 0 {
		t.Errorf("the resolver was consulted with %v; the loop is visible "+
			"before any read", r.seen)
	}
}
