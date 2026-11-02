package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// streamableContainers returns the streamable xsl:source-document and
// xsl:stream elements of a stylesheet tree.
func streamableContainers(root *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	walkElements(root, func(el *xdm.Node) bool {
		if (isXSL(el, "source-document") || isXSL(el, "stream")) && isYes(el.AttrValue("streamable")) {
			out = append(out, el)
		}
		return true
	})
	return out
}

// planMentions reports whether any attribute in el's subtree names one of
// the calls whose properties the instruction walk carries in from outside
// the construct (§19.8.9.1, §19.8.9.4), so that a fresh re-analysis of the
// construct alone cannot reproduce them.
func planMentions(el *xdm.Node) bool {
	found := false
	walkElements(el, func(n *xdm.Node) bool {
		for a := range n.Attrs() {
			v := a.Value()
			if strings.Contains(v, "current-group") || strings.Contains(v, "accumulator-after") ||
				strings.Contains(v, "current-merge-group") {
				found = true
			}
		}
		return !found
	})
	return found
}

func insideFunction(el *xdm.Node) bool {
	for n := el; n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if isXSL(n, "function") {
			return true
		}
	}
	return false
}

// planSrcRec resolves an operand's src to the record of the construct it
// names.
func planSrcRec(pl *streamPlan, src any) *planRec {
	if s, ok := src.(planSite); ok {
		r := pl.root(s)
		if r == nil {
			return nil
		}
		return pl.rec(r)
	}
	return pl.rec(src)
}

