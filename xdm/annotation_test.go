package xdm

import "testing"

// TestShadowedBuiltinCoexistsWithItsShadow pins the reason annotation keys are
// namespace-qualified.
//
// The derivation table is package-level and process-global, so a schema that
// declared a type sharing a built-in's local name used to overwrite the
// built-in's entry for the rest of the process — permanently, and for every
// later schema. That is not hypothetical: schema-for-xslt20.xsd declares its
// own xsl:QName as a restriction of xs:Name, and says in its own text why
// ("This schema does not use the built-in type xs:QName... a schema processor
// would expand unprefixed QNames incorrectly"). Loading it made
// DerivedBase("QName") answer "Name" everywhere.
//
// Both readings have to survive rather than one displacing the other, which
// is what rules out every cheaper fix: refusing the shadowing registration
// loses the shadow (import-schema-029 asserts the shadowing type DOES erase to
// a string), and a global "a built-in was shadowed" flag loses the built-in
// (type-functions-0501 asserts xs:QName still atomises to a QName). One key
// cannot hold two answers; two keys can.
func TestShadowedBuiltinCoexistsWithItsShadow(t *testing.T) {
	shadow := AnnotationName(NSXSL, "QName")
	if shadow == "QName" {
		t.Fatalf("AnnotationName did not qualify %q", shadow)
	}
	RegisterDerivedType(shadow, "Name")

	if got := DerivedBase("QName"); got != "" {
		t.Errorf("built-in xs:QName was poisoned by the shadow: "+
			"DerivedBase(%q) = %q, want %q", "QName", got, "")
	}
	if got := DerivedBase(shadow); got != "Name" {
		t.Errorf("shadowing type lost its registration: "+
			"DerivedBase(%q) = %q, want %q", shadow, got, "Name")
	}
}

// TestAnnotationNameLeavesBuiltinsBare records the one deliberate asymmetry in
// the encoding.
//
// A type in the XML Schema namespace keys under its bare local name, so the
// hundred-odd sites that compare an annotation against a literal "string",
// "QName" or "ID" keep working untouched. Only names that were genuinely
// ambiguous change spelling.
func TestAnnotationNameLeavesBuiltinsBare(t *testing.T) {
	for _, c := range []struct{ uri, local, want string }{
		{NSXS, "string", "string"},
		{"", "partNumberType", "partNumberType"},
		{"http://x/", "partNumberType", "{http://x/}partNumberType"},
		{NSXS, "", ""},
	} {
		if got := AnnotationName(c.uri, c.local); got != c.want {
			t.Errorf("AnnotationName(%q, %q) = %q, want %q",
				c.uri, c.local, got, c.want)
		}
	}
}

// TestSplitAnnotationNameIsNotSplitQName is the trap this package's callers
// have to avoid.
//
// SplitQName cuts at the first colon, so handed a Clark-notation key it
// returns nonsense rather than an error: "{http://x}foo" splits into the
// "prefix" "{http" and the "local part" "//x}foo". Every comparison built on
// that would be silently wrong.
func TestSplitAnnotationNameIsNotSplitQName(t *testing.T) {
	const key = "{http://x}foo"

	if _, local := SplitQName(key); local == "foo" {
		t.Fatalf("SplitQName suddenly understands Clark notation; "+
			"the guards in xpath that avoid it may now be dead code (got %q)",
			local)
	}
	uri, local := SplitAnnotationName(key)
	if uri != "http://x" || local != "foo" {
		t.Errorf("SplitAnnotationName(%q) = (%q, %q), want (%q, %q)",
			key, uri, local, "http://x", "foo")
	}

	// A bare key has no namespace and is returned whole as the local part.
	if uri, local := SplitAnnotationName("string"); uri != "" || local != "string" {
		t.Errorf("SplitAnnotationName(%q) = (%q, %q), want (%q, %q)",
			"string", uri, local, "", "string")
	}
	// An unterminated brace is not a key; it is returned unchanged rather
	// than being carved at an index that does not exist.
	if uri, local := SplitAnnotationName("{oops"); uri != "" || local != "{oops" {
		t.Errorf("SplitAnnotationName(%q) = (%q, %q), want (%q, %q)",
			"{oops", uri, local, "", "{oops")
	}
}
