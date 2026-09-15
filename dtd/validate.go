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
// #FIXED), enumerated attribute values, ID/IDREF, and the two §3.3.1 rules
// that tie an attribute to a declaration elsewhere in the DTD — every name in
// a NOTATION attribute's enumeration must be declared by a <!NOTATION>, and
// the value of an ENTITY or ENTITIES attribute must name an entity declared
// with an NDATA notation.
//
// Those last two turn on a name being ABSENT from the DTD, so they are
// skipped when the DTD is only half of one: a DOCTYPE that named an external
// subset which was not read (HasExternalSubset with no ExternalSubset, which
// only LoadOptions.InternalSubsetOnly or Parse produces) may be missing the
// very declarations they look for, and reporting them would reject a valid
// document.
//
// An attribute whose declared type or default declaration is outside the sets
// XML 1.0 §3.3 closes is reported here rather than skipped. Such a declaration
// constrains the attribute somehow and this package cannot say how, so leaving
// it silent would report an unexamined attribute as valid — the one thing this
// package must never do. Compare HasExternalSubset, which says the same about
// declarations that were never read.
//
// Which declarations reach here is decided by how the DTD was read, not by
// this function. Parse reads the internal subset alone, and a DTD from it
// carries HasExternalSubset so a caller knows the check was partial; Load with
// a Resolver reads both subsets, and everything either one declares is applied
// on the same terms.
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
		matchers:   map[string]*xsd.SequenceMatcher{},
		ids:        map[string]bool{},

		// A DTD assembled by a caller rather than by Parse or Load may have
		// nil name sets — the maps are exported and the zero value is a
		// legal one. Reading a nil map is safe but would make every notation
		// undeclared, so an absent set means "not recorded", not "empty".
		partial: d.HasExternalSubset && d.ExternalSubset == "" ||
			d.Notations == nil && d.Unparsed == nil,
		notationsChecked: map[string]bool{},
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
	errs       []*Error
	matchers   map[string]*xsd.SequenceMatcher
	// ids are every ID value seen, for uniqueness and for resolving IDREF.
	ids map[string]bool
	// partial records that the DTD was read from the internal subset alone
	// while a DOCTYPE named an external one. The checks that turn on a name
	// being ABSENT — notations and unparsed entities — are then unsound and
	// are skipped; the checks that turn on a name being present are not.
	partial bool
	// notationsChecked keys the declarations whose enumeration has already
	// been reported on, so that a fault in the DTD is reported once rather
	// than once per element.
	notationsChecked map[string]bool
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
		case AttrInvalid:
			v.fail(path, "attribute %s has an unrecognised default "+
				"declaration %s, so its presence was not checked",
				d.Name, d.Value)
		}
		if !have {
			continue
		}
		if len(d.Enum) > 0 && !containsString(d.Enum, val) {
			v.fail(path, "attribute %s = %q is not one of %s",
				d.Name, val, strings.Join(d.Enum, ", "))
		}
		switch d.Type {
		case "NOTATION":
			// §3.3.1's Notation Attributes constraint has two halves. The
			// value must be one of the names in the enumeration, which the
			// Enum check above has already applied, and every name in that
			// enumeration must be a declared notation. Only the second is
			// new here, and it is a property of the DECLARATION rather than
			// of this element's value — so it is reported once per
			// declaration, not once per occurrence.
			v.checkNotationDecl(path, d)
		case "ENTITY":
			// §3.3.1: the value must be the name of an unparsed entity —
			// declared, and declared with an NDATA notation. A parsed
			// entity's name is as wrong here as an undeclared one.
			v.checkEntityValue(path, d.Name, val)
		case "ENTITIES":
			// The same rule over a whitespace-separated list. An empty list
			// is not a Names, so it fails the type as surely as an unknown
			// name would.
			names := strings.Fields(val)
			if len(names) == 0 {
				v.fail(path, "attribute %s is ENTITIES but is empty, which "+
					"names no unparsed entity", d.Name)
			}
			for _, n := range names {
				v.checkEntityValue(path, d.Name, n)
			}
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
		case "CDATA", "NMTOKEN", "NMTOKENS", "ENUMERATION":
			// Declared, and either unconstrained (CDATA) or constrained by
			// something this package does not yet check. Silence here is a
			// deliberate gap in coverage, not a failure to recognise the
			// declaration.
		default:
			// XML 1.0 §3.3.1 closes the type position to the names above.
			// Without this branch an unrecognised type — "IDREFF" for
			// "IDREFS" — falls through to no check at all, and the caller
			// cannot tell an attribute that passed from one never examined.
			v.fail(path, "attribute %s is declared with the unrecognised "+
				"type %s, so its value was not checked", d.Name, d.Type)
		}
	}
}

// checkNotationDecl applies the half of §3.3.1's Notation Attributes
// constraint that is about the declaration: every name in the enumeration of
// a NOTATION attribute must be declared by a <!NOTATION>.
//
// It is reported once per declaration rather than once per element carrying
// the attribute, because the fault is in the DTD and repeating it for every
// instance would bury the finding under its own copies.
func (v *validator) checkNotationDecl(path string, d *Attribute) {
	if v.partial {
		// The declarations were never all read, so a name absent from
		// Notations may simply be in the half that was not fetched.
		// Reporting it would reject a valid document, which is the one
		// failure worse than a missing check.
		return
	}
	key := d.Element + " " + d.Name
	if v.notationsChecked[key] {
		return
	}
	v.notationsChecked[key] = true
	for _, n := range d.Enum {
		if !v.dtd.Notations[n] {
			v.fail(path, "attribute %s permits the notation %s, which no "+
				"<!NOTATION> declares", d.Name, n)
		}
	}
}

// checkEntityValue applies §3.3.1's Entity Name constraint to one name.
func (v *validator) checkEntityValue(path, attr, name string) {
	if v.partial {
		// As for notations: half a DTD cannot say an entity is undeclared.
		return
	}
	if !v.dtd.Unparsed[name] {
		v.fail(path, "attribute %s = %q names no unparsed entity", attr, name)
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
