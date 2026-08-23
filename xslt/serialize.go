package xslt

import (
	"fmt"
	"io"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// serialize writes a result sequence using the given output settings.
func serialize(w io.Writer, seq xdm.Sequence, opts OutputSettings, charMap map[rune]string) error {
	s := &serializer{w: w, opts: opts, charMap: charMap}

	if opts.Method == "text" {
		// Text output emits string values only; markup is discarded, which is
		// the point of the method.
		for _, it := range seq {
			switch v := it.(type) {
			case *xdm.Node:
				s.writeString(v.StringValue())
			case *xdm.Atomic:
				s.writeString(v.String())
			}
		}
		return s.err
	}

	html := strings.EqualFold(opts.Method, "html")
	s.html = html

	// The HTML output method never emits an XML declaration: an HTML document
	// beginning with "<?xml" is served as XML by some browsers, which defeats
	// the point of asking for HTML.
	if !opts.OmitXMLDecl && !html {
		enc := opts.Encoding
		if enc == "" {
			enc = "UTF-8"
		}
		decl := `<?xml version="1.0" encoding="` + enc + `"`
		if opts.Standalone != "" {
			decl += ` standalone="` + opts.Standalone + `"`
		}
		s.writeString(decl + "?>\n")
	}
	switch {
	case html && opts.DocTypeSystem == "" && opts.DocTypePublic == "":
		// HTML5 asks for a bare "<!DOCTYPE HTML>"; earlier HTML versions get
		// one only when the stylesheet names a public or system identifier.
		if strings.HasPrefix(opts.Version, "5") {
			s.writeString("<!DOCTYPE HTML>\n")
		}
	case opts.DocTypeSystem != "" || opts.DocTypePublic != "":
		s.writeDoctype(seq)
	}

	// suppressFirstIndent stops the top-level element from emitting the
	// newline that indent mode puts before every element: the XML declaration
	// already ended with one, and two would leave a blank line.
	s.atTop = true
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			s.node(v, 0)
		case *xdm.Atomic:
			s.escapeText(v.String())
		}
	}
	return s.err
}

type serializer struct {
	w    io.Writer
	opts OutputSettings
	err  error
	// nsStack tracks namespace prefixes already declared on ancestors, so a
	// binding is emitted once rather than on every descendant.
	nsStack []map[string]string
	// atTop suppresses the leading newline before the first top-level node.
	atTop bool
	// html selects the HTML output method, which differs from XML in three
	// ways that matter: void elements are written unclosed, no element is
	// ever self-closed, and a content-type meta is injected into <head>.
	html bool
	// inHead marks that serialisation is inside <head>, where a duplicate
	// charset meta is suppressed.
	inHead bool
	// rawText marks that serialisation is inside an HTML element whose
	// content is CDATA rather than parsed character data. rawTextName is
	// which one, so that the error naming it can say so.
	rawText     bool
	rawTextName string
	// charMap substitutes individual characters for arbitrary strings,
	// bypassing escaping. Declared by xsl:character-map.
	charMap map[rune]string
}

func (s *serializer) writeString(str string) {
	if s.err != nil {
		return
	}
	_, s.err = io.WriteString(s.w, str)
}

func (s *serializer) writeDoctype(seq xdm.Sequence) {
	// The doctype names the first element of the result.
	for _, it := range seq {
		n, ok := it.(*xdm.Node)
		if !ok || n.Kind != xdm.KindElement {
			continue
		}
		s.writeString("<!DOCTYPE " + n.Name.Lexical())
		if s.opts.DocTypePublic != "" {
			s.writeString(` PUBLIC "` + s.opts.DocTypePublic + `" "` + s.opts.DocTypeSystem + `"`)
		} else {
			s.writeString(` SYSTEM "` + s.opts.DocTypeSystem + `"`)
		}
		s.writeString(">\n")
		return
	}
}

