package xquery

import (
	"strings"
	"testing"
)

// The rules asserted here are all static errors that a previous implementation
// reported as XQST0059 or not at all, because each was decided only after the
// prolog had tried to reach the outside world. A static error is owed
// regardless of the input (§2.2.4), so each is asserted against a query whose
// imports CANNOT be resolved: if the engine needs the schema or the module in
// order to find the fault, it has not implemented the rule.

// TestDuplicateSchemaImportIsXQST0058 pins §4.11's once-only rule for a schema
// import's target namespace.
//
// The namespace is deliberately unresolvable. Two imports of one namespace
// would contribute the same components whether or not either schema exists, so
// the fault is in the prolog's text and XQST0058 is owed ahead of the
// XQST0059 that the missing schema would otherwise raise.
func TestDuplicateSchemaImportIsXQST0058(t *testing.T) {
	for _, q := range []string{
		// The prefixed form, as misc-CombinedErrorCodes/XQST0058 writes it.
		`import schema namespace foo = "http://example.com/x";
		 import schema namespace bar = "http://example.com/x"; 1`,
		// The prefixless form names the same target namespace and is the
		// same fault.
		`import schema "http://example.com/x";
		 import schema "http://example.com/x"; 1`,
		// Mixed spellings still name one namespace.
		`import schema namespace foo = "http://example.com/x";
		 import schema "http://example.com/x"; 1`,
		// §4.11 normalises the URI literal before it is used as a namespace,
		// so two spellings that collapse to one namespace are one namespace.
		`import schema namespace foo = "http://example.com/x";
		 import schema namespace bar = "  http://example.com/x  "; 1`,
		// A declaration between the two imports must not hide the duplicate.
		`import schema namespace foo = "http://example.com/x";
		 declare namespace z = "http://example.com/z";
		 import schema namespace bar = "http://example.com/x"; 1`,
		// The scan steps over a declaration by finding its ";", so a ";"
		// inside a URI literal must not be mistaken for one.
		`import schema namespace foo = "http://example.com/a;b";
		 import schema namespace bar = "http://example.com/a;b"; 1`,
		// Nor one inside a comment.
		`import schema namespace foo = "http://example.com/x";
		 (: a comment with ; in it :)
		 import schema namespace bar = "http://example.com/x"; 1`,
	} {
		_, err := Compile(q, Options{})
		if err == nil {
			t.Errorf("want XQST0058, got no error for:\n%s", q)
			continue
		}
		if !strings.Contains(err.Error(), "XQST0058") {
			t.Errorf("want XQST0058, got %v for:\n%s", err, q)
		}
	}
}

// TestDistinctSchemaImportsAreNotXQST0058 is the other half: the rule is about
// one namespace imported twice, not about importing two schemas.
//
// Both imports here are unresolvable, so the query still fails — but it must
// fail on the missing schema, not on a duplicate that is not there. Without
// this the check above could be satisfied by refusing every second import.
func TestDistinctSchemaImportsAreNotXQST0058(t *testing.T) {
	for _, q := range []string{
		`import schema namespace foo = "http://example.com/x";
		 import schema namespace bar = "http://example.com/y"; 1`,
		// A module import of the same URI is a different kind of import and
		// does not collide with a schema import's namespace.
		`import schema namespace foo = "http://example.com/x";
		 import module namespace bar = "http://example.com/x"; 1`,
	} {
		_, err := Compile(q, Options{})
		if err != nil && strings.Contains(err.Error(), "XQST0058") {
			t.Errorf("XQST0058 is for one namespace imported twice; got it for:\n%s", q)
		}
	}
}

// TestOutputDeclarationInLibraryModuleIsXQST0108 pins §2.2.4: "It is a static
// error [err:XQST0108] if an output declaration occurs in a library module."
//
// Serialization describes a query's result and a library module has none, so
// the declaration could never take effect. The module here is otherwise
// perfectly well-formed and its function is never called — the error is owed
// for writing the declaration at all, not for using it.
func TestOutputDeclarationInLibraryModuleIsXQST0108(t *testing.T) {
	const ns = "http://example.com/lib"
	lib := `module namespace m = "` + ns + `";
		declare namespace output = "http://www.w3.org/2010/xslt-xquery-serialization";
		declare option output:indent "yes";
		declare function m:ok() { "ok" };`

	_, err := Compile(`import module namespace m = "`+ns+`"; m:ok()`,
		Options{Modules: []Module{{Namespace: ns, Source: lib}}})
	if err == nil {
		t.Fatal("an output declaration in a library module must be XQST0108, got no error")
	}
	if !strings.Contains(err.Error(), "XQST0108") {
		t.Fatalf("want XQST0108, got %v", err)
	}
}

// TestOutputDeclarationInMainModuleIsAccepted is the counterweight: XQST0108
// is about WHERE the declaration is written, not about the declaration.
//
// The identical option in a main module is legal and must still take effect,
// so a check that simply refused every output declaration would fail here.
func TestOutputDeclarationInMainModuleIsAccepted(t *testing.T) {
	q, err := Compile(`declare namespace output = "http://www.w3.org/2010/xslt-xquery-serialization";
		declare option output:indent "yes"; 1`, Options{})
	if err != nil {
		t.Fatalf("an output declaration in a main module is legal: %v", err)
	}
	if got := q.SerializationOptions()["indent"]; got != "yes" {
		t.Errorf(`the declaration must still take effect: indent = %q, want "yes"`, got)
	}
}
