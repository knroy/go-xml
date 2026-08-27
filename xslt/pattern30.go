package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// The XSLT 3.0 pattern forms that the right-to-left step walk cannot express.
//
// XSLT 2.0 patterns are a restricted path syntax chosen precisely so that a
// candidate node can be tested by walking *up* from it: every step names an
// axis whose inverse is cheap, so a match costs O(depth) instead of O(document
// size). XSLT 3.0 section 5.5.2 widens the grammar past what that trick
// covers, in three directions at once:
//
//   - a PredicatePattern, ".[E]", which constrains the node itself rather than
//     its position in a tree — and which is the only pattern form that can
//     match an atomic value, since an atomic value has no ancestry at all;
//   - intersect and except between patterns, and parenthesised groups used as
//     a step, whose result is a set operation rather than an axis;
//   - a pattern that starts from something other than the candidate's own
//     tree: a variable reference, doc(), or root().
//
// Section 5.5.3 defines all three the same way, by the *equivalent
// expression*: a node N matches such a pattern P if N is one of the nodes the
// expression selects. That is what generalPattern evaluates. The cost is real
// — the expression is evaluated once per candidate node rather than once —
// which is why it is a fallback for the forms the step walk cannot handle
// rather than the way every pattern is matched.
type generalPattern struct {
	// expr is the equivalent expression, evaluated with the candidate node
	// as the context item.
	expr *xpath.Compiled
	// selfOnly marks the ".[E]" form, where the expression is the predicate
	// and matching is "the predicate holds of the candidate" rather than
	// "the candidate is among the nodes selected". The distinction matters
	// because ".[E]" must also match atomic values, which are never members
	// of a node sequence.
	selfOnly bool
	// rooted marks a pattern whose equivalent expression names its own
	// starting point — "$v//x", "doc('a.xml')/b", "root()[self::A]" — so it
	// is evaluated once against the candidate's context rather than tried
	// from each of the candidate's ancestors.
	rooted bool
	// prio is the default priority section 6.4 gives the form.
	prio float64
	src  string
}

// compileGeneralPattern compiles a pattern the step grammar rejected, when it
// is one of the XSLT 3.0 forms the equivalent-expression rule covers.
//
// It returns nil, nil when src is not such a form, so that the caller reports
// the original XTSE0340 rather than a second, less useful one.
func compileGeneralPattern(src string, ns xpath.NamespaceResolver) (*generalPattern, error) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		return nil, nil
	}

	// ".[E]" — the PredicatePattern. The whole of E is the predicate, and the
	// candidate item is what it is evaluated against, so the pattern is used
	// as written: "." with its predicates yields the candidate itself exactly
	// when every predicate holds.
	if strings.HasPrefix(trimmed, ".") {
		rest := strings.TrimSpace(trimmed[1:])
		if rest == "" || strings.HasPrefix(rest, "[") {
			expr, err := compileExpr(trimmed, ns)
			if err != nil {
				return nil, err
			}
			// Section 6.4 gives a PredicatePattern the priority of a pattern
			// with predicates, which is 0.5.
			prio := 0.5
			if rest == "" {
				// "." alone constrains nothing, so it scores as a bare kind
				// test would.
				prio = -0.5
			}
			return &generalPattern{
				expr: expr, selfOnly: true, prio: prio, src: trimmed,
			}, nil
		}
		return nil, nil
	}

	if !isGeneralPatternForm(trimmed) {
		return nil, nil
	}
	expr, err := compileExpr(trimmed, ns)
	if err != nil {
		return nil, err
	}
	return &generalPattern{
		expr:   expr,
		rooted: startsFromOwnRoot(trimmed),
		// Every form that reaches here is a multi-step or operator pattern,
		// which section 6.4 scores 0.5 like any other complex pattern.
		prio: 0.5,
		src:  trimmed,
	}, nil
}

// isGeneralPatternForm reports whether src is one of the XSLT 3.0 pattern
// shapes the equivalent-expression rule covers.
//
// The test is on the source rather than on the parsed AST because the AST has
// already lost the distinction that matters: "a|b" and "(a|b)/c" parse to
// shapes the step converter refuses for the same reason, but only the second
// is a pattern this can help with. Anything not recognised here falls back to
// the original XTSE0340, which names the step the author actually wrote.
func isGeneralPatternForm(src string) bool {
	switch {
	case strings.HasPrefix(src, "$"):
		// A variable reference as the pattern root, "$v" or "$v//baz".
		return true
	case strings.HasPrefix(src, "("):
		// A parenthesised group, used either as the whole pattern or as the
		// first step of one: "(a|b)[1]", "(/)[doc]".
		return true
	}
	// An operator that is not an axis: intersect and except combine two node
	// sets, and neither is expressible as a walk up from the candidate.
	for _, op := range []string{" intersect ", " except "} {
		if containsTopLevel(src, op) {
			return true
		}
	}
	// A call other than id() or key() as the pattern root — doc(), root(),
	// element-with-id() — followed by nothing or by further steps.
	if name, ok := leadingCallName(src); ok {
		switch name {
		case "id", "key":
			// These the step grammar already handles, and better: it matches
			// them by membership without evaluating the rest of the path.
			return false
		}
		return true
	}
	// A parenthesised group appearing as a later step, "x/(a|b)/text()".
	return containsTopLevel(src, "/(")
}

