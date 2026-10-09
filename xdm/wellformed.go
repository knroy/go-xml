package xdm

import (
	"fmt"
	"regexp"
	"strings"

	xml "github.com/knroy/go-xml/v2/internal/xmltok"
)

// xmlDeclSyntax is productions [23]-[26] reduced to the declarations this
// parser supports. It deliberately validates the *whole* PI data: searching
// for version= would accept duplicate fields and trailing garbage, both of
// which make a document not well formed.
//
// Each name is separated from its value by [25] Eq ::= S? '=' S?, not by a
// bare "=". Requiring the bare form rejected `version = "1.0"` and
// `encoding = "UTF-8"`, both well formed and both used by the QT3 corpus.
const (
	xmlDeclS  = `[ \t\r\n]`
	xmlDeclEq = xmlDeclS + `*=` + xmlDeclS + `*`
)

// standaloneYes finds standalone="yes" in a declaration xmlDeclSyntax has
// already accepted.
var standaloneYes = regexp.MustCompile(`standalone` + xmlDeclEq + `(?:"yes"|'yes')`)

var xmlDeclSyntax = regexp.MustCompile(
	`^version` + xmlDeclEq + `(?:"1\.[0-9]+"|'1\.[0-9]+')` +
		`(?:` + xmlDeclS + `+encoding` + xmlDeclEq +
		`(?:"[A-Za-z][A-Za-z0-9._-]*"|'[A-Za-z][A-Za-z0-9._-]*'))?` +
		`(?:` + xmlDeclS + `+standalone` + xmlDeclEq +
		`(?:"(?:yes|no)"|'(?:yes|no)'))?$`)

// validateXMLDecl checks the XML declaration separately from the token reader.
// RawToken deliberately exposes it as a PI so clients that want a token stream
// can decide what to do with it; Parse, however, promises an XML document.
func validateXMLDecl(inst string) error {
	// xmltok's PI scanner consumes the required separating space between the
	// target and its data, so Inst begins directly with "version=".
	if inst == "" {
		return fmt.Errorf("parse XML: XML declaration must contain VersionInfo")
	}
	if !xmlDeclSyntax.MatchString(strings.TrimSpace(inst)) {
		return fmt.Errorf("parse XML: malformed XML declaration")
	}
	return nil
}

// isXMLSpace is XML's S production, not Unicode whitespace. In particular,
// accepting NBSP here would make an XML 1.0 document valid only to this parser.
func isXMLSpace(b byte, xml11 bool) bool {
	if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
		return true
	}
	// XML 1.1 adds NEL and line separator to S. CharData has already been
	// decoded to UTF-8, so its multi-byte spellings are handled by
	// isXMLWhitespace below.
	return false
}

