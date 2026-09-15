package xslt

import (
	"strings"
	"testing"
)

// TestResolvePathAdmitsDrivePathsAndStillRefusesTheEscape covers the Windows
// CI failure and the trap that comes with fixing it.
//
// The failure: url.Parse(`C:\Users\x\s.xsl`) returns Scheme "c", so the guard
// that refuses every scheme but "file" refused every absolute path on Windows
// with `scheme "c" is not permitted (only local files)` -- before
// fileURIToPath, which handles drive letters correctly, ever ran.
//
// The trap: "c:///secret.xsl" is textually a one-letter scheme too, and is
// IDENTICAL to "C:/Users/x" after url.Parse. A fix reading "a single-letter
// scheme is a drive letter" would pass Windows CI and reopen a confinement
// hole. The escape must stay refused, and that is what the second half of this
// table asserts.
//
// This runs on every platform: the scheme decision is unconditional, so the
// Windows-shaped inputs are decided the same way here as on Windows. Only the
// SCHEME refusal is asserted -- a drive path admitted by the scheme guard on a
// Unix host still fails later at "no such file", which is correct and is not
// what is under test.
func TestResolvePathAdmitsDrivePathsAndStillRefusesTheEscape(t *testing.T) {
	r := &FileResolver{}

	// schemeRefused reports whether err is the scheme guard talking, as
	// opposed to any later refusal such as containment or ENOENT.
	schemeRefused := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "is not permitted (only local files)")
	}

	for _, c := range []struct {
		name, href string
		// want is true when the SCHEME guard must fire.
		want bool
	}{
		{"backslash drive path", `C:\Users\x\s.xsl`, false},
		{"forward drive path", "C:/Users/x/s.xsl", false},
		{"lowercase drive", "c:/users/x/s.xsl", false},
		{"bare drive root", `C:\`, false},

		// The escape. An empty authority plus a rooted path is a URI, not a
		// drive path, and admitting it would hand "/secret.xsl" to the
		// filesystem from outside any root.
		{"empty-authority escape", "c:///secret.xsl", true},
		{"authority escape", "c://host/secret.xsl", true},

		// Unchanged refusals.
		{"http", "http://example.invalid/s.xsl", true},
		{"https", "https://example.invalid/s.xsl", true},
		{"ftp", "ftp://example.invalid/s.xsl", true},
		{"data", "data:text/xml,<a/>", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := r.resolvePath(c.href, "")
			if got := schemeRefused(err); got != c.want {
				t.Fatalf("resolvePath(%q): scheme-refused = %v, want %v (err = %v)",
					c.href, got, c.want, err)
			}
		})
	}
}

// TestResolvePathStillRefusesARemoteFileAuthority pins the neighbouring guard,
// which the drive-letter change must not have weakened: a file: URI naming a
// host is a remote read, and dropping the authority would silently read the
// same-named local file instead.
func TestResolvePathStillRefusesARemoteFileAuthority(t *testing.T) {
	r := &FileResolver{}
	_, err := r.resolvePath("file://remote-host/etc/passwd", "")
	if err == nil || !strings.Contains(err.Error(), "names the remote host") {
		t.Fatalf("file://remote-host/etc/passwd gave %v, want a remote-host refusal", err)
	}
}

// TestResolvePathLeavesUnixPathsAlone asserts the change is inert on the
// platform it was not written for: a Unix absolute path has no colon at index
// 1, so it never reaches the drive-letter branch at all.
func TestResolvePathLeavesUnixPathsAlone(t *testing.T) {
	for _, href := range []string{"/etc/passwd", "sub/s.xsl", "../s.xsl"} {
		_, err := (&FileResolver{}).resolvePath(href, "")
		if err != nil && strings.Contains(err.Error(), "is not permitted") {
			t.Errorf("resolvePath(%q) was refused by a scheme or host guard: %v", href, err)
		}
	}
}
