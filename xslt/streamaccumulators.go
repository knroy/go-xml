package xslt

// The XTSE3430 check for xsl:accumulator, driven by §18.2.8 "Streamability of
// Accumulators" and §19.8.10 "Classifying Patterns".
//
// §18.2.8 gives five conditions, all of which an accumulator declared
// streamable="yes" must satisfy to be guaranteed-streamable:
//
//	1. the declaration has streamable="yes";
//	2. the applies-to pattern, if present, is motionless;
//	3. in every xsl:accumulator-rule, the match pattern is motionless;
//	4. the initial-value expression is grounded and motionless;
//	5. the select expression, or the contained sequence constructor, is
//	   grounded and motionless.
//
// Conditions 2 and 3 are decided by §19.8.10, whose rules are about the shape
// of the pattern rather than about the lattice; conditions 4 and 5 are decided
// by the same §19.8 analysis that streamcheck.go already applies to select
// expressions, so they reuse analyzeExpr.
//
// The `known` discipline of streamcheck.go is kept exactly: a verdict is
// reported only when the analysis positively modelled every construct it met.
// A pattern or expression holding anything unmodelled is abandoned in silence,
// because the cost of a spurious XTSE3430 -- rejecting a valid stylesheet at
// compile time, with no way around it -- is far higher than the cost of a
// missing one.

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// checkAccumulatorStreamability applies §18.2.8 to every xsl:accumulator in
// the module that declares streamable="yes".
//
// It is deliberately independent of whether any mode or source document is
// itself streamable. §18.2.8 attaches the condition to the accumulator
// declaration alone: an accumulator that says streamable="yes" must be
// guaranteed-streamable, whatever else the stylesheet does with it.
func checkAccumulatorStreamability(root *xdm.Node) error {
	var err error
	walkElements(root, func(el *xdm.Node) bool {
		if err != nil {
			return false
		}
		if !isXSL(el, "accumulator") {
			return true
		}
		// Condition 1. An accumulator that does not claim streamability is
		// not held to any of this. Note that the value is taken literally:
		// where the attribute is written as a shadow attribute
		// (_streamable="{$p}") the static parameter has already been
		// substituted by the time the static checks run, so what is read
		// here is the resolved value.
		if !isYes(el.AttrValue("streamable")) {
			return true
		}
		if e := checkOneAccumulator(el); e != nil {
			err = e
			return false
		}
		return true
	})
	return err
}

