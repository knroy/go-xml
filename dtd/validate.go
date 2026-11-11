package dtd

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/v2/internal/dtdnote"
	"github.com/knroy/go-xml/v2/internal/xmlname"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xsd"
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

	// AllowUndeclared skips elements and attributes the DTD says nothing
	// about instead of reporting them.
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

	// MaxContentModelPositions, MaxContentModelEdges and MaxMatchStates
	// bound each element content model Validate compiles and matches, as
	// the fields of the same meaning in xsd.SequenceMatcherOptions do. Zero
	// means the default — 8192 positions (xsd.DefaultMaxContentModelPositions),
	// 1<<20 follow entries, 4096 simultaneous states
	// (xsd.DefaultMaxMatchStates) — and a negative value means no limit, for
	// a DTD the caller produced itself.
	//
	// They bound what one declaration can cost: a model past the first two
	// is reported as a validity error on every element that uses it, and an
	// element whose children need more states than MaxMatchStates is
	// reported as not matching. Raising them admits denser models at the
	// cost of memory and time that grow with the model (quadratically in
	// positions for a dense (a?,a?,...)); lowering them can turn a valid
	// document into a reported failure.
	MaxContentModelPositions int
	MaxContentModelEdges     int
	MaxMatchStates           int
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
// #FIXED), enumerated attribute values, the lexical form of tokenized values,
// ID/IDREF, the validity constraints on the declarations themselves (recorded
// as the DTD was read; see declarationsInto), and the two §3.3.1 rules
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
// Also reported are the faults xdm.Parse saw and the tree cannot show, which
// it records as it reads: a CDATA section or character reference where
// element content admits only white space, an entity reference in an EMPTY
// element (§3, VC: Element Valid), a reference to an entity declared nowhere
// (§4.1, VC: Entity Declared), and under standalone="yes" an external markup
// declaration that defaulted or normalized an attribute or declared element
// content holding white space (§2.9, VC: Standalone Document Declaration).
// The external ones are seen only when the parse read the external subset
// (ParseOptions.ExternalEntities).
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
		matcherOpts: xsd.SequenceMatcherOptions{
			MaxPositions:   opts.MaxContentModelPositions,
			MaxEdges:       opts.MaxContentModelEdges,
			MaxMatchStates: opts.MaxMatchStates,
		},
		ids: map[string]bool{},

		// A DTD assembled by a caller rather than by Parse or Load may have
		// nil name sets — the maps are exported and the zero value is a
		// legal one. Reading a nil map is safe but would make every notation
		// undeclared, so an absent set means "not recorded", not "empty".
		partial: d.HasExternalSubset && d.ExternalSubset == "" ||
			d.Notations == nil && d.Unparsed == nil,
		notationsChecked: map[string]bool{},
	}
	root := doc
	if root.Kind() == xdm.KindDocument {
		for c := range root.Children() {
			if c.Kind() == xdm.KindElement {
				root = c
				break
			}
		}
	}
	v.root = root
	// XML 1.0 §2.8 VC Root Element Type: the document's root is the element
	// the DOCTYPE names. Without it a document could send any other declared
	// element as its root and escape the named one's attributes and model.
	// Only a whole document has a root to hold to; a subtree passed in is
	// validated as itself.
	if doc.Kind() == xdm.KindDocument && root != doc && d.name != "" &&
		root.Name().Lexical() != d.name {
		v.fail(root, "root element %s is not the DOCTYPE's %s",
			root.Name().Lexical(), d.name)
	}
	// XML 1.1 §4.3.4: a document takes its version from the document
	// entity, one with no declaration is 1.0, and a 1.0 document may not
	// include a 1.1 entity. Load read the external entities but never saw
	// the document; this is where the two meet.
	if d.v11 != "" {
		ver := ""
		if t := doc.Tree(); t != nil {
			ver = t.XMLVersion
		}
		if ver != "1.1" {
			v.fail(root, "the DTD's external resource %s declares XML 1.1 but the document is XML 1.0 (XML §4.3.4)", d.v11)
		}
	}
	// The faults in the declarations themselves, found as they were read.
	for _, m := range d.declErrs {
		v.fail(root, "%s", m)
	}
	// XML 1.0 §4.2.2 VC Notation Declared: "The Name MUST match the
	// declared name of a notation." Like the other absence checks it is
	// unsound on half a DTD.
	if !v.partial {
		for _, e := range d.ndata {
			if !d.Notations[e[1]] {
				v.fail(root, "unparsed entity %s names the notation %s, which no <!NOTATION> declares", e[0], e[1])
			}
		}
	}
	for _, msg := range d.nesting {
		v.fail(root, "%s", msg)
	}
	// The faults the parser saw that the tree cannot show (internal/dtdnote):
	// white space written as a reference where only white space may be, and
	// under standalone="yes" what external declarations defaulted or
	// normalized. A fault about the document is one about its root; those
	// outside the subtree being validated are not this call's.
	for n, msg := range dtdnote.Notes(doc) {
		el := n.(*xdm.Node)
		if el.Kind() == xdm.KindDocument {
			if doc.Kind() != xdm.KindDocument {
				continue
			}
			el = root
		}
		for a := el; a != nil; a = a.Parent() {
			if a == root {
				v.fail(el, "%s", msg)
				break
			}
		}
	}
	v.walk(root)
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
	// matcherOpts bounds every matcher compiled into matchers.
	matcherOpts xsd.SequenceMatcherOptions
	names       []xdm.QName // child-name buffer reused across elements
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
	// root is the element the walk started from, where a path begins.
	root *xdm.Node
}

