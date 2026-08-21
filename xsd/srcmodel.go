package xsd

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// The schema for schemas constrains the children of every XSD element, and a
// schema document that breaks those constraints is not a schema at all. The
// readers in parse.go and its neighbours are deliberately forgiving: each one
// picks out the children it needs by name and ignores whatever else it finds,
// which is what lets a single reader serve several element shapes. That
// tolerance also let plainly malformed documents load — two <simpleContent>
// children, an <annotation> after the content model, a second <group> where
// the grammar allows one — so the shape has to be checked separately, before
// the readers run.
//
// The check is a small regular-expression match over the child element names.
// Each production below is the "Content:" line the spec prints in its XML
// Representation Summary for that element (XSD 1.0 §3, XSD 1.1 §3), written
// as a sequence of terms; matching is greedy per term with no backtracking,
// which suffices because none of these grammars is ambiguous at a term
// boundary — every term's alternatives are disjoint from the next term's.

// srcTerm is one term of a content model: a set of permitted element names
// together with how many times the term may repeat.
type srcTerm struct {
	names []string
	min   int
	max   int // -1 for unbounded
}

func one(names ...string) srcTerm  { return srcTerm{names: names, min: 1, max: 1} }
func opt(names ...string) srcTerm  { return srcTerm{names: names, min: 0, max: 1} }
func star(names ...string) srcTerm { return srcTerm{names: names, min: 0, max: -1} }
func plus(names ...string) srcTerm { return srcTerm{names: names, min: 1, max: -1} }

// annot is the leading "annotation?" that opens nearly every content model.
// Writing it once keeps the productions below readable and makes it obvious
// that an annotation is only ever allowed *first*.
var annot = opt("annotation")

// particleNames are the four ways to spell a content model in a type or an
// extension/restriction body.
var particleNames = []string{"all", "choice", "sequence", "group"}

// attrDecls is the "((attribute | attributeGroup)*, anyAttribute?)" tail that
// closes every element which may carry attribute declarations.
var attrDecls = []srcTerm{star("attribute", "attributeGroup"), opt("anyAttribute")}

// facetNames are the constraining facets that may appear in a simple type
// restriction. The 1.1 additions are accepted in both versions: whether a
// facet is *honoured* is Options.Version's business, but a document that uses
// one is not made ill-formed by running the 1.0 rules over it.
var facetNames = []string{
	"minExclusive", "minInclusive", "maxExclusive", "maxInclusive",
	"totalDigits", "fractionDigits", "length", "minLength", "maxLength",
	"enumeration", "whiteSpace", "pattern", "assertion", "explicitTimezone",
}

// srcModels maps a schema element's local name to its permitted children.
//
// Several names appear in more than one context with different content models
// — <restriction> under <simpleType>, under <simpleContent> and under
// <complexContent> are three different grammars — so those are resolved by the
// parent's name in srcModelFor rather than listed here.
var srcModels = map[string][]srcTerm{
	"complexType":  nil, // resolved in srcModelFor: three-way choice
	"simpleType":   {annot, one("restriction", "list", "union")},
	"simpleContent": {annot, one("restriction", "extension")},
	"complexContent": {annot, one("restriction", "extension")},
	"attribute":    {annot, opt("simpleType")},
	"element": {annot, opt("simpleType", "complexType"),
		star("alternative"), star("unique", "key", "keyref")},
	"attributeGroup": nil, // resolved in srcModelFor: ref vs. definition
	"group":          nil, // resolved in srcModelFor: ref vs. definition
	"all":            {annot, star("element", "any", "group")},
	"choice":         {annot, star("element", "group", "choice", "sequence", "any")},
	"sequence":       {annot, star("element", "group", "choice", "sequence", "any")},
	"any":            {annot},
	"anyAttribute":   {annot},
	"unique":         nil, // resolved in srcModelFor: ref vs. definition
	"key":            nil,
	"keyref":         nil,
	"selector":       {annot},
	"field":          {annot},
	"notation":       {annot},
	"include":        {annot},
	"import":         {annot},
	"list":           {annot, opt("simpleType")},
	"union":          {annot, star("simpleType")},
	"openContent":    {annot, opt("any")},
	"defaultOpenContent": {annot, one("any")},
	"alternative":    {annot, opt("simpleType", "complexType")},
	"assert":         {annot},
	"assertion":      {annot},
}

