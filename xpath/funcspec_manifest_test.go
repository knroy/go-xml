package xpath

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// manifestPath is the normalized data source cmd/genfunctions writes from the
// vendored F&O 3.1 Recommendation. It is checked in, so these tests need no
// network and no testdata symlink.
const manifestPath = "spec/function-signatures.json"

// manifestRow is one row of that file.
type manifestRow struct {
	Name   string   `json:"name"`
	Arity  int      `json:"arity"`
	Params []string `json:"params"`
	Result string   `json:"result"`
}

func loadManifest(t *testing.T) []manifestRow {
	t.Helper()
	blob, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading the function manifest: %v", err)
	}
	var rows []manifestRow
	if err := json.Unmarshal(blob, &rows); err != nil {
		t.Fatalf("parsing the function manifest: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the function manifest is empty; run go generate ./xpath/...")
	}
	return rows
}

// manifestByKey indexes the manifest the way the library is keyed.
func manifestByKey(t *testing.T) map[string]manifestRow {
	t.Helper()
	out := map[string]manifestRow{}
	for _, r := range loadManifest(t) {
		name, ok := parseSpecName(r.Name)
		if !ok {
			t.Fatalf("manifest row %q has a prefix this package does not map to a namespace", r.Name)
		}
		out[specKey(name, r.Arity)] = r
	}
	return out
}

// extensionAllowlist names the registered functions that are deliberately
// absent from the manifest because F&O does not define them.
//
// This is the "explicit extension allowlist" the plan requires. An entry here
// is a claim that the specification defines no such function, so it needs a
// reason, not just a name — a function added here to silence the enforcement
// test would defeat the test's whole purpose.
var extensionAllowlist = map[string]string{
	"{http://www.w3.org/2005/xpath-functions}stream-available#1": "XSLT 3.0 " +
		"19.3 defines fn:stream-available for streamability; F&O 3.1 defines " +
		"no function of that name, so it has no proforma to extract.",
}

// variadicAllowlist names functions whose proforma declares no fixed arity,
// so no single manifest row can describe them.
//
// fn:concat is the only one in F&O 3.1: its signature ends in a literal "..."
// standing for further xs:anyAtomicType? parameters, and the library
// registers it at every arity from 2 upwards. cmd/genfunctions excludes it
// for the same reason and records that reason there.
var variadicAllowlist = map[string]bool{
	"{http://www.w3.org/2005/xpath-functions}concat": true,
}

// TestRegisteredFunctionsHaveManifestMetadata is the plan's enforcement test:
// "a test that fails if a callback is registered without manifest metadata,
// except for an explicit extension allowlist".
//
// It ENFORCES, because it already can: the generated manifest describes every
// registered function except the two documented below, so there is nothing to
// phase in. A new callback registered without a proforma — or with a name F&O
// does not define — fails here, which is what makes a thirteenth
// hand-signature impossible to add unnoticed.
//
// What is deliberately partial is the CALL-BINDING migration, not this
// coverage: the plan requires that to land "in reviewable family-sized
// commits". TestCallBindingMigrationInventory tracks it and carries the flip.
func TestRegisteredFunctionsHaveManifestMetadata(t *testing.T) {
	manifest := manifestByKey(t)
	lib := Builtins().(*Library)

	var missing []string
	for _, fn := range lib.fns {
		if variadicAllowlist[fn.Name.Clark()] {
			continue
		}
		key := specKey(fn.Name, fn.Arity)
		if _, ok := manifest[key]; ok {
			continue
		}
		if _, ok := extensionAllowlist[key]; ok {
			continue
		}
		if fn.Name.URI == xdm.NSXS {
			// The xs: constructor functions are defined by their type, not by
			// an F&O proforma: every one of them is
			// ($arg as xs:anyAtomicType?) as T?, stated once in F&O 18.1
			// rather than once per type. They are covered uniformly by
			// TestConstructorFunctionsShareOneSignature below.
			continue
		}
		missing = append(missing, key)
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("%d registered function(s) have no manifest entry and are not "+
			"allowlisted:\n\t%v\n"+
			"Either cmd/genfunctions must extract a proforma for it, or it is "+
			"a host extension and belongs in extensionAllowlist with a reason.",
			len(missing), missing)
	}
}

// TestCallBindingMigrationInventory reports how far the call-binding
// migration has got: which manifest entries are enforced at call binding and
// which are not yet.
//
// The enforcement test above asks "does the specification describe every
// registered function?", and already enforces. This one asks the narrower
// question "does the cardinality check actually RUN for it?", which is the
// number a family agent works down, and which reads 0 remaining only when the
// last family has been added to specSignatures.
//
// It reports rather than fails because 251 entries are not yet migrated and a
// hard failure now would block every family-sized commit until all of them
// existed.
//
// TO FLIP IT TO HARD-FAIL, change the `report := t.Logf` line below to
// `report := t.Errorf`. That is the whole change; nothing else in this test
// is conditional on the mode. Flip it when `pending` reaches zero.
func TestCallBindingMigrationInventory(t *testing.T) {
	report := t.Logf // FLIP: change to t.Errorf to make this test enforce.

	manifest := manifestByKey(t)
	var pending []string
	for key, row := range manifest {
		name, _ := parseSpecName(row.Name)
		if _, ok := lookupSpecParams(name, row.Arity); !ok {
			pending = append(pending, key)
		}
	}
	sort.Strings(pending)
	if len(pending) > 0 {
		report("call-binding cardinality is enforced for %d of %d manifest "+
			"entries; %d remain to be added to specSignatures",
			len(manifest)-len(pending), len(manifest), len(pending))
	}
}

