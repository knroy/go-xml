package xquery

import (
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
