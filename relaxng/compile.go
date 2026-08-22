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
	return CompileWithOptions(doc, Options{})
}

// CompileWithOptions builds a schema, with a Resolver for <externalRef> and
// <include>.
//
// Compile is this with no options, which refuses both. Splitting them keeps
// the safe thing the short thing to write: reaching outside the schema
// document is something a caller opts into, not something a schema can decide
// for itself.
func CompileWithOptions(doc *xdm.Node, opts Options) (*Schema, error) {
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
		combined: map[string][]*xdm.Node{}, how: map[string]string{},
		opts: opts}
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
	// parent is the compiler of the enclosing <grammar>, which <parentRef>
	// names. A grammar nested inside another may refer outward to it, and
	// that is the only way the two scopes ever meet.
	parent *compiler
	// opts carries the Resolver and base URI.
	opts Options
	// includeDepth bounds a chain of includes, which may otherwise cycle.
	includeDepth int
	// expanding names the definitions currently being compiled, so that one
	// reaching itself becomes a lazy reference rather than an infinite
	// expansion.
	expanding map[string]bool
	// lazy holds one Ref per name, so that a definition reached twice yields
	// the same object and the walks over a compiled pattern can recognise it.
	lazy map[string]*Ref
	// elementDepth counts the <element> boundaries crossed so far, and
	// expandingAt records the depth at which each definition began. A
	// definition reaching itself at the same depth has not crossed one.
	elementDepth int
	expandingAt  map[string]int
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
			case "include":
				if err := c.collectInclude(kid, collect); err != nil {
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
	// Every definition is checked, not only the reachable ones: a <define>
	// naming a type that does not exist is broken whether or not anything
	// refers to it. Checking is not compiling, though — a definition may
	// legitimately refer to itself, and expanding one that nothing reaches
	// would not terminate.
	if err := c.checkUnreferenced(g); err != nil {
		return nil, err
	}
	return p, nil
}

// checkUnreferenced validates the parts of each definition that can be checked
// without expanding it.
//
// A <define> nothing refers to is still part of the schema, and the suite
// tests that a definition naming a datatype that does not exist is refused
// even when unreachable. Expanding it is the wrong way to find that out: a
// definition may refer to itself, which is legal and would not terminate.
// So the datatypes are resolved directly instead.
func (c *compiler) checkUnreferenced(n *xdm.Node) error {
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		switch kid.Name.Local {
		case "data", "value":
			dflt := ""
			if kid.Name.Local == "value" {
				dflt = "token"
			}
			lib, name := datatypeOf(kid, dflt)
			if name == "" {
				return fmt.Errorf("relaxng: <data> has no type")
			}
			dt, err := lookupDatatype(lib, name)
			if err != nil {
				return fmt.Errorf("relaxng: <%s>: %w", kid.Name.Local, err)
			}
			if kid.Name.Local == "data" {
				var params []Param
				for _, p := range kid.ChildElements() {
					if p.Name.URI == NS && p.Name.Local == "param" {
						params = append(params,
							Param{Name: p.AttrValue("name"), Value: p.StringValue()})
					}
				}
				if err := checkParams(dt, lib, name, params); err != nil {
					return fmt.Errorf("relaxng: <data>: %w", err)
				}
			}
		}
		if err := c.checkUnreferenced(kid); err != nil {
			return err
		}
	}
	return nil
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
		if kid.Name.URI != NS {
			continue
		}
		switch kid.Name.Local {
		case "param", "except", "name", "anyName", "nsName":
			continue
		case "choice":
			// A <choice> directly inside an <element> or <attribute> may be a
			// choice of *names* rather than of patterns — <element><choice>
			// <name>a</name><name>b</name></choice>...</element> names two
			// alternatives for one element. Treating it as a pattern loses
			// the name and then reports the choice as empty, since a name
			// class holds no patterns.
			if isNameClassChoice(kid) && (n.Name.Local == "element" ||
				n.Name.Local == "attribute") {
				continue
			}
		}
		out = append(out, kid)
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
		c.elementDepth++
		body, err := c.compileChildren(n)
		c.elementDepth--
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

	case "group", "div":
		// <div> groups for documentation and has no effect on the grammar,
		// so as a pattern it is simply its children.
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
			depth: c.depth, parent: c, opts: c.opts,
			includeDepth: c.includeDepth}
		return sub.compileGrammar(n)

	case "parentRef":
		return c.compileParentRef(n)

	case "externalRef":
		return c.compileExternalRef(n)

	case "include":
		// An <include> may only appear inside a <grammar>, where
		// compileGrammar handles it. Reaching one here means it was written
		// where a pattern belongs.
		return nil, fmt.Errorf(
			"relaxng: <include> is only allowed inside <grammar>")
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
	return c.compileRefNamed(normalizeToken(n.AttrValue("name")))
}

