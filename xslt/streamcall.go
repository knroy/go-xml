package xslt

// Calls to stylesheet functions in a streamed XPath split (full streaming,
// component C11; DESIGN section 1.7, XSLT 3.0 §19.8.5).
//
// A consuming expression that calls an xsl:function is split as if the call
// were its body: "When the body is one xsl:sequence select=E, E is spliced
// into the caller's split as path steps." The call f(A1, ..., An) is
// replaced, in the split's own parse of the expression (reparse), by E with
// each parameter reference replaced by its argument -- but only where that
// is the call, exactly: where every conversion the call would make (§10.3.3:
// the parameters' declared types, the declared result type) is one that
// cannot fail and changes nothing, so that the inlined form returns what the
// call returns and raises what it raises. Then the split decides as it
// decides for any expression: the inlined body is a path, a tail, a reducer,
// or it is refused. The category (absorbing, inspection, filter,
// shallow-descent, deep-descent, ascent) is not consulted: the analysis has
// trusted the call by it, and the split checks the body itself.
//
// The conditions, conservative:
//
//   - the body is one xsl:sequence with a select (after the xsl:param
//     elements), reading no focus and no variable but the parameters, with
//     no range variable that could capture an argument's;
//   - the function caches nothing (new-each-time, cache) and sets no static
//     context of its own (default-collation, xml:base, version);
//   - a parameter that takes nodes is given the context item (the call's
//     focus, a node: node() or item(), perhaps with an occurrence) or a path
//     (when the type is node()* or item()*, absent included), each of its
//     references in E at E's own focus (not in a predicate, a later step or
//     the right of "!"), a path once;
//   - an atomic parameter is given a literal of its very type;
//   - the result type is absent or item()*, or E's value is certainly of it
//     (an atomic type E's top function returns, node()* for a path).
//
// Only the calls in the expression itself are inlined, once: a call inside
// an inlined body stays a call (a recursive function stays on the tree).
//
// With the inlining comes "P ! R" for an R that makes atomic values: it is
// "P/R" (both evaluate R once per node of P, in order, with that focus; P,
// a path, has its nodes in document order without duplicates), which the
// split takes as a path with a tail -- the dominant call-site shape,
// "path ! f:g(.)".

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// streamFuncs returns the stylesheet's functions, as the analysis indexes
// them, once per compile.
func (c *compiler) streamFuncs() map[funcKey]*streamFunc {
	if c.streamFns == nil {
		c.streamFns = collectStreamFuncs(c.sheet.source)
	}
	return c.streamFns
}

// inlineCalls rewrites, in e, the calls on stylesheet functions that are
// their bodies exactly, and the simple maps that are paths.
func (c *compiler) inlineCalls(e xpath.Expr) xpath.Expr {
	return rewriteCalls(e, c.streamFuncs())
}

// rewriteCalls is inlineCalls over the table fns.
func rewriteCalls(e xpath.Expr, fns map[funcKey]*streamFunc) xpath.Expr {
	re := func(x xpath.Expr) xpath.Expr { return rewriteCalls(x, fns) }
	switch x := e.(type) {
	case *xpath.FuncCall:
		for i := range x.Args {
			x.Args[i] = re(x.Args[i])
		}
		if body := inlineCall(x, fns); body != nil {
			return body
		}
		if v := traceValue(x); v != nil {
			return v // streamsplit_fns.go
		}
	case *xpath.SimpleMap:
		x.Left, x.Right = re(x.Left), re(x.Right)
		if p := mapAsPath(x); p != nil {
			return p
		}
	case *xpath.PathExpr:
		for i := range x.Steps {
			if _, ok := x.Steps[i].(*xpath.Step); !ok {
				x.Steps[i] = re(x.Steps[i])
			}
		}
	case *xpath.FilterExpr:
		x.Base = re(x.Base)
	case *xpath.BinaryOp:
		x.Left, x.Right = re(x.Left), re(x.Right)
	case *xpath.UnaryOp:
		x.Operand = re(x.Operand)
	case *xpath.CastExpr:
		x.Operand = re(x.Operand)
	case *xpath.TreatExpr:
		x.Operand = re(x.Operand)
	case *xpath.InstanceOfExpr:
		x.Operand = re(x.Operand)
	case *xpath.IfExpr:
		x.Cond, x.Then, x.Else = re(x.Cond), re(x.Then), re(x.Else)
	case *xpath.SequenceExpr:
		for i := range x.Items {
			x.Items[i] = re(x.Items[i])
		}
	}
	return e
}

