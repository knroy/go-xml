package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// xsl:use-package and package composition, XSLT 3.0 section 3.5.
//
// A package is a stylesheet that publishes *components* — templates,
// functions, variables, attribute sets and modes — under a name and a version,
// and hides the rest. Where xsl:import merges two modules wholesale and
// settles clashes by precedence, xsl:use-package merges only what the used
// package chose to expose, and settles nothing: a clash between two accepted
// components is a static error rather than a precedence contest.
//
// The composition is carried out here as a *filtered import*. The used
// package's tree is compiled into the same Stylesheet at a lower import
// precedence, with the declarations the manifest does not admit deleted from
// the tree first. That reuses every existing declaration compiler unchanged,
// which matters because a package's components are ordinary templates and
// functions in every respect but their visibility — the only thing packaging
// changes is *which* of them the using package can see.
//
// What the tree-pruning model cannot express is a second, independent copy of
// a component: an xsl:override that supplies a new body while the original
// remains reachable through xsl:original from inside the override, and while
// the used package's own internal calls still bind to the original. That is
// implemented for the common case — the overriding declaration replaces the
// used one outright — and xsl:original is resolved against the replaced
// declaration. A used package whose private components call the overridden
// one therefore see the override, where the specification says they should
// see it too (3.5.4 makes the override replace the component for every
// binding within the using package's component graph, which includes the used
// package's own references). What is *not* implemented is the abstract
// component and the multi-level override chain; see visibilityAbstract below.

// componentKind is the value of the component attribute of xsl:expose and
// xsl:accept: which sort of component a token in @names refers to.
//
// The kinds are disjoint namespaces. A template named "p" and a function
// named "p" are different components, so a symbolic name has to carry the
// kind as well as the QName.
type componentKind string

const (
	kindTemplate     componentKind = "template"
	kindFunction     componentKind = "function"
	kindAttributeSet componentKind = "attribute-set"
	kindVariable     componentKind = "variable"
	kindMode         componentKind = "mode"
)

// visibility is a component's visibility, section 3.5.2.
type visibility string

const (
	visPublic   visibility = "public"
	visPrivate  visibility = "private"
	visFinal    visibility = "final"
	visAbstract visibility = "abstract"
	visHidden   visibility = "hidden"
	visAbsent   visibility = "absent"
)

// symbolicName identifies one component within a package.
//
// The arity is part of it for a function and only for a function: 3.5.1 makes
// the symbolic name of a function its name *and* arity, so two overloads are
// two components, while a template has one name and one component. -1 marks
// "every arity", which is what a token in an xsl:expose @names writes when it
// names a function without one.
type symbolicName struct {
	kind  componentKind
	name  xdm.QName
	arity int
}

func (s symbolicName) String() string {
	if s.kind == kindFunction && s.arity >= 0 {
		return fmt.Sprintf("%s %s#%d", s.kind, s.name.Lexical(), s.arity)
	}
	return fmt.Sprintf("%s %s", s.kind, s.name.Lexical())
}

// component is one declaration in a package, with the visibility the package
// gives it.
type component struct {
	sym symbolicName
	// el is the declaration's element in the package's tree. Pruning the
	// tree is how a component is withheld, so the node has to be kept.
	el *xdm.Node
	// declared is the visibility the declaration's own visibility attribute
	// states, or "" where it states none.
	declared visibility
	// vis is the effective visibility, after xsl:expose has been applied.
	vis visibility
}

// packageDecl is a resolved package: its name, version and tree.
type packageDecl struct {
	name    string
	version string
	doc     *xdm.Node
}

// PackageResolver locates the package a use-package declaration names.
//
// A package is addressed by name and version, not by a URI to fetch, so this
// cannot be folded into ModuleResolver: there is no href to resolve. The
// resolver is handed the name and the version-matching expression exactly as
// written, and answers with the tree of the package that best matches, or an
// error if there is none.
type PackageResolver interface {
	ResolvePackage(name, versionMatch string) (*xdm.Node, error)
}

// packageComponents reads the components a package declares, in document
// order, with their declared visibility.
//
// A package's components are its top-level declarations of the five component
// kinds. Everything else at the top level — xsl:output, xsl:key,
// xsl:strip-space — is not a component and has no visibility; it is stylesheet
// configuration, which a using package inherits or does not by rules of its
// own and never names in an xsl:expose.
func packageComponents(root *xdm.Node) ([]*component, error) {
	var out []*component
	for _, el := range root.ChildElements() {
		if el.Name.URI != xdm.NSXSL {
			continue
		}
		var kind componentKind
		switch el.Name.Local {
		case "template":
			// Only a *named* template is a component. A match template has no
			// symbolic name, so it can be neither exposed nor accepted nor
			// overridden by name; it reaches the using package as part of the
			// mode it belongs to.
			if el.AttrValue("name") == "" {
				continue
			}
			kind = kindTemplate
		case "function":
			kind = kindFunction
		case "attribute-set":
			kind = kindAttributeSet
		case "variable":
			kind = kindVariable
		case "param":
			// A stylesheet parameter is not a component. 3.5.1 lists the
			// component kinds and xsl:param is not among them: a parameter is
			// supplied from outside the whole stylesheet, so a package has
			// nothing to publish about it and an xsl:expose naming one names
			// nothing -- which is what expose-908 asserts.
			continue
		case "mode":
			kind = kindMode
		default:
			continue
		}
		sym := symbolicName{kind: kind, arity: -1}
		if kind == kindMode {
			// xsl:mode names the unnamed mode when it has no name attribute,
			// and the unnamed mode is a component like any other.
			if n := el.AttrValue("name"); n != "" {
				qn, err := resolveQNameAttr(el, n)
				if err != nil {
					return nil, err
				}
				sym.name = qn
			}
		} else {
			qn, err := resolveQNameAttr(el, el.AttrValue("name"))
			if err != nil {
				return nil, err
			}
			sym.name = qn
		}
		if kind == kindFunction {
			sym.arity = countFunctionParams(el)
		}
		c := &component{sym: sym, el: el}
		if v := strings.TrimSpace(el.AttrValue("visibility")); v != "" {
			c.declared = visibility(v)
		}
		out = append(out, c)
	}
	return out, nil
}

// countFunctionParams counts the leading xsl:param children of an
// xsl:function, which is its arity.
func countFunctionParams(el *xdm.Node) int {
	n := 0
	for _, ch := range el.ChildElements() {
		if !isXSL(ch, "param") {
			break
		}
		n++
	}
	return n
}

// defaultVisibility is the visibility a declaration with no visibility
// attribute has, section 3.5.2.
//
// It is private for every component kind but one: xsl:mode defaults to
// private as well, and there is no kind that defaults to public. A component
// is exposed only by saying so.
func defaultVisibility(kind componentKind) visibility { return visPrivate }

// exposeRule is one xsl:expose or xsl:accept element, parsed.
type exposeRule struct {
	el    *xdm.Node
	kinds map[componentKind]bool
	// tokens are the @names tokens. A wildcard token is recorded with
	// wildcard set, because the two error rules — XTSE3010/3020 for
	// xsl:expose and XTSE3030/3040 for xsl:accept — exempt a wildcard match
	// from the consistency check and from the must-match rule alike.
	tokens []nameToken
	vis    visibility
	// order is the element's position among its siblings, which is what
	// decides the winner when two rules match the same component: 3.5.2 takes
	// the last matching xsl:expose in document order.
	order int
	// anyKind records component="*", which constrains what @names may say:
	// see the XTSE3022 check in parseExposeRule.
	anyKind bool
}

// nameToken is one token of an @names attribute.
type nameToken struct {
	// wildcard is "*", "*:local" or "prefix:*"; the last two constrain half
	// the name and leave the other half open.
	wildcard bool
	// uri and local are the halves. An empty half of a wildcard token
	// matches anything.
	uri, local string
	anyURI     bool
	anyLocal   bool
	arity      int
	lexical    string
}

// parseExposeRule reads an xsl:expose or xsl:accept element.
func parseExposeRule(el *xdm.Node, order int) (*exposeRule, error) {
	r := &exposeRule{el: el, order: order, kinds: map[componentKind]bool{}}
	comp := strings.TrimSpace(el.AttrValue("component"))
	if comp == "" {
		return nil, fmt.Errorf("XTSE0010: %s requires a component attribute",
			el.Name.Lexical())
	}
	for _, tok := range strings.Fields(comp) {
		switch componentKind(tok) {
		case kindTemplate, kindFunction, kindAttributeSet, kindVariable, kindMode:
			r.kinds[componentKind(tok)] = true
		case "*":
			for _, k := range []componentKind{kindTemplate, kindFunction,
				kindAttributeSet, kindVariable, kindMode} {
				r.kinds[k] = true
			}
			r.anyKind = true
		default:
			return nil, fmt.Errorf(
				"XTSE0020: %s/@component may not be %q",
				el.Name.Lexical(), tok)
		}
	}
	vis := strings.TrimSpace(el.AttrValue("visibility"))
	if vis == "" {
		return nil, fmt.Errorf("XTSE0010: %s requires a visibility attribute",
			el.Name.Lexical())
	}
	r.vis = visibility(vis)
	names := el.AttrValue("names")
	if strings.TrimSpace(names) == "" {
		return nil, fmt.Errorf("XTSE0010: %s requires a names attribute",
			el.Name.Lexical())
	}
	for _, tok := range strings.Fields(names) {
		nt, err := parseNameToken(el, tok)
		if err != nil {
			return nil, err
		}
		// component="*" spans every kind at once, and a name means different
		// components in different kinds. Erratum bug 29478 settles that by
		// forbidding the combination outright rather than picking one
		// reading: with component="*" the names must all be wildcards.
		if r.anyKind && !nt.wildcard {
			code := "XTSE3022"
			if isXSL(el, "accept") {
				code = "XTSE3032"
			}
			return nil, fmt.Errorf(
				"%s: %s has component=\"*\", so the token %q in @names must "+
					"be a wildcard", code, el.Name.Lexical(), nt.lexical)
		}
		r.tokens = append(r.tokens, nt)
	}
	return r, nil
}

