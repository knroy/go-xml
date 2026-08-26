package xpath

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// registerPathFunc adds fn:path, F&O 3.0 section 13.9.
//
// It returns a path expression that selects the node relative to the root of
// its containing document. The form is deliberately verbose — every name is
// written as a braced URI literal and every step carries a position — because
// the result has to select exactly one node when evaluated with no namespace
// bindings in scope. A prefix would need a binding the caller may not have.
func registerPathFunc(l *Library) {
	l.registerFnSince(XPath30, "path", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return xdm.Empty(), err
		}
		return strSeq(nodePath(n)), nil
	})
}

// nodePath builds the path expression for one node.
func nodePath(n *xdm.Node) string {
	if n.Kind == xdm.KindDocument {
		return "/"
	}

	// Steps are produced from the node upwards, then reversed: each needs its
	// position among its like-named siblings, which is a property of the node
	// rather than of the path so far.
	var steps []string
	root := n
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Kind == xdm.KindDocument {
			root = cur
			break
		}
		root = cur
		steps = append(steps, pathStep(cur))
	}

	// Reverse into document order.
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}

	var sb strings.Builder
	// A tree whose root is not a document node cannot be reached by a leading
	// "/", so the path names it with fn:root() instead.
	if root.Kind != xdm.KindDocument {
		sb.WriteString("Q{http://www.w3.org/2005/xpath-functions}root()")
	}
	for _, s := range steps {
		sb.WriteString("/")
		sb.WriteString(s)
	}
	return sb.String()
}

// pathStep renders the one step that selects n from its parent.
func pathStep(n *xdm.Node) string {
	switch n.Kind {
	case xdm.KindElement:
		return fmt.Sprintf("Q{%s}%s[%d]", n.Name.URI, n.Name.Local, siblingPosition(n))
	case xdm.KindAttribute:
		// An attribute is unique on its element, so it carries no position.
		if n.Name.URI == "" {
			return "@" + n.Name.Local
		}
		return fmt.Sprintf("@Q{%s}%s", n.Name.URI, n.Name.Local)
	case xdm.KindText:
		return fmt.Sprintf("text()[%d]", siblingPosition(n))
	case xdm.KindComment:
		return fmt.Sprintf("comment()[%d]", siblingPosition(n))
	case xdm.KindPI:
		return fmt.Sprintf("processing-instruction(%s)[%d]", n.Name.Local, siblingPosition(n))
	case xdm.KindNamespace:
		// A namespace node's name is its prefix; the default namespace has no
		// name, and is selected by testing for an empty local name.
		if n.Name.Local == "" {
			return `namespace::*[Q{http://www.w3.org/2005/xpath-functions}local-name()=""]`
		}
		return "namespace::" + n.Name.Local
	}
	return "node()"
}

// siblingPosition returns n's position among the siblings it shares a step
// with: like-named siblings for an element or PI, same-kind siblings for text
// and comments.
//
// One-based, and it counts only the siblings a step of the same form would
// select — which is what makes the generated path select exactly this node.
func siblingPosition(n *xdm.Node) int {
	if n.Parent == nil {
		return 1
	}
	pos := 0
	for _, sib := range n.Parent.Children {
		if !sameStepKind(n, sib) {
			continue
		}
		pos++
		if sib == n {
			return pos
		}
	}
	return pos
}

// sameStepKind reports whether two siblings would be selected by the same
// path step, and so share a position sequence.
func sameStepKind(a, b *xdm.Node) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case xdm.KindElement, xdm.KindPI:
		return a.Name == b.Name
	}
	return true
}
