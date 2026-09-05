package xsd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The resolution default.
//
// SECURITY.md says no schema, document or entity is fetched unless a resolver
// is supplied. Four sites in this package defaulted a nil Options.Resolver to a
// FileResolver with no Root, which its own doc comment says permits "any
// readable path", so the claim did not hold here.
//
// The gap was reachable from outside the package. xslt refuses an
// xsl:import-schema that names a schema-location when no SchemaResolver is
// configured, and then passes that same nil Options.Resolver to xsd.Load for an
// inline <xs:schema> — whose own xs:include was followed against the open
// default. A caller who hardened xslt still had the filesystem readable through
// a stylesheet it compiled.
//
// The rule now is that the default follows the grant the caller already made:
//
//   - Load takes a tree and no path. Nothing on disk was granted, so nothing on
//     disk is read, and a location that cannot be resolved says so.
//   - LoadFile and LoadFiles were handed paths. Reading beside them is what the
//     caller asked for, so a FileResolver stays — rooted at those directories,
//     rather than open, so an absolute path elsewhere is still refused.
//   - WithInstanceLocations takes locations from the instance, the least
//     trusted input here, and is rooted at the schema's own directories.

// writeSchemaFiles lays out a temporary directory and returns it.
func writeSchemaFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestLoadRefusesWithoutResolver is the security assertion: Load with a
// zero-value Options must not open a file, and must say why rather than
// dropping the include and reporting success.
func TestLoadRefusesWithoutResolver(t *testing.T) {
	// A file that exists and is readable. If the default still resolved,
	// the error would be about this file's contents — the old behaviour
	// reported the root element it found, which proved the read happened.
	dir := writeSchemaFiles(t, map[string]string{
		"canary.xsd": `<notASchema/>`,
	})
	canary := filepath.Join(dir, "canary.xsd")

	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:include schemaLocation="` + canary + `"/>
	</xs:schema>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(tree.Root, "", Options{})
	if err == nil {
		t.Fatal("Load with no Resolver accepted a schema whose include " +
			"named an absolute path; it must refuse")
	}
	if !strings.Contains(err.Error(), "no Resolver is configured") {
		t.Errorf("error should name the missing Resolver, got: %v", err)
	}
	// The decisive check: the file must not have been read. The old
	// behaviour reported the root element it found inside.
	if strings.Contains(err.Error(), "notASchema") {
		t.Errorf("the file was opened and parsed: %v", err)
	}
}

// TestLoadWithoutResolverAllowsSelfContained keeps the refusal narrow. A schema
// that resolves nothing has nothing to refuse, and must still load with a
// zero-value Options — that is by far the common use of Load.
func TestLoadWithoutResolverAllowsSelfContained(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="a" type="xs:string"/>
	</xs:schema>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(tree.Root, "", Options{})
	if err != nil {
		t.Fatalf("a self-contained schema must load with no Resolver: %v", err)
	}
	if s.Elements[xdm.QName{Local: "a"}] == nil {
		t.Error("the element declaration did not survive")
	}
}

// TestLoadRefusalIsLoudForInclude covers the "silent hardening" case.
//
// §4.2.1 lets an unresolvable include be dropped, and a resolver that looked
// and came back empty is exactly that case. No resolver at all is not: the
// schema loses components of its own namespace for a reason that has nothing to
// do with the schema, and a caller who hardened would otherwise validate
// against whatever was left and be told it succeeded.
func TestLoadRefusalIsLoudForInclude(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:include schemaLocation="other.xsd"/>
	  <xs:element name="a" type="xs:string"/>
	</xs:schema>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tree.Root, "t.xsd", Options{}); err == nil {
		t.Fatal("an include that no resolver could even look for should be " +
			"reported, not dropped")
	}

	// A resolver that answers "I have nothing" keeps the §4.2.1 behaviour:
	// the include is dropped and assembly succeeds. This is the boundary
	// the diagnostic must not cross.
	if _, err := Load(tree.Root, "t.xsd", Options{Resolver: &MapResolver{}}); err != nil {
		t.Errorf("a resolver that declines a location must leave assembly "+
			"successful per §4.2.1: %v", err)
	}
}

