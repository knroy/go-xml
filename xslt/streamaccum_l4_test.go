package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// accumReadDecls are accumulators whose rules read accumulators of their
// node (streamaccum_l4.go); stream says TransformReader must stream them.
var accumReadDecls = []struct {
	decls  string
	stream bool
}{
	// A text rule reading another's before (accumulator-068, si-fork-816).
	{`<xsl:accumulator name="k" as="xs:string?" initial-value="()" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="string(@k)"/></xsl:accumulator>
	 <xsl:accumulator name="m" as="map(xs:string, xs:string)" initial-value="map{}" streamable="yes" xmlns:map="http://www.w3.org/2005/xpath-functions/map">
	   <xsl:accumulator-rule match="rec/text()" select="map:put($value, accumulator-before('k'), string())"/></xsl:accumulator>`, true},
	// End rules reading others' after, a chain declared backwards
	// (accumulator-079s), and a let (accumulator-069s).
	{`<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" phase="end" select="accumulator-after('b') + 1"/></xsl:accumulator>
	 <xsl:accumulator name="b" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" phase="end" select="let $c := accumulator-after('c') return $value + $c"/></xsl:accumulator>
	 <xsl:accumulator name="c" as="xs:integer*" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="0"/>
	   <xsl:accumulator-rule match="rec/text()" select="string-length(.)"/></xsl:accumulator>`, true},
	// An end rule reading its own before, and another's before.
	{`<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="$value + 1"/>
	   <xsl:accumulator-rule match="rec" phase="end" select="accumulator-before('a') * 10 + accumulator-before('b')"/></xsl:accumulator>
	 <xsl:accumulator name="b" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="*" select="$value + 1"/></xsl:accumulator>`, true},
	// Its own after in an end rule: XTDE3400 in both (accumulator-080s).
	{`<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" phase="end" select="accumulator-after('a') + 1"/></xsl:accumulator>
	 <xsl:accumulator name="b" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" phase="end" select="accumulator-after('a')"/></xsl:accumulator>`, true},
	// A read of an accumulator that fails later in the document: the
	// tree's walk of a fails at that read, with b's error.
	{`<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="accumulator-before('b') + xs:integer(if (@k = 'c') then 'x' else '1')"/></xsl:accumulator>
	 <xsl:accumulator name="b" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="$value + xs:integer(if (@v = '2') then 'y' else '1')"/></xsl:accumulator>`, true},
	// A rule body of a variable and an xsl:sequence (evaluate-046).
	{`<xsl:accumulator name="a" as="xs:string*" initial-value="()" streamable="yes">
	   <xsl:accumulator-rule match="rec" phase="end">
	     <xsl:variable name="e" as="item()*"><xsl:evaluate xpath="'string(' || @v || ')'" with-params="map{}"/></xsl:variable>
	     <xsl:sequence select="($value, $e, string(count(accumulator-before('a'))))"/>
	   </xsl:accumulator-rule></xsl:accumulator>`, true},
	// Refused: two accumulators reading each other; a read beside the
	// node; -after in a start rule; a variable holding the node.
	{`<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" phase="end" select="accumulator-before('b')"/></xsl:accumulator>
	 <xsl:accumulator name="b" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" phase="end" select="accumulator-before('a')"/></xsl:accumulator>`, false},
	{`<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="../accumulator-before('b')"/></xsl:accumulator>
	 <xsl:accumulator name="b" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="*" select="$value + 1"/></xsl:accumulator>`, false},
	{`<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="accumulator-after('b')"/></xsl:accumulator>
	 <xsl:accumulator name="b" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="*" select="$value + 1"/></xsl:accumulator>`, false},
	{`<xsl:accumulator name="a" initial-value="()" streamable="yes">
	   <xsl:accumulator-rule match="rec"><xsl:variable name="n" select="."/><xsl:sequence select="$n"/></xsl:accumulator-rule></xsl:accumulator>`, false},
}

// TestStreamAccumReadsMatchTree: accumulator rules reading accumulators of
// their node give, through TransformReader, Parse + Transform's result or
// error, and stream where they are admitted.
func TestStreamAccumReadsMatchTree(t *testing.T) {
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}}
	var why string
	traceMain = func(m, w string) {
		if m == "" {
			why = w
		}
	}
	defer func() { traceMain = nil }()
	const rules = `<xsl:mode streamable="yes" use-accumulators="#all" on-no-match="shallow-skip"/>
	  <xsl:template match="rec"><r b="{accumulator-before('a')}"><xsl:apply-templates/>
	    <xsl:value-of select="accumulator-after('a')"/></r></xsl:template>
	  <xsl:template match="/"><out><xsl:apply-templates/><t n="{accumulator-after('a')}"/></out></xsl:template>`
	for name, doc := range mainDocs {
		for ci, c := range accumReadDecls {
			decls := c.decls
			if ci == 0 {
				decls += `<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
				  <xsl:accumulator-rule match="rec" phase="end" select="map:size(accumulator-after('m'))" xmlns:map="http://www.w3.org/2005/xpath-functions/map"/></xsl:accumulator>`
			}
			sheet := mainSheet(decls + rules)
			why = ""
			so, st := mainRun(sheet, doc, true, opts)
			w := why
			to, _ := mainRun(sheet, doc, false, opts)
			where := fmt.Sprintf("doc %s, accumulators %d", name, ci)
			if so != to {
				t.Errorf("%s:\nstreamed %s\ntree     %s", where, so, to)
			}
			if st.Guards != 0 {
				t.Errorf("%s: %d guard hits", where, st.Guards)
			}
			m := st.Runs[streamtrace.Main]
			if c.stream && m == 0 {
				t.Errorf("%s: not streamed: %s", where, w)
			}
			if !c.stream && m != 0 {
				t.Errorf("%s: streamed", where)
			}
		}
	}
}

