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
	// streamability category. Together they drive the §19.8.8.12 rule that
	// gives a reference to the first parameter of a declared-streamable
	// function a posture other than grounded. Empty outside such a body.
	streamingParam xdm.QName
	paramCategory  streamCategory
	hasStreamParam bool

	// ctxStreamedGrounded says the context item here is a streamed node that
	// §19.8.8.12 nevertheless reports as grounded — it was reached from a
	// bare reference to the streaming parameter, as in "$input ! path()".
	// It carries operand.streamedGrounded across a change of focus, so that
	// a navigation usage applied to "." is charged the same way it would be
	// if applied to "$input" directly. Set by simpleMap.
	ctxStreamedGrounded bool

	// higherOrder records that the expression now being analysed sits inside
	// a higher-order operand of some construct between it and the function
	// body. §19.8.8.12 calls a variable reference "singular" when no such
	// construct intervenes, and gives the two answers different postures.
	higherOrder bool

	// currentGroup carries the properties §19.8.9.4 gives a call on
	// fn:current-group: the posture and sweep of the select expression of
	// the innermost containing xsl:for-each-group, when that instruction is
	// also the call's focus-setting container and no higher-order operand
	// lies on the path between them. groupInScope is false where no such
	// instruction contains the call, which by the same rule makes the call
	// roaming and free-ranging.
	currentGroup props
	groupInScope bool

	// groupOutOfReach says a call on fn:current-group() here belongs to an
	// xsl:for-each-group whose body this walk never assessed. Such a call is
	// withheld rather than judged: see the current-group case in funcCall.
	groupOutOfReach bool

	// currentPosture carries §19.8.9.3's answer for a call on fn:current:
	// the context posture of E, where E is "the outermost containing XPath
	// expression of the call to the current function". (Previously quoted
	// from the Last Call draft, whose wording -- "the context posture that
	// would obtain if the entire XPath expression were replaced with '.'" --
	// the Recommendation does not carry.) It is set once where
	// that outermost expression is entered and copied unchanged into every
	// inner analyzer, so that descending into a predicate -- which does
	// change ctxPosture -- leaves it alone. Within a pattern §19.8.9.3 fixes
	// it at striding.
	//
	// currentAllowsChildren is the same answer to §19.8.1's question for
	// that outermost context item, so that absorbing current() is charged
	// exactly as absorbing "." at the outermost level would be. Without it
	// "text()[$parts = current()]" -- the accumulator pattern of
	// stream-200..203, which the catalog expects to run -- would absorb a
	// node assumed to have children and be rejected, while the equivalent
	// "text()[$parts = .]" is motionless. stream-204 is the same pattern on
	// an element step, where the absorption stands and XTSE3430 is right.
	//
	// currentInScope is false where no outermost context was recorded; there
	// a call on fn:current stays unmodelled, as it was before this rule.
	currentPosture        posture
	currentAllowsChildren bool
	currentInScope        bool

	// vars is the data-flow environment: for a range variable bound to a
	// value that is NOT grounded, the posture and sweep that value has.
	//
	// §19.8.8.12 says a variable reference is grounded and motionless, and
	// §19.8.8.1's note explains why the rules stop there: separating a
	// binding that is used harmlessly from one that is navigated from
	// "requires data flow analysis (tracing from the binding of a variable
	// to its usages), rather than purely syntactic analysis". This map is
	// that tracing, and it is confined to the one construct that needs it,
	// the quantified expression of §19.8.8.2. Nothing else consults it,
	// because nothing else has a binding it can see through: an
	// xsl:variable's initialiser is given a navigation usage precisely so
	// that a streamed node cannot be bound to it at all.
	//
	// A binding absent from the map is grounded, which is what §19.8.8.12
	// says and what every reference got before. So the map can only make a
	// reference LESS grounded, never a construct more streamable, and a
	// construct whose binding this analysis cannot assess never enters it.
	vars map[xdm.QName]props

	// accumAfter describes the position of this expression within its
	// enclosing sequence constructor, which is what §19.8.9.1 needs to give
	// a call on fn:accumulator-after a sweep. Its zero value has known
	// false, so a call reached without the instruction walk having set it --
	// from analyzeExpr, say -- stays unmodelled, as it was before this rule
	// existed. See streamaccumafter.go.
	accumAfter accumAfterState
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
		// e is the outermost containing XPath expression -- the E of
		// §19.8.9.3's "let E be the outermost containing XPath expression of
		// the call to the current function" -- so its context posture is
		// this analyzer's own starting context. (The parenthetical once
		// quoted here, "the context posture that would obtain if the entire
		// XPath expression were replaced with '.'", is Last Call wording and
		// is not in the Recommendation.)
		currentPosture:        ctx,
		currentAllowsChildren: true,
		currentInScope:        true,
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
		// §19.8.8.12: a variable reference is motionless, and grounded
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
		// §19.8.8.7: the posture and sweep of "a!b" are those of the right
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

	case *xpath.InlineFunctionExpr:
		// §19.8.8.16: roaming if it textually contains a reference to the
		// containing stylesheet function's streaming parameter.
		return a.inlineFunction(x)

	case *xpath.NamedFunctionRef:
		// §19.8.8.15, to the extent the focus-dependence of the referent is
		// knowable here.
		return a.namedFunctionRef(x)

	case *xpath.LetExpr:
		// §19.8.8's table: "let $var := N return T".
		return a.letExpr(x)
	case *xpath.ForExpr:
		// §19.8.8.1, in streamexprs.go.
		return a.forExpr(x)

	case *xpath.QuantifiedExpr:
		// §19.8.8.2, in streamexprs.go.
		return a.quantifiedExpr(x)

	default:
		// Dynamic function calls (§19.8.8.11), and any expression kind this
		// switch has not been taught. Reporting no opinion is what keeps an
		// unmodelled construct from becoming a spurious refusal.
		return a.unknown()
	}
}

