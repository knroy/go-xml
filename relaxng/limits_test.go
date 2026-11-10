package relaxng

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// includeChain is a resolver whose g0.rng includes g1.rng and so on, n
// includes deep, ending in a grammar that defines a start.
func includeChain(n int) *mapResolver {
	docs := map[string]string{}
	for i := 0; i < n; i++ {
		docs[fmt.Sprintf("g%d.rng", i)] = fmt.Sprintf(
			`<grammar`+rngNS+`><include href="g%d.rng"/></grammar>`, i+1)
	}
	docs[fmt.Sprintf("g%d.rng", n)] = `<grammar` + rngNS + `>
		<start><element name="doc"><empty/></element></start></grammar>`
	return &mapResolver{docs: docs}
}

const includeG0 = `<grammar` + rngNS + `><include href="g0.rng"/></grammar>`

// MaxIncludeDepth moves the include bound either way; zero and negative keep
// the default, and a value past the ceiling is clamped, since a chain of
// includes recurses in Go.
func TestOptionsMaxIncludeDepth(t *testing.T) {
	for _, c := range []struct {
		chain, opt int
		ok         bool
	}{
		{DefaultMaxIncludeDepth - 1, 0, true}, {DefaultMaxIncludeDepth + 1, 0, false},
		{DefaultMaxIncludeDepth + 1, -1, false},
		{DefaultMaxIncludeDepth + 5, 50, true}, {52, 50, false},
		{5, 3, false},
		{maxIncludeDepthCeiling - 1, 1 << 30, true}, {maxIncludeDepthCeiling + 1, 1 << 30, false},
	} {
		_, err := compileWith(t, includeG0, Options{Resolver: includeChain(c.chain), MaxIncludeDepth: c.opt})
		if c.ok && err != nil {
			t.Errorf("MaxIncludeDepth=%d, chain %d: refused: %v", c.opt, c.chain, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "resource limit exceeded")) {
			t.Errorf("MaxIncludeDepth=%d, chain %d: err = %v, want a resource limit", c.opt, c.chain, err)
		}
	}
}

// The fetch count and the fetched-document budget are options: zero is
// today's bound, a lowered value refuses, a negative one lifts it.
func TestOptionsFetchBudgets(t *testing.T) {
	for _, c := range []struct {
		name string
		opts Options
		ok   bool
	}{
		{"defaults", Options{}, true},
		{"MaxSchemaFetches 2", Options{MaxSchemaFetches: 2}, false},
		{"MaxSchemaFetches -1", Options{MaxSchemaFetches: -1}, true},
		{"MaxFetchedNodes 3", Options{MaxFetchedNodes: 3}, false},
		{"MaxFetchedNodes -1", Options{MaxFetchedNodes: -1}, true},
		{"MaxFetchedBytes 1", Options{MaxFetchedBytes: 1}, false},
		{"MaxFetchedBytes -1", Options{MaxFetchedBytes: -1}, true},
		{"MaxInt", Options{MaxSchemaFetches: math.MaxInt, MaxFetchedNodes: math.MaxInt,
			MaxFetchedBytes: math.MaxInt64, MaxRefExpansions: math.MaxInt, MaxIncludeDepth: math.MaxInt}, true},
	} {
		o := c.opts
		o.Resolver = includeChain(4)
		_, err := compileWith(t, includeG0, o)
		if c.ok && err != nil {
			t.Errorf("%s: refused: %v", c.name, err)
		}
		if !c.ok && !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: err = %v, want xdm.ErrResourceLimit", c.name, err)
		}
	}
}

// MaxRefExpansions bounds the <ref> expansions, and an <externalRef>'s own
// compilation runs under it too rather than under the default.
func TestOptionsMaxRefExpansions(t *testing.T) {
	const grammar = `<grammar` + rngNS + `>
		<start><element name="doc"><ref name="a"/></element></start>
		<define name="a"><ref name="b"/></define>
		<define name="b"><empty/></define></grammar>`
	for _, c := range []struct {
		opt int
		ok  bool
	}{{0, true}, {-1, true}, {2, true}, {1, false}} {
		_, err := compileWith(t, grammar, Options{MaxRefExpansions: c.opt})
		if c.ok != (err == nil) {
			t.Errorf("MaxRefExpansions=%d: err = %v, want ok=%v", c.opt, err, c.ok)
		}
		_, err = compileWith(t, `<element`+rngNS+` name="top"><externalRef href="x.rng"/></element>`,
			Options{Resolver: &mapResolver{docs: map[string]string{"x.rng": grammar}}, MaxRefExpansions: c.opt})
		if c.ok != (err == nil) {
			t.Errorf("MaxRefExpansions=%d through externalRef: err = %v, want ok=%v", c.opt, err, c.ok)
		}
	}
}
