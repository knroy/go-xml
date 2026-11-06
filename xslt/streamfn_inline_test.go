package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStreamInlinedCallsMatchTree: calls on stylesheet functions whose
// bodies are sequence constructors, planned as those bodies inside the
// caller's plan (streamfn_inline.go), against the tree on every core
// document and whitespace declaration. stream says the core must take the
// container.
func TestStreamInlinedCallsMatchTree(t *testing.T) {
	decls := `<xsl:mode name="m" streamable="yes"/>` +
		`<xsl:template match="rec" mode="m"><r k="{@k}"/></xsl:template>` +
		`<xsl:function name="p:wrap" as="element()" streamability="absorbing"><xsl:param name="n" as="node()*"/>` +
		`<w><xsl:copy-of select="$n"/></w></xsl:function>` +
		`<xsl:function name="p:app" streamability="absorbing"><xsl:param name="n" as="node()*"/>` +
		`<d><xsl:apply-templates select="$n" mode="m"/></d></xsl:function>` +
		`<xsl:function name="p:grp" streamability="absorbing"><xsl:param name="n" as="node()*"/><xsl:param name="x" as="xs:string"/>` +
		`<xsl:for-each-group select="$n" group-adjacent="string(@k)"><g k="{current-grouping-key()}" x="{$x}">` +
		`<xsl:value-of select="current-group()/@v"/></g></xsl:for-each-group></xsl:function>` +
		`<xsl:function name="p:self" streamability="absorbing"><xsl:param name="n" as="element()"/>` +
		`<s><xsl:value-of select="$n/sub/@x"/></s></xsl:function>` +
		// Refused: the body reads a focus it has not (XPDY0002 on the tree),
		// or the parameter where the body has a focus of its own.
		`<xsl:function name="p:focus"><xsl:param name="n" as="node()*"/>` +
		`<f><xsl:value-of select="name(.)"/></f><xsl:copy-of select="$n"/></xsl:function>` +
		`<xsl:function name="p:inner"><xsl:param name="n" as="node()*"/>` +
		`<xsl:for-each select="1 to 2"><xsl:copy-of select="$n"/></xsl:for-each></xsl:function>` +
		// Lane L2 round 4: recursion on the streamed current group (call
		// frames, streamfn_frame.go), su-absorbing-202's shape.
		`<xsl:function name="p:nest" streamability="absorbing"><xsl:param name="n" as="element()*"/><xsl:param name="d" as="xs:integer"/>` +
		`<xsl:for-each-group select="$n" group-starting-with="rec[@k = 'a']"><xsl:choose>` +
		`<xsl:when test="self::rec[@k = 'a']"><g k="{@k}" d="{$d}"><xsl:sequence select="p:nest(tail(current-group()), $d + 1)"/></g></xsl:when>` +
		`<xsl:otherwise><xsl:apply-templates select="p:nest(tail(current-group()), $d + 1)" mode="m"/></xsl:otherwise>` +
		`</xsl:choose></xsl:for-each-group></xsl:function>` +
		`<xsl:function name="p:nestn" streamability="absorbing"><xsl:param name="n" as="element(rec)*"/><xsl:param name="d" as="xs:integer"/>` +
		`<xsl:for-each-group select="$n" group-starting-with="rec[@k = 'a']"><xsl:choose>` +
		`<xsl:when test="self::rec[@k = 'a']"><g k="{@k}" d="{$d}"><xsl:sequence select="p:nestn(tail(current-group()), $d + 1)"/></g></xsl:when>` +
		`<xsl:otherwise><xsl:apply-templates select="p:nestn(tail(current-group()), $d + 1)" mode="m"/></xsl:otherwise>` +
		`</xsl:choose></xsl:for-each-group></xsl:function>`
	decls += `<xsl:function name="p:one" streamability="absorbing"><xsl:param name="n" as="element()"/>` +
		`<r k="{$n/@k}"><xsl:value-of select="$n/sub/@x"/></r></xsl:function>` +
		`<xsl:function name="p:chk" streamability="absorbing"><xsl:param name="n" as="node()"/>` +
		`<in x="{$n!node-name()}" y="{$n!@k}"/><xsl:text>&#10;</xsl:text><xsl:sequence select="$n!*!p:chk(.)"/></xsl:function>`
	// A grouping key reading the function's parameter, the group read by
	// another inlined body, whose own parameter has the key's name
	// (su-absorbing-301's and -202's shapes): the key sees its own frame.
	decls += `<xsl:function name="p:chunk" streamability="absorbing"><xsl:param name="n" as="node()*"/><xsl:param name="c" as="xs:integer"/>` +
		`<xsl:for-each-group select="$n" group-adjacent="(position() - 1) idiv $c"><xsl:sequence select="p:wrap(current-group())"/></xsl:for-each-group></xsl:function>` +
		`<xsl:function name="p:chunk2" streamability="absorbing"><xsl:param name="n" as="node()*"/><xsl:param name="c" as="xs:integer"/>` +
		`<xsl:for-each-group select="$n" group-adjacent="(position() - 1) idiv $c"><xsl:sequence select="p:wrapc(current-group(), 100)"/></xsl:for-each-group></xsl:function>` +
		`<xsl:function name="p:wrapc" streamability="absorbing"><xsl:param name="n" as="node()*"/><xsl:param name="c" as="xs:integer"/>` +
		`<w c="{$c}"><xsl:copy-of select="$n"/></w></xsl:function>`
	bodies := []struct {
		body   string
		stream bool
	}{
		{`<o><xsl:copy-of select="p:wrap(doc/rec)"/></o>`, true},
		{`<o><xsl:sequence select="p:wrap(doc/rec)"/></o>`, true},
		{`<o><xsl:sequence select="count(p:wrap(doc/rec)/*)"/></o>`, true},
		{`<o><xsl:copy-of select="p:app(doc/rec)"/></o>`, true},
		{`<o><xsl:sequence select="p:grp(doc/rec, 'y')"/></o>`, true},
		{`<xsl:for-each select="doc/rec"><o><xsl:sequence select="p:self(.)"/></o></xsl:for-each>`, true},
		// A call given only a variable's copies reads nothing of the stream.
		{`<xsl:variable name="c" select="copy-of(doc/rec)"/><o><xsl:sequence select="p:wrap($c)"/></o>`, true},
		{`<o><xsl:copy-of select="p:focus(doc/rec)"/></o>`, false},
		{`<o><xsl:copy-of select="p:inner(doc/rec)"/></o>`, false},
		{`<o><xsl:sequence select="p:nest(doc/rec, 1)"/></o>`, true},
		{`<o><xsl:sequence select="p:nestn(doc/rec, 1)"/></o>`, true},
		// Lane L2 round 4: a one-node parameter given a path (a loop with
		// the tree's cardinality: two recs or none decline to the tree),
		// and recursion over the stream ($n!*!p:chk(.), su-absorbing-204).
		{`<o><xsl:sequence select="p:one(doc/rec)"/></o>`, true},
		{`<o><xsl:sequence select="p:chk(*)"/></o>`, true},
		{`<o><xsl:copy-of select="doc ! p:chk(.)"/></o>`, true},
		{`<o><xsl:sequence select="p:chunk(doc/rec, 2)"/></o>`, true},
		{`<o><xsl:sequence select="p:chunk2(doc/rec, 1)"/></o>`, true},
	}
	n := 0
	for name, doc := range coreDocs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for di, d := range coreDecls {
			for bi, b := range bodies {
				if raceSkip(name, di, bi) {
					continue
				}
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

// TestStreamInlinedRecursionLimit: an inlined absorbing function recursing
// on the streamed group (su-absorbing-203's shape) is bounded as the tree's
// call is, by the XPath call depth (A4-F5): a nest at the limit succeeds and
// one past it raises the tree's XPDY0001, not the template depth's error.
func TestStreamInlinedRecursionLimit(t *testing.T) {
	sheet := mainSheet(`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>` +
		`<xsl:function name="p:group" as="element(Item)*" streamability="absorbing">` +
		`<xsl:param name="items" as="element(Item)*"/><xsl:param name="level" as="xs:integer"/>` +
		`<xsl:for-each-group select="$items" group-starting-with="Item[string-length(@Value) eq $level]"><xsl:choose>` +
		`<xsl:when test="self::Item[string-length(@Value) eq $level]"><xsl:copy><xsl:copy-of select="@*"/>` +
		`<xsl:sequence select="p:group(tail(current-group()), $level + 1)"/></xsl:copy></xsl:when>` +
		`<xsl:otherwise><xsl:apply-templates select="p:group(current-group(), $level + 1)"/></xsl:otherwise>` +
		`</xsl:choose></xsl:for-each-group></xsl:function>` +
		`<xsl:template match="Items"><xsl:copy><xsl:sequence select="p:group(Item, 2)"/></xsl:copy></xsl:template>`)
	for _, levels := range []int{49, 50, 51} {
		var d strings.Builder
		d.WriteString("<Items>")
		for i := 2; i <= levels; i++ {
			d.WriteString(`<Item Value="` + strings.Repeat("1", i) + `"/>`)
		}
		d.WriteString("</Items>")
		opts := TransformOptions{MaxDepth: 50}
		so, st := mainRun(sheet, d.String(), true, opts)
		to, _ := mainRun(sheet, d.String(), false, opts)
		if so != to || st.Streamed() == 0 {
			t.Errorf("%d levels, streamed %d:\nstream %.200s\ntree   %.200s", levels, st.Streamed(), so, to)
		}
		if failed := strings.Contains(to, "XPDY0001"); failed != (levels > 50) {
			t.Errorf("%d levels: tree %.200s", levels, to)
		}
	}
	// No Item matches a level: the recursion runs away.
	so, _ := mainRun(sheet, `<Items><Item Value="1"/></Items>`, true, TransformOptions{})
	if to, _ := mainRun(sheet, `<Items><Item Value="1"/></Items>`, false, TransformOptions{}); so != to || !strings.Contains(so, "XPDY0001") {
		t.Errorf("unbounded:\nstream %.200s\ntree   %.200s", so, to)
	}
}

// TestTemplateDepthThroughFunction: a template applied inside a stylesheet
// function continues its caller's template depth, so a recursion through
// the function meets MaxDepth at the depth a direct one does, with the same
// error, in both modes (R5). It used to restart at the function's runtime
// and stop one level later on the XPath call depth's XPDY0001.
func TestTemplateDepthThroughFunction(t *testing.T) {
	for _, body := range []string{
		`<xsl:template match="e"><x><xsl:apply-templates select="*"/></x></xsl:template>`,
		`<xsl:template match="e"><x><xsl:sequence select="p:a(.)"/></x></xsl:template>` +
			`<xsl:function name="p:a" streamability="absorbing"><xsl:param name="n" as="element()"/>` +
			`<xsl:apply-templates select="$n/*"/></xsl:function>`,
	} {
		sheet := mainSheet(`<xsl:mode streamable="yes"/>` + body)
		for _, n := range []int{49, 50} {
			doc := strings.Repeat("<e>", n) + strings.Repeat("</e>", n)
			opts := TransformOptions{MaxDepth: 50}
			so, st := mainRun(sheet, doc, true, opts)
			to, _ := mainRun(sheet, doc, false, opts)
			if so != to || st.Streamed() == 0 {
				t.Errorf("%d levels, streamed %d:\nstream %.200s\ntree   %.200s", n, st.Streamed(), so, to)
			}
			if failed := strings.Contains(to, "template recursion exceeded 50 levels"); failed != (n == 50) {
				t.Errorf("%d levels of %.60s: %.200s", n, body, to)
			}
		}
	}
}