type idref struct {
	value string
	el    *xdm.Node
}

func (v *validator) fail(el *xdm.Node, format string, args ...any) {
	if v.max > 0 && len(v.errs) >= v.max {
		return
	}
	// A #FIXED or enumerated value comes from the DTD or the document and
	// may be megabytes; 200 mismatches against a 1 MB #FIXED value kept
	// 105 MB. Arguments are clipped before formatting, the message after.
	for i, a := range args {
		switch x := a.(type) {
		case string:
			args[i] = clip(x)
		case error:
			args[i] = clip(x.Error())
		}
	}
	v.errs = append(v.errs, &Error{Path: v.pathOf(el), Message: clip(fmt.Sprintf(format, args...))})
}

// clip shortens s to 1 KB, marking the cut with "..." as the xsd package
// does. The concatenation copies, so the result never pins the original.
func clip(s string) string {
	const max = 1024
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// pathOf renders the path from the root to el, as "/root/child". It is built
// only for a recorded failure: concatenating it at every element cost the
// square of the depth, 2 GB allocated for a valid 990-deep chain of 4 KB names.
func (v *validator) pathOf(el *xdm.Node) string {
	var names []string
	for n := el; ; n = n.Parent() {
		names = append(names, n.Name().Local)
		if n == v.root {
			break
		}
	}
	slices.Reverse(names)
	return "/" + strings.Join(names, "/")
}

func (v *validator) walk(el *xdm.Node) {
	v.checkContent(el)
	v.checkAttributes(el)
	for c := range el.Children() {
		if c.Kind() == xdm.KindElement {
			v.walk(c)
		}
	}
}

// checkContent applies the element's content model.
//
// Names are matched as written, prefix:local, because a DTD predates
// namespaces and declares "o:order" as that string; matching on the local
// name alone validated <o:order> as <order>.
func (v *validator) checkContent(el *xdm.Node) {
	name := el.Name().Lexical()
	decl, ok := v.dtd.Elements[name]
	if !ok {
		// An undeclared element is a validity error in a DTD-validated
		// document — unlike XSD, where a wildcard may admit it. A caller
		// working with a deliberately partial internal subset can say so.
		if !v.allowUndec {
			v.fail(el, "element %s is not declared", name)
		}
		return
	}
	switch decl.Kind {
	case ContentAny:
		return
	case ContentEmpty:
		// XML 1.0 §3 VC Element Valid: "The declaration matches EMPTY and
		// the element has no content (not even entity references,
		// comments, PIs or white space)." Any child at all is content.
		for range el.Children() {
			v.fail(el, "element %s is declared EMPTY but has content",
				name)
			return
		}
	case ContentMixed:
		for c := range el.Children() {
			if c.Kind() == xdm.KindElement && !decl.Mixed[c.Name().Lexical()] {
				v.fail(el, "element %s is not permitted in the mixed "+
					"content of %s", c.Name().Lexical(), name)
			}
		}
	case ContentChildren:
		// An element-only model forbids character data outright, not merely
		// unexpected elements: "(a, b)" does not admit text between them.
		for c := range el.Children() {
			if c.Kind() == xdm.KindText && strings.TrimSpace(c.Value()) != "" {
				v.fail(el, "element %s has element-only content but "+
					"contains character data", name)
				break
			}
		}
		m, err := v.matcherFor(decl)
		if err != nil {
			v.fail(el, "content model of %s: %v", name, err)
			return
		}
		names := v.names[:0]
		for c := range el.Children() {
			if c.Kind() == xdm.KindElement {
				names = append(names, xdm.QName{Local: c.Name().Lexical()})
			}
		}
		v.names = names
		if ok, at := m.Match(names); !ok {
			if at < len(names) {
				v.fail(el, "element %s is not permitted here in the "+
					"content of %s", names[at].Local, name)
			} else {
				v.fail(el, "the content of %s is incomplete", name)
			}
		}
	}
}

func (v *validator) matcherFor(decl *Element) (*xsd.SequenceMatcher, error) {
	if m, ok := v.matchers[decl.Name]; ok {
		return m, nil
	}
	m, err := xsd.NewSequenceMatcherWith(decl.Particle, v.matcherOpts)
	if err != nil {
		return nil, err
	}
	v.matchers[decl.Name] = m
	return m, nil
}

// checkAttributes applies the ATTLIST declarations for an element, and
// reports an attribute the ATTLIST does not declare (XML 1.0 §3.3.1, VC
// Attribute Value Type). Names are matched as written, so a declared xml:lang
// or o:x is checked like any other.
//
// The element's namespace declarations are checked as the attributes xmlns
// and xmlns:p that XML 1.0 sees them as. A DTD predates namespaces, so a
// valid document declares them in an ATTLIST like any other attribute, and
// libxml2 reports one that is not declared ("No declaration for attribute
// xmlns"). The tree holds them apart from the attributes, so they are added
// back here.
func (v *validator) checkAttributes(el *xdm.Node) {
	decls := v.dtd.Attributes[el.Name().Lexical()]
	type attr struct{ name, value string }
	var attrs []attr
	for p, uri := range el.DeclaredNamespaces() {
		name := "xmlns"
		if p != "" {
			name += ":" + p
		}
		attrs = append(attrs, attr{name, uri})
	}
	for a := range el.Attrs() {
		attrs = append(attrs, attr{a.Name().Lexical(), a.Value()})
	}
	if !v.allowUndec {
		for _, a := range attrs {
			if !slices.ContainsFunc(decls, func(d *Attribute) bool { return d.Name == a.name }) {
				v.fail(el, "attribute %s is not declared", a.name)
			}
		}
	}
	if len(decls) == 0 {
		return
	}
	present := make(map[string]string, len(attrs))
	for _, a := range attrs {
		present[a.name] = a.value
	}

	for _, d := range decls {
		val, have := present[d.Name]
		switch d.Default {
		case AttrRequired:
			if !have {
				v.fail(el, "required attribute %s is missing", d.Name)
				continue
			}
		case AttrFixed:
			if have && val != normalizedDefault(d.Value) {
				v.fail(el, "attribute %s is #FIXED %q but is %q",
					d.Name, d.Value, val)
			}
		case AttrInvalid:
			v.fail(el, "attribute %s has an unrecognised default "+
				"declaration %s, so its presence was not checked",
				d.Name, d.Value)
		}
		if !have {
			continue
		}
		if len(d.Enum) > 0 && !containsString(d.Enum, val) {
			v.fail(el, "attribute %s = %q is not one of %s",
				d.Name, val, strings.Join(d.Enum, ", "))
		}
		if f := lexicalFault(d.Type, val); f != "" {
			v.fail(el, "attribute %s = %q %s", d.Name, val, f)
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
			v.checkNotationDecl(el, d)
		case "ENTITY":
			// §3.3.1: the value must be the name of an unparsed entity —
			// declared, and declared with an NDATA notation. A parsed
			// entity's name is as wrong here as an undeclared one.
			v.checkEntityValue(el, d.Name, val)
		case "ENTITIES":
			// The same rule over the list. An empty list is not a Names,
			// which lexicalFault has already reported.
			for _, n := range tokens(val) {
				v.checkEntityValue(el, d.Name, n)
			}
		case "ID":
			if v.ids[val] {
				v.fail(el, "duplicate ID %q", val)
			}
			v.ids[val] = true
		case "IDREF":
			v.refs = append(v.refs, idref{val, el})
		case "IDREFS":
			for _, r := range tokens(val) {
				v.refs = append(v.refs, idref{r, el})
			}
		case "CDATA", "NMTOKEN", "NMTOKENS", "ENUMERATION":
			// Unconstrained (CDATA), or constrained only lexically or by
			// the enumeration, both checked above.
		default:
			// XML 1.0 §3.3.1 closes the type position to the names above.
			// Without this branch an unrecognised type — "IDREFF" for
			// "IDREFS" — falls through to no check at all, and the caller
			// cannot tell an attribute that passed from one never examined.
			v.fail(el, "attribute %s is declared with the unrecognised "+
				"type %s, so its value was not checked", d.Name, d.Type)
		}
	}
}

// lexicalFault applies the lexical half of XML 1.0 §3.3.1's VCs ID, IDREF,
// Entity Name and Name Token to a normalized value of a tokenized type, and
// says what is wrong, or returns "" for a value that matches. Types without a
// production of their own (CDATA, the enumerations) always match.
//
// The list types are split on #x20 alone: Names and Nmtokens are tokens
// separated by single spaces, and a TAB that reached the value through a
// character reference survives normalization as a TAB (rmt-e2e-20).
func lexicalFault(typ, val string) string {
	var ok func(string) bool
	switch typ {
	case "ID", "IDREF", "ENTITY", "IDREFS", "ENTITIES":
		// NCName rather than Name, because xdm parses with namespaces:
		// Namespaces in XML 1.0 §7 makes a namespace-valid document one in
		// which every token XML 1.0 requires to match Name matches NCName.
		ok = xdm.IsNCName
	case "NMTOKEN", "NMTOKENS":
		ok = isNmtoken
	default:
		return ""
	}
	toks := []string{val}
	if strings.HasSuffix(typ, "S") {
		if toks = tokens(val); len(toks) == 0 {
			return "is " + typ + " but is empty, and a list needs one or more tokens"
		}
	}
	for _, t := range toks {
		if !ok(t) {
			if typ == "NMTOKEN" || typ == "NMTOKENS" {
				return "does not match the Nmtoken production"
			}
			return "does not match the NCName production"
		}
	}
	return ""
}

// tokens splits a normalized list value on #x20.
func tokens(val string) []string {
	return strings.FieldsFunc(val, func(r rune) bool { return r == ' ' })
}

func isNmtoken(s string) bool {
	for _, r := range s {
		if !xmlname.IsNameRune(r) {
			return false
		}
	}
	return s != ""
}

// checkNotationDecl applies the half of §3.3.1's Notation Attributes
// constraint that is about the declaration: every name in the enumeration of
// a NOTATION attribute must be declared by a <!NOTATION>.
//
// It is reported once per declaration rather than once per element carrying
// the attribute, because the fault is in the DTD and repeating it for every
// instance would bury the finding under its own copies.
func (v *validator) checkNotationDecl(el *xdm.Node, d *Attribute) {
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
			v.fail(el, "attribute %s permits the notation %s, which no "+
				"<!NOTATION> declares", d.Name, n)
		}
	}
}