// TestLoadFileDefaultIsRootedToItsDirectory holds the LoadFile compromise. The
// caller named a file, so a sibling include still resolves; an absolute path
// outside that directory does not.
func TestLoadFileDefaultIsRootedToItsDirectory(t *testing.T) {
	outside := writeSchemaFiles(t, map[string]string{
		"secret.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`,
	})
	dir := writeSchemaFiles(t, map[string]string{
		"main.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:include schemaLocation="part.xsd"/>
		  <xs:element name="a" type="xs:string"/>
		</xs:schema>`,
		"part.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="b" type="xs:string"/>
		</xs:schema>`,
		"escaping.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:include schemaLocation="` + filepath.Join(outside, "secret.xsd") + `"/>
		</xs:schema>`,
	})

	// The sibling include must still work: refusing it would break every
	// command-line use for no gain, since the caller's own argument already
	// granted this directory.
	s, err := LoadFile(filepath.Join(dir, "main.xsd"), Options{})
	if err != nil {
		t.Fatalf("a sibling include must resolve under the default: %v", err)
	}
	if s.Elements[xdm.QName{Local: "b"}] == nil {
		t.Error("the included declaration did not arrive")
	}

	// An absolute path outside the named directory must not.
	_, err = LoadFile(filepath.Join(dir, "escaping.xsd"), Options{})
	if err == nil {
		t.Fatal("an include naming a path outside the schema's own directory " +
			"was followed under the default resolver")
	}
	if !strings.Contains(err.Error(), "outside the permitted root") {
		t.Errorf("error should name the root confinement, got: %v", err)
	}
}

// TestLoadFilesDefaultIsRootedToTheirDirectories is the same for LoadFiles,
// which may be handed paths in several directories and so grants the set.
func TestLoadFilesDefaultIsRootedToTheirDirectories(t *testing.T) {
	outside := writeSchemaFiles(t, map[string]string{
		"secret.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`,
	})
	a := writeSchemaFiles(t, map[string]string{
		"a.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="a" type="xs:string"/>
		</xs:schema>`,
	})
	b := writeSchemaFiles(t, map[string]string{
		"b.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:include schemaLocation="bpart.xsd"/>
		  <xs:element name="b" type="xs:string"/>
		</xs:schema>`,
		"bpart.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="c" type="xs:string"/>
		</xs:schema>`,
		"escaping.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:include schemaLocation="` + filepath.Join(outside, "secret.xsd") + `"/>
		</xs:schema>`,
	})

	// Both named directories are granted, and an include inside either
	// resolves.
	s, err := LoadFiles([]string{
		filepath.Join(a, "a.xsd"),
		filepath.Join(b, "b.xsd"),
	}, Options{})
	if err != nil {
		t.Fatalf("LoadFiles over two directories: %v", err)
	}
	for _, n := range []string{"a", "b", "c"} {
		if s.Elements[xdm.QName{Local: n}] == nil {
			t.Errorf("declaration %q did not arrive", n)
		}
	}

	// A third directory was never named, so it is not granted.
	_, err = LoadFiles([]string{
		filepath.Join(a, "a.xsd"),
		filepath.Join(b, "escaping.xsd"),
	}, Options{})
	if err == nil {
		t.Fatal("LoadFiles followed an include into a directory the caller " +
			"never named")
	}
	if !strings.Contains(err.Error(), "outside the permitted root") {
		t.Errorf("error should name the root confinement, got: %v", err)
	}
}

// namedFileOnlyResolver opens exactly one path and declines everything else.
//
// LoadFile resolves the file it was given through the caller's resolver, so a
// bare MapResolver cannot even open the schema the caller named; this is the
// smallest resolver that can distinguish "the caller's resolver was used" from
// "the default replaced it".
type namedFileOnlyResolver struct{ allow string }

func (r namedFileOnlyResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if location != r.allow {
		return nil, "", nil
	}
	f, err := os.Open(location)
	if err != nil {
		return nil, "", err
	}
	return f, location, nil
}

// TestExplicitResolverIsNeverOverridden guards the whole arrangement: none of
// the defaulting above may touch a resolver the caller supplied.
func TestExplicitResolverIsNeverOverridden(t *testing.T) {
	dir := writeSchemaFiles(t, map[string]string{
		"main.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:include schemaLocation="part.xsd"/>
		</xs:schema>`,
		"part.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="b" type="xs:string"/>
		</xs:schema>`,
	})
	main := filepath.Join(dir, "main.xsd")

	// The caller's resolver opens the named file and declines the sibling.
	// Under §4.2.1 declining is a location that is not there, so the
	// include is dropped and the load succeeds without it — the caller's
	// resolver decided that, and the default must not have stepped in and
	// followed it anyway.
	s, err := LoadFile(main, Options{Resolver: namedFileOnlyResolver{allow: main}})
	if err != nil {
		t.Fatalf("the caller's resolver should have opened the named file: %v", err)
	}
	if s.Elements[xdm.QName{Local: "b"}] != nil {
		t.Error("the include was followed even though the caller's resolver " +
			"declined it: the default overrode the explicit resolver")
	}
}
