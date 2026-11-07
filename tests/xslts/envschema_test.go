package xslts

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnvSchemaHonoursXSDVersion: an environment's <schema xsd-version="1.1">
// is loaded as XSD 1.1. strip-space-009.xsd carries an xs:assert and no
// vc:minVersion, so read as 1.0 it is refused and the environment had no
// schema at all.
func TestEnvSchemaHonoursXSDVersion(t *testing.T) {
	dir := t.TempDir()
	const xsd = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="t"><xs:sequence/><xs:assert test="true()"/></xs:complexType>
  <xs:element name="e" type="t"/>
</xs:schema>`
	if err := os.WriteFile(filepath.Join(dir, "s.xsd"), []byte(xsd), 0o644); err != nil {
		t.Fatal(err)
	}
	set := &TestSet{Dir: dir}
	// Elements, not nil: the merged schema always holds the built-in types.
	loaded := func(version string) bool {
		s := envSchema(set, &Environment{Schemas: []Schema{{File: "s.xsd", XSDVersion: version}}})
		return s != nil && len(s.Elements) > 0
	}
	if !loaded("1.1") {
		t.Error(`xsd-version="1.1" schema did not load`)
	}
	if loaded("") {
		t.Error("1.0 accepted xs:assert; the test no longer shows anything")
	}
}
