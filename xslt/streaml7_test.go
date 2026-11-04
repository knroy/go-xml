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
	// doc() of another URI: another tree in both modes, so it streams.
	{`<xsl:variable name="g" select="doc('other.xml')/doc/rec"/>`, `<out><xsl:value-of select="count(/doc/rec union $g)"/></out>`, true},
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
	// root() of a streamed node stays unmodelled.
	{`<xsl:for-each select="a"><xsl:value-of select="count(root()//b)"/></xsl:for-each>`, props{}, false},
	// fold-right over a grounded, consuming first argument (§19.8.9.9).
	{`<xsl:value-of select="fold-right(a/b/string(), '', concat#2)"/>`, props{postureGrounded, sweepConsuming}, true},
	// Any other first argument stays unmodelled.
	{`<xsl:value-of select="fold-right((), 'd', concat#2)"/>`, props{}, false},
	{`<xsl:value-of select="fold-right(a/b, '', concat#2)"/>`, props{}, false},
}

// TestStreamL7MatchesTree runs l7Rows on every core document and
// whitespace declaration against the tree.
func TestStreamL7MatchesTree(t *testing.T) {
	// streamable-116: a streamed principal input in a streamable mode has an
	// absent global context item (§2.3.2): XPDY0002 from TransformReader,
	// streamed or not; Transform of a tree keeps the source as the item.
	g := mainSheet(`<xsl:mode streamable="yes"/><xsl:variable name="g" select="count(//rec)"/><xsl:template match="rec"><r g="{$g}"/></xsl:template>`)
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
	const base = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" name="urn:base" package-version="1.0.0" version="3.0">
		<xsl:accumulator name="ac" initial-value="0" streamable="yes"><xsl:accumulator-rule match="*" select="$value + 1"/></xsl:accumulator>
	</xsl:package>`
	const top = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" name="urn:top" package-version="1.0.0" version="3.0">
		<xsl:use-package name="urn:base" package-version="1.0.0"/>
		<xsl:accumulator name="ac" initial-value="0" streamable="yes"><xsl:accumulator-rule match="*" select="$value - 1"/></xsl:accumulator>
		<xsl:template name="main" visibility="public">
			<xsl:source-document streamable="yes" href="doc.xml" use-accumulators="ac">
				<b n="{count(//*)}" acc="{accumulator-after('ac')}"/>
			</xsl:source-document>
		</xsl:template>
	</xsl:package>`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(core bool) (string, streamtrace.Counts) {
		before := streamtrace.Snapshot()
		defer func() { noBursts = false }()
		tree, err := xdm.ParseString(top, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{PackageResolver: fixedPackages{"urn:base": base}})
		if err != nil {
			t.Fatalf("compile: %v", err)
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
		t.Errorf("core %q\ntree %q", so, to)
	}
	if st.Runs[streamtrace.Core] == 0 {
		t.Error("not on the core")
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
