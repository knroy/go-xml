package xsd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// loadFromMap assembles a schema from an in-memory set of documents, so that a
// multi-document test needs no files on disk.
func loadFromMap(t *testing.T, main string, docs map[string]string) (*Schema, error) {
	t.Helper()
	tree, err := xdm.ParseString(docs[main], xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing %s: %v", main, err)
	}
	return Load(tree.Root, main, Options{
		Resolver: &MapResolver{ByLocation: docs},
	})
}

func TestImportAnotherNamespace(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:o="urn:other" targetNamespace="urn:main">
		  <xs:import namespace="urn:other" schemaLocation="other.xsd"/>
		  <xs:element name="root" type="o:otherType"/>
		</xs:schema>`,
		"other.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:other">
		  <xs:simpleType name="otherType">
		    <xs:restriction base="xs:string"><xs:maxLength value="5"/></xs:restriction>
		  </xs:simpleType>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	d := s.Elements[xdm.QName{URI: "urn:main", Local: "root"}]
	if d == nil {
		t.Fatal("root was not declared")
	}
	if d.Type == nil {
		t.Fatal("the imported type was not resolved")
	}
	if got := d.Type.TypeName(); got.URI != "urn:other" || got.Local != "otherType" {
		t.Errorf("type is %v, want {urn:other}otherType", got)
	}
}

func TestIncludeSameNamespace(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t">
		  <xs:include schemaLocation="part.xsd"/>
		  <xs:element name="root" type="t:partType"/>
		</xs:schema>`,
		"part.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
		  <xs:simpleType name="partType">
		    <xs:restriction base="xs:int"/>
		  </xs:simpleType>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Types[xdm.QName{URI: "urn:t", Local: "partType"}] == nil {
		t.Error("the included type is missing")
	}
}

