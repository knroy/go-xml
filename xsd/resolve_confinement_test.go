package xsd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A symlink out of the root is refused BY os.Root, at open time.
//
// The assertion is on which layer refuses, and that is not pedantry — it is
// the only thing a test can hold on to here. Both the old check-then-open
// shape and this one refuse every statically visible vector, with the same
// message: EvalSymlinks collapses a planted link before the prefix check sees
// it. So a test that merely plants a link and asserts refusal passes against
// the unhardened code too. That is exactly what xsd/assemble_test.go's
// static-symlink case does, and why it reads as coverage it does not provide.
//
// The shapes differ only inside the check-to-open window, and a racing test
// for it is worthless: 200,000 swap attempts against the unhardened resolver
// in a tight loop escaped zero times, matching the figure docs/security.md
// recorded when the risk was accepted. What IS deterministic is that os.Root
// is on the path at all. If the leaf were resolved before the open, os.Root
// would be handed an already-followed path, never see a link, and never
// produce "escapes from parent" — so this error is the evidence that the
// enforcement runs, and it disappears the moment the pre-resolution comes
// back. Sabotage confirms: restoring check-then-open fails this test.
func TestFileResolverRefusesSymlinkAtOpenTime(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.xsd")
	if err := os.WriteFile(secret, []byte(`<secret/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "sub.xsd")
	if err := os.WriteFile(inside, []byte(`<ok/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "main.xsd")
	r := &FileResolver{Root: root}

	// The honest path reads what it should, or refusing everything would
	// satisfy the check below for the wrong reason.
	rc, _, err := r.Resolve("", "sub.xsd", base)
	if err != nil {
		t.Fatalf("a file inside the root should be readable: %v", err)
	}
	rc.Close()

	// Swap it for a link out of the root, as an attacker with write access
	// inside the root would.
	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, inside); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rc, _, err = r.Resolve("", "sub.xsd", base)
	if err == nil {
		defer rc.Close()
		buf := make([]byte, 64)
		n, _ := rc.Read(buf)
		if strings.Contains(string(buf[:n]), "secret") {
			t.Fatal("followed a symlink out of the root; containment is advisory")
		}
		t.Fatal("a symlink out of the root should be refused")
	}
	if !errors.Is(err, errRefusedByPolicy) {
		t.Errorf("refusal did not carry errRefusedByPolicy: %v", err)
	}
	// The load-bearing assertion. os.Root's wording is what proves the open
	// itself refused; without it the pre-resolution is back and the window
	// with it.
	if !strings.Contains(err.Error(), "escapes from parent") {
		t.Errorf("refusal did not come from os.Root at open time, so the "+
			"containment is a string check again: %v", err)
	}
}

// Traversal vectors that need no privilege on any platform.
//
// CLAUDE.md §3. Every symlink-based confinement test in this tree calls
// t.Skipf when os.Symlink fails, and unprivileged Windows cannot create one —
// so on Windows those tests report success without executing, which is the
// thing tests/check.sh's header forbids. These vectors use no symlink, so
// they run everywhere, and the Windows-shaped ones (drive-absolute, UNC, ADS,
// reserved device names) are refused on Unix too, where they are simply
// filenames that do not exist inside the root.
func TestFileResolverRefusesUnprivilegedVectors(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "main.xsd")
	r := &FileResolver{Root: root}

	vectors := []struct{ name, location string }{
		{"parent traversal", "../secret.xsd"},
		{"deep traversal", "../../../../../../etc/passwd"},
		{"traversal through a subdir", "sub/../../secret.xsd"},
		{"drive absolute", `C:\Windows\win.ini`},
		{"drive absolute forward", "C:/Windows/win.ini"},
		{"unix absolute", "/etc/passwd"},
		{"UNC path", `\\server\share\secret.xsd`},
		{"UNC forward", "//server/share/secret.xsd"},
		{"alternate data stream", "main.xsd::$DATA"},
		{"reserved device CON", "CON"},
		{"reserved device NUL", "NUL"},
		{"reserved device COM1", "COM1"},
		{"reserved device AUX", "AUX"},
	}
	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			rc, resolved, err := r.Resolve("", v.location, base)
			if err == nil {
				rc.Close()
				t.Errorf("%q was admitted, resolving to %q", v.location, resolved)
			}
		})
	}
}

// The positive case, stated separately so the refusals above cannot be
// satisfied by a resolver that refuses everything.
func TestFileResolverAdmitsPathsInsideRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "in.xsd"), []byte(`<in/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.xsd"), []byte(`<top/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "main.xsd")
	r := &FileResolver{Root: root}

	// Spelled with a slash because a schemaLocation is a URI reference, not a
	// path -- filepath.Join would be wrong here even on Windows.
	for _, loc := range []string{"top.xsd", "sub/in.xsd", "./sub/in.xsd", "sub/../top.xsd"} {
		rc, _, err := r.Resolve("", loc, base)
		if err != nil {
			t.Errorf("%q is inside the root and should resolve: %v", loc, err)
			continue
		}
		rc.Close()
	}
}

// A location that simply is not there must stay distinguishable from one the
// root refused: XSD 1.1 section 4.2.1 lets an unresolvable include be dropped,
// and the W3C suite's own common/xsts.xsd depends on that, while a refusal is
// a decision and has to surface. Opening through os.Root must not collapse
// the two into one error.
func TestFileResolverMissingIsNotRefusal(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "main.xsd")
	r := &FileResolver{Root: root}

	if _, _, err := r.Resolve("", "absent.xsd", base); err == nil {
		t.Fatal("a missing file should be an error")
	} else if errors.Is(err, errRefusedByPolicy) {
		t.Errorf("a missing file was reported as a policy refusal: %v", err)
	}
	if _, _, err := r.Resolve("", "../secret.xsd", base); err == nil {
		t.Fatal("an escape should be an error")
	} else if !errors.Is(err, errRefusedByPolicy) {
		t.Errorf("an escape was not reported as a policy refusal: %v", err)
	}
}