// higherOrderOperand assesses e as a higher-order operand of the construct
// being analysed, with the given usage.
//
// Two things follow from the operand being higher-order, and both matter.
// combine refuses a construct whose one consuming operand is higher-order,
// because the parent may evaluate it more than once and the input cannot be
// rewound. And §19.8.8.12 makes a reference to the streaming parameter
// non-singular once a higher-order operand separates it from the function
// body, which for the absorbing, shallow-descent and deep-descent categories
// turns that reference roaming.
func (a *analyzer) higherOrderOperand(e xpath.Expr, u usage) operand {
	inner := &analyzer{
		ctxPosture:            a.ctxPosture,
		ctxAllowsChildren:     a.ctxAllowsChildren,
		known:                 a.known,
		funcs:                 a.funcs,
		streamingParam:        a.streamingParam,
		paramCategory:         a.paramCategory,
		hasStreamParam:        a.hasStreamParam,
		higherOrder:           true,
		currentGroup:          a.currentGroup,
		groupInScope:          a.groupInScope,
		groupOutOfReach:       a.groupOutOfReach,
		currentPosture:        a.currentPosture,
		currentAllowsChildren: a.currentAllowsChildren,
		currentInScope:        a.currentInScope,
		vars:                  a.vars,
	}
	p := inner.expr(e)
	a.known = a.known && inner.known
	return operand{
		props:          p,
		usage:          u,
		allowsChildren: inner.allowsChildren(e),
		higherOrder:    true,
	}
}

// operandOf assesses a subexpression and packages it as an operand with the
// given usage.
func (a *analyzer) operandOf(e xpath.Expr, u usage) operand {
	p := a.expr(e)
	return operand{
		props:            p,
		usage:            u,
		allowsChildren:   a.allowsChildren(e),
		streamedGrounded: a.isStreamingParamRef(e),
	}
}

