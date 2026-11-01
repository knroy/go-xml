package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// A document read through fn:doc is parsed as it is read. It used to be read
// whole by io.ReadAll (about two copies while it grew) and then converted with
// string(data), so a 62 MB file allocated 121 MB + 59 MB beyond its tree.
// Allocated bytes are the measure, not wall time: the document is mostly a
// single text run, whose tree costs about its own size, so the whole call must
// stay below 3x the file.
func TestResolveDocumentDoesNotBufferTheFile(t *testing.T) {
	const size = 16 << 20
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("<r>")
	for sb.Len() < size {
		fmt.Fprintf(&sb, "<a>%s</a>", strings.Repeat("x", 100))
	}
	sb.WriteString("</r>")
	path := filepath.Join(dir, "big.xml")
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	fileSize := int64(sb.Len())
	sb.Reset()
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	var m0, m1 goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&m0)
	tree, err := r.ResolveDocument(path, "")
	if err != nil {
		t.Fatal(err)
	}
	goruntime.ReadMemStats(&m1)
	goruntime.KeepAlive(tree)
	t.Logf("file %d, allocated %d", fileSize, m1.TotalAlloc-m0.TotalAlloc)
	if got := int64(m1.TotalAlloc - m0.TotalAlloc); got > 3*fileSize {
		t.Errorf("ResolveDocument allocated %d bytes for a %d byte file, want <= %d",
			got, fileSize, 3*fileSize)
	}
}
