package dtd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// DTD validation benchmarks.
//
// Content models go through the same Glushkov automaton the XSD validator
// uses, so these figures should track the XSD ones for equivalent shapes. A
// divergence would mean the DTD layer is doing work of its own that it should
// not be.

const benchDTD = `<!DOCTYPE root [
<!ELEMENT root (item)*>
<!ELEMENT item (name, value?, note*)>
<!ELEMENT name (#PCDATA)>
<!ELEMENT value (#PCDATA)>
<!ELEMENT note (#PCDATA)>
<!ATTLIST item id ID #REQUIRED kind (a|b|c) "a">
]>`

func benchTree(b *testing.B, n int) *xdm.Node {
	b.Helper()
	var sb strings.Builder
	sb.WriteString(benchDTD)
	sb.WriteString(`<root>`)
	for i := 0; i < n; i++ {
		sb.WriteString(`<item id="i`)
		sb.WriteString(strings.Repeat("0", 4))
		sb.WriteString(itoa(i))
		sb.WriteString(`"><name>a</name><value>1</value><note>x</note></item>`)
	}
	sb.WriteString(`</root>`)
	tree, err := xdm.ParseString(sb.String(), xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		b.Fatal(err)
	}
	return tree.Root
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func BenchmarkValidate(b *testing.B) {
	root := benchTree(b, 500)
	d, err := Parse(benchDTD)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Validate(root, d, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// Parsing the DTD itself: a caller does this once per document, so it is on
// the hot path for a service validating many small documents against their
// own internal subsets.
func BenchmarkParseDTD(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(benchDTD); err != nil {
			b.Fatal(err)
		}
	}
}