// TestChameleonInclude covers the rule that an included document with no target
// namespace adopts the includer's.
//
// It is called a chameleon because the same file means different things
// depending on who includes it, and the rewrite has to reach every named
// component — not just the top-level ones.
func TestChameleonInclude(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t">
		  <xs:include schemaLocation="nons.xsd"/>
		  <xs:element name="root" type="t:floating"/>
		</xs:schema>`,
		"nons.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="floating">
		    <xs:restriction base="xs:string"/>
		  </xs:simpleType>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The type was declared with no namespace but must land in urn:t.
	if s.Types[xdm.QName{URI: "urn:t", Local: "floating"}] == nil {
		t.Error("the chameleon-included type did not adopt the including namespace")
	}
	if s.Types[xdm.QName{Local: "floating"}] != nil {
		t.Error("the chameleon-included type stayed in the absent namespace")
	}
}

// TestCircularImportTerminates covers two schemas importing each other, which
// is legal and common.
func TestCircularImportTerminates(t *testing.T) {
	docs := map[string]string{
		"a.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:b="urn:b" targetNamespace="urn:a">
		  <xs:import namespace="urn:b" schemaLocation="b.xsd"/>
		  <xs:element name="ra" type="b:tb"/>
		  <xs:simpleType name="ta"><xs:restriction base="xs:string"/></xs:simpleType>
		</xs:schema>`,
		"b.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:a="urn:a" targetNamespace="urn:b">
		  <xs:import namespace="urn:a" schemaLocation="a.xsd"/>
		  <xs:element name="rb" type="a:ta"/>
		  <xs:simpleType name="tb"><xs:restriction base="xs:int"/></xs:simpleType>
		</xs:schema>`,
	}
	done := make(chan error, 1)
	go func() {
		_, err := loadFromMap(t, "a.xsd", docs)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mutually importing schemas should load: %v", err)
		}
	case <-timeoutAfterSecond():
		t.Fatal("a circular import did not terminate")
	}
}

// TestSelfImportRejected covers src-import.1.1: a schema may not import its own
// namespace. Import is for a *different* namespace; include is the same-
// namespace mechanism.
func TestSelfImportRejected(t *testing.T) {
	docs := map[string]string{
		"a.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:a">
		  <xs:import namespace="urn:a" schemaLocation="b.xsd"/>
		</xs:schema>`,
		"b.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:a"/>`,
	}
	_, err := loadFromMap(t, "a.xsd", docs)
	if err == nil {
		t.Fatal("importing one's own namespace should be rejected")
	}
	if !strings.Contains(err.Error(), "src-import") {
		t.Errorf("error %q does not cite src-import", err)
	}
}

// TestUnresolvableIncludeIsNotAnError covers §4.2.1 clause 1: "It is not an
// error for the actual value of the schemaLocation attribute to fail to resolve
// at all, in which case no corresponding inclusion is performed."
func TestUnresolvableIncludeIsNotAnError(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
		  <xs:include schemaLocation="nowhere.xsd"/>
		  <xs:element name="root" type="xs:string"/>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("an unresolvable include should not be an error: %v", err)
	}
	if s.Elements[xdm.QName{URI: "urn:t", Local: "root"}] == nil {
		t.Error("the rest of the document should still have been read")
	}
}

func TestDocumentLimitIsEnforced(t *testing.T) {
	// Each document includes the next, so the chain is longer than the
	// limit and assembly must stop rather than run to the end.
	docs := map[string]string{}
	for i := 0; i < 20; i++ {
		next := ""
		if i < 19 {
			next = `<xs:include schemaLocation="d` + string(rune('a'+i+1)) + `.xsd"/>`
		}
		docs["d"+string(rune('a'+i))+".xsd"] =
			`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` + next + `</xs:schema>`
	}
	tree, err := xdm.ParseString(docs["da.xsd"], xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(tree.Root, "da.xsd", Options{
		Resolver:     &MapResolver{ByLocation: docs},
		MaxDocuments: 5,
	})
	if err == nil {
		t.Fatal("the document limit should have been enforced")
	}
	if !strings.Contains(err.Error(), "MaxDocuments") {
		t.Errorf("error %q does not mention the limit", err)
	}
}

func TestSubstitutionGroupClosure(t *testing.T) {
	// C substitutes for B, and B for A, so C substitutes for A. The
	// closure is transitive and has to be computed after every document is
	// read, since a member may arrive later than its head.
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">
		  <xs:element name="a" type="xs:string"/>
		  <xs:element name="b" type="xs:string" substitutionGroup="t:a"/>
		  <xs:element name="c" type="xs:string" substitutionGroup="t:b"/>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := s.Elements[xdm.QName{URI: "urn:t", Local: "a"}]
	if a == nil {
		t.Fatal("a was not declared")
	}
	got := map[string]bool{}
	for _, d := range a.Substitutable() {
		got[d.Name.Local] = true
	}
	if !got["b"] || !got["c"] {
		t.Errorf("a's substitution group is %v, want b and c", got)
	}

	b := s.Elements[xdm.QName{URI: "urn:t", Local: "b"}]
	if len(b.Substitutable()) != 1 || b.Substitutable()[0].Name.Local != "c" {
		t.Errorf("b's substitution group should be just c")
	}
}

// TestSubstitutionGroupCycleTerminates guards the closure against a circular
// substitution group. The spec bans them, but a malformed schema can still
// write one and the closure must not hang before the ban can be reported.
func TestSubstitutionGroupCycleTerminates(t *testing.T) {
	s := NewSchema()
	a := &ElementDecl{Name: xdm.QName{Local: "a"}, Scope: ScopeGlobal}
	b := &ElementDecl{Name: xdm.QName{Local: "b"}, Scope: ScopeGlobal}
	a.SubstitutionGroup = b
	b.SubstitutionGroup = a
	s.Elements[a.Name] = a
	s.Elements[b.Name] = b

	done := make(chan struct{})
	go func() { linkSubstitutionGroups(s); close(done) }()
	select {
	case <-done:
	case <-timeoutAfterSecond():
		t.Fatal("a circular substitution group did not terminate")
	}
}

func TestFileResolverConfinesToRoot(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "ok.xsd")
	if err := os.WriteFile(inside, []byte(`<a/>`), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &FileResolver{Root: dir}
	rc, _, err := r.Resolve("", "ok.xsd", filepath.Join(dir, "main.xsd"))
	if err != nil {
		t.Fatalf("a file inside the root should resolve: %v", err)
	}
	rc.Close()

	// "../" out of the root must be refused, whatever it points at.
	if _, _, err := r.Resolve("", "../escape.xsd", filepath.Join(dir, "main.xsd")); err == nil {
		t.Error("a location escaping the root should be refused")
	}
	if _, _, err := r.Resolve("", "/etc/passwd", ""); err == nil {
		t.Error("an absolute path outside the root should be refused")
	}
}

// TestFileResolverRefusesRemote records that network resolution is off unless
// the caller asks for it. A schemaLocation is chosen by whoever wrote the
// schema — and, through xsi:schemaLocation, by whoever wrote the instance — so
// fetching it by default would let a document choose what this process
// requests, and which schema it is judged against.
func TestFileResolverRefusesRemote(t *testing.T) {
	r := &FileResolver{}
	_, _, err := r.Resolve("", "http://example.com/s.xsd", "")
	if err == nil {
		t.Fatal("the default resolver should refuse a remote location")
	}
	if !strings.Contains(err.Error(), "HTTPResolver") {
		t.Errorf("error %q does not say how to enable network resolution", err)
	}
}

func TestHTTPResolverHostPolicy(t *testing.T) {
	// AllowHost runs before the request, so a refused host means no
	// connection is attempted at all — which is what makes it usable to
	// block loopback and link-local addresses.
	r := &HTTPResolver{AllowHost: func(host string) bool { return host == "good.example" }}
	if _, _, err := r.Resolve("", "http://bad.example/s.xsd", ""); err == nil {
		t.Error("a host outside the policy should be refused")
	}
}

func TestHTTPResolverFallsBackToFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "local.xsd")
	if err := os.WriteFile(p, []byte(`<a/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &HTTPResolver{}
	rc, _, err := r.Resolve("", "local.xsd", filepath.Join(dir, "main.xsd"))
	if err != nil {
		t.Fatalf("a local location should still resolve: %v", err)
	}
	rc.Close()
}

