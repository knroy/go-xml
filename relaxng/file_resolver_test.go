package relaxng

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func TestFileResolverConfinesSchemesPathsAndBytes(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.rng")
	if err := os.WriteFile(inside, []byte(`<element name="r"><empty/></element>`), 0600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Root: root}
	if _, err := r.ResolveSchema(inside); err != nil {
		t.Fatalf("inside schema refused: %v", err)
	}
	for _, href := range []string{"https://example.invalid/a.rng", filepath.Join(root, "..", "outside.rng")} {
		if _, err := r.ResolveSchema(href); err == nil {
			t.Errorf("ResolveSchema(%q) succeeded", href)
		}
	}
	r.MaxBytes = 4
	_, err := r.ResolveSchema(inside)
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("limited ResolveSchema error = %v, want ErrResourceLimit", err)
	}
}

func TestFileResolverRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.rng")
	if err := os.WriteFile(outside, []byte(`<empty/>`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.rng")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := (&FileResolver{Root: root}).ResolveSchema(link)
	if err == nil || strings.Contains(err.Error(), "<nil>") {
		t.Errorf("symlink escape error = %v, want refusal", err)
	}
}
