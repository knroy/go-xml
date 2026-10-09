package xdm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/knroy/go-xml/v2/internal/fileuri"
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
		dir = filepath.Dir(fileuri.ToPath(base))
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
	return f, fileuri.Of(abs), nil
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
		BaseURI:      fileuri.Of(filepath.Join(dir, "doc.xml")),
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
	if n := len(tree.Root.children[0].children); n != 1 {
		t.Fatalf("want one child element from the entity, got %d", n)
	}
	if got := tree.Root.children[0].children[0].name.Local; got != "frag" {
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
		BaseURI:          fileuri.Of(p),
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
		BaseURI:          fileuri.Of(p),
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
		BaseURI:          fileuri.Of(p),
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
		BaseURI:          fileuri.Of(p),
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
	for _, c := range tree.Root.children[0].children {
		if c.kind == KindPI {
			t.Fatalf("text declaration survived as a PI: %q", c.value)
		}
	}
}

// A node written inside an external parsed entity takes its base URI from
// that entity, not from the document that referenced it. XML Base section 4.2
// and the XDM base-uri accessor both say so, and the XSLT suite asserts it
// directly in resolve-uri-021: a processing instruction and an element pulled
// in from two different files each report their own.
func TestExternalEntityGivesItsNodesItsOwnBaseURI(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"level1/element.xml": `<extEnt attr="x"><sub2>z</sub2></extEnt>`,
		"level1/pi.xml":      `<?pi data?>`,
		"doc.xml": `<!DOCTYPE test [
  <!ENTITY extElement SYSTEM "level1/element.xml">
  <!ENTITY extPI SYSTEM "level1/pi.xml">
]>
<test>&extElement;&extPI;</test>`,
	})
	tree := mustParseExternal(t, dir, "doc.xml")
	root := tree.Root.ChildElements()[0]

	el := root.ChildElements()[0]
	if !strings.HasSuffix(el.baseURI, "level1/element.xml") {
		t.Errorf("element base URI = %q, want it to end in level1/element.xml",
			el.baseURI)
	}
	// The base travels down: a descendant inherits from the element it was
	// written under, which is inside the same entity.
	if kid := el.ChildElements()[0]; kid.baseURI != el.baseURI {
		t.Errorf("child base URI = %q, want %q", kid.baseURI, el.baseURI)
	}

	var pi *Node
	for _, c := range root.children {
		if c.kind == KindPI {
			pi = c
		}
	}
	if pi == nil {
		t.Fatal("no processing instruction in the tree")
	}
	if !strings.HasSuffix(pi.baseURI, "level1/pi.xml") {
		t.Errorf("PI base URI = %q, want it to end in level1/pi.xml", pi.baseURI)
	}

	// And the document's own nodes are unaffected: the rule is per entity,
	// not a blanket override.
	if !strings.HasSuffix(root.baseURI, "doc.xml") {
		t.Errorf("document element base URI = %q, want it to end in doc.xml",
			root.baseURI)
	}
}

// xml:base inside an external entity resolves against the ENTITY's URI, which
// is the base in force where the attribute was written — not against the
// including document's.
func TestXMLBaseInsideExternalEntityResolvesAgainstTheEntity(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"level1/frag.xml": `<frag xml:base="deeper/"><kid/></frag>`,
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "level1/frag.xml"> ]>
<r>&e;</r>`,
	})
	tree := mustParseExternal(t, dir, "doc.xml")
	frag := tree.Root.ChildElements()[0].ChildElements()[0]
	if !strings.HasSuffix(frag.baseURI, "level1/deeper/") {
		t.Errorf("base URI = %q, want it to end in level1/deeper/", frag.baseURI)
	}
}

// >>> XML section 4.3.4. A 1.0 document may not include a 1.1 entity. <<<
//
// The rule is asymmetric, so the test is bidirectional: proving the refusal
// alone would be satisfied by a parser that refused every versioned entity,
// which is why the 1.1-includes-1.0 direction is asserted beside it.
//
// The asymmetry is not arbitrary. XML 1.1 widens what a name and a character
// may be, so text that is well-formed inside a 1.1 entity can be illegal in
// the 1.0 document including it; the reverse cannot happen.
func TestEntityVersionAgainstIncludingDocument(t *testing.T) {
	const frag11 = `<?xml version="1.1" encoding="UTF-8"?><frag>text</frag>`
	const frag10 = `<?xml version="1.0" encoding="UTF-8"?><frag>text</frag>`
	// No text declaration at all. Section 4.3.4 treats such an entity as 1.0,
	// so it is legal in both.
	const fragNone = `<frag>text</frag>`

	for _, c := range []struct {
		name    string
		docVer  string
		frag    string
		wantErr bool
	}{
		{"10 includes 11", `<?xml version="1.0"?>`, frag11, true},
		{"10 includes 10", `<?xml version="1.0"?>`, frag10, false},
		{"10 includes undeclared", `<?xml version="1.0"?>`, fragNone, false},
		{"11 includes 11", `<?xml version="1.1"?>`, frag11, false},
		{"11 includes 10", `<?xml version="1.1"?>`, frag10, false},
		// A document with no declaration is XML 1.0, and inherits 1.0's
		// restriction rather than escaping it.
		{"undeclared includes 11", ``, frag11, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{
				"frag.xml": c.frag,
				"doc.xml": c.docVer + `<!DOCTYPE r [ <!ENTITY e SYSTEM "frag.xml"> ]>
