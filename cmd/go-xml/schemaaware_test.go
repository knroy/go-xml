package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/fileuri"
)

// Schema-aware processing from the command line. The CLI gave
// xsl:import-schema and "import schema ... at" no resolver, so a stylesheet or
// query importing a schema failed to compile, and it had no way to validate
// the source -- so no route through the command gave a typed value. Every
// path is built under t.TempDir with filepath, so the cases run unchanged on
// Windows, where the schema-location resolves against a file:///C:/... base.

const priceSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="order"><xs:complexType><xs:sequence>
    <xs:element name="price" type="xs:decimal"/>
    <xs:element name="qty" type="xs:integer"/>
  </xs:sequence></xs:complexType></xs:element>
</xs:schema>`

// priceSheet reports whether the source atomises to schema types.
func priceSheet(location string) string {
	return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
    xmlns:xs="http://www.w3.org/2001/XMLSchema" version="3.0">
  <xsl:import-schema schema-location="` + location + `"/>
  <xsl:output method="text"/>
  <xsl:template match="/">
    <xsl:value-of select="data(order/price) instance of xs:decimal,
                          (order/qty * 2) instance of xs:integer"/>
  </xsl:template>
</xsl:stylesheet>`
}

func TestTransformValidateTypesTheSource(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "order.xsd"), priceSchema)
	xsl := filepath.Join(dir, "sheet.xsl")
	writeFile(t, xsl, priceSheet("order.xsd"))
	good := filepath.Join(dir, "good.xml")
	writeFile(t, good, `<order><price>10.50</price><qty>3</qty></order>`)
	bad := filepath.Join(dir, "bad.xml")
	writeFile(t, bad, `<order><price>abc</price><qty>3</qty></order>`)

	for _, c := range []struct {
		args []string
		want string
	}{
		// Importing a schema does not validate the input: it stays untyped.
		{nil, "false false"},
		{[]string{"-validate", "strict"}, "true true"},
		{[]string{"-validate", "lax"}, "true true"},
	} {
		args := append([]string{"-xsl", xsl}, c.args...)
		out, err := exec.Command(bin, append(args, good)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", c.args, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != c.want {
			t.Errorf("%v: typed = %q, want %q", c.args, got, c.want)
		}
	}

	out, err := exec.Command(bin, "-xsl", xsl, "-validate", "strict", bad).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "is not a valid xs:decimal") {
		t.Errorf("an invalid source under -validate strict: %v\n%s", err, out)
	}
}

// The schema is read through the stylesheet's resolver, so it is confined to
// the stylesheet's directory and -allow-dir like every other read.
func TestImportSchemaFollowsAllowDir(t *testing.T) {
	bin := buildCLI(t)
	dir, other := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(other, "order.xsd"), priceSchema)
	xsl := filepath.Join(dir, "sheet.xsl")
	writeFile(t, xsl, priceSheet(fileuri.Of(filepath.Join(other, "order.xsd"))))
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<order><price>1</price><qty>1</qty></order>`)

	out, err := exec.Command(bin, "-xsl", xsl, in).CombinedOutput()
	if err == nil {
		t.Fatalf("a schema outside the roots was read:\n%s", out)
	}
	out, err = exec.Command(bin, "-xsl", xsl, "-allow-dir", other,
		"-validate", "strict", in).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "true true" {
		t.Errorf("schema under -allow-dir: %v\n%s", err, out)
	}
}

func TestXQueryImportSchemaAndValidate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "order.xsd"), priceSchema)
	q := filepath.Join(dir, "q.xq")
	writeFile(t, q, `import schema default element namespace "" at "order.xsd";
let $v := validate strict { . }
return (data($v/order/price) instance of xs:decimal, data(order/price) instance of xs:decimal)`)
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<order><price>10.50</price><qty>3</qty></order>`)

	got, err := runQuery(t, dir, "-q", q, in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "true false") {
		t.Errorf("validated price typed, input untyped: got %q, want ... true false", got)
	}
}
