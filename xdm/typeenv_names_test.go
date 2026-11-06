package xdm

import (
	"reflect"
	"testing"
)

// The tables hold names split at the namespace, so every lookup rebuilds the
// name it returns. These are the spellings that split differently -- bare,
// qualified, empty braces, an unclosed brace -- and each must come back byte
// for byte and miss exactly where it missed before, including after a Merge.
func TestTypeEnvironmentNamesRoundTrip(t *testing.T) {
	names := []string{"string", "{urn:a}T", "{}T", "{urn:a", "{urn:b}T"}
	src := NewTypeEnvironment()
	for i, n := range names {
		next := names[(i+1)%len(names)]
		src.RegisterDerived(n, next)
		src.RegisterList(n, next)
		src.RegisterUnion(n, []string{next, "{urn:a}U"})
	}
	merged := NewTypeEnvironment()
	merged.Merge(src)
	for _, e := range []*TypeEnvironment{src, merged} {
		for i, n := range names {
			next := names[(i+1)%len(names)]
			if got := e.DerivedBase(n); got != next {
				t.Errorf("DerivedBase(%q) = %q, want %q", n, got, next)
			}
			if got := e.ListItemOf(n); got != next {
				t.Errorf("ListItemOf(%q) = %q, want %q", n, got, next)
			}
			if got, want := e.UnionMembersOf(n), []string{next, "{urn:a}U"}; !reflect.DeepEqual(got, want) {
				t.Errorf("UnionMembersOf(%q) = %q, want %q", n, got, want)
			}
		}
		for _, miss := range []string{"T", "{urn:a}", "urn:a}T", "{urn:a}t", "{urn:c}T"} {
			if e.DerivedBase(miss) != "" || e.ListItemOf(miss) != "" || e.UnionMembersOf(miss) != nil {
				t.Errorf("%q hit, want a miss", miss)
			}
		}
	}
}