func (c *compiler) compileRefNamed(name string) (Pattern, error) {
	def, ok := c.defines[name]
	if !ok {
		return nil, fmt.Errorf("relaxng: <ref> names %q, which no <define> provides", name)
	}
	// A definition already being compiled is being reached recursively. That
	// is legal — a <bar> whose content may hold another <bar> is the ordinary
	// way to write a nested structure — and expanding it here would not
	// terminate, so it becomes a reference resolved on demand instead. The
	// recursion then unfolds once per level the document actually has.
	if c.expanding == nil {
		c.expanding = map[string]bool{}
	}
	if c.expanding[name] {
		// §4.19: a definition may reach itself only through an <element>. A
		// <bar> whose content may hold another <bar> describes arbitrarily
		// deep nesting, and each level consumes an element, so a document
		// ends the recursion. One that reaches itself without crossing an
		// element boundary describes nothing finite — there is no input that
		// would stop it — and is refused.
		if c.elementDepth <= c.expandingAt[name] {
			return nil, fmt.Errorf(
				"relaxng: definition %q refers to itself without an "+
					"intervening <element> (section 4.19)", name)
		}
		return c.lazyRef(name), nil
	}
	if c.depth >= maxRefDepth {
		return nil, fmt.Errorf(
			"relaxng: definition %q recurses more than %d deep", name, maxRefDepth)
	}
	if c.expandingAt == nil {
		c.expandingAt = map[string]int{}
	}
	c.expanding[name] = true
	c.expandingAt[name] = c.elementDepth
	c.depth++
	defer func() {
		c.depth--
		delete(c.expanding, name)
		delete(c.expandingAt, name)
	}()
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

// compileParentRef resolves a <parentRef>, which names a definition in the
// grammar enclosing this one.
//
// It exists because a nested <grammar> is a fresh scope: its definitions do
// not collide with the outer grammar's, which is what makes a grammar safe to
// nest. parentRef is the one door between the two.
func (c *compiler) compileParentRef(n *xdm.Node) (Pattern, error) {
	name := normalizeToken(n.AttrValue("name"))
	if c.parent == nil {
		return nil, fmt.Errorf(
			"relaxng: <parentRef name=%q> has no enclosing <grammar>", name)
	}
	if c.depth >= maxRefDepth {
		return nil, fmt.Errorf(
			"relaxng: definition %q recurses more than %d deep", name, maxRefDepth)
	}
	// The definition is compiled in the *parent's* scope, so that a <ref>
	// inside it resolves there rather than here.
	c.parent.depth = c.depth + 1
	defer func() { c.parent.depth = 0 }()
	return c.parent.compileRefNamed(name)
}

// fetch resolves an href through the configured Resolver.
func (c *compiler) fetch(n *xdm.Node) (*xdm.Node, string, error) {
	href, err := resolveHref(n, n.AttrValue("href"), c.opts.BaseURI)
	if err != nil {
		return nil, "", err
	}
	if c.opts.Resolver == nil {
		return nil, "", fmt.Errorf(
			"relaxng: <%s href=%q> needs a Resolver; none was configured",
			n.Name.Local, href)
	}
	if c.includeDepth >= maxIncludeDepth {
		return nil, "", fmt.Errorf(
			"relaxng: schemas include one another more than %d deep at %q",
			maxIncludeDepth, href)
	}
	doc, err := c.opts.Resolver.ResolveSchema(href)
	if err != nil {
		return nil, "", fmt.Errorf("relaxng: <%s href=%q>: %w",
			n.Name.Local, href, err)
	}
	if doc == nil {
		return nil, "", fmt.Errorf(
			"relaxng: <%s href=%q>: the resolver returned nothing",
			n.Name.Local, href)
	}
	return rootElement(doc), href, nil
}

// collectInclude merges the grammar an <include> names into this one.
//
// The subtlety is override: a <define> written inside the <include> element
// replaces the definition of that name in the included grammar rather than
// combining with it. That is what makes include useful — a schema adopts
// another and changes the parts it needs — and it is also why the included
// definitions cannot simply be collected first. They are filtered.
func (c *compiler) collectInclude(inc *xdm.Node, collect func(*xdm.Node) error) error {
	root, href, err := c.fetch(inc)
	if err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf(
			"relaxng: <include href=%q>: the document has no root element", href)
	}
	if root.Name.URI != NS || root.Name.Local != "grammar" {
		return fmt.Errorf(
			"relaxng: <include href=%q> names a <%s>, not a <grammar>",
			href, root.Name.Local)
	}
	if err := checkSyntax(root); err != nil {
		return err
	}

	// What the include overrides: the names it defines itself, and whether it
	// replaces <start>.
	overridden := map[string]bool{}
	var overridesStart bool
	var scanOverrides func(n *xdm.Node)
	scanOverrides = func(n *xdm.Node) {
		for _, kid := range n.ChildElements() {
			if kid.Name.URI != NS {
				continue
			}
			switch kid.Name.Local {
			case "define":
				overridden[normalizeToken(kid.AttrValue("name"))] = true
			case "start":
				overridesStart = true
			case "div":
				scanOverrides(kid)
			}
		}
	}
	scanOverrides(inc)

	// The included grammar's own definitions, less the overridden ones.
	filtered := *root
	filtered.Children = nil
	var keep func(n *xdm.Node) []*xdm.Node
	keep = func(n *xdm.Node) []*xdm.Node {
		var out []*xdm.Node
		for _, kid := range n.ChildElements() {
			if kid.Name.URI != NS {
				continue
			}
			switch kid.Name.Local {
			case "define":
				if overridden[normalizeToken(kid.AttrValue("name"))] {
					continue
				}
			case "start":
				if overridesStart {
					continue
				}
			case "div":
				out = append(out, keep(kid)...)
				continue
			}
			out = append(out, kid)
		}
		return out
	}
	filtered.Children = keep(root)

	// The included definitions are collected in a compiler whose base URI is
	// the included document's, so that an href inside it resolves there.
	was := c.opts.BaseURI
	wasDepth := c.includeDepth
	c.opts.BaseURI = href
	c.includeDepth++
	err = collect(&filtered)
	c.opts.BaseURI = was
	c.includeDepth = wasDepth
	if err != nil {
		return err
	}
	// Then the overriding definitions written inside the <include> itself.
	return collect(inc)
}