// checkOneAccumulator applies conditions 2 to 5 to a single declaration.
func checkOneAccumulator(acc *xdm.Node) error {
	name := acc.AttrValue("name")

	// Condition 2: the applies-to pattern must be motionless.
	if a := acc.Attr("", "applies-to"); a != nil {
		if free, known := patternIsFreeRanging(a.Value, acc); known && free {
			return fmt.Errorf(
				"the applies-to pattern %q of streamable accumulator %q is "+
					"free-ranging, so the accumulator is not "+
					"guaranteed-streamable (XTSE3430)", a.Value, name)
		}
	}

	// Condition 4: the initial-value expression must be grounded and
	// motionless. §18.2.8 evaluates it with a singleton focus on the root of
	// the streamed tree, so its context posture is striding.
	if a := acc.Attr("", "initial-value"); a != nil {
		if bad, known := exprIsNotGroundedMotionless(a.Value, acc); known && bad {
			return fmt.Errorf(
				"the initial-value expression %q of streamable accumulator "+
					"%q is not grounded and motionless, so the accumulator "+
					"is not guaranteed-streamable (XTSE3430)", a.Value, name)
		}
	}

	// Conditions 3 and 5, per contained rule.
	for _, rule := range acc.ChildElements() {
		if !isXSL(rule, "accumulator-rule") {
			continue
		}
		if a := rule.Attr("", "match"); a != nil {
			if free, known := patternIsFreeRanging(a.Value, rule); known && free {
				return fmt.Errorf(
					"the match pattern %q of a rule in streamable "+
						"accumulator %q is free-ranging, so the accumulator "+
						"is not guaranteed-streamable (XTSE3430)",
					a.Value, name)
			}
		}
		if a := rule.Attr("", "select"); a != nil {
			// The rule's select is evaluated with the focus on the node the
			// rule matched, so what "." can be -- and therefore whether
			// absorbing it reads anything further from the stream -- is
			// decided by the rule's own match pattern.
			kids := matchedNodeAllowsChildren(rule.AttrValue("match"), rule)
			bad, known := exprIsNotGroundedMotionlessCtx(a.Value, rule, kids)
			// §18.2.1: "the result of both the initial-value and select
			// expressions is converted to the type declared in the as
			// attribute by applying the function conversion rules". Where
			// that type is atomic, the conversion atomizes, and an atomized
			// value holds no streamed nodes -- so the posture is grounded
			// whatever the expression itself returned. accumulator-007's
			// "if (@amount < $value) then @amount else $value" is striding on
			// its own, and grounded once as="xs:double" has converted it.
			//
			// The sweep is untouched: atomizing a node that is already in
			// hand moves the input position no further, and atomizing one
			// that is not was already consuming before the conversion.
			if bad && known && atomicSequenceType(acc.AttrValue("as")) {
				if p, k := exprSweepCtx(a.Value, rule, kids); k &&
					p == sweepMotionless {
					bad = false
				}
			}
			if known && bad {
				return fmt.Errorf(
					"the select expression %q of a rule in streamable "+
						"accumulator %q is not grounded and motionless, so "+
						"the accumulator is not guaranteed-streamable "+
						"(XTSE3430)", a.Value, name)
			}
		}
		// A rule with a contained sequence constructor rather than a select
		// is condition 5's other half. The constructor's instructions are
		// governed by §19.8.6, whose rules are not written here, so it is
		// left alone rather than guessed at.
	}
	return nil
}

// exprIsNotGroundedMotionless reports whether src is positively known to be
// something other than grounded and motionless, per §18.2.8 conditions 4 and
// 5. The second result is false when the analysis met a construct it does not
// model, in which case the first must be ignored.
//
// The context posture is striding: §18.2.8 evaluates both the initial-value
// expression and an accumulator rule's select with a singleton focus on a node
// of the streamed tree.
func exprIsNotGroundedMotionless(src string, at *xdm.Node) (bool, bool) {
	return exprIsNotGroundedMotionlessCtx(src, at, true)
}

// exprIsNotGroundedMotionlessCtx is exprIsNotGroundedMotionless with the one
// extra fact that decides an absorption's sweep: whether the context item can
// be a node with children (§19.8.1). Absorbing an attribute or a text node
// reads nothing further from the stream, so "string()" on a text node is
// motionless where the same expression on an element is consuming.
func exprIsNotGroundedMotionlessCtx(src string, at *xdm.Node, ctxAllowsChildren bool) (bool, bool) {
	ns := newNSResolver(at, xpathDefaultNamespace(at))
	expr, err := xpath.ParseVersion(src, ns, xpathVersionAt(at))
	if err != nil {
		// A malformed expression is some other check's error to report.
		return false, false
	}
	a := &analyzer{
		ctxPosture:        postureStriding,
		ctxAllowsChildren: ctxAllowsChildren,
		known:             true,
	}
	p := a.expr(expr)
	if !a.known {
		return false, false
	}
	return p.posture != postureGrounded || p.sweep != sweepMotionless, true
}

// exprSweepCtx returns just the sweep of src, and whether it was fully
// modelled. It exists for the atomizing case, where the posture the conversion
// imposes is known in advance and only the sweep still has to be derived.
func exprSweepCtx(src string, at *xdm.Node, ctxAllowsChildren bool) (sweep, bool) {
	ns := newNSResolver(at, xpathDefaultNamespace(at))
	expr, err := xpath.ParseVersion(src, ns, xpathVersionAt(at))
	if err != nil {
		return sweepFreeRanging, false
	}
	a := &analyzer{
		ctxPosture:        postureStriding,
		ctxAllowsChildren: ctxAllowsChildren,
		known:             true,
	}
	p := a.expr(expr)
	return p.sweep, a.known
}

