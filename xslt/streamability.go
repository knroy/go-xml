package xslt

// The per-construct streamability rules of XSLT 3.0 §19.8, built on the
// lattice in streamlattice.go.
//
// This file covers the XPath expression constructs of §19.8.8 and the
// built-in function classification of §19.8.9. It does not cover the XSLT
// instructions of §19.8.6; see the note on coverage in
// docs/conformance-gaps.md.
//
// The analysis is conservative in one direction only. Where a construct is
// not modelled, the answer is roaming and free-ranging, which says "not
// guaranteed-streamable". That is the safe direction for the *analysis* but
// the dangerous direction for the *caller*, because a spurious XTSE3430
// rejects a valid stylesheet. The caller in streamcheck.go therefore reports
// an error only for the constructs it recognises, and treats an unmodelled
// one as "no opinion" rather than as a failure.

import (
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// analyzer carries the context an expression is assessed in.
type analyzer struct {
	// ctxPosture is the context posture (§19.6): the posture of the item
	// that "." denotes here.
	ctxPosture posture

	// ctxAllowsChildren records whether the context item can be a node with
	// children. It decides whether absorbing "." reads anything from the
	// stream: inside a predicate on "@code" or "text()" the context item is
	// an attribute or a text node, whose whole subtree is already in hand,
	// so "@code[. = 'x']" is motionless rather than consuming (§19.8.1).
	ctxAllowsChildren bool

	// known is cleared when the analysis meets a construct it does not
	// model. The properties it returns in that case are roaming and
	// free-ranging, which is what the rules require, but the caller must
	// not turn that into an error, because the cause is a gap in this
	// implementation rather than a fact about the stylesheet.
	known bool

	// funcs indexes the stylesheet's own xsl:function declarations, so that
	// a call on one can be assessed under §19.8.5 instead of abandoning the
	// enclosing construct. Nil when no declarations were collected, which
	// leaves every stylesheet function unmodelled as before.
	funcs map[funcKey]*streamFunc

	// streamingParam names the streaming parameter (§19.8.5) of the function
	// whose body is being analysed, and paramCategory is that function's
	// streamability category. Together they drive the §19.8.8.11 rule that
	// gives a reference to the first parameter of a declared-streamable
	// function a posture other than grounded. Empty outside such a body.
	streamingParam xdm.QName
	paramCategory  streamCategory
	hasStreamParam bool

	// higherOrder records that the expression now being analysed sits inside
	// a higher-order operand of some construct between it and the function
	// body. §19.8.8.11 calls a variable reference "singular" when no such
	// construct intervenes, and gives the two answers different postures.
	higherOrder bool
}

// analyzeExpr returns the posture and sweep of e, and whether every construct
// within it was one the analysis models.
func analyzeExpr(e xpath.Expr, ctx posture) (props, bool) {
	return analyzeExprFuncs(e, ctx, nil)
}

// analyzeExprFuncs is analyzeExpr with the stylesheet's xsl:function
// declarations in hand, so that a call on one is assessed under §19.8.5
// (streamfunctions.go) rather than abandoning the enclosing construct.
func analyzeExprFuncs(e xpath.Expr, ctx posture, funcs map[funcKey]*streamFunc) (props, bool) {
	a := &analyzer{
		ctxPosture:        ctx,
		ctxAllowsChildren: true,
		known:             true,
		funcs:             funcs,
	}
	p := a.expr(e)
	return p, a.known
}

// unknown records that the analysis met a construct it does not model, and
// returns the pair that says "not guaranteed-streamable".
func (a *analyzer) unknown() props {
	a.known = false
	return roamingFreeRanging
}

// expr dispatches on the kind of expression.
func (a *analyzer) expr(e xpath.Expr) props {
	switch x := e.(type) {
	case nil:
		return groundedMotionless

	case *xpath.Literal:
		// §19.8.1, no operands.
		return groundedMotionless

	case *xpath.ContextItem:
		// §19.8.8.12: the posture of "." is the context posture, and its
		// sweep is motionless. What makes "string(.)" consuming is the
		// usage the containing construct gives the operand, not "." itself.
		return props{a.ctxPosture, sweepMotionless}

	case *xpath.VarRef:
		// §19.8.8.11: a variable reference is motionless, and grounded
		// except when bound to a streaming parameter of a
		// declared-streamable stylesheet function, where the posture comes
		// from the function's category and whether the reference is
		// singular.
		return a.varRef(x)

	case *xpath.Step:
		return a.step(x, a.ctxPosture)

	case *xpath.PathExpr:
		return a.path(x)

	case *xpath.SequenceExpr:
		// A sequence constructor transmits each of its items (§19.8.1).
		var ops []operand
		for _, it := range x.Items {
			ops = append(ops, a.operandOf(it, usageTransmission))
		}
		return combine(ops, false)

	case *xpath.IfExpr:
		// §19.8.8.3: the condition is inspected; the two arms transmit and
		// form a choice operand group.
		cond := a.operandOf(x.Cond, usageInspection)
		then := a.operandOf(x.Then, usageTransmission)
		els := a.operandOf(x.Else, usageTransmission)
		then.choiceGroup = true
		els.choiceGroup = true
		return combine([]operand{cond, then, els}, false)

	case *xpath.BinaryOp:
		return a.binary(x)

	case *xpath.UnaryOp:
		// Arithmetic negation atomizes its operand.
		return combine([]operand{a.operandOf(x.Operand, usageAbsorption)}, false)

	case *xpath.FilterExpr:
		return a.filter(x)

	case *xpath.InstanceOfExpr:
		// §19.8.8.5: "instance of" inspects its operand -- it looks at the
		// item's type, never at its subtree. The one exception is an item
		// type of the form document-node(element(X)), which cannot be
		// decided without reading the document's children, and so absorbs
		// (streamexprs.go).
		u := usageInspection
		if isDocumentNodeWithContent(x.Type) {
			u = usageAbsorption
		}
		return combine([]operand{a.operandOf(x.Operand, u)}, false)

	case *xpath.CastExpr, *xpath.TreatExpr:
		// A cast atomizes. "treat as" transmits, but modelling it as
		// absorption would be wrong rather than merely conservative, so
		// both are handled explicitly below.
		switch y := e.(type) {
		case *xpath.CastExpr:
			return combine([]operand{a.operandOf(y.Operand, usageAbsorption)}, false)
		case *xpath.TreatExpr:
			return a.treatExpr(y)
		}
		return a.unknown()

	case *xpath.FuncCall:
		return a.funcCall(x)

	case *xpath.SimpleMap:
		// §19.8.8.6: the posture and sweep of "a!b" are those of the right
		// operand, assessed with a context posture and type taken from the
		// left operand.
		return a.simpleMap(x)

	case *xpath.MapConstructor:
		// §19.8.8.16, in streamexprs.go.
		return a.mapConstructor(x)

	case *xpath.ArrayConstructor:
		// Not in the §19.8.8 table, which enumerates XPath 3.0 productions;
		// the general rules apply (streamexprs.go).
		return a.arrayConstructor(x)

	default:
		// for, some/every, let, inline functions, dynamic calls and named
		// function references. Each has its own section in §19.8.8 and none
		// is modelled yet.
		return a.unknown()
	}
}

// operandOf assesses a subexpression and packages it as an operand with the
// given usage.
func (a *analyzer) operandOf(e xpath.Expr, u usage) operand {
	p := a.expr(e)
	return operand{
		props:          p,
		usage:          u,
		allowsChildren: a.allowsChildren(e),
	}
}

// binary handles the operators whose operand usages the spec fixes.
func (a *analyzer) binary(x *xpath.BinaryOp) props {
	switch x.Op {
	case "+", "-", "*", "div", "idiv", "mod",
		"=", "!=", "<", "<=", ">", ">=",
		"eq", "ne", "lt", "le", "gt", "ge",
		"to":
		// Arithmetic and comparison atomize both operands. A general
		// comparison atomizes too: it compares the atomized values.
		return combine([]operand{
			a.operandOf(x.Left, usageAbsorption),
			a.operandOf(x.Right, usageAbsorption),
		}, false)

	case "and", "or":
		// The effective boolean value of each operand is taken, which for a
		// node sequence asks only whether it is empty (§19.4, inspection).
		//
		// "and" and "or" are not a choice operand group: the spec gives
		// that status only to constructs whose operands are mutually
		// exclusive, and both arms of "and" may be evaluated.
		return combine([]operand{
			a.operandOf(x.Left, usageInspection),
			a.operandOf(x.Right, usageInspection),
		}, false)

	case "is", "<<", ">>":
		// Node identity and order compare node identity, which is available
		// without reading the subtree.
		return combine([]operand{
			a.operandOf(x.Left, usageInspection),
			a.operandOf(x.Right, usageInspection),
		}, false)

	case "|", "union", "intersect", "except":
		// §19.8.8.4, in streamexprs.go: a cascade on the two operands'
		// postures rather than the general rules.
		return a.unionExpr(x.Left, x.Right)

	default:
		// "!" has its own rule in §19.8.8.6.
		return a.unknown()
	}
}

// filter applies §19.8.8.9 to a filter expression.
func (a *analyzer) filter(x *xpath.FilterExpr) props {
	base := a.expr(x.Base)
	if !base.streamable() {
		return base
	}
	// Each predicate is assessed with the base's posture as its context
	// posture, and applied in turn.
	cur := base
	for _, p := range x.Predicates {
		inner := &analyzer{
			ctxPosture:        cur.posture,
			ctxAllowsChildren: a.allowsChildren(x.Base),
			known:             a.known,
			funcs:             a.funcs,
			streamingParam:    a.streamingParam,
			paramCategory:     a.paramCategory,
			hasStreamParam:    a.hasStreamParam,
			higherOrder:       a.higherOrder,
		}
		pp := inner.expr(p)
		a.known = a.known && inner.known
		// The first rule of §19.8.8.9 narrows crawling to striding for a
		// numeric predicate independent of the focus. It is not
		// implemented; not applying it costs precision but never
		// correctness, because the fallback below is stricter.
		if pp.sweep != sweepMotionless {
			return roamingFreeRanging
		}
		// A motionless predicate leaves the posture and sweep of the base.
	}
	return cur
}

// step applies §19.8.8.8 to a single axis step evaluated in the given context
// posture.
func (a *analyzer) step(s *xpath.Step, ctx posture) props {
	if ctx == postureRoaming {
		return roamingFreeRanging
	}
	base := postureOfAxisStep(ctx, axisOf(s.Axis), stepSelectsElements(s))
	if !base.streamable() {
		return base
	}
	// A predicate is assessed with the step's own posture as its context
	// posture. §19.8.8.8: if any predicate is not motionless, the step is
	// roaming and free-ranging.
	for _, p := range s.Predicates {
		inner := &analyzer{
			ctxPosture:        base.posture,
			ctxAllowsChildren: stepAllowsChildren(s),
			known:             a.known,
			funcs:             a.funcs,
			streamingParam:    a.streamingParam,
			paramCategory:     a.paramCategory,
			hasStreamParam:    a.hasStreamParam,
			higherOrder:       a.higherOrder,
		}
		pp := inner.expr(p)
		a.known = a.known && inner.known
		if pp.sweep != sweepMotionless {
			return roamingFreeRanging
		}
	}
	return base
}

// path applies §19.8.8.7 to a relative path expression.
//
// The spec treats "a/b/c" as the binary tree "(a/b)/c", and gives the sweep as
// the wider of the two operands and the posture as that of the right-hand
// operand assessed in the left-hand operand's posture. Folding left to right
// over the steps computes exactly that.
func (a *analyzer) path(x *xpath.PathExpr) props {
	cur := props{a.ctxPosture, sweepMotionless}
	if x.Root {
		// A leading "/" is rewritten to a call on fn:root (§19.8.8.7),
		// which from a striding posture in a streamed document yields the
		// document node -- striding and motionless (§19.8.9.18). From any
		// other context posture the analysis has no opinion.
		if a.ctxPosture != postureStriding && a.ctxPosture != postureGrounded {
			return a.unknown()
		}
		if a.ctxPosture == postureGrounded {
			cur = groundedMotionless
		} else {
			cur = props{postureStriding, sweepMotionless}
		}
	}
	// §19.8.8.7 assesses a path in two phases: a provisional posture folded
	// left to right over the steps, and — if that comes out roaming — a
	// reassessment that recognises a scanning expression.
	//
	// The two are interleaved here rather than run one after the other, and
	// the reason is paths like "//PRICE/..". Folding reaches
	// descendant-or-self::node() (crawling), then child::PRICE, which the
	// axis table calls roaming from a crawling posture. Reassessing the
	// whole path would fail, because parent:: is not a scanning step. What
	// the spec's binary tree does is reassess the left-hand operand — the
	// prefix "//PRICE", which does scan and is therefore crawling — and then
	// continue. So whenever a step roams, the prefix up to and including it
	// is retried as a scanning expression, and the fold carries on from
	// there.
	//
	// Whether the context item each step sees can have children. It starts as
	// the analyzer's own context and is narrowed by every axis step, so that
	// a non-step operand later in the path is assessed against the node the
	// steps before it actually deliver.
	curAllowsChildren := a.ctxAllowsChildren
	if x.Root {
		curAllowsChildren = true
	}
	for i, e := range x.Steps {
		var next props
		if st, ok := e.(*xpath.Step); ok {
			next = a.step(st, cur.posture)
			curAllowsChildren = stepAllowsChildren(st)
		} else {
			// A non-step operand in a path, as in "(a|b)/c". Assessed in
			// the current posture like any other expression, and with the
			// context item that the steps so far deliver: in "@nr/string()"
			// the context of string() is an attribute, which has no
			// children, so the absorption is downgraded to inspection and
			// the step is motionless rather than consuming. Assuming
			// children here made "chapter/(@nr/string(), @length/string())"
			// two consuming operands and so roaming, which rejected
			// streamable-031 -- a case the catalog expects to run.
			inner := &analyzer{
				ctxPosture:        cur.posture,
				ctxAllowsChildren: curAllowsChildren,
				known:             a.known,
				funcs:             a.funcs,
				streamingParam:    a.streamingParam,
				paramCategory:     a.paramCategory,
				hasStreamParam:    a.hasStreamParam,
				higherOrder:       a.higherOrder,
			}
			next = inner.expr(e)
			curAllowsChildren = inner.allowsChildren(e)
			a.known = a.known && inner.known
		}
		if !next.streamable() {
			prefix := x.Steps[:i+1]
			if !a.isScanningSteps(prefix) {
				return roamingFreeRanging
			}
			// A scanning prefix is crawling if it can select an element,
			// striding otherwise, and it has read the subtree to find out.
			pp := postureStriding
			if stepsSelectElements(prefix) {
				pp = postureCrawling
			}
			next = props{pp, sweepConsuming}
		}
		cur = props{next.posture, wider(cur.sweep, next.sweep)}
	}
	return cur
}

// isScanningSteps reports whether every step is a scanning expression in the
// sense of §19.8.8.7: an axis step on child, descendant, descendant-or-self or
// self, whose predicates are all motionless and non-positional.
func (a *analyzer) isScanningSteps(steps []xpath.Expr) bool {
	for _, e := range steps {
		if !a.isScanningStep(e) {
			return false
		}
	}
	return true
}

func (a *analyzer) isScanningStep(e xpath.Expr) bool {
	switch s := e.(type) {
	case *xpath.ContextItem:
		// "." is the self axis with no predicate.
		return true
	case *xpath.VarRef:
		// A reference to the streaming parameter of a declared-streamable
		// stylesheet function (§19.8.5) plays the same role at the head of a
		// path that "." plays: it names the striding node the scan starts
		// from. "$node//section" is a scanning expression for exactly the
		// reason ".//section" is. Any other variable is grounded, and a path
		// rooted at a grounded value never needed the scanning rule.
		return a.hasStreamParam &&
			s.Name.URI == a.streamingParam.URI &&
			s.Name.Local == a.streamingParam.Local
	case *xpath.Step:
		switch s.Axis {
		case xpath.AxisChild, xpath.AxisDescendant,
			xpath.AxisDescendantOrSelf, xpath.AxisSelf:
		default:
			return false
		}
		for _, p := range s.Predicates {
			// §19.8.8.7's own note: "positional predicates (such as [1])
			// are allowed in the left-hand operand of a relative path
			// expression if it uses the child axis, but not if it uses the
			// descendant axis." A positional predicate on child:: picks one
			// node from a set of peers, which stays a single downward pass;
			// on descendant:: it picks one from a nested set, which does
			// not.
			if isPositionalPredicate(p) &&
				s.Axis != xpath.AxisChild && s.Axis != xpath.AxisSelf {
				return false
			}
			// The predicate is assessed with a striding context posture.
			// Using the step's own posture would be circular, and striding
			// is the posture a scanning expression's steps are reached in.
			inner := &analyzer{
				ctxPosture:        postureStriding,
				ctxAllowsChildren: stepAllowsChildren(s),
				known:             true,
				funcs:             a.funcs,
				streamingParam:    a.streamingParam,
				paramCategory:     a.paramCategory,
				hasStreamParam:    a.hasStreamParam,
				higherOrder:       a.higherOrder,
			}
			sw := inner.expr(p).sweep
			if !inner.known {
				// The predicate holds something this analysis does not
				// model, so "not a scanning expression" is a statement
				// about the implementation rather than about the path.
				// Saying so is what keeps the caller from reporting the
				// resulting roaming verdict as an XTSE3430: si-group-051
				// filters on current-group(), and without this the whole
				// path came back roaming with known still true.
				a.known = false
				return false
			}
			if sw != sweepMotionless {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// isPositionalPredicate reports whether a predicate depends on the position of
// the item within the sequence being filtered. §19.8.8.7 excludes such
// predicates from a scanning expression: "[1]" on the descendant axis selects
// one node out of a nested set, which cannot be found in a single pass.
//
// The test is syntactic and deliberately over-broad: any numeric literal, and
// any call on fn:position or fn:last at any depth, disqualifies the step.
// Over-broad is the safe direction here, because a step wrongly excluded from
// the scanning rule falls back to roaming, which reports nothing.
func isPositionalPredicate(e xpath.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *xpath.Literal:
		// A bare numeric predicate is positional; a string is not.
		if x.Val == nil {
			return false
		}
		return x.Val.Type.IsNumeric()
	case *xpath.FuncCall:
		if x.Name.URI == fnNS && (x.Name.Local == "position" || x.Name.Local == "last") {
			return true
		}
		for _, arg := range x.Args {
			if isPositionalPredicate(arg) {
				return true
			}
		}
		return false
	case *xpath.BinaryOp:
		return isPositionalPredicate(x.Left) || isPositionalPredicate(x.Right)
	case *xpath.UnaryOp:
		return isPositionalPredicate(x.Operand)
	case *xpath.IfExpr:
		return isPositionalPredicate(x.Cond) ||
			isPositionalPredicate(x.Then) || isPositionalPredicate(x.Else)
	case *xpath.FilterExpr:
		if isPositionalPredicate(x.Base) {
			return true
		}
		for _, p := range x.Predicates {
			if isPositionalPredicate(p) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// stepsSelectElements reports whether the last step of a scanning expression
// can select an element -- the U{element} test that decides whether it is
// crawling or striding.
func stepsSelectElements(steps []xpath.Expr) bool {
	for i := len(steps) - 1; i >= 0; i-- {
		if s, ok := steps[i].(*xpath.Step); ok {
			return stepSelectsElements(s)
		}
	}
	return true
}

// funcCall applies §19.8.9 to a call on a built-in function.
func (a *analyzer) funcCall(x *xpath.FuncCall) props {
	if x.Name.URI != fnNS {
		// A stylesheet function is assessed under §19.8.5 (streamfunctions.go).
		// An extension function, or a stylesheet function this analysis did
		// not collect, stays unmodelled.
		if p, ok := a.callProps(x); ok {
			return p
		}
		return a.unknown()
	}
	// The two nullary context functions §19.8.9 gives sections of their own,
	// because they have no operands for the general rules to work on
	// (streamexprs.go).
	if len(x.Args) == 0 {
		switch x.Name.Local {
		case "last":
			// §19.8.9.14.
			return a.lastFunction()
		case "position":
			// §19.8.9.16: no operands, so the general rules make it
			// grounded and motionless.
			return groundedMotionless
		}
	}
	usages, ok := builtinOperandUsages(x.Name.Local, len(x.Args))
	if !ok {
		return a.unknown()
	}
	// A function with no arguments, or one whose arguments were defaulted
	// to the context item, is handled by the table: the zero-arity entries
	// that default to "." carry the usage of the one-argument form and an
	// implicit context-item operand.
	if len(x.Args) == 0 && len(usages) == 1 {
		// fn:string(), fn:data(), fn:name() and the rest are equivalent to
		// the same call on ".".
		ci := operand{
			props: props{a.ctxPosture, sweepMotionless},
			usage: usages[0],
			// The implicit argument is ".", so whether absorbing it reads
			// further from the stream is the context item's question, exactly
			// as it is for the spelled-out call. Hardcoding true here made
			// "string()" consuming where "string(.)" was motionless, which
			// §19.8.1 gives no warrant for: the two are equivalent.
			allowsChildren: a.ctxAllowsChildren,
		}
		return combine([]operand{ci}, false)
	}
	if len(usages) != len(x.Args) {
		return a.unknown()
	}
	ops := make([]operand, 0, len(x.Args))
	for i, arg := range x.Args {
		ops = append(ops, a.operandOf(arg, usages[i]))
	}
	return combine(ops, singletonBuiltin[x.Name.Local])
}

const fnNS = "http://www.w3.org/2005/xpath-functions"

// singletonBuiltin names the built-in functions whose return type has a
// maximum cardinality of one and whose argument usage is transmission. §19.8.1
// notes that these three are the only ones, and gives them the rule that turns
// a crawling operand into a striding result.
var singletonBuiltin = map[string]bool{
	"head": true, "exactly-one": true, "zero-or-one": true,
}

// builtinOperandUsages returns the operand usage of each argument of a call on
// fn:name with the given arity, as listed in §19.8.9, and whether the function
// is one this analysis models.
//
// A function absent from this table is not necessarily unstreamable; it is
// merely one the analysis has no entry for, and the caller treats it as "no
// opinion". Functions that §19.8.9 gives their own section -- fn:current,
// fn:last, fn:position, fn:root, fn:reverse, fn:innermost, fn:outermost,
// fn:fold-right, fn:function-lookup, and the accumulator and merge functions
// -- are deliberately absent, because the general rules do not describe them.
func builtinOperandUsages(name string, arity int) ([]usage, bool) {
	const (
		A = usageAbsorption
		I = usageInspection
		T = usageTransmission
		N = usageNavigation
	)
	// Functions whose every argument is absorption, keyed by the arities
	// §19.8.9 lists for them.
	allAbsorption := map[string][]int{
		"abs": {1}, "avg": {1}, "ceiling": {1}, "floor": {1},
		"round": {1, 2}, "round-half-to-even": {1, 2},
		"sum": {1, 2}, "min": {1, 2}, "max": {1, 2},
		"string": {1}, "data": {1}, "number": {1},
		"string-length": {1}, "normalize-space": {1},
		"string-join": {1, 2}, "string-to-codepoints": {1},
		"codepoints-to-string": {1}, "concat": {3},
		"upper-case": {1}, "lower-case": {1},
		"substring": {2, 3}, "substring-before": {2, 3},
		"substring-after": {2, 3}, "contains": {2, 3},
		"starts-with": {2, 3}, "ends-with": {2, 3},
		"compare": {2, 3}, "codepoint-equal": {2},
		"translate": {3}, "tokenize": {2}, "matches": {2, 3},
		"replace": {3, 4}, "normalize-unicode": {1, 2},
		"distinct-values": {1, 2}, "index-of": {2, 3},
		"deep-equal": {2, 3}, "copy-of": {1}, "snapshot": {1},
		"doc": {1}, "doc-available": {1}, "document": {1},
		"parse-xml": {1}, "parse-xml-fragment": {1},
		"serialize": {1, 2}, "json-to-xml": {1, 2},
		"escape-html-uri": {1}, "encode-for-uri": {1},
		"iri-to-uri": {1}, "resolve-uri": {1, 2},
		"format-number": {2, 3}, "format-integer": {2, 3},
		"format-date": {2, 5}, "format-dateTime": {2, 5}, "format-time": {2, 5},
		"element-available": {1}, "function-available": {1, 2},
		"function-arity": {1}, "function-name": {1},
		"system-property": {1}, "environment-variable": {1},
		"stream-available": {1}, "regex-group": {1},
		"local-name-from-QName": {1}, "namespace-uri-from-QName": {1},
		"prefix-from-QName": {1}, "QName": {2},
		"collation-key": {1, 2}, "collection": {1},
		"analyze-string": {2, 3}, "dateTime": {2},
		"day-from-date": {1}, "month-from-date": {1}, "year-from-date": {1},
		"day-from-dateTime": {1}, "month-from-dateTime": {1},
		"year-from-dateTime": {1}, "hours-from-dateTime": {1},
		"minutes-from-dateTime": {1}, "seconds-from-dateTime": {1},
		"hours-from-time": {1}, "minutes-from-time": {1}, "seconds-from-time": {1},
		"days-from-duration": {1}, "hours-from-duration": {1},
		"minutes-from-duration": {1}, "seconds-from-duration": {1},
		"months-from-duration": {1}, "years-from-duration": {1},
		"timezone-from-date": {1}, "timezone-from-dateTime": {1},
		"timezone-from-time":          {1},
		"adjust-date-to-timezone":     {1, 2},
		"adjust-dateTime-to-timezone": {1, 2},
		"adjust-time-to-timezone":     {1, 2},
	}
	// Functions whose every argument is inspection.
	allInspection := map[string][]int{
		"count": {1}, "empty": {1}, "exists": {1}, "boolean": {1},
		"not": {1}, "has-children": {1}, "nilled": {1},
		"name": {1}, "local-name": {1}, "namespace-uri": {1},
		"node-name": {1}, "base-uri": {1}, "document-uri": {1},
		"generate-id": {1}, "in-scope-prefixes": {1},
	}
	// The remainder, whose arguments differ from one another.
	mixed := map[string]map[int][]usage{
		"head":          {1: {T}},
		"tail":          {1: {T}},
		"exactly-one":   {1: {T}},
		"zero-or-one":   {1: {T}},
		"one-or-more":   {1: {T}},
		"remove":        {2: {T, A}},
		"subsequence":   {2: {T, A}, 3: {T, A, A}},
		"insert-before": {3: {T, A, T}},
		"unordered":     {1: {T}},
		// §19.8.9.17 and §19.8.9.13. Both have their own subsection, but only
		// to explain why the usage is navigation, not to displace the general
		// rules: reverse "follows the general streamability rules, with its
		// operand classified as having operand usage navigation", and
		// innermost "follows the general streamability rules, with the first
		// argument having operand usage navigation" because a node cannot be
		// known to be in the result until its descendants have been read.
		// fn:outermost stays absent: §19.8.9.15 gives it a genuine exception
		// to the general rules, turning a crawling argument striding.
		"reverse":                  {1: {N}},
		"innermost":                {1: {N}},
		"filter":                   {2: {N, I}},
		"for-each":                 {2: {N, I}},
		"for-each-pair":            {3: {N, N, I}},
		"fold-left":                {3: {N, A, I}},
		"lang":                     {2: {A, I}},
		"id":                       {2: {A, N}},
		"idref":                    {2: {A, N}},
		"element-with-id":          {2: {A, N}},
		"key":                      {3: {A, A, N}},
		"path":                     {1: {N}},
		"namespace-uri-for-prefix": {2: {A, I}},
		"resolve-QName":            {2: {A, I}},
		"document":                 {2: {A, I}},
		"error":                    {3: {A, A, N}},
	}
	// Functions that take no arguments at all are grounded and motionless
	// by §19.8.1, having no operands.
	noArgs := map[string]bool{
		"true": true, "false": true, "current-date": true,
		"current-dateTime": true, "current-time": true,
		"implicit-timezone": true, "default-collation": true,
		"static-base-uri": true, "current-output-uri": true,
		"available-environment-variables": true, "collection": true,
	}
	if arity == 0 {
		if noArgs[name] {
			return nil, true
		}
		// A function defaulting its one argument to the context item. The
		// caller supplies the implicit "." operand with the usage returned
		// here.
		if as, ok := allAbsorption[name]; ok && contains(as, 1) {
			return []usage{A}, true
		}
		if is, ok := allInspection[name]; ok && contains(is, 1) {
			return []usage{I}, true
		}
		if name == "path" {
			return []usage{N}, true
		}
		return nil, false
	}
	if as, ok := allAbsorption[name]; ok && contains(as, arity) {
		us := make([]usage, arity)
		for i := range us {
			us[i] = A
		}
		return us, true
	}
	if is, ok := allInspection[name]; ok && contains(is, arity) {
		us := make([]usage, arity)
		for i := range us {
			us[i] = I
		}
		return us, true
	}
	if byArity, ok := mixed[name]; ok {
		if us, ok := byArity[arity]; ok {
			return us, true
		}
	}
	return nil, false
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// axisOf translates an xpath.Axis into the lattice's axisKind.
func axisOf(a xpath.Axis) axisKind {
	switch a {
	case xpath.AxisChild:
		return axisChild
	case xpath.AxisDescendant:
		return axisDescendant
	case xpath.AxisAttribute:
		return axisAttribute
	case xpath.AxisSelf:
		return axisSelf
	case xpath.AxisDescendantOrSelf:
		return axisDescendantOrSelf
	case xpath.AxisFollowingSibling:
		return axisFollowingSibling
	case xpath.AxisFollowing:
		return axisFollowing
	case xpath.AxisParent:
		return axisParent
	case xpath.AxisAncestor:
		return axisAncestor
	case xpath.AxisPrecedingSibling:
		return axisPrecedingSibling
	case xpath.AxisPreceding:
		return axisPreceding
	case xpath.AxisAncestorOrSelf:
		return axisAncestorOrSelf
	default:
		return axisNamespace
	}
}

// stepSelectsElements reports whether the step's node test can select an
// element -- the "Selects elements?" column of the §19.8.8.8 table.
//
// The answer must be conservative in the direction of "yes": saying an element
// cannot be selected when it can would turn a crawling result into a striding
// one, which is the unsafe direction. Only a test that demonstrably names
// another node kind answers no.
func stepSelectsElements(s *xpath.Step) bool {
	switch t := s.Test.(type) {
	case *xpath.KindTest:
		if t.Any {
			// node() selects elements among everything else.
			return true
		}
		return t.Kind == xdm.KindElement || t.Kind == xdm.KindDocument
	case *xpath.NameTest:
		// A name test selects the principal node kind of the axis, which
		// is the element for every axis but attribute and namespace.
		return s.Axis != xpath.AxisAttribute && s.Axis != xpath.AxisNamespace
	default:
		return true
	}
}

// stepAllowsChildren reports whether the nodes a step selects can have
// children -- whether, that is, absorbing one of them reads further from the
// stream. Attribute and namespace nodes cannot, and neither can a step whose
// kind test names text, comment or processing-instruction.
func stepAllowsChildren(s *xpath.Step) bool {
	if s.Axis == xpath.AxisAttribute || s.Axis == xpath.AxisNamespace {
		return false
	}
	if kt, ok := s.Test.(*xpath.KindTest); ok && !kt.Any {
		return kt.Kind == xdm.KindElement || kt.Kind == xdm.KindDocument
	}
	return true
}

// allowsChildren reports whether the expression's static type can deliver a
// node with children -- the U{element(), document-node()} intersection of
// §19.8.1 that decides whether an absorption usage is downgraded.
//
// The analysis carries no static type inference, so this is answered
// syntactically and only for the one case that matters in practice and that
// the spec calls out: a step on the attribute or namespace axis, whose result
// is an attribute or namespace node and so has no children. Everything else
// answers yes, which leaves the usage at absorption -- the conservative
// direction, since absorption yields the wider sweep.
func (a *analyzer) allowsChildren(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.ContextItem:
		// "." is whatever the context item is.
		return a.ctxAllowsChildren
	case *xpath.Step:
		return stepAllowsChildren(x)
	case *xpath.PathExpr:
		if n := len(x.Steps); n > 0 {
			return a.allowsChildren(x.Steps[n-1])
		}
		return true
	case *xpath.FilterExpr:
		return a.allowsChildren(x.Base)
	case *xpath.Literal:
		return false
	case *xpath.BinaryOp:
		// §19.8.1 asks about the static type T of the whole operand, and for
		// "|", "intersect" and "except" that type is bounded by the two
		// operand types: every node the expression can return comes from one
		// side or the other. So "@* except @length" delivers attributes only
		// and allows no children, exactly as "@*" alone does.
		//
		// Without this, the union rule's deliberate widening to crawling
		// (§19.8.8.4, "author | author/name") met an absorption usage that
		// was never downgraded to inspection, and a crawling absorbing
		// operand is charged free-ranging -- which rejected the
		// "<xsl:copy-of select='@* except @length'/>" of streamable-046 and
		// -063, both of which the catalog expects to run.
		switch x.Op {
		case "|", "union", "intersect", "except":
			return a.allowsChildren(x.Left) || a.allowsChildren(x.Right)
		}
		return true
	default:
		return true
	}
}
