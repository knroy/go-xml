package xpath

import (
	"strings"
	"sync"

	"github.com/knroy/go-xml/xdm"
)

// This file holds the FunctionSpec table and the families that have been
// migrated onto it.
//
// The plan requires this to land "in reviewable family-sized commits, not as
// an untestable 322-entry hand edit". What is here is therefore the mechanism
// plus one migrated family — the seventeen entries builtinSignatures already
// carried — which is what proves the path from a declared type to an
// XPTY0004 at call binding actually runs. The normalized data source for the
// remaining families is xpath/spec/function-signatures.json, extracted from
// the vendored Recommendation by cmd/genfunctions; see funcspec_manifest.go.
//
// To migrate a family, a family agent adds its "local/arity" keys to
// specSignatures below, using the spellings the manifest already holds for
// them, and runs the QT3 lanes. Nothing else changes: registration, lookup
// and the callbacks are untouched, because a spec constrains an existing
// registration rather than replacing it.

// specSignatures is the migrated portion of the manifest, keyed by
// "local/arity" in the fn: namespace, as the return type followed by the
// parameter types.
//
// It is seeded from builtinSignatures — the same seventeen entries, in the
// same spelling and the same order — because those were already written
// against the specification's function summary and already read by function
// subtyping. Reusing them means the first family to go through the new path
// is one whose declared types were reviewed before, so a behaviour change
// here would be a defect in the mechanism rather than in the data.
//
// Every entry below is verified against xpath/spec/function-signatures.json
// by TestMigratedSignaturesMatchManifest, so a hand-edited spelling that
// disagrees with the Recommendation fails the build rather than silently
// constraining a function wrongly.
// The fn:substring and fn:subsequence rows are added on top of those
// seventeen, and they are what make the EMPTY arm of the check live. The
// seventeen all declare their one parameter "?" or "*", so they exercise only
// the too-many-items arm; migrating a family whose proforma has a
// non-nullable parameter is what puts the twelve-function defect class — an
// empty sequence passed where F&O declares no "?" — behind the mechanism
// instead of behind twelve hand-written guards. These two are precisely the
// functions commit 7668773 fixed by hand, so the structural check and the
// hand fix now answer the same question, and the hand fix's tests are what
// prove they answer it the same way.
var specSignatures = func() map[string][]string {
	m := make(map[string][]string, len(builtinSignatures)+18)
	for k, v := range builtinSignatures {
		m[k] = v
	}
	m["substring/2"] = []string{"xs:string", "xs:string?", "xs:double"}
	m["substring/3"] = []string{"xs:string", "xs:string?", "xs:double", "xs:double"}
	m["subsequence/2"] = []string{"item()*", "item()*", "xs:double"}
	m["subsequence/3"] = []string{"item()*", "item()*", "xs:double", "xs:double"}

	// The numeric family, F&O 3.1 4.4 and 4.5, taken from the manifest.
	//
	// Most of its parameters are declared "?" or "*", so they constrain
	// nothing new; the rows that do are fn:round/2 and
	// fn:round-half-to-even/2, whose $precision is xs:integer with no "?",
	// and fn:min/2 and fn:max/2, whose $collation is xs:string with no "?".
	// Those four are the empty-sequence arm this family adds, and they are
	// the same defect class commit 7668773 fixed by hand for fn:round/1.
	m["abs/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["ceiling/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["floor/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["round/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["round/2"] = []string{"xs:numeric?", "xs:numeric?", "xs:integer"}
	m["round-half-to-even/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["round-half-to-even/2"] = []string{"xs:numeric?", "xs:numeric?", "xs:integer"}
	m["avg/1"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*"}
	m["min/1"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*"}
	m["min/2"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*", "xs:string"}
	m["max/1"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*"}
	m["max/2"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*", "xs:string"}
	m["sum/1"] = []string{"xs:anyAtomicType", "xs:anyAtomicType*"}
	m["sum/2"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*", "xs:anyAtomicType?"}
	return m
}()

// functionSpecs is the parsed table, built once and shared, keyed by
// specKey(name, arity).
//
// It is separate from Library.fns rather than a field on Function because a
// Function value is copied on every Lookup: hanging parsed types off it would
// copy a slice header per call for data that never varies. The table is
// immutable after construction, so a lookup needs no lock.
var (
	functionSpecsOnce sync.Once
	functionSpecs     map[string][]SequenceType
)

// lookupSpecParams returns the declared parameter types of a function, if the
// manifest covers it.
//
// A miss is the ordinary case while migration is in progress and means "bind
// this call exactly as before". That is what makes filling a family a pure
// narrowing rather than a behaviour change with a blast radius.
func lookupSpecParams(name xdm.QName, arity int) ([]SequenceType, bool) {
	functionSpecsOnce.Do(buildFunctionSpecs)
	p, ok := functionSpecs[specKey(name, arity)]
	return p, ok
}

// buildFunctionSpecs parses the migrated spellings once.
//
// A spelling this package cannot parse is skipped rather than panicking: an
// unparseable declared type must constrain nothing, on the same reasoning
// that makes an unknown spelling subsume only itself in subtype.go. The
// enforcement test reports such an entry, so it cannot hide.
func buildFunctionSpecs() {
	functionSpecs = map[string][]SequenceType{}
	for key, sig := range specSignatures {
		slash := strings.IndexByte(key, '/')
		if slash < 0 || len(sig) < 1 {
			continue
		}
		local, arity := key[:slash], int(key[slash+1]-'0')
		name := xdm.QName{URI: xdm.NSFN, Local: local}
		params, ok := parseSpellings(sig[1:]) // sig[0] is the return type
		if !ok || len(params) != arity {
			continue
		}
		functionSpecs[specKey(name, arity)] = params
	}
}

// parseSpellings parses a run of sequence-type spellings, answering false if
// any of them is one this package cannot read.
func parseSpellings(spellings []string) ([]SequenceType, bool) {
	out := make([]SequenceType, 0, len(spellings))
	for _, s := range spellings {
		st, err := ParseSequenceType(s, nil)
		if err != nil {
			return nil, false
		}
		out = append(out, st)
	}
	return out, true
}
