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

// A restriction of a union inherits the base's member list, and the xsd
// package registers that list again for every restriction. N restrictions of
// a K-member union kept N*K stored names in the never-evicted global table;
// identical lists now share one stored slice, through Merge as well.
func TestRegisterUnionSharesIdenticalMemberLists(t *testing.T) {
	const k, n = 50, 20
	members := make([]string, k)
	for i := range members {
		members[i] = "{urn:a}m" + string(rune('A'+i%26)) + string(rune('a'+i/26))
	}
	src := NewTypeEnvironment()
	for j := 0; j < n; j++ {
		src.RegisterUnion("{urn:a}r"+string(rune('A'+j)), members)
	}
	merged := NewTypeEnvironment()
	merged.Merge(src)
	for _, e := range []*TypeEnvironment{src, merged} {
		backing := map[*typeName]bool{}
		for _, m := range e.union {
			backing[&m[0]] = true
		}
		if len(e.union) != n || len(backing) != 1 {
			t.Errorf("%d unions over %d stored member lists, want %d over 1",
				len(e.union), len(backing), n)
		}
		if got := e.UnionMembersOf("{urn:a}rC"); !reflect.DeepEqual(got, members) {
			t.Errorf("UnionMembersOf = %q, want %q", got, members)
		}
	}
}
