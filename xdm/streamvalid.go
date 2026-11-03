package xdm

import "github.com/knroy/go-xml/v2/internal/xdmburst"

// A validator on the stream adds default attributes to the start tag being
// told (xdmburst.AppendAttr), as the parser adds an element's own.
func init() {
	xdmburst.AppendAttr = func(el any, prefix, uri, local, value string) any {
		n := el.(*Node)
		t := n.tree
		if n.Kind() != KindElement || t.frozen || n.v1 != noIdx || t.n != n.self+1+n.attrCount() {
			return nil
		}
		a := n.appendAttrRaw()
		a.name = t.intern(QName{Prefix: prefix, URI: uri, Local: local})
		if value != "" {
			a.v0, a.v1 = t.text.add(value)
		}
		return a
	}
}
