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
	start pattern
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
// derivative algorithm gives: a pattern that reaches notAllowedPat carries no
// record of the alternatives it tried, so there is one failure and it is the
// point at which every branch died. Reporting the *last* place the document
// was still viable is more useful than reporting the root.
func (s *Schema) Validate(doc *xdm.Node) error {
	return s.ValidateWithOptions(doc, ValidateOptions{})
}

// ValidateOptions bound one validation run.
type ValidateOptions struct {
	// MaxDepth bounds how deep validation will recurse. Zero means
	// DefaultMaxDepth; a negative value means no limit.
	//
	// This is not the parser's limit, and the distinction matters more here
	// than elsewhere: taking derivatives over a nested document costs time and
	// memory *quadratic* in the depth, since each level carries the pattern
	// remaining at every level above it. A tree can also be built by a
	// transform rather than parsed, and a caller who raises
	// xdm.ParseOptions.MaxDepth to accept a deep document has not thereby
	// agreed to let the validator spend a gigabyte on it.
	MaxDepth int

	// MaxPatternSize bounds the size of the derivative pattern carried
	// during validation. Zero means DefaultMaxPatternSize; a negative value
	// means no limit.
	//
	// It is a separate knob from MaxDepth because it bounds a different
	// thing. MaxDepth bounds cost that grows with how deep the document is;
	// this bounds cost that grows with how WIDE it is, which a schema
	// nesting oneOrMore inside oneOrMore makes multiplicative — a 63-byte
	// instance of fourteen children measured at 1.2 GB before this existed,
	// at a depth of two, where no depth bound could reach it.
	MaxPatternSize int
}

// DefaultMaxDepth bounds validation recursion when MaxDepth is zero. It
// matches xdm.DefaultMaxDepth, so a document the parser accepts is one the
// validator will not refuse for depth alone.
const DefaultMaxDepth = 1000

// DefaultMaxPatternSize bounds the derivative pattern when MaxPatternSize is
// zero.
//
// It is set high enough that no schema in the RELAX NG spec test suite comes
// near it — the whole suite passes unchanged — and low enough that the
// multiplicative blowup is refused in milliseconds rather than after a
// gigabyte of allocation.
const DefaultMaxPatternSize = 100_000

// ValidateWithOptions checks a document, with limits on the run.
func (s *Schema) ValidateWithOptions(doc *xdm.Node, opts ValidateOptions) error {
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
	maxDepth := opts.MaxDepth
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	maxPattern := opts.MaxPatternSize
	if maxPattern == 0 {
		maxPattern = DefaultMaxPatternSize
	}
	v := &validator{maxDepth: maxDepth, maxPattern: maxPattern}
	p := v.childDeriv(s.start, root)
	if v.tooBig {
		return &Error{
			Path: v.deepPath,
			Message: fmt.Sprintf(
				"the derivative pattern exceeds %d nodes; a oneOrMore nested "+
					"inside a oneOrMore grows the pattern multiplicatively in "+
					"the number of children", maxPattern),
		}
	}
	if v.tooDeep {
		return &Error{
			Path: v.deepPath,
			Message: fmt.Sprintf(
				"nesting exceeds %d levels; validation cost grows with the "+
					"square of the depth", maxDepth),
		}
	}
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
	// maxDepth bounds recursion; a negative value means no bound.
	maxDepth int
	// tooDeep records that the bound was reached, so the caller is told why
	// rather than being handed a validity failure that is really a limit.
	tooDeep  bool
	deepPath string
	depth    int
	// deepest records the furthest point the document was still viable, and
	// why it stopped being so. A derivative that fails says only "no", so the
	// path has to be captured on the way down.
	deepest string
	why     string
	path    []string
	// maxPattern bounds the size of the derivative pattern; a negative value
	// means no bound. See patternSize.
	maxPattern int
	// tooBig records that the pattern bound was reached, so the caller is
	// told why rather than being handed a validity failure that is really a
	// limit.
	tooBig bool
}

