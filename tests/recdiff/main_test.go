package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/internal/record"
)

// TestCompareRules pins what compare counts as unexplained: a byte difference
// no rule deletes, and a case recorded on one side only.
func TestCompareRules(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	put := func(root, key, data string) {
		if err := record.Store(root, "s", key, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	put(a, "same", "x")
	put(b, "same", "x")
	put(a, "set/genid", `<p id="N1x2"/>`)
	put(b, "set/genid", `<p id="N7x2"/>`)
	put(a, "real", "1")
	put(b, "real", "2")
	put(a, "gone", "x")
	rules := filepath.Join(dir, "allow.txt")
	os.WriteFile(rules, []byte("# c\ns * N\\d+x\\d+\n"), 0o644)

	if bad, err := compare(a, b, rules, 2); err != nil || bad != 2 {
		t.Fatalf("compare = %d, %v; want 2 unexplained (real, gone)", bad, err)
	}
	os.WriteFile(rules, []byte("s * N\\d+x\\d+\ns real .\ns gone (?s).*\n"), 0o644)
	if bad, err := compare(a, b, rules, 2); err != nil || bad != 0 {
		t.Fatalf("compare = %d, %v; want 0 once every difference has a rule", bad, err)
	}
}