// compileExternalRef compiles the schema an <externalRef> names, as a pattern
// standing where the externalRef stands.
func (c *compiler) compileExternalRef(n *xdm.Node) (Pattern, error) {
	root, href, err := c.fetch(n)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf(
			"relaxng: <externalRef href=%q>: the document has no root element",
			href)
	}
	if err := checkSyntax(root); err != nil {
		return nil, err
	}
	if err := checkRestrictions(root); err != nil {
		return nil, err
	}
	// The referenced schema is a document of its own: its definitions are its
	// own, and an ns= written here does not reach into it.
	sub := &compiler{defines: map[string]*xdm.Node{},
		combined: map[string][]*xdm.Node{}, how: map[string]string{},
		opts:         Options{Resolver: c.opts.Resolver, BaseURI: href},
		includeDepth: c.includeDepth + 1, depth: c.depth}
	return sub.compileTop(root)
}

// rootElement returns the document element of a parsed schema.
func rootElement(doc *xdm.Node) *xdm.Node {
	if doc.Kind != xdm.KindDocument {
		return doc
	}
	for _, kid := range doc.Children {
		if kid.Kind == xdm.KindElement {
			return kid
		}
	}
	return nil
}

// lazyRef makes a reference that compiles the definition when it is first
// needed.
//
// One Ref is kept per name, and it is the same object every time. That matters
// as much as the laziness: the checks that walk a compiled pattern stop when
// they meet a reference they have already followed, and they recognise it by
// identity. A fresh Ref each time would defeat that and recurse forever.
//
// The compiler is captured rather than the pattern, because the definition
// cannot be compiled yet — it is the one being compiled, several frames up.
func (c *compiler) lazyRef(name string) Pattern {
	if c.lazy == nil {
		c.lazy = map[string]*Ref{}
	}
	if r, ok := c.lazy[name]; ok {
		return r
	}
	r := &Ref{name: name}
	c.lazy[name] = r
	r.resolve = func() (Pattern, error) {
		sub := &compiler{
			defines: c.defines, combined: c.combined, how: c.how,
			parent: c.parent, opts: c.opts, includeDepth: c.includeDepth,
			expanding: map[string]bool{}, expandingAt: map[string]int{},
			lazy: c.lazy,
		}
		return sub.compileRefNamed(name)
	}
	return r
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
			// Several patterns inside one <except> mean their choice: a data
			// excepting x, y and z admits nothing that matches any of them.
			// compileChildren would group them instead, which excludes only
			// the concatenation and so excludes nothing at all.
			ex, err := c.combine(kid, choice)
			if err != nil {
				return nil, err
			}
			d.Except = ex
		}
	}
	if err := checkParams(dt, lib, name, d.Params); err != nil {
		return nil, fmt.Errorf("relaxng: <data>: %w", err)
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
		if n.Name.Local == "attribute" {
			return QName{Name: c.resolveAttrNameAttr(n, normalizeToken(v))}, nil
		}
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
		e, err := c.exceptClass(n)
		if err != nil {
			return nil, err
		}
		nc.Except = e
		return nc, nil

	case "nsName":
		nc := NsName{Ns: nsInForce(n)}
		e, err := c.exceptClass(n)
		if err != nil {
			return nil, err
		}
		nc.Except = e
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
// classes inside it.
//
// An except may exclude several names at once, and they mean their choice:
// <anyName><except><name>a</name><name>b</name></except></anyName> is every
// name but those two.
func exceptOf(n *xdm.Node) []*xdm.Node {
	for _, kid := range n.ChildElements() {
		if kid.Name.URI == NS && kid.Name.Local == "except" {
			var out []*xdm.Node
			for _, g := range kid.ChildElements() {
				if g.Name.URI == NS {
					out = append(out, g)
				}
			}
			return out
		}
	}
	return nil
}

// exceptClass compiles the contents of a name-class <except> as one class.
func (c *compiler) exceptClass(n *xdm.Node) (NameClass, error) {
	kids := exceptOf(n)
	if len(kids) == 0 {
		return nil, nil
	}
	out, err := c.compileNameClass(kids[0])
	if err != nil {
		return nil, err
	}
	for _, kid := range kids[1:] {
		k, err := c.compileNameClass(kid)
		if err != nil {
			return nil, err
		}
		out = NameChoice{Left: out, Right: k}
	}
	return out, nil
}

// resolveAttrNameAttr resolves the name= attribute of an <attribute>.
//
// §4.10 treats the two spellings of an attribute's name differently, and the
// difference is not decoration. An unprefixed name written as name= is in no
// namespace unless that same <attribute> carries ns=; an inherited ns= does
// not reach it, because XML's own rule is that an unprefixed attribute is in
// no namespace however the surrounding document declares defaults.
//
// Written as a <name> child, the name is an ordinary name class and does
// inherit ns= from wherever it was declared. The suite tests both spellings
// against the same schema shape, which is how the asymmetry shows up at all.
func (c *compiler) resolveAttrNameAttr(n *xdm.Node, lexical string) xdm.QName {
	if strings.IndexByte(lexical, ':') >= 0 {
		return c.resolveName(n, lexical)
	}
	for _, a := range n.Attrs {
		if a.Name.URI == "" && a.Name.Local == "ns" {
			return xdm.QName{URI: a.Value, Local: lexical}
		}
	}
	return xdm.QName{Local: lexical}
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
