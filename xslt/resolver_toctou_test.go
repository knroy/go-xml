package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A symlink swapped in after the containment check must not be followed.
//
// resolvePath calls filepath.EvalSymlinks and compares the result against the
// roots, then the file is opened later. Those are two different moments, and an
// attacker able to write to the filesystem can replace a checked path with a
// link pointing outside the root in between. Reading through os.Root closes
// that window: each component is resolved against the root's own descriptor,
// so a link leaving the root is refused at open time by the kernel rather than
// admitted by a string comparison taken earlier.
//
// This needs filesystem write access and so sits outside the threat model a
// hostile document reaches. It is fixed because a multi-tenant deployment can
// hand exactly that access to an untrusted caller.
func TestReadConfinedRefusesSwappedSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.xml")
	if err := os.WriteFile(secret, []byte(`<secret/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "doc.xml")
	if err := os.WriteFile(inside, []byte(`<ok/>`), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &FileResolver{Roots: []string{root}}

	// The honest path reads what it should.
	if data, err := r.readConfined(inside); err != nil {
		t.Fatalf("reading a file inside the root: %v", err)
	} else if !strings.Contains(string(data), "<ok/>") {
		t.Fatalf("got %q", data)
	}

	// Now swap it for a link out of the root, as an attacker with write access
	// would between the check and the open.
	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, inside); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	data, err := r.readConfined(inside)
	if err == nil && strings.Contains(string(data), "secret") {
		t.Fatal("followed a symlink out of the root; containment is advisory")
	}
}
