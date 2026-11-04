package xslt

import "github.com/knroy/go-xml/v2/xpath"

// viewLeafCopyOK admits copy-of() on a focus of kind vLeaf: an attribute or
// namespace node of a view, or a kept text, comment or PI copy, each a
// complete node with nothing below it. fn:copy-of copies the node alone (its
// name, value and annotation; an attribute or a leaf has no children to
// copy), so the copy is the tree's copy of the original: a new node either
// way, never one the outer can tell by identity from the tree's.
// sx-square-array-135's tail ancestor-or-self::*/@*/copy-of() is the case.
// snapshot() is not admitted: it copies the node's ancestors too, with
// their attributes and namespaces.
func viewLeafCopyOK(x *xpath.FuncCall, f vfocus) bool {
	return x.Name.Local == "copy-of" && len(x.Args) == 0 && f == vLeaf
}
