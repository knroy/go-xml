package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The pathological shape for identity constraints, kept as a fixed yardstick.
//
// A key with a ".//" selector declared on an element that is its own
// descendant is evaluated once per level, and each evaluation walks the whole
// remaining subtree, so the cost is depth times subtree size. docs/security.md
// records four attempts to remove that and why each was reverted; two of them
// looked like improvements until they were measured on both shapes at once.
// Deep-and-narrow and shallow-and-wide move in opposite directions under some
// of those changes, so both are benchmarked here.
//
// These are benchmarks rather than tests: they assert nothing, and exist so a
// future attempt can be compared against a recorded baseline instead of
// against an impression. Run with:
//
//	go test ./xsd/ -run XXX -bench BenchmarkIdentityConstraint -benchmem
func identityBenchSchema() string {
	return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:element name="r">
    <xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
      </xs:element>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:key>
  </xs:element>
</xs:schema>`
}

func identityBenchDoc(depth, width int) string {
	var b strings.Builder
	b.WriteString("<root>")
	for i := 0; i < depth; i++ {
		b.WriteString("<r>")
		for j := 0; j < width; j++ {
			fmt.Fprintf(&b, `<leaf id="d%di%d"/>`, i, j)
		}
	}
	for i := 0; i < depth; i++ {
		b.WriteString("</r>")
	}
	b.WriteString("</root>")
	return b.String()
}

func benchIdentity(b *testing.B, depth, width int) {
	b.Helper()
	st, err := xdm.ParseString(identityBenchSchema(), xdm.ParseOptions{})
	if err != nil {
		b.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		b.Fatalf("load schema: %v", err)
	}
	doc := identityBenchDoc(depth, width)
	tr, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		b.Fatalf("parse instance: %v", err)
	}
	b.SetBytes(int64(len(doc)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Validate(tr.Root, ValidateOptions{}); err != nil {
			b.Fatalf("validate: %v", err)
		}
	}
}

// Deep and narrow: the quadratic is in the depth.
func BenchmarkIdentityConstraintDeep240(b *testing.B) { benchIdentity(b, 240, 0) }
func BenchmarkIdentityConstraintDeep960(b *testing.B) { benchIdentity(b, 960, 0) }

// Shallow and wide: width is the factor MaxDepth does not bound, and the one
// that moved the wrong way under two of the reverted attempts.
func BenchmarkIdentityConstraintWide20(b *testing.B) { benchIdentity(b, 200, 20) }
func BenchmarkIdentityConstraintWide40(b *testing.B) { benchIdentity(b, 200, 40) }

// The pruning that made key and unique linear applies to buildNodeTable.
// checkKeyref still selects with the unpruned walk, so these measure whether
// that path carries the cost the other one shed. Same two shapes, so the
// numbers are directly comparable to the four above.
func identityKeyrefBenchSchema() string {
	return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:element name="r">
    <xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType>
          <xs:attribute name="id" type="xs:string"/>
          <xs:attribute name="ref" type="xs:string"/>
        </xs:complexType>
      </xs:element>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:key>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
</xs:schema>`
}

// Every leaf refers to its own id, so every reference resolves and the walk is
// never cut short by an early failure.
func identityKeyrefBenchDoc(depth, width int) string {
	var b strings.Builder
	b.WriteString("<root>")
	for i := 0; i < depth; i++ {
		b.WriteString("<r>")
		for j := 0; j < width; j++ {
			fmt.Fprintf(&b, `<leaf id="d%di%d" ref="d%di%d"/>`, i, j, i, j)
		}
	}
	for i := 0; i < depth; i++ {
		b.WriteString("</r>")
	}
	b.WriteString("</root>")
	return b.String()
}

func benchKeyref(b *testing.B, depth, width int) {
	b.Helper()
	st, err := xdm.ParseString(identityKeyrefBenchSchema(), xdm.ParseOptions{})
	if err != nil {
		b.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		b.Fatalf("load schema: %v", err)
	}
	doc := identityKeyrefBenchDoc(depth, width)
	tr, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		b.Fatalf("parse instance: %v", err)
	}
	b.SetBytes(int64(len(doc)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Validate(tr.Root, ValidateOptions{}); err != nil {
			b.Fatalf("validate: %v", err)
		}
	}
}

func BenchmarkIdentityKeyrefDeep240(b *testing.B) { benchKeyref(b, 240, 1) }
func BenchmarkIdentityKeyrefDeep480(b *testing.B) { benchKeyref(b, 480, 1) }
func BenchmarkIdentityKeyrefDeep960(b *testing.B) { benchKeyref(b, 960, 1) }
func BenchmarkIdentityKeyrefWide40(b *testing.B)  { benchKeyref(b, 200, 40) }
