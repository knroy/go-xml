package xsd

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// retentionResolver grants nothing. Load refuses a nil Resolver by design, and
// these schemas name no location, so a stub that answers "no schema document"
// is both sufficient and the narrowest grant available.
type retentionResolver struct{}

func (retentionResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

// TestSchemaTypeRegistryRetainsPerType pins what loading a schema costs the
// process permanently.
//
// The registries in xdm (derivedPrimitives, unionMembers, listItems) are
// process-global with no eviction, so a type registered by a schema outlives
// that schema: every *Schema here is unreachable and collected, and the map
// entries are not. That is deliberate -- xdm cannot import xsd, so a node
// annotated with a user-defined type needs somewhere package-independent to
// look up what the type erases to -- and the SEMANTIC hazard of the sharing
// (two schemas defining {uri}T differently) is already handled elsewhere, by
// the node-local Node.DerivedPrimitive / UnionMember / ListItem fields; see
// the commentary at xdm/node.go around those fields, and
// annotation_isolation_test.go.
//
// What is NOT handled, and what this test bounds, is the retention. The cost
// is per DISTINCT TYPE, not per load: reloading the same schema a million
// times rewrites the same key. So the invariant worth pinning is that a
// distinct type costs a small, roughly constant amount -- two short strings and
// a map slot -- and never a whole tree.
//
// The ceiling is 2 KB per type against a measured ~100 bytes. That is twenty
// times the real figure on purpose: heap accounting after GC is noisy, the test
// runs alongside whatever else the package left live, and a ceiling that trips
// on noise is a ceiling that gets deleted. It is still tight enough to catch
// the regression that matters -- someone retaining the *Schema, a component, or
// a subtree from the key rather than two strings, which would move the figure
// by orders of magnitude, not percent.
//
// On global state: this test writes 2000 entries the rest of the process can
// read, and never removes them. It is safe for the other tests in the package
// because the registries are keyed by expanded QName ({uri}local) and every
// namespace here is unique to this test's own prefix, so no key any other test
// reads can collide. It does leave the maps larger for the remainder of the
// run, which costs a fraction of a megabyte and nothing else.
func TestSchemaTypeRegistryRetainsPerType(t *testing.T) {
	const n = 2000

	schemaFor := func(i int) string {
		return fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		    xmlns:p="urn:retention:%d" targetNamespace="urn:retention:%d">
		  <xs:simpleType name="T%d">
		    <xs:restriction base="xs:decimal"/>
		  </xs:simpleType>
		  <xs:element name="e" type="p:T%d"/>
		</xs:schema>`, i, i, i, i)
	}

	// Load one first, outside the measurement, so that any one-off
	// allocation the first schema in the process triggers -- built-in type
	// tables, regexp compilation, sync pools -- is not billed to the 2000.
	load := func(i int) {
		tree, err := xdm.Parse(strings.NewReader(schemaFor(i)), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse schema %d: %v", i, err)
		}
		if _, err := Load(tree.Root, "", Options{Resolver: retentionResolver{}}); err != nil {
			t.Fatalf("load schema %d: %v", i, err)
		}
	}
	load(-1)

	settle := func() runtime.MemStats {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms
	}

	before := settle()
	for i := 0; i < n; i++ {
		load(i)
	}
	after := settle()

	// HeapAlloc can legitimately fall (the warm-up left garbage), so guard
	// the subtraction rather than relying on unsigned wraparound.
	var growth uint64
	if after.HeapAlloc > before.HeapAlloc {
		growth = after.HeapAlloc - before.HeapAlloc
	}

	const maxPerType = 2 << 10 // 2 KB; measured ~100 B. See the comment above.
	if perType := growth / n; perType > maxPerType {
		t.Errorf("retention per distinct schema type is %d bytes (%d bytes over %d types), ceiling %d bytes\n"+
			"a schema type registration should retain two short strings and a map slot, not a tree",
			perType, growth, n, maxPerType)
	}

	// Reloading the same types must not add retention: the registries are
	// keyed by expanded QName, so the cost is per distinct type and not per
	// load. If this ever fails, something started accumulating per call --
	// a slice appended to, a list of schemas -- and the bound above would
	// no longer describe the real cost of a long-running process.
	reloadBefore := settle()
	for i := 0; i < n; i++ {
		load(i)
	}
	reloadAfter := settle()

	var reloadGrowth uint64
	if reloadAfter.HeapAlloc > reloadBefore.HeapAlloc {
		reloadGrowth = reloadAfter.HeapAlloc - reloadBefore.HeapAlloc
	}
	if perType := reloadGrowth / n; perType > maxPerType {
		t.Errorf("reloading the same %d types retained a further %d bytes (%d per type, ceiling %d)\n"+
			"registration is keyed by expanded QName and must be idempotent",
			n, reloadGrowth, perType, maxPerType)
	}
}
