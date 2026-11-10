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

// l7Rows are lane L7's container bodies, with the top-level declarations
// they need; stream says the core must take them.
var l7Rows = []struct {
	decl, body string
	stream     bool
}{
	// A non-streamable accumulator read over the streamed document:
	// XTDE3362 in both modes (nonStreamableAccumulator).
	{`<xsl:accumulator name="na" initial-value="0" streamable="no"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`,
		`<out><xsl:value-of select="accumulator-before('na')"/><xsl:value-of select="count(//rec)"/></out>`, true},
	{`<xsl:accumulator name="na" initial-value="0" streamable="no"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`,
		`<out><xsl:value-of select="count(//rec)"/><xsl:value-of select="accumulator-after('na')"/></out>`, true},
	// xsl:copy of an attribute of the stream against a type (si-copy-116,
	// -117): a detached copy is assessed (streamvalid_l7.go).
	{``, `<o><xsl:copy select="doc/rec[1]/@v" type="xs:decimal"/></o>`, true},
	{``, `<o><xsl:copy select="doc/rec[1]/@k" type="xs:date"/></o>`, true},
	{``, `<o><xsl:copy select="/*/*/@v" type="xs:decimal"/></o>`, true},
	// A for-each over a union of two paths of child steps at one depth,
	// its body absorbing "." with || (sx-union-315; disjointDepthPaths).
	{``, `<out><xsl:for-each select="/doc/rec union /doc/other"><xsl:value-of select=". || '|'"/></xsl:for-each></out>`, true},
	// copy-of() of the attributes a climbing tail reaches (sx-square-array-135;
	// viewLeafCopyOK).
	{``, `<out><xsl:value-of select="//sub/ancestor-or-self::*/@*/copy-of()" separator="|"/></out>`, true},
	// xsl:perform-sort over a grounded consuming select (si-perform-sort-001,
	// -002; performSortOp); over streamed nodes it stays on the tree.
	{``, `<out><xsl:perform-sort select="copy-of(doc/rec)"><xsl:sort select="@k"/><xsl:sort select="@v" order="descending"/></xsl:perform-sort></out>`, true},
	{``, `<out><xsl:perform-sort select="//rec/@k/string()"><xsl:sort select="."/></xsl:perform-sort></out>`, true},
	{``, `<out><xsl:perform-sort select="doc/rec"><xsl:sort select="@k"/></xsl:perform-sort></out>`, false},
	// xsl:apply-templates in an xsl:map in a sequence-typed variable
	// (si-map-008; mapContent).
	{`<xsl:mode name="mp" streamable="yes"/><xsl:template match="rec" mode="mp"><xsl:map><xsl:map-entry key="'k'" select="string(@k)"/></xsl:map></xsl:template>`,
		`<out><xsl:for-each select="doc/rec"><xsl:variable name="m" as="map(*)"><xsl:map><xsl:apply-templates select="." mode="mp"/></xsl:map></xsl:variable><r k="{$m('k')}"/></xsl:for-each></out>`, true},
	// A variable holding doc() nodes of the document streamed: in tree mode
	// they are the source-document's own (the resolver's cache), so the
	// container stays on the tree (streamalias_l7.go; L5's finding).
	{`<xsl:variable name="g" select="doc('doc.xml')/doc/rec"/>`, `<out><xsl:value-of select="count(/doc/rec intersect $g)"/></out>`, false},
	{`<xsl:variable name="g" select="doc('doc.xml')/doc/rec"/>`, `<out><xsl:value-of select="count(/doc/rec union $g)"/></out>`, false},
	{``, `<xsl:variable name="g" select="doc('doc.xml')//sub"/><out><xsl:copy-of select="//sub except $g"/></out>`, false},
	// Lane MM1: only parsed expressions count (separator="|" is text), and
	// an identity operator over a load's nodes alone cannot meet the stream
	// (sf-deep-equal, sf-snapshot-0102); a function returning them to meet
	// the stream still refuses.
	{`<xsl:variable name="g" select="doc('doc.xml')/doc/rec"/>`, `<out><xsl:value-of select="count($g), count(//rec)" separator="|"/></out>`, true},
	{`<xsl:variable name="g" select="doc('doc.xml')/doc/rec"/>`, `<out><xsl:value-of select="count(//rec), count($g intersect $g[1]), $g[1] is $g[1]"/></out>`, true},
	{`<xsl:function name="p:id"><xsl:param name="n"/><xsl:sequence select="$n"/></xsl:function><xsl:variable name="g" select="p:id(doc('doc.xml')/doc/rec)"/>`,
		`<out><xsl:value-of select="count(/doc/rec intersect $g)"/></out>`, false},
	// doc() of another URI: another tree in both modes, so it streams.
	{`<xsl:variable name="g" select="doc('other.xml')/doc/rec"/>`, `<out><xsl:value-of select="count(/doc/rec union $g)"/></out>`, true},
	// ... and orders after the streamed document, as the tree's source
	// does, though the body loads it before the stream reaches a match
	// (A1-F2: the stream's tree id is fixed when its cursor opens).
	{``, `<xsl:variable name="v" select="doc('other.xml')/*"/><o><xsl:for-each select="doc/rec"><e><xsl:value-of select="(* union $v)/name()"/></e></xsl:for-each></o>`, true},
	{``, `<xsl:variable name="v" select="doc('other.xml')/*/*"/><o><xsl:for-each select="doc/rec ! (sub union $v)"><e n="{name()}"/></xsl:for-each></o>`, true},
	{``, `<xsl:variable name="v" select="doc('other.xml')/*"/><o><xsl:for-each select="doc/rec"><e b="{. &lt;&lt; $v}"/></xsl:for-each></o>`, true},
	// A path's sort and dedupe, outermost() and innermost() are identity
	// uses: over the stream's nodes and doc() of the streamed URI they stay
	// on the tree (A1-F3); with another URI's, not.
	{``, `<o><xsl:for-each select="doc/rec"><e n="{count((sub, doc('doc.xml')//sub)/.)}"/></xsl:for-each></o>`, false},
	{``, `<o><xsl:for-each select="doc/rec"><e n="{count((sub, doc('doc.xml')//sub)/self::*)}"/></xsl:for-each></o>`, false},
	{``, `<o><xsl:for-each select="doc/rec"><e n="{count((sub, doc('doc.xml')//sub)/..)}"/></xsl:for-each></o>`, false},
	{``, `<o><xsl:for-each select="doc/rec"><e n="{count((sub, doc('doc.xml')//sub)/./@x)}"/></xsl:for-each></o>`, false},
	{``, `<o><xsl:for-each select="doc/rec"><e n="{count(outermost((sub, doc('doc.xml')//sub)))}"/></xsl:for-each></o>`, false},
	{``, `<o><xsl:for-each select="doc/rec"><e n="{count(innermost((sub, doc('doc.xml')//sub)))}"/></xsl:for-each></o>`, false},
	{``, `<o><xsl:for-each select="doc/rec"><xsl:variable name="m" select="(sub, doc('doc.xml')//sub)"/><e n="{count($m/.)}"/></xsl:for-each></o>`, false},
	{``, `<o><xsl:for-each select="doc/rec"><e n="{count((sub, doc('other.xml')//sub)/.)}"/></xsl:for-each></o>`, true},
	// A call given values only, its body instructions (j:escape's shape;
	// streamvaluefn_l7.go): run as itself.
	{`<xsl:function name="p:esc" as="xs:string"><xsl:param name="s" as="xs:string"/><xsl:value-of><xsl:for-each select="string-to-codepoints($s)"><xsl:choose><xsl:when test=". = 97">\a</xsl:when><xsl:otherwise><xsl:value-of select="codepoints-to-string(.)"/></xsl:otherwise></xsl:choose></xsl:for-each></xsl:value-of></xsl:function>`,
		`<out><xsl:for-each select="doc/rec"><r e="{p:esc(@k)}"/></xsl:for-each></out>`, true},
	{`<xsl:function name="p:esc" as="xs:string"><xsl:param name="s" as="xs:string"/><xsl:value-of><xsl:for-each select="string-to-codepoints($s)"><xsl:value-of select=". + 1"/></xsl:for-each></xsl:value-of></xsl:function>`,
		`<out><xsl:value-of select="p:esc(string-join(//rec/@k))"/></out>`, true},
}

