package xsd

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/knroy/go-xml/v2/xdm"
)

// The process-global type tables outlive every schema, so what one load
// leaves there must scale with the distinct types, not with how often they
// are referenced. A 4 KB namespace and one type listed 2000 times in a
// union's memberTypes used to retain 2000 copies of "{namespace}a": 8 MB
// from a 16 KB schema, never released. Duplicates are now dropped, and every
// occurrence of one name shares one string.
func TestUnionReferencesDoNotAmplifyGlobalTypeEnv(t *testing.T) {
	ns := "urn:amplify:" + strings.Repeat("x", 4096)
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="` + ns +
		`" xmlns:t="` + ns + `"><xs:simpleType name="a"><xs:restriction base="xs:string"/></xs:simpleType>` +
		`<xs:simpleType name="u"><xs:union memberTypes="` + strings.Repeat("t:a ", 2000) + `"/></xs:simpleType>` +
		`<xs:simpleType name="v"><xs:union memberTypes="t:a"/></xs:simpleType></xs:schema>`
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(doc.Root, "", Options{}); err != nil {
		t.Fatal(err)
	}
	u := xdm.UnionMembersOf(xdm.AnnotationName(ns, "u"))
	v := xdm.UnionMembersOf(xdm.AnnotationName(ns, "v"))
	if len(u) != 1 || len(v) != 1 {
		t.Fatalf("members of u = %d, of v = %d; want 1 each: a duplicate member adds nothing", len(u), len(v))
	}
	if unsafe.StringData(u[0]) != unsafe.StringData(v[0]) {
		t.Error("u and v name the same member type with two copies of its 4 KB name")
	}
}
