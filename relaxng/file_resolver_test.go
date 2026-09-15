package relaxng

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func TestFileResolverConfinesSchemesPathsAndBytes(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.rng")
	if err := os.WriteFile(inside, []byte(`<element name="r"><empty/></element>`), 0600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Root: root}
	if _, err := r.ResolveSchema(inside); err != nil {
		t.Fatalf("inside schema refused: %v", err)
	}
	// Each refusal is matched against the specific message that names it.
	// Asserting only err != nil pinned nothing: neither of these paths exists,
	// so "no such file or directory" satisfied that test just as well, and
	// deleting BOTH guards -- the remote-scheme check and the outside-root
	// check -- left it green.
	for _, c := range []struct{ href, want string }{
		// A remote scheme is refused for being remote, before any open.
		{"https://example.invalid/a.rng", "remote schema URI"},
		// A file: URL naming a host is a network fetch, not a local read.
		{"file://otherhost/a.rng", "names the remote host"},
		// A path above Root is refused by the confinement check.
		{filepath.Join(root, "..", "outside.rng"), "resolves outside root"},
	} {
		_, err := r.ResolveSchema(c.href)
		if err == nil {
			t.Errorf("ResolveSchema(%q) succeeded", c.href)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("ResolveSchema(%q) error = %v, want one naming %q; an "+
				"error from some other cause (a missing file, say) means the "+
				"guard under test is no longer what refuses this",
				c.href, err, c.want)
		}
	}

	// The confinement is a rule about the path, not about what happens to
	// exist: a file that IS present outside Root is refused just the same.
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "real.rng")
	if err := os.WriteFile(outside, []byte(`<empty/>`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := r.ResolveSchema(outside)
	if err == nil {
		t.Fatalf("ResolveSchema(%q) read a file outside Root", outside)
	}
	if !strings.Contains(err.Error(), "resolves outside root") {
		t.Errorf("an existing file outside Root was refused by %v, want the "+
			"confinement check; if this is an open error the confinement is "+
			"not what refused it", err)
	}
	r.MaxBytes = 4
	_, err = r.ResolveSchema(inside)
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("limited ResolveSchema error = %v, want ErrResourceLimit", err)
	}
}

func TestFileResolverRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.rng")
	if err := os.WriteFile(outside, []byte(`<empty/>`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.rng")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := (&FileResolver{Root: root}).ResolveSchema(link)
	if err == nil || strings.Contains(err.Error(), "<nil>") {
		t.Errorf("symlink escape error = %v, want refusal", err)
	}
}
