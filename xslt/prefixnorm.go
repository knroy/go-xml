package xslt

import (
	"github.com/knroy/go-xml/xdm"
)

// htmlNativeNamespaces are the three vocabularies an HTML5 parser understands
// without a prefix: HTML itself, SVG and MathML. Everything else is foreign
// and keeps whatever prefix the tree gave it.
var htmlNativeNamespaces = map[string]bool{
	nsXHTML:                              true,
	"http://www.w3.org/2000/svg":         true,
	"http://www.w3.org/1998/Math/MathML": true,
}

// normalizePrefixes rewrites a result tree for the HTML5 serialisation rules.
//
// An HTML5 parser recognises an element of those three namespaces only when
// it is written WITHOUT a prefix, so "prefix normalization" strips the prefix
// from every such element and removes any namespace node that binds a prefix
// to one of them, wherever it sits. A document that kept <svg:svg> would be
// read back as an unknown element in no namespace at all, which is the one
// case where the prefix changes what the document means.
//
// Attributes are left exactly as they are. An attribute has no default
// namespace to fall into -- an unprefixed attribute is in no namespace -- so
// removing its prefix would change which attribute it is. output-0603a puts
// svg:att on a MathML mi and requires both the prefix and the xmlns:svg
// declaration that its element then needs; the serializer writes that
// declaration back from the attribute's own name.
//
// The tree is copied rather than edited in place: a Result may be serialised
// more than once, and by a caller who is also navigating it.
func normalizePrefixes(seq xdm.Sequence) xdm.Sequence {
	if !sequenceNeedsPrefixNorm(seq) {
		return seq
	}
	out := make(xdm.Sequence, 0, len(seq))
	for _, it := range seq {
		if n, ok := it.(*xdm.Node); ok {
			out = append(out, normalizeNodePrefixes(n))
			continue
		}
		out = append(out, it)
	}
	return out
}

// sequenceNeedsPrefixNorm reports whether anything in seq would be changed,
// so that the common result -- which has no SVG or MathML in it and no
// prefixed XHTML -- is not copied for nothing.
func sequenceNeedsPrefixNorm(seq xdm.Sequence) bool {
	for _, it := range seq {
		if n, ok := it.(*xdm.Node); ok && nodeNeedsPrefixNorm(n) {
			return true
		}
	}
	return false
}

func nodeNeedsPrefixNorm(n *xdm.Node) bool {
	if n.Kind == xdm.KindElement {
		if n.Name.Prefix != "" && htmlNativeNamespaces[n.Name.URI] {
			return true
		}
		for _, ns := range n.Namespaces {
			if ns.Name.Local != "" && htmlNativeNamespaces[ns.Value] {
				return true
			}
		}
	}
	for _, c := range n.Children {
		if nodeNeedsPrefixNorm(c) {
			return true
		}
	}
	return false
}

// normalizeNodePrefixes copies n with the rewriting applied.
func normalizeNodePrefixes(n *xdm.Node) *xdm.Node {
	c := *n
	if c.Kind == xdm.KindElement {
		if htmlNativeNamespaces[c.Name.URI] {
			c.Name.Prefix = ""
		}
		// A namespace node binding a prefix to one of the three is removed
		// outright. The default binding is kept: it is how the element's own
		// unprefixed name is spelled, and dropping it would leave the
		// serializer to reinvent it lower down the tree than it belongs.
		var keep []*xdm.Node
		for _, ns := range c.Namespaces {
			if ns.Name.Local != "" && htmlNativeNamespaces[ns.Value] {
				continue
			}
			keep = append(keep, ns)
		}
		c.Namespaces = keep
	}
	if len(n.Children) > 0 {
		kids := make([]*xdm.Node, 0, len(n.Children))
		for _, k := range n.Children {
			nk := normalizeNodePrefixes(k)
			nk.Parent = &c
			kids = append(kids, nk)
		}
		c.Children = kids
	}
	return &c
}
