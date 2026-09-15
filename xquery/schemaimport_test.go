package xquery_test

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xquery"
	"github.com/knroy/go-xml/xsd"
)

// hatsSchema is the schema these tests import. It is the shape the QT3 suite's
// own "hats" schema has -- an atomic restriction of xs:integer, a global
// element declaration, and a complex type -- reduced to what each assertion
// below needs.
const hatsNS = "http://example.org/hats"

const hatsSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="http://example.org/hats"
    xmlns="http://example.org/hats"
    elementFormDefault="qualified">
  <xs:simpleType name="hatsize">
    <xs:restriction base="xs:integer">
      <xs:minInclusive value="4"/>
      <xs:maxInclusive value="12"/>
    </xs:restriction>
  </xs:simpleType>
  <xs:element name="hat" type="hatsize"/>
  <xs:element name="box">
    <xs:complexType>
      <xs:sequence>
        <xs:element ref="hat" minOccurs="0" maxOccurs="unbounded"/>
      </xs:sequence>
    </xs:complexType>
  </xs:element>
</xs:schema>`

// withHats registers the schema as source text, which is the store form that
// grants the query no reach at all: nothing is opened and nothing is fetched.
func withHats() xquery.Options {
	return xquery.Options{Schemas: []xquery.Schema{
		{Namespace: hatsNS, Source: hatsSchema},
	}}
}

// TestSchemaImportTypeNameReachesStaticContext is the property the whole
// feature rests on: a type an imported schema defines is resolvable BY THE
// PARSER, because XQuery resolves type names while parsing rather than after.
// Without the import the same query is a static error.
func TestSchemaImportTypeNameReachesStaticContext(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q; 8 cast as h:hatsize`, hatsNS)
	got, err := run(t, src, withHats())
	if err != nil {
		t.Fatalf("an imported type name must resolve: %v", err)
	}
	if got != "8" {
		t.Errorf("got %q, want %q", got, "8")
	}
}

// TestSchemaImportFacetsAreApplied checks that the type means what the schema
// author wrote rather than merely resolving. hatsize admits 4 to 12, so 99 is
// a failed cast (FORG0001) and not a silently accepted integer -- which is
// what the cast did before ValidateSchemaValue reached this package's static
// context.
func TestSchemaImportFacetsAreApplied(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q; 99 castable as h:hatsize`, hatsNS)
	got, err := run(t, src, withHats())
	if err != nil {
		t.Fatalf("castable must not raise: %v", err)
	}
	if got != "false" {
		t.Errorf("99 is outside hatsize's facets; got castable = %q", got)
	}
}

// TestSchemaImportWithoutImportIsStatic is the control for the two above: the
// same type name with no import is a static error, so the tests above are
// measuring the import rather than a name the engine knew anyway.
func TestSchemaImportWithoutImportIsStatic(t *testing.T) {
	_, err := run(t, `declare namespace h = "http://example.org/hats";
		8 cast as h:hatsize`, xquery.Options{})
	if err == nil {
		t.Fatal("an unimported type name must be a static error")
	}
	// XQST0052 rather than XPST0051: a 3.0-or-later module's cast to an
	// unknown target type is XQST0052, which version.go already settles. The
	// point of the check is that the name is refused at all.
	if !strings.Contains(err.Error(), "XQST0052") {
		t.Errorf("want XQST0052 for an unknown type, got %v", err)
	}
}

// TestSchemaImportDefaultElementNamespace checks the "import schema default
// element namespace URI" form, which additionally makes the imported namespace
// the default element AND TYPE namespace for the rest of the module -- so the
// type is nameable with no prefix at all.
func TestSchemaImportDefaultElementNamespace(t *testing.T) {
	src := fmt.Sprintf(
		`import schema default element namespace %q; 8 cast as hatsize`, hatsNS)
	got, err := run(t, src, withHats())
	if err != nil {
		t.Fatalf("the default element namespace must reach a type name: %v", err)
	}
	if got != "8" {
		t.Errorf("got %q, want %q", got, "8")
	}
}

// TestSchemaImportSchemaElementTest checks that a global element DECLARATION
// reaches the static context too, which is what schema-element() names. It is
// a different table from the type table and a different lookup.
func TestSchemaImportSchemaElementTest(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q;
		 <h:hat xmlns:h=%q>8</h:hat> instance of element()`, hatsNS, hatsNS)
	if _, err := run(t, src, withHats()); err != nil {
		t.Fatalf("%v", err)
	}
	// schema-element names the declaration; with no schema it is XPST0008.
	src = fmt.Sprintf(
		`import schema namespace h = %q;
		 empty(() treat as schema-element(h:hat)*)`, hatsNS)
	if _, err := run(t, src, withHats()); err != nil {
		t.Fatalf("schema-element must resolve against an imported "+
			"declaration: %v", err)
	}
	_, err := run(t, `declare namespace h = "http://example.org/hats";
		 empty(() treat as schema-element(h:hat)*)`, xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "XPST0008") {
		t.Errorf("want XPST0008 without the import, got %v", err)
	}
}