// tailPath renders the last few segments of a deep path.
func tailPath(path []string, last string) string {
	const keep = 4
	segs := append(append([]string{}, path...), last)
	if len(segs) <= keep {
		return "/" + strings.Join(segs, "/")
	}
	return ".../" + strings.Join(segs[len(segs)-keep:], "/")
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

// nsContextOf reads the namespace bindings in scope at an element.
//
// A qnamePat in a document means what the document's prefixes say it means, and
// the schema's prefixes are a separate set — so the comparison needs both, and
// this supplies the document half.
func nsContextOf(n *xdm.Node) nsContext {
	ctx := nsContext{prefixes: n.InScopeNamespaces()}
	if uri, ok := ctx.prefixes[""]; ok {
		ctx.dflt = uri
	}
	return ctx
}

// textDeriv takes the derivative over a run of character data.
func (v *validator) textDeriv(p pattern, s string, ctx nsContext) pattern {
	if whitespaceOnly(s) {
		// Whitespace between elements is not content unless the pattern asks
		// for text: a document written across lines must not fail because of
		// its own indentation.
		if isNotAllowed(textDeriv(p, s, ctx)) {
			return p
		}
	}
	return textDeriv(p, s, ctx)
}

// childDeriv takes the derivative with respect to one node of content.
func (v *validator) childDeriv(p pattern, n *xdm.Node) pattern {
	switch n.Kind {
	case xdm.KindText:
		return v.textDeriv(p, n.Value, nsContextOf(n))

	case xdm.KindElement:
		// The depth bound is checked here because this is the only place
		// recursion deepens, and it is fatal rather than recoverable: the
		// derivative that would be taken next is the expensive one, so
		// carrying on to report a validity failure would spend exactly the
		// resources the bound exists to refuse.
		if v.maxDepth >= 0 && v.depth >= v.maxDepth {
			// The path is recorded now, because the deferred pops unwind it
			// before the caller reads it — reporting "/" would name the
			// document rather than the element that was too deep.
			v.tooDeep = true
			// A path a thousand segments long tells the reader nothing, so
			// only the last few are kept: what identifies the failure is the
			// depth, which the message states, not the route to it.
			v.deepPath = tailPath(v.path, n.Name.Local)
			return notAllowedPat{}
		}
		v.depth++
		v.path = append(v.path, n.Name.Local)
		defer func() {
			v.depth--
			v.path = v.path[:len(v.path)-1]
		}()

		name := xdm.QName{URI: n.Name.URI, Local: n.Name.Local}
		// The pattern the derivative carries is checked before the next one
		// is taken, for the same reason the depth bound is fatal here: the
		// derivative about to be computed is the expensive one, so noticing
		// afterwards would spend exactly what the bound exists to refuse.
		if v.maxPattern >= 0 && patternSize(p, v.maxPattern+1) > v.maxPattern {
			v.tooBig = true
			v.deepPath = tailPath(v.path, n.Name.Local)
			return notAllowedPat{}
		}
		p1 := startTagOpenDeriv(p, name)
		if isNotAllowed(p1) {
			v.note(fmt.Sprintf("element %s is not permitted here", n.Name.Local))
			return notAllowedPat{}
		}
		p1 = attsDeriv(p1, elementAttrs(n), nsContextOf(n))
		if isNotAllowed(p1) {
			v.note(fmt.Sprintf("the attributes of %s do not match", n.Name.Local))
			return notAllowedPat{}
		}
		p1 = startTagCloseDeriv(p1)
		if isNotAllowed(p1) {
			v.note(fmt.Sprintf("element %s is missing a required attribute",
				n.Name.Local))
			return notAllowedPat{}
		}
		p1 = v.childrenDeriv(p1, n)
		if isNotAllowed(p1) {
			return notAllowedPat{}
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
func (v *validator) childrenDeriv(p pattern, el *xdm.Node) pattern {
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
		// wanted an element it is notAllowedPat either way, and taking it would
		// replace a useful failure with a bare one.
		//
		// "Helps" is judged by what the end tag will make of it, not by
		// nullability: at this point the pattern is inside an afterPat, whose
		// continuation is the rest of the enclosing content, and an afterPat is
		// never nullable however well its left half matched.
		if d := textDeriv(p, "", nsContextOf(el)); !isNotAllowed(endTagDeriv(d)) {
			return d
		}
		return p
	}
	// Consecutive text nodes are one string. The data model splits character
	// data wherever a comment or an entity boundary falls, and a pattern like
	// <data type="string"/> matches a *value*, not a run of nodes: deriving
	// over each piece separately consumes the pattern on the first and then
	// fails on the second, so a document differing only in where its comments
	// sit would validate differently.
	for i := 0; i < len(kids); i++ {
		c := kids[i]
		if c.Kind == xdm.KindText {
			var sb strings.Builder
			for ; i < len(kids) && kids[i].Kind == xdm.KindText; i++ {
				sb.WriteString(kids[i].Value)
			}
			i--
			p = v.textDeriv(p, sb.String(), nsContextOf(el))
		} else {
			p = v.childDeriv(p, c)
		}
		if isNotAllowed(p) {
			return notAllowedPat{}
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
