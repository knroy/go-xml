package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// FileResolver doubles as the entity resolver for the documents it parses.
// These pin that doing so does not widen what it permits: the confinement,
// the scheme rejection and the symlink handling are the same code, and an
// external entity must not be a way around any of them.

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	// The temp dir itself is symlinked on macOS (/var -> /private/var), and
	// the containment check resolves symlinks — so the root handed to the
	// resolver is resolved too, or every file under it is refused as outside
	// a directory it is plainly inside.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Off by default: a resolver with AllowDOCTYPE set but ExternalEntities unset
// must not read an external entity. This is the property that keeps every
// existing caller of FileResolver unchanged.
func TestFileResolverExternalEntitiesOffByDefault(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"secret.xml": `<secret>leaked</secret>`,
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "secret.xml"> ]>
<r>&e;</r>`,
	})
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.AllowDOCTYPE = true
	tree, err := r.ResolveDocument("doc.xml", fileURIOf(filepath.Join(dir, "doc.xml")))
	if err == nil && strings.Contains(tree.Root.StringValue(), "leaked") {
		t.Fatal("AllowDOCTYPE alone read an external entity through FileResolver")
	}
}

func TestFileResolverReadsEntityWhenEnabled(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"frag.xml": `<frag>text</frag>`,
		"doc.xml": `<!DOCTYPE r [ <!ENTITY e SYSTEM "frag.xml"> ]>
<r>&e;</r>`,
	})
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.AllowDOCTYPE = true
	r.ExternalEntities = true
	tree, err := r.ResolveDocument("doc.xml", fileURIOf(filepath.Join(dir, "doc.xml")))
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Root.StringValue(); !strings.Contains(got, "text") {
		t.Fatalf("entity not expanded: %q", got)
	}
}

// >>> The containment check applies to entities. <<<
//
// The document lives inside the root; the entity it names does not. Reading it
// would be a file-disclosure primitive reachable from any document the
// resolver parses, which is the whole thing this test exists to prevent.
func TestFileResolverRefusesEntityOutsideRoots(t *testing.T) {
	outer := writeTree(t, map[string]string{
		"secret.txt":   `leaked`,
		"docs/doc.xml": "<!DOCTYPE r [ <!ENTITY e SYSTEM \"../secret.txt\"> ]>\n<r>&e;</r>",
	})
	inner := filepath.Join(outer, "docs")
	r, err := NewFileResolver(inner)
	if err != nil {
		t.Fatal(err)
	}
	r.AllowDOCTYPE = true
	r.ExternalEntities = true
	tree, err := r.ResolveDocument("doc.xml", fileURIOf(filepath.Join(inner, "doc.xml")))
	if err == nil {
		if strings.Contains(tree.Root.StringValue(), "leaked") {
			t.Fatal("an entity outside the roots was read")
		}
		t.Fatal("an entity outside the roots did not produce an error")
	}
	if !strings.Contains(err.Error(), "outside the permitted directories") {
		t.Fatalf("refused, but not by the containment check: %v", err)
	}
}

// A non-file scheme is rejected before the filesystem is touched, so an
// external entity cannot be turned into an SSRF primitive.
func TestFileResolverRefusesNonFileSchemeEntity(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"doc.xml": "<!DOCTYPE r [ <!ENTITY e SYSTEM \"http://example.invalid/x\"> ]>\n<r>&e;</r>",
	})
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.AllowDOCTYPE = true
	r.ExternalEntities = true
	_, err = r.ResolveDocument("doc.xml", fileURIOf(filepath.Join(dir, "doc.xml")))
	if err == nil {
		t.Fatal("an http:// entity was accepted")
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("refused, but not by the scheme check: %v", err)
	}
}

// A symlink inside the root pointing outside it is refused, because
// resolvePath resolves symlinks BEFORE the containment check. Without that
// ordering, confinement is decorative.
func TestFileResolverRefusesSymlinkedEntity(t *testing.T) {
	outer := writeTree(t, map[string]string{
		"secret.txt":   `leaked`,
		"docs/doc.xml": "<!DOCTYPE r [ <!ENTITY e SYSTEM \"link.txt\"> ]>\n<r>&e;</r>",
	})
	inner := filepath.Join(outer, "docs")
	if err := os.Symlink(filepath.Join(outer, "secret.txt"),
		filepath.Join(inner, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r, err := NewFileResolver(inner)
	if err != nil {
		t.Fatal(err)
	}
	r.AllowDOCTYPE = true
	r.ExternalEntities = true
	tree, err := r.ResolveDocument("doc.xml", fileURIOf(filepath.Join(inner, "doc.xml")))
	if err == nil && strings.Contains(tree.Root.StringValue(), "leaked") {
		t.Fatal("a symlink escaped the root through an external entity")
	}
}

// The resolver satisfies the interface xdm asks for. A compile-time assertion,
// because the wiring is otherwise only exercised through a parse.
var _ xdm.EntityResolver = (*FileResolver)(nil)
