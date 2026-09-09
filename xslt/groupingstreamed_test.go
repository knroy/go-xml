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
// the group. The suite's si-fork-115 is the case this mirrors: it wraps a
// for-each-group in xsl:source-document streamable="yes", calls out to a
// template that asks for the key, and expects the catch clause to fire and
// write "#absent#".
//
// Both probes ask for the KEY rather than the group, and that is not
// incidental. §19.8.9.5 makes fn:current-grouping-key grounded and motionless
// wherever it is written, while §19.8.9.4 makes fn:current-group roaming once
// the group is out of reach -- so a streamable mode whose rule calls
// current-group() is a static XTSE3430, which is si-fork-116, asserted as an
// error by the suite and noted there as "it's a static error". si-fork-115 and
// -116 are otherwise the same stylesheet, and the pair is what fixes the line.
// Testing §14.4 with the group rather than the key would be testing a
// stylesheet the streamability rules refuse before §14.4 is reached.
func TestGroupingAbsentInsideDeclaredStreamable(t *testing.T) {
	dir := t.TempDir()
	doc := `<doc><i k="a">1</i><i k="a">2</i><i k="b">3</i></doc>`
	if err := os.WriteFile(filepath.Join(dir, "d.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	// body is the sequence constructor wrapped around the for-each-group, so
	// the two cases differ only in whether the invocation is written inside a
	// declared-streamable construct.
	//
	// Two details are what §19.8 requires of a grouping written inside a
	// streamable container, and si-fork-115 -- the suite case this mirrors --
	// carries both. Neither is incidental: §14.4 is about the group being
	// absent inside a construct that is genuinely streamable, so a body the
	// streamability rules refuse is not the case the suite is making.
	//
	//   - The xsl:for-each-group is a child of xsl:fork. §19.8.4.19's second
	//     clause is "if there is a group-by attribute and the instruction is
	//     not a child of xsl:fork, then roaming and free-ranging": grouping by
	//     an arbitrary key needs every group held at once, which only a fork
	//     makes possible.
	//   - The called template declares xsl:context-item use="absent".
	//     §19.8.4.9 gives xsl:call-template an implicit context-item operand
	//     unless the target prohibits one, with type-determined usage
	//     defaulting to item()*, which navigates -- and navigation from a
	//     streamed posture is free-ranging. Its note says so outright:
	//     "Calling xsl:call-template will usually make stylesheet code
	//     unstreamable if a streamed node is passed explicitly or implicitly
	//     to the called template". probe reads no context item, so declaring
	//     it absent states what the template already meant. (The working draft
	//     spells the value "prohibited" and the Recommendation "absent";
	//     contextItemAbsent accepts either, the element grammar only "absent".)
	const grouping = `<xsl:fork><xsl:for-each-group select="/doc/i" group-by="@k">
			<g k="{current-grouping-key()}">
				<xsl:call-template name="probe"/>
				<xsl:apply-templates select="current-group()[1]" mode="m"/>
			</g>
		</xsl:for-each-group></xsl:fork>`

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
			`<out><g k="a"><c>a</c><a>a</a></g>` +
				`<g k="b"><c>b</c><a>b</a></g></out>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
				<xsl:output omit-xml-declaration="yes"/>
				<xsl:mode name="m" streamable="yes"/>
				<xsl:template name="xsl:initial-template"><out>` + tc.body + `</out></xsl:template>
				<xsl:template name="probe">
					<xsl:context-item use="absent"/>
					<xsl:try><c><xsl:value-of select="current-grouping-key()"/></c>
						<xsl:catch errors="*:XTDE1071"><c>#absent#</c></xsl:catch></xsl:try>
				</xsl:template>
				<xsl:template match="*" mode="m">
					<xsl:try><a><xsl:value-of select="current-grouping-key()"/></a>
						<xsl:catch errors="*:XTDE1071"><a>#absent#</a></xsl:catch></xsl:try>
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
