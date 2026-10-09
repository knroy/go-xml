package xpath

import (
	"sync"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
)

func init() {
	xpathleaf.NewStepMemo = func() any { return &stepMemo{} }
}

// stepMemo remembers, for one XSLT transform, which nodes of a parsed
// document have a child of a given name, so that "//x[p]" stops walking the
// whole document every time it is evaluated.
//
// "//x[p]" is descendant-or-self::node() followed by child::x[p], and the
// predicate keeps the two steps from being fused into descendant::x[p]: it
// numbers x among its siblings. Run as written, the first step materialises
// every node of the document and the second visits each one's children. A
// compiled Schematron rule set evaluates such a path once per assertion per
// rule context, and the CEN rules spend about a third of their transform
// there. Only the nodes with a matching child contribute anything to the
// second step: on any other node it selects nothing, so its predicates are
// never evaluated and it raises nothing. Handing the second step just those
// nodes, in the same document order, gives it the same input groups, the
// same positions and sizes, and so the same result, errors and all.
//
// Only a document node of a parsed tree is memoised. A parsed tree is not
// changed while a transform runs, which is the assumption the xsl:key index
// already makes; temporary and result trees are built by the transform, and
// any tree can be changed by a caller through Node's exported fields between
// transforms. The memo lives on the XSLT transform's runtime (see
// xpathleaf.StepMemoHost), so it is released with the transform, and nothing
// outside XSLT sees it.
//
// The steps involved charge no budget, read no focus and raise no error, so
// a hit is observably the walk it replaces.
//
// A function item made in one transform carries its runtime, and so this
// memo, into whatever calls it, which may be several goroutines at once: the
// lock is for that.
type stepMemo struct {
	mu      sync.Mutex
	parents map[namedParentsKey]xdm.Sequence
	held    int // items held across all entries
}

// stepMemoMaxItems bounds what one transform's memo holds, at 16 bytes an
// item. A list that would pass it is still used, just not kept.
const stepMemoMaxItems = 1 << 20

type namedParentsKey struct {
	doc              *xdm.Node
	uri, local       string
	anyURI, anyLocal bool
}

// namedParents answers steps[i] when it is descendant-or-self::node() over a
// parsed document node and steps[i+1] is a child step with a name test: it
// returns the nodes of descendant-or-self::node() that have a child the test
// matches, in document order. ok is false for anything else, which is then
// evaluated as written.
func namedParents(ctx *Context, cur xdm.Sequence, steps []Expr, i int) (xdm.Sequence, bool) {
	dos, ok := steps[i].(*Step)
	if !ok || dos.Axis != AxisDescendantOrSelf || len(dos.Predicates) != 0 ||
		len(cur) != 1 || i+1 >= len(steps) || ctx.host == nil {
		return nil, false
	}
	doc, ok := cur[0].(*xdm.Node)
	if !ok || doc.Kind != xdm.KindDocument || doc.Tree() == nil || doc.Tree().XMLVersion == "" {
		return nil, false
	}
	if kt, ok := dos.Test.(*KindTest); !ok || !kt.Any {
		return nil, false
	}
	child, ok := steps[i+1].(*Step)
	if !ok || child.Axis != AxisChild {
		return nil, false
	}
	nt, ok := child.Test.(*NameTest)
	if !ok {
		return nil, false
	}
	h, ok := ctx.host.Runtime.(xpathleaf.StepMemoHost)
	if !ok {
		return nil, false
	}
	m, ok := h.StepMemo().(*stepMemo)
	if !ok {
		return nil, false
	}
	k := namedParentsKey{doc, nt.Name.URI, nt.Name.Local, nt.AnyURI, nt.AnyLocal}
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.parents[k]; ok {
		return v, true
	}
	v := appendNamedParents(nil, doc, nt)
	v = v[:len(v):len(v)]
	if m.held+len(v) <= stepMemoMaxItems {
		if m.parents == nil {
			m.parents = map[namedParentsKey]xdm.Sequence{}
		}
		m.parents[k] = v
		m.held += len(v)
	}
	return v, true
}

// appendNamedParents appends n, then each of its descendants, that has an
// element child t matches: descendant-or-self::node()[child::t] in document
// order. Attributes and namespaces have no children, so they never qualify.
func appendNamedParents(out xdm.Sequence, n *xdm.Node, t *NameTest) xdm.Sequence {
	for _, c := range n.Children {
		if t.Matches(c, xdm.KindElement) {
			out = append(out, n)
			break
		}
	}
	for _, c := range n.Children {
		if c.NumChildren() > 0 {
			out = appendNamedParents(out, c, t)
		}
	}
	return out
}
