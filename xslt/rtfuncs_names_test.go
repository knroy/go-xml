package xslt

import (
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
func TestRuntimeFuncNamesMatchRegistration(t *testing.T) {
	lib := xpath.NewLibrary(nil)
	registerRuntimeFuncs(lib, nil)
	registerGroupingFuncs(lib)
	registerMergeFuncs(lib)
	registerOutputFuncs(lib)

	// Every name the list claims must really be registered at some arity.
	for name := range runtimeFuncNames {
		found := false
		for arity := 0; arity <= 3 && !found; arity++ {
			_, found = lib.Lookup(xdm.QName{URI: xdm.NSFN, Local: name}, arity)
		}
		if !found {
			t.Errorf("runtimeFuncNames has %q, but registerRuntimeFuncs does not bind it", name)
		}
	}

	// And every name it registers in the fn namespace must be in the list.
	// "runtime" is the internal opaque binding, not a callable function.
	for _, name := range []string{
		"current", "current-group", "current-grouping-key", "document",
		"element-available", "function-available", "generate-id", "key",
		"regex-group", "system-property", "type-available",
		"current-merge-group", "current-merge-key", "current-output-uri",
	} {
		if !runtimeFuncNames[name] {
			t.Errorf("registerRuntimeFuncs binds %q, but runtimeFuncNames omits it", name)
		}
	}
}