// TestValidateStrictAgainstImportedSchema is the validate expression's whole
// point: with a schema in scope, a valid element is assessed and ANNOTATED, so
// the result carries the type it was validated against.
func TestValidateStrictAgainstImportedSchema(t *testing.T) {
	src := fmt.Sprintf(
		`import schema default element namespace %q;
		 validate strict { <hat xmlns=%q>8</hat> } instance of element(*, hatsize)`,
		hatsNS, hatsNS)
	got, err := run(t, src, withHats())
	if err != nil {
		t.Fatalf("validate strict against an imported schema: %v", err)
	}
	if got != "true" {
		t.Errorf("the validated element must carry its type annotation; got %q", got)
	}
}

// TestValidateStrictNoDeclarationIsXQDY0084 checks §3.21's error for an
// element the in-scope schema definitions do not declare at top level. It is
// the case the suite writes as qischema90131-err.
func TestValidateStrictNoDeclarationIsXQDY0084(t *testing.T) {
	src := fmt.Sprintf(
		`import schema default element namespace %q;
		 validate strict { <nosuch xmlns=%q/> }`, hatsNS, hatsNS)
	_, err := run(t, src, withHats())
	if err == nil {
		t.Fatal("strict validation of an undeclared element must fail")
	}
	if !strings.Contains(err.Error(), "XQDY0084") {
		t.Errorf("want XQDY0084, got %v", err)
	}
}

// TestValidateStrictInvalidIsXQDY0027 checks the other half: the element IS
// declared, so the assessment runs, and it finds the content invalid. 99 is
// outside hatsize's facets.
func TestValidateStrictInvalidIsXQDY0027(t *testing.T) {
	src := fmt.Sprintf(
		`import schema default element namespace %q;
		 validate strict { <hat xmlns=%q>99</hat> }`, hatsNS, hatsNS)
	_, err := run(t, src, withHats())
	if err == nil {
		t.Fatal("an invalid element must be refused")
	}
	if !strings.Contains(err.Error(), "XQDY0027") {
		t.Errorf("want XQDY0027 for an assessed-and-invalid operand, got %v", err)
	}
}

// TestValidateWithNoImportStillRefuses is the boundary this change must not
// blur: a query that imported no schema gets exactly the behaviour it had
// before -- XQDY0084 for strict, and a skipped assessment for lax.
func TestValidateWithNoImportStillRefuses(t *testing.T) {
	_, err := run(t, `validate strict { <a/> }`, xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "XQDY0084") {
		t.Errorf("want XQDY0084 with no schema in scope, got %v", err)
	}
	got, err := run(t, `validate lax { <a/> }`, xquery.Options{})
	if err != nil {
		t.Fatalf("lax validation with no schema is a skipped assessment: %v", err)
	}
	if got != "<a/>" {
		t.Errorf("got %q", got)
	}
}

// TestSchemaImportTwiceIsXQST0058 is schema-import-2: §4.11 forbids importing
// one target namespace twice in one prolog.
func TestSchemaImportTwiceIsXQST0058(t *testing.T) {
	src := fmt.Sprintf(`import schema namespace a = %q;
		import schema namespace b = %q; 1`, hatsNS, hatsNS)
	_, err := run(t, src, withHats())
	if err == nil || !strings.Contains(err.Error(), "XQST0058") {
		t.Errorf("want XQST0058, got %v", err)
	}
}