// startsFromOwnRoot reports whether the pattern names the tree it selects
// from, rather than being relative to the candidate node.
//
// Such a pattern is evaluated once, against the candidate's own focus, and the
// candidate is looked for in the result. A relative one has to be tried from
// each of the candidate's ancestors instead, because "a/b" as a pattern means
// "a b whose parent is an a", not "a b among this node's children".
func startsFromOwnRoot(src string) bool {
	if strings.HasPrefix(src, "$") || strings.HasPrefix(src, "/") {
		return true
	}
	name, ok := leadingCallName(src)
	return ok && name != "id" && name != "key"
}

// leadingCallName returns the name of a function call at the start of src.
func leadingCallName(src string) (string, bool) {
	i := 0
	for i < len(src) && (isQNameChar(src[i]) || src[i] == ':') {
		i++
	}
	if i == 0 {
		return "", false
	}
	name := src[:i]
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n') {
		i++
	}
	if i >= len(src) || src[i] != '(' {
		return "", false
	}
	// Strip a prefix: only the local name decides whether the call is one the
	// step grammar already handles.
	if k := strings.LastIndex(name, ":"); k >= 0 {
		name = name[k+1:]
	}
	return name, true
}

// containsTopLevel reports whether sub occurs in s outside brackets, parens
// and string literals, the same scope splitTopLevel respects.
func containsTopLevel(s, sub string) bool {
	depth, quote := 0, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		default:
			if depth == 0 && strings.HasPrefix(s[i:], sub) {
				return true
			}
		}
	}
	return false
}

// matches reports whether node satisfies the pattern.
func (g *generalPattern) matches(node *xdm.Node, ctx *xpath.Context) (bool, error) {
	if g.selfOnly {
		// ".[E]" selects the context item when every predicate holds, so
		// evaluating it against the candidate and asking whether anything
		// came back is the whole test.
		seq, err := g.expr.Eval(ctx.WithFocus(node, 1, 1))
		if err != nil {
			return false, err
		}
		return len(seq) > 0, nil
	}
	if g.rooted {
		seq, err := g.expr.Eval(ctx.WithFocus(node, 1, 1))
		if err != nil {
			return false, err
		}
		return containsNode(seq, node), nil
	}
	// A relative pattern is anchored at some ancestor of the candidate: "a/b"
	// matches a b whose parent is an a, wherever that a is. Section 5.5.3
	// says so by making the equivalent expression "root(.)//" + P, so the
	// anchor may be any node on the candidate's ancestor-or-self chain, not
	// only its parent — and the walk starts at the candidate itself so that
	// a one-step pattern still finds it.
	//
	// A temporary tree makes the chain matter: the x built by an
	// xsl:variable is its own root, so "x/(a|b)" has to be evaluated from
	// that x's *parent*, which does not exist — the anchor is the x itself
	// and the expression is evaluated from one level above where the first
	// step names. Walking the whole chain covers both.
	for anc := node; ; anc = anc.Parent {
		seq, err := g.expr.Eval(ctx.WithFocus(anc, 1, 1))
		if err != nil {
			// A pattern evaluated against a node it was not written for is a
			// non-match rather than a failure; 5.5.4 says so, and Matches
			// applies the same rule to the step forms.
			if !recoverPatternError(err) {
				return false, err
			}
		} else if containsNode(seq, node) {
			return true, nil
		}
		if anc.Parent == nil {
			// The chain ends at the root. A relative pattern is still
			// anchored below it — "x/(a|b)" against a tree whose root is the
			// x needs the x's own parent, which the chain does not have — so
			// the root's descendants are tried as anchors too, which is what
			// the "//" in the equivalent expression amounts to.
			return g.matchesUnderRoot(anc, node, ctx)
		}
	}
}

