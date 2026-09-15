package fileuri

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// TestFromSlashedAbsSpellsBothPlatforms covers the Windows spelling on a host
// that is not Windows, which is the only way this bug is ever caught before
// CI: on darwin and Linux filepath.ToSlash is a no-op and every absolute path
// already starts with a slash, so Of cannot produce the broken form there no
// matter how it is written. Feeding the already-slashed Windows shape to the
// pure half is what makes the platform difference testable anywhere.
func TestFromSlashedAbsSpellsBothPlatforms(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/home/u/s.xsl", "file:///home/u/s.xsl"},
		{"C:/Users/runneradmin/s.xsl", "file:///C:/Users/runneradmin/s.xsl"},
		{"C:/dir/a b/s.xsl", "file:///C:/dir/a%20b/s.xsl"},
		{"/mnt/c/a b/s.xsl", "file:///mnt/c/a%20b/s.xsl"},
	}
	for _, c := range cases {
		if got := FromSlashedAbs(c.in); got != c.want {
			t.Errorf("FromSlashedAbs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWindowsPathKeepsItsDrive is the property the failing Windows CI run was
// really about, stated as what must survive rather than as a spelling.
//
// The base URI reported for a node in C:\...\level1\element.xml was
// "file://C:\\...\\level1\\element.xml". Two slashes make "C:" the URI
// AUTHORITY rather than the drive, and the backslashes are not URI path
// separators at all. Both are asserted here against the two broken spellings
// the hand-written concatenation produced.
func TestWindowsPathKeepsItsDrive(t *testing.T) {
	// What "file://" + filepath.ToSlash(path) produced: two slashes.
	twoSlash := "file://" + "C:/tmp/001/level1/element.xml"
	u, err := url.Parse(twoSlash)
	if err != nil {
		t.Fatalf("parsing %q: %v", twoSlash, err)
	}
	if u.Host != "C:" {
		t.Fatalf("premise wrong: %q parsed with host %q, expected the drive to "+
			"be read as the authority", twoSlash, u.Host)
	}

	// What "file://" + path produced when ToSlash was skipped: not a URI at
	// all. url.Parse reads the backslash run after the host as a port.
	rawBackslash := `file://C:\tmp\001\level1\element.xml`
	if _, err := url.Parse(rawBackslash); err == nil {
		t.Fatalf("premise wrong: %q parsed cleanly; expected url.Parse to "+
			"reject the backslashes", rawBackslash)
	}

	// And what this package produces instead: an empty authority, the drive
	// kept in the path, forward slashes throughout.
	got := FromSlashedAbs("C:/tmp/001/level1/element.xml")
	if got != "file:///C:/tmp/001/level1/element.xml" {
		t.Fatalf("FromSlashedAbs = %q, want file:///C:/tmp/001/level1/element.xml", got)
	}
	v, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parsing %q: %v", got, err)
	}
	if v.Host != "" {
		t.Errorf("host is %q, want empty: the drive was read as an authority", v.Host)
	}
	if v.Path != "/C:/tmp/001/level1/element.xml" {
		t.Errorf("path is %q, want /C:/tmp/001/level1/element.xml", v.Path)
	}
	if strings.Contains(got, `\`) {
		t.Errorf("%q still contains a backslash", got)
	}
}

// TestRelativeReferenceResolvesAgainstTheWindowsBase is why the spelling
// matters rather than being cosmetic. An entity's base URI exists so that
// what the entity references resolves beside it; against the two-slash form
// the drive letter is gone from the result.
func TestRelativeReferenceResolvesAgainstTheWindowsBase(t *testing.T) {
	base, err := url.Parse(FromSlashedAbs("C:/tmp/001/level1/element.xml"))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := url.Parse("sibling.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := base.ResolveReference(ref).String(),
		"file:///C:/tmp/001/level1/sibling.xml"; got != want {
		t.Errorf("resolved to %q, want %q", got, want)
	}
}

// TestToPathInvertsOf on both platforms' spellings. The Windows case is the
// one a textual TrimPrefix gets wrong: it leaves "/C:/dir/s.xsl", which no
// filesystem call accepts, and leaves "%20" where a space belongs.
func TestToPathInvertsOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"file:///home/u/s.xsl", "/home/u/s.xsl"},
		{"file:///C:/dir/s.xsl", "C:/dir/s.xsl"},
		{"file:///C:/dir/a b/s.xsl", "C:/dir/a b/s.xsl"},
		{"file:///C:/dir/a%20b/s.xsl", "C:/dir/a b/s.xsl"},
		// Not a file: URI, so it comes back as a path untouched.
		{"/already/a/path", "/already/a/path"},
		{"relative.xsl", "relative.xsl"},
	}
	for _, c := range cases {
		want := filepath.FromSlash(c.want)
		if got := ToPath(c.in); got != want {
			t.Errorf("ToPath(%q) = %q, want %q", c.in, got, want)
		}
	}
}

// TestOfIsIdempotent: a value that is already a URI is left alone, so a caller
// that passes one does not get file:///file:/...
func TestOfIsIdempotent(t *testing.T) {
	const in = "file:///home/u/s.xsl"
	if got := Of(in); got != in {
		t.Errorf("Of(%q) = %q, want it unchanged", in, got)
	}
	if got := Of(""); got != "" {
		t.Errorf("Of(\"\") = %q, want empty", got)
	}
}

// TestOfRoundTripsOnThisPlatform ties the pure half back to the real one:
// whatever this platform spells absolute has to survive Of and ToPath.
func TestOfRoundTripsOnThisPlatform(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("testdata", "a b", "s.xsl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ToPath(Of(abs)); got != abs {
		t.Errorf("round trip: %q -> %q -> %q", abs, Of(abs), got)
	}
}

// TestDirEndsInASlash, because a relative reference resolved against a
// directory without one names a sibling of that directory rather than a file
// inside it.
func TestDirEndsInASlash(t *testing.T) {
	// The slash itself is all Dir adds, and that much is host-independent.
	if got := Dir(filepath.FromSlash("/srv/schemas")); !strings.HasSuffix(got, "/") {
		t.Errorf("Dir = %q, want a trailing slash", got)
	}

	// The RESOLUTION is asserted through FromSlashedAbs rather than Dir,
	// because Dir goes through Of, and Of calls filepath.Abs. A path that is
	// absolute on Unix is RELATIVE on Windows -- "/srv/schemas" has no drive
	// -- so Abs prepends the process's current drive and the answer there is
	// "file:///D:/srv/schemas/main.rng". Asserting the Unix spelling made
	// this test fail on Windows CI for the same reason the bugs it guards
	// against were invisible on darwin: the host decides, and the assertion
	// pretended it did not. FromSlashedAbs takes the spelling already made,
	// so both platforms' answers can be checked from either.
	for _, c := range []struct{ dir, want string }{
		{"/srv/schemas", "file:///srv/schemas/main.rng"},
		{"C:/srv/schemas", "file:///C:/srv/schemas/main.rng"},
		{"D:/srv/schemas", "file:///D:/srv/schemas/main.rng"},
	} {
		base, err := url.Parse(FromSlashedAbs(c.dir) + "/")
		if err != nil {
			t.Fatalf("%s: %v", c.dir, err)
		}
		ref, _ := url.Parse("main.rng")
		if got := base.ResolveReference(ref).String(); got != c.want {
			t.Errorf("resolved against %q: got %q, want %q", c.dir, got, c.want)
		}
	}
}

// TestOfConvertsBackslashesOnEveryPlatform is the half of the fix that darwin
// could otherwise not see at all.
//
// filepath.ToSlash is a no-op where the separator is already "/", so a call
// site that simply forgets it behaves identically on darwin and Linux and
// differs only on Windows — which is how the unconverted separator reached CI
// in the first place, and why removing the conversion must fail a test HERE
// rather than three weeks later in a Windows run. Of converts unconditionally,
// so this holds on every platform.
func TestOfConvertsBackslashesOnEveryPlatform(t *testing.T) {
	// A Windows absolute path, fed to Of on whatever platform is running.
	// The backslash must come out as a URI path SEPARATOR, not merely as an
	// escaped character: url.URL will happily write "%5C" for a backslash it
	// is handed, and "file:///C:/dir%5Cs.xsl" is a single path segment naming
	// a file that does not exist, not a file in a directory. Asserting the
	// absence of a bare "\\" is therefore not enough -- %5C passes that and is
	// still wrong -- so the assertion is on the segments.
	const in = `C:\dir\sub\s.xsl`
	got := Of(in)
	if strings.Contains(got, "%5C") || strings.Contains(got, "%5c") {
		t.Fatalf("Of(%q) = %q: the backslash was escaped as a path character "+
			"instead of converted to a separator", in, got)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("Of(%q) = %q, which does not parse as a URI: %v", in, got, err)
	}
	if !strings.HasSuffix(u.Path, "/dir/sub/s.xsl") {
		t.Errorf("Of(%q) = %q, whose path is %q; want it to end in "+
			"/dir/sub/s.xsl with the backslashes converted", in, got, u.Path)
	}
}

// TestOnHostKeepsHostAndPathSeparate is the assertion the four hostile-href
// tests were making by hand and getting wrong on Windows.
//
// A test that asserts a foreign host is refused has to actually NAME that
// host. Concatenating a Windows path onto "file://evil.example.com" fuses the
// drive onto the authority, so the URI names "evil.example.comC:" -- a host
// the test never wrote, and the assertion on the host name fails.
func TestOnHostKeepsHostAndPathSeparate(t *testing.T) {
	cases := []struct{ host, path, want string }{
		{"evil.example.com", "/tmp/x/s.xsd", "file://evil.example.com/tmp/x/s.xsd"},
		{"evil.example.com", "C:/Users/r/s.xsd", "file://evil.example.com/C:/Users/r/s.xsd"},
		{"evil.example.com", `C:\Users\r\s.xsd`, "file://evil.example.com/C:/Users/r/s.xsd"},
		{"localhost", "C:/Users/r/s.xsd", "file://localhost/C:/Users/r/s.xsd"},
	}
	for _, c := range cases {
		got := OnHost(c.host, c.path)
		if got != c.want {
			t.Errorf("OnHost(%q, %q) = %q, want %q", c.host, c.path, got, c.want)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Errorf("OnHost(%q, %q) = %q, which does not parse: %v",
				c.host, c.path, got, err)
			continue
		}
		if u.Host != c.host {
			t.Errorf("OnHost(%q, %q) = %q, whose host is %q: the path fused "+
				"onto the authority", c.host, c.path, got, u.Host)
		}
	}
}

// TestOnHostBeatsTheConcatenation states the defect directly, so that going
// back to the concatenation fails here rather than in Windows CI.
func TestOnHostBeatsTheConcatenation(t *testing.T) {
	const host, win = "evil.example.com", "C:/Users/r/s.xsd"

	// The premise: what the hand-written form produces on Windows.
	fused := "file://" + host + win
	u, err := url.Parse(fused)
	if err != nil {
		t.Fatalf("premise wrong: %q did not parse: %v", fused, err)
	}
	if u.Host != "evil.example.comC:" {
		t.Fatalf("premise wrong: %q parsed with host %q, expected the drive "+
			"to have fused onto the authority", fused, u.Host)
	}

	// And what OnHost produces instead.
	v, err := url.Parse(OnHost(host, win))
	if err != nil {
		t.Fatalf("OnHost did not parse: %v", err)
	}
	if v.Host != host {
		t.Errorf("host is %q, want %q", v.Host, host)
	}
	if v.Path != "/C:/Users/r/s.xsd" {
		t.Errorf("path is %q, want /C:/Users/r/s.xsd", v.Path)
	}
}

// TestOfIsParseableWhereConcatenationIsNot covers the empty-host form of the
// same fusion, which is what the three xslt refusal tests were building.
//
// "file://" + a Windows path is not merely a URI with the wrong host: with
// backslashes it is not a URI at all. A resolver that refuses foreign hosts
// and non-file schemes by inspecting the PARSED url sees nothing to inspect,
// so the refusal such a test observes comes from some later check.
func TestOfIsParseableWhereConcatenationIsNot(t *testing.T) {
	const win = `C:\Users\r\outside\secret.txt`

	if _, err := url.Parse("file://" + win); err == nil {
		t.Fatalf("premise wrong: %q parsed cleanly; expected url.Parse to "+
			"reject the backslashes", "file://"+win)
	}
	// Forward slashes parse, but make the drive the authority.
	slashed := "file://" + ToSlash(win)
	u, err := url.Parse(slashed)
	if err != nil {
		t.Fatalf("premise wrong: %q did not parse: %v", slashed, err)
	}
	if u.Host != "C:" {
		t.Fatalf("premise wrong: %q parsed with host %q, want the drive read "+
			"as the authority", slashed, u.Host)
	}

	v, err := url.Parse(Of(win))
	if err != nil {
		t.Fatalf("Of(%q) = %q does not parse: %v", win, Of(win), err)
	}
	if v.Host != "" {
		t.Errorf("Of(%q) has host %q, want empty", win, v.Host)
	}
	if v.Scheme != "file" {
		t.Errorf("Of(%q) has scheme %q, want file", win, v.Scheme)
	}
}
