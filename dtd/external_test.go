package dtd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// loadCheck parses a document, loads its DTD through Load, and validates.
//
// It is the external-subset counterpart of check in validate_test.go: the
// difference is only which entry point reads the DOCTYPE, so a test here reads
// the same way as one there.
func loadCheck(t *testing.T, src string, opts LoadOptions) error {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	d, err := Load(tree.DocType, opts)
	if err != nil {
		return err
	}
	return Validate(tree.Root, d, Options{})
}

func mapOpts(docs map[string]string) LoadOptions {
	return LoadOptions{Resolver: &MapResolver{Docs: docs}}
}

// The whole point of the feature: a document whose <!ELEMENT> declarations
// live in another file is now validated against them, rather than against the
// handful it happens to declare inline.
func TestExternalSubsetIsLoadedAndEnforced(t *testing.T) {
	opts := mapOpts(map[string]string{
		"r.dtd": `<!ELEMENT r (a, b)>
<!ELEMENT a EMPTY>
<!ELEMENT b EMPTY>
<!ATTLIST a n CDATA #REQUIRED>`,
	})
	const doc = `<!DOCTYPE r SYSTEM "r.dtd">`
	if err := loadCheck(t, doc+`<r><a n="x"/><b/></r>`, opts); err != nil {
		t.Errorf("a document matching the external subset should be valid: %v", err)
	}
	// Each of these violates a declaration that exists ONLY in the external
	// subset, so before this feature every one of them passed.
	invalid := map[string]string{
		"order":             `<r><b/><a n="x"/></r>`,
		"missing child":     `<r><a n="x"/></r>`,
		"missing #REQUIRED": `<r><a/><b/></r>`,
		"undeclared child":  `<r><a n="x"/><b/><z/></r>`,
	}
	for name, body := range invalid {
		if err := loadCheck(t, doc+body, opts); err == nil {
			t.Errorf("%s: %s should be invalid", name, body)
		}
	}
}

