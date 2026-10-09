package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// The transform, validate and xquery commands parse their inputs from the
// file as it is read (or from a bytes.Reader over it) rather than from a
// string copy. A missing file must still report what os.ReadFile reported,
// and the document size limit must still refuse what it refused.

func readFileErr(t *testing.T, path string) string {
	t.Helper()
	_, err := os.ReadFile(path)
	if err == nil {
		t.Fatalf("%s exists", path)
	}
	return err.Error()
}

func TestCLIInputMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "sub dir", "missing.xml")
	want := readFileErr(t, missing)

	sheetPath := filepath.Join(dir, "s.xsl")
	writeSchema(t, sheetPath, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"/>`)
	q := filepath.Join(dir, "q.xq")
	writeSchema(t, q, `.`)

	if _, err := compileStylesheet(missing, nil, nil, "", xslt.Compatibility{}); err == nil || err.Error() != want {
		t.Errorf("stylesheet: %v, want %q", err, want)
	}
	sheet, err := compileStylesheet(sheetPath, nil, nil, "", xslt.Compatibility{})
	if err != nil {
		t.Fatal(err)
	}
	if err := transformOne(sheet, missing, filepath.Join(dir, "out.xml"), transformCfg{}); err == nil || err.Error() != want {
		t.Errorf("transform: %v, want %q", err, want)
	}
	if err := validateOne(missing, xdm.ParseOptions{}, func(*xdm.Node) error { return nil }, true); err == nil || err.Error() != want {
		t.Errorf("validate: %v, want %q", err, want)
	}
	if err := runXQuery([]string{"-q", q, "-o", filepath.Join(dir, "out.xml"), missing}); err == nil || err.Error() != want {
		t.Errorf("xquery: %v, want %q", err, want)
	}
}

// One byte over xdm.DefaultMaxBytes, the limit the commands parse under.
func TestCLIInputSizeLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 64 MB file")
	}
	dir := t.TempDir()
	big := filepath.Join(dir, "big.xml")
	src := append([]byte("<a>"), bytes.Repeat([]byte("x"), int(xdm.DefaultMaxBytes))...)
	if err := os.WriteFile(big, src, 0o600); err != nil {
		t.Fatal(err)
	}
	_, perr := xdm.Parse(bytes.NewReader(src), xdm.ParseOptions{})
	if perr == nil || !strings.Contains(perr.Error(), "exceeds") {
		t.Fatalf("the library accepted an oversized document: %v", perr)
	}
	want := perr.Error()

	sheetPath := filepath.Join(dir, "s.xsl")
	writeSchema(t, sheetPath, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"/>`)
	q := filepath.Join(dir, "q.xq")
	writeSchema(t, q, `.`)
	sheet, err := compileStylesheet(sheetPath, nil, nil, "", xslt.Compatibility{})
	if err != nil {
		t.Fatal(err)
	}
	if err := transformOne(sheet, big, filepath.Join(dir, "out.xml"), transformCfg{}); err == nil || err.Error() != want {
		t.Errorf("transform: %v, want %q", err, want)
	}
	if err := validateOne(big, xdm.ParseOptions{}, func(*xdm.Node) error { return nil }, true); err == nil || err.Error() != "parsing: "+want {
		t.Errorf("validate: %v, want %q", err, "parsing: "+want)
	}
	if err := runXQuery([]string{"-q", q, "-o", filepath.Join(dir, "out.xml"), big}); err == nil || err.Error() != big+": "+want {
		t.Errorf("xquery: %v, want %q", err, big+": "+want)
	}
}
