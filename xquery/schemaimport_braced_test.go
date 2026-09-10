package xquery_test

import (
	"testing"
)

// TestBracedTypeNameSeesTheSchema asserts that a type written in the XPath 3.0
// braced form Q{uri}local resolves against the imported schema exactly as the
// same type written with a prefix does.
//
// The two spellings denote the same expanded QName, so every question asked of
// one must get the answer the other gets -- no rule anywhere distinguishes
// them. The lexer rewrites Q{uri}local into a synthetic prefix and wraps the
// resolver to answer it, and that wrapper had been carrying only one of the
// six schema interfaces the static context answers. A pure union asked about
// through the braced spelling therefore reported XPST0051, "only a pure union
// type is an item type", about a union that is pure. CastAs-UnionType-29 and
// -30 are the suite's cases.
//
// The rows are written as pairs so the assertion is "the two spellings agree",
// which is the actual rule, rather than a snapshot of either one's answer.
func TestBracedTypeNameSeesTheSchema(t *testing.T) {
	const q = "Q{http://example.org/unions}"
	for _, c := range []struct{ prefixed, braced string }{
		// A pure union in an ItemType position: legal, and matching is by
		// member type.
		{`xs:date("2001-01-01") instance of u:approxDate`,
			`xs:date("2001-01-01") instance of ` + q + `approxDate`},
		{`"x" instance of u:approxDate`,
			`"x" instance of ` + q + `approxDate`},
		{`() instance of u:approxDate*`,
			`() instance of ` + q + `approxDate*`},
		// An atomic schema type, and the facets it carries.
		{`u:sizeType(5) instance of xs:integer`,
			q + `sizeType(5) instance of xs:integer`},
		{`u:sizeType(5) instance of u:sizeType`,
			q + `sizeType(5) instance of ` + q + `sizeType`},
		// A list type, and an impure union: both still refused in an
		// ItemType position by BOTH spellings, which is the boundary the fix
		// must not cross.
		{`"1.5" castable as u:decimals`, `"1.5" castable as ` + q + `decimals`},
		{`xs:date("2001-01-01") castable as u:impure`,
			`xs:date("2001-01-01") castable as ` + q + `impure`},
	} {
		gotP, errP := run(t, unionQuery(c.prefixed), withUnions())
		gotB, errB := run(t, unionQuery(c.braced), withUnions())
		if (errP == nil) != (errB == nil) {
			t.Errorf("the two spellings disagree on legality:\n  %s -> %v\n  %s -> %v",
				c.prefixed, errP, c.braced, errB)
			continue
		}
		if errP != nil {
			continue
		}
		if gotP != gotB {
			t.Errorf("the two spellings disagree:\n  %s -> %s\n  %s -> %s",
				c.prefixed, gotP, c.braced, gotB)
		}
	}
	// The pure union really is usable as an item type through the braced
	// spelling, and really does match -- a positive fact, so that the
	// agreement above cannot be satisfied by both spellings failing.
	for _, c := range []struct{ body, want string }{
		{`xs:date("2001-01-01") instance of ` + q + `approxDate`, "true"},
		{`"x" instance of ` + q + `approxDate`, "false"},
		{q + `sizeType(5) instance of ` + q + `sizeType`, "true"},
	} {
		got, err := run(t, unionQuery(c.body), withUnions())
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.body, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.body, got, c.want)
		}
	}
}
