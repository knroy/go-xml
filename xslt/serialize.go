package xslt

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// jsonParams carries the settings the JSON and adaptive output methods read
// across to xpath, which owns their rendering. Only the parameters those two
// methods consult are passed: the rest of xsl:output describes an XML
// declaration, indentation and escaping that neither method has.
func jsonParams(opts OutputSettings, charMap map[rune]string) xpath.SerializeParams {
	p := xpath.SerializeParams{
		AllowDuplicateNames:  opts.AllowDuplicateNames,
		JSONNodeOutputMethod: opts.JSONNodeOutputMethod,
		CharMap:              charMap,
	}
	if opts.ItemSeparator != nil {
		p.ItemSeparator, p.HasItemSeparator = *opts.ItemSeparator, true
	}
	return p
}

// Serialize writes a sequence with the serialization methods of the XSLT and
// XQuery Serialization 3.1 specification.
//
// The rules it applies are the specification's, not XSLT's: sequence
// normalisation, the xml, xhtml, html, text, json and adaptive output
// methods, and the parameters that steer each. Nothing in it is peculiar to a
// stylesheet. It lives in this package because this is where the serialiser
// was written, and moving it would be a change to the packages of every
// existing caller for no gain; but a host other than XSLT — an XQuery main
// module stating its parameters with "declare option output:*", which is the
// same specification's other binding — serialises its result through this
// same function, so that the two agree by construction rather than by
// maintenance.
//
// charMap is the character map applied to the finished text, or nil. An
// OutputSettings with an empty Method asks for the default method to be
// chosen from the sequence itself, which is what a host that stated no method
// wants; see defaultMethod.
func Serialize(w io.Writer, seq xdm.Sequence, opts OutputSettings, charMap map[rune]string) error {
	return serialize(w, seq, opts, charMap)
}

