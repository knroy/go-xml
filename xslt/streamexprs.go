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
//	§19.8.8.9  the numeric-predicate rule       numericFocusFreePredicate
//	§19.8.8.10 the numeric-predicate rule       (the same two conditions)
//	§19.8.8.17 map constructors              mapConstructor
//	           square/curly array constructors, on the same shape as a map
//	§19.8.9.14 fn:last                       lastFunction
//	§19.8.9.16 fn:position                   (grounded and motionless)

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
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
		if l.posture == postureStriding && r.posture == postureStriding && a.disjointDepthPaths(left, right) { // streamexprs_l7.go
			return props{postureStriding, wider(l.sweep, r.sweep)}
		}
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
		entry := a.combine([]operand{
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
		p := a.combine([]operand{a.operandOf(m, usageNavigation)}, false)
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
// document-node()" motionless in the worked example of §19.8.8.8.
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
		return a.combine([]operand{a.operandOf(x.Operand, usageTransmission)}, false)
	}
	return a.combine([]operand{
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
// distinction the spec's own worked example in §19.8.8.8 relies on, where
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
// letExpr applies the §19.8.8 operand-role table to a let expression.
//
// The proforma is given directly rather than by reference to a subsection:
//
//	LetExpr [11,11]  let $var := N return T
//	                 Binding of variables to streamed nodes is not allowed.
//
// So the binding sequence has usage NAVIGATION and the return has usage
// TRANSMISSION, and the general rules of §19.8.1 combine them. The note is the
// rule's whole purpose: §19.8.1 gives navigation an adjusted sweep of
// free-ranging for every non-grounded posture, so a binding that touches the
// stream makes the let roaming before anything else is consulted. That is what
// "binding of variables to streamed nodes is not allowed" means operationally.
//
// The return is an ORDINARY operand, not a higher-order one. That is the whole
// difference from forExpr, and it is normative rather than a judgement call:
// §19.8.8.1 says of the for expression's return "This is a higher-order
// operand with usage transmission", because the body is evaluated once per
// item; §19.8.8's table gives let a bare T, because the binding is evaluated
// once. Making it higher-order here would refuse a consuming let body that the
// spec permits.
//
// A multi-clause "let $a := X, $b := Y return R" is nested the same way
// forExpr nests its clauses: the outer binding's return is the rest of the
// let. Only the first binding is in scope for the second, which is what the
// nesting expresses.
func (a *analyzer) letExpr(x *xpath.LetExpr) props {
	n := len(x.Bindings)
	if n == 0 {
		return a.unknown()
	}
	// The clauses are walked in a loop, not by recursing on the rest of the
	// let: recursion made the Go stack depth the binding count, and a stack
	// overflow is fatal to the process. The props are then folded from the
	// innermost clause outward, which is the nesting described above.
	seqs := make([]operand, n)
	saved := make([]savedVar, n)
	for i, b := range x.Bindings {
		seqs[i] = a.operandOf(b.Seq, usageNavigation)
		// The variable is recorded as grounded, which is what §19.8.8.12
		// gives it anyway, so that a declared variable it shadows is not
		// consulted.
		saved[i] = a.bind(b.Var, groundedMotionless)
	}
	ret := a.operandOf(x.Return, usageTransmission)
	a.vars.unbind(saved)
	for i := n - 1; i > 0; i-- {
		ret = operand{
			props:          combine([]operand{seqs[i], ret}, false),
			usage:          usageTransmission,
			allowsChildren: true,
			src:            &xpath.LetExpr{Bindings: x.Bindings[i:], Return: x.Return},
		}
	}
	return a.combine([]operand{seqs[0], ret}, false)
}

// forExpr applies §19.8.8.1. A multi-clause for is the nesting letExpr
// describes, walked the same way: in a loop, so the Go stack does not grow
// with the clause count. Every clause after the first, and the return, sits
// in a higher-order operand, so they are assessed with the one analyzer
// higherOrderAnalyzer gives.
func (a *analyzer) forExpr(x *xpath.ForExpr) props {
	n := len(x.Bindings)
	if n == 0 {
		return a.unknown()
	}
	seqs := make([]operand, n)
	saved := make([]savedVar, n)
	seqs[0] = a.operandOf(x.Bindings[0].Seq, usageNavigation)
	// Recorded as grounded for the reason letExpr gives.
	saved[0] = a.bind(x.Bindings[0].Var, groundedMotionless)
	h := a.higherOrderAnalyzer()
	for i := 1; i < n; i++ {
		seqs[i] = h.operandOf(x.Bindings[i].Seq, usageNavigation)
		saved[i] = h.bind(x.Bindings[i].Var, groundedMotionless)
	}
	ret := h.higherOrderOperand(x.Return, usageTransmission)
	a.vars.unbind(saved)
	for i := n - 1; i > 0; i-- {
		ret = operand{
			props:          combine([]operand{seqs[i], ret}, false),
			usage:          usageTransmission,
			allowsChildren: true,
			higherOrder:    true,
			src:            &xpath.ForExpr{Bindings: x.Bindings[i:], Return: x.Return},
		}
	}
	a.known = a.known && h.known
	a.unverified = a.unverified || h.unverified
	return a.combine([]operand{seqs[0], ret}, false)
}

func (a *analyzer) quantifiedExpr(x *xpath.QuantifiedExpr) props {
	if len(x.Bindings) == 0 {
		return a.unknown()
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
	//
	// The clauses are walked in a loop, as letExpr explains, and folded from
	// the innermost outward. A clause whose binding is not streamable makes
	// its level roaming and free-ranging, and the clauses inside it are not
	// assessed, exactly as the nested rewrite returned there.
	n := len(x.Bindings)
	seqs := make([]operand, 0, n)
	saved := make([]savedVar, 0, n)
	b, h := a, (*analyzer)(nil)
	for _, bd := range x.Bindings {
		seq := b.operandOf(bd.Seq, usageTransmission)
		if !seq.props.streamable() {
			break
		}
		seqs = append(seqs, seq)
		// A grounded binding is recorded too: its entry gives §19.8.8.12's
		// plain answer, and it is what tells staticallyNumeric and
		// dynamicCallUsages that the name no longer refers to a declared
		// xsl:variable or xsl:param.
		saved = append(saved, b.bind(bd.Var, seq.props))
		if h == nil {
			h = a.higherOrderAnalyzer()
			b = h
		}
	}
	if len(seqs) == 0 {
		return roamingFreeRanging
	}
	var op operand
	if len(seqs) == n {
		op = h.higherOrderOperand(x.Test, usageInspection)
	} else {
		op = operand{props: roamingFreeRanging, usage: usageInspection, allowsChildren: true, higherOrder: true,
			src: &xpath.QuantifiedExpr{Every: x.Every, Bindings: x.Bindings[len(seqs):], Test: x.Test}}
	}
	a.vars.unbind(saved)
	for i := len(seqs) - 1; i > 0; i-- {
		op = operand{
			props:          quantifiedProps(combine([]operand{seqs[i], op}, false)),
			usage:          usageInspection,
			allowsChildren: true,
			higherOrder:    true,
			src:            &xpath.QuantifiedExpr{Every: x.Every, Bindings: x.Bindings[i:], Test: x.Test},
		}
	}
	a.known = a.known && h.known
	a.unverified = a.unverified || h.unverified
	return quantifiedProps(a.combine([]operand{seqs[0], op}, false))
}

// quantifiedProps is the result of one clause of a quantified expression whose
// operands combine to p.
func quantifiedProps(p props) props {
	if !p.streamable() {
		return roamingFreeRanging
	}
	// The value of "some"/"every" is an xs:boolean, so the construct is
	// grounded however the binding was reached; only the sweep survives.
	return props{postureGrounded, p.sweep}
}

// varScope is the data-flow environment of one walk (analyzer.vars), shared
// by every analyzer of it. A binding is added where its scope opens and
// removed, in reverse, where it closes, so that it cannot outlive the
// expression that introduced it: "some $t in a satisfies P" and a sibling
// "some $t in b satisfies Q" must not see each other's $t, and neither must
// anything after them. It replaces a copy of the whole map per binding, which
// made k bindings quadratic in live memory.
type varScope struct {
	m map[xdm.QName]props
	// streamed counts the entries whose posture is not grounded, the only
	// ones inlineFunction has to look at; byName counts them by expanded
	// name (prefix dropped), so that inlineFunction asks of each reference
	// in the body whether it names one, in one walk of the body whatever the
	// number of bindings in scope.
	streamed int
	byName   map[xdm.QName]int
}

// note adjusts the streamed counts for name bound to p, by d.
func (s *varScope) note(name xdm.QName, p props, d int) {
	if p.posture == postureGrounded {
		return
	}
	s.streamed += d
	k := xdm.QName{URI: name.URI, Local: name.Local}
	if s.byName[k] += d; s.byName[k] == 0 {
		delete(s.byName, k)
	}
}

// streamedRef reports whether v refers, by expanded name, to a binding in
// scope whose posture is not grounded.
func (s *varScope) streamedRef(v *xpath.VarRef) bool {
	return s.byName[xdm.QName{URI: v.Name.URI, Local: v.Name.Local}] > 0
}

// savedVar is what bind displaced, for unbind to put back.
type savedVar struct {
	name xdm.QName
	old  props
	had  bool
}

// bind records name with props p in the environment and returns what it
// displaced.
func (a *analyzer) bind(name xdm.QName, p props) savedVar {
	if a.vars == nil {
		a.vars = &varScope{m: map[xdm.QName]props{}, byName: map[xdm.QName]int{}}
	}
	s := a.vars
	old, had := s.m[name]
	if had {
		s.note(name, old, -1)
	}
	s.note(name, p, 1)
	s.m[name] = p
	return savedVar{name, old, had}
}

// unbind undoes the binds that returned saved, most recent first.
func (s *varScope) unbind(saved []savedVar) {
	for i := len(saved) - 1; i >= 0; i-- {
		v := saved[i]
		s.note(v.name, s.m[v.name], -1)
		if !v.had {
			delete(s.m, v.name)
			continue
		}
		s.m[v.name] = v.old
		s.note(v.name, v.old, 1)
	}
}

// lookup returns the props name is bound to within the expression being
// analysed, and whether it is bound there.
func (s *varScope) lookup(name xdm.QName) (props, bool) {
	if s == nil {
		return props{}, false
	}
	p, ok := s.m[name]
	return p, ok
}

// bound reports whether name is bound within the expression being analysed.
func (s *varScope) bound(name xdm.QName) bool {
	_, ok := s.lookup(name)
	return ok
}

// inlineFunction applies §19.8.8.16 to an inline function declaration:
//
//	"An inline function declaration that textually contains a variable
//	reference bound to a streaming parameter (of some containing stylesheet
//	function) is roaming and free-ranging. All other inline function
//	declarations are grounded and motionless."
//
// Note the parenthetical. The streaming parameter is the one belonging to the
// enclosing xsl:function -- a.streamingParam -- not a parameter of the inline
// function itself, which cannot be streaming. An inline function's own
// parameters are ordinary bindings, which is why no new variable environment
// is needed here: the spec asks one textual question, and this answers it.
//
// The rule is TEXTUAL, so a reference inside a nested inline function still
// counts: it is still within the outer declaration's text, and the note
// explains why that matters -- "the only other way an inline function could
// access a streamed node is by having the streamed node in its closure, and
// this is prevented by the rule above". A walker that stopped at a nested
// function boundary would let exactly that closure through.
//
// Two closures the section does not foresee are unknown rather than grounded.
// A range variable that this analysis's own data flow (quantifiedExpr) bound
// to streamed nodes is not a streaming parameter, but closing over it carries
// the streamed node out exactly as the note fears. And fn:current() in the
// body denotes the item that was current where the function was created --
// a streamed node in a streamable construct -- which no variable reference
// betrays. The spec read literally grounds both; the verdict is withheld
// rather than refused, because the first rests on data flow the spec does
// not have and the second on a gap in its rules.
func (a *analyzer) inlineFunction(x *xpath.InlineFunctionExpr) props {
	// Only a binding that is not grounded can be closed over; the count
	// spares the scan when every binding in scope is grounded. One walk of
	// the body asks of each reference whether it names such a binding, so
	// the cost is the body's, not the body's times the bindings in scope.
	if a.vars != nil && a.vars.streamed > 0 && refMention(x.Body, a.vars.streamedRef) != mentionNo {
		return a.unknown()
	}
	if !a.hasStreamParam {
		// With no streaming parameter in scope there is nothing the body
		// could refer to, so the second sentence applies directly -- once
		// the walk has found no call on fn:current().
		if a.bodyMentionsStreamingParam(x.Body) == mentionUnknown {
			return a.unknown()
		}
		return groundedMotionless
	}
	switch a.bodyMentionsStreamingParam(x.Body) {
	case mentionYes:
		return roamingFreeRanging
	case mentionNo:
		return groundedMotionless
	default:
		// An expression kind the walker does not know. Answering "no
		// reference" would be a false negative -- a stylesheet accepted whose
		// inline function does close over a streamed node -- so the analysis
		// reports no opinion instead and the caller declines to judge.
		return a.unknown()
	}
}

// mention is the three-valued answer bodyMentionsStreamingParam gives.
//
// Two values would not do. The walker has to enumerate expression kinds, and
// the analyzer's own dispatch has 43 of them; a walker that answered "no" for
// a kind it had not been taught would silently ground an inline function that
// captures a streamed node. The third value routes that case to unknown(),
// where the checker reports nothing rather than something wrong.
type mention int

const (
	mentionNo mention = iota
	mentionYes
	mentionUnknown
)

// bodyMentionsStreamingParam reports whether e textually contains a reference
// to the streaming parameter of the containing stylesheet function.
//
// It is a plain textual walk, not an analysis: §19.8.8.16 asks only whether
// the reference is present, not what is done with it, so posture and sweep
// play no part and no context is threaded.
func (a *analyzer) bodyMentionsStreamingParam(e xpath.Expr) mention {
	return refMention(e, func(v *xpath.VarRef) bool { return a.isStreamingParamRef(v) })
}

// refMention is bodyMentionsStreamingParam with the question asked of each
// variable reference given as ref.
func refMention(e xpath.Expr, ref func(*xpath.VarRef) bool) mention {
	switch x := e.(type) {
	case nil:
		return mentionNo

	case *xpath.VarRef:
		if ref(x) {
			return mentionYes
		}
		return mentionNo

	// A literal is not a reference, and "." is not a VARIABLE reference --
	// §19.8.8.16 asks only about the latter. An inline function body does not
	// inherit the caller's focus, so "." here cannot be the streamed node.
	case *xpath.Literal, *xpath.ContextItem:
		return mentionNo

	// A partial application's "?" is not a reference either.
	case *xpath.ArgumentPlaceholder:
		return mentionNo
	case *xpath.StringConcat:
		return mentionAny([]xpath.Expr{x.Left, x.Right}, ref)
	// A unary lookup's operand is the context item, as for ".".
	case *xpath.LookupExpr:
		return mentionAny([]xpath.Expr{x.Base, x.Expr}, ref)

	// A name, not a reference to a variable -- except fn:current#0, which
	// reaches the creating context's current item as a call does.
	case *xpath.NamedFunctionRef:
		if x.Name.URI == fnNS && x.Name.Local == "current" {
			return mentionUnknown
		}
		return mentionNo

	// Nested, and still within this declaration's text -- which is the point.
	// Stopping here would let through exactly the closure the section's note
	// says the rule exists to prevent.
	case *xpath.InlineFunctionExpr:
		return refMention(x.Body, ref)

	case *xpath.LetExpr:
		return mentionAny(append(bindingExprs(x.Bindings), x.Return), ref)
	case *xpath.ForExpr:
		return mentionAny(append(bindingExprs(x.Bindings), x.Return), ref)
	case *xpath.QuantifiedExpr:
		return mentionAny(append(bindingExprs(x.Bindings), x.Test), ref)

	case *xpath.FuncCall:
		// See inlineFunction: no variable reference, and still a streamed
		// node.
		if x.Name.URI == fnNS && x.Name.Local == "current" {
			return mentionUnknown
		}
		return mentionAny(x.Args, ref)
	case *xpath.DynamicCall:
		return mentionAny(append([]xpath.Expr{x.Target}, x.Args...), ref)

	case *xpath.BinaryOp:
		return mentionAny([]xpath.Expr{x.Left, x.Right}, ref)
	case *xpath.UnaryOp:
		return mentionAny([]xpath.Expr{x.Operand}, ref)
	case *xpath.SimpleMap:
		return mentionAny([]xpath.Expr{x.Left, x.Right}, ref)
	case *xpath.IfExpr:
		return mentionAny([]xpath.Expr{x.Cond, x.Then, x.Else}, ref)

	case *xpath.InstanceOfExpr:
		return refMention(x.Operand, ref)
	case *xpath.CastExpr:
		return refMention(x.Operand, ref)
	case *xpath.TreatExpr:
		return refMention(x.Operand, ref)

	case *xpath.PathExpr:
		return mentionAny(x.Steps, ref)
	case *xpath.Step:
		return mentionAny(x.Predicates, ref)
	case *xpath.FilterExpr:
		return mentionAny(append([]xpath.Expr{x.Base}, x.Predicates...), ref)
	case *xpath.SequenceExpr:
		return mentionAny(x.Items, ref)

	case *xpath.MapConstructor:
		return mentionAny(append(append([]xpath.Expr{}, x.Keys...), x.Values...), ref)
	case *xpath.ArrayConstructor:
		return mentionAny(x.Members, ref)
	}
	return mentionUnknown
}

// bindingExprs is the sequence expressions of a binding list, which for this
// walk is all that matters: the bound NAMES are the construct's own, and a
// reference to one of them is not a reference to the streaming parameter.
func bindingExprs(bs []xpath.Binding) []xpath.Expr {
	es := make([]xpath.Expr, 0, len(bs)+1)
	for _, b := range bs {
		es = append(es, b.Seq)
	}
	return es
}

// mentionAny is refMention over a list, with unknown winning
// over no: one unrecognised subexpression makes the whole answer unreliable,
// but a definite reference anywhere settles it regardless.
func mentionAny(es []xpath.Expr, ref func(*xpath.VarRef) bool) mention {
	worst := mentionNo
	for _, e := range es {
		switch refMention(e, ref) {
		case mentionYes:
			return mentionYes
		case mentionUnknown:
			worst = mentionUnknown
		}
	}
	return worst
}

// namedFunctionRef applies §19.8.8.15 to a named function reference:
//
//	"Let F be the function to which the NamedFunctionRef refers. If F is
//	focus-dependent and the context posture is not grounded, then the
//	NamedFunctionRef is roaming and free-ranging. If F is an extension
//	function, the posture and sweep are implementation-defined. Otherwise,
//	the NamedFunctionRef is grounded and motionless."
//
// F is resolved from the reference's expanded name and arity, and whether it
// is focus-dependent is read from focusDependent (streamfocus.go), the table
// derived from the F&O 3.1 and XSLT 3.0 "Properties" paragraphs:
//
//   - a built-in function (fn:, xs:, map:, array:, math:) in the table, with
//     a context posture that is not grounded: roaming and free-ranging.
//   - any other built-in, or any built-in under a grounded context posture:
//     grounded and motionless. The table classifies every specified
//     function, so absence from it is a focus-independent answer, not a
//     missing one.
//   - a stylesheet function: grounded and motionless. §5.3.3.1: "When a
//     stylesheet function is called, the focus within the body of the
//     function is initially absent", so no such function is focus-dependent.
//   - an extension function, which is any other name: no opinion. The
//     posture and sweep are implementation-defined, and the note to the
//     section leaves it to the implementation to know whether the function
//     depends on the focus; this one does not, so it says nothing.
func (a *analyzer) namedFunctionRef(x *xpath.NamedFunctionRef) props {
	key := keyOf(x.Name, x.Arity)
	switch {
	case builtinFunctionNS(x.Name.URI):
		if focusDependent[key] && a.ctxPosture != postureGrounded {
			return roamingFreeRanging
		}
		return groundedMotionless
	case a.funcs[key] != nil:
		return groundedMotionless
	}
	return a.unknown()
}

// dynamicCall applies §19.8.8.11 to a call on a function item, "$f(X, Y)" or
// "X => $f(Y)":
//
//	"The posture and sweep of a dynamic function call such as $F(X, Y) are
//	determined by the 19.8.1 General Rules for Streamability. The operands
//	and their usages are as follows: The base expression that computes the
//	function value itself (here $F). This has usage inspection. The
//	argument expressions excluding any ? placeholders (here X and Y). These
//	have type-determined usage dependent on ancillary information
//	associated with the static type of the base expression, where available
//	[...]. If this information indicates that the base expression is a
//	function with signature function(A, B, ...) as R, then the first
//	argument X has type-determined usage based on the first argument type
//	A, the second argument Y has type-determined usage based on the second
//	argument type B, and so on. If no function signature is available, then
//	the usage of each of the argument expressions is navigation."
//
// The one static type this analyzer can read is the "as" of the
// xsl:variable or xsl:param that binds a function variable, found by the
// syntactic scoping walk in declaredTypeOf. The section's note settles the
// map and array types: "it is also known that the argument type is
// xs:anyAtomicType, and that the operand usage is therefore absorption. A
// call that passes a streamed node will therefore be grounded and
// consuming." A declared function(A, B) gives each argument the
// type-determined usage of its parameter type. Anything else -- no
// declaration, no "as", function(*), a base that is not a variable -- is
// the last sentence, and a streamed argument then makes the call roaming,
// which the note calls the expected outcome: "it is desirable to declare
// the type of any variable holding a map or array."
//
// A grounded argument is unaffected either way, because §19.8.1 keeps a
// grounded operand's own sweep whatever its usage.
//
// The note on focus-dependent function items ("name#0, lang#1, or last#0
// [...] does not affect the static streamability analysis") is honoured by
// giving the base expression nothing beyond its inspection usage; what
// §19.8.8.15 makes of the reference itself is its own affair.
func (a *analyzer) dynamicCall(x *xpath.DynamicCall) props {
	usages := a.dynamicCallUsages(x)
	ops := []operand{a.operandOf(x.Target, usageInspection)}
	for i, arg := range x.Args {
		if _, ok := arg.(*xpath.ArgumentPlaceholder); ok {
			// "excluding any ? placeholders"
			continue
		}
		u := usageNavigation
		if i < len(usages) {
			u = usages[i]
		}
		ops = append(ops, a.operandOf(arg, u))
	}
	return a.combine(ops, false)
}

// dynamicCallUsages reads the argument usages §19.8.8.11 derives from the
// signature of the base expression, or returns nil when no signature is
// available. A nil result, or one shorter than the argument list, leaves the
// remaining arguments with usage navigation.
//
// Where a signature is available is §19.1's to say, and it says it in full:
//
//	"This ancillary information is available for a NamedFunctionRef, for an
//	InlineFunctionExpr, for a MapConstructor, for a FunctionCall whose
//	static type is U{function(*)}, and for a VarRef if the variable is bound
//	to any of the forgoing, or if it has a declared type corresponding to
//	U{function(*)}."
//
// So a base that is none of those -- a lookup "$m?f", a conditional "if (C)
// then f#1 else g#1", a dynamic call "$g()" -- has no signature, whatever
// its own static type, and its arguments navigate. Of the VarRef forms only
// the declared type is read; a variable without "as" that is bound to a
// function reference is left to the fallback, which refuses more than the
// rule does but never accepts what it refuses.
func (a *analyzer) dynamicCallUsages(x *xpath.DynamicCall) []usage {
	switch t := x.Target.(type) {
	case *xpath.NamedFunctionRef:
		// The signature is the function's declaration: a stylesheet
		// function's xsl:param types (an override must keep them),
		// or the F&O signature a built-in carries.
		if f := a.funcs[keyOf(t.Name, t.Arity)]; f != nil && !builtinFunctionNS(t.Name.URI) {
			return paramUsages(f.params)
		}
		if fn, ok := xpath.Builtins().Lookup(t.Name, t.Arity); ok && builtinFunctionNS(t.Name.URI) &&
			len(fn.Signature) == t.Arity+1 {
			return paramUsages(fn.Signature[1:])
		}
		return nil
	case *xpath.InlineFunctionExpr:
		// "function($a as A, $b) {...}": an omitted type is item()*.
		ts := make([]string, len(t.Params))
		for i, p := range t.Params {
			if p.Type != nil {
				ts[i] = p.Type.String()
			}
		}
		return paramUsages(ts)
	case *xpath.MapConstructor:
		// The note: a map's argument type is xs:anyAtomicType.
		return []usage{usageAbsorption}
	case *xpath.FuncCall:
		// A call whose declared result type is a function type: the
		// signature is that type's. Only a stylesheet function's "as" is
		// read here; a built-in result goes to the fallback.
		f := a.funcs[keyOf(t.Name, len(t.Args))]
		if f == nil || builtinFunctionNS(t.Name.URI) {
			return nil
		}
		return signatureUsages(f.asType, newNSResolver(f.body, ""))
	case *xpath.VarRef:
		return a.varSignatureUsages(t)
	}
	return nil
}

// paramUsages is the type-determined usage of each parameter type.
func paramUsages(types []string) []usage {
	us := make([]usage, len(types))
	for i, t := range types {
		us[i] = instrTypeDeterminedUsage(t)
	}
	return us
}

// varSignatureUsages reads the signature of a function variable from the
// "as" of the xsl:variable or xsl:param that binds it.
func (a *analyzer) varSignatureUsages(v *xpath.VarRef) []usage {
	if a.decl == nil {
		return nil
	}
	if a.vars.bound(v.Name) {
		// Bound inside the expression, where the declaration walk cannot
		// see it; whatever shares its name outside is not this variable.
		return nil
	}
	as, ok := declaredTypeOf(a.decl, v.Name)
	if !ok {
		return nil
	}
	return signatureUsages(as, newNSResolver(a.decl, ""))
}

// signatureUsages reads the argument usages from a declared function, map or
// array type, or returns nil for any other type and for function(*).
func signatureUsages(as string, ns *nsResolver) []usage {
	as = strings.TrimSpace(as)
	if n := len(as); n > 0 {
		switch as[n-1] {
		case '?', '*', '+':
			as = strings.TrimSpace(as[:n-1])
		}
	}
	switch {
	case strings.HasPrefix(as, "map(") || strings.HasPrefix(as, "array("):
		// The note: the argument type is xs:anyAtomicType, so absorption.
		return []usage{usageAbsorption}
	case strings.HasPrefix(as, "function"):
		params, ok := functionParamTypes(as, ns)
		if !ok {
			// function(*): no signature.
			return nil
		}
		return paramUsages(params)
	}
	return nil
}

// functionParamTypes reads the parameter types of a declared function type,
// "function(A, B) as R", with the XPath SequenceType parser, so a comma inside
// a braced URI and a space before the parenthesis are read as XPath reads
// them. It reports false for function(*), which declares no signature, and
// for anything that does not parse as a typed function test.
func functionParamTypes(as string, ns *nsResolver) ([]string, bool) {
	st, err := xpath.ParseSequenceType(as, anySchemaType{ns})
	if err != nil || !st.IsFunctionTest || !st.HasFunctionArity {
		return nil, false
	}
	params := make([]string, len(st.FunctionParams))
	for i, p := range st.FunctionParams {
		params[i] = p.String()
	}
	return params, true
}

// anySchemaType reads every type and declaration name as known. The analyzer
// has no imported schema to hand, and the compiler has already refused an
// unknown name; all functionParamTypes needs is the type's shape. A schema
// type it cannot see renders as its name, which instrTypeDeterminedUsage
// reads as navigation.
type anySchemaType struct{ *nsResolver }

func (anySchemaType) LookupSchemaType(xdm.QName) (xdm.TypeCode, bool, bool) { return 0, false, true }
func (anySchemaType) LookupSchemaDeclaration(xdm.QName, bool) bool          { return true }

// declaredTypeOf finds the "as" attribute of the declaration that binds the
// variable name at el, by the syntactic scoping rules of §9.7: a local
// xsl:variable or xsl:param is in scope for its following siblings and their
// descendants (which covers the params of an enclosing xsl:function or
// xsl:template); "$value" inside an xsl:accumulator-rule is the accumulator's
// own value, typed by its "as" (§18.2.2); and a top-level declaration is in
// scope everywhere. The innermost binding wins. A declaration without "as"
// is found but reports "".
func declaredTypeOf(el *xdm.Node, name xdm.QName) (string, bool) {
	for n := el; n != nil && n.Parent() != nil && n.Parent().Parent() != nil; n = n.Parent() {
		if isXSL(n, "accumulator-rule") && name.URI == "" && name.Local == "value" {
			return n.Parent().AttrValue("as"), true
		}
		sibs := n.Parent().ChildElements()
		for i := range sibs {
			if sibs[i] == n {
				sibs = sibs[:i]
				break
			}
		}
		for i := len(sibs) - 1; i >= 0; i-- {
			if declaresVar(sibs[i], name) {
				return sibs[i].AttrValue("as"), true
			}
		}
		if n.Parent().Parent().Parent() == nil {
			// n's parent is the stylesheet element: every top-level
			// declaration is in scope, following ones included.
			for _, d := range n.Parent().ChildElements() {
				if declaresVar(d, name) {
					return d.AttrValue("as"), true
				}
			}
		}
	}
	return "", false
}

// declaresVar reports whether d is an xsl:variable or xsl:param binding name.
func declaresVar(d *xdm.Node, name xdm.QName) bool {
	if !isXSL(d, "variable") && !isXSL(d, "param") {
		return false
	}
	q, err := resolveQNameAttr(d, d.AttrValue("name"))
	return err == nil && q == name
}

// numericFocusFreePredicate reports whether a predicate P satisfies the two
// conditions that the fourth rule of §19.8.8.9 (axis steps) and the first rule
// of §19.8.8.10 (filter expressions) share:
//
//	"The static type of P is a subtype of U{xs:decimal, xs:double, xs:float}"
//	"Neither P, nor any operand of P, at any depth provided it has [the step
//	 or filter expression] as its focus-setting container, is a context item
//	 expression, an axis expression, or a call on a focus-dependent function"
//
// A predicate that passes selects at most one node, which is what lets the
// two rules narrow a crawling posture to striding. Each rule sits ahead of
// its section's motionless-predicate rule, and is applied there. Both halves
// are decided statically and conservatively: a false here costs the
// narrowing and nothing else, because the caller falls through to the
// ordinary predicate rule, whereas a wrong true would accept a stylesheet the
// specification makes roaming.
func (a *analyzer) numericFocusFreePredicate(p xpath.Expr) bool {
	return a.staticallyNumeric(p) && focusFree(p)
}

// staticallyNumeric reports whether the static type of e is certainly numeric.
//
// What it decides: numeric literals; unary and binary arithmetic on numeric
// operands; "to", whose result is xs:integer* whatever its operands; the
// built-in functions whose result type is numeric at every arity; and a
// filter expression on any of those, which keeps its base's item type; and a
// reference to an xsl:variable or xsl:param whose "as" names a numeric atomic
// type or xs:numeric, which admits the specification's own
// "descendant::section[$i+1]". The static type of such a reference is its
// declared type, and the mapping to U-types (§19.1) ignores the occurrence
// indicator, so xs:integer* qualifies as xs:integer does. A variable bound
// inside the expression, or declared without "as", is not decided.
func (a *analyzer) staticallyNumeric(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal:
		return x.Val != nil && x.Val.Type.IsNumeric()
	case *xpath.UnaryOp:
		return a.staticallyNumeric(x.Operand)
	case *xpath.BinaryOp:
		switch x.Op {
		case "to":
			return true
		case "+", "-", "*", "div", "idiv", "mod":
			return a.staticallyNumeric(x.Left) && a.staticallyNumeric(x.Right)
		}
		return false
	case *xpath.FuncCall:
		if x.Name.URI != fnNS {
			return false
		}
		switch x.Name.Local {
		case "position", "last", "count", "index-of", "string-length", "number":
			return true
		}
		return false
	case *xpath.FilterExpr:
		return a.staticallyNumeric(x.Base)
	case *xpath.VarRef:
		if a.vars.bound(x.Name) || a.decl == nil {
			return false
		}
		as, ok := declaredTypeOf(a.decl, x.Name)
		if !ok {
			return false
		}
		st, err := xpath.ParseSequenceType(as, nil)
		return err == nil && !st.Empty &&
			(st.IsNumericType || st.HasAtomicType && st.AtomicType.IsNumeric())
	}
	return false
}

// focusFree reports whether e contains no context item expression, axis
// expression, or call on a focus-dependent function among the operands that
// take their focus from the predicate's own container. An operand inside a
// nested focus-setting container -- a filter's predicates, the steps of a
// path after its first, the right-hand side of "!" -- is not walked, which
// is what admits §19.8.8.10's own example "(//x)[index-of($a, $b)[last()]]".
// An expression kind not listed is not decided, and so not admitted.
func focusFree(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal, *xpath.VarRef, *xpath.NamedFunctionRef:
		return true
	case *xpath.ContextItem, *xpath.Step:
		return false
	case *xpath.PathExpr:
		// A leading "/" is fn:root(.), a context item expression
		// (§19.8.8.8). Only the first operand has the outer focus.
		return !x.Root && len(x.Steps) > 0 && focusFree(x.Steps[0])
	case *xpath.FilterExpr:
		return focusFree(x.Base)
	case *xpath.SimpleMap:
		return focusFree(x.Left)
	case *xpath.FuncCall:
		if focusDependentCall(x) {
			return false
		}
		return allFocusFree(x.Args)
	case *xpath.UnaryOp:
		return focusFree(x.Operand)
	case *xpath.BinaryOp:
		return focusFree(x.Left) && focusFree(x.Right)
	case *xpath.IfExpr:
		return allFocusFree([]xpath.Expr{x.Cond, x.Then, x.Else})
	case *xpath.SequenceExpr:
		return allFocusFree(x.Items)
	case *xpath.InstanceOfExpr:
		return focusFree(x.Operand)
	case *xpath.CastExpr:
		return focusFree(x.Operand)
	case *xpath.TreatExpr:
		return focusFree(x.Operand)
	case *xpath.LetExpr:
		return allFocusFree(append(bindingExprs(x.Bindings), x.Return))
	case *xpath.ForExpr:
		return allFocusFree(append(bindingExprs(x.Bindings), x.Return))
	case *xpath.QuantifiedExpr:
		return allFocusFree(append(bindingExprs(x.Bindings), x.Test))
	}
	return false
}

func allFocusFree(es []xpath.Expr) bool {
	for _, e := range es {
		if !focusFree(e) {
			return false
		}
	}
	return true
}

// focusDependentCall reports whether a call is on a focus-dependent function,
// for the arity written, by the focusDependent table (streamfocus.go). Only
// the fn namespace is classified here, besides map: and array:, none of whose
// functions reads the focus (map:get is how mapCalls spells a variable's
// $m('k')); a call on anything else -- an xsl:function, an extension, a
// constructor -- is treated as focus-dependent, because nothing here records
// that it is not.
func focusDependentCall(x *xpath.FuncCall) bool {
	if x.Name.URI == xdm.NSMap || x.Name.URI == xdm.NSArray {
		return false
	}
	return x.Name.URI != fnNS || focusDependent[keyOf(x.Name, len(x.Args))]
}
