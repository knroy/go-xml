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
	// The schema being compiled is itself on the inclusion path from the
	// outset, when the caller said where it was read from. Without that, a
	// schema including the file it is written in would be read a second time
	// and only caught one level further down, naming the wrong href.
	if opts.BaseURI != "" {
		(*c.active())[opts.BaseURI] = true
	}
	p, err := c.compileTop(root)
	if err != nil {
		return nil, err
	}
	// Sections 7.3 and 7.4 are checked on the compiled pattern rather than
	// the syntax: they ask whether two name classes overlap, which is a
	// question about the classes, not about how they were written.
	if len(c.unbound) > 0 {
		return nil, fmt.Errorf(
			"relaxng: prefix %q is not declared in the schema", c.unbound[0])
	}
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
	// includeDepth counts the includes on the current chain. It is a
	// resource bound only — see maxIncludeDepth — and says nothing about
	// whether the chain is circular.
	includeDepth int
	// activeHrefs is the set of resolved hrefs currently being compiled: the
	// path from the schema being compiled down to here, not everything seen.
	// An href that is its own ancestor is a cycle; one reached twice by two
	// different routes is a diamond, which is legal, so entries are removed
	// on the way out rather than accumulated.
	//
	// It is a pointer because an <externalRef> compiles in a compiler of its
	// own, and the chain it is on is the same chain. Sharing the map is what
	// makes a cycle that passes through an externalRef visible.
	activeHrefs *map[string]bool
	// expanding names the definitions currently being compiled, so that one
	// reaching itself becomes a lazy reference rather than an infinite
	// expansion.
	expanding map[string]bool
	// lazy holds one refPat per name, so that a definition reached twice yields
	// the same object and the walks over a compiled pattern can recognise it.
	lazy map[string]*refPat
	// elementDepth counts the <element> boundaries crossed so far, and
	// expandingAt records the depth at which each definition began. A
	// definition reaching itself at the same depth has not crossed one.
	elementDepth int
	expandingAt  map[string]int
	// inheritedNs is the ns= in force where an <externalRef> or <include>
	// brought this schema in, used when the schema itself sets none.
	inheritedNs string
	// defineNs records the inherited ns for definitions that arrived through
	// an <include>, since they are collected while that ns is in force and
	// compiled later, when it is not.
	defineNs map[string]string
	// unbound collects prefixes used in a name that nothing declares. They
	// are reported once the schema is read, so that the message names the
	// prefix rather than the place.
	unbound []string
	// defines maps a name to the <define> that provides it, so that <ref>
	// resolves. A grammar is flat: nested <grammar> elements each have their
	// own scope, which parentRef reaches out of.
	defines map[string]*xdm.Node
	// depth counts the <ref> expansions on the current descent. It is
	// carried across a <parentRef> so that the enclosing grammar continues
	// this descent rather than starting a fresh one; nothing bounds it —
	// see maxRefDepth's removal note in compileRefNamed.
	depth int
}

// active returns the set of hrefs on the current inclusion path, creating it
// on first use.
//
// A compiler built directly by Compile has none, and one is made here rather
// than at every construction site so that the several places that build a
// sub-compiler cannot each forget it in their own way.
func (c *compiler) active() *map[string]bool {
	if c.activeHrefs == nil {
		m := map[string]bool{}
		c.activeHrefs = &m
	}
	return c.activeHrefs
}

// startKey is the key under which a <start>'s inherited namespace is kept. It
// cannot collide with a definition name, since a name is an NCName.
const startKey = "<start>"

func (c *compiler) compileTop(root *xdm.Node) (pattern, error) {
	if root.Name.Local == "grammar" {
		return c.compileGrammar(root)
	}
	return c.compilePattern(root)
}

