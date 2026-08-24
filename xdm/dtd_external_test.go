package xdm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exist because external entity resolution is the XXE boundary.
// A conformance gain is not what they are for: they pin the four properties
// that make the feature admissible at all — that it is off unless asked for,
// that it cannot be reached through AllowDOCTYPE alone, that expansion stays
// bounded when it IS asked for, and that a system identifier cannot escape
// the resolver's roots. An untested security path rots silently, and this one
// is a file-read primitive.

// dirResolver reads entities from a directory tree, resolving relative system
// identifiers against the including resource. It stands in for the real
// xslt.FileResolver, which xdm cannot import.
type dirResolver struct {
	root    string
	fetched []string
}

func (d *dirResolver) ResolveEntity(sys, pub, base string) (io.ReadCloser, string, error) {
	dir := d.root
	if base != "" {
		dir = filepath.Dir(strings.TrimPrefix(base, "file://"))
	}
	p := filepath.Join(dir, sys)
	// The containment check the real resolver performs, in miniature: a
	// system identifier that escapes the root is refused before the file is
	// opened.
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(d.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("%q is outside the permitted root", abs)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, "", err
	}
	d.fetched = append(d.fetched, sys)
	return f, "file://" + abs, nil
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// >>> PROPERTY 1: OFF BY DEFAULT, AND NOT IMPLIED BY AllowDOCTYPE. <<<
//
// This is the property that keeps every existing caller safe without a code
// change. AllowDOCTYPE admits a DOCTYPE and its internal declarations; it must
// not admit reads of other files. If this test ever fails, every caller in the
// repository that sets AllowDOCTYPE has silently gained a file-read primitive.
func TestExternalEntityStillRefusedWithAllowDOCTYPEAlone(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"secret.xml": `<secret>leaked</secret>`,
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "secret.xml"> ]>
<r>&e;</r>`,
	})
	src, err := os.ReadFile(filepath.Join(dir, "doc.xml"))
	if err != nil {
		t.Fatal(err)
	}
	// AllowDOCTYPE on, no resolver: the reference must fail, and above all
	// the file's contents must not appear in the tree.
	tree, err := ParseString(string(src), ParseOptions{
		AllowDOCTYPE: true,
		BaseURI:      "file://" + filepath.Join(dir, "doc.xml"),
	})
	if err == nil {
		if got := tree.Root.StringValue(); strings.Contains(got, "leaked") {
			t.Fatalf("AllowDOCTYPE alone read an external entity: %q", got)
		}
	}
}

// The same document with a resolver supplied resolves, which is what makes
// the test above a statement about the GATE rather than about a parse that
// fails for some unrelated reason.
func TestExternalEntityResolvesWhenPermitted(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"frag.xml": `<frag>text</frag>`,
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "frag.xml"> ]>
<r>&e;</r>`,
	})
	tree := mustParseExternal(t, dir, "doc.xml")
	if got := tree.Root.StringValue(); !strings.Contains(got, "text") {
		t.Fatalf("external entity not expanded: %q", got)
	}
	// The replacement text is parsed as MARKUP, not delivered as characters:
	// an entity is a way to factor out a fragment.
	if n := len(tree.Root.Children[0].Children); n != 1 {
		t.Fatalf("want one child element from the entity, got %d", n)
	}
	if got := tree.Root.Children[0].Children[0].Name.Local; got != "frag" {
		t.Fatalf("entity text was not parsed as markup: got %q", got)
	}
}

func mustParseExternal(t *testing.T, dir, name string) *Tree {
	t.Helper()
	p := filepath.Join(dir, name)
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := ParseString(string(src), ParseOptions{
		AllowDOCTYPE:     true,
		ExternalEntities: &dirResolver{root: dir},
		BaseURI:          "file://" + p,
	})
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return tree
}

func parseExternalErr(t *testing.T, dir, name string) error {
	t.Helper()
	p := filepath.Join(dir, name)
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseString(string(src), ParseOptions{
		AllowDOCTYPE:     true,
		ExternalEntities: &dirResolver{root: dir},
		BaseURI:          "file://" + p,
	})
	return err
}

// >>> PROPERTY 3: BOUNDED EXPANSION. A bomb must fail closed. <<<
//
// The classic billion-laughs, assembled out of EXTERNAL files so that it
// exercises the fetch path rather than the internal expander. Each file is
// tiny; the expansion is exponential. The parse must return an error rather
// than exhaust memory, and the test would hang rather than fail if the bound
// were missing — which is the honest failure mode for this class of bug.
func TestExternalEntityBombIsRefused(t *testing.T) {
	files := map[string]string{"e0.ent": strings.Repeat("A", 512)}
	// Ten levels, each referencing the level below ten times: 512 * 10^10
	// bytes if it were ever allowed to complete.
	for i := 1; i <= 10; i++ {
		files[fmt.Sprintf("e%d.ent", i)] = strings.Repeat(
			fmt.Sprintf("&e%d;", i-1), 10)
	}
	var decls strings.Builder
	for i := 0; i <= 10; i++ {
		fmt.Fprintf(&decls, "<!ENTITY e%d SYSTEM \"e%d.ent\">\n", i, i)
	}
	files["doc.xml"] = "<!DOCTYPE r [\n" + decls.String() + "]>\n<r>&e10;</r>"

	dir := writeFiles(t, files)
	err := parseExternalErr(t, dir, "doc.xml")
	if err == nil {
		t.Fatal("an external billion-laughs was accepted; expansion is unbounded")
	}
	t.Logf("refused as expected: %v", err)
}

