package qt3

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCaseSchemasReadAsXSD11 pins that the harness assembles an environment's
// schema under the XSD version the engine reads imports at, 1.1. xq-e18.xsd
// restricts xs:dayTimeDuration and xs:yearMonthDuration, which XSD 1.0 does
// not define, so a 1.0 reading refused the fixture before the query ran.
func TestCaseSchemasReadAsXSD11(t *testing.T) {
	dir := t.TempDir()
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:d">
	  <xs:simpleType name="t"><xs:restriction base="xs:dayTimeDuration"/></xs:simpleType>
	</xs:schema>`
	if err := os.WriteFile(filepath.Join(dir, "s.xsd"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Root: dir}
	got, err := r.caseSchemas(Environment{Schemas: []Schema{{URI: "urn:d", File: "s.xsd"}}})
	if err != nil {
		t.Fatalf("caseSchemas: %v", err)
	}
	if len(got) != 1 || got[0].Components == nil {
		t.Fatalf("got %+v", got)
	}
}