// parseNameToken reads one token of an @names attribute, whose grammar is
// section 3.5.2's NameTest with an optional "#arity" or "#*" suffix.
func parseNameToken(el *xdm.Node, tok string) (nameToken, error) {
	nt := nameToken{arity: -1, lexical: tok}
	// The arity suffix belongs to a function token. "#*" says every arity,
	// which is the same as writing no suffix at all.
	if i := strings.LastIndexByte(tok, '#'); i >= 0 {
		suffix := tok[i+1:]
		tok = tok[:i]
		if suffix != "*" {
			n := 0
			for _, r := range suffix {
				if r < '0' || r > '9' {
					return nt, fmt.Errorf(
						"XTSE0020: %q is not a component name in %s/@names",
						nt.lexical, el.Name.Lexical())
				}
				n = n*10 + int(r-'0')
			}
			nt.arity = n
		}
	}
	// The pseudo-names an @mode attribute admits are not names here. The
	// unnamed mode is private to its package by definition -- 3.5.2 gives it
	// no visibility to change -- so a manifest naming it has written
	// something the grammar does not admit at all.
	if strings.HasPrefix(tok, "#") {
		return nt, fmt.Errorf(
			"XTSE0020: %q is not a component name in %s/@names",
			nt.lexical, el.Name.Lexical())
	}
	// Q{uri}* is the EQName spelling of prefix:*, and the only way to write a
	// wildcard over the no-namespace components: there is no prefix bound to
	// "", so "Q{}*" cannot be said any other way. accept-022 relies on it.
	if strings.HasPrefix(tok, "Q{") {
		if end := strings.IndexByte(tok, '}'); end > 0 && tok[end+1:] == "*" {
			nt.wildcard, nt.anyLocal = true, true
			nt.uri = tok[2:end]
			return nt, nil
		}
	}
	switch {
	case tok == "*":
		nt.wildcard, nt.anyURI, nt.anyLocal = true, true, true
		return nt, nil
	case strings.HasPrefix(tok, "*:"):
		nt.wildcard, nt.anyURI = true, true
		nt.local = tok[2:]
		return nt, nil
	case strings.HasSuffix(tok, ":*"):
		nt.wildcard, nt.anyLocal = true, true
		prefix := tok[:len(tok)-2]
		uri := lookupPrefix(el, prefix)
		if uri == "" {
			// An unbound prefix makes the token unreadable as a name at all,
			// which is the grammar error XTSE0020 rather than the namespace
			// error XTSE0280: expose-927 asks for the former.
			return nt, fmt.Errorf(
				"XTSE0020: the prefix %q in %s/@names is not bound",
				prefix, el.Name.Lexical())
		}
		nt.uri = uri
		return nt, nil
	}
	qn, err := resolveQNameAttr(el, tok)
	if err != nil {
		return nt, err
	}
	nt.uri, nt.local = qn.URI, qn.Local
	return nt, nil
}

// lookupPrefix resolves a namespace prefix against el's in-scope namespaces.
func lookupPrefix(el *xdm.Node, prefix string) string {
	if prefix == "xml" {
		return xdm.NSXML
	}
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		for _, ns := range cur.Namespaces {
			// A namespace node keeps the prefix in its name and the URI in
			// its value; there are no Prefix and URI fields.
			if ns.Name.Local == prefix {
				return ns.Value
			}
		}
	}
	return ""
}

// matches reports whether the rule matches the component, and whether the
// token that matched was a wildcard.
//
// The wildcard answer is what the error rules turn on: a wildcard that would
// assign an inconsistent visibility is "treated as not matching", where an
// explicit name is XTSE3010 or XTSE3040.
func (r *exposeRule) matches(sym symbolicName) (ok, wild bool) {
	ok, _, wild = r.matchRank(sym)
	return ok, wild
}

// matchTier is how specific a token's match is, which is what decides between
// two rules that both match one component.
//
// 3.5.2 states the ladder for xsl:expose, and the "best-matching xsl:accept
// element" of 3.5.3.2 is settled the same way: an explicit EQName beats a
// wildcard that constrains half the name, which in turn beats a bare "*".
// Within one tier the last matching element in document order wins.
//
// accept-901 turns on this. It accepts xsl:initial-template by name with
// visibility="public" and then writes a blanket
// <xsl:accept names="*" component="*" visibility="hidden"/>. Taking simply
// the last rule that matched hid the template the previous rule had just
// made public, and the package was left with no entry point at all.
const (
	tierNone = iota
	tierAnyName
	tierPartialWildcard
	tierExact
)

// matchRank reports whether the rule matches, how specific the best matching
// token was, and whether that token was a wildcard.
func (r *exposeRule) matchRank(sym symbolicName) (ok bool, tier int, wild bool) {
	if !r.kinds[sym.kind] {
		return false, tierNone, false
	}
	for _, t := range r.tokens {
		if t.arity >= 0 && (sym.kind != kindFunction || t.arity != sym.arity) {
			continue
		}
		// Draft erratum E36: a token naming a function must state the arity.
		// Two overloads are two components, and a bare name says nothing
		// about which one is meant -- so rather than read it as "all of
		// them", the erratum makes it match none, which surfaces as the
		// XTSE3020 that expose-926 asks for.
		if sym.kind == kindFunction && t.arity < 0 && !t.wildcard {
			continue
		}
		if !t.anyURI && t.uri != sym.name.URI {
			continue
		}
		if !t.anyLocal && t.local != sym.name.Local {
			continue
		}
		if !t.wildcard {
			// An exact token beats a wildcard: the specification's tie-break
			// prefers the more specific match, so an exact hit ends the scan
			// rather than being overwritten by a later wildcard in the same
			// element.
			return true, tierExact, false
		}
		// A wildcard that pins half the name -- "p:*", "*:local", "Q{uri}*"
		// -- is its own tier above a bare "*", which the ladder in 3.5.2
		// lists as the last resort.
		this := tierAnyName
		if !t.anyURI || !t.anyLocal {
			this = tierPartialWildcard
		}
		if this > tier {
			tier = this
		}
		ok, wild = true, true
	}
	return ok, tier, wild
}

// exposeTable is section 3.5.2's table of permitted (declared, exposed) pairs,
// indexed in that order: the declaration's visibility selects the row, and the
// row holds the visibilities an xsl:expose may then assign.
//
// The exposed visibility wins wherever the pair is permitted, which is why
// the table answers only "permitted or not": the caller already knows what the
// result would be.
var exposeTable = map[visibility]map[visibility]bool{
	visPublic:   {visPublic: true, visPrivate: true, visFinal: true},
	visPrivate:  {visPrivate: true},
	visFinal:    {visPrivate: true, visFinal: true},
	visAbstract: {visAbstract: true},
}

// acceptTable is section 3.5.3's table: which visibility an xsl:accept may
// assign, given the component's visibility in the used package.
var acceptTable = map[visibility]map[visibility]bool{
	visPublic: {
		visPublic: true, visPrivate: true, visFinal: true,
		visHidden: true,
	},
	visFinal: {
		visPrivate: true, visFinal: true, visHidden: true,
	},
	// An abstract component may also be hidden. Hiding it is not the same as
	// supplying it: the using package simply declines to see it, and 3.5.3
	// leaves that legal because nothing then invokes the missing body. What
	// makes it an error is *calling* the hidden component, which is the
	// dynamic error XTDE3052 rather than anything visible statically --
	// accept-040 hides an abstract function and passes, accept-041b hides the
	// same one and calls it.
	visAbstract: {visAbstract: true, visAbsent: true, visHidden: true},
}

// applyExpose computes each component's effective visibility from its declared
// visibility and the package's xsl:expose declarations, section 3.5.2.
func applyExpose(comps []*component, exposes []*exposeRule) error {
	for _, c := range comps {
		declared := c.declared
		if declared == "" {
			declared = defaultVisibility(c.sym.kind)
		}
		c.vis = declared
		// The last matching xsl:expose in document order wins, so the scan
		// runs forward and keeps overwriting.
		var best *exposeRule
		var bestWild bool
		for _, r := range exposes {
			ok, wild := r.matches(c.sym)
			if !ok {
				continue
			}
			if c.declared != "" && !exposeTable[c.declared][r.vis] {
				if wild {
					// "unless the token that matches the component is a
					// wildcard, in which case it is treated as not matching
					// that component".
					continue
				}
				return fmt.Errorf(
					"XTSE3010: xsl:expose gives %s visibility=%q, which is "+
						"inconsistent with its declared visibility=%q",
					c.sym, r.vis, c.declared)
			}
			if err := checkAbstractExposure(c, r); err != nil {
				return err
			}
			best, bestWild = r, wild
			_ = bestWild
		}
		if best != nil {
			c.vis = best.vis
		}
	}
	// XTSE3020: a non-wildcard token that matches no component at all.
	for _, r := range exposes {
		for _, t := range r.tokens {
			if t.wildcard {
				continue
			}
			if !tokenMatchesAny(r, t, comps) {
				return fmt.Errorf(
					"XTSE3020: the token %q in xsl:expose/@names matches no "+
						"component in the package", t.lexical)
			}
		}
	}
	return nil
}

