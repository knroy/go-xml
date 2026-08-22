package relaxng

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Syntax checking of the schema document itself.
//
// RELAX NG's XML syntax is tightly specified — each element takes exactly
// which attributes and which children — and more than half the conformance
// suite is schemas that must be *rejected* for breaking those rules. Checking
// them is therefore not a nicety: a compiler that quietly accepts a malformed
// schema validates documents against something the author did not write.
//
// The checks are structural only. Whether a pattern is *meaningful* — an
// attribute inside a list, a text inside a data — is checked during
// compilation, where the context is known.

// elementSpec says what one RELAX NG element may carry.
type elementSpec struct {
	// attrs are the attributes permitted, beyond the ones allowed everywhere.
	attrs []string
	// required are attributes that must be present.
	required []string
	// minPatterns is how many pattern children the element must have.
	minPatterns int
	// maxPatterns bounds them; -1 means no bound.
	maxPatterns int
	// nameClass says the element takes a name class, either as name= or as a
	// name-class child, and exactly one of the two.
	nameClass bool
	// textOnly says the element's content is character data, so a child
	// element is an error.
	textOnly bool
	// maxExcept bounds the <except> children. Zero means unbounded here,
	// since only <data> constrains it.
	maxExcept int
}

// specs is the grammar of the XML syntax, one entry per element.
var specs = map[string]elementSpec{
	"element":     {nameClass: true, minPatterns: 1, maxPatterns: -1},
	"attribute":   {nameClass: true, minPatterns: 0, maxPatterns: 1},
	"group":       {minPatterns: 1, maxPatterns: -1},
	"interleave":  {minPatterns: 1, maxPatterns: -1},
	"choice":      {minPatterns: 1, maxPatterns: -1},
	"optional":    {minPatterns: 1, maxPatterns: -1},
	"zeroOrMore":  {minPatterns: 1, maxPatterns: -1},
	"oneOrMore":   {minPatterns: 1, maxPatterns: -1},
	"list":        {minPatterns: 1, maxPatterns: -1},
	"mixed":       {minPatterns: 1, maxPatterns: -1},
	"ref":         {attrs: []string{"name"}, required: []string{"name"}, maxPatterns: 0},
	"parentRef":   {attrs: []string{"name"}, required: []string{"name"}, maxPatterns: 0},
	"empty":       {maxPatterns: 0},
	"text":        {maxPatterns: 0},
	"notAllowed":  {maxPatterns: 0},
	"value":       {attrs: []string{"type"}, maxPatterns: 0, textOnly: true},
	"data":        {attrs: []string{"type"}, required: []string{"type"}, maxPatterns: -1, maxExcept: 1},
	"externalRef": {attrs: []string{"href"}, required: []string{"href"}, maxPatterns: 0},
	"grammar":     {maxPatterns: -1},
	"start":       {attrs: []string{"combine"}, minPatterns: 1, maxPatterns: -1},
	"define":      {attrs: []string{"name", "combine"}, required: []string{"name"}, minPatterns: 1, maxPatterns: -1},
	"include":     {attrs: []string{"href"}, required: []string{"href"}, maxPatterns: -1},
	"div":         {maxPatterns: -1},
	"param":       {attrs: []string{"name"}, required: []string{"name"}, maxPatterns: 0, textOnly: true},
	"except":      {minPatterns: 1, maxPatterns: -1},
	"name":        {maxPatterns: 0, textOnly: true},
	"anyName":     {maxPatterns: -1},
	"nsName":      {maxPatterns: -1},
}

// commonAttrs may appear on any RELAX NG element.
//
// ns and datatypeLibrary are inherited down the schema, which is why they are
// permitted everywhere rather than only where they take effect.
var commonAttrs = map[string]bool{"ns": true, "datatypeLibrary": true}

// grammarChildren are the only elements a <grammar> may hold.
//
// §4.18: a grammar is definitions, not patterns. An <element> written
// directly inside one is not a start — it is nothing, and reading past it
// validates against a grammar with no content where the author thought they
// had written some.
var grammarChildren = map[string]bool{
	"start": true, "define": true, "div": true, "include": true,
}

// checkGrammarChildren applies §4.18 to <grammar> and <div>.
func checkGrammarChildren(n *xdm.Node) error {
	switch n.Name.Local {
	case "grammar":
	case "div":
		// A <div> groups whatever its parent groups. Inside a grammar it
		// holds definitions; written where a pattern belongs it holds
		// patterns, and the grammar rule does not apply to it.
		if !inGrammar(n) {
			return nil
		}
	default:
		return nil
	}
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		if !grammarChildren[kid.Name.Local] {
			return fmt.Errorf(
				"relaxng: <%s> holds <%s>; a grammar takes only <start>, "+
					"<define>, <div> and <include> (section 4.18)",
				n.Name.Local, kid.Name.Local)
		}
	}
	return nil
}

