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
	// matches a b whose parent is an a, wherever that a is. Evaluating from
	// each ancestor in turn — and from the root, which an absolute form needs
	// — finds every anchoring the pattern admits.
	for anc := node; anc != nil; anc = anc.Parent {
		seq, err := g.expr.Eval(ctx.WithFocus(anc, 1, 1))
		if err != nil {
			// A pattern evaluated against a node it was not written for is a
			// non-match rather than a failure; 5.5.4 says so, and Matches
			// applies the same rule to the step forms.
			if recoverPatternError(err) {
				continue
			}
			return false, err
		}
		if containsNode(seq, node) {
			return true, nil
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
