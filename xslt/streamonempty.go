package xslt

// xsl:on-empty and xsl:on-non-empty in a streamed sequence constructor
// (lane g1, bucket OE).
//
// §8.4 has no streaming rules of its own, and §19.8.4 assesses both as
// xsl:sequence. The tree's algorithm (execConditionalSequence, §8.4.4) runs
// the instructions in order, each into a builder of its own, and evaluates
// a deferred xsl:on-non-empty when the first non-vacuous item arrives, an
// xsl:on-empty after the rest. §19.8 allows one consuming instruction per
// sequence constructor, so when xsl:on-empty or xsl:on-non-empty is it,
// everything it waits for is motionless: the cursor is still at the start
// of the context when it reads, and what it never reads is skipped as any
// unread content is. So the planner takes them as it takes xsl:sequence (a
// select op, or a sequence constructor), and the tree's algorithm runs
// unchanged. Memory: R is buffered until the decision as the tree buffers
// it; the stream holds no source tree beside it.

// appendEval evaluates the select f feeds and appends it as xsl:sequence
// does (evalConditionalContent).
func (f *burstFeed) appendEval(rt *runtime, out *outputBuilder) error {
	if f.eval == nil {
		return errStreamPlan
	}
	seq, err := f.eval(rt)
	if err != nil {
		return err
	}
	return appendSequence(seq, out)
}
