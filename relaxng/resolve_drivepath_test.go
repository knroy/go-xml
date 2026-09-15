package relaxng

import (
	"strings"
	"testing"
)

// TestResolveSchemeGuardAdmitsDrivePathsAndRefusesTheEscape is the relaxng half
// of the Windows drive-letter fix.
//
// url.Parse(`C:\Users\x\big.rng`) returns Scheme "c", so the guard refusing
// every scheme but "file" refused every absolute path on Windows. The trap is
// that "c:///secret.rng" carries the same one-letter scheme and is identical
// to a drive path after url.Parse, so the fix cannot key on the scheme alone.
//
// Only the scheme refusal is asserted. On a Unix host a drive path is admitted
// past the guard and then fails at open time, which is correct and not what is
// under test.
func TestResolveSchemeGuardAdmitsDrivePathsAndRefusesTheEscape(t *testing.T) {
	r := &FileResolver{}

	schemeRefused := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "is not permitted")
	}

	for _, c := range []struct {
		name, href string
		want       bool
	}{
		{"backslash drive path", `C:\Users\x\big.rng`, false},
		{"forward drive path", "C:/Users/x/big.rng", false},
		{"lowercase drive", "c:/users/x/big.rng", false},

		// The escape: an empty authority plus a rooted path.
		{"empty-authority escape", "c:///secret.rng", true},
		{"authority escape", "c://host/secret.rng", true},

		{"http", "http://example.invalid/s.rng", true},
		{"https", "https://example.invalid/s.rng", true},
		{"ftp", "ftp://example.invalid/s.rng", true},
		{"data", "data:text/xml,<empty/>", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := r.ResolveSchema(c.href)
			if got := schemeRefused(err); got != c.want {
				t.Fatalf("ResolveSchema(%q): scheme-refused = %v, want %v (err = %v)",
					c.href, got, c.want, err)
			}
		})
	}
}

// TestResolveStillRefusesARemoteFileAuthority pins the neighbouring guard the
// drive-letter change must not weaken.
func TestResolveStillRefusesARemoteFileAuthority(t *testing.T) {
	_, err := (&FileResolver{}).ResolveSchema("file://remote-host/etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "names the remote host") {
		t.Fatalf("file://remote-host/etc/passwd gave %v, want a remote-host refusal", err)
	}
}

// TestResolveLeavesUnixPathsAlone asserts the change is inert for references
// that were never drive paths.
func TestResolveLeavesUnixPathsAlone(t *testing.T) {
	for _, href := range []string{"/etc/passwd", "sub/s.rng", "../s.rng"} {
		_, err := (&FileResolver{}).ResolveSchema(href)
		if err != nil && strings.Contains(err.Error(), "is not permitted") {
			t.Errorf("ResolveSchema(%q) was refused by a scheme or host guard: %v", href, err)
		}
	}
}
