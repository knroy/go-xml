package xquery

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// unionSchema declares the two shapes the conversion rules treat differently:
// a pure union of atomic types, and one with an xs:QName member, which makes
// it namespace-sensitive.
const unionSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
   targetNamespace="http://example.com/u" xmlns:u="http://example.com/u"
   elementFormDefault="qualified">
  <xs:simpleType name="unionType">
    <xs:union memberTypes="xs:integer xs:float"/>
  </xs:simpleType>
  <xs:simpleType name="nsSensitive">
    <xs:union memberTypes="xs:QName xs:string"/>
  </xs:simpleType>
  <xs:simpleType name="listType">
    <xs:list itemType="xs:integer"/>
  </xs:simpleType>
</xs:schema>`

func unionOpts() Options {
	return Options{Schemas: []Schema{
		{Namespace: "http://example.com/u", Source: unionSchema},
	}}
}

// TestModuleNamespaceURIIsCollapsed pins the whitespace facet of a URILiteral.
//
// A URILiteral has type xs:anyURI, whose whitespace facet is "collapse": ends
// trimmed AND every internal run of whitespace reduced to one space. A module
// import and the module declaration it resolves to must therefore agree even
// when written with different whitespace.
func TestModuleNamespaceURIIsCollapsed(t *testing.T) {
	lib := `module namespace m = "http://example.com/Test Modules/lib";
declare function m:ok() { "ok" };`

	// The import writes four spaces where the module declaration writes one.
	// Collapse makes both "http://example.com/Test Modules/lib".
	main := `import module namespace m = "http://example.com/Test    Modules/lib";
m:ok()`

	opts := Options{Modules: []Module{
		{Namespace: "http://example.com/Test Modules/lib", Source: lib},
	}}
	seq, err := Eval(main, nil, opts)
	if err != nil {
		t.Fatalf("import across collapsed whitespace failed: %v", err)
	}
	if got := seqText(t, seq); got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

// TestContextItemDeclInLibraryModuleRejectsValue pins XQST0113.
//
// §4.16: a context item declaration in a library module must not specify a
// value. The library does not own the context item — the main module supplies
// it and the library may only constrain its type — so an initialiser there is
// a static error, whether or not the declaration also says "external".
func TestContextItemDeclInLibraryModuleRejectsValue(t *testing.T) {
	for _, decl := range []string{
		"declare context item external := 17;",
		"declare context item as xs:integer := 17;",
		"declare context item := 17;",
	} {
		lib := `module namespace m = "http://example.com/ci";
` + decl + `
declare function m:f() { 1 };`
		opts := Options{Modules: []Module{
			{Namespace: "http://example.com/ci", Source: lib},
		}}
		src := `import module namespace m = "http://example.com/ci"; m:f()`
		_, err := Eval(src, nil, opts)
		if err == nil {
			t.Errorf("%q in a library module: want XQST0113, got no error", decl)
			continue
		}
		if !strings.Contains(err.Error(), "XQST0113") {
			t.Errorf("%q in a library module: want XQST0113, got %v", decl, err)
		}
	}
}

// TestContextItemDeclInLibraryModuleAllowsType is the other side of §4.16: a
// declaration that only constrains the type is legal, so the check must key on
// the initialiser and not on the declaration existing at all.
func TestContextItemDeclInLibraryModuleAllowsType(t *testing.T) {
	lib := `module namespace m = "http://example.com/ci";
declare context item as xs:integer external;
declare function m:f() { 1 };`
	opts := Options{Modules: []Module{
		{Namespace: "http://example.com/ci", Source: lib},
	}}
	src := `import module namespace m = "http://example.com/ci"; m:f()`
	if _, err := Eval(src, nil, opts); err != nil {
		t.Errorf("a type-only context item declaration is legal: %v", err)
	}
}

// TestTypeswitchCaseRequiresPureItemType pins XPST0051 on a case clause.
//
// §3.14.2 gives a CaseClause a SequenceType, so its ItemType obeys §2.5.4: an
// AtomicOrUnionType there must be a generalized atomic type — an atomic type
// or a *pure* union. A list type names a type but not an item type, so it is
// XPST0051 rather than a clause that quietly fails to match.
func TestTypeswitchCaseRequiresPureItemType(t *testing.T) {
	src := `import schema namespace u = "http://example.com/u";