// node writes one node at the given indent depth.
func (s *serializer) node(n *xdm.Node, depth int) {
	switch n.Kind {
	case xdm.KindDocument:
		for _, c := range n.Children {
			s.node(c, depth)
		}

	case xdm.KindElement:
		s.element(n, depth)

	case xdm.KindText:
		if s.rawText {
			// Inside <script> and <style> the HTML method writes text
			// unescaped: these elements hold CDATA in HTML, so escaping "&"
			// and ">" would corrupt a CSS child selector or a JavaScript
			// comparison rather than protect anything.
			//
			// The rule the spec pairs with that one was missing. Since the
			// text is written raw, a value containing "</" closes the element
			// early and everything after it is markup — the standard XSS
			// primitive, reachable from any document value that reaches a
			// <script> body. Escaping is not an option here, so the spec
			// makes it a serialization error, as it does for "--" in a
			// comment and "?>" in a processing instruction.
			if strings.Contains(n.Value, "</") {
				if s.err == nil {
					s.err = fmt.Errorf(
						"SERE0007: %s content contains '</', which would end "+
							"the element; it cannot be escaped in the html "+
							"output method", s.rawTextName)
				}
				return
			}
			s.writeString(n.Value)
			return
		}
		s.escapeText(n.Value)

	case xdm.KindComment:
		s.indent(depth)
		s.writeString("<!--" + n.Value + "-->")

	case xdm.KindPI:
		s.indent(depth)
		s.writeString("<?" + n.Name.Local)
		if n.Value != "" {
			s.writeString(" " + n.Value)
		}
		s.writeString("?>")

	case xdm.KindAttribute:
		// An attribute reaching the top level of a result is an error the
		// spec calls out; serialising it as markup would produce something
		// that is not well-formed.
		if s.err == nil {
			s.err = fmt.Errorf("XTDE0420: attribute %q cannot be serialised outside an element",
				n.Name.Lexical())
		}
	}
}

func (s *serializer) element(n *xdm.Node, depth int) {
	// The method already emitted a content-type meta, so a charset meta from
	// the stylesheet would be a duplicate declaration.
	if s.html && s.inHead && strings.EqualFold(n.Name.Local, "meta") &&
		n.Attr("", "charset") != nil {
		return
	}
	s.indent(depth)

	name := s.elementName(n)
	s.writeString("<" + name)

	// Emit namespace declarations that are not already in scope on an
	// ancestor. Re-declaring an inherited binding is legal but noisy, and for
	// a document with a namespace on every element it doubles the output size.
	inScope := s.currentScope()
	declared := map[string]string{}
	for _, ns := range n.Namespaces {
		if inScope[ns.Name.Local] == ns.Value {
			continue
		}
		s.writeNamespaceDecl(ns.Name.Local, ns.Value)
		declared[ns.Name.Local] = ns.Value
	}
	// An element whose namespace has no declaration in scope needs one, which
	// happens for elements built by xsl:element with a computed namespace.
	if n.Name.URI != "" && inScope[n.Name.Prefix] != n.Name.URI &&
		declared[n.Name.Prefix] != n.Name.URI {
		s.writeNamespaceDecl(n.Name.Prefix, n.Name.URI)
		declared[n.Name.Prefix] = n.Name.URI
	}
	// An element in no namespace under an ancestor with a default namespace
	// has to undeclare it. Without xmlns="" the element is read back as
	// being in the ancestor's namespace, which is a different document from
	// the one the transform produced — and the one case where omitting a
	// declaration changes meaning rather than only size.
	if n.Name.URI == "" && n.Name.Prefix == "" && inScope[""] != "" &&
		declared[""] == "" {
		s.writeNamespaceDecl("", "")
		declared[""] = ""
	}
	for _, a := range n.Attrs {
		if a.Name.URI == "" || a.Name.URI == xdm.NSXML {
			continue
		}
		if inScope[a.Name.Prefix] == a.Name.URI || declared[a.Name.Prefix] == a.Name.URI {
			continue
		}
		s.writeNamespaceDecl(a.Name.Prefix, a.Name.URI)
		declared[a.Name.Prefix] = a.Name.URI
	}

	for _, a := range n.Attrs {
		s.writeString(" " + s.attrName(a) + `="` + escapeAttr(a.Value) + `"`)
	}

	if len(n.Children) == 0 {
		if s.html {
			// HTML has no self-closing syntax. A void element takes no end
			// tag; every other empty element takes an explicit one, because
			// "<div/>" is parsed by HTML parsers as an unclosed "<div>".
			if isVoidElement(n.Name.Local) {
				s.writeString(">")
			} else {
				s.writeString("></" + name + ">")
			}
			return
		}
		s.writeString("/>")
		return
	}

	s.pushScope(inScope, declared)
	s.writeString(">")

	// The HTML method adds the content-type meta so the encoding survives
	// being served without a charset header.
	if s.html && isRawTextElement(n.Name.Local) {
		saved, savedName := s.rawText, s.rawTextName
		s.rawText, s.rawTextName = true, n.Name.Local
		defer func() { s.rawText, s.rawTextName = saved, savedName }()
	}
	if s.html && strings.EqualFold(n.Name.Local, "head") {
		s.inHead = true
		defer func() { s.inHead = false }()
		enc := s.opts.Encoding
		if enc == "" {
			enc = "UTF-8"
		}
		s.writeString(`<meta http-equiv="Content-Type" content="text/html; charset=` + enc + `">`)
	}

	// Indentation is suppressed for mixed content: adding whitespace around
	// text would change the element's string value, which for a validator's
	// output is a correctness issue rather than a cosmetic one.
	indentChildren := s.opts.Indent && !hasTextChild(n)
	for _, c := range n.Children {
		if indentChildren {
			s.node(c, depth+1)
		} else {
			s.nodeNoIndent(c)
		}
	}
	if indentChildren {
		s.indent(depth)
	}
	s.writeString("</" + name + ">")
	s.popScope()
}

