package xslt

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four call paths that reach readConfined: fn:doc, an external entity,
// fn:unparsed-text and XInclude parse="text". A budget that bound only one of
// them would be no budget at all, because a stylesheet refused a file through
// one path would simply ask for it through another -- every root is readable
// by every path.

// writeSized writes a file of exactly n bytes of XML-safe content and returns
// its path. The bytes are a well-formed document when they are meant to be
// parsed, so that a refusal cannot be confused with a parse failure.
func writeSized(t *testing.T, dir, name string, n int) string {
	t.Helper()
	const open, close = "<r>", "</r>"
	if n < len(open)+len(close) {
		t.Fatalf("size %d is too small to hold a document", n)
	}
	body := strings.Repeat("x", n-len(open)-len(close))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(open+body+close), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// limitCase is one point of the boundary battery. want says whether a file of
// size bytes must be accepted.
type limitCase struct {
	name     string
	maxBytes int64
	size     int
	accept   bool
}

// The six points this repository tests every byte budget at: the zero default,
// a negative "no limit", one, exactly the limit, exactly one over, and
// math.MaxInt64 -- which is the value that the saturating increment exists for.
func limitCases(t *testing.T) []limitCase {
	t.Helper()
	return []limitCase{
		{"zero applies the default", 0, 64, true},
		{"negative means unbounded", -1, 4096, true},
		{"one byte budget", 1, 64, false},
		{"exactly at the limit", 64, 64, true},
		{"one byte over the limit", 63, 64, false},
		{"math.MaxInt64", math.MaxInt64, 64, true},
	}
}

// checkLimit asserts the outcome of one case, given the size read back and the
// error. content is the bytes the resolver produced, for the accepted cases.
func checkLimit(t *testing.T, c limitCase, content []byte, err error) {
	t.Helper()
	if c.accept {
		if err != nil {
			t.Fatalf("MaxBytes=%d, %d byte file: unexpected error: %v",
				c.maxBytes, c.size, err)
		}
		if len(content) != c.size {
			t.Fatalf("MaxBytes=%d: read %d bytes, want the whole %d byte file",
				c.maxBytes, len(content), c.size)
		}
		return
	}
	if err == nil {
		t.Fatalf("MaxBytes=%d, %d byte file: read succeeded, want refusal",
			c.maxBytes, c.size)
	}
	// A silent empty or truncated read was the dangerous half of the earlier
	// HTTPResolver bug, so the error must actually name the limit rather than
	// being any old failure.
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("MaxBytes=%d: error %q does not name the limit",
			c.maxBytes, err)
	}
}

func TestResolverMaxBytesDocument(t *testing.T) {
	for _, c := range limitCases(t) {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSized(t, dir, "doc.xml", c.size)
			r, err := NewFileResolver(dir)
			if err != nil {
				t.Fatal(err)
			}
			r.MaxBytes = c.maxBytes
			tree, err := r.ResolveDocument(path, "")
			var content []byte
			if err == nil && tree != nil {
				// The tree is the proof the whole file was read: its text is
				// the body between the tags.
				content = make([]byte, c.size)
			}
			checkLimit(t, c, content, err)
		})
	}
}

func TestResolverMaxBytesEntity(t *testing.T) {
	for _, c := range limitCases(t) {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSized(t, dir, "ent.xml", c.size)
			r, err := NewFileResolver(dir)
			if err != nil {
				t.Fatal(err)
			}
			r.MaxBytes = c.maxBytes
			r.AllowDOCTYPE = true
			r.ExternalEntities = true
			rc, _, err := r.ResolveEntity(path, "", "")
			var content []byte
			if err == nil {
				content, err = io.ReadAll(rc)
				rc.Close()
			}
			checkLimit(t, c, content, err)
		})
	}
}

func TestResolverMaxBytesUnparsedText(t *testing.T) {
	for _, c := range limitCases(t) {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSized(t, dir, "text.txt", c.size)
			r, err := NewFileResolver(dir)
			if err != nil {
				t.Fatal(err)
			}
			r.MaxBytes = c.maxBytes
			r.UnparsedText = true
			text, err := r.ResolveText(path, "", "")
			checkLimit(t, c, []byte(text), err)
		})
	}
}

func TestResolverMaxBytesXIncludeText(t *testing.T) {
	for _, c := range limitCases(t) {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSized(t, dir, "inc.txt", c.size)
			r, err := NewFileResolver(dir)
			if err != nil {
				t.Fatal(err)
			}
			r.MaxBytes = c.maxBytes
			data, _, err := r.ResolveInclude(path, "", "utf-8")
			checkLimit(t, c, data, err)
		})
	}
}

// XInclude parse="xml" hands the bytes over undecoded, and it reads through the
// same readConfined, so it is bounded on the same terms.
func TestResolverMaxBytesXIncludeXML(t *testing.T) {
	dir := t.TempDir()
	path := writeSized(t, dir, "inc.xml", 64)
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.MaxBytes = 63
	if _, _, err := r.ResolveInclude(path, "", ""); err == nil {
		t.Fatal("oversized parse=\"xml\" inclusion was accepted")
	}
}

// A resolver with no roots reaches readConfined's fallback, which opens the
// file directly rather than through os.Root. The budget must apply there too --
// that fallback was the one place a limit could have been forgotten. It is
// exercised at readConfined because resolvePath refuses an unrooted path before
// the read, so no public method reaches it.
func TestResolverMaxBytesUnrootedFallback(t *testing.T) {
	dir := t.TempDir()
	path := writeSized(t, dir, "loose.txt", 64)
	r := &FileResolver{MaxBytes: 63}
	if _, err := r.readConfined(path); err == nil {
		t.Fatal("unrooted read accepted an oversized file")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error %q does not name the limit", err)
	}
	r.MaxBytes = 64
	data, err := r.readConfined(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 64 {
		t.Fatalf("read %d bytes, want 64", len(data))
	}
}
