package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// With no -root, "go-xml validate" must confine schema reads to the schema's
// own directory, as the transform does for its stylesheet. It once passed an
// empty Root to the resolvers unconditionally, and a non-nil resolver with no
// root permits any readable path -- so the flag's absence removed even the
// library's closed default, and the CLI was weaker than the package it wraps.
//
// Each case is driven through schemaValidator, the entry point the subcommand
// really uses, and asserts on the containment message: an err != nil alone
// would be met by a schema that is simply broken.

func writeSchema(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateXSDDefaultsRootToTheSchemaDirectory(t *testing.T) {
	parent := t.TempDir()
	// The escape target is a real, well-formed schema one level above the
	// schema's directory, so that it loads cleanly once confinement is gone
	// and this case fails the moment the default does.
	writeSchema(t, filepath.Join(parent, "far", "deep.xsd"),
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
			`<xs:element name="ok" type="xs:string"/></xs:schema>`)
	main := filepath.Join(parent, "d", "main.xsd")
	writeSchema(t, main,
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
			`<xs:include schemaLocation="../far/deep.xsd"/></xs:schema>`)

	// Sanity: rooted at the parent the schema loads and validates, so the
	// refusal below is the default root holding, not a broken schema.
	validate, err := schemaValidator(main, "", "1.0", "2.0", parent, 1)
	if err != nil {
		t.Fatalf("the escape schema must load under -root %s, or the "+
			"confined case proves nothing: %v", parent, err)
	}
	doc, err := xdm.ParseString(`<ok>x</ok>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(doc.Root); err != nil {
		t.Errorf("a document matching the included schema should validate: %v", err)
	}

	_, err = schemaValidator(main, "", "1.0", "2.0", "", 1)
	if err == nil {
		t.Fatal("with no -root, an xs:include climbing out of the schema's " +
			"directory was admitted")
	}
	if !strings.Contains(err.Error(), "resolves outside the permitted root") {
		t.Errorf("the CLI refused, but not as a containment failure: %v", err)
	}
}

func TestValidateRNGDefaultsRootToTheSchemaDirectory(t *testing.T) {
	parent := t.TempDir()
	writeSchema(t, filepath.Join(parent, "far", "deep.rng"),
		`<grammar xmlns="http://relaxng.org/ns/structure/1.0">`+
			`<start><element name="ok"><empty/></element></start></grammar>`)
	main := filepath.Join(parent, "d", "main.rng")
	writeSchema(t, main,
		`<grammar xmlns="http://relaxng.org/ns/structure/1.0">`+
			`<include href="../far/deep.rng"/></grammar>`)

	validate, err := schemaValidator("", main, "1.0", "", parent, 1)
	if err != nil {
		t.Fatalf("the escape grammar must compile under -root %s, or the "+
			"confined case proves nothing: %v", parent, err)
	}
	doc, err := xdm.ParseString(`<ok/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(doc.Root); err != nil {
		t.Errorf("a document matching the included grammar should validate: %v", err)
	}

	_, err = schemaValidator("", main, "1.0", "", "", 1)
	if err == nil {
		t.Fatal("with no -root, an <include> climbing out of the schema's " +
			"directory was admitted")
	}
	if !strings.Contains(err.Error(), "resolves outside root") {
		t.Errorf("the CLI refused, but not as a containment failure: %v", err)
	}
}

// The derived root is the schema's directory on whichever OS runs this, so
// the comparison goes through filepath on both sides: a Windows-shaped path
// is one name on Unix and a drive-rooted directory on Windows, and either way
// the root must be what filepath.Dir says it is.
func TestValidateDefaultRootIsFilepathDirOfTheSchema(t *testing.T) {
	for _, p := range []string{
		filepath.Join(t.TempDir(), "schemas", "main.rng"),
		"main.rng",
		filepath.Join("rel", "sub", "..", "main.rng"),
		`C:\schemas\main.rng`,
	} {
		abs, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Dir(filepath.Clean(abs))
		got, err := filepath.Abs(cliRNGResolver(schemaRoot("", p)).Root)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("schemaRoot(%q): root %q, want %q", p, got, want)
		}
	}
	// An explicit -root is taken as given, never replaced by the derivation.
	if got := schemaRoot("given", filepath.Join("d", "main.rng")); got != "given" {
		t.Errorf("an explicit -root was replaced: %q", got)
	}
}
