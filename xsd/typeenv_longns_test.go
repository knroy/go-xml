package xsd

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The process-global type tables are never evicted, so what one load leaves
// there must not scale with namespace length times type count. 10,000 types
// under one 4 KB namespace (a 783 KB schema) used to retain 49 MB, one
// "{namespace}local" string per type; the tables now hold the namespace once
// and retain about 0.64 MB (0.68 MB under -race). The 4 MB ceiling is six
// times that and a twelfth of the old figure.
func TestManyTypesUnderLongNamespaceRetention(t *testing.T) {
	const n = 10000
	ns := "urn:longns:" + strings.Repeat("x", 4096)
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="` + ns + `">`)
	for i := 0; i < n; i++ {
		b.WriteString(`<xs:simpleType name="t` + strconv.Itoa(i) + `"><xs:restriction base="xs:string"/></xs:simpleType>`)
	}
	b.WriteString(`</xs:schema>`)
	settle := func() uint64 {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	before := settle()
	func() {
		doc, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Load(doc.Root, "", Options{Resolver: retentionResolver{}}); err != nil {
			t.Fatal(err)
		}
	}()
	after := settle()
	var growth uint64
	if after > before {
		growth = after - before
	}
	t.Logf("retained %d bytes", growth)
	const ceiling = 4 << 20
	if growth > ceiling {
		t.Errorf("%d types under a %d-byte namespace retained %d bytes after the schema was dropped, ceiling %d\n"+
			"the global type tables must keep the namespace once, not once per type", n, len(ns), growth, ceiling)
	}
	if got := xdm.DerivedBase(xdm.AnnotationName(ns, "t42")); got != "string" {
		t.Errorf("DerivedBase({ns}t42) = %q, want \"string\"", got)
	}
}
