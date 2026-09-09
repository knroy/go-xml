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
// which is the guard the whole check rests on.
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
		if known && !p.streamable() {
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
