package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// accumDecls are accumulator declarations the accumulator cases run under:
// counts and sums of elements, start and end rules nested, text and
// comment rules, a rule at the document node, an error at a record, a
// growing sequence.
var accumDecls = []string{
	`<xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>
	 <xsl:accumulator name="v" as="xs:double" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec[@v castable as xs:double]" select="$value + @v"/></xsl:accumulator>`,
	`<xsl:accumulator name="n" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="*" select="$value + 1"/>
	   <xsl:accumulator-rule match="*" phase="end" select="$value - 1"/></xsl:accumulator>
	 <xsl:accumulator name="v" as="xs:string*" initial-value="()" streamable="yes">
	   <xsl:accumulator-rule match="rec" phase="start" select="($value, string(@k))"/>
	   <xsl:accumulator-rule match="rec" phase="end" select="tail($value)"/></xsl:accumulator>`,
	`<xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="text()" select="$value + string-length(.)"/>
	   <xsl:accumulator-rule match="comment() | processing-instruction()" select="$value + 100"/></xsl:accumulator>
	 <xsl:accumulator name="v" as="xs:string" initial-value="''" streamable="yes">
	   <xsl:accumulator-rule match="rec/text()" select="$value || ."/>
	   <xsl:accumulator-rule match="/" phase="end" select="'end:' || $value"/></xsl:accumulator>`,
	`<xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
	   <xsl:accumulator-rule match="rec" select="$value + 10 idiv (2 - xs:integer(@v))"/></xsl:accumulator>
	 <xsl:accumulator name="v" as="map(xs:string, xs:integer)" initial-value="map{}" streamable="yes" xmlns:map="http://www.w3.org/2005/xpath-functions/map">
	   <xsl:accumulator-rule match="rec">
	     <xsl:sequence select="map:put($value, string(@k), ($value(string(@k)), 0)[1] + 1)"/>
	   </xsl:accumulator-rule></xsl:accumulator>`,
}

// accumBodies are container bodies reading the accumulators; stream says
// the core must take the container.
var accumBodies = []struct {
	body   string
	stream bool
}{
	{`<xsl:for-each select="*/rec"><r n="{accumulator-before('n')}" v="{accumulator-before('v')}"/></xsl:for-each>`, true},
	{`<xsl:for-each select="//rec"><r><xsl:value-of select="accumulator-before('n')"/></r></xsl:for-each>`, true},
	{`<xsl:for-each select="*/rec"><r><xsl:copy-of select="sub"/><xsl:value-of select="accumulator-after('n'), accumulator-after('v')"/></r></xsl:for-each>`, true},
	{`<xsl:for-each select="*/rec"><r a="{accumulator-after('n')}"/></xsl:for-each>`, true},
	{`<o n="{count(//rec)}"/><xsl:value-of select="accumulator-after('n')"/>`, true},
	{`<xsl:value-of select="accumulator-before('n')"/>`, true},
	{`<xsl:value-of select="serialize(accumulator-after('v'), map{'method':'adaptive'})"/>`, true},
	{`<xsl:apply-templates select="*" mode="a"/>`, true},
	{`<xsl:apply-templates mode="a"/><xsl:value-of select="accumulator-after('n')"/>`, true},
	// A streamed source-document inside, done before the rule reads the
	// outer document's accumulators: its observer leaves the outer's be.
	{`<xsl:source-document streamable="yes" href="doc.xml"><i><xsl:value-of select="count(//rec)"/></i></xsl:source-document><xsl:apply-templates select="*" mode="a"/>`, true},
	// A group's body has read past its first member, its focus.
	{`<xsl:for-each-group select="*/rec" group-adjacent="@k"><g n="{count(current-group())}" b="{accumulator-before('n')}"/></xsl:for-each-group>`, false},
	{`<xsl:for-each-group select="*/rec" group-adjacent="@k"><g b="{accumulator-before('n')}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group>`, false},
	{`<xsl:for-each-group select="*/rec" group-adjacent="@k"><g><xsl:value-of select="count(current-group())"/><xsl:value-of select="accumulator-before('n')"/></g></xsl:for-each-group>`, false},
	// In an xsl:try: a read raises an accumulator's error in both modes when
	// the value read depends on the failed step (§18.2.5).
	{`<xsl:try><xsl:for-each select="*/rec"><r n="{accumulator-before('n')}"/></xsl:for-each><xsl:catch><c/></xsl:catch></xsl:try>`, false},
	{`<xsl:try><xsl:value-of select="count(//rec)"/><xsl:catch><c n="{accumulator-before('n')}"/></xsl:catch></xsl:try>`, false},
	// Below an element's start: the after-value needs it read first.
	{`<xsl:for-each select="*/rec"><xsl:value-of select="accumulator-after('n')"/><xsl:value-of select="count(sub)"/></xsl:for-each>`, false},
}