// nodeNoIndent writes a node without introducing whitespace.
func (s *serializer) nodeNoIndent(n *xdm.Node) {
	saved := s.opts.Indent
	s.opts.Indent = false
	s.node(n, 0)
	s.opts.Indent = saved
}

func (s *serializer) writeNamespaceDecl(prefix, uri string) {
	if prefix == "" {
		s.writeString(` xmlns="` + escapeAttr(uri) + `"`)
		return
	}
	s.writeString(` xmlns:` + prefix + `="` + escapeAttr(uri) + `"`)
}

func (s *serializer) currentScope() map[string]string {
	if len(s.nsStack) == 0 {
		return map[string]string{}
	}
	return s.nsStack[len(s.nsStack)-1]
}

func (s *serializer) pushScope(base, added map[string]string) {
	next := make(map[string]string, len(base)+len(added))
	for k, v := range base {
		next[k] = v
	}
	for k, v := range added {
		next[k] = v
	}
	s.nsStack = append(s.nsStack, next)
}

func (s *serializer) popScope() {
	if len(s.nsStack) > 0 {
		s.nsStack = s.nsStack[:len(s.nsStack)-1]
	}
}

// elementName returns the lexical name to serialise.
func (s *serializer) elementName(n *xdm.Node) string {
	if n.Name.Prefix != "" {
		return n.Name.Prefix + ":" + n.Name.Local
	}
	return n.Name.Local
}

func (s *serializer) attrName(a *xdm.Node) string {
	if a.Name.URI == xdm.NSXML {
		return "xml:" + a.Name.Local
	}
	if a.Name.Prefix != "" {
		return a.Name.Prefix + ":" + a.Name.Local
	}
	return a.Name.Local
}

func (s *serializer) indent(depth int) {
	if !s.opts.Indent {
		return
	}
	if s.atTop {
		s.atTop = false
		return
	}
	s.writeString("\n" + strings.Repeat("  ", depth))
}

func hasTextChild(n *xdm.Node) bool {
	for _, c := range n.Children {
		if c.Kind == xdm.KindText && !xdm.IsXMLWhitespace(c.Value) {
			return true
		}
	}
	return false
}

// escapeText writes character data with the three characters that cannot
// appear literally.
//
// ">" is escaped even though it is only required in the "]]>" sequence,
// because doing it unconditionally is cheaper than scanning for that sequence
// and produces output every parser accepts.
func (s *serializer) escapeText(text string) {
	var sb strings.Builder
	sb.Grow(len(text))
	for _, r := range text {
		// A character map wins over escaping: emitting "&nbsp;" is the whole
		// reason a stylesheet declares one, and escaping the ampersand would
		// defeat it.
		if repl, ok := s.charMap[r]; ok {
			sb.WriteString(repl)
			continue
		}
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '\u00a0':
			// The HTML method writes a no-break space as the named entity, so
			// that it survives a transport that mangles non-ASCII bytes and
			// stays visible to anyone reading the source. XML output has no
			// such convention and keeps the character.
			if s.html {
				sb.WriteString("&nbsp;")
				continue
			}
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	s.writeString(sb.String())
}

// escapeAttr escapes a value for an attribute, which additionally must not
// contain a raw quote, newline or tab: a literal newline in an attribute is
// normalised to a space by every conformant parser, so it has to be escaped to
// survive a round trip.
func escapeAttr(v string) string {
	var sb strings.Builder
	sb.Grow(len(v))
	for _, r := range v {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			sb.WriteString("&quot;")
		case '\n':
			sb.WriteString("&#10;")
		case '\r':
			sb.WriteString("&#13;")
		case '\t':
			sb.WriteString("&#9;")
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// voidElements are the HTML elements that take no end tag. Writing one with a
// closing tag, or self-closing a non-void element, both produce a tree that
// an HTML parser reads differently from the one the stylesheet built.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
	"basefont": true, "frame": true, "isindex": true,
}

func isVoidElement(local string) bool {
	return voidElements[strings.ToLower(local)]
}

// isRawTextElement reports whether an HTML element's content is CDATA, and so
// must be serialised without escaping.
func isRawTextElement(local string) bool {
	switch strings.ToLower(local) {
	case "script", "style":
		return true
	}
	return false
}
