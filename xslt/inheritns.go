package xslt

import (
	"sort"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// blockNamespaceInheritance implements [xsl:]inherit-namespaces="no" on the
// element el has just finished building.
//
// §5.8.1 makes namespace inheritance a property of complex content
// construction rather than of the tree: the children of a new element normally
// acquire its namespace nodes, and inherit-namespaces="no" stops them. The
// XDM has no separate place to record that, because in-scope namespaces are
// derived by walking ancestors — so the only way to express "this child does
// not have that binding" is the undeclaration the note in §5.8.1 points at,
// xmlns:foo="" as XML 1.1 writes it. InScopeNamespaces already reads an
// empty-valued namespace node that way.
//
// Only the direct children are touched. A grandchild inherits from its own
// parent, whose bindings are whatever survived this pass, so the effect
// propagates without walking the subtree.
// noAttr reads a boolean-valued XSLT attribute that defaults to yes,
// reporting whether it is present and says no.
//
// The value space is XSLT's boolean vocabulary, not the literal string "no":
// a 3.0 module may write false or 0, and copy-0613 does exactly that by
// feeding _inherit-namespaces a static parameter whose value is "false".
func noAttr(el *xdm.Node, name string) bool {
	a := el.Attr("", name)
	return a != nil && !yesAttr(el, name)
}

func blockNamespaceInheritance(el *xdm.Node) {
	if el == nil || el.Kind != xdm.KindElement {
		return
	}
	scope := el.InScopeNamespaces()
	prefixes := make([]string, 0, len(scope))
	for p := range scope {
		// The xml prefix is bound everywhere by the XML Namespaces
		// specification and cannot be undeclared.
		if p != "xml" {
			prefixes = append(prefixes, p)
		}
	}
	// A stable order, for the same reason copyNamespacesTo sorts.
	sort.Strings(prefixes)

	for _, child := range el.Children {
		if child.Kind != xdm.KindElement {
			continue
		}
		declared := map[string]bool{}
		for _, ns := range child.Namespaces {
			declared[ns.Name.Local] = true
		}
		for _, p := range prefixes {
			if declared[p] {
				continue
			}
			// A binding the child's own name or one of its attributes uses
			// is not inherited decoration — undeclaring it would leave the
			// name unserialisable. Namespace fixup would only put it back.
			if usedByNames(child, p, scope[p]) {
				continue
			}
			child.AddNamespace(p, "")
		}
	}
}

// usedByNames reports whether the prefix/URI pair is the binding that el's own
// name or one of its attribute names depends on.
func usedByNames(el *xdm.Node, prefix, uri string) bool {
	if el.Name.URI == uri && el.Name.Prefix == prefix {
		return true
	}
	for _, a := range el.Attrs {
		if a.Name.URI == uri && a.Name.Prefix == prefix {
			return true
		}
	}
	return false
}

// regexDialect is the version of the regular-expression grammar in force,
// which xpath.Context exposes only through its two public fields.
func regexDialect(ctx *xpath.Context) xpath.Version {
	if ctx == nil {
		return xpath.XPath20
	}
	if ctx.RegexVersion > ctx.Version {
		return ctx.RegexVersion
	}
	return ctx.Version
}
