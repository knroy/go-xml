package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// forkBodies are container bodies holding an xsl:fork or xsl:map. stream says
// the core must take the container.
var forkBodies = []struct {
	body   string
	stream bool
}{
	// Several prongs read the document: a tee.
	{`<xsl:fork><xsl:sequence><a><xsl:value-of select="sum(/doc/rec/@v)"/></a></xsl:sequence>` +
		`<xsl:sequence><b><xsl:value-of select="count(//sub)"/></b></xsl:sequence></xsl:fork>`, true},
	{`<out><xsl:fork><xsl:sequence select="'['"/><xsl:sequence><a><xsl:value-of select="avg(/doc/rec/@v)"/></a></xsl:sequence>` +
		`<xsl:sequence><b><xsl:copy-of select="/doc/rec[@k = 'a']"/></b></xsl:sequence><xsl:sequence select="']'"/></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:sequence select="count(//rec)"/><xsl:sequence select="string-join(//sub/@x, '-')"/>` +
		`<xsl:sequence select="max(//rec/@v)"/><xsl:sequence select="exists(//other)"/></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:sequence><xsl:for-each select="/doc/rec"><r k="{@k}"/></xsl:for-each></xsl:sequence>` +
		`<xsl:sequence><xsl:for-each select="/doc/rec/sub"><s x="{@x}"><xsl:value-of select="."/></s></xsl:for-each></xsl:sequence></xsl:fork></out>`, true},
	// A tee in a streamed xsl:try's body (it panicked at the planner once
	// fork and try met): the first error in prong order is the try's, and a
	// failed prong reads on to the end, so a parse error is still the
	// document's (streamtee_try.go; si-fork-008).
	{`<out><xsl:try><xsl:fork><xsl:sequence select="count(//rec)"/><xsl:sequence select="sum(/doc/rec/@v)"/></xsl:fork>` +
		`<xsl:catch><caught/></xsl:catch></xsl:try></out>`, true},
	{`<out><xsl:try><xsl:fork><xsl:sequence select="count(/doc/rec/xs:integer(@k))"/><xsl:sequence select="string-join(outermost(//sub)/@x)"/></xsl:fork>` +
		`<xsl:catch errors="*:FORG0001"><caught/></xsl:catch></xsl:try></out>`, true},
	// A prong's error, the first in prong order is the fork's: xs:integer
	// of v="x" fails in prong 2, the sum over k fails (FORG0001) in prong 1.
	{`<out><xsl:fork><xsl:sequence select="sum(/doc/rec/@k)"/><xsl:sequence select="sum(/doc/rec/xs:integer(@v))"/></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:sequence select="count(//rec)"/><xsl:sequence select="sum(/doc/rec/xs:integer(@v))"/></xsl:fork></out>`, true},
	// A map of entries that each read the document.
	{`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="'n'" select="count(//rec)"/>` +
		`<xsl:map-entry key="'s'" select="sum(//rec/@v)"/><xsl:map-entry key="'k'" select="string-join(//rec/@k)"/></xsl:map></xsl:variable>` +
		`<m n="{$m?n}" s="{$m?s}" k="{$m?k}"/>`, true},
	{`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="'a'" select="count(//rec)"/>` +
		`<xsl:map-entry key="'a'" select="count(//sub)"/></xsl:map></xsl:variable><m n="{$m?a}"/>`, true}, // XTDE3365
	// A map constructor whose values each read the document, in a
	// variable's select: a tee of its entries (streamtee_map.go;
	// sx-MapExpr-002/004/005). The second fails on the nested document's
	// v="x" (FORG0001) as the tree's does.
	{`<xsl:variable name="m" as="map(*)" select="map{'n': count(//rec), 'k': string-join(//rec/@k, ','), 'c': 1}"/>` +
		`<m n="{$m?n}" k="{$m('k')}" c="{$m?c}"/>`, true},
	{`<xsl:variable name="m" as="map(*)" select="map{'lo': min(//rec/@v/number()), 'hi': max(//rec/@v/number()), 's': sum(//rec/@v)}"/>` +
		`<m lo="{$m?lo}" hi="{$m?hi}" s="{$m?s}"/>`, true},
	// At most one prong reads: a sequence constructor.
	{`<out><xsl:fork><xsl:sequence select="'['"/><xsl:sequence><a/></xsl:sequence><xsl:sequence select="']'"/></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:sequence select="'['"/><xsl:sequence><a><xsl:value-of select="count(//rec)"/></a></xsl:sequence></xsl:fork></out>`, true},
	{`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="'n'" select="count(//rec)"/><xsl:map-entry key="'c'" select="1"/></xsl:map></xsl:variable><m n="{$m?n}" c="{$m?c}"/>`, true},
	// Several prongs read each record: on a grounded copy of it.
	{`<out><xsl:for-each select="/doc/rec"><r><xsl:fork><xsl:sequence><xsl:attribute name="k" select="@k"/></xsl:sequence>` +
		`<xsl:sequence><xsl:attribute name="n" select="count(sub)"/></xsl:sequence><xsl:sequence><xsl:attribute name="s" select="string(.)"/></xsl:sequence></xsl:fork></r></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="/doc/rec"><xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="'n'" select="count(sub)"/>` +
		`<xsl:map-entry key="'s'" select="string(.)"/></xsl:map></xsl:variable><r n="{$m('n')}" s="{$m?s}"/></xsl:for-each></out>`, true},
	// Refused: prongs returning streamed nodes (XTSE3430 in both modes), a
	// consuming map key.
	{`<out><xsl:fork><xsl:sequence select="/doc/rec"/><xsl:sequence select="//sub"/></xsl:fork></out>`, false},
	{`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:map-entry key="string(/doc/rec[1])" select="1"/><xsl:map-entry key="'b'" select="count(//sub)"/></xsl:map></xsl:variable><m b="{$m?b}"/>`, false},
}

