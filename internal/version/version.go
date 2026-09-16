// Package version holds the go-xml module's release version.
//
// It is here, not in xslt, because it is the MODULE's version: xsd, relaxng,
// xpath and xdm are released under the same tag and are equally entitled to
// ask. A constant in xslt would have made every one of them import the
// heaviest package in the repository to learn what release it is part of, and
// would have said, falsely, that the version belongs to XSLT.
//
// Internal rather than exported, because the version is not part of the API.
// It answers fn:system-property('xsl:product-version'), which is an XSLT
// contract; making it public would add something every future release has to
// keep working, to no one's benefit.
package version

import "strings"

// Version is the release of go-xml this source tree is. It is what
// fn:system-property('xsl:product-version') answers.
//
// EDITED BY HAND, at release time, as the first step of the procedure in
// RELEASE.md: bump this, rename the CHANGELOG.md heading from Unreleased to
// the same version, commit, then push the tag. The constant is the source of
// truth and the tag follows it -- release.yml refuses a tag that disagrees
// with this line, rather than amending the commit to match, so a mistake
// fails loudly instead of rewriting history.
//
// A hand-edited constant is what this property used to be, and it said "0.1"
// through the 1.0, 1.1 and 1.2 releases. That history is the obvious argument
// against doing it again, so it is worth being exact about what actually
// failed: not that a human typed the value, but that NOTHING CHECKED IT. A
// wrong constant and a right one were indistinguishable to the build, so the
// wrong one survived three tags and was found in the field.
//
// Two checks close that, and they are why this is safe now:
//
//   - TestVersionIsReleasedAndDescribed, in every CI run on every push and
//     pull request: this is a release triple, and CHANGELOG.md has a section
//     heading for exactly it. A constant claiming a version the changelog
//     does not describe -- including one still sitting under "Unreleased" --
//     fails the build. It needs no tags, so it genuinely runs in a shallow
//     actions/checkout rather than skipping.
//   - release.yml, on the tag push: the tag must equal this constant.
//
// Between them, the constant cannot drift ahead of the changelog or behind
// the tags without a red build. That is the check the original constant never
// had.
const Version = "1.3.1"

// IsReleaseTriple reports whether v is N.N.N with no empty component.
//
// It lives beside the constant because it is the rule about what the constant
// may be, not an XSLT detail. TestVersionIsReleasedAndDescribed applies it to
// Version directly, so a version typed in a shape Go accepts and a package
// version does not fails CI rather than reaching a stylesheet: xslt writes
// this value into xsl:package/@package-version via a replace() that strips
// everything but digits and dots, so a pre-release suffix does not merely
// look untidy -- its digits splice onto the triple.
func IsReleaseTriple(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
