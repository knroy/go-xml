package relaxng

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func parseSchemaDoc(t *testing.T, s string) *xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(s, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tree.Root
}

// verdictOf compiles schema and validates inst, returning "valid",
// "invalid", or "schema-error".
func verdictOf(t *testing.T, schema, inst string) string {
	t.Helper()
	s, err := Compile(parseSchemaDoc(t, schema))
	if err != nil {
		return "schema-error"
	}
	if err := s.Validate(parseSchemaDoc(t, inst)); err != nil {
		return "invalid"
	}
	return "valid"
}

func dataSchema(typ, param, val string) string {
	return `<element name="e" xmlns="` + NS + `" datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes">` +
		`<data type="` + typ + `"><param name="` + param + `">` + val + `</param></data></element>`
}

func TestBoundFacetsAreExact(t *testing.T) {
	cases := []struct {
		typ, param, bound, inst, want string
	}{
		// 2^53 boundary, maxInclusive.
		{"integer", "maxInclusive", "9007199254740992", "9007199254740992", "valid"},
		{"integer", "maxInclusive", "9007199254740992", "9007199254740993", "invalid"},
		// 2^53 boundary, minInclusive.
		{"integer", "minInclusive", "9007199254740993", "9007199254740993", "valid"},
		{"integer", "minInclusive", "9007199254740993", "9007199254740992", "invalid"},
		// Exclusive bounds at the same boundary.
		{"integer", "maxExclusive", "9007199254740993", "9007199254740993", "invalid"},
		{"integer", "maxExclusive", "9007199254740993", "9007199254740992", "valid"},
		{"integer", "minExclusive", "9007199254740992", "9007199254740992", "invalid"},
		{"integer", "minExclusive", "9007199254740992", "9007199254740993", "valid"},
		// Far beyond int64 / float64 exact range.
		{"integer", "maxInclusive", "179769313486231570000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
			"179769313486231570000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001", "invalid"},
		// Negative side of the 2^53 boundary.
		{"integer", "minInclusive", "-9007199254740992", "-9007199254740993", "invalid"},
		{"integer", "minInclusive", "-9007199254740992", "-9007199254740992", "valid"},
		{"integer", "maxInclusive", "-9007199254740993", "-9007199254740992", "invalid"},
		// xs:decimal differing only past the 17th significant digit.
		{"decimal", "maxInclusive", "1.00000000000000000001", "1.00000000000000000002", "invalid"},
		{"decimal", "maxInclusive", "1.00000000000000000001", "1.00000000000000000001", "valid"},
		{"decimal", "minInclusive", "1.00000000000000000002", "1.00000000000000000001", "invalid"},
		// xs:double genuinely is a float64: these must keep working.
		{"double", "maxInclusive", "1.5", "1.25", "valid"},
		{"double", "maxInclusive", "1.5", "2.0", "invalid"},
	}
	for _, c := range cases {
		got := verdictOf(t, dataSchema(c.typ, c.param, c.bound),
			`<e>`+c.inst+`</e>`)
		if got != c.want {
			t.Errorf("%s %s=%s inst=%s: got %s, want %s",
				c.typ, c.param, c.bound, c.inst, got, c.want)
		}
	}
}

func TestLengthParamOutOfRange(t *testing.T) {
	cases := []struct {
		param, val, inst, want string
	}{
		{"minLength", "9223372036854775808", "a", "schema-error"},
		{"minLength", "10000000000000000000", "a", "schema-error"},
		{"minLength", "18446744073709551616", "a", "schema-error"},
		{"maxLength", "9223372036854775808", "a", "schema-error"},
		{"length", "9223372036854775808", "a", "schema-error"},
		// In-range params must keep working.
		{"minLength", "2", "a", "invalid"},
		{"minLength", "1", "a", "valid"},
		{"maxLength", "1", "ab", "invalid"},
		{"length", "1", "a", "valid"},
	}
	for _, c := range cases {
		got := verdictOf(t, dataSchema("string", c.param, c.val),
			`<e>`+c.inst+`</e>`)
		if got != c.want {
			t.Errorf("%s=%s inst=%q: got %s, want %s",
				c.param, c.val, c.inst, got, c.want)
		}
	}
}

// TestIncludedGrammarIsRestrictionChecked pins that section 7 is enforced on a
// grammar reached through <include>.
//
// derive.go assumes every pattern it sees has already passed restrict.go. The
// top-level and <externalRef> paths both check it; <include> once checked only
// the syntax, so a forbidden construct could reach the deriver from an
// included file.
func TestIncludedGrammarIsRestrictionChecked(t *testing.T) {
	// An <attribute> directly inside an <attribute> — a section 7.1.1
	// restriction that checkRestrictions rejects.
	included := `<grammar xmlns="` + NS + `"><define name="d">` +
		`<attribute name="a"><attribute name="b"><text/></attribute></attribute>` +
		`</define></grammar>`
	main := `<grammar xmlns="` + NS + `"><include href="inc.rng"/>` +
		`<start><element name="e"><ref name="d"/></element></start></grammar>`

	res := &suiteResolver{files: map[string]*xdm.Node{
		"inc.rng": firstElement(parseSchemaDoc(t, included)),
	}}
	_, err := CompileWithOptions(parseSchemaDoc(t, main),
		Options{Resolver: res, BaseURI: "main.rng"})
	if err == nil {
		t.Fatal("an included grammar with attribute//attribute compiled; " +
			"section 7 was not enforced on the included document")
	}
}