typeswitch (-23) case $i as u:listType return true() default $v return false()`
	_, err := Eval(src, nil, unionOpts())
	if err == nil {
		t.Fatal("a list type in a typeswitch case: want XPST0051, got no error")
	}
	if !strings.Contains(err.Error(), "XPST0051") {
		t.Errorf("want XPST0051, got %v", err)
	}
}

// TestTypeswitchCaseAllowsPureUnion guards the same check from over-reach: a
// pure union IS a generalized atomic type and must still be accepted.
func TestTypeswitchCaseAllowsPureUnion(t *testing.T) {
	src := `import schema namespace u = "http://example.com/u";
typeswitch (23) case $i as u:unionType return "matched" default $v return "default"`
	seq, err := Eval(src, nil, unionOpts())
	if err != nil {
		t.Fatalf("a pure union in a typeswitch case is legal: %v", err)
	}
	if got := seqText(t, seq); got != "matched" {
		t.Errorf("got %q, want %q", got, "matched")
	}
}

// TestUnionConversionCastsOnlyUntypedAtomic pins the §3.1.5 rule that numeric
// promotion does not occur when the target is a union.
//
// A typed value gets exactly two conversions: the xs:untypedAtomic cast and
// numeric/anyURI promotion. Promotion is defined against a single target type,
// not a union, so an xs:decimal that matches no member has no conversion left
// and is XPTY0004. An xs:untypedAtomic, by contrast, IS cast to a member.
//
// Both spellings of a function are checked because they used to disagree: an
// inline function whose body was ordinary XPath took a schema-blind converter
// while a declared one did not.
func TestUnionConversionCastsOnlyUntypedAtomic(t *testing.T) {
	const decl = `import schema namespace u = "http://example.com/u";
declare function local:f($in as u:unionType) as xs:boolean { $in instance of xs:integer };
`
	// A typed xs:decimal matches neither xs:integer nor xs:float, and must
	// not be promoted into the union.
	t.Run("typed value is not promoted", func(t *testing.T) {
		for name, src := range map[string]string{
			"declared": decl + `local:f(12.3)`,
			"inline": `import schema namespace u = "http://example.com/u";
function($in as u:unionType) as xs:boolean { $in instance of xs:integer }(12.3)`,
		} {
			_, err := Eval(src, nil, unionOpts())
			if err == nil {
				t.Errorf("%s: want XPTY0004, got no error", name)
				continue
			}
			if !strings.Contains(err.Error(), "XPTY0004") {
				t.Errorf("%s: want XPTY0004, got %v", name, err)
			}
		}
	})

	// An xs:untypedAtomic IS cast, member by member, so '123' becomes the
	// xs:integer 123 and "instance of xs:integer" is true.
	t.Run("untypedAtomic is cast to a member", func(t *testing.T) {
		for name, src := range map[string]string{
			"declared": decl + `local:f(xs:untypedAtomic('123'))`,
			"inline": `import schema namespace u = "http://example.com/u";
function($in as u:unionType) as xs:boolean { $in instance of xs:integer }(xs:untypedAtomic('123'))`,
		} {
			seq, err := Eval(src, nil, unionOpts())
			if err != nil {
				t.Errorf("%s: unexpected error %v", name, err)
				continue
			}
			if got := seqText(t, seq); got != "true" {
				t.Errorf("%s: got %q, want %q", name, got, "true")
			}
		}
	})
}

// TestNamespaceSensitiveTargetRefusesUntypedAtomic pins XPTY0117.
//
// §3.1.5 refuses the untypedAtomic conversion outright when the target is
// namespace-sensitive: resolving a prefix in the supplied string would need
// bindings that are not in scope at the call site, so nothing the caller could
// write would make it succeed. That is its own code, not an ordinary mismatch.
func TestNamespaceSensitiveTargetRefusesUntypedAtomic(t *testing.T) {
	for name, src := range map[string]string{
		"declared": `import schema namespace u = "http://example.com/u";
declare function local:g() as u:nsSensitive { xs:untypedAtomic('xsi:type') };
local-name-from-QName(local:g())`,
		"inline": `import schema namespace u = "http://example.com/u";
let $f := function() as u:nsSensitive { xs:untypedAtomic('xsi:type') }
return local-name-from-QName($f())`,
	} {
		_, err := Eval(src, nil, unionOpts())
		if err == nil {
			t.Errorf("%s: want XPTY0117, got no error", name)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0117") {
			t.Errorf("%s: want XPTY0117, got %v", name, err)
		}
	}
}

// seqText returns the string value of a one-item sequence.
func seqText(t *testing.T, seq xdm.Sequence) string {
	t.Helper()
	if len(seq) != 1 {
		t.Fatalf("want 1 item, got %d", len(seq))
	}
	switch v := seq[0].(type) {
	case *xdm.Atomic:
		return v.String()
	case *xdm.Node:
		return v.StringValue()
	}
	t.Fatalf("unexpected item type %T", seq[0])
	return ""
}

// constructorClashSchema declares one named atomic type, so that importing it
// puts exactly one constructor function into the static context.
const constructorClashSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
   targetNamespace="http://example.com/ctor" xmlns:c="http://example.com/ctor">
  <xs:simpleType name="sizeType">
    <xs:restriction base="xs:integer"><xs:maxInclusive value="100"/></xs:restriction>
  </xs:simpleType>
</xs:schema>`