// srcModelFor returns the content model for el, whose parent is parent.
//
// The name alone does not always decide the grammar: <restriction> and
// <extension> mean different things under simple and complex content, and
// <group>/<attributeGroup> take no children at all when they are a reference
// rather than a definition.
func srcModelFor(el, parent *xdm.Node) ([]srcTerm, bool) {
	name := el.Name.Local
	parentName := ""
	if parent != nil && parent.Name.URI == NSSchema {
		parentName = parent.Name.Local
	}

	switch name {
	case "complexType":
		// The three branches of complexTypeModel are disjoint on their
		// first element, so the child that is actually present picks
		// the branch. An empty type matches the third branch trivially.
		switch {
		case hasSchemaChild(el, "simpleContent"):
			return []srcTerm{annot, one("simpleContent")}, true
		case hasSchemaChild(el, "complexContent"):
			return []srcTerm{annot, one("complexContent")}, true
		}
		terms := []srcTerm{annot, opt("openContent"), opt(particleNames...)}
		terms = append(terms, attrDecls...)
		return append(terms, star("assert")), true

	case "restriction":
		switch parentName {
		case "simpleContent":
			// Simple content may re-state the base's simple type and
			// narrow it with facets, then redeclare attributes.
			terms := []srcTerm{annot, opt("simpleType"), star(facetNames...),
				opt("openContent")}
			terms = append(terms, attrDecls...)
			return append(terms, star("assert")), true
		case "complexContent":
			terms := []srcTerm{annot, opt("openContent"), opt(particleNames...)}
			terms = append(terms, attrDecls...)
			return append(terms, star("assert")), true
		case "simpleType":
			return []srcTerm{annot, opt("simpleType"), star(facetNames...)}, true
		}
		return nil, false

	case "extension":
		switch parentName {
		case "simpleContent":
			terms := []srcTerm{annot, opt("openContent")}
			terms = append(terms, attrDecls...)
			return append(terms, star("assert")), true
		case "complexContent":
			terms := []srcTerm{annot, opt("openContent"), opt(particleNames...)}
			terms = append(terms, attrDecls...)
			return append(terms, star("assert")), true
		}
		return nil, false

	case "attributeGroup":
		// A reference carries nothing but an annotation; a definition
		// carries the attributes it groups.
		if el.Attr("", "ref") != nil {
			return []srcTerm{annot}, true
		}
		return append([]srcTerm{annot}, attrDecls...), true

	case "group":
		if el.Attr("", "ref") != nil {
			return []srcTerm{annot}, true
		}
		return []srcTerm{annot, one("all", "choice", "sequence")}, true

	case "unique", "key", "keyref":
		// XSD 1.1 §3.11.2 lets an identity constraint be written as a
		// reference to one declared elsewhere, as <unique ref="..."/>.
		// Such a reference states no selector or field of its own — it
		// takes them from the constraint it names — so requiring them
		// here rejected the saxonData/Id id040 and id043 schemas, which
		// exist precisely to exercise the reference form.
		if el.Attr("", "ref") != nil {
			return []srcTerm{annot}, true
		}
		return []srcTerm{annot, one("selector"), plus("field")}, true
	}

	m, ok := srcModels[name]
	if !ok || m == nil {
		return nil, false
	}
	return m, true
}

// hasSchemaChild reports whether el has a child in the schema namespace with
// the given local name, ignoring elements conditional inclusion removes.
func hasSchemaChild(el *xdm.Node, name string) bool {
	for _, c := range el.ChildElements() {
		if c.Name.URI == NSSchema && c.Name.Local == name {
			return true
		}
	}
	return false
}

