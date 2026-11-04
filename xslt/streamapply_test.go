package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// applyCases are template rules and modes (decls) and a container body that
// applies them; stream says the core must take the container.
var applyCases = []struct {
	decls, body string
	stream      bool
}{
	// The identity transform: the built-in shallow-copy, of the document node
	// and of its children.
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, `<xsl:apply-templates select="."/>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, `<xsl:apply-templates/>`, true},
	// A filter: one rule deletes, one rewrites, the rest is copied.
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="sub"/>` +
		`<xsl:template match="rec[@k = 'a']"><a v="{@v}"><xsl:apply-templates/></a></xsl:template>`,
		`<xsl:apply-templates select="."/>`, true},
	// The default built-in rules (text-only-copy) and rules over elements.
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><r k="{@k}" p="{position()}"><xsl:apply-templates/></r></xsl:template>`,
		`<xsl:apply-templates/>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><r><xsl:value-of select="."/></r></xsl:template>`,
		`<xsl:apply-templates select="*/rec"/>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><r><xsl:copy-of select="."/></r></xsl:template>`,
		`<xsl:apply-templates select="*/rec[@k = 'a']"/>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><r p="{position()}"/></xsl:template>`,
		`<xsl:apply-templates select="*/rec[2]"/>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="*"><xsl:copy><xsl:apply-templates select="@*"/><xsl:apply-templates/></xsl:copy></xsl:template>` +
		`<xsl:template match="@*"><xsl:attribute name="{name()}" select="concat('[', ., ']')"/></xsl:template>` +
		`<xsl:template match="text()"><xsl:value-of select="upper-case(.)"/></xsl:template>`,
		`<xsl:apply-templates/>`, true},
	// The document node's rule.
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><o><xsl:apply-templates/></o></xsl:template>` +
		`<xsl:template match="*"><e n="{name()}"><xsl:apply-templates/></e></xsl:template>`,
		`<xsl:apply-templates select="."/>`, true},
	// The document node's rule is planned on the document node: what it
	// reads of it, a stand-in answers; its string value it may not read
	// (the fuzzer's seeds 2418, 4165: planned at an element's view, the
	// split read the cursor's start tag that is not there).
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><o n="{name()}"><xsl:apply-templates/></o></xsl:template>`,
		`<xsl:apply-templates select="."/>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><v><xsl:value-of select="upper-case(.)"/></v></xsl:template>`,
		`<xsl:apply-templates select="."/>`, false},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:attribute name="a" select="."/></xsl:template>`,
		`<xsl:apply-templates select="."/>`, false},
	// xsl:copy of the document node, its children applied (streamable-001):
	// the copy takes the DOCTYPE and base URI the dispatch gave the stand-in.
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><xsl:copy><xsl:apply-templates/></xsl:copy></xsl:template>` +
		`<xsl:template match="*"><xsl:copy><xsl:apply-templates/></xsl:copy></xsl:template><xsl:template match="sub"/>`,
		`<xsl:variable name="d"><xsl:apply-templates select="."/></xsl:variable><b u="{base-uri($d/*)}"/><xsl:copy-of select="$d"/>`, true},
	{`<xsl:mode streamable="yes"/>`, `<xsl:copy><xsl:apply-templates select="*/rec"/></xsl:copy>`, false},
	// Built-in rules: deep-skip, shallow-skip, deep-copy, fail.
	{`<xsl:mode streamable="yes" on-no-match="deep-skip"/><xsl:template match="/"><o><xsl:apply-templates/></o></xsl:template>` +
		`<xsl:template match="doc|rec"><xsl:apply-templates/></xsl:template><xsl:template match="sub"><s x="{@x}"/></xsl:template>`,
		`<xsl:apply-templates select="."/>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-skip"/><xsl:template match="text()"><t><xsl:value-of select="."/></t></xsl:template>` +
		`<xsl:template match="@k"><k v="{.}"/></xsl:template>`,
		`<xsl:apply-templates/>`, true},
	{`<xsl:mode streamable="yes" on-no-match="deep-copy"/><xsl:template match="/*"><top><xsl:apply-templates/></top></xsl:template>`,
		`<xsl:apply-templates/>`, true},
	{`<xsl:mode streamable="yes" on-no-match="deep-copy"/>`, `<xsl:apply-templates select="."/>`, false},
	{`<xsl:mode streamable="yes" on-no-match="fail"/><xsl:template match="/|doc|rec"><xsl:apply-templates/></xsl:template>`,
		`<xsl:apply-templates select="."/>`, true},
	// Priorities, next-match chains, apply-imports with nothing imported.
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>` +
		`<xsl:template match="rec" priority="3"><a><xsl:next-match/></a></xsl:template>` +
		`<xsl:template match="rec[@k = 'a']" priority="2"><b><xsl:next-match><xsl:with-param name="q" select="'Q'"/></xsl:next-match></b></xsl:template>` +
		`<xsl:template match="*:rec"><xsl:param name="q" select="'-'"/><c q="{$q}"><xsl:apply-templates/></c></xsl:template>`,
		`<xsl:apply-templates/>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><a><xsl:apply-imports/></a></xsl:template>`,
		`<xsl:apply-templates/>`, true},
	// Tunnel parameters across levels, mode switches and #current.
	{`<xsl:mode streamable="yes"/><xsl:mode name="m" streamable="yes" on-no-match="shallow-copy"/>` +
		`<xsl:template match="rec"><xsl:param name="t" tunnel="yes"/><r t="{$t}"><xsl:apply-templates mode="m"/></r></xsl:template>` +
		`<xsl:template match="sub" mode="m"><xsl:param name="t" tunnel="yes"/><s t="{$t}"><xsl:apply-templates mode="#current"/></s></xsl:template>`,
		`<xsl:apply-templates><xsl:with-param name="t" tunnel="yes" select="'T'"/></xsl:apply-templates>`, true},
	// Deep recursion over the descent: rules for every element.
	{`<xsl:mode streamable="yes"/><xsl:template match="*"><xsl:element name="x{count(ancestor::*)}"><xsl:apply-templates/></xsl:element></xsl:template>`,
		`<xsl:apply-templates/>`, true},
	// outermost() selects from a rule.
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><o><xsl:apply-templates select="outermost(.//rec)"/></o></xsl:template>` +
		`<xsl:template match="rec"><r k="{@k}"><xsl:value-of select="."/></r></xsl:template>`,
		`<xsl:apply-templates/>`, true},
	// Two rules matching, on-multiple-match="fail".
	{`<xsl:mode streamable="yes" on-multiple-match="fail"/><xsl:template match="rec"/><xsl:template match="*[@k]"/>`,
		`<xsl:apply-templates select="*/rec"/>`, true},
	// Several consumers of a rule's element: the body on a grounded copy
	// of it (streamtee_ground.go): fork prongs, and a copy in a variable
	// beside a motionless apply-templates over it in another mode
	// (streamable-009's shape).
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><r><xsl:fork><xsl:sequence><xsl:value-of select="count(sub)"/></xsl:sequence>` +
		`<xsl:sequence><xsl:value-of select="string(.)"/></xsl:sequence></xsl:fork></r></xsl:template>`,
		`<xsl:apply-templates select="*/rec"/>`, true},
	{`<xsl:mode name="s" streamable="yes"/><xsl:template match="rec" mode="s"><xsl:variable name="c"><xsl:copy-of select="."/></xsl:variable>` +
		`<r><xsl:apply-templates select="$c/rec/sub"/></r></xsl:template><xsl:template match="sub"><s x="{@x}"/></xsl:template>`,
		`<xsl:apply-templates select="*/rec" mode="s"/>`, true},
	// Refused: a positional pattern, a mode that is not streamable, a
	// rule body that reads twice, a sort.
	{`<xsl:mode streamable="yes"/><xsl:template match="rec[1]"><f/></xsl:template>`, `<xsl:apply-templates select="*/rec"/>`, false},
	{`<xsl:mode name="n"/><xsl:template match="rec" mode="n"><f/></xsl:template>`, `<xsl:apply-templates select="*/rec" mode="n"/>`, false},
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><xsl:apply-templates/><xsl:apply-templates/></xsl:template>`,
		`<xsl:apply-templates/>`, false},
	{`<xsl:mode streamable="yes"/>`, `<xsl:apply-templates select="*/rec"><xsl:sort select="@k"/></xsl:apply-templates>`, false},
}