// The same shape through PARAMETER entities, which is a separate code path:
// the subset is textually substituted and re-scanned, so an unbounded chain
// there would blow up before any content is parsed.
func TestExternalParameterEntityBombIsRefused(t *testing.T) {
	files := map[string]string{}
	// Each module declares the next and references it, twenty deep — past
	// maxExternalDepth, so the chain must be cut.
	for i := 0; i < 20; i++ {
		files[fmt.Sprintf("p%d.ent", i)] = fmt.Sprintf(
			`<!ENTITY %% p%d SYSTEM "p%d.ent">%%p%d;`, i+1, i+1, i+1)
	}
	files["p20.ent"] = `<!ENTITY final "x">`
	files["doc.xml"] = `<!DOCTYPE r [
<!ENTITY % p0 SYSTEM "p0.ent">%p0;
]>
<r>ok</r>`
	dir := writeFiles(t, files)
	if err := parseExternalErr(t, dir, "doc.xml"); err == nil {
		t.Fatal("an unbounded parameter-entity chain was accepted")
	}
}

// A single external entity larger than the expansion budget is refused on the
// strength of its own length, before it is expanded. The charge-before-expand
// ordering is what this pins: without it a 2 MB file would be read and
// expanded and only then measured.
func TestOversizeExternalEntityIsRefused(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"big.ent": strings.Repeat("A", maxExternalFetchBytes+1024),
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "big.ent"> ]>
<r>&e;</r>`,
	})
	if err := parseExternalErr(t, dir, "doc.xml"); err == nil {
		t.Fatal("an oversize external entity was accepted")
	}
}

// Many small external entities, each individually under the per-entity cap,
// must still trip the shared document budget. A bomb divided among files is
// the way around a per-entity limit, and the shared total is the answer to it.
func TestManySmallExternalEntitiesTripTheSharedBudget(t *testing.T) {
	files := map[string]string{}
	var decls, refs strings.Builder
	const each = 60 << 10
	for i := 0; i < 40; i++ {
		files[fmt.Sprintf("f%d.ent", i)] = strings.Repeat("A", each)
		fmt.Fprintf(&decls, "<!ENTITY e%d SYSTEM \"f%d.ent\">\n", i, i)
		fmt.Fprintf(&refs, "&e%d;", i)
	}
	files["doc.xml"] = "<!DOCTYPE r [\n" + decls.String() + "]>\n<r>" + refs.String() + "</r>"
	dir := writeFiles(t, files)
	if err := parseExternalErr(t, dir, "doc.xml"); err == nil {
		t.Fatal("40 x 60KB of external entities was accepted; " +
			"the shared budget is not being charged")
	}
}

// The number of fetches is bounded independently of their size, so a document
// naming thousands of tiny files cannot turn one parse into thousands of I/O
// operations.
func TestExternalFetchCountIsBounded(t *testing.T) {
	files := map[string]string{}
	var decls, refs strings.Builder
	for i := 0; i < maxExternalFetches+40; i++ {
		files[fmt.Sprintf("f%d.ent", i)] = "x"
		fmt.Fprintf(&decls, "<!ENTITY e%d SYSTEM \"f%d.ent\">\n", i, i)
		fmt.Fprintf(&refs, "&e%d;", i)
	}
	files["doc.xml"] = "<!DOCTYPE r [\n" + decls.String() + "]>\n<r>" + refs.String() + "</r>"
	dir := writeFiles(t, files)
	if err := parseExternalErr(t, dir, "doc.xml"); err == nil {
		t.Fatalf("more than %d external fetches were accepted", maxExternalFetches)
	}
}

// An entity that refers to itself through the external path must be an error
// rather than an unbounded chain of fetches.
func TestSelfReferentialExternalEntityIsRefused(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"loop.ent": `&e;`,
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "loop.ent"> ]>
<r>&e;</r>`,
	})
	if err := parseExternalErr(t, dir, "doc.xml"); err == nil {
		t.Fatal("a self-referential external entity was accepted")
	}
}

