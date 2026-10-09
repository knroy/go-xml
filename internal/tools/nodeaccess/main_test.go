package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportsToV2 checks the -v1 import rewrite: v1 paths move to /v2, while a
// path already on v2, a module whose name merely starts the same way, a
// vendor directory and a nested module are left as they were.
func TestImportsToV2(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const v1 = "package a\n\nimport (\n\t\"github.com/knroy/go-xml/xdm\"\n\tx \"github.com/knroy/go-xml/v2/xpath\"\n\t\"github.com/knroy/go-xmlfoo\"\n)\n"
	write("go.mod", "module example.com/a\n")
	write("a.go", v1)
	write("vendor/v.go", v1)
	write("nested/go.mod", "module example.com/n\n")
	write("nested/n.go", v1)
	n, err := importsToV2(root)
	if err != nil || n != 1 {
		t.Fatalf("importsToV2 = %d, %v; want 1 file", n, err)
	}
	want := strings.Replace(v1, "go-xml/xdm", "go-xml/v2/xdm", 1)
	for rel, w := range map[string]string{"a.go": want, "vendor/v.go": v1, "nested/n.go": v1} {
		b, _ := os.ReadFile(filepath.Join(root, rel))
		if string(b) != w {
			t.Errorf("%s:\n%s\nwant:\n%s", rel, b, w)
		}
	}
}
