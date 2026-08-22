package dtd

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// Error is one validity failure.
type Error struct {
	// Path locates the element, as "/root/child".
	Path string
	// Message says what was wrong.
	Message string
}

func (e *Error) Error() string { return e.Path + ": " + e.Message }

// Errors is what Validate returns when a document is not valid.
type Errors struct{ Errors []*Error }

func (e *Errors) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d validity errors:", len(e.Errors))
	for _, x := range e.Errors {
		sb.WriteString("\n  " + x.Error())
	}
	return sb.String()
}

// Options configures Validate.
type Options struct {
	// MaxErrors stops after this many failures. Zero means
	// DefaultMaxErrors; a negative value means no limit.
	//
	// A document wrong in every element would otherwise produce an error per
	// element, which helps nobody and costs memory proportional to the input.
	MaxErrors int

	// AllowUndeclared skips elements the DTD says nothing about instead of
	// reporting them.
	//
	// Strictly, an undeclared element is a validity error: a DTD is a closed
	// description, unlike a schema where a wildcard may admit the unknown.
	// But a document whose DOCTYPE names an *external* subset and declares
	// only a few things internally is the common real-world shape — the
	// W3C's own RFC 3986 type library declares one element and one attribute
	// list, purely so that an external DTD's attributes work — and validating
	// that against its internal subset alone reports every other element as
	// undeclared, which is noise rather than a finding.
	//
	// Off by default, so the strict reading is what a caller gets unless they
	// ask otherwise. Turn it on when the DTD is known to be partial;
	// HasExternalSubset is how to detect that case.
	AllowUndeclared bool
}

// DefaultMaxErrors bounds how many failures are reported.
const DefaultMaxErrors = 100

// Validate checks a document against the DTD in its own internal subset.
//
// The DTD is read from the document rather than supplied separately, which is
// what a DOCTYPE means. A document with no DOCTYPE is valid trivially: there
// are no constraints to violate.
//
// The document must have been parsed with xdm.ParseOptions.AllowDOCTYPE set,
// since without it the parse fails before this is reachable.
//
// What is checked: element content models, attribute presence (#REQUIRED and
// #FIXED), enumerated attribute values, and ID/IDREF. What is not: anything
// declared in an *external* subset, which is not fetched — Validate reports
// that as a limitation rather than passing the document silently.
func Validate(doc *xdm.Node, d *DTD, opts Options) error {
	if doc == nil {
		return fmt.Errorf("dtd: nil document")
	}
	if d == nil {
		return nil
	}
	max := opts.MaxErrors
	if max == 0 {
		max = DefaultMaxErrors
	}
	v := &validator{
		dtd:        d,
		max:        max,
		allowUndec: opts.AllowUndeclared,
		matchers: map[string]*xsd.SequenceMatcher{},
		ids:      map[string]bool{},
	}
	root := doc
	if root.Kind == xdm.KindDocument {
		for _, c := range root.Children {
			if c.Kind == xdm.KindElement {
				root = c
				break
			}
		}
	}
	v.walk(root, "")
	v.checkIDRefs()
	if len(v.errs) == 0 {
		return nil
	}
	return &Errors{Errors: v.errs}
}

type validator struct {
	dtd        *DTD
	max        int
	allowUndec bool
	errs     []*Error
	matchers map[string]*xsd.SequenceMatcher
	// ids are every ID value seen, for uniqueness and for resolving IDREF.
	ids map[string]bool
	// refs are IDREF values and where they appeared, checked once the whole
	// document has been read — a reference may point forward.
	refs []idref
}

type idref struct {
	value string
	path  string
}