// TestStreamPlanAgreesWithAnalysis checks the side map against the analysis
// on every streamable container of the W3C XSLT 3.0 suite:
//
//   - recording moves no verdict: the plan walk's verdict is the one the same
//     walk gives without a plan;
//   - the container's own body record is that verdict;
//   - every recorded operand carries the posture and sweep its construct's
//     own record has (the map is one consistent tree);
//   - every recorded XPath site and instruction, re-analysed alone with the
//     focus the map says it had, gets the posture and sweep the map holds.
func TestStreamPlanAgreesWithAnalysis(t *testing.T) {
	root := filepath.Join(corpusBase(t), "xslt30-test", "tests")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no xslt30-test under testdata")
	}
	var n struct {
		files, containers, trusted, recs, withOps, conflicts int
		links, sites, siteSkips, instrs, instrSkips          int
		exprs, exprSkips                                     int
	}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".xsl") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(data), `streamable="yes"`) {
			return nil
		}
		tree, err := xdm.Parse(strings.NewReader(string(data)), xdm.ParseOptions{})
		if err != nil {
			return nil
		}
		n.files++
		for _, sd := range streamableContainers(tree.Root) {
			n.containers++
			name := p + " href=" + sd.AttrValue("href")
			pl := buildStreamPlan(sd, false)
			if len(pl.frames) != 0 {
				t.Errorf("%s: %d frames left open", name, len(pl.frames))
			}
			tr := newDeclTrust(documentElementOf(sd), false)
			p0, k0, u0 := sequenceConstructorVerdict(sd, postureStriding, tr.sets, tr.funcs, tr)
			v := pl.verdict
			if v.props != p0 || v.known != k0 || v.unverified != u0 {
				t.Errorf("%s: plan walk verdict %v/%v/%v, plain walk %v/%v/%v",
					name, v.props, v.known, v.unverified, p0, k0, u0)
			}
			if pl.trusted() {
				n.trusted++
			}
			if b := pl.rec(planBody{sd}); b == nil ||
				b.props != v.props || b.known != v.known || b.unverified != v.unverified {
				t.Errorf("%s: container body record %+v, verdict %+v", name, b, v)
			}
			for key, r := range pl.recs {
				n.recs++
				if r.conflict {
					n.conflicts++
					continue
				}
				if r.ops != nil {
					n.withOps++
				}
				for _, o := range r.ops {
					if o.src == nil {
						continue
					}
					if c := planSrcRec(pl, o.src); c != nil {
						n.links++
						if c.props != o.props {
							t.Errorf("%s: operand %T of %T: map says %v, combine saw %v",
								name, o.src, key, c.props, o.props)
						}
					}
				}
				if e, ok := key.(xpath.Expr); ok {
					// Any sub-expression, alone. A variable or current() reads
					// state the enclosing walk set up; those are left to the
					// site check above, which reproduces it.
					if src := e.String(); strings.Contains(src, "$") || strings.Contains(src, "current") ||
						strings.Contains(src, "accumulator-") {
						n.exprSkips++
						continue
					}
					n.exprs++
					a := &analyzer{ctxPosture: r.ctx, ctxAllowsChildren: r.ctxAllowsChildren, known: true,
						funcs: tr.funcs, currentPosture: r.ctx,
						currentAllowsChildren: r.ctxAllowsChildren, currentInScope: true}
					if got := a.expr(e); got != r.props {
						t.Errorf("%s: sub-expression %s (%T) re-analysed alone %v, map %v", name, e, e, got, r.props)
					}
					continue
				}
				el, ok := key.(*xdm.Node)
				if !ok {
					continue
				}
				if insideFunction(el) || planMentions(el) {
					n.instrSkips++
					continue
				}
				n.instrs++
				a := &instrAnalyzer{ctxPosture: r.ctx, ctxAllowsChildren: r.ctxAllowsChildren,
					known: true, attrSets: tr.sets, funcs: tr.funcs}
				if got := a.instruction(el); got != r.props {
					t.Errorf("%s: <%s> re-analysed alone %v, map %v", name, el.Name().Local, got, r.props)
				}
			}
			for s := range pl.sites {
				e := pl.root(s)
				r := pl.rec(e)
				if e == nil || r == nil {
					continue
				}
				if insideFunction(s.el) || planMentions(s.el) {
					n.siteSkips++
					continue
				}
				n.sites++
				a := &analyzer{ctxPosture: r.ctx, ctxAllowsChildren: r.ctxAllowsChildren, known: true,
					decl: s.el, funcs: tr.funcs, currentPosture: r.ctx,
					currentAllowsChildren: r.ctxAllowsChildren, currentInScope: true}
				if got := a.expr(e); got != r.props {
					t.Errorf("%s: %q re-analysed alone %v, map %v", name, s.src, got, r.props)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.containers == 0 {
		t.Fatal("no streamable containers found")
	}
	t.Logf("stream plan: %d stylesheets, %d containers (%d trusted), %d records (%d with operands, %d conflicts), "+
		"%d operand links, %d sites re-analysed (%d skipped), %d instructions re-analysed (%d skipped), "+
		"%d sub-expressions re-analysed (%d skipped)",
		n.files, n.containers, n.trusted, n.recs, n.withOps, n.conflicts,
		n.links, n.sites, n.siteSkips, n.instrs, n.instrSkips, n.exprs, n.exprSkips)
}

// planOfSheet builds the plan of the one streamable container in a stylesheet.
func planOfSheet(t *testing.T, sheet string) (*streamPlan, *xdm.Node) {
	t.Helper()
	tree, err := xdm.Parse(strings.NewReader(sheet), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sds := streamableContainers(tree.Root)
	if len(sds) != 1 {
		t.Fatalf("%d streamable containers", len(sds))
	}
	return buildStreamPlan(sds[0], false), sds[0]
}

// firstXSL returns the first element in el's subtree with the given XSLT
// local name.
func firstXSL(el *xdm.Node, local string) *xdm.Node {
	var got *xdm.Node
	walkElements(el, func(n *xdm.Node) bool {
		if got == nil && isXSL(n, local) {
			got = n
		}
		return got == nil
	})
	return got
}

func TestStreamPlanRules(t *testing.T) {
	const hdr = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f" exclude-result-prefixes="f xs">`
	sheet := func(decls, body string) string {
		return hdr + decls + `<xsl:template name="main"><xsl:source-document streamable="yes" href="d.xml">` +
			body + `</xsl:source-document></xsl:template></xsl:stylesheet>`
	}
	type opWant struct {
		consuming, choice bool
	}
	check := func(t *testing.T, r *planRec, want []opWant, consumer int, single bool) {
		t.Helper()
		if r == nil {
			t.Fatal("no record")
		}
		if len(r.ops) != len(want) {
			t.Fatalf("%d operands %+v, want %d", len(r.ops), r.ops, len(want))
		}
		for i, w := range want {
			if r.ops[i].consuming != w.consuming || r.ops[i].choice != w.choice {
				t.Errorf("operand %d: consuming %v choice %v, want %v %v",
					i, r.ops[i].consuming, r.ops[i].choice, w.consuming, w.choice)
			}
		}
		if i, ok := r.consumer(); i != consumer || ok != single {
			t.Errorf("consumer() = %d, %v; want %d, %v", i, ok, consumer, single)
		}
	}
	site := func(t *testing.T, pl *streamPlan, el *xdm.Node, attr string) *planRec {
		t.Helper()
		e := pl.root(planSite{el, el.AttrValue(attr)})
		if e == nil {
			t.Fatalf("no parse of @%s", attr)
		}
		r := pl.rec(e)
		if r == nil {
			t.Fatalf("no record of @%s", attr)
		}
		return r
	}

	t.Run("single consuming operand", func(t *testing.T) {
		pl, sd := planOfSheet(t, sheet("", `<out><xsl:value-of select="@k"/><xsl:copy-of select="doc/rec"/></out>`))
		lre := sd.ChildElements()[0]
		check(t, pl.rec(planBody{lre}), []opWant{{false, false}, {true, false}}, 1, true)
		cp := firstXSL(sd, "copy-of")
		check(t, pl.rec(cp), []opWant{{true, false}}, 0, true)
		if r := pl.rec(cp); r.props != (props{postureGrounded, sweepConsuming}) || !r.known || r.unverified {
			t.Errorf("copy-of record %+v", r)
		}
		if !pl.trusted() {
			t.Error("container not trusted")
		}
	})
	t.Run("motionless transmissions", func(t *testing.T) {
		pl, sd := planOfSheet(t, sheet("", `<xsl:for-each select="doc/rec"><xsl:sequence select="(@a, @b)"/></xsl:for-each>`))
		r := site(t, pl, firstXSL(sd, "sequence"), "select")
		check(t, r, []opWant{{true, false}, {true, false}}, -1, false)
		if r.props != (props{postureStriding, sweepMotionless}) || r.ctx != postureStriding {
			t.Errorf("(@a, @b) record %+v", r)
		}
	})
	t.Run("xpath if is a choice group", func(t *testing.T) {
		pl, sd := planOfSheet(t, sheet("", `<xsl:for-each select="doc/rec"><xsl:copy-of select="if (@x) then a else b"/></xsl:for-each>`))
		r := site(t, pl, firstXSL(sd, "copy-of"), "select")
		check(t, r, []opWant{{false, false}, {true, true}, {true, true}}, -1, false)
	})
	t.Run("xsl:choose is a choice group", func(t *testing.T) {
		pl, sd := planOfSheet(t, sheet("", `<xsl:for-each select="doc/rec"><xsl:choose>`+
			`<xsl:when test="@x"><xsl:copy-of select="a"/></xsl:when>`+
			`<xsl:when test="@y"><v/></xsl:when>`+
			`<xsl:otherwise><xsl:copy-of select="b"/></xsl:otherwise></xsl:choose></xsl:for-each>`))
		ch := firstXSL(sd, "choose")
		r := pl.rec(ch)
		check(t, r, []opWant{{false, false}, {true, true}, {false, false}, {false, true}, {true, true}}, -1, false)
		if _, ok := r.ops[0].src.(planSite); !ok {
			t.Errorf("when test src %T", r.ops[0].src)
		}
		if b, ok := r.ops[1].src.(planBody); !ok || b.el != ch.ChildElements()[0] {
			t.Errorf("when body src %#v", r.ops[1].src)
		}
	})
	t.Run("xsl:try catches are a choice group, the try is not", func(t *testing.T) {
		pl, sd := planOfSheet(t, sheet("", `<xsl:try><xsl:copy-of select="doc"/>`+
			`<xsl:catch><xsl:copy-of select="doc"/></xsl:catch><xsl:catch errors="*:X"><e/></xsl:catch></xsl:try>`))
		r := pl.rec(firstXSL(sd, "try"))
		check(t, r, []opWant{{true, false}, {true, true}, {false, true}}, -1, false)
	})
	t.Run("for-each is a cascade: select site and body", func(t *testing.T) {
		pl, sd := planOfSheet(t, sheet("", `<xsl:for-each select="doc/rec"><v><xsl:value-of select="@k"/></v></xsl:for-each>`))
		fe := firstXSL(sd, "for-each")
		r := pl.rec(fe)
		if r == nil || r.ops != nil || r.props != (props{postureGrounded, sweepConsuming}) {
			t.Fatalf("for-each record %+v", r)
		}
		if s := site(t, pl, fe, "select"); s.props != (props{postureStriding, sweepConsuming}) {
			t.Errorf("select record %+v", s)
		}
		b := pl.rec(planBody{fe})
		if b == nil || b.ctx != postureStriding || b.props != (groundedMotionless) {
			t.Errorf("body record %+v", b)
		}
		vo := site(t, pl, firstXSL(sd, "value-of"), "select")
		if vo.ctx != postureStriding || vo.props != (props{postureStriding, sweepMotionless}) {
			t.Errorf("@k record %+v", vo)
		}
	})
	t.Run("higher-order operand", func(t *testing.T) {
		pl, sd := planOfSheet(t, sheet("", `<xsl:value-of select="for $i in 1 to 2 return string(doc)"/>`))
		r := site(t, pl, firstXSL(sd, "value-of"), "select")
		if r.props.streamable() {
			t.Fatalf("for record %+v", r)
		}
		found := false
		for _, o := range r.ops {
			found = found || (o.higherOrder && o.consuming)
		}
		if !found {
			t.Errorf("no consuming higher-order operand in %+v", r.ops)
		}
	})
	t.Run("unknown is recorded where it arises", func(t *testing.T) {
		pl, sd := planOfSheet(t, sheet("", `<out><xsl:value-of select="@k"/><xsl:where-populated><w/></xsl:where-populated></out>`))
		if r := site(t, pl, firstXSL(sd, "value-of"), "select"); !r.known {
			t.Errorf("value-of before the gap: %+v", r)
		}
		if r := pl.rec(firstXSL(sd, "where-populated")); r == nil || r.known {
			t.Errorf("where-populated: %+v", r)
		}
		if pl.verdict.known || pl.trusted() {
			t.Errorf("verdict %+v", pl.verdict)
		}
	})
	t.Run("unverified is recorded where it arises", func(t *testing.T) {
		const fn = `<xsl:function name="f:g" streamability="absorbing" as="xs:string"><xsl:param name="n" as="node()"/><xsl:variable name="v" select="1"/><xsl:sequence select="string($n/following-sibling::rec[1]/@k)"/></xsl:function>`
		pl, sd := planOfSheet(t, sheet(fn, `<xsl:for-each select="doc/rec"><v k="{@k}"><xsl:value-of select="f:g(.)"/></v></xsl:for-each>`))
		if r := pl.rec(planAVT{firstXSL(sd, "for-each").ChildElements()[0], "{@k}"}); r == nil || !r.known || r.unverified {
			t.Errorf("AVT before the call: %+v", r)
		}
		if r := site(t, pl, firstXSL(sd, "value-of"), "select"); !r.known || !r.unverified {
			t.Errorf("call: %+v", r)
		}
		if !pl.verdict.known || !pl.verdict.unverified || pl.trusted() {
			t.Errorf("verdict %+v", pl.verdict)
		}
	})
	t.Run("a construct assessed twice differently is a conflict", func(t *testing.T) {
		pl := &streamPlan{recs: map[any]*planRec{}}
		k := &xpath.Literal{}
		for _, p := range []props{groundedMotionless, groundedMotionless} {
			pl.enter()
			pl.leave(k, p, postureStriding, true, true, false)
		}
		if pl.rec(k) == nil {
			t.Fatal("same result twice must stand")
		}
		pl.enter()
		pl.leave(k, groundedMotionless, postureGrounded, true, true, false)
		if pl.rec(k) != nil {
			t.Fatal("a different focus must conflict")
		}
	})
}
