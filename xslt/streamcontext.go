package xslt

// §19.8.10 applied to the match patterns of a streamable mode's template
// rules, and §19.6's "template rule whose mode is declared streamable" clause
// that gives those rules their context posture.
//
// §19.8.10 was already written, for accumulators: patternIsFreeRanging in
// streamaccumulators.go classifies one pattern against the spec's conditions
// (a) no RootedPath, (b) every top-level predicate motionless and
// non-positional, (c) no streaming-parameter variable reference. What was
// missing was not the classification but its reach. The rule that makes a
// pattern's streamability matter is §19.6's third clause -- "if the
// focus-setting container of C is a template rule whose mode is declared with
// streamable='yes', then the context posture is striding" -- and no code
// followed a mode declaration to the rules that belong to it.
//
// So this file is a wiring job, deliberately: it finds the modes a stylesheet
// declares streamable, finds the template rules in them, and hands each rule's
// match pattern to the classifier that already exists. Nothing here
// re-implements §19.8.10.
//
// The same argument reaches a rule's BODY, which this file also assesses --
// see checkStreamableModeBodies. Following xsl:apply-templates into the other
// rules of a mode is not needed to do it: §19.6 makes a template rule of a
// streamable mode a focus-setting container in its own right, so the body's
// context posture is striding whatever dispatched to it, and each body is
// judged alone under the §19.8.4 instruction rules.
//
// The same reach argument covers xsl:accumulator-rule: §18.2.8 condition 3
// requires a streamable accumulator's rule patterns to be motionless, and
// checkAccumulatorStreamability already enforces it. It is not repeated here.

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// checkStreamableModePatterns raises XTSE3430 for a template rule in a
// streamable mode whose match pattern is not motionless (§19.8.10).
//
// The check is on the pattern alone. A pattern is matched against a node the
// processor already holds, so classifying it needs no context beyond the
// pattern itself -- which is why §19.8.10 can be applied here even though the
// rule's body cannot yet be. That asymmetry is the point: the pattern is
// decidable now, the body is not, and reporting only what is decidable is what
// keeps the check from refusing a stylesheet it does not understand.
func checkStreamableModePatterns(root *xdm.Node) error {
	streamable := streamableModeNames(root)
	if len(streamable) == 0 {
		return nil
	}
	var err error
	walkElements(root, func(el *xdm.Node) bool {
		if err != nil {
			return false
		}
		if !isXSL(el, "template") {
			return true
		}
		// templateModeNames returns nil for a named template (no @match) and
		// for "#all"/"#current", neither of which names a mode that can be
		// looked up. A rule with no match pattern has no pattern to judge.
		a := el.Attr("", "match")
		if a == nil {
			return true
		}
		inStreamable := false
		for _, m := range templateModeNames(el) {
			if streamable[m] {
				inStreamable = true
				break
			}
		}
		if !inStreamable {
			return true
		}
		free, known := patternIsFreeRanging(a.Value, el)
		if known && free && !patternPredicateOnlySyntacticallyNumeric(a.Value, el) {
			err = fmt.Errorf(
				"the match pattern %q of a template rule in a streamable "+
					"mode is not motionless, so it is not "+
					"guaranteed-streamable (XTSE3430)", a.Value)
			return false
		}
		return true
	})
	return err
}

// streamableModeNames collects the Clark names of the modes an xsl:mode
// declaration marks streamable="yes".
//
// A shadow attribute -- streamable-142 and its neighbours write
// _streamable="{$STREAMABLE}" over a static parameter -- has already been
// expanded into a plain @streamable by the time any static check runs, so
// reading the plain attribute is enough and no shadow handling belongs here.
//
// The unnamed mode is a mode like any other: xsl:mode with no @name and
// streamable="yes" makes every rule with no @mode streamable, which is how
// streamable-143 and -144 are written.
func streamableModeNames(root *xdm.Node) map[string]bool {
	out := map[string]bool{}
	walkElements(root, func(el *xdm.Node) bool {
		if isXSL(el, "mode") && isYes(el.AttrValue("streamable")) {
			for _, m := range modeNamesOf(el) {
				out[m] = true
			}
		}
		return true
	})
	return out
}