// XML 1.0 section 2.8: the internal subset is read FIRST, so where both
// subsets declare a name the internal declaration binds and the external one
// is ignored — not an error.
func TestInternalSubsetTakesPrecedence(t *testing.T) {
	opts := mapOpts(map[string]string{
		"r.dtd": `<!ELEMENT r (a)>
<!ELEMENT a EMPTY>
<!ELEMENT b EMPTY>
<!ATTLIST a n CDATA "external">`,
	})
	// The internal subset redeclares r as (a, b) and a's default as
	// "internal". Both must win.
	const doc = `<!DOCTYPE r SYSTEM "r.dtd" [
<!ELEMENT r (a, b)>
<!ATTLIST a n CDATA "internal">
]>`
	if err := loadCheck(t, doc+`<r><a/><b/></r>`, opts); err != nil {
		t.Errorf("the internal (a, b) model should bind: %v", err)
	}
	// Under the EXTERNAL model, (a), this document is valid; under the
	// internal one it is not. That it is refused is the precedence.
	if err := loadCheck(t, doc+`<r><a/></r>`, opts); err == nil {
		t.Error("the external (a) model must not override the internal (a, b)")
	}

	d, err := Load(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	attrs := d.Attributes["a"]
	if len(attrs) != 1 {
		t.Fatalf("attribute a/n declared %d times, want the first to bind alone", len(attrs))
	}
	if attrs[0].Value != "internal" {
		t.Errorf("attribute default = %q, want %q (XML 1.0 section 2.8)",
			attrs[0].Value, "internal")
	}
	// The external subset still contributes what the internal one does not
	// redeclare.
	if _, ok := d.Elements["b"]; !ok {
		t.Error("element b, declared only externally, should still be present")
	}
	if !d.HasExternalSubset || d.ExternalSubset == "" {
		t.Error("the loaded external subset should be recorded")
	}
}

// A parameter entity declared in the internal subset and used in the external
// one. Section 2.8 makes this work precisely because the internal subset is
// read first: the external subset's own declaration of the same name is
// ignored, which is the "internal subset as a set of overrides" idiom.
func TestParameterEntitySpansBothSubsets(t *testing.T) {
	opts := mapOpts(map[string]string{
		"r.dtd": `<!ENTITY % model "(a)">
<!ELEMENT r %model;>
<!ELEMENT a EMPTY>
<!ELEMENT b EMPTY>`,
	})
	// The internal subset declares %model; first, so its "(a, b)" binds and
	// the external subset's "(a)" is ignored.
	const doc = `<!DOCTYPE r SYSTEM "r.dtd" [<!ENTITY % model "(a, b)">]>`
	if err := loadCheck(t, doc+`<r><a/><b/></r>`, opts); err != nil {
		t.Errorf("the internal %%model; should bind: %v", err)
	}
	if err := loadCheck(t, doc+`<r><a/></r>`, opts); err == nil {
		t.Error("the external %model; must not override the internal one")
	}
	// Without the internal override the external declaration applies, which
	// pins that the test above measured precedence and not simply breakage.
	if err := loadCheck(t, `<!DOCTYPE r SYSTEM "r.dtd">`+`<r><a/></r>`, opts); err != nil {
		t.Errorf("with no override the external %%model; should bind: %v", err)
	}
}

// A parameter entity that expands to WHOLE DECLARATIONS. This is legal only in
// the external subset (XML 1.0 section 2.8) and is the mechanism DTD
// modularisation is built on: a subset that is nothing but a list of module
// references.
func TestParameterEntityExpandsToDeclarations(t *testing.T) {
	opts := mapOpts(map[string]string{
		"r.dtd": `<!ENTITY % core SYSTEM "core.mod">
%core;
<!ELEMENT r (a, b)>`,
		"core.mod": `<!ELEMENT a EMPTY>
<!ELEMENT b EMPTY>`,
	})
	const doc = `<!DOCTYPE r SYSTEM "r.dtd">`
	if err := loadCheck(t, doc+`<r><a/><b/></r>`, opts); err != nil {
		t.Errorf("a modular DTD should validate: %v", err)
	}
	if err := loadCheck(t, doc+`<r><a/><b/><c/></r>`, opts); err == nil {
		t.Error("an element no module declares should be invalid")
	}
	// An internal parameter entity expanding to declarations, also
	// external-subset-only.
	opts2 := mapOpts(map[string]string{
		"r.dtd": `<!ENTITY % decls "<!ELEMENT a EMPTY><!ELEMENT b EMPTY>">
%decls;
<!ELEMENT r (a, b)>`,
	})
	if err := loadCheck(t, doc+`<r><a/><b/></r>`, opts2); err != nil {
		t.Errorf("declarations from an internal PE should apply: %v", err)
	}
}

// Conditional sections, XML 1.0 section 3.4. External-subset-only, and the
// keyword is normally itself a parameter entity, which is why they are
// resolved after expansion rather than before.
func TestConditionalSections(t *testing.T) {
	const subset = `<!ENTITY %% draft "%s">
<!ELEMENT r (a)>
<!ELEMENT a EMPTY>
<![%%draft;[
<!ATTLIST a note CDATA #REQUIRED>
]]>`
	const doc = `<!DOCTYPE r SYSTEM "r.dtd">`

	// INCLUDE: the attribute declaration inside the section applies, so a
	// document without it is invalid.
	inc := mapOpts(map[string]string{"r.dtd": fmt.Sprintf(subset, "INCLUDE")})
	if err := loadCheck(t, doc+`<r><a note="x"/></r>`, inc); err != nil {
		t.Errorf("INCLUDE: %v", err)
	}
	if err := loadCheck(t, doc+`<r><a/></r>`, inc); err == nil {
		t.Error("INCLUDE should have made note #REQUIRED")
	}

	// IGNORE: the same declaration contributes nothing, so the same document
	// is valid.
	ign := mapOpts(map[string]string{"r.dtd": fmt.Sprintf(subset, "IGNORE")})
	if err := loadCheck(t, doc+`<r><a/></r>`, ign); err != nil {
		t.Errorf("IGNORE should have suppressed the declaration: %v", err)
	}
	if d, err := Load(doc, ign); err != nil {
		t.Fatal(err)
	} else if len(d.Attributes["a"]) != 0 {
		t.Errorf("IGNORE left %d attribute declarations, want 0", len(d.Attributes["a"]))
	}
}

// Nesting is the part of section 3.4 that is easy to get wrong. The contents
// of an IGNORE are not parsed as declarations, but its "<![" and "]]>"
// delimiters are still counted — otherwise the inner section's "]]>" ends the
// outer one and everything after it is read when it should not be.
func TestNestedConditionalSections(t *testing.T) {
	const doc = `<!DOCTYPE r SYSTEM "r.dtd">`

	// An INCLUDE inside an IGNORE contributes nothing, and — the actual
	// hazard — the declaration AFTER the inner section is still inside the
	// outer IGNORE and must also contribute nothing.
	outerIgnore := mapOpts(map[string]string{
		"r.dtd": `<!ELEMENT r (a)>
<!ELEMENT a EMPTY>
<![IGNORE[
  <![INCLUDE[ <!ATTLIST a inner CDATA #REQUIRED> ]]>
  <!ATTLIST a after CDATA #REQUIRED>
]]>`,
	})
	if err := loadCheck(t, doc+`<r><a/></r>`, outerIgnore); err != nil {
		t.Errorf("everything inside an IGNORE should be suppressed: %v", err)
	}
	d, err := Load(doc, outerIgnore)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(d.Attributes["a"]); n != 0 {
		t.Errorf("an IGNORE with a nested section leaked %d declarations", n)
	}

	// An IGNORE inside an INCLUDE: the outer contents apply, the inner do
	// not.
	outerInclude := mapOpts(map[string]string{
		"r.dtd": `<!ELEMENT r (a)>
<!ELEMENT a EMPTY>
<![INCLUDE[
  <!ATTLIST a outer CDATA #REQUIRED>
  <![IGNORE[ <!ATTLIST a inner CDATA #REQUIRED> ]]>
]]>`,
	})
	if err := loadCheck(t, doc+`<r><a outer="x"/></r>`, outerInclude); err != nil {
		t.Errorf("an INCLUDE's own declarations should apply: %v", err)
	}
	if err := loadCheck(t, doc+`<r><a outer="x" inner="y"/></r>`, outerInclude); err != nil {
		t.Errorf("the nested IGNORE should not have made inner required: %v", err)
	}
	if err := loadCheck(t, doc+`<r><a/></r>`, outerInclude); err == nil {
		t.Error("the outer INCLUDE should have made outer #REQUIRED")
	}
}

// SECURITY. The default is closed: with no Resolver nothing is fetched, and
// the refusal is LOUD.
//
// The alternative — quietly validating against the internal subset alone — is
// the defect class this repository's governing invariant forbids: a document
// whose <!ELEMENT> declarations all live externally would then be reported
// valid without one of them having been checked. "I could not read the
// constraints" must never become "the constraints hold".
func TestNoResolverRefusesRatherThanValidatingHalfADTD(t *testing.T) {
	const doc = `<!DOCTYPE r SYSTEM "r.dtd" [<!ELEMENT r (a)>]>`
	_, err := Load(doc, LoadOptions{})
	if err == nil {
		t.Fatal("a DOCTYPE naming an external subset must not load with no resolver")
	}
	if !errors.Is(err, ErrNoResolver) {
		t.Errorf("error = %v, want it to wrap ErrNoResolver", err)
	}
	// The message has to name the way out, or a caller cannot act on it.
	if !strings.Contains(err.Error(), "Resolver") ||
		!strings.Contains(err.Error(), "InternalSubsetOnly") {
		t.Errorf("error should name both remedies: %v", err)
	}

	// The old reading is still reachable, but only by asking for it.
	d, err := Load(doc, LoadOptions{InternalSubsetOnly: true})
	if err != nil {
		t.Fatalf("InternalSubsetOnly should not fetch and should not fail: %v", err)
	}
	if !d.HasExternalSubset {
		t.Error("a caller taking the partial reading must still be able to see it is partial")
	}
	if d.ExternalSubset != "" {
		t.Error("InternalSubsetOnly must not have read anything")
	}

	// A DOCTYPE naming nothing external needs no resolver at all.
	if _, err := Load(`<!DOCTYPE r [<!ELEMENT r EMPTY>]>`, LoadOptions{}); err != nil {
		t.Errorf("a purely internal subset should load with no resolver: %v", err)
	}
}

// countingResolver records what was asked for, so a test can assert that
// nothing was.
type countingResolver struct{ calls int }

func (r *countingResolver) ResolveExternal(systemID, publicID, base string) (io.ReadCloser, string, error) {
	r.calls++
	return io.NopCloser(strings.NewReader("")), systemID, nil
}

// The refusal above must happen BEFORE the resolver would be consulted, so
// that "no resolver" is not merely an error message over a fetch that already
// happened.
func TestInternalSubsetOnlyFetchesNothingEvenWithAResolver(t *testing.T) {
	r := &countingResolver{}
	_, err := Load(`<!DOCTYPE r SYSTEM "r.dtd" [<!ELEMENT r EMPTY>]>`,
		LoadOptions{Resolver: r, InternalSubsetOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 0 {
		t.Errorf("InternalSubsetOnly made %d fetches, want 0", r.calls)
	}
}

// SECURITY. A billion-laughs bomb split across the two subsets.
//
// This is the shape the external subset newly makes possible: half the ladder
// is declared inline and half is fetched, so a design with a per-subset budget
// would give each half its own allowance and the product would pass. The
// budget is shared, so it does not.
func TestBombSplitAcrossSubsetsIsRefused(t *testing.T) {
	// One ladder, cut in half. Rungs a0..a5 are declared in the internal
	// subset and a6..a9 in the external one, so neither half is a bomb by
	// itself and each is asserted harmless below. Together they are the
	// classic exponential, and it is the SHARED budget that refuses them —
	// a design giving each subset its own allowance would admit exactly
	// this.
	rungs := func(lo, hi int, pad int) string {
		var b strings.Builder
		if lo == 0 {
			fmt.Fprintf(&b, `<!ENTITY %% a0 "%s">`+"\n", strings.Repeat("x", pad))
			lo = 1
		}
		for i := lo; i <= hi; i++ {
			fmt.Fprintf(&b, `<!ENTITY %% a%d "`, i)
			for j := 0; j < 4; j++ {
				fmt.Fprintf(&b, "%%a%d;", i-1)
			}
			b.WriteString("\">\n")
		}
		return b.String()
	}
	internal := rungs(0, 5, 200)
	external := rungs(6, 9, 0) + "<!ELEMENT r (%a9;)>"
	doc := "<!DOCTYPE r SYSTEM \"r.dtd\" [\n" + internal + "\n]>"

	if _, err := Load("<!DOCTYPE r [\n"+internal+"\n<!ELEMENT r EMPTY>]>",
		LoadOptions{}); err != nil {
		t.Fatalf("the internal half alone should be within budget: %v", err)
	}
	if _, err := Load(`<!DOCTYPE r SYSTEM "r.dtd">`,
		mapOpts(map[string]string{"r.dtd": external})); err != nil {
		t.Fatalf("the external half alone should be within budget: %v", err)
	}

	_, err := Load(doc, mapOpts(map[string]string{"r.dtd": external}))
	if err == nil {
		t.Fatal("a bomb split across the two subsets was not refused")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("error = %v, want it to wrap xdm.ErrResourceLimit", err)
	}
	// It must be the EXPANSION budget that bound, not the fetch count or the
	// bytes read — those would be the right answer for the wrong reason and
	// would not bind on a bomb built entirely from internal entities.
	if !strings.Contains(err.Error(), "expansion") {
		t.Errorf("error = %v, want the expansion budget to be what bound", err)
	}
}

// The classic billion-laughs ladder, wholly inside the external subset. It is
// separate from the split case above because the two fail for different
// reasons: this one is refused by the size of what substitution PRODUCES,
// which is why the charge is on the expanded text and not on the few dozen
// characters of "%a8;%a8;..." that produce it.
func TestBillionLaughsInTheExternalSubsetIsRefused(t *testing.T) {
	var b strings.Builder
	fmt.Fprintf(&b, `<!ENTITY %% a0 "%s">`+"\n", strings.Repeat("x", 100))
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&b, `<!ENTITY %% a%d "`, i)
		for j := 0; j < 10; j++ {
			fmt.Fprintf(&b, "%%a%d;", i-1)
		}
		b.WriteString("\">\n")
	}
	b.WriteString("<!ELEMENT r (%a9;)>")

	_, err := Load(`<!DOCTYPE r SYSTEM "r.dtd">`,
		mapOpts(map[string]string{"r.dtd": b.String()}))
	if err == nil {
		t.Fatal("a billion-laughs ladder in the external subset was not refused")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("error = %v, want it to wrap xdm.ErrResourceLimit", err)
	}
}

// SECURITY. Parameter-entity recursion, direct and through a cycle. XML 1.0
// section 4.1 makes it a well-formedness error; it must be REFUSED rather than
// recursed into until something else stops it.
func TestParameterEntityRecursionIsRefused(t *testing.T) {
	cases := map[string]string{
		"direct":      `<!ENTITY % a "%a;"><!ELEMENT r (%a;)>`,
		"two-cycle":   `<!ENTITY % a "%b;"><!ENTITY % b "%a;"><!ELEMENT r (%a;)>`,
		"three-cycle": `<!ENTITY % a "%b;"><!ENTITY % b "%c;"><!ENTITY % c "%a;"><!ELEMENT r (%a;)>`,
		// The cycle spans the two subsets: %a is internal, %b external.
		// A cycle detector scoped to one subset would miss this.
	}
	for name, subset := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(`<!DOCTYPE r SYSTEM "r.dtd">`,
				mapOpts(map[string]string{"r.dtd": subset}))
			if err == nil {
				t.Fatal("a recursive parameter entity was not refused")
			}
			if !strings.Contains(err.Error(), "recursive") {
				t.Errorf("error = %v, want it to name the recursion", err)
			}
		})
	}

	// Spanning both subsets, called out separately because it is the case
	// the shared params map exists for.
	_, err := Load(`<!DOCTYPE r SYSTEM "r.dtd" [<!ENTITY % a "%b;">]>`,
		mapOpts(map[string]string{"r.dtd": `<!ENTITY % b "%a;"><!ELEMENT r (%a;)>`}))
	if err == nil {
		t.Fatal("a cycle spanning both subsets was not refused")
	}
	if !strings.Contains(err.Error(), "recursive") {
		t.Errorf("error = %v, want it to name the recursion", err)
	}
}

