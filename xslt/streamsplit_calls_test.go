package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamSheetCallsMatchTree: calls on stylesheet functions the inliner
// does not take (recursive, called in a predicate, given values, given a
// grounded copy of a match) against the tree, on every core document and
// whitespace declaration. stream says the core must take the container.
func TestStreamSheetCallsMatchTree(t *testing.T) {
	decls := `<xsl:function name="p:cnt" streamability="absorbing" as="xs:integer"><xsl:param name="n" as="node()"/>` +
		`<xsl:sequence select="count($n/descendant::*)"/></xsl:function>` +
		`<xsl:function name="p:size" streamability="absorbing" as="xs:integer"><xsl:param name="n" as="node()"/>` +
		`<xsl:sequence select="1 + sum($n/* ! p:size(.))"/></xsl:function>` +
		`<xsl:function name="p:join" as="xs:string"><xsl:param name="s" as="xs:string*"/>` +
		`<xsl:sequence select="if (exists($s)) then head($s) || '-' || p:join(tail($s)) else '.'"/></xsl:function>` +
		`<xsl:function name="p:n"><xsl:sequence select="2"/></xsl:function>` +
		`<xsl:function name="p:filt" streamability="filter" as="node()?"><xsl:param name="n" as="node()"/><xsl:param name="k" as="xs:string"/>` +
		`<xsl:sequence select="$n[@k = $k]"/></xsl:function>` +
		`<xsl:function name="p:cnts" streamability="absorbing" as="xs:integer"><xsl:param name="n" as="node()*"/>` +
		`<xsl:sequence select="count($n/descendant::*)"/></xsl:function>` +
		`<xsl:function name="p:up" streamability="absorbing" as="xs:string"><xsl:param name="n" as="node()"/>` +
		`<xsl:sequence select="string($n/..)"/></xsl:function>` +
		`<xsl:function name="p:self" streamability="filter" as="node()"><xsl:param name="n" as="node()"/>` +
		`<xsl:sequence select="$n"/></xsl:function>` +
		// Lane L2: bodies that climb (streamfn_climb.go).
		`<xsl:function name="p:depth" streamability="inspection" as="xs:integer"><xsl:param name="n" as="node()"/>` +
		`<xsl:sequence select="if ($n/.. instance of document-node()) then 1 else p:depth($n/..) + 1"/></xsl:function>` +
		`<xsl:function name="p:inh" streamability="inspection" as="xs:string?"><xsl:param name="n" as="node()?"/>` +
		`<xsl:sequence select="if (empty($n)) then () else if (exists($n/@k)) then string($n/@k) else p:inh($n/..)"/></xsl:function>` +
		`<xsl:function name="p:top" streamability="ascent" as="element()"><xsl:param name="n" as="node()"/>` +
		`<xsl:sequence select="if ($n/ancestor::rec) then p:top($n/ancestor::rec[1]) else $n"/></xsl:function>` +
		`<xsl:function name="p:par" streamability="filter" as="node()?"><xsl:param name="n" as="node()"/>` +
		`<xsl:sequence select="$n[parent::rec]"/></xsl:function>` +
		`<xsl:function name="p:big" as="xs:boolean"><xsl:param name="a" as="item()"/>` +
		`<xsl:sequence select="number($a) gt 1"/></xsl:function>` +
		`<xsl:function name="p:two" as="xs:integer"><xsl:param name="one" as="node()?"/><xsl:param name="two" as="xs:string*"/>` +
		`<xsl:sequence select="count($two) + count($one)"/></xsl:function>`
	bodies := []struct {
		body   string
		stream bool
	}{
		{`<v><xsl:value-of select="doc/rec ! p:cnt(.)"/></v>`, true},
		{`<v><xsl:value-of select="doc/rec ! p:size(.)"/></v>`, true},
		{`<v><xsl:value-of select="p:join(doc/rec/@k)"/></v>`, true},
		{`<v><xsl:value-of select="p:join(//sub/string(@x))"/></v>`, true},
		{`<o><xsl:copy-of select="doc/rec/sub[position() ne p:n()]"/></o>`, false},
		{`<o><xsl:value-of select="doc/rec ! p:filt(., 'a')/@v"/></o>`, true},
		{`<v><xsl:value-of select="doc/rec ! p:cnt(copy-of(sub))"/></v>`, true},
		// Copies collected for the call: the memory rule decides.
		{`<xsl:for-each select="doc/rec"><c n="{p:cnts(sub)}"/></xsl:for-each>`, false},
		{`<v><xsl:value-of select="p:cnts(doc/rec)"/></v>`, false},
		// Refused: the body reads an ancestor's content; nested matches'
		// copies would be distinct nodes where the tree's are one.
		{`<v><xsl:value-of select="doc/rec ! p:up(.)"/></v>`, false},
		{`<v><xsl:value-of select="count(//rec ! p:self(.))"/></v>`, false},
		{`<v><xsl:value-of select="p:cnts(//rec)"/></v>`, false},
		// Lane L2: climbing bodies on each match's copy, whose ancestors
		// are shallow copies: names, attributes, existence only.
		{`<v><xsl:value-of select="doc/rec ! p:depth(.)"/></v>`, true},
		{`<v><xsl:value-of select="//sub ! p:depth(.)"/></v>`, true},
		{`<v><xsl:value-of select="//sub ! p:inh(.)"/></v>`, true},
		{`<v><xsl:value-of select="//sub ! p:top(.) ! @k"/></v>`, true},
		{`<v><xsl:value-of select="//sub ! p:par(.)/@x"/></v>`, true},
		// Refused: an ancestor's copy kept, or its string value read.
		{`<v><xsl:value-of select="//sub ! p:top(.)"/></v>`, false},
		{`<v><xsl:value-of select="count(//sub ! p:top(.) ! @k)"/></v>`, false},
		{`<o><xsl:copy-of select="//sub ! p:top(.) ! @k"/></o>`, false},
		// Lane L2: function items in a burst expression, and outermost or
		// innermost of copies (streamfn_refs.go).
		{`<v><xsl:value-of select="doc/rec ! filter((@k, @v), p:big#1)"/></v>`, true},
		{`<v><xsl:value-of select="doc/rec ! filter(tokenize(string(.), ' '), p:big#1)"/></v>`, true},
		{`<v><xsl:value-of select="doc/rec ! for-each(sub, string#1)"/></v>`, true},
		{`<o><xsl:copy-of select="outermost(copy-of(doc/rec/sub))"/></o>`, true},
		{`<o><xsl:copy-of select="innermost(copy-of(doc/rec))"/></o>`, true},
		{`<v><xsl:value-of select="count(outermost(copy-of(//rec)))"/></v>`, false},
		// Lane L2: an atomic parameter's argument read as values, whatever
		// the other parameters are (su-shallow-descent-101's shape).
		{`<v><xsl:value-of select="p:two((), outermost(//rec))"/></v>`, true},
		// Lane L2 round 5: a copy of the document element the stylesheet asks
		// for, as an argument (su-shallow-descent-003's shape).
		{`<v><xsl:value-of select="p:cnt(copy-of(doc))"/></v>`, true},
	}
	n := 0
	for name, doc := range coreDocs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for di, d := range coreDecls {
			for bi, b := range bodies {
				sheet := coreSheetWith(d+decls, b.body)
				so, se, st := coreRun(sheet, dir, true, TransformOptions{})
				to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
				if so != to || se != te {
					t.Errorf("%s decl %d body %d:\ncore %q %q\ntree %q %q", name, di, bi, so, se, to, te)
				}
				if st.Guards != 0 {
					t.Errorf("%s decl %d body %d: %d guard hits", name, di, bi, st.Guards)
				}
				if st.Reruns != 0 && te == "" {
					t.Errorf("%s decl %d body %d: re-run where the tree succeeds", name, di, bi)
				}
				if b.stream && te == "" && name != "empty" && name != "recroot" && st.Runs[0] == 0 {
					t.Errorf("%s decl %d body %d: not streamed", name, di, bi)
				}
				n += int(st.Runs[0])
			}
		}
	}
	if n == 0 {
		t.Fatal("nothing streamed")
	}
}
