package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func TestFileResolverConfinesToRoots(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "ok.xml")
	if err := os.WriteFile(inside, []byte(`<ok/>`), 0o600); err != nil {
		t.Fatal(err)
	}

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.xml")
	if err := os.WriteFile(outside, []byte(`<secret/>`), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.ResolveDocument(inside, ""); err != nil {
		t.Errorf("a file inside the root was refused: %v", err)
	}
	if _, err := r.ResolveDocument(outside, ""); err == nil {
		t.Error("a file outside the root was allowed")
	}
	// Traversal must not escape either.
	if _, err := r.ResolveDocument(filepath.Join(dir, "..", "escape.xml"), ""); err == nil {
		t.Error("a ..-traversal path was allowed")
	}
}

func TestFileResolverRejectsNonFileSchemes(t *testing.T) {
	r, err := NewFileResolver(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{
		"http://example.com/x.xml",
		"https://example.com/x.xml",
		"ftp://example.com/x.xml",
	} {
		if _, err := r.ResolveDocument(uri, ""); err == nil {
			t.Errorf("%s was allowed; network access must be refused", uri)
		}
	}
}

func TestFileResolverSymlinkCannotEscape(t *testing.T) {
	// A symlink inside an allowed root pointing outside it must not grant
	// access: the containment check runs after symlink resolution.
	root := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "secret.xml")
	if err := os.WriteFile(target, []byte(`<secret/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.xml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r, err := NewFileResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveDocument(link, ""); err == nil {
		t.Error("a symlink escaping the root was followed")
	}
}

func TestFileResolverCachesForNodeIdentity(t *testing.T) {
	// fn:doc must return the same node for the same URI within an execution,
	// so "doc('x') is doc('x')" holds.
	dir := t.TempDir()
	p := filepath.Join(dir, "d.xml")
	if err := os.WriteFile(p, []byte(`<a/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := r.ResolveDocument(p, "")
	if err != nil {
		t.Fatal(err)
	}
	t2, err := r.ResolveDocument(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if t1 != t2 {
		t.Error("the same URI produced two different trees; node identity would break")
	}
}

func TestIncludeWorksWithResolver(t *testing.T) {
	dir := t.TempDir()

	module := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:template match="inc">FROM-MODULE</xsl:template>
	</xsl:stylesheet>`
	if err := os.WriteFile(filepath.Join(dir, "mod.xsl"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}

	mainSrc := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:include href="mod.xsl"/>
		<xsl:template match="/"><r><xsl:apply-templates select="//inc"/></r></xsl:template>
	</xsl:stylesheet>`
	mainPath := filepath.Join(dir, "main.xsl")
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	stree, err := xdm.ParseString(mainSrc, xdm.ParseOptions{BaseURI: mainPath})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(stree.Root, CompileOptions{Resolver: r, BaseURI: mainPath})
	if err != nil {
		t.Fatal(err)
	}
	dtree, _ := xdm.ParseString(`<d><inc/></d>`, xdm.ParseOptions{})
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.String(), "FROM-MODULE") {
		t.Errorf("got %q, want the included template to apply", res.String())
	}
}

func TestCircularIncludeIsDetected(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.xsl")
	src := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:include href="a.xsl"/>
	</xsl:stylesheet>`
	if err := os.WriteFile(a, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	stree, _ := xdm.ParseString(src, xdm.ParseOptions{BaseURI: a})
	_, err = Compile(stree.Root, CompileOptions{Resolver: r, BaseURI: a})
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Errorf("err = %v, want a circular-include error", err)
	}
}

func TestImportPrecedence(t *testing.T) {
	// An imported template loses to a same-priority one in the importer.
	dir := t.TempDir()
	module := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:template match="x">IMPORTED</xsl:template>
	</xsl:stylesheet>`
	if err := os.WriteFile(filepath.Join(dir, "mod.xsl"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	mainSrc := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:import href="mod.xsl"/>
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:template match="x">MAIN</xsl:template>
		<xsl:template match="/"><r><xsl:apply-templates select="//x"/></r></xsl:template>
	</xsl:stylesheet>`
	mainPath := filepath.Join(dir, "main.xsl")
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	r, _ := NewFileResolver(dir)
	stree, _ := xdm.ParseString(mainSrc, xdm.ParseOptions{BaseURI: mainPath})
	s, err := Compile(stree.Root, CompileOptions{Resolver: r, BaseURI: mainPath})
	if err != nil {
		t.Fatal(err)
	}
	dtree, _ := xdm.ParseString(`<d><x/></d>`, xdm.ParseOptions{})
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.String(), "MAIN") {
		t.Errorf("got %q, want the importing stylesheet's template to win", res.String())
	}
}

// The containment check is the security boundary: a stylesheet is untrusted
// input, and an href it computes must not reach outside the permitted roots.
// These are written as an attacker would probe it.
func TestResolverContainmentAttacks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(outside, "secret.xml"),
		[]byte("<s>TOPSECRET</s>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.xml"), []byte("<ok/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A symlink to a file outside, and a symlink to the directory itself.
	if err := os.Symlink(filepath.Join(outside, "secret.xml"),
		filepath.Join(root, "link.xml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r, err := NewFileResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "ok.xml")

	refused := []struct{ why, href string }{
		{"a symlink out of the root is followed before the check", "link.xml"},
		{"a symlinked directory is no different", "dirlink/secret.xml"},
		{"plain traversal", "../" + filepath.Base(outside) + "/secret.xml"},
		{"repeated traversal", "../../etc/hosts"},
		{"an absolute path outside the root", filepath.Join(outside, "secret.xml")},
		{"an absolute system path", "/etc/hosts"},
		{"a file: URI is still a path", "file://" + filepath.Join(outside, "secret.xml")},
		{"traversal disguised by a same-directory prefix", "./../" +
			filepath.Base(outside) + "/secret.xml"},
		{"a non-file scheme is refused before touching the disk", "http://example.com/x.xml"},
		{"and so is every other scheme", "ftp://example.com/x.xml"},
	}
	for _, c := range refused {
		if _, err := r.ResolveDocument(c.href, base); err == nil {
			t.Errorf("%s: %q was permitted and must not be", c.why, c.href)
		}
	}

	// The legitimate case must still work, or the check is merely a denial.
	if _, err := r.ResolveDocument("ok.xml", base); err != nil {
		t.Errorf("a file inside the root was refused: %v", err)
	}
}

// A root given as a symlink must be compared after resolution, or every path
// inside it would look like an escape.
func TestResolverRootThroughSymlink(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "ok.xml"), []byte("<ok/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r, err := NewFileResolver(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveDocument("ok.xml", filepath.Join(link, "ok.xml")); err != nil {
		t.Errorf("a file inside a symlinked root was refused: %v", err)
	}
}

// A FileResolver is documented as usable as a plain literal, so the cache it
// keeps has to be created on first use rather than by a constructor nobody
// calls. Writing to a nil map panics, and a panic in a request handler is a
// denial of service.
func TestFileResolverLiteralDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.xml")
	if err := os.WriteFile(path, []byte(`<a/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{dir}}
	if _, err := r.ResolveDocument(path, ""); err != nil {
		t.Fatalf("resolving through a literal FileResolver: %v", err)
	}
	// Twice, so the cache is both written and read.
	if _, err := r.ResolveDocument(path, ""); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
}

// A root written relatively must confine the same paths an absolute one does.
//
// The containment check makes the candidate path absolute and resolves its
// symlinks; comparing that against a root the caller wrote relatively makes
// filepath.Rel fail, and every file is then refused as outside a directory it
// is plainly inside. A caller passing "./schemas" got a resolver that
// resolved nothing.
func TestFileResolverAcceptsRelativeRoots(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "x.xml"), []byte(`<a/>`), 0o600); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	r := &FileResolver{Roots: []string{"sub"}}
	if _, err := r.ResolveDocument("sub/x.xml", ""); err != nil {
		t.Errorf("a relative root should confine the same paths: %v", err)
	}
	// And still refuse what is outside it.
	if err := os.WriteFile(filepath.Join(dir, "out.xml"), []byte(`<a/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveDocument("out.xml", ""); err == nil {
		t.Error("a file outside the relative root was accepted")
	}
}
