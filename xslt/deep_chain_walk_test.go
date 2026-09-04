package xslt

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Two walks up a type's derivation chain and one walk back along a copy's
// origin chain all stopped at a fixed step count. Each returned a definite
// answer on running out of steps rather than refusing, so each decided a legal
// input wrongly past its cliff.
//
// The counts are now visited sets, keyed on what the walk actually revisits —
// the annotation name for the two type walks, the node pointer for the copy
// walk. A repeated key is exactly the cycle each count was written to catch.

var deepChainDepths = []int{1, 2, 31, 32, 33, 64, 65, 128, 256, 512}

// registerXSLTChain builds T1 derived from base, ... Tn from T{n-1}, in ns.
// xdm's derivation registry is process-global, so every case gets a namespace
// naming its own label and depth.
func registerXSLTChain(ns, base string, n int) string {
	prev := base
	for i := 1; i <= n; i++ {
		name := xdm.AnnotationName(ns, fmt.Sprintf("T%d", i))
		xdm.RegisterDerivedType(name, prev)
		prev = name
	}
	return prev
}

// isNamespaceSensitiveType decides XTTE0950: whether copying a node detaches a
// QName from the binding its prefix needs. Truncation returned FALSE, which is
// the permissive verdict — a type 33 links above xs:QName stopped being
// namespace-sensitive, so xsl:copy-of with validation="preserve" copied an
// attribute the specification says it must refuse, and the copied value's
// prefix pointed at nothing.
func TestDeepChainIsNamespaceSensitiveType(t *testing.T) {
	for _, n := range deepChainDepths {
		for _, base := range []string{"QName", "NOTATION"} {
			ns := fmt.Sprintf("http://example.com/xsltchain/nss-%s/%d", base, n)
			top := registerXSLTChain(ns, base, n)
			if !isNamespaceSensitiveType(top) {
				t.Errorf("depth %d: a type %d links above xs:%s is not "+
					"namespace-sensitive, so XTTE0950 goes unreported",
					n, n, base)
			}
		}
		// A chain of the same length grounded in a type that carries no
		// binding must stay insensitive: the fix must not widen the verdict.
		plain := registerXSLTChain(
			fmt.Sprintf("http://example.com/xsltchain/nss-plain/%d", n),
			"Name", n)
		if isNamespaceSensitiveType(plain) {
			t.Errorf("depth %d: a type derived from xs:Name reported as "+
				"namespace-sensitive", n)
		}
	}
}

// namespaceSensitiveType decides XTTE1545: whether xsl:copy/xsl:copy-of may
// validate a CONSTRUCTED attribute against a named type. Section 19.2 forbids
// it for xs:QName and xs:NOTATION, because a constructed attribute's value is
// a string with no namespace context to resolve the prefix against.
// Truncation returned false, the permissive verdict, so the forbidden
// validation went ahead for a type past the cliff.
//
// It walks LOCAL names rather than annotation keys, so its chain is registered
// with unqualified names — which is what the function is handed in practice.
func TestDeepChainNamespaceSensitiveType(t *testing.T) {
	for _, n := range deepChainDepths {
		for _, base := range []string{"QName", "NOTATION"} {
			// Unqualified, so a per-case label keeps the process-global
			// registry from sharing names across depths.
			prev := base
			var top string
			for i := 1; i <= n; i++ {
				top = fmt.Sprintf("nsst_%s_%d_T%d", base, n, i)
				xdm.RegisterDerivedType(top, prev)
				prev = top
			}
			bad, why := namespaceSensitiveType(nil, xdm.QName{Local: top})
			if !bad {
				t.Errorf("depth %d: a type %d links above xs:%s is not "+
					"namespace-sensitive, so XTTE1545 goes unreported",
					n, n, base)
			}
			if bad && why == "" {
				t.Errorf("depth %d: reported sensitive with no reason", n)
			}
		}
	}
}