// mapAsPath is P ! R as the path P/R, when P is a path ending in a step
// and R makes only atomic values; nil otherwise.
func mapAsPath(x *xpath.SimpleMap) xpath.Expr {
	if !atomicValued(x.Right) {
		return nil
	}
	switch l := x.Left.(type) {
	case *xpath.Step:
		return &xpath.PathExpr{Steps: []xpath.Expr{l, x.Right}}
	case *xpath.PathExpr:
		if len(l.Steps) == 0 || !isStep(l.Steps[len(l.Steps)-1]) {
			return nil
		}
		steps := append(append([]xpath.Expr(nil), l.Steps...), x.Right)
		return &xpath.PathExpr{Root: l.Root, Steps: steps}
	case *xpath.FuncCall:
		if outerInner(l) {
			return &xpath.PathExpr{Steps: []xpath.Expr{l, x.Right}}
		}
	}
	return nil
}

// inlineCall returns the body of the function x calls with its arguments
// in place, or nil when that would not be the call exactly.
func inlineCall(x *xpath.FuncCall, fns map[funcKey]*streamFunc) xpath.Expr {
	if x.Name.URI == "" || x.Name.URI == fnNS || x.Name.URI == xdm.NSXS {
		return nil
	}
	f := fns[keyOf(x.Name, len(x.Args))]
	if f == nil || len(x.Args) == 0 {
		return nil
	}
	fn := f.body
	for a := range fn.Attrs() {
		switch n := a.Name(); {
		case n.URI == xdm.NSXML:
			return nil // xml:base
		case n.URI != "":
		case n.Local == "name", n.Local == "as", n.Local == "streamability", n.Local == "visibility",
			n.Local == "override-extension-function", n.Local == "override", n.Local == "use-when",
			n.Local == "exclude-result-prefixes":
		default:
			return nil // new-each-time, cache, default-collation, version, ...
		}
	}
	var params []xdm.QName
	var seq *xdm.Node
	for ch := range fn.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "", ch.Kind() == xdm.KindComment:
		case isXSL(ch, "param") && seq == nil:
			q, err := resolveQNameAttr(ch, ch.AttrValue("name"))
			if err != nil || ch.Attr("", "select") != nil || ch.FirstChild() != nil {
				return nil
			}
			params = append(params, q)
		case isXSL(ch, "sequence") && seq == nil && ch.FirstChild() == nil:
			seq = ch
		default:
			return nil
		}
	}
	if seq == nil || len(params) != len(x.Args) {
		return nil
	}
	for a := range seq.Attrs() {
		if n := a.Name(); n.URI != "" || n.Local != "select" && n.Local != "use-when" {
			return nil
		}
	}
	body := parseAt(seq, seq.AttrValue("select"))
	if body == nil || !focusFree(body) || !resultFits(body, f.asType) {
		return nil
	}
	subst := map[xdm.QName]xpath.Expr{}
	once := map[xdm.QName]bool{}
	for i, q := range params {
		a, t := x.Args[i], f.params[i]
		switch {
		case paramTakes(t, a):
			subst[q] = a
			_, ctx := a.(*xpath.ContextItem)
			once[q] = !ctx
		case literalOf(a, t):
			subst[q] = a
		default:
			return nil
		}
	}
	s := &inliner{subst: subst, once: once, seen: map[xdm.QName]int{}}
	out := s.expr(body, true)
	if s.bad {
		return nil
	}
	return out
}

// paramTakes reports whether a, an argument, is a node argument the
// parameter type t takes without conversion or failure: the context item
// (a node, as a split's focus always is) for node() or item(), any
// occurrence; a path for node()* or item()*.
func paramTakes(t string, a xpath.Expr) bool {
	t = strings.Join(strings.Fields(t), "")
	switch a.(type) {
	case *xpath.ContextItem:
		switch strings.TrimRight(t, "?*+") {
		case "", "item()", "node()":
			return true
		}
	case *xpath.PathExpr, *xpath.Step:
		if !nodeValued(a) {
			return false
		}
		switch t {
		case "", "item()*", "node()*":
			return true
		}
	}
	return false
}