// matchesUnderRoot tries every descendant of root as the anchor for a relative
// pattern, stopping as soon as one selects node.
//
// This is the "//" of the equivalent expression root(.)//P made explicit. It
// is only reached once the ancestor chain has been exhausted, and only for the
// XSLT 3.0 forms, so the ordinary step patterns never pay for it.
func (g *generalPattern) matchesUnderRoot(root, node *xdm.Node,
	ctx *xpath.Context) (bool, error) {

	for _, ch := range root.Children {
		if ch.Kind != xdm.KindElement && ch.Kind != xdm.KindDocument {
			continue
		}
		seq, err := g.expr.Eval(ctx.WithFocus(ch, 1, 1))
		if err != nil {
			if !recoverPatternError(err) {
				return false, err
			}
		} else if containsNode(seq, node) {
			return true, nil
		}
		ok, err := g.matchesUnderRoot(ch, node, ctx)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// containsNode reports whether seq holds node, by identity.
func containsNode(seq xdm.Sequence, node *xdm.Node) bool {
	for _, it := range seq {
		if n, ok := it.(*xdm.Node); ok && n == node {
			return true
		}
	}
	return false
}

// matchesAtomic reports whether an atomic value satisfies the pattern.
//
// Only the ".[E]" form can: every other pattern selects nodes, and an atomic
// value is never among them. This is what lets xsl:apply-templates over a
// sequence of integers dispatch on ".[. mod 3 = 0]".
func (g *generalPattern) matchesAtomic(item xdm.Item, ctx *xpath.Context) (bool, error) {
	if !g.selfOnly {
		return false, nil
	}
	seq, err := g.expr.Eval(ctx.WithFocus(item, 1, 1))
	if err != nil {
		return false, err
	}
	return len(seq) > 0, nil
}

// selfAxisStep converts a self:: step, which the XSLT 3.0 grammar admits and
// the XSLT 2.0 one does not.
//
// "self::foo" constrains the candidate rather than its parent, so it is the
// one explicit axis that the right-to-left walk can take directly: it becomes
// a step matched against the node itself. The child-or-top widening does not
// apply to it, which is why explicitAxis is set.
func selfAxisStep(s *xpath.Step) (patternStep, bool) {
	if s.Axis != xpath.AxisSelf {
		return patternStep{}, false
	}
	return patternStep{
		nodeTest:     s.Test,
		preds:        s.Predicates,
		explicitAxis: true,
		self:         true,
	}, true
}

// checkQBraceName reports a Q{uri}local name the XPath parser refused.
//
// XPath 3.0 added the URIQualifiedName form, and a pattern may use it wherever
// a QName is allowed. This is a diagnostic only: whether the parser accepts it
// is decided in the XPath package, and a pattern reaching here has already
// failed there.
func checkQBraceName(src string) error {
	if strings.Contains(src, "Q{") {
		return fmt.Errorf(
			"the URIQualifiedName form Q{uri}local is not accepted in a " +
				"pattern by this build")
	}
	return nil
}

// patternsAllow30 reports whether the pattern being compiled may use the
// forms XSLT 3.0 added.
//
// A 2.0 stylesheet must get XTSE0340 for them, not a working match: telling it
// that "self::foo" or ".[E]" is a pattern would silently change which template
// fires. The answer rides on the resolver because that is what already carries
// the version of the element the pattern was written on, and a pattern is a
// static property of that element exactly as its base URI is.
func patternsAllow30(ns xpath.NamespaceResolver) bool {
	r, ok := ns.(*nsResolver)
	// Exactly the 3.0 family, not "3.0 or later". A stylesheet declaring
	// version="25.0" is in forwards-compatible mode: it is processed by the
	// 2.0 rules with unknown constructs ignored, and its patterns are still
	// judged by the 2.0 grammar — version-023 checks that "/(a|b)" is
	// XTSE0340 there rather than being quietly accepted.
	return ok && r.xsltVersion >= 3.0 && r.xsltVersion < 4.0
}

// declaredXSLTVersion returns the XSLT version stated on el or on the nearest
// ancestor that states one, defaulting to 2.0 as versionAt does.
func declaredXSLTVersion(el *xdm.Node) float64 {
	for a := el; a != nil; a = a.Parent {
		if a.Kind == xdm.KindElement && hasVersionAttr(a) {
			return versionAt(a)
		}
	}
	return 2.0
}

// matchesAtomicItem reports whether an atomic value matches the pattern.
//
// Only the ".[E]" form can match one; every other pattern selects nodes, and
// an atomic value is never among them. This is what lets xsl:apply-templates
// over a sequence of integers dispatch on ".[. mod 3 = 0]".
func (p *Pattern) matchesAtomicItem(item xdm.Item, ctx *xpath.Context) (bool, error) {
	ctx = ctx.WithVar(currentVar, xdm.One(item))
	// Section 24.3 clears the current output URI while a pattern is
	// evaluated, whether the item being matched is a node or an atomic
	// value. Pattern.Matches does the same for the node case;
	// current-output-uri-008 matches atomic values with
	// group-starting-with and needs both.
	ctx = ctx.WithVar(outputURIVar, xdm.Empty())
	for _, g := range p.general {
		ok, err := g.matchesAtomic(item, ctx)
		if err != nil {
			if recoverPatternError(err) {
				continue
			}
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// applyToAtomic selects and runs the best-matching template rule for an atomic
// value, or applies the built-in rule when none matches.
//
// Section 6.7.1 gives an atomic value the built-in rule "copy the value to the
// result", whatever the mode's on-no-match action says: those actions are all
// phrased about nodes, and none of them has a meaning for a value with no
// children, no name and no ancestry.
func applyToAtomic(rt *runtime, item xdm.Item, mode string,
	params, tunnels map[string]xdm.Sequence, out *outputBuilder) error {

	t, next := rt.sheet.findAtomicTemplateFrom(item, mode, rt.ctx, 0)
	if t == nil {
		if a, ok := item.(*xdm.Atomic); ok {
			out.appendValue(a)
		}
		return nil
	}
	// The resume index is the position after the winner, so xsl:next-match
	// from such a rule continues down the list exactly as it does for a node.
	sub := rt.withSelection(t, next, mode, params, tunnels)
	return runTemplate(sub, t, params, tunnels, out)
}

// findAtomicTemplateFrom picks the highest-priority rule in mode whose
// pattern matches an atomic value, resuming the scan at index start.
//
// The scan is separate from findTemplateFrom because that one is built around
// a node — it consults the node's kind and name to narrow the candidates — and
// an atomic value answers none of those questions. The templates are already
// sorted by precedence and priority, so the first match wins.
// It returns the match and the index after it, which is what xsl:next-match
// needs to resume the scan over the rules the winner beat.
func (s *Stylesheet) findAtomicTemplateFrom(item xdm.Item, mode string,
	ctx *xpath.Context, start int) (*Template, int) {

	for i := start; i < len(s.templates); i++ {
		t := s.templates[i]
		if t.Match == nil || !t.matchesMode(mode) {
			continue
		}
		ok, err := t.Match.matchesAtomicItem(item, ctx)
		if err != nil || !ok {
			continue
		}
		return t, i + 1
	}
	return nil, len(s.templates)
}

// expandGroupedSteps rewrites a parenthesised alternation used as a step into
// the union of the paths it stands for: "x/(a|b)/text()" becomes
// "x/a/text() | x/b/text()".
//
// The rewrite is exact — "/" distributes over "|" — and it is worth making
// because the result is an ordinary step pattern. That matters for more than
// speed: a step pattern is matched by walking up from the candidate, which
// needs no anchor, and a pattern whose first step names the root of a
// parentless temporary tree has no anchor to find. It also restores the
// per-alternative default priorities, which the equivalent-expression form
// flattens to 0.5.
//
// It returns nil when src holds no such group, or when a group holds anything
// but a top-level alternation — "(a except b)" is not a union and does not
// distribute.
func expandGroupedSteps(src string) []string {
	open := -1
	depth := 0
	quote := byte(0)
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			if depth == 0 {
				open = i
			}
			depth++
		case ')':
			depth--
			if depth != 0 || open < 0 {
				continue
			}
			// A group qualifies only where a step may stand: at the very
			// front, or straight after a "/".
			if open > 0 && src[open-1] != '/' {
				open = -1
				continue
			}
			inner := src[open+1 : i]
			parts := splitTopLevel(inner, '|')
			if len(parts) < 2 {
				open = -1
				continue
			}
			before, after := src[:open], src[i+1:]
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					return nil
				}
				out = append(out, before+p+after)
			}
			return out
		}
	}
	return nil
}

// matchesAtomicValues reports whether the pattern has an alternative that can
// match something other than a node.
//
// Only the ".[E]" form can. It is asked wherever a construct would otherwise
// refuse an atomic value out of hand — positional grouping is the case — so
// that a 3.0 stylesheet grouping a sequence of integers is not turned away.
func (p *Pattern) matchesAtomicValues() bool {
	for _, g := range p.general {
		if g.selfOnly {
			return true
		}
	}
	return false
}

// patternMatchesItem asks a pattern about an item of any kind.
//
// A node goes through the ordinary match; anything else can only be answered
// by the ".[E]" alternatives, since every other pattern form selects nodes.
// It exists so that the constructs which ask a pattern about each member of a
// sequence — positional grouping above all — do not have to know which forms
// can match what.
func patternMatchesItem(pat *Pattern, it xdm.Item, ctx *xpath.Context) (bool, error) {
	if n, ok := it.(*xdm.Node); ok {
		return pat.Matches(n, ctx)
	}
	return pat.matchesAtomicItem(it, ctx)
}

// nextMatchAtomic is xsl:next-match where the item being processed is an
// atomic value rather than a node.
//
// It resumes the atomic scan after the rule that won, and falls to section
// 6.7.1's built-in rule — copy the value to the result — when nothing further
// matches, which is what applyToAtomic does for the first dispatch.
func (i *nextMatchInstr) nextMatchAtomic(rt *runtime, out *outputBuilder,
	name string) error {

	item := rt.ctx.Item
	params, tunnels, err := evalParams(rt, i.params)
	if err != nil {
		return err
	}
	if tunnels == nil {
		tunnels = rt.sel.tunnels
	}
	t, next := rt.sheet.findAtomicTemplateFrom(
		item, rt.sel.mode, rt.ctx, rt.sel.next)
	if t == nil {
		if a, ok := item.(*xdm.Atomic); ok {
			out.appendValue(a)
		}
		return nil
	}
	if err := rt.descend(); err != nil {
		return err
	}
	defer rt.ascend()
	sub := rt.withSelection(t, next, rt.sel.mode, params, tunnels)
	return runTemplate(sub, t, params, tunnels, out)
}

// splitPatternAlts splits a pattern into its top-level alternatives.
//
// XSLT 3.0 spells the union of a pattern's alternatives two ways: "|" and the
// "union" keyword, which the XPath grammar has always treated as synonyms.
// splitTopLevel only knows single-byte separators, and a pattern is the one
// place the word form appears, so the keyword is recognised here rather than
// by widening a helper a dozen other callers share.
func splitPatternAlts(s string) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[' || c == '(':
			depth++
		case c == ']' || c == ')':
			depth--
		case c == '|' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		case depth == 0 && isUnionKeywordAt(s, i):
			out = append(out, s[start:i])
			start = i + len("union")
			i = start - 1
		}
	}
	return append(out, s[start:])
}

