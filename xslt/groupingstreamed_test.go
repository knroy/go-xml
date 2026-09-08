package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// An invocation construct inside a declared-streamable construct sets the
// current group and the current grouping key to absent, so current-group()
// and current-grouping-key() in the template it enters are XTDE1061 and
// XTDE1071 rather than the enclosing xsl:for-each-group's values.
//
// Section 14.4 makes the rule turn on where the invocation is written, not on
// what it reaches, so an identical call outside the xsl:source-document keeps
// the group. The suite's si-fork-113, -114 and -115 are the cases: each wraps
// a for-each-group in xsl:source-document streamable="yes", calls out to a
// template that asks for the key or the group, and expects the catch clause
// to fire and write "#absent#".
func TestGroupingAbsentInsideDeclaredStreamable(t *testing.T) {
	dir := t.TempDir()
	doc := `<doc><i k="a">1</i><i k="a">2</i><i k="b">3</i></doc>`
	if err := os.WriteFile(filepath.Join(dir, "d.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	// body is the sequence constructor wrapped around the for-each-group, so
	// the two cases differ only in whether the invocation is written inside a
	// declared-streamable construct.
	const grouping = `<xsl:for-each-group select="/doc/i" group-by="@k">
			<g k="{current-grouping-key()}">
				<xsl:call-template name="probe"/>
				<xsl:apply-templates select="current-group()[1]" mode="m"/>
			</g>
		</xsl:for-each-group>`

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		// Inside: both invocations lose the group.
		{"streamable source-document", `<xsl:source-document streamable="yes" href="d.xml">` +
			grouping + `</xsl:source-document>`,
			`<out><g k="a"><c>#absent#</c><a>#absent#</a></g>` +
				`<g k="b"><c>#absent#</c><a>#absent#</a></g></out>`},
		// streamable="no" is not a declared-streamable construct, so the
		// scope of the group stays dynamic and the called template sees it.
		{"non-streamable source-document", `<xsl:source-document streamable="no" href="d.xml">` +
			grouping + `</xsl:source-document>`,
			`<out><g k="a"><c>a</c><a>2</a></g>` +
				`<g k="b"><c>b</c><a>1</a></g></out>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
				<xsl:output omit-xml-declaration="yes"/>
				<xsl:template name="xsl:initial-template"><out>` + tc.body + `</out></xsl:template>
				<xsl:template name="probe">
					<xsl:try><c><xsl:value-of select="current-grouping-key()"/></c>
						<xsl:catch errors="*:XTDE1071"><c>#absent#</c></xsl:catch></xsl:try>
				</xsl:template>
				<xsl:template match="*" mode="m">
					<xsl:try><a><xsl:value-of select="count(current-group())"/></a>
						<xsl:catch errors="*:XTDE1061"><a>#absent#</a></xsl:catch></xsl:try>
				</xsl:template>
			</xsl:transform>`
			path := filepath.Join(dir, "s.xsl")
			r, err := NewFileResolver(dir)
			if err != nil {
				t.Fatal(err)
			}
			stree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: path})
			if err != nil {
				t.Fatal(err)
			}
			s, err := Compile(stree.Root, CompileOptions{Resolver: r, BaseURI: path})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			res, err := s.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "initial-template",
					InitialTemplateURI: xdm.NSXSL, Documents: r})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			got := collapseTagWhitespace(res.String())
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// collapseTagWhitespace removes the indentation the sequence constructor
// copies into the result, leaving the markup on one line. Whitespace inside a
// start tag is kept so that "<g k=" does not become "<gk=".
func collapseTagWhitespace(s string) string {
	var b strings.Builder
	inTag := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '<':
			inTag = true
		case '>':
			inTag = false
		}
		if !inTag && (c == '\n' || c == '\t' || c == ' ' || c == '\r') {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