// isXMLWhitespace is the rune form of S for text already decoded to UTF-8.
// It is used outside the document element, where XML permits only Misc and S.
func isXMLWhitespace(s string, xml11 bool) bool {
	for _, r := range s {
		switch r {
		case ' ', '\t', '\r', '\n':
		case '\u0085', '\u2028':
			if !xml11 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isDOCTYPEDirective rejects look-alikes such as <!DOCTYPEfoo>. The tokeniser
// returns every markup declaration as Directive; document grammar belongs here.
func isDOCTYPEDirective(d string) bool {
	if !strings.HasPrefix(d, "DOCTYPE") || len(d) == len("DOCTYPE") {
		return false
	}
	return isXMLSpace(d[len("DOCTYPE")], false)
}

// validateStartElement checks the constraints RawToken intentionally leaves to
// callers: duplicate attributes and namespace well-formedness. It runs before
// buildElement so an invalid document cannot become an XDM tree.
func validateStartElement(t xml.StartElement, parent *Node, xml11 bool) error {
	// Namespaces in XML §3 [7] QName. The tokeniser splits a name at its colon
	// but leaves "p:", ":l" and "xmlns:" whole in Local, so a colon still there
	// is a prefix or local part that is empty, which no QName has.
	if err := requireQName(t.Name); err != nil {
		return err
	}
	for _, a := range t.Attr {
		if err := requireQName(a.Name); err != nil {
			return err
		}
	}

	// The in-scope environment, against which every prefix on the tag is
	// resolved. A declaration on this start tag is in scope for its own
	// element and attrs; see tagScope for how it is looked up.
	scope := tagScope{t: t, parent: parent}
	if len(t.Attr) > smallTag {
		scope.m = map[string]string{}
	}
	for i, a := range t.Attr {
		prefix, isDecl := namespaceDecl(a)
		if !isDecl {
			continue
		}
		if scope.declaredBefore(i, prefix) {
			return fmt.Errorf("parse XML: duplicate namespace declaration for prefix %q", prefix)
		}
		if err := validateNamespaceBinding(prefix, a.Value, xml11); err != nil {
			return err
		}
		if scope.m != nil {
			scope.m[prefix] = a.Value
		}
	}
	scope.inherit()

	if err := requireBoundPrefix(t.Name.Space, &scope); err != nil {
		return err
	}
	if t.Name.Space == "xmlns" {
		return fmt.Errorf("parse XML: xmlns prefix cannot be used in an element name")
	}

	// XML checks raw names; Namespaces in XML strengthens that to expanded names.
	// Keying on {URI, local} makes p:a and q:a collide when p and q bind alike.
	// A tag's few attributes are checked by a scan of those before them, which
	// allocates nothing; past a handful the keys move into a map, so that a tag
	// with thousands of attributes is not checked in quadratic time.
	var small [smallTag]expandedName
	seen := small[:0]
	var many map[expandedName]bool
	for _, a := range t.Attr {
		if _, declaration := namespaceDecl(a); declaration {
			continue
		}
		if err := requireBoundPrefix(a.Name.Space, &scope); err != nil {
			return err
		}
		uri := ""
		if a.Name.Space != "" {
			uri, _ = scope.lookup(a.Name.Space)
		}
		k := expandedName{uri, a.Name.Local}
		dup := many[k]
		for _, s := range seen {
			dup = dup || s == k
		}
		if dup {
			return fmt.Errorf("parse XML: duplicate attribute {%s}%s", uri, a.Name.Local)
		}
		switch {
		case many != nil:
			many[k] = true
		case len(seen) < len(small):
			seen = append(seen, k)
		default:
			many = make(map[expandedName]bool, 2*len(small))
			for _, s := range seen {
				many[s] = true
			}
			many[k] = true
			seen = nil
		}
	}
	return nil
}

// expandedName is an attribute's {namespace URI, local name}.
type expandedName struct{ uri, local string }

// smallTag is how many attributes a start tag may carry and still be checked
// by scanning it rather than through maps.
const smallTag = 8

// tagScope resolves the prefixes used on one start tag: the tag's own
// declarations first, then the xml prefix, then the nearest ancestor that
// declares the prefix. Building that as a map per start tag, as this once did,
// copied every ancestor's declarations for every element parsed, a large
// share of what loading a stylesheet or schema allocated. A tag of at most
// smallTag attributes is scanned instead, which allocates nothing; a larger
// one fills m once, so that each of its attributes is not a scan of the tag
// and of every ancestor.
type tagScope struct {
	t      xml.StartElement
	parent *Node
	m      map[string]string // every binding in scope, when the tag is large
}

// declaredBefore reports whether an attribute before t.Attr[i] declares
// prefix. With m in use it holds exactly the tag's declarations so far.
func (s *tagScope) declaredBefore(i int, prefix string) bool {
	if s.m != nil {
		_, ok := s.m[prefix]
		return ok
	}
	for _, a := range s.t.Attr[:i] {
		if p, ok := namespaceDecl(a); ok && p == prefix {
			return true
		}
	}
	return false
}

// inherit completes m, once the tag's own declarations are in it, with the
// bindings it does not override.
func (s *tagScope) inherit() {
	if s.m == nil {
		return
	}
	if _, ok := s.m["xml"]; !ok {
		s.m["xml"] = NSXML
	}
	for p := s.parent; p != nil; p = p.Parent() {
		for _, b := range p.frame() {
			if _, ok := s.m[b.prefix]; !ok {
				s.m[b.prefix] = b.uri
			}
		}
	}
}

// lookup returns the URI bound to prefix, and whether any binding is in scope.
// An undeclaration (XML 1.1 xmlns:p="") is a binding to "".
func (s *tagScope) lookup(prefix string) (string, bool) {
	if s.m != nil {
		uri, ok := s.m[prefix]
		return uri, ok
	}
	for _, a := range s.t.Attr {
		if p, ok := namespaceDecl(a); ok && p == prefix {
			return a.Value, true
		}
	}
	if prefix == "xml" {
		return NSXML, true
	}
	for p := s.parent; p != nil; p = p.Parent() {
		for _, b := range p.frame() {
			if b.prefix == prefix {
				return b.uri, true
			}
		}
	}
	return "", false
}

func requireQName(n xml.Name) error {
	if strings.Contains(n.Local, ":") {
		return fmt.Errorf("parse XML: %q is not a QName", lexicalName(n))
	}
	return nil
}

// normalizeAttTokens applies the XML 1.0 §3.3.3 collapse for DTD-typed
// attributes to the raw tag, ahead of validateStartElement. Namespaces in
// XML §3 binds a prefix to the attribute's *normalized* value, so xmlns:b
// declared NMTOKEN and written " urn:x " binds urn:x — and p:a, b:a collide.
func normalizeAttTokens(t xml.StartElement, types []attDeclaredType) xml.StartElement {
	var attrs []xml.Attr
	for _, d := range types {
		if !lexicalIs(t.Name.Space, t.Name.Local, d.element) && d.element != t.Name.Local {
			continue
		}
		for i, a := range t.Attr {
			if !lexicalIs(a.Name.Space, a.Name.Local, d.attr) && a.Name.Local != d.attr {
				continue
			}
			if attrs == nil {
				// Copy: the tokeniser's attribute slice is not ours to write.
				attrs = append([]xml.Attr(nil), t.Attr...)
			}
			attrs[i].Value = strings.Join(strings.FieldsFunc(a.Value, func(r rune) bool { return r == ' ' }), " ")
		}
	}
	if attrs != nil {
		t.Attr = attrs
	}
	return t
}

// namespaceDecl recognizes the lexical representation RawToken preserves.
// Namespace declarations are not ordinary attributes for the uniqueness rule.
func namespaceDecl(a xml.Attr) (string, bool) {
	if a.Name.Space == "xmlns" {
		return a.Name.Local, true
	}
	return "", a.Name.Space == "" && a.Name.Local == "xmlns"
}

// validateNamespaceBinding implements the reserved-name constraints from
// Namespaces in XML §3. XML 1.1 alone permits a prefixed undeclaration.
func validateNamespaceBinding(prefix, uri string, xml11 bool) error {
	switch {
	case prefix == "xmlns":
		return fmt.Errorf("parse XML: xmlns prefix must not be declared")
	case uri == NSXMLNS:
		return fmt.Errorf("parse XML: namespace URI %q is reserved for xmlns", NSXMLNS)
	case prefix == "xml" && uri != NSXML:
		return fmt.Errorf("parse XML: xml prefix must be bound to %q", NSXML)
	case prefix != "xml" && uri == NSXML:
		return fmt.Errorf("parse XML: only xml prefix may be bound to %q", NSXML)
	case prefix != "" && uri == "" && !xml11:
		return fmt.Errorf("parse XML: XML 1.0 does not allow undeclaring prefix %q", prefix)
	}
	return nil
}

// requireBoundPrefix applies the Prefix Declared constraint. Returning an empty
// URI is not a harmless recovery: it changes a namespace-ill-formed document
// into a different XDM name, so public document parsing must fail instead.
func requireBoundPrefix(prefix string, scope *tagScope) error {
	if prefix == "" {
		return nil
	}
	if uri, ok := scope.lookup(prefix); !ok || uri == "" {
		return fmt.Errorf("parse XML: no namespace declaration is in scope for prefix %q", prefix)
	}
	return nil
}
