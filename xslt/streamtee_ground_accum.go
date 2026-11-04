package xslt

// A grounded rule copy that carries its accumulator values (lane MM4,
// si-fork-816).
//
// A rule run on a grounded copy of its element (streamtee_ground.go) was
// refused when its module calls accumulator-before or -after: the copy's
// own tree would compute the accumulators over the copy, not over the
// document. The run's observer (streamaccum.go) computes them over the
// document as the cursor passes; Ground with the observer recording
// (groundAccums, streamaccum_ground_l4.go) pairs every node of the copy
// with its original's values, before and after, which fnAccumulator reads
// (accumHeld) for the copy's nodes and for copies made of them
// (accumOrigin). Memory: the values of the element's nodes, which the tree
// computes for them too. With no observer to record (no accumulators
// computed at that depth) the plan does not run: errStreamPlan.

import "github.com/knroy/go-xml/v2/xdm"

// groundAccumFns are the calls a rule copy carrying its values answers.
var groundAccumFns = []string{"accumulator-before", "accumulator-after"}

// groundRuleCopy grounds the element at the cursor, with its accumulator
// values when accums says the module reads them.
func (run *streamRun) groundRuleCopy(accums bool) (*xdm.Node, error) {
	if !accums {
		return run.c.Ground()
	}
	g, ok, err := groundAccums(run.c)
	if !ok {
		return nil, errStreamPlan
	}
	return g, err
}
