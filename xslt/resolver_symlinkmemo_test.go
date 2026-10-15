package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FileResolver remembers what each path's symlinks resolve to, and a
// remembered answer that has gone stale still cannot read outside the root:
// readConfined resolves the path again, through os.Root, when it opens it.
func TestFileResolverRemembersSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.xml")
	if err := os.WriteFile(secret, []byte(`<secret/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.xml")
	if err := os.WriteFile(target, []byte(`<ok/>`), 0o600); err != nil {
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
	base := fileURIOf(filepath.Join(root, "main.xsl"))
	first, err := r.resolvePath("link.xml", base)
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	_, remembered := r.real[link]
	r.mu.Unlock()
	if !remembered {
		t.Fatalf("the resolution of %s was not remembered", link)
	}

	// Point the link out of the root, then replace what it used to name
	// with a link out of the root too. The remembered answer still names
	// target.xml, and opening that must not reach the secret.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	again, err := r.resolvePath("link.xml", base)
	if err != nil || again != first {
		t.Fatalf("second resolution: %q, %v; want the remembered %q", again, err, first)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, target); err != nil {
		t.Fatal(err)
	}
	if data, err := r.readConfined(again); err == nil && strings.Contains(string(data), "secret") {
		t.Fatal("a remembered resolution read a file outside the root")
	}
}