// hugeResolver hands back an endless stream, which is what a hostile or merely
// broken resolver looks like from here.
type hugeResolver struct{}

func (hugeResolver) ResolveExternal(systemID, publicID, base string) (io.ReadCloser, string, error) {
	return io.NopCloser(endlessReader{}), systemID, nil
}

type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

// SECURITY. A resolver returning something enormous is bounded, and bounded
// during the read rather than after it — an unbounded ReadAll would never
// return at all here.
func TestHugeExternalSubsetIsBounded(t *testing.T) {
	_, err := Load(`<!DOCTYPE r SYSTEM "r.dtd">`,
		LoadOptions{Resolver: hugeResolver{}})
	if err == nil {
		t.Fatal("an unbounded external subset was accepted")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("error = %v, want it to wrap xdm.ErrResourceLimit", err)
	}

	// And a finite resource over an explicitly lowered bound.
	big := strings.Repeat("<!-- x -->\n", 4096)
	_, err = Load(`<!DOCTYPE r SYSTEM "r.dtd">`, LoadOptions{
		Resolver:         &MapResolver{Docs: map[string]string{"r.dtd": big}},
		MaxExternalBytes: 1024,
	})
	if err == nil {
		t.Fatal("a resource over MaxExternalBytes was accepted")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("error = %v, want it to wrap xdm.ErrResourceLimit", err)
	}
	// Exactly at the limit is permitted; the off-by-one here is the
	// difference between refusing and silently truncating.
	if _, err := Load(`<!DOCTYPE r SYSTEM "r.dtd">`, LoadOptions{
		Resolver:         &MapResolver{Docs: map[string]string{"r.dtd": "<!ELEMENT r EMPTY>"}},
		MaxExternalBytes: int64(len("<!ELEMENT r EMPTY>")),
	}); err != nil {
		t.Errorf("a resource exactly at the limit should be read: %v", err)
	}
}