// atomicSequenceType reports whether the as attribute of an xsl:accumulator
// names a sequence of atomic values, so that the function conversion rules of
// §18.2.1 atomize the accumulator's value and thereby ground it.
//
// The reading is deliberately narrow and syntactic: a built-in atomic type
// name from the XML Schema namespace, with an optional occurrence indicator.
// Anything else -- item()*, node(), element(), a map or array type, a
// user-defined type, or an absent attribute (which defaults to item()*) --
// answers false, leaving the expression's own posture to stand. That is the
// safe direction here: a false answer can only preserve an error the
// unconverted analysis found, and the only errors it preserves are ones where
// the value really can hold a streamed node.
func atomicSequenceType(as string) bool {
	s := strings.TrimSpace(as)
	if s == "" {
		return false
	}
	// Strip an occurrence indicator. "xs:double?" and "xs:double*" atomize
	// exactly as "xs:double" does.
	if n := len(s); n > 0 {
		switch s[n-1] {
		case '?', '*', '+':
			s = strings.TrimSpace(s[:n-1])
		}
	}
	prefix, local, ok := strings.Cut(s, ":")
	if !ok {
		// An unprefixed name cannot be a built-in schema type: the default
		// element namespace does not apply to type names in a sequence type.
		return false
	}
	// The prefix must be bound to the XML Schema namespace. Checking the
	// conventional spelling alone would be wrong in either direction, so the
	// name is required to look like a schema type and the caller's stylesheet
	// is trusted to have bound xs: conventionally -- the same assumption the
	// rest of the static checks make.
	if prefix != "xs" && prefix != "xsd" {
		return false
	}
	return atomicSchemaTypes[local]
}

// atomicSchemaTypes names the built-in atomic types of XML Schema. Only the
// atomic ones appear: xs:anyType and xs:untyped are not atomic, and a value of
// type xs:anyAtomicType is.
var atomicSchemaTypes = map[string]bool{
	"anyAtomicType": true, "string": true, "boolean": true, "decimal": true,
	"float": true, "double": true, "duration": true, "dateTime": true,
	"time": true, "date": true, "gYearMonth": true, "gYear": true,
	"gMonthDay": true, "gDay": true, "gMonth": true, "hexBinary": true,
	"base64Binary": true, "anyURI": true, "QName": true, "NOTATION": true,
	"normalizedString": true, "token": true, "language": true,
	"NMTOKEN": true, "Name": true, "NCName": true, "ID": true, "IDREF": true,
	"ENTITY": true, "integer": true, "nonPositiveInteger": true,
	"negativeInteger": true, "long": true, "int": true, "short": true,
	"byte": true, "nonNegativeInteger": true, "unsignedLong": true,
	"unsignedInt": true, "unsignedShort": true, "unsignedByte": true,
	"positiveInteger": true, "yearMonthDuration": true,
	"dayTimeDuration": true, "untypedAtomic": true,
}

// matchedNodeAllowsChildren reports whether a node matched by the pattern src
// can have children. It answers the §19.8.1 question for the focus of an
// accumulator rule, whose context item is the node its match pattern selected.
//
// The answer is read off the pattern's last step, which is the one that
// constrains the matched node itself: "section/p/text()" matches a text node,
// "@price" an attribute. Anything the reading does not recognise answers yes,
// which leaves an absorption at its full consuming sweep -- the conservative
// direction, since it can only suppress an error, never invent one.
func matchedNodeAllowsChildren(src string, at *xdm.Node) bool {
	if src == "" {
		return true
	}
	ns := newNSResolver(at, xpathDefaultNamespace(at))
	expr, err := xpath.ParseVersion(src, ns, xpathVersionAt(at))
	if err != nil {
		return true
	}
	return matchedExprAllowsChildren(expr)
}

