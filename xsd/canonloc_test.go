package xsd

import (
	"os"
	"path/filepath"
	"testing"
)

// canonCache gives the answers canonicalLocation gives -- a path spelt in
// another case names the file as the directory spells it, where the
// filesystem says the two are one file -- and a load asks it the same
// question many times, so a repeat is answered from the cache: no stat, no
// directory listing, no allocation.
func TestCanonCacheKeepsIdentityAndCaches(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "schZ012_b.xsd")
	if err := os.WriteFile(file, []byte("<x/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "Schz012_b.xsd")
	want := other // case-sensitive filesystem: another (missing) file
	if _, err := os.Stat(other); err == nil {
		want = file // case-insensitive: the same file, as listed
	}

	var c canonCache
	for _, tc := range []struct{ in, want string }{
		{file, file},
		{other, want},
		{"http://example.com/a.xsd", "http://example.com/a.xsd"},
		{filepath.Join(dir, "missing.xsd"), filepath.Join(dir, "missing.xsd")},
	} {
		if got := c.location(tc.in); got != tc.want {
			t.Errorf("location(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := canonicalLocation(tc.in); got != tc.want {
			t.Errorf("canonicalLocation(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if n := testing.AllocsPerRun(50, func() { c.location(other) }); n != 0 {
		t.Errorf("a repeated lookup allocated %v times; the answer is not cached", n)
	}
}