// patternPredicateOnlySyntacticallyNumeric reports whether a pattern's
// free-ranging verdict rests only on isPositionalPredicate's deliberately
// over-broad numeric test, and so must not be raised as an error.
//
// §19.8.10 defines a non-positional predicate by the predicate expression's
// STATIC TYPE: it is non-numeric when the intersection of that type with
// U{xs:decimal, xs:double, xs:float} is empty. isPositionalPredicate answers a
// different, syntactic question -- "does a numeric literal appear anywhere
// inside?" -- and §19.8.8.7, its original caller, can afford that: there an
// over-broad answer only withholds the scanning-expression rescue, which loses
// precision and reports nothing.
//
// Here the same answer would be reported as XTSE3430, so the gap between the
// two questions becomes a spurious rejection. accumulator-055s writes
//
//	match="p/text()[accumulator-before('text-in-p-count') eq 1]"
//
// whose predicate is a comparison. §19.8.10's own list makes the distinction
// explicitly: "p[@status = $status-codes[1]]" is motionless even though a
// numeric literal appears in it, because the predicate's static type is
// xs:boolean, while "p[$pnum + 1]" is not, because its type is numeric.
//
// So a predicate whose top-level operator is a comparison or a boolean
// connective has a boolean static type, and no numeric literal buried in its
// operands can change that. Where the free-ranging verdict rests on nothing
// but such a predicate, the classification is withheld rather than reported --
// the same "no opinion" the analysis gives any construct it does not model.
func patternPredicateOnlySyntacticallyNumeric(src string, at *xdm.Node) bool {
	ns := newNSResolver(at, xpathDefaultNamespace(at))
	expr, err := xpath.ParseVersion(src, ns, xpathVersionAt(at))
	if err != nil {
		return false
	}
	step, ok := expr.(*xpath.Step)
	if !ok {
		p, isPath := expr.(*xpath.PathExpr)
		if !isPath || len(p.Steps) == 0 {
			return false
		}
		if step, ok = p.Steps[len(p.Steps)-1].(*xpath.Step); !ok {
			return false
		}
	}
	sawRescued := false
	for _, pred := range step.Predicates {
		if !isPositionalPredicate(pred) {
			continue
		}
		// §19.8.10's first condition is independent of the static type: a
		// call to position(), last() or function-lookup() outside a nested
		// predicate makes the predicate positional whatever it returns.
		// "p[position() gt 2]" is a comparison, and the spec still lists it
		// as not motionless.
		if callsPositionOrLast(pred) || !hasBooleanStaticType(pred) {
			// A genuinely positional predicate -- "p[1]", "p[last()]" -- so
			// the verdict stands on its own merits.
			return false
		}
		sawRescued = true
	}
	return sawRescued
}

// callsPositionOrLast reports whether an expression calls one of the three
// functions §19.8.10 names as making a predicate positional: fn:position,
// fn:last and fn:function-lookup.
//
// The spec excepts calls that occur "within a nested predicate", which is why
// "p[@code = $status[last()]]" stays motionless; a FilterExpr's own predicates
// are therefore not descended into.
func callsPositionOrLast(e xpath.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *xpath.FuncCall:
		if x.Name.URI == fnNS {
			switch x.Name.Local {
			case "position", "last", "function-lookup":
				return true
			}
		}
		for _, arg := range x.Args {
			if callsPositionOrLast(arg) {
				return true
			}
		}
		return false
	case *xpath.BinaryOp:
		return callsPositionOrLast(x.Left) || callsPositionOrLast(x.Right)
	case *xpath.UnaryOp:
		return callsPositionOrLast(x.Operand)
	case *xpath.IfExpr:
		return callsPositionOrLast(x.Cond) ||
			callsPositionOrLast(x.Then) || callsPositionOrLast(x.Else)
	case *xpath.FilterExpr:
		// Only the base: the predicates of a nested filter are the nested
		// predicates the spec exempts.
		return callsPositionOrLast(x.Base)
	default:
		return false
	}
}