// SECURITY. Fan-out is bounded too: a subset that pulls in module after module
// cannot spend the process one small file at a time.
func TestExternalDocumentCountIsBounded(t *testing.T) {
	docs := map[string]string{}
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("m%d.ent", i)
		docs[name] = "<!-- module -->"
		fmt.Fprintf(&sb, "<!ENTITY %% m%d SYSTEM %q>\n%%m%d;\n", i, name, i)
	}
	sb.WriteString("<!ELEMENT r EMPTY>")
	docs["r.dtd"] = sb.String()

	_, err := Load(`<!DOCTYPE r SYSTEM "r.dtd">`, mapOpts(docs))
	if err == nil {
		t.Fatal("200 modules were read with a default bound of 64")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("error = %v, want it to wrap xdm.ErrResourceLimit", err)
	}
}

// A resolver that fails must make the load fail. There is no path on which an
// unreadable subset becomes an empty one and the document validates against
// what is left.
func TestResolverFailureRefusesTheLoad(t *testing.T) {
	_, err := Load(`<!DOCTYPE r SYSTEM "missing.dtd" [<!ELEMENT r EMPTY>]>`,
		mapOpts(map[string]string{"other.dtd": ""}))
	if err == nil {
		t.Fatal("an unreadable external subset validated against the internal one")
	}
	// A resolver answering (nil, nil) is the same hazard wearing a success.
	_, err = Load(`<!DOCTYPE r SYSTEM "r.dtd">`, LoadOptions{Resolver: nilResolver{}})
	if err == nil {
		t.Fatal("a resolver returning no content and no error was accepted")
	}
}