// TestSchemaImportEmptyNamespaceWithPrefixIsXQST0057 is schema-import-3: a
// prefix bound by a schema import must have a namespace to be bound to.
func TestSchemaImportEmptyNamespaceWithPrefixIsXQST0057(t *testing.T) {
	_, err := run(t, `import schema namespace ns1 = ""; "abc"`, xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "XQST0057") {
		t.Errorf("want XQST0057, got %v", err)
	}
}

// TestSchemaImportReservedPrefixIsXQST0070 is schema-import-31.
func TestSchemaImportReservedPrefixIsXQST0070(t *testing.T) {
	_, err := run(t,
		`import schema namespace xml = "http://example.org/x"; "abc"`,
		xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "XQST0070") {
		t.Errorf("want XQST0070, got %v", err)
	}
}

// TestSchemaImportRegisteredComponents checks the other store form: a caller
// that already holds an *xsd.Schema -- from xsd.Load, or from a stylesheet's
// Schema() -- registers it directly, so one schema is shared rather than
// loaded twice and risked disagreeing.
func TestSchemaImportRegisteredComponents(t *testing.T) {
	tree, err := xdm.ParseString(hatsSchema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the schema: %v", err)
	}
	sch, err := xsd.Load(tree.Root, "", xsd.Options{})
	if err != nil {
		t.Fatalf("loading the schema: %v", err)
	}
	src := fmt.Sprintf(
		`import schema namespace h = %q; 8 cast as h:hatsize`, hatsNS)
	got, err := run(t, src, xquery.Options{Schemas: []xquery.Schema{
		{Namespace: hatsNS, Components: sch},
	}})
	if err != nil {
		t.Fatalf("a registered *xsd.Schema must satisfy an import: %v", err)
	}
	if got != "8" {
		t.Errorf("got %q", got)
	}
}

// --- security ---------------------------------------------------------------
//
// These are the tests docs/security.md's claims about "import schema" rest on.
// Each was validated by sabotage; see the CHANGELOG entry.

// TestSchemaNoResolverDoesNotFetch is the central security property, and is
// the exact counterpart of TestNoResolverDoesNotFetch for modules. With no
// SchemaResolver configured an "at" location is NOT opened -- not tried and
// failed, not opened -- so a query cannot read a file by naming it.
//
// The location named exists on every Unix, so a resolver that defaulted to the
// filesystem would succeed in reading it and fail later with a parse error.
// XQST0059, with no mention of the path, is what proves nothing was opened.
func TestSchemaNoResolverDoesNotFetch(t *testing.T) {
	_, err := run(t,
		`import schema namespace z="http://x/absent" at "/etc/passwd"; 1`,
		xquery.Options{})
	if err == nil {
		t.Fatal("a query with no resolver must not resolve an \"at\" location")
	}
	if !strings.Contains(err.Error(), "XQST0059") {
		t.Errorf("want XQST0059, got %v", err)
	}
	if strings.Contains(err.Error(), "passwd") {
		t.Errorf("the error names the location, which means it was opened: %v", err)
	}
	// The POSITIVE fact, and the one that actually detects a breach: the
	// refusal is the configured-nothing refusal, naming the option that would
	// allow it. Absence of the path is too weak on its own -- a default
	// resolver that DID open /etc/passwd would fail on the XML inside it and
	// still report XQST0059 without naming the file, which is exactly what a
	// sabotage of noSchemaResolver produced. That refusal says "could not be
	// parsed"; this one says "no SchemaResolver is configured".
	if !strings.Contains(err.Error(), "no SchemaResolver is configured") {
		t.Errorf("the refusal must be the configured-nothing refusal, which "+
			"is what proves the location was never opened; got %v", err)
	}
	if strings.Contains(err.Error(), "could not be parsed") ||
		strings.Contains(err.Error(), "could not be read") {
		t.Errorf("the error reports reading the location, which means it "+
			"was opened: %v", err)
	}
}

// TestSchemaNoResolverIsDistinguishable checks that "you configured no
// resolver" is separable from "no such schema". The XQST0059 the query sees is
// the same either way, because that is the code §4.11 gives.
func TestSchemaNoResolverIsDistinguishable(t *testing.T) {
	_, err := run(t, `import schema namespace z="http://x/absent"; 1`,
		xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "SchemaResolver") {
		t.Errorf("the refusal should name the option that would allow it, got %v", err)
	}
}

