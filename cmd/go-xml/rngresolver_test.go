package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The RELAX NG resolver had no test at all before the confinement rewrite of
// 2026-09-10. relaxng ships no file resolver of its own, so this type is the
// only thing standing between an href in a grammar and the filesystem, and it
// was hardened here without a single case exercising it.

// A grammar inside the root loads; the root is enforced at open time.
//
// The refusal must carry os.Root's own "escapes from parent" wording, because
// that is what proves the open refused rather than the string comparison
// above it. Pre-resolving the final component would leave os.Root nothing to
// refuse, and this assertion is what would catch that regression -- the
// planted-symlink assertion on its own would not, since the earlier check
// refuses a visible link too.
func TestRNGFileResolverRefusesSymlinkAtOpenTime(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.rng")
	if err := os.WriteFile(secret, []byte(`<element name="s"><empty/></element>`), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "sub.rng")
	if err := os.WriteFile(inside, []byte(`<element name="ok"><empty/></element>`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &rngFileResolver{root: root, base: filepath.Join(root, "main.rng")}

	if _, err := r.ResolveSchema("sub.rng"); err != nil {
		t.Fatalf("a grammar inside the root should load: %v", err)
	}

	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, inside); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := r.ResolveSchema("sub.rng")
	if err == nil {
		t.Fatal("a symlink out of the root should be refused")
	}
	if !strings.Contains(err.Error(), "escapes from parent") {
		t.Errorf("refusal did not come from os.Root at open time, so the "+
			"containment is a string check again: %v", err)
	}
}

// Vectors that need no symlink privilege, so they execute on Windows rather
// than skipping. CLAUDE.md section 3: a check that does not run must not read
// as a check that passed.
func TestRNGFileResolverRefusesUnprivilegedVectors(t *testing.T) {
	root := t.TempDir()
	r := &rngFileResolver{root: root, base: filepath.Join(root, "main.rng")}

	for _, v := range []struct{ name, href string }{
		{"parent traversal", "../secret.rng"},
		{"deep traversal", "../../../../../../etc/passwd"},
		{"traversal through a subdir", "sub/../../secret.rng"},
		{"drive absolute", `C:\Windows\win.ini`},
		{"drive absolute forward", "C:/Windows/win.ini"},
		{"unix absolute", "/etc/passwd"},
		{"UNC path", `\\server\share\secret.rng`},
		{"UNC forward", "//server/share/secret.rng"},
		{"alternate data stream", "main.rng::$DATA"},
		{"reserved device CON", "CON"},
		{"reserved device NUL", "NUL"},
		{"reserved device COM1", "COM1"},
		{"reserved device AUX", "AUX"},
		{"remote scheme", "http://example.invalid/s.rng"},
	} {
		t.Run(v.name, func(t *testing.T) {
			if _, err := r.ResolveSchema(v.href); err == nil {
				t.Errorf("%q was admitted", v.href)
			}
		})
	}
}

// The positive case, so the refusals above cannot be met by refusing all.
func TestRNGFileResolverAdmitsPathsInsideRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	grammar := []byte(`<element name="ok"><empty/></element>`)
	if err := os.WriteFile(filepath.Join(sub, "in.rng"), grammar, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.rng"), grammar, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &rngFileResolver{root: root, base: filepath.Join(root, "main.rng")}

	// An href is a URI reference, so the separator is "/" on every platform.
	for _, href := range []string{"top.rng", "sub/in.rng", "./sub/in.rng", "sub/../top.rng"} {
		if _, err := r.ResolveSchema(href); err != nil {
			t.Errorf("%q is inside the root and should load: %v", href, err)
		}
	}
}

// An empty root is the documented unconfined command-line case and must keep
// reading any readable path, or hardening the rooted case would have silently
// broken the default one.
func TestRNGFileResolverUnrootedStillReads(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "top.rng")
	if err := os.WriteFile(p, []byte(`<element name="ok"><empty/></element>`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &rngFileResolver{base: filepath.Join(dir, "main.rng")}
	if _, err := r.ResolveSchema("top.rng"); err != nil {
		t.Errorf("an unrooted resolver should read any readable path: %v", err)
	}
}