// TestFunctionDeclClashesWithSchemaConstructor pins XQST0034 for the
// constructor functions.
//
// §4.15 forbids a function declaration whose expanded QName and arity are
// already those of a function in the static context, and §4.11 puts one
// constructor function per imported simple type there. So declaring
// c:sizeType#1 after importing the schema that defines c:sizeType is a static
// error, and prod-CastExpr.schema/user-defined-11 asserts exactly it.
func TestFunctionDeclClashesWithSchemaConstructor(t *testing.T) {
	opts := Options{Schemas: []Schema{
		{Namespace: "http://example.com/ctor", Source: constructorClashSchema},
	}}
	src := `import schema namespace c = "http://example.com/ctor";
declare function c:sizeType($a as xs:integer) { $a + 1 };
c:sizeType(16)`
	_, err := Eval(src, nil, opts)
	if err == nil {
		t.Fatalf("a declaration of the constructor's name and arity: " +
			"want XQST0034, got no error")
	}
	if !strings.Contains(err.Error(), "XQST0034") {
		t.Errorf("want XQST0034, got %v", err)
	}
}

// TestFunctionDeclNotClashingWithSchemaConstructor is the other side of the
// rule, and is what keeps the check from refusing valid queries.
//
// A constructor function exists for each simple type the schema defines and
// for nothing else, and it has arity ONE. So a name the schema does not
// declare as a type, and the same name at another arity, are both legal
// declarations in the very namespace the schema owns.
func TestFunctionDeclNotClashingWithSchemaConstructor(t *testing.T) {
	opts := Options{Schemas: []Schema{
		{Namespace: "http://example.com/ctor", Source: constructorClashSchema},
	}}
	for _, src := range []string{
		// Not a type the schema declares.
		`import schema namespace c = "http://example.com/ctor";
declare function c:notAType($a as xs:integer) { $a + 1 };
c:notAType(16)`,
		// The type's name, but arity two: no constructor has that arity.
		`import schema namespace c = "http://example.com/ctor";
declare function c:sizeType($a as xs:integer, $b as xs:integer) { $a + $b };
c:sizeType(16, 1)`,
	} {
		if _, err := Eval(src, nil, opts); err != nil {
			t.Errorf("no constructor has this name and arity: "+
				"want no error, got %v", err)
		}
	}
}

// elementDerivationSchema declares a union type and a restriction of it, which
// is the one-way derivation the element-test subtype rule turns on.
const elementDerivationSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
   targetNamespace="http://example.com/d" xmlns:d="http://example.com/d">
  <xs:simpleType name="approximateDate">
    <xs:union memberTypes="xs:date xs:dateTime xs:gYear xs:gYearMonth"/>
  </xs:simpleType>
  <xs:simpleType name="restrictedUnion">
    <xs:restriction base="d:approximateDate"><xs:pattern value="20.*"/></xs:restriction>
  </xs:simpleType>
</xs:schema>`

// TestElementTestSubtypeFollowsSchemaDerivation pins XPath 3.1 2.5.6.2's
// subtype-itemtype judgement for two element tests.
//
// element(*, T1) is a subtype of element(*, T2) when T1 is DERIVED FROM T2,
// not only when the two names are equal. Reached through the CONTRAVARIANT
// parameter rule for function types, that makes
//
//	function(element(*, approximateDate)) as xs:integer
//
// an instance of
//
//	function(element(*, restrictedUnion)) as xs:integer
//
// because restrictedUnion restricts approximateDate: the function accepts
// everything the test's parameter type admits. The relation runs ONE way, so
// the reverse must stay false — that is the whole point of the pair, and the
// reason this cannot go through schemaSubsumes, which relates a union and a
// restriction of it in both directions. prod-FunctionCall/FunctionCall-051
// and -052.
func TestElementTestSubtypeFollowsSchemaDerivation(t *testing.T) {
	opts := Options{Schemas: []Schema{
		{Namespace: "http://example.com/d", Source: elementDerivationSchema},
	}}
	const prolog = `declare namespace d = 'http://example.com/d';
