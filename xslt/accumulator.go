package xslt

import (
	"fmt"
	"strconv"
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
	name xdm.QName
	// pkg is the package the declaration was written in. 3.5.5 makes
	// accumulators local to their package, so two packages may declare one
	// name differently and a call resolves against the package the CALL is
	// written in. See accumulatorFor.
	pkg     int
	initial *xpath.Compiled
	// asType is @as, applied to the initial value and to the result of every
	// rule. Section 18.1 applies the function conversion rules at each step,
	// so it converts as well as checks — an untyped attribute feeding an
	// xs:integer accumulator is atomised and cast, not rejected.
	asType *sequenceType
	rules  []*accumulatorRule
	// streamable is @streamable="yes". Nothing here streams, so it only
	// matters for XTDE3362: reading a non-streamable accumulator over a
	// document that xsl:source-document asked to stream is an error even for
	// a processor that answered the request by not streaming.
	streamable bool
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
	priority  float64
	hasPrio   bool
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
	// pkg is the package whose declaration produced these values, for the
	// reason keyCacheKey carries one: two packages declaring an accumulator
	// of the same name compute different values over the same tree, and a
	// cache on the name alone returned whichever asked first to both.
	pkg int
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
	def.streamable = isYes(el.AttrValue("streamable"))

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
		c.accumNames = map[string]xdm.QName{}
	}
	key := def.name.Clark()
	// A tie is not reported here. An importing module's declaration masks the
	// imported one entirely, and the imported module may perfectly well
	// contain a tie among the declarations the importer overrides — which is
	// what accumulator-027 relies on. The tie is therefore judged over the
	// declarations that are still *visible* once every module has been
	// compiled; see checkAccumulatorConflicts.
	if c.accumTies == nil {
		c.accumTies = map[string][]int{}
	}
	c.accumTies[key] = append(c.accumTies[key], precedence)
	c.accumNames[key] = qn
	if prev, ok := c.accumPrecedence[key]; ok && prev > precedence {
		return nil
	}
	c.declOrder++
	def.declOrder = c.declOrder
	c.accumPrecedence[key] = precedence
	// Filed under the package as well as the name. 3.5.5 makes accumulators
	// local to their package, so two packages may each declare "ac" and both
	// declarations have to survive -- keeping one per name let the second
	// compiled overwrite the first, and the package that lost its own then
	// found no accumulator of that name at all. override-misc-005 declares
	// "ac" counting up in the used package and down in the using one.
	//
	// Precedence, ties and declaration order stay keyed by name alone: those
	// are questions about one package's declarations, which is the only place
	// two declarations of a name can legitimately compete.
	def.pkg = compilePackage
	c.sheet.accumulators[accumKey(compilePackage, key)] = def
	c.sheet.accumOrder = append(c.sheet.accumOrder, key)
	return nil
}

// referencesValueVar reports whether an accumulator rule's match pattern names
// $value, skipping occurrences inside string literals so that
// match="p[@x = '$value']" is not mistaken for one.
//
// A prefixed name such as $my:value is a different variable and is left alone;
// only the unprefixed "value" is the one §18.2 declares.
func referencesValueVar(src string) bool {
	var quote byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c != '$' {
			continue
		}
		j := i + 1
		for j < len(src) && (src[j] == ':' || src[j] == '-' || src[j] == '_' ||
			src[j] == '.' ||
			(src[j] >= 'a' && src[j] <= 'z') ||
			(src[j] >= 'A' && src[j] <= 'Z') ||
			(src[j] >= '0' && src[j] <= '9')) {
			j++
		}
		if src[i+1:j] == "value" {
			return true
		}
		i = j - 1
	}
	return false
}