// isStreamingParamRef reports whether the expression denotes the streaming
// parameter of the stylesheet function being analysed -- either as a bare
// reference to it, or as "." where the focus was set from one.
//
// Only those two shapes count. Once the reference is used in a path or a
// filter, the enclosing expression has a posture of its own that the ordinary
// rules already carry, and it is that posture -- not the parameter's -- that
// decides what a further usage costs.
func (a *analyzer) isStreamingParamRef(e xpath.Expr) bool {
	if !a.hasStreamParam {
		return false
	}
	switch x := e.(type) {
	case *xpath.VarRef:
		return x.Name.URI == a.streamingParam.URI &&
			x.Name.Local == a.streamingParam.Local
	case *xpath.ContextItem:
		// "." inside "$input ! f(.)" denotes the same streamed node the
		// streaming parameter does; simpleMap records that in the context.
		return a.ctxStreamedGrounded
	}
	return false
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
		// "!" has its own rule in §19.8.8.7.
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
			ctxPosture:            cur.posture,
			ctxAllowsChildren:     a.allowsChildren(x.Base),
			known:                 a.known,
			funcs:                 a.funcs,
			streamingParam:        a.streamingParam,
			paramCategory:         a.paramCategory,
			hasStreamParam:        a.hasStreamParam,
			higherOrder:           a.higherOrder,
			currentGroup:          a.currentGroup,
			groupInScope:          a.groupInScope,
			groupOutOfReach:       a.groupOutOfReach,
			currentPosture:        a.currentPosture,
			currentAllowsChildren: a.currentAllowsChildren,
			currentInScope:        a.currentInScope,
			vars:                  a.vars,
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
			ctxPosture:            base.posture,
			ctxAllowsChildren:     stepAllowsChildren(s),
			known:                 a.known,
			funcs:                 a.funcs,
			streamingParam:        a.streamingParam,
			paramCategory:         a.paramCategory,
			hasStreamParam:        a.hasStreamParam,
			higherOrder:           a.higherOrder,
			currentGroup:          a.currentGroup,
			groupInScope:          a.groupInScope,
			groupOutOfReach:       a.groupOutOfReach,
			currentPosture:        a.currentPosture,
			currentAllowsChildren: a.currentAllowsChildren,
			currentInScope:        a.currentInScope,
			vars:                  a.vars,
		}
		pp := inner.expr(p)
		a.known = a.known && inner.known
		if pp.sweep != sweepMotionless {
			return roamingFreeRanging
		}
	}
	return base
}

