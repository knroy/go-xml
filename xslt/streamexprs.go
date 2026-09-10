package xslt

// The XPath expression rules of §19.8.8 that streamability.go's dispatch does
// not cover, plus the two §19.8.9 sections for fn:position and fn:last.
//
// Each rule here exists to make a construct *modelled*, so that the analysis
// returns known=true and streamcheck.go may act on the verdict. None of them
// makes the XTSE3430 check fire more eagerly: a construct this file does not
// recognise still falls through to analyzer.unknown().
//
// Covered here:
//
//	§19.8.8.4  union, intersect, except      unionExpr
//	§19.8.8.5  the document-node(element(X)) clause, shared with "treat as"
//	§19.8.8.16 map expressions               mapConstructor
//	           square/curly array constructors, on the same shape as a map
//	§19.8.9.14 fn:last                       lastFunction
//	§19.8.9.16 fn:position                   (grounded and motionless)

import (
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// unionExpr applies §19.8.8.4 to "E | F", "E intersect F" and "E except F".
//
// The spec gives a five-step cascade rather than the general streamability
// rules, because a union of two streamed operands is evaluated in a single
// pass with the results merged, which the general rules cannot express. Both
// operands have what amounts to a transmission role, so neither is assessed
// with a usage; the cascade works on their postures and sweeps directly.
func (a *analyzer) unionExpr(left, right xpath.Expr) props {
	l := a.expr(left)
	r := a.expr(right)

	// "If either of the two operands is free-ranging, then roaming and
	// free-ranging." Testing streamable() rather than the sweep alone also
	// catches a roaming operand, which §19 never separates from
	// free-ranging.
	if !l.streamable() || !r.streamable() {
		return roamingFreeRanging
	}

	// "If either of the two operands is grounded and motionless, then the
	// posture and sweep of the other operand." This is the clause that makes
	// "$extraItem | //ITEM" take the posture of "//ITEM" -- crawling --
	// rather than being rejected outright, and it is why sx-union-201 fails
	// later, at the "/" that follows, rather than here.
	if l == groundedMotionless {
		return r
	}
	if r == groundedMotionless {
		return l
	}

	// "If both operands are climbing, then climbing and the wider of the
	// sweeps of the two operands."
	if l.posture == postureClimbing && r.posture == postureClimbing {
		return props{postureClimbing, wider(l.sweep, r.sweep)}
	}

	// "If the left-hand operand is striding or crawling and the right-hand
	// operand is also striding or crawling, then crawling and the wider of
	// the sweeps of the two operands."
	//
	// Crawling even when both are striding: the spec's own note explains
	// that "author | author/name" yields nodes with nested subtrees, so two
	// striding operands do not in general compose to a striding result.
	if streamedDescent(l.posture) && streamedDescent(r.posture) {
		return props{postureCrawling, wider(l.sweep, r.sweep)}
	}

	// "Otherwise, roaming and free-ranging" -- the heterogeneous case, such
	// as "child::div | parent::div".
	return roamingFreeRanging
}

// streamedDescent reports whether a posture is one of the two §19.8.8.4 calls
// out together: striding or crawling, the postures of a node reached by
// descending from the context node.
func streamedDescent(p posture) bool {
	return p == postureStriding || p == postureCrawling
}

// mapConstructor applies §19.8.8.16 to "map{k: v, ...}".
//
// §19.8.8.16 defines the posture and sweep as those of the equivalent xsl:map
// instruction, whose rule is §19.8.4.23. A map expression always translates to
// an xsl:map whose children are exclusively xsl:map-entry, so only the first
// branch of §19.8.4.23 can apply, and the "otherwise" branch is unreachable
// here. Each entry is one xsl:map-entry under §19.8.4.24: the key expression
// absorbs, the value expression navigates.
//
// Navigation on the value is the whole point. It is what makes
// "map{'authors': //AUTHOR}" free-ranging, because a streamed node cannot be
// stored in a map and read back after the stream has moved on. Grounding the
// value first -- "map{'authors': copy-of(//AUTHOR)}" -- is the supported way
// to write it.
func (a *analyzer) mapConstructor(x *xpath.MapConstructor) props {
	// §19.8.4.23: "if any of these xsl:map-entry children is roaming or
	// free-ranging, then roaming and free-ranging; otherwise, grounded and
	// the widest sweep of the xsl:map-entry children."
	widest := sweepMotionless
	for i, key := range x.Keys {
		entry := combine([]operand{
			a.operandOf(key, usageAbsorption),
			a.operandOf(x.Values[i], usageNavigation),
		}, false)
		if !entry.streamable() {
			return roamingFreeRanging
		}
		widest = wider(widest, entry.sweep)
	}
	// The map itself is grounded: it is a newly constructed value, holding
	// no reference to a streamed node -- which the navigation usage above
	// has already guaranteed.
	return props{postureGrounded, widest}
}

// arrayConstructor applies the general streamability rules to "[a, b]" and
// "array{...}".
//
// Neither spelling appears in the §19.8.8 proforma table, because that table
// enumerates XPath 3.0 productions and array constructors arrived in XPath
// 3.1. The general rules are what remains, and they give the right answer for
// the same reason they do for a map: an array, like a map, is a function item
// that may outlive the stream position at which it was built, so a member that
// delivered a streamed node would be read back after the stream had moved on.
// That is navigation, and navigation from any streamed posture is
// free-ranging (§19.8.1).
//
// The construct is therefore grounded whenever it is streamable at all, and
// "[$extraItem, //ITEM]" is refused outright, because "//ITEM" would have put
// a streamed node into the array. That is the array half of
// sx-square-array-201; the "?*" that follows it there is the postfix lookup
// operator, which is not modelled, so that case still returns no verdict.
//
// The members are assessed the way §19.8.4.23 assesses the entries of an
// xsl:map: each on its own, with the array grounded and carrying the widest
// sweep any member needed. Rolling them up through combine instead would apply
// §19.8.1's limit of one potentially-consuming operand, and that limit is
// wrong here for the same reason it is wrong for xsl:map -- the members are
// evaluated in one pass of the input, as an implicit fork, so two grounded
// members that each read the stream are between them still one read.
func (a *analyzer) arrayConstructor(x *xpath.ArrayConstructor) props {
	widest := sweepMotionless
	for _, m := range x.Members {
		p := combine([]operand{a.operandOf(m, usageNavigation)}, false)
		if !p.streamable() {
			return roamingFreeRanging
		}
		widest = wider(widest, p.sweep)
	}
	// Grounded whatever the members cost: the navigation usage above has
	// already refused any member that would have delivered a streamed node.
	return props{postureGrounded, widest}
}

// lastFunction applies §19.8.9.14 to a call on fn:last.
//
// "If the context posture for a call on the last function is striding,
// crawling, or roaming, then the posture of the function is roaming, and the
// sweep is free-ranging. In all other cases the function is grounded and
// motionless."
//
// The exempt postures are grounded and climbing, which is what makes
// "ancestor::*[@xml:space][last()]" streamable: the ancestors of the current
// node are all in hand, so counting them reads nothing further.
func (a *analyzer) lastFunction() props {
	switch a.ctxPosture {
	case postureStriding, postureCrawling, postureRoaming:
		return roamingFreeRanging
	default:
		return groundedMotionless
	}
}

// treatExpr applies the §19.8.8 proforma "T treat as TYPE", with the
// §19.8.8.5 exception for an item type that cannot be tested without reading
// the document.
//
// The proforma gives a single operand with usage transmission, and that is the
// whole rule for almost every type -- it is what makes "root(.) treat as
// document-node()" motionless in the worked example of §19.8.8.7.
//
// An item type of the form document-node(element(X)) is different, for the
// reason §19.8.8.5 gives when it makes the same type absorption for "instance
// of": such a type "matches a document node only if it has exactly one element
// node child, and this cannot be determined without consuming the document".
// The check is the same check whichever operator asks for it, so the read it
// costs is the same read.
//
// Unlike "instance of", which yields a boolean and so has that one operand,
// "treat as" also returns the node itself. Both roles are therefore present:
// the operand is transmitted, and it is additionally absorbed to establish the
// type. Modelling only the absorption would ground the result and lose the
// node's streamed posture; modelling only the transmission would lose the cost
// of the test, which is what sx-treat-902 and sx-treat-903 exist to catch.
func (a *analyzer) treatExpr(x *xpath.TreatExpr) props {
	if !isDocumentNodeWithContent(x.Type) {
		return combine([]operand{a.operandOf(x.Operand, usageTransmission)}, false)
	}
	return combine([]operand{
		a.operandOf(x.Operand, usageTransmission),
		a.operandOf(x.Operand, usageAbsorption),
	}, false)
}

// isDocumentNodeWithContent reports whether the item type of st is a
// document-node() test that constrains its element child -- the
// document-node(element(X)) and document-node(schema-element(X)) forms of
// §19.8.8.5.
//
// A bare document-node(), with no inner test, is not one of them: it matches
// any document node on sight, so it inspects rather than absorbs. That is the
// distinction the spec's own worked example in §19.8.8.7 relies on, where
// "root(.) treat as document-node()" is transmission and stays motionless.
func isDocumentNodeWithContent(st xpath.SequenceType) bool {
	kt, ok := st.ItemType.(*xpath.KindTest)
	if !ok {
		return false
	}
	return kt.Kind == xdm.KindDocument && !kt.Any && kt.Content != nil
}

// forExpr applies §19.8.8.1 to "for $v in S return R".
//
// The section gives two rules, in order:
//
//	If S is not grounded, then roaming and free-ranging.
//	Otherwise, the general streamability rules apply. The operand roles are:
//	  The in expression (S). This has usage navigation.
//	  The return expression (R). This is a higher-order operand with usage
//	  transmission.
//
// The first rule stops the range variable being bound to a streamed node --
// "this disallows expressions of the form for $x in child::section return
// $x/para, because this requires data flow analysis". It is not written out
// below, because the second rule already implies it and a branch that cannot
// be reached is a branch no test can hold honest: §19.8.1 gives the usage
// navigation an adjusted sweep of free-ranging for EVERY non-grounded posture
// (climbing, striding and crawling alike), and a free-ranging operand makes
// the construct roaming before any other rule is consulted. So giving S the
// usage navigation, as rule 2 requires, rejects exactly the expressions rule 1
// names. Sabotage-testing an explicit posture test here left every assertion
// green, which is what dead code looks like.
//
// A multi-clause "for $i in X, $j in Y return R" is nested, exactly as
// §19.8.8.2 says to rewrite the quantified form: the outer clause's return
// expression is the rest of the for. The rewriting is done here rather than
// in the parser because it is a rule about this analysis and nothing else.
func (a *analyzer) forExpr(x *xpath.ForExpr) props {
	if len(x.Bindings) == 0 {
		return a.unknown()
	}
	seq := a.operandOf(x.Bindings[0].Seq, usageNavigation)
	ret := x.Return
	if len(x.Bindings) > 1 {
		ret = &xpath.ForExpr{Bindings: x.Bindings[1:], Return: x.Return}
	}
	return combine([]operand{
		seq,
		a.higherOrderOperand(ret, usageTransmission),
	}, false)
}

// quantifiedExpr applies §19.8.8.2 to "some|every $v in S satisfies C".
//
// Unlike the for rule, there is no separate "S must be grounded" test: the
// general rules apply directly, with S carrying usage navigation -- and
// navigation from any non-grounded posture is free-ranging (§19.8.1), which
// reaches the same place by the route the spec chose. C is a higher-order
// operand with usage inspection.
//
// "some $i in X, $j in Y satisfies C" is rewritten as nested quantified
// expressions, which §19.8.8.2 asks for in as many words.
func (a *analyzer) quantifiedExpr(x *xpath.QuantifiedExpr) props {
	if len(x.Bindings) == 0 {
		return a.unknown()
	}
	test := x.Test
	if len(x.Bindings) > 1 {
		test = &xpath.QuantifiedExpr{
			Every: x.Every, Bindings: x.Bindings[1:], Test: x.Test,
		}
	}
	// §19.8.8.2 makes S a navigation operand, and navigation from a
	// non-grounded posture is free-ranging (§19.8.1). Transcribed literally
	// that refuses "some $t in transaction satisfies xs:decimal($t/@value)
	// lt 0", whose binding is a plain striding child step -- streamable-100,
	// -101 and -102, which the suite marks
	// "_WRONG:streamability-rules-incorrect" and expects to RUN. The rule as
	// written also gains streamable-129, whose binding really is navigated
	// from: "$t/preceding-sibling::*[1]/@value".
	//
	// The navigation usage cannot tell the two apart, because it is charged
	// to the BINDING, and both bindings are the same plain striding child
	// step. What differs is the USE, and §19.8.8.1's own note names the
	// missing ingredient: "this requires data flow analysis (tracing from
	// the binding of a variable to its usages), rather than purely syntactic
	// analysis". So the tracing is done -- the range variable is recorded in
	// the environment with the binding sequence's posture -- and the
	// ordinary rules then separate the two shapes without a special case.
	// "$t/@value" is an attribute step from a striding posture, striding and
	// motionless (§19.8.8.8); "$t/preceding-sibling::*" is a reordering axis
	// from a striding posture, which the same table makes roaming.
	//
	// With the use assessed honestly, the binding no longer needs the
	// navigation usage that stood in for it: navigation is §19.4's answer
	// for "the analysis cannot tell what is done with the node", and here it
	// can. The binding is TRANSMITTED into the range variable, and whatever
	// the body does with it is charged to the body.
	//
	// Transmission is not the whole truth either -- the nodes are handed to
	// the range variable, not returned by the quantified expression, whose
	// result is an xs:boolean. So the operand carries the transmission usage
	// through §19.8.1's arithmetic, which is what keeps the limit of one
	// potentially-consuming operand honest, and the RESULT is forced
	// grounded below: no streamed node can leave a "some" or an "every".
	seq := a.operandOf(x.Bindings[0].Seq, usageTransmission)
	if !seq.props.streamable() {
		return roamingFreeRanging
	}
	// A grounded binding leaves the environment alone: §19.8.8.11's plain
	// answer is already right for it, and recording it would only add an
	// entry that says the same thing.
	inner := a
	if seq.props.posture != postureGrounded {
		inner = a.withVar(x.Bindings[0].Var, seq.props)
	}
	op := inner.higherOrderOperand(test, usageInspection)
	a.known = a.known && inner.known
	p := combine([]operand{seq, op}, false)
	if !p.streamable() {
		return roamingFreeRanging
	}
	// The value of "some"/"every" is an xs:boolean, so the construct is
	// grounded however the binding was reached; only the sweep survives.
	return props{postureGrounded, p.sweep}
}

// withVar returns a copy of the analyzer with one more binding in the
// data-flow environment.
//
// The map is copied rather than mutated so that a binding cannot outlive the
// expression that introduced it: "some $t in a satisfies P" and a sibling
// "some $t in b satisfies Q" must not see each other's $t, and neither must
// anything after them.
func (a *analyzer) withVar(name xdm.QName, p props) *analyzer {
	b := *a
	b.vars = make(map[xdm.QName]props, len(a.vars)+1)
	for k, v := range a.vars {
		b.vars[k] = v
	}
	b.vars[name] = p
	return &b
}