// compileGrammar reads <grammar>, collecting its definitions before compiling
// <start>, so that a <ref> may name a definition that appears later.
func (c *compiler) compileGrammar(g *xdm.Node) (pattern, error) {
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
				if c.inheritedNs != "" {
					if c.defineNs == nil {
						c.defineNs = map[string]string{}
					}
					c.defineNs[startKey] = c.inheritedNs
				}
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
				if c.inheritedNs != "" {
					if c.defineNs == nil {
						c.defineNs = map[string]string{}
					}
					if _, seen := c.defineNs[name]; !seen {
						c.defineNs[name] = c.inheritedNs
					}
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
	if ns, ok := c.defineNs[startKey]; ok && c.inheritedNs == "" {
		was := c.inheritedNs
		c.inheritedNs = ns
		p0, err := c.compileChildren(start)
		c.inheritedNs = was
		if err != nil {
			return nil, err
		}
		return c.joinStarts(p0, startCombine)
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
	if err := c.checkAll(g); err != nil {
		return nil, err
	}
	return p, nil
}

// joinStarts combines the further <start> elements onto the first.
func (c *compiler) joinStarts(p pattern, how string) (pattern, error) {
	join := choice
	if how == "interleave" {
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

// checkAll runs the whole-grammar checks.
func (c *compiler) checkAll(g *xdm.Node) error {
	// Every definition is checked, not only the reachable ones: a <define>
	// naming a type that does not exist is broken whether or not anything
	// refers to it. Checking is not compiling, though — a definition may
	// legitimately refer to itself, and expanding one that nothing reaches
	// would not terminate.
	if err := c.checkUnreferenced(g); err != nil {
		return err
	}
	if err := checkNestedGrammars(g); err != nil {
		return err
	}
	return c.checkRefsResolve(g)
}

// checkNestedGrammars requires a <start> of every grammar written inside this
// one.
//
// The outer grammar's start is checked when it is compiled. A nested one may
// sit in a definition nothing refers to, so nothing would compile it — but a
// grammar with no start describes nothing, and is a mistake wherever it is
// written.
func checkNestedGrammars(n *xdm.Node) error {
	for _, kid := range n.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		if kid.Name.Local == "grammar" && !hasStart(kid) {
			return fmt.Errorf("relaxng: <grammar> has no <start>")
		}
		if err := checkNestedGrammars(kid); err != nil {
			return err
		}
	}
	return nil
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
			lib, name := datatypeOf(kid, "")
			if name == "" {
				if kid.Name.Local == "value" {
					// A <value> with no type is the built-in token, and the
					// library in force does not come into it.
					continue
				}
				return fmt.Errorf("relaxng: <data> has no type")
			}
			dt, err := lookupDatatype(lib, name)
			if err != nil {
				return fmt.Errorf("relaxng: <%s>: %w", kid.Name.Local, err)
			}
			if kid.Name.Local == "data" {
				var params []param
				for _, p := range kid.ChildElements() {
					if p.Name.URI == NS && p.Name.Local == "param" {
						params = append(params,
							param{Name: p.AttrValue("name"), Value: p.StringValue()})
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

// checkRefsResolve reports a <ref> that names nothing, wherever it stands.
//
// A definition nothing refers to is still part of the schema, so a reference
// inside it to a name that does not exist is an error. Compiling the
// definition to find that out is not an option — it may refer to itself — so
// the names are checked directly.
//
// A nested <grammar> is a scope of its own, so its refs are checked against
// its own definitions, and a <parentRef> inside it against this one.
func (c *compiler) checkRefsResolve(g *xdm.Node) error {
	var walk func(n *xdm.Node, defs map[string]bool, outer map[string]bool) error
	walk = func(n *xdm.Node, defs, outer map[string]bool) error {
		for _, kid := range n.ChildElements() {
			if kid.Name.URI != NS {
				continue
			}
			switch kid.Name.Local {
			case "ref":
				name := normalizeToken(kid.AttrValue("name"))
				if !defs[name] {
					return fmt.Errorf(
						"relaxng: <ref> names %q, which no <define> provides",
						name)
				}
			case "parentRef":
				name := normalizeToken(kid.AttrValue("name"))
				if outer == nil {
					return fmt.Errorf(
						"relaxng: <parentRef name=%q> has no enclosing <grammar>",
						name)
				}
				if !outer[name] {
					return fmt.Errorf(
						"relaxng: <parentRef> names %q, which the enclosing "+
							"<grammar> does not define", name)
				}
			case "grammar":
				inner := definedNames(kid)
				if err := walk(kid, inner, defs); err != nil {
					return err
				}
				continue
			}
			if err := walk(kid, defs, outer); err != nil {
				return err
			}
		}
		return nil
	}
	defs := map[string]bool{}
	for name := range c.defines {
		defs[name] = true
	}
	var outer map[string]bool
	if c.parent != nil {
		outer = map[string]bool{}
		for name := range c.parent.defines {
			outer[name] = true
		}
	}
	return walk(g, defs, outer)
}

// hasStart reports whether a grammar provides a <start>.
func hasStart(g *xdm.Node) bool {
	for _, kid := range g.ChildElements() {
		if kid.Name.URI != NS {
			continue
		}
		if kid.Name.Local == "start" {
			return true
		}
		if kid.Name.Local == "div" && hasStart(kid) {
			return true
		}
	}
	return false
}

// definedNames collects the names a <grammar> defines, including through
// <div> and <include>.
func definedNames(g *xdm.Node) map[string]bool {
	out := map[string]bool{}
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		for _, kid := range n.ChildElements() {
			if kid.Name.URI != NS {
				continue
			}
			switch kid.Name.Local {
			case "define":
				out[normalizeToken(kid.AttrValue("name"))] = true
			case "div", "include":
				walk(kid)
			}
		}
	}
	walk(g)
	return out
}

// compileChildren compiles an element's pattern children as a group.
//
// Several RELAX NG elements take a sequence of patterns and mean their group:
// <start>, <define>, <element> and <group> all do. Writing that once is what
// keeps the individual cases short.
func (c *compiler) compileChildren(n *xdm.Node) (pattern, error) {
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

func (c *compiler) compilePattern(n *xdm.Node) (pattern, error) {
	if n.Name.URI != NS {
		return nil, fmt.Errorf("relaxng: {%s}%s is not a RELAX NG pattern",
			n.Name.URI, n.Name.Local)
	}
	switch n.Name.Local {
	case "empty":
		return emptyPat{}, nil
	case "notAllowed":
		return notAllowedPat{}, nil
	case "text":
		return textPat{}, nil

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
		return elementPat{Name: nc, Pattern: body}, nil

	case "attribute":
		nc, err := c.nameClass(n)
		if err != nil {
			return nil, err
		}
		kids := patternChildren(n)
		var body pattern = textPat{}
		if len(kids) > 0 {
			body, err = c.compileChildren(n)
			if err != nil {
				return nil, err
			}
		}
		return attributePat{Name: nc, Pattern: body}, nil

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
		return choice(p, emptyPat{}), nil

	case "zeroOrMore":
		p, err := c.compileChildren(n)
		if err != nil {
			return nil, err
		}
		return choice(oneOrMore(p), emptyPat{}), nil

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
		return interleave(textPat{}, p), nil

	case "list":
		p, err := c.compileChildren(n)
		if err != nil {
			return nil, err
		}
		return listPat{Pattern: p}, nil

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
			includeDepth: c.includeDepth, activeHrefs: c.active()}
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

func (c *compiler) combine(n *xdm.Node, f func(a, b pattern) pattern) (pattern, error) {
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

func (c *compiler) compileRef(n *xdm.Node) (pattern, error) {
	return c.compileRefNamed(normalizeToken(n.AttrValue("name")))
}

func (c *compiler) compileRefNamed(name string) (pattern, error) {
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
	// No bound on c.depth. There was one — maxRefDepth = 500 — and because
	// c.expanding above already catches every re-entry into a definition
	// still being compiled, the count could only ever fire on a chain that
	// was NOT recursive: 501 distinct definitions each <ref>ing the next,
	// an entirely acyclic and perfectly legal grammar, was refused with
	// "definition \"D500\" recurses more than 500 deep". Nor did it bound
	// runtime recursion, which unfolds through lazyRef — that builds a
	// fresh compiler with depth 0 each time.
	if c.expandingAt == nil {
		c.expandingAt = map[string]int{}
	}
	if ns, ok := c.defineNs[name]; ok && c.inheritedNs == "" {
		was := c.inheritedNs
		c.inheritedNs = ns
		defer func() { c.inheritedNs = was }()
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
func (c *compiler) compileParentRef(n *xdm.Node) (pattern, error) {
	name := normalizeToken(n.AttrValue("name"))
	if c.parent == nil {
		return nil, fmt.Errorf(
			"relaxng: <parentRef name=%q> has no enclosing <grammar>", name)
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
	// Cycle first, because it is the semantic answer: a schema that includes
	// itself is defective however shallow the chain is, and reporting it as a
	// depth overrun would both name the wrong href and take 40 fetches to do
	// it. The key is the href resolved against the base in force, so that two
	// spellings of one document — "sub/../a.rng" and "a.rng" — are one entry.
	if c.activeHrefs != nil && (*c.activeHrefs)[href] {
		return nil, "", fmt.Errorf(
			"relaxng: circular schema inclusion: %q includes itself", href)
	}
	// Then the resource bound, which is a refusal to spend more rather than a
	// statement about the schema.
	if c.includeDepth >= maxIncludeDepth {
		return nil, "", fmt.Errorf(
			"relaxng: resource limit exceeded: schemas nest more than %d "+
				"includes deep at %q", maxIncludeDepth, href)
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
	// An included grammar is a schema document like any other, and section 7
	// applies to it. Checking only its syntax would let a construct the
	// restrictions forbid — an <attribute> inside an <attribute>, a <text>
	// inside a <list> — reach the deriver, which assumes it has already been
	// refused. The top-level and <externalRef> paths both check it here.
	if err := checkRestrictions(root); err != nil {
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

	// §4.7: an override must have something to override. A <define> inside an
	// <include> that names nothing in the included grammar is a mistake — most
	// often a typo — and treating it as an addition would silently leave the
	// definition the author meant to replace in force.
	included := definedNames(root)
	for name := range overridden {
		if !included[name] {
			return fmt.Errorf(
				"relaxng: <include href=%q> overrides %q, which it does not define",
				href, name)
		}
	}
	if overridesStart && !hasStart(root) {
		return fmt.Errorf(
			"relaxng: <include href=%q> overrides <start>, which it does not define",
			href)
	}

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
	wasNs := c.inheritedNs
	c.opts.BaseURI = href
	c.includeDepth++
	// href joins the active path for exactly as long as it is being compiled,
	// and leaves it again below. A set that only ever grew would refuse a
	// diamond — two schemas including one third — which is legal.
	active := c.active()
	(*active)[href] = true
	// The ns= written on the <include> reaches the definitions it brings in,
	// the same way it reaches an <externalRef>'s schema.
	c.inheritedNs = inheritedNs(inc, c.inheritedNs)
	err = collect(&filtered)
	delete(*active, href)
	c.opts.BaseURI = was
	c.includeDepth = wasDepth
	c.inheritedNs = wasNs
	if err != nil {
		return err
	}
	// Then the overriding definitions written inside the <include> itself.
	return collect(inc)
}

// compileExternalRef compiles the schema an <externalRef> names, as a pattern
// standing where the externalRef stands.
func (c *compiler) compileExternalRef(n *xdm.Node) (pattern, error) {
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
	// own. But the ns= in force where the reference is written *does* reach
	// into it, when the referenced schema does not set one itself — that is
	// how a schema is written once and used in several namespaces.
	sub := &compiler{defines: map[string]*xdm.Node{},
		combined: map[string][]*xdm.Node{}, how: map[string]string{},
		opts:         Options{Resolver: c.opts.Resolver, BaseURI: href},
		includeDepth: c.includeDepth + 1, depth: c.depth,
		activeHrefs: c.active(),
		inheritedNs: inheritedNs(n, c.inheritedNs)}
	// The referenced schema is on the active path while it compiles, and off
	// it again afterwards, so that a second externalRef to the same schema
	// from a sibling is not mistaken for a cycle.
	(*sub.activeHrefs)[href] = true
	defer delete(*sub.activeHrefs, href)
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
// One refPat is kept per name, and it is the same object every time. That matters
// as much as the laziness: the checks that walk a compiled pattern stop when
// they meet a reference they have already followed, and they recognise it by
// identity. A fresh refPat each time would defeat that and recurse forever.
//
// The compiler is captured rather than the pattern, because the definition
// cannot be compiled yet — it is the one being compiled, several frames up.
func (c *compiler) lazyRef(name string) pattern {
	if c.lazy == nil {
		c.lazy = map[string]*refPat{}
	}
	if r, ok := c.lazy[name]; ok {
		return r
	}
	r := &refPat{name: name}
	c.lazy[name] = r
	r.resolve = func() (pattern, error) {
		sub := &compiler{
			defines: c.defines, combined: c.combined, how: c.how,
			parent: c.parent, opts: c.opts, includeDepth: c.includeDepth,
			activeHrefs: c.activeHrefs,
			expanding:   map[string]bool{}, expandingAt: map[string]int{},
			lazy: c.lazy,
		}
		return sub.compileRefNamed(name)
	}
	return r
}

func (c *compiler) compileValue(n *xdm.Node) (pattern, error) {
	lib, name := datatypeOf(n, "")
	if name == "" {
		// §4.16: a <value> with no type= is the built-in token, whatever
		// datatypeLibrary happens to be in force. The library only names
		// where a *named* type comes from, so a schema that sets one and then
		// writes a plain <value> is not asking for anything from it.
		lib, name = builtinLibrary, "token"
	}
	dt, err := lookupDatatype(lib, name)
	if err != nil {
		return nil, fmt.Errorf("relaxng: <value>: %w", err)
	}
	// The schema's own prefixes travel with the value: a qnamePat written here
	// means what this document's bindings say, and the instance's bindings
	// are a different set entirely.
	return valuePat{
		Type:     dt,
		Value:    n.StringValue(),
		Ns:       c.nsFor(n),
		Prefixes: n.InScopeNamespaces(),
	}, nil
}

func (c *compiler) compileData(n *xdm.Node) (pattern, error) {
	lib, name := datatypeOf(n, "")
	if name == "" {
		return nil, fmt.Errorf("relaxng: <data> has no type")
	}
	dt, err := lookupDatatype(lib, name)
	if err != nil {
		return nil, fmt.Errorf("relaxng: <data>: %w", err)
	}
	d := dataPat{Type: dt}
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
			d.Params = append(d.Params, param{Name: pn, Value: kid.StringValue()})
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
func (c *compiler) nameClass(n *xdm.Node) (nameClass, error) {
	if v := n.AttrValue("name"); v != "" {
		// The attribute is declared xsd:qnamePat in the schema for schemas, so
		// it is whitespace-normalised: name=" foo " names foo.
		if n.Name.Local == "attribute" {
			return qnamePat{Name: c.resolveAttrNameAttr(n, normalizeToken(v))}, nil
		}
		return qnamePat{Name: c.resolveName(n, normalizeToken(v))}, nil
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

func (c *compiler) compileNameClass(n *xdm.Node) (nameClass, error) {
	switch n.Name.Local {
	case "name":
		return qnamePat{Name: c.resolveName(n, normalizeToken(n.StringValue()))}, nil

	case "anyName":
		nc := anyNamePat{}
		e, err := c.exceptClass(n)
		if err != nil {
			return nil, err
		}
		nc.Except = e
		return nc, nil

	case "nsName":
		nc := nsNamePat{Ns: c.nsFor(n)}
		e, err := c.exceptClass(n)
		if err != nil {
			return nil, err
		}
		nc.Except = e
		return nc, nil

	case "choice":
		var out nameClass
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
				out = nameChoicePat{Left: out, Right: k}
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
func (c *compiler) exceptClass(n *xdm.Node) (nameClass, error) {
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
		out = nameChoicePat{Left: out, Right: k}
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

// resolveName turns a lexical name into a qnamePat using the ns= in force.
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
		// The xml prefix is bound whether or not anything declares it — XML
		// Namespaces binds it by fiat — so xml:lang names the attribute it
		// always names, in a schema that never mentions the binding.
		if prefix == "xml" {
			return xdm.QName{URI: xdm.NSXML, Local: local}
		}
		// An unbound prefix names nothing. Dropping it and keeping the local
		// name would silently match elements in no namespace, which is not
		// what foo:bar was written to mean.
		c.unbound = append(c.unbound, prefix)
		return xdm.QName{Local: local}
	}
	return xdm.QName{URI: c.nsFor(n), Local: lexical}
}

// inheritedNs is the ns= that a referenced schema inherits: the one written on
// the reference itself, or failing that the one in force around it.
func inheritedNs(n *xdm.Node, outer string) string {
	for _, a := range n.Attrs {
		if a.Name.URI == "" && a.Name.Local == "ns" {
			return a.Value
		}
	}
	if ns := nsInForce(n); ns != "" {
		return ns
	}
	return outer
}

// nsFor is the ns= that applies at n, falling back to the one this schema
// inherited from the reference that brought it in.
//
// The fallback is what lets a schema be written once and used in several
// namespaces: <externalRef ns="..."/> supplies a namespace to a document that
// names none of its own.
func (c *compiler) nsFor(n *xdm.Node) string {
	if ns := nsInForce(n); ns != "" {
		return ns
	}
	return c.inheritedNs
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