// path applies §19.8.8.8 to a relative path expression.
//
// The spec treats "a/b/c" as the binary tree "(a/b)/c", and gives the sweep as
// the wider of the two operands and the posture as that of the right-hand
// operand assessed in the left-hand operand's posture. Folding left to right
// over the steps computes exactly that.
func (a *analyzer) path(x *xpath.PathExpr) props {
	cur := props{a.ctxPosture, sweepMotionless}
	if x.Root {
		// A leading "/" is rewritten to a call on fn:root (§19.8.8.8),
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
	// §19.8.8.8 assesses a path in two phases: a provisional posture folded
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
				ctxPosture:            cur.posture,
				ctxAllowsChildren:     curAllowsChildren,
				known:                 a.known,
				funcs:                 a.funcs,
				streamingParam:        a.streamingParam,
				paramCategory:         a.paramCategory,
				hasStreamParam:        a.hasStreamParam,
				higherOrder:           a.higherOrder,
				currentGroup:          a.currentGroup,
				groupInScope:          a.groupInScope,
				groupOutOfReach:       a.groupOutOfReach,
				currentPosture:        a.currentPosture,
				currentAllowsChildren: a.currentAllowsChildren,
				currentInScope:        a.currentInScope,
				vars:                  a.vars,
			}
			next = inner.expr(e)
			curAllowsChildren = inner.allowsChildren(e)
			a.known = a.known && inner.known
		}
		if !next.streamable() {
			prefix := x.Steps[:i+1]
			// §19.8.8.8's reassessment presupposes the scan starts from a
			// node of the stream that has not yet been passed: its own note
			// gives the strategy as "examine each descendant of the context
			// node", and every worked example it closes with is prefaced
			// "assume that the context posture is striding". From a climbing
			// posture that strategy is not available -- the descendants of an
			// ancestor include the whole subtree already read -- so the
			// provisional roaming verdict stands. Without this guard
			// "for-each select='..'" with a "count(*)" body came out
			// grounded and consuming, accepting streamable-126, while the
			// same navigation written as "count(../*)" was correctly refused.
			if !a.scanMayStart(prefix) || !a.isScanningSteps(prefix) {
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

// scanMayStart reports whether §19.8.8.8's reassessment may be applied to this
// prefix, which turns on the node the scan starts from.
//
// The reassessment leaves that precondition implicit: its note gives the
// strategy as "examine each descendant of the context node", and the worked
// examples it closes with are prefaced "assume that the context posture is
// striding". From a climbing or crawling context those descendants include
// nodes the stream has already delivered, so the provisional roaming verdict
// has to stand -- which is what makes streamable-126 ("for-each select='..'"
// with a "count(*)" body) the refusal §18.1 requires rather than a grounded
// pass.
//
// A prefix that begins with a VarRef does not start from the context item at
// all: "$node//section" scans from the striding node a streaming parameter
// denotes, whatever the context posture of the function body happens to be.
// isScanningStep already recognises that head, and gating it on the context
// posture refused function-5016, a valid deep-descent function.
func (a *analyzer) scanMayStart(prefix []xpath.Expr) bool {
	if len(prefix) > 0 {
		if _, ok := prefix[0].(*xpath.VarRef); ok {
			return true
		}
	}
	return a.ctxPosture == postureStriding || a.ctxPosture == postureGrounded
}

// isScanningSteps reports whether every step is a scanning expression in the
// sense of §19.8.8.8: an axis step on child, descendant, descendant-or-self or
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
			// §19.8.8.8's note, verbatim: "Scanning expressions cannot use
			// positional predicates: for example //section/head[1] is not
			// recognized as a scanning expression because this would require
			// information about a streamed node (specifically, about its
			// preceding siblings) that is not retained during streaming."
			//
			// This comment previously quoted a sentence that is not in the
			// spec at all -- "positional predicates (such as [1]) are allowed
			// in the left-hand operand of a relative path expression if it
			// uses the child axis, but not if it uses the descendant axis."
			// The rule the code applies is the real one: a positional
			// predicate disqualifies a step from being part of a SCANNING
			// expression, because deciding it needs the preceding siblings
			// the stream has already passed.
			if isPositionalPredicate(p) &&
				s.Axis != xpath.AxisChild && s.Axis != xpath.AxisSelf {
				return false
			}
			// The predicate is assessed with a striding context posture.
			// Using the step's own posture would be circular, and striding
			// is the posture a scanning expression's steps are reached in.
			inner := &analyzer{
				ctxPosture:            postureStriding,
				ctxAllowsChildren:     stepAllowsChildren(s),
				known:                 true,
				funcs:                 a.funcs,
				streamingParam:        a.streamingParam,
				paramCategory:         a.paramCategory,
				hasStreamParam:        a.hasStreamParam,
				higherOrder:           a.higherOrder,
				currentGroup:          a.currentGroup,
				groupInScope:          a.groupInScope,
				groupOutOfReach:       a.groupOutOfReach,
				currentPosture:        a.currentPosture,
				currentAllowsChildren: a.currentAllowsChildren,
				currentInScope:        a.currentInScope,
				vars:                  a.vars,
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
// the item within the sequence being filtered. §19.8.8.8 excludes such
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
	if x.Name.URI == xdm.NSXS && len(x.Args) == 1 {
		// §19.8.8.14: "For a call to a constructor function, the general
		// rules for streamability apply. There is a single operand role
		// (the argument to the function), with operand usage absorption."
		// §19.9's own worked example spells this out for xs:date(@timestamp).
		// A constructor function is any name in the XML Schema namespace
		// with one argument, since that namespace holds nothing else that
		// is callable.
		return combine([]operand{a.operandOf(x.Args[0], usageAbsorption)}, false)
	}
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
		case "current":
			// §19.8.9.3 gives a four-way cascade. The wording quoted here
			// before -- "the posture is the context posture for evaluation of
			// the outermost containing XPath expression (that is, the context
			// posture that would obtain if the entire XPath expression were
			// replaced with '.')" -- is from the Last Call draft and is not
			// in the Recommendation, which reads:
			//
			//   "If the call appears within a pattern, then climbing and
			//   motionless. [...] Otherwise, let E be the outermost
			//   containing XPath expression of the call to the current
			//   function. If the context posture of E is grounded, then
			//   motionless and grounded. If the path in the expression tree
			//   that connects the call on current to E (excluding E itself)
			//   contains an expression that is a higher-order operand of its
			//   parent expression, then motionless and climbing. [...]
			//   Otherwise, the posture is the context posture, and the sweep
			//   is motionless."
			//
			// The pattern clause is applied by the caller, which sets
			// currentPosture before entering the pattern. The grounded clause
			// needs no separate branch: where E's context posture is
			// grounded, currentPosture is already grounded, so the final
			// clause returns the same answer.
			//
			// The higher-order clause is NOT implemented, deliberately. It
			// was written (return climbing when a.higherOrder) and reverted,
			// because nothing could be found that observes it:
			//
			//   - instrumenting the branch with a panic and running the whole
			//     XSLT 3.0 corpus never fired it, and the corpus count is
			//     identical with and without it (11,490 / 28 either way);
			//   - the constructs that DO set higherOrder absorb the
			//     distinction before it can surface. xsl:for-each with a
			//     grounded select is the obvious candidate, and its body is
			//     motionless whatever current() returned: "current()/a" and
			//     "./a" give the same grounded/motionless answer, so no
			//     assertion over that construct can tell the two readings
			//     apart. A test written against it passed with the branch
			//     deleted.
			//
			// Adding a rule no test can fail is how dead code enters an
			// analysis that is otherwise pinned case by case, so the clause
			// is recorded here rather than written. If a construct is found
			// that exposes it, this comment is the place to start.
			//
			// Where no outermost context was recorded the call stays
			// unmodelled, so the caller reports nothing rather than guessing.
			if !a.currentInScope {
				return a.unknown()
			}
			return props{a.currentPosture, sweepMotionless}
		case "position":
			// §19.8.9.16: no operands, so the general rules make it
			// grounded and motionless.
			return groundedMotionless
		case "current-grouping-key", "current-merge-key", "current-merge-group":
			// §19.8.9.5, §19.8.9.7 and §19.8.9.6. Each is grounded and
			// motionless unconditionally. For current-merge-group the spec
			// gives the reason: "the nodes to be merged are always
			// snapshots, and therefore grounded".
			return groundedMotionless
		case "current-group":
			// §19.8.9.4: the sweep and posture of the call are those of the
			// select expression of the containing xsl:for-each-group, but
			// only when that instruction is the call's focus-setting
			// container and no higher-order operand separates them.
			// "Otherwise, roaming and free-ranging" -- a fact about the
			// stylesheet, not a gap in this analysis, so known stays set.
			// A container nested inside an xsl:for-each-group has the outer
			// select assessed for it by enclosingGroupSelect.
			if !a.groupInScope {
				if a.groupOutOfReach {
					return a.unknown()
				}
				return roamingFreeRanging
			}
			return a.currentGroup
		}
	}
	// §19.8.9.15: fn:outermost has a single transmission operand and
	// follows the general rules "with one exception: if the posture of the
	// argument is crawling, then the posture of the result is striding".
	// Outermost strips the nested nodes out of a crawling sequence, so what
	// is left cannot contain a node inside another -- which is exactly what
	// striding asserts. The exception is a narrowing, so modelling it can
	// only make a call streamable where abandoning it said nothing.
	if x.Name.Local == "outermost" && len(x.Args) == 1 {
		p := combine([]operand{a.operandOf(x.Args[0], usageTransmission)}, false)
		if p.posture == postureCrawling {
			p.posture = postureStriding
		}
		return p
	}
	// §19.8.9.1: fn:accumulator-after has its own cascade, whose answer
	// depends on where in the enclosing sequence constructor the call sits.
	// The posture is grounded in every case; only the sweep is computed, and
	// the instruction walk supplies the position it turns on.
	if x.Name.Local == "accumulator-after" && len(x.Args) == 1 {
		// Rule 1: "If the first argument (the accumulator name) is not
		// motionless, the function is free-ranging."
		if arg := a.expr(x.Args[0]); arg.sweep != sweepMotionless {
			return props{postureGrounded, sweepFreeRanging}
		}
		sw, ok := accumulatorAfterSweep(a.accumAfter, a.ctxPosture, a.ctxAllowsChildren)
		if !ok {
			return a.unknown()
		}
		return props{postureGrounded, sw}
	}

	// §19.8.9.2: "If the argument to accumulator-before is motionless, the
	// function call is grounded and motionless. Otherwise, the function call
	// is roaming and free-ranging." Without this entry the call was
	// unmodelled, and an expression such as
	// "accumulator-after('w') - accumulator-before('w')" -- accumulator-059's
	// -- took the whole expression with it, so the consuming call on
	// accumulator-after beside it was never seen.
	if x.Name.Local == "accumulator-before" && len(x.Args) == 1 {
		if arg := a.expr(x.Args[0]); arg.sweep != sweepMotionless {
			return roamingFreeRanging
		}
		return groundedMotionless
	}

	usages, ok := builtinOperandUsages(x.Name.Local, len(x.Args))
	if !ok && contextDefaultingBuiltin[x.Name.Local] {
		// A call one argument short of a form whose FINAL argument defaults
		// to the context item. The table holds the spelt-out form, so the
		// defaulted one is looked up there and the implicit "." operand is
		// supplied below.
		usages, ok = builtinOperandUsages(x.Name.Local, len(x.Args)+1)
	}
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
			// Likewise "path()" is "path(.)": when the context item is the
			// streamed node a streaming parameter denotes, navigating from
			// it costs the same either way. See operand.streamedGrounded.
			streamedGrounded: a.ctxStreamedGrounded,
		}
		return combine([]operand{ci}, false)
	}
	// A function whose last argument defaults to the context item, called
	// without it. §19.8.9's list gives both forms: "fn:lang(x) -- Equivalent
	// to fn:lang(x, .)" followed by "fn:lang(A, I)". The spelt-out call is
	// modelled by the table, so the defaulted one is modelled by supplying
	// the implicit "." operand with the usage the table gives its position.
	// Leaving it unmodelled made the whole enclosing construct unknown,
	// which withheld the refusal of streamable-128.
	if len(usages) == len(x.Args)+1 && contextDefaultingBuiltin[x.Name.Local] {
		ops := make([]operand, 0, len(usages))
		for i, arg := range x.Args {
			ops = append(ops, a.operandOf(arg, usages[i]))
		}
		ops = append(ops, operand{
			props:            props{a.ctxPosture, sweepMotionless},
			usage:            usages[len(usages)-1],
			allowsChildren:   a.ctxAllowsChildren,
			streamedGrounded: a.ctxStreamedGrounded,
		})
		return combine(ops, singletonBuiltin[x.Name.Local])
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

// contextDefaultingBuiltin names the built-in functions whose FINAL argument
// defaults to the context item, so that a call one argument short is the same
// call with an implicit ".". §19.8.9's list gives each of these two entries,
// the equivalence and the usages: "fn:lang(x) -- Equivalent to fn:lang(x, .)"
// with "fn:lang(A, I)", and likewise fn:id and fn:idref with "fn:id(A, N)"
// and "fn:idref(A, N)".
//
// fn:key is deliberately absent: §19.8.9 defaults its third argument to "/",
// not to ".", so the implicit operand is not the context item and the rule
// here does not describe it. Functions that default their ONLY argument are
// handled by the zero-arity branch above instead.
var contextDefaultingBuiltin = map[string]bool{
	"lang": true, "id": true, "idref": true,
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
	case *xpath.FuncCall:
		// current() denotes the outermost context item, so §19.8.1's
		// question about it is that item's question -- not the default
		// "assume children" below. "text()[$parts = current()]" absorbs a
		// text node, which has none, exactly as "text()[$parts = .]" does.
		if x.Name.URI == fnNS && x.Name.Local == "current" && len(x.Args) == 0 {
			return a.currentAllowsChildren
		}
		return true
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
