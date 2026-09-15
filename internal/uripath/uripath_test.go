package uripath

import (
	"net/url"
	"testing"
)

// TestIsDriveLetterPath is the whole security argument for this package in one
// table. It runs on every platform, because the rule is unconditional: the
// Windows-shaped inputs below are decided identically on a Unix host, which is
// what makes the rule reviewable without a Windows machine.
func TestIsDriveLetterPath(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
		why  string
	}{
		// Drive paths. These are what Windows CI hands the resolvers, and
		// refusing them is the defect this package exists to fix.
		{`C:\Users\x\big.rng`, true, "backslash drive path"},
		{`C:/Users/x/big.rng`, true, "forward-slash drive path"},
		{`c:/users/x`, true, "lowercase drive letter"},
		{`Z:\x`, true, "any letter is a drive"},
		{`C:\`, true, "bare drive root"},
		{`C:/`, true, "bare drive root, forward"},
		{`C:`, true, "drive with no path"},
		{`C:rel\x`, true, "drive-relative path is a real Windows spelling"},

		// THE ESCAPE. "c:///secret.rng" parses to Scheme "c", Host "", Path
		// "/secret.rng" -- indistinguishable from "C:/Users/x" after
		// url.Parse. Admitting it would let a rooted path outside any grant
		// reach the filesystem. It must stay refused.
		{`c:///secret.rng`, false, "empty authority plus rooted path: the escape"},
		{`C:///secret.rng`, false, "same escape, uppercase"},
		{`c://secret.rng`, false, "non-empty authority"},
		{`c://host/etc/passwd`, false, "authority naming a host"},

		// Ordinary schemes, which were already refused and must remain so.
		{"http://example.invalid/s.rng", false, "http"},
		{"https://example.invalid/s.rng", false, "https"},
		{"ftp://example.invalid/s.rng", false, "ftp"},
		{"file:///C:/x", false, "file: URI is handled by fileURIToPath, not here"},
		{"file://remote-host/etc/passwd", false, "remote file authority"},
		{"data:text/xml,<empty/>", false, "data URI"},

		// Unix and relative references, which must be unaffected.
		{"/etc/passwd", false, "unix absolute has no colon at index 1"},
		{"sub/x.rng", false, "relative"},
		{"../secret.rng", false, "traversal stays a traversal"},
		{"", false, "empty"},
		{":", false, "colon alone"},
		{":/x", false, "colon first"},
		{"1:/x", false, "a digit is not a drive letter"},
		{"ab:/x", false, "two-letter scheme"},
	} {
		if got := IsDriveLetterPath(c.in); got != c.want {
			t.Errorf("IsDriveLetterPath(%q) = %v, want %v (%s)", c.in, got, c.want, c.why)
		}
	}
}

// TestDrivePathAndEscapeAreIdenticalAfterParse pins the premise the rule rests
// on. If a future Go release made url.Parse separate these two, the textual
// rule could be replaced by a parse-based one -- and this test is what would
// tell us. Until then, any fix that inspects only the *url.URL is unsound.
func TestDrivePathAndEscapeAreIdenticalAfterParse(t *testing.T) {
	drive, err := url.Parse("C:/Users/x")
	if err != nil {
		t.Fatal(err)
	}
	escape, err := url.Parse("c:///Users/x")
	if err != nil {
		t.Fatal(err)
	}
	if drive.Scheme != escape.Scheme || drive.Host != escape.Host ||
		drive.Path != escape.Path || drive.Opaque != escape.Opaque {
		t.Skipf("url.Parse now separates a drive path from an empty-authority "+
			"URI (drive=%#v escape=%#v); the textual rule could be revisited",
			drive, escape)
	}
	if IsDriveLetterPath("C:/Users/x") == IsDriveLetterPath("c:///Users/x") {
		t.Fatal("the two are identical after url.Parse, so IsDriveLetterPath " +
			"must be what separates them, and it does not")
	}
}
