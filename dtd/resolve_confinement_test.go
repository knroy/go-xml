package dtd

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A symlink out of Root is refused BY os.Root, at open time.
//
// The assertion is on which layer refuses. Both the old check-then-open shape
// and this one refuse every statically visible vector with the same message,
// because resolvePath's EvalSymlinks collapses a planted link before the
// containment check reads it — so a test that merely plants a link and asserts
// refusal passes against the unhardened code too, which is the flaw in
// TestFileResolverConfinement's coverage. The shapes differ only inside the
// check-to-open window, which no racing test can hit reliably.
//
// What is deterministic is that os.Root is on the path. Had the leaf been
// resolved first, os.Root would be handed an already-followed path and could
// never report "escapes from parent"; that wording is therefore the evidence
// that the open enforced containment, and it vanishes if the pre-resolution
// returns. Modelled on xslt/resolver_toctou_test.go.
func TestFileResolverRefusesSymlinkAtOpenTime(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.dtd")
	if err := os.WriteFile(secret, []byte("<!ELEMENT secret EMPTY>"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "sub.dtd")
	if err := os.WriteFile(inside, []byte("<!ELEMENT ok EMPTY>"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Root: root}
	base := fileURIOf(filepath.Join(root, "doc.xml"))

	rc, _, err := r.ResolveExternal("sub.dtd", "", base)
	if err != nil {
		t.Fatalf("a file inside Root should be readable: %v", err)
	}
	rc.Close()

	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, inside); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rc, _, err = r.ResolveExternal("sub.dtd", "", base)
	if err == nil {
		data, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(data), "secret") {
			t.Fatal("followed a symlink out of Root; containment is advisory")
		}
		t.Fatal("a symlink out of Root should be refused")
	}
	if !strings.Contains(err.Error(), "escapes from parent") {
		t.Errorf("refusal did not come from os.Root at open time, so the "+
			"containment is a string check again: %v", err)
	}
}

// Traversal vectors needing no privilege on any platform.
//
// CLAUDE.md §3. The symlink tests above and in external_test.go skip on
// unprivileged Windows, where os.Symlink fails — a check that does not run
// must not read as a check that passed. These use no symlink and so execute
// identically on all three CI platforms.
func TestFileResolverRefusesUnprivilegedVectors(t *testing.T) {
	root := t.TempDir()
	r := &FileResolver{Root: root}
	base := fileURIOf(filepath.Join(root, "doc.xml"))

	vectors := []struct{ name, systemID string }{
		{"parent traversal", "../secret.dtd"},
		{"deep traversal", "../../../../../../etc/passwd"},
		{"traversal through a subdir", "sub/../../secret.dtd"},
		{"drive absolute", `C:\Windows\win.ini`},
		{"drive absolute forward", "C:/Windows/win.ini"},
		{"drive absolute as file URI", "file:///C:/Windows/win.ini"},
		{"unix absolute", "/etc/passwd"},
		{"UNC path", `\\server\share\secret.dtd`},
		{"UNC forward", "//server/share/secret.dtd"},
		{"alternate data stream", "doc.dtd::$DATA"},
		{"reserved device CON", "CON"},
		{"reserved device NUL", "NUL"},
		{"reserved device COM1", "COM1"},
		{"reserved device AUX", "AUX"},
	}
	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			rc, uri, err := r.ResolveExternal(v.systemID, "", base)
			if err == nil {
				rc.Close()
				t.Errorf("%q was admitted, resolving to %q", v.systemID, uri)
			}
		})
	}
}

// The positive case, so the refusals above cannot be met by refusing all.
//
// A system identifier is a URI, so the separator is always "/" regardless of
// platform -- filepath.Join would be wrong on the wire even where it is right
// on disk. The file is created with filepath.Join, which is right on disk.
func TestFileResolverAdmitsPathsInsideRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "in.dtd"),
		[]byte("<!ELEMENT in EMPTY>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.dtd"),
		[]byte("<!ELEMENT top EMPTY>"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Root: root}
	base := fileURIOf(filepath.Join(root, "doc.xml"))

	for _, id := range []string{"top.dtd", "sub/in.dtd", "./sub/in.dtd", "sub/../top.dtd"} {
		rc, _, err := r.ResolveExternal(id, "", base)
		if err != nil {
			t.Errorf("%q is inside Root and should resolve: %v", id, err)
			continue
		}
		rc.Close()
	}
}

// A refusal names the system identifier as written, not the resolved path or
// Root (R5, as xslt.FileResolver after A4-F3); the outside one is
// fs.ErrPermission to errors.Is.
func TestFileResolverErrorsNameOnlyTheSystemID(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.dtd"), []byte("<!ELEMENT big EMPTY>"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Root: root, MaxBytes: 4}
	base := fileURIOf(filepath.Join(root, "doc.xml"))
	resolved, _ := filepath.EvalSymlinks(root)
	for _, id := range []string{"../outside.dtd", "big.dtd"} {
		_, _, err := r.ResolveExternal(id, "", base)
		if err == nil {
			t.Fatalf("%s: no error", id)
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, `"`+id+`" is `) {
			t.Errorf("%s: %q does not name the identifier first", id, msg)
		}
		for _, leak := range []string{root, resolved, filepath.ToSlash(root)} {
			if strings.Contains(msg, leak) {
				t.Errorf("%s: %q names the root %s", id, msg, leak)
			}
		}
		if outside := id == "../outside.dtd"; errors.Is(err, fs.ErrPermission) != outside {
			t.Errorf("%s: errors.Is(fs.ErrPermission) = %v", id, !outside)
		}
	}
}

// A missing file in a missing subdirectory of a Root reached through a
// symlink is not found, not refused: the parent is resolved up to its deepest
// existing ancestor before it is compared with the resolved Root. Escapes stay
// refused.
func TestFileResolverMissingInRootThroughLink(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real"), filepath.Join(dir, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r := &FileResolver{Root: link}
	base := filepath.Join(link, "doc.xml")
	_, _, err := r.ResolveExternal("nodir/x.ent", "", base)
	if err == nil || errors.Is(err, fs.ErrPermission) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("nodir/x.ent: %v; want not-exist, not a refusal", err)
	}
	if _, _, err := r.ResolveExternal("../nodir/x.ent", "", base); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("../nodir/x.ent: %v; want the outside-the-root refusal", err)
	}
}