// hasBooleanStaticType reports whether an expression's static type is
// xs:boolean on syntax alone, which §19.8.10 needs in order to call a
// predicate non-numeric.
//
// Only the forms that are unambiguously boolean are named: a value or general
// comparison, a node comparison, and the "and"/"or" connectives. Anything else
// answers false, which leaves the existing verdict untouched.
func hasBooleanStaticType(e xpath.Expr) bool {
	b, ok := e.(*xpath.BinaryOp)
	if !ok {
		return false
	}
	switch b.Op {
	case "=", "!=", "<", "<=", ">", ">=",
		"eq", "ne", "lt", "le", "gt", "ge",
		"is", "<<", ">>", "and", "or":
		return true
	}
	return false
}

// checkStreamableModeBodies raises XTSE3430 for a template rule in a
// streamable mode whose BODY is not guaranteed-streamable: §19.8.4's
// instruction rules assessed against the striding context posture §19.6 gives
// such a rule.
//
// The reach argument that kept bodies out of the analysis was that following
// xsl:apply-templates into the other rules of a mode is not decidable. §19.6
// makes that argument unnecessary rather than answering it. Its third clause
// reads: "If the focus-setting container of C is a template rule whose mode is
// declared with streamable='yes', then the context posture is striding." A
// template rule is a focus-setting container in its own right, so its body's
// context posture is fixed by the rule's own mode declaration -- not by which
// apply-templates dispatched to it, nor by the posture of the expression that
// did. Each rule body is therefore assessed independently, exactly as an
// xsl:source-document body is. There is no fixed point to compute over a
// mode's rules, and so no termination obligation to discharge.
//
// What an xsl:apply-templates INSIDE such a body contributes is its own
// §19.8.4 rule, which reads the posture of its select expression and never the
// bodies of the rules it might reach. That locality is what makes the set
// decidable: "apply-templates select='.//section'" is free-ranging because
// .//section descends, whatever the section rule then goes on to do.
//
// A rule whose @mode is "#all" is not assessed. templateModeNames excludes it,
// because "#all" names every mode rather than any particular one, and a rule
// written for every mode cannot be held to the streamability of one of them.
//
// The verdict is withheld unless the analysis fully modelled the body (known),
// which is the guard the whole check rests on, and unless it rests on
// fn:current-group inside its own xsl:for-each-group: see
// bodyCallsCurrentGroup.
func checkStreamableModeBodies(root *xdm.Node) error {
	streamable := streamableModeNames(root)
	if len(streamable) == 0 {
		return nil
	}
	sets := attributeSetDeclarations(root)
	var err error
	walkElements(root, func(el *xdm.Node) bool {
		if err != nil {
			return false
		}
		// Only a template rule -- one with a match pattern -- is in a mode,
		// and only a rule has a mode declaration to take a posture from.
		if !isXSL(el, "template") || el.Attr("", "match") == nil {
			return true
		}
		inStreamable := false
		for _, m := range templateModeNames(el) {
			if streamable[m] {
				inStreamable = true
				break
			}
		}
		if !inStreamable {
			return true
		}
		p, known := analyzeSequenceConstructor(el, postureStriding, sets)
		if known && !p.streamable() && !bodyCallsCurrentGroup(el) {
			err = fmt.Errorf(
				"the body of the template rule matching %q in a streamable "+
					"mode is %v and %v, so it is not "+
					"guaranteed-streamable (XTSE3430)",
				el.AttrValue("match"), p.posture, p.sweep)
			return false
		}
		return true
	})
	return err
}

