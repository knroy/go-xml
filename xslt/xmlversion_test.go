package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// runSheet compiles a stylesheet and serialises its result against <r/>.
func runSheet(t *testing.T, sheet string) (string, error) {
	t.Helper()
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse stylesheet: %v", err)
	}
	st, err := Compile(doc.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	src, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	res, err := st.Transform(context.Background(), src.Root, TransformOptions{})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// TestXMLVersionC0Serialization pins the two halves of XML 1.1 output: the
// declaration announces the version, and the C0 controls it admits are
// written as character references rather than as raw bytes.
//
// The raw spelling was the bug. Every arm of the escaper missed the range —
// CR, U+2028 and the C1 block were handled and #x1-#x1F fell through to
// WriteRune — so the output held control bytes no parser will read back at
// either version. That is not a conformance detail: the serialiser was
// producing documents it could not itself re-read.
func TestXMLVersionC0Serialization(t *testing.T) {
	const head = `<?xml version="1.1"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">`

	t.Run("text is written as a reference under 1.1", func(t *testing.T) {
		out, err := runSheet(t, head+`
<xsl:output method="xml" version="1.1"/>
<xsl:template match="/"><out><xsl:text>&#x1;&#x1f;</xsl:text></out></xsl:template>
</xsl:stylesheet>`)
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if !strings.Contains(out, "&#1;") || !strings.Contains(out, "&#31;") {
			t.Errorf("C0 controls not written as references: %q", out)
		}
		// The bug's signature: the raw byte in the output.
		if strings.ContainsRune(out, 0x1) {
			t.Errorf("a raw control byte reached the output: %q", out)
		}
	})

	t.Run("attributes too", func(t *testing.T) {
		out, err := runSheet(t, head+`
<xsl:output method="xml" version="1.1"/>
<xsl:template match="/"><out><a status="&#x8;&#x1f;"/></out></xsl:template>
</xsl:stylesheet>`)
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if !strings.Contains(out, "&#8;") || !strings.Contains(out, "&#31;") {
			t.Errorf("C0 controls in an attribute not written as references: %q", out)
		}
	})

	t.Run("the declaration announces 1.1", func(t *testing.T) {
		out, err := runSheet(t, head+`
<xsl:output method="xml" version="1.1"/>
<xsl:template match="/"><out/></xsl:template>
</xsl:stylesheet>`)
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if !strings.Contains(out, `version="1.1"`) {
			t.Errorf("declaration does not announce 1.1: %q", out)
		}
	})

	// The negative half. A C0 control has no XML 1.0 spelling at all — not
	// literally, because [2] Char excludes it, and not as a reference either,
	// because [66] CharRef is constrained to Char. Writing one is SERE0006,
	// and writing it raw (which is what happened) is worse than the error:
	// it produces a document that cannot be parsed.
	t.Run("under 1.0 it is SERE0006, not a raw byte", func(t *testing.T) {
		_, err := runSheet(t, head+`
<xsl:output method="xml" version="1.0"/>
<xsl:template match="/"><out><xsl:text>&#x7;</xsl:text></out></xsl:template>
</xsl:stylesheet>`)
		if err == nil {
			t.Fatal("a BEL was accepted as XML 1.0 output, want SERE0006")
		}
		if !strings.Contains(err.Error(), "SERE0006") {
			t.Errorf("error = %v, want SERE0006", err)
		}
	})

	// version defaults to 1.0, so the same text is refused with no @version.
	t.Run("1.0 is the default", func(t *testing.T) {
		out, err := runSheet(t, head+`
<xsl:output method="xml"/>
<xsl:template match="/"><out/></xsl:template>
</xsl:stylesheet>`)
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if !strings.Contains(out, `version="1.0"`) {
			t.Errorf("declaration = %q, want version=\"1.0\"", out)
		}
	})
}

// TestResultDocumentOutputVersion pins xsl:result-document/@output-version,
// which sets the same serialization parameter under a different name: §3.5
// renames it there because "version" on that element would collide with the
// xsl:version an XSLT element may carry. It was accepted by the attribute
// table and then read by nothing.
func TestResultDocumentOutputVersion(t *testing.T) {
	doc, err := xdm.ParseString(`<?xml version="1.1"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
<xsl:template match="/">
  <xsl:result-document method="xml" output-version="1.1">
    <out><xsl:text>&#x1;</xsl:text></out>
  </xsl:result-document>
</xsl:template>
</xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	st, err := Compile(doc.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	src, _ := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	res, err := st.Transform(context.Background(), src.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(res.Secondary) != 1 {
		t.Fatalf("secondary results = %d, want 1", len(res.Secondary))
	}
	var b strings.Builder
	if err := res.Secondary[0].Serialize(&b, nil); err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if !strings.Contains(b.String(), `version="1.1"`) {
		t.Errorf("output-version was not honoured: %q", b.String())
	}
	if !strings.Contains(b.String(), "&#1;") {
		t.Errorf("C0 control not written as a reference: %q", b.String())
	}
}

// TestLiteralResultInheritNamespaces pins xsl:inherit-namespaces on a literal
// result element. Section 11.1 gives an LRE the same property xsl:element and
// xsl:copy carry unprefixed, and it was read by neither the compiler nor the
// instruction: the children went on inheriting the parent's bindings, so the
// undeclaration XML 1.1 output is supposed to show was never owed and never
// written. xml-version-026/031/032/035/037/039/042 are the cases.
//
// The child in the negative arms is built by xsl:element rather than written
// literally, and that is deliberate. A literal result element copies every
// binding in scope on it in the stylesheet, so an LRE child always redeclares
// the prefix for itself and blockNamespaceInheritance passes it over --
// forcing noInherit on unconditionally changes nothing there, and a negative
// arm built that way would stay green under a fix that undeclared
// everywhere. xsl:element copies no stylesheet bindings, so its child holds
// only what it inherits, and the difference between yes and no is visible.
func TestLiteralResultInheritNamespaces(t *testing.T) {
	const head = `<?xml version="1.1"?>
<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
<xsl:output method="xml" version="1.1" undeclare-prefixes="yes"/>`

	// The positive half, spelled as the conformance cases spell it: the
	// stopped binding is written as the undeclaration XML 1.1 permits.
	t.Run("no blocks inheritance and undeclares", func(t *testing.T) {
		out, err := runSheet(t, head+`
<xsl:template match="/">
  <doc xmlns:a="http://a/" xsl:inherit-namespaces="no">
    <chap xsl:inherit-namespaces="no">
      <para/>
      <para xmlns:a=""/>
    </chap>
  </doc>
</xsl:template>
</xsl:stylesheet>`)
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if !strings.Contains(out, `xmlns:a=""`) {
			t.Errorf("no namespace undeclaration in the output: %q", out)
		}
	})

	// The same shape with an xsl:element child, which is the arm the two
	// negative ones are compared against.
	t.Run("no blocks a computed child too", func(t *testing.T) {
		out, err := runSheet(t, head+`
<xsl:template match="/">
  <doc xmlns:a="http://a/" xsl:inherit-namespaces="no">
    <xsl:element name="chap"><para xmlns:a=""/></xsl:element>
  </doc>
</xsl:template>
</xsl:stylesheet>`)
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if !strings.Contains(out, `<chap xmlns:a=""`) {
			t.Errorf("the computed child was not undeclared: %q", out)
		}
	})

	// The negative half, and the one that matters: undeclaring everywhere
	// would satisfy the arms above and be badly wrong. With the default the
	// binding is inherited, so there is nothing to undeclare and the output
	// must carry no empty declaration at all.
	t.Run("yes is the default and inherits", func(t *testing.T) {
		out, err := runSheet(t, head+`
<xsl:template match="/">
  <doc xmlns:a="http://a/">
    <xsl:element name="chap"><para xmlns:a=""/></xsl:element>
  </doc>
</xsl:template>
</xsl:stylesheet>`)
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if strings.Contains(out, `xmlns:a=""`) {
			t.Errorf("a prefix was undeclared although it is inherited: %q", out)
		}
		if !strings.Contains(out, `xmlns:a="http://a/"`) {
			t.Errorf("the binding was not declared at all: %q", out)
		}
	})

	// An explicit yes is the same as omitting it, and is what tells apart a
	// fix that reads the attribute's value from one that merely notices it
	// is present.
	t.Run("an explicit yes still inherits", func(t *testing.T) {
		out, err := runSheet(t, head+`
<xsl:template match="/">
  <doc xmlns:a="http://a/" xsl:inherit-namespaces="yes">
    <xsl:element name="chap"><para xmlns:a=""/></xsl:element>
  </doc>
</xsl:template>
</xsl:stylesheet>`)
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if strings.Contains(out, `xmlns:a=""`) {
			t.Errorf("inherit-namespaces=\"yes\" undeclared a prefix: %q", out)
		}
	})
}
