package xdm

import (
	"fmt"
	"regexp"
	"strings"

	xml "github.com/knroy/go-xml/internal/xmlfork"
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
	// xmlfork's PI scanner consumes the required separating space between the
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
	// Build the in-scope environment before looking at ordinary attributes. A
	// declaration on this start tag is in scope for its own element and attrs.
	bindings := map[string]string{"xml": NSXML}
	for p := parent; p != nil; p = p.Parent {
		for _, ns := range p.Namespaces {
			if _, seen := bindings[ns.Name.Local]; !seen {
				bindings[ns.Name.Local] = ns.Value
			}
		}
	}

	declared := map[string]bool{}
	for _, a := range t.Attr {
		prefix, isDecl := namespaceDecl(a)
		if !isDecl {
			continue
		}
		if declared[prefix] {
			return fmt.Errorf("parse XML: duplicate namespace declaration for prefix %q", prefix)
		}
		declared[prefix] = true
		if err := validateNamespaceBinding(prefix, a.Value, xml11); err != nil {
			return err
		}
		bindings[prefix] = a.Value
	}

	if err := requireBoundPrefix(t.Name.Space, bindings); err != nil {
		return err
	}
	if t.Name.Space == "xmlns" {
		return fmt.Errorf("parse XML: xmlns prefix cannot be used in an element name")
	}

	// XML checks raw names; Namespaces in XML strengthens that to expanded names.
	// The URI/NUL/local key makes p:a and q:a collide when p and q bind alike.
	seen := map[string]bool{}
	for _, a := range t.Attr {
		if _, declaration := namespaceDecl(a); declaration {
			continue
		}
		if err := requireBoundPrefix(a.Name.Space, bindings); err != nil {
			return err
		}
		uri := ""
		if a.Name.Space != "" {
			uri = bindings[a.Name.Space]
		}
		key := uri + "\x00" + a.Name.Local
		if seen[key] {
			return fmt.Errorf("parse XML: duplicate attribute {%s}%s", uri, a.Name.Local)
		}
		seen[key] = true
	}
	return nil
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
func requireBoundPrefix(prefix string, bindings map[string]string) error {
	if prefix == "" {
		return nil
	}
	if uri, ok := bindings[prefix]; !ok || uri == "" {
		return fmt.Errorf("parse XML: no namespace declaration is in scope for prefix %q", prefix)
	}
	return nil
}
