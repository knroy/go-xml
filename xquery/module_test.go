package xquery_test

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xquery"
)

// Module import is XQuery 3.1 §4.12. The cases below are named for the QT3
// cases they follow where one exists, because the specification's prose leaves
// several of these to be settled by what the suite asserts — the version split
// on cyclic imports most of all.

// libPubPriv is module-pub-priv.xq from the suite, reduced to what the
// visibility rules need. It is the fixture modules-pub-priv-1 through -4 use.
const libPubPriv = `xquery version "3.0";
module namespace mod="http://www.w3.org/TestModules/module-pub-priv";
declare %public variable $mod:one := 1;
declare %private variable $mod:two := 2;
declare %private function mod:f() { 23 };
declare %public function mod:g($a as xs:integer) {
   mod:f() + $a + $mod:two - 2*$mod:one
};`

func ppModule() xquery.Module {
	return xquery.Module{
		Namespace: "http://www.w3.org/TestModules/module-pub-priv",
		Source:    libPubPriv,
	}
}

// TestModuleImportRegistered is modules-simple: a module registered with the
// compilation is found by its target namespace and its function is callable.
func TestModuleImportRegistered(t *testing.T) {
	lib := xquery.Module{
		Namespace: "http://www.w3.org/TestModules/test1",
		Source: `module namespace test1="http://www.w3.org/TestModules/test1";
declare variable $test1:flag := 1;
declare function test1:ok() as xs:string { "ok" };`,
	}
	got, err := run(t, `import module namespace test1="http://www.w3.org/TestModules/test1";
<result>{test1:ok()}</result>`, xquery.Options{Modules: []xquery.Module{lib}})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if want := `<result>ok</result>`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestModuleImportByNamespaceNoLocation is the import with no "at" clause. The
// module is located by target namespace alone, from what the caller
// registered, which is the only thing §4.12 guarantees a processor can do.
func TestModuleImportByNamespaceNoLocation(t *testing.T) {
	got, err := run(t,
		`import module namespace d="http://www.w3.org/TestModules/module-pub-priv"; $d:one`,
		xquery.Options{Modules: []xquery.Module{ppModule()}})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got != "1" {
		t.Errorf("got %q, want %q", got, "1")
	}
}

// TestModuleImportWrongNamespace is modules-bad-ns: a module registered under
// a namespace it does not declare answers no import. The store is keyed by
// namespace, but the module's own declaration is what settles its identity, so
// a mismatch is XQST0059 rather than a module silently imported under the
// wrong name.
func TestModuleImportWrongNamespace(t *testing.T) {
	mis := xquery.Module{
		Namespace: "http://www.w3.org/TestModules/test2",
		Source: `module namespace test1="http://www.w3.org/TestModules/test1";
declare function test1:ok() as xs:string { "ok" };`,
	}
	_, err := run(t, `import module namespace test2="http://www.w3.org/TestModules/test2"; test2:ok()`,
		xquery.Options{Modules: []xquery.Module{mis}})
	if err == nil || !strings.Contains(err.Error(), "XQST0059") {
		t.Errorf("want XQST0059 for a module declaring another namespace, got %v", err)
	}
}

// TestModulePublicIsVisible is modules-pub-priv-1. The public function is
// callable, and its body reaches the module's OWN private function and private
// variable — visibility is scoped to the module, not withheld from it.
func TestModulePublicIsVisible(t *testing.T) {
	got, err := run(t,
		`import module namespace defs="http://www.w3.org/TestModules/module-pub-priv"; defs:g(42)`,
		xquery.Options{Modules: []xquery.Module{ppModule()}})
	if err != nil {
		t.Fatalf("public function: %v", err)
	}
	// 23 (private f) + 42 + 2 (private $two) - 2*1 (public $one) = 65.
	if got != "65" {
		t.Errorf("got %q, want %q; a public body must see its own module's "+
			"private declarations", got, "65")
	}
}

// TestModulePrivateIsNotVisible is modules-pub-priv-2 and -4: neither a
// private function nor a private variable crosses the import.
func TestModulePrivateIsNotVisible(t *testing.T) {
	for _, c := range []struct{ src, code string }{
		{`import module namespace defs="http://www.w3.org/TestModules/module-pub-priv"; defs:f()`,
			"XPST0017"},
		{`import module namespace defs="http://www.w3.org/TestModules/module-pub-priv"; $defs:two`,
			"XPST0008"},
	} {
		_, err := run(t, c.src, xquery.Options{Modules: []xquery.Module{ppModule()}})
		if err == nil || !strings.Contains(err.Error(), c.code) {
			t.Errorf("%s: want %s, got %v", c.src, c.code, err)
		}
	}
}

// TestModuleMutualRecursionIsLegal is errata8-002a, and it is the case that
// settles the design.
//
// The same two mutually importing modules are XQST0093 at XQuery 1.0
// (errata8-002) and evaluate to 10 at XQuery 3.0 and later. So a cycle in the
// import graph is NOT an error: §4.12 permits mutually recursive modules, and
// only a circularity among the values is left, which is dynamic.
func TestModuleMutualRecursionIsLegal(t *testing.T) {
	a := xquery.Module{Namespace: "http://www.w3.org/TestModules/errata8_2a",
		Source: `module namespace a="http://www.w3.org/TestModules/errata8_2a";
import module namespace b="http://www.w3.org/TestModules/errata8_2b";
declare function a:fun() { $b:var };
declare function a:fun2() { 10 };`}
	b := xquery.Module{Namespace: "http://www.w3.org/TestModules/errata8_2b",
		Source: `module namespace b="http://www.w3.org/TestModules/errata8_2b";
import module namespace a="http://www.w3.org/TestModules/errata8_2a";
declare variable $b:var := a:fun2();`}
	got, err := run(t,
		`import module namespace a="http://www.w3.org/TestModules/errata8_2a"; a:fun()`,
		xquery.Options{Modules: []xquery.Module{a, b}})
	if err != nil {
		t.Fatalf("mutually recursive modules must be legal at 3.0+: %v", err)
	}
	if got != "10" {
		t.Errorf("got %q, want %q", got, "10")
	}
}

// TestModuleAcyclicImportChain is errata8-003: a module importing another with
// no cycle at all, which must not be disturbed by the cycle handling.
func TestModuleAcyclicImportChain(t *testing.T) {
	a := xquery.Module{Namespace: "http://x/3a",
		Source: `module namespace a="http://x/3a";
import module namespace b="http://x/3b";
declare function a:fun() { $b:var };`}
	b := xquery.Module{Namespace: "http://x/3b",
		Source: `module namespace b="http://x/3b";
declare variable $b:var := 10;`}
	got, err := run(t, `import module namespace a="http://x/3a"; a:fun()`,
		xquery.Options{Modules: []xquery.Module{a, b}})
	if err != nil {
		t.Fatalf("acyclic chain: %v", err)
	}
	if got != "10" {
		t.Errorf("got %q, want %q", got, "10")
	}
}

// TestModuleVariableCircularity is modules-28a: two modules whose global
// variables genuinely need each other's values.
//
// This is the error the removal of XQST0093 leaves behind, and it is DYNAMIC.
// Once mutually recursive modules are legal there is no static order for the
// variables of two modules to be in, so a circularity among them can only be
// found by evaluating — XQDY0054, not XQST0054.
func TestModuleVariableCircularity(t *testing.T) {
	d1 := xquery.Module{Namespace: "http://www.w3.org/TestModules/defs1",
		Source: `module namespace defs1="http://www.w3.org/TestModules/defs1";
import module namespace defs2="http://www.w3.org/TestModules/defs2";
declare variable $defs1:var as xs:integer := $defs2:var;`}
	d2 := xquery.Module{Namespace: "http://www.w3.org/TestModules/defs2",
		Source: `module namespace defs2="http://www.w3.org/TestModules/defs2";
import module namespace defs1="http://www.w3.org/TestModules/defs1";
declare variable $defs2:var as xs:integer := $defs1:var;`}
	_, err := run(t,
		`import module namespace defs1="http://www.w3.org/TestModules/defs1"; $defs1:var`,
		xquery.Options{Modules: []xquery.Module{d1, d2}})
	if err == nil || !strings.Contains(err.Error(), "XQDY0054") {
		t.Errorf("want XQDY0054 for a circular variable initialisation across "+
			"modules, got %v", err)
	}
}

// TestModuleInitialisationOrder checks that a global in one module which
// depends on a global in another is initialised after it, whichever order the
// declarations happen to be walked in.
func TestModuleInitialisationOrder(t *testing.T) {
	a := xquery.Module{Namespace: "http://x/oa",
		Source: `module namespace a="http://x/oa";
import module namespace b="http://x/ob";
declare variable $a:v := $b:v * 2;`}
	b := xquery.Module{Namespace: "http://x/ob",
		Source: `module namespace b="http://x/ob";
declare variable $b:v := 21;`}
	got, err := run(t, `import module namespace a="http://x/oa"; $a:v`,
		xquery.Options{Modules: []xquery.Module{a, b}})
	if err != nil {
		t.Fatalf("initialisation order: %v", err)
	}
	if got != "42" {
		t.Errorf("got %q, want %q", got, "42")
	}
}

// TestModuleDuplicateFunction is XQST0034: a function declared both by an
// imported module and by the importing module. §4.12 adds the imported
// declarations to the importing module's static context, so the clash is the
// one a duplicate declaration in a single module raises.
//
// The two colliding declarations cannot be in two different imported modules,
// because XQST0048 already requires each to declare only into its own target
// namespace and two modules cannot share one -- so the importing module is the
// other party.
func TestModuleDuplicateFunction(t *testing.T) {
	a := xquery.Module{Namespace: "http://x/da",
		Source: `module namespace a="http://x/da";
declare function a:dup() { 1 };`}
	_, err := run(t, `import module namespace a="http://x/da";
declare namespace b="http://x/da";
declare function b:dup() { 2 };
1`, xquery.Options{Modules: []xquery.Module{a}})
	if err == nil || !strings.Contains(err.Error(), "XQST0034") {
		t.Errorf("want XQST0034 for one function declared twice, got %v", err)
	}
}

// TestModuleDuplicateVariable is XQST0049, the variable half of the rule
// above.
func TestModuleDuplicateVariable(t *testing.T) {
	a := xquery.Module{Namespace: "http://x/va",
		Source: `module namespace a="http://x/va";
declare variable $a:dup := 1;`}
	_, err := run(t, `import module namespace a="http://x/va";
declare namespace b="http://x/va";
declare variable $b:dup := 2;
1`, xquery.Options{Modules: []xquery.Module{a}})
	if err == nil || !strings.Contains(err.Error(), "XQST0049") {
		t.Errorf("want XQST0049 for one variable declared twice, got %v", err)
	}
}

// TestModuleForeignNamespaceDeclaration is XQST0048: a library module may only
// declare into its own target namespace. modules-17 is the case.
func TestModuleForeignNamespaceDeclaration(t *testing.T) {
	bad := xquery.Module{Namespace: "http://x/own",
		Source: `module namespace m="http://x/own";
declare namespace other="http://x/other";
declare variable $other:v := 1;`}
	_, err := run(t, `import module namespace m="http://x/own"; 1`,
		xquery.Options{Modules: []xquery.Module{bad}})
	if err == nil || !strings.Contains(err.Error(), "XQST0048") {
		t.Errorf("want XQST0048 for a declaration outside the module's target "+
			"namespace, got %v", err)
	}
}

// TestModuleImportedTwice is XQST0047: one target namespace imported twice in
// one prolog.
func TestModuleImportedTwice(t *testing.T) {
	_, err := run(t, `import module namespace a="http://x/pp2";
import module namespace b="http://x/pp2"; 1`,
		xquery.Options{Modules: []xquery.Module{{Namespace: "http://x/pp2",
			Source: `module namespace m="http://x/pp2"; declare variable $m:v := 1;`}}})
	if err == nil || !strings.Contains(err.Error(), "XQST0047") {
		t.Errorf("want XQST0047 for a namespace imported twice, got %v", err)
	}
}

// TestModuleSelfImport is XQST0073: a module that imports itself. This is the
// one cycle that is an error at every version, because it is not mutual
// recursion — the module's own declarations would be contributed to it twice.
func TestModuleSelfImport(t *testing.T) {
	self := xquery.Module{Namespace: "http://x/self",
		Source: `module namespace s="http://x/self";
import module namespace s2="http://x/self";
declare variable $s:v := 1;`}
	_, err := run(t, `import module namespace s="http://x/self"; $s:v`,
		xquery.Options{Modules: []xquery.Module{self}})
	if err == nil || !strings.Contains(err.Error(), "XQST0073") {
		t.Errorf("want XQST0073 for a module importing itself, got %v", err)
	}
}

// --- security ---------------------------------------------------------------
//
// The three tests below are the ones docs/security.md's claims about this
// feature rest on. Each was validated by sabotage: see the CHANGELOG entry.

// TestNoResolverDoesNotFetch is the central security property. With no
// ModuleResolver configured, an "at" location is NOT opened — not tried and
// failed, not opened — so a query cannot read a file by naming it, and the
// import fails cleanly with XQST0059.
//
// The location named is one that exists on every Unix, so a resolver that
// defaulted to the filesystem would succeed in reading it and fail somewhere
// later with a parse error. XQST0059 is what proves nothing was opened.
func TestNoResolverDoesNotFetch(t *testing.T) {
	_, err := run(t,
		`import module namespace z="http://x/absent" at "/etc/passwd"; 1`,
		xquery.Options{})
	if err == nil {
		t.Fatal("a query with no resolver must not resolve an \"at\" location")
	}
	if !strings.Contains(err.Error(), "XQST0059") {
		t.Errorf("want XQST0059, got %v", err)
	}
	// The wording must not suggest the file was looked at: a message naming a
	// parse failure or a permission error would mean it had been.
	if strings.Contains(err.Error(), "passwd") {
		t.Errorf("the error names the location, which means it was opened: %v", err)
	}
}

// TestNoResolverIsDistinguishable checks that "you configured no resolver" is
// separable from "no such module", as xsd.errNoResolver is. The XQST0059 the
// query sees is the same either way, because that is the code §4.12 gives.
func TestNoResolverIsDistinguishable(t *testing.T) {
	_, err := run(t, `import module namespace z="http://x/absent"; 1`,
		xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "ModuleResolver") {
		t.Errorf("the refusal should name the option that would allow it, got %v", err)
	}
}

// countingResolver answers every namespace with a module that imports the
// next, so that one import expands without bound. It is how the module-count
// budget is exercised without a filesystem.
type countingResolver struct{ n int }

func (r *countingResolver) Resolve(ns string, hints []string, base string) (
	io.ReadCloser, string, error) {
	r.n++
	// Each module imports the next, so the graph never terminates on its own.
	src := fmt.Sprintf(
		`module namespace m%[1]d="%[2]s";
import module namespace nxt="%[2]s/x";
declare variable $m%[1]d:v := 1;`, r.n, ns)
	return io.NopCloser(strings.NewReader(src)), "", nil
}

// TestMaxModulesIsEnforced checks the count budget, and checks that exceeding
// it FAILS rather than compiling against the modules that fitted.
//
// The error must wrap xdm.ErrResourceLimit and must NOT be XQST0059: the
// budget declined to answer, and saying "no such module" would be a claim
// about the store that is not true. This is the invariant docs/security.md
// states — a resource budget may decline, but must never turn "I could not
// prove the constraint" into "the constraint holds".
func TestMaxModulesIsEnforced(t *testing.T) {
	_, err := run(t, `import module namespace a="urn:start"; 1`,
		xquery.Options{ModuleResolver: &countingResolver{}, MaxModules: 8})
	if err == nil {
		t.Fatal("an unbounded import graph must be refused, not compiled")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("the refusal must wrap xdm.ErrResourceLimit so a caller can "+
			"tell a budget from a fault; got %v", err)
	}
	if strings.Contains(err.Error(), "XQST0059") {
		t.Errorf("a budget refusal must not claim the module was not found: %v", err)
	}
}

// hugeResolver answers with a module far larger than any byte budget.
type hugeResolver struct{ size int }

func (r hugeResolver) Resolve(ns string, hints []string, base string) (
	io.ReadCloser, string, error) {
	// A valid module followed by a comment of arbitrary length: the size is
	// in the text rather than in the declarations, so what the budget refuses
	// is bytes read and not work done after parsing.
	head := fmt.Sprintf(`module namespace m="%s";
declare variable $m:v := 1;
(: `, ns)
	return io.NopCloser(io.MultiReader(
		strings.NewReader(head),
		io.LimitReader(neverEnding{}, int64(r.size)),
		strings.NewReader(` :)`),
	)), "", nil
}

// neverEnding is an infinite reader of one byte, so that a resolver can offer
// more text than any budget without allocating it.
type neverEnding struct{}

func (neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// TestMaxModuleBytesIsEnforced is the third security property: a resolver that
// returns a huge module is bounded, and the read is bounded as it happens
// rather than after the whole thing is in memory.
func TestMaxModuleBytesIsEnforced(t *testing.T) {
	_, err := run(t, `import module namespace a="urn:huge"; 1`,
		xquery.Options{
			ModuleResolver: hugeResolver{size: 1 << 20},
			MaxModuleBytes: 4096,
		})
	if err == nil {
		t.Fatal("a module larger than MaxModuleBytes must be refused")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("want a refusal wrapping xdm.ErrResourceLimit, got %v", err)
	}
}

// TestModuleBudgetIsCumulative checks that the byte budget is spent across the
// whole compilation rather than per module: a budget reset per import would
// bound nothing, since a graph of small modules could be arbitrarily large in
// total.
func TestModuleBudgetIsCumulative(t *testing.T) {
	// Four modules of roughly 100 bytes each against a 250-byte budget: each
	// one fits, the set does not.
	var mods []xquery.Module
	src := `import module namespace a="urn:c0"; 1`
	for i := 0; i < 4; i++ {
		next := ""
		if i < 3 {
			next = fmt.Sprintf(
				"import module namespace n%d=\"urn:c%d\";", i+1, i+1)
		}
		mods = append(mods, xquery.Module{
			Namespace: fmt.Sprintf("urn:c%d", i),
			Source: fmt.Sprintf(
				"module namespace m%d=\"urn:c%d\";\n%s\ndeclare variable $m%d:v := 1;",
				i, i, next, i),
		})
	}
	_, err := run(t, src, xquery.Options{Modules: mods, MaxModuleBytes: 250})
	if err == nil || !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("the byte budget must be cumulative across modules; got %v", err)
	}
}

// TestMapModuleResolverIgnoresLocations checks that MapModuleResolver answers
// for its own keys and for nothing else — a query naming an "at" location it
// does not know gets XQST0059 rather than a fetch.
func TestMapModuleResolverIgnoresLocations(t *testing.T) {
	r := xquery.MapModuleResolver{Modules: map[string]string{
		"urn:known": `module namespace k="urn:known"; declare function k:v() { 7 };`,
	}}
	got, err := run(t, `import module namespace k="urn:known" at "/etc/passwd"; k:v()`,
		xquery.Options{ModuleResolver: r})
	if err != nil {
		t.Fatalf("a known namespace should resolve from the table: %v", err)
	}
	if got != "7" {
		t.Errorf("got %q, want %q", got, "7")
	}
	if _, err := run(t, `import module namespace u="urn:unknown" at "/etc/passwd"; 1`,
		xquery.Options{ModuleResolver: r}); err == nil ||
		!strings.Contains(err.Error(), "XQST0059") {
		t.Errorf("an unknown namespace must be XQST0059, not a fetch; got %v", err)
	}
}

// TestModuleImportNoBodyIsRefused is K2-ModuleProlog-1's other half: a library
// module with a query body is a grammar error, checked here through the import
// path rather than through Compile.
func TestModuleImportNoBodyIsRefused(t *testing.T) {
	bad := xquery.Module{Namespace: "http://x/body",
		Source: `module namespace b="http://x/body";
declare variable $b:v := 1;
"an expression"`}
	_, err := run(t, `import module namespace b="http://x/body"; $b:v`,
		xquery.Options{Modules: []xquery.Module{bad}})
	if err == nil || !strings.Contains(err.Error(), "XPST0003") {
		t.Errorf("want XPST0003 for a library module with a query body, got %v", err)
	}
}

// TestModuleImportEmptyNamespace is XQST0088, which checkImportSyntax already
// raised before any of this existed; it is here so that the rule is not lost
// when the import stops being refused outright.
func TestModuleImportEmptyNamespace(t *testing.T) {
	_, err := run(t, `import module namespace m = ""; 1`, xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "XQST0088") {
		t.Errorf("want XQST0088 for an empty target namespace, got %v", err)
	}
}
