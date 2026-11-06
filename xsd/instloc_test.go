package xsd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/fileuri"
	"github.com/knroy/go-xml/v2/xdm"
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

// An instance-supplied location outside the schema's own directories must not
// be statted, nor its directory listed, to deduplicate it: that is host
// filesystem access the resolver never admitted, repeated once per spelling.
// Inside those directories identity is still what names the schema's own
// documents, however the instance spells them.
func TestInstanceLocationKeyStatsOnlySchemaDirs(t *testing.T) {
	dir, main := writeSchemaSet(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "target.xsd")
	if err := os.WriteFile(target, []byte("<x/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	var c canonCache
	spelt := filepath.Join(outside, ".", "target.xsd")
	if got := instanceLocationKey(&c, []string{abs}, spelt); got != target {
		t.Errorf("key(%q) = %q, want the cleaned text %q", spelt, got, target)
	}
	if len(c.byPath) != 0 {
		t.Errorf("an outside location was looked up on the filesystem: %v", c.byPath)
	}
	if got, want := instanceLocationKey(&c, []string{abs}, filepath.Join(dir, ".", "main.xsd")),
		canonicalLocation(main); got != want {
		t.Errorf("the schema's own document keyed as %q, want its identity %q", got, want)
	}
}

// A document the instance names must define the namespace it was named for.
// The policy allows namespaces, and the check ran on the pair's namespace
// text alone, so pairing an allowed namespace with a document defining
// another let the instance supply declarations the policy never allowed.
// xs:import makes the same check (src-import.3.1).
func TestInstanceLocationMustDefineItsNamespace(t *testing.T) {
	dir, main := writeSchemaSet(t)
	if err := os.WriteFile(filepath.Join(dir, "evil.xsd"), []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="urn:secret">
	  <xs:element name="c" type="xs:string"/>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "none.xsd"), []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="c" type="xs:string"/>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFiles([]string{main}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	policy := InstanceLocationPolicy{
		AllowNamespace:   func(ns string) bool { return ns == "urn:other" },
		AllowNoNamespace: true,
	}
	for _, doc := range []string{
		// An allowed namespace paired with a document defining urn:secret.
		`<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
			` xsi:schemaLocation="urn:other evil.xsd"><c xmlns="urn:secret">x</c></doc>`,
		// No namespace, naming a document that has one.
		`<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
			` xsi:noNamespaceSchemaLocation="evil.xsd"><c xmlns="urn:secret">x</c></doc>`,
		// An allowed namespace, naming a document that has none.
		`<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
			` xsi:schemaLocation="urn:other none.xsd"><c>x</c></doc>`,
	} {
		tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ext, err := s.WithInstanceLocations(tree.Root, policy, Options{})
		if err == nil {
			t.Errorf("%s: extended without error; Validate = %v",
				doc, ext.Validate(tree.Root, ValidateOptions{}))
			continue
		}
		if !strings.Contains(err.Error(), "target namespace") {
			t.Errorf("%s: error %q is not the namespace mismatch", doc, err)
		}
	}
	// The matching pair is still followed.
	tree, err := xdm.ParseString(instDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ext, err := s.WithInstanceLocations(tree.Root, policy, Options{})
	if err != nil {
		t.Fatalf("a matching location was refused: %v", err)
	}
	if err := ext.Validate(tree.Root, ValidateOptions{}); err != nil {
		t.Errorf("the matching location was not used: %v", err)
	}
}

// A local location the instance names must lie inside the schema's own
// directories whatever resolver is supplied. An unrooted FileResolver stands
// in here for xsdnet.HTTPResolver with no Files, which reads every local
// location through one.
func TestInstanceLocationConfinedWhateverResolver(t *testing.T) {
	_, main := writeSchemaSet(t)
	outside := t.TempDir()
	evil := filepath.Join(outside, "evil.xsd")
	if err := os.WriteFile(evil, []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="x" type="xs:string"/>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFiles([]string{main}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Dir(main), evil)
	if err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{evil, rel, fileuri.Of(evil)} {
		doc := `<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
			` xsi:noNamespaceSchemaLocation="` + loc + `"><x>1</x></doc>`
		tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{
			AllowNoNamespace: true,
			Resolver:         &FileResolver{},
		}, Options{})
		if !errors.Is(err, errRefusedByPolicy) {
			t.Errorf("%s: WithInstanceLocations = %v, want the confinement refusal", loc, err)
		}
	}
}

// The confinement check runs on the string before the filesystem for a
// network path, on every OS.
func TestInstanceLocalPathRefusesNetworkPaths(t *testing.T) {
	dirs := []string{t.TempDir()}
	for _, loc := range []string{`\\host\share\x.xsd`, `//host/share/x.xsd`,
		`\??\UNC\host\share\x.xsd`, "file://host/share/x.xsd"} {
		if _, err := instanceLocalPath(loc, dirs[0], dirs); err == nil {
			t.Errorf("instanceLocalPath(%q) was admitted", loc)
		}
	}
	if got, err := instanceLocalPath("a.xsd", dirs[0], dirs); err != nil ||
		got != filepath.Join(dirs[0], "a.xsd") {
		t.Errorf("instanceLocalPath(a.xsd) = %q, %v", got, err)
	}
	if got, err := instanceLocalPath("https://e.example/a.xsd", dirs[0], dirs); err != nil ||
		got != "https://e.example/a.xsd" {
		t.Errorf("a remote location is the resolver's to judge: %q, %v", got, err)
	}
}