// TestStreamApplyMatchesTree: every apply case over every document, under
// every whitespace declaration, gives on the core the tree's output or the
// tree's error, the ones that must stream without a re-run, no guard hit.
func TestStreamApplyMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range applyCases {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, rules %s, body %s", name, d, c.decls, c.body)
				streamed += coreCheck(t, coreSheetWith(d+c.decls, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}

// TestStreamApplyDeep: apply-templates down a document 400 elements deep,
// irregular, on one cursor, against the tree; and the template recursion
// limit raises the tree's error.
func TestStreamApplyDeep(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := range 400 {
		fmt.Fprintf(&b, `<e%d k="%d">t%d<x/>`, i%3, i, i)
	}
	for i := 399; i >= 0; i-- {
		fmt.Fprintf(&b, `<y/></e%d>`, i%3)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet := coreSheetWith(`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="x"/>`+
		`<xsl:template match="e1"><f k="{@k}"><xsl:apply-templates/></f></xsl:template>`, `<xsl:apply-templates/>`)
	if coreCheck(t, sheet, dir, "400 deep", true, TransformOptions{}) != 1 {
		t.Error("400 deep: not streamed without a re-run")
	}
	coreCheck(t, sheet, dir, "400 deep, MaxDepth 300", true, TransformOptions{MaxDepth: 300})
}

// TestStreamApplyTopLevel: at the top of a sequence the tree's built-in
// shallow-copy keeps the source's own text nodes, whose parents a later
// instruction reads; the stream has parentless copies. A container whose
// result is such a sequence runs on the tree once the stream shows it
// (a fallback, not a failure), and an apply op in a sequence-typed variable
// is refused.
func TestStreamApplyTopLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
		t.Fatal(err)
	}
	const decls = `<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="sub"><xsl:apply-templates/></xsl:template>`
	outer := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + decls +
		`<xsl:template name="main"><xsl:variable name="v" as="node()*"><xsl:source-document streamable="yes" href="doc.xml">` +
		`<xsl:apply-templates select="*/rec/sub"/></xsl:source-document></xsl:variable>` +
		`<out n="{count($v)}" p="{count($v/..)}"/></xsl:template></xsl:stylesheet>`
	so, se, st := coreRun(outer, dir, true, TransformOptions{})
	to, te, _ := coreRun(outer, dir, false, TransformOptions{})
	if so != to || se != te || st.Fallbacks != 1 || st.Reruns != 0 || st.Guards != 0 {
		t.Errorf("container in a sequence-typed variable:\ncore %q %q %+v\ntree %q %q", so, se, st, to, te)
	}
	inner := coreSheetWith(decls, `<xsl:variable name="v" as="node()*"><xsl:apply-templates select="*/rec/sub"/></xsl:variable>`+
		`<out n="{count($v)}"/>`)
	coreCheck(t, inner, dir, "apply-templates in a sequence-typed variable", false, TransformOptions{})
	// Elements the built-in rules make at the top of the principal result
	// are new nodes in both: it streams.
	main := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + decls +
		`<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml">` +
		`<xsl:apply-templates select="*/rec"/></xsl:source-document></xsl:template></xsl:stylesheet>`
	if _, _, st := coreRun(main, dir, true, TransformOptions{}); st.Runs[streamtrace.Core] != 1 || st.Fallbacks+st.Reruns != 0 {
		t.Errorf("principal result: %+v", st)
	}
}

// TestStreamResultNodes: Result.Nodes, the raw principal result, holds the
// tree's nodes whatever streamed: where a node of the stream would be at its
// top (a text node or an element a built-in rule keeps), the container runs
// on the tree, so that parent, root and identity are the source's, as in tree
// mode. A loop body or select returning a streamed node is XTSE3430 (§18.1:
// not grounded) in both modes; copies are new nodes in both.
func TestStreamResultNodes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ decls, body string }{
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="sub"><xsl:apply-templates/></xsl:template>`,
			`<xsl:apply-templates select="*/rec/sub"/>`},
		{`<xsl:mode streamable="yes" on-no-match="deep-copy"/>`, `<xsl:apply-templates select="*/rec"/>`},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, `<xsl:apply-templates select="*/rec/@k"/>`},
		{``, `<xsl:for-each select="/doc/rec"><xsl:sequence select="@k"/></xsl:for-each>`},
		{``, `<xsl:for-each select="/doc/rec"><xsl:sequence select="."/></xsl:for-each>`},
		{``, `<xsl:for-each select="/doc/rec"><xsl:sequence select="sub"/></xsl:for-each>`},
		{``, `<xsl:for-each select="//rec"><xsl:sequence select="@v"/></xsl:for-each>`},
		// Copies, in both modes: it streams, nothing falls back.
		{``, `<xsl:for-each select="/doc/rec"><xsl:copy-of select="."/></xsl:for-each>`},
		{`<xsl:mode streamable="yes"/><xsl:template match="rec"><xsl:copy-of select="."/></xsl:template>`,
			`<xsl:apply-templates select="*/rec"/>`},
	} {
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + c.decls +
			`<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml">` + c.body +
			`</xsl:source-document></xsl:template></xsl:stylesheet>`
		nodes := func(core bool) string {
			defer func() { noBursts = false }()
			tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
			if err != nil {
				t.Fatal(err)
			}
			s, err := Compile(tree.Root, CompileOptions{})
			if err != nil {
				return "compile " + err.Error() // the analysis refuses it (§18.1: not grounded)
			}
			docs, _ := NewFileResolver(dir)
			noBursts = !core
			before := streamtrace.Snapshot()
			res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
			if core {
				tr := streamtrace.Snapshot().Sub(before)
				t.Logf("%s: runs %d fallbacks %d re-runs %d guards %d", c.body, tr.Runs[streamtrace.Core], tr.Fallbacks, tr.Reruns, tr.Guards)
				if tr.Guards != 0 {
					t.Errorf("%s: guard hits", c.body)
				}
			}
			if err != nil {
				return "error " + err.Error()
			}
			var b strings.Builder
			for i, it := range res.Nodes {
				n, ok := it.(*xdm.Node)
				if !ok {
					fmt.Fprintf(&b, "[%d atomic %v]", i, it)
					continue
				}
				fmt.Fprintf(&b, "[%d %v %s %q", i, n.Kind(), n.Name().Local, n.StringValue())
				if p := n.Parent(); p != nil {
					fmt.Fprintf(&b, " parent %v %s", p.Kind(), p.Name().Local)
				} else {
					b.WriteString(" no parent")
				}
				r := n
				for r.Parent() != nil {
					r = r.Parent()
				}
				fmt.Fprintf(&b, " root %v", r.Kind())
				for j, o := range res.Nodes {
					if m, ok := o.(*xdm.Node); ok && j != i && n.Is(m) {
						fmt.Fprintf(&b, " is %d", j)
					}
				}
				for j, o := range res.Nodes {
					if m, ok := o.(*xdm.Node); ok && j < i && n.Parent() != nil && m.Parent() != nil && n.Parent().Is(m.Parent()) {
						fmt.Fprintf(&b, " sibling of %d", j)
					}
				}
				b.WriteString("]")
			}
			return b.String()
		}
		if co, tr := nodes(true), nodes(false); co != tr {
			t.Errorf("%s %s:\ncore %s\ntree %s", c.decls, c.body, co, tr)
		}
	}
}