// literalOf reports whether a is a literal of the atomic type t names, which
// the parameter then takes as it is.
func literalOf(a xpath.Expr, t string) bool {
	t = strings.TrimRight(strings.Join(strings.Fields(t), ""), "?*+")
	switch l := a.(type) {
	case *xpath.Literal:
		switch l.Val.Type {
		case xdm.TypeString:
			return t == "xs:string"
		case xdm.TypeInteger:
			return t == "xs:integer"
		case xdm.TypeDecimal:
			return t == "xs:decimal"
		case xdm.TypeDouble:
			return t == "xs:double"
		}
	case *xpath.FuncCall:
		return t == "xs:boolean" && l.Name.URI == fnNS && len(l.Args) == 0 &&
			(l.Name.Local == "true" || l.Name.Local == "false")
	}
	return false
}

// resultFits reports whether the value of e, a function's body, is
// certainly of the declared result type as (§10.3.3 converts it, which then
// changes nothing and cannot fail).
func resultFits(e xpath.Expr, as string) bool {
	t := strings.Join(strings.Fields(as), "")
	switch t {
	case "", "item()*":
		return true
	case "node()*":
		return nodeValued(e)
	}
	got := atomicTypeOf(e)
	want := strings.TrimRight(t, "?*+")
	return got != "" && (got == want || got == "xs:integer" && want == "xs:decimal")
}

// atomicTypeOf is the type of the one atomic value e certainly returns, ""
// when it is not known.
func atomicTypeOf(e xpath.Expr) string {
	switch x := e.(type) {
	case *xpath.Literal:
		switch x.Val.Type {
		case xdm.TypeString:
			return "xs:string"
		case xdm.TypeInteger:
			return "xs:integer"
		}
	case *xpath.BinaryOp:
		switch x.Op {
		case "||":
			return "xs:string"
		case "and", "or", "=", "!=", "<", "<=", ">", ">=":
			return "xs:boolean"
		}
	case *xpath.FuncCall:
		if x.Name.URI != fnNS {
			return ""
		}
		switch x.Name.Local {
		case "count", "string-length":
			return "xs:integer"
		case "string", "name", "local-name", "concat", "string-join", "normalize-space", "upper-case", "lower-case":
			return "xs:string"
		case "exists", "empty", "boolean", "not", "true", "false", "has-children", "contains", "starts-with", "ends-with":
			return "xs:boolean"
		}
	case *xpath.IfExpr:
		if t := atomicTypeOf(x.Then); t != "" && t == atomicTypeOf(x.Else) {
			return t
		}
	}
	return ""
}

// inliner puts the arguments in a body in place of its parameters.
type inliner struct {
	subst map[xdm.QName]xpath.Expr
	once  map[xdm.QName]bool
	seen  map[xdm.QName]int
	bad   bool
}