// inGrammar reports whether n sits inside a <grammar>, with only <div>
// between.
func inGrammar(n *xdm.Node) bool {
	for cur := n.Parent; cur != nil && cur.Kind == xdm.KindElement; cur = cur.Parent {
		if cur.Name.URI != NS {
			return false
		}
		switch cur.Name.Local {
		case "grammar", "include":
			return true
		case "div":
			continue
		}
		return false
	}
	return false
}

// checkSyntax walks a schema document and reports the first violation.
func checkSyntax(n *xdm.Node) error {
	if n.Name.URI != NS {
		// A foreign element is permitted wherever a pattern is not required,
		// and ignored. Its subtree is not checked, since it is not RELAX NG.
		return nil
	}
	spec, known := specs[n.Name.Local]
	if !known {
		return fmt.Errorf("relaxng: <%s> is not a RELAX NG element", n.Name.Local)
	}
	// A <choice> of name classes is not a pattern, so the pattern spec does
	// not describe it: it holds names, and requiring a pattern child would
	// reject every element that offers two names for itself.
	if n.Name.Local == "choice" && isNameClassChoice(n) {
		spec = elementSpec{minPatterns: 0, maxPatterns: -1}
	}

	if err := checkAttrs(n, spec); err != nil {
		return err
	}
	if err := checkAttrValues(n); err != nil {
		return err
	}
	if err := checkNameClass(n, spec); err != nil {
		return err
	}
	if err := checkChildren(n, spec); err != nil {
		return err
	}
	if err := checkNameClassExcept(n); err != nil {
		return err
	}
	if err := checkGrammarChildren(n); err != nil {
		return err
	}
	if spec.maxExcept > 0 {
		var n_except int
		for _, kid := range n.ChildElements() {
			if kid.Name.URI == NS && kid.Name.Local == "except" {
				n_except++
			}
		}
		if n_except > spec.maxExcept {
			return fmt.Errorf(
				"relaxng: <%s> has %d <except> children; at most %d is allowed",
				n.Name.Local, n_except, spec.maxExcept)
		}
	}
	for _, kid := range n.ChildElements() {
		if err := checkSyntax(kid); err != nil {
			return err
		}
	}
	return nil
}

func checkAttrs(n *xdm.Node, spec elementSpec) error {
	allowed := map[string]bool{}
	for _, a := range spec.attrs {
		allowed[a] = true
	}
	if spec.nameClass {
		allowed["name"] = true
	}
	for _, a := range n.Attrs {
		// A foreign-namespaced attribute is permitted and ignored, which is
		// how a schema carries annotations. xml:base and friends likewise.
		if a.Name.URI != "" {
			continue
		}
		if commonAttrs[a.Name.Local] || allowed[a.Name.Local] {
			continue
		}
		return fmt.Errorf("relaxng: <%s> has no attribute %q",
			n.Name.Local, a.Name.Local)
	}
	for _, req := range spec.required {
		if n.AttrValue(req) == "" {
			return fmt.Errorf("relaxng: <%s> requires a %s attribute",
				n.Name.Local, req)
		}
	}
	return nil
}

// checkNameClass enforces that a name is given exactly once.
//
// <element name="foo"><name>foo</name>...</element> is an error: the name is
// given twice, and the language has no rule for reconciling them.
func checkNameClass(n *xdm.Node, spec elementSpec) error {
	if !spec.nameClass {
		return nil
	}
	hasAttr := n.AttrValue("name") != ""
	var classes int
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		switch kid.Name.Local {
		case "name", "anyName", "nsName":
			classes++
		case "choice":
			// A <choice> directly inside <element> is ambiguous in the
			// abstract, but the syntax resolves it: a choice of name classes
			// comes first, before any pattern.
			if isNameClassChoice(kid) {
				classes++
			}
		}
	}
	switch {
	case hasAttr && classes > 0:
		return fmt.Errorf(
			"relaxng: <%s> gives a name both as an attribute and as a child",
			n.Name.Local)
	case !hasAttr && classes == 0:
		return fmt.Errorf("relaxng: <%s> has no name", n.Name.Local)
	case classes > 1:
		return fmt.Errorf("relaxng: <%s> has more than one name class",
			n.Name.Local)
	}
	return nil
}

// isNameClassChoice reports whether a <choice> holds name classes rather than
// patterns.
func isNameClassChoice(n *xdm.Node) bool {
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		switch kid.Name.Local {
		case "name", "anyName", "nsName":
			return true
		}
		return false
	}
	return false
}

