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
	if n.Kind() != xdm.KindElement {
		return false
	}
	local := n.Name().Local
	switch {
	case xhtml && n.Name().URI == NSXHTML:
	case n.Name().URI == "" && (!xhtml || html5),
		!xhtml && html5 && n.Name().URI == NSXHTML:
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

// SkipIndentBefore reports whether no indent may go before c, a child of n
// (c == nil meaning before n's end tag): next to an inline child, or before
// the end tag of an inline element. The boundaries inside an
// element follow Saxon, which the spec permits: an indent may follow the start
// tag of an inline element, but none goes before its end tag.
func SkipIndentBefore(n, c *xdm.Node, xhtml, html5 bool) bool {
	prev := n.LastChild()
	if c != nil {
		prev = c.PrevSibling()
	}
	if prev != nil && Inline(prev, xhtml, html5) {
		return true
	}
	if c == nil {
		return Inline(n, xhtml, html5)
	}
	return Inline(c, xhtml, html5)
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
	if head == nil || n.Parent() != head {
		return false
	}
	if n.Kind() != xdm.KindElement || !strings.EqualFold(n.Name().Local, "meta") {
		return false
	}
	for a := range n.Attrs() {
		if a.Name().URI != "" {
			continue
		}
		if a.Name().Local == "charset" ||
			strings.EqualFold(a.Name().Local, "http-equiv") &&
				strings.EqualFold(strings.TrimSpace(a.Value()), "content-type") {
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

// MetaEnd is what closes the content-type meta the html and xhtml methods add
// to head, after its content value. xhtml is XML, so the element is closed,
// with the space the compatibility guidelines ask for (§6.1.14). Under html a
// head in an XML island -- a namespace other than none, or XHTML's below
// HTML5 -- is written as XML too (§7.1: the html method writes an element
// differently from the xml method only "if the element is to be serialized
// as an HTML element"), so its meta takes an end tag; result-document-1402
// asks for that spelling. Anywhere else it is a bare HTML start tag.
func MetaEnd(head *xdm.Node, xhtml, html5 bool) string {
	switch {
	case xhtml:
		return `" />`
	case Island(head, html5):
		return `"></meta>`
	}
	return `">`
}

// Island reports whether the html method writes element n with XML syntax
// rather than HTML's: n is in a namespace that is neither absent nor, under
// HTML5, XHTML's. Serialization 3.1 §7.1 has the html method write an element
// differently from the xml method only "if the element is to be serialized
// as an HTML element", so such an "XML island" self-closes when empty:
// Serialization-html-5 and -6 embed <magic xmlns="..."/> and ask for it back
// self-closed, and -3 (HTML 4) wants the XHTML-namespace elements -4 (HTML5)
// writes as HTML self-closed too, since HTML 4 did not know that namespace.
func Island(n *xdm.Node, html5 bool) bool {
	return n.Name().URI != "" && !(html5 && n.Name().URI == NSXHTML)
}

// XHTML5Doctype reports whether the xhtml method, under html-version 5 with
// no doctype-system, writes a bare "<!DOCTYPE " + local name + ">" ahead of
// document element n. Serialization 3.1 §6.1.7: the first element "is
// recognized as an HTML element, the local part of the QName of which is
// equal to the string HTML, without regard to case". Under HTML5 the xhtml
// method recognises the XHTML namespace and no namespace (§6), so a foreign
// namespace (output-0214) or another name (output-0213) gets none.
func XHTML5Doctype(n *xdm.Node) bool {
	return n.Kind() == xdm.KindElement &&
		(n.Name().URI == NSXHTML || n.Name().URI == "") &&
		strings.EqualFold(n.Name().Local, "html")
}

// Void reports whether an HTML element named local, matched without regard
// to case, takes no end tag: the names void in every HTML version, plus
// HTML5's own under html5 and HTML 4's otherwise. The version decides for
// seven names (Serialization-html-1 writes the HTML 4 list, -2 the HTML5
// one), so a single merged list answers both versions wrongly.
func Void(local string, html5 bool) bool {
	local = strings.ToLower(local)
	if voidElements[local] {
		return true
	}
	if html5 {
		return html5VoidElements[local]
	}
	return html4VoidElements[local]
}

// voidElements are the HTML elements that take no end tag in every HTML
// version the serialisers write. Writing one with a closing tag, or
// self-closing a non-void element, both produce a tree that an HTML parser
// reads differently from the one built.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true,
}

// html4VoidElements are void in HTML 4 and gone from HTML 5, which has no
// frameset and no isindex at all. Writing "<frame>" unclosed under HTML 5
// leaves an element an HTML 5 parser reads as open, swallowing what follows.
var html4VoidElements = map[string]bool{
	"basefont": true, "frame": true, "isindex": true,
}

// html5VoidElements are void in HTML 5 and unknown to HTML 4, where an
// unclosed one would be an unrecognised start tag rather than an empty
// element.
var html5VoidElements = map[string]bool{
	"keygen": true, "source": true, "track": true, "wbr": true,
}