// expr returns e with the parameters replaced; top says e is at the body's
// own focus, where a node argument may go.
func (s *inliner) expr(e xpath.Expr, top bool) xpath.Expr {
	if s.bad {
		return e
	}
	switch x := e.(type) {
	case *xpath.VarRef:
		a, ok := s.subst[x.Name]
		if !ok {
			s.bad = true // a global, or a variable of the caller's that might shadow it
			return e
		}
		if !isLiteralArg(a) && !top {
			s.bad = true // a node argument read at another focus
			return e
		}
		if s.seen[x.Name]++; s.once[x.Name] && s.seen[x.Name] > 1 {
			s.bad = true
		}
		return a
	case *xpath.Literal, *xpath.ContextItem:
		return e
	case *xpath.Step:
		for i := range x.Predicates {
			x.Predicates[i] = s.expr(x.Predicates[i], false)
		}
		return e
	case *xpath.PathExpr:
		for i := range x.Steps {
			x.Steps[i] = s.expr(x.Steps[i], top && i == 0 && !x.Root)
		}
		if len(x.Steps) > 0 {
			// $p/rest with $p a path or the context item: one path.
			switch a := x.Steps[0].(type) {
			case *xpath.ContextItem:
			case *xpath.PathExpr:
				if !x.Root {
					x.Root = a.Root
					x.Steps = append(append([]xpath.Expr(nil), a.Steps...), x.Steps[1:]...)
				}
			case *xpath.Step:
				if !x.Root {
					x.Steps = append([]xpath.Expr{a}, x.Steps[1:]...)
				}
			}
		}
		return e
	case *xpath.FilterExpr:
		x.Base = s.expr(x.Base, top)
		for i := range x.Predicates {
			x.Predicates[i] = s.expr(x.Predicates[i], false)
		}
		return e
	case *xpath.SimpleMap:
		x.Left, x.Right = s.expr(x.Left, top), s.expr(x.Right, false)
		return e
	case *xpath.FuncCall:
		for i := range x.Args {
			x.Args[i] = s.expr(x.Args[i], top)
		}
		return e
	case *xpath.BinaryOp:
		x.Left, x.Right = s.expr(x.Left, top), s.expr(x.Right, top)
		return e
	case *xpath.UnaryOp:
		x.Operand = s.expr(x.Operand, top)
		return e
	case *xpath.CastExpr:
		x.Operand = s.expr(x.Operand, top)
		return e
	case *xpath.TreatExpr:
		x.Operand = s.expr(x.Operand, top)
		return e
	case *xpath.InstanceOfExpr:
		x.Operand = s.expr(x.Operand, top)
		return e
	case *xpath.IfExpr:
		x.Cond, x.Then, x.Else = s.expr(x.Cond, top), s.expr(x.Then, top), s.expr(x.Else, top)
		return e
	case *xpath.SequenceExpr:
		for i := range x.Items {
			x.Items[i] = s.expr(x.Items[i], top)
		}
		return e
	}
	// A range variable, an inline function, a map or array constructor...:
	// not taken.
	s.bad = true
	return e
}

func isLiteralArg(a xpath.Expr) bool {
	switch a.(type) {
	case *xpath.Literal, *xpath.FuncCall:
		return true
	}
	return false
}

// useSets checks a, a use-attribute-sets attribute (xsl:use-attribute-sets
// on a literal result element) of el at focus f: every set it names is
// declared streamable, and its xsl:attribute instructions, and the sets
// they use in turn, are motionless at f (§19.8.4.4: a streamable attribute
// set is motionless). They then run as the tree runs them, on the view.
func (b *streamBuilder) useSets(el, a *xdm.Node, f vfocus) bool {
	lre := el.Name().URI != xdm.NSXSL
	if n := a.Name(); n.Local != "use-attribute-sets" || lre && n.URI != xdm.NSXSL ||
		!lre && (n.URI != "" || !isXSL(el, "element") && !isXSL(el, "copy")) {
		return false
	}
	sets := attributeSetDeclarations(documentElementOf(el))
	return b.setsOK(el, a.Value(), f, sets, map[xdm.QName]bool{})
}

func (b *streamBuilder) setsOK(el *xdm.Node, names string, f vfocus, sets map[xdm.QName][]*xdm.Node, seen map[xdm.QName]bool) bool {
	for _, n := range strings.Fields(names) {
		q, err := resolveQNameAttr(el, n)
		if err != nil || len(sets[q]) == 0 {
			return b.refuse("attribute set " + n)
		}
		if seen[q] {
			continue
		}
		seen[q] = true
		for _, d := range sets[q] {
			if !isYes(strings.TrimSpace(d.AttrValue("streamable"))) {
				return b.refuse("attribute set " + n + " is not streamable")
			}
			for a := range d.Attrs() {
				switch a.Name().Local {
				case "name", "streamable", "visibility":
				case "use-attribute-sets":
					if !b.setsOK(d, a.Value(), f, sets, seen) {
						return false
					}
				default:
					if a.Name().URI == "" {
						return b.refuse("attribute " + a.Name().Local + " of xsl:attribute-set")
					}
				}
			}
			for ch := range d.Children() {
				if ch.Kind() == xdm.KindElement && (!isXSL(ch, "attribute") || !b.motionless(ch, f)) {
					return b.refuse("attribute set " + n + ": " + elName(ch))
				}
			}
		}
	}
	return true
}
