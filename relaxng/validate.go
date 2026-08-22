package relaxng

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Schema is a compiled RELAX NG schema.
//
// It is immutable once built and safe to share across goroutines, like a
// compiled XSD schema: validation takes derivatives of the pattern rather than
// mutating it.
type Schema struct {
	start Pattern
}

// Error is one validity failure.
type Error struct {
	Path    string
	Message string
}

func (e *Error) Error() string { return e.Path + ": " + e.Message }

// Validate checks a document against the schema.
//
// The result is a single error rather than a list, which is the shape the
// derivative algorithm gives: a pattern that reaches NotAllowed carries no
// record of the alternatives it tried, so there is one failure and it is the
// point at which every branch died. Reporting the *last* place the document
// was still viable is more useful than reporting the root.
func (s *Schema) Validate(doc *xdm.Node) error {
	if doc == nil {
		return fmt.Errorf("relaxng: nil document")
	}
	root := doc
	if root.Kind == xdm.KindDocument {
		root = nil
		for _, c := range doc.Children {
			if c.Kind == xdm.KindElement {
				root = c
				break
			}
		}
		if root == nil {
			return &Error{Path: "/", Message: "the document has no root element"}
		}
	}
	v := &validator{}
	p := v.childDeriv(s.start, root)
	if !p.nullable() {
		where := v.deepest
		if where == "" {
			where = "/" + root.Name.Local
		}
		return &Error{Path: where, Message: v.why}
	}
	return nil
}

type validator struct {
	// deepest records the furthest point the document was still viable, and
	// why it stopped being so. A derivative that fails says only "no", so the
	// path has to be captured on the way down.
	deepest string
	why     string
	path    []string
}

func (v *validator) at() string {
	return "/" + strings.Join(v.path, "/")
}

func (v *validator) note(msg string) {
	// Keep the deepest failure: an outer pattern reporting "content is not
	// allowed" is less useful than the inner one naming the element that
	// could not be placed.
	if len(v.at()) >= len(v.deepest) {
		v.deepest, v.why = v.at(), msg
	}
}

// childDeriv takes the derivative with respect to one node of content.
func (v *validator) childDeriv(p Pattern, n *xdm.Node) Pattern {
	switch n.Kind {
	case xdm.KindText:
		if whitespaceOnly(n.Value) {
			// Whitespace between elements is not content unless the pattern
			// asks for text: a schema written across lines must not fail
			// because of its own indentation.
			if isNotAllowed(textDeriv(p, n.Value)) {
				return p
			}
		}
		return textDeriv(p, n.Value)

	case xdm.KindElement:
		v.path = append(v.path, n.Name.Local)
		defer func() { v.path = v.path[:len(v.path)-1] }()

		name := xdm.QName{URI: n.Name.URI, Local: n.Name.Local}
		p1 := startTagOpenDeriv(p, name)
		if isNotAllowed(p1) {
			v.note(fmt.Sprintf("element %s is not permitted here", n.Name.Local))
			return NotAllowed{}
		}
		p1 = attsDeriv(p1, elementAttrs(n))
		if isNotAllowed(p1) {
			v.note(fmt.Sprintf("the attributes of %s do not match", n.Name.Local))
			return NotAllowed{}
		}
		p1 = startTagCloseDeriv(p1)
		if isNotAllowed(p1) {
			v.note(fmt.Sprintf("element %s is missing a required attribute",
				n.Name.Local))
			return NotAllowed{}
		}
		p1 = v.childrenDeriv(p1, n)
		if isNotAllowed(p1) {
			return NotAllowed{}
		}
		p2 := endTagDeriv(p1)
		if isNotAllowed(p2) {
			v.note(fmt.Sprintf("the content of %s is incomplete", n.Name.Local))
		}
		return p2
	}
	// Comments and processing instructions are not content: RELAX NG
	// validates the element and attribute structure, and a comment cannot
	// make a document invalid.
	return p
}

// childrenDeriv takes the derivative over an element's children.
func (v *validator) childrenDeriv(p Pattern, el *xdm.Node) Pattern {
	kids := contentChildren(el)
	if len(kids) == 0 {
		// An empty element and one containing "" are the same document, so a
		// pattern that admits the empty sequence already matches.
		if p.nullable() {
			return p
		}
		// If it does not, the element's content is the empty string, and a
		// pattern that matches strings may still accept it: <data
		// type="string"/> matches "" as readily as any other value. Skipping
		// this leaves <foo/> failing against a schema that plainly admits it.
		//
		// The derivative is only taken when it helps. For a pattern that
		// wanted an element it is NotAllowed either way, and taking it would
		// replace a useful failure with a bare one.
		//
		// "Helps" is judged by what the end tag will make of it, not by
		// nullability: at this point the pattern is inside an After, whose
		// continuation is the rest of the enclosing content, and an After is
		// never nullable however well its left half matched.
		if d := textDeriv(p, ""); !isNotAllowed(endTagDeriv(d)) {
			return d
		}
		return p
	}
	for _, c := range kids {
		p = v.childDeriv(p, c)
		if isNotAllowed(p) {
			return NotAllowed{}
		}
	}
	return p
}

// contentChildren drops the nodes that are not content.
func contentChildren(el *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	for _, c := range el.Children {
		switch c.Kind {
		case xdm.KindElement, xdm.KindText:
			out = append(out, c)
		}
	}
	return out
}

// elementAttrs collects the attributes a pattern may match.
//
// Namespace declarations are not attributes in the data model RELAX NG
// validates: xmlns:p="..." is a binding, not content, and a schema is never
// asked to declare one.
func elementAttrs(el *xdm.Node) []attr {
	var out []attr
	for _, a := range el.Attrs {
		if a.Name.URI == xdm.NSXMLNS || a.Name.Local == "xmlns" {
			continue
		}
		out = append(out, attr{
			name:  xdm.QName{URI: a.Name.URI, Local: a.Name.Local},
			value: a.Value,
		})
	}
	return out
}
