package relaxng

import (
	"strings"
	"testing"
)

// An error raised inside an included grammar must name the file it came from.
//
// Issue #4's shape: the construct that is wrong is in lib.rng, but the message
// described only the construct, so the reader went looking in the schema they
// had written — which is clean — with nothing in the text to suggest the
// failure was a level down.
func TestSyntaxErrorInsideIncludeNamesTheIncludedFile(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"lib.rng": `<grammar` + rngNS + `><define name="inner">
			<zeroOrMore1><element name="x"><text/></element></zeroOrMore1>
			</define></grammar>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`>
		<include href="lib.rng"/>
		<start><ref name="inner"/></start></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("a bogus element inside an included grammar should be refused")
	}
	// The identity of the complaint survives.
	if !strings.Contains(err.Error(), "zeroOrMore1") {
		t.Errorf("error = %v, should still name the offending element", err)
	}
	if !strings.Contains(err.Error(), "lib.rng") {
		t.Errorf("error = %v, should name lib.rng as the document the error "+
			"came from; without it the reader hunts through the schema they "+
			"wrote, which is correct", err)
	}
}

// The same for the restriction pass, which is a separate traversal and so a
// separate call site.
func TestRestrictionErrorInsideIncludeNamesTheIncludedFile(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"lib.rng": `<grammar` + rngNS + `><define name="inner">
			<element name="x"><attribute name="a">
				<attribute name="b"><text/></attribute>
			</attribute></element></define></grammar>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`>
		<include href="lib.rng"/>
		<start><ref name="inner"/></start></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("an attribute inside an attribute should be refused")
	}
	if !strings.Contains(err.Error(), "lib.rng") {
		t.Errorf("error = %v, should name lib.rng", err)
	}
}

// And through <externalRef>, which is the other nested fetch.
func TestSyntaxErrorInsideExternalRefNamesTheFile(t *testing.T) {
	r := &mapResolver{docs: map[string]string{
		"leaf.rng": `<element` + rngNS + ` name="leaf"><zeroOrMore1><text/></zeroOrMore1></element>`,
	}}
	_, err := compileWith(t, `<grammar`+rngNS+`>
		<start><externalRef href="leaf.rng"/></start></grammar>`,
		Options{Resolver: r})
	if err == nil {
		t.Fatal("a bogus element inside an externalRef schema should be refused")
	}
	if !strings.Contains(err.Error(), "leaf.rng") {
		t.Errorf("error = %v, should name leaf.rng", err)
	}
}

// The other half: a top-level error must read exactly as it did before. The
// prefix is earned by being nested, not paid by everyone.
func TestTopLevelSyntaxErrorIsNotPrefixed(t *testing.T) {
	_, err := compileWith(t, `<grammar`+rngNS+`>
		<start><zeroOrMore1><text/></zeroOrMore1></start></grammar>`, Options{})
	if err == nil {
		t.Fatal("a bogus element should be refused")
	}
	const want = "relaxng: <zeroOrMore1> is not a RELAX NG element"
	if err.Error() != want {
		t.Errorf("top-level error = %q, want exactly %q; a document that "+
			"includes nothing has no other document to name", err.Error(), want)
	}
}
