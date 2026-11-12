package xslt

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// oldGlobalReach is the globals-only walk markGlobalOrder made over the
// flat globalRefs list before functions became nodes of their own.
func oldGlobalReach(globals []*Variable) (selfRef, reaches map[*Variable]bool) {
	byName := map[string]*Variable{}
	for _, g := range globals {
		if _, dup := byName[g.Name.Clark()]; !dup {
			byName[g.Name.Clark()] = g
		}
	}
	mark := map[*Variable]int{}
	selfRef, reaches = map[*Variable]bool{}, map[*Variable]bool{}
	var walk func(g *Variable) bool
	walk = func(g *Variable) bool {
		switch mark[g] {
		case 1:
			return true
		case 2:
			return reaches[g]
		}
		mark[g] = 1
		r := false
		for _, dep := range globalRefs(g) {
			d, ok := byName[dep]
			if !ok {
				continue
			}
			if d == g {
				selfRef[g] = true
				continue
			}
			if walk(d) {
				r = true
			}
		}
		mark[g], reaches[g] = 2, r
		return r
	}
	for _, g := range globals {
		walk(g)
	}
	return
}

// TestSMB1GlobalOrderMatchesFlatWalk checks the function-node walk marks
// the same globals eager as the flat walk did, on random dependency graphs
// through globals and functions.
func TestSMB1GlobalOrderMatchesFlatWalk(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	const nG, nF = 6, 4
	for seed := range 300 {
		var b strings.Builder
		b.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f">`)
		pick := func(n int, f string) []string {
			var out []string
			for i := range n {
				if r.Intn(4) == 0 {
					out = append(out, fmt.Sprintf(f, i))
				}
			}
			return out
		}
		for i := range nG {
			parts := append(pick(nG, "$g%d"), pick(nF, "f:f%d()")...)
			parts = append(parts, "1")
			fmt.Fprintf(&b, `<xsl:variable name="g%d" select="(%s)"/>`, i, strings.Join(parts, ","))
		}
		for i := range nF {
			parts := append(pick(nG, "$g%d"), pick(nF, "f:f%d()")...)
			parts = append(parts, "1")
			fmt.Fprintf(&b, `<xsl:function name="f:f%d"><xsl:sequence select="(%s)"/></xsl:function>`, i, strings.Join(parts, ","))
		}
		b.WriteString(`<xsl:template name="main"/></xsl:stylesheet>`)
		tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		self, reach := oldGlobalReach(s.globals)
		for _, g := range s.globals {
			if want := self[g] || reach[g]; g.eager != want || g.selfRef != self[g] {
				t.Fatalf("seed %d: %s eager %v selfRef %v, want %v %v\n%s",
					seed, g.Name.Local, g.eager, g.selfRef, want, self[g], b.String())
			}
		}
	}
}

// TestSMB1FunctionRefsNotCopied checks that globals calling one function do
// not each retain a copy of the variables that function names.
func TestSMB1FunctionRefsNotCopied(t *testing.T) {
	const k = 200
	var b strings.Builder
	b.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f">`)
	refs := make([]string, k)
	for i := range k {
		fmt.Fprintf(&b, `<xsl:variable name="r%d" select="%d"/>`, i, i)
		refs[i] = fmt.Sprintf("$r%d", i)
	}
	fmt.Fprintf(&b, `<xsl:function name="f:f"><xsl:sequence select="(%s)"/></xsl:function>`, strings.Join(refs, ","))
	for i := range k {
		fmt.Fprintf(&b, `<xsl:variable name="g%d" select="f:f()"/>`, i)
	}
	b.WriteString(`<xsl:template name="main"><xsl:sequence select="$g0[1]"/></xsl:template></xsl:stylesheet>`)
	tree, _ := xdm.ParseString(b.String(), xdm.ParseOptions{})
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, g := range s.globals {
		held += len(g.bodyRefs) + len(g.calls)
	}
	if held > 2*k {
		t.Fatalf("globals retain %d references for k=%d, want at most %d", held, k, 2*k)
	}
	// The dependency is still seen: g0's refs come through f:f.
	for _, g := range s.globals {
		if g.Name.Local == "g0" && len(globalRefs(g)) != k {
			t.Fatalf("globalRefs(g0) = %d names, want %d", len(globalRefs(g)), k)
		}
	}
}
