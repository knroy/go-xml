package xsd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// GO-2026-4970: on Unix, an unpatched os.Root (before Go 1.25.12 / 1.26.5)
// follows a final-component symlink out of the root when the name ends in "/",
// so root.Open("dlink/") opens the directory dlink points at. FileResolver is
// safe on such a Go only because the name is cleaned (Clean, then Rel) before
// it reaches os.Root, which strips the slash and leaves a name os.Root refuses.
// A refactor that hands os.Root a raw schemaLocation must keep this passing on
// an unpatched Go.
func TestTrailingSlashSymlinkStaysConfined(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows; GO-2026-4970 is Unix-only")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.xsd"), []byte("<secret/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.xsd"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dlink")); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Root: root}
	for _, loc := range []string{
		"dlink/", "dlink//", "./dlink/", "sub/../dlink/", "link/", "link//", "link/.",
		"file://" + filepath.ToSlash(root) + "/dlink/",
	} {
		rc, _, err := r.Resolve("", loc, filepath.Join(root, "main.xsd"))
		if rc != nil {
			rc.Close()
		}
		if rc != nil || !errors.Is(err, errRefusedByPolicy) {
			t.Errorf("Resolve(%q) = %v, %v; want a confinement refusal and nothing opened", loc, rc != nil, err)
		}
	}
}