// TestChameleonIncludeIntoTwoNamespaces covers one namespace-less document
// pulled into two different target namespaces by two separate includers.
//
// Each reading produces a distinct set of components — {urn:a}shared and
// {urn:b}shared — and both are required, because each includer refers to the
// name it created. Deduplicating on the document's location alone let whichever
// include ran first suppress the other, leaving the loser's reference dangling.
func TestChameleonIncludeIntoTwoNamespaces(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:import namespace="urn:a" schemaLocation="a.xsd"/>
		  <xs:import namespace="urn:b" schemaLocation="b.xsd"/>
		</xs:schema>`,
		"a.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:a="urn:a" targetNamespace="urn:a">
		  <xs:include schemaLocation="nons.xsd"/>
		  <xs:element name="ea" type="a:shared"/>
		</xs:schema>`,
		"b.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:b="urn:b" targetNamespace="urn:b">
		  <xs:include schemaLocation="nons.xsd"/>
		  <xs:element name="eb" type="b:shared"/>
		</xs:schema>`,
		"nons.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="shared">
		    <xs:restriction base="xs:string"/>
		  </xs:simpleType>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, ns := range []string{"urn:a", "urn:b"} {
		if s.Types[xdm.QName{URI: ns, Local: "shared"}] == nil {
			t.Errorf("the chameleon include produced no {%s}shared", ns)
		}
	}
}

// TestChameleonIncludeOfDocumentAlsoLoadedDirectly covers boeingData's ipo3,
// where the harness hands the loader ipo.xsd and the namespace-less itematt.xsd
// side by side, and ipo.xsd also includes itematt.xsd.
//
// The direct reading defines {}ItemDelivery and the included one defines
// {urn:ipo}ItemDelivery. ipo.xsd's attributeGroup ref names the latter, so
// suppressing the include because the file had already been read as a top-level
// document left that ref unresolvable.
func TestChameleonIncludeOfDocumentAlsoLoadedDirectly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	main := write("main.xsd", `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:ipo" targetNamespace="urn:ipo">
		  <xs:include schemaLocation="grp.xsd"/>
		  <xs:complexType name="CT">
		    <xs:attributeGroup ref="t:ItemDelivery"/>
		  </xs:complexType>
		</xs:schema>`)
	grp := write("grp.xsd", `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:attributeGroup name="ItemDelivery">
		    <xs:attribute name="partNum" type="xs:string"/>
		  </xs:attributeGroup>
		</xs:schema>`)

	s, err := LoadFiles([]string{main, grp}, Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatalf("LoadFiles: %v", err)
	}
	if s.AttributeGroups[xdm.QName{URI: "urn:ipo", Local: "ItemDelivery"}] == nil {
		t.Error("the chameleon-included attribute group did not adopt urn:ipo")
	}
	if s.AttributeGroups[xdm.QName{Local: "ItemDelivery"}] == nil {
		t.Error("the directly loaded document lost its absent-namespace group")
	}
}

// TestUnresolvedImportLeavesReferencesAbsent covers §5.3 Missing Sub-components.
//
// schemaLocation is a hint, so an import naming a document this processor will
// not fetch — an http:// URL with no network resolver — contributes nothing and
// is not itself an error. The references into that namespace are then
// unresolvable, and §5.3 is explicit that this leaves the property · absent ·
// and defers the consequence to validation, rather than failing assembly.
//
// The suite's own metadata schema, common/xsts.xsd, is exactly this: it imports
// XLink from a URL and then writes <xsd:attribute ref="xlink:type"/>. Treating
// that as a schema error rejected a schema every conforming processor loads,
// and took the whole introspection test set down with it.
func TestUnresolvedImportLeavesReferencesAbsent(t *testing.T) {
	const src = `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
	            xmlns:xlink="http://www.w3.org/1999/xlink">
	  <xsd:import namespace="http://www.w3.org/1999/xlink"
	              schemaLocation="http://www.w3.org/XML/2008/06/xlink.xsd"/>
	  <xsd:element name="e">
	    <xsd:complexType>
	      <xsd:attribute ref="xlink:href"/>
	    </xsd:complexType>
	  </xsd:element>
	</xsd:schema>`

	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(tree.Root, "s.xsd", Options{})
	if err != nil {
		t.Fatalf("a reference into an unfetched imported namespace should not "+
			"fail assembly:\n%v", err)
	}

	// The absent use must not act as a required attribute, and must not be
	// dereferenced while validating.
	doc, err := xdm.ParseString(`<e/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root, ValidateOptions{}); err != nil {
		t.Errorf("an element omitting the absent attribute should be valid:\n%v", err)
	}
}

