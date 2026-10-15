package xquery_test

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// ser renders a node the way an XML serializer would, for comparing what a
// constructor built against what the query said.
//
// The package does not depend on a serializer of its own: xslt has one, and
// depending on it here would invert the layering for the sake of tests.
func ser(n *xdm.Node) string {
	var sb strings.Builder
	writeNode(&sb, n)
	return sb.String()
}

func writeNode(sb *strings.Builder, n *xdm.Node) {
	switch n.Kind() {
	case xdm.KindText:
		sb.WriteString(escapeText(n.Value()))
	case xdm.KindComment:
		sb.WriteString("<!--" + n.Value() + "-->")
	case xdm.KindPI:
		sb.WriteString("<?" + n.Name().Local)
		if n.Value() != "" {
			sb.WriteString(" " + n.Value())
		}
		sb.WriteString("?>")
	case xdm.KindAttribute:
		sb.WriteString(" " + n.Name().Lexical() + `="` + escapeAttr(n.Value()) + `"`)
	case xdm.KindDocument:
		for c := range n.Children() {
			writeNode(sb, c)
		}
	case xdm.KindElement:
		sb.WriteString("<" + n.Name().Lexical())
		// A binding an ancestor already made is inherited, so writing it
		// again says the same thing twice. Every element carries a binding
		// for its own name, which on a nested element is usually one the
		// parent declared.
		var inherited map[string]string
		if n.Parent() != nil {
			inherited = n.Parent().InScopeNamespaces()
		}
		for prefix, uri := range n.DeclaredNamespaces() {
			if inherited[prefix] == uri {
				continue
			}
			if prefix == "" {
				sb.WriteString(` xmlns="` + uri + `"`)
			} else {
				sb.WriteString(` xmlns:` + prefix + `="` + uri + `"`)
			}
		}
		for a := range n.Attrs() {
			writeNode(sb, a)
		}
		if n.FirstChild() == nil {
			sb.WriteString("/>")
			return
		}
		sb.WriteString(">")
		for c := range n.Children() {
			writeNode(sb, c)
		}
		sb.WriteString("</" + n.Name().Lexical() + ">")
	}
}

func escapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func escapeAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;")
	return r.Replace(s)
}

// render renders a whole result sequence.
func render(seq xdm.Sequence) string {
	var sb strings.Builder
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			writeNode(&sb, v)
		case *xdm.Atomic:
			sb.WriteString(v.String())
		}
	}
	return sb.String()
}
