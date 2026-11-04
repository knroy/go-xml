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
// ponytail: the check is stylesheet-wide and textual. When a load may reach
// a URI the stylesheet streams -- a literal that resolves to the same
// absolute URI as a streamed literal href, or any load whose URI is not a
// literal -- AND a node-identity operator (union, |, intersect, except, is,
// <<, >>, generate-id) is written anywhere, nothing streams: no
// xsl:source-document plan, no rule plan, no streamed merge. A literal load
// of a different URI is safe: its nodes are another tree in both modes.
// Narrow it to the operators whose operands can reach such nodes when that
// is worth the dataflow.

import (
	"regexp"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

var (
	loadCallRE    = regexp.MustCompile(`\b(doc|document|collection|transform|function-lookup)\s*[(#]`)
	litLoadRE     = regexp.MustCompile(`\b(doc|document)\s*\(\s*(?:'([^']*)'|"([^"]*)")\s*\)`)
	identityOpsRE = regexp.MustCompile(`\b(union|intersect|except|is)\b|\||<<|>>|generate-id`)
	litStringRE   = regexp.MustCompile(`^\s*(?:'([^']*)'|"([^"]*)")\s*$`)
)

// aliasScan is what streamMayAlias reads of the stylesheet's modules.
type aliasScan struct {
	// streamed holds the absolute URIs of the literal hrefs read as
	// streams (a streamable xsl:source-document, a streamable
	// xsl:merge-source's literal for-each-source); anyStream says one is
	// not a literal.
	streamed  map[string]bool
	anyStream bool
	// loads holds the absolute URIs of the literal loads (doc('x'),
	// document('x'), a non-streamable source-document's literal href);
	// anyLoad says a load's URI is not known here: a computed argument,
	// collection, transform, function-lookup, a named reference doc#1,
	// xsl:evaluate, xsl:use-package (its modules compile last).
	loads   map[string]bool
	anyLoad bool
	ident   bool
}

// streamMayAlias reports whether a streamed run of a source-document or a
// merge may compare a node of the stream with the tree's own node for it:
// a load may reach a URI a container streams, and an identity operator is
// written somewhere. main asks the same for the rule plans, which also
// serve a streamed principal input, whose URI is not known at compile time
// (the harness or a host may have it in the resolver's cache): there any
// load counts.
func (c *compiler) streamMayAlias(main bool) bool {
	sc := c.aliasScanOf()
	if !sc.ident {
		return false
	}
	if sc.anyLoad || main && len(sc.loads) > 0 {
		return true
	}
	if sc.anyStream && len(sc.loads) > 0 {
		return true
	}
	for u := range sc.loads {
		if sc.streamed[u] {
			return true
		}
	}
	return false
}

// aliasBase is the static base URI of el.
func (c *compiler) aliasBase(el *xdm.Node) string {
	if b := el.BaseURI(); b != "" {
		return b
	}
	return c.opts.BaseURI
}

func (c *compiler) aliasScanOf() *aliasScan {
	// Used packages compile last and add their modules: rescanned then.
	if c.l7Alias != nil && c.l7AliasN == len(c.staticDone) {
		return c.l7Alias
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
	sc := &aliasScan{streamed: map[string]bool{}, loads: map[string]bool{}}
	lit := func(el *xdm.Node, v string, into map[string]bool, any *bool) {
		if strings.ContainsAny(v, "{}") {
			*any = true
			return
		}
		into[resolveAgainst(c.aliasBase(el), strings.TrimSpace(v))] = true
	}
	litExpr := func(el *xdm.Node, v string, into map[string]bool, any *bool) {
		m := litStringRE.FindStringSubmatch(v)
		if m == nil {
			*any = true
			return
		}
		into[resolveAgainst(c.aliasBase(el), m[1]+m[2])] = true
	}
	scanText := func(el *xdm.Node, v string) {
		calls := len(loadCallRE.FindAllString(v, -1))
		lits := litLoadRE.FindAllStringSubmatch(v, -1)
		if calls > len(lits) {
			sc.anyLoad = true
		}
		for _, m := range lits {
			sc.loads[resolveAgainst(c.aliasBase(el), m[2]+m[3])] = true
		}
		sc.ident = sc.ident || identityOpsRE.MatchString(strings.ReplaceAll(v, "||", ""))
	}
	for r := range roots {
		walkElements(r, func(el *xdm.Node) bool {
			streamable := isYes(el.AttrValue("streamable"))
			switch {
			case isXSL(el, "evaluate"), isXSL(el, "use-package"):
				sc.anyLoad = true
			case isXSL(el, "source-document") && streamable:
				lit(el, el.AttrValue("href"), sc.streamed, &sc.anyStream)
			case isXSL(el, "source-document"):
				lit(el, el.AttrValue("href"), sc.loads, &sc.anyLoad)
			case isXSL(el, "merge-source") && el.Attr("", "for-each-source") != nil:
				if streamable {
					litExpr(el, el.AttrValue("for-each-source"), sc.streamed, &sc.anyStream)
				} else {
					litExpr(el, el.AttrValue("for-each-source"), sc.loads, &sc.anyLoad)
				}
			}
			for a := range el.Attrs() {
				scanText(el, a.Value())
			}
			for ch := range el.Children() {
				if ch.Kind() == xdm.KindText && strings.Contains(ch.Value(), "{") {
					scanText(el, ch.Value())
				}
			}
			return true
		})
	}
	c.l7Alias = sc
	return sc
}