func (v *validator) fail(path, format string, args ...any) {
	if v.max > 0 && len(v.errs) >= v.max {
		return
	}
	v.errs = append(v.errs, &Error{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) walk(el *xdm.Node, parentPath string) {
	path := parentPath + "/" + el.Name.Local
	v.checkContent(el, path)
	v.checkAttributes(el, path)
	for _, c := range el.Children {
		if c.Kind == xdm.KindElement {
			v.walk(c, path)
		}
	}
}

// checkContent applies the element's content model.
func (v *validator) checkContent(el *xdm.Node, path string) {
	decl, ok := v.dtd.Elements[el.Name.Local]
	if !ok {
		// An undeclared element is a validity error in a DTD-validated
		// document — unlike XSD, where a wildcard may admit it. A caller
		// working with a deliberately partial internal subset can say so.
		if !v.allowUndec {
			v.fail(path, "element %s is not declared", el.Name.Local)
		}
		return
	}
	switch decl.Kind {
	case ContentAny:
		return
	case ContentEmpty:
		for _, c := range el.Children {
			if c.Kind == xdm.KindElement || (c.Kind == xdm.KindText && strings.TrimSpace(c.Value) != "") {
				v.fail(path, "element %s is declared EMPTY but has content",
					el.Name.Local)
				return
			}
		}
	case ContentMixed:
		for _, c := range el.Children {
			if c.Kind == xdm.KindElement && !decl.Mixed[c.Name.Local] {
				v.fail(path, "element %s is not permitted in the mixed "+
					"content of %s", c.Name.Local, el.Name.Local)
			}
		}
	case ContentChildren:
		// An element-only model forbids character data outright, not merely
		// unexpected elements: "(a, b)" does not admit text between them.
		for _, c := range el.Children {
			if c.Kind == xdm.KindText && strings.TrimSpace(c.Value) != "" {
				v.fail(path, "element %s has element-only content but "+
					"contains character data", el.Name.Local)
				break
			}
		}
		m, err := v.matcherFor(decl)
		if err != nil {
			v.fail(path, "content model of %s: %v", el.Name.Local, err)
			return
		}
		var names []xdm.QName
		for _, c := range el.Children {
			if c.Kind == xdm.KindElement {
				names = append(names, xdm.QName{Local: c.Name.Local})
			}
		}
		if ok, at := m.Match(names); !ok {
			if at < len(names) {
				v.fail(path, "element %s is not permitted here in the "+
					"content of %s", names[at].Local, el.Name.Local)
			} else {
				v.fail(path, "the content of %s is incomplete", el.Name.Local)
			}
		}
	}
}

func (v *validator) matcherFor(decl *Element) (*xsd.SequenceMatcher, error) {
	if m, ok := v.matchers[decl.Name]; ok {
		return m, nil
	}
	m, err := xsd.NewSequenceMatcher(decl.Particle)
	if err != nil {
		return nil, err
	}
	v.matchers[decl.Name] = m
	return m, nil
}

// checkAttributes applies the ATTLIST declarations for an element.
func (v *validator) checkAttributes(el *xdm.Node, path string) {
	decls := v.dtd.Attributes[el.Name.Local]
	present := map[string]string{}
	for _, a := range el.Attrs {
		name := a.Name.Local
		if a.Name.URI != "" || strings.HasPrefix(name, "xmlns") {
			// A DTD predates namespaces and declares "xmlns" as an ordinary
			// attribute when it declares it at all. Skipping namespace
			// declarations avoids reporting every one as undeclared.
			continue
		}
		present[name] = a.Value
	}

	for _, d := range decls {
		val, have := present[d.Name]
		switch d.Default {
		case AttrRequired:
			if !have {
				v.fail(path, "required attribute %s is missing", d.Name)
				continue
			}
		case AttrFixed:
			if have && val != d.Value {
				v.fail(path, "attribute %s is #FIXED %q but is %q",
					d.Name, d.Value, val)
			}
		}
		if !have {
			continue
		}
		if len(d.Enum) > 0 && !containsString(d.Enum, val) {
			v.fail(path, "attribute %s = %q is not one of %s",
				d.Name, val, strings.Join(d.Enum, ", "))
		}
		switch d.Type {
		case "ID":
			if v.ids[val] {
				v.fail(path, "duplicate ID %q", val)
			}
			v.ids[val] = true
		case "IDREF":
			v.refs = append(v.refs, idref{val, path})
		case "IDREFS":
			for _, r := range strings.Fields(val) {
				v.refs = append(v.refs, idref{r, path})
			}
		}
	}
}

// checkIDRefs resolves every IDREF once the whole document has been read,
// because a reference may point forward.
func (v *validator) checkIDRefs() {
	for _, r := range v.refs {
		if !v.ids[r.value] {
			v.fail(r.path, "IDREF %q matches no ID in the document", r.value)
		}
	}
}

func containsString(set []string, want string) bool {
	for _, s := range set {
		if s == want {
			return true
		}
	}
	return false
}
