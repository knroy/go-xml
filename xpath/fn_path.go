package xpath

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// registerPathFunc adds fn:path, F&O 3.0 section 13.9.
//
// It returns a path expression that selects the node again from the root of
// its tree — the inverse of evaluating a path, and the reason it exists is
// diagnostics: an error message naming a node is far more useful when the
// name is something the reader can paste back into an expression.
//
// Every step is written in the braced-URI form Q{uri}local, which is what
// makes the result independent of whatever prefixes happened to be in scope
// where the node was written. A path built from prefixes would only work in
// a context that bound the same ones.
func registerPathFunc(l *Library) {
	l.registerFnSince(XPath30, "path", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args) == 0 {
			n, err := contextNodeArg(ctx)
			if err != nil {
				return nil, err
			}
			return strSeq(nodePath(n)), nil
		}
		// The empty sequence gives the empty sequence, not an error: the
		// parameter is node()? and asking for the path of nothing is a
		// question with no answer rather than a mistake.
		if len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		it, err := args[0].Single()
		if err != nil {
			return nil, err
		}
		n, ok := it.(*xdm.Node)
		if !ok {
			return nil, xdm.ErrType(
				"fn:path: expected a node, got %s", it.TypeName())
		}
		return strSeq(nodePath(n)), nil
	})
}

// nodePath builds the path expression for one node.
func nodePath(n *xdm.Node) string {
	// The steps are collected from the node upwards and reversed, because a
	// node knows its parent and not its children's positions.
	var steps []string
	cur := n
	for cur != nil && cur.Kind != xdm.KindDocument {
		p := cur.Parent
		if p == nil {
			break
		}
		steps = append(steps, pathStep(cur))
		cur = p
	}

	// The root reached decides the prefix. A path from a document node is
	// absolute and starts at "/"; one whose root is a parentless element or
	// any other node cannot be, so it is anchored with fn:root() applied to
	// the context item, which is what section 13.9 prescribes.
	prefix := ""
	if cur == nil || cur.Kind != xdm.KindDocument {
		prefix = "Q{" + xdm.NSFN + "}root()"
	}
	if len(steps) == 0 {
		if prefix != "" {
			return prefix
		}
		// The document node itself.
		return "/"
	}
	var b strings.Builder
	b.WriteString(prefix)
	for i := len(steps) - 1; i >= 0; i-- {
		b.WriteByte('/')
		b.WriteString(steps[i])
	}
	return b.String()
}

// pathStep writes the one step that selects n from its parent.
func pathStep(n *xdm.Node) string {
	switch n.Kind {
	case xdm.KindElement:
		return fmt.Sprintf("Q{%s}%s[%d]", n.Name.URI, n.Name.Local,
			positionAmongLikeNamed(n))
	case xdm.KindAttribute:
		// An attribute in no namespace is written bare: it has no position
		// because an element cannot carry two attributes of one name.
		if n.Name.URI == "" {
			return "@" + n.Name.Local
		}
		return fmt.Sprintf("@Q{%s}%s", n.Name.URI, n.Name.Local)
	case xdm.KindText:
		return fmt.Sprintf("text()[%d]", positionAmongLikeNamed(n))
	case xdm.KindComment:
		return fmt.Sprintf("comment()[%d]", positionAmongLikeNamed(n))
	case xdm.KindPI:
		return fmt.Sprintf("processing-instruction(%s)[%d]", n.Name.Local,
			positionAmongLikeNamed(n))
	case xdm.KindNamespace:
		// A namespace node's "name" is the prefix it binds. The default
		// namespace binds no prefix, so it has no name to select by and the
		// spec falls back to a predicate on the empty local name.
		if n.Name.Local == "" {
			return "namespace::*[Q{" + xdm.NSFN + "}local-name()=\"\"]"
		}
		return "namespace::" + n.Name.Local
	}
	return ""
}

// positionAmongLikeNamed counts n among its siblings of the same kind and
// name, one-based.
//
// "Like-named" is what makes the path select exactly one node: a step naming
// only the kind would match every sibling of that kind, so the position has
// to be counted over the same set the step selects — like-named elements for
// an element, all text nodes for a text node, like-named PIs for a PI.
func positionAmongLikeNamed(n *xdm.Node) int {
	p := n.Parent
	if p == nil {
		return 1
	}
	pos := 0
	for _, sib := range p.Children {
		if sib.Kind != n.Kind {
			continue
		}
		// Text and comment nodes are selected by kind alone, so every
		// sibling of the kind counts. Elements and PIs are selected by name
		// as well, so only the like-named ones do.
		switch n.Kind {
		case xdm.KindText, xdm.KindComment:
		case xdm.KindPI:
			if sib.Name.Local != n.Name.Local {
				continue
			}
		default:
			if sib.Name.URI != n.Name.URI || sib.Name.Local != n.Name.Local {
				continue
			}
		}
		pos++
		if sib == n {
			return pos
		}
	}
	return pos
}