// >>> PROPERTY 4: NO PATH ESCAPE. <<<
//
// A system identifier that climbs out of the permitted root must be refused,
// and the file must not reach the tree. The refusal is the resolver's — xdm
// never constructs a path — so what this pins is that xdm ASKS the resolver
// and honours its answer rather than falling back to some other route.
func TestSystemIdentifierOutsideRootIsRefused(t *testing.T) {
	outer := t.TempDir()
	if err := os.WriteFile(filepath.Join(outer, "secret.txt"), []byte("leaked"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "docs")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `<!DOCTYPE r [ <!ENTITY e SYSTEM "../secret.txt"> ]>
<r>&e;</r>`
	p := filepath.Join(inner, "doc.xml")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := ParseString(doc, ParseOptions{
		AllowDOCTYPE:     true,
		ExternalEntities: &dirResolver{root: inner},
		BaseURI:          "file://" + p,
	})
	if err == nil {
		if got := tree.Root.StringValue(); strings.Contains(got, "leaked") {
			t.Fatalf("a path outside the root was read: %q", got)
		}
		t.Fatal("a path outside the root did not produce an error")
	}
	if !strings.Contains(err.Error(), "outside the permitted root") {
		t.Fatalf("refused, but not by the containment check: %v", err)
	}
}

// The same escape through an external DTD subset, which is a different code
// path from a general entity and reaches the resolver from a different place.
func TestExternalSubsetOutsideRootIsRefused(t *testing.T) {
	outer := t.TempDir()
	if err := os.WriteFile(filepath.Join(outer, "evil.dtd"),
		[]byte(`<!ENTITY e "leaked">`), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "docs")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "<!DOCTYPE r SYSTEM \"../evil.dtd\">\n<r>ok</r>"
	p := filepath.Join(inner, "doc.xml")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseString(doc, ParseOptions{
		AllowDOCTYPE:     true,
		ExternalEntities: &dirResolver{root: inner},
		BaseURI:          "file://" + p,
	})
	if err == nil {
		t.Fatal("an external subset outside the root was accepted")
	}
	if !strings.Contains(err.Error(), "outside the permitted root") {
		t.Fatalf("refused, but not by the containment check: %v", err)
	}
}

// A resolver that returns an error for everything is the deny-by-default
// posture a cautious caller wants, and it must produce a clean parse failure
// rather than a panic or a silent skip.
func TestResolverRefusalIsHonoured(t *testing.T) {
	src := `<!DOCTYPE r [ <!ENTITY e SYSTEM "x.ent"> ]>
<r>&e;</r>`
	_, err := ParseString(src, ParseOptions{
		AllowDOCTYPE:     true,
		ExternalEntities: refusingResolver{},
	})
	if err == nil {
		t.Fatal("a refusing resolver did not stop the entity")
	}
}

type refusingResolver struct{}

func (refusingResolver) ResolveEntity(sys, pub, base string) (io.ReadCloser, string, error) {
	return nil, "", fmt.Errorf("denied")
}

// Correctness, not security: the declarations an external DTD subset makes
// must be visible in the document, and the internal subset must win where
// both declare a name — XML section 4.2's first-declaration-wins rule applied
// across the two subsets.
func TestExternalSubsetSuppliesEntities(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"d.dtd": `<!ENTITY greeting "from-external">
<!ENTITY other "external-only">`,
		"doc.xml": `<!DOCTYPE r SYSTEM "d.dtd" [ <!ENTITY greeting "from-internal"> ]>
<r>&greeting;|&other;</r>`,
	})
	tree := mustParseExternal(t, dir, "doc.xml")
	if got, want := tree.Root.StringValue(), "from-internal|external-only"; got != want {
		t.Fatalf("subset precedence wrong: got %q want %q", got, want)
	}
}

// A relative system identifier inside an external subset resolves against the
// SUBSET's URI, not the document's — XML section 4.4.3. Getting this wrong
// makes a modular DTD resolve against the wrong directory, and it is only
// visible when the two directories differ, as they do here.
func TestExternalSubsetResolvesRelativeToItself(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"sub/d.dtd":    `<!ENTITY % more SYSTEM "more.ent">%more;`,
		"sub/more.ent": `<!ENTITY v "found">`,
		"doc.xml": `<!DOCTYPE r SYSTEM "sub/d.dtd">
<r>&v;</r>`,
	})
	tree := mustParseExternal(t, dir, "doc.xml")
	if got := tree.Root.StringValue(); got != "found" {
		t.Fatalf("nested subset did not resolve against its own base: %q", got)
	}
}

// An external entity's text declaration is not part of its replacement text
// (XML section 4.3.1). Left in, it reaches the including document as a
// processing instruction in a position no XML document may have one.
func TestTextDeclarationIsStripped(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"frag.xml": `<?xml version="1.0" encoding="UTF-8"?><frag/>`,
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "frag.xml"> ]>
<r>&e;</r>`,
	})
	tree := mustParseExternal(t, dir, "doc.xml")
	for _, c := range tree.Root.Children[0].Children {
		if c.Kind == KindPI {
			t.Fatalf("text declaration survived as a PI: %q", c.Value)
		}
	}
}