// serialize writes a result sequence using the given output settings.
func serialize(w io.Writer, seq xdm.Sequence, opts OutputSettings, charMap map[rune]string) error {
	s := &serializer{w: w, opts: opts, charMap: charMap}
	s.normalize = normalizerFor(opts.NormalizationForm)
	if len(opts.SuppressIndentation) > 0 {
		s.noIndentElems = make(map[xdm.QName]bool, len(opts.SuppressIndentation))
		for _, q := range opts.SuppressIndentation {
			// URI and Local only, for the same reason as cdataElems below:
			// a QName compares as a whole struct, and the prefix a result
			// element happens to carry is not part of its identity.
			s.noIndentElems[xdm.QName{URI: q.URI, Local: q.Local}] = true
		}
	}
	if len(opts.CDataElements) > 0 {
		s.cdataElems = make(map[xdm.QName]bool, len(opts.CDataElements))
		for _, q := range opts.CDataElements {
			// Only URI and Local are set: a QName is compared as a whole
			// struct, so leaving the prefix in would never match a result
			// element carrying a different one for the same namespace.
			s.cdataElems[xdm.QName{URI: q.URI, Local: q.Local}] = true
		}
	}

	// The method defaults to one chosen from the result itself: a document
	// whose first element is <html> is meant to be read as HTML, and writing
	// it as XML would produce something a browser renders differently. The
	// choice is made here rather than at compile time because it depends on
	// the tree, which does not exist until the transform has run.
	if opts.Method == "" {
		// The second argument is the backwards-compatibility case described
		// on defaultMethod: a principal stylesheet module at version 1.0
		// whose result tree was generated implicitly. compileModule records
		// the version half in Version10Implicit; xsl:result-document clears
		// the flag on its own copy of the settings, which is what makes the
		// tree it produces an explicit one.
		opts.Method = defaultMethod(seq, opts.Version10Implicit)
		s.opts.Method = opts.Method
	}

	// The json and adaptive methods do not serialise item by item and do not
	// go through sequence normalisation at all: json renders the whole result
	// as one JSON value, and adaptive writes each item in a form of its own.
	// Both are XPath 3.1 rules that xpath already applies for fn:serialize, so
	// they are delegated rather than written a second time -- the two must
	// agree, since result-document-1401 and serialize-json-010 describe the
	// same output by different routes. Delegating here, before
	// checkOutputSettings, is also what lets a map through: that check exists
	// to raise SENR0001 for an item the *XML-family* methods have no
	// rendering for, and a map is precisely what the JSON method renders.
	switch strings.ToLower(opts.Method) {
	case "json":
		out, err := xpath.SerializeJSON(seq, jsonParams(opts, charMap))
		if err != nil {
			return err
		}
		// Unicode normalisation is the last step of the pipeline for every
		// method, this one included (Serialization 3.1 §7). It is applied to
		// the finished text rather than to each string as it is written,
		// because a form like NFC composes across a boundary: a base
		// character at the end of one value and a combining mark at the
		// start of the next are one character after normalisation, and
		// normalising them apart would leave them two. JSON's own escapes
		// are all ASCII and no normalisation form touches those.
		s.writeString(s.normalized(out))
		return s.err
	case "adaptive":
		out, err := xpath.SerializeAdaptive(seq, jsonParams(opts, charMap))
		if err != nil {
			return err
		}
		out = s.normalized(out)
		// The adaptive method renders a node by handing it to the XML output
		// method (Serialization 3.1 §10), so the XML declaration is part of
		// what it produces and omit-xml-declaration is a parameter it
		// honours. The JSON method is the opposite case and is left alone:
		// a JSON document with "<?xml" in front of it is not JSON.
		//
		// output-0721 is the case that settles it. It selects the adaptive
		// method through a parameter document and asks for
		// "^<\?xml[^<]+><test>AAA</test>$" - the declaration once, ahead of
		// the whole result rather than ahead of each item, which is why it
		// is written here and not inside SerializeAdaptive.
		if !opts.OmitXMLDecl {
			enc := opts.Encoding
			if enc == "" {
				enc = "UTF-8"
			}
			decl := `<?xml version="1.0" encoding="` + enc + `"`
			if opts.Standalone != "" {
				decl += ` standalone="` + opts.Standalone + `"`
			}
			if opts.Indent {
				s.writeString(decl + "?>\n")
			} else {
				s.writeString(decl + "?>")
			}
		}
		s.writeString(out)
		return s.err
	}

	// Sequence normalisation, step 1: an array is replaced by its members,
	// recursively, so that the XML-family methods see the sequence the array
	// was holding rather than an item they have no rendering for. Only an
	// array is flattened; a map and a function item still have no
	// serialisation and are SENR0001 below, which is why this is a
	// substitution rather than a general unwrapping.
	//
	// It happens before checkOutputSettings, since that check exists to
	// refuse what cannot be written and an array's members can be. It also
	// happens before the item separator is inserted, because the members are
	// items of the sequence being separated: [1,2] with item-separator="-"
	// is "1-2", not one item.
	seq = flattenArrays(seq)

	// Sequence normalisation, step 3 (XSLT/XQuery Serialization 3.1, 2):
	// when an item separator is in force it is inserted between every pair
	// of adjacent items in the sequence, in place of the default rules.
	//
	// It happens after the json and adaptive methods have been dispatched
	// above, because sequence normalisation does not apply to either of them
	// -- §2 applies it to "the XML, XHTML, HTML and Text output methods"
	// only. Both of those methods separate items themselves, adaptive with
	// this very parameter (§10, defaulting to a newline), so inserting the
	// separator here as well wrote it twice: the sequence (1,2,3) with
	// item-separator="-" came out as "1---2---3", the separator once as an
	// inserted text item and once again as the join between items.
	//
	// The default method is chosen above this rather than below it for the
	// same reason it is chosen at all: it depends on the first item of the
	// result, which separator insertion does not change.
	seq = insertItemSeparator(seq, opts.ItemSeparator)

	// Parameter conflicts are diagnosed before a byte is written. The
	// alternative — discovering halfway through that the requested encoding
	// does not exist — leaves the caller holding a truncated document that
	// looks like a complete one.
	if err := checkOutputSettings(opts, seq); err != nil {
		return err
	}

	// Sequence normalisation, step 1: a run of adjacent atomic values becomes
	// one text node with a single space between each. Deferred to here, after
	// the default method has been chosen and the settings checked, because
	// neither decision is meant to change with the form the values take.
	seq = joinAdjacentAtomics(seq)

	if opts.Method == "text" {
		// A byte order mark precedes everything the method writes, text
		// included: it is what tells a reader how to decode the bytes, and
		// text output has no declaration to carry the encoding instead.
		if opts.ByteOrderMark {
			s.writeString("\uFEFF")
		}
		// Text output emits string values only; markup is discarded, which is
		// the point of the method.
		for _, it := range seq {
			switch v := it.(type) {
			case *xdm.Node:
				// XSLT Serialization 3.0 section 10: the text output method
				// writes the string value of every text node in the result,
				// and nothing else. Comments and processing instructions are
				// not text nodes and contribute nothing — the same rule that
				// keeps them out of an ancestor's string value. Reaching them
				// through StringValue here would have written a comment's text
				// and a PI's data as if they were character content, which is
				// exactly the markup the method exists to suppress.
				if v.Kind == xdm.KindComment || v.Kind == xdm.KindPI {
					continue
				}
				s.writeString(s.normalized(s.mapChars(v.StringValue())))
			case *xdm.Atomic:
				s.writeString(s.normalized(s.mapChars(v.String())))
			}
		}
		return s.err
	}

	html := strings.EqualFold(opts.Method, "html")
	// XHTML serialises as XML — declaration, self-closing empty elements,
	// well-formed markup — while sharing the HTML method's content-type meta
	// and its treatment of a few elements. So it sets the HTML flag for
	// those behaviours but is not "html" for the XML-declaration rule below.
	xhtml := strings.EqualFold(opts.Method, "xhtml")
	s.html = html || xhtml
	s.xhtml = xhtml
	// html-version decides, falling back to version for the html method
	// only: for xhtml, @version is the version of XML, so reading it would
	// make every XHTML document version="1.0" and never HTML5.
	htmlVer := opts.HTMLVersion
	if htmlVer == "" && !xhtml {
		htmlVer = opts.Version
	}
	s.html5 = strings.HasPrefix(htmlVer, "5")

	// Prefix normalisation is an HTML5 rule and applies to both methods that
	// write HTML: output-0602a and -0602b are the html method and -0211 and
	// -0225 the xhtml one, and all four want the same tree.
	if s.html5 {
		seq = normalizePrefixes(seq)
	}

	// A byte order mark, when asked for. It precedes everything, including
	// the XML declaration, since it is what tells a reader how to decode
	// that declaration in the first place.
	if opts.ByteOrderMark {
		s.writeString("\uFEFF")
	}

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
		// The newline after the declaration is indentation, not structure.
		// With indent off the result is meant to be exactly the markup the
		// stylesheet produced, and a stray line break there is a character
		// the caller did not ask for.
		if opts.Indent {
			s.writeString(decl + "?>\n")
		} else {
			s.writeString(decl + "?>")
		}
	}
	switch {
	case s.html && opts.DocTypeSystem == "" && (opts.DocTypePublic == "" || s.html5 || html):
		// HTML5 asks for a bare doctype; earlier HTML versions get one only
		// when the stylesheet names a public or system identifier.
		//
		// The html method also gets one from a public identifier alone,
		// which no XML-based method can write: HTML's grammar does not
		// require the system literal that XML's puts after PUBLIC. So an
		// explicit public identifier schedules a declaration under that
		// method whatever the version, and writeDoctypeFor decides its
		// spelling.
		//
		// s.html rather than html: XHTML5 wants the declaration too. It is
		// deferred like every other doctype rather than written now, because
		// its name has to be the document element's own -- an XHTML document
		// is XML, where the DOCTYPE name must match the root element exactly,
		// so <HtMl> takes "<!DOCTYPE HtMl>". Writing it here would have to
		// guess the name before the element is in hand.
		if s.html5 || (html && opts.DocTypePublic != "") {
			s.pendingDoctype = true
		}
	case opts.DocTypeSystem != "":
		// A document type declaration is written only when a system
		// identifier is available. A public identifier alone cannot be
		// written at all: the XML grammar puts the system literal after
		// PUBLIC and makes it required, so there is no legal spelling of a
		// declaration that names only a public identifier.
		//
		// The declaration goes immediately before the document element, not
		// before the whole result: XML puts comments and processing
		// instructions in the prolog ahead of it, and a result that starts
		// with one of those would otherwise have them moved after the
		// declaration, which changes the document.
		s.pendingDoctype = true
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
	// pendingDoctype records that a document type declaration is owed, to be
	// written immediately before the document element.
	pendingDoctype bool
	// atTop suppresses the leading newline before the first top-level node.
	atTop bool
	// html selects the HTML output method, which differs from XML in three
	// ways that matter: void elements are written unclosed, no element is
	// ever self-closed, and a content-type meta is injected into <head>.
	html bool
	// html5 records that the HTML version asked for is 5 or later, where the
	// C1 range that HTML 4 forbids is merely discouraged.
	html5 bool
	// xhtml marks the XHTML method, which shares the HTML method's
	// content-type meta but serialises as XML: an XML declaration, and empty
	// elements closed rather than left open.
	xhtml bool
	// inHead marks that serialisation is inside <head>, where a duplicate
	// charset meta is suppressed.
	inHead bool
	// rawText marks that serialisation is inside an HTML element whose
	// content is CDATA rather than parsed character data. rawTextName is
	// which one, so that the error naming it can say so.
	rawText     bool
	rawTextName string
	// inCData marks that serialisation is inside an element named in
	// cdata-section-elements, whose text children are wrapped in CDATA
	// sections instead of being escaped.
	inCData bool
	// cdataElems is that list, as a set keyed by expanded name.
	cdataElems map[xdm.QName]bool
	// noIndentElems is suppress-indentation as a set keyed by expanded name:
	// the elements whose content is written exactly as it stands even when
	// indent is yes.
	noIndentElems map[xdm.QName]bool
	// normalize applies the Unicode normalisation named by
	// normalization-form, or is nil when none was asked for.
	normalize func(string) string
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

// writeDoctypeFor writes the document type declaration naming this element.
func (s *serializer) writeDoctypeFor(n *xdm.Node) {
	// HTML5 with no identifiers is the bare form.
	//
	// The two methods take the name from different places. XHTML is XML, so
	// the DOCTYPE name must match the document element exactly and <HtMl>
	// takes "<!DOCTYPE HtMl>" -- which is why the declaration is deferred to
	// the element rather than written with the prolog. HTML5's doctype is
	// instead a fixed string: it is "<!DOCTYPE HTML>" whatever the document
	// element is called, so a fragment rooted at <input> still gets it, and
	// naming the element there produced "<!DOCTYPE INPUT>".
	// A public identifier alone cannot be written -- the XML grammar puts the
	// system literal after PUBLIC and makes it required -- but under HTML5 it
	// does not suppress the declaration either: the ruling on bug 20264 is
	// that the bare form is still output, which output-0229 asserts.
	// A public identifier with no system identifier is writable under the
	// html method and not under any of the XML-based ones. XML's grammar puts
	// the system literal after PUBLIC and makes it required; HTML's does not,
	// and "<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.0//EN">" is the standard
	// HTML 4 declaration. Serialization-html-25 and -27 ask for exactly that,
	// under version 4 and version 5 alike -- an explicit identifier overrides
	// HTML 5's bare form rather than being ignored in favour of it.
	if s.html && !s.xhtml && s.opts.DocTypeSystem == "" &&
		s.opts.DocTypePublic != "" {
		s.writeString("<!DOCTYPE " + s.elementName(n) +
			" PUBLIC " + quoteLiteral(s.opts.DocTypePublic) + ">\n")
		return
	}
	if s.html5 && s.opts.DocTypeSystem == "" {
		if s.xhtml {
			// The declaration is written only for a document element that
			// really is html in the XHTML namespace, and it names the
			// element's LOCAL name -- <HtMl> takes "<!DOCTYPE HtMl>",
			// because an XHTML document is XML and the DOCTYPE name must
			// match the element. The prefix is not part of that match,
			// which is why it is not the serialised element name: <h:html>
			// took "<!DOCTYPE h:html>" and matched nothing.
			//
			// Four tests pin the three halves of this apart: output-0209
			// and -0210 the casing, -0211 the prefix, -0213 an element
			// that is not html, and -0214 an html that is not XHTML's.
			if n.Kind == xdm.KindElement && n.Name.URI == nsXHTML &&
				strings.EqualFold(n.Name.Local, "html") {
				s.writeString("<!DOCTYPE " + n.Name.Local + ">\n")
			}
			return
		}
		s.writeString("<!DOCTYPE HTML>\n")
		return
	}
	s.writeString("<!DOCTYPE " + s.elementName(n))
	if s.opts.DocTypePublic != "" {
		s.writeString(" PUBLIC " + quoteLiteral(s.opts.DocTypePublic))
	} else {
		s.writeString(" SYSTEM")
	}
	s.writeString(" " + quoteLiteral(s.opts.DocTypeSystem) + ">\n")
}

// quoteLiteral wraps an external identifier in quotes it does not itself
// contain. XML gives these literals no escaping mechanism, so a value holding
// a double quote has to be delimited with single quotes instead — the choice
// is the only way to write it at all.
func quoteLiteral(v string) string {
	if strings.Contains(v, `"`) {
		return "'" + v + "'"
	}
	return `"` + v + `"`
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
		if s.inCData {
			s.writeCData(n.Value)
			return
		}
		s.escapeText(n.Value)

	case xdm.KindComment:
		s.indent(depth)
		s.writeString("<!--" + n.Value + "-->")

	case xdm.KindPI:
		// The HTML method ends a processing instruction at the first ">"
		// rather than at "?>", so a ">" inside the data would truncate it.
		// There is no escape for it in HTML, which is why the spec makes it
		// an error rather than something to encode.
		if s.html && !s.xhtml && strings.Contains(n.Value, ">") {
			if s.err == nil {
				s.err = fmt.Errorf("SERE0015: a processing instruction " +
					"contains '>', which ends it in the html output method")
			}
			return
		}
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
	if s.pendingDoctype {
		s.pendingDoctype = false
		s.writeDoctypeFor(n)
	}

	// The method already emitted a content-type meta, so a charset meta from
	// the stylesheet would be a duplicate declaration.
	// The method already emitted a content-type meta, so one from the
	// stylesheet would be a second, contradicting declaration. Both spellings
	// are dropped: the HTML5 "charset" form and the HTTP-header form the
	// serialiser itself writes.
	if s.html && s.inHead && strings.EqualFold(n.Name.Local, "meta") &&
		(n.Attr("", "charset") != nil || isContentTypeMeta(n)) {
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
	//
	// Presence in the map, not its value: an undeclaration records the empty
	// string, so testing declared[""] == "" could not tell "already
	// undeclared here" from "not mentioned here" and wrote xmlns="" twice on
	// an element that needed it once.
	if _, undeclared := declared[""]; n.Name.URI == "" && n.Name.Prefix == "" &&
		inScope[""] != "" && !undeclared {
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
		s.writeString(" " + s.attrName(a) + s.attrValue(a, n))
	}

	// An empty element still has to be opened when the method is going to put
	// something inside it: an empty <head/> under the html or xhtml method
	// gets the content-type meta, and writing "<head></head>" from the
	// no-children branch below would have dropped it. Serialization-html-33
	// and -xhtml-33 build exactly that document.
	emptyHead := len(n.Children) == 0 && s.html &&
		strings.EqualFold(n.Name.Local, "head") &&
		(!s.xhtml || n.Name.URI == nsXHTML) &&
		(s.opts.IncludeContentType == nil || *s.opts.IncludeContentType)

	if len(n.Children) == 0 && !emptyHead {
		if s.html && !s.xhtml {
			// HTML has no self-closing syntax. A void element takes no end
			// tag; every other empty element takes an explicit one, because
			// "<div/>" is parsed by HTML parsers as an unclosed "<div>".
			if s.isVoidElement(n.Name.Local) {
				s.writeString(">")
			} else {
				s.writeString("></" + name + ">")
			}
			return
		}
		if s.xhtml {
			// XHTML has to satisfy both parsers at once. An HTML parser
			// reading "<div/>" sees an unclosed start tag, so a non-void
			// element gets an explicit end tag; a void element has no end
			// tag in HTML at all, so it is written self-closed with the
			// space that the HTML compatibility guidelines ask for.
			//
			// The name decides, not the namespace. output-0217 and -0223
			// write the void names with no namespace at all and want them
			// self-closed, while -0219 and -0220 write non-void names the
			// same way and want a full end tag: what an HTML parser would
			// do with the name is the whole of the rule, and requiring the
			// XHTML namespace for it sent every no-namespace element down
			// the wrong half.
			if s.isVoidElement(n.Name.Local) {
				s.writeString(" />")
			} else {
				s.writeString("></" + name + ">")
			}
			return
		}
		// XML self-closes an empty element, which is the shortest spelling
		// and the one every parser reads identically.
		s.writeString("/>")
		return
	}

	s.pushScope(inScope, declared)
	s.writeString(">")

	// The HTML method adds the content-type meta so the encoding survives
	// being served without a charset header.
	// Only the html method writes script and style content raw. XHTML is
	// XML, where those elements hold ordinary parsed character data — the
	// suite's expected output escapes "<" and "&" inside a script there, and
	// a document that did not would not parse as XML at all.
	if s.html && !s.xhtml && isRawTextElement(n.Name.Local) {
		saved, savedName := s.rawText, s.rawTextName
		s.rawText, s.rawTextName = true, n.Name.Local
		defer func() { s.rawText, s.rawTextName = saved, savedName }()
	}
	// The head this belongs in is an HTML one. Under the html method every
	// element is HTML by definition, but the xhtml method serialises whatever
	// tree it is given, and a <head> of somebody else's vocabulary is not a
	// place to describe a media type -- output-0214 and -0215 build exactly
	// that and assert no meta appears.
	if s.html && strings.EqualFold(n.Name.Local, "head") &&
		(!s.xhtml || n.Name.URI == nsXHTML) {
		s.inHead = true
		defer func() { s.inHead = false }()
		// include-content-type="no" suppresses the meta element. It defaults
		// to yes, which is why an absent attribute is nil rather than false.
		if s.opts.IncludeContentType == nil || *s.opts.IncludeContentType {
			enc := s.opts.Encoding
			if enc == "" {
				enc = "UTF-8"
			}
			media := s.opts.MediaType
			if media == "" {
				// The default is text/html for the html *and* xhtml methods.
				// XHTML served as application/xhtml+xml is the stricter
				// choice, but the specification names text/html for both,
				// and this element exists to describe what a browser will
				// see rather than what the author would prefer.
				media = "text/html"
			}
			// A character map applies to the value of every attribute the
			// serializer writes, and this one is no exception: XSLT 3.0
			// section 27.1 puts the character map at the very end of the
			// serialization pipeline, after the HTML method has added its
			// meta element, so the substitution sees the injected value like
			// any other. The suite says so outright — character-map-017
			// carries the note "this character map will modify characters in
			// the generated meta element. Not very desirable but that's what
			// the spec says." Only the value is mapped; the element and
			// attribute names are markup the map never touches.
			content := s.mapChars(media + `; charset=` + enc)
			// The injected element is indented like a child of <head>,
			// because that is what it is. Writing it flush against the start
			// tag while the head's real children were each on their own line
			// put the document's own markup and the serialiser's on different
			// footings, which validation-0201 asserts against.
			if s.opts.Indent && !hasTextChild(n) {
				s.indent(depth + 1)
			}
			tag := `<meta http-equiv="Content-Type" content="` + content + `">`
			if s.xhtml {
				// XHTML is XML: an empty element must be closed. The space
				// before the slash is what the HTML compatibility guidelines
				// ask for, so that an HTML parser reading the same bytes does
				// not take the slash as part of the last attribute value.
				tag = strings.TrimSuffix(tag, ">") + " />"
			}
			s.writeString(tag)
		}
	}

	if s.cdataElems[xdm.QName{URI: n.Name.URI, Local: n.Name.Local}] &&
		!s.htmlNativeElement(n) {
		saved := s.inCData
		s.inCData = true
		defer func() { s.inCData = saved }()
	} else if s.inCData {
		// The parameter names the elements whose *own* text children are
		// wrapped, not a subtree. A nested element's text is escaped
		// normally unless that element is named too.
		s.inCData = false
		defer func() { s.inCData = true }()
	}

	// Indentation is suppressed for mixed content: adding whitespace around
	// text would change the element's string value, which for a validator's
	// output is a correctness issue rather than a cosmetic one.
	//
	// It is suppressed for the same reason, said explicitly rather than
	// inferred, when the element is named by suppress-indentation or carries
	// xml:space="preserve". Both are a statement that this element's content
	// is significant to the character: Serialization 3.1 §5 gives the
	// serialiser licence to add whitespace "only where the effect is not
	// significant", and these two are how a caller says where that is. An
	// <li> full of <p> elements has no text child at all, so the mixed-content
	// rule alone would have re-indented it and the suppress-indentation
	// parameter would have meant nothing.
	//
	// Suppression covers the whole subtree, not this element alone: the
	// point is that the content comes out as it went in, and re-indenting a
	// grandchild disturbs it exactly as much as re-indenting a child.
	indentChildren := s.opts.Indent && !hasTextChild(n) && !s.suppressed(n)
	for _, c := range n.Children {
		if indentChildren {
			s.node(c, depth+1)
		} else {
			s.nodeNoIndent(c)
		}
	}
	if indentChildren || (emptyHead && s.opts.Indent) {
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

// suppressed reports whether an element's content is written with no added
// whitespace: either it is named by suppress-indentation, or it declares
// xml:space="preserve".
//
// xml:space is honoured because a result carrying it says, in the document's
// own vocabulary, that its whitespace is data. Ignoring it while writing the
// attribute out would produce a document that contradicts itself: it would
// tell a reader to preserve whitespace the serialiser had just invented.
// htmlNativeElement reports whether an element is one the html output method
// writes as HTML rather than as foreign markup, which for that method means
// one in no namespace.
//
// It exists for cdata-section-elements, which the html method honours only
// for elements outside that set. HTML has no CDATA section: the sequence
// "<![CDATA[" inside a <p> is nine characters of text and an HTML parser
// reads it as such, so writing one there would change the document rather
// than only how its text was escaped. Serialization 3.1 §9.4 restricts the
// parameter to foreign content for that reason, and Serialization-html-18
// pins it down by naming "p em ex:isle1" together and asking for a CDATA
// section around exactly one of the three.
//
// The xhtml method is deliberately excluded: it produces XML, where a CDATA
// section is a CDATA section whatever the namespace, and output-0114 and
// -0138 ask for one around XHTML's own <example> and <h1>.
func (s *serializer) htmlNativeElement(n *xdm.Node) bool {
	return s.html && !s.xhtml && n.Name.URI == ""
}

func (s *serializer) suppressed(n *xdm.Node) bool {
	if s.noIndentElems[xdm.QName{URI: n.Name.URI, Local: n.Name.Local}] {
		return true
	}
	if a := n.Attr(xdm.NSXML, "space"); a != nil && a.Value == "preserve" {
		return true
	}
	return false
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
	// The text is split at the characters the map claims, so that
	// normalisation applies to the runs between them and not to the map's
	// inputs or its outputs. See mapSegments.
	for _, seg := range s.mapSegments(text) {
		s.escapeTextRun(&sb, seg.text)
		if s.err != nil {
			return
		}
		if seg.has {
			// A character map wins over escaping: emitting "&nbsp;" is the
			// whole reason a stylesheet declares one, and escaping the
			// ampersand would defeat it.
			sb.WriteString(seg.repl)
		}
	}
	s.writeString(sb.String())
}

// escapeTextRun escapes one run of character data into sb. It is the body of
// escapeText for a stretch of text no character map claims.
func (s *serializer) escapeTextRun(sb *strings.Builder, text string) {
	for _, r := range text {
		// HTML 4 gives #x7F-#x9F no meaning: the numeric character
		// references in that range name positions in the C1 control block,
		// and browsers historically remapped them to the windows-1252
		// characters instead. Writing one either way produces a document
		// whose meaning depends on the reader, so the spec forbids it.
		if s.html && !s.xhtml && r >= 0x7F && r <= 0x9F && !s.html5 {
			if s.err == nil {
				s.err = fmt.Errorf("SERE0014: character #x%X cannot be "+
					"output by the html method", r)
			}
			return
		}
		if !s.representable(r) {
			fmt.Fprintf(sb, "&#%d;", r)
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
			//
			// XHTML is excluded, which is why the guard is the file's usual
			// "html && !xhtml" and not a bare s.html: s.html is set for the
			// xhtml method too (see where it is assigned), but XHTML escapes
			// as XML, and "nbsp" is not one of XML's five predefined entity
			// names. Writing it there produced a document that references an
			// undeclared entity — unparseable by the XML parser the method
			// exists to satisfy — and the character needs no escape anyway:
			// it is representable in every encoding this serialiser emits
			// that the document would otherwise have to escape it for.
			if s.html && !s.xhtml {
				sb.WriteString("&nbsp;")
				continue
			}
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
}

// writeCData writes text as one or more CDATA sections.
//
// The section ends at the first "]]>", which the text may itself contain — so
// the text is split there and the ">" begins a new section. That is the only
// way to write the sequence inside CDATA at all, and it is what the
// specification's own example shows.
//
// A character map does not apply here. Inside a CDATA section nothing is
// escaped, so there is no escaping for a map to replace; the test suite
// checks that a mapped character survives unchanged in such an element.
func (s *serializer) writeCData(text string) {
	// A character the encoding cannot represent has to leave the section
	// too: there is no escaping inside CDATA, so the section is closed, the
	// character written as a numeric reference, and a new one opened. That
	// is the only spelling that both preserves the character and keeps every
	// byte inside the declared encoding.
	// Normalisation applies to the content of a CDATA section as it does to
	// any other text: the section is a way of writing characters, not a way
	// of exempting them. It matters because a normalisation can produce a
	// character the encoding cannot hold — NFD splits "\u00e7" into "c" and a
	// combining cedilla, and US-ASCII output must then break the section
	// around the cedilla rather than write it raw.
	text = s.normalized(text)
	var sb strings.Builder
	sb.WriteString("<![CDATA[")
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "]]>") {
			// The section ends at the first "]]>", which the text may itself
			// contain — so it is split there and the ">" begins a new one.
			sb.WriteString("]]]]><![CDATA[>")
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if !s.representable(r) {
			sb.WriteString("]]>")
			fmt.Fprintf(&sb, "&#%d;", r)
			sb.WriteString("<![CDATA[")
			i += size
			continue
		}
		sb.WriteString(text[i : i+size])
		i += size
	}
	sb.WriteString("]]>")
	s.writeString(sb.String())
}

// representable reports whether the declared encoding can hold this
// character. Only the ASCII-limited encodings restrict anything; the Unicode
// ones hold every character by construction.
func (s *serializer) representable(r rune) bool {
	if r < 0x80 {
		return true
	}
	switch strings.ToLower(s.opts.Encoding) {
	case "us-ascii", "ascii":
		return false
	case "iso-8859-1", "latin1":
		return r < 0x100
	}
	return true
}

// encodingHoldsAll reports whether the declared encoding can hold every
// character, so that a caller may escape a whole run at once instead of
// asking representable() about each rune. It is the run-level form of
// representable and must agree with it: the two switch on the same names.
func (s *serializer) encodingHoldsAll() bool {
	switch strings.ToLower(s.opts.Encoding) {
	case "us-ascii", "ascii", "iso-8859-1", "latin1":
		return false
	}
	return true
}

// normalized applies the requested Unicode normalisation, if any.
func (s *serializer) normalized(text string) string {
	if s.normalize == nil {
		return text
	}
	return s.normalize(text)
}

// mapSegments splits text at the characters a character map claims, and
// normalises everything in between.
//
// The two transforms have to be interleaved rather than run one after the
// other, because each would otherwise feed the other. Normalising first
// decomposes a character the map does not name into characters it does — with
// normalization-form="NFD" and a map for "c", the "\u00e7" of "abc\u00e7de"
// becomes "c" plus a combining cedilla, and the map rewrites that "c" too,
// substituting a character the stylesheet never mentioned. Mapping first and
// normalising afterwards is no better: it normalises the map's own output,
// which the specification says is written through untouched.
//
// So the map is matched against the original characters, and only the runs
// between them are normalised. Each run is normalised whole rather than
// character by character, since a combining sequence spans several characters
// and normalising each one alone would leave it exactly as it was.
//
// The result alternates: an unmapped run, then the replacement string for the
// character that ended it. Callers escape the runs and write the
// replacements verbatim, which is the point of declaring a map at all.
func (s *serializer) mapSegments(text string) []mapSegment {
	if len(s.charMap) == 0 {
		return []mapSegment{{text: s.normalized(text)}}
	}
	var segs []mapSegment
	run := 0
	for i, r := range text {
		repl, ok := s.charMap[r]
		if !ok {
			continue
		}
		segs = append(segs, mapSegment{
			text: s.normalized(text[run:i]),
			repl: repl,
			has:  true,
		})
		run = i + utf8.RuneLen(r)
	}
	return append(segs, mapSegment{text: s.normalized(text[run:])})
}

// mapSegment is one unmapped run of text and, where the run ended at a
// character the map claims, the string that replaces it.
type mapSegment struct {
	text string
	repl string
	// has distinguishes a replacement that is the empty string — a map may
	// legitimately delete a character — from no replacement at all.
	has bool
}

// mapChars applies the character map without escaping anything else, for the
// text output method — which writes characters as they stand.
func (s *serializer) mapChars(text string) string {
	if len(s.charMap) == 0 {
		return text
	}
	var sb strings.Builder
	sb.Grow(len(text))
	for _, r := range text {
		if repl, ok := s.charMap[r]; ok {
			sb.WriteString(repl)
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// attrValue writes an attribute value with its delimiters.
//
// A character map applies to attribute nodes as well as to text nodes, and
// the substituted string bypasses escaping — that is the point of declaring
// one. That leaves the delimiter: a map that produces a quotation mark cannot
// have it escaped, so the specification says the serialiser uses the other
// delimiter around the value where it can. Only where both quote characters
// appear is there no choice, and then the double quote is escaped.
func (s *serializer) attrValue(a *xdm.Node, owner *xdm.Node) string {
	// XSLT 1.0 section 16.2, carried into the serialization specification's
	// html output method: "The html output method should output boolean
	// attributes (that is attributes with only a single possible value that
	// is equal to the name of the attribute) in minimized form." CHECKED on
	// an INPUT is written as the bare name, not as CHECKED="CHECKED".
	//
	// The html method only. XHTML is XML, where an attribute without a value
	// is not well formed, and the XHTML compatibility guidelines say so
	// explicitly.
	if s.html && !s.xhtml && isBooleanAttribute(owner.Name.Local, a) {
		return ""
	}
	if s.html && s.escapeURIs() && isURIAttribute(owner.Name.Local, a) {
		// A character map does not reach a URI-valued attribute that is being
		// percent-escaped. The two rewrites contradict each other — the map
		// would substitute characters the escaping is there to encode — and
		// the serialization specification gives the escaping precedence.
		// character-map-009 checks exactly this: an href of "z-linkage.html"
		// keeps its "z" even with a map that rewrites "z" everywhere else.
		return `="` + s.escapeAttrRunes(escapeURIAttribute(s.normalized(a.Value))) + `"`
	}
	body, raw := s.escapeAttrMapped(a.Value, false)
	if raw && strings.Contains(body, `"`) && !strings.Contains(body, "'") {
		return "='" + body + "'"
	}
	return `="` + body + `"`
}

// escapeAttrMapped escapes an attribute value, passing character-mapped
// substitutions through untouched. The second result reports whether any
// substitution happened, which is what makes the delimiter choice necessary.
//
// uri asks for percent-escaping of the unmapped runs, for a URI-valued
// attribute of the html and xhtml methods. It applies to those runs only:
// percent-escaping a replacement string would defeat the map, and the test
// suite checks that a map leaves a URI attribute alone.
func (s *serializer) escapeAttrMapped(v string, uri bool) (string, bool) {
	var sb strings.Builder
	sb.Grow(len(v))
	mapped := false
	for _, seg := range s.mapSegments(v) {
		run := seg.text
		if uri {
			run = escapeURIAttribute(run)
		}
		// The whole-run spelling is only safe when every character of the run
		// can be written in the declared encoding. escapeAttr knows nothing
		// about the encoding and writes each rune raw, so an iso-8859-1 or
		// us-ascii output would carry UTF-8 bytes the declared encoding
		// cannot hold — element text goes through representable() and comes
		// out as "&#776;", while the same character in an attribute did not.
		// normalize-unicode-017/018 are exactly that asymmetry.
		if len(s.charMap) == 0 && !s.html && s.encodingHoldsAll() {
			sb.WriteString(escapeAttr(run))
		} else {
			for _, r := range run {
				sb.WriteString(s.escapeAttrRune(r))
			}
		}
		if seg.has {
			sb.WriteString(seg.repl)
			mapped = true
		}
	}
	return sb.String(), mapped
}

// escapeAttrRunes escapes a whole attribute value one character at a time,
// so that the html and xhtml spellings escapeAttrRune knows about apply. The
// percent-escaped URI path used escapeAttr directly and so wrote "&quot;"
// where the rest of the html serialiser writes "&#34;".
func (s *serializer) escapeAttrRunes(v string) string {
	var sb strings.Builder
	sb.Grow(len(v))
	for _, r := range v {
		sb.WriteString(s.escapeAttrRune(r))
	}
	return sb.String()
}

// escapeURIs reports whether URI-valued attributes are percent-escaped. The
// parameter defaults to yes, which is why it is a pointer.
func (s *serializer) escapeURIs() bool {
	return s.opts.EscapeURIAttributes == nil || *s.opts.EscapeURIAttributes
}

// escapeAttrRune escapes one character of an attribute value.
//
// The HTML and XHTML methods write the C1 range as a numeric character
// reference. Those code points are the ones HTML 4 leaves undefined and that
// browsers have historically remapped to windows-1252 characters, so a
// reference — which names a Unicode code point unambiguously — is the only
// spelling that survives being read back.
func (s *serializer) escapeAttrRune(r rune) string {
	if s.html && r >= 0x7F && r <= 0x9F || !s.representable(r) {
		return fmt.Sprintf("&#%d;", r)
	}
	if s.html && r == '"' {
		// The html and xhtml methods spell an embedded quotation mark as a
		// numeric reference rather than as "&quot;". HTML 4 defines &quot;
		// only in its own entity set, and the serialisation of an attribute
		// value has to be readable by a parser that has not loaded it; the
		// numeric form names the code point with no entity set at all.
		// output-0102c and output-0103c both accept &#34; or &#x22; and
		// nothing else.
		return "&#34;"
	}
	return escapeAttr(string(r))
}

// uriAttributes are the attributes the HTML DTD declares with type URI, whose
// values the html and xhtml methods percent-escape. The key is the element
// name, or "*" for an attribute that carries a URI on any element.
var uriAttributes = map[string]map[string]bool{
	"a":          {"href": true, "name": true},
	"applet":     {"codebase": true, "archive": true},
	"area":       {"href": true},
	"base":       {"href": true},
	"blockquote": {"cite": true},
	"body":       {"background": true},
	"del":        {"cite": true},
	"form":       {"action": true},
	"frame":      {"src": true, "longdesc": true},
	"head":       {"profile": true},
	"iframe":     {"src": true, "longdesc": true},
	"img":        {"src": true, "longdesc": true, "usemap": true},
	"input":      {"src": true, "usemap": true},
	"ins":        {"cite": true},
	"link":       {"href": true},
	"object":     {"classid": true, "codebase": true, "data": true, "usemap": true, "archive": true},
	"q":          {"cite": true},
	"script":     {"src": true, "for": true},
	"*":          {},
}

// booleanAttributes are the attributes HTML 4.01 declares with the single
// permitted value equal to their own name, keyed by element name. These are
// the ones the html output method writes in minimized form.
//
// Keyed by element for the same reason uriAttributes is: "selected" is a
// boolean on <option> and an ordinary attribute anywhere the HTML DTD does
// not declare it, and minimizing it elsewhere would delete a value the
// stylesheet put there. The list is the complete set from the HTML 4.01 DTD.
var booleanAttributes = map[string]map[string]bool{
	"area":     {"nohref": true},
	"button":   {"disabled": true},
	"dir":      {"compact": true},
	"dl":       {"compact": true},
	"frame":    {"noresize": true},
	"hr":       {"noshade": true},
	"img":      {"ismap": true},
	"input":    {"checked": true, "disabled": true, "ismap": true, "readonly": true},
	"menu":     {"compact": true},
	"object":   {"declare": true},
	"ol":       {"compact": true},
	"optgroup": {"disabled": true},
	"option":   {"disabled": true, "selected": true},
	"script":   {"defer": true},
	"select":   {"disabled": true, "multiple": true},
	"td":       {"nowrap": true},
	"textarea": {"disabled": true, "readonly": true},
	"th":       {"nowrap": true},
	"ul":       {"compact": true},
}

// isBooleanAttribute reports whether an attribute is one the html output
// method writes as a bare name.
//
// The value must equal the attribute's own name, ignoring case: that is what
// "only a single possible value that is equal to the name" means, and an
// author who wrote checked="false" said something the minimized form cannot
// express. Writing the bare name for it would turn a value the stylesheet
// chose into its opposite, so such an attribute is serialised in full.
//
// A namespaced attribute is never one of these -- the HTML DTD declares no
// namespaces -- which is the same guard isURIAttribute applies.
func isBooleanAttribute(element string, a *xdm.Node) bool {
	if a.Name.URI != "" {
		return false
	}
	attrs, ok := booleanAttributes[strings.ToLower(element)]
	if !ok {
		return false
	}
	if !attrs[strings.ToLower(a.Name.Local)] {
		return false
	}
	return strings.EqualFold(a.Value, a.Name.Local)
}

// isURIAttribute reports whether an attribute holds a URI, and so is subject
// to percent-escaping in the html and xhtml output methods.
//
// The list is by element and attribute name, because "href" is a URI on <a>
// and an ordinary string on an element the HTML DTD does not define it for.
// The test suite checks exactly that distinction: accesskey on <a> holds a
// character, not a URI, and must not be escaped.
func isURIAttribute(element string, a *xdm.Node) bool {
	if a.Name.URI != "" {
		return false
	}
	attrs, ok := uriAttributes[strings.ToLower(element)]
	if !ok {
		return false
	}
	return attrs[strings.ToLower(a.Name.Local)]
}

// escapeURIAttribute percent-escapes the characters a URI cannot hold.
//
// Only non-ASCII characters are escaped. XSLT 1.0 section 16.2 and the
// Serialization Recommendation both delegate to HTML 4.0 appendix B.2.1,
// which escapes "characters ... outside the range of US-ASCII" and nothing
// else. ASCII characters that are merely illegal in a URI — space, quote,
// angle brackets, and the rest of " <>\"{}|^`" — are left for ordinary
// attribute escaping to deal with; percent-escaping them here turned
// href='% %C2%96 ... a " < > &' into a value in which every separator had
// been rewritten. A "%" is likewise left alone: a value that is already
// escaped must survive unchanged, or every round trip through a stylesheet
// would double-escape it.
//
// The value is put into NFC first. Percent-escaping an IRI is defined by
// RFC 3987 section 3.1, which normalises to NFC before encoding the UTF-8
// bytes, so a decomposed "a" plus combining ring and a precomposed "\u00e5"
// escape to the same %C3%A5 rather than to two different byte sequences that
// no longer compare equal as URIs.
func escapeURIAttribute(v string) string {
	v = norm.NFC.String(v)
	var sb strings.Builder
	sb.Grow(len(v))
	for _, r := range v {
		if r < 0x80 {
			sb.WriteRune(r)
			continue
		}
		for _, b := range []byte(string(r)) {
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
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

// isContentTypeMeta reports whether an element is a meta declaring the
// content type, in the http-equiv spelling. Case is ignored on both the
// attribute name and its value, which is how HTTP header names compare.
func isContentTypeMeta(n *xdm.Node) bool {
	for _, a := range n.Attrs {
		if a.Name.URI == "" && strings.EqualFold(a.Name.Local, "http-equiv") &&
			strings.EqualFold(strings.TrimSpace(a.Value), "content-type") {
			return true
		}
	}
	return false
}

// nsXHTML is the namespace an element must be in for the XHTML output
// method's HTML-specific rules to apply to it. An element outside it is
// ordinary XML that happens to be serialised by this method.
const nsXHTML = "http://www.w3.org/1999/xhtml"

// voidElements are the HTML elements that take no end tag. Writing one with a
// closing tag, or self-closing a non-void element, both produce a tree that
// an HTML parser reads differently from the one the stylesheet built.
// voidElements are the names with an empty content model in every HTML
// version this serialiser writes. Three more are version-specific and live in
// the two maps below.
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

// isVoidElement reports whether an element takes no end tag.
//
// The version decides for six of the names, which is what
// Serialization-html-1 and -2 are for: the first writes the HTML 4 list under
// version="4.0" and the second the HTML 5 list under version="5.0", and each
// asks for exactly its own set minimised. A serialiser with one combined list
// passes both only by accident and fails the moment a case names an element
// from the other version.
func (s *serializer) isVoidElement(local string) bool {
	local = strings.ToLower(local)
	if voidElements[local] {
		return true
	}
	if s.html5 {
		return html5VoidElements[local]
	}
	return html4VoidElements[local]
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

// defaultMethod chooses the output method for a result with no explicit one.
//
// The rule reads the first element child of the result: "html" in the XHTML
// namespace selects xhtml, "html" in no namespace — in any case — selects
// html, and everything else, including a result whose first element is
// preceded by non-whitespace text, is xml.
//
// v10Implicit turns off the xhtml case. Section 20 makes it the one
// exception: "if the version attribute of the xsl:stylesheet element of the
// principal stylesheet module has the value 1.0, and if the result tree is
// generated implicitly (rather than by an explicit xsl:result-document
// instruction), then the default output method in this situation is xml".
// A 1.0 stylesheet predates the xhtml method, so a result that happens to be
// rooted at an XHTML html element was never asking for it — and choosing
// xhtml would add a content-type meta and percent-escape its URIs, changing
// output the stylesheet's author had already settled.
func defaultMethod(seq xdm.Sequence, v10Implicit bool) string {
	var first *xdm.Node
	var scan func(xdm.Sequence) bool
	scan = func(items xdm.Sequence) bool {
		for _, it := range items {
			switch v := it.(type) {
			case *xdm.Node:
				switch v.Kind {
				case xdm.KindDocument:
					kids := make(xdm.Sequence, 0, len(v.Children))
					for _, c := range v.Children {
						kids = append(kids, c)
					}
					if !scan(kids) {
						return false
					}
				case xdm.KindElement:
					if first == nil {
						first = v
					}
					return false
				case xdm.KindText:
					// Text before the first element rules out the HTML
					// methods, which describe a document rather than an
					// arbitrary sequence — unless it is only whitespace.
					if !xdm.IsXMLWhitespace(v.Value) {
						return false
					}
				}
			case *xdm.Atomic:
				if !xdm.IsXMLWhitespace(v.String()) {
					return false
				}
			}
		}
		return true
	}
	scan(seq)
	if first == nil {
		return "xml"
	}
	switch {
	case first.Name.URI == nsXHTML && first.Name.Local == "html":
		if v10Implicit {
			return "xml"
		}
		return "xhtml"
	case first.Name.URI == "" && strings.EqualFold(first.Name.Local, "html"):
		return "html"
	}
	return "xml"
}

// checkOutputSettings reports the serialization errors that a set of output
// parameters raises on its own, before any node is written.
//
// These are the conflicts the Serialization specification names: a parameter
// whose value this serialiser cannot honour, and a combination of parameters
// that contradict each other. Each is a serialization error rather than
// something to fall back from, because every fallback would produce a
// document that differs from the one the stylesheet asked for without saying
// so.
func checkOutputSettings(opts OutputSettings, seq xdm.Sequence) error {
	method := strings.ToLower(opts.Method)
	if method == "" {
		method = "xml"
	}

	// Sequence normalisation, step 1 (Serialization 3.1, 2): an item that is
	// a function -- which a map and an array both are in the data model --
	// has no serialised form under the xml, html, xhtml and text methods,
	// and the error is raised in place of writing something plausible. The
	// json and adaptive methods do not normalise the sequence and so are not
	// subject to it. output-0710 through -0712 are one map sequence sent to
	// each of xml, html and the method chosen from the result.
	for _, it := range seq {
		switch it.(type) {
		case *xdm.MapItem, *xdm.ArrayItem, *xdm.FunctionItem:
			return fmt.Errorf(
				"SENR0001: an item in the sequence to serialize is %s, "+
					"which the %s output method cannot serialize",
				it.TypeName(), method)
		}
	}

	// An encoding this serialiser cannot produce. Everything is written as
	// UTF-8, and the encodings that are byte-for-byte compatible enough to
	// name are the Unicode ones; anything else would be a declaration that
	// lies about the bytes that follow.
	if enc := opts.Encoding; enc != "" && !supportedEncoding(enc) {
		return fmt.Errorf("SESU0007: encoding %q is not supported", enc)
	}

	// A form the serialiser cannot apply. Claiming a normalisation while
	// emitting unnormalised text would be worse than refusing: a consumer
	// that trusts the parameter would compare the output against a
	// normalised form and find a difference that is not really there.
	if nf := opts.NormalizationForm; nf != "" && normalizerFor(nf) == nil &&
		nf != "none" {
		return fmt.Errorf(
			"SESU0011: normalization form %q is not supported", nf)
	}

	// A public identifier is restricted to the PubidChar production. A
	// character outside it cannot be written between the quotes at all.
	if p := opts.DocTypePublic; p != "" && !isPubidLiteral(p) {
		return fmt.Errorf(
			"SEPM0016: %q is not a valid public identifier", p)
	}

	if method == "html" {
		// The html method supports the HTML versions it knows how to write.
		// An unrecognised one would silently get HTML 4 rules, which for a
		// stylesheet that asked for something else is the wrong document.
		if v := opts.Version; v != "" && !supportedHTMLVersion(v) {
			return fmt.Errorf(
				"SESU0013: HTML version %q is not supported", v)
		}
		return nil
	}
	if method != "xml" && method != "xhtml" {
		return nil
	}

	// undeclare-prefixes needs the xmlns:p="" syntax, which only XML 1.1
	// permits. Asking for it with version 1.0 is a request the output cannot
	// express.
	if opts.UndeclarePrefixes && !strings.HasPrefix(opts.Version, "1.1") {
		return fmt.Errorf("SEPM0010: undeclare-prefixes requires XML 1.1, " +
			"but the output version is 1.0")
	}

	// standalone and a document type declaration both live in the prolog,
	// which omit-xml-declaration removes. Honouring both would mean writing
	// a declaration that was asked to be omitted.
	if opts.OmitXMLDecl {
		if opts.Standalone != "" && opts.Standalone != "omit" {
			return fmt.Errorf("SEPM0009: standalone=%q cannot be written "+
				"when omit-xml-declaration is yes", opts.Standalone)
		}
		if opts.DocTypeSystem != "" && opts.Version != "" &&
			!strings.HasPrefix(opts.Version, "1.0") {
			return fmt.Errorf("SEPM0009: doctype-system with version %q "+
				"cannot be written when omit-xml-declaration is yes",
				opts.Version)
		}
	}

	// A standalone declaration and a document type declaration each describe
	// a document with exactly one element. A result with several top-level
	// elements, or with text beside them, is not one — it is an external
	// general parsed entity, and neither construct is legal there.
	if opts.Standalone != "" && opts.Standalone != "omit" ||
		opts.DocTypeSystem != "" || opts.DocTypePublic != "" {
		if !isWellFormedDocument(seq) {
			return fmt.Errorf("SEPM0004: the result is not a well-formed " +
				"document, so a standalone or doctype declaration cannot " +
				"be written")
		}
	}
	return nil
}

// normalizerFor returns the Unicode normalisation the named form asks for, or
// nil when this serialiser cannot apply it.
//
// "fully-normalized" is not one of the four Unicode forms: it additionally
// requires that no text node begin with a combining character, which is a
// property of the whole result rather than a transformation of it. Refusing
// it is honest; pretending it is NFC would not be.
func normalizerFor(form string) func(string) string {
	var f norm.Form
	switch form {
	case "NFC":
		f = norm.NFC
	case "NFD":
		f = norm.NFD
	case "NFKC":
		f = norm.NFKC
	case "NFKD":
		f = norm.NFKD
	default:
		return nil
	}
	return f.String
}

// isWellFormedDocument reports whether a result sequence would serialise as an
// XML document rather than as an entity: exactly one element, and no text
// beside it.
func isWellFormedDocument(seq xdm.Sequence) bool {
	elems := 0
	var walk func(xdm.Sequence) bool
	walk = func(items xdm.Sequence) bool {
		for _, it := range items {
			switch v := it.(type) {
			case *xdm.Node:
				switch v.Kind {
				case xdm.KindDocument:
					kids := make(xdm.Sequence, 0, len(v.Children))
					for _, c := range v.Children {
						kids = append(kids, c)
					}
					if !walk(kids) {
						return false
					}
				case xdm.KindElement:
					elems++
				case xdm.KindText:
					// Whitespace between top-level nodes is permitted in a
					// document; anything else is character data outside the
					// document element.
					if !xdm.IsXMLWhitespace(v.Value) {
						return false
					}
				}
			case *xdm.Atomic:
				if !xdm.IsXMLWhitespace(v.String()) {
					return false
				}
			}
		}
		return true
	}
	if !walk(seq) {
		return false
	}
	return elems == 1
}

// supportedEncoding reports whether the serialiser can write this encoding.
//
// Output is always UTF-8 bytes, so the encodings that can be named honestly
// are the ones whose declaration matches those bytes. The rest are refused
// rather than approximated.
func supportedEncoding(enc string) bool {
	switch strings.ToLower(enc) {
	case "utf-8", "utf8", "utf-16", "utf16", "utf-16be", "utf-16le",
		"us-ascii", "ascii", "iso-8859-1", "latin1", "iso-8859-15",
		"windows-1252", "iso-10646-ucs-2", "iso-10646-ucs-4":
		return true
	}
	return false
}

// supportedHTMLVersion reports whether the html output method knows the rules
// for this version of HTML.
func supportedHTMLVersion(v string) bool {
	switch v {
	case "4.0", "4.01", "4", "5", "5.0", "1.0", "1.1":
		return true
	}
	return false
}

// isPubidLiteral reports whether every character is a PubidChar, the
// production XML restricts a public identifier to.
func isPubidLiteral(s string) bool {
	for _, r := range s {
		switch {
		case r == 0x20 || r == 0x0D || r == 0x0A:
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-'()+,./:=?;!*#@$_%", r):
		default:
			return false
		}
	}
	return true
}

// insertItemSeparator implements step 3 of sequence normalisation: with an
// item separator in force, a text node holding it goes between every pair of
// adjacent items of the result sequence.
//
// A nil separator means the attribute was absent and the default rules stand
// — a single space between adjacent atomic values, nothing between nodes —
// so the sequence is returned untouched. An empty separator is not the same
// thing: it asks for nothing between any pair, including between two atomic
// values that would otherwise be spaced, so it still runs and still clears
// the runs the serialiser would have joined.
func insertItemSeparator(seq xdm.Sequence, sep *string) xdm.Sequence {
	if sep == nil || len(seq) < 2 {
		return seq
	}
	out := make(xdm.Sequence, 0, 2*len(seq)-1)
	for i, it := range seq {
		if i > 0 {
			out = append(out, &xdm.Node{Kind: xdm.KindText, Value: *sep})
		}
		out = append(out, it)
	}
	return out
}

// flattenArrays replaces every array in a sequence by its members, and does
// so to any depth.
//
// Sequence normalisation calls for it because an array is not a thing the
// XML, XHTML, HTML or text output methods can write, but its members usually
// are: serialising [<a/>] and <a/> to different results -- one a document,
// the other an error -- would make the brackets change what the document
// says rather than only how the value was assembled. The json and adaptive
// methods do not go through normalisation at all and keep their arrays,
// which is the whole difference between "[1,2]" and "12".
//
// The common case is a sequence with no array in it, which is returned as it
// stands rather than copied.
func flattenArrays(seq xdm.Sequence) xdm.Sequence {
	has := false
	for _, it := range seq {
		if _, ok := it.(*xdm.ArrayItem); ok {
			has = true
			break
		}
	}
	if !has {
		return seq
	}
	out := make(xdm.Sequence, 0, len(seq))
	for _, it := range seq {
		if a, ok := it.(*xdm.ArrayItem); ok {
			for _, m := range a.Members() {
				out = append(out, flattenArrays(m)...)
			}
			continue
		}
		out = append(out, it)
	}
	return out
}
