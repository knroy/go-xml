package xslt

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestCopyNoNamespacesFixup pins which prefixes the namespace axis reports on
// an element produced by xsl:copy with copy-namespaces="no".
//
// The answer is not "none": §11.9.1 drops the source element's namespace
// nodes, but §5.8.3 namespace fixup still requires a namespace node for every
// prefix the copy's own name -- and the names of the attributes its body
// writes -- resolve through. The distinction the test has to make is that
// fixup adds those and ONLY those: a copy of <gml:description> out of a
// document that also declares a default namespace and xmlns:bldg must see gml
// and xml, and must NOT see bldg or the default.
//
// The bug this pins was one-sided in the other direction. xsl:copy never ran
// fixup at all, so the copy carried no namespace node for its own gml prefix.
// It was invisible in the serialised output -- the serialiser writes the
// declaration a name needs whether the tree says so or not -- and only the
// namespace axis could see it. si-copy-020 and si-copy-026 ask for exactly
// this list.
func TestCopyNoNamespacesFixup(t *testing.T) {
	const src = `<CityModel xmlns="http://ns/city" xmlns:bldg="http://ns/bldg" ` +
		`xmlns:gml="http://ns/gml"><gml:description a="1">hi</gml:description></CityModel>`

	cases := []struct {
		name  string
		body  string
		want  []string
		never []string
	}{{
		name: "copy of a prefixed element keeps only the prefix its name needs",
		body: `<xsl:for-each select="/*/*:description">` +
			`<xsl:copy copy-namespaces="no"><xsl:value-of select="."/></xsl:copy>` +
			`</xsl:for-each>`,
		want:  []string{"gml", "xml"},
		never: []string{"bldg", ""},
	}, {
		// si-copy-026: the same copy over a grounded, already-copied node.
		name: "copy of a grounded copy answers the same way",
		body: `<xsl:for-each select="copy-of(/*/*:description)">` +
			`<xsl:copy copy-namespaces="no"><xsl:value-of select="."/></xsl:copy>` +
			`</xsl:for-each>`,
		want:  []string{"gml", "xml"},
		never: []string{"bldg", ""},
	}, {
		// An attribute name written by the body needs its prefix too, and it
		// is the body -- not the source -- that brings the prefix in.
		name: "an attribute the body writes brings its own prefix back",
		body: `<xsl:for-each select="/*/*:description">` +
			`<xsl:copy copy-namespaces="no">` +
			`<xsl:attribute name="p:att" namespace="http://ns/p">v</xsl:attribute>` +
			`</xsl:copy></xsl:for-each>`,
		want:  []string{"gml", "p", "xml"},
		never: []string{"bldg", ""},
	}, {
		// The negative arm's counterpart: copy-namespaces="yes" is what
		// carries everything over, and the fixup must not have quietly
		// become the only source of declarations.
		name: "copy-namespaces=yes still carries every binding in scope",
		body: `<xsl:for-each select="/*/*:description">` +
			`<xsl:copy><xsl:value-of select="."/></xsl:copy></xsl:for-each>`,
		want: []string{"", "bldg", "gml", "xml"},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sheet := `<xsl:stylesheet version="3.0" ` +
				`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
				`<xsl:template match="/"><out>` + tc.body +
				`</out></xsl:template></xsl:stylesheet>`
			got := inScopeOfCopy(t, sheet, src)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("in-scope prefixes = %q, want %q", got, tc.want)
			}
			for _, p := range tc.never {
				for _, g := range got {
					if g == p {
						t.Errorf("prefix %q must not be in scope on the copy, got %q", p, got)
					}
				}
			}
		})
	}
}

// inScopeOfCopy runs the stylesheet over src and returns the sorted prefixes
// the namespace axis reports on /out/*.
func inScopeOfCopy(t *testing.T, sheet, src string) []string {
	t.Helper()
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse stylesheet: %v", err)
	}
	st, err := Compile(doc.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	in, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	res, err := st.Transform(context.Background(), in.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var copied *xdm.Node
	var walk func(*xdm.Node)
	walk = func(n *xdm.Node) {
		if copied != nil {
			return
		}
		if n.Kind == xdm.KindElement && n.Name.Local == "out" {
			for _, c := range n.Children {
				if c.Kind == xdm.KindElement {
					copied = c
					return
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(res.Tree())
	if copied == nil {
		t.Fatal("the stylesheet produced no /out/* element")
	}
	scope := copied.InScopeNamespaces()
	prefixes := make([]string, 0, len(scope))
	for p := range scope {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	return prefixes
}
