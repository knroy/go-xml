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

	// Every constraining facet takes an annotation and nothing else. The
	// facets were absent from this table, so <xs:notation> nested inside
	// an <xs:enumeration> or an <xs:length> loaded without complaint
	// (notatF025, notatF041, notatF045, notatF049, notatF053).
	"minExclusive":     {annot},
	"minInclusive":     {annot},
	"maxExclusive":     {annot},
	"maxInclusive":     {annot},
	"totalDigits":      {annot},
	"fractionDigits":   {annot},
	"length":           {annot},
	"minLength":        {annot},
	"maxLength":        {annot},
	"enumeration":      {annot},
	"whiteSpace":       {annot},
	"pattern":          {annot},
	"explicitTimezone": {annot},

	// An annotation holds appinfo and documentation, in any order and any
	// number, and nothing else — notatF003 puts an <xs:notation> straight
	// under one. The *contents* of appinfo and documentation are open,
	// which checkSourceModel handles by not descending into them.
	"annotation": {star("appinfo", "documentation")},

	// <redefine> admits the ·redefinable· components and annotations,
	// interleaved in any order — the one place an annotation is not
	// restricted to the front. Notably absent is <notation>, which is not
	// redefinable, so notatF055 is not a schema.
	"redefine": {star("annotation", "simpleType", "complexType",
		"group", "attributeGroup")},

	// <override> takes a leading annotation and then anything that may
	// appear at the top of a schema, which unlike <redefine> does include
	// <notation> and <element> and <attribute>.
	"override": {annot, star("simpleType", "complexType", "group",
		"attributeGroup", "element", "attribute", "notation")},
}

// srcAttrs maps a schema element's local name to the attributes it may carry,
// beside `id` and any attribute in a non-schema namespace — both of which the
// schema for schemas allows on every element and checkAttrs handles separately.
//
// The lists are the "XML Representation Summary" boxes of XSD 1.0 §3 and XSD
// 1.1 §3, unioned across the two versions. Taking the union is deliberate:
// this check exists to catch a name that is simply not an XSD attribute — the
// `foo="bar"` of wildI003, the `minOccurs` that wildQ002 puts on an
// <anyAttribute> which has no occurrence range at all. Whether a 1.1-only
// attribute is *honoured* under 1.0 is a separate question, decided where that
// attribute is read (readDisallowedNames does exactly this for notQName), and
// rejecting it here would pre-empt that decision for every one of them at once.
var srcAttrs = map[string][]string{
	"schema": {"attributeFormDefault", "blockDefault", "elementFormDefault",
		"finalDefault", "targetNamespace", "version", "lang",
		"defaultAttributes", "xpathDefaultNamespace"},
	"element": {"abstract", "block", "default", "final", "fixed", "form",
		"maxOccurs", "minOccurs", "name", "nillable", "ref",
		"substitutionGroup", "type", "targetNamespace"},
	"attribute": {"default", "fixed", "form", "name", "ref", "type", "use",
		"targetNamespace", "inheritable"},
	"complexType": {"abstract", "block", "final", "mixed", "name",
		"defaultAttributesApply"},
	"simpleType":     {"final", "name"},
	"simpleContent":  {},
	"complexContent": {"mixed"},
	"restriction":    {"base"},
	"extension":      {"base"},
	"attributeGroup": {"name", "ref"},
	"group":          {"maxOccurs", "minOccurs", "name", "ref"},
	"all":            {"maxOccurs", "minOccurs"},
	"choice":         {"maxOccurs", "minOccurs"},
	"sequence":       {"maxOccurs", "minOccurs"},
	"any": {"maxOccurs", "minOccurs", "namespace", "processContents",
		"notNamespace", "notQName"},
	"anyAttribute": {"namespace", "processContents",
		"notNamespace", "notQName"},
	"unique":      {"name", "ref"},
	"key":         {"name", "ref"},
	"keyref":      {"name", "refer", "ref"},
	"selector":    {"xpath", "xpathDefaultNamespace"},
	"field":       {"xpath", "xpathDefaultNamespace"},
	"notation":    {"name", "public", "system"},
	"include":     {"schemaLocation"},
	"import":      {"namespace", "schemaLocation"},
	"redefine":    {"schemaLocation"},
	"override":    {"schemaLocation"},
	"list":        {"itemType"},
	"union":       {"memberTypes"},
	"annotation":  {},
	"openContent": {"mode"},
	"defaultOpenContent": {"mode", "appliesToEmpty"},
	"alternative": {"test", "type", "xpathDefaultNamespace"},
	"assert":      {"test", "xpathDefaultNamespace"},
	"assertion":   {"test", "xpathDefaultNamespace"},
}

// checkAttrs reports attributes el carries that the schema for schemas does
// not allow on it.
//
// Two kinds of attribute are always permitted and so are skipped: `id`, which
// every XSD element takes (and checkID validates), and anything in a
// non-schema namespace, which the "{any attributes with non-schema namespace}"
// clause in every summary box admits — that is why wildI002's `a:b="c"` is
// fine while its unprefixed `b="c"` is not.
//
// A prefixed attribute whose namespace *is* the schema namespace is not
// allowed: the wildcard in the schema for schemas excludes its own namespace.
func (p *parser) checkAttrs(el *xdm.Node) {
	allowed, ok := srcAttrs[el.Name.Local]
	if !ok {
		return
	}
	for _, a := range el.Attrs {
		if a.Name.URI != "" && a.Name.URI != NSSchema {
			continue
		}
		if a.Name.Local == "id" {
			continue
		}
		found := false
		for _, n := range allowed {
			if n == a.Name.Local {
				found = true
				break
			}
		}
		if !found {
			p.errs = append(p.errs, errorAt(el, "",
				"attribute %q is not allowed on xs:%s",
				a.Name.Local, el.Name.Local))
			continue
		}
		// Every `name` in the schema for schemas is an xs:NCName — the
		// component being declared gets its {name} from it, and a
		// component name is a local name with no colon in it. The
		// readers take the value verbatim, so without this a group
		// called "1" (groupA010) or "a:b" (groupA012) declared itself
		// happily under a name no reference could ever be written for.
		//
		// The value is trimmed first. NCName derives from xs:token, so
		// its whiteSpace facet is a fixed "collapse" and leading and
		// trailing space is gone before the value is ever matched
		// against the NCName production. addB193 declares
		// name="sub2-elem " with a trailing space and the suite expects
		// it to be accepted, under exactly that rule.
		if a.Name.Local == "name" && !isNCName(strings.TrimSpace(a.Value)) {
			p.errs = append(p.errs, errorAt(el, "",
				"name=%q is not a valid NCName", a.Value))
		}
	}
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
	// An <appinfo> or <documentation> holds open content, so nothing under
	// one is checked. The annotation itself still is: it admits only those
	// two children (notatF003).
	if el.Name.Local == "appinfo" || el.Name.Local == "documentation" {
		return
	}

	p.checkAttrs(el)

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

// checkID applies xs:ID to the id attribute the schema for schemas puts on
// every XSD element.
//

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