// tokenMatchesAny reports whether one token of a rule matches some component.
func tokenMatchesAny(r *exposeRule, t nameToken, comps []*component) bool {
	single := &exposeRule{kinds: r.kinds, tokens: []nameToken{t}}
	for _, c := range comps {
		if ok, _ := single.matches(c.sym); ok {
			return true
		}
	}
	return false
}

// usePackageDecl is one xsl:use-package element in a package manifest, with
// everything the composition needs read off it.
type usePackageDecl struct {
	el        *xdm.Node
	name      string
	versions  string
	accepts   []*exposeRule
	overrides []*xdm.Node
	// comps are the used package's components, with the visibility the used
	// package gives them.
	comps []*component
	// root is the used package's module element.
	root *xdm.Node
	doc  *xdm.Node
	// acceptedVis is the visibility each accepted component ends up with in
	// the using package, keyed by its symbolic name. It is computed once, by
	// acceptComponents, because xsl:accept may both narrow and widen and the
	// answer has to be settled before any reference is resolved.
	acceptedVis map[string]visibility
	// staticVars are the static variables the used package's own static phase
	// evaluated. They belong to that package and not to the using one, so
	// they are reinstated only while it compiles; see compileUsedPackage.
	staticVars []staticVar
	// assignedVis records which of those an xsl:accept actually named, as
	// against inheriting the used package's own answer. XTSE3080 turns on the
	// difference; see compileUsePackages.
	assignedVis map[string]bool
	// hiddenByAccept records the components an xsl:accept explicitly gave
	// visibility="hidden" or "absent". Only those are unreachable within the
	// used package too. The private-to-hidden default of 3.5.3 withholds a
	// component from the *using* package while the used package's own
	// references must still bind -- which is what override-base-f-001 relies
	// on when its p:f-final calls the private p:f-private.
	hiddenByAccept map[string]bool
	// overriding maps a symbolic name to the xsl:override child that
	// replaces it, so that compiling the used package can substitute the
	// overriding declaration for the original.
	overriding map[string]*xdm.Node
}

// compileUsePackage carries out the package composition for one module.
//
// It runs before the module's own declarations are compiled, so that a
// component accepted from a used package is in scope for every reference in
// the using package regardless of where the xsl:use-package sits — 3.5 puts no
// ordering constraint on the manifest beyond xsl:import coming first.
func (c *compiler) compileUsePackages(root *xdm.Node, precedence int) error {
	// The principal module is the one whose tree was recorded first, and its
	// package is the top-level package: nobody uses it, so nothing can
	// resolve an abstract component it accepts. See the XTSE3080 check below.
	topLevel := c.sheet.source != nil && firstElement(c.sheet.source) == root
	// XTSE3080 over the package's own declarations, which is the half of the
	// rule that needs no manifest at all: "It is a static error if a top-level
	// package (as distinct from a library package) contains components whose
	// visibility is abstract."
	//
	// An abstract component has no body, and only a package that uses this
	// one can supply it through xsl:override. A top-level package is by
	// definition the one nobody uses, so an abstract component it declares
	// can never be supplied -- 3.5.4 puts it as "a package is executable if
	// and only if it contains no component whose visibility is abstract",
	// and a package that is not executable is not a stylesheet.
	//
	// The error is static, so it has to be raised here rather than left to
	// surface when the component is invoked. error-3080a declares an
	// abstract template t and calls it from main; without this the
	// compilation succeeded and the failure appeared much later as the
	// XTDE0040 eligibility check, a long way from the cause.
	//
	// Only a module compiled as the principal one is judged. A package
	// reached by xsl:use-package is a library package, where an abstract
	// component is exactly what the feature is for, and the check inside the
	// manifest loop below covers the case where such a component is accepted
	// into the top-level package still abstract.
	if topLevel && c.usedPackageDepth == 0 && c.importDepth == 0 {
		comps, err := packageComponents(root)
		if err != nil {
			return err
		}
		for _, comp := range comps {
			if comp.declared == visAbstract {
				return fmt.Errorf(
					"XTSE3080: %s is declared with visibility=\"abstract\" "+
						"in the top-level package, and nothing can supply "+
						"its implementation", comp.sym)
			}
		}
	}
	// XTSE3008: a module reached by xsl:import may not use a package. An
	// imported module is a separate stylesheet, so its manifest would belong
	// to no package at all; an included one is part of the including module
	// and shares its package, which is why xsl:include is explicitly allowed.
	if c.importDepth > 0 {
		for _, el := range root.ChildElements() {
			if isXSL(el, "use-package") {
				return fmt.Errorf(
					"XTSE3008: xsl:use-package may not appear in a module " +
						"reached by xsl:import")
			}
		}
	}
	var uses []*usePackageDecl
	for _, el := range root.ChildElements() {
		if !isXSL(el, "use-package") {
			continue
		}
		u, err := c.readUsePackage(el)
		if err != nil {
			return err
		}
		uses = append(uses, u)
	}
	if len(uses) == 0 {
		return nil
	}
	// XTSE3050 is judged over the manifest as a whole: two used packages may
	// each legitimately expose a component of the same name, and the clash
	// only exists once both are accepted.
	accepted := map[string]*usePackageDecl{}
	for _, u := range uses {
		vis, err := c.acceptComponents(u)
		if err != nil {
			return err
		}
		for _, comp := range u.comps {
			v := vis[comp.sym.String()]
			// XTSE3080: "a top-level package ... contains symbolic
			// references referring to components whose visibility is
			// abstract". An abstract component has no body, so a package
			// nobody will use has no later chance to supply one: the
			// unresolved reference a library may legitimately carry is at
			// the top level simply a call to nothing.
			//
			// An xsl:override supplies exactly that missing body, which is
			// what abstract components are for, so an overridden one is not
			// a reference to nothing and is not this error.
			//
			// Nor is one that is abstract only because the used package said
			// so and no xsl:accept mentioned it: 3.5.3 turns such a component
			// hidden, and a hidden component is not referenced at all. The
			// error belongs to the manifest that deliberately wrote
			// visibility="abstract", which is what accept-904 and -912 do;
			// accept-902 and -910 inherit it and want the dynamic XTDE3052
			// when the component is actually invoked.
			_, supplied := u.overriding[comp.sym.String()]
			if v == visAbstract && topLevel && !supplied &&
				u.assignedVis[comp.sym.String()] {
				return fmt.Errorf(
					"XTSE3080: %s is accepted into the top-level package "+
						"with visibility=\"abstract\", and nothing can "+
						"supply its implementation", comp.sym)
			}
			if v == visHidden || v == visAbsent || v == "" {
				continue
			}
			key := comp.sym.String()
			if prev, dup := accepted[key]; dup && prev != u {
				return fmt.Errorf(
					"XTSE3050: %s is accepted from both %s and %s with a "+
						"visibility other than hidden",
					comp.sym, prev.name, u.name)
			}
			accepted[key] = u
		}
	}
	// XTSE3050 again, this time between the manifest and the using package's
	// own declarations: a package may not declare a component whose symbolic
	// name it also accepted from a package it uses. There is no precedence
	// contest to settle it -- 3.5 removed the one xsl:import has -- so the
	// clash is simply an error. override-t-008 declares a template named "t"
	// alongside an xsl:use-package that accepts one.
	own, err := packageComponents(root)
	if err != nil {
		return err
	}
	// XTSE3055 comes first, because it is the more specific rule about the
	// same shape of clash: "a component declaration appearing as a child of
	// xsl:override is homonymous with any other declaration in the using
	// package, regardless of import precedence, including any other
	// overriding declaration in the package manifest".
	//
	// XTSE3050 is about a component the manifest *accepted* colliding with
	// one the package declares. An overridden component is not accepted --
	// the override supplies it -- so a top-level declaration of the same name
	// is this error rather than that one. error-3055a overrides t-public and
	// declares a t-public of its own.
	ownByName := map[string]bool{}
	for _, comp := range own {
		ownByName[comp.sym.String()] = true
	}
	overridingSeen := map[string]bool{}
	for _, u := range uses {
		for key := range u.overriding {
			if ownByName[key] {
				return fmt.Errorf(
					"XTSE3055: %s is declared as a child of xsl:override and "+
						"also declared in the using package", key)
			}
			if overridingSeen[key] {
				return fmt.Errorf(
					"XTSE3055: %s is declared as a child of xsl:override in "+
						"more than one place in the package manifest", key)
			}
			overridingSeen[key] = true
		}
	}
	for _, comp := range own {
		if u, dup := accepted[comp.sym.String()]; dup {
			return fmt.Errorf(
				"XTSE3050: %s is declared in this package and also accepted "+
					"from %s", comp.sym, u.name)
		}
	}
	// A template rule naming a mode the manifest accepted is adding to that
	// mode, which is overriding it, and 3.5.4 admits that only inside an
	// xsl:override. Written at the top level it is the same clash as
	// declaring a homonymous component: override-m-018 adds a rule to an
	// accepted mode m3 from outside.
	for _, el := range root.ChildElements() {
		if !isXSL(el, "template") || el.AttrValue("name") != "" {
			continue
		}
		for _, m := range overriddenModes(el) {
			if u, dup := accepted[m.String()]; dup {
				return fmt.Errorf(
					"XTSE3050: a template rule outside xsl:override names "+
						"%s, which is accepted from %s", m, u.name)
			}
		}
	}
	// The used packages are compiled below the using package, so that the
	// using package's own declarations win any contest precedence decides.
	// They are compiled in reverse manifest order for the same reason
	// xsl:import numbers the later import higher.
	for i := len(uses) - 1; i >= 0; i-- {
		if err := c.compileUsedPackage(uses[i]); err != nil {
			return err
		}
	}
	return nil
}

