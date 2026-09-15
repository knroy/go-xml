package main

import (
	"net/url"
	"strings"
	"testing"
)

// TestAbsPathToFileURI covers both platforms' spellings on either platform.
//
// A Windows path is the case that went wrong: filepath.Abs gives C:\dir\s.xsl
// and ToSlash makes it C:/dir/s.xsl, which has no leading slash. url.URL then
// writes file://C:/dir/s.xsl, where C: is the authority rather than the drive
// -- parsing that back gives host "C:" and path "/dir/s.xsl", so the drive
// letter is gone and every URI resolved against it names a file that is not
// there. Saxon writes file:///C:/dir/s.xsl, and so must this.
func TestAbsPathToFileURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/home/u/s.xsl", "file:///home/u/s.xsl"},
		{"C:/Users/marti/s.xsl", "file:///C:/Users/marti/s.xsl"},
		{"C:/dir/a b/s.xsl", "file:///C:/dir/a%20b/s.xsl"},
		{"/mnt/c/a b/s.xsl", "file:///mnt/c/a%20b/s.xsl"},
	}
	for _, c := range cases {
		if got := absPathToFileURI(c.in); got != c.want {
			t.Errorf("absPathToFileURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFileURIRoundTripsThroughURLParse is the property that actually matters:
// the drive letter has to survive being parsed back, and the authority has to
// stay empty. file://C:/dir/s.xsl satisfies neither.
func TestFileURIRoundTripsThroughURLParse(t *testing.T) {
	for _, in := range []string{"/home/u/s.xsl", "C:/Users/marti/s.xsl"} {
		u, err := url.Parse(absPathToFileURI(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if u.Host != "" {
			t.Errorf("%s: host is %q, want empty -- the path was read as an authority", in, u.Host)
		}
		if want := "/" + trimLeadingSlash(in); u.Path != want {
			t.Errorf("%s: path is %q, want %q", in, u.Path, want)
		}
	}
}

func trimLeadingSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}

// TestFileURIIsIdempotent: a value that is already a URI is left alone, so a
// caller that passes one does not get file:///file:/...
func TestFileURIIsIdempotent(t *testing.T) {
	const in = "file:///home/u/s.xsl"
	if got := fileURI(in); got != in {
		t.Errorf("fileURI(%q) = %q, want it unchanged", in, got)
	}
	if got := fileURI(""); got != "" {
		t.Errorf("fileURI(\"\") = %q, want empty", got)
	}
}

// TestDirURISlashRuleOnWindowsShapes exercises the trailing-slash rule against
// Windows-shaped inputs on any host.
//
// dirURI itself cannot be asked this question off Windows: it runs its
// argument through filepath.Abs, and "C:/x" is not absolute on unix, so the
// host's working directory is prepended and the drive letter stops being a
// drive letter. The rule under test is the part *after* Abs -- take an
// absolute slash-separated path, spell it as a file: URI, ensure exactly one
// trailing slash -- so that is what is applied here directly, with the
// drive-letter spellings the CI runner actually produced.
func TestDirURISlashRuleOnWindowsShapes(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"D:/tmp/results", "file:///D:/tmp/results/"},
		{"D:/tmp/results/", "file:///D:/tmp/results/"},
		{"C:/Users/runneradmin/AppData/Local/Temp/001", "file:///C:/Users/runneradmin/AppData/Local/Temp/001/"},
		{"/tmp/results", "file:///tmp/results/"},
		{"/tmp/results/", "file:///tmp/results/"},
	} {
		got := slashedDirURI(c.in)
		if got != c.want {
			t.Errorf("slashedDirURI(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.HasSuffix(got, "//") {
			t.Errorf("slashedDirURI(%q) doubled the trailing slash: %q", c.in, got)
		}
		// The drive letter must survive as part of the path, never as the
		// authority -- that is the defect file://C:/... would reintroduce.
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("slashedDirURI(%q) = %q is not a URI: %v", c.in, got, err)
		}
		if u.Host != "" {
			t.Errorf("slashedDirURI(%q) = %q has authority %q, want empty", c.in, got, u.Host)
		}
	}
}
