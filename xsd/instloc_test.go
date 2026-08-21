package xsd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// writeSchemaSet writes the two-document fixture the instance-location tests
// share: a root schema with a strict wildcard, and a second document the
// instance names for the namespace the wildcard admits.
func writeSchemaSet(t *testing.T) (dir, main string) {
	t.Helper()
	dir = t.TempDir()
	main = filepath.Join(dir, "main.xsd")
	if err := os.WriteFile(main, []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType><xs:sequence>
	      <xs:any namespace="##any" processContents="strict"/>
	    </xs:sequence></xs:complexType>
	  </xs:element>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.xsd"), []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="urn:other">
	  <xs:element name="b" type="xs:string"/>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, main
}

const instDoc = `<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
	` xsi:schemaLocation="urn:other other.xsd">` +
	`<b xmlns="urn:other">x</b></doc>`

// The default is to ignore xsi:schemaLocation entirely: following it lets
// whoever wrote the document choose the schema it is judged against.
func TestInstanceLocationsIgnoredByDefault(t *testing.T) {
	_, main := writeSchemaSet(t)
	s, err := LoadFiles([]string{main}, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString(instDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{}); err == nil {
		t.Error("xsi:schemaLocation was followed without being asked for")
	}
}

// The zero policy grants nothing: AllowNamespace nil means no namespace is
// allowed, so a policy that merely exists does not open the door.
func TestZeroPolicyGrantsNothing(t *testing.T) {
	_, main := writeSchemaSet(t)
	s, err := LoadFiles([]string{main}, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString(instDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ext, err := s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{},
		Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ext.Validate(tree.Root, ValidateOptions{}); err == nil {
		t.Error("the zero policy followed a location")
	}
}

// With the namespace allowed, the document the instance names is assembled
// alongside the ones already loaded and the wildcard finds its declaration.
func TestInstanceLocationsWhenAllowed(t *testing.T) {
	_, main := writeSchemaSet(t)
	s, err := LoadFiles([]string{main}, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString(instDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ext, err := s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{
		AllowNamespace: func(ns string) bool { return ns == "urn:other" },
	}, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatalf("extending the schema: %v", err)
	}
	if err := ext.Validate(tree.Root, ValidateOptions{}); err != nil {
		t.Errorf("the allowed location was not used: %v", err)
	}
	// The original is unchanged: a Schema is immutable and shared.
	if err := s.Validate(tree.Root, ValidateOptions{}); err == nil {
		t.Error("extending the schema mutated the receiver")
	}
}

// A namespace the policy refuses is ignored rather than being an error: the
// location is a hint, and declining it is not a fault in the document.
func TestRefusedNamespaceIsIgnoredNotAnError(t *testing.T) {
	_, main := writeSchemaSet(t)
	s, err := LoadFiles([]string{main}, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString(instDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ext, err := s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{
		AllowNamespace: func(ns string) bool { return ns == "urn:trusted" },
	}, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatalf("a refused location was an error: %v", err)
	}
	if err := ext.Validate(tree.Root, ValidateOptions{}); err == nil {
		t.Error("a refused location was followed anyway")
	}
}

// An instance naming a document the schema already holds must not load it
// twice.
//
// The ordinary spelling puts xsi:noNamespaceSchemaLocation — the instance's own
// schema — beside an xsi:schemaLocation for some other namespace, so the list
// routinely contains a document already in hand. Loading it again made every
// global in it a duplicate of itself and failed the whole assembly with
// sch-props-correct.2, which reads as a broken schema rather than as the
// double-load it is. The suite reaches this through particlesB013, ctL021 and
// attgD034.
func TestInstanceLocationNamingTheSchemaItself(t *testing.T) {
	_, main := writeSchemaSet(t)

	s, err := LoadFile(main, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	// The instance names main.xsd as well as other.xsd. The spellings
	// differ from the one the schema was loaded under, which is why the
	// comparison has to be by file identity rather than by path text.
	doc := `<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
		` xsi:noNamespaceSchemaLocation="./main.xsd"` +
		` xsi:schemaLocation="urn:other other.xsd">` +
		`<b xmlns="urn:other">x</b></doc>`
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}

	ext, err := s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{
		AllowNamespace:   func(string) bool { return true },
		AllowNoNamespace: true,
		Resolver:         &FileResolver{},
	}, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatalf("the schema's own document should not collide with itself: %v", err)
	}
	if err := ext.Validate(tree.Root, ValidateOptions{}); err != nil {
		t.Errorf("the strict wildcard should find the declaration: %v", err)
	}
}