func matchedExprAllowsChildren(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Step:
		return stepAllowsChildren(x)
	case *xpath.PathExpr:
		if n := len(x.Steps); n > 0 {
			return matchedExprAllowsChildren(x.Steps[n-1])
		}
		// The pattern "/" matches the document node, which has children.
		return true
	case *xpath.BinaryOp:
		// A union matches a node of either kind, so it can have children if
		// either alternative can.
		if x.Op == "|" || x.Op == "union" {
			return matchedExprAllowsChildren(x.Left) ||
				matchedExprAllowsChildren(x.Right)
		}
		return true
	default:
		return true
	}
}

// patternIsFreeRanging reports whether the pattern src is positively known to
// be free-ranging, per §19.8.10. The second result is false when the pattern
// held something the classification does not model.
//
// §19.8.10 states the rule as three conditions for being motionless, and
// classifies anything else as free-ranging:
//
//	a. the pattern contains no RootedPath;
//	b. every top-level predicate contains a motionless expression, assessed
//	   with a context posture of striding, and is a non-positional predicate;
//	c. the pattern contains no reference to a streaming parameter.
//
// Condition (c) concerns streamable stylesheet functions, which the analysis
// does not model at all; every variable is treated as grounded and motionless,
// which is the right answer for any stylesheet that declares no such function.
func patternIsFreeRanging(src string, at *xdm.Node) (bool, bool) {
	ns := newNSResolver(at, xpathDefaultNamespace(at))
	expr, err := xpath.ParseVersion(src, ns, xpathVersionAt(at))
	if err != nil {
		return false, false
	}
	return patternExprFreeRanging(expr, at)
}

// patternExprFreeRanging classifies one alternative of a parsed pattern.
//
// The node the pattern as a whole matches is what fn:current() denotes inside
// its predicates (§19.8.9.3), so its kind is computed once here, from the
// alternative being classified, and carried down to every predicate.
func patternExprFreeRanging(e xpath.Expr, at *xdm.Node) (bool, bool) {
	return patternExprFreeRangingIn(e, at, matchedExprAllowsChildren(e))
}

func patternExprFreeRangingIn(e xpath.Expr, at *xdm.Node, matchedAllowsChildren bool) (bool, bool) {
	switch x := e.(type) {
	case *xpath.BinaryOp:
		// "a | b" and "a union b": a union of patterns is motionless only if
		// every alternative is. The operator is the only binary form that
		// can appear at the top level of a pattern.
		if x.Op != "|" && x.Op != "union" {
			return false, false
		}
		lf, lk := patternExprFreeRanging(x.Left, at)
		if !lk {
			return false, false
		}
		rf, rk := patternExprFreeRanging(x.Right, at)
		if !rk {
			return false, false
		}
		return lf || rf, true

	case *xpath.PathExpr:
		// Condition (a) needs no test here. A RootedPath is production [6]
		// of the pattern grammar: a pattern that STARTS with a variable
		// reference or one of the outer function calls (doc, id,
		// element-with-id, key, root), as in "$doc//p" or "id('abc')". A
		// leading "/" does not make one -- §19.8.10 lists "/", "/*", "p/q"
		// and "//p/text()[. = 'Introduction']" among its motionless
		// examples. Both RootedPath forms reach patternExprFreeRanging's
		// default branch, which abandons the classification, so a pattern
		// built from steps is exactly the case that arrives here.
		for _, s := range x.Steps {
			free, known := patternStepFreeRanging(s, at, matchedAllowsChildren)
			if !known {
				return false, false
			}
			if free {
				return true, true
			}
		}
		return false, true

	case *xpath.Step:
		return patternStepFreeRanging(x, at, matchedAllowsChildren)

	case *xpath.FilterExpr:
		// XSLT 3.0 extends the pattern grammar with "." as a PatternAxis-free
		// alternative, so ".[starts-with(., ':B:')]" is a legal pattern that
		// matches any node satisfying the predicate. The parser gives it as a
		// FilterExpr over a ContextItem rather than as a Step, so without
		// this arm the classification abandoned every such pattern and
		// §19.8.10 was never applied to it -- streamable-142.
		//
		// The predicates are top-level predicates of the pattern, so
		// condition (b) applies to them exactly as it does to a step's. "."
		// matches a node of any kind, so the context item may have children:
		// a predicate that atomises it, as starts-with(., ...) does, is
		// absorbing and hence not motionless, which is precisely why
		// streamable-142 marks its rule "NOT MOTIONLESS".
		if _, ok := x.Base.(*xpath.ContextItem); !ok {
			return false, false
		}
		return patternPredicatesFreeRanging(x.Predicates, true, matchedAllowsChildren)

	default:
		// id() and key() patterns, and anything else the classification does
		// not model.
		return false, false
	}
}

