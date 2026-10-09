package xslt

import "github.com/knroy/go-xml/v2/xdm"

// pkgEdits records what composition changes about the declarations of a used
// package: the attributes a declaration gains, loses or has rewritten, a
// rename, namespace bindings added. The package's own tree is never edited --
// a resolver shares it -- so the changes are kept here, read back through
// attrValue while composition still needs them, and applied by rebuild, which
// makes the tree that is compiled.
type pkgEdits map[*xdm.Node]*declEdit

// declEdit is the change to one element. attrs, once set, is the element's
// whole attribute list in order.
type declEdit struct {
	name  *xdm.QName
	attrs []editAttr
	ns    [][2]string
}

// editAttr is one attribute of an edited element: from is the attribute it
// copies, or nil for one composition added.
type editAttr struct {
	name  xdm.QName
	value string
	from  *xdm.Node
}

func (e pkgEdits) edit(el *xdm.Node) *declEdit {
	d := e[el]
	if d == nil {
		d = &declEdit{}
		e[el] = d
	}
	return d
}

// attrList is el's attribute list as composition has left it so far.
func (e pkgEdits) attrList(el *xdm.Node) []editAttr {
	if d := e[el]; d != nil && d.attrs != nil {
		return d.attrs
	}
	out := make([]editAttr, 0, el.NumAttrs())
	for a := range el.Attrs() {
		out = append(out, editAttr{name: a.Name(), value: a.Value(), from: a})
	}
	return out
}

func (e pkgEdits) ownAttrs(el *xdm.Node) *declEdit {
	d := e.edit(el)
	if d.attrs == nil {
		d.attrs = e.attrList(el)
	}
	return d
}

// attrValue is el.AttrValue(local) with composition's changes applied.
func (e pkgEdits) attrValue(el *xdm.Node, local string) string {
	for _, a := range e.attrList(el) {
		if a.name.URI == "" && a.name.Local == local {
			return a.value
		}
	}
	return ""
}

func (e pkgEdits) hasAttr(el *xdm.Node, local string) bool {
	for _, a := range e.attrList(el) {
		if a.name.URI == "" && a.name.Local == local {
			return true
		}
	}
	return false
}

// setAttr sets or replaces an unprefixed attribute of an element.
func (e pkgEdits) setAttr(el *xdm.Node, local, value string) {
	d := e.ownAttrs(el)
	for i := range d.attrs {
		if d.attrs[i].name.URI == "" && d.attrs[i].name.Local == local {
			d.attrs[i].value = value
			return
		}
	}
	d.attrs = append(d.attrs, editAttr{name: xdm.QName{Local: local}, value: value})
}

// addAttr adds an attribute after el's others.
func (e pkgEdits) addAttr(el *xdm.Node, name xdm.QName, value string) {
	d := e.ownAttrs(el)
	d.attrs = append(d.attrs, editAttr{name: name, value: value})
}

// dropAttrs removes unprefixed attributes from an element.
func (e pkgEdits) dropAttrs(el *xdm.Node, names ...string) {
	d := e.ownAttrs(el)
	kept := d.attrs[:0]
	for _, a := range d.attrs {
		drop := false
		if a.name.URI == "" {
			for _, n := range names {
				if a.name.Local == n {
					drop = true
					break
				}
			}
		}
		if !drop {
			kept = append(kept, a)
		}
	}
	d.attrs = kept
}

func (e pkgEdits) rename(el *xdm.Node, name xdm.QName) { e.edit(el).name = &name }

func (e pkgEdits) addNS(el *xdm.Node, prefix, uri string) {
	d := e.edit(el)
	d.ns = append(d.ns, [2]string{prefix, uri})
}

// rebuild makes the tree a used package is compiled from: a copy of doc whose
// document element root has children kept, in order, each copied with its
// edits applied. kept may hold declarations from another tree -- the
// overriding declarations of the using package -- which become children of
// the copy. Source positions travel with every node.
//
// The bookkeeping kept by node for declarations follows them to their copies.
func (e pkgEdits) rebuild(doc, root *xdm.Node, kept []*xdm.Node) (newDoc, newRoot *xdm.Node) {
	tree := xdm.NewTree()
	tree.CopySourceFrom(root.Tree())
	shell := tree.Root
	if doc.Kind() == xdm.KindDocument {
		shell.SetBaseURI(doc.BaseURI())
		shell.SetDocumentURI(doc.DocumentURI())
		shell.CopyTypingFrom(doc)
	}
	copies := map[*xdm.Node]*xdm.Node{}
	place := func(parent *xdm.Node) {
		newRoot = copyStylesheetElement(parent, root)
		for _, k := range kept {
			copies[k] = e.copyInto(newRoot, k)
		}
	}
	if doc.Kind() == xdm.KindDocument && doc != root {
		for ch := range doc.Children() {
			if ch == root {
				place(shell)
			} else {
				copyStylesheetNode(shell, ch)
			}
		}
	} else {
		place(shell)
	}
	tree.Finalize()
	for orig, cp := range copies {
		if pkg, ok := overridingDecls[orig]; ok {
			overridingDecls[cp] = pkg
		}
		if v, ok := composedVisibility[orig]; ok {
			composedVisibility[cp] = v
		}
	}
	return shell, newRoot
}

// copyInto appends to parent a deep copy of n with e's changes applied.
func (e pkgEdits) copyInto(parent, n *xdm.Node) *xdm.Node {
	if n.Kind() != xdm.KindElement {
		return copyStylesheetNode(parent, n)
	}
	d := e[n]
	if d == nil {
		return copyStylesheetNode(parent, n)
	}
	c := parent.AppendShallowCopy(n)
	xdm.CopyPosition(c, n)
	if d.name != nil {
		c.SetName(*d.name)
	}
	for ns := range n.NamespaceDecls() {
		c.AddNamespace(ns.Name().Local, ns.Value())
	}
	for _, b := range d.ns {
		c.AddNamespace(b[0], b[1])
	}
	for _, a := range e.attrList(n) {
		if a.from == nil {
			c.AppendAttr(a.name, a.value)
			continue
		}
		ac := c.AppendShallowCopy(a.from)
		ac.SetValue(a.value)
		xdm.CopyPosition(ac, a.from)
	}
	for ch := range n.Children() {
		e.copyInto(c, ch)
	}
	return c
}