// l7Analysis are §19.8 verdicts lane L7 models (streamability_l7.go).
var l7Analysis = []struct {
	src   string
	want  props
	known bool
}{
	// root() of a grounded node: the node's posture and sweep (§19.8.9.18).
	{`<xsl:for-each select="copy-of(a)"><xsl:value-of select="count(root()//b)"/></xsl:for-each>`, props{postureGrounded, sweepConsuming}, true},
	{`<xsl:value-of select="count(root(copy-of(a))//b)"/>`, props{postureGrounded, sweepConsuming}, true},
	// root() of a streamed node whose static type is not known stays
	// unmodelled: the for-each body's context item type is not inferred.
	{`<xsl:for-each select="a"><xsl:value-of select="count(root()//b)"/></xsl:for-each>`, props{}, false},
	// fold-right: the general rules, the first argument navigation
	// (§19.8.9.9), so a streamed first argument roams (§19.8.1).
	{`<xsl:value-of select="fold-right(a/b/string(), '', concat#2)"/>`, props{postureGrounded, sweepConsuming}, true},
	{`<xsl:value-of select="fold-right((), 'd', concat#2)"/>`, props{postureGrounded, sweepMotionless}, true},
	{`<xsl:value-of select="fold-right(a/b, '', concat#2)"/>`, roamingFreeRanging, true},
}