const accumRules = `
<xsl:mode name="a" streamable="yes" on-no-match="shallow-skip"/>
<xsl:template match="rec" mode="a"><r b="{accumulator-before('n')}"><xsl:apply-templates mode="a"/>
  <xsl:value-of select="accumulator-after('n'), accumulator-after('v')"/></r></xsl:template>
<xsl:template match="sub" mode="a"><s b="{accumulator-before('n')}"/></xsl:template>`

// TestStreamAccumMatchesTree: streamed containers reading accumulators give
// the tree's output or error, and the cases that must stream do.
func TestStreamAccumMatchesTree(t *testing.T) {
	var why string
	traceCore = func(_ *xdm.Node, w string) { why = w }
	defer func() { traceCore = nil }()
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for ai, acc := range accumDecls {
			for _, ws := range coreDecls {
				for _, b := range accumBodies {
					sheet := coreSheetWith(ws+acc+accumRules, b.body)
					why = ""
					so, se, st := coreRun(sheet, dir, true, TransformOptions{})
					w := why
					to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
					where := fmt.Sprintf("doc %s, accumulators %d, decls %q, body %s", name, ai, ws, b.body)
					if so != to || se != te {
						t.Errorf("%s:\nstreamed %s %s\ntree     %s %s", where, so, se, to, te)
					}
					if st.Guards != 0 {
						t.Errorf("%s: %d guard hits", where, st.Guards)
					}
					runs := st.Runs[streamtrace.Core]
					// The analysis verifies neither accumulator v of sets 2 and 3
					// (a rule at the document node, a sequence constructor).
					trusted := ai < 2 || !strings.Contains(b.body+accumRules, "'v'")
					if b.stream && trusted && runs == 0 {
						t.Errorf("%s: not streamed: %s", where, w)
					}
					// A try reading an accumulator may stream (§18.2.5): its
					// output is still the tree's.
					tryAt0 := strings.HasPrefix(b.body, "<xsl:try>")
					if !b.stream && !tryAt0 && runs != 0 {
						t.Errorf("%s: streamed", where)
					}
					if st.Reruns != 0 && name != "bad" && ai != 3 {
						t.Errorf("%s: %d re-runs", where, st.Reruns)
					}
					streamed += int(runs)
				}
			}
		}
	}
	t.Logf("%d streamed runs", streamed)
}

// accumMainRules are stylesheets a principal input is streamed through,
// reading the accumulators.
var accumMainRules = []struct {
	decls  string
	stream bool
}{
	{`<xsl:mode streamable="yes" use-accumulators="#all"/>` + strings.ReplaceAll(accumRules, ` mode="a"`, ``) +
		`<xsl:template match="/"><out><xsl:apply-templates/><t n="{accumulator-after('n')}"/></out></xsl:template>`, true},
	{`<xsl:mode streamable="yes" use-accumulators="n"/>
	  <xsl:template match="rec"><r b="{accumulator-before('n')}"/></xsl:template>`, true},
	// v is not applicable: XTDE3362 in both.
	{`<xsl:mode streamable="yes" use-accumulators="n"/>
	  <xsl:template match="rec"><r b="{accumulator-before('v')}"/></xsl:template>`, true},
	// No use-accumulators: none applicable.
	{`<xsl:mode streamable="yes"/>
	  <xsl:template match="rec"><r b="{accumulator-before('n')}"/></xsl:template>`, true},
	{`<xsl:mode streamable="yes" use-accumulators="#all"/>
	  <xsl:template match="/"><xsl:value-of select="accumulator-after('n')"/></xsl:template>`, true},
}

// TestStreamAccumMainMatchesTree: TransformReader with accumulators gives
// Parse + Transform's result or error.
func TestStreamAccumMainMatchesTree(t *testing.T) {
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}}
	var why string
	traceMain = func(m, w string) {
		if m == "" {
			why = w
		}
	}
	defer func() { traceMain = nil }()
	streamed := 0
	for name, doc := range mainDocs {
		for ai, acc := range accumDecls {
			for _, c := range accumMainRules {
				sheet := mainSheet(acc + c.decls)
				why = ""
				so, st := mainRun(sheet, doc, true, opts)
				w := why
				to, _ := mainRun(sheet, doc, false, opts)
				where := fmt.Sprintf("doc %s, accumulators %d, rules %s", name, ai, c.decls)
				if so != to {
					t.Errorf("%s:\nstreamed %s\ntree     %s", where, so, to)
				}
				if st.Guards != 0 {
					t.Errorf("%s: %d guard hits", where, st.Guards)
				}
				m := st.Runs[streamtrace.Main]
				if c.stream && (ai < 2 || !strings.Contains(c.decls, "'v'")) && m == 0 {
					t.Errorf("%s: not streamed: %s", where, w)
				}
				streamed += int(m)
			}
		}
	}
	t.Logf("%d streamed runs", streamed)
}

