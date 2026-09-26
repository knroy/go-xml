package c14nsuite

import (
	"context"
	"testing"

	"github.com/knroy/go-xml/c14n"
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// TestCanonicalizeXSLTResultTree checks that a tree an XSLT transform BUILT
// canonicalizes exactly as its serialization does once reparsed. Canonical
// XML reads namespace declarations off the tree, and a constructed element
// gets them from XSLT's namespace fixup rather than from markup; if fixup
// left one out, the canonical form of a transform's result would differ from
// the canonical form of the same result written out and read back.
func TestCanonicalizeXSLTResultTree(t *testing.T) {
	bodies := map[string]string{
		"literal result elements": `<p:x xmlns:p="urn:p"><p:y a="1"/><z/></p:x>`,
		"xsl:element":             `<xsl:element name="q:z" namespace="urn:q"><xsl:element name="w" namespace="urn:w"/></xsl:element>`,
		"xsl:attribute":           `<e><xsl:attribute name="r:a" namespace="urn:r">v</xsl:attribute><xsl:attribute name="b" namespace="urn:b">v</xsl:attribute></e>`,
		"unused declaration":      `<o xmlns:unused="urn:unused"><i/></o>`,
		"default undeclared":      `<d xmlns="urn:d"><e xmlns=""/></d>`,
	}
	in, err := xdm.ParseString("<in/>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			st, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
				`<xsl:template match="/">`+body+`</xsl:template></xsl:stylesheet>`, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			ss, err := xslt.Compile(st.Root, xslt.CompileOptions{})
			if err != nil {
				t.Fatal(err)
			}
			res, err := ss.Transform(context.Background(), in.Root, xslt.TransformOptions{})
			if err != nil {
				t.Fatal(err)
			}
			re, err := xdm.ParseString(res.String(), xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, alg := range []c14n.Algorithm{c14n.Inclusive10, c14n.Exclusive10, c14n.Inclusive11} {
				built, err := c14n.Bytes(res.Tree(), c14n.Options{Algorithm: alg})
				if err != nil {
					t.Fatal(err)
				}
				reparsed, err := c14n.Bytes(re.Root, c14n.Options{Algorithm: alg})
				if err != nil {
					t.Fatal(err)
				}
				if string(built) != string(reparsed) {
					t.Errorf("%s:\n   built: %s\nreparsed: %s", alg, built, reparsed)
				}
			}
		})
	}
}