// l7Containers are §19.8.9.18 verdicts on a whole xsl:source-document body,
// whose context item type is U{document-node()} (§19.2): root(X) of a
// striding X of that static type is rewritten as X (§19.9's worked example;
// sx-path-001's shapes), and of a striding X typed otherwise takes the head()
// form, climbing, from which a child step roams (§19.8.8.9).
var l7Containers = []struct {
	src   string
	want  props
	known bool
}{
	{`<xsl:copy-of select="root()"/>`, props{postureGrounded, sweepConsuming}, true},
	{`<xsl:copy-of select="root(.)/*"/>`, props{postureGrounded, sweepConsuming}, true},
	{`<xsl:copy-of select="(root(self::node()) treat as document-node())/*"/>`, props{postureGrounded, sweepConsuming}, true},
	{`<xsl:value-of select="count(root()//b)"/>`, props{postureGrounded, sweepConsuming}, true},
	{`<xsl:copy-of select="root(self::element())/*"/>`, roamingFreeRanging, true},
	// A for-each body's context item type is not inferred: unmodelled.
	{`<xsl:for-each select="."><xsl:copy-of select="root()/*"/></xsl:for-each>`, props{}, false},
}

func TestStreamL7RootStaticType(t *testing.T) {
	for _, c := range l7Containers {
		tree, err := xdm.ParseString(`<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
			`<xsl:template name="main"><xsl:source-document streamable="yes" href="in.xml">`+c.src+
			`</xsl:source-document></xsl:template></xsl:transform>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var sd *xdm.Node
		walkElements(firstElement(tree.Root), func(el *xdm.Node) bool {
			if isXSL(el, "source-document") {
				sd = el
			}
			return sd == nil
		})
		p, known := analyzeSequenceConstructor(sd, postureStriding, nil, nil)
		if known != c.known || known && p != c.want {
			t.Errorf("%s: %v/%v known %v, want %v/%v known %v", c.src, p.posture, p.sweep, known, c.want.posture, c.want.sweep, c.known)
		}
	}
}

// TestStreamL7MatchesTree runs l7Rows on every core document and
// whitespace declaration against the tree.
func TestStreamL7MatchesTree(t *testing.T) {
	// streamable-116: a streamed principal input in a streamable mode has an
	// absent global context item (§2.3.2): XPDY0002 from TransformReader,
	// streamed or not; Transform of a tree keeps the source as the item.
	g := mainSheet(globalItemRow + `<xsl:mode streamable="yes"/><xsl:variable name="g" select="count(//rec)"/><xsl:template match="rec"><r g="{$g}"/></xsl:template>`)
	for _, streamed := range []bool{true, false} {
		if out, _ := mainRun(g, coreDocs["flat"], streamed, TransformOptions{}); !strings.Contains(out, "XPDY0002") {
			t.Errorf("TransformReader (streamed %v): %q, want XPDY0002", streamed, out)
		}
	}
	if tree, err := xdm.ParseString(g, xdm.ParseOptions{}); err == nil {
		if s, err := Compile(tree.Root, CompileOptions{}); err == nil {
			src, _ := xdm.ParseString(coreDocs["flat"], xdm.ParseOptions{})
			if res, err := s.Transform(context.Background(), src.Root, TransformOptions{}); err != nil || !strings.Contains(res.String(), `g="3"`) {
				t.Errorf("Transform of a tree: %v %v, want g=\"3\"", res, err)
			}
		}
	}
	// The analysis rows (l7Analysis): verdicts, no run.
	for _, c := range l7Analysis {
		p, known := analyzeInstrSource(t, c.src)
		if known != c.known || known && p != c.want {
			t.Errorf("%s: %v/%v known %v, want %v/%v known %v", c.src, p.posture, p.sweep, known, c.want.posture, c.want.sweep, c.known)
		}
	}
	for name, doc := range coreDocs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "other.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
			t.Fatal(err)
		}
		for di, d := range coreDecls {
			for bi, b := range l7Rows {
				if raceSkip(name, di, bi) {
					continue
				}
				coreCheck(t, coreSheetWith(d+b.decl, b.body), dir, fmt.Sprintf("%s decl %d row %d", name, di, bi), b.stream, TransformOptions{})
			}
		}
	}
}