func checkChildren(n *xdm.Node, spec elementSpec) error {
	if spec.textOnly {
		if len(n.ChildElements()) > 0 {
			return fmt.Errorf("relaxng: <%s> takes text, not elements",
				n.Name.Local)
		}
		return nil
	}
	if spec.maxPatterns == 0 {
		for _, kid := range n.ChildElements() {
			if kid.Name.URI == NS {
				return fmt.Errorf("relaxng: <%s> takes no pattern",
					n.Name.Local)
			}
		}
		if strings.TrimSpace(n.StringValue()) != "" && n.Name.Local != "value" {
			return fmt.Errorf("relaxng: <%s> takes no content", n.Name.Local)
		}
		return nil
	}
	count := len(patternChildren(n))
	if n.Name.Local == "except" && isNameClassExcept(n) {
		// An <except> inside a name class holds name classes, which are not
		// patterns. Counting patterns here would reject every anyName that
		// excludes a name — the commonest thing an except is used for.
		count = 0
		for _, kid := range n.ChildElements() {
			if kid.Name.URI == NS && (isNameClass(kid.Name.Local) ||
				kid.Name.Local == "choice") {
				count++
			}
		}
	}
	if count < spec.minPatterns {
		return fmt.Errorf("relaxng: <%s> requires at least %d pattern",
			n.Name.Local, spec.minPatterns)
	}
	if spec.maxPatterns >= 0 && count > spec.maxPatterns {
		return fmt.Errorf("relaxng: <%s> takes at most %d pattern",
			n.Name.Local, spec.maxPatterns)
	}
	// Character data where a pattern belongs is an error: a schema is
	// elements, and stray text is a typo the author will not otherwise see.
	for _, kid := range n.Children {
		if kid.Kind == xdm.KindText && !whitespaceOnly(kid.Value) {
			return fmt.Errorf("relaxng: <%s> contains character data",
				n.Name.Local)
		}
	}
	return nil
}

// checkAttrValues applies the lexical rules on the attributes themselves.
//
// The RELAX NG schema for schemas declares these types, and the conformance
// suite tests them directly: a name that is not an NCName, or a
// datatypeLibrary that is not an absolute URI without a fragment, is a
// malformed schema rather than a schema naming something unusual.
func checkAttrValues(n *xdm.Node) error {
	if v := n.AttrValue("datatypeLibrary"); v != "" {
		if err := checkDatatypeLibrary(normalizeToken(v)); err != nil {
			return err
		}
	}
	switch n.Name.Local {
	case "element", "attribute":
		if v := n.AttrValue("name"); v != "" {
			if !isQName(normalizeToken(v)) {
				return fmt.Errorf(
					"relaxng: <%s> name %q is not a QName", n.Name.Local, v)
			}
		}
		if n.Name.Local == "attribute" {
			if err := checkAttributeName(n, normalizeToken(n.AttrValue("name")),
				nsInForce(n)); err != nil {
				return err
			}
		}
	case "ref", "parentRef", "define":
		if v := n.AttrValue("name"); v != "" {
			if !xdm.IsNCName(normalizeToken(v)) {
				return fmt.Errorf(
					"relaxng: <%s> name %q is not an NCName", n.Name.Local, v)
			}
		}
	case "name":
		if !isQName(normalizeToken(n.StringValue())) {
			return fmt.Errorf("relaxng: <name> %q is not a QName",
				strings.TrimSpace(n.StringValue()))
		}
		// A <name> inside an <attribute> names an attribute, so the same
		// rules apply as to attribute name=. A name inside an <except> is
		// excluded rather than named, so the rule does not reach it: an
		// attribute class that *excepts* xmlns is exactly the right way to
		// write "any attribute but a namespace declaration".
		if namesAnAttribute(n) {
			if err := checkAttributeName(n, normalizeToken(n.StringValue()),
				nsInForce(n)); err != nil {
				return err
			}
		}
	}
	return nil
}

// xmlnsNS is the namespace reserved for namespace declarations. No attribute
// a schema declares may live in it.
const xmlnsNS = "http://www.w3.org/2000/xmlns"

// checkAttributeName applies §4.10: an <attribute> may not name a namespace
// declaration.
//
// A namespace declaration is not an attribute as far as validation is
// concerned — it is consumed by the XML parser and never reaches the pattern —
// so a schema that declares one is describing something that cannot occur.
// The rule catches it at both spellings: the name xmlns with no namespace, and
// any name in the xmlns namespace.
func checkAttributeName(n *xdm.Node, name, ns string) error {
	if name == "" {
		return nil
	}
	local := name
	prefixed := false
	if i := strings.IndexByte(name, ':'); i >= 0 {
		local, prefixed = name[i+1:], true
	}
	if !prefixed && local == "xmlns" && ns == "" {
		return fmt.Errorf(
			"relaxng: <attribute> names xmlns, which is a namespace " +
				"declaration rather than an attribute (section 4.10)")
	}
	if ns == xmlnsNS || ns == xmlnsNS+"/" {
		return fmt.Errorf(
			"relaxng: <attribute> is in the namespace-declaration namespace %q "+
				"(section 4.10)", ns)
	}
	return nil
}