// compileAccumulatorRule compiles one xsl:accumulator-rule.
func (c *compiler) compileAccumulatorRule(el *xdm.Node) (*accumulatorRule, error) {
	match := strings.TrimSpace(el.AttrValue("match"))
	if match == "" {
		return nil, fmt.Errorf(
			"XTSE0010: xsl:accumulator-rule requires a match attribute")
	}
	// $value is bound only for the rule's select expression and sequence
	// constructor. XSLT 3.0 §18.2 gives the scope of the implicitly declared
	// $value as "the select attribute or contained sequence constructor of
	// the xsl:accumulator-rule element", which excludes its own match
	// pattern, so a reference from there is a reference to an undeclared
	// variable: XPST0008.
	//
	// The check is textual and made before the pattern is compiled, for the
	// same reason globalRefs is textual: the compiled pattern does not carry
	// its variable references, and an unbound name in a pattern is otherwise
	// only discovered when a node happens to reach the rule - which for
	// accumulator-091 is never, because the accumulator is applied through a
	// mode and the pattern is evaluated in a context where the outer $value
	// was still visible.
	if referencesValueVar(match) {
		return nil, fmt.Errorf(
			"XPST0008: undeclared variable $value in " +
				"xsl:accumulator-rule/@match: $value is in scope only in the " +
				"rule's select expression or sequence constructor")
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

	// The package comes from the declaration, which is already the one the
	// calling package's lookup chose, so an index built from one package's
	// declaration is never handed to another's call of the same name.
	key := accumCacheKey{name: def.name.Clark(), tree: root.Tree(), pkg: def.pkg}
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
		// Attributes and namespaces are NOT visited. §18.2.4's formal model
		// defines an accumulator over the sequence of events a streamed
		// traversal of the tree produces, and those events are the start and
		// end of each element and the arrival of each text, comment and
		// processing-instruction node; an attribute arrives as part of its
		// element's start event rather than as an event of its own, so no
		// rule ever fires for one. A pattern matching an attribute is legal
		// to write and simply never matches anything the walk offers.
		//
		// accumulator-026 is named for exactly this - "Rules matching
		// attribute nodes are legal but ignored" - and its third rule,
		// match="@alt" select="$value + 100000", would otherwise add 100000
		// to every figure number.
		//
		// The before and after maps are left without entries for attribute
		// nodes for the same reason; accumulator-before on an attribute is
		// XTTE3360 and never reaches the map.
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
			"XTDE3340: %s() was called with no accumulator name", fname)
	}
	lex := strings.TrimSpace(atoms[0].(*xdm.Atomic).String())
	uri, local := "", ""
	switch {
	case isEQName(lex):
		// The URIQualifiedName form carries its own namespace, so no prefix
		// need be in scope for it — which is the point of writing one.
		end := strings.IndexByte(lex, '}')
		uri, local = lex[2:end], lex[end+1:]
	case isLexicalQName(lex):
		var prefix string
		prefix, local = xdm.SplitQName(lex)
		if prefix != "" {
			bound := false
			if uri, bound = rt.sheet.prefixes[prefix]; !bound {
				return nil, fmt.Errorf(
					"XTDE3340: %s(%q): no namespace declaration is in scope "+
						"for the prefix %q", fname, lex, prefix)
			}
		}
	default:
		return nil, fmt.Errorf(
			"XTDE3340: %s(%q): the name is not a valid QName", fname, lex)
	}
	name := xdm.QName{URI: uri, Local: local}.Clark()
	// 3.5.5 scopes an accumulator to the package that declares it, so which
	// declaration answers is decided by where the call is WRITTEN; the
	// context carries that. override-misc-005 declares "ac" in both packages,
	// counting up in one and down in the other.
	def, ok := rt.sheet.accumulatorFor(packageOf(ctx), name)
	if !ok {
		return nil, fmt.Errorf(
			"XTDE3340: no xsl:accumulator is named %q", lex)
	}
	// XTDE3400 also covers reading an accumulator the current mode does not
	// list in @use-accumulators.
	if !rt.sheet.accumulatorInScope(rt.sel.mode, name) {
		return nil, fmt.Errorf(
			"XTDE3400: accumulator %q is not available in the current mode, "+
				"which does not name it in use-accumulators", lex)
	}

	// 18.2 splits the focus errors three ways: an absent context item is
	// XTDE3350, while a context item that is not a node — or is an attribute
	// or namespace node, neither of which an accumulator rule can match — is
	// the type error XTTE3360.
	if ctx.Item == nil {
		return nil, fmt.Errorf(
			"XTDE3350: %s(%q) was called with no context item", fname, lex)
	}
	node, ok := ctx.Item.(*xdm.Node)
	if !ok {
		return nil, fmt.Errorf(
			"XTTE3360: %s(%q): the context item is not a node", fname, lex)
	}
	if node.Kind == xdm.KindAttribute || node.Kind == xdm.KindNamespace {
		return nil, fmt.Errorf(
			"XTTE3360: %s(%q): the context item is an %v", fname, lex, node.Kind)
	}
	// A node produced by a copy-accumulators="yes" copy answers with the
	// value its original had, not with what the rules would compute over the
	// copy's own tree.
	node = rt.accumulatorOrigin(node)
	// 18.2.2 narrows the applicable set for a document read with an explicit
	// use-accumulators list — an xsl:merge-source is where this engine can
	// say so — and XTDE3362 is reading one the list leaves out. The check is
	// here rather than at the call site because an accumulator rule may reach
	// another accumulator, which is how merge-067 gets at a name its
	// merge source never listed.
	// 18.2 makes reading a non-streamable accumulator over a document that
	// xsl:source-document asked to stream an error in its own right. This
	// engine answers streamable="yes" by not streaming, which the spec
	// permits, but the restriction the stylesheet asked for still stands.
	if !def.streamable && rt.streamedTrees[node.Root()] {
		return nil, fmt.Errorf(
			"XTDE3362: accumulator %q is not declared streamable, so it "+
				"cannot be read over a streamed document", lex)
	}
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

