package xslt

// Nodes of the streamed document reached another way (lane L7; L5's
// finding).
//
// In tree mode xsl:source-document reads its document through the
// resolver's cache, so a node the stylesheet reaches by doc(U), document(),
// collection(), a non-streamed xsl:source-document or xsl:merge-source, or
// fn:transform -- for the URI U a container streams -- is the tree's own
// node: `P intersect $v` has P's nodes, `P union $v` has them once, and
// generate-id() agrees. Streamed, P's nodes are views or copies of another
// tree, so the same expression gives () and 2n. The split's foreign rewrites
// (foreignSets, foreignUnion, foreignPath, mapSetSafe), the grounded bodies'
// identityOperands and every other place that compares a stream's node with
// a node from elsewhere would have to know which variables, functions and
// lookups may hold such nodes; they read only the expression's text.
//
// ponytail: the check is stylesheet-wide and textual. When any module may
// load a document by URI AND writes a node-identity operator (union, |,
// intersect, except, is, <<, >>, generate-id) anywhere, nothing streams: no
// xsl:source-document plan, no rule plan (so no streamed mode), no streamed
// merge. Narrow it to the operators whose operands can reach such nodes
// when that is worth the dataflow.

import (
	"regexp"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

var (
	loadsDocRE    = regexp.MustCompile(`\b(doc|document|collection|transform|function-lookup)\s*[(#]`)
	identityOpsRE = regexp.MustCompile(`\b(union|intersect|except|is)\b|\||<<|>>|generate-id`)
)

// streamMayAlias reports whether a streamed run of this stylesheet may
// compare a node of the stream with the tree's own node for it.
func (c *compiler) streamMayAlias() bool {
	// Used packages compile last and add their modules: recomputed then.
	if c.l7Alias != 0 && c.l7AliasN == len(c.staticDone) {
		return c.l7Alias > 0
	}
	c.l7AliasN = len(c.staticDone)
	roots := map[*xdm.Node]bool{}
	add := func(n *xdm.Node) {
		if n == nil {
			return
		}
		if n.Kind() != xdm.KindElement {
			n = firstElement(n)
		}
		if n != nil {
			roots[n] = true
		}
	}
	if c.sheet != nil {
		add(c.sheet.source)
	}
	for k, v := range c.staticDone {
		add(k)
		add(v)
	}
	loads, ident := false, false
	for r := range roots {
		walkElements(r, func(el *xdm.Node) bool {
			if isXSL(el, "evaluate") || isXSL(el, "use-package") ||
				(isXSL(el, "source-document") || isXSL(el, "merge-source")) && !isYes(el.AttrValue("streamable")) {
				loads = true
			}
			for a := range el.Attrs() {
				v := a.Value()
				loads = loads || loadsDocRE.MatchString(v)
				ident = ident || identityOpsRE.MatchString(strings.ReplaceAll(v, "||", ""))
			}
			for ch := range el.Children() {
				if ch.Kind() == xdm.KindText && strings.Contains(ch.Value(), "{") {
					v := ch.Value()
					loads = loads || loadsDocRE.MatchString(v)
					ident = ident || identityOpsRE.MatchString(strings.ReplaceAll(v, "||", ""))
				}
			}
			return !(loads && ident)
		})
	}
	c.l7Alias = -1
	if loads && ident {
		c.l7Alias = 1
	}
	return c.l7Alias > 0
}
