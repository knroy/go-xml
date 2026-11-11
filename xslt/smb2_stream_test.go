package xslt

// Round SM-b lane 2: the streamed-container analysis reads each module's
// tables once per compile, a stylesheet function's tall body is charged
// call depth, the tree's accumulator walk holds no frames per document
// level, and a streamed xsl:source-document in a fork's prong counts
// against the transform's readers.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// fragmentBody, via copyFreeBody and leafAtomic: n xsl:sequence in a
// container of a module with n functions read the function table once.
func TestSMB2FragmentBodyOneFunctionTable(t *testing.T) {
	sm02Growth(t, 150, 9, func(n int) func() {
		var b strings.Builder
		b.WriteString(sm02Head)
		for i := range n {
			fmt.Fprintf(&b, `<xsl:function name="f:g%d"><xsl:sequence select="1"/></xsl:function>`, i)
		}
		b.WriteString(`<xsl:template name="main"><xsl:source-document href="a.xml" streamable="yes">`)
		b.WriteString(strings.Repeat(`<xsl:sequence select="string('x')"/>`, n))
		b.WriteString(`</xsl:source-document></xsl:template></xsl:stylesheet>`)
		sd := sm02Find(sm02Doc(t, b.String()), "source-document")
		return func() {
			var tabs moduleTabs
			if !fragmentBody(sd, &tabs) {
				t.Fatal("fragmentBody refused a body of atomic sequences")
			}
		}
	})
}

