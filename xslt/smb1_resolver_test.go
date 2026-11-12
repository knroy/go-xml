package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSMB1BlankResolverRoot checks an empty or blank root never means the
// working directory: NewFileResolver refuses it, and a Roots set by hand
// that holds only blank roots refuses every path instead of reading from
// the working directory or falling back to unconfined.
func TestSMB1BlankResolverRoot(t *testing.T) {
	for _, root := range []string{"", "  ", "\t"} {
		if _, err := NewFileResolver(root); err == nil {
			t.Errorf("NewFileResolver(%q) accepted a blank root", root)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d.xml"), []byte("<d/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, root := range []string{"", " "} {
		r := &FileResolver{Roots: []string{root}}
		if _, err := r.ResolveDocument("d.xml", ""); err == nil {
			t.Errorf("Roots{%q} read a file from the working directory", root)
		}
	}
	// A named root still works beside a blank one.
	r := &FileResolver{Roots: []string{"", dir}}
	if _, err := r.ResolveDocument(filepath.Join(dir, "d.xml"), ""); err != nil {
		t.Errorf("a file inside a named root was refused: %v", err)
	}
}
