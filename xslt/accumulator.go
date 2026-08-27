package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// accumulatorDef is a compiled xsl:accumulator declaration.
//
// Section 18 makes an accumulator a value that varies as a document is read in
// document order: a starting value, and a set of rules that recompute it at
// the start and at the end of the nodes they match. The two values a node
// therefore has — the value on the way in and the value on the way out — are
// what fn:accumulator-before and fn:accumulator-after return.
type accumulatorDef struct {
	name    xdm.QName
	initial *xpath.Compiled
	// asType is @as, applied to the initial value and to the result of every
	// rule. Section 18.1 applies the function conversion rules at each step,
	// so it converts as well as checks — an untyped attribute feeding an
	// xs:integer accumulator is atomised and cast, not rejected.
	asType *sequenceType
	rules  []*accumulatorRule
	// declOrder is the position of the declaration in the stylesheet, used
	// to break priority ties between two rules of the same accumulator the
	// same way the template conflict rules do.
	declOrder int
}

// accumulatorRule is one xsl:accumulator-rule.
type accumulatorRule struct {
	match *Pattern
	// post is @phase="end": the rule fires on the way out of the matched
	// node rather than on the way in.
	post bool
	// select and body are the two ways of giving the new value; exactly one
	// is set, as for xsl:key/@use.
	select_ *xpath.Compiled
	body    []Instruction
	// priority is the rule's effective priority, from @priority or from the
	// pattern's default. Two rules of one accumulator that both match a node
	// conflict, and the higher priority wins.
	priority float64
	hasPrio  bool
	declOrder int
}

// accumulatorValues holds one accumulator's value at every node of one tree.
//
// The values are computed by a single pre/post traversal rather than on demand
// per node: a rule's value depends on the value the previous matched node left
// behind, so answering for one node means having walked everything before it
// anyway. Doing the walk once per (accumulator, tree) pair makes a stylesheet
// that reads an accumulator inside a template rule linear rather than
// quadratic.
type accumulatorValues struct {
	before map[*xdm.Node]xdm.Sequence
	after  map[*xdm.Node]xdm.Sequence
}

// accumCacheKey identifies one accumulator over one tree.
type accumCacheKey struct {
	name string
	tree *xdm.Tree
}

// compileAccumulator compiles an xsl:accumulator declaration.
func (c *compiler) compileAccumulator(el *xdm.Node, precedence int) error {
	name := strings.TrimSpace(el.AttrValue("name"))
	if name == "" {
		return fmt.Errorf("XTSE0010: xsl:accumulator requires a name attribute")
	}
	qn, err := resolveQNameAttr(el, name)
	if err != nil {
		return err
	}
	initial := el.Attr("", "initial-value")
	if initial == nil {
		return fmt.Errorf(
			"XTSE0010: xsl:accumulator %s has no initial-value attribute",
			qn.Lexical())
	}
	ns := newNSResolver(el, "")
	def := &accumulatorDef{name: xdm.QName{URI: qn.URI, Local: qn.Local}}
	if def.initial, err = compileExpr(initial.Value, ns); err != nil {
		return err
	}
	if a := el.Attr("", "as"); a != nil {
		if def.asType, err = compileSequenceType(a.Value, ns); err != nil {
			return fmt.Errorf("in xsl:accumulator/@as: %w", err)
		}
	}

	for _, ch := range el.ChildElements() {
		if !isXSL(ch, "accumulator-rule") {
			// The element table has already refused anything else, so this
			// only guards against a namespace-alias oddity reaching here.
			continue
		}
		rule, err := c.compileAccumulatorRule(ch)
		if err != nil {
			return err
		}
		def.rules = append(def.rules, rule)
	}
	if len(def.rules) == 0 {
		return fmt.Errorf(
			"XTSE0010: xsl:accumulator %s declares no xsl:accumulator-rule",
			qn.Lexical())
	}

	// XTSE3350: "it is a static error if two accumulator declarations with
	// the same name are visible", which for a single package means two
	// declarations at the same import precedence. A higher precedence
	// legitimately overrides a lower one.
	if c.sheet.accumulators == nil {
		c.sheet.accumulators = map[string]*accumulatorDef{}
		c.accumPrecedence = map[string]int{}
	}
	key := def.name.Clark()
	if prev, ok := c.accumPrecedence[key]; ok {
		if prev == precedence {
			return fmt.Errorf(
				"XTSE3350: two xsl:accumulator declarations are named %s at "+
					"the same import precedence", qn.Lexical())
		}
		if prev > precedence {
			return nil
		}
	}
	c.declOrder++
	def.declOrder = c.declOrder
	c.accumPrecedence[key] = precedence
	c.sheet.accumulators[key] = def
	c.sheet.accumOrder = append(c.sheet.accumOrder, key)
	return nil
}

