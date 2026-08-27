// Package xslt implements XSLT 2.0 transformation over the xdm data model,
// using the xpath package for expression evaluation.
package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// Pattern is a compiled xsl:template match pattern.
//
// Patterns look like path expressions but mean something different: a path
// says "navigate from here", a pattern says "does this node match". The
// natural implementation of matching — evaluate the path and check membership
// — is quadratic, because it would visit every node in the document for every
// node being matched.
//
// Instead a pattern is matched right-to-left from the candidate node: check
// the last step's node test against the node itself, then walk *up* verifying
// each preceding step. That makes a match cost O(depth) rather than O(document
// size), which is the difference between a transform that finishes and one
// that does not on a large invoice.
type Pattern struct {
	src  string
	alts []*patternAlt
	// general holds the alternatives written in one of the XSLT 3.0 forms
	// the step walk cannot express, matched by the equivalent-expression
	// rule instead. See pattern30.go.
	general []*generalPattern
	prio    float64
}

// patternAlt is one alternative of a "|"-separated pattern.
type patternAlt struct {
	// steps are the pattern's steps in source order; matching walks them
	// backwards.
	steps []patternStep
	// absolute marks a pattern rooted at the document node ("/foo").
	absolute bool
	// call is an id() or key() pattern, which is matched by evaluating the
	// call and testing membership rather than by walking steps. It is the one
	// pattern form the right-to-left walk cannot express, because the set it
	// selects is not derived from the candidate node's ancestry.
	call *xpath.FuncCall
	// callSteps are the steps that follow the call, as in "key('k','v')/para".
	callSteps []patternStep
	// callDescendant records that the step immediately after the call used
	// the descendant axis, so that key('k','v')//para admits any ancestor
	// rather than only the parent.
	callDescendant bool
	// pendingCallDescendant carries the "//" between the call and the next
	// named step, which the parser emits as its own descendant-or-self step,
	// onto that step while the alternative is being built.
	pendingCallDescendant bool
	// predicate expressions are attached to the step they qualify.
}

type patternStep struct {
	test xdm.NodeKind
	// nodeTest is the compiled name or kind test for this step.
	nodeTest xpath.NodeTest
	// axis is the axis this step traverses when walking up: child steps are
	// verified against the parent, descendant steps against any ancestor.
	descendant bool
	// attribute marks a step matching on the attribute axis.
	attribute bool
	// namespace marks a step matching on the namespace axis, which XSLT 3.0
	// added to ForwardAxisP. Like the attribute axis it holds nodes that are
	// not children of their parent, so it is kept apart from attribute rather
	// than folded into it: the two select from different collections.
	namespace bool
	// preds are predicates on this step, evaluated with the candidate node as
	// context.
	preds []xpath.Expr
	// explicitAxis records that the axis was spelled out. The child-or-top
	// widening of section 5.5.3 applies only to the abbreviated form.
	explicitAxis bool
	// orSelf marks the descendant-or-self axis, whose candidate set includes
	// the anchor itself. It only matters where the set is materialised, which
	// is when a positional predicate has to number it.
	orSelf bool
	// self marks a self:: step, which the XSLT 3.0 grammar admits. It
	// constrains the candidate rather than its parent, so the walk does not
	// step up before testing it. See pattern30.go.
	self bool
}

// CompilePattern compiles an XSLT match pattern.
func CompilePattern(src string, ns xpath.NamespaceResolver) (*Pattern, error) {
	// XTSE1060 and XTSE1070: the grouping functions may not be used in a
	// pattern. A pattern is matched against a node with no grouping in
	// progress, so there is no current group for them to return — the call
	// would be evaluated in a context that cannot supply an answer.
	if err := checkNoGroupingFuncs(src); err != nil {
		return nil, err
	}
	p := &Pattern{src: src}
	// The "union" keyword joins pattern alternatives exactly as "|" does, and
	// is spelled out only in a 3.0 pattern. See splitPatternAlts.
	alts := splitTopLevel(src, '|')
	// The "union" keyword as a pattern operator is 3.0 pattern syntax, which
	// a stylesheet CONSTRUCTS, so it follows the module's version. match-057
	// writes it in a version="3.0" module; match-038 is a version="2.0"
	// module scoped XSLT10+ whose "/ union /*" must NOT parse as a union, so
	// that the rule never matches and the plain "/" rule wins.
	if patternsAllow30(ns) {
		alts = splitPatternAlts(src)
	}
	// Where a PredicatePattern may stand is a grammar rule, not a matching
	// one, so it is settled before any alternative is compiled; see
	// checkPredicatePatternPlacement.
	if patternsAllow30(ns) {
		if err := checkPredicatePatternPlacement(src, alts); err != nil {
			return nil, err
		}
	}
	for _, alt := range alts {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue
		}
		// A parenthesised alternation used as a step distributes over "/",
		// and the union it expands to is an ordinary step pattern — which
		// matches by walking up from the candidate and so needs no anchor.
		// See expandGroupedSteps.
		// The gate is the *processor's* version, not the module's: match-081
		// and match-081a run the one version="2.0" stylesheet at the two
		// targets and demand XTSE0340 from the first and a working pattern
		// from the second, which only the cap can distinguish.
		if parts := expandGroupedSteps(alt); parts != nil && processorAtLeast30() {
			ok := true
			var built []*patternAlt
			for _, part := range parts {
				sub, err := compilePatternAlt(part, ns)
				if err != nil {
					ok = false
					break
				}
				built = append(built, sub)
			}
			if ok {
				p.alts = append(p.alts, built...)
				continue
			}
		}
		a, err := compilePatternAlt(alt, ns)
		if err != nil {
			// XSLT 3.0 widened the grammar past what the step walk can
			// express. Before reporting the error, see whether this is one
			// of the forms the equivalent-expression rule covers; see
			// pattern30.go.
			g, gerr := compileGeneralPattern(alt, ns)
			if gerr == nil && g != nil &&
				(patternsAllow30(ns) || variablePatternAllowed(alt)) {
				p.general = append(p.general, g)
				continue
			}
			// Every way a pattern can fail to compile is the same static
			// error: the attribute does not match the Pattern production.
			// The code is attached here, once, rather than at each of the
			// dozen places that can detect it, so that a new check cannot
			// forget it.
			return nil, fmt.Errorf("XTSE0340: pattern %q: %w", src, err)
		}
		p.alts = append(p.alts, a)
	}
	if len(p.alts) == 0 && len(p.general) == 0 {
		return nil, fmt.Errorf("XTSE0340: pattern %q is empty", src)
	}
	p.prio = p.computePriority()
	return p, nil
}

