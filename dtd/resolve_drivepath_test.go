package dtd

import (
	"strings"
	"testing"
)

// TestResolvePathAdmitsDrivePathsAndRefusesTheEscape is the dtd half of the
// Windows drive-letter fix.
//
// A system identifier is a URI (XML 1.0 section 4.2.2), and
// url.Parse("C:/dtd/r.dtd") returns Scheme "c" -- so the SSRF gate refusing
// every scheme but "file" refused every absolute path on Windows before
// fileURIToPath, which handles drive letters, ever ran. The trap is that
// "c:///secret.dtd" is indistinguishable from a drive path after url.Parse, so
// the discriminator has to be textual; see internal/uripath.
//
// Only the scheme refusal is asserted -- a drive path admitted past the gate
// on a Unix host is then caught by root confinement, which is correct.
func TestResolvePathAdmitsDrivePathsAndRefusesTheEscape(t *testing.T) {
	r := &FileResolver{Root: t.TempDir()}

	schemeRefused := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "reads local files only")
	}

	for _, c := range []struct {
		name, id string
		want     bool
	}{
		{"backslash drive path", `C:\dtd\r.dtd`, false},
		{"forward drive path", "C:/dtd/r.dtd", false},
		{"lowercase drive", "c:/dtd/r.dtd", false},

		// The escape.
		{"empty-authority escape", "c:///secret.dtd", true},
		{"authority escape", "c://host/secret.dtd", true},

		{"http", "http://example.invalid/r.dtd", true},
		{"https", "https://example.invalid/r.dtd", true},
		{"ftp", "ftp://example.invalid/r.dtd", true},
		{"data", "data:text/xml,<!ELEMENT a EMPTY>", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := r.resolvePath(c.id, "")
			if got := schemeRefused(err); got != c.want {
				t.Fatalf("resolvePath(%q): scheme-refused = %v, want %v (err = %v)",
					c.id, got, c.want, err)
			}
		})
	}
}

// TestResolvePathStillRefusesARemoteFileAuthority pins the neighbouring guard.
func TestResolvePathStillRefusesARemoteFileAuthority(t *testing.T) {
	r := &FileResolver{Root: t.TempDir()}
	_, _, err := r.resolvePath("file://remote-host/etc/passwd", "")
	if err == nil || !strings.Contains(err.Error(), "names the remote host") {
		t.Fatalf("file://remote-host/etc/passwd gave %v, want a remote-host refusal", err)
	}
}

// TestResolvePathConfinesADrivePathAnyway is the point that makes the
// unconditional (non-GOOS-gated) rule safe: admitting a drive path past the
// SCHEME gate does not admit it past the ROOT. Confinement is enforced by
// filepath.Rel against the root, and that is unchanged on every platform.
func TestResolvePathConfinesADrivePathAnyway(t *testing.T) {
	r := &FileResolver{Root: t.TempDir()}
	for _, id := range []string{`C:\Windows\win.ini`, "C:/Windows/win.ini", "c:///secret.dtd"} {
		if _, _, err := r.resolvePath(id, ""); err == nil {
			t.Errorf("resolvePath(%q) was admitted; it is outside the root", id)
		}
	}
}