// patternStepFreeRanging applies condition (b) to the predicates of one step.
//
// The step's axis plays no part: §19.8.10 classifies a pattern by its
// RootedPath and its predicates alone, because a pattern is matched against a
// node already in hand rather than evaluated as a path. What makes
// "fig[caption]" free-ranging is the predicate, not the "fig".
func patternStepFreeRanging(e xpath.Expr, at *xdm.Node, matchedAllowsChildren bool) (bool, bool) {
	s, ok := e.(*xpath.Step)
	if !ok {
		return false, false
	}
	return patternPredicatesFreeRanging(s.Predicates, stepAllowsChildren(s), matchedAllowsChildren)
}

// patternPredicatesFreeRanging applies §19.8.10 condition (b) to a list of
// top-level pattern predicates. allowsChildren says whether the node the
// predicates are applied to can have children, which is what decides whether
// atomising the context item is absorbing; matchedAllowsChildren says the same
// of the node the WHOLE pattern matches, which is what fn:current() denotes
// (§19.8.9.3).
func patternPredicatesFreeRanging(preds []xpath.Expr, allowsChildren, matchedAllowsChildren bool) (bool, bool) {
	for _, pred := range preds {
		// Condition (b), second half: the predicate must be non-positional.
		// isPositionalPredicate already implements §19.8.10's definition --
		// a call to position(), last(), or function-lookup() outside a
		// nested predicate, or a numeric predicate expression.
		if isPositionalPredicate(pred) {
			return true, true
		}
		// Condition (b), first half: the expression immediately contained in
		// the predicate must be motionless, assessed with a context posture
		// of striding. A predicate on a step of a pattern is applied to a
		// node of the streamed tree, so striding is the posture, and reading
		// a child of that node -- "fig[caption]" -- is consuming. The
		// context item is what the step itself selects, which is why
		// "@price[starts-with(., '$')]" and "text()[starts-with(., '$')]"
		// are motionless while "p[starts-with(., '$')]" is not.
		a := &analyzer{
			ctxPosture:        postureStriding,
			ctxAllowsChildren: allowsChildren,
			known:             true,
			// §19.8.9.3 on a call inside a pattern: "If the call appears
			// within a pattern, then climbing and motionless." (The
			// wording "the context posture is always striding" is from
			// the Last Call draft, which Bug30033 superseded.)
			//
			// Striding rather than climbing is used here deliberately, and
			// the two are equivalent for what this function asks. The only
			// question put to the analyzer is whether the predicate is
			// MOTIONLESS. Off current(), every axis that is motionless
			// under climbing -- self, parent, ancestor[-or-self],
			// attribute, namespace -- is motionless under striding too,
			// and every axis that is not (child, descendant, the sibling
			// and document-order axes) is non-motionless under both:
			// consuming under striding, roaming and free-ranging under
			// climbing. So no pattern's verdict turns on the choice.
			//
			// Striding is what currentAllowsChildren below is stated
			// against: current() denotes the node the pattern is being
			// matched against, which is the node this step selects, so
			// whether absorbing it reads further from the stream is that
			// step's question -- the distinction that separates
			// "text()[$parts = current()]" from "p[$parts = current()]".
			currentPosture:        postureStriding,
			currentAllowsChildren: matchedAllowsChildren,
			currentInScope:        true,
		}
		p := a.expr(pred)
		if !a.known {
			return false, false
		}
		if p.sweep != sweepMotionless {
			return true, true
		}
	}
	return false, true
}