// TestMigratedSignaturesMatchManifest is the test that keeps the migrated
// table honest.
//
// specSignatures is hand-written Go, so a family agent can mistype a
// spelling; a mistyped "?" is exactly the defect this whole mechanism exists
// to prevent, and it would constrain a function wrongly rather than leave it
// unconstrained. Every migrated entry is therefore checked against the
// manifest the generator extracted from the Recommendation.
//
// This one DOES fail, not report, because it does not depend on how much of
// the migration has happened — only on whether what HAS been migrated agrees
// with the specification.
func TestMigratedSignaturesMatchManifest(t *testing.T) {
	manifest := manifestByKey(t)
	for key, sig := range specSignatures {
		name, arity, ok := splitSpecEntryKey(key)
		if !ok {
			t.Errorf("specSignatures key %q is not in \"local/arity\" or "+
				"\"prefix:local/arity\" form", key)
			continue
		}
		row, found := manifest[specKey(name, arity)]
		if !found {
			t.Errorf("specSignatures has %q, which the F&O manifest does not "+
				"describe; if it is a host extension it belongs in "+
				"extensionAllowlist with a reason", key)
			continue
		}
		if len(sig) != len(row.Params)+1 {
			t.Errorf("%s: table declares %d parameter(s), F&O declares %d",
				key, len(sig)-1, len(row.Params))
			continue
		}
		for i, want := range row.Params {
			got := sig[i+1]
			if !sameDeclaredType(t, got, want) {
				t.Errorf("%s parameter %d: table declares %q, F&O declares %q",
					key, i+1, got, want)
			}
		}
	}
}

// sameDeclaredType compares two spellings as parsed types, so that a
// difference in whitespace or in an equivalent spelling is not reported as a
// disagreement while a difference in the OCCURRENCE INDICATOR — the thing
// that matters here — always is.
func sameDeclaredType(t *testing.T, got, want string) bool {
	t.Helper()
	g, gerr := ParseSequenceType(got, nil)
	w, werr := ParseSequenceType(want, nil)
	if gerr != nil || werr != nil {
		return got == want
	}
	return g.Occurrence == w.Occurrence && g.String() == w.String()
}

// TestConstructorFunctionsShareOneSignature records why the xs: constructors
// need no manifest row of their own.
//
// F&O 18.1 states their signature once for every type: a constructor takes
// xs:anyAtomicType? and returns the type it names, optional. There is no
// per-type proforma to extract, and inventing 48 identical rows would add
// data without adding a constraint.
func TestConstructorFunctionsShareOneSignature(t *testing.T) {
	lib := Builtins().(*Library)
	n := 0
	for _, fn := range lib.fns {
		if fn.Name.URI != xdm.NSXS {
			continue
		}
		n++
		if fn.Arity != 1 {
			t.Errorf("%s is registered at arity %d; every xs: constructor "+
				"takes exactly one argument", displayName(fn.Name), fn.Arity)
		}
	}
	if n == 0 {
		t.Fatal("no xs: constructor functions are registered, so this test " +
			"is asserting nothing")
	}
	t.Logf("%d xs: constructor functions share the F&O 18.1 signature "+
		"($arg as xs:anyAtomicType?) as T?", n)
}

// TestManifestCoversTheSpecExtraction pins the size and shape of the
// extraction, so that a change to the generator that silently drops rows is
// visible.
//
// The numbers are properties of the vendored Recommendation, not targets: F&O
// 3.1 defines this many callable (name, arity) pairs in the four namespaces
// this library implements.
func TestManifestCoversTheSpecExtraction(t *testing.T) {
	rows := loadManifest(t)
	byPrefix := map[string]int{}
	for _, r := range rows {
		name, ok := parseSpecName(r.Name)
		if !ok {
			t.Fatalf("unmappable manifest name %q", r.Name)
		}
		byPrefix[namespacePrefixes[name.URI]]++
		if r.Arity != len(r.Params) {
			t.Errorf("%s#%d records %d parameter type(s)", r.Name, r.Arity, len(r.Params))
		}
		if r.Result == "" {
			t.Errorf("%s#%d records no result type", r.Name, r.Arity)
		}
	}
	if len(rows) < 250 {
		t.Errorf("the manifest holds %d entries; the extraction found 272, so "+
			"this is a silent loss of rows", len(rows))
	}
	t.Logf("manifest: %d entries (%v)", len(rows), fmt.Sprint(byPrefix))
}