// compileAccumulatorRule compiles one xsl:accumulator-rule.
func (c *compiler) compileAccumulatorRule(el *xdm.Node) (*accumulatorRule, error) {
	match := strings.TrimSpace(el.AttrValue("match"))
	if match == "" {
		return nil, fmt.Errorf(
			"XTSE0010: xsl:accumulator-rule requires a match attribute")
	}
	ns := newNSResolver(el, "")
	pat, err := CompilePattern(match, ns)
	if err != nil {
		return nil, err
	}
	c.declOrder++
	r := &accumulatorRule{
		match:     pat,
		post:      strings.TrimSpace(el.AttrValue("phase")) == "end",
		priority:  pat.Priority(),
		declOrder: c.declOrder,
	}
	if p := el.Attr("", "priority"); p != nil {
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(p.Value), "%g", &f); err != nil {
			return nil, fmt.Errorf(
				"XTSE0530: xsl:accumulator-rule/@priority=%q is not a number",
				p.Value)
		}
		r.priority, r.hasPrio = f, true
	}

	sel := el.Attr("", "select")
	hasBody := len(el.ChildElements()) > 0
	switch {
	case sel != nil && hasBody:
		// The same rule xsl:key has: giving the value both ways leaves no
		// way to reconcile them.
		return nil, fmt.Errorf(
			"XTSE3300: xsl:accumulator-rule has both a select attribute and " +
				"a sequence constructor")
	case sel != nil:
		if r.select_, err = compileExpr(sel.Value, ns); err != nil {
			return nil, err
		}
	case hasBody:
		if r.body, err = c.compileSequence(el, el); err != nil {
			return nil, err
		}
	default:
		// An empty rule with no select is legal and yields the empty
		// sequence, which is how a rule resets an accumulator to nothing.
		r.select_ = nil
	}
	return r, nil
}

// modeAccumulators is the set of accumulators xsl:mode/@use-accumulators makes
// available in a mode, or nil when the mode names none.
//
// A nil entry and an absent one differ: an absent mode has no declaration and
// so no accumulators, while "#all" is recorded as the whole set. Both are
// answered by accumulatorInScope.
type modeAccumulators struct {
	all   bool
	names map[string]bool
}

// compileModeAccumulators records xsl:mode/@use-accumulators for a mode.
func (c *compiler) compileModeAccumulators(el *xdm.Node, mode string) error {
	a := el.Attr("", "use-accumulators")
	if a == nil {
		return nil
	}
	set := &modeAccumulators{names: map[string]bool{}}
	for _, tok := range strings.Fields(a.Value) {
		if tok == "#all" {
			set.all = true
			continue
		}
		if tok == "#default" {
			// Only meaningful inside a package that sets a default, which
			// this processor does not model; treated as naming nothing.
			continue
		}
		qn, err := resolveQNameAttr(el, tok)
		if err != nil {
			return err
		}
		set.names[xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()] = true
	}
	if c.sheet.modeAccums == nil {
		c.sheet.modeAccums = map[string]*modeAccumulators{}
	}
	c.sheet.modeAccums[mode] = set
	return nil
}

