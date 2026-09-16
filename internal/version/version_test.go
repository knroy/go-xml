package version

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestVersionIsReleasedAndDescribed is the check that makes a hand-edited
// Version safe where the previous hand-edited constant was not.
//
// fn:system-property('xsl:product-version') was a constant once and answered
// "0.1" through the 1.0, 1.1 and 1.2 releases. The failure was not that a
// human typed it -- it was that nothing in the build could tell a right value
// from a wrong one, so the wrong one survived three tags and was reported
// from the field. This is that missing check.
//
// It asserts the two things that are true WITHOUT a tag, deliberately:
//
//  1. Version is an N.N.N release triple. The value is written into
//     xsl:package/@package-version by QT3 case package-version-010, through a
//     replace() that strips everything but digits and dots, so a pre-release
//     suffix is not untidy but invalid -- its digits splice onto the triple.
//  2. CHANGELOG.md has a section heading for exactly that version. A constant
//     claiming v1.4.0 while the changelog still says "## Unreleased" is a
//     constant that has run ahead of its own release notes, and the release
//     workflow would then build a GitHub release with no body.
//
// Neither needs git tags, which is the point. A check that reads `git
// describe` cannot run under actions/checkout's default shallow, tagless
// clone; it would skip, and a guard that skips in CI is a guard that does not
// run where running is automatic. This one has no such failure mode and needs
// no fetch-depth setting.
//
// What it cannot see is a tag that disagrees with the constant, because on an
// ordinary commit there is no tag to disagree. release.yml covers that, on
// the tag push, by refusing to release a tag that is not this constant.
func TestVersionIsReleasedAndDescribed(t *testing.T) {
	// The triple shape is checked here, on the constant itself, rather than
	// left to xslt: this package owns the value, so it owns the rule about
	// what shape it may take. xslt's own test covers the other half -- that
	// the property actually answers this constant.
	if !IsReleaseTriple(Version) {
		t.Fatalf("version.Version is %q, which is not an N.N.N release triple. "+
			"fn:system-property('xsl:product-version') would answer 0.0.0, and "+
			"package-version-010 writes that property into "+
			"xsl:package/@package-version.", Version)
	}

	// Two levels up: this package is internal/version, the changelog is at
	// the module root.
	path := filepath.Join("..", "..", "CHANGELOG.md")
	b, err := os.ReadFile(path)
	if err != nil {
		// Not a skip. The changelog is committed at the module root, so a
		// read failure means the tree is not the tree, and quietly passing on
		// it is the shape of the original defect: a check that can stop
		// checking without saying so.
		t.Fatalf("cannot read %s, so the constant %q is unverified: %v", path, Version, err)
	}
	// The convention is "## v1.3.0 — 2026-09-14", with an em dash. The date is
	// not matched: it is not derivable here and pinning it would make this
	// test a second place to edit on release day. The heading's EXISTENCE is
	// what says the version has release notes.
	head := regexp.MustCompile(`(?m)^## v` + regexp.QuoteMeta(Version) + `\b`)
	if !head.Match(b) {
		t.Errorf("version.Version is %q but CHANGELOG.md has no \"## v%s\" section.\n"+
			"Rename the \"## Unreleased\" heading to \"## v%s — <date>\", or correct "+
			"the constant. The constant is the source of truth for the release, "+
			"and release.yml builds the GitHub release body from that section: "+
			"without it the release ships empty notes.",
			Version, Version, Version)
	}
	// An "## Unreleased" section ABOVE this version's heading is fine and
	// normal: it is the next cycle's notes accumulating after a release. What
	// is not fine is one BELOW it, or one with no "## v<Version>" heading at
	// all -- that is the release heading itself never renamed, which is what
	// happened at v1.2.2, whose tagged CHANGELOG.md still led with
	// "## Unreleased". The version-heading check above already catches the
	// no-heading case; this catches the ordering.
	//
	// Matching by byte offset rather than by parsing: there is exactly one of
	// each heading, and the question is only which comes first.
	if loc := regexp.MustCompile(`(?mi)^##\s+unreleased\b`).FindIndex(b); loc != nil {
		if h := head.FindIndex(b); h != nil && loc[0] > h[0] {
			t.Errorf("CHANGELOG.md has an \"## Unreleased\" heading BELOW the "+
				"\"## v%s\" section. Newest first is the file's order, so an "+
				"Unreleased section under a released one means the release "+
				"heading was never renamed -- which is how v1.2.2 was tagged "+
				"with \"## Unreleased\" still at the top.", Version)
		}
	}
}

// TestIsReleaseTriple pins the filter, including the forms that reached it
// from a real build back when the version was read from build info: a
// pseudo-version, a dirty working tree, and the two values
// debug.ReadBuildInfo gives when there is no module version at all.
func TestIsReleaseTriple(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"1.2.0", true},
		{"0.0.0", true},
		{"10.20.30", true},
		{"1.2", false},
		{"1.2.3.4", false},
		{"1.2.", false},
		{"", false},
		{"(devel)", false},
		{"1.2.1-0.20260902200715-20286fbc88af", false},
		{"unreleased", false},
	} {
		if got := IsReleaseTriple(c.in); got != c.want {
			t.Errorf("IsReleaseTriple(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