// bodyCallsCurrentGroup reports whether a template rule's body calls
// fn:current-group INSIDE an xsl:for-each-group of its own.
//
// §19.8.9.4 gives such a call the posture and sweep of the group's select
// expression, so where the select is striding the call is striding too. The
// analysis reaches that answer, but only along the traversal that
// xsl:for-each-group itself drives: the group's properties are put in scope by
// forEachGroup as it descends into the body, and are not recoverable from the
// call site alone. Composing them back out is where the precision is lost.
//
// si-group-055 is the case. Its rule groups with group-starting-with over a
// striding select and writes
//
//	<xsl:apply-templates select="current-group() except ."/>
//
// inside an xsl:fork. Both operands of the except are striding and motionless,
// so §19.8.8.4 widens the result to crawling -- by its own admission a choice
// rather than a necessity:
//
//	"Where the two operands are both striding, there are cases where an
//	implementation could determine that the result is also striding: for
//	example (author | editor). In general, however, the combination of two
//	striding operands may produce a sequence of nodes that have nested
//	subtrees (consider author | author/name), so the result is classified as
//	crawling."
//
// §19.8.4.5 then makes an xsl:apply-templates over a crawling selection
// roaming, and the rule is refused for a nesting the group cannot produce: its
// members are siblings, and except only removes items. The catalog asserts
// OUTPUT for si-group-055, and its description names the Saxon bug (3256) it
// was written against, so a processor that refuses it is wrong about the
// stylesheet. si-group-203 and aspiring-002 write the same idiom.
//
// Separating the group's real posture from the widened one needs the §19.2
// U-type inference this analysis approximates syntactically, so the verdict is
// withheld rather than guessed -- the same "no opinion" every unmodelled
// construct gets.
//
// The withholding stops at the xsl:for-each-group boundary, and that boundary
// is what keeps si-fork-116 refused. There the call is written in a rule with
// no grouping of its own, so its group is genuinely out of reach and
// §19.8.9.4's "otherwise, roaming and free-ranging" is a fact about the
// stylesheet; the catalog asserts XTSE3430 for it, noting "it's a static
// error". si-fork-115, the same stylesheet asking for the KEY instead, is
// streamable by §19.8.9.5 without needing anything here.
func bodyCallsCurrentGroup(rule *xdm.Node) bool {
	found := false
	var walk func(el *xdm.Node, inGroup bool)
	walk = func(el *xdm.Node, inGroup bool) {
		if found {
			return
		}
		if el != rule && isXSL(el, "for-each-group") {
			inGroup = true
		}
		if inGroup {
			for _, at := range el.Attrs {
				// Exactly one reference. Two references to the group in one
				// expression read it twice, which §19.8.1's limit of one
				// potentially-consuming operand refuses on its own terms and
				// independently of §19.8.8.4's widening -- si-group-017
				// writes "count(current-group()), current-group()" and the
				// catalog asserts XTSE3430 for it. Withholding there would
				// silence a real refusal, so only the single-reference case
				// is withheld.
				if countCurrentGroupRefs(at.Value) == 1 {
					found = true
					return
				}
			}
		}
		for _, c := range el.ChildElements() {
			walk(c, inGroup)
		}
	}
	walk(rule, false)
	return found
}

// countCurrentGroupRefs counts the calls on fn:current-group in an attribute
// value, not counting calls on fn:current-grouping-key, whose name begins with
// the same characters.
func countCurrentGroupRefs(v string) int {
	const name = "current-group"
	n := 0
	for i := 0; ; {
		j := strings.Index(v[i:], name)
		if j < 0 {
			return n
		}
		at := i + j
		i = at + len(name)
		if at > 0 && isNameContinuation(rune(v[at-1])) {
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(v[i:], " \t\r\n"), "(") {
			n++
		}
	}
}