// accumulatorInScope reports whether an accumulator may be read in a mode.
//
// Section 18.2 raises XTDE3400 for a call naming an accumulator the current
// mode does not use. Since this processor never streams, restricting the
// answer costs nothing but conformance — but a stylesheet that declares no
// modes at all clearly means every accumulator to be usable, so an
// undeclared mode is permissive rather than empty.
func (s *Stylesheet) accumulatorInScope(mode, name string) bool {
	set, ok := s.modeAccums[mode]
	if !ok {
		return true
	}
	return set.all || set.names[name]
}

// accumulatorValuesFor computes, and caches, one accumulator's value at every
// node of the tree containing root.
func (rt *runtime) accumulatorValuesFor(def *accumulatorDef, root *xdm.Node,
	ctx *xpath.Context) (*accumulatorValues, error) {

	key := accumCacheKey{name: def.name.Clark(), tree: root.Tree()}
	if v, ok := rt.accumValues[key]; ok {
		return v, nil
	}
	// A rule whose own expression reads this accumulator over this tree is
	// circular: the value it asks for is the one being computed. The cache is
	// written only when the walk finishes, so without this mark the
	// re-entrant call would start a second walk and recurse until the depth
	// guard fired with the wrong diagnosis.
	if rt.accumBuilding[key] {
		return nil, fmt.Errorf(
			"XTDE3400: accumulator %s is defined circularly: a rule reads "+
				"the accumulator it computes", def.name.Lexical())
	}
	rt.accumBuilding[key] = true
	defer delete(rt.accumBuilding, key)

	vals := &accumulatorValues{
		before: map[*xdm.Node]xdm.Sequence{},
		after:  map[*xdm.Node]xdm.Sequence{},
	}
	cur, err := def.initial.Eval(ctx.WithFocus(root, 1, 1).
		WithVar(currentVar, xdm.One(root)))
	if err != nil {
		return nil, fmt.Errorf(
			"evaluating xsl:accumulator %s initial-value: %w",
			def.name.Lexical(), err)
	}
	if cur, err = rt.convertAccum(def, cur); err != nil {
		return nil, err
	}

	// The walk is pre/post order over the whole tree: every node records the
	// value on the way in and the value on the way out, and the start and end
	// rules that match it move the value between the two.
	var walk func(*xdm.Node) error
	walk = func(n *xdm.Node) error {
		next, err := rt.applyAccumRules(def, n, cur, ctx, false)
		if err != nil {
			return err
		}
		cur = next
		vals.before[n] = cur
		// Attributes and namespaces are not descended into, but they are
		// visited: section 18.1 lets a rule match an attribute, and such a
		// node's before and after values are the same because it has no
		// children to descend past.
		for _, a := range n.Attrs {
			av, err := rt.applyAccumRules(def, a, cur, ctx, false)
			if err != nil {
				return err
			}
			cur = av
			vals.before[a] = cur
			ae, err := rt.applyAccumRules(def, a, cur, ctx, true)
			if err != nil {
				return err
			}
			cur = ae
			vals.after[a] = cur
		}
		for _, ch := range n.Children {
			if err := walk(ch); err != nil {
				return err
			}
		}
		end, err := rt.applyAccumRules(def, n, cur, ctx, true)
		if err != nil {
			return err
		}
		cur = end
		vals.after[n] = cur
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}

	rt.accumValues[key] = vals
	return vals, nil
}

