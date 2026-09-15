package xquery

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// libWithContextItemType is a library module that constrains the context
// item's type and supplies no value. §4.16 permits exactly this: a value there
// would be XQST0113, but a type is the module saying what it needs the
// importing query's context item to be.
func libWithContextItemType(ns, typ string) string {
	return `module namespace m = "` + ns + `";
		declare context item as ` + typ + ` external;
		declare function m:ok() { "ok" };`
}

// TestImportedContextItemTypeIsEnforced pins §4.16: the context item
// declarations in all modules must be consistent, so the item a query is
// evaluated with must match the type EVERY imported module declared, not only
// the type the main module declared.
//
// Both shapes come from the suite. contextDecl-050 declares its own
// "xs:integer" over a module wanting xs:date; contextDecl-051 declares
// "node() external" and is supplied an element. Neither value is an xs:date,
// so both owe XPTY0004 — and before the imported declaration was carried
// through the module's compilation, both returned a value instead.
func TestImportedContextItemTypeIsEnforced(t *testing.T) {
	const ns = "http://example.com/ctxlib"
	opts := Options{Modules: []Module{
		{Namespace: ns, Source: libWithContextItemType(ns, "xs:date")},
	}}

	t.Run("main module declares a conflicting type and a value", func(t *testing.T) {
		// contextDecl-050's shape: the main module supplies an xs:integer.
		_, err := Eval(`import module namespace m = "`+ns+`";
			declare context item as xs:integer := 23; . eq 23`, nil, opts)
		if err == nil {
			t.Fatal("an xs:integer context item does not match the imported xs:date: want XPTY0004, got no error")
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Fatalf("want XPTY0004, got %v", err)
		}
	})

	t.Run("element context item declared as node()", func(t *testing.T) {
		// contextDecl-051's shape: the type the main module declares is
		// satisfied (an element IS a node()), so only the imported xs:date
		// can reject it. That is what makes this case prove the imported
		// declaration is consulted rather than the main one doing the work.
		// The element is built by the declaration's own initialiser so the
		// test needs no node-construction API of its own.
		_, err := Eval(`import module namespace m = "`+ns+`";
			declare context item as node() := <e/>; . instance of element()`,
			nil, opts)
		if err == nil {
			t.Fatal("an element context item does not match the imported xs:date: want XPTY0004, got no error")
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Fatalf("want XPTY0004, got %v", err)
		}
	})

	t.Run("no context item declaration in the main module", func(t *testing.T) {
		// The importing module need not declare anything for the imported
		// constraint to bind: §4.16 makes the type a property of the context
		// item, not of the declaration that happens to mention it.
		_, err := Eval(`import module namespace m = "`+ns+`"; string(.)`,
			xpath.NewContext(xdm.NewInteger(23), nil), opts)
		if err == nil {
			t.Fatal("an xs:integer context item does not match the imported xs:date: want XPTY0004, got no error")
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Fatalf("want XPTY0004, got %v", err)
		}
	})
}

// TestImportedContextItemTypeAcceptsAMatchingValue is the counterweight: the
// imported type must ADMIT the values it describes, not reject everything.
//
// Without this, the check above would be satisfied by an implementation that
// failed every query importing a module with a context item declaration.
func TestImportedContextItemTypeAcceptsAMatchingValue(t *testing.T) {
	const ns = "http://example.com/ctxlib"
	opts := Options{Modules: []Module{
		{Namespace: ns, Source: libWithContextItemType(ns, "xs:integer")},
	}}

	got, err := Eval(`import module namespace m = "`+ns+`"; . + 1`,
		xpath.NewContext(xdm.NewInteger(41), nil), opts)
	if err != nil {
		t.Fatalf("an xs:integer matches the imported xs:integer: %v", err)
	}
	if len(got) != 1 || got[0].(*xdm.Atomic).String() != "42" {
		t.Fatalf("got %v, want 42", got)
	}
}

// TestImportedContextItemTypeIgnoresAnAbsentItem pins the boundary of the
// rule. A module that declares a type says what the item must be IF there is
// one; whether there must be one at all is the main module's business.
//
// The query below never reads ".", so an engine that treated the imported
// declaration as making the item REQUIRED would fail a query that is legal.
func TestImportedContextItemTypeIgnoresAnAbsentItem(t *testing.T) {
	const ns = "http://example.com/ctxlib"
	opts := Options{Modules: []Module{
		{Namespace: ns, Source: libWithContextItemType(ns, "xs:date")},
	}}

	got, err := Eval(`import module namespace m = "`+ns+`"; m:ok()`, nil, opts)
	if err != nil {
		t.Fatalf("a query that never reads the context item is legal: %v", err)
	}
	if len(got) != 1 || got[0].(*xdm.Atomic).String() != "ok" {
		t.Fatalf(`got %v, want "ok"`, got)
	}
}
