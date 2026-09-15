package xslt

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestFileURIToPathHandlesBothPlatforms covers the conversion that used to be
// a textual TrimPrefix.
//
// The Windows spelling is the case that was wrong. file:///C:/dir/s.xsl is the
// RFC 8089 form -- empty authority, drive-letter path -- and stripping
// "file://" from it leaves "/C:/dir/s.xsl", which no filesystem call accepts.
// A percent-escape was not decoded either, so a directory whose name contains
// a space became one containing "%20".
func TestFileURIToPathHandlesBothPlatforms(t *testing.T) {
	cases := []struct {
		in, want string
		// native says the want is a slash-spelled path that fileURIToPath
		// converts to the host separator, so the expectation has to be run
		// through FromSlash before comparing. An input that is not a file:
		// URI is returned byte for byte and must not be, or the test demands
		// "\already\a\path" on Windows from a function that never touched it.
		native bool
	}{
		{in: "file:///home/u/s.xsl", want: "/home/u/s.xsl", native: true},
		{in: "file:///C:/dir/s.xsl", want: "C:/dir/s.xsl", native: true},
		{in: "file:///home/u/a b/s.xsl", want: "/home/u/a b/s.xsl", native: true},
		{in: "file:///C:/dir/a b/s.xsl", want: "C:/dir/a b/s.xsl", native: true},
		// Not a file: URI, so it is returned untouched -- on every platform,
		// separators included. The resolver hands the result to filepath.Abs
		// and filepath.Join, and Windows accepts a forward slash as a
		// separator there, so leaving it alone names the same file.
		{in: "/already/a/path", want: "/already/a/path"},
		{in: "relative.xsl", want: "relative.xsl"},
		{in: `sub\s.xsl`, want: `sub\s.xsl`},
	}
	for _, c := range cases {
		want := c.want
		if c.native {
			want = filepath.FromSlash(want)
		}
		if got := fileURIToPath(c.in); got != want {
			t.Errorf("fileURIToPath(%q) = %q, want %q", c.in, got, want)
		}
	}
}

// TestFileURIToPathWindowsSpellingsOnEveryPlatform pins the parts of the
// conversion that are pure string work, so they are checked on the machine
// running the tests rather than only on Windows CI.
//
// filepath.FromSlash is the identity on Unix, which is what let a wrong
// Windows expectation sit green here and fail there. Comparing against
// ToSlash of the result removes the host separator from the comparison
// entirely: the drive letter, the decoded escape and the dropped leading
// slash are the same on both platforms, and this asserts them on both.
func TestFileURIToPathWindowsSpellingsOnEveryPlatform(t *testing.T) {
	cases := []struct{ in, want string }{
		// The leading slash of the RFC 8089 empty-authority form belongs to
		// the URI, not to the path, and only in front of a drive letter.
		{"file:///C:/dir/s.xsl", "C:/dir/s.xsl"},
		{"file:///home/u/s.xsl", "/home/u/s.xsl"},
		// The percent-escape is decoded, so "a b" stays a directory with a
		// space and does not become one named "a%20b".
		{"file:///C:/dir/a b/s.xsl", "C:/dir/a b/s.xsl"},
	}
	for _, c := range cases {
		if got := filepath.ToSlash(fileURIToPath(c.in)); got != c.want {
			t.Errorf("ToSlash(fileURIToPath(%q)) = %q, want %q", c.in, got, c.want)
		}
	}

	// A non-file: input is returned byte for byte. This is the assertion the
	// Windows build actually failed: it is spelled without FromSlash so it
	// means the same thing on both platforms.
	for _, in := range []string{"/already/a/path", "relative.xsl", `sub\s.xsl`} {
		if got := fileURIToPath(in); got != in {
			t.Errorf("fileURIToPath(%q) = %q, want it returned unchanged", in, got)
		}
	}
}

// TestFileURIOfIsThreeSlashed pins the spelling fileURIOf produces for a path
// that has no leading slash of its own, which is what a Windows absolute path
// looks like after ToSlash. Two slashes would make the drive an authority.
func TestFileURIOfIsThreeSlashed(t *testing.T) {
	if got := fileURIOf("file:///C:/dir/s.xsl"); got != "file:///C:/dir/s.xsl" {
		t.Errorf("fileURIOf left a URI alone as %q", got)
	}
	// A round trip through both helpers has to land back on the same path,
	// whatever the platform spells absolute.
	abs, err := filepath.Abs(filepath.Join("testdata", "s.xsl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fileURIToPath(fileURIOf(abs)); got != abs {
		t.Errorf("round trip: %q -> %q -> %q", abs, fileURIOf(abs), got)
	}
}

// TestTwoSlashFileURILosesTheDriveLetter pins why a base URI must be built
// with fileURIOf rather than by hand.
//
// "file://" + "C:/dir/s.xsl" is the natural-looking mistake, and it is silent:
// url.Parse reads "C:" as the *authority*, so u.Path is "/dir/s.xsl" with the
// drive gone. A relative href joined against that names a drive-less path,
// which filepath.Abs then re-anchors onto whatever drive the process happens
// to be on -- so the file is looked for on D: while the resolver root is on
// C:, and containment refuses a document that is plainly inside the root.
// That is how TestSourceDocumentResolvesAgainstXMLBase failed on Windows CI,
// and this states the cause where it can be checked from any host: neither
// url.Parse nor fileURIToPath consults the filesystem.
func TestTwoSlashFileURILosesTheDriveLetter(t *testing.T) {
	const win = "C:/Users/RUNNER~1/AppData/Local/Temp/001/sub/deep/s.xsl"

	// The mistake: the drive letter does not survive.
	if got := fileURIToPath("file://" + win); got == filepath.FromSlash(win) {
		t.Fatalf("the two-slash form unexpectedly kept the drive: %q", got)
	} else if strings.HasPrefix(got, "C:") {
		t.Errorf("the two-slash form kept %q; the test below is then pointless", got)
	}

	// The three-slash form fileURIOf writes keeps the drive, and it round
	// trips. fileURIOf is not called on this input: it runs filepath.IsAbs,
	// which is false for a C: path off Windows, so the host's working
	// directory would be prepended and the drive letter would stop being one.
	// The spelling it produces for an absolute path is what matters here, and
	// TestFileURIOfIsThreeSlashed pins that it produces it.
	if got, want := fileURIToPath("file:///"+win), filepath.FromSlash(win); got != want {
		t.Errorf("round trip lost the drive: %q -> %q", win, got)
	}
}
