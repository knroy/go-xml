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
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"strings"
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
	if len(x.Bindings) == 0 {
		return a.unknown()
	}
	seq := a.operandOf(x.Bindings[0].Seq, usageNavigation)
	ret := x.Return
	if len(x.Bindings) > 1 {
		ret = &xpath.LetExpr{Bindings: x.Bindings[1:], Return: x.Return}
	}
	return combine([]operand{
		seq,
		a.operandOf(ret, usageTransmission),
	}, false)
}

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
	// A grounded binding leaves the environment alone: §19.8.8.12's plain
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
func (a *analyzer) inlineFunction(x *xpath.InlineFunctionExpr) props {
	if !a.hasStreamParam {
		// With no streaming parameter in scope there is nothing the body
		// could refer to, so the second sentence applies directly.
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
	switch x := e.(type) {
	case nil:
		return mentionNo

	case *xpath.VarRef:
		if a.isStreamingParamRef(x) {
			return mentionYes
		}
		return mentionNo

	// A literal is not a reference, and "." is not a VARIABLE reference --
	// §19.8.8.16 asks only about the latter. An inline function body does not
	// inherit the caller's focus, so "." here cannot be the streamed node.
	case *xpath.Literal, *xpath.ContextItem:
		return mentionNo

	// A name, not a reference to a variable.
	case *xpath.NamedFunctionRef:
		return mentionNo

	// Nested, and still within this declaration's text -- which is the point.
	// Stopping here would let through exactly the closure the section's note
	// says the rule exists to prevent.
	case *xpath.InlineFunctionExpr:
		return a.bodyMentionsStreamingParam(x.Body)

	case *xpath.LetExpr:
		return a.mentionAny(append(bindingExprs(x.Bindings), x.Return))
	case *xpath.ForExpr:
		return a.mentionAny(append(bindingExprs(x.Bindings), x.Return))
	case *xpath.QuantifiedExpr:
		return a.mentionAny(append(bindingExprs(x.Bindings), x.Test))

	case *xpath.FuncCall:
		return a.mentionAny(x.Args)
	case *xpath.DynamicCall:
		return a.mentionAny(append([]xpath.Expr{x.Target}, x.Args...))

	case *xpath.BinaryOp:
		return a.mentionAny([]xpath.Expr{x.Left, x.Right})
	case *xpath.UnaryOp:
		return a.mentionAny([]xpath.Expr{x.Operand})
	case *xpath.SimpleMap:
		return a.mentionAny([]xpath.Expr{x.Left, x.Right})
	case *xpath.IfExpr:
		return a.mentionAny([]xpath.Expr{x.Cond, x.Then, x.Else})

	case *xpath.InstanceOfExpr:
		return a.bodyMentionsStreamingParam(x.Operand)
	case *xpath.CastExpr:
		return a.bodyMentionsStreamingParam(x.Operand)
	case *xpath.TreatExpr:
		return a.bodyMentionsStreamingParam(x.Operand)

	case *xpath.PathExpr:
		return a.mentionAny(x.Steps)
	case *xpath.Step:
		return a.mentionAny(x.Predicates)
	case *xpath.FilterExpr:
		return a.mentionAny(append([]xpath.Expr{x.Base}, x.Predicates...))
	case *xpath.SequenceExpr:
		return a.mentionAny(x.Items)

	case *xpath.MapConstructor:
		return a.mentionAny(append(append([]xpath.Expr{}, x.Keys...), x.Values...))
	case *xpath.ArrayConstructor:
		return a.mentionAny(x.Members)
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

// mentionAny is bodyMentionsStreamingParam over a list, with unknown winning
// over no: one unrecognised subexpression makes the whole answer unreliable,
// but a definite reference anywhere settles it regardless.
func (a *analyzer) mentionAny(es []xpath.Expr) mention {
	worst := mentionNo
	for _, e := range es {
		switch a.bodyMentionsStreamingParam(e) {
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
	return combine(ops, false)
}

// dynamicCallUsages reads the argument usages §19.8.8.11 derives from the
// declared type of the function variable, or returns nil when no signature
// is available. A nil result, or one shorter than the argument list, leaves
// the remaining arguments with usage navigation.
func (a *analyzer) dynamicCallUsages(x *xpath.DynamicCall) []usage {
	v, ok := x.Target.(*xpath.VarRef)
	if !ok || a.decl == nil {
		return nil
	}
	if _, bound := a.vars[v.Name]; bound {
		// Bound inside the expression, where the declaration walk cannot
		// see it; whatever shares its name outside is not this variable.
		return nil
	}
	as, ok := declaredTypeOf(a.decl, v.Name)
	if !ok {
		return nil
	}
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
	case strings.HasPrefix(as, "function("):
		params, ok := functionParamTypes(as)
		if !ok {
			// function(*): no signature.
			return nil
		}
		us := make([]usage, len(params))
		for i, t := range params {
			us[i] = instrTypeDeterminedUsage(t)
		}
		return us
	}
	return nil
}

// functionParamTypes splits the parameter list of a declared function type,
// "function(A, B) as R", at its top-level commas. It reports false for
// function(*), which declares no signature.
func functionParamTypes(as string) ([]string, bool) {
	depth, start := 0, -1
	var params []string
	for i, c := range as {
		switch c {
		case '(':
			depth++
			if depth == 1 {
				start = i + 1
			}
		case ')':
			depth--
			if depth == 0 {
				params = append(params, as[start:i])
				if len(params) == 1 && strings.TrimSpace(params[0]) == "*" {
					return nil, false
				}
				return params, true
			}
		case ',':
			if depth == 1 {
				params = append(params, as[start:i])
				start = i + 1
			}
		}
	}
	return nil, false
}

// declaredTypeOf finds the "as" attribute of the declaration that binds the
// variable name at el, by the syntactic scoping rules of §9.7: a local
// xsl:variable or xsl:param is in scope for its following siblings and their
// descendants (which covers the params of an enclosing xsl:function or
// xsl:template); "$value" inside an xsl:accumulator-rule is the accumulator's
// own value, typed by its "as" (§18.2.2); and a top-level declaration is in
// scope everywhere. The innermost binding wins. A declaration without "as"
// is found but reports "".
func declaredTypeOf(el *xdm.Node, name xdm.QName) (string, bool) {
	for n := el; n != nil && n.Parent != nil && n.Parent.Parent != nil; n = n.Parent {
		if isXSL(n, "accumulator-rule") && name.URI == "" && name.Local == "value" {
			return n.Parent.AttrValue("as"), true
		}
		sibs := n.Parent.ChildElements()
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
		if n.Parent.Parent.Parent == nil {
			// n's parent is the stylesheet element: every top-level
			// declaration is in scope, following ones included.
			for _, d := range n.Parent.ChildElements() {
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
func numericFocusFreePredicate(p xpath.Expr) bool {
	return staticallyNumeric(p) && focusFree(p)
}

// staticallyNumeric reports whether the static type of e is certainly numeric.
//
// What it decides: numeric literals; unary and binary arithmetic on numeric
// operands; "to", whose result is xs:integer* whatever its operands; the
// built-in functions whose result type is numeric at every arity; and a
// filter expression on any of those, which keeps its base's item type. A
// variable reference is never decided, because the analysis carries no
// variable types -- so the specification's own "descendant::section[$i+1]"
// is left to the ordinary predicate rule, at the price of precision only.
func staticallyNumeric(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal:
		return x.Val != nil && x.Val.Type.IsNumeric()
	case *xpath.UnaryOp:
		return staticallyNumeric(x.Operand)
	case *xpath.BinaryOp:
		switch x.Op {
		case "to":
			return true
		case "+", "-", "*", "div", "idiv", "mod":
			return staticallyNumeric(x.Left) && staticallyNumeric(x.Right)
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
		return staticallyNumeric(x.Base)
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
// for the arity written. Only the fn namespace is classified; a call on
// anything else -- an xsl:function, an extension, a constructor -- is treated
// as focus-dependent, because nothing here records that it is not.
func focusDependentCall(x *xpath.FuncCall) bool {
	if x.Name.URI != fnNS {
		return true
	}
	switch x.Name.Local {
	case "position", "last":
		return true
	case "name", "local-name", "namespace-uri", "string", "data", "number",
		"string-length", "normalize-space", "root", "base-uri",
		"document-uri", "path", "has-children", "generate-id", "node-name",
		"nilled":
		return len(x.Args) == 0
	case "lang", "id", "idref", "element-with-id":
		return len(x.Args) == 1
	}
	return false
}
