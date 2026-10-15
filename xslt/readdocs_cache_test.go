package xslt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// fn:doc answers a repeated call from the transform's own cache, keyed on the
// call's arguments and package, rather than resolving the path again. The
// first call still goes through the FileResolver's confinement check: here
// the roots are withdrawn after it, so a second resolution would be refused,
// and only the cached answer can succeed.
func TestDocCacheAnswersRepeatedCalls(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.xml"), []byte(`<a/>`), 0o644); err != nil {
		t.Fatal(err)
	}
	fr, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := &readDocResolver{inner: fr, read: map[string]bool{}, docs: map[docKey]*xdm.Tree{}}
	base := fileURIOf(filepath.Join(dir, "s.xsl"))
	ctx := xpath.NewContext(nil, nil)
	first, err := r.ResolveDocumentIn(ctx, "a.xml", base)
	if err != nil {
		t.Fatal(err)
	}
	fr.Roots = nil
	again, err := r.ResolveDocumentIn(ctx, "a.xml", base)
	if err != nil {
		t.Fatalf("repeated call resolved again: %v", err)
	}
	if again != first {
		t.Fatal("repeated call returned a different tree")
	}
	// Another base naming the same file is another call: it is resolved,
	// and refused.
	if _, err := r.ResolveDocumentIn(ctx, "a.xml", fileURIOf(filepath.Join(dir, "other.xsl"))); err == nil {
		t.Fatal("a call with different arguments was answered from the cache")
	}
}
