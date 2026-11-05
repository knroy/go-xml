package xslt

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// readOnly hides every method of a reader but Read: TransformReader is
// handed what cannot be sought or measured.
type readOnly struct{ r io.Reader }

func (o readOnly) Read(p []byte) (int, error) { return o.r.Read(p) }

// mainRun runs sheet over doc through TransformReader (streamed) or through
// xdm.Parse and Transform, and describes the result: its serialisation, or
// the error, then the shape of Result.Nodes.
func mainRun(sheet, doc string, streamed bool, opts TransformOptions) (out string, tr streamtrace.Counts) {
	before := streamtrace.Snapshot()
	defer func() { tr = streamtrace.Snapshot().Sub(before) }()
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: "file:///s/s.xsl"})
	if err != nil {
		return "SHEET: " + err.Error(), tr
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		return "COMPILE: " + err.Error(), tr
	}
	var res *Result
	if streamed {
		res, err = s.TransformReader(context.Background(), readOnly{strings.NewReader(doc)}, opts)
	} else if strings.Contains(sheet, globalItemRow) {
		// A row that reads the global context item (§2.3.2): the oracle is
		// TransformReader's own fallback, Parse + Transform with the item
		// absent for a streamable initial mode (streamglobal_l7.go).
		noBursts = true
		res, err = s.TransformReader(context.Background(), readOnly{strings.NewReader(doc)}, opts)
		noBursts = false
	} else {
		// Every other row: the plain tree path.
		var src *xdm.Tree
		if src, err = xdm.Parse(strings.NewReader(doc), opts.SourceParseOptions); err == nil {
			res, err = s.Transform(context.Background(), src.Root, opts)
		}
	}
	if err != nil {
		return "error: " + err.Error(), tr
	}
	out = res.String() + "\n" + nodesShape(res.Nodes)
	for i := range res.Secondary {
		sr := &res.Secondary[i]
		out += "\n" + sr.Href + ": " + sr.String()
	}
	return out, tr
}

// nodesShape describes each item of nodes: kind, name, value, parent, root,
// and which other items it is, or is a sibling of (TestStreamResultNodes).
func nodesShape(nodes xdm.Sequence) string {
	var b strings.Builder
	for i, it := range nodes {
		n, ok := it.(*xdm.Node)
		if !ok {
			fmt.Fprintf(&b, "[%d atomic %v]", i, it)
			continue
		}
		fmt.Fprintf(&b, "[%d %v %s %q base %q doc %q", i, n.Kind(), n.Name().Local, n.StringValue(), n.BaseURI(), n.DocumentURI())
		if p := n.Parent(); p != nil {
			fmt.Fprintf(&b, " parent %v %s", p.Kind(), p.Name().Local)
		}
		for j, o := range nodes {
			if m, ok := o.(*xdm.Node); ok && j != i && (n.Is(m) || j < i && n.Parent() != nil && m.Parent() != nil && n.Parent().Is(m.Parent())) {
				fmt.Fprintf(&b, " with %d", j)
			}
		}
		b.WriteString("]")
	}
	return b.String()
}

// globalItemRow marks a stylesheet whose globals read the global context
// item: mainRun's tree leg for it is TransformReader's fallback, not Parse +
// Transform (§2.3.2, streamglobal_l7.go).
const globalItemRow = "<!--2.3.2-->"

// mainSheet is a stylesheet of declarations decls.
func mainSheet(decls string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p" exclude-result-prefixes="xs p">` + decls + `</xsl:stylesheet>`
}

