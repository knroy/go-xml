package xsd

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// validateWithStats runs a validation with the identity counters enabled.
// It duplicates the two lines ValidateContext uses to build a validator,
// which is the whole reason icStats is a field rather than an option: the
// counters are a measurement tool for this package, not API surface.
func validateWithStats(t *testing.T, s *Schema, root *xdm.Node) *icStats {
	t.Helper()
	st := &icStats{}
	icStatsHook = func() *icStats { return st }
	defer func() { icStatsHook = nil }()
	_ = s.Validate(root, ValidateOptions{})
	return st
}

// The identity evaluator's cost is not in any one traversal being slow; it is
// in the same nodes being traversed once per enclosing scope. Elapsed time
// cannot show that and a counter can, so this reports the ratio directly.
//
// The property to watch is SelectorEvals against depth. A constraint declared
// on a recursive element is evaluated once per level, so a one-pass design
// would hold selector evaluations near the number of scopes while the current
// design multiplies the nodes each one walks. NodesVisited over the node count
// is the amplification the review measured as 1.31 MB of input producing
// 17.7 GB of churn.
func TestIdentityConstraintWorkScales(t *testing.T) {
	st, err := xdm.ParseString(identityBenchSchema(), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	type row struct{ depth, width int }
	for _, r := range []row{
		{60, 0}, {120, 0}, {240, 0}, {480, 0},
		{200, 10}, {200, 20}, {200, 40},
	} {
		doc := identityBenchDoc(r.depth, r.width)
		tr, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("d=%d w=%d: parse: %v", r.depth, r.width, err)
		}
		// The number of elements in the document, for the ratio below.
		nodes := 0
		for _, n := range descendantsOrSelf(tr.Root.ChildElements()[0]) {
			_ = n
			nodes++
		}
		stats := validateWithStats(t, s, tr.Root)
		t.Logf("d=%-4d w=%-3d nodes=%-6d selectorEvals=%-6d fieldEvals=%-7d nodesVisited=%-9d targets=%-7d seeded=%-7d visited/node=%.1f",
			r.depth, r.width, nodes,
			stats.SelectorEvals, stats.FieldEvals, stats.NodesVisited,
			stats.Targets, stats.Seeded,
			float64(stats.NodesVisited)/float64(nodes))
	}
}

// Doubling the depth must not quadruple the nodes a selector walks, once the
// evaluator visits each node a bounded number of times. It does today, which
// is what the redesign has to change; this records the current ratio so the
// change is measurable rather than asserted.
func TestIdentityConstraintAmplification(t *testing.T) {
	st, _ := xdm.ParseString(identityBenchSchema(), xdm.ParseOptions{})
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var prev uint64
	for _, depth := range []int{120, 240, 480, 960} {
		tr, _ := xdm.ParseString(identityBenchDoc(depth, 0), xdm.ParseOptions{})
		stats := validateWithStats(t, s, tr.Root)
		ratio := "-"
		if prev != 0 {
			ratio = fmt.Sprintf("%.2fx", float64(stats.NodesVisited)/float64(prev))
		}
		t.Logf("depth=%-4d nodesVisited=%-9d growth on doubling: %s", depth, stats.NodesVisited, ratio)
		prev = stats.NodesVisited
	}
}