// checkDatatypeLibrary applies §3 of the spec: the value must be an absolute
// URI with no fragment identifier.
//
// The rule exists because the library is an *identity*, not a location: two
// schemas naming the same library must mean the same types, and a fragment or
// a relative reference makes that depend on where the schema was read from.
func checkDatatypeLibrary(v string) error {
	if v == "" {
		return nil
	}
	if strings.Contains(v, "#") {
		return fmt.Errorf(
			"relaxng: datatypeLibrary %q has a fragment identifier", v)
	}
	i := strings.IndexByte(v, ':')
	if i <= 0 {
		return fmt.Errorf("relaxng: datatypeLibrary %q is not an absolute URI", v)
	}
	// A scheme alone is not a URI: "foo:" names no library.
	if i == len(v)-1 {
		return fmt.Errorf(
			"relaxng: datatypeLibrary %q is a scheme with no path", v)
	}
	// The scheme is ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ).
	scheme := v[:i]
	for j := 0; j < len(scheme); j++ {
		c := scheme[j]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case j > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return fmt.Errorf(
				"relaxng: datatypeLibrary %q does not begin with a URI scheme", v)
		}
	}
	// A percent-escape must be two hex digits, which is the one syntax error
	// the suite tests directly.
	for j := 0; j < len(v); j++ {
		if v[j] != '%' {
			continue
		}
		if j+2 >= len(v) || !isHex(v[j+1]) || !isHex(v[j+2]) {
			return fmt.Errorf(
				"relaxng: datatypeLibrary %q has a malformed percent-escape", v)
		}
	}
	return nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// namesAnAttribute reports whether a <name> element gives an attribute's name,
// as opposed to excluding one.
//
// §4.16 phrases this as "the first child of an attribute element, or the
// descendant of the first child" — that is, within the name class itself, and
// not below an <except>, which negates rather than names.
func namesAnAttribute(n *xdm.Node) bool {
	for cur := n.Parent; cur != nil && cur.Kind == xdm.KindElement; cur = cur.Parent {
		if cur.Name.URI != NS {
			return false
		}
		switch cur.Name.Local {
		case "except":
			return false
		case "attribute":
			return true
		case "choice", "anyName", "nsName":
			// still inside the name class
		default:
			return false
		}
	}
	return false
}

// isNameClassExcept reports whether an <except> belongs to a name class
// rather than to a <data>.
func isNameClassExcept(n *xdm.Node) bool {
	p := n.Parent
	if p == nil || p.Kind != xdm.KindElement || p.Name.URI != NS {
		return false
	}
	return p.Name.Local == "anyName" || p.Name.Local == "nsName"
}

// checkNameClassExcept applies §4.16 to <anyName> and <nsName>.
//
// A name class may exclude names, but not in a way that leaves nothing or
// says nothing: <anyName><except><anyName/></except></anyName> excludes every
// name it admits, and an nsName that excepts an anyName does the same within
// its namespace. Both are refused because the result matches nothing, which
// notAllowed already says plainly.
func checkNameClassExcept(n *xdm.Node) error {
	if n.Name.Local != "anyName" && n.Name.Local != "nsName" {
		return nil
	}
	var excepts int
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS || kid.Name.Local != "except" {
			continue
		}
		excepts++
		if excepts > 1 {
			return fmt.Errorf(
				"relaxng: <%s> has more than one <except>", n.Name.Local)
		}
		// anyName may hold no anyName below its except; nsName may hold
		// neither.
		bad := []string{"anyName"}
		if n.Name.Local == "nsName" {
			bad = append(bad, "nsName")
		}
		if found := findDescendant(kid, bad); found != "" {
			return fmt.Errorf(
				"relaxng: <%s> excepts <%s>, which excludes everything it "+
					"admits (section 4.16)", n.Name.Local, found)
		}
	}
	return nil
}

// findDescendant returns the first of names found at or below n.
func findDescendant(n *xdm.Node, names []string) string {
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		for _, want := range names {
			if kid.Name.Local == want {
				return want
			}
		}
		if found := findDescendant(kid, names); found != "" {
			return found
		}
	}
	return ""
}

// isQName reports whether s is an NCName or prefix:NCName.
func isQName(s string) bool {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return xdm.IsNCName(s[:i]) && xdm.IsNCName(s[i+1:])
	}
	return xdm.IsNCName(s)
}
