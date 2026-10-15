package xslt

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// GO-2026-4970: on Unix, an unpatched os.Root (before Go 1.25.12 / 1.26.5)
// follows a final-component symlink out of the root when the name ends in "/",
// so root.Open("dlink/") opens the directory dlink points at. FileResolver is
// safe on such a Go only because resolvePath and readConfined clean the name
// (Abs, then Rel) before it reaches os.Root, which strips the slash. A refactor
// that hands os.Root a raw href must keep this passing on an unpatched Go.
func TestTrailingSlashSymlinkStaysConfined(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows; GO-2026-4970 is Unix-only")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.xml"), []byte("<secret/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.xml"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dlink")); err != nil {
		t.Fatal(err)
	}
	r, err := NewFileResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	r.UnparsedText = true
	base := filepath.Join(root, "s.xsl")
	for _, href := range []string{"dlink/", "dlink//", "./dlink/", "link/", "link//"} {
		refused := func(api string, opened bool, err error) {
			if opened || err == nil || !strings.Contains(err.Error(), "outside the permitted directories") {
				t.Errorf("%s(%q) = %v, %v; want a confinement refusal", api, href, opened, err)
			}
		}
		d, err := r.ResolveDocument(href, base)
		refused("ResolveDocument", d != nil, err)
		b, _, err := r.ResolveInclude(href, base, "")
		refused("ResolveInclude", b != nil, err)
		s, err := r.ResolveText(href, base, "")
		refused("ResolveText", s != "", err)
		rc, _, err := r.ResolveEntity(href, "", base)
		if rc != nil {
			rc.Close()
		}
		refused("ResolveEntity", rc != nil, err)
	}
}