// buildStreamPlan: n containers in a module of 8n functions and attribute
// sets walk the module once per compile, not once per container.
func TestSMB2DeclTrustOncePerCompile(t *testing.T) {
	sm02Growth(t, 60, 7, func(n int) func() {
		var b strings.Builder
		b.WriteString(sm02Head)
		for i := range 8 * n {
			fmt.Fprintf(&b, `<xsl:function name="f:g%d"><xsl:sequence select="1"/></xsl:function><xsl:attribute-set name="s%d"/>`, i, i)
		}
		b.WriteString(`<xsl:template name="main">`)
		b.WriteString(strings.Repeat(`<xsl:source-document href="a.xml" streamable="yes"><xsl:value-of select="count(*)"/></xsl:source-document>`, n))
		b.WriteString(`</xsl:template></xsl:stylesheet>`)
		root := sm02Doc(t, b.String())
		return func() {
			if _, err := Compile(root, CompileOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	})
	var tabs moduleTabs
	root := sm02Doc(t, sm02Head+`<xsl:function name="f:g"><xsl:sequence select="1"/></xsl:function><xsl:template name="main">`+
		`<xsl:source-document href="a.xml" streamable="yes"/><xsl:source-document href="b.xml" streamable="yes"/></xsl:template></xsl:stylesheet>`)
	var sds []*xdm.Node
	walkElements(root, func(el *xdm.Node) bool {
		if isXSL(el, "source-document") {
			sds = append(sds, el)
		}
		return true
	})
	p1, p2 := buildStreamPlan(&tabs, sds[0], false), buildStreamPlan(&tabs, sds[1], false)
	if len(p1.funcs) != 1 || fmt.Sprintf("%p", p1.funcs) != fmt.Sprintf("%p", p2.funcs) {
		t.Fatalf("two containers of one module: function tables %p and %p, want one shared", p1.funcs, p2.funcs)
	}
}

// A stylesheet function whose recursive call sits under a long operator
// chain is charged extra depth per call, so chain height times call depth
// stays bounded (XPDY0001, a resource limit); a short body is not.
func TestSMB2FunctionTallBodyChargesDepth(t *testing.T) {
	run := func(body string, n int) error {
		s := compileString(t, sm02Head+`<xsl:function name="f:f"><xsl:param name="n"/>`+
			`<xsl:sequence select="if ($n le 0) then 0 else `+body+`"/></xsl:function>`+
			fmt.Sprintf(`<xsl:template name="main"><xsl:value-of select="f:f(%d)"/></xsl:template></xsl:stylesheet>`, n))
		_, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main"})
		return err
	}
	tall := "f:f($n - 1)" + strings.Repeat(" + 1", 999)
	if err := run(tall, 400); err == nil || !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "XPDY0001") {
		t.Fatalf("tall body at depth 400: %v, want an XPDY0001 resource limit", err)
	}
	if err := run(tall, 20); err != nil {
		t.Fatalf("tall body at depth 20: %v", err)
	}
	if err := run("f:f($n - 1) + 1", 400); err != nil {
		t.Fatalf("short body at depth 400: %v", err)
	}
}

// A chain of accumulators each reading the one before at the innermost of
// a deep document walks each in a loop: the stack holds a few frames per
// link, not one per link per level.
func TestSMB2AccumulatorChainFlatWalk(t *testing.T) {
	const links, depth = 200, 900
	var b strings.Builder
	b.WriteString(sm02Head + `<xsl:mode use-accumulators="#all"/>`)
	b.WriteString(`<xsl:accumulator name="a0" initial-value="0"><xsl:accumulator-rule match="x" select="1"/></xsl:accumulator>`)
	for i := 1; i < links; i++ {
		fmt.Fprintf(&b, `<xsl:accumulator name="a%d" initial-value="0"><xsl:accumulator-rule match="x[not(x)]" select="accumulator-before('a%d') + 1"/></xsl:accumulator>`, i, i-1)
	}
	fmt.Fprintf(&b, `<xsl:template match="/"><xsl:value-of select="(//x)[last()]/accumulator-after('a%d')"/></xsl:template></xsl:stylesheet>`, links-1)
	s := compileString(t, b.String())
	doc := sm02Doc(t, strings.Repeat("<x>", depth)+strings.Repeat("</x>", depth))
	old := debug.SetMaxStack(8 << 20) // the recursive walk needed about 15 MB
	res, err := s.Transform(context.Background(), doc, TransformOptions{})
	debug.SetMaxStack(old)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.String(); !strings.Contains(got, fmt.Sprint(links)) {
		t.Fatalf("got %q, want %d", got, links)
	}
}

// A streamed xsl:source-document with a fork of its own, in a prong of a
// teed fork, opens a cursor beside the outer consumers: past maxTeeParts
// readers at once it runs on the tree, with the tree's result.
func TestSMB2SourceDocInProngCountsReaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
		t.Fatal(err)
	}
	inner := `<xsl:sequence><xsl:for-each select="/doc/rec"><r k="{@k}"/></xsl:for-each></xsl:sequence>`
	body := func(outer, in int) string {
		prong := `<xsl:sequence><xsl:for-each select="/doc/rec"><o k="{@k}"/></xsl:for-each>` +
			`<xsl:source-document streamable="yes" href="doc.xml"><xsl:fork>` + strings.Repeat(inner, in) +
			`</xsl:fork></xsl:source-document></xsl:sequence>`
		return `<out><xsl:fork>` + strings.Repeat(prong, outer) + `</xsl:fork></out>`
	}
	for _, c := range []struct {
		outer, inner int
		refused      bool
	}{{20, 20, false}, {33, 33, true}} {
		var reruns []error
		traceRerun = func(err error) { reruns = append(reruns, err) }
		so, se, _ := coreRun(coreSheet(body(c.outer, c.inner)), dir, true, TransformOptions{})
		traceRerun = nil
		to, te, _ := coreRun(coreSheet(body(c.outer, c.inner)), dir, false, TransformOptions{})
		if se != "" || te != "" || so != to {
			t.Fatalf("%d x %d: stream %q %s\ntree %q %s", c.outer, c.inner, so, se, to, te)
		}
		refused := len(reruns) == 1 && errors.Is(reruns[0], errStreamReaders)
		if refused != c.refused || !c.refused && len(reruns) > 0 {
			t.Errorf("%d x %d: reruns %v, want refused %v", c.outer, c.inner, reruns, c.refused)
		}
	}
}