type nilResolver struct{}

func (nilResolver) ResolveExternal(systemID, publicID, base string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

// FileResolver, on a real filesystem, with the modular-DTD shape: the subset
// lives in a subdirectory and references a sibling relatively, which is what
// makes the base URI matter.
func TestFileResolverLoadsAModularSubset(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "dtd")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(sub, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("r.dtd", `<!ENTITY % core SYSTEM "core.mod">
%core;
<!ELEMENT r (a, b)>`)
	write("core.mod", "<!ELEMENT a EMPTY>\n<!ELEMENT b EMPTY>")

	opts := LoadOptions{
		Resolver: &FileResolver{Root: dir},
		// The document's base is inside the same tree, and "dtd/r.dtd" is
		// relative to it — the ordinary arrangement.
		BaseURI: fileURIOf(filepath.Join(dir, "doc.xml")),
	}
	const doc = `<!DOCTYPE r SYSTEM "dtd/r.dtd">`
	if err := loadCheck(t, doc+`<r><a/><b/></r>`, opts); err != nil {
		t.Fatalf("a modular DTD on disk should load: %v", err)
	}
	if err := loadCheck(t, doc+`<r><b/><a/></r>`, opts); err == nil {
		t.Error("the model from the on-disk subset should be enforced")
	}
}

// SECURITY. FileResolver is confined to Root, and the confinement holds
// against the three ways out: "..", an absolute path, and a non-file scheme.
func TestFileResolverConfinement(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "in")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.dtd")
	if err := os.WriteFile(secret, []byte("<!ELEMENT r EMPTY>"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Root: inside}
	base := fileURIOf(filepath.Join(inside, "doc.xml"))

	for _, id := range []string{
		"../secret.dtd",
		filepath.ToSlash(secret),
		"http://example.invalid/r.dtd",
		"https://example.invalid/r.dtd",
		"ftp://example.invalid/r.dtd",
	} {
		if _, _, err := r.ResolveExternal(id, "", base); err == nil {
			t.Errorf("%q should have been refused", id)
		}
	}
	// A file genuinely inside Root is still readable, or the test above
	// would pass against a resolver that refuses everything.
	if err := os.WriteFile(filepath.Join(inside, "ok.dtd"),
		[]byte("<!ELEMENT r EMPTY>"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, _, err := r.ResolveExternal("ok.dtd", "", base)
	if err != nil {
		t.Fatalf("a file inside Root should be readable: %v", err)
	}
	rc.Close()
}

// Cross-platform. A system identifier is a URI, not a path (XML 1.0 section
// 4.2.2), and both spellings of a local file have to survive the round trip —
// in particular the Windows one, where "file://C:/x" would make the drive
// letter an authority and lose it.
func TestFileURIRoundTripIsPlatformCorrect(t *testing.T) {
	// The conversion is checked in both directions on whichever platform the
	// test runs, using the spelling that platform produces.
	var abs string
	if runtime.GOOS == "windows" {
		abs = `C:\dtd\r.dtd`
	} else {
		abs = "/dtd/r.dtd"
	}
	uri := fileURIOf(abs)
	if !strings.HasPrefix(uri, "file:///") {
		t.Errorf("fileURIOf(%q) = %q, want the RFC 8089 three-slash form", abs, uri)
	}
	if got := fileURIToPath(uri); got != abs {
		t.Errorf("round trip of %q gave %q", abs, got)
	}

	// The Windows spellings are checked on every platform, because the bug
	// they guard is a string bug and does not need Windows to reproduce.
	if got := fileURIToPath("file:///C:/dtd/r.dtd"); filepath.ToSlash(got) != "C:/dtd/r.dtd" {
		t.Errorf("fileURIToPath(file:///C:/dtd/r.dtd) = %q, want the drive kept", got)
	}
	// Nothing may be read as an authority: a host would silently discard the
	// drive.
	u := fileURIOf("/a b/r.dtd")
	if strings.Contains(u, " ") {
		t.Errorf("fileURIOf(%q) = %q, want the space escaped", "/a b/r.dtd", u)
	}
	// A slash-separated relative identifier — which is how a DTD always
	// spells one, since a system identifier is a URI — becomes the native
	// separator. On Windows that is the backslash form; on Unix it is
	// unchanged. Either way the file named is the same one.
	if got := fileURIToPath("sub/r.dtd"); got != filepath.Join("sub", "r.dtd") {
		t.Errorf("fileURIToPath(%q) = %q, want %q",
			"sub/r.dtd", got, filepath.Join("sub", "r.dtd"))
	}
	// A backslash reaching here is already a native Windows path and must
	// survive: filepath.FromSlash leaves it alone on Windows and there is no
	// separator to translate on Unix, so it names the same file on both.
	if got := fileURIToPath(`sub\r.dtd`); got != `sub\r.dtd` {
		t.Errorf("fileURIToPath(%q) = %q, want it unchanged", `sub\r.dtd`, got)
	}
}

// Load and Parse must agree wherever no external subset is involved, or the
// new entry point is a second implementation of the old one that drifts.
func TestLoadAgreesWithParseWithoutAnExternalSubset(t *testing.T) {
	const doctype = `DOCTYPE r [
<!ELEMENT r (a, b*)>
<!ELEMENT a EMPTY>
<!ELEMENT b (#PCDATA)>
<!ATTLIST a id ID #IMPLIED n CDATA "x">
]`
	p, err := Parse(doctype)
	if err != nil {
		t.Fatal(err)
	}
	l, err := Load(doctype, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Elements) != len(l.Elements) {
		t.Errorf("Parse found %d elements, Load %d", len(p.Elements), len(l.Elements))
	}
	for name, pe := range p.Elements {
		le, ok := l.Elements[name]
		if !ok {
			t.Errorf("Load lost element %s", name)
			continue
		}
		if pe.Kind != le.Kind {
			t.Errorf("element %s: Parse kind %v, Load kind %v", name, pe.Kind, le.Kind)
		}
	}
	if len(p.Attributes["a"]) != len(l.Attributes["a"]) {
		t.Errorf("Parse found %d attributes on a, Load %d",
			len(p.Attributes["a"]), len(l.Attributes["a"]))
	}
	if l.HasExternalSubset {
		t.Error("a purely internal subset should not be marked external")
	}
}

// The bounds at their edges, on the pattern limits_boundary_test.go sets: zero
// is the default, negative is no limit, and the largest value a caller can
// name is permissive rather than a refusal.
func TestExternalLimitBoundaries(t *testing.T) {
	const subset = "<!ELEMENT r EMPTY>"
	docs := map[string]string{"r.dtd": subset}
	const doc = `<!DOCTYPE r SYSTEM "r.dtd">`

	for _, tt := range []struct {
		name string
		opts LoadOptions
		ok   bool
	}{
		{"zero is the default", LoadOptions{}, true},
		{"negative bytes is unlimited", LoadOptions{MaxExternalBytes: -1}, true},
		{"negative docs is unlimited", LoadOptions{MaxExternalDocuments: -1}, true},
		{"negative expansion is unlimited", LoadOptions{MaxEntityBytes: -1}, true},
		{"exactly at the byte limit", LoadOptions{MaxExternalBytes: int64(len(subset))}, true},
		{"one under the byte limit", LoadOptions{MaxExternalBytes: int64(len(subset)) - 1}, false},
		{"one document is enough for one", LoadOptions{MaxExternalDocuments: 1}, true},
		{"no documents refuses", LoadOptions{MaxExternalDocuments: 1e9}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := tt.opts
			o.Resolver = &MapResolver{Docs: docs}
			_, err := Load(doc, o)
			if tt.ok && err != nil {
				t.Errorf("want a load, got %v", err)
			}
			if !tt.ok && err == nil {
				t.Error("want a refusal, got a load")
			}
		})
	}
	// Zero documents permitted means nothing may be read, including the
	// subset itself. A bound of zero must not read the "default" meaning
	// here, since the field's zero value already does.
	_, err := Load(doc, LoadOptions{
		Resolver: &MapResolver{Docs: docs}, MaxExternalDocuments: -1,
		MaxExternalBytes: -1, MaxEntityBytes: -1})
	if err != nil {
		t.Errorf("every bound unlimited should load: %v", err)
	}
}