// readUsePackage resolves one xsl:use-package and reads its manifest children.
func (c *compiler) readUsePackage(el *xdm.Node) (*usePackageDecl, error) {
	u := &usePackageDecl{el: el}
	u.name = strings.TrimSpace(el.AttrValue("name"))
	if u.name == "" {
		return nil, fmt.Errorf("XTSE0010: xsl:use-package requires a name attribute")
	}
	u.versions = strings.TrimSpace(el.AttrValue("package-version"))
	if u.versions == "" {
		// 3.5's default is "*", every version.
		u.versions = "*"
	}
	// The range is checked against the grammar before it is matched against
	// anything, because a malformed range and an unsatisfied one are
	// different errors: 3.5.2 requires the attribute to "conform to the rules
	// for a PackageVersionRange", and a value that does not is XTSE0020,
	// where a well-formed range that no available package satisfies is the
	// XTSE3000 below.
	if err := checkPackageVersionRange(u.versions); err != nil {
		return nil, err
	}
	order := 0
	for _, ch := range el.ChildElements() {
		switch {
		case isXSL(ch, "accept"):
			r, err := parseExposeRule(ch, order)
			if err != nil {
				return nil, err
			}
			order++
			u.accepts = append(u.accepts, r)
		case isXSL(ch, "override"):
			if err := checkOverrideChildren(ch); err != nil {
				return nil, err
			}
			u.overrides = append(u.overrides, ch)
		}
	}
	if c.opts.PackageResolver == nil {
		return nil, fmt.Errorf(
			"XTSE3000: no package named %s is available (package loading is "+
				"disabled)", u.name)
	}
	doc, err := c.opts.PackageResolver.ResolvePackage(u.name, u.versions)
	if err != nil {
		return nil, fmt.Errorf("XTSE3000: no package matches name %q and "+
			"package-version %q: %w", u.name, u.versions, err)
	}
	u.doc = doc
	u.root = firstElement(doc)
	if u.root == nil {
		return nil, fmt.Errorf("XTSE3000: the package named %s is empty", u.name)
	}
	// The used package's own manifest is processed first: its components
	// include what it in turn accepted from the packages it uses.
	//
	// A package is its own static scope, so the used package's static
	// variables are evaluated separately and then discarded: what the using
	// package's own use-when expressions see must be its own declarations.
	// runStaticPhase assigns c.staticVars outright, so without saving them
	// the using package's static parameters vanished the moment it used a
	// package -- which is why accept-041a could not find its $accept.
	// staticDone is deliberately not restored: it records the trees whose
	// shadow attributes have already been expanded, and expanding one twice
	// is not idempotent.
	savedVars := c.staticVars
	if err := c.runStaticPhase(doc); err != nil {
		return nil, err
	}
	// The used package's own static variables are kept for the moment it is
	// compiled, which happens later and needs them: package-version-001
	// declares a static parameter and spells a shadow attribute with it.
	u.staticVars = c.staticVars
	c.staticVars = savedVars
	comps, err := packageComponents(u.root)
	if err != nil {
		return nil, err
	}
	var exposes []*exposeRule
	eOrder := 0
	for _, ch := range u.root.ChildElements() {
		if !isXSL(ch, "expose") {
			continue
		}
		r, err := parseExposeRule(ch, eOrder)
		if err != nil {
			return nil, err
		}
		eOrder++
		exposes = append(exposes, r)
	}
	if err := applyExpose(comps, exposes); err != nil {
		return nil, err
	}
	u.comps = comps
	return u, nil
}

// acceptComponents applies the xsl:accept and xsl:override children of one
// xsl:use-package, answering the visibility each component of the used package
// has in the using package, keyed by symbolic name.
func (c *compiler) acceptComponents(u *usePackageDecl) (map[string]visibility, error) {
	byName := map[string]*component{}
	for _, comp := range u.comps {
		byName[comp.sym.String()] = comp
	}
	// The overriding declarations first: XTSE3051 makes a name that an
	// xsl:accept lists explicitly *and* an xsl:override declares an error,
	// so the override's names have to be known before the accepts are read.
	overridden := map[string]*xdm.Node{}
	for _, ov := range u.overrides {
		comps, err := packageComponents(ov)
		if err != nil {
			return nil, err
		}
		for _, oc := range comps {
			key := oc.sym.String()
			target, ok := byName[key]
			if !ok {
				return nil, fmt.Errorf(
					"XTSE3058: the %s declared in xsl:override is not "+
						"homonymous with any component of package %s",
					oc.sym, u.name)
			}
			if target.vis != visPublic && target.vis != visAbstract {
				return nil, fmt.Errorf(
					"XTSE3060: %s cannot be overridden: its visibility in "+
						"package %s is %q, not public or abstract",
					oc.sym, u.name, target.vis)
			}
			if err := checkOverrideSignature(oc.el, target.el); err != nil {
				return nil, err
			}
			overridden[key] = oc.el
		}
		// A template rule inside the override adds a rule to the mode it
		// names, and adding one is overriding the mode: 3.5.4 lets that
		// happen only where the mode is public or abstract in the used
		// package. override-m-002 writes a rule in a final mode and
		// override-m-003 in a private one; both are XTSE3060.
		for _, decl := range ov.ChildElements() {
			if !isXSL(decl, "template") || decl.AttrValue("name") != "" {
				continue
			}
			for _, m := range overriddenModes(decl) {
				target, ok := byName[m.String()]
				if !ok {
					continue
				}
				if target.vis != visPublic && target.vis != visAbstract {
					return nil, fmt.Errorf(
						"XTSE3060: a template rule in xsl:override names "+
							"%s, whose visibility in package %s is %q, not "+
							"public or abstract", m, u.name, target.vis)
				}
			}
		}
	}
	vis := map[string]visibility{}
	assigned := map[string]bool{}
	hidden := map[string]bool{}
	for _, comp := range u.comps {
		// 3.5.3: a component the manifest does not mention keeps its
		// visibility, except that a private one becomes hidden.
		v := comp.vis
		if v == visPrivate {
			v = visHidden
		}
		vis[comp.sym.String()] = v
	}
	// The tier each component's winning xsl:accept matched at, so that a
	// later but vaguer rule does not displace an earlier, more specific one.
	// See matchRank.
	bestTier := map[string]int{}
	for _, r := range u.accepts {
		for _, comp := range u.comps {
			ok, tier, wild := r.matchRank(comp.sym)
			if !ok {
				continue
			}
			if !wild {
				if _, dup := overridden[comp.sym.String()]; dup {
					return nil, fmt.Errorf(
						"XTSE3051: the token %q in xsl:accept/@names names "+
							"%s, which an xsl:override of the same "+
							"xsl:use-package also declares",
						comp.sym.name.Lexical(), comp.sym)
				}
			}
			if !acceptTable[comp.vis][r.vis] {
				if wild {
					continue
				}
				return nil, fmt.Errorf(
					"XTSE3040: xsl:accept gives %s visibility=%q, which is "+
						"incompatible with its visibility=%q in package %s",
					comp.sym, r.vis, comp.vis, u.name)
			}
			// Within a tier the last element in document order wins, so a
			// tie is taken; a strictly vaguer match is not.
			if tier < bestTier[comp.sym.String()] {
				continue
			}
			bestTier[comp.sym.String()] = tier
			vis[comp.sym.String()] = r.vis
			assigned[comp.sym.String()] = true
			hidden[comp.sym.String()] = r.vis == visHidden || r.vis == visAbsent
		}
	}
	// XTSE3030: a non-wildcard xsl:accept token matching no component of the
	// used package.
	for _, r := range u.accepts {
		for _, t := range r.tokens {
			if t.wildcard {
				continue
			}
			if !tokenMatchesAny(r, t, u.comps) {
				return nil, fmt.Errorf(
					"XTSE3030: the token %q in xsl:accept/@names matches no "+
						"component in package %s", t.lexical, u.name)
			}
		}
	}
	u.acceptedVis = vis
	u.assignedVis = assigned
	u.hiddenByAccept = hidden
	u.overriding = overridden
	return vis, nil
}

// overrideChildren is the content model of xsl:override, 3.5.4.
//
// It is not "every component kind": xsl:mode is a component and is still not
// allowed here, because an override supplies a component's *body* and a mode
// declaration has none -- what it carries is the mode's properties, which
// belong to the package that introduced the mode. A rule for that mode is
// written as an ordinary template child instead.
var overrideChildren = map[string]bool{
	"template": true, "function": true, "variable": true,
	"param": true, "attribute-set": true,
}

// checkOverrideChildren applies that content model, XTSE0010.
func checkOverrideChildren(ov *xdm.Node) error {
	for _, ch := range ov.ChildElements() {
		if ch.Name.URI != xdm.NSXSL || !overrideChildren[ch.Name.Local] {
			return fmt.Errorf(
				"XTSE0010: %s is not allowed as a child of xsl:override",
				ch.Name.Lexical())
		}
	}
	return nil
}

