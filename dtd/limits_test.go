package dtd

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// peChain is an external subset whose declaration-level reference nests n
// substitutions deep before it reaches a declaration: the reference to e0,
// then n-1 entities each naming the next.
func peChain(n int) string {
	n--
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "<!ENTITY %% e%d '%%e%d;'>\n", i, i+1)
	}
	fmt.Fprintf(&b, "<!ENTITY %% e%d '<!ELEMENT r EMPTY>'>\n%%e0;\n", n)
	return b.String()
}

// sectionChain is an external subset declaring r inside n nested INCLUDE
// sections.
func sectionChain(n int) string {
	return strings.Repeat("<![INCLUDE[", n) + "<!ELEMENT r EMPTY>" + strings.Repeat("]]>", n)
}

// The two recursion bounds of Load are options now: zero and negative keep
// the default of 40, a positive value moves the bound either way, and a value
// past maxDepthCeiling is clamped to it rather than allowed to recurse into a
// fatal stack overflow.
func TestLoadDepthOptions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chain func(int) string
		set   func(*LoadOptions, int)
	}{
		{"MaxParameterDepth", peChain, func(o *LoadOptions, v int) { o.MaxParameterDepth = v }},
		{"MaxConditionalDepth", sectionChain, func(o *LoadOptions, v int) { o.MaxConditionalDepth = v }},
	} {
		load := func(depth, opt int) error {
			o := mapOpts(map[string]string{"r.dtd": tc.chain(depth)})
			tc.set(&o, opt)
			_, err := Load(`DOCTYPE r SYSTEM "r.dtd"`, o)
			return err
		}
		for _, c := range []struct {
			depth, opt int
			ok         bool
		}{
			{40, 0, true}, {41, 0, false}, // the default, unchanged
			{41, -1, false},                 // negative is the default, not unlimited
			{41, 50, true}, {51, 50, false}, // raised
			{5, 4, false},                                                           // lowered
			{maxDepthCeiling, 1 << 30, true}, {maxDepthCeiling + 1, 1 << 30, false}, // clamped
			{maxDepthCeiling + 1, math.MaxInt, false},
		} {
			err := load(c.depth, c.opt)
			if c.ok && err != nil {
				t.Errorf("%s=%d, depth %d: refused: %v", tc.name, c.opt, c.depth, err)
			}
			if !c.ok && !errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("%s=%d, depth %d: err = %v, want a resource limit", tc.name, c.opt, c.depth, err)
			}
		}
	}
}

// The content-model bounds Validate applies are options now, and reach the
// matcher: zero keeps today's refusal of a dense model, a negative value lifts
// it, and a lowered positions bound refuses a model the default admits.
func TestValidateContentModelOptions(t *testing.T) {
	dense := func(n int) string {
		return `<!DOCTYPE r [<!ELEMENT r (` + strings.TrimSuffix(strings.Repeat("a?,", n), ",") +
			`)><!ELEMENT a EMPTY>]><r><a/></r>`
	}
	validate := func(src string, opts Options) error {
		tree, err := xdm.ParseString(src, xdm.ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Fatal(err)
		}
		d, err := Parse(tree.DocType)
		if err != nil {
			t.Fatal(err)
		}
		return Validate(tree.Root, d, opts)
	}
	if err := validate(dense(2000), Options{}); err == nil {
		t.Error("default: a 2000-position dense model should exceed the follow-entry bound")
	}
	if err := validate(dense(2000), Options{MaxContentModelEdges: -1}); err != nil {
		t.Errorf("MaxContentModelEdges -1 should admit the dense model: %v", err)
	}
	if err := validate(dense(10), Options{}); err != nil {
		t.Errorf("default: a 10-position model should validate: %v", err)
	}
	if err := validate(dense(10), Options{MaxContentModelPositions: 5}); err == nil {
		t.Error("MaxContentModelPositions 5 should refuse a 10-position model")
	}
	if err := validate(dense(10), Options{MaxContentModelEdges: 3}); err == nil {
		t.Error("MaxContentModelEdges 3 should refuse a 10-position dense model")
	}
	// After an a, (a?,a?) is in two states at once, one per position the a
	// could have taken; a budget of one cannot follow both.
	const two = `<!DOCTYPE r [<!ELEMENT r (a?,a?)><!ELEMENT a EMPTY>]><r><a/></r>`
	if err := validate(two, Options{}); err != nil {
		t.Errorf("default: (a?,a?) should admit one a: %v", err)
	}
	if err := validate(two, Options{MaxMatchStates: 1}); err == nil {
		t.Error("MaxMatchStates 1 should refuse a sequence that needs two states")
	}
	if err := validate(two, Options{MaxMatchStates: -1}); err != nil {
		t.Errorf("MaxMatchStates -1 should admit it: %v", err)
	}
}