// mainCases are stylesheets a principal input is applied to in the unnamed
// mode; stream says TransformReader must stream it.
var mainCases = []struct {
	decls  string
	stream bool
}{
	// The identity transform, a filter, rules for elements and text.
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="sub"/>` +
		`<xsl:template match="rec[@k = 'a']"><a v="{@v}"><xsl:apply-templates/></a></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><r k="{@k}" p="{position()}"><xsl:apply-templates/></r></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><o b="{base-uri(.)}"><xsl:apply-templates/></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><xsl:copy><xsl:apply-templates/></xsl:copy></xsl:template>` +
		`<xsl:template match="*"><xsl:copy><xsl:copy-of select="@*"/><xsl:apply-templates/></xsl:copy></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><o><xsl:value-of select="count(.//rec)"/></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><o><xsl:value-of select="sum(//rec/@v)"/></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><xsl:copy-of select="."/></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="fail"/><xsl:template match="/|doc|rec"><xsl:apply-templates/></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="deep-skip"/><xsl:template match="/"><o><xsl:apply-templates/></o></xsl:template>` +
		`<xsl:template match="doc|rec"><xsl:apply-templates/></xsl:template><xsl:template match="sub"><s x="{@x}"/></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:param name="g" select="'G'"/><xsl:template match="rec"><r g="{$g}"/></xsl:template>`, true},
	// A global that reads the context item: the global context item is
	// absent for a streamed principal input (§2.3.2; streamglobal_l7.go), so
	// XPDY0002 in both legs (streamable-116). An accumulator streams since
	// M4 (streamaccum.go).
	{globalItemRow + `<xsl:mode streamable="yes"/><xsl:variable name="g" select="count(//rec)"/><xsl:template match="rec"><r g="{$g}"/></xsl:template>`, true},
	{globalItemRow + `<xsl:mode streamable="yes"/><xsl:variable name="g"><xsl:value-of select="name(/*)"/></xsl:variable><xsl:template match="rec"><r g="{$g}"/></xsl:template>`, true},
	// An xsl:global-context-item: the item is absent for a streamed
	// principal input (§2.3.2, 6cc2247f), so use="required" is XTDE3086
	// (§3.5.6, glob-cxt-item-012) before any run, and a type is checked
	// against nothing, in both legs; use="absent" streams.
	{globalItemRow + `<xsl:mode streamable="yes"/><xsl:global-context-item use="required"/><xsl:template match="rec"><r/></xsl:template>`, false},
	{`<xsl:mode streamable="yes"/><xsl:global-context-item use="absent"/><xsl:template match="rec"><r/></xsl:template>`, true},
	{globalItemRow + `<xsl:mode streamable="yes"/><xsl:global-context-item as="document-node(element(doc))"/><xsl:template match="rec"><r/></xsl:template>`, false},
	{`<xsl:mode streamable="yes"/><xsl:accumulator name="a" initial-value="0" streamable="yes"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`, true},
	// At the top of the principal result the tree keeps the source's own
	// nodes: the built-ins' copies of leaves, a deep-copy, an attribute rule.
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:apply-templates/></xsl:template>`, false},
	{`<xsl:mode streamable="yes" on-no-match="deep-copy"/><xsl:template match="/"><xsl:apply-templates/></xsl:template>`, false},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:apply-templates select="*"/></xsl:template>` +
		`<xsl:template match="/*"><xsl:apply-templates select="@*"/></xsl:template>`, false},
	// What a rule writes is its own: a copy, or a new node, in both
	// (§19.8: an attribute rule may not return its node). Inside a literal
	// result element nothing is at the top.
	{`<xsl:mode streamable="yes" on-no-match="shallow-skip"/><xsl:template match="@k"><xsl:copy-of select="."/></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-skip"/><xsl:template match="@k|text()"><xsl:copy/></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><o><xsl:apply-templates/></o></xsl:template>`, true},
	// The built-in deep-copy of the document node is its tree (linkApply
	// asks it of the document node whatever rule matches it).
	{`<xsl:mode streamable="yes" on-no-match="deep-copy"/><xsl:template match="/*"><o><xsl:apply-templates/></o></xsl:template>`, false},
	// A max at the document node may give up for the tree to answer.
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><o><xsl:value-of select="max(//rec/@v)"/></o></xsl:template>`, false},
	// A secondary result holds the items of its body as they are, as
	// Result.Nodes does: a node of the stream there is not the tree's, and
	// a principal input has no tree to re-run on. A copy (a built-in's of a
	// leaf too, as in a container), or a node inside a literal result
	// element, is the result's own.
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><xsl:result-document href="r.xml"><xsl:sequence select="."/></xsl:result-document></xsl:template>`, false},
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><xsl:result-document href="r.xml"><xsl:sequence select="copy-of(.)"/></xsl:result-document></xsl:template>`, false},
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><xsl:result-document href="r.xml"><xsl:sequence select="text()"/></xsl:result-document></xsl:template>`, false},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/*"><xsl:result-document href="r.xml"><xsl:apply-templates select="@*"/></xsl:result-document></xsl:template>`, false},
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><xsl:result-document href="r.xml"><o><xsl:sequence select="."/></o></xsl:result-document></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><xsl:result-document href="r.xml"><xsl:copy-of select="."/></xsl:result-document></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/*"><xsl:result-document href="r.xml"><xsl:apply-templates/></xsl:result-document></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/*"><xsl:result-document href="r.xml"><o><xsl:apply-templates/></o></xsl:result-document></xsl:template>`, true},
	// A dynamic call of a global accumulator-after#1 beside the rule's
	// consumer: the function item's focus is the global context item,
	// absent here, so the call raises XTDE3350 in both runs
	// (streamtee_dyncall.go; accumulator-061).
	{globalItemRow + `<xsl:param name="aa" select="accumulator-after#1"/><xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">` +
		`<xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator><xsl:mode streamable="yes" use-accumulators="n"/>` +
		`<xsl:template match="rec"><r b="{accumulator-before('n')}"><xsl:apply-templates/><e><xsl:value-of select="../$aa('n')"/></e></r></xsl:template>`, true},
	// A grouping over attributes, text or node() children in a rule for the
	// document element: the population a pruned copy of the document built
	// from the events (L5, streamgroup_pruned.go; si-group-024/033/037).
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><o><xsl:for-each-group select="rec/@k" group-adjacent="."><g k="{current-grouping-key()}" v="{current-group()/../@v}"/></xsl:for-each-group></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><o><xsl:for-each-group select="*/text()" group-starting-with="text()[contains(., 't')]"><g><xsl:copy-of select="snapshot(current-group())/.."/></g></xsl:for-each-group></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="rec"><xsl:copy><xsl:for-each-group select="node()" group-adjacent="node-name(.), @x" composite="yes">` +
		`<xsl:copy><xsl:copy-of select="@x"/><xsl:apply-templates select="current-group()/node()"/></xsl:copy></xsl:for-each-group></xsl:copy></xsl:template>`, true},
	// A group-by in xsl:fork over elements whose body applies templates to,
	// or sequences, the group: the members copied with their ancestors
	// (si-fork-802, si-group-205).
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><o><xsl:fork><xsl:for-each-group select="doc/rec" group-by="@k">` +
		`<g k="{current-grouping-key()}"><xsl:apply-templates select="current-group()"/></g></xsl:for-each-group></xsl:fork></o></xsl:template>` +
		`<xsl:template match="rec/@*"><xsl:element name="{name()}"><xsl:value-of select="."/></xsl:element></xsl:template><xsl:template match="rec/@k" priority="5"/>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><o><xsl:fork><xsl:for-each-group select="doc/rec" group-by="@k">` +
		`<g k="{current-grouping-key()}"><xsl:sequence select="current-group()"/></g></xsl:for-each-group></xsl:fork></o></xsl:template>`, true},
	// ... and one whose body reads only the key and values of the group:
	// streamcopy_l3group.go's one fragment (si-fork-809).
	{`<xsl:mode streamable="yes"/><xsl:output method="text"/><xsl:template match="/"><o><xsl:fork><xsl:for-each-group select="doc/rec" composite="yes" group-by="@k, name()">` +
		`<xsl:value-of select="current-grouping-key(), sum(current-group()/@v)" separator=", "/><br/></xsl:for-each-group></xsl:fork></o></xsl:template>`, true},
	// ... refused when the body counts siblings, which the copy prunes. The
	// part of the id after the tree's number ("N<tree>x<order>"): the two
	// legs parse different trees.
	{`<xsl:mode streamable="yes"/><xsl:template match="/*"><o><xsl:for-each-group select="rec/@k" group-adjacent="."><g n="{substring-after(generate-id(current-group()[1]/..), 'x')}"/></xsl:for-each-group></o></xsl:template>`, false},
	// unparsed-entity-uri and -public-id beside the consumer of a rule's
	// element: the rule on a grounded copy, given the view's DTD
	// (streamtee_ground.go; sf-unparsed-entity-01).
	{`<xsl:mode streamable="yes"/><xsl:template match="doc"><o u="{unparsed-entity-uri('pic')}"><p><xsl:value-of select="unparsed-entity-public-id('pic')"/></p>` +
		`<n><xsl:value-of select="count(rec)"/></n></o></xsl:template>`, true},
	// The same in rule "/" before its consumer, a copy of the document
	// element into an element: the run moves to the document element first,
	// past the leaves before it (streamtee_docnode.go; sf-unparsed-entity-05).
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><out><a><xsl:value-of select="unparsed-entity-uri('pic')"/></a>` +
		`<b><xsl:value-of select="unparsed-entity-public-id('pic')"/></b><xsl:copy-of select="*"/></out></xsl:template>`, true},
	// A held select at the document node with at most one match never
	// gives up, so a principal input streams it (streammain_onematch.go;
	// accumulator-046s/047s); one with many matches still may, and is refused.
	{`<xsl:mode streamable="yes" on-no-match="shallow-skip"/><xsl:template match="/"><o><xsl:apply-templates select="copy-of(/doc/rec[2])"/></o></xsl:template>` +
		`<xsl:template match="rec"><r k="{@k}"><xsl:copy-of select="."/></r></xsl:template>`, true},
	{`<xsl:accumulator name="n" initial-value="0" streamable="yes"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>` +
		`<xsl:mode streamable="yes" on-no-match="shallow-skip" use-accumulators="n"/><xsl:template match="/"><o><xsl:apply-templates select="snapshot(/doc/rec[2])"/></o></xsl:template>` +
		`<xsl:template match="rec"><r n="{accumulator-before('n')}"/></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-skip"/><xsl:template match="/"><o><xsl:apply-templates select="copy-of(/doc/rec)"/></o></xsl:template>`, false},
	// A filter over tokenize()'s strings: the predicates read those, never
	// the stream (si-iterate-037).
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><o><xsl:value-of select="//text()!tokenize(., ' ')[matches(., 't')]"/></o></xsl:template>`, true},
	// A group read beside its consuming path, as a map's two copies: refused
	// as the plan readies, the rule runs on a grounded copy of its element
	// (streamtee_ground.go; sx-MapExpr-101, su-absorbing-201).
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="rec"><xsl:copy><xsl:for-each-group select="*" group-starting-with="sub">` +
		`<xsl:variable name="m" select="map{1: copy-of(.), 2: copy-of(current-group()[position() gt 1])}"/><g n="{count($m?2)}"><xsl:copy-of select="$m?1"/></g></xsl:for-each-group></xsl:copy></xsl:template>`, true},
	// A map of two consuming values: the rule runs on a grounded copy of
	// its element (streamtee_ground.go; sx-MapExpr-008).
	{`<xsl:mode streamable="yes"/><xsl:template match="rec"><xsl:variable name="m" select="map{'a': string(sub[1]), 'b': count(sub)}"/><r a="{$m?a}" b="{$m?b}"/></xsl:template>`, true},
	// A rule on a grounded copy (xsl:fork) that reads accumulators: the
	// copy carries the document's values, before and after
	// (streamtee_ground_accum.go; si-fork-816).
	{`<xsl:accumulator name="n" initial-value="0" streamable="yes"><xsl:accumulator-rule match="sub" select="$value + 1"/></xsl:accumulator>` +
		`<xsl:mode streamable="yes" use-accumulators="n"/><xsl:template match="rec"><r n="{accumulator-before('n')}"><xsl:fork><xsl:sequence><xsl:value-of select="string(sub[1])"/></xsl:sequence>` +
		`<xsl:sequence><xsl:for-each select="sub"><s b="{accumulator-before('n')}" a="{accumulator-after('n')}"/></xsl:for-each></xsl:sequence></xsl:fork></r></xsl:template>`, true},
	// A copy-of at the document node whose tail runs on a grounded copy of
	// the document element: a principal input grounds it, as the tree holds
	// it (streammain_rootground.go; function-5004). <doc/> raises XPTY0004
	// in both legs.
	{`<xsl:function name="p:f" as="node()" streamability="filter"><xsl:param name="n" as="node()"/><xsl:sequence select="$n[self::rec]"/></xsl:function>` +
		`<xsl:mode streamable="yes"/><xsl:template match="/"><o><xsl:copy-of select="doc/p:f(rec[1])/sub"/></o></xsl:template>`, true},
	// A rule for a leaf returning a stylesheet function of it, declared
	// atomic: its body, inlined, reads the copy's value only
	// (streamview_leafcall.go, streamcopy_l3leaf.go; su-absorbing-016).
	{`<xsl:function name="p:len" as="xs:integer" streamability="absorbing"><xsl:param name="n" as="node()"/><xsl:sequence select="string-length($n)"/></xsl:function>` +
		`<xsl:mode streamable="yes"/><xsl:template match="sub/text()"><xsl:sequence select="p:len(.)"/></xsl:template>`, true},
	// Each member of a group copied: a grounded member copied by the tail and
	// let go, the copies what the tree makes too (streamgroup_copies.go;
	// si-group-064). The rule's element may be the document element.
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:mode name="c" on-no-match="shallow-copy"/>` +
		`<xsl:template match="rec"><xsl:copy><xsl:for-each-group select="*" group-adjacent="node-name()"><xsl:apply-templates select="current-group()!copy-of()" mode="c"/></xsl:for-each-group></xsl:copy></xsl:template>` +
		`<xsl:template match="*[not(parent::*)]" mode="c"><e n="{name()}" p="{position()}" l="{last()}" b="{base-uri()}"><xsl:copy-of select="@*, node()"/></e></xsl:template>`, true},
	{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:mode name="c" on-no-match="shallow-copy"/>` +
		`<xsl:template match="rec"><xsl:copy><xsl:for-each-group select="*" group-adjacent="node-name()"><xsl:apply-templates select="current-group()!(copy-of(), .)" mode="c"/></xsl:for-each-group></xsl:copy></xsl:template>`, false},
	// A for-each over the ancestors a path climbs to, its body motionless
	// on their views, off the cursor (streampop_climb.go; function-5017/5018);
	// not when the body asks has-children or an accumulator, which only a
	// match's view answers (viewsBeyond: the accumulator row streams without
	// it).
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><o><xsl:for-each select="//sub/ancestor-or-self::*">` +
		`<e n="{name()}" k="{@k}" d="{count(ancestor::*)}" p="{position()}"/></xsl:for-each></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:function name="p:up" as="node()*" streamability="ascent"><xsl:param name="n" as="node()"/>` +
		`<xsl:sequence select="$n/ancestor-or-self::rec"/></xsl:function><xsl:template match="/"><o>` +
		`<xsl:for-each select="doc//sub/p:up(.)"><e k="{@k}" d="{count(ancestor-or-self::rec)}"/></xsl:for-each></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="/"><o><xsl:for-each select="//sub/ancestor::rec">` +
		`<e k="{@k}" h="{has-children()}"/></xsl:for-each></o></xsl:template>`, false},
	{`<xsl:accumulator name="n" initial-value="0" streamable="yes"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>` +
		`<xsl:mode streamable="yes" use-accumulators="n"/><xsl:template match="/"><o><xsl:for-each select="//sub/ancestor::rec">` +
		`<e k="{@k}" n="{accumulator-before('n')}"/></xsl:for-each></o></xsl:template>`, false},
	// An accumulator rule whose value is a sequence constructor, at a text
	// node: its context has no children, as for a select (streamtrust.go
	// accumulatorOK; accumulator-036s).
	{`<xsl:accumulator name="t" initial-value="0" as="xs:integer" streamable="yes"><xsl:accumulator-rule match="sub/text()">` +
		`<xsl:sequence select="$value + string-length(.)"/></xsl:accumulator-rule></xsl:accumulator>` +
		`<xsl:mode streamable="yes" use-accumulators="t"/><xsl:template match="doc"><o><n><xsl:value-of select="count(.//sub)"/></n>` +
		`<t><xsl:value-of select="accumulator-after('t')"/></t></o></xsl:template>`, true},
	// A call at the document node on an absorbing function of the module
	// whose body is an instruction, inlined (streampkgframe_l7.go
	// pkgDocParam; function-5002).
	{`<xsl:mode streamable="yes"/><xsl:function name="p:abs" streamability="absorbing"><xsl:param name="n" as="node()"/>` +
		`<xsl:copy-of select="$n/*/rec[1]/sub"/></xsl:function><xsl:template match="/"><o><xsl:copy-of select="p:abs(.)"/></o></xsl:template>`, true},
	// A call on a function whose body is instructions, given only a copy of
	// the match (streamsplit_copies.go): in a loop's select over snapshot(.);
	// in a tail over copy-of(.) (function-5011); not given the node itself.
	{`<xsl:mode streamable="yes"/><xsl:function name="p:f"><xsl:param name="n"/><r k="{$n/@k}" up="{name($n/..)}">` +
		`<xsl:value-of select="count($n/sub)"/></r></xsl:function>` +
		`<xsl:template match="/*"><o><xsl:for-each select="rec"><xsl:sequence select="p:f(snapshot(.))"/></xsl:for-each></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:function name="p:g" as="node()"><xsl:param name="n"/><new><xsl:copy-of select="$n/sub"/></new></xsl:function>` +
		`<xsl:template match="/"><o><xsl:copy-of select="(*/rec/p:g(copy-of(.))[self::new]/sub)[1]"/></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:function name="p:f"><xsl:param name="n"/><r k="{$n/@k}" up="{name($n/..)}"/></xsl:function>` +
		`<xsl:template match="/*"><o><xsl:for-each select="rec"><xsl:sequence select="p:f(.)"/></xsl:for-each></o></xsl:template>`, false},
	// unparsed-entity-uri#1 and -public-id#1 at an element's view: its
	// tree's DTD is the document's (sf-unparsed-entity-02).
	{`<xsl:mode streamable="yes"/><xsl:template match="doc"><o><xsl:value-of select="unparsed-entity-uri('pic'), unparsed-entity-public-id('pic')"/></o></xsl:template>`, true},
	{`<xsl:mode streamable="yes"/><xsl:template match="doc"><o><xsl:for-each select="rec"><r u="{unparsed-entity-uri('pic')}" k="{@k}"/></xsl:for-each></o></xsl:template>`, true},
	// Not a streamable mode, or a rule the core does not take.
	{`<xsl:template match="rec"><r/></xsl:template>`, false},
	{`<xsl:mode streamable="yes"/><xsl:template match="rec[1]"><f/></xsl:template>`, false},
}

// mainDocs are documents for the main-input cases: mixed content, comments
// and PIs beside the document element, a DOCTYPE.
var mainDocs = map[string]string{
	"flat":    coreDocs["flat"],
	"mixed":   coreDocs["mixed"],
	"ns":      coreDocs["ns"],
	"deep":    coreDocs["deep"],
	"empty":   coreDocs["empty"],
	"recroot": coreDocs["recroot"],
	"doctype": `<!DOCTYPE doc [<!ENTITY e "ent">]><doc><rec k="a" v="1">&e;</rec><!--c--></doc><?tail t?>`,
	"ndata": `<!--c0--><!DOCTYPE doc [<!NOTATION gif PUBLIC "-//g//N" "gif"><!ENTITY pic PUBLIC "-//p//E" "pic.gif" NDATA gif>]>` +
		`<?p0 x?><doc><rec k="a" v="1"/><rec k="b" v="2"/></doc>`,
	"space": "<doc>\n  <rec k=\"a\" v=\"1\">\n    <sub x=\"1\"> </sub>\n  </rec>\n  <rec k=\"b\" v=\"2\" xml:space=\"preserve\">\n    <sub/>\n  </rec>\n</doc>\n",
}

// TestStreamMainMatchesTree: TransformReader gives Parse + Transform's
// result, Result.Nodes and error, over every main case, every apply case's
// rules, every document and whitespace declaration; the main cases that
// must stream do, the others do not, and no guard fires.
func TestStreamMainMatchesTree(t *testing.T) {
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true,
		BaseURI: "file:///d/doc.xml", DocumentURI: "file:///d/doc.xml"}}
	type sheetCase struct {
		decls  string
		stream int // 1 must, 0 must not, -1 either
	}
	var cases []sheetCase
	for _, c := range mainCases {
		st := 0
		if c.stream {
			st = 1
		}
		cases = append(cases, sheetCase{c.decls, st})
	}
	for _, c := range applyCases {
		cases = append(cases, sheetCase{c.decls, -1})
	}
	var why string
	traceMain = func(m, w string) {
		if m == "" {
			why = w
		}
	}
	defer func() { traceMain = nil }()
	streamed := 0
	for name, doc := range mainDocs {
		for _, c := range cases {
			for _, d := range coreDecls {
				sheet := mainSheet(d + c.decls)
				co, ct := mainRun(sheet, doc, true, opts)
				tr, _ := mainRun(sheet, doc, false, opts)
				where := fmt.Sprintf("doc %s, decls %q", name, d+c.decls)
				if co != tr {
					t.Errorf("%s:\nstreamed %s\ntree     %s", where, co, tr)
				}
				if ct.Guards != 0 {
					t.Errorf("%s: %d guard hits", where, ct.Guards)
				}
				if m := ct.Runs[streamtrace.Main]; c.stream == 1 && m != 1 || c.stream == 0 && m != 0 {
					t.Errorf("%s: %d streamed runs (must stream: %v): %s", where, m, c.stream == 1, why)
				}
				streamed += int(ct.Runs[streamtrace.Main])
			}
		}
	}
	t.Logf("%d runs streamed", streamed)
}

// TestStreamMainErrors: a streamed run that fails gives Parse + Transform's
// error: a malformed document or a parse limit after the transform's own
// error is reported, as Parse reports it before the transform starts; the
// transform's errors, its result budgets and depth are the tree's.
func TestStreamMainErrors(t *testing.T) {
	failing := `<xsl:mode streamable="yes" on-no-match="fail"/><xsl:template match="/|doc"><xsl:apply-templates/></xsl:template>`
	typed := `<xsl:mode streamable="yes"/><xsl:template match="rec"><r v="{xs:integer(@v) + 1}"/></xsl:template>`
	div := `<xsl:mode streamable="yes"/><xsl:template match="rec"><r v="{1 idiv (xs:integer(@v) - 2)}"/></xsl:template>`
	deep := `<xsl:mode streamable="yes"/><xsl:template match="*"><e><xsl:apply-templates/></e></xsl:template>`
	identity := `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`
	var deepDoc strings.Builder
	for range 60 {
		deepDoc.WriteString("<a>")
	}
	for range 60 {
		deepDoc.WriteString("</a>")
	}
	docs := []string{
		`<doc><rec k="a" v="1"/><zz/><rec k="b" v="x"/></doc>`,
		`<doc><rec k="a" v="1"/><zz/><rec k="b" v="x"></doc>`,      // malformed after the error
		`<doc><rec k="a" v="1"/><zz/><rec k="b" v="2"/></doc><x/>`, // two document elements
		`<doc><rec k="a" v="1"/><zz/><rec k="b" v="2"/>`,           // unclosed
		`<doc><rec k="a" v="1"/><rec k="b" v="x"/>&undeclared;</doc>`,
		`<!DOCTYPE doc><doc><rec k="b" v="1"/></doc>`, // a DOCTYPE, not allowed
		deepDoc.String(),
		``,
	}
	sheets := []string{failing, typed, div, deep, identity}
	limits := []TransformOptions{
		{},
		{SourceParseOptions: xdm.ParseOptions{MaxNodes: 4}},
		{SourceParseOptions: xdm.ParseOptions{MaxBytes: 30}},
		{SourceParseOptions: xdm.ParseOptions{MaxDepth: 20}},
		{MaxDepth: 30},
		{MaxNodes: 3},
	}
	for _, d := range sheets {
		sheet := mainSheet(d)
		for _, doc := range docs {
			for _, o := range limits {
				co, ct := mainRun(sheet, doc, true, o)
				tr, _ := mainRun(sheet, doc, false, o)
				if co != tr {
					t.Errorf("sheet %s, doc %s, opts %+v:\nstreamed %s\ntree     %s", d, doc, o, co, tr)
				}
				if ct.Runs[streamtrace.Main] != 1 || ct.Guards != 0 {
					t.Errorf("sheet %s, doc %s: trace %+v", d, doc, ct)
				}
			}
		}
	}
}

// TestStreamMainMemory: streaming a principal input takes less live heap
// than Parse + Transform, by the shape's bound; peak live heap of the whole
// transform, document text excluded (both read it from one string).
func TestStreamMainMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("memory test")
	}
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 150000 {
		fmt.Fprintf(&b, `<rec k="%c" v="%d">%d<sub x="%d"/><sub x="1">x</sub></rec>`, 'a'+i%5, i%100, i%10, i%7)
	}
	b.WriteString("</doc>")
	doc := b.String()
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{MaxBytes: -1, MaxNodes: -1}}
	for _, c := range []struct {
		decls string
		frac  float64 // the most of the tree's peak streaming may take
	}{
		{`<xsl:mode streamable="yes"/><xsl:template match="/"><n><xsl:value-of select="count(//rec)"/></n></xsl:template>`, 0.2},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="sub"/><xsl:template match="rec[@k != 'a']"/>`, 0.4},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, 0.9},
	} {
		sheet := mainSheet(c.decls)
		var st streamtrace.Counts
		var co, tr string
		streamed := splitPeak(func() { co, st = mainRun(sheet, doc, true, opts) })
		tree := splitPeak(func() { tr, _ = mainRun(sheet, doc, false, opts) })
		t.Logf("%s: streamed %.1f MB, tree %.1f MB", c.decls, float64(streamed)/1e6, float64(tree)/1e6)
		if st.Runs[streamtrace.Main] != 1 || co != tr {
			t.Errorf("%s: trace %+v, outputs equal %v", c.decls, st, co == tr)
		}
		if float64(streamed) > c.frac*float64(tree) && !raceEnabled {
			t.Errorf("%s: peak live heap %.1f MB, tree %.1f MB (bound %.0f%%)", c.decls,
				float64(streamed)/1e6, float64(tree)/1e6, 100*c.frac)
		}
	}
}
