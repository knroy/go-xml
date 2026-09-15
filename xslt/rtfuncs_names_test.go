package xslt

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// TestRuntimeFuncNamesMatchRegistration holds runtimeFuncNames to what
// registerRuntimeFuncs and registerGroupingFuncs actually bind.
//
// The list drives the XPST0017 check on match patterns, so drift is not
// cosmetic in either direction: a name registered but missing from the list
// makes a pattern calling it fail to compile, and a name in the list that
// nothing registers lets a genuinely undeclared function through.
//
// Both directions are computed from the library rather than compared against a
// hand-typed roster. The reverse direction used to list the expected names
// inline, which could only ever find a name that someone had already thought
// to add to it: a newly registered fn function -- or one dropped from the list
// and from the roster in the same edit -- was invisible to it.
func TestRuntimeFuncNamesMatchRegistration(t *testing.T) {
	lib := xpath.NewLibrary(nil)
	registerRuntimeFuncs(lib, nil)
	registerGroupingFuncs(lib)
	registerMergeFuncs(lib)
	registerOutputFuncs(lib)

	registered := registeredFnLocals(t, lib)
	if len(registered) == 0 {
		t.Fatal("no fn functions were enumerated; the enumeration itself is broken")
	}

	// Every name the list claims must really be registered at some arity.
	for name := range runtimeFuncNames {
		if !registered[name] {
			t.Errorf("runtimeFuncNames has %q, but the registrars do not bind it", name)
		}
	}

	// And every name they register in the fn namespace must be accounted for:
	// either by runtimeFuncNames, or by the deliberate exclusion below.
	// "runtime" is the internal opaque binding, not a callable function.
	for name := range registered {
		if name == "runtime" || statelessXSLTFuncs[name] {
			continue
		}
		if !runtimeFuncNames[name] {
			t.Errorf("the registrars bind %q, but neither runtimeFuncNames nor "+
				"statelessXSLTFuncs accounts for it", name)
		}
	}

	// The exclusion must stay honest in its own right: a name listed as a
	// deliberate omission that nothing registers any more is stale, and one
	// that has since been added to runtimeFuncNames is listed twice.
	for name := range statelessXSLTFuncs {
		if !registered[name] {
			t.Errorf("statelessXSLTFuncs excludes %q, but nothing registers it", name)
		}
		if runtimeFuncNames[name] {
			t.Errorf("%q is in both runtimeFuncNames and statelessXSLTFuncs", name)
		}
		// Whatever runtimeFuncNames omits, lateBoundFuncNames must still
		// carry, or a static check reports XPST0017 for a callable function.
		if !lateBoundFuncNames[name] {
			t.Errorf("%q is omitted from runtimeFuncNames but also absent from "+
				"lateBoundFuncNames, so a static check would reject it", name)
		}
	}
}

// statelessXSLTFuncs are the fn: functions registered per transform that
// runtimeFuncNames deliberately omits.
//
// They are XSLT-defined rather than XPath-defined, so they are bound into the
// per-transform library, but they close over no transform state -- which is
// what runtimeFuncNames is for. The omission is documented at
// lateBoundFuncNames in varfuncs.go, which is the map that does have to carry
// them, and the check above asserts that it does.
var statelessXSLTFuncs = map[string]bool{
	"unparsed-entity-uri":         true,
	"unparsed-entity-public-id":   true,
	"available-system-properties": true,
}

// registeredFnLocals returns the local names the library itself binds in the
// fn namespace, read out of its key map.
//
// Library exposes Lookup and Declares, which answer about a name the caller
// already has, and nothing that enumerates. Probing a list of guesses is what
// made the old reverse check vacuous, so the keys are read directly; the
// helper fails loudly rather than silently returning nothing if that shape
// ever changes.
func registeredFnLocals(t *testing.T, lib *xpath.Library) map[string]bool {
	t.Helper()
	v := reflect.ValueOf(lib).Elem().FieldByName("fns")
	if !v.IsValid() || v.Kind() != reflect.Map {
		t.Fatal("xpath.Library no longer has an fns map; update this enumeration")
	}
	prefix := "{" + xdm.NSFN + "}"
	out := map[string]bool{}
	for _, k := range v.MapKeys() {
		key := k.String() // "{uri}local#arity"
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		local := strings.TrimPrefix(key, prefix)
		if i := strings.LastIndex(local, "#"); i >= 0 {
			local = local[:i]
		}
		out[local] = true
	}
	return out
}

// TestRuntimeFuncNamesEnumerationIsNotEmpty pins the enumeration's own
// premise. If reflect ever stops reaching the map, the check above would
// compare against an empty set and report nothing rather than everything.
func TestRuntimeFuncNamesEnumerationIsNotEmpty(t *testing.T) {
	lib := xpath.NewLibrary(nil)
	registerRuntimeFuncs(lib, nil)
	registerGroupingFuncs(lib)
	registerMergeFuncs(lib)
	registerOutputFuncs(lib)

	got := registeredFnLocals(t, lib)
	// A sample that must be there whatever else moves.
	for _, name := range []string{"key", "current", "generate-id", "document"} {
		if !got[name] {
			names := make([]string, 0, len(got))
			for n := range got {
				names = append(names, n)
			}
			sort.Strings(names)
			t.Fatalf("enumeration missed %q; it found %v", name, names)
		}
	}
}