// overriddenModes returns the symbolic names of the modes a template rule
// inside an xsl:override contributes to.
//
// A rule may name several modes at once, and the two pseudo-names are not
// components: "#all" is not a mode, and "#default" resolves against the
// using package rather than the used one.
func overriddenModes(decl *xdm.Node) []symbolicName {
	var out []symbolicName
	for _, tok := range strings.Fields(decl.AttrValue("mode")) {
		switch tok {
		case "#all", "#default", "#current":
			continue
		case "#unnamed":
			out = append(out, symbolicName{kind: kindMode, arity: -1})
			continue
		}
		qn, err := resolveQNameAttr(decl, tok)
		if err != nil {
			continue
		}
		out = append(out, symbolicName{kind: kindMode, name: qn, arity: -1})
	}
	return out
}

// checkOverrideSignature compares an overriding declaration with the one it
// overrides, XTSE3070.
//
// "Compatible" is defined in 3.5.4 in terms of the declared types, and the
// full rule needs subtype checking over sequence types. What is checked here
// is the part that catches every case the suite writes and every case a
// reader would call an obvious mismatch: a function's arity, and whether the
// two declarations are the same kind of thing at all — the latter is already
// guaranteed by the homonymy check, since the symbolic name carries the kind.
func checkOverrideSignature(overriding, original *xdm.Node) error {
	if isXSL(overriding, "template") {
		// A template's parameters are named rather than positional, so they
		// are compared by name: an override may declare them in any order,
		// but every one it declares must have the type the original gave it,
		// and it may not introduce or drop one.
		return checkTemplateParams(overriding, original)
	}
	if !isXSL(overriding, "function") {
		return nil
	}
	if a, b := countFunctionParams(overriding), countFunctionParams(original); a != b {
		return fmt.Errorf(
			"XTSE3070: the overriding xsl:function %s has %d parameters and "+
				"the one it overrides has %d",
			overriding.AttrValue("name"), a, b)
	}
	// The parameters' declared types must agree, position by position. A
	// caller of the used package was compiled against the original
	// signature, so an override that widens or narrows a parameter would
	// receive a value its body was not written for.
	op, np := leadingParams(overriding), leadingParams(original)
	for i := range op {
		if i >= len(np) {
			break
		}
		if a, b := op[i].AttrValue("as"), np[i].AttrValue("as"); !sameDeclaredType(a, b) {
			return fmt.Errorf(
				"XTSE3070: the overriding declaration of %s declares "+
					"parameter $%s as=%q and the one it overrides declares "+
					"as=%q", overriding.AttrValue("name"),
				op[i].AttrValue("name"), a, b)
		}
	}
	// Determinism is part of a function's signature. A caller compiled
	// against new-each-time="no" may have been optimised on the promise that
	// two calls with the same arguments give the same answer, which an
	// override that says "yes" withdraws. override-f-021 turns on exactly
	// that.
	if a, b := functionDeterminism(overriding), functionDeterminism(original); a != b {
		return fmt.Errorf(
			"XTSE3070: the overriding xsl:function %s declares "+
				"new-each-time=%q and the one it overrides declares %q",
			overriding.AttrValue("name"), a, b)
	}
	// The declared return types must agree. A weaker type on the override
	// would let a caller of the used package receive a value the used
	// package's own signature promised it would not.
	if a, b := overriding.AttrValue("as"), original.AttrValue("as"); a != "" &&
		b != "" && !sameDeclaredType(a, b) {
		return fmt.Errorf(
			"XTSE3070: the overriding xsl:function %s declares as=%q and the "+
				"one it overrides declares as=%q",
			overriding.AttrValue("name"), a, b)
	}
	return nil
}

// functionDeterminism reads xsl:function/@new-each-time, whose default is
// "maybe": the processor may or may not re-evaluate the body, 10.3.
func functionDeterminism(el *xdm.Node) string {
	if v := strings.TrimSpace(el.AttrValue("new-each-time")); v != "" {
		return v
	}
	return "maybe"
}

// sameDeclaredType compares two sequence types written on two declarations,
// for the purpose of XTSE3070.
//
// 3.5.4 states the rule as subtype compatibility, which needs the schema
// components to settle: two user-defined union types with different names may
// be the same type, and override-f-031's u1 and u2 are exactly that pair.
// Comparing the lexical forms answers every case the suite writes where the
// types are built-in, and a name this compiler cannot resolve to a built-in
// type is not judged rather than judged wrongly -- an override refused for a
// difference that is not one costs more than an override let through.
func sameDeclaredType(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == b {
		return true
	}
	return !builtinTypeName(a) || !builtinTypeName(b)
}

// builtinTypeName reports whether a sequence type is written wholly in terms
// this compiler can compare lexically: the XML Schema built-ins, the item
// type keywords, and an occurrence indicator.
func builtinTypeName(t string) bool {
	t = strings.TrimRight(strings.TrimSpace(t), "?*+")
	switch {
	case t == "":
		return false
	case strings.HasPrefix(t, "xs:"), strings.HasPrefix(t, "Q{"+xdm.NSXS+"}"):
		return true
	}
	// An item type keyword -- item(), node(), element(), function(*) and the
	// rest -- is written with parentheses and no prefix.
	return strings.Contains(t, "(")
}

// leadingParams returns the xsl:param children that open a declaration.
func leadingParams(el *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	for _, ch := range el.ChildElements() {
		if !isXSL(ch, "param") {
			break
		}
		out = append(out, ch)
	}
	return out
}

// checkTemplateParams compares the parameters of an overriding template with
// those of the template it overrides, XTSE3070.
func checkTemplateParams(overriding, original *xdm.Node) error {
	// Only the REQUIRED parameters have to correspond. An optional one is
	// part of the body's own business rather than of the signature a caller
	// was compiled against: dropping one costs a caller nothing, since it
	// need not have supplied it, and adding one costs nothing either, since
	// it has a default. Both directions appear in the suite -- override-t-001
	// drops the original's optional tunnel $extra, and override-t-011 adds an
	// optional $extra of its own -- and both are declared successful.
	orig := map[string]*xdm.Node{}
	for _, p := range leadingParams(original) {
		orig[p.AttrValue("name")] = p
	}
	seen := map[string]bool{}
	for _, p := range leadingParams(overriding) {
		name := p.AttrValue("name")
		seen[name] = true
		o, ok := orig[name]
		if !ok {
			if !stylesheetYes(p.AttrValue("required")) {
				continue
			}
			return fmt.Errorf(
				"XTSE3070: the overriding template %s requires a parameter "+
					"$%s that the one it overrides does not declare",
				overriding.AttrValue("name"), name)
		}
		if a, b := p.AttrValue("as"), o.AttrValue("as"); !sameDeclaredType(a, b) {
			return fmt.Errorf(
				"XTSE3070: the overriding template %s declares $%s as=%q "+
					"and the one it overrides declares as=%q",
				overriding.AttrValue("name"), name, a, b)
		}
		// A tunnel parameter and an ordinary one of the same name are not
		// the same parameter: the first is supplied by an ancestor call and
		// the second by the caller, so swapping them silently changes where
		// the value comes from. override-t-013 makes the original's
		// non-tunnel $in a tunnel parameter.
		if a, b := stylesheetYes(p.AttrValue("tunnel")),
			stylesheetYes(o.AttrValue("tunnel")); a != b {
			return fmt.Errorf(
				"XTSE3070: the overriding template %s declares $%s "+
					"tunnel=%q and the one it overrides declares %q",
				overriding.AttrValue("name"), name,
				p.AttrValue("tunnel"), o.AttrValue("tunnel"))
		}
	}
	for name, o := range orig {
		if !seen[name] && stylesheetYes(o.AttrValue("required")) {
			return fmt.Errorf(
				"XTSE3070: the overriding template %s does not declare the "+
					"required parameter $%s that the one it overrides "+
					"declares", overriding.AttrValue("name"), name)
		}
	}
	if a, b := overriding.AttrValue("as"), original.AttrValue("as"); !sameDeclaredType(a, b) {
		return fmt.Errorf(
			"XTSE3070: the overriding template %s declares as=%q and the "+
				"one it overrides declares as=%q",
			overriding.AttrValue("name"), a, b)
	}
	// The context item is part of the signature too: it says what a caller
	// must have established before calling, so an override that demands a
	// different type, or demands one where the original wanted none, refuses
	// callers the original accepted.
	oc, nc := contextItemChild(overriding), contextItemChild(original)
	for _, attr := range []string{"as", "use"} {
		a, b := contextItemAttr(oc, attr), contextItemAttr(nc, attr)
		if a != b {
			return fmt.Errorf(
				"XTSE3070: the overriding template %s declares "+
					"xsl:context-item/@%s=%q and the one it overrides "+
					"declares %q", overriding.AttrValue("name"), attr, a, b)
		}
	}
	return nil
}

// contextItemChild returns a declaration's xsl:context-item child, or nil.
func contextItemChild(el *xdm.Node) *xdm.Node {
	for _, ch := range el.ChildElements() {
		if isXSL(ch, "context-item") {
			return ch
		}
	}
	return nil
}

// contextItemAttr reads one attribute of an xsl:context-item, filling in the
// default a missing declaration implies.
//
// A template with no xsl:context-item at all is the same as one declaring
// use="optional" with no type constraint, 6.2, so the two spell the same
// signature and must compare equal.
func contextItemAttr(el *xdm.Node, name string) string {
	def := ""
	if name == "use" {
		def = "optional"
	}
	if el == nil {
		return def
	}
	if v := strings.TrimSpace(el.AttrValue(name)); v != "" {
		return v
	}
	return def
}