// The counts were nominally there so a schema whose derivations formed a cycle
// could not spin. A visited set has to keep that property.
func TestCyclicChainTerminatesXSLT(t *testing.T) {
	for _, n := range []int{1, 2, 33, 64, 128} {
		ns := fmt.Sprintf("http://example.com/xsltchain/cycle/%d", n)
		names := make([]string, n+1)
		for i := 1; i <= n; i++ {
			names[i] = xdm.AnnotationName(ns, fmt.Sprintf("C%d", i))
		}
		for i := 2; i <= n; i++ {
			xdm.RegisterDerivedType(names[i], names[i-1])
		}
		xdm.RegisterDerivedType(names[1], names[n])
		if isNamespaceSensitiveType(names[n]) {
			t.Errorf("n=%d: a cyclic chain touching no QName reported as "+
				"namespace-sensitive", n)
		}

		// The same ring in unqualified names, for the local-name walk.
		locals := make([]string, n+1)
		for i := 1; i <= n; i++ {
			locals[i] = fmt.Sprintf("cyc_%d_L%d", n, i)
		}
		for i := 2; i <= n; i++ {
			xdm.RegisterDerivedType(locals[i], locals[i-1])
		}
		xdm.RegisterDerivedType(locals[1], locals[n])
		if bad, _ := namespaceSensitiveType(nil, xdm.QName{Local: locals[n]}); bad {
			t.Errorf("n=%d: a cyclic local chain reported as "+
				"namespace-sensitive", n)
		}
	}
}

// accumulatorOrigin follows a node produced by a copy-accumulators="yes" copy
// back to the node it was copied from. Section 18.3 makes an accumulator
// answer about the copy with the ORIGINAL's value, and the original is the far
// end of the chain — a copy of a copy still has to reach it.
//
// The walk stopped after 64 steps and returned whatever node it had reached,
// which is not a refusal but a definite WRONG answer: an intermediate copy,
// living in a tree of its own where the accumulator computes something else
// entirely. A stylesheet that copies a copy 65 times is legal and got a
// legal-looking wrong number.
func TestAccumulatorOriginDeepCopyChain(t *testing.T) {
	for _, n := range []int{1, 2, 31, 32, 33, 63, 64, 65, 128, 256, 512} {
		rt := &runtime{accumOrigin: map[*xdm.Node]*xdm.Node{}}

		// nodes[0] is the original; nodes[i] is the i'th successive copy.
		nodes := make([]*xdm.Node, n+1)
		for i := range nodes {
			nodes[i] = &xdm.Node{
				Kind: xdm.KindElement,
				Name: xdm.QName{Local: fmt.Sprintf("c%d", i)},
			}
		}
		for i := 1; i <= n; i++ {
			rt.accumOrigin[nodes[i]] = nodes[i-1]
		}

		if got := rt.accumulatorOrigin(nodes[n]); got != nodes[0] {
			t.Errorf("copies=%d: origin of the last copy is %s, want the "+
				"original c0 — the accumulator reports the wrong node's value",
				n, got.Name.Local)
		}
		// Every intermediate resolves to the same original, and the original
		// resolves to itself.
		if got := rt.accumulatorOrigin(nodes[0]); got != nodes[0] {
			t.Errorf("copies=%d: the original does not resolve to itself", n)
		}
		mid := n / 2
		if got := rt.accumulatorOrigin(nodes[mid]); got != nodes[0] {
			t.Errorf("copies=%d: copy %d resolves to %s, want c0",
				n, mid, got.Name.Local)
		}
	}
}

// A node not recorded as a copy is its own origin, and an origin chain that
// somehow closed on itself must stop rather than hang. The count was
// nominally the guard for the second; the visited set has to keep it.
func TestAccumulatorOriginCyclicTerminates(t *testing.T) {
	for _, n := range []int{1, 2, 65, 128} {
		rt := &runtime{accumOrigin: map[*xdm.Node]*xdm.Node{}}
		nodes := make([]*xdm.Node, n)
		for i := range nodes {
			nodes[i] = &xdm.Node{Kind: xdm.KindElement}
		}
		for i := range nodes {
			rt.accumOrigin[nodes[i]] = nodes[(i+1)%n]
		}
		got := rt.accumulatorOrigin(nodes[0])
		found := false
		for _, nd := range nodes {
			if nd == got {
				found = true
			}
		}
		if !found {
			t.Errorf("n=%d: a cyclic origin chain returned a node outside "+
				"the cycle", n)
		}
	}
}
