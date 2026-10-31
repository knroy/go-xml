package xslt

// Benchmarks over the two production workloads the engine is actually used
// for, rather than microbenchmarks: XRechnung UBL -> xr:invoice and the
// Peppol CEN EN 16931 Schematron rules (compiled to XSLT by SchXslt). The
// inputs are the docs/benchmark.md workloads under testdata/bench, which
// bench/fetch-inputs.sh fetches; the benchmarks skip when they are absent,
// which is the normal state of a fresh clone -- the corpora are third-party
// material and are not redistributed here.
//
// Wall-clock figures vary about 15% run to run on a laptop; the allocation
// counts are stable and are the more useful number to watch for regressions.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/internal/fileuri"
	"github.com/knroy/go-xml/v2/xdm"
)

var (
	benchCEN     = filepath.Join("..", "testdata", "bench", "peppol", "xsl", "CEN-EN16931-UBL.xsl")
	benchXR      = filepath.Join("..", "testdata", "bench", "xrechnung", "xsl", "ubl-invoice-xr.xsl")
	benchInvoice = filepath.Join("..", "testdata", "bench", "peppol", "invoices", "base-example.xml")
	benchXRDoc   = filepath.Join("..", "testdata", "bench", "xrechnung", "invoices", "03.07a-INVOICE_ubl.xml")
)

// benchParse parses path with its file URI as base, skipping when it is absent.
func benchParse(b *testing.B, path string) (*xdm.Node, string) {
	b.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		b.Fatal(err)
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		b.Skip(err)
	}
	t, err := xdm.ParseString(string(src), xdm.ParseOptions{BaseURI: fileuri.Of(abs)})
	if err != nil {
		b.Fatal(err)
	}
	return t.Root, abs
}

// benchCompile compiles the stylesheet at path, resolving its modules from
// its own directory.
func benchCompile(b *testing.B, root *xdm.Node, abs string) *Stylesheet {
	b.Helper()
	r, err := NewFileResolver(filepath.Dir(abs))
	if err != nil {
		b.Fatal(err)
	}
	s, err := Compile(root, CompileOptions{Resolver: r, BaseURI: fileuri.Of(abs)})
	if err != nil {
		b.Fatal(err)
	}
	return s
}

func benchTransform(b *testing.B, sheetPath, docPath string) {
	root, abs := benchParse(b, sheetPath)
	s := benchCompile(b, root, abs)
	doc, _ := benchParse(b, docPath)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Transform(context.Background(), doc, TransformOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkXRechnungUBL(b *testing.B) { benchTransform(b, benchXR, benchXRDoc) }

func BenchmarkPeppolCENValidate(b *testing.B) { benchTransform(b, benchCEN, benchInvoice) }

func BenchmarkCompileCEN(b *testing.B) {
	root, abs := benchParse(b, benchCEN)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCompile(b, root, abs)
	}
}

func BenchmarkParseInvoice(b *testing.B) {
	ds, err := os.ReadFile(benchInvoice)
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