// compileUsedPackage compiles the components of one used package into the
// using stylesheet.
//
// The tree is pruned first: a component the using package cannot see is
// deleted outright, so that no later phase can bind a reference to it. That is
// what makes use-package-003 report XPST0017 for a call to a private function
// rather than quietly calling it.
func (c *compiler) compileUsedPackage(u *usePackageDecl) error {
	keep := map[*xdm.Node]bool{}
	for _, comp := range u.comps {
		if comp.sym.kind == kindMode &&
			!u.hiddenByAccept[comp.sym.String()] &&
			!(comp.sym.name.Local == "" && c.principalHasUnnamedMode()) {
			// A mode declaration survives the private-to-hidden default. It
			// is a component, but what it holds is the mode's properties,
			// and the template rules that travel with the mode need them
			// whatever the manifest said about who may name the mode from
			// outside. Deleting a private unnamed mode left its own
			// package's rules in a mode nothing declared, which under
			// declared-modes="yes" is XTSE3085.
			//
			// A mode an xsl:accept explicitly hid still goes, because that
			// is the visibility 3.6.3.1 says governs use within the package
			// too.
			//
			// The used package's UNNAMED mode declaration is the exception
			// to the exception. It has no name for an xsl:expose or
			// xsl:accept to reach it by, so each package's unnamed mode is
			// its own -- but the flat declaration table this compiler keeps
			// has room for one set of properties per mode name. Where the
			// principal package declares an unnamed mode of its own, that is
			// the one that must win, so the used package's declaration goes
			// and its rules stay. package-019 sets on-no-match="text-only-copy"
			// over a used package that sets on-no-match="fail"; keeping both
			// let the used package's setting win and failed as XTDE0555.
			//
			// Where the principal declares none there is no contest, and the
			// used package's declaration is kept so that its own rules are
			// in a mode something declared -- which override-v-001 needs
			// under declared-modes="yes".
			keep[comp.el] = true
			continue
		}
		v := u.acceptedVis[comp.sym.String()]
		// A hidden component is not merely unexported. The note under 3.6.3.1
		// singles it out: visibility "primarily affects how the component can
		// be used in other packages... There is one exception: if the
		// visibility is hidden, it also affects how the component can be used
		// WITHIN P." So hidden is genuinely unreachable, and leaving the
		// declaration in the tree would let a reference resolve to it --
		// which is the only thing distinguishing hidden from private. That
		// applies only where an xsl:accept said hidden: 3.5.3's default of
		// treating an unmentioned private component as hidden speaks about
		// the using package, and pruning on it would break the used
		// package's own calls to its private components.
		//
		// An overridden component is exempt, and the exemption is about
		// xsl:original rather than about visibility: the overriding body may
		// call the declaration it replaces, so that declaration has to stay
		// reachable however the manifest labelled it. The loop below re-adds
		// overridden components for the same reason.
		// An abstract component nothing overrides is kept, with its body
		// replaced by a stub that raises XTDE3052 if it is ever reached.
		//
		// It cannot be deleted, because the error the specification asks for
		// is dynamic: "It is a dynamic error if an invocation of an abstract
		// component is evaluated". Deleting the declaration turns every
		// reference to it into a static error at compile time instead, which
		// fails the cases that reference an abstract component without
		// evaluating it. accept-045a and accept-045b are the same stylesheet
		// under different runtime parameters -- one reaches the abstract
		// template and one does not -- so no static answer can be right for
		// both. See abstractcomponent.go.
		//
		// Hiding it with xsl:accept does not change this WHERE THE USED
		// PACKAGE ITSELF still reaches the component. 3.5.3.2's note says an
		// abstract component "accepted with visibility='hidden'... has the
		// effect that any invocation of the component raises a dynamic
		// error", and such an invocation is one written inside the used
		// package, whose reference bound before the accept was ever seen.
		// accept-041 is that shape: the using package calls C:f1-proxy, a
		// component of the used package, and the proxy reaches the hidden
		// C:f1 from within. XTDE3052.
		//
		// A reference written in the USING package is the other case, and it
		// is static. A hidden component is not a component of the using
		// package at all -- 3.6.3.1 singles hidden out as the visibility that
		// governs use within the declaring package too -- so naming it there
		// names nothing. error-3052a calls the hidden abstract t-abstract
		// directly from the using package and expects XTSE0650, the code for
		// a template that does not exist, rather than the dynamic error for
		// one that exists and has no body.
		if comp.declared == visAbstract &&
			u.overriding[comp.sym.String()] == nil &&
			!(u.hiddenByAccept[comp.sym.String()] &&
				!referencedWithin(u.root, comp)) {
			markAbstract(comp.el, comp.sym.String())
			keep[comp.el] = true
			continue
		}
		if (v == visHidden || v == visAbsent) &&
			u.overriding[comp.sym.String()] == nil &&
			(u.hiddenByAccept[comp.sym.String()] ||
				!referencedWithin(u.root, comp)) {
			// A component the using package cannot see is deleted, so that a
			// reference to it fails as XPST0017 or XTSE0650 rather than
			// quietly binding -- which is what use-package-003 asks for.
			//
			// Deleting it outright would break the used package's own calls,
			// though: 3.5.3's private-to-hidden default speaks about the
			// using package, and override-base-f-001's public p:f-final
			// calls the private p:f-private. So a component the used package
			// still references stays, and only what an xsl:accept explicitly
			// hid -- the one visibility 3.6.3.1 says governs use within the
			// declaring package too -- goes regardless.
			continue
		}
		keep[comp.el] = true
	}
	// An overriding declaration replaces the one it overrides. The original
	// element is left in the tree under a marker so that xsl:original inside
	// the override can still reach it; see rewriteOverride.
	replaced := map[*xdm.Node]*xdm.Node{}
	for _, comp := range u.comps {
		if ov, ok := u.overriding[comp.sym.String()]; ok {
			replaced[comp.el] = ov
			keep[comp.el] = true
		}
	}
	var kept []*xdm.Node
	for _, ch := range u.root.Children {
		if ch.Kind != xdm.KindElement {
			kept = append(kept, ch)
			continue
		}
		if ch.Name.URI == xdm.NSXSL {
			switch ch.Name.Local {
			case "expose":
				// The manifest is not compiled: it has already done its work.
				continue
			case "template":
				// A match template is not a component and travels with its
				// mode, so it is kept whatever the manifest said.
				if ch.AttrValue("name") == "" {
					kept = append(kept, ch)
					continue
				}
			}
		}
		if _, isComp := componentOf(u.comps, ch); isComp {
			if !keep[ch] {
				continue
			}
			if ov, ok := replaced[ch]; ok {
				// Both declarations stay: the overriding one under the
				// component's real name, and the original under a generated
				// one that only xsl:original inside the override can reach.
				//
				// An abstract original is the exception. It has no body by
				// definition -- supplying one is what the override is for --
				// so there is nothing for xsl:original to call, and keeping
				// it would leave a global variable with a declared type and
				// no value, which fails as XTTE0570 the moment anything
				// evaluates it. accept-040 and its neighbours override
				// exactly such a component.
				// The overriding declaration is the USING package's, and
				// stays so wherever it is put. Substituting it into the used
				// package's tree is how the composition makes it stand in
				// for the component it replaces, but that is a mechanism of
				// this implementation and not a change of authorship: 3.5.4
				// says the overriding declaration is a component of the
				// package containing the xsl:override.
				//
				// It matters for every static property 3.5.5 makes
				// package-local. document-2401 overrides a template whose
				// body calls document(), where the two packages declare
				// different xsl:strip-space; compiled in the used package's
				// tree it took the used package's stripping for both calls
				// and answered 0 where the overriding half wanted 0 and the
				// original half 4.
				//
				// The node is recorded rather than the identity pushed,
				// because compilation of this subtree happens later, inside
				// compileDocument, with compilePackage set to the used
				// package. See overridingPackage.
				noteOverridingDecl(ov, compilePackage)
				kept = append(kept, rewriteOverride(ov, ch))
				if !isAbstractDecl(ch) {
					kept = append(kept, ch)
				}
				continue
			}
		}
		kept = append(kept, ch)
	}
	u.root.Children = kept
	for _, ch := range kept {
		ch.Parent = u.root
	}
	// The used package's static variables are its own, so they are put back
	// for the compilation and taken away again after: a using package must
	// not see them, and the two packages may legitimately declare the same
	// name with different values.
	savedVars := c.staticVars
	c.staticVars = u.staticVars
	defer func() { c.staticVars = savedVars }()
	// Section 3.5.5, "Declarations Local to a Package", is explicit that
	// several kinds of declaration do not cross a package boundary at all:
	// "Declarations of keys, accumulators, decimal formats, namespace
	// aliases, output definitions, and character maps within a package have
	// local scope within that package -- they are all effectively private.
	// The elements that declare these constructs do not have a visibility
	// attribute. The unnamed decimal format and the unnamed output format
	// are also local to a package."
	//
	// They are not components: no xsl:expose can publish one and no
	// xsl:accept can name one, so a using package has no way to ask for one
	// and must never inherit one by accident. The composition here compiles
	// the used package into the SAME Stylesheet, which put every such
	// declaration into one flat table and let the using package see -- and
	// collide with -- the used package's.
	//
	// The names the used package adds are therefore taken away again once it
	// has compiled. Removal rather than a separate table is what this
	// flattened composition can express: compileUsePackages runs before the
	// using module's own declarations are compiled, so everything a table
	// gains across this call came from the used package and nothing the
	// using package declares has been added yet.
	//
	// use-package-104 asks for FODF1280 when the using package names a
	// decimal format only the used package declares, and -105 for XTDE1260
	// on a key. Both succeeded before, because the used package's
	// declaration was simply still there for the using package to find.
	restore := c.snapshotPackageLocalDecls()
	defer restore()
	// The used package compiles as an imported module: a lower import
	// precedence than the using package, so that the using package's own
	// declarations win. It is compiled through compileDocument, which
	// allocates the number and runs every check the module deserves.
	c.usedPackageDepth++
	defer func() { c.usedPackageDepth-- }()
	// A used package's whitespace declarations are its own, on the same
	// terms as its schema components below. 4.4: "The effect of
	// xsl:strip-space and xsl:preserve-space is local to the package in
	// which they appear. Declarations within a library package only affect
	// the handling of documents loaded using a call on the document, doc, or
	// collection functions ... appearing lexically within the same package."
	//
	// Every used package gets a serial rather than a flag, so that two
	// library packages are distinguishable from each other and not merely
	// from the top level. Saved and restored rather than incremented and
	// decremented, because a package used from inside another must not be
	// mistaken for its user on the way back out.
	savedPkg := compilePackage
	packageSerial++
	compilePackage = packageSerial
	defer func() { compilePackage = savedPkg }()
	// A used package brings its own in-scope schema components, so the
	// xsl:import-schema pre-pass runs again for it. 2.5.3 makes the set
	// package-scoped in as many words: "The schema components that may be
	// referenced by name in a package are referred to as the in-scope schema
	// components. The set of in-scope schema components may vary between one
	// package and another."
	//
	// The latch exists to stop the pre-pass repeating for every xsl:include
	// and xsl:import, where re-running it from a module that sees less than
	// the first one did would lose components. A package is the other case:
	// it is a separate stylesheet level with a schema of its own, and
	// leaving the latch set meant its xsl:import-schema was never processed
	// at all. override-base-v-002 declares a union type u1 in an inline
	// schema and types a public variable as="u1"; compiling it without its
	// own schema failed as XPST0051 for a type the package plainly declares.
	savedHoisted := c.schemaHoisted
	c.schemaHoisted = false
	defer func() { c.schemaHoisted = savedHoisted }()
	return c.compileDocument(u.doc, 0)
}

