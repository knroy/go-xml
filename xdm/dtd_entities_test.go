package xdm

import (
	"strings"
	"testing"
)

// An internal general entity is expanded. Some schemas need this: the W3C's
// own RFC 3986 type library composes its URI regexes out of fifty entities
// named after the grammar's productions, and without expansion the document is
// simply unparseable.
func TestInternalEntityExpands(t *testing.T) {
	cases := []struct{ src, want string }{
		{`<!DOCTYPE r [<!ENTITY x "hello">]><r>&x;</r>`, "hello"},
		// Nesting is what makes this useful, and what makes it dangerous.
		{`<!DOCTYPE r [<!ENTITY a "1"><!ENTITY b "&a;2">]><r>&b;</r>`, "12"},
		// A character reference inside replacement text is decoded here: text
		// arriving through the decoder's entity map is substituted rather
		// than re-scanned, so "&#65;" would otherwise reach the value whole.
		{`<!DOCTYPE r [<!ENTITY x "&#65;&#x42;">]><r>&x;</r>`, "AB"},
	}
	for _, c := range cases {
		tree, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got := tree.Root.ChildElements()[0].StringValue(); got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// XML §4.2: where an entity is declared more than once the *first* binds.
//
// The RFC 3986 library depends on it — sub-delims is declared three times, the
// first escaped for a regex and the later ones showing the unescaped grammar
// for a human reader. Keeping the last produced a pattern with bare "(" and
// "+" in it, which then failed to compile.
func TestFirstEntityDeclarationWins(t *testing.T) {
	const src = `<!DOCTYPE r [<!ENTITY x "first"><!ENTITY x "second">]><r>&x;</r>`
	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := tree.Root.ChildElements()[0].StringValue(); got != "first" {
		t.Errorf("got %q, want the first declaration", got)
	}
}

// Nothing external is ever fetched. This is the line AllowDOCTYPE exists to
// hold, and expanding internal entities must not move it.
func TestExternalEntitiesStayRefused(t *testing.T) {
	for _, src := range []string{
		`<!DOCTYPE r [<!ENTITY x SYSTEM "file:///etc/passwd">]><r>&x;</r>`,
		`<!DOCTYPE r [<!ENTITY x SYSTEM "http://127.0.0.1/">]><r>&x;</r>`,
		`<!DOCTYPE r [<!ENTITY x PUBLIC "-//id" "file:///etc/passwd">]><r>&x;</r>`,
		// Reached through an internal entity rather than directly.
		`<!DOCTYPE r [<!ENTITY e SYSTEM "file:///etc/passwd"><!ENTITY x "&e;">]><r>&x;</r>`,
	} {
		if _, err := ParseString(src, ParseOptions{AllowDOCTYPE: true}); err == nil {
			t.Errorf("an external entity was resolved: %s", src)
		}
	}
}

// Expansion is bounded, because nesting is exactly how billion-laughs works.
func TestEntityExpansionIsBounded(t *testing.T) {
	// Five levels of ten reaches 100,000 bytes. An earlier 1 MB per-entity cap
	// let this through, which is why the limit is measured against the largest
	// legitimate use (9,569 bytes) rather than picked.
	bomb := `<!DOCTYPE r [
<!ENTITY a "aaaaaaaaaa">
<!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;">
<!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;">
<!ENTITY d "&c;&c;&c;&c;&c;&c;&c;&c;&c;&c;">
<!ENTITY e "&d;&d;&d;&d;&d;&d;&d;&d;&d;&d;">
]><r>&e;</r>`
	if _, err := ParseString(bomb, ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Error("an entity-expansion bomb parsed successfully")
	}

	// A cycle must terminate rather than recurse forever.
	for _, src := range []string{
		`<!DOCTYPE r [<!ENTITY x "&x;">]><r>&x;</r>`,
		`<!DOCTYPE r [<!ENTITY x "&y;"><!ENTITY y "&x;">]><r>&x;</r>`,
	} {
		if _, err := ParseString(src, ParseOptions{AllowDOCTYPE: true}); err == nil {
			t.Errorf("a self-referential entity resolved: %s", src)
		}
	}
}

// A parameter entity is not read: those are expanded inside the DTD itself,
// and interpreting one means treating the subset as a grammar rather than
// scanning it.
func TestParameterEntitiesAreIgnored(t *testing.T) {
	const src = `<!DOCTYPE r [<!ENTITY % p SYSTEM "file:///etc/passwd">%p;]><r>ok</r>`
	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		// Refusing outright is also acceptable.
		return
	}
	if got := tree.Root.ChildElements()[0].StringValue(); got != "ok" {
		t.Errorf("got %q; a parameter entity must not have been expanded", got)
	}
}

// Without AllowDOCTYPE nothing changes: the declaration is refused before any
// of this runs.
func TestEntitiesNeedAllowDOCTYPE(t *testing.T) {
	const src = `<!DOCTYPE r [<!ENTITY x "hello">]><r>&x;</r>`
	_, err := ParseString(src, ParseOptions{})
	if err == nil {
		t.Fatal("a DOCTYPE must be refused by default")
	}
	if !strings.Contains(err.Error(), "DOCTYPE") {
		t.Errorf("error = %v, want it to name the DOCTYPE", err)
	}
}

// The predefined five keep working and are not double-expanded.
func TestPredefinedEntitiesUnaffected(t *testing.T) {
	const src = `<!DOCTYPE r [<!ENTITY x "a&amp;b">]><r>&x;</r>`
	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := tree.Root.ChildElements()[0].StringValue(); got != "a&b" {
		t.Errorf("got %q, want a&b", got)
	}
}