// TestUnresolvedReferenceStillFails is the other half: the relaxation above is
// scoped to namespaces an import actually named and failed to fetch. A
// reference that simply names nothing must still be a schema error, or every
// misspelt ref would load silently.
func TestUnresolvedReferenceStillFails(t *testing.T) {
	const src = `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
	  <xsd:element name="e">
	    <xsd:complexType>
	      <xsd:attribute ref="xsd:noSuchAttribute"/>
	    </xsd:complexType>
	  </xsd:element>
	</xsd:schema>`

	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tree.Root, "s.xsd", Options{}); err == nil {
		t.Fatal("an attribute ref naming nothing should still be a schema error")
	}
}

// TestTypelessMemberTakesHeadType covers §3.3.2: an element declaration with
// neither a type attribute nor an inline type definition takes its
// substitution group head's {type definition}, falling back to xs:anyType only
// when it has no head at all.
//
// The defaulting and the binding of the affiliation are both deferred, and the
// affiliation is queued second, so doing the defaulting in the same pass always
// read a nil head and settled on anyType. elemT064 pins the consequence: <sa3>
// substitutes for a head typed A, and an anyType member made the substitution
// look blocked against a document that is valid.
//
// The chain is transitive — c's head b is itself typeless — because neither
// deferred step can be ordered before the other in general.
func TestTypelessMemberTakesHeadType(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">
		  <xs:element name="c" substitutionGroup="t:b"/>
		  <xs:element name="b" substitutionGroup="t:a"/>
		  <xs:element name="a" type="xs:int"/>
		  <xs:element name="lone"/>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		d := s.Elements[xdm.QName{URI: "urn:t", Local: name}]
		if d == nil {
			t.Fatalf("%s was not declared", name)
		}
		if got := d.Type.TypeName().Local; got != "int" {
			t.Errorf("element %q has type %q, want xs:int from its head",
				name, got)
		}
	}
	lone := s.Elements[xdm.QName{URI: "urn:t", Local: "lone"}]
	if got := lone.Type.TypeName().Local; got != "anyType" {
		t.Errorf("a declaration with no head has type %q, want anyType", got)
	}
}