// referencedWithin reports whether the used package's own tree names the
// component somewhere outside the component's own declaration.
//
// The test is lexical: the component's name as written, looked for in every
// attribute value of every other element. That is coarse -- a string literal
// spelling the same name counts -- but it errs towards keeping a declaration,
// and keeping one the package does not use costs nothing while deleting one
// it does use breaks the package outright.
func referencedWithin(root *xdm.Node, comp *component) bool {
	name := comp.el.AttrValue("name")
	if name == "" {
		return false
	}
	local := name
	if i := strings.IndexByte(local, ':'); i >= 0 {
		local = local[i+1:]
	}
	var walk func(n *xdm.Node) bool
	walk = func(n *xdm.Node) bool {
		if n == comp.el {
			return false
		}
		if n.Kind == xdm.KindElement {
			for _, a := range n.Attrs {
				if strings.Contains(a.Value, local) {
					return true
				}
			}
		}
		for _, ch := range n.Children {
			if walk(ch) {
				return true
			}
		}
		return false
	}
	return walk(root)
}

// isAbstractDecl reports whether a declaration declares itself abstract.
//
// The effective visibility is not consulted: what matters here is whether the
// declaration has a body, and only the declaration's own visibility attribute
// says that.
func isAbstractDecl(el *xdm.Node) bool {
	return visibility(strings.TrimSpace(el.AttrValue("visibility"))) ==
		visAbstract
}

// principalHasUnnamedMode reports whether the principal module declares the
// unnamed mode.
//
// The principal rather than the module in hand, because an xsl:use-package
// may sit in an included module while the mode is declared in the package
// that includes it, which is how package-019 writes it.
func (c *compiler) principalHasUnnamedMode() bool {
	if c.sheet.source == nil {
		return false
	}
	root := firstElement(c.sheet.source)
	if root == nil {
		return false
	}
	for _, el := range root.ChildElements() {
		if isXSL(el, "mode") && strings.TrimSpace(el.AttrValue("name")) == "" {
			return true
		}
	}
	return false
}

// componentOf finds the component an element declares, if it declares one.
func componentOf(comps []*component, el *xdm.Node) (*component, bool) {
	for _, c := range comps {
		if c.el == el {
			return c, true
		}
	}
	return nil, false
}

