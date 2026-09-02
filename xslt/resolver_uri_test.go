package xslt

import (
	"path/filepath"
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
	cases := []struct{ in, want string }{
		{"file:///home/u/s.xsl", "/home/u/s.xsl"},
		{"file:///C:/dir/s.xsl", "C:/dir/s.xsl"},
		{"file:///home/u/a b/s.xsl", "/home/u/a b/s.xsl"},
		{"file:///C:/dir/a b/s.xsl", "C:/dir/a b/s.xsl"},
		// Not a file: URI, so it is returned untouched.
		{"/already/a/path", "/already/a/path"},
		{"relative.xsl", "relative.xsl"},
	}
	for _, c := range cases {
		want := filepath.FromSlash(c.want)
		if got := fileURIToPath(c.in); got != want {
			t.Errorf("fileURIToPath(%q) = %q, want %q", c.in, got, want)
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