<r>&e;</r>`,
			})
			err := parseExternalErr(t, dir, "doc.xml")
			if c.wantErr {
				if err == nil {
					t.Fatal("want the include refused, got no error")
				}
				if !strings.Contains(err.Error(), "4.3.4") {
					t.Fatalf("refused for the wrong reason: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want the include permitted, got %v", err)
			}
		})
	}
}

// A version this implementation does not know is refused rather than assumed
// compatible: an entity declaring 2.0 is not a 1.0 entity merely because we
// cannot read it. Asserted under a 1.1 document so that the refusal cannot be
// the 4.3.4 rule firing by accident.
func TestEntityUnknownVersionIsRefused(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"frag.xml": `<?xml version="2.0"?><frag>text</frag>`,
		"doc.xml": `<?xml version="1.1"?><!DOCTYPE r [ <!ENTITY e SYSTEM "frag.xml"> ]>
<r>&e;</r>`,
	})
	err := parseExternalErr(t, dir, "doc.xml")
	if err == nil {
		t.Fatal("want an unknown entity version refused, got no error")
	}
	if !strings.Contains(err.Error(), "unsupported XML version") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

// utf16Bytes encodes s as UTF-16, optionally preceded by a byte order mark.
// The test inputs are generated rather than checked in, so no line-ending
// conversion on any OS can corrupt them.
func utf16Bytes(s string, bigEndian, bom bool) string {
	units := utf16.Encode([]rune(s))
	if bom {
		units = append([]uint16{0xFEFF}, units...)
	}
	b := make([]byte, 0, 2*len(units))
	for _, u := range units {
		if bigEndian {
			b = append(b, byte(u>>8), byte(u))
		} else {
			b = append(b, byte(u), byte(u>>8))
		}
	}
	return string(b)
}

// An external parsed entity carries its own encoding (XML §4.3.3), so a UTF-16
// entity included by a UTF-8 document is decoded as UTF-16. It was read as
// UTF-8 and failed with "invalid UTF-8" (W3C xmlconf valid-ext-sa-007, -008,
// -014, ext02, invalid-bo-1/2/4/5). The byte order mark is not part of the
// replacement text; a second one is a ZERO WIDTH NO-BREAK SPACE and stays.
func TestExternalEntityEncoding(t *testing.T) {
	cases := []struct {
		name, ent, want string
		wantErr         bool
	}{
		{"UTF-16LE with BOM", utf16Bytes("<f>caf\u00e9</f>", false, true), "caf\u00e9", false},
		{"UTF-16BE with BOM", utf16Bytes("<f>caf\u00e9</f>", true, true), "caf\u00e9", false},
		{"UTF-16 with a text declaration", utf16Bytes(`<?xml encoding="UTF-16"?><f>x</f>`, false, true), "x", false},
		{"UTF-16 with two BOMs keeps one as content", utf16Bytes("\uFEFF<f/>", true, true), "\uFEFF", false},
		{"UTF-8 BOM is dropped", "\uFEFF<f>x</f>", "x", false},
		{"UTF-8 unchanged", "<f>x</f>", "x", false},
		{"BOM then a reversed BOM is an illegal character", utf16Bytes("\uFFFE<f/>", true, true), "", true},
		{"odd byte count is not UTF-16", utf16Bytes("<f/>", true, true) + "x", "", true},
	}
	for _, c := range cases {
		dir := writeFiles(t, map[string]string{
			"e.ent":   c.ent,
			"doc.xml": `<!DOCTYPE r [<!ENTITY e SYSTEM "e.ent">]><r>&e;</r>`,
		})
		p := filepath.Join(dir, "doc.xml")
		src, _ := os.ReadFile(p)
		tree, err := ParseString(string(src), ParseOptions{
			AllowDOCTYPE: true, ExternalEntities: &dirResolver{root: dir}, BaseURI: fileuri.Of(p)})
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: parsed, want an error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: parse: %v", c.name, err)
			continue
		}
		if got := tree.Root.StringValue(); got != c.want {
			t.Errorf("%s: text %q, want %q", c.name, got, c.want)
		}
	}
}

// Parameter entities are scoped to the DTD, not to the text that declared
// them, and an external one resolves against the entity its DECLARATION is in
// (XML §4.2.2). Both shapes are the W3C xmlconf's: v-pe02 (Appendix D, a
// character reference in a parameter entity value that becomes a reference
// when substituted) and rmt-e2e-18 (a parameter entity declared in one module
// and referenced from the internal subset).
func TestParameterEntityScopeAndBase(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"pe02.xml": `<!DOCTYPE r [
<!ENTITY % xx '&#37;zz;'>
<!ENTITY % zz '&#60;!ENTITY tricky "error-prone" >' >
%xx;
]><r>&tricky;</r>`,
		"e18.xml": `<!DOCTYPE r [
<!ENTITY % pe SYSTEM "sub1/pe.ent">
%pe;
%intpe;
]><r>&ent;</r>`,
		"sub1/pe.ent":    `<!ENTITY % extpe SYSTEM "../sub2/extpe.ent"><!ENTITY % intpe "%extpe;">`,
		"sub2/extpe.ent": `<!ENTITY ent "from sub2">`,
	})
	for name, want := range map[string]string{"pe02.xml": "error-prone", "e18.xml": "from sub2"} {
		if got := mustParseExternal(t, dir, name).Root.StringValue(); got != want {
			t.Errorf("%s: text %q, want %q", name, got, want)
		}
	}
}

// TestDTDCommentDashes: XML 1.0 §2.5 [15] admits no "--" in a comment's body
// and no "-" ending it ("--->"). That holds in content, in the internal subset
// and in an external subset alike, with the one error text.
func TestDTDCommentDashes(t *testing.T) {
	for _, src := range []string{
		`<!DOCTYPE r [<!-- a -- b -->]><r/>`,
		`<!DOCTYPE r [<!-- a --->]><r/>`,
		`<r><!-- a -- b --></r>`,
		`<r><!-- a ---></r>`,
		`<!-- a ---><r/>`,
	} {
		_, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
		if err == nil || !strings.Contains(err.Error(), `invalid sequence "--" not allowed in comments`) {
			t.Errorf("%s: err = %v, want the comment refused", src, err)
		}
	}
	for _, body := range []string{" a -- b ", " a -"} {
		dir := writeFiles(t, map[string]string{
			"d.dtd":   "<!--" + body + "--><!ENTITY e \"x\">",
			"doc.xml": `<!DOCTYPE r SYSTEM "d.dtd"><r>&e;</r>`,
		})
		p := filepath.Join(dir, "doc.xml")
		src, _ := os.ReadFile(p)
		_, err := ParseString(string(src), ParseOptions{
			AllowDOCTYPE: true, ExternalEntities: &dirResolver{root: dir}, BaseURI: fileuri.Of(p),
		})
		if err == nil || !strings.Contains(err.Error(), `invalid sequence "--" not allowed in comments`) {
			t.Errorf("external subset comment %q: err = %v, want it refused", body, err)
		}
	}
	dir := writeFiles(t, map[string]string{
		"d.dtd":   "<!-- a - b > c --><!ENTITY e \"x\">",
		"doc.xml": `<!DOCTYPE r SYSTEM "d.dtd" [<!-- a - b > c -->]><r>&e;</r>`,
	})
	if got := mustParseExternal(t, dir, "doc.xml").Root.StringValue(); got != "x" {
		t.Errorf("well-formed comments: got %q, want x", got)
	}
}

// TestDeclarationsAfterUnreadParameterEntity is XML 1.0 §5.1: a processor
// that has not read a parameter entity must not process entity or ATTLIST
// declarations that follow the reference, since the entity may have held
// overriding declarations — unless the document is standalone="yes", when it
// must. Declarations before the reference are processed either way, and a
// processor that does read the entity (a resolver is set) processes all.
func TestDeclarationsAfterUnreadParameterEntity(t *testing.T) {
	const subset = `<!ENTITY b "B"><!ATTLIST r c CDATA "C"> %p; <!ENTITY e "v"><!ATTLIST r a CDATA "d">`
	summary := func(tree *Tree) string {
		r := tree.Root.ChildElements()[0]
		s := r.StringValue()
		for _, n := range []string{"c", "a"} {
			if a := r.Attr("", n); a != nil {
				s += " " + n + "=" + a.value
			}
		}
		return s
	}
	cases := []struct{ name, prolog, decl, want string }{
		{"external PE unread", "", `<!ENTITY % p SYSTEM "p.ent">`, "[B] c=C"},
		{"internal PE unread", "", `<!ENTITY % p "">`, "[B] c=C"},
		{"standalone", `<?xml version="1.0" standalone="yes"?>`, `<!ENTITY % p "">`, "[Bv] c=C a=d"},
	}
	for _, c := range cases {
		src := c.prolog + "<!DOCTYPE r [" + c.decl + subset + "]><r>[&b;&e;]</r>"
		tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := summary(tree); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	dir := writeFiles(t, map[string]string{
		"p.ent":   "",
		"doc.xml": `<!DOCTYPE r [<!ENTITY % p SYSTEM "p.ent">` + subset + `]><r>[&b;&e;]</r>`,
	})
	if got := summary(mustParseExternal(t, dir, "doc.xml")); got != "[Bv] c=C a=d" {
		t.Errorf("PE read through a resolver: got %q, want %q", got, "[Bv] c=C a=d")
	}
}

// TestParameterEntityCharRefsAreLegal: WFC Legal Character holds for the
// character references in a parameter entity's value as for a general one,
// by the same rule, whether the entity is read or not, and for declarations
// after an unread one that §5.1 leaves unprocessed.
func TestParameterEntityCharRefsAreLegal(t *testing.T) {
	const v11 = `<?xml version="1.1"?>`
	cases := []struct {
		name, src string
		ok        bool
	}{
		{"1.0 #x1", `<!DOCTYPE r [<!ENTITY % p "&#x1;">]><r/>`, false},
		{"1.1 #x1", v11 + `<!DOCTYPE r [<!ENTITY % p "&#x1;">]><r/>`, true},
		{"1.1 #x0", v11 + `<!DOCTYPE r [<!ENTITY % p "&#0;">]><r/>`, false},
		{"after an unread PE", `<!DOCTYPE r [<!ENTITY % p ""> %p; <!ENTITY % q "&#0;">]><r/>`, false},
		{"general after an unread PE", `<!DOCTYPE r [<!ENTITY % p ""> %p; <!ENTITY e "&#0;">]><r/>`, false},
		{"legal", `<!DOCTYPE r [<!ENTITY % p "&#x9;&#60;">]><r/>`, true},
	}
	for _, c := range cases {
		_, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if (err == nil) != c.ok || err != nil && !strings.Contains(err.Error(), "character code") {
			t.Errorf("%s: err = %v, want ok %v", c.name, err, c.ok)
		}
	}
	dir := writeFiles(t, map[string]string{
		"d.dtd":   `<!ENTITY % q "&#xFFFE;">`,
		"doc.xml": `<!DOCTYPE r SYSTEM "d.dtd"><r/>`,
	})
	p := filepath.Join(dir, "doc.xml")
	src, _ := os.ReadFile(p)
	_, err := ParseString(string(src), ParseOptions{
		AllowDOCTYPE: true, ExternalEntities: &dirResolver{root: dir}, BaseURI: fileuri.Of(p),
	})
	if err == nil || !strings.Contains(err.Error(), "illegal character code U+FFFE") {
		t.Errorf("external subset PE: err = %v, want it refused", err)
	}
}