// compileOverrideRules compiles the template rules an xsl:override adds to a
// mode of a used package.
//
// A match template inside xsl:override is not a component: it has no symbolic
// name, so it overrides nothing by name. What it does is add a rule to the
// mode it names, and 3.5.4 makes that rule outrank every rule the used package
// declared for the same mode. Compiling it as a declaration of the *using*
// module gives it exactly that: the using module already ranks above every
// package it uses, so the ordinary conflict rules settle it without a special
// case.
//
// It runs after the module's own declarations, once the module's final import
// precedence is known.
func (c *compiler) compileOverrideRules(root *xdm.Node, precedence int) error {
	for _, use := range root.ChildElements() {
		if !isXSL(use, "use-package") {
			continue
		}
		for _, ov := range use.ChildElements() {
			if !isXSL(ov, "override") {
				continue
			}
			for _, decl := range ov.ChildElements() {
				if !isXSL(decl, "template") || decl.AttrValue("name") != "" {
					// A named declaration is a component, and has already
					// been substituted into the used package's tree.
					continue
				}
				if err := c.compileTopLevel(decl, precedence); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// originalNS is the namespace the declaration an xsl:override replaces is
// renamed into, with a serial number appended per override.
//
// It is a URI no stylesheet can write, so a generated name in it cannot
// collide with one the source declared.
const originalNS = "http://go-xml.invalid/xslt/original/"

// originalSerial numbers the generated namespaces. Two overrides in one
// stylesheet each keep a distinct original -- override-t-015 overrides a
// template in each of two used packages and calls xsl:original from both --
// so the namespace has to differ per overriding declaration rather than per
// stylesheet.
var originalSerial int

// rewriteOverride prepares an overriding declaration to stand in the used
// package's tree in place of the declaration it overrides.
//
// xsl:original, written inside the override, means "the component this one
// overrides". The original is not inlined at each reference: it is a
// component of the used package with its own body and its own static context,
// and inlining would evaluate it in the override's context instead. Instead
// the original keeps its body and is renamed into a generated namespace, and
// the overriding declaration gets a namespace node rebinding the prefix "xsl"
// to that same namespace.
//
// One rebinding covers every form the reference can take. xsl:original(...),
// xsl:original#2, xsl:original(?, $n), $xsl:original, <xsl:call-template
// name="xsl:original"> and use-attribute-sets="xsl:original" all expand their
// prefix through the in-scope namespaces of the element they are written on,
// so all six land on the generated name without a rewriter each. Element
// names are not affected: they were resolved when the tree was parsed, and
// the compiler reads Node.Name rather than re-expanding the prefix.
func rewriteOverride(overriding, original *xdm.Node) *xdm.Node {
	originalSerial++
	uri := fmt.Sprintf("%s%d", originalNS, originalSerial)
	// A mode is not reachable through xsl:original -- there is no syntax for
	// it -- and renaming it would detach the used package's template rules
	// from the mode they name.
	if isXSL(original, "mode") {
		return overriding
	}
	setAttr(original, "name", "Q{"+uri+"}original")
	if isXSL(original, "template") || isXSL(original, "function") {
		// A renamed component must not stay visible under its old identity,
		// and a named template renamed this way is no longer an eligible
		// initial template either.
		setAttr(original, "visibility", "private")
	}
	// The rebinding goes on the overriding declaration and nowhere higher.
	// Widening it to xsl:override or to the package element would capture
	// every other xsl:-prefixed QName written in an attribute value --
	// name="xsl:initial-template" above all, which several cases in this set
	// write just outside their xsl:override. That would rename the
	// stylesheet's entry point into the generated namespace and fail as "no
	// template named xsl:initial-template", a long way from the cause.
	//
	// EVERY prefix bound to the XSLT namespace is rebound, not the literal
	// "xsl". The reference is a QName, so what identifies it is the namespace
	// its prefix expands to and not the spelling of the prefix; a stylesheet
	// may bind the XSLT namespace to anything. document-2401b writes
	// xmlns:t="http://www.w3.org/1999/XSL/Transform" throughout and calls
	// <t:call-template name="t:original"/>, which expanded to the real XSLT
	// namespace, missed a rebinding made only for "xsl", and failed as "no
	// template named t:original".
	for prefix, ns := range overriding.InScopeNamespaces() {
		if ns != xdm.NSXSL || prefix == "" {
			continue
		}
		overriding.Namespaces = append(overriding.Namespaces, &xdm.Node{
			Kind:  xdm.KindNamespace,
			Name:  xdm.QName{Local: prefix},
			Value: uri,
		})
	}
	return overriding
}

// packageSerial numbers the packages one compilation sees, so that each has
// an identity its declarations can be filed under. It is guarded by compileMu
// with the rest of the compile-time package state.
var packageSerial int

// setAttr sets or replaces an unprefixed attribute of an element.
func setAttr(el *xdm.Node, name, value string) {
	for _, a := range el.Attrs {
		if a.Name.URI == "" && a.Name.Local == name {
			a.Value = value
			return
		}
	}
	el.Attrs = append(el.Attrs, &xdm.Node{
		Kind:   xdm.KindAttribute,
		Name:   xdm.QName{Local: name},
		Value:  value,
		Parent: el,
	})
}

// checkPackageVersionRange applies the PackageVersionRange grammar of 3.5.1
// to an xsl:use-package/@package-version attribute.
//
//	PackageVersionRange ::= AnyVersion | VersionRanges
//	AnyVersion          ::= "*"
//	VersionRanges       ::= VersionRange (S? "," S? VersionRange)*
//	VersionRange        ::= PackageVersion | VersionPrefix |
//	                        VersionFrom | VersionTo | VersionFromTo
//	VersionPrefix       ::= PackageVersion ".*"
//	VersionFrom         ::= PackageVersion "+"
//	VersionTo           ::= "to" S (PackageVersion | VersionPrefix)
//	VersionFromTo       ::= PackageVersion S "to" S (PackageVersion | VersionPrefix)
//
// The check is separate from the matching itself because a range that no
// available package satisfies is XTSE3000 -- "no package can be located" --
// while a range that is not a range at all is XTSE0020, the generic "attribute
// value is invalid" error. The suite draws the line sharply: use-package-291
// through -294 write "2.0.0-alpha:beta", "TotallyInvalid", "-3.6" and
// "-alpha", and all four want XTSE0020 even though a resolver that simply
// failed to match them would have reported XTSE3000 instead.
func checkPackageVersionRange(v string) error {
	v = strings.TrimSpace(v)
	if v == "" || v == "*" {
		return nil
	}
	for _, alt := range strings.Split(v, ",") {
		if err := checkVersionRange(strings.TrimSpace(alt)); err != nil {
			return err
		}
	}
	return nil
}

// checkVersionRange applies the VersionRange production to one alternative.
func checkVersionRange(alt string) error {
	bad := func() error {
		return fmt.Errorf(
			"XTSE0020: %q is not a valid package version range in "+
				"xsl:use-package/@package-version", alt)
	}
	// "to X" is the only form that opens with a keyword, so it is recognised
	// before anything is read as a version. The space after "to" is required
	// by the grammar -- "to" is a terminal followed by S -- which is what
	// keeps "to" itself from being read as an NCName-only version.
	if rest, ok := cutVersionKeyword(alt, "to"); ok {
		return checkVersionOrPrefix(rest, bad)
	}
	// VersionFromTo puts the keyword between two versions. It is looked for
	// before the single-version forms because "1 to 5" would otherwise fail
	// as a version containing a space.
	if lo, hi, ok := cutVersionFromTo(alt); ok {
		if err := checkPackageVersion(lo, bad); err != nil {
			return err
		}
		return checkVersionOrPrefix(hi, bad)
	}
	// VersionFrom is a version with "+" appended.
	if s, ok := strings.CutSuffix(alt, "+"); ok {
		return checkPackageVersion(s, bad)
	}
	return checkVersionOrPrefix(alt, bad)
}

// cutVersionKeyword splits off a leading keyword that the grammar requires to
// be followed by whitespace.
func cutVersionKeyword(s, kw string) (string, bool) {
	if !strings.HasPrefix(s, kw) {
		return "", false
	}
	rest := s[len(kw):]
	if rest == "" || !isXMLSpace(rest[0]) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// cutVersionFromTo splits "V1 to V2" at the keyword.
//
// The keyword has to be delimited by whitespace on both sides, because "to"
// is also a perfectly good NCName and "1.0-to" is one version rather than
// two.
func cutVersionFromTo(s string) (lo, hi string, ok bool) {
	for i := 0; i+4 <= len(s); i++ {
		if !isXMLSpace(s[i]) || s[i+1:i+3] != "to" {
			continue
		}
		if !isXMLSpace(s[i+3]) {
			continue
		}
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+3:]), true
	}
	return "", "", false
}

// checkVersionOrPrefix applies "PackageVersion | VersionPrefix".
func checkVersionOrPrefix(s string, bad func() error) error {
	if p, ok := strings.CutSuffix(s, ".*"); ok {
		return checkPackageVersion(p, bad)
	}
	return checkPackageVersion(s, bad)
}

// checkPackageVersion applies the PackageVersion production:
//
//	PackageVersion ::= NumericPart ( "-" NamePart )?
//	NumericPart    ::= IntegerLiteral ( "." IntegerLiteral )*
//	NamePart       ::= NCName
//
// The NumericPart is not optional, which is what makes "-alpha" invalid
// (use-package-294) and "-3.6" invalid (use-package-293) -- a leading hyphen
// leaves nothing for the NumericPart to match. Only the FIRST hyphen
// separates the two parts: the note under 3.5.1 says "1-alpha-2 is a valid
// version number... The second hyphen is part of the NCName". That is also
// why "2.0.0-alpha:beta" fails (use-package-291): the colon is not an NCName
// character, so the NamePart is not an NCName.
func checkPackageVersion(s string, bad func() error) error {
	num := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		num = s[:i]
		if !xdm.IsNCName(s[i+1:]) {
			return bad()
		}
	}
	if num == "" {
		return bad()
	}
	for _, part := range strings.Split(num, ".") {
		if part == "" {
			return bad()
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return bad()
			}
		}
	}
	return nil
}

// isXMLSpace reports whether a byte is one of XML's four whitespace
// characters, which is what the S terminal of the version-range grammar
// admits.
func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// snapshotPackageLocalDecls records the package-local declaration tables as
// they stand, and answers a function restoring them.
//
// Section 3.5.5 makes keys, decimal formats and character maps local to the
// package that declares them. This composition compiles a used package into
// the using package's Stylesheet, so the only way to keep the scopes apart is
// to put the tables back the way the using package left them once the used
// package has compiled. See the call site in compileUsedPackage for why a
// snapshot taken there captures exactly the using package's own state.
//
// Only the three tables the conformance suite pins are touched. The section
// names namespace aliases, output definitions and accumulators too; those are
// left alone deliberately, because nothing yet distinguishes a case that
// needs the scoping from one that relies on the inheritance, and narrowing a
// scope with no test to hold it is how a regression gets in.
func (c *compiler) snapshotPackageLocalDecls() func() {
	keys := make(map[string][]*keyDef, len(c.sheet.keys))
	for k, v := range c.sheet.keys {
		keys[k] = v
	}
	formats := make(map[string]*DecimalFormat, len(c.sheet.decimalFormats))
	for k, v := range c.sheet.decimalFormats {
		formats[k] = v
	}
	maps := make(map[string]map[rune]string, len(c.sheet.characterMaps))
	for k := range c.sheet.characterMaps {
		maps[k] = nil
	}
	return func() {
		c.sheet.keys = keys
		c.sheet.decimalFormats = formats
		// Character maps are the one table whose entries must stay. A
		// character map is consulted at serialisation time, by name, from
		// whatever xsl:output or xsl:result-document named it -- including
		// one written inside the used package, whose right to its own map is
		// not in question. use-package-108 calls the used package's "go"
		// template, which does xsl:result-document format="with-maps" over
		// that package's own "cm"; removing the entry left the substitution
		// undone and the output unmapped.
		//
		// So the scope is enforced on the NAME rather than on the table: the
		// names a used package contributed are recorded, and the one check
		// that speaks for the top-level package -- the XTSE1590 raised for
		// the principal xsl:output/@use-character-maps -- refuses them. That
		// is what use-package-106 asks for, naming a "cm" only the used
		// package declares.
		for name := range c.sheet.characterMaps {
			if _, had := maps[name]; !had {
				if c.packageLocalCharMaps == nil {
					c.packageLocalCharMaps = map[string]bool{}
				}
				c.packageLocalCharMaps[name] = true
			}
		}
	}
}

// overridingDecls maps an overriding declaration's element to the package that
// wrote it.
//
// An xsl:override child is substituted into the USED package's tree so that it
// stands in for the component it replaces, but it remains a component of the
// package that contains the xsl:override -- 3.5.4 is explicit about that. Every
// static property 3.5.5 makes package-local therefore has to be answered from
// the using package for this subtree, even though the subtree is compiled while
// compilePackage names the used one.
//
// It is keyed by node because there is nowhere else to put the answer: the
// nodes are the same trees the parser produced, and the compilation that reads
// them runs later. Guarded by compileMu along with compilePackage itself.
var overridingDecls map[*xdm.Node]int

// noteOverridingDecl records that an overriding declaration belongs to pkg.
func noteOverridingDecl(ov *xdm.Node, pkg int) {
	if overridingDecls == nil {
		overridingDecls = map[*xdm.Node]int{}
	}
	overridingDecls[ov] = pkg
}

// overridingPackage answers the package an element belongs to, which is the
// package that wrote the nearest enclosing overriding declaration if there is
// one and the package being compiled otherwise.
//
// The walk is up the ancestors because the record is kept on the declaration
// and the question is asked of every expression inside it. An override nested
// inside another -- a package that uses a package that overrides -- answers
// with the innermost, which is the one that wrote the expression.
func overridingPackage(el *xdm.Node, current int) int {
	if len(overridingDecls) == 0 {
		return current
	}
	for n := el; n != nil; n = n.Parent {
		if pkg, ok := overridingDecls[n]; ok {
			return pkg
		}
	}
	return current
}
