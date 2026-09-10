package xslt

// §19.8.9.1, the streamability of fn:accumulator-after.
//
// The function has no entry in builtinOperandUsages, and deliberately so: the
// general rules of §19.8.1 do not describe it. §19.8.9.1 states its own
// ordered cascade instead, and the posture is grounded in every case; only the
// sweep is computed. What makes the rule unlike the rest of §19.8.9 is that it
// is not a property of the expression at all -- it depends on where in the
// enclosing sequence constructor the call sits, and on what the instructions
// before it do. So the verdict is computed by the instruction walk, which
// knows the position, and handed down to the expression analyser.
//
// Only the sweep of the *call* is modelled here. The final rule of the cascade
// is the one error-3420a turns on:
//
//	If no enclosing node N of the function call has a preceding sibling node
//	P such that (a) N and P are part of the same sequence constructor, and
//	(b) the sweep of P is consuming, then the function call is consuming.
//
// A consuming call in a sequence constructor that also contains a consuming
// xsl:copy-of gives that constructor two consuming operands, which §19.8.1
// makes roaming and free-ranging -- the XTSE3430 error-3420a expects, and the
// reasoning its own stylesheet comment spells out.

// accumAfterState is what the instruction walk knows about the position of a
// sequence constructor's current member, as far as §19.8.9.1 needs it.
//
// known is false where the walk cannot decide the case -- inside an
// xsl:accumulator-rule, or anywhere the enclosing sequence constructor was not
// identified -- and a call on fn:accumulator-after then stays unmodelled, the
// same "no opinion" the analysis gives any construct it does not describe.
type accumAfterState struct {
	known bool

	// precedingConsuming records rule 8's condition (b): some preceding
	// sibling of an enclosing node, within the same sequence constructor, is
	// consuming. When it is set, the call falls through to "otherwise,
	// motionless"; when it is not, the call is consuming.
	precedingConsuming bool
}

// accumulatorAfterSweep applies §19.8.9.1 to a call whose enclosing position
// the walk has described by st.
//
// ctx is the context posture at the call and ctxAllowsChildren says whether
// the context item can have children; rules 2 and 3 turn on exactly those.
// The second result is false when the rule cannot be decided, in which case
// the caller must leave the construct unmodelled.
func accumulatorAfterSweep(st accumAfterState, ctx posture, ctxAllowsChildren bool) (sweep, bool) {
	// Rule 2: "If the context posture is grounded, the function is
	// motionless." The target of the accumulator is not a streamed node, so
	// no streaming restriction applies.
	if ctx == postureGrounded {
		return sweepMotionless, true
	}
	// "If the context posture is climbing, the function is free-ranging."
	// The rule is not in the Last Call draft, whose cascade -- the eight
	// rules below -- cannot refuse accumulator-060's
	// "../accumulator-after('f:figNr')": the parent step is climbing and
	// motionless, the call after a consuming xsl:apply-templates is
	// motionless by rule 8, and the path is then grounded and motionless.
	// The Recommendation added the rule with the 2016 rework of this
	// section (the one accumulator-008s cites as "Bug 30018"), and the
	// reason is the case's own description: the target of the accumulator
	// is an ancestor of the node being processed, whose post-descent value
	// is not known until that ancestor's end event, which the stream has
	// not reached. It sits before the known check deliberately: the
	// position of the call in its sequence constructor is irrelevant when
	// the value asked for cannot exist yet, so an unmodelled position is no
	// reason to let the call through.
	if ctx == postureClimbing {
		return sweepFreeRanging, true
	}
	// Rule 3: "If the context item type has an empty intersection with
	// U{document-node(), element()} ... the function is motionless." Both
	// the pre-descent and post-descent values of a childless node are known
	// before any user construct sees the node.
	if !ctxAllowsChildren {
		return sweepMotionless, true
	}
	if !st.known {
		return sweepFreeRanging, false
	}
	// Rule 8, and the "otherwise" that closes the cascade.
	if st.precedingConsuming {
		return sweepMotionless, true
	}
	return sweepConsuming, true
}
