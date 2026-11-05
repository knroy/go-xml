package xslt

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/fileuri"
	"github.com/knroy/go-xml/v2/xdm"
)

// symlinkedTree builds <tmp>/real holding files, <tmp>/link -> real, and
// <tmp>/outside.xml beside them. It skips where os.Symlink fails.
func symlinkedTree(t *testing.T, files map[string]string) (real, link string) {
	t.Helper()
	dir := t.TempDir()
	real = filepath.Join(dir, "real")
	link = filepath.Join(dir, "link")
	for name, body := range files {
		p := filepath.Join(real, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside.xml"), []byte(`<secret/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return real, link
}

// A missing file inside a root reached through a symlink is not found, not
// refused: the path is resolved up to its deepest existing ancestor before
// it is compared with the resolved root. It was refused as outside the
// permitted directories on macOS, where t.TempDir is under the /var link.
// Escapes stay refused.
func TestFileResolverMissingInRootThroughLink(t *testing.T) {
	real, link := symlinkedTree(t, map[string]string{"ok.xml": `<ok/>`})
	if err := os.Symlink(filepath.Join(filepath.Dir(real), "gone.xml"),
		filepath.Join(real, "dangling.xml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(filepath.Dir(real), "outside.xml"),
		filepath.Join(real, "out.xml")); err != nil {
		t.Fatal(err)
	}
	r, err := NewFileResolver(link)
	if err != nil {
		t.Fatal(err)
	}
	base := fileuri.Of(filepath.Join(link, "s.xsl"))
	for _, href := range []string{"nosuch.xml", "nodir/nosuch.xml",
		fileuri.Of(filepath.Join(link, "nodir", "deeper", "x.xml"))} {
		_, err := r.ResolveDocument(href, base)
		if err == nil || errors.Is(err, fs.ErrPermission) || !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %v; want not-exist, not a refusal", href, err)
		}
	}
	for _, href := range []string{"../outside.xml", "../nodir/x.xml", "out.xml", "dangling.xml"} {
		if tree, err := r.ResolveDocument(href, base); err == nil {
			t.Errorf("%s escaped the root: %s", href, tree.Root.StringValue())
		}
	}
	for _, href := range []string{"../outside.xml", "../nodir/x.xml"} {
		if _, err := r.ResolveDocument(href, base); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%s: %v; want the outside-the-roots refusal", href, err)
		}
	}

	// Through fn:doc, the stylesheet sees FODC0002 naming the missing file.
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template name="xsl:initial-template"><xsl:copy-of select="doc('nosuch.xml')"/></xsl:template></xsl:stylesheet>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "{http://www.w3.org/1999/XSL/Transform}initial-template", Documents: r})
	if err == nil || !strings.Contains(err.Error(), "FODC0002") ||
		strings.Contains(err.Error(), "outside the permitted") {
		t.Errorf("doc('nosuch.xml') = %v; want FODC0002 not found", err)
	}
}