// TestStreamApplyLeafAllocs: the streamed identity transform does not copy
// each text node it hands to the built-in rule (a tree per node): its
// allocations stay within 6x the tree's run of the same transform (measured
// at d2b7f885: 36,940 against 9,800, the views and the cursor's events; a
// copy per text node made it 129,941).
func TestStreamApplyLeafAllocs(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 3000 {
		fmt.Fprintf(&b, "<r>t%d<s>u</s>v<s>w</s>x</r>\n", i)
	}
	b.WriteString("</doc>")
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet := coreSheetWith(`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, `<xsl:apply-templates/>`)
	allocs := func(core bool) float64 {
		return testing.AllocsPerRun(2, func() { coreRun(sheet, dir, core, TransformOptions{}) })
	}
	core, tree := allocs(true), allocs(false)
	t.Logf("allocations: core %.0f, tree %.0f", core, tree)
	if core > 6*tree {
		t.Errorf("core %.0f allocations, tree %.0f: more than 6x", core, tree)
	}
}

// TestStreamApplyDocCopy: the document node's copy at the top of the
// principal result (xsl:copy in its rule, the built-in shallow-copy) has
// the document's DOCTYPE and base URI, which the streamed run takes from the
// views' document node, as the tree's run has them.
func TestStreamApplyDocCopy(t *testing.T) {
	dir := t.TempDir()
	doc := `<!DOCTYPE doc [<!ENTITY e "x">]><doc xml:base="http://ex/b/"><rec k="a">&e;</rec></doc>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rules := range []string{
		`<xsl:mode streamable="yes"/><xsl:template match="/"><xsl:copy><xsl:apply-templates/></xsl:copy></xsl:template>`,
		`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`,
	} {
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + rules +
			`<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml">` +
			`<xsl:apply-templates select="."/></xsl:source-document></xsl:template></xsl:stylesheet>`
		props := func(core bool) string {
			defer func() { noBursts = false }()
			tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
			if err != nil {
				t.Fatal(err)
			}
			s, err := Compile(tree.Root, CompileOptions{})
			if err != nil {
				t.Fatal(err)
			}
			docs, _ := NewFileResolver(dir)
			docs.AllowDOCTYPE = true
			noBursts = !core
			before := streamtrace.Snapshot()
			res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
			if err != nil {
				t.Fatal(err)
			}
			if tr := streamtrace.Snapshot().Sub(before); core && (tr.Runs[streamtrace.Core] != 1 || tr.Reruns != 0) {
				t.Errorf("%s: not streamed: %+v", rules, tr)
			}
			if len(res.Nodes) != 1 {
				return fmt.Sprintf("%d nodes", len(res.Nodes))
			}
			d, ok := res.Nodes[0].(*xdm.Node)
			if !ok {
				return "not a node"
			}
			return fmt.Sprintf("kind %v base %q doctype %q", d.Kind(), d.BaseURI(), d.Tree().DocType)
		}
		if c, tr := props(true), props(false); c != tr {
			t.Errorf("%s:\ncore %s\ntree %s", rules, c, tr)
		}
	}
}
