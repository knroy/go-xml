package dtd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// GO-2026-4970: on Unix, an unpatched os.Root (before Go 1.25.12 / 1.26.5)
// follows a final-component symlink out of the root when the name ends in "/",
// so root.Open("dlink/") opens the directory dlink points at. FileResolver is
// safe on such a Go only because the name is cleaned (Abs, then Rel) before it
// reaches os.Root, which strips the slash and leaves a name os.Root refuses. A
// refactor that hands os.Root a raw system identifier must keep this passing
// on an unpatched Go.
func TestTrailingSlashSymlinkStaysConfined(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows; GO-2026-4970 is Unix-only")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.dtd"), []byte("<!ENTITY s 'x'>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.dtd"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dlink")); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Root: root}
	for _, id := range []string{
		"dlink/", "dlink//", "./dlink/", "link/", "link//",
		"file://" + filepath.ToSlash(root) + "/dlink/",
	} {
		rc, _, err := r.ResolveExternal(id, "", filepath.Join(root, "doc.xml"))
		if rc != nil {
			rc.Close()
		}
		// os.Root's own wording: the refusal must be the confinement, not a
		// read that failed only because the escape reached a directory.
		if rc != nil || err == nil || !strings.Contains(err.Error(), "escapes from parent") {
			t.Errorf("ResolveExternal(%q) = %v, %v; want os.Root's confinement refusal", id, rc != nil, err)
		}
	}
}