// TestStreamL7UsePackageTrust: a container in a top-level package that uses
// another package without xsl:override reads its own streamable
// accumulator on the core, with the tree's output (streamtrust_pkg_l7.go;
// override-misc-007's principal container).
func TestStreamL7UsePackageTrust(t *testing.T) {
	const base = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f" name="urn:base" package-version="1.0.0" version="3.0">
		<xsl:accumulator name="ac" initial-value="0" streamable="yes"><xsl:accumulator-rule match="*" select="$value + 1"/></xsl:accumulator>
		<xsl:variable name="cat" select="'a'" visibility="public"/>
		<xsl:function name="f:pick" as="node()?" streamability="filter" visibility="public">
			<xsl:param name="n" as="node()"/><xsl:sequence select="$n[@k = $cat]"/>
		</xsl:function>
		<xsl:template name="use-base" visibility="public">
			<xsl:source-document streamable="yes" href="DOC" use-accumulators="ac"><a v="{accumulator-after('ac')}"/></xsl:source-document>
		</xsl:template>
	</xsl:package>`
	const top = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f" name="urn:top" package-version="1.0.0" version="3.0">
		<xsl:use-package name="urn:base" package-version="1.0.0">USE</xsl:use-package>
		<xsl:accumulator name="ac" initial-value="0" streamable="yes"><xsl:accumulator-rule match="*" select="$value - 1"/></xsl:accumulator>
		<xsl:template name="main" visibility="public">PRE
			<xsl:source-document streamable="yes" href="doc.xml" use-accumulators="ac">BODY</xsl:source-document>
		</xsl:template>
	</xsl:package>`
	// override-misc-007: the module's own accumulator; su-filter-102: a
	// function the module overrides, inlined in this package's scope.
	over := `<xsl:override><xsl:function name="f:pick" as="node()?" streamability="filter" visibility="public">` +
		`<xsl:param name="n" as="node()"/><xsl:sequence select="$n[not(@k = $cat)]"/></xsl:function></xsl:override>`
	for i, c := range []struct {
		use, body, pre string
		runs           int64 // core runs wanted; 0: at least one
	}{
		{``, `<b><c n="{count(//*)}"/><a v="{accumulator-after('ac')}"/></b>`, ``, 0},
		{over, `<b><xsl:value-of select="doc/rec ! f:pick(.) / sub/@x"/></b>`, ``, 0},
		// su-filter-101: the used package's own f:pick, run as itself
		// (streampkgfn_l7.go), its $cat the package's.
		{``, `<b><xsl:value-of select="doc/rec ! f:pick(.) / sub/@x"/></b>`, ``, 0},
		// override-misc-007's use-base: the used package's own container
		// reads the package's own ac, not the top's (streamtrust_pkguse.go).
		{``, `<b><a v="{accumulator-after('ac')}"/></b>`, `<xsl:call-template name="use-base"/>`, 2},
	} {
		src := strings.Replace(strings.Replace(strings.Replace(top, "USE", c.use, 1), "BODY", c.body, 1), "PRE", c.pre, 1)
		dir := t.TempDir()
		base := strings.Replace(base, "DOC", fileURIOf(filepath.Join(dir, "doc.xml")), 1)
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
			t.Fatal(err)
		}
		run := func(core bool) (string, streamtrace.Counts) {
			before := streamtrace.Snapshot()
			defer func() { noBursts = false }()
			tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
			if err != nil {
				t.Fatal(err)
			}
			s, err := Compile(tree.Root, CompileOptions{PackageResolver: fixedPackages{"urn:base": base}})
			if err != nil {
				return "COMPILE " + err.Error(), streamtrace.Snapshot().Sub(before)
			}
			docs, _ := NewFileResolver(dir)
			noBursts = !core
			res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
			if err != nil {
				return "ERR " + err.Error(), streamtrace.Snapshot().Sub(before)
			}
			return res.String(), streamtrace.Snapshot().Sub(before)
		}
		so, st := run(true)
		to, _ := run(false)
		if so != to {
			t.Errorf("case %d: core %q\ntree %q", i, so, to)
		}
		if n := st.Runs[streamtrace.Core]; n == 0 || c.runs != 0 && n != c.runs {
			t.Errorf("case %d: %d runs on the core, want %d", i, n, c.runs)
		}
	}
}