// TestStreamTryAccumFailsAtStart: an xsl:try reading an accumulator
// streams through TransformReader and catches what the tree catches: a read
// fails, in both, exactly when the value depends on a failed step
// (§18.2.5; accumulator-056s, -057s).
func TestStreamTryAccumFailsAtStart(t *testing.T) {
	cases := []struct {
		initial, rule string
		stream        bool
	}{
		{"0", "$value + 1", true},
		{"100 div 0", "$value + 1", true},
		{"0", "($value - 2) + 1", true},
		{"0", "$value idiv (xs:integer(@v) - 2)", true},
		{"0", "string-length(@k)", true},
	}
	for name, doc := range mainDocs {
		for ci, c := range cases {
			sheet := mainSheet(`<xsl:accumulator name="n" as="xs:integer" initial-value="` + c.initial + `" streamable="yes">
			    <xsl:accumulator-rule match="rec" select="` + c.rule + `"/></xsl:accumulator>
			  <xsl:mode streamable="yes" use-accumulators="#all" on-no-match="shallow-skip"/>
			  <xsl:template match="rec"><xsl:try><r n="{accumulator-before('n')}"/>
			    <xsl:catch><c/></xsl:catch></xsl:try></xsl:template>`)
			so, st := mainRun(sheet, doc, true, TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}})
			to, _ := mainRun(sheet, doc, false, TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}})
			where := fmt.Sprintf("doc %s, case %d", name, ci)
			if so != to {
				t.Errorf("%s:\nstreamed %s\ntree     %s", where, so, to)
			}
			if m := st.Runs[streamtrace.Main]; c.stream != (m != 0) {
				t.Errorf("%s: %d main runs, want streamed %v", where, m, c.stream)
			}
		}
	}
}

// TestAccumDynamicCallStreamedInput: accumulator-after#1 made at the global
// context item saves an absent focus when the input is supplied to
// TransformReader (§10.3.6, accumulator-061): XTDE3350, streamed or on its
// tree fallback; over a tree given to Transform it keeps its focus.
func TestAccumDynamicCallStreamedInput(t *testing.T) {
	const decls = `<xsl:param name="aa" select="accumulator-after#1"/>
	  <xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
	    <xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>
	  <xsl:mode streamable="yes" use-accumulators="#all" on-no-match="shallow-skip"/>`
	for _, body := range []string{
		// Refused by the planner (two consumers): the tree fallback.
		`<xsl:template match="rec"><r b="{accumulator-before('n')}"><xsl:apply-templates/></r><e><xsl:value-of select="../$aa('n')"/></e></xsl:template>`,
		`<xsl:template match="/"><o><xsl:value-of select="$aa('n')"/></o></xsl:template>`,
	} {
		sheet := mainSheet(decls + body)
		doc := mainDocs["flat"]
		so, _ := mainRun(sheet, doc, true, TransformOptions{})
		to, _ := mainRun(sheet, doc, false, TransformOptions{})
		if !strings.Contains(so, "XTDE3350") {
			t.Errorf("%s: TransformReader gave %s, want XTDE3350", body, so)
		}
		if strings.Contains(to, "XTDE3350") {
			t.Errorf("%s: Transform of a tree gave %s, want a value", body, to)
		}
	}
}

// TestStreamCopyAccumulatorsNo: copy-accumulators="no" is the default the
// plan already takes (accumulator-049s): it is no reason to refuse, and the
// output is the tree's.
func TestStreamCopyAccumulatorsNo(t *testing.T) {
	var why string
	traceMain = func(m, w string) {
		if m == "" {
			why = w
		}
	}
	defer func() { traceMain = nil }()
	for _, v := range []string{"no", "yes"} {
		sheet := mainSheet(`<xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
		    <xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>
		  <xsl:mode streamable="yes" use-accumulators="#all" on-no-match="shallow-skip"/>
		  <xsl:template match="rec"><r n="{accumulator-before('n')}"/></xsl:template>
		  <xsl:template match="/"><out><xsl:variable name="c" as="element()*"><xsl:copy-of select="doc/rec" copy-accumulators="` + v + `"/></xsl:variable>
		    <xsl:apply-templates select="$c"/></out></xsl:template>`)
		why = ""
		so, _ := mainRun(sheet, mainDocs["flat"], true, TransformOptions{})
		w := why
		to, _ := mainRun(sheet, mainDocs["flat"], false, TransformOptions{})
		if so != to {
			t.Errorf("copy-accumulators=%s:\nstreamed %s\ntree     %s", v, so, to)
		}
		if refused := strings.Contains(w, "copy-accumulators"); refused != (v == "yes") {
			t.Errorf("copy-accumulators=%s: refusal %q", v, w)
		}
	}
}