import schema "http://example.com/d";
declare variable $f := function($in as element(*, d:%s)) as xs:integer {1};
$f instance of function(element(*, d:%s)) as xs:integer`
	for _, tc := range []struct {
		param, test string
		want        bool
	}{
		// The parameter admits MORE than the test's: a subtype.
		{"approximateDate", "restrictedUnion", true},
		// The reverse: the function would refuse values the test admits.
		{"restrictedUnion", "approximateDate", false},
		// The same type on both sides is always a subtype of itself.
		{"restrictedUnion", "restrictedUnion", true},
	} {
		src := fmt.Sprintf(prolog, tc.param, tc.test)
		seq, err := Eval(src, nil, opts)
		if err != nil {
			t.Errorf("param %s, test %s: unexpected error %v",
				tc.param, tc.test, err)
			continue
		}
		if got := seqText(t, seq); got != fmt.Sprint(tc.want) {
			t.Errorf("function(element(*, d:%s)) instance of "+
				"function(element(*, d:%s)): got %s, want %v",
				tc.param, tc.test, got, tc.want)
		}
	}
}

// typedValueSchema declares one element of each of the three complex content
// kinds, so that the rule can be tested where it applies and where it does not.
const typedValueSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
   targetNamespace="http://example.com/tv" xmlns:t="http://example.com/tv"
   elementFormDefault="qualified">
  <xs:element name="elementOnly">
    <xs:complexType><xs:sequence>
      <xs:element name="content" minOccurs="0"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:element name="mixed">
    <xs:complexType mixed="true"><xs:sequence>
      <xs:element name="content" minOccurs="0"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:element name="empty">
    <xs:complexType/>
  </xs:element>
</xs:schema>`

// TestAtomizeElementOnlyContentIsFOTY0012 pins the "typed value is absent"
// rule.
//
// XDM 3.1 6.2.4 leaves dm:typed-value UNDEFINED for an element whose type is a
// complex type with element-only content, and F&O makes fn:data on such a node
// FOTY0012. Before this, the node atomized to xs:untypedAtomic of its string
// value — the empty string here — so the query answered with a value instead
// of failing. misc-CombinedErrorCodes/FOTY0012.
func TestAtomizeElementOnlyContentIsFOTY0012(t *testing.T) {
	opts := Options{Schemas: []Schema{
		{Namespace: "http://example.com/tv", Source: typedValueSchema},
	}}
	src := `import schema namespace t = "http://example.com/tv";
data(validate strict { <t:elementOnly/> })`
	_, err := Eval(src, nil, opts)
	if err == nil {
		t.Fatalf("atomizing an element-only element: want FOTY0012, " +
			"got no error")
	}
	if !strings.Contains(err.Error(), "FOTY0012") {
		t.Errorf("want FOTY0012, got %v", err)
	}
}

// TestAtomizeOtherContentKindsHaveTypedValues is the other side of 6.2.4, and
// is what keeps the rule from swallowing the two content kinds that DO have a
// typed value.
//
// MIXED content atomizes to the string value as xs:untypedAtomic; EMPTY
// content atomizes to the empty sequence. Neither is an error, and an
// unvalidated element is untouched by the rule altogether — it was never
// assessed, so nothing concluded that its typed value is absent.
func TestAtomizeOtherContentKindsHaveTypedValues(t *testing.T) {
	opts := Options{Schemas: []Schema{
		{Namespace: "http://example.com/tv", Source: typedValueSchema},
	}}
	for _, tc := range []struct{ src, want string }{
		{`import schema namespace t = "http://example.com/tv";
string-join(data(validate strict { <t:mixed>hi</t:mixed> }))`, "hi"},
		// EMPTY content. 6.2.4 gives it the empty sequence and we still give
		// one xs:untypedAtomic("") — a SEPARATE, pre-existing gap, unrelated
		// to this rule and deliberately not fixed here. What matters for
		// FOTY0012 is that it does not RAISE: empty content has a typed
		// value, so marking it absent would turn a defined value into an
		// error. Asserting on the string keeps the case honest about what we
		// do without pinning the wrong count.
		{`import schema namespace t = "http://example.com/tv";
string-join(data(validate strict { <t:empty/> }))`, ""},
		// Never validated: xs:untypedAtomic of the string value, as always.
		{`string-join(data(<a><b/></a>))`, ""},
	} {
		seq, err := Eval(tc.src, nil, opts)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.src, err)
			continue
		}
		if got := seqText(t, seq); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.src, got, tc.want)
		}
	}
}