// TestStreamL7ImportTrust: a streamable mode whose rules are spread over an
// importing and an imported module, joined by xsl:apply-imports, streams
// with the tree's output when every rule verifies (streamtrust_xmod_l7.go;
// si-apply-imports-068..070); a rule in the imported module that reads a
// sibling keeps it on the tree.
func TestStreamL7ImportTrust(t *testing.T) {
	const hdr = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`
	cases := []struct {
		imported, mode string
		stream         bool
	}{
		{hdr + `<xsl:mode streamable="yes"/><xsl:template match="rec"><i k="{@k}"><xsl:value-of select="."/></i></xsl:template></xsl:stylesheet>`, ``, true},
		{hdr + `<xsl:mode streamable="no"/><xsl:template match="rec"><i k="{@k}"><xsl:value-of select="."/></i></xsl:template></xsl:stylesheet>`, `<xsl:mode streamable="yes"/>`, true},
		{hdr + `<xsl:mode streamable="yes"/><xsl:template match="rec"><i><xsl:value-of select="following-sibling::rec[1]/@k"/></i></xsl:template></xsl:stylesheet>`, ``, false},
	}
	for i, c := range cases {
		dir := t.TempDir()
		for n, s := range map[string]string{"doc.xml": coreDocs["flat"], "a.xsl": c.imported} {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		main := hdr + `<xsl:import href="a.xsl"/>` + c.mode +
			`<xsl:template match="rec"><o p="{position()}"><xsl:apply-imports/></o></xsl:template>` +
			`<xsl:template name="main"><out><xsl:source-document streamable="yes" href="doc.xml"><xsl:apply-templates select="doc/rec"/></xsl:source-document></out></xsl:template></xsl:stylesheet>`
		run := func(core bool) (string, streamtrace.Counts) {
			before := streamtrace.Snapshot()
			defer func() { noBursts = false }()
			base := fileURIOf(filepath.Join(dir, "s.xsl"))
			tree, err := xdm.ParseString(main, xdm.ParseOptions{BaseURI: base})
			if err != nil {
				t.Fatal(err)
			}
			docs, _ := NewFileResolver(dir)
			s, err := Compile(tree.Root, CompileOptions{BaseURI: base, Resolver: docs})
			if err != nil {
				return "COMPILE " + err.Error(), streamtrace.Snapshot().Sub(before)
			}
			noBursts = !core
			res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
			if err != nil {
				return "ERR " + err.Error(), streamtrace.Snapshot().Sub(before)
			}
			return res.String(), streamtrace.Snapshot().Sub(before)
		}
		so, st := run(true)
		to, _ := run(false)
		if so != to {
			t.Errorf("case %d: core %q\ntree %q", i, so, to)
		}
		if got := st.Runs[streamtrace.Core] > 0; got != c.stream {
			t.Errorf("case %d: on the core %v, want %v", i, got, c.stream)
		}
	}
}

// TestStreamL7PackageFrame: a call at the document node on a used
// package's absorbing function whose body applies templates in the
// package's own streamable mode, through a one-call wrapper -- the shape of
// use-package-150's j:xml-to-json -- runs as a cross-package call frame on
// the core with the tree's output (streampkgframe_l7.go). An xsl:override
// in the xsl:use-package keeps it on the tree.
func TestStreamL7PackageFrame(t *testing.T) {
	const pkg = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema"
		xmlns:fn="http://www.w3.org/2005/xpath-functions" xmlns:j="urn:j" name="urn:j" package-version="1.0" version="3.0"
		exclude-result-prefixes="xs fn j">
		<xsl:variable name="quot" visibility="public">"</xsl:variable>
		<xsl:mode name="no-indent" streamable="yes" visibility="public"/>
		<xsl:function name="j:conv" as="xs:string" visibility="public" streamability="absorbing">
			<xsl:param name="input" as="node()"/>
			<xsl:sequence select="j:conv($input, map{})"/>
		</xsl:function>
		<xsl:function name="j:conv" as="xs:string" visibility="public" streamability="absorbing">
			<xsl:param name="input" as="node()"/>
			<xsl:param name="options" as="map(*)"/>
			<xsl:choose>
				<xsl:when test="$options('indent') eq true()"><xsl:apply-templates select="$input" mode="no-indent"/></xsl:when>
				<xsl:otherwise><xsl:apply-templates select="$input" mode="no-indent"/></xsl:otherwise>
			</xsl:choose>
		</xsl:function>
		<xsl:template match="/" mode="no-indent"><xsl:apply-templates mode="#current"/></xsl:template>
		<xsl:template match="fn:map" mode="no-indent">
			<xsl:value-of><xsl:text>{</xsl:text><xsl:for-each select="*"><xsl:if test="position() gt 1"><xsl:text>,</xsl:text></xsl:if>
			<xsl:value-of select="concat($quot, j:esc(string(@key)), $quot)"/><xsl:text>:</xsl:text><xsl:apply-templates select="." mode="#current"/></xsl:for-each><xsl:text>}</xsl:text></xsl:value-of>
		</xsl:template>
		<xsl:template match="fn:string" mode="no-indent"><xsl:sequence select="concat($quot, j:esc(.), $quot)"/></xsl:template>
		<xsl:template match="fn:number" mode="no-indent"><xsl:value-of select="xs:string(xs:double(.))"/></xsl:template>
		<xsl:template match="text()[not(normalize-space())]" mode="no-indent"/>
		<xsl:function name="j:esc" as="xs:string" visibility="final">
			<xsl:param name="in" as="xs:string"/>
			<xsl:value-of><xsl:for-each select="string-to-codepoints($in)"><xsl:choose>
				<xsl:when test=". = 34">\"</xsl:when><xsl:otherwise><xsl:value-of select="codepoints-to-string(.)"/></xsl:otherwise>
			</xsl:choose></xsl:for-each></xsl:value-of>
		</xsl:function>
	</xsl:package>`
	const doc = `<map xmlns="http://www.w3.org/2005/xpath-functions"><string key="a">x"y</string> <number key="n">1</number><map key="m"><string key="s">t</string></map></map>`
	for i, c := range []struct {
		use    string
		stream bool
	}{
		{``, true},
		{`<xsl:override><xsl:variable name="quot" visibility="public">"</xsl:variable></xsl:override>`, false},
	} {
		top := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:j="urn:j" version="3.0" exclude-result-prefixes="j">
			<xsl:use-package name="urn:j" package-version="1.0">` + c.use + `</xsl:use-package>
			<xsl:mode name="t" streamable="yes"/>
			<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml"><out><xsl:sequence select="j:conv(.)"/></out></xsl:source-document></xsl:template>
		</xsl:stylesheet>`
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		run := func(core bool) (string, streamtrace.Counts) {
			before := streamtrace.Snapshot()
			defer func() { noBursts = false }()
			tree, err := xdm.ParseString(top, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
			if err != nil {
				t.Fatal(err)
			}
			s, err := Compile(tree.Root, CompileOptions{PackageResolver: fixedPackages{"urn:j": pkg}})
			if err != nil {
				return "COMPILE " + err.Error(), streamtrace.Snapshot().Sub(before)
			}
			docs, _ := NewFileResolver(dir)
			noBursts = !core
			res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
			if err != nil {
				return "ERR " + err.Error(), streamtrace.Snapshot().Sub(before)
			}
			return res.String(), streamtrace.Snapshot().Sub(before)
		}
		so, st := run(true)
		to, _ := run(false)
		if so != to {
			t.Errorf("case %d: core %q\ntree %q", i, so, to)
		}
		if got := st.Runs[streamtrace.Core] > 0 && st.Reruns == 0; got != c.stream {
			t.Errorf("case %d: on the core without a re-run %v, want %v", i, got, c.stream)
		}
	}
}
