package relaxng

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// NS is the RELAX NG structure namespace.
const NS = "http://relaxng.org/ns/structure/1.0"

// Compile builds a schema from a parsed RELAX NG document in the XML syntax.
//
// The grammar is checked as it is read rather than afterwards: RELAX NG's
// restrictions are mostly about what may appear where — an attribute inside an
// attribute, a text inside a list — and catching those at the point of use
// gives an error that names the construct.
func Compile(doc *xdm.Node) (*Schema, error) {
	root := doc
	if root.Kind == xdm.KindDocument {
		root = nil
		for _, c := range doc.Children {
			if c.Kind == xdm.KindElement {
				root = c
				break
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("relaxng: the schema has no root element")
	}
	if root.Name.URI != NS {
		return nil, fmt.Errorf(
			"relaxng: the schema root is {%s}%s, not a RELAX NG pattern",
			root.Name.URI, root.Name.Local)
	}

	// The syntax is checked before anything is compiled: more than half the
	// conformance suite is schemas that must be rejected for breaking the
	// XML syntax's rules, and a compiler that reads past them validates
	// documents against something the author did not write.
	if err := checkSyntax(root); err != nil {
		return nil, err
	}
	// The restrictions of section 7 are a separate pass because they are
	// contextual: whether a construct is legal depends on what encloses it,
	// which the structural check above does not track.
	if err := checkRestrictions(root); err != nil {
		return nil, err
	}

	c := &compiler{defines: map[string]*xdm.Node{},
		combined: map[string][]*xdm.Node{}, how: map[string]string{}}
	p, err := c.compileTop(root)
	if err != nil {
		return nil, err
	}
	// Sections 7.3 and 7.4 are checked on the compiled pattern rather than
	// the syntax: they ask whether two name classes overlap, which is a
	// question about the classes, not about how they were written.
	if err := checkCompetition(p); err != nil {
		return nil, err
	}
	if err := checkStringSequences(p); err != nil {
		return nil, err
	}
	return &Schema{start: p}, nil
}

type compiler struct {
	// combined holds the further definitions of a name beyond the first, to
	// be joined by the combine= method they all agreed on.
	combined map[string][]*xdm.Node
	// starts holds further <start> elements, likewise.
	starts []*xdm.Node
	// how records the combine method agreed for each name.
	how map[string]string
	// defines maps a name to the <define> that provides it, so that <ref>
	// resolves. A grammar is flat: nested <grammar> elements each have their
	// own scope, which parentRef reaches out of.
	defines map[string]*xdm.Node
	// depth bounds recursion through <ref>, which a schema may make
	// self-referential.
	depth int
}

// maxRefDepth bounds how deep a chain of <ref> may go while compiling.
//
// A recursive definition is legal and common — a list whose items may contain
// lists — so the pattern is built lazily rather than expanded, and this bound
// only catches a definition that refers to itself with nothing in between.
const maxRefDepth = 500

func (c *compiler) compileTop(root *xdm.Node) (Pattern, error) {
	if root.Name.Local == "grammar" {
		return c.compileGrammar(root)
	}
	return c.compilePattern(root)
}

// compileGrammar reads <grammar>, collecting its definitions before compiling
// <start>, so that a <ref> may name a definition that appears later.
func (c *compiler) compileGrammar(g *xdm.Node) (Pattern, error) {
	var start *xdm.Node
	var collect func(n *xdm.Node) error
	collect = func(n *xdm.Node) error {
		for _, kid := range n.ChildElements() {
			if kid.Name.URI != NS {
				continue
			}
			switch kid.Name.Local {
			case "start":
				if start != nil {
					c.starts = append(c.starts, kid)
					break
				}
				start = kid
			case "define":
				name := normalizeToken(kid.AttrValue("name"))
				if name == "" {
					return fmt.Errorf("relaxng: <define> has no name")
				}
				// Section 4.17 is checked once the whole grammar has been
				// read, since it constrains the *set* of definitions of a
				// name rather than any pair of them.
				if _, dup := c.defines[name]; !dup {
					c.defines[name] = kid
				} else {
					c.combined[name] = append(c.combined[name], kid)
				}
			case "div":
				// <div> groups definitions for documentation and has no
				// effect on the grammar, so its children are collected as if
				// written in its parent.
				if err := collect(kid); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collect(g); err != nil {
		return nil, err
	}
	// Section 4.17, applied to each name once the grammar has been read.
	for name, first := range c.defines {
		how, err := agreedCombine("definition of "+strconv.Quote(name),
			append([]*xdm.Node{first}, c.combined[name]...))
		if err != nil {
			return nil, err
		}
		c.how[name] = how
	}
	if start == nil {
		return nil, fmt.Errorf("relaxng: <grammar> has no <start>")
	}
	startCombine, err := agreedCombine("<start>",
		append([]*xdm.Node{start}, c.starts...))
	if err != nil {
		return nil, err
	}
	p, err := c.compileChildren(start)
	if err != nil {
		return nil, err
	}
	join := choice
	if startCombine == "interleave" {
		join = interleave
	}
	for _, extra := range c.starts {
		q, err := c.compileChildren(extra)
		if err != nil {
			return nil, err
		}
		p = join(p, q)
	}
	return p, nil
}

// compileChildren compiles an element's pattern children as a group.
//
// Several RELAX NG elements take a sequence of patterns and mean their group:
// <start>, <define>, <element> and <group> all do. Writing that once is what
// keeps the individual cases short.
func (c *compiler) compileChildren(n *xdm.Node) (Pattern, error) {
	kids := patternChildren(n)
	if len(kids) == 0 {
		return nil, fmt.Errorf("relaxng: <%s> has no pattern", n.Name.Local)
	}
	p, err := c.compilePattern(kids[0])
	if err != nil {
		return nil, err
	}
	for _, kid := range kids[1:] {
		q, err := c.compilePattern(kid)
		if err != nil {
			return nil, err
		}
		p = group(p, q)
	}
	return p, nil
}

func patternChildren(n *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	for _, kid := range n.ChildElements() {
		if kid.Name.URI == NS && kid.Name.Local != "param" &&
			kid.Name.Local != "except" && kid.Name.Local != "name" &&
			kid.Name.Local != "anyName" && kid.Name.Local != "nsName" &&
			kid.Name.Local != "choice-name" {
			out = append(out, kid)
		}
	}
	return out
}

func (c *compiler) compilePattern(n *xdm.Node) (Pattern, error) {
	if n.Name.URI != NS {
		return nil, fmt.Errorf("relaxng: {%s}%s is not a RELAX NG pattern",
			n.Name.URI, n.Name.Local)
	}
	switch n.Name.Local {
	case "empty":
		return Empty{}, nil
	case "notAllowed":
		return NotAllowed{}, nil
	case "text":
		return Text{}, nil

	case "element":
		nc, err := c.nameClass(n)
		if err != nil {
			return nil, err
		}
		body, err := c.compileChildren(n)
		if err != nil {
			return nil, err
		}
		return Element{Name: nc, Pattern: body}, nil

	case "attribute":
		nc, err := c.nameClass(n)
		if err != nil {
			return nil, err
		}
		kids := patternChildren(n)
		var body Pattern = Text{}
		if len(kids) > 0 {
			body, err = c.compileChildren(n)
			if err != nil {
				return nil, err
			}
		}
		return Attribute{Name: nc, Pattern: body}, nil

	case "group":
		return c.compileChildren(n)

	case "interleave":
		return c.combine(n, interleave)

	case "choice":
		return c.combine(n, choice)

	case "optional":
		p, err := c.compileChildren(n)
		if err != nil {
			return nil, err
		}
		return choice(p, Empty{}), nil

	case "zeroOrMore":
		p, err := c.compileChildren(n)
		if err != nil {
			return nil, err
		}
		return choice(oneOrMore(p), Empty{}), nil

	case "oneOrMore":
		p, err := c.compileChildren(n)
		if err != nil {
			return nil, err
		}
		return oneOrMore(p), nil

	case "mixed":
		// <mixed>p</mixed> is <interleave><text/>p</interleave>.
		p, err := c.compileChildren(n)
		if err != nil {
			return nil, err
		}
		return interleave(Text{}, p), nil

	case "list":
		p, err := c.compileChildren(n)
		if err != nil {
			return nil, err
		}
		return List{Pattern: p}, nil

	case "value":
		return c.compileValue(n)

	case "data":
		return c.compileData(n)

	case "ref":
		return c.compileRef(n)

	case "grammar":
		sub := &compiler{defines: map[string]*xdm.Node{},
			combined: map[string][]*xdm.Node{}, how: map[string]string{},
			depth: c.depth}
		return sub.compileGrammar(n)

	case "externalRef", "include", "parentRef":
		// Each of these reaches outside the document. externalRef and include
		// name a file, which is the fetch AllowDOCTYPE-style gating exists to
		// refuse; parentRef needs a grammar stack this compiler does not
		// keep. Refusing is honest — silently ignoring one would validate
		// against a schema the author did not write.
		return nil, fmt.Errorf(
			"relaxng: <%s> is not supported; it reaches outside the schema document",
			n.Name.Local)
	}
	return nil, fmt.Errorf("relaxng: <%s> is not a RELAX NG pattern",
		n.Name.Local)
}

func (c *compiler) combine(n *xdm.Node, f func(a, b Pattern) Pattern) (Pattern, error) {
	kids := patternChildren(n)
	if len(kids) == 0 {
		return nil, fmt.Errorf("relaxng: <%s> has no pattern", n.Name.Local)
	}
	p, err := c.compilePattern(kids[0])
	if err != nil {
		return nil, err
	}
	for _, kid := range kids[1:] {
		q, err := c.compilePattern(kid)
		if err != nil {
			return nil, err
		}
		p = f(p, q)
	}
	return p, nil
}

func (c *compiler) compileRef(n *xdm.Node) (Pattern, error) {
	name := normalizeToken(n.AttrValue("name"))
	def, ok := c.defines[name]
	if !ok {
		return nil, fmt.Errorf("relaxng: <ref> names %q, which no <define> provides", name)
	}
	if c.depth >= maxRefDepth {
		return nil, fmt.Errorf(
			"relaxng: definition %q recurses more than %d deep", name, maxRefDepth)
	}
	c.depth++
	defer func() { c.depth-- }()
	p, err := c.compileChildren(def)
	if err != nil {
		return nil, err
	}
	// The further definitions of this name are joined by the method they all
	// declared. Combining is what makes a grammar extensible: a schema may
	// add an alternative to a definition it did not write.
	join := choice
	if c.how[name] == "interleave" {
		join = interleave
	}
	for _, extra := range c.combined[name] {
		q, err := c.compileChildren(extra)
		if err != nil {
			return nil, err
		}
		p = join(p, q)
	}
	return p, nil
}

func (c *compiler) compileValue(n *xdm.Node) (Pattern, error) {
	lib, name := datatypeOf(n, "token")
	dt, err := lookupDatatype(lib, name)
	if err != nil {
		return nil, fmt.Errorf("relaxng: <value>: %w", err)
	}
	return Value{Type: dt, Value: n.StringValue()}, nil
}

func (c *compiler) compileData(n *xdm.Node) (Pattern, error) {
	lib, name := datatypeOf(n, "")
	if name == "" {
		return nil, fmt.Errorf("relaxng: <data> has no type")
	}
	dt, err := lookupDatatype(lib, name)
	if err != nil {
		return nil, fmt.Errorf("relaxng: <data>: %w", err)
	}
	d := Data{Type: dt}
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		switch kid.Name.Local {
		case "param":
			pn := kid.AttrValue("name")
			if pn == "" {
				return nil, fmt.Errorf("relaxng: <param> has no name")
			}
			d.Params = append(d.Params, Param{Name: pn, Value: kid.StringValue()})
		case "except":
			ex, err := c.compileChildren(kid)
			if err != nil {
				return nil, err
			}
			d.Except = ex
		}
	}
	return d, nil
}

// datatypeOf reads the type and the datatypeLibrary in force.
//
// datatypeLibrary is inherited from an ancestor, which is the only attribute
// in the language that works that way, so it is resolved by walking up rather
// than read from the element alone.
func datatypeOf(n *xdm.Node, dflt string) (library, name string) {
	// These are declared xsd:NCName and xsd:anyURI in the RELAX NG schema for
	// schemas, so they are whitespace-normalised: a type written across lines
	// names the same type as one written inline.
	name = normalizeToken(n.AttrValue("type"))
	if name == "" {
		name = dflt
	}
	for cur := n; cur != nil; cur = cur.Parent {
		if v := normalizeToken(cur.AttrValue("datatypeLibrary")); v != "" {
			return v, name
		}
		if cur.Kind != xdm.KindElement {
			break
		}
	}
	return builtinLibrary, name
}

// nameClass reads the name class of an <element> or <attribute>.
//
// The name may be written as an attribute — name="foo" — or as a child
// element, which is the form that allows anyName and nsName.
func (c *compiler) nameClass(n *xdm.Node) (NameClass, error) {
	if v := n.AttrValue("name"); v != "" {
		// The attribute is declared xsd:QName in the schema for schemas, so
		// it is whitespace-normalised: name=" foo " names foo.
		return QName{Name: c.resolveName(n, normalizeToken(v))}, nil
	}
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		switch kid.Name.Local {
		case "name", "anyName", "nsName", "choice":
			return c.compileNameClass(kid)
		}
	}
	return nil, fmt.Errorf("relaxng: <%s> has no name class", n.Name.Local)
}

func (c *compiler) compileNameClass(n *xdm.Node) (NameClass, error) {
	switch n.Name.Local {
	case "name":
		return QName{Name: c.resolveName(n, normalizeToken(n.StringValue()))}, nil

	case "anyName":
		nc := AnyName{}
		if ex := exceptOf(n); ex != nil {
			e, err := c.compileNameClass(ex)
			if err != nil {
				return nil, err
			}
			nc.Except = e
		}
		return nc, nil

	case "nsName":
		nc := NsName{Ns: nsInForce(n)}
		if ex := exceptOf(n); ex != nil {
			e, err := c.compileNameClass(ex)
			if err != nil {
				return nil, err
			}
			nc.Except = e
		}
		return nc, nil

	case "choice":
		var out NameClass
		for _, kid := range n.ChildElements() {
			if kid.Name.URI != NS {
				continue
			}
			k, err := c.compileNameClass(kid)
			if err != nil {
				return nil, err
			}
			if out == nil {
				out = k
			} else {
				out = NameChoice{Left: out, Right: k}
			}
		}
		if out == nil {
			return nil, fmt.Errorf("relaxng: <choice> name class is empty")
		}
		return out, nil
	}
	return nil, fmt.Errorf("relaxng: <%s> is not a name class", n.Name.Local)
}

// exceptOf finds the <except> child of a name class, and returns the name
// class inside it.
func exceptOf(n *xdm.Node) *xdm.Node {
	for _, kid := range n.ChildElements() {
		if kid.Name.URI == NS && kid.Name.Local == "except" {
			for _, g := range kid.ChildElements() {
				if g.Name.URI == NS {
					return g
				}
			}
		}
	}
	return nil
}

// resolveName turns a lexical name into a QName using the ns= in force.
//
// RELAX NG does not use the document's namespace bindings for pattern names:
// ns= is an attribute inherited down the schema, and an unprefixed name means
// whatever ns= says rather than the default namespace. That is a deliberate
// difference from XSD, and reading it wrong makes every name match nothing.
func (c *compiler) resolveName(n *xdm.Node, lexical string) xdm.QName {
	if i := strings.IndexByte(lexical, ':'); i >= 0 {
		prefix, local := lexical[:i], lexical[i+1:]
		if uri, ok := n.InScopeNamespaces()[prefix]; ok {
			return xdm.QName{URI: uri, Local: local}
		}
		return xdm.QName{Local: local}
	}
	return xdm.QName{URI: nsInForce(n), Local: lexical}
}

// nsInForce reads the nearest ns= attribute on or above n.
func nsInForce(n *xdm.Node) string {
	for cur := n; cur != nil && cur.Kind == xdm.KindElement; cur = cur.Parent {
		for _, a := range cur.Attrs {
			if a.Name.URI == "" && a.Name.Local == "ns" {
				return a.Value
			}
		}
	}
	return ""
}

// agreedCombine applies §4.17 to the definitions of one name.
//
// A name may be defined more than once, which is how a schema extends one it
// did not write. The rule that makes that safe is that the definitions must
// agree: at most one may omit combine=, since that one is the base being
// extended, and every other must name the same method. Two plain definitions
// are a mistake rather than an extension — one of them would be silently lost.
func agreedCombine(what string, defs []*xdm.Node) (string, error) {
	if len(defs) < 2 {
		return "", nil
	}
	var how string
	var plain int
	for _, d := range defs {
		c := normalizeToken(d.AttrValue("combine"))
		if c == "" {
			plain++
			continue
		}
		if c != "choice" && c != "interleave" {
			return "", fmt.Errorf(
				"relaxng: %s has combine=%q, which is neither choice nor interleave",
				what, c)
		}
		if how == "" {
			how = c
		} else if how != c {
			return "", fmt.Errorf(
				"relaxng: %s is combined both by %s and by %s (section 4.17)",
				what, how, c)
		}
	}
	if plain > 1 {
		return "", fmt.Errorf(
			"relaxng: %s appears %d times without combine= (section 4.17)",
			what, plain)
	}
	if how == "" {
		return "", fmt.Errorf(
			"relaxng: %s appears more than once without combine= (section 4.17)",
			what)
	}
	return how, nil
}