// hugeSchemaResolver answers with a schema document far larger than any byte
// budget: a valid schema followed by a comment of arbitrary length, so the
// size is the only thing wrong with it.
type hugeSchemaResolver struct{ size int }

func (r hugeSchemaResolver) Resolve(ns, location, base string) (
	io.ReadCloser, string, error) {
	head := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		targetNamespace="` + ns + `"><!--`
	return io.NopCloser(io.MultiReader(
		strings.NewReader(head),
		strings.NewReader(strings.Repeat("x", r.size)),
		strings.NewReader(`--></xs:schema>`),
	)), "", nil
}

// TestMaxSchemaBytesRefusesRatherThanTruncates is the budget's invariant: a
// schema over the allowance FAILS the compilation. It must never yield a query
// compiled against a truncated schema, because a schema whose components are
// partly missing validates documents against the half that is left.
//
// The error must wrap xdm.ErrResourceLimit and must NOT be XQST0059: the
// budget declined to answer, and saying "no such schema" would be a claim
// about the store that is not true.
func TestMaxSchemaBytesRefusesRatherThanTruncates(t *testing.T) {
	_, err := run(t, `import schema namespace a="urn:big" at "x.xsd"; 1`,
		xquery.Options{
			SchemaResolver: hugeSchemaResolver{size: 4096},
			MaxSchemaBytes: 1024,
		})
	if err == nil {
		t.Fatal("a schema over the byte budget must be refused, not truncated")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("the refusal must wrap xdm.ErrResourceLimit so a caller can "+
			"tell a budget from a fault; got %v", err)
	}
	if strings.Contains(err.Error(), "XQST0059") {
		t.Errorf("a budget refusal must not claim the schema was not found: %v", err)
	}
}

// TestMaxSchemaBytesIsPerCompilation checks that the allowance is spent across
// every import rather than reset per import. A budget spent one import at a
// time is not spent at all.
func TestMaxSchemaBytesIsPerCompilation(t *testing.T) {
	src := `import schema namespace a="urn:a" at "a.xsd";
		import schema namespace b="urn:b" at "b.xsd"; 1`
	// Each document is 600 bytes of padding, so either alone fits under 1024
	// and the two together do not.
	_, err := run(t, src, xquery.Options{
		SchemaResolver: hugeSchemaResolver{size: 600},
		MaxSchemaBytes: 1024,
	})
	if err == nil {
		t.Fatal("two imports must share one allowance")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("want a resource-limit refusal, got %v", err)
	}
}

// TestSchemaResolverIsSharedWithXSD checks the confinement an imported
// schema's OWN references are followed under: the resolver the query's import
// was granted, and never a wider one. A schema that xs:includes a document the
// resolver declines cannot reach it by any other route.
func TestSchemaResolverIsSharedWithXSD(t *testing.T) {
	r := &recordingResolver{docs: map[string]string{
		"main.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
			targetNamespace="urn:m"><xs:include schemaLocation="side.xsd"/>
			</xs:schema>`,
	}}
	if _, err := run(t, `import schema namespace m="urn:m" at "main.xsd"; 1`,
		xquery.Options{SchemaResolver: r}); err != nil {
		// An xs:include the resolver cannot answer is not itself an error --
		// XSD Part 1 §4.2.1 drops it -- so the compilation succeeds. What is
		// being measured is WHICH resolver was asked.
		t.Fatalf("%v", err)
	}
	// side.xsd was asked for through the query's own resolver, which is the
	// confinement: an imported schema's own references are followed by the
	// resolver the query's import was granted and never by a wider one. Had
	// xsd applied a default of its own, this resolver would never have seen
	// the location.
	if !r.asked["side.xsd"] {
		t.Errorf("the query's resolver must be the one xsd follows an "+
			"include with; it was asked for %v", r.asked)
	}
}

// recordingResolver answers only from its table and records every location it
// was asked for.
type recordingResolver struct {
	docs  map[string]string
	asked map[string]bool
}

func (r *recordingResolver) Resolve(ns, location, base string) (
	io.ReadCloser, string, error) {
	if r.asked == nil {
		r.asked = map[string]bool{}
	}
	r.asked[location] = true
	src, ok := r.docs[location]
	if !ok {
		return nil, "", fmt.Errorf("no schema document at %q", location)
	}
	return io.NopCloser(strings.NewReader(src)), location, nil
}