// isUnionKeywordAt reports whether the word "union" starts at i and stands
// alone there.
//
// It has to be a whole word: "union" is also a perfectly good element name, so
// the pattern "union" is one name test and "a union b" is two joined by the
// operator. Requiring a non-name character on each side separates them, which
// is the same rule the XPath lexer applies.
func isUnionKeywordAt(s string, i int) bool {
	if !strings.HasPrefix(s[i:], "union") {
		return false
	}
	if i > 0 && isNameByte(s[i-1]) {
		return false
	}
	j := i + len("union")
	if j >= len(s) || isNameByte(s[j]) {
		return false
	}
	// An operator needs an operand before it; at the front of the pattern
	// "union" can only be a name.
	return strings.TrimSpace(s[:i]) != ""
}

// isNameByte reports whether c can appear inside an unprefixed XML name. It is
// deliberately ASCII-only: it exists to find a word boundary around an ASCII
// keyword, and every byte of a multi-byte UTF-8 rune has the high bit set, so
// such a rune is treated as a name character and correctly blocks the match.
func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' ||
		c == ':' || c >= 0x80
}

// canMatchNamespaceNode reports whether any alternative of the pattern could
// select a namespace node.
//
// It exists so that the xsl:key index walk only synthesizes the namespace axis
// where a key actually asks for it: the axis is built from the accumulated
// in-scope set rather than stored, so offering it for every element of every
// document would cost every stylesheet for the sake of the few that key on it.
//
// The question is asked of the last step, which is the one that tests the
// candidate itself. A general pattern is answered conservatively — its whole
// point is that the step walk cannot express it.
func (p *Pattern) canMatchNamespaceNode() bool {
	if len(p.general) > 0 {
		return true
	}
	for _, a := range p.alts {
		if a.call != nil {
			// An id() or key() pattern selects by membership, so the step
			// walk cannot answer; neither selects namespace nodes in
			// practice, but saying so here would be a guess.
			return true
		}
		if len(a.steps) == 0 {
			continue
		}
		last := a.steps[len(a.steps)-1]
		if last.namespace {
			return true
		}
		if kt, ok := last.nodeTest.(*xpath.KindTest); ok &&
			(kt.Any || kt.Kind == xdm.KindNamespace) {
			return true
		}
	}
	return false
}
