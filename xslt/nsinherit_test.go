package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// nsSheet wraps a template body in a stylesheet that binds p and q, so every
// literal result element in it carries those two bindings.
func nsSheet(version, body string) string {
	return `<?xml version="` + version + `"?><xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform" ` +
		`xmlns:p="urn:p" xmlns:q="urn:q">` +
		`<xsl:template match="/">` + body + `</xsl:template></xsl:stylesheet>`
}

func runNSSheet(t *testing.T, sheet string) *Result {
	t.Helper()
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse stylesheet: %v", err)
	}
	st, err := Compile(doc.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	in, err := xdm.ParseString(`<a xmlns:s="urn:s"><b/></a>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	res, err := st.Transform(context.Background(), in.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	return res
}

// TestConstructedElementsInheritBindings pins that a constructed element no
// longer carries a namespace node for a binding its parent already has.
//
// A literal result element copies every binding in scope in the stylesheet,
// and xsl:copy every binding in scope on the source, so a result tree built
// from them repeated the same declarations on every element: 85.7% of the
// nodes of a Schematron SVRL report were such copies. The namespace axis and
// LookupPrefix walk the ancestors, so leaving them to the parent changes
// nothing observable; TestNamespaceInheritanceUnchanged checks that.
func TestConstructedElementsInheritBindings(t *testing.T) {
	res := runNSSheet(t, nsSheet("1.0",
		`<xsl:variable name="v"><out><in/></out></xsl:variable>`+
			`<out><lre/><xsl:element name="p:el"/>`+
			`<xsl:for-each select="/a/b"><xsl:copy/></xsl:for-each>`+
			`<xsl:sequence select="$v/out/in"/></out>`))
	out := res.Tree().FirstElement("", "out")
	if out == nil {
		t.Fatal("no <out> element in the result")
	}
	if len(out.Namespaces) != 2 {
		t.Errorf("<out> carries %d namespace nodes, want its own 2", len(out.Namespaces))
	}
	for _, c := range out.Children {
		var got []string
		for _, ns := range c.Namespaces {
			got = append(got, ns.Name.Local+"="+ns.Value)
		}
		want := ""
		switch c.Name.Local {
		case "b":
			// The copy's s binding is the one its parent does not have.
			want = "s=urn:s"
		case "el":
			// xsl:element keeps the binding its own name needs.
			want = "p=urn:p"
		}
		if strings.Join(got, " ") != want {
			t.Errorf("<%s> carries namespace nodes %q, want %q",
				c.Name.Lexical(), got, want)
		}
	}
}

// TestNamespaceInheritanceUnchanged runs the cases where leaving a binding to
// the parent could change what the result says, each written so that the
// namespace axis and the serialised form are both in the output. The
// expected strings are what c233f4e, which copied every binding, produced.
func TestNamespaceInheritanceUnchanged(t *testing.T) {
	probe := func(sel string) string {
		return `<xsl:value-of select="string-join(for $e in ` + sel +
			` return string-join(sort(in-scope-prefixes($e)), ','), '|')"/>`
	}
	cases := []struct {
		name, xmlVersion, body, want string
	}{{
		name: "nested literal result elements",
		body: `<xsl:variable name="v"><out><in><deep/></in></out></xsl:variable>` +
			`<r><xsl:sequence select="$v"/>` + probe(`$v//*`) + `</r>`,
		want: `<r xmlns:p="urn:p" xmlns:q="urn:q"><out><in><deep/></in></out>` +
			`p,q,xml|p,q,xml|p,q,xml</r>`,
	}, {
		// The child is a literal result element with p in scope in the
		// stylesheet, so it has p as a namespace node of its own and keeps
		// it; only bindings it merely inherits are blocked.
		name: "inherit-namespaces=no on a literal result element",
		body: `<xsl:variable name="v"><out xsl:inherit-namespaces="no">` +
			`<in/><xsl:element name="e"><deep/></xsl:element></out></xsl:variable>` +
			`<r>` + probe(`$v//*`) + `</r>`,
		want: `<r xmlns:p="urn:p" xmlns:q="urn:q">p,q,xml|p,q,xml|xml|p,q,xml</r>`,
	}, {
		name: "inherit-namespaces=no on xsl:element and xsl:copy",
		body: `<xsl:variable name="v"><xsl:element name="p:x" inherit-namespaces="no">` +
			`<in/></xsl:element><xsl:for-each select="/a"><xsl:copy ` +
			`inherit-namespaces="no"><in/></xsl:copy></xsl:for-each></xsl:variable>` +
			`<r>` + probe(`$v//*`) + `</r>`,
		want: `<r xmlns:p="urn:p" xmlns:q="urn:q">p,xml|p,q,xml|s,xml|p,q,xml</r>`,
	}, {
		// <q:a> is excluded from carrying urn:w, so namespace fixup binds q
		// to it only when the element is finished, after its child was
		// built under the outer binding of q. The child must keep its own.
		name: "fixup of the parent's name after the child is built",
		body: `<xsl:variable name="v"><g xmlns:q="urn:z">` +
			`<q:a xmlns:q="urn:w" xsl:exclude-result-prefixes="q">` +
			`<c xmlns:q="urn:z"/></q:a></g></xsl:variable>` +
			`<r><xsl:sequence select="$v"/><xsl:value-of select="` +
			`namespace-uri-for-prefix('q', $v//c)"/></r>`,
		want: `<r xmlns:p="urn:p" xmlns:q="urn:q"><g xmlns:q="urn:z">` +
			`<q:a xmlns:q="urn:w"><c xmlns:q="urn:z"/></q:a></g>urn:z</r>`,
	}, {
		// The rename rebinds the element's own namespace node in place, so
		// the declarations come out in the order they were made.
		name: "xsl:namespace rebinding the prefix of xsl:element",
		body: `<out><xsl:element name="p:e" namespace="urn:p">` +
			`<xsl:namespace name="z" select="'urn:z'"/>` +
			`<xsl:namespace name="p" select="'urn:x'"/></xsl:element></out>`,
		want: `<out xmlns:p="urn:p" xmlns:q="urn:q"><p_0:e xmlns:p="urn:x" xmlns:z="urn:z" xmlns:p_0="urn:p"/></out>`,
	}, {
		name: "copy-namespaces=no",
		body: `<xsl:variable name="v"><out><in/></out></xsl:variable>` +
			`<r><xsl:copy-of select="$v/out/in" copy-namespaces="no"/>` +
			`<xsl:variable name="c"><xsl:copy-of select="$v/out/in" ` +
			`copy-namespaces="no"/></xsl:variable>` + probe(`$c/in`) + `</r>`,
		want: `<r xmlns:p="urn:p" xmlns:q="urn:q"><in/>xml</r>`,
	}, {
		name: "xsl:copy-of and xsl:copy of an inner element",
		body: `<xsl:variable name="v"><out><in><deep/></in></out></xsl:variable>` +
			`<r xmlns:p="urn:other"><xsl:copy-of select="$v/out/in"/>` +
			`<xsl:for-each select="$v/out/in"><xsl:copy/></xsl:for-each></r>`,
		want: `<r xmlns:p="urn:other" xmlns:q="urn:q"><in xmlns:p="urn:p"><deep/></in>` +
			`<in xmlns:p="urn:p"/></r>`,
	}, {
		name: "xsl:sequence of an inner element into new content",
		body: `<xsl:variable name="v"><out><in><deep/></in></out></xsl:variable>` +
			`<r xmlns:p="urn:other"><xsl:sequence select="$v/out/in"/>` +
			`<xsl:variable name="w" as="element()*"><xsl:sequence ` +
			`select="$v/out/in"/></xsl:variable>` + probe(`$w`) + `</r>`,
		want: `<r xmlns:p="urn:other" xmlns:q="urn:q"><in xmlns:p="urn:p"><deep/></in>` +
			`p,q,xml</r>`,
	}, {
		// xsl:perform-sort hands its nodes to the builder, which copies
		// them itself.
		name: "xsl:perform-sort of an inner element into new content",
		body: `<xsl:variable name="v"><out><in><deep/></in></out></xsl:variable>` +
			`<r xmlns:p="urn:other"><xsl:perform-sort select="$v/out/in">` +
			`<xsl:sort select="name()"/></xsl:perform-sort></r>`,
		want: `<r xmlns:p="urn:other" xmlns:q="urn:q"><in xmlns:p="urn:p"><deep/></in></r>`,
	}, {
		name: "an inner element serialised without its ancestors",
		body: `<xsl:variable name="v"><out><in><deep/></in></out></xsl:variable>` +
			`<xsl:sequence select="$v/out/in"/>`,
		want: `<in xmlns:p="urn:p" xmlns:q="urn:q"><deep/></in>`,
	}, {
		name: "default namespace undeclaration",
		body: `<out xmlns="urn:d"><in xmlns=""><deep/></in>` +
			`<xsl:variable name="v"><x xmlns="urn:d"><y xmlns=""/></x></xsl:variable>` +
			`<xsl:sequence select="$v/*/*"/></out>`,
		want: `<out xmlns="urn:d" xmlns:p="urn:p" xmlns:q="urn:q">` +
			`<in xmlns=""><deep/></in><y xmlns=""/></out>`,
	}, {
		// XML 1.1 undeclares p on the stylesheet element, so <in> has no p
		// binding of its own; it inherits <out>'s in the result, as before.
		name:       "XML 1.1 prefix undeclaration",
		xmlVersion: "1.1",
		body: `<xsl:variable name="v"><out><in xmlns:p=""/></out></xsl:variable>` +
			`<r>` + probe(`$v//*`) + `</r>`,
		want: `<r xmlns:p="urn:p" xmlns:q="urn:q">p,q,xml|p,q,xml</r>`,
	}, {
		name: "namespace nodes on a parentless temporary tree",
		body: `<xsl:variable name="v" as="element()"><out a="1"><in b="2">` +
			`<deep c="3"/></in></out></xsl:variable>` +
			`<xsl:variable name="all" select="$v/descendant-or-self::*/(.|@*|namespace::*)"/>` +
			`<r><xsl:value-of select="count($all), count(distinct-values($all ! generate-id()))"/></r>`,
		want: `<r xmlns:p="urn:p" xmlns:q="urn:q">15 15</r>`,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.xmlVersion
			if v == "" {
				v = "1.0"
			}
			res := runNSSheet(t, nsSheet(v, tc.body))
			var sb strings.Builder
			if err := res.Serialize(&sb); err != nil {
				t.Fatalf("serialize: %v", err)
			}
			got := sb.String()
			if i := strings.Index(got, "?>"); strings.HasPrefix(got, "<?xml") && i > 0 {
				got = got[i+2:]
			}
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestScopeBindingsMatchesInScopeNamespaces pins scopeBindings, which the
// copies read in place of building InScopeNamespaces' map, to the map's
// answer: inner declarations shadow outer ones, the last of two declarations
// on one element wins, an undeclaration removes the prefix, and xml is left
// out.
func TestScopeBindingsMatchesInScopeNamespaces(t *testing.T) {
	doc, err := xdm.ParseString(`<?xml version="1.1"?>`+
		`<a xmlns="urn:d" xmlns:p="urn:p" xmlns:q="urn:q">`+
		`<b xmlns:p="urn:p2" xmlns:q=""><c xmlns=""/></b></a>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := doc.Root.FirstElement("", "c")
	b := c.Parent
	// Two declarations of one prefix on one element, as a constructed tree
	// can hold: the later one is in force.
	b.AddNamespace("r", "urn:r1")
	b.AddNamespace("r", "urn:r2")
	for _, n := range []*xdm.Node{b.Parent, b, c} {
		var got []string
		for _, nb := range scopeBindings(n, nil) {
			got = append(got, nb.prefix+"="+nb.uri)
		}
		scope := n.InScopeNamespaces()
		var want []string
		for _, p := range []string{"", "p", "q", "r"} {
			if u, ok := scope[p]; ok {
				want = append(want, p+"="+u)
			}
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("<%s>: scopeBindings = %q, InScopeNamespaces = %q",
				n.Name.Local, got, want)
		}
	}
}