// noteCopiedAccumulators records that copy was made from orig, so that an
// accumulator asked about the copy answers with the original's value.
//
// Section 18.3: xsl:copy-of and xsl:copy with copy-accumulators="yes" produce
// a copy "with the same accumulator values as the original". The copy is in a
// tree of its own, where the accumulator rules would compute something else
// entirely — usually the initial value, since the copy's subtree is all the
// document there is. Nothing but the correspondence to the original can
// supply the right answer, so it is recorded here and consulted before the
// walk.
//
// The correspondence is recorded for the whole subtree at once: a copy is deep
// and the copies of the descendants have to answer as their originals did too.
func (rt *runtime) noteCopiedAccumulators(orig, copy *xdm.Node) {
	if rt.accumOrigin == nil {
		return
	}
	var pair func(a, b *xdm.Node)
	pair = func(a, b *xdm.Node) {
		if a == nil || b == nil {
			return
		}
		rt.accumOrigin[b] = a
		for i := range b.Children {
			if i < len(a.Children) {
				pair(a.Children[i], b.Children[i])
			}
		}
	}
	pair(orig, copy)
}

// accumulatorOrigin follows a node back to the node it was copied from, or
// returns it unchanged. The chain is followed to its end so that a copy of a
// copy still answers with the original's value.
func (rt *runtime) accumulatorOrigin(n *xdm.Node) *xdm.Node {
	for i := 0; i < 64; i++ {
		orig, ok := rt.accumOrigin[n]
		if !ok {
			return n
		}
		n = orig
	}
	return n
}

// checkAccumulatorConflicts reports XTSE3350 for a name declared twice at the
// precedence that wins.
//
// The rule is about the accumulators a package can see, and an importing
// module's declaration masks the imported one entirely — so a tie among
// declarations that are all overridden is invisible and not an error.
// Judging it as each declaration was compiled reported exactly that
// invisible tie, because the module holding it had been compiled before the
// module that overrides it.
func (c *compiler) checkAccumulatorConflicts() error {
	for key, precs := range c.accumTies {
		best := precs[0]
		n := 0
		for _, p := range precs {
			if p > best {
				best, n = p, 0
			}
			if p == best {
				n++
			}
		}
		if n > 1 {
			return fmt.Errorf(
				"XTSE3350: two xsl:accumulator declarations are named %s at "+
					"the same import precedence", c.accumNames[key].Lexical())
		}
	}
	return nil
}

// accumulatorFor returns the xsl:accumulator declaration of one name that is
// in scope for a call written in pkg.
//
// Section 3.5.5 makes accumulators local to the package declaring them, so a
// call sees its own package's declaration and not another's. The table holds
// one declaration per name, which is what import precedence leaves; the
// package is therefore matched rather than selected among, and a name the
// calling package does not declare is absent rather than inherited.
//
// A declaration compiled outside any package carries zero, so a plain
// stylesheet -- and every module of the top-level package -- keeps everything
// it declares.
func (s *Stylesheet) accumulatorFor(pkg int, name string) (*accumulatorDef, bool) {
	def, ok := s.accumulators[accumKey(pkg, name)]
	return def, ok
}

// accumKey qualifies an accumulator's Clark name with its package.
//
// The package number is prefixed rather than appended because a Clark name
// may contain anything after the closing brace, while the separator here
// cannot appear in a decimal integer.
func accumKey(pkg int, name string) string {
	if pkg == 0 {
		// The top-level package's names are unqualified so that every table
		// keyed by accumulator name elsewhere -- accumOrder, the mode
		// use-accumulators check -- keeps working unchanged for the
		// overwhelming majority of stylesheets, which use no package at all.
		return name
	}
	return strconv.Itoa(pkg) + " " + name
}
