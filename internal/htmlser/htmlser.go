// Package htmlser holds the HTML rules of Serialization 3.1 that both
// serialisers apply: the full one in xslt and fn:serialize's in xpath. xslt
// imports xpath, so neither can hold them for the other.
package htmlser

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// NSXHTML is the XHTML namespace.
const NSXHTML = "http://www.w3.org/1999/xhtml"

// Inline reports whether n is an inline element in the sense of Serialization
// 3.1 §7.4.3 (html) and §6.1.4 (xhtml), next to which indentation "MUST NOT"
// add whitespace: an element the method treats as HTML whose name is in
// inlineNames. The html method treats an element in no namespace as HTML, and
// one in the XHTML namespace under HTML5, matching names without regard to
// case; the xhtml method one in the XHTML namespace, or in none under HTML5.
// ponytail: area, link and meta, phrasing only in some positions, are left
// out.
func Inline(n *xdm.Node, xhtml, html5 bool) bool {
	if n.Kind != xdm.KindElement {
		return false
	}
	local := n.Name.Local
	switch {
	case xhtml && n.Name.URI == NSXHTML:
	case n.Name.URI == "" && (!xhtml || html5),
		!xhtml && html5 && n.Name.URI == NSXHTML:
		local = strings.ToLower(local)
	default:
		return false
	}
	return inlineNames[local]
}

// inlineNames is the union §7.4.3 names: the HTML 4.01 %inline elements
// (%fontstyle, %phrase, %special, %formctrl) and HTML5's phrasing content.
var inlineNames = map[string]bool{
	// HTML 4.01 %inline.
	"tt": true, "i": true, "b": true, "u": true, "s": true, "strike": true,
	"big": true, "small": true, "em": true, "strong": true, "dfn": true,
	"code": true, "samp": true, "kbd": true, "var": true, "cite": true,
	"abbr": true, "acronym": true, "a": true, "img": true, "applet": true,
	"object": true, "font": true, "basefont": true, "br": true,
	"script": true, "map": true, "q": true, "sub": true, "sup": true,
	"span": true, "bdo": true, "iframe": true, "input": true, "select": true,
	"textarea": true, "label": true, "button": true,
	// ins and del are inline only without element children; Saxon treats
	// them as inline always, which only withholds whitespace the spec permits.
	"ins": true, "del": true,
	// HTML5 phrasing content not already listed.
	"audio": true, "bdi": true, "canvas": true, "data": true,
	"datalist": true, "embed": true, "mark": true, "math": true,
	"meter": true, "noscript": true, "output": true, "picture": true,
	"progress": true, "ruby": true, "slot": true, "svg": true,
	"template": true, "time": true, "video": true, "wbr": true,
}

// SkipIndentBefore reports whether no indent may go before child i of n's
// children (i == len(children) meaning before n's end tag): next to an inline
// child, or before the end tag of an inline element. The boundaries inside an
// element follow Saxon, which the spec permits: an indent may follow the start
// tag of an inline element, but none goes before its end tag.
func SkipIndentBefore(n *xdm.Node, i int, xhtml, html5 bool) bool {
	if i > 0 && Inline(n.Children[i-1], xhtml, html5) {
		return true
	}
	if i == len(n.Children) {
		return Inline(n, xhtml, html5)
	}
	return Inline(n.Children[i], xhtml, html5)
}

// ReplacedMeta reports whether n is a meta element the html and xhtml methods
// discard from head, the <head> they have added their own content-type meta
// to. §7.4.13 and §6.1.14: "any existing meta element child of the head
// element having an http-equiv attribute with the value "Content-Type",
// making the comparison without regard to case after first stripping leading
// and trailing spaces ... MUST be discarded". A child only: a meta deeper in
// head is left alone. The HTML5 charset spelling of the same declaration is
// discarded too, as Saxon does, since it would contradict the added one.
func ReplacedMeta(head, n *xdm.Node) bool {
	if head == nil || n.Parent != head {
		return false
	}
	if n.Kind != xdm.KindElement || !strings.EqualFold(n.Name.Local, "meta") {
		return false
	}
	for _, a := range n.Attrs {
		if a.Name.URI != "" {
			continue
		}
		if a.Name.Local == "charset" ||
			strings.EqualFold(a.Name.Local, "http-equiv") &&
				strings.EqualFold(strings.TrimSpace(a.Value), "content-type") {
			return true
		}
	}
	return false
}

// MetaContent is the content attribute of the meta element the html and xhtml
// methods add (§7.4.13, §6.1.14): the media-type parameter, text/html when
// absent, and the charset, UTF-8 when no encoding is given.
func MetaContent(mediaType, encoding string) string {
	if mediaType == "" {
		mediaType = "text/html"
	}
	if encoding == "" {
		encoding = "UTF-8"
	}
	return mediaType + "; charset=" + encoding
}
