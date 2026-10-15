package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xslt"
)

// GO-2026-4970: on Unix, an unpatched os.Root (before Go 1.25.12 / 1.26.5)
// follows a final-component symlink out of the root when the name ends in "/".
// writeSecondary is safe on such a Go only because the href is cleaned (Clean,
// then Rel; path.Join in mkdirAllIn) before it reaches os.Root, which strips
// the slash and leaves a name os.Root refuses. A refactor that hands os.Root a
// raw href must keep this passing on an unpatched Go.
func TestTrailingSlashSymlinkStaysConfined(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows; GO-2026-4970 is Unix-only")
	}
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("KEEP"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dlink")); err != nil {
		t.Fatal(err)
	}
	for _, href := range []string{"dlink/", "dlink//", "./dlink/", "dlink/x/", "link/", "link//"} {
		err := writeSecondary([]xslt.SecondaryResult{{Href: href}}, root)
		if err == nil || !strings.Contains(err.Error(), "escapes from parent") {
			t.Errorf("writeSecondary(%q) = %v; want os.Root's confinement refusal", href, err)
		}
		got, _ := os.ReadFile(victim)
		ents, _ := os.ReadDir(outside)
		if string(got) != "KEEP" || len(ents) != 1 {
			t.Errorf("writeSecondary(%q) wrote outside the root: victim=%q, %d entries", href, got, len(ents))
		}
	}
}
