package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// -validate gives the xquery subcommand what -validate gives the transform:
// the input document is assessed against the schema the query imports, so a
// query that declares a typed context item, or compares typed values, sees
// schema types. Without it a command-line run could never satisfy
// "declare context item as document-node(schema-element(...))". Paths are
// built under t.TempDir so the relative "at" also resolves on Windows.
func TestXQueryValidateInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hat.xsd"),
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:h"
		    xmlns="urn:h" elementFormDefault="qualified">
		  <xs:element name="hat"><xs:complexType><xs:sequence>
		    <xs:element name="size" type="xs:integer"/>
		  </xs:sequence></xs:complexType></xs:element>
		</xs:schema>`)
	q := filepath.Join(dir, "q.xq")
	writeFile(t, q, `import schema namespace h = "urn:h" at "hat.xsd";
declare context item as document-node(schema-element(h:hat)) external;
<r>{ data(/h:hat/h:size) instance of xs:integer, data(/h:hat/h:size) + 1 }</r>`)
	good := filepath.Join(dir, "good.xml")
	writeFile(t, good, `<hat xmlns="urn:h"><size>7</size></hat>`)
	bad := filepath.Join(dir, "bad.xml")
	writeFile(t, bad, `<hat xmlns="urn:h"><size>big</size></hat>`)
	other := filepath.Join(dir, "other.xml")
	writeFile(t, other, `<other/>`)

	// Unvalidated, the input is untyped and cannot match the declared type.
	if _, err := runQuery(t, dir, "-q", q, good); err == nil || !strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("without -validate: err = %v, want XPTY0004", err)
	}
	for _, mode := range []string{"strict", "lax"} {
		got, err := runQuery(t, dir, "-validate", mode, "-q", q, good)
		if err != nil {
			t.Fatalf("-validate %s: %v", mode, err)
		}
		if !strings.Contains(got, "<r>true 8</r>") {
			t.Errorf("-validate %s: output %q, want <r>true 8</r>", mode, got)
		}
	}
	if _, err := runQuery(t, dir, "-validate", "strict", "-q", q, bad); err == nil ||
		!strings.Contains(err.Error(), "cvc-datatype-valid") {
		t.Errorf("invalid input: err = %v, want cvc-datatype-valid", err)
	}
	// strict needs the document element declared; lax lets an undeclared one
	// through untyped, and the declared context item type then rejects it.
	if _, err := runQuery(t, dir, "-validate", "strict", "-q", q, other); err == nil ||
		!strings.Contains(err.Error(), "cvc-elt.1") {
		t.Errorf("strict, undeclared root: err = %v, want cvc-elt.1", err)
	}
	if _, err := runQuery(t, dir, "-validate", "lax", "-q", q, other); err == nil ||
		!strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("lax, undeclared root: err = %v, want XPTY0004", err)
	}
}

// -validate is refused, with the reason, when there is nothing to validate
// against or nothing to validate, and for an unknown mode.
func TestXQueryValidateRefusals(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.xq")
	writeFile(t, plain, `1`)
	in := filepath.Join(dir, "in.xml")
	writeFile(t, in, `<r/>`)
	writeFile(t, filepath.Join(dir, "s.xsd"),
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:s"/>`)
	withSchema := filepath.Join(dir, "s.xq")
	writeFile(t, withSchema, `import schema namespace s = "urn:s" at "s.xsd"; 1`)

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"-validate", "strict", "-q", plain, in}, "imports none"},
		{[]string{"-validate", "strict", "-q", withSchema}, "needs an input document"},
		{[]string{"-validate", "loose", "-q", withSchema, in}, "expected strict or lax"},
	}
	for _, c := range cases {
		if _, err := runQuery(t, dir, c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.args, err, c.want)
		}
	}
}
