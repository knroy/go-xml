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
// a node from elsewhere read only the expression's text, so the guard here
// decides for the whole stylesheet.
//
// Lane MM1 made it a flow over the parsed expressions (aliasFlow, below):
// every value carries whether it may hold a node of a stream (aS) and a
// node a load gave (aTA: a load that may reach a streamed URI -- a literal
// that resolves to one, or any non-literal load; aTL: any load). Values flow
// through variables and parameters (by name, every binding of a name
// joined), function arguments and results, template focus by mode or name,
// with-params, groups and accumulators, to a fixpoint. A node-identity use
// (union, |, intersect, except, is, <<, >>, generate-id) whose operands may
// hold both a stream's node and an aliasing load's is what refuses. Literal
// attribute text (separator="|") and string literals are not operators any
// more, and an operator over a load's nodes alone, or a stream's alone, does
// not refuse. An xsl:evaluate given a stream's node is such a use: what it
// evaluates is not known until run time. Copies (copy-of, snapshot, constructed nodes) are new nodes in
// both modes. A stylesheet that uses a package keeps the stylesheet-wide
// rule: its modules may compile after the question is asked.
//
// ponytail: names are joined stylesheet-wide (no scopes, no call contexts)
// and a dynamic call or inline function parameter may hold anything; a path
// that sorts and dedupes a mixed sequence (`($s, $t)/.`) is not counted, as
// before.

