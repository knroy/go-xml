package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// The whitespace-stripped copy of a fn:doc tree is shared by every transform
// of one stylesheet. Concurrent transforms must all see it stripped and stable
// within themselves, and the stylesheet must keep one copy, not one each.
func TestStrippedDocSharedAcrossTransforms(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d.xml"),
		[]byte("<d>\n  <e>x</e>\n  <e>y</e>\n</d>"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:strip-space elements="*"/>
		<xsl:template match="/"><out><xsl:value-of select="count(doc('d.xml')//text()),
			doc('d.xml') is doc('d.xml')"/></out></xsl:template>
	</xsl:stylesheet>`
	fr, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := fileURIOf(filepath.Join(dir, "s.xsl"))
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	in, err := xdm.ParseString("<r/>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run := func(docs any, want string) (string, error) {
		opts := TransformOptions{}
		switch d := docs.(type) {
		case *FileResolver:
			opts.Documents = d
		case freshResolver:
			opts.Documents = d
		}
		res, err := s.Transform(context.Background(), in.Root, opts)
		if err != nil {
			return "", err
		}
		if got := strings.TrimSpace(res.String()); got != want {
			return "", fmt.Errorf("got %q, want %q", got, want)
		}
		return "", nil
	}
	stress(t, 8, 20, func() (string, error) { return run(fr, "<out>2 true</out>") })
	s.strippedMu.Lock()
	n := len(s.stripped)
	s.strippedMu.Unlock()
	if n != 1 {
		t.Fatalf("stylesheet holds %d stripped copies, want 1", n)
	}

	// A resolver that parses afresh for every call leaves nothing behind
	// once its trees are unreachable: the source is held weakly. (Two such
	// calls are two documents, so "is" is false here, as it was before.)
	for i := 0; i < 5; i++ {
		if _, err := run(freshResolver{fr}, "<out>2 false</out>"); err != nil {
			t.Fatal(err)
		}
	}
	fr.cache = nil // drop the trees the shared resolver held
	deadline := time.Now().Add(5 * time.Second)
	for {
		goruntime.GC()
		s.strippedMu.Lock()
		n = len(s.stripped)
		s.strippedMu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d stripped copies outlived their sources", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// freshResolver parses the document again on every call.
type freshResolver struct{ fr *FileResolver }

func (r freshResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	path, err := r.fr.resolvePath(uri, base)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return xdm.ParseString(string(b), xdm.ParseOptions{})
}