// splitTopLevel splits on sep, ignoring occurrences inside brackets, parens or
// string literals. A naive strings.Split would break "a[b|c]".
func splitTopLevel(s string, sep byte) []string {
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
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// compilePatternAlt compiles one alternative by parsing it as a path
// expression and reinterpreting the result.
//
// Reusing the XPath parser rather than writing a second one keeps the two in
// agreement about name tests, kind tests and predicates. The grammar of
// patterns is a subset of path expressions, so anything the pattern grammar
// allows parses here; the reinterpretation below rejects the constructs that
// parse but are not legal patterns.
func compilePatternAlt(src string, ns xpath.NamespaceResolver) (*patternAlt, error) {
	allow30 := patternsAllow30(ns)
	// The version decides what the grammar admits, here as everywhere else:
	// a predicate may contain any expression, and "instance of map(*)" names
	// an item type that exists only in 3.1. Parsing every pattern as 2.0
	// rejected such a predicate before the pattern machinery saw it.
	v := xpath.XPath20
	if r, ok := ns.(*nsResolver); ok {
		v = r.xpathVersion
	}
	// A predicate holds an arbitrary XPath expression, and which expressions
	// exist is the PROCESSOR's question rather than the module's -- the same
	// reasoning that governs which functions are in the library. number-1904
	// writes "foo[let $n := number(@bar) return . = $n*$n]" in a version="2.0"
	// module scoped XSLT30+. The pattern GRAMMAR is a separate matter and
	// stays with the module: patternsAllow30 still refuses "/(a|b)" there,
	// which is what version-023 requires.
	if processorAtLeast30() && !v.AtLeast31() {
		v = xpath.XPath31
	}
	expr, err := xpath.ParseVersion(src, ns, v)
	if err != nil {
		return nil, err
	}

	a := &patternAlt{}
	switch e := expr.(type) {
	case *xpath.PathExpr:
		a.absolute = e.Root
		if len(e.Steps) == 0 {
			// The pattern "/" matches the document node alone.
			a.steps = []patternStep{{
				nodeTest:   &xpath.KindTest{Kind: xdm.KindDocument},
				descendant: false,
			}}
			return a, nil
		}
		rest := e.Steps
		// A path may begin with id() or key(), as in "key('k','v')//para".
		// The call selects the starting set and the steps that follow are
		// walked up from the candidate; only the *first* step may be a call.
		if call, ok := rest[0].(*xpath.FuncCall); ok {
			// The RelativePathPattern production puts an IdKeyPattern only
			// at the very front: "/key(...)" is not a pattern, because the
			// call already names the nodes it starts from and rooting it
			// would say nothing.
			if a.absolute {
				return nil, fmt.Errorf(
					"not a valid pattern step: %s may not follow \"/\"",
					call.String())
			}
			if err := checkPatternCall(call, allow30); err != nil {
				return nil, err
			}
			a.call = call
			rest = rest[1:]
		}
		for _, s := range rest {
			step, ok := s.(*xpath.Step)
			if !ok {
				return nil, fmt.Errorf("not a valid pattern step: %s", s.String())
			}
			ps, err := convertStep(step, allow30)
			if err != nil {
				return nil, err
			}
			if a.call != nil {
				// "//" parses as an explicit descendant-or-self::node()
				// step between the two named ones, so "key('k','v')//para"
				// arrives here as two steps rather than one. That synthetic
				// step is not a step to match against an ancestor — it is
				// the *axis* joining the call to what follows — so it is
				// folded into the descendant flag of the step after it.
				// Leaving it in place made the pattern demand one more
				// ancestor level than the path actually names, and no node
				// ever matched.
				if isDescendantOrSelfNode(step) {
					a.pendingCallDescendant = true
					continue
				}
				if a.pendingCallDescendant {
					ps.descendant = true
					a.pendingCallDescendant = false
				}
				if len(a.callSteps) == 0 {
					a.callDescendant = ps.descendant
				}
				a.callSteps = append(a.callSteps, ps)
				continue
			}
			a.steps = append(a.steps, ps)
		}

	case *xpath.Step:
		ps, err := convertStep(e, allow30)
		if err != nil {
			return nil, err
		}
		a.steps = []patternStep{ps}

	case *xpath.FuncCall:
		// id() and key() are legal pattern starts; they are matched by
		// evaluating the function and testing membership, which is the one
		// case where the right-to-left trick does not apply.
		if err := checkPatternCall(e, allow30); err != nil {
			return nil, err
		}
		a.call = e

	default:
		return nil, fmt.Errorf("not a valid pattern: %s", expr.String())
	}
	return a, nil
}

// convertStep reinterprets a path step as a pattern step.
// checkPITarget rejects a processing-instruction() test whose target is not an
// NCName.
//
// XPath 2.0's PITest production is
//
//	"processing-instruction" "(" (NCName | StringLiteral)? ")"
//
// and Namespaces in XML section 6 says a PI target is an NCName, never a
// QName: a colon in a PI target is not merely unmatched, it is not a name at
// all. The XPath parser stores whatever token it read, so a target written
// with a colon parses and then silently matches nothing. In a pattern that is
// XTSE0340 — CompilePattern attaches the code to every error from here — and
// error-0340c is exactly that shape.
//
// Only the colon is rejected, not every non-NCName character: the target may
// also be written as a StringLiteral, and the AST does not record which form
// was used. A colon is invalid in both forms, so this stays inside what both
// productions already forbid.
func checkPITarget(t xpath.NodeTest) error {
	kt, ok := t.(*xpath.KindTest)
	if !ok || kt.Kind != xdm.KindPI || !kt.HasName || kt.Name == nil {
		return nil
	}
	if strings.Contains(kt.Name.Local, ":") || kt.Name.URI != "" {
		return fmt.Errorf(
			"processing-instruction(%s): a PI target is an NCName and may not contain a colon",
			kt.Name.Local)
	}
	return nil
}

func convertStep(s *xpath.Step, allow30 bool) (patternStep, error) {
	ps := patternStep{nodeTest: s.Test, preds: s.Predicates, explicitAxis: s.Explicit}
	if err := checkPITarget(s.Test); err != nil {
		return ps, err
	}
	switch s.Axis {
	case xpath.AxisChild:
	case xpath.AxisAttribute:
		ps.attribute = true
	case xpath.AxisDescendantOrSelf:
		// This is what "//" expands to.
		ps.descendant = true
		ps.orSelf = true
	case xpath.AxisDescendant:
		ps.descendant = true
	case xpath.AxisNamespace:
		// XSLT 3.0 added namespace:: to ForwardAxisP. A 2.0 module must still
		// refuse it, as every conforming 2.0 processor does.
		if !allow30 {
			return ps, fmt.Errorf("axis %q is not allowed in a pattern", s.String())
		}
		ps.namespace = true
	case xpath.AxisSelf:
		// XSLT 3.0 admits self:: in a pattern, where it constrains the
		// candidate itself. The walk therefore does not step up for it.
		if !allow30 {
			return ps, fmt.Errorf("axis %q is not allowed in a pattern", s.String())
		}
		ps.self = true
	default:
		return ps, fmt.Errorf("axis %q is not allowed in a pattern", s.String())
	}
	return ps, nil
}

// Matches reports whether node matches the pattern.
//
// ctx supplies the focus for predicate evaluation. Predicates in a pattern are
// evaluated with the candidate node as the context item, and with a context
// position derived from its position among its like-named siblings — which is
// why "para[1]" as a pattern means "a para that is the first para child of its
// parent" rather than "the first para in the document".
func (p *Pattern) Matches(node *xdm.Node, ctx *xpath.Context) (bool, error) {
	// Section 16.6.1 fixes what current() means inside a pattern: "its value
	// is the node that is being matched against the pattern" — not whatever
	// the enclosing instruction was processing. The distinction shows up in
	// xsl:number/@count, where a predicate such as "[@bar = current()/@bar]"
	// compares each candidate against *itself* and so always holds; binding
	// the outer node instead made the predicate select only the candidates
	// sharing the numbered node's value.
	ctx = ctx.WithVar(currentVar, xdm.One(node))
	// Section 24.3: the current output URI is cleared while evaluating a
	// pattern. A pattern is matched against candidate nodes at moments that
	// have nothing to do with which result tree is being written, and
	// current-output-uri-008 asserts the absence directly.
	ctx = ctx.WithVar(outputURIVar, xdm.Empty())
	for _, g := range p.general {
		ok, err := g.matches(node, ctx)
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
	for _, alt := range p.alts {
		ok, err := alt.matches(node, ctx)
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

// recoverPatternError decides whether a failure while matching one alternative
// is recovered by treating that alternative as a non-match.
//
// XSLT 2.0 5.5.4: "Any dynamic error or type error that occurs during the
// evaluation of a pattern against a particular node is treated as a
// recoverable error even if the error would not be recoverable under other
// circumstances. The optional recovery action is to treat the pattern as not
// matching that node." The Rec's own note gives the reason: a stylesheet
// author cannot predict which patterns are evaluated against which node, so a
// predicate that is nonsensical for a node it was never written for must not
// fail the transformation. import-1201 is exactly that: the union stylesheet
// has match="*[.=117]", and conflict resolution tests it against the document
// element, whose untyped string value is every number in the file
// concatenated. Casting that to xs:double is FORG0001, and propagating it
// killed a transform whose expected output does not mention the error at all.
//
// Recovery is deliberately NOT applied to the XSLT-level errors a pattern can
// raise about the stylesheet rather than about this node — XTDE0640's key
// recursion is a property of the stylesheet and reporting it is the point.
func recoverPatternError(err error) bool {
	msg := err.Error()
	for _, code := range nonRecoverablePatternCodes {
		if strings.HasPrefix(msg, code) || strings.Contains(msg, ": "+code) {
			return false
		}
	}
	return true
}

// nonRecoverablePatternCodes are the errors a pattern may report that are
// about the stylesheet, not about the node being matched, and so are outside
// what 5.5.4 makes recoverable.
var nonRecoverablePatternCodes = []string{
	"XTDE0640", // circular xsl:key definition
	"XTDE1270", // key() called with an unknown key name
	"XPDY0001", // recursion guard: masking it would hide a runaway
}

func (a *patternAlt) matches(node *xdm.Node, ctx *xpath.Context) (bool, error) {
	if a.call != nil {
		return a.matchesCall(node, ctx)
	}
	if len(a.steps) == 0 {
		return false, nil
	}
	last := a.steps[len(a.steps)-1]
	rest := a.steps[:len(a.steps)-1]
	// A positional predicate on a descendant step counts within the whole
	// descendant set of the anchor, not among the candidate's siblings:
	// "chapter/descendant::foo[1]" is the first foo anywhere under a chapter.
	// The anchor is not known until the steps before it have been satisfied,
	// so the two are resolved together. See matchDescendantPositional.
	if last.descendant && len(rest) > 0 && stepIsPositional(last) {
		return a.matchDescendantPositional(last, rest, node, ctx)
	}
	// The last step must match the candidate node itself.
	ok, err := matchStep(last, node, ctx)
	if err != nil || !ok {
		return false, err
	}
	// An explicit descendant axis on the LAST step separates it from what
	// precedes it by any number of levels: "x/descendant::b" is a b at any
	// depth below an x, not a b whose parent is an x. Written as "x//b" the
	// same path carries the flag on the synthetic descendant-or-self::node()
	// step that "//" expands to, which is never last, so matchAncestors saw
	// it and scanned; written with the axis spelled out there is no such
	// step, and the walk demanded a direct parent instead. match-266 and
	// match-282 are that pattern, reached through the group expansion of
	// "x/(child::a|descendant::b)".
	if last.descendant && len(rest) > 0 {
		// The anchor is the node the step before the descendant one names,
		// so it is tested against that step directly and the rest verified
		// from it. Handing the anchor to matchAncestors instead would test
		// that step against the anchor's PARENT, which starts the scan a
		// level too high: "doc/descendant::foo" then missed the foo that is
		// doc's own child, since the nearest anchor tried was doc's parent.
		inner := rest[len(rest)-1]
		before := rest[:len(rest)-1]
		for anc := node.Parent; anc != nil; anc = anc.Parent {
			ok, err := matchStep(inner, anc, ctx)
			if err != nil {
				return false, err
			}
			if !ok {
				continue
			}
			ok, err = a.matchAncestors(before, anc, ctx)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	return a.matchAncestors(rest, node, ctx)
}

// matchAncestors verifies the remaining steps against node's ancestors,
// walking up.
func (a *patternAlt) matchAncestors(steps []patternStep, node *xdm.Node, ctx *xpath.Context) (bool, error) {
	if len(steps) == 0 {
		// Every step is satisfied. An absolute pattern additionally requires
		// that we have reached the document node.
		if a.absolute {
			return node.Parent != nil && node.Parent.Kind == xdm.KindDocument ||
				node.Kind == xdm.KindDocument, nil
		}
		return true, nil
	}

	step := steps[len(steps)-1]
	rest := steps[:len(steps)-1]

	// A self:: step names the same node the step to its right does, so it
	// does not consume a level of the walk. In "self::foo/bar" the foo IS the
	// bar's parent, so the step is verified against that parent and the walk
	// carries on from there rather than stepping up again — which is what
	// lets several self:: steps in a row all constrain the one node, as
	// match-258's "self::foo/self::*[@att1]/baz" requires.
	//
	// Testing it against the node the walk has reached instead put it one
	// level too low: the first such step then described the candidate rather
	// than its parent, and a second found nothing left to agree with.
	if step.self {
		parent := node.Parent
		if parent == nil {
			return false, nil
		}
		ok, err := matchStep(step, parent, ctx)
		if err != nil || !ok {
			return false, err
		}
		// The level is not consumed, so the remaining steps are matched from
		// the same node — the next one up will test this same parent.
		return a.matchAncestors(rest, node, ctx)
	}

	if step.descendant {
		// A "//" step matches at any depth, so try every ancestor. The step
		// itself is the node() test that "//" expands to; what has to match
		// is the *remaining* steps ending at some ancestor.
		//
		// The last remaining step is tested against the ancestor itself
		// rather than against the ancestor's parent: in "B//X" the X may be a
		// direct child of the B, and matching B one level higher than the
		// node "//" landed on skipped exactly that case.
		if len(rest) == 0 {
			// Nothing further to satisfy, but "//X" still expands to
			// root(.)/descendant-or-self::node()/child::X, so the node has to
			// be somebody's child. A parentless element built by
			// xsl:variable/@as is its own root and has no parent, so it does
			// not match — which is what match-178 and match-183 check, and
			// what the built-in rule they expect to fire depends on.
			return node.Parent != nil, nil
		}
		inner := rest[len(rest)-1]
		before := rest[:len(rest)-1]
		for anc := node.Parent; anc != nil; anc = anc.Parent {
			ok, err := matchStep(inner, anc, ctx)
			if err != nil {
				return false, err
			}
			if !ok {
				continue
			}
			ok, err = a.matchAncestors(before, anc, ctx)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}

	parent := node.Parent
	if parent == nil {
		return false, nil
	}
	ok, err := matchStep(step, parent, ctx)
	if err != nil || !ok {
		return false, err
	}
	return a.matchAncestors(rest, parent, ctx)
}

// matchStep tests one step against one node, including its predicates.
func matchStep(s patternStep, node *xdm.Node, ctx *xpath.Context) (bool, error) {
	principal := xdm.KindElement
	if s.attribute {
		principal = xdm.KindAttribute
	}
	if s.namespace {
		principal = xdm.KindNamespace
	}
	// The namespace axis contains only namespace nodes, and no other axis
	// contains any, so the two disagreeing is an immediate non-match.
	if s.namespace != (node.Kind == xdm.KindNamespace) {
		return false, nil
	}
	if node.Kind == xdm.KindAttribute && !s.attribute {
		// A child-axis step never matches an attribute.
		return false, nil
	}
	if s.attribute && node.Kind != xdm.KindAttribute {
		// The attribute axis contains only attributes, so a step on it never
		// matches anything else — whatever its node test says.
		return false, nil
	}
	if s.attribute {
		// A kind test naming another kind is unsatisfiable on this axis:
		// "attribute::element()" selects elements from among the attributes,
		// of which there are none. Left to the node test alone it matched
		// every attribute, because a KindTest for a named kind is checked
		// against the axis principal kind rather than against the node.
		//
		// That is not merely a pattern that fires too often. A stylesheet
		// declaring match="attribute::element()" alongside ordinary rules had
		// its elements captured by the attribute rule, so they never reached
		// the rule that should have handled them.
		if kt, ok := s.nodeTest.(*xpath.KindTest); ok && !kt.Any &&
			kt.Kind != xdm.KindAttribute {
			return false, nil
		}
	}
	// Section 5.5.3: a pattern step on the child axis is evaluated on the
	// child-or-top axis, and "for backwards compatibility reasons, the
	// pattern node(), when used without an explicit axis, does not match
	// document nodes, attribute nodes, or namespace nodes".
	//
	// Without this the pattern node() matched the document node, and since a
	// document node is where template selection starts, a stylesheet
	// declaring both match="doc" and match="node()" ran the second on the
	// root and never reached the first.
	if node.Kind == xdm.KindDocument && !s.attribute {
		// Only the unrestricted node() test is excluded. document-node() and
		// "/" name the document node explicitly and must still match it;
		// they differ from node() by not being the "any kind" test.
		if kt, ok := s.nodeTest.(*xpath.KindTest); ok && kt.Any {
			return false, nil
		}
		// The child-or-top widening is a property of the abbreviated syntax
		// only. A written-out "child::document-node()" is legal but can never
		// match, because no document node is the child of anything.
		if s.explicitAxis && !s.descendant {
			return false, nil
		}
	}
	if !s.nodeTest.Matches(node, principal) {
		return false, nil
	}
	if !schemaDeclaredMatches(s.nodeTest, node) {
		return false, nil
	}

	return matchPredicates(s, node, ctx)
}

// matchPredicates applies a step's predicates to a candidate node.
//
// A pattern is defined by the path it is equivalent to, and in a path each
// predicate filters the sequence the previous one produced and *renumbers* it.
// So in "x[position() mod 2 = 1][position() > 3][position() = 2]" the second
// predicate counts positions among the survivors of the first, not among the
// original children.
//
// Evaluating each predicate against the original sibling set independently is
// what this replaced. That is right whenever at most one predicate is
// positional, which is the overwhelmingly common case and why it went
// unnoticed, but it makes every predicate after a positional one count the
// wrong sequence.
//
// The survivor set is only materialised when there is more than one predicate
// and at least one of them can observe the position; a single predicate, or
// predicates that cannot see position, take the cheap path that tests the
// candidate alone.
func matchPredicates(s patternStep, node *xdm.Node, ctx *xpath.Context) (bool, error) {
	positional := false
	for _, pred := range s.preds {
		if needsPosition(pred) {
			positional = true
			break
		}
	}
	if len(s.preds) < 2 || !positional || node.Parent == nil {
		for _, pred := range s.preds {
			ok, err := evalPatternPredicate(pred, s, node, ctx)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}

	principal := xdm.KindElement
	siblings := node.Parent.Children
	if s.attribute {
		principal = xdm.KindAttribute
		siblings = node.Parent.Attrs
	}
	cand := make([]*xdm.Node, 0, len(siblings))
	for _, sib := range siblings {
		if s.nodeTest.Matches(sib, principal) {
			cand = append(cand, sib)
		}
	}

	for _, pred := range s.preds {
		kept := cand[:0:0]
		for i, c := range cand {
			sub := ctx.WithFocus(c, i+1, len(cand))
			v, err := pred.Eval(sub)
			if err != nil {
				return false, err
			}
			ok := false
			if len(v) == 1 {
				if a, isAtomic := v[0].(*xdm.Atomic); isAtomic && a.Type.IsNumeric() {
					ok = !a.IsNaN() && a.Float64() == float64(i+1)
				} else {
					ok, err = xpath.EffectiveBooleanValue(v)
				}
			} else {
				ok, err = xpath.EffectiveBooleanValue(v)
			}
			if err != nil {
				return false, err
			}
			if ok {
				kept = append(kept, c)
			}
		}
		cand = kept
		if len(cand) == 0 {
			return false, nil
		}
	}
	for _, c := range cand {
		if c == node {
			return true, nil
		}
	}
	return false, nil
}

// evalPatternPredicate evaluates a pattern predicate against a candidate node.
//
// The context position is the node's position among the siblings that the same
// step would have selected, so "item[2]" matches the second item child of its
// parent. Computing that requires scanning the siblings, which is why
// positional predicates in patterns are more expensive than name tests and
// worth avoiding in hot rule sets.
func evalPatternPredicate(pred xpath.Expr, s patternStep, node *xdm.Node, ctx *xpath.Context) (bool, error) {
	pos, size := 1, 1
	if needsPosition(pred) && node.Parent != nil {
		principal := xdm.KindElement
		if s.attribute {
			principal = xdm.KindAttribute
		}
		siblings := node.Parent.Children
		if s.attribute {
			siblings = node.Parent.Attrs
		}
		size = 0
		for _, sib := range siblings {
			if s.nodeTest.Matches(sib, principal) {
				size++
				if sib == node {
					pos = size
				}
			}
		}
	}

	sub := ctx.WithFocus(node, pos, size)
	v, err := pred.Eval(sub)
	if err != nil {
		return false, err
	}
	// A numeric predicate selects by position, exactly as in a path.
	if len(v) == 1 {
		if a, isAtomic := v[0].(*xdm.Atomic); isAtomic && a.Type.IsNumeric() {
			return !a.IsNaN() && a.Float64() == float64(pos), nil
		}
	}
	return xpath.EffectiveBooleanValue(v)
}

// needsPosition reports whether an expression could observe the context
// position, so that the sibling scan is skipped when it cannot.
//
// It is deliberately conservative: a false positive costs a scan, a false
// negative would produce a wrong match.
func needsPosition(e xpath.Expr) bool {
	switch v := e.(type) {
	case *xpath.Literal:
		return v.Val.Type.IsNumeric()
	case *xpath.VarRef:
		// A variable can hold a number, and a numeric predicate is
		// positional. Nothing here knows what the variable holds, so the
		// conservative answer is the only sound one: judging chapter[$v3]
		// non-positional pinned the context position at 1 and made every
		// chapter match.
		return true
	case *xpath.FuncCall:
		if v.Name.URI == xdm.NSFN && (v.Name.Local == "position" || v.Name.Local == "last") {
			return true
		}
		for _, a := range v.Args {
			if needsPosition(a) {
				return true
			}
		}
		return false
	case *xpath.BinaryOp:
		return needsPosition(v.Left) || needsPosition(v.Right)
	case *xpath.UnaryOp:
		return needsPosition(v.Operand)
	case *xpath.IfExpr:
		return needsPosition(v.Cond) || needsPosition(v.Then) || needsPosition(v.Else)
	}
	return false
}

// Priority returns the pattern's default priority, per the XSLT rules:
// a specific name test scores 0, a namespace wildcard -0.25, a full wildcard
// or bare kind test -0.5, and anything more complex 0.5.
//
// These numbers exist so that a more specific template wins over a general
// one without the author having to say so. Getting them wrong makes template
// selection silently pick the wrong rule, which is far harder to debug than a
// crash.
func (p *Pattern) Priority() float64 { return p.prio }

func (p *Pattern) computePriority() float64 {
	best := -1000.0
	for _, alt := range p.alts {
		pr := alt.priority()
		if pr > best {
			best = pr
		}
	}
	for _, g := range p.general {
		if g.prio > best {
			best = g.prio
		}
	}
	return best
}

func (a *patternAlt) priority() float64 {
	// A pattern with more than one step, or with predicates, is "complex" and
	// takes the highest default priority. The one exception is the pattern
	// "/", which the parser records as an absolute pattern with a single
	// document-node step: section 6.4 gives it -0.5 explicitly, and XSLT 2.0
	// changed it from +0.5 for exactly this reason.
	if a.call != nil {
		return 0.5
	}
	if len(a.steps) != 1 || len(a.steps[0].preds) > 0 {
		return 0.5
	}
	if a.absolute {
		if kt, ok := a.steps[0].nodeTest.(*xpath.KindTest); ok &&
			kt.Kind == xdm.KindDocument && !kt.HasName && kt.Content == nil {
			return -0.5 // the pattern "/"
		}
		return 0.5
	}
	return nodeTestPriority(a.steps[0].nodeTest)
}

// nodeTestPriority scores a single node test by the table in section 6.4.
//
// The table is written in terms of how much the test pins down: a name alone
// or a type alone scores 0, both together 0.25, a namespace wildcard -0.25,
// and a bare kind test -0.5. Collapsing every kind test to -0.5, as this used
// to, made element(x) lose to *:x — which is backwards, and is exactly what
// the union tests in the next-match set detect.
func nodeTestPriority(nt xpath.NodeTest) float64 {
	switch t := nt.(type) {
	case *xpath.NameTest:
		switch {
		case t.AnyURI && t.AnyLocal:
			return -0.5 // "*"
		case t.AnyLocal, t.AnyURI:
			return -0.25 // "prefix:*" or "*:local"
		}
		return 0 // a specific name
	case *xpath.KindTest:
		switch t.Kind {
		case xdm.KindElement, xdm.KindAttribute:
			// schema-element(E) and schema-attribute(A) match by declaration,
			// which pins down name and type together.
			if t.SchemaDeclared {
				return 0.25
			}
			named := t.HasName && t.Name != nil
			typed := t.TypeName != ""
			switch {
			case named && typed:
				return 0.25
			case named || typed:
				return 0
			}
			return -0.5 // element() / element(*) / attribute() / attribute(*)
		case xdm.KindDocument:
			// document-node(E) takes the priority of its inner element test;
			// document-node() alone is just a kind test.
			if t.Content != nil {
				return nodeTestPriority(t.Content)
			}
			return -0.5
		case xdm.KindPI:
			// processing-instruction("x") names its target, so it scores like
			// a name test rather than like a bare kind test.
			if t.HasName && t.Name != nil {
				return 0
			}
			return -0.5
		}
		return -0.5 // "node()", "text()", "comment()"
	}
	return 0.5
}

// String returns the pattern source.
func (p *Pattern) String() string { return p.src }

// checkPatternCall reports whether a function call is one the pattern grammar
// allows at the start of a path.
//
// Only fn:id and fn:key may appear there. Any other call parses as a path
// expression but is not a pattern, which is XTSE0340.
func checkPatternCall(e *xpath.FuncCall, allow30 bool) error {
	if e.Name.URI == xdm.NSFN || e.Name.URI == "" {
		// The arities come from the IdKeyPattern production, which spells
		// each call out rather than deferring to the function signature:
		// the two-argument forms search a document the pattern names, and a
		// pattern is matched against a node whose document is already fixed.
		switch e.Name.Local {
		case "id":
			// XSLT 3.0 admits the two-argument form, whose second argument
			// names the tree to search. The candidate node's own tree is
			// what the one-argument form searches, so the two ask different
			// questions and 3.0 wants both available.
			maxArgs := 1
			if allow30 {
				maxArgs = 2
			}
			if len(e.Args) < 1 || len(e.Args) > maxArgs {
				return fmt.Errorf(
					"id() in a pattern takes one argument, not %d", len(e.Args))
			}
			return patternCallArg("id", e.Args[0])
		case "key":
			maxKeyArgs := 2
			if allow30 {
				maxKeyArgs = 3
			}
			if len(e.Args) < 2 || len(e.Args) > maxKeyArgs {
				return fmt.Errorf(
					"key() in a pattern takes two arguments, not %d", len(e.Args))
			}
			if _, ok := e.Args[0].(*xpath.Literal); !ok {
				return fmt.Errorf(
					"the key name in a pattern must be a string literal, not %s",
					e.Args[0].String())
			}
			return patternCallArg("key", e.Args[1])
		}
	}
	return fmt.Errorf("not a valid pattern step: %s", e.String())
}

// patternCallArg checks the value argument of an IdKeyPattern.
//
// The productions IdValue and KeyValue admit only a literal or a variable
// reference. Anything else would have to be evaluated with a focus, and a
// pattern is matched with the candidate node as the focus — so "key('k', .)"
// would silently mean something different from what it reads as.
func patternCallArg(name string, arg xpath.Expr) error {
	switch arg.(type) {
	case *xpath.Literal, *xpath.VarRef:
		return nil
	}
	return fmt.Errorf(
		"the value argument of %s() in a pattern must be a literal or a "+
			"variable reference, not %s", name, arg.String())
}

// matchesCall matches an id() or key() pattern.
//
// Section 5.5.3 defines a match as "evaluate the expression root(.)//(EE) with
// a singleton focus based on N" and test whether the result contains N. For a
// call that is exactly what evaluating the call with N as the context item
// does: both fn:id and fn:key search the whole tree containing the context
// node, so the root(.)// wrapper adds nothing and membership is the answer.
func (a *patternAlt) matchesCall(node *xdm.Node, ctx *xpath.Context) (bool, error) {
	// The candidate is the focus, so that fn:key resolves against the tree
	// the node is in rather than against whatever the caller was looking at.
	sub := *ctx
	sub.Item = node
	sub.Position, sub.Size = 1, 1

	seq, err := a.call.Eval(&sub)
	if err != nil {
		// A pattern that cannot be evaluated does not match. It is not an
		// error in the transform: fn:key against a key that selects nothing
		// is an ordinary empty result, and template selection asks this
		// question of every node.
		//
		// A non-recoverable dynamic error is the exception. Swallowing one
		// turned a circular xsl:key — which section 16.3 makes XTDE0640 —
		// into a silent non-match, so the stylesheet ran to completion and
		// the error the test is about was never reported.
		if isNonRecoverable(err) {
			return false, err
		}
		return false, nil
	}

	// Without following steps, membership of the returned set is the answer.
	if len(a.callSteps) == 0 {
		for _, it := range seq {
			if n, ok := it.(*xdm.Node); ok && n == node {
				return true, nil
			}
		}
		return false, nil
	}

	// With following steps — "key('k','v')/para" — the candidate must match
	// the trailing steps, and the node the walk arrives at must be in the set.
	rest := a.callSteps
	last := rest[len(rest)-1]
	ok, err := matchStep(last, node, ctx)
	if err != nil || !ok {
		return false, err
	}
	// The remaining steps are verified against ancestors, and the node the
	// walk arrives above must be in the set the call selected. A descendant
	// step ("//") admits any ancestor rather than only the parent, which is
	// what makes key('k','v')//para work.
	return a.callAncestors(rest[:len(rest)-1], node, seq, ctx)
}

// callAncestors verifies the steps preceding the candidate and then tests
// whether the call's result contains a node above them.
func (a *patternAlt) callAncestors(steps []patternStep, node *xdm.Node,
	seq xdm.Sequence, ctx *xpath.Context) (bool, error) {

	if len(steps) == 0 {
		// Every step is satisfied; some ancestor must be in the set. The
		// first step of the path was the call, and the axis joining it to
		// what follows is a descendant one in the "//" form, so any
		// ancestor will do.
		for p := node.Parent; p != nil; p = p.Parent {
			for _, it := range seq {
				if n, ok := it.(*xdm.Node); ok && n == p {
					return true, nil
				}
			}
			if !a.callDescendant {
				return false, nil
			}
		}
		return false, nil
	}

	last := steps[len(steps)-1]
	for anc := node.Parent; anc != nil; anc = anc.Parent {
		ok, err := matchStep(last, anc, ctx)
		if err != nil {
			return false, err
		}
		if ok {
			found, err := a.callAncestors(steps[:len(steps)-1], anc, seq, ctx)
			if err != nil || found {
				return found, err
			}
		}
		if !last.descendant {
			return false, nil
		}
	}
	return false, nil
}

// checkNoGroupingFuncs reports XTSE1060 or XTSE1070 for a pattern that calls
// one of the grouping functions.
//
// The test is lexical rather than over the parsed tree because a pattern is
// parsed one alternative at a time and the call may sit inside a predicate;
// looking at the source once is both simpler and catches every position. A
// name appearing inside a string literal is not a call, so quoted regions are
// skipped.
func checkNoGroupingFuncs(src string) error {
	for _, fn := range []struct{ name, code string }{
		{"current-group", "XTSE1060"},
		{"current-grouping-key", "XTSE1070"},
		// 15.6.1 and 15.6.2 give the merging pair codes of their own for the
		// same condition: a pattern is matched with the merge state absent,
		// so a call on either inside one could only ever fail.
		{"current-merge-group", "XTSE3470"},
		{"current-merge-key", "XTSE3500"},
	} {
		if callsFunction(src, fn.name) {
			return fmt.Errorf(
				"%s: %s() cannot be used in a pattern", fn.code, fn.name)
		}
	}
	return nil
}

// patternFuncCall is one function call found by scanning a pattern's source.
type patternFuncCall struct {
	name  string
	arity int
}

// patternFuncCalls returns the function calls written in a pattern.
//
// The scan is over the source text rather than the compiled tree because
// xpath.Expr exposes only Eval and String: there is no way to walk it looking
// for calls, and the parser resolves nothing about a function beyond its name.
//
// Two constructs look like calls and are not, and both are skipped: a node or
// kind test — node(), element(), attribute(), schema-element() and the rest —
// and an axis step, which is spelled "axis::name" and so is preceded by "::".
// Treating either as a call would report XPST0017 for a perfectly ordinary
// pattern, which is much worse than missing a genuine undeclared function.
func patternFuncCalls(src string) []patternFuncCall {
	var out []patternFuncCall
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
		if !isQNameStart(c) {
			continue
		}
		j := i
		for j < len(src) && isQNameChar(src[j]) {
			j++
		}
		name := src[i:j]
		rest := strings.TrimLeft(src[j:], " \t\r\n")
		// Not a call unless "(" follows, and not a call if "::" does: that is
		// an axis, whose name is not a function.
		if !strings.HasPrefix(rest, "(") {
			i = j - 1
			continue
		}
		// A name preceded by "::" is the node test of an axis step, and a
		// name preceded by a QName character is part of a longer name the
		// scan has already passed.
		if i >= 2 && src[i-1] == ':' && src[i-2] == ':' {
			i = j - 1
			continue
		}
		if reservedNodeTest[name] || xpathKeyword[name] {
			i = j - 1
			continue
		}
		// "(:" opens an XPath comment, not an argument list. Without this a
		// pattern that merely has a comment after a name — "letters (:1:)" —
		// reads as a one-argument call to that name.
		if strings.HasPrefix(rest, "(:") {
			i = j - 1
			continue
		}
		args, end, ok := countCallArgs(src, j+strings.Index(src[j:], "("))
		if !ok {
			i = j - 1
			continue
		}
		out = append(out, patternFuncCall{name: name, arity: args})
		i = end
	}
	return out
}

// xpathKeyword holds the XPath 2.0 keywords that may be followed by "(",
// where the parenthesis opens a sub-expression rather than an argument list.
//
// "some $v in .//element() satisfies (string($v) ne ”)" is the case that
// matters: "satisfies (" is not a call to a function named satisfies, and
// reading it as one reported XPST0017 for a valid pattern.
var xpathKeyword = map[string]bool{
	"and": true, "or": true, "not": true, "div": true, "idiv": true,
	"mod": true, "eq": true, "ne": true, "lt": true, "le": true,
	"gt": true, "ge": true, "is": true, "to": true, "in": true,
	"satisfies": true, "return": true, "then": true, "else": true,
	"some": true, "every": true, "for": true, "union": true,
	"intersect": true, "except": true, "instance": true, "of": true,
	"treat": true, "as": true, "castable": true, "cast": true,
}

// reservedNodeTest holds the names that are node or kind tests rather than
// functions. They are spelled exactly like a call and must never be reported
// as an undeclared one.
var reservedNodeTest = map[string]bool{
	"node": true, "text": true, "comment": true,
	"processing-instruction": true, "document-node": true,
	"element": true, "attribute": true, "namespace-node": true,
	"schema-element": true, "schema-attribute": true,
	"item": true, "empty-sequence": true, "if": true,
	// The 3.0/3.1 item types are spelled exactly like a call too. Without
	// them a pattern testing ". instance of map(*)" reported XPST0017 for an
	// undeclared function named "map".
	"map": true, "array": true, "function": true,
}

// countCallArgs counts the arguments of the call whose "(" is at open, and
// returns the index of the matching ")".
//
// Arity is counted by splitting on top-level commas, so nested calls and
// predicates inside an argument do not inflate it. An unbalanced call yields
// ok=false and is left alone: the pattern will fail to compile on its own, and
// reporting an arity derived from a truncated call would be noise.
func countCallArgs(src string, open int) (args, end int, ok bool) {
	depth := 0
	var quote byte
	n := 0
	empty := true
	for i := open; i < len(src); i++ {
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
			empty = false
		case '(', '[':
			depth++
		case ')', ']':
			depth--
			if depth == 0 && c == ')' {
				if !empty {
					n++
				}
				return n, i, true
			}
		case ',':
			if depth == 1 {
				n++
			}
		case ' ', '\t', '\r', '\n':
		default:
			empty = false
		}
	}
	return 0, 0, false
}

// splitPatternQName splits a lexical QName into prefix and local part.
func splitPatternQName(s string) (prefix, local string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

func isQNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isQNameChar(c byte) bool {
	return isQNameStart(c) || c == '-' || c == '.' || c == ':' ||
		(c >= '0' && c <= '9')
}

// callsFunction reports whether src calls name outside a string literal.
func callsFunction(src, name string) bool {
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
		if c != name[0] || !strings.HasPrefix(src[i:], name) {
			continue
		}
		// The name must be whole: "current-group" must not match inside
		// "current-grouping-key", and it must be followed by "(".
		rest := strings.TrimLeft(src[i+len(name):], " \t")
		if !strings.HasPrefix(rest, "(") {
			continue
		}
		if i > 0 {
			prev := src[i-1]
			if prev == '-' || prev == ':' || prev == '_' ||
				(prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') ||
				(prev >= '0' && prev <= '9') {
				continue
			}
		}
		return true
	}
	return false
}

// isDescendantOrSelfNode reports whether a step is the synthetic
// "descendant-or-self::node()" that the parser inserts for "//".
//
// It is distinguished from a written-out descendant-or-self step by having no
// predicates and the any-node test; a pattern that spells the axis itself is
// vanishingly rare and matching it the same way is harmless.
func isDescendantOrSelfNode(s *xpath.Step) bool {
	if s.Axis != xpath.AxisDescendantOrSelf || len(s.Predicates) != 0 {
		return false
	}
	kt, ok := s.Test.(*xpath.KindTest)
	return ok && kt.Any
}

// Alternatives splits a union pattern into one Pattern per branch.
//
// Section 6.4 says a template rule whose match pattern is a union behaves as
// if it were several template rules, one per branch, each with the default
// priority computed for that branch alone. Keeping them fused would give the
// whole rule the highest branch's priority, so a low-priority branch would
// outrank templates it should lose to; it would also make xsl:next-match skip
// the rule entirely after the first branch fired, when the spec has it
// reconsider the rule for each remaining branch.
func (p *Pattern) Alternatives() []*Pattern {
	if len(p.alts) < 2 {
		return []*Pattern{p}
	}
	out := make([]*Pattern, 0, len(p.alts))
	for _, a := range p.alts {
		q := &Pattern{src: p.src, alts: []*patternAlt{a}}
		q.prio = q.computePriority()
		out = append(out, q)
	}
	return out
}

// schemaDeclaredMatches applies the part of a schema-element() or
// schema-attribute() test that a plain name comparison cannot express.
//
// Section 2.5.5.3 of XPath 2.0 says schema-element(E) matches a node only if
// it "has been validated against" the global declaration E — the name alone
// is not enough. A node built by a stylesheet without validation carries no
// type annotation, so it is xs:untyped (or xs:untypedAtomic), and it does not
// match however it is named.
//
// The suite pins the distinction: match-180 builds my:userNode and
// my:simpleUserElem in a temporary tree with no validation, and expects the
// generic element() rule to take both. Treating schema-element(E) as a
// synonym for element(E) gave them to the schema rules instead, so nothing
// ever reached the untyped rule.
//
// The rest of the test — substitution-group membership and derivation of the
// annotation from the declaration's type — needs the schema components, which
// are not reachable from a compiled pattern. Rejecting the untyped case is the
// half that can be decided here, and it is the half that is unambiguously
// wrong to get backwards.
func schemaDeclaredMatches(nt xpath.NodeTest, node *xdm.Node) bool {
	kt, ok := nt.(*xpath.KindTest)
	if !ok || !kt.SchemaDeclared {
		return true
	}
	return node.TypeAnnotation != ""
}

// isNonRecoverable reports whether an error must propagate out of a pattern
// match rather than being read as "this node does not match".
//
// Only the codes the specification calls non-recoverable qualify. Everything
// else — an unbound key, a type mismatch inside a predicate — is an ordinary
// failure to match, which is what pattern evaluation is for.
func isNonRecoverable(err error) bool {
	return strings.HasPrefix(err.Error(), "XTDE0640")
}

// stepIsPositional reports whether any of a step's predicates can observe the
// context position.
func stepIsPositional(s patternStep) bool {
	for _, pred := range s.preds {
		if needsPosition(pred) {
			return true
		}
	}
	return false
}

// matchDescendantPositional matches a final descendant step whose predicates
// are positional.
//
// The equivalent expression of "chapter/descendant::foo[1]" numbers the foos
// within each chapter: the predicate filters the sequence descendant::foo
// selects from one anchor, in document order. Numbering the candidate among
// its siblings instead — which is what a step predicate ordinarily counts —
// gave every first-of-its-parent foo, so match-235 found the foo under a
// section but not the one that is its chapter's only child.
//
// Each ancestor that satisfies the preceding steps is a candidate anchor, and
// the node matches if it survives the predicates under any of them. The anchor
// is sought from the nearest ancestor outwards so that the innermost match is
// found first, which is the one document order would reach.
func (a *patternAlt) matchDescendantPositional(last patternStep,
	rest []patternStep, node *xdm.Node, ctx *xpath.Context) (bool, error) {

	// The node test has to hold whatever the anchor turns out to be.
	if !nodeTestHolds(last, node) {
		return false, nil
	}
	inner := rest[len(rest)-1]
	before := rest[:len(rest)-1]
	// descendant-or-self reaches the anchor itself, so the candidate may BE
	// the node the preceding step names: match-274's outer x is the root of
	// a parentless tree and is its own anchor.
	start := node.Parent
	if last.orSelf {
		start = node
	}
	for anc := start; anc != nil; anc = anc.Parent {
		// The anchor is the node the step before the descendant one names,
		// so it is tested against that step directly; matchAncestors then
		// verifies what precedes it, starting from the anchor.
		ok, err := matchStep(inner, anc, ctx)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		ok, err = a.matchAncestors(before, anc, ctx)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		ok, err = predicatesHoldAmong(last, node, descendantsMatching(last, anc), ctx)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// nodeTestHolds applies a step's axis and node test to a node, without its
// predicates.
func nodeTestHolds(s patternStep, node *xdm.Node) bool {
	principal := xdm.KindElement
	if s.attribute {
		principal = xdm.KindAttribute
	}
	if s.namespace {
		principal = xdm.KindNamespace
	}
	if s.namespace != (node.Kind == xdm.KindNamespace) {
		return false
	}
	if s.attribute != (node.Kind == xdm.KindAttribute) {
		return false
	}
	return s.nodeTest.Matches(node, principal) && schemaDeclaredMatches(s.nodeTest, node)
}

// descendantsMatching returns the descendants of root that satisfy a step's
// node test, in document order.
func descendantsMatching(s patternStep, root *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	// descendant-or-self puts the anchor at the head of the set, which is
	// where document order puts it.
	if s.orSelf && nodeTestHolds(s, root) {
		out = append(out, root)
	}
	var walk func(*xdm.Node)
	walk = func(n *xdm.Node) {
		for _, ch := range n.Children {
			if nodeTestHolds(s, ch) {
				out = append(out, ch)
			}
			walk(ch)
		}
	}
	walk(root)
	return out
}

// predicatesHoldAmong applies a step's predicates to node, numbering it within
// cand, and renumbering after each predicate as a path does.
func predicatesHoldAmong(s patternStep, node *xdm.Node, cand []*xdm.Node,
	ctx *xpath.Context) (bool, error) {

	for _, pred := range s.preds {
		kept := make([]*xdm.Node, 0, len(cand))
		for i, c := range cand {
			v, err := pred.Eval(ctx.WithFocus(c, i+1, len(cand)))
			if err != nil {
				return false, err
			}
			ok := false
			if len(v) == 1 {
				if at, isAtomic := v[0].(*xdm.Atomic); isAtomic && at.Type.IsNumeric() {
					ok = !at.IsNaN() && at.Float64() == float64(i+1)
				} else {
					ok, err = xpath.EffectiveBooleanValue(v)
					if err != nil {
						return false, err
					}
				}
			} else {
				ok, err = xpath.EffectiveBooleanValue(v)
				if err != nil {
					return false, err
				}
			}
			if ok {
				kept = append(kept, c)
			}
		}
		cand = kept
	}
	for _, c := range cand {
		if c == node {
			return true, nil
		}
	}
	return false, nil
}
