package qt3

import "testing"

// TestMergeDepsOverridesFeaturePerValue pins the rule mergeDeps encodes, in
// both of the directions the two harnesses got wrong.
//
// The catalog idiom that motivates it is fn-load-xquery-module: the set
// declares the feature satisfied="true" and cases 901..914 override it to
// satisfied="false". Merged additively both copies survive and unsupportedSpec
// skips on the set's, so the override can never win and all fourteen cases
// vanish from the denominator. Merged wholesale -- the mistake that once cost
// tests/xslts 1,500 regex-syntax cases -- the set's <spec> gate vanishes
// instead, and cases run at versions they never claimed.
func TestMergeDepsOverridesFeaturePerValue(t *testing.T) {
	// The real shape of fn/load-xquery-module.xml.
	set := []Dependency{
		{Type: "spec", Value: "XP31+ XQ31+"},
		{Type: "feature", Value: "fn-load-xquery-module", Satisfied: "true"},
		{Type: "feature", Value: "higherOrderFunctions"},
	}
	tc := []Dependency{
		{Type: "feature", Value: "fn-load-xquery-module", Satisfied: "false"},
	}

	got := mergeDeps(set, tc)

	// The override wins: exactly one copy of the feature, and it is the
	// case's. Additive merging leaves two and the "true" one gates.
	var seen int
	for _, d := range got {
		if d.Type == "feature" && d.Value == "fn-load-xquery-module" {
			seen++
			if d.Satisfied != "false" {
				t.Errorf("feature satisfied = %q, want %q",
					d.Satisfied, "false")
			}
		}
	}
	if seen != 1 {
		t.Errorf("copies of the overridden feature = %d, want 1", seen)
	}

	// The set's other dependencies survive. A wholesale override drops the
	// spec gate here, which is the 1,500-case mistake.
	var spec, hof bool
	for _, d := range got {
		if d.Type == "spec" && d.Value == "XP31+ XQ31+" {
			spec = true
		}
		if d.Type == "feature" && d.Value == "higherOrderFunctions" {
			hof = true
		}
	}
	if !spec {
		t.Error("the set's spec gate was dropped")
	}
	if !hof {
		t.Error("the set's unrelated feature was dropped")
	}

	// And the gate agrees: the case is in scope for XQuery 3.1, where an
	// additive merge would skip it.
	if why := unsupportedSpec(got, XQuery31); why != "" {
		t.Errorf("case skipped as %q, want in scope", why)
	}
}

// TestMergeDepsKeepsBothSpecs holds the other half of the rule: a case whose
// spec value DIFFERS from its set's does not displace it, so both bind.
//
// This is what keeps the fix narrower than a per-kind override. Across the
// suite's set/case spec conflicts, additive and override diverge only ever in
// the direction an override would ADMIT, and admitting there would be wrong:
// a set saying XP31+ and a case saying XQ31+ are two constraints, not a
// replacement.
func TestMergeDepsKeepsBothSpecs(t *testing.T) {
	set := []Dependency{{Type: "spec", Value: "XQ10+"}}
	tc := []Dependency{{Type: "spec", Value: "XP20+ XQ10+"}}

	got := mergeDeps(set, tc)
	if len(got) != 2 {
		t.Fatalf("merged %d dependencies, want 2: %+v", len(got), got)
	}

	// The set's XQuery-only spec still excludes the case from an XPath 2.0
	// run, even though the case names XP20+ itself.
	if why := unsupportedSpec(got, XPath20); why == "" {
		t.Error("the set's spec gate stopped binding at XPath 2.0")
	}
}
