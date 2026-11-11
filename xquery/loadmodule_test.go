package xquery_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/fileuri"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xquery"
)

// lxmModule is the library module the fn:load-xquery-module tests load.
var lxmModule = xquery.Module{Namespace: "urn:lxm", Source: `
module namespace m = "urn:lxm";
declare variable $m:v := 1;
declare variable $m:ext as xs:integer external := 10;
declare %private variable $m:hidden := 2;
declare function m:f($x) { $x * 2 };
declare function m:f($x, $y) { $x + $y + $m:ext };
declare %private function m:p() { 3 };`}

// TestLoadXQueryModule calls fn:load-xquery-module from a query whose module
// store holds the module: the result's functions and variables, the options,
// and every error code F&O 3.1 section 14.6.1 gives.
func TestLoadXQueryModule(t *testing.T) {
	const load = `load-xquery-module("urn:lxm"`
	bad := xquery.Module{Namespace: "urn:bad", Source: `module namespace b = "urn:bad"; declare function b:f( {`}
	for _, c := range []struct {
		name, query, want, code string
	}{
		{"function", load + `)("functions")(QName("urn:lxm", "f"))(1)(21)`, "42", ""},
		{"function reads a module global", load + `)("functions")(QName("urn:lxm", "f"))(2)(1, 2)`, "13", ""},
		{"variable", load + `)("variables")(QName("urn:lxm", "v"))`, "1", ""},
		{"external variable supplied", load + `, map{"variables": map{QName("urn:lxm", "ext"): 5}})("functions")(QName("urn:lxm", "f"))(2)(1, 2)`, "8", ""},
		{"private declarations are not listed", `let $m := ` + load + `) return count(map:keys($m("variables"))) * 10 + count(map:keys($m("functions")))`, "21", ""},
		{"vendor-options ignored", load + `, map{"vendor-options": map{QName("urn:x", "o"): 1}})("variables")(QName("urn:lxm", "v"))`, "1", ""},
		{"empty URI", `load-xquery-module("")`, "", "FOQM0001"},
		{"no such module", `load-xquery-module("urn:none")`, "", "FOQM0002"},
		{"static error in the module", `load-xquery-module("urn:bad")`, "", "FOQM0003"},
		{"variable of the wrong type", load + `, map{"variables": map{QName("urn:lxm", "ext"): "five"}})`, "", "FOQM0005"},
		{"unsupported version", load + `, map{"xquery-version": 4.0})`, "", "FOQM0006"},
		{"URI of the wrong type", `load-xquery-module(1)`, "", "XPTY0004"},
		{"option of the wrong type", load + `, map{"xquery-version": "3.1"})`, "", "XPTY0004"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := run(t, c.query, xquery.Options{Modules: []xquery.Module{lxmModule, bad}})
			if c.code != "" {
				if xdm.ErrorCode(err) != c.code {
					t.Errorf("err = %v, want %s", err, c.code)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

// TestLoadXQueryModuleReadsOnlyThroughTheResolver is the sandbox: a location
// hint naming a module file that exists is not opened when the caller has no
// resolver, exactly as an "import module ... at" is not. The refusal says
// which option would allow it rather than anything about the file.
func TestLoadXQueryModuleReadsOnlyThroughTheResolver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.xqm")
	if err := os.WriteFile(path, []byte(lxmModule.Source), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, `load-xquery-module("urn:lxm", map{"location-hints": "`+
		fileuri.Of(path)+`"})`, xquery.Options{})
	if xdm.ErrorCode(err) != "FOQM0002" || !strings.Contains(err.Error(), "ModuleResolver") {
		t.Errorf("err = %v, want FOQM0002 naming ModuleResolver", err)
	}
}

// TestLoadXQueryModuleSpendsTheCallersBudget: the caller's MaxModules governs
// the load, and running out is a resource limit rather than FOQM0003.
func TestLoadXQueryModuleSpendsTheCallersBudget(t *testing.T) {
	_, err := run(t, `load-xquery-module("urn:start")`,
		xquery.Options{ModuleResolver: &countingResolver{}, MaxModules: 4})
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("err = %v, want one wrapping xdm.ErrResourceLimit", err)
	}
}

// TestLoadXQueryModuleFromXPath: an XPath host gets the function by
// importing this package and installing Env.Modules.
func TestLoadXQueryModuleFromXPath(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx = ctx.WithVersion(xpath.XPath31)
	ctx = ctx.WithEnv(func(e *xpath.Env) {
		e.Modules = xquery.MapModuleResolver{Modules: map[string]string{"urn:lxm": lxmModule.Source}}
	})
	seq, err := xpath.Eval(`load-xquery-module("urn:lxm")("functions")(QName("urn:lxm", "f"))(1)(4)`, ctx, nil)
	if err != nil || render(seq) != "8" {
		t.Errorf("got %q, %v; want 8", render(seq), err)
	}
}

// lxmResolver serves lxmModule and counts the reads.
type lxmResolver struct{ n int }

func (r *lxmResolver) Resolve(ns string, hints []string, base string) (
	io.ReadCloser, string, error) {
	r.n++
	return io.NopCloser(strings.NewReader(lxmModule.Source)), "", nil
}

// TestLoadXQueryModuleIsCachedAndBudgeted: within one evaluation a repeated
// call reads and compiles the module once, and distinct calls spend one
// cumulative MaxModuleBytes. Each call compiled afresh with a new allowance:
// five calls made five resolver reads, and five calls with distinct hints
// read five times a budget meant to bound the evaluation.
func TestLoadXQueryModuleIsCachedAndBudgeted(t *testing.T) {
	r := &lxmResolver{}
	_, err := run(t, `for $i in 1 to 5 return load-xquery-module("urn:lxm")("variables")(QName("urn:lxm", "v"))`,
		xquery.Options{ModuleResolver: r})
	if err != nil {
		t.Fatal(err)
	}
	if r.n != 1 {
		t.Errorf("5 calls read the module %d times, want 1", r.n)
	}
	_, err = run(t, `for $i in 1 to 5 return load-xquery-module("urn:lxm", map{"location-hints": "h" || $i})`,
		xquery.Options{ModuleResolver: &lxmResolver{}, MaxModuleBytes: 3 * int64(len(lxmModule.Source))})
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("err = %v, want the cumulative MaxModuleBytes refusal", err)
	}
}