// checkSourceModel reports every way el's children depart from the schema for
// schemas, walking the whole subtree.
//
// Elements outside the schema namespace are skipped along with everything
// below them: <appinfo> and <documentation> take arbitrary content, and a
// foreign element anywhere else is already rejected by the readers.
func (p *parser) checkSourceModel(el *xdm.Node) {
	if el.Name.URI != NSSchema {
		return
	}
	// An annotation's own children are open content, so neither it nor
	// anything under it is checked here.
	if el.Name.Local == "annotation" {
		return
	}

	p.checkElementID(el)

	if terms, ok := srcModelFor(el, el.Parent); ok {
		p.matchSourceModel(el, terms)
	}
	for _, c := range el.ChildElements() {
		if !includeElement(c, p.schema.Version) {
			continue
		}
		p.checkSourceModel(c)
	}
}

// matchSourceModel walks el's schema-namespace children against terms,
// reporting the first child that does not fit.
//
// One error per element is enough: once the children have gone off the
// grammar, later positions are meaningless, and a cascade of complaints about
// the same mistake helps nobody.
func (p *parser) matchSourceModel(el *xdm.Node, terms []srcTerm) {
	var kids []*xdm.Node
	for _, c := range el.ChildElements() {
		if c.Name.URI != NSSchema {
			// A foreign-namespace element is allowed anywhere by the
			// {any} wildcards in the schema for schemas.
			continue
		}
		if !includeElement(c, p.schema.Version) {
			continue
		}
		kids = append(kids, c)
	}

	i := 0
	for _, t := range terms {
		n := 0
		for i < len(kids) && (t.max < 0 || n < t.max) && matchesName(kids[i], t.names) {
			i++
			n++
		}
		if n < t.min {
			want := strings.Join(t.names, " | ")
			if i < len(kids) {
				p.errs = append(p.errs, errorAt(kids[i], "src-"+el.Name.Local,
					"<%s> may not have <%s> here; expected <%s>",
					el.Name.Local, kids[i].Name.Local, want))
			} else {
				p.errs = append(p.errs, errorAt(el, "src-"+el.Name.Local,
					"<%s> is missing a required <%s> child",
					el.Name.Local, want))
			}
			return
		}
	}
	if i < len(kids) {
		p.errs = append(p.errs, errorAt(kids[i], "src-"+el.Name.Local,
			"<%s> may not have <%s> here",
			el.Name.Local, kids[i].Name.Local))
	}
}

// matchesName reports whether c's local name is one of names.
func matchesName(c *xdm.Node, names []string) bool {
	for _, n := range names {
		if c.Name.Local == n {
			return true
		}
	}
	return false
}

// checkElementID applies the xs:ID rules to an id= attribute.
//
// Almost every element in the schema for schemas carries `id = ID`, and ID
// means two things: the value is an NCName, and it is unique within the
// document that writes it — not across the assembled schema, which is why the
// set lives on schemaDoc. Neither was checked, which let a large part of MS-IdentityConstraint
// through — idA002 and idB002 write one id= on an element declaration and the
// same id= on a constraint beside it, idA006 writes the empty string. The same
// rule covers notatA005..A007 in MS-Notations.
//
// This sits in the source-model walk because that walk already visits every
// schema-namespace element exactly once, on every path into the readers.
func (p *parser) checkElementID(el *xdm.Node) {
	a := el.Attr("", "id")
	if a == nil || p.doc == nil {
		return
	}
	if !isNCName(a.Value) {
		p.errs = append(p.errs, errorAt(el, "src-resolve",
			"id %q is not an NCName", a.Value))
		return
	}
	if p.doc.ids[a.Value] {
		p.errs = append(p.errs, errorAt(el, "src-resolve",
			"id %q is already used in this schema", a.Value))
		return
	}
	if p.doc.ids == nil {
		p.doc.ids = map[string]bool{}
	}
	p.doc.ids[a.Value] = true
}
