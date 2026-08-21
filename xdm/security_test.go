package xdm

import (
	"strings"
	"testing"
)

// A DOCTYPE is the entry point for XXE and for entity-expansion blowup, so it
// is rejected unless the caller opts in. These pin both layers of the defence:
// the DOCTYPE gate itself, and the fact that custom entities are still not
// expanded even once the gate is opened.
func TestDoctypeIsRejectedByDefault(t *testing.T) {
	const xxe = `<?xml version="1.0"?>
<!DOCTYPE r [ <!ENTITY xxe SYSTEM "file:///etc/passwd"> ]>
<r>&xxe;</r>`

	_, err := ParseString(xxe, ParseOptions{})
	if err == nil {
		t.Fatal("a DOCTYPE was accepted by default")
	}
	if !strings.Contains(err.Error(), "DOCTYPE") {
		t.Errorf("error = %v, want it to name the DOCTYPE", err)
	}
}

// Opting into DOCTYPE must not also opt into entity expansion: the parser
// allows only the five predefined entities, so an external-entity reference
// fails rather than reading a file.
func TestExternalEntityIsNotExpanded(t *testing.T) {
	const xxe = `<?xml version="1.0"?>
<!DOCTYPE r [ <!ENTITY xxe SYSTEM "file:///etc/passwd"> ]>
<r>&xxe;</r>`

	tree, err := ParseString(xxe, ParseOptions{AllowDOCTYPE: true})
	if err == nil {
		// If it ever parses, it must at least not contain file contents.
		if got := tree.Root.StringValue(); strings.Contains(got, "root:") {
			t.Fatalf("external entity was expanded: %q", got)
		}
		t.Fatalf("external entity reference parsed without error: %q", tree.Root.StringValue())
	}
}

// The billion-laughs expansion must not be attempted at all.
func TestEntityExpansionBlowupIsRefused(t *testing.T) {
	const bomb = `<?xml version="1.0"?>
<!DOCTYPE r [
<!ENTITY a "aaaaaaaaaa">
<!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;">
<!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;">
<!ENTITY d "&c;&c;&c;&c;&c;&c;&c;&c;&c;&c;">
<!ENTITY e "&d;&d;&d;&d;&d;&d;&d;&d;&d;&d;">
<!ENTITY f "&e;&e;&e;&e;&e;&e;&e;&e;&e;&e;">
]>
<r>&f;</r>`

	if _, err := ParseString(bomb, ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Error("an entity-expansion bomb parsed successfully")
	}
}

// The predefined entities must keep working, or the defence is a denial.
func TestPredefinedEntitiesStillWork(t *testing.T) {
	tree, err := ParseString(`<r>&lt;&amp;&gt;&quot;&apos;</r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Root.StringValue(); got != `<&>"'` {
		t.Errorf("predefined entities gave %q, want %q", got, `<&>"'`)
	}
}

// Deeply nested input is the cheapest route to stack exhaustion, so the parser
// bounds it rather than letting the recursion run.
func TestNestingDepthIsBounded(t *testing.T) {
	var b strings.Builder
	const depth = DefaultMaxDepth + 100
	b.WriteString("<r>")
	for i := 0; i < depth; i++ {
		b.WriteString("<d>")
	}
	for i := 0; i < depth; i++ {
		b.WriteString("</d>")
	}
	b.WriteString("</r>")

	if _, err := ParseString(b.String(), ParseOptions{}); err == nil {
		t.Errorf("input nested %d deep was accepted; the limit is %d",
			depth, DefaultMaxDepth)
	}
	// A document inside the limit must still parse.
	if _, err := ParseString("<r><a><b><c/></b></a></r>", ParseOptions{}); err != nil {
		t.Errorf("ordinary nesting was refused: %v", err)
	}
}