// checkEntityValue applies §3.3.1's Entity Name constraint to one name.
func (v *validator) checkEntityValue(el *xdm.Node, attr, name string) {
	if v.partial {
		// As for notations: half a DTD cannot say an entity is undeclared.
		return
	}
	if !v.dtd.Unparsed[name] {
		v.fail(el, "attribute %s = %q names no unparsed entity", attr, name)
	}
}

// checkIDRefs resolves every IDREF once the whole document has been read,
// because a reference may point forward.
func (v *validator) checkIDRefs() {
	for _, r := range v.refs {
		if !v.ids[r.value] {
			v.fail(r.el, "IDREF %q matches no ID in the document", r.value)
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

// normalizedDefault applies XML 1.0 §3.3.3 attribute-value normalization to a
// declared default (from memory: §3.3.2 says it "is applied to the default
// value"), so that a #FIXED default is compared as the parser reports the
// attribute: character references and the predefined entities are replaced,
// and white space characters become spaces. A reference to any other entity
// is left as written, which can only make the comparison stricter than the
// parser's, never looser.
func normalizedDefault(v string) string {
	if !strings.ContainsAny(v, "&\t\n\r") {
		return v
	}
	var sb strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\t' || c == '\n' || c == '\r' {
			sb.WriteByte(' ')
			continue
		}
		j := strings.IndexByte(v[i:], ';')
		if c != '&' || j < 0 {
			sb.WriteByte(c)
			continue
		}
		name := v[i+1 : i+j]
		r, ok := rune(0), true
		switch name {
		case "lt":
			r = '<'
		case "gt":
			r = '>'
		case "amp":
			r = '&'
		case "apos":
			r = '\''
		case "quot":
			r = '"'
		default:
			var n uint64
			var err error = strconv.ErrSyntax
			if strings.HasPrefix(name, "#x") {
				n, err = strconv.ParseUint(name[2:], 16, 32)
			} else if strings.HasPrefix(name, "#") {
				n, err = strconv.ParseUint(name[1:], 10, 32)
			}
			r, ok = rune(n), err == nil
		}
		if !ok {
			sb.WriteByte(c)
			continue
		}
		sb.WriteRune(r)
		i += j
	}
	return sb.String()
}
