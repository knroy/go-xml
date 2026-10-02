package xmltok

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// benchInputs are a large real document and two generated shapes: one that
// is mostly attributes and one that is mostly text. The generated ones are
// built here so that they do not depend on a checked-in file's line endings.
func benchInputs(b *testing.B) map[string][]byte {
	var attrs, text strings.Builder
	attrs.WriteString("<root>\n")
	text.WriteString("<root>\n")
	for i := range 5000 {
		fmt.Fprintf(&attrs, `  <item id="i%d" name="name %d" type="xs:string" minOccurs="0" maxOccurs="unbounded" ref="p:q%d"/>`+"\n", i, i, i)
		fmt.Fprintf(&text, "  <p>Paragraph %d has a sentence or two of ordinary prose &amp; one reference, "+
			"and it runs on for a while so that the text dominates the markup around it.</p>\n", i)
	}
	attrs.WriteString("</root>\n")
	text.WriteString("</root>\n")
	in := map[string][]byte{"attrs": []byte(attrs.String()), "text": []byte(text.String())}
	if src, err := os.ReadFile("../../testdata/xsdtests/msMeta/DataTypes_w3c.xml"); err == nil {
		in["DataTypes_w3c"] = src
	}
	return in
}

func BenchmarkRawToken(b *testing.B) {
	for name, src := range benchInputs(b) {
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for b.Loop() {
				d := NewDecoder(bytes.NewReader(src))
				for {
					if _, err := d.RawToken(); err != nil {
						if err != io.EOF {
							b.Fatal(err)
						}
						break
					}
				}
			}
		})
	}
}