// TestStreamAccumErrors: an accumulator's error is the tree's error, raised
// as the tree raises it -- by a read whose value depends on the failed
// computation (§18.2.5), after a parse error -- through TransformReader,
// which has no tree to re-run on, and through a source-document container,
// which has.
func TestStreamAccumErrors(t *testing.T) {
	const accs = `
	<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
	  <xsl:accumulator-rule match="r" select="$value + 10 idiv (2 - xs:integer(@v))"/></xsl:accumulator>
	<xsl:accumulator name="b" as="xs:integer" initial-value="0" streamable="yes">
	  <xsl:accumulator-rule match="r" select="$value + xs:integer(if (@v = '3') then 'b3' else '1')"/></xsl:accumulator>
	<xsl:accumulator name="c" as="xs:integer" initial-value="xs:integer('x')" streamable="yes">
	  <xsl:accumulator-rule match="r" select="1"/></xsl:accumulator>`
	docs := []string{
		`<d><r v="1"/><r v="1"/><x/><y/><r v="2"/><r v="1"/></d>`,
		`<d><r v="1"/><x/><r v="3"/><y/><r v="2"/></d>`,
		`<d><r v="1"/><y/><r v="1"/></d>`,
		`<d><r v="1"/><y/><r v="2"/><r v="1"></d>`,
		`<d><y/><r v="3"/><x/><r v="2"/></d>`,
	}
	rules := []string{
		`<xsl:template match="r"><r a="{accumulator-before('a')}"/></xsl:template>`,
		`<xsl:template match="r"><r b="{accumulator-before('b')}"/></xsl:template>`,
		`<xsl:template match="r"/><xsl:template match="x"><x a="{xs:date(name())}"/></xsl:template>
		 <xsl:template match="y"><y a="{accumulator-before('a')}"/></xsl:template>`,
		`<xsl:template match="r"><r a="{accumulator-before('b')}"/></xsl:template>
		 <xsl:template match="y"><y a="{accumulator-before('a')}"/></xsl:template>`,
		`<xsl:template match="r"><r a="{accumulator-before('a')}"/></xsl:template>
		 <xsl:template match="y"><y b="{accumulator-before('b')}"/></xsl:template>`,
		`<xsl:template match="y"><y c="{accumulator-before('c')}"/></xsl:template>`,
		`<xsl:template match="r"/><xsl:template match="y"><y/></xsl:template>`,
		`<xsl:template match="/"><o><xsl:apply-templates/><xsl:value-of select="accumulator-after('b')"/></o></xsl:template>
		 <xsl:template match="r"/>`,
	}
	var why string
	traceMain = func(m, w string) {
		if m == "" {
			why = w
		}
	}
	defer func() { traceMain = nil }()
	dir := t.TempDir()
	for di, doc := range docs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for ri, r := range rules {
			decls := accs + `<xsl:mode streamable="yes" use-accumulators="#all" on-no-match="shallow-skip"/>` + r
			sheet := mainSheet(decls)
			so, st := mainRun(sheet, doc, true, TransformOptions{})
			to, _ := mainRun(sheet, doc, false, TransformOptions{})
			if so != to {
				t.Errorf("doc %d, rules %d, TransformReader:\nstreamed %s\ntree     %s", di, ri, so, to)
			}
			if st.Runs[streamtrace.Main] != 1 || st.Guards != 0 {
				t.Errorf("doc %d, rules %d: streamed runs %d, guard hits %d: %s", di, ri, st.Runs[streamtrace.Main], st.Guards, why)
			}
			// The same through a container: the rules in the unnamed mode.
			sheet = coreSheetWith(decls, `<xsl:apply-templates/>`)
			co, ce, ct := coreRun(sheet, dir, true, TransformOptions{})
			cto, cte, _ := coreRun(sheet, dir, false, TransformOptions{})
			if co != cto || ce != cte {
				t.Errorf("doc %d, rules %d, container:\nstreamed %s %s\ntree     %s %s", di, ri, co, ce, cto, cte)
			}
			if ct.Runs[streamtrace.Core] != 1 || ct.Guards != 0 {
				t.Errorf("doc %d, rules %d: container streamed runs %d, guard hits %d", di, ri, ct.Runs[streamtrace.Core], ct.Guards)
			}
		}
	}
}