import (
	"reflect"
	"regexp"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

var (
	identityOpsRE = regexp.MustCompile(`\b(union|intersect|except|is)\b|\||<<|>>|generate-id`)
	litStringRE   = regexp.MustCompile(`^\s*(?:'([^']*)'|"([^"]*)")\s*$`)
)

// aliasScan is what streamMayAlias reads of the stylesheet's modules: sd
// for a streamed source-document or merge, main for the rule plans.
type aliasScan struct{ sd, main bool }

// streamMayAlias reports whether a streamed run of a source-document or a
// merge may compare a node of the stream with the tree's own node for it.
// main asks the same for the rule plans, which also serve a streamed
// principal input, whose URI is not known at compile time (the harness or a
// host may have it in the resolver's cache): there any load counts.
func (c *compiler) streamMayAlias(main bool) bool {
	sc := c.aliasScanOf()
	if main {
		return sc.main
	}
	return sc.sd
}

// aliasBase is the static base URI of el.
func (c *compiler) aliasBase(el *xdm.Node) string {
	if b := el.BaseURI(); b != "" {
		return b
	}
	return c.opts.BaseURI
}

// The flow's value bits.
type abits uint8

const (
	aS  abits = 1 << iota // may hold a node of a stream
	aTA                   // may hold a node of a load that may reach a streamed URI
	aTL                   // may hold a node of any load
)

type aqn struct{ uri, local string }

// aenv is the focus an expression is read with: the context item's, the
// instruction's (current()), and the group's (current-group()).
type aenv struct {
	focus, cur, group abits
	el                *xdm.Node
}

type aliasFlow struct {
	c *compiler
	// streamed holds the absolute URIs read as streams; anyStream says one
	// is not a literal.
	streamed  map[string]bool
	anyStream bool
	usePkg    bool
	defMode   bool // a default-mode attribute: unnamed means any mode

	funcs      map[funcKey]*xdm.Node
	vars       map[aqn]abits
	funcParams map[funcKey][]abits
	funcRes    map[funcKey]abits
	tplParams  map[aqn]abits
	modeFocus  map[string]abits // "" the unnamed mode, "*" every mode
	namedFocus map[aqn]abits
	tplRes     abits
	accRes     abits
	allLoad    abits
	gen        abits // joined arguments of generate-id
	changed    bool

	sd, main, identAny bool
}

func (a *aliasFlow) all() abits { return aS | a.allLoad }

func (a *aliasFlow) meet(b abits) {
	if b&aS != 0 {
		a.sd = a.sd || b&aTA != 0
		a.main = a.main || b&aTL != 0
	}
}

func orInto[K comparable](m map[K]abits, k K, v abits, changed *bool) {
	if m[k]|v != m[k] {
		m[k] |= v
		*changed = true
	}
}

func (a *aliasFlow) or(p *abits, v abits) {
	if *p|v != *p {
		*p |= v
		a.changed = true
	}
}

// loadOf is the bits of a load of the literal URI u written on el.
func (a *aliasFlow) loadOf(el *xdm.Node, u string) abits {
	if a.anyStream || a.streamed[resolveAgainst(a.c.aliasBase(el), strings.TrimSpace(u))] {
		return aTA | aTL
	}
	return aTL
}

func (a *aliasFlow) load(b abits) abits {
	a.or(&a.allLoad, b)
	return b
}

func (a *aliasFlow) qname(el *xdm.Node, lex string) aqn {
	q, err := resolveQNameAttr(el, lex)
	if err != nil {
		return aqn{local: lex}
	}
	return aqn{q.URI, q.Local}
}

func (a *aliasFlow) modeKey(el *xdm.Node, m string) string {
	switch m = strings.TrimSpace(m); m {
	case "", "#default", "#unnamed":
		if a.defMode {
			return "*"
		}
		return ""
	case "#current":
		return "*"
	}
	q := a.qname(el, m)
	return q.uri + "}" + q.local
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
	a := &aliasFlow{c: c, streamed: map[string]bool{}, funcs: map[funcKey]*xdm.Node{},
		vars: map[aqn]abits{}, funcParams: map[funcKey][]abits{}, funcRes: map[funcKey]abits{},
		tplParams: map[aqn]abits{}, modeFocus: map[string]abits{}, namedFocus: map[aqn]abits{}}
	for r := range roots {
		walkElements(r, func(el *xdm.Node) bool {
			if el.Attr("", "default-mode") != nil {
				a.defMode = true
			}
			streamable := isYes(el.AttrValue("streamable"))
			switch {
			case isXSL(el, "use-package"):
				a.usePkg = true
			case isXSL(el, "source-document") && streamable:
				if v := el.AttrValue("href"); strings.ContainsAny(v, "{}") {
					a.anyStream = true
				} else {
					a.streamed[resolveAgainst(c.aliasBase(el), strings.TrimSpace(v))] = true
				}
			case isXSL(el, "merge-source") && streamable && el.Attr("", "for-each-source") != nil:
				if m := litStringRE.FindStringSubmatch(el.AttrValue("for-each-source")); m == nil {
					a.anyStream = true
				} else {
					a.streamed[resolveAgainst(c.aliasBase(el), m[1]+m[2])] = true
				}
			case isXSL(el, "function"):
				q, err := resolveQNameAttr(el, el.AttrValue("name"))
				if err == nil {
					n := 0
					for _, p := range el.ChildElements() {
						if isXSL(p, "param") {
							n++
						}
					}
					a.funcs[keyOf(q, n)] = el
				}
			}
			return true
		})
	}
	for range 64 { // monotone over finite bits: a handful of passes
		a.changed = false
		for r := range roots {
			a.module(r)
		}
		if !a.changed {
			break
		}
	}
	a.meet(a.gen)
	sc := &aliasScan{sd: a.sd, main: a.main}
	if a.usePkg {
		sc = &aliasScan{sd: a.identAny, main: a.identAny}
	}
	c.l7Alias = sc
	return sc
}

// module walks one module's declarations.
func (a *aliasFlow) module(r *xdm.Node) {
	if r.Name().URI != xdm.NSXSL {
		a.seq(r, aenv{focus: a.all(), cur: a.all(), el: r}) // a simplified stylesheet
		return
	}
	for _, d := range r.ChildElements() {
		switch {
		case isXSL(d, "override"):
			a.module(d)
		case isXSL(d, "template"):
			a.template(d)
		case isXSL(d, "function"):
			a.function(d)
		case isXSL(d, "mode"):
			if isYes(d.AttrValue("streamable")) {
				orInto(a.modeFocus, a.modeKey(d, d.AttrValue("name")), aS, &a.changed)
			}
		case isXSL(d, "accumulator"):
			en := aenv{focus: a.all(), cur: a.all(), el: d}
			a.or(&a.accRes, a.attrs(d, aenv{el: d}, ""))
			for _, r := range d.ChildElements() {
				en.el = r
				a.or(&a.accRes, a.attrs(r, en, "")|a.seq(r, en))
			}
			orInto(a.vars, aqn{local: "value"}, a.accRes, &a.changed)
		case isXSL(d, "key"), isXSL(d, "attribute-set"):
			a.instr(d, aenv{focus: a.all(), cur: a.all(), el: d})
		case isXSL(d, "variable"), isXSL(d, "param"):
			a.instr(d, aenv{el: d})
		}
	}
}

func (a *aliasFlow) template(t *xdm.Node) {
	var f abits
	if m := t.Attr("", "match"); m != nil {
		modes := strings.Fields(t.AttrValue("mode"))
		if len(modes) == 0 {
			modes = []string{"#default"}
		}
		for _, m := range modes {
			if m == "#all" {
				for _, v := range a.modeFocus {
					f |= v
				}
				continue
			}
			k := a.modeKey(t, m)
			if k == "*" {
				for _, v := range a.modeFocus {
					f |= v
				}
			}
			f |= a.modeFocus[k]
		}
		f |= a.modeFocus["*"]
	}
	if n := t.AttrValue("name"); n != "" {
		f |= a.namedFocus[a.qname(t, n)]
	}
	for _, p := range t.ChildElements() {
		if isXSL(p, "param") {
			q := a.qname(p, p.AttrValue("name"))
			orInto(a.vars, q, a.tplParams[q], &a.changed)
		}
	}
	en := aenv{focus: f, cur: f, el: t}
	a.attrs(t, en, "")
	a.or(&a.tplRes, a.seq(t, en))
}

func (a *aliasFlow) function(fn *xdm.Node) {
	q, err := resolveQNameAttr(fn, fn.AttrValue("name"))
	if err != nil {
		return
	}
	var ps []*xdm.Node
	for _, p := range fn.ChildElements() {
		if isXSL(p, "param") {
			ps = append(ps, p)
		}
	}
	k := keyOf(q, len(ps))
	args := a.funcParams[k]
	for i, p := range ps {
		if i < len(args) {
			orInto(a.vars, a.qname(p, p.AttrValue("name")), args[i], &a.changed)
		}
	}
	orInto(a.funcRes, k, a.seq(fn, aenv{el: fn}), &a.changed)
}

// seq walks a sequence constructor and returns what its items may hold.
func (a *aliasFlow) seq(parent *xdm.Node, en aenv) abits {
	var v abits
	for ch := range parent.Children() {
		switch ch.Kind() {
		case xdm.KindElement:
			v |= a.instr(ch, en)
		case xdm.KindText:
			if strings.Contains(ch.Value(), "{") {
				a.avt(parent, ch.Value(), en)
			}
		}
	}
	return v
}

var aliasExprAttrs = map[string]bool{"select": true, "test": true, "group-by": true, "group-adjacent": true,
	"value": true, "key": true, "use": true, "initial-value": true, "for-each-item": true, "xpath": true,
	"context-item": true, "with-params": true, "namespace-context": true}

var aliasPatternAttrs = map[string]bool{"match": true, "count": true, "from": true,
	"group-starting-with": true, "group-ending-with": true}

// attrs reads el's attributes but skip with en, and returns what its
// expressions may hold.
func (a *aliasFlow) attrs(el *xdm.Node, en aenv, skip string) abits {
	en.el = el
	var v abits
	for at := range el.Attrs() {
		name, src := at.Name(), at.Value()
		if name.Local == skip && name.URI == "" {
			continue
		}
		xsl := el.Name().URI == xdm.NSXSL
		switch {
		case xsl && name.URI == "" && aliasExprAttrs[name.Local]:
			v |= a.expr(src, en)
		case xsl && name.URI == "" && aliasPatternAttrs[name.Local]:
			if e := parseAt(el, src); e != nil {
				a.pattern(e, en)
			} else {
				a.unparsed(src)
			}
		case xsl && name.URI == "" && name.Local == "for-each-source":
		case !xsl && name.URI == xdm.NSXSL:
		default:
			a.avt(el, src, en)
		}
	}
	return v
}

func (a *aliasFlow) unparsed(src string) {
	if identityOpsRE.MatchString(strings.ReplaceAll(src, "||", "")) {
		a.sd, a.main, a.identAny = true, true, true
	}
}

func (a *aliasFlow) expr(src string, en aenv) abits {
	if strings.TrimSpace(src) == "" {
		return 0
	}
	e := parseAt(en.el, src)
	if e == nil {
		a.unparsed(src)
		return a.all()
	}
	return a.eval(e, en)
}

// avt reads the expressions of an attribute or text value template; a part
// that does not parse is not an expression (an EQName's braces).
func (a *aliasFlow) avt(el *xdm.Node, src string, en aenv) {
	if !strings.Contains(src, "{") {
		return
	}
	parts, _ := avtExpressions(src)
	en.el = el
	for _, s := range parts {
		if e := parseAt(el, s); e != nil {
			a.eval(e, en)
		}
	}
}

// pattern reads a pattern: its set operators combine patterns, not nodes.
func (a *aliasFlow) pattern(e xpath.Expr, en aenv) {
	if b, ok := e.(*xpath.BinaryOp); ok && (b.Op == "union" || b.Op == "intersect" || b.Op == "except") {
		a.pattern(b.Left, en)
		a.pattern(b.Right, en)
		return
	}
	a.eval(e, en)
}

func (a *aliasFlow) instr(el *xdm.Node, en aenv) abits {
	en.el = el
	if el.Name().URI != xdm.NSXSL {
		a.attrs(el, en, "")
		a.seq(el, en)
		return 0 // a new node
	}
	switch el.Name().Local {
	case "for-each", "for-each-group", "iterate":
		s := a.expr(el.AttrValue("select"), en)
		in := aenv{focus: s, cur: s, group: en.group, el: el}
		if el.Name().Local == "for-each-group" {
			in.group = s
		}
		a.attrs(el, in, "select")
		return a.seq(el, in)
	case "source-document":
		a.attrs(el, en, "")
		s := aS
		if !isYes(el.AttrValue("streamable")) {
			if h := el.AttrValue("href"); strings.ContainsAny(h, "{}") {
				s = a.load(aTA | aTL)
			} else {
				s = a.load(a.loadOf(el, h))
			}
		}
		return a.seq(el, aenv{focus: s, cur: s, el: el})
	case "apply-templates", "call-template", "next-match", "apply-imports":
		s := en.focus
		if el.Attr("", "select") != nil {
			s = a.expr(el.AttrValue("select"), en)
		}
		switch el.Name().Local {
		case "apply-templates":
			orInto(a.modeFocus, a.modeKey(el, el.AttrValue("mode")), s, &a.changed)
		case "call-template":
			orInto(a.namedFocus, a.qname(el, el.AttrValue("name")), s, &a.changed)
		default:
			orInto(a.modeFocus, "*", s, &a.changed)
		}
		for _, ch := range el.ChildElements() {
			if isXSL(ch, "sort") {
				a.instr(ch, aenv{focus: s, cur: s, el: ch})
			} else {
				a.instr(ch, en)
			}
		}
		return a.tplRes
	case "variable", "param", "with-param":
		v := a.attrs(el, en, "")
		if el.Attr("", "select") == nil {
			v |= a.seq(el, en)
		}
		q := a.qname(el, el.AttrValue("name"))
		orInto(a.vars, q, v, &a.changed)
		if el.Name().Local == "with-param" {
			orInto(a.tplParams, q, v, &a.changed)
		}
		return 0
	case "copy":
		s := en.focus
		if el.Attr("", "select") != nil {
			s = a.expr(el.AttrValue("select"), en)
		}
		in := aenv{focus: s, cur: s, group: en.group, el: el}
		a.attrs(el, in, "select")
		a.seq(el, in)
		return 0
	case "copy-of", "value-of", "element", "attribute", "comment", "processing-instruction",
		"namespace", "text", "document", "result-document", "message", "number", "assert":
		a.attrs(el, en, "")
		a.seq(el, en)
		return 0 // new nodes or atomic values
	case "analyze-string":
		a.attrs(el, en, "")
		return a.seq(el, aenv{group: en.group, el: el})
	case "evaluate":
		// The evaluated expression is data (§10.4.2): it may doc() the
		// streamed URI and compare what it loads with what it is given, so
		// a stream's node given to it, as its context item or a parameter,
		// is an identity use of both kinds.
		a.attrs(el, en, "")
		var fed abits
		for _, n := range []string{"context-item", "with-params"} {
			if el.Attr("", n) != nil {
				fed |= a.expr(el.AttrValue(n), en)
			}
		}
		for _, ch := range el.ChildElements() {
			v := a.instr(ch, en)
			if isXSL(ch, "with-param") {
				v |= a.vars[a.qname(ch, ch.AttrValue("name"))]
			}
			fed |= v
		}
		v := a.load(aTA | aTL)
		a.meet(fed | v)
		return fed | v
	case "merge":
		var g abits
		for _, ms := range el.ChildElements() {
			if !isXSL(ms, "merge-source") {
				continue
			}
			var docs abits
			switch {
			case ms.Attr("", "for-each-source") == nil:
			case isYes(ms.AttrValue("streamable")):
				docs = aS
			default:
				fes := ms.AttrValue("for-each-source")
				a.expr(fes, aenv{focus: en.focus, cur: en.cur, el: ms})
				if m := litStringRE.FindStringSubmatch(fes); m != nil {
					docs = a.load(a.loadOf(ms, m[1]+m[2]))
				} else {
					docs = a.load(aTA | aTL)
				}
			}
			if ms.Attr("", "for-each-item") != nil {
				docs |= a.expr(ms.AttrValue("for-each-item"), aenv{focus: en.focus, cur: en.cur, el: ms})
			}
			in := aenv{focus: docs, cur: docs, el: ms}
			if docs == 0 {
				in.focus, in.cur = en.focus, en.cur
			}
			s := a.expr(ms.AttrValue("select"), in)
			g |= s
			for _, k := range ms.ChildElements() {
				a.instr(k, aenv{focus: s, cur: s, el: k})
			}
		}
		var v abits
		for _, ch := range el.ChildElements() {
			if !isXSL(ch, "merge-source") {
				v |= a.instr(ch, aenv{focus: en.focus, cur: en.cur, group: g, el: ch})
			}
		}
		return v
	}
	return a.attrs(el, en, "") | a.seq(el, en)
}

var aliasBuiltinNS = map[string]bool{fnNS: true, "http://www.w3.org/2005/xpath-functions/math": true,
	xdm.NSMap: true, xdm.NSArray: true, "http://www.w3.org/2001/XMLSchema": true}

// eval returns what e may hold, read with en, and meets every identity use.
func (a *aliasFlow) eval(e xpath.Expr, en aenv) abits {
	switch x := e.(type) {
	case nil, *xpath.Literal:
		return 0
	case *xpath.ContextItem:
		return en.focus
	case *xpath.VarRef:
		return a.vars[aqn{x.Name.URI, x.Name.Local}]
	case *xpath.Step:
		a.preds(x.Predicates, en)
		return en.focus
	case *xpath.PathExpr:
		cur := en.focus
		for _, s := range x.Steps {
			if st, ok := s.(*xpath.Step); ok {
				a.preds(st.Predicates, aenv{focus: cur, cur: en.cur, group: en.group, el: en.el})
			} else {
				cur = a.eval(s, aenv{focus: cur, cur: en.cur, group: en.group, el: en.el})
			}
		}
		return cur
	case *xpath.FilterExpr:
		b := a.eval(x.Base, en)
		a.preds(x.Predicates, aenv{focus: b, cur: en.cur, group: en.group, el: en.el})
		return b
	case *xpath.SimpleMap:
		l := a.eval(x.Left, en)
		return a.eval(x.Right, aenv{focus: l, cur: en.cur, group: en.group, el: en.el})
	case *xpath.BinaryOp:
		l, r := a.eval(x.Left, en), a.eval(x.Right, en)
		switch x.Op {
		case "union", "intersect", "except":
			a.identAny = true
			a.meet(l | r)
			return l | r
		case "is", "<<", ">>":
			a.identAny = true
			a.meet(l | r)
		}
		return 0
	case *xpath.IfExpr:
		a.eval(x.Cond, en)
		return a.eval(x.Then, en) | a.eval(x.Else, en)
	case *xpath.ForExpr:
		a.bind(x.Bindings, en)
		return a.eval(x.Return, en)
	case *xpath.LetExpr:
		a.bind(x.Bindings, en)
		return a.eval(x.Return, en)
	case *xpath.QuantifiedExpr:
		a.bind(x.Bindings, en)
		a.eval(x.Test, en)
		return 0
	case *xpath.InlineFunctionExpr:
		for _, p := range x.Params {
			orInto(a.vars, aqn{p.Name.URI, p.Name.Local}, a.all(), &a.changed)
		}
		a.eval(x.Body, aenv{el: en.el})
		return 0
	case *xpath.DynamicCall:
		v := a.eval(x.Target, en)
		for _, arg := range x.Args {
			v |= a.eval(arg, en)
		}
		return v | a.all()
	case *xpath.NamedFunctionRef:
		a.ref(x.Name, x.Arity)
		return 0
	case *xpath.FuncCall:
		return a.call(x, en)
	}
	// Anything else: what its operands hold.
	var v abits
	for _, ch := range aliasChildren(e) {
		v |= a.eval(ch, en)
	}
	if l, ok := e.(*xpath.LookupExpr); ok && l.Base == nil {
		v |= en.focus
	}
	return v
}

func (a *aliasFlow) preds(ps []xpath.Expr, en aenv) {
	for _, p := range ps {
		a.eval(p, en)
	}
}

func (a *aliasFlow) bind(bs []xpath.Binding, en aenv) {
	for _, b := range bs {
		orInto(a.vars, aqn{b.Var.URI, b.Var.Local}, a.eval(b.Seq, en), &a.changed)
	}
}

// ref is a function item made of name#arity: called with anything.
func (a *aliasFlow) ref(q xdm.QName, arity int) {
	k := keyOf(q, arity)
	if _, ok := a.funcs[k]; ok {
		a.args(k, nil, arity)
		return
	}
	switch q.Local {
	case "doc", "document", "collection", "transform", "function-lookup":
		if q.URI == fnNS {
			a.load(aTA | aTL)
		}
	}
}

// args joins argument bits into k's parameters; nil means anything.
func (a *aliasFlow) args(k funcKey, vs []abits, n int) {
	ps := a.funcParams[k]
	for len(ps) < n {
		ps = append(ps, 0)
	}
	for i := range n {
		v := a.all()
		if vs != nil {
			v = vs[i]
		}
		if ps[i]|v != ps[i] {
			ps[i] |= v
			a.changed = true
		}
	}
	a.funcParams[k] = ps
}

func (a *aliasFlow) call(x *xpath.FuncCall, en aenv) abits {
	vs := make([]abits, len(x.Args))
	var v abits
	for i, arg := range x.Args {
		vs[i] = a.eval(arg, en)
		v |= vs[i]
	}
	if !aliasBuiltinNS[x.Name.URI] {
		k := keyOf(x.Name, len(x.Args))
		if _, ok := a.funcs[k]; ok {
			a.args(k, vs, len(vs))
			return a.funcRes[k]
		}
		return v
	}
	if x.Name.URI != fnNS {
		return v
	}
	switch x.Name.Local {
	case "doc", "document":
		if len(x.Args) == 1 {
			if l, ok := x.Args[0].(*xpath.Literal); ok && l.Val != nil && l.Val.Type == xdm.TypeString {
				return a.load(a.loadOf(en.el, l.Val.Str()))
			}
		}
		return a.load(aTA | aTL)
	case "collection", "transform":
		return v | a.load(aTA|aTL)
	case "function-lookup":
		a.load(aTA | aTL)
		for k := range a.funcs {
			a.args(k, nil, k.arity)
		}
		return 0
	case "copy-of", "snapshot":
		return 0 // new nodes in both modes
	case "generate-id":
		if len(x.Args) == 0 {
			v = en.focus
		}
		a.identAny = true
		a.or(&a.gen, v)
		return 0
	case "current":
		return en.cur
	case "current-group", "current-merge-group":
		return en.group
	case "accumulator-before", "accumulator-after":
		return a.accRes
	case "root", "key", "id", "idref", "element-with-id":
		return v | en.focus
	}
	return v
}

// aliasChildren is the expressions directly below e.
func aliasChildren(e xpath.Expr) []xpath.Expr {
	var out []xpath.Expr
	exprType := reflect.TypeOf((*xpath.Expr)(nil)).Elem()
	var walk func(v reflect.Value, top bool)
	walk = func(v reflect.Value, top bool) {
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			if !top && v.Type() == exprType {
				out = append(out, v.Interface().(xpath.Expr))
				return
			}
			walk(v.Elem(), top)
		case reflect.Pointer:
			if !v.IsNil() && v.Type().Elem().PkgPath() == "github.com/knroy/go-xml/v2/xpath" {
				if !top && v.Type().Implements(exprType) {
					out = append(out, v.Interface().(xpath.Expr))
					return
				}
				walk(v.Elem(), false)
			}
		case reflect.Struct:
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i), false)
				}
			}
		case reflect.Slice:
			for i := range v.Len() {
				walk(v.Index(i), false)
			}
		}
	}
	walk(reflect.ValueOf(&e).Elem(), true)
	return out
}
