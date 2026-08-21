package xslt

// Benchmarks over the two production workloads the engine is actually used
// for, rather than microbenchmarks. They skip when testdata/ is absent, which
// is the normal state of a fresh clone — the corpora are third-party material
// and are not redistributed here.
//
// Wall-clock figures vary about 15% run to run on a laptop; the allocation
// counts are stable and are the more useful number to watch for regressions.

import (
	"context"
	"os"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func benchSheet(b *testing.B, sheetPath, docPath string) (*Stylesheet, *xdm.Node) {
	b.Helper()
	ss, err := os.ReadFile(sheetPath)
	if err != nil {
		b.Skip(err)
	}
	ds, err := os.ReadFile(docPath)
	if err != nil {
		b.Skip(err)
	}
	st, err := xdm.ParseString(string(ss), xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	s, err := Compile(st.Root, CompileOptions{})
	if err != nil {
		b.Fatal(err)
	}
	dt, err := xdm.ParseString(string(ds), xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	return s, dt.Root
}

func BenchmarkUBLRender(b *testing.B) {
	s, doc := benchSheet(b, "../testdata/ubl-invoice.xslt", "../testdata/base-example.xml")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Transform(context.Background(), doc, TransformOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOmanValidate(b *testing.B) {
	s, doc := benchSheet(b, "../testdata/oman/pint-om-rules.xslt", "../testdata/oman/CommercialInvoice.xml")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Transform(context.Background(), doc, TransformOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileUBL(b *testing.B) {
	ss, err := os.ReadFile("../testdata/ubl-invoice.xslt")
	if err != nil {
		b.Skip(err)
	}
	st, err := xdm.ParseString(string(ss), xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(st.Root, CompileOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseInvoice(b *testing.B) {
	ds, err := os.ReadFile("../testdata/base-example.xml")
	if err != nil {
		b.Skip(err)
	}
	src := string(ds)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := xdm.ParseString(src, xdm.ParseOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
