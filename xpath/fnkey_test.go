package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A library is keyed by namespace URI, local name and arity, each of which
// tells two functions apart on its own, and a lookup builds no string to find
// one: every function call goes through Lookup, so a per-call allocation
// there is paid once per call evaluated.
func TestLibraryKeyAllocatesNothing(t *testing.T) {
	const ns = "urn:k"
	lib := NewLibrary(nil)
	for _, f := range []Function{
		{Name: xdm.QName{URI: ns, Local: "f"}, Arity: 1},
		{Name: xdm.QName{URI: ns, Local: "f"}, Arity: 2},
		{Name: xdm.QName{URI: ns + "2", Local: "f"}, Arity: 1},
	} {
		lib.Add(f)
	}
	for _, c := range []struct {
		uri, local string
		arity      int
		want       bool
	}{
		{ns, "f", 1, true},
		{ns, "f", 2, true},
		{ns, "f", 3, false},
		{ns + "2", "f", 1, true},
		{ns + "2", "f", 2, false},
		{"", "f", 1, false},
		{ns, "g", 1, false},
	} {
		name := xdm.QName{URI: c.uri, Local: c.local}
		f, ok := lib.Lookup(name, c.arity)
		if ok != c.want || ok && (f.Name != name || f.Arity != c.arity) {
			t.Errorf("Lookup(%s, %d) = %v, %v; want found=%v",
				name.Clark(), c.arity, f.Name.Clark(), ok, c.want)
		}
		if got := lib.Declares(name, c.arity); got != c.want {
			t.Errorf("Declares(%s, %d) = %v, want %v", name.Clark(), c.arity, got, c.want)
		}
	}

	name := xdm.QName{URI: ns, Local: "f"}
	if n := testing.AllocsPerRun(100, func() { lib.Lookup(name, 2) }); n != 0 {
		t.Errorf("Lookup allocated %v times, want 0", n)
	}
	builtins := Builtins()
	sub := xdm.QName{URI: xdm.NSFN, Local: "substring"}
	if n := testing.AllocsPerRun(100, func() { builtins.Lookup(sub, 3) }); n != 0 {
		t.Errorf("Builtins().Lookup(fn:substring#3) allocated %v times, want 0", n)
	}
}
