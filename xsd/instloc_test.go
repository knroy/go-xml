package xsd

import (
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
// xs:import makes the same check (src-import.3.1). The location is a hint
// (§4.3.2), so a document that fails it is declined, not an error: nothing
// from it is added, and the rest of the instance's locations still apply.
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
		if err != nil {
			t.Errorf("%s: a declined location failed the call: %v", doc, err)
			continue
		}
		if len(ext.Elements) != len(s.Elements) {
			t.Errorf("%s: the declined document added components: %v", doc, ext.Elements)
		}
		if ext.Validate(tree.Root, ValidateOptions{}) == nil {
			t.Errorf("%s: validated against the declined document", doc)
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
// location through one. A location outside is declined like any other the
// policy refuses (§4.3.2 makes it a hint): the resolver is never asked for it.
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
	// The scheme is case-insensitive (RFC 3986 §3.1). An upper-case one
	// was checked as a relative path under the schema directory and then
	// opened, by the unrooted resolver, as the absolute file it names.
	u := fileuri.Of(evil)
	for _, loc := range []string{evil, rel, u, "FILE" + u[4:], "File" + u[4:]} {
		doc := `<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
			` xsi:noNamespaceSchemaLocation="` + loc + `"><x>1</x></doc>`
		tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		r := &countingResolver{Resolver: &FileResolver{}}
		ext, err := s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{
			AllowNoNamespace: true,
			Resolver:         r,
		}, Options{})
		if err != nil {
			t.Errorf("%s: a declined location failed the call: %v", loc, err)
			continue
		}
		if r.fetches != 0 {
			t.Errorf("%s: the resolver was asked %d times", loc, r.fetches)
		}
		if ext.Validate(tree.Root, ValidateOptions{}) == nil {
			t.Errorf("%s: validated against a document outside the schema directory", loc)
		}
	}
}

// An error about a document the instance named quotes the location the
// instance wrote, never the host path it was joined into: a document that is
// not XML, and one whose root is not xs:schema.
func TestInstanceLocationErrorQuotesOnlyTheLocation(t *testing.T) {
	dir, main := writeSchemaSet(t)
	for name, src := range map[string]string{
		"broken.xsd": "<xs:schema", "notschema.xsd": "<x/>",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := LoadFiles([]string{main}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(dir)
	for _, name := range []string{"broken.xsd", "notschema.xsd"} {
		tree, err := xdm.ParseString(`<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`+
			` xsi:noNamespaceSchemaLocation="`+name+`"><x>1</x></doc>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{AllowNoNamespace: true}, Options{})
		if err == nil {
			t.Fatalf("%s: a document the instance named that is not a schema loaded", name)
		}
		if !strings.Contains(err.Error(), `"`+name+`"`) ||
			strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), abs) {
			t.Errorf("error %q should quote only the instance's location", err)
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
	// The path checked is the path returned, whatever the scheme's case.
	in := filepath.Join(dirs[0], "a.xsd")
	for _, loc := range []string{fileuri.Of(in), "FILE" + fileuri.Of(in)[4:]} {
		if got, err := instanceLocalPath(loc, dirs[0], dirs); err != nil || got != in {
			t.Errorf("instanceLocalPath(%q) = %q, %v, want %q", loc, got, err, in)
		}
	}
	if _, err := instanceLocalPath("FILE://host/share/x.xsd", dirs[0], dirs); err == nil {
		t.Error("instanceLocalPath(FILE://host/...) was admitted")
	}
	if got, err := instanceLocalPath("https://e.example/a.xsd", dirs[0], dirs); err != nil ||
		got != "https://e.example/a.xsd" {
		t.Errorf("a remote location is the resolver's to judge: %q, %v", got, err)
	}
}

// Errors about documents reached from an instance-named location, through
// its includes and imports, name them by the location text as written in the
// referring document. The same faults in the caller's own load keep the
// resolved path, which the caller chose and which helps them find the file.
func TestInstanceOriginErrorsQuoteOnlyLocations(t *testing.T) {
	dir, main := writeSchemaSet(t)
	for name, src := range map[string]string{
		"inc.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
			`<xs:include schemaLocation="broken.xsd"/>` +
			`<xs:include schemaLocation="deep.xsd"/>` +
			`<xs:import namespace="urn:x" schemaLocation="wrongns.xsd"/>` +
			`</xs:schema>`,
		"broken.xsd":  "<xs:schema",
		"deep.xsd":    `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element/></xs:schema>`,
		"wrongns.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:y"/>`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	abs, _ := filepath.Abs(dir)
	real, _ := filepath.EvalSymlinks(abs)
	leaks := func(msg string) bool {
		return strings.Contains(msg, dir) || strings.Contains(msg, abs) ||
			strings.Contains(msg, real)
	}

	s, err := LoadFiles([]string{main}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString(`<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`+
		` xsi:noNamespaceSchemaLocation="inc.xsd"><x>1</x></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{AllowNoNamespace: true}, Options{})
	if err == nil {
		t.Fatal("an instance-named schema with faulty includes loaded")
	}
	msg := err.Error()
	if leaks(msg) {
		t.Errorf("an error from the instance's documents carries a host path:\n%s", msg)
	}
	for _, name := range []string{`"broken.xsd"`, `"wrongns.xsd"`, "deep.xsd"} {
		if !strings.Contains(msg, name) {
			t.Errorf("the error does not name %s:\n%s", name, msg)
		}
	}

	// A resolver's own error text is scrubbed too: an unrooted FileResolver
	// reports a missing file by the absolute path it tried to open.
	if err := os.WriteFile(filepath.Join(dir, "redef.xsd"), []byte(
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
			`<xs:redefine schemaLocation="missing.xsd"><xs:simpleType name="t">`+
			`<xs:restriction base="t"/></xs:simpleType></xs:redefine></xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err = xdm.ParseString(`<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`+
		` xsi:noNamespaceSchemaLocation="redef.xsd"><x>1</x></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.WithInstanceLocations(tree.Root, InstanceLocationPolicy{
		AllowNoNamespace: true, Resolver: &FileResolver{},
	}, Options{})
	if err == nil || leaks(err.Error()) || !strings.Contains(err.Error(), `"missing.xsd"`) {
		t.Errorf("a resolver error from the instance's documents: %v", err)
	}

	// The caller's own load is named by resolved path.
	_, err = LoadFiles([]string{filepath.Join(dir, "inc.xsd")}, Options{})
	if err == nil || !leaks(err.Error()) {
		t.Errorf("the caller's own load should keep the resolved path: %v", err)
	}
}