// applyAccumRules runs the highest-priority rule of the requested phase that
// matches n, and returns the value it leaves behind.
//
// Section 18.1 resolves a conflict between two rules of one accumulator the
// way template rules are resolved: highest priority wins, and among equals the
// one declared last. Unlike template rules this is not an error to report, so
// no ambiguity diagnostic is raised.
func (rt *runtime) applyAccumRules(def *accumulatorDef, n *xdm.Node,
	cur xdm.Sequence, ctx *xpath.Context, post bool) (xdm.Sequence, error) {

	var best *accumulatorRule
	for _, r := range def.rules {
		if r.post != post {
			continue
		}
		ok, err := r.match.Matches(n, ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if best == nil || r.priority > best.priority ||
			(r.priority == best.priority && r.declOrder > best.declOrder) {
			best = r
		}
	}
	if best == nil {
		return cur, nil
	}

	// $value is the accumulator's value as the rule found it. It is bound as
	// an ordinary variable so that a rule body can read it from anywhere in a
	// sequence constructor, not only from @select.
	sub := ctx.WithFocus(n, 1, 1).
		WithVar(currentVar, xdm.One(n)).
		WithVar(xdm.QName{Local: "value"}, cur)

	var out xdm.Sequence
	var err error
	switch {
	case best.select_ != nil:
		if out, err = best.select_.Eval(sub); err != nil {
			return nil, err
		}
	case best.body != nil:
		r2 := rt.temporaryOutput()
		r2.ctx = sub
		ob := newOutputBuilder()
		if err := execSequence(best.body, r2, ob); err != nil {
			return nil, err
		}
		out = ob.sequence()
	default:
		out = xdm.Empty()
	}
	return rt.convertAccum(def, out)
}

// convertAccum applies xsl:accumulator/@as to a value.
func (rt *runtime) convertAccum(def *accumulatorDef, seq xdm.Sequence) (xdm.Sequence, error) {
	if def.asType == nil {
		return seq, nil
	}
	return def.asType.convertAs(seq,
		"the value of accumulator "+def.name.Lexical(), "XTTE3350")
}

// fnAccumulator implements fn:accumulator-before and fn:accumulator-after.
func fnAccumulator(rt *runtime, ctx *xpath.Context, args []xdm.Sequence,
	post bool) (xdm.Sequence, error) {

	fname := "accumulator-before"
	if post {
		fname = "accumulator-after"
	}
	atoms := xdm.Atomize(args[0])
	if len(atoms) == 0 {
		return nil, fmt.Errorf(
			"XTDE3400: %s() was called with no accumulator name", fname)
	}
	lex := atoms[0].(*xdm.Atomic).String()
	if !isLexicalQName(lex) {
		return nil, fmt.Errorf(
			"XTDE3400: %s(%q): the name is not a valid QName", fname, lex)
	}
	prefix, local := xdm.SplitQName(lex)
	uri := ""
	if prefix != "" {
		bound := false
		if uri, bound = rt.sheet.prefixes[prefix]; !bound {
			return nil, fmt.Errorf(
				"XTDE3400: %s(%q): no namespace declaration is in scope for "+
					"the prefix %q", fname, lex, prefix)
		}
	}
	name := xdm.QName{URI: uri, Local: local}.Clark()
	def, ok := rt.sheet.accumulators[name]
	if !ok {
		return nil, fmt.Errorf(
			"XTDE3400: no xsl:accumulator is named %q", lex)
	}
	// XTDE3400 also covers reading an accumulator the current mode does not
	// list in @use-accumulators.
	if !rt.sheet.accumulatorInScope(rt.sel.mode, name) {
		return nil, fmt.Errorf(
			"XTDE3400: accumulator %q is not available in the current mode, "+
				"which does not name it in use-accumulators", lex)
	}

	node, err := ctx.ContextNode()
	if err != nil {
		return nil, fmt.Errorf(
			"XTDE3400: %s(%q) has no context node", fname, lex)
	}
	// 18.2.2 narrows the applicable set for a document read with an explicit
	// use-accumulators list — an xsl:merge-source is where this engine can
	// say so — and XTDE3362 is reading one the list leaves out. The check is
	// here rather than at the call site because an accumulator rule may reach
	// another accumulator, which is how merge-067 gets at a name its
	// merge source never listed.
	if set, ok := rt.treeAccums[node.Root()]; ok && !set.all && !set.names[name] {
		return nil, fmt.Errorf(
			"XTDE3362: accumulator %q is not applicable to this document, "+
				"whose use-accumulators list does not name it", lex)
	}
	// Section 18.2: an accumulator applies to the tree the node belongs to,
	// so the walk starts at that tree's root even when the node is deep in
	// it — the value at a node depends on everything that precedes it.
	vals, err := rt.accumulatorValuesFor(def, node.Root(), ctx)
	if err != nil {
		return nil, err
	}
	if post {
		return vals.after[node], nil
	}
	return vals.before[node], nil
}