// TestStreamForkMatchesTree: every fork and map body over every document,
// under every whitespace declaration, gives on the core the tree's output or
// the tree's error; the ones that must stream ran on the core without a
// re-run, and the tees among them did run (KindFork).
func TestStreamForkMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	before := streamtrace.Snapshot()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range forkBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
	if n := streamtrace.Snapshot().Sub(before).Kinds[streamtrace.KindFork]; n == 0 {
		t.Error("no tee ran")
	}
}

// TestStreamForkErrorOrder: the error a tee returns, before the re-run on the
// tree replaces it, is the tree's: that of the first prong in prong order
// that fails, though a later prong fails earlier in the document. The
// document: rec 1 has v="x", where the early prong's cast fails; the late
// prong's fails once it has read every @k.
func TestStreamForkErrorOrder(t *testing.T) {
	dir := t.TempDir()
	doc := `<doc><rec k="a" v="x"/><rec k="b" v="2"/><rec k="zz" v="3"/></doc>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	late := `<xsl:sequence select="xs:integer(string-join(/doc/rec/@k))"/>`
	early := `<xsl:sequence select="sum(/doc/rec/xs:integer(@v))"/>`
	fine := `<xsl:sequence select="count(//rec)"/>`
	for _, prongs := range [][]string{{late, early}, {early, late}, {fine, late, early}, {fine, early, fine, late}} {
		body := "<out><xsl:fork>" + strings.Join(prongs, "") + "</xsl:fork></out>"
		var streamErr error
		traceRerun = func(err error) { streamErr = err }
		_, se, st := coreRun(coreSheet(body), dir, true, TransformOptions{})
		traceRerun = nil
		_, te, _ := coreRun(coreSheet(body), dir, false, TransformOptions{})
		if st.Kinds[streamtrace.KindFork] != 1 || st.Reruns != 1 || streamErr == nil {
			t.Fatalf("%s: trace %+v, stream error %v", body, st, streamErr)
		}
		if se != te || !strings.Contains(te, streamErr.Error()) {
			t.Errorf("%s:\nstream %v\ntree   %s", body, streamErr, te)
		}
	}
}

// TestStreamForkHostCalls: a host function item reaching the prongs of a tee,
// or the coroutines a streamed merge reads its inputs on, by any route -- a
// static parameter, a parameter, a map in one, a global variable, a partial
// application, a stylesheet function wrapping it, a merge source's
// predicate -- makes a host call there, which shares xpath's EvalLock and
// leaves the goroutine holding it; so does a predicate forcing such a
// global on a cursor's parse goroutine (one cursor too). Whoever takes the
// lock next (a prong, a parse, or the transform forcing a global after the
// fork) must not wait forever: each goroutine hands the lock back at every
// switch. Each run has a deadline,
// so a hang fails the test instead of the binary; streamed and tree answers
// agree; the tees ran.
func TestStreamForkHostCalls(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.xml", "b.xml"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(streamMergeDocs[n]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A Go extension function: a function item this module did not make.
	host := xdm.One(&xdm.FunctionItem{Arity: 1, Invoke: func(_ any, args []xdm.Sequence) (xdm.Sequence, error) {
		s := ""
		for _, it := range xdm.Atomize(args[0]) {
			s += it.(*xdm.Atomic).String()
		}
		return xdm.One(xdm.NewString("h" + s)), nil
	}})
	decls := `<xsl:param name="f" required="yes"/><xsl:param name="m" select="map{}"/>
<xsl:variable name="g" select="$f"/><xsl:variable name="p" select="$f(?)"/><xsl:variable name="late" select="string-length('abc')"/>
<xsl:variable name="mf" select="$m?f"/><xsl:variable name="hg" select="$f('g')"/>
<xsl:variable name="h1" select="$f(1)"/><xsl:variable name="hm" select="$mf(1)"/><xsl:variable name="hgv" select="$g(1)"/><xsl:variable name="hp" select="$p(1)"/>
<xsl:variable name="late2" select="concat('x', 'y')"/>
<xsl:function name="p:k"><xsl:param name="x"/><xsl:sequence select="$f($x)"/></xsl:function>`
	// A prong that reads the document and calls host code (a global whose
	// value calls it, forced there: a dynamic call on a function variable is
	// not motionless), one that forces a global after it, and a global forced
	// after the fork.
	fork := func(call string) string {
		return `<out><xsl:fork><xsl:sequence><a><xsl:value-of select="count(//rec)"/><xsl:value-of select="` + call + `"/></a></xsl:sequence>` +
			`<xsl:sequence><b><xsl:value-of select="sum(//rec/@v)"/><xsl:value-of select="$late2"/></b></xsl:sequence></xsl:fork><c v="{$late}"/></out>`
	}
	type route struct {
		name, body string
		static     bool
		params     map[string]xdm.Sequence
		merge      bool
	}
	withMap := map[string]xdm.Sequence{"f": host}
	b := xdm.NewMapBuilder()
	if err := b.Set(xdm.NewString("f"), host); err != nil {
		t.Fatal(err)
	}
	withMap["m"] = xdm.One(b.Build())
	routes := []route{
		{"static parameter", fork("$h1"), true, nil, false},
		{"parameter", fork("$h1"), false, map[string]xdm.Sequence{"f": host}, false},
		{"map in a parameter", fork("$hm"), false, withMap, false},
		{"global forced in a prong", fork("$hg"), false, map[string]xdm.Sequence{"f": host}, false},
		{"global variable", fork("$hgv"), false, map[string]xdm.Sequence{"f": host}, false},
		{"partial application", fork("$hp"), false, map[string]xdm.Sequence{"f": host}, false},
		{"stylesheet function", fork("p:k(1)"), false, map[string]xdm.Sequence{"f": host}, false},
		{"function reference", fork("p:k#1(1)"), false, map[string]xdm.Sequence{"f": host}, false},
		// One cursor, no fork: the predicate runs on the parse's goroutine.
		{"global forced in a predicate, one cursor", `<out><xsl:value-of select="count(//rec[@v != $hg])"/><c v="{$late}"/></out>`,
			false, map[string]xdm.Sequence{"f": host}, false},
		// A grounded loop body (a fork per record): GroundLater runs it on
		// the parse's goroutine.
		{"global forced in a grounded loop body, one cursor", `<out><xsl:for-each select="doc/rec"><r><xsl:fork><xsl:sequence select="count(sub)"/>` +
			`<xsl:sequence select="string(.)"/><xsl:sequence select="$hg"/></xsl:fork></r></xsl:for-each><c v="{$late}"/></out>`,
			false, map[string]xdm.Sequence{"f": host}, false},
		// (A dynamic call in the select itself is roaming: XTSE3430.)
		{"global forced in a merge predicate", `<out><xsl:merge><xsl:merge-source name="A" for-each-source="'a.xml'" select="log/e[@v != $hg]">` +
			`<xsl:merge-key select="xs:integer(@t)"/></xsl:merge-source><xsl:merge-source name="B" for-each-source="'b.xml'" select="log/e">` +
			`<xsl:merge-key select="xs:integer(@t)"/></xsl:merge-source><xsl:merge-action><g k="{current-merge-key()}" l="{$late}"/></xsl:merge-action></xsl:merge></out>`,
			false, map[string]xdm.Sequence{"f": host}, true},
	}
	run := func(r route, core bool) (out, errText string, tr streamtrace.Counts) {
		type res struct {
			out, err string
			tr       streamtrace.Counts
		}
		done := make(chan res, 1)
		go func() {
			before := streamtrace.Snapshot()
			var rr res
			defer func() { rr.tr = streamtrace.Snapshot().Sub(before); done <- rr }()
			d := decls
			if r.static {
				d = strings.Replace(d, `<xsl:param name="f" required="yes"/>`, `<xsl:param name="f" static="yes" required="yes"/>`, 1)
			}
			tree, err := xdm.ParseString(coreSheetWith(d, r.body), xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
			if err != nil {
				rr.err = err.Error()
				return
			}
			co := CompileOptions{}
			if r.static {
				co.StaticParams = map[string]xdm.Sequence{"f": host}
			}
			s, err := Compile(tree.Root, co)
			if err != nil {
				rr.err = "COMPILE: " + err.Error()
				return
			}
			docs, _ := NewFileResolver(dir)
			streamtrace.SetOff(!core)
			defer streamtrace.SetOff(false)
			res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs, Params: r.params})
			if err != nil {
				rr.err = err.Error()
				return
			}
			rr.out = res.String()
		}()
		select {
		case rr := <-done:
			return rr.out, rr.err, rr.tr
		case <-time.After(20 * time.Second):
			t.Fatalf("%s: the transform hangs (core %v)", r.name, core)
		}
		return
	}
	for _, r := range routes {
		so, se, st := run(r, true)
		to, te, _ := run(r, false)
		if so != to || se != te {
			t.Errorf("%s:\nstreamed %q %q\ntree     %q %q", r.name, so, se, to, te)
		}
		ran := st.Kinds[streamtrace.KindFork] > 0
		if strings.HasSuffix(r.name, "one cursor") {
			ran = st.Runs[streamtrace.Core] > 0
		}
		if r.merge {
			ran = st.Runs[streamtrace.Merge] > 0
		}
		t.Logf("%s: streamed %v, re-runs %d, grounded %d", r.name, ran, st.Reruns, st.Kinds[streamtrace.KindGround])
		if strings.Contains(r.name, "grounded") && st.Kinds[streamtrace.KindGround] == 0 {
			t.Errorf("%s: the loop body was not grounded", r.name)
		}
		if !ran && r.name != "stylesheet function" && r.name != "function reference" {
			t.Errorf("%s: the tee or merge did not run (%+v)", r.name, st)
		}
	}
}

// TestStreamDynamicCallTargets: a dynamic call on a variable is motionless
// only when the variable is provably a map or an array (mapCalls): a
// variable may hold a function item that reads the focus or the stream.
// accumulator-061's shape -- $aa bound to accumulator-after#1, called on a
// streamed node in a rule -- streams (844cd8f5: the call reads nothing of
// its focus) and gives TransformReader's own tree fallback's answer, with no
// guard hit (it streamed, and failed with "a context read twice", before the
// fix). That answer is XTDE3350, the function item saving an absent focus on
// a streamed input (§10.3.6, be09e991); Transform of a tree gives a value
// (TestAccumDynamicCallStreamedInput), so the rows are globalItemRow's. On the core (a
// source-document container; TransformReader refuses map-valued globals by
// its own rule), a map or array variable's lookup, declared by type, by
// constructor or by xsl:map, still streams, and a function variable's call
// does not.
func TestStreamDynamicCallTargets(t *testing.T) {
	accum := `<xsl:accumulator name="p:n" as="xs:decimal" initial-value="0" streamable="yes">
<xsl:accumulator-rule match="chap" select="0"/><xsl:accumulator-rule match="fig" select="$value + 1"/></xsl:accumulator>
<xsl:mode streamable="yes" on-no-match="shallow-skip" use-accumulators="p:n"/>`
	doc := `<doc><chap><fig/><fig><diag/></fig></chap><chap><fig/></chap></doc>`
	for _, c := range []struct {
		decls  string
		stream bool
	}{
		// The function item's focus is the global context item: absent
		// under TransformReader's streamable initial mode (§2.3.2,
		// streamglobal_l7.go), so the oracle is TransformReader's fallback
		// and both raise XTDE3350; the call reads nothing of the stream and
		// streams (844cd8f5, streamtee_dyncall.go).
		{globalItemRow + `<xsl:param name="aa" select="accumulator-after#1"/>` + accum +
			`<xsl:template match="fig"><p n="{accumulator-before('p:n')}"/><xsl:apply-templates/><p><xsl:value-of select="../$aa('p:n')"/></p></xsl:template>`, true},
		{globalItemRow + `<xsl:param name="aa" select="accumulator-before#1"/>` + accum +
			`<xsl:template match="fig"><p><xsl:value-of select="$aa('p:n')"/></p></xsl:template>`, true},
		{`<xsl:param name="nm" select="name#1"/><xsl:mode streamable="yes" on-no-match="shallow-skip"/>` +
			`<xsl:template match="fig"><p n="{$nm(.)}"/></xsl:template>`, false},
		// A param without a map type may be given a function: refused.
		{`<xsl:param name="m" select="map{'fig': 1}"/><xsl:mode streamable="yes" on-no-match="shallow-skip"/>` +
			`<xsl:template match="fig"><p n="{$m(name())}"/></xsl:template>`, false},
	} {
		sheet := mainSheet(c.decls)
		opts := TransformOptions{}
		so, st := mainRun(sheet, doc, true, opts)
		to, _ := mainRun(sheet, doc, false, opts)
		if so != to {
			t.Errorf("%s:\nstreamed %s\ntree     %s", c.decls, so, to)
		}
		if st.Guards != 0 {
			t.Errorf("%s: %d guard hits", c.decls, st.Guards)
		}
		if got := st.Runs[streamtrace.Main] > 0; got != c.stream {
			t.Errorf("%s: streamed %v, want %v", c.decls, got, c.stream)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
		t.Fatal(err)
	}
	loop := `<out><xsl:for-each select="doc/rec"><r n="{CALL}"/></xsl:for-each></out>`
	for _, c := range []struct {
		decls, call string
		stream      bool
	}{
		{`<xsl:param name="m" as="map(*)" select="map{'a': 1}"/>`, `$m(string(@k))`, true},
		{`<xsl:variable name="m" select="map{'a': 2, 'b': 3}"/>`, `$m(string(@k))`, true},
		{`<xsl:variable name="m"><xsl:map><xsl:map-entry key="'b'" select="4"/></xsl:map></xsl:variable>`, `$m(string(@k))`, true},
		{`<xsl:variable name="a" select="['x', 'y']"/>`, `$a(1)`, true},
		{`<xsl:variable name="a" as="array(*)" select="['x']"/>`, `$a(xs:integer(@v))`, true}, // FOAY0001 at v="2"
		{`<xsl:param name="nm" select="name#1"/>`, `$nm(.)`, false},
		{`<xsl:param name="m" select="map{'a': 1}"/>`, `$m(string(@k))`, false},
		// A range variable of the same name is not the map.
		{`<xsl:variable name="m" select="map{'a': 1}"/>`, `for $m in name#1 return $m(.)`, false},
	} {
		where := c.decls + " " + c.call
		coreCheck(t, coreSheetWith(c.decls, strings.Replace(loop, "CALL", c.call, 1)), dir, where, c.stream, TransformOptions{})
	}
}
