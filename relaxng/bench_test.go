package relaxng

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Benchmarks for the derivative algorithm.
//
// These are self-contained rather than corpus-driven: RELAX NG has no
// production corpus here the way XSLT does, and the shapes that matter are
// known — interleave, deep recursion and a long sequence are where a
// derivative implementation goes quadratic if the simplifying constructors
// are not doing their job.
//
// Allocation counts are the number to watch. The derivative rebuilds patterns
// as it goes, so a regression in the constructors shows up as allocations per
// input item long before it shows up as wall-clock.

func benchSchema(b *testing.B, src string) *Schema {
	b.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	s, err := Compile(tree.Root)
	if err != nil {
		b.Fatal(err)
	}
	return s
}

func benchDoc(b *testing.B, src string) *xdm.Node {
	b.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	return tree.Root
}

const benchNS = ` xmlns="http://relaxng.org/ns/structure/1.0"`

// A long sequence of elements: the ordinary case, and the one that must stay
// linear.
func BenchmarkValidateSequence(b *testing.B) {
	s := benchSchema(b, `<element`+benchNS+` name="root">
		<zeroOrMore><element name="item"><text/></element></zeroOrMore>
	</element>`)
	var sb strings.Builder
	sb.WriteString("<root>")
	for i := 0; i < 1000; i++ {
		sb.WriteString("<item>x</item>")
	}
	sb.WriteString("</root>")
	doc := benchDoc(b, sb.String())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Validate(doc); err != nil {
			b.Fatal(err)
		}
	}
}

// Interleave is the construct that makes RELAX NG more expressive than an XSD
// all group, and the one where a naive implementation blows up: each item may
// go to either branch, so the pattern doubles unless the constructors
// simplify it back down.
func BenchmarkValidateInterleave(b *testing.B) {
	s := benchSchema(b, `<element`+benchNS+` name="root">
		<interleave>
			<zeroOrMore><element name="a"><empty/></element></zeroOrMore>
			<zeroOrMore><element name="b"><empty/></element></zeroOrMore>
			<zeroOrMore><element name="c"><empty/></element></zeroOrMore>
		</interleave>
	</element>`)
	var sb strings.Builder
	sb.WriteString("<root>")
	for i := 0; i < 200; i++ {
		sb.WriteString("<a/><c/><b/>")
	}
	sb.WriteString("</root>")
	doc := benchDoc(b, sb.String())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Validate(doc); err != nil {
			b.Fatal(err)
		}
	}
}

// A recursive definition, expanded lazily. Each level of nesting costs one
// expansion, and the Ref cache is what keeps it from costing more.
func BenchmarkValidateRecursive(b *testing.B) {
	s := benchSchema(b, `<grammar`+benchNS+`>
		<start><element name="root"><ref name="n"/></element></start>
		<define name="n">
			<element name="n"><optional><ref name="n"/></optional></element>
		</define></grammar>`)
	var sb strings.Builder
	sb.WriteString("<root>")
	const depth = 200
	for i := 0; i < depth; i++ {
		sb.WriteString("<n>")
	}
	for i := 0; i < depth; i++ {
		sb.WriteString("</n>")
	}
	sb.WriteString("</root>")
	doc := benchDoc(b, sb.String())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Validate(doc); err != nil {
			b.Fatal(err)
		}
	}
}

// Attributes go through a separate derivative, and a wide element exercises
// it: every attribute is matched against every alternative the pattern offers.
func BenchmarkValidateAttributes(b *testing.B) {
	var schema strings.Builder
	schema.WriteString(`<element` + benchNS + ` name="root">`)
	var doc strings.Builder
	doc.WriteString("<root")
	for i := 0; i < 30; i++ {
		name := string(rune('a'+i%26)) + string(rune('0'+i/26))
		schema.WriteString(`<attribute name="` + name + `"><text/></attribute>`)
		doc.WriteString(` ` + name + `="v"`)
	}
	schema.WriteString(`<empty/></element>`)
	doc.WriteString("/>")

	s := benchSchema(b, schema.String())
	d := benchDoc(b, doc.String())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Validate(d); err != nil {
			b.Fatal(err)
		}
	}
}

// Compilation runs the whole restriction pass, which is most of the package's
// code. A caller compiles once and validates many times, so this matters less
// than the validate figures — but it is where a schema-loading service spends
// its startup.
func BenchmarkCompile(b *testing.B) {
	src := `<grammar` + benchNS + `>
		<start><element name="root"><ref name="body"/></element></start>
		<define name="body">
			<interleave>
				<zeroOrMore><element name="a"><text/></element></zeroOrMore>
				<optional><element name="b"><ref name="body"/></element></optional>
			</interleave>
		</define></grammar>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(tree.Root); err != nil {
			b.Fatal(err)
		}
	}
}