// TestStreamAccumItemLimit: the items an accumulator's values hold are
// counted as the tree's walk counts them, so a value growing and shrinking
// over a document is refused at the same limit (MaxItems) in both modes.
func TestStreamAccumItemLimit(t *testing.T) {
	const decls = `<xsl:accumulator name="s" as="xs:string*" initial-value="()" streamable="yes">
	  <xsl:accumulator-rule match="rec" select="($value, string(@k))"/>
	  <xsl:accumulator-rule match="sub" phase="end" select="tail($value)"/></xsl:accumulator>
	<xsl:mode streamable="yes" use-accumulators="#all"/>
	<xsl:template match="rec"><r n="{count(accumulator-before('s'))}"/></xsl:template>`
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 40 {
		fmt.Fprintf(&b, `<rec k="k%d"><sub/></rec><rec k="j%d"/>`, i, i)
	}
	b.WriteString("</doc>")
	doc := b.String()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	refused, passed := 0, 0
	for _, lim := range []int{20, 45, 60, 80, 100, 400, 2000} {
		opts := TransformOptions{MaxItems: lim}
		so, _ := mainRun(mainSheet(decls), doc, true, opts)
		to, _ := mainRun(mainSheet(decls), doc, false, opts)
		if so != to {
			t.Errorf("MaxItems %d, TransformReader:\nstreamed %.300s\ntree     %.300s", lim, so, to)
		}
		co, ce, _ := coreRun(coreSheetWith(decls, `<xsl:apply-templates/>`), dir, true, opts)
		cto, cte, _ := coreRun(coreSheetWith(decls, `<xsl:apply-templates/>`), dir, false, opts)
		if co != cto || ce != cte {
			t.Errorf("MaxItems %d, container:\nstreamed %.300s %s\ntree     %.300s %s", lim, co, ce, cto, cte)
		}
		if strings.HasPrefix(to, "error: ") {
			refused++
		} else {
			passed++
		}
	}
	if refused == 0 || passed == 0 {
		t.Errorf("the limits should refuse some runs and pass others: %d refused, %d passed", refused, passed)
	}
}

// TestStreamAccumMemory: a running total and the positions of some records,
// over 150,000 records, take a small part of the tree's peak heap, as a
// container and through TransformReader: the observer holds a value per
// open element, not one per node.
func TestStreamAccumMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("memory test")
	}
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 150000 {
		fmt.Fprintf(&b, `<t v="%d"><n>%d</n><n x="1">x</n></t>`, i%100, i%10)
	}
	b.WriteString("</doc>")
	doc := b.String()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	const decls = `<xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
	  <xsl:accumulator-rule match="t" select="$value + 1"/></xsl:accumulator>
	<xsl:accumulator name="s" as="xs:integer" initial-value="0" streamable="yes">
	  <xsl:accumulator-rule match="t" select="$value + xs:integer(@v)"/></xsl:accumulator>
	<xsl:mode streamable="yes" use-accumulators="#all"/>`
	for _, body := range []string{
		`<xsl:for-each select="doc/t"/><out n="{accumulator-after('n')}" s="{accumulator-after('s')}"/>`,
		`<out><xsl:for-each select="doc/t[@v = '0']"><p n="{accumulator-before('n')}" s="{accumulator-before('s')}"/></xsl:for-each></out>`,
	} {
		var st streamtrace.Counts
		sheet := coreSheetWith(decls, body)
		core := splitPeak(func() { _, _, st = coreRun(sheet, dir, true, TransformOptions{}) })
		tree := splitPeak(func() { coreRun(sheet, dir, false, TransformOptions{}) })
		msheet := mainSheet(decls + `<xsl:template match="/">` + body + `</xsl:template>`)
		var mt streamtrace.Counts
		main := splitPeak(func() { _, mt = mainRun(msheet, doc, true, TransformOptions{}) })
		mtree := splitPeak(func() { mainRun(msheet, doc, false, TransformOptions{}) })
		t.Logf("%s: core %.1f MB, tree %.1f MB; TransformReader %.1f MB, Parse + Transform %.1f MB", body,
			float64(core)/1e6, float64(tree)/1e6, float64(main)/1e6, float64(mtree)/1e6)
		if st.Runs[streamtrace.Core] != 1 || st.Reruns != 0 || mt.Runs[streamtrace.Main] != 1 {
			t.Errorf("%s: trace %+v, main %+v", body, st, mt)
		}
		if !raceEnabled && (float64(core) > 0.1*float64(tree) || float64(main) > 0.1*float64(mtree)) {
			t.Errorf("%s: peak live heap core %.1f MB (tree %.1f), TransformReader %.1f MB (tree %.1f): bound 10%%",
				body, float64(core)/1e6, float64(tree)/1e6, float64(main)/1e6, float64(mtree)/1e6)
		}
	}
}
