package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// Static variables, static parameters and shadow attributes, sections 9.5,
// 9.7 and 3.13.2.
//
// These three are one feature seen from three sides. A static expression is
// "an XPath expression whose value must be computed during static analysis of
// the stylesheet", and there are exactly three places one may be written: a
// use-when attribute, the select of an xsl:variable or xsl:param carrying
// static="yes", and a shadow attribute. All three are evaluated in the same
// context, before anything else in the stylesheet has been looked at, and the
// only thing that context carries over from the stylesheet is the values of
// the static variables declared before it in stylesheet tree order.
//
// That ordering is the reason this is a pass of its own rather than something
// compileTopLevel could do as it goes. Stylesheet tree order is "the order
// that results when all xsl:import and xsl:include declarations are replaced
// by the declarations in the imported or included stylesheet module" — an
// inlining of the module graph in document order. Compilation walks that graph
// in a quite different order, because import precedence is a post-order
// numbering of the import tree, so a static variable declared in a module and
// used in the module that included it would be evaluated in the wrong order.
//
// The pass runs once, from Compile, over the whole module graph, and does
// three things at each element in tree order:
//
//  1. expands the element's shadow attributes, so that everything downstream
//     — including this pass itself — sees ordinary attributes;
//  2. evaluates use-when and prunes the element if it is false;
//  3. records the value, if the element is a static variable declaration.
//
// Conditional inclusion has to be part of the same walk rather than a pass
// before or after it: a use-when may name a static variable declared above it,
// and a static variable may sit inside a module that a false use-when excludes.

// staticPhase is the state of one run of the pass.
type staticPhase struct {
	c *compiler
	// vars holds the static variables declared so far, in tree order. The
	// spec says the most recent declaration in tree order wins, so a repeated
	// name overwrites the value in place rather than appending.
	vars []staticVar
	// seen guards the module graph against include cycles. The cycle itself
	// is reported by compileInclude with the right error code; this pass only
	// has to avoid recursing forever before it gets there.
	seen map[string]bool
	// depth is how many xsl:import edges deep the walk currently is, which
	// stands in for import precedence: the importing module outranks
	// everything it imports. xsl:include does not change it, an included
	// module having the precedence of its includer.
	depth int
	// tick counts the xsl:import edges the walk has crossed, so that two
	// imports at the same nesting can still be ordered against one another.
	tick int
	// done records the module trees this pass has already walked, so that
	// compileModule does not run conditional inclusion over them a second
	// time. The trees are shared: a resolver hands back the same nodes for
	// the same URI, and pruning a tree twice would be harmless but
	// re-evaluating its use-when expressions would not, since the second
	// evaluation would see static variables the first could not.
	done map[*xdm.Node]bool
}

// runStaticPhase performs conditional element inclusion and static variable
// evaluation over the whole module graph, starting at the principal module.
func (c *compiler) runStaticPhase(doc *xdm.Node) error {
	p := &staticPhase{
		c:    c,
		seen: map[string]bool{},
		done: map[*xdm.Node]bool{},
	}
	if err := p.module(doc); err != nil {
		return err
	}
	c.staticVars = p.vars
	c.staticDone = p.done
	return nil
}

// importRankSpan is how far apart two nesting levels are placed, which bounds
// how many imports one level may hold before it could reach into the next.
// The suite's deepest module graph is a handful of modules; a million is
// beyond anything a stylesheet writes and still far from overflow.
const importRankSpan = 1 << 20

// staticVar is one evaluated static variable or parameter declaration.
type staticVar struct {
	name xdm.QName
	val  xdm.Sequence
	// isParam distinguishes the two spellings, which XTSE3450 treats as
	// inconsistent with one another however equal their values.
	isParam bool
	// prec is the declaration's import precedence, as a depth: 0 is the
	// principal module and every module it imports is one deeper. Only the
	// comparison matters, and a smaller depth is a higher precedence.
	prec int
}

// module walks one stylesheet module.
func (p *staticPhase) module(doc *xdm.Node) error {
	if p.done[doc] {
		// The tree has already been pruned and its shadow attributes
		// expanded, both of which must happen once. Its static variables
		// still have to be re-declared, though: stylesheet tree order
		// replaces every xsl:import by the declarations of the module it
		// names, so a module imported twice contributes its declarations
		// twice, at two different import precedences. use-when-0137 imports
		// one module either side of another and expects the two to conflict.
		return p.redeclare(doc)
	}
	p.done[doc] = true
	root := firstElement(doc)
	if root == nil {
		return nil
	}
	// xsl:stylesheet is treated specially: excluding it excludes its children
	// but not the element itself, so that one condition at the top of a module
	// can govern every declaration in it.
	if root.Kind == xdm.KindElement && root.Name.URI == xdm.NSXSL &&
		isStylesheetRootName(root.Name.Local) {
		if err := p.expandShadow(root); err != nil {
			return err
		}
		keep, err := p.included(root)
		if err != nil {
			return err
		}
		if !keep {
			root.Children = nil
			return nil
		}
		return p.children(root, true)
	}
	// A simplified stylesheet: the document element is a literal result
	// element, so there are no declarations, only a template body.
	return p.children(root, false)
}

// children walks n's element children, pruning the excluded ones.
//
// topLevel says whether the children are top-level declarations, which is
// where a static variable declaration and an xsl:include or xsl:import may
// appear. Everything below that is walked only for its shadow attributes and
// its use-when.
func (p *staticPhase) children(n *xdm.Node, topLevel bool) error {
	var kept []*xdm.Node
	for _, ch := range n.Children {
		if ch.Kind != xdm.KindElement {
			kept = append(kept, ch)
			continue
		}
		if err := p.expandShadow(ch); err != nil {
			return err
		}
		// A top-level element that forwards compatible behavior ignores is
		// ignored entirely, use-when included. Section 3.9 says "the element
		// and its content must be ignored", and forwards-008 writes
		// use-when="fn:new-function()" on such an element -- an expression
		// this version cannot even compile, which is the point.
		if topLevel && ignoredTopLevel(ch) {
			continue
		}
		keep, err := p.included(ch)
		if err != nil {
			return err
		}
		if !keep {
			continue
		}
		// The children are walked first, because whether a static
		// declaration has "empty content" is a question about the tree
		// conditional inclusion leaves behind. use-when-0421 writes a child
		// with a false [xsl:]use-when inside a static xsl:variable and
		// expects the declaration to be accepted: the child is not there by
		// the time 9.5's rule is applied to it.
		if err := p.children(ch, false); err != nil {
			return err
		}
		if topLevel {
			if err := p.topLevel(ch); err != nil {
				return err
			}
		}
		kept = append(kept, ch)
	}
	n.Children = kept
	return nil
}

// topLevel handles the two kinds of top-level declaration this pass cares
// about: a static variable, whose value it computes, and a module reference,
// whose target it walks in place.
func (p *staticPhase) topLevel(el *xdm.Node) error {
	if el.Name.URI != xdm.NSXSL {
		return nil
	}
	switch el.Name.Local {
	case "variable", "param":
		// static="yes" is XSLT 3.0's, and a 2.0 stylesheet writing it gets
		// XTSE0090 from the grammar check. Evaluating the declaration here
		// would bind a variable the stylesheet is about to be rejected for
		// declaring, and — worse — would make the value visible to a
		// use-when above the rejection.
		if !staticDeclAllowed(el) {
			return nil
		}
		return p.declare(el)
	case "include", "import":
		return p.includeModule(el)
	}
	return nil
}

// declare evaluates one static variable or parameter declaration.
func (p *staticPhase) declare(el *xdm.Node) error {
	name := el.AttrValue("name")
	if name == "" {
		return fmt.Errorf("%s requires a name attribute", el.Name.Lexical())
	}
	qn, err := resolveQNameAttr(el, name)
	if err != nil {
		return err
	}
	key := qn.Clark()

	// A static declaration takes its value from the calling processor when
	// one was supplied, whatever the select attribute says. Only a parameter
	// may be set that way; XTSE0020 covers a variable that tries.
	supplied, fromCaller := p.c.opts.StaticParams[key]

	// 9.5 forbids content on a static declaration outright: "when the
	// attribute static="yes" is specified, the xsl:variable or xsl:param
	// element must have empty content", because the value has to come from an
	// expression — there is no result tree to build one in at static analysis
	// time. The schema states the same rule as empty((*,text())), and the
	// violation is a content model violation, so the code is XTSE0010.
	//
	// Whitespace-only text does not count. Every declaration in the suite is
	// written across indented lines, and treating that layout as content
	// would reject static-004 along with static-007.
	sel := el.AttrValue("select")

	if !emptyStaticContent(el) {
		// XTSE0620 is the general rule -- "a variable-binding element has a
		// select attribute and has non-empty content" -- and it says the
		// same thing about this declaration more specifically than the
		// content-model violation does, so it takes precedence where both
		// apply. Without a select the only rule broken is 9.5's, which the
		// schema states as empty((*,text())): a content model violation, so
		// XTSE0010.
		if sel != "" {
			return fmt.Errorf(
				"XTSE0620: %s has a select attribute and non-empty content",
				el.Name.Lexical())
		}
		return fmt.Errorf(
			"XTSE0010: %s has static=\"yes\" and a sequence constructor; "+
				"a static variable's value must come from its select attribute",
			el.Name.Lexical())
	}

	// required="yes" says the value must come from the caller, and select
	// supplies one from the stylesheet. The two contradict each other, and
	// 9.5's content model admits only one of them — static-005a asks for
	// XTSE0010 even in the run where the caller does supply a value, so the
	// rejection cannot be conditional on what was supplied.
	required := isYes(el.AttrValue("required"))
	if required && sel != "" {
		return fmt.Errorf(
			"XTSE0010: %s has required=\"yes\" and a select attribute; "+
				"a required parameter takes its value from the caller",
			el.Name.Lexical())
	}
	if required && !fromCaller {
		return fmt.Errorf(
			"XTDE0050: no value was supplied for the required static "+
				"parameter $%s", qn.Lexical())
	}
	var val xdm.Sequence
	switch {
	case fromCaller && el.Name.Local == "param":
		val = supplied
	case sel != "":
		val, err = p.eval(el, sel)
		if err != nil {
			return err
		}
	case el.Attr("", "as") != nil:
		// A declaration with an "as" and no select and no supplied value is
		// implicitly required: the empty sequence is its only candidate
		// default, and the conversion below rejects it whenever the declared
		// type excludes it.
		val = nil
	default:
		// 9.5: a static parameter with neither select nor as defaults to a
		// zero-length string rather than to the empty sequence, which is why
		// static-008 can ask for upper-case($p) and get "".
		val = xdm.Sequence{xdm.NewString("")}
	}

	if as := el.AttrValue("as"); as != "" {
		t, terr := compileSequenceType(as, newNSResolver(el, ""))
		if terr != nil {
			return fmt.Errorf("in %s/@as: %w", el.Name.Lexical(), terr)
		}
		// A value the CALLER supplied that will not convert is a type error,
		// XTTE0590 -- the same code an xsl:param gets anywhere else, which is
		// what static-013c asks for. A value the DECLARATION supplied that
		// will not convert leaves the parameter with no usable default, so
		// section 9.2 makes it implicitly mandatory and the caller having
		// supplied nothing is the missing-value error rather than a type one.
		code := "XTSE0590"
		if fromCaller && el.Name.Local == "param" {
			code = "XTTE0590"
		}
		conv, cerr := t.convertAs(val,
			"static "+strings.TrimPrefix(el.Name.Local, "xsl:")+" $"+qn.Lexical(),
			code)
		if cerr != nil {
			if !fromCaller && el.Name.Local == "param" {
				if len(val) == 0 && sel == "" {
					return fmt.Errorf(
						"XTDE0700: no value was supplied for the static parameter $%s, "+
							"and the empty sequence is not a valid instance of %s",
						qn.Lexical(), as)
				}
				// W3C bug 28355 settled this one on XTDE0050: an explicit
				// default that does not match the declared type is no default
				// at all, so the parameter is required and no value was
				// supplied for it. static-013 is the case.
				return fmt.Errorf(
					"XTDE0050: the declared default of the static parameter $%s "+
						"is not a valid instance of %s, which makes the "+
						"parameter required, and no value was supplied",
					qn.Lexical(), as)
			}
			return cerr
		}
		val = conv
	}

	isParam := el.Name.Local == "param"
	for i := range p.vars {
		if p.vars[i].name.Clark() != key {
			continue
		}
		prev := &p.vars[i]
		// XTSE3450, section 9.5 rule 2. When the declaration reached later in
		// stylesheet tree order has the *higher* import precedence, it is the
		// one that wins — but every static expression between the two has
		// already been evaluated against the earlier value, and cannot be
		// re-evaluated. The specification resolves that by requiring the two
		// to agree, so that which one was used cannot be observed.
		//
		// The other direction needs no check: a later declaration of lower
		// precedence simply loses, and the value in scope never changed.
		if p.depth < prev.prec {
			if isParam != prev.isParam {
				return fmt.Errorf(
					"XTSE3450: static $%s is declared as xsl:%s at a higher "+
						"import precedence than the xsl:%s declared before it "+
						"in stylesheet tree order",
					qn.Lexical(), el.Name.Local,
					declKind(prev.isParam))
			}
			// A parameter whose value came from the caller is not
			// "initialized" in 9.5's sense: both declarations take the
			// supplied value, so they cannot disagree.
			if !(isParam && fromCaller) && !identicalStatic(prev.val, val) {
				return fmt.Errorf(
					"XTSE3450: the two declarations of the static variable $%s "+
						"have different values, and the one with the higher "+
						"import precedence comes later in stylesheet tree order",
					qn.Lexical())
			}
			prev.val = val
			prev.prec = p.depth
			return nil
		}
		// Equal or lower precedence: the most recent declaration in tree
		// order is the one a following static expression sees.
		if p.depth == prev.prec {
			prev.val = val
			prev.isParam = isParam
		}
		return nil
	}
	p.vars = append(p.vars, staticVar{
		name: qn, val: val, isParam: isParam, prec: p.depth})
	return nil
}

// declKind names the element a staticVar was declared by, for a diagnostic.
func declKind(isParam bool) string {
	if isParam {
		return "param"
	}
	return "variable"
}

// identicalStatic reports whether two static values are identical in the
// fn:deep-equal-with-identical-types sense 9.5 requires of two declarations
// that must agree. Function items are never identical, which is why 9.5
// forbids them here outright.
func identicalStatic(a, b xdm.Sequence) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		xa, xok := a[i].(*xdm.Atomic)
		ya, yok := b[i].(*xdm.Atomic)
		if !xok || !yok {
			// A node or a function item: identity for a node is being the
			// same node, and a function item is never identical to anything.
			if a[i] != b[i] {
				return false
			}
			continue
		}
		// Identity is stricter than eq: it distinguishes the types as well as
		// the values, so xs:integer 1 and xs:double 1 are not identical. The
		// canonical lexical form stands in for the value, which is what makes
		// it canonical.
		if xa.Type != ya.Type || xa.String() != ya.String() {
			return false
		}
	}
	return true
}

// includeModule walks the module an xsl:include or xsl:import names, in place,
// so that its declarations appear in stylesheet tree order.
//
// A failure to resolve is not reported here. The compiler reaches the same
// element later and reports XTSE0165 with the context the caller expects; a
// second, differently worded report from a pass that exists only to order
// declarations would be the one the caller saw.
func (p *staticPhase) includeModule(el *xdm.Node) error {
	href := el.AttrValue("href")
	if href == "" || p.c.opts.Resolver == nil {
		return nil
	}
	base := el.BaseURI
	if base == "" {
		base = p.c.opts.BaseURI
	}
	fragment := ""
	if i := strings.IndexByte(href, '#'); i >= 0 {
		fragment, href = href[i+1:], href[:i]
	}
	doc, resolved, err := p.c.opts.Resolver.ResolveModule(href, base)
	if err != nil {
		return nil
	}
	if p.seen[resolved] {
		return nil
	}
	p.seen[resolved] = true
	defer delete(p.seen, resolved)
	if fragment != "" {
		if sub := embeddedModule(doc, fragment); sub != nil {
			doc = sub
		}
	}
	// An imported module ranks below its importer; an included one shares the
	// includer's precedence, so only xsl:import moves the depth.
	if el.Name.Local == "import" {
		// Section 3.10.2 orders sibling imports as well as nested ones: "the
		// one that occurs later in document order has higher import
		// precedence". Depth alone gave two siblings the same rank, which is
		// what let the second import of a module silently agree with the
		// first instead of outranking what came between them. The tick
		// distinguishes them: it only ever rises, so a later import is a
		// smaller number here, where smaller is higher.
		saved := p.depth
		p.tick++
		// Every import ranks below its importer, and among the imports one
		// module makes, the later one ranks above the earlier. Encoding the
		// nesting in the high bits and the tick in the low ones gives both
		// orderings at once, with smaller meaning higher precedence: a
		// deeper import is a larger number however early it was reached, and
		// two imports at the same depth are separated by their ticks, the
		// later one -- the larger tick -- being made the smaller number.
		p.depth = (saved+1)*importRankSpan - p.tick
		defer func() { p.depth = saved }()
	}
	return p.module(doc)
}

// staticDeclAllowed reports whether el may carry static="yes" here.
//
// The two declarations differ in what governs them, and the grammar table is
// where that is recorded: xsl:variable/@static is since30, so a 2.0 module
// may not write it, while xsl:param/@static is processor30, because a static
// parameter is supplied by whoever drives the processor. Binding a
// declaration the grammar check is about to reject would make its value
// visible to a use-when above the rejection, so the same distinction has to
// be drawn here rather than assumed either way.
func staticDeclAllowed(el *xdm.Node) bool {
	if !isStaticDecl(el) {
		return false
	}
	if el.Name.Local == "param" {
		return processorAtLeast30()
	}
	return moduleAtLeast30(el)
}

// isStaticDecl reports whether a declaration carries static="yes".
//
// The attribute is a boolean in the XSLT sense, so "true" is a synonym. It is
// only meaningful on a top-level declaration; XTSE0020 for one written on a
// local variable is checked with the rest of the static errors.
func isStaticDecl(el *xdm.Node) bool {
	return isYes(el.AttrValue("static"))
}

// isYes reads an attribute whose value space is XSLT's boolean vocabulary.
func isYes(v string) bool {
	switch strings.TrimSpace(v) {
	case "yes", "true", "1":
		return true
	}
	return false
}

// emptyStaticContent reports whether el satisfies 9.5's requirement that a
// static declaration have empty content.
//
// Whitespace-only text is not content: the schema writes the rule as
// empty((*,text())), but every declaration in a real stylesheet is indented,
// and reading the indentation as a sequence constructor would reject the
// declarations 9.5 exists to allow.
func emptyStaticContent(el *xdm.Node) bool {
	for _, ch := range el.Children {
		switch ch.Kind {
		case xdm.KindElement:
			return false
		case xdm.KindText:
			if strings.TrimSpace(ch.Value) != "" {
				return false
			}
		}
	}
	return true
}

// expandShadow replaces the shadow attributes of an XSLT element with the
// attributes they stand for.
//
// A shadow attribute is one whose no-namespace name begins with an underscore,
// written on an element in the XSLT namespace. Its value is a value template
// whose expressions are static expressions, and the result becomes the value
// of the attribute of the same name without the underscore. The mechanism
// does not nest: a name beginning with two underscores names an attribute
// beginning with one, not a second round of preprocessing.
//
// It applies to XSLT elements alone. On a literal result element an underscore
// is an ordinary first character of an ordinary attribute name, which the
// result tree carries through unchanged.
func (p *staticPhase) expandShadow(el *xdm.Node) error {
	if el.Name.URI != xdm.NSXSL {
		return nil
	}
	// Shadow attributes are XSLT 3.0's. To a 2.0 stylesheet an underscore is
	// the ordinary first character of an attribute name the summaries do not
	// define, which the grammar check reports as XTSE0090 — so expanding one
	// here would silently accept a stylesheet every conforming 2.0 processor
	// rejects.
	//
	// The module element is the exception, because @_version is what sets the
	// version the rest of the module is read at. Reading the version it is
	// about to overwrite to decide whether to expand it would make the
	// attribute unable to do the one thing it is for, so on the module
	// element the shadow form is expanded first and the version read after.
	// A version="2.0" module is not reached at all: whether the processor
	// admits 3.0 has already been settled by the time this pass runs.
	// A shadow attribute supplies the value of the attribute it names, and
	// that attribute is still put through the grammar afterwards -- so a
	// version="2.0" module writing a shadow form of an attribute 2.0 does not
	// have is refused just the same, only by the check that owns the rule.
	// What expansion itself follows is therefore the processor: function-1025
	// writes _new-each-time in a version="2.0" module scoped XSLT30+, the
	// same shape as the plain new-each-time beside it.
	if !processorAtLeast30() && !isModuleElement(el) {
		return nil
	}
	// The overwhelmingly common case is an element with no shadow attribute
	// at all, and this runs for every element of every module.
	any := false
	for _, a := range el.Attrs {
		if a.Name.URI == "" && strings.HasPrefix(a.Name.Local, "_") {
			any = true
			break
		}
	}
	if !any {
		return nil
	}

	shadowed := map[string]string{}
	var kept []*xdm.Node
	for _, a := range el.Attrs {
		if a.Name.URI != "" || !strings.HasPrefix(a.Name.Local, "_") {
			kept = append(kept, a)
			continue
		}
		v, err := p.valueTemplate(el, a.Value)
		if err != nil {
			return fmt.Errorf("in %s/@%s: %w",
				el.Name.Lexical(), a.Name.Local, err)
		}
		shadowed[strings.TrimPrefix(a.Name.Local, "_")] = v
	}
	// "If a shadow attribute is present, then any attribute node with name N
	// is ignored" — including for the purpose of reporting an error in its
	// value, which is why the target is removed rather than left in place for
	// the grammar check to object to.
	el.Attrs = el.Attrs[:0]
	for _, a := range kept {
		if a.Name.URI == "" {
			if _, shadowedOut := shadowed[a.Name.Local]; shadowedOut {
				continue
			}
		}
		el.Attrs = append(el.Attrs, a)
	}
	for name, v := range shadowed {
		el.Attrs = append(el.Attrs, &xdm.Node{
			Kind:   xdm.KindAttribute,
			Name:   xdm.QName{Local: name},
			Value:  v,
			Parent: el,
		})
	}
	return nil
}

// valueTemplate evaluates a value template whose expressions are static.
//
// It is compileAVT's grammar — doubled braces escape, expressions between
// single braces — but evaluated here and now rather than compiled for later,
// because a static expression has no runtime to be evaluated in.
func (p *staticPhase) valueTemplate(el *xdm.Node, src string) (string, error) {
	if !strings.ContainsAny(src, "{}") {
		return src, nil
	}
	var sb strings.Builder
	for i := 0; i < len(src); {
		switch src[i] {
		case '{':
			if i+1 < len(src) && src[i+1] == '{' {
				sb.WriteByte('{')
				i += 2
				continue
			}
			end, err := findAVTClose(src, i+1)
			if err != nil {
				return "", err
			}
			exprSrc := src[i+1 : end]
			// A brace pair enclosing nothing contributes nothing, exactly as
			// it does in an ordinary value template: 5.6.1's rule is about
			// the value template, not about which of the two evaluates it.
			// shadow-001 writes _select="${}{$N}" to make "$x".
			if processorAtLeast30() &&
				strings.TrimSpace(commentsStripped(exprSrc)) == "" {
				i = end + 1
				continue
			}
			v, err := p.eval(el, exprSrc)
			if err != nil {
				return "", err
			}
			sb.WriteString(constructedText(v, " "))
			i = end + 1
		case '}':
			if i+1 < len(src) && src[i+1] == '}' {
				sb.WriteByte('}')
				i += 2
				continue
			}
			return "", fmt.Errorf(
				"XTSE0370: unescaped '}' in attribute value template %q", src)
		default:
			sb.WriteByte(src[i])
			i++
		}
	}
	return sb.String(), nil
}

// eval evaluates one static expression written on el.
//
// The static context is the one 9.7 tabulates: the in-scope namespaces of the
// containing element, no context item, no schema, the restricted function
// library, and the static variables declared before el in tree order — which
// is exactly the set this pass has accumulated by the time it reaches el.
func (p *staticPhase) eval(el *xdm.Node, src string) (xdm.Sequence, error) {
	ns := &nsResolver{
		bindings:  el.InScopeNamespaces(),
		defaultNS: xpathDefaultNamespace(el),
	}
	compiled, err := xpath.CompileVersion(src, ns, xpathVersionAt(el))
	if err != nil {
		return nil, err
	}
	ctx := xpath.NewContext(nil, useWhenFuncs(ns.bindings))
	// 3.12's table gives a static expression "the core functions defined in
	// [Functions and Operators]" -- the whole library, not a 2.0 subset of
	// it. Which functions exist follows the PROCESSOR, exactly as it does at
	// run time; the module's own version still governs the grammar. Left at
	// the default the library was 2.0, so use-when-0127b could not call
	// fn:generate-id, which F&O 3.0 defines.
	if processorAtLeast30() {
		ctx.LibraryVersion = xpath.XPath31
		ctx.RegexVersion = xpath.XPath31
	}
	ctx.StaticBaseURI = el.BaseURI
	for _, sv := range p.vars {
		ctx = ctx.WithVar(sv.name, sv.val)
	}
	return compiled.Eval(ctx)
}

// included evaluates el's use-when, if it has one.
func (p *staticPhase) included(el *xdm.Node) (bool, error) {
	// use-when is written unprefixed on an XSLT element and prefixed on a
	// literal result element, and neither form is accepted in the other's
	// place. Reading xsl:use-when on an XSLT element here would prune the
	// element on the strength of an attribute XTSE0090 is about to reject it
	// for carrying -- and having pruned it, nothing would be left to reject.
	var expr string
	if el.Name.URI == xdm.NSXSL {
		if a := el.Attr("", "use-when"); a != nil {
			expr = a.Value
		}
	} else if a := el.Attr(xdm.NSXSL, "use-when"); a != nil {
		expr = a.Value
	}
	if expr == "" {
		return true, nil
	}
	v, err := p.eval(el, expr)
	if err != nil {
		// An error in the use-when expression itself is reported: it is the
		// one error the exclusion rule does not suppress.
		return false, fmt.Errorf("in %s/@use-when: %w", el.Name.Lexical(), err)
	}
	b, err := xpath.EffectiveBooleanValue(v)
	if err != nil {
		return false, fmt.Errorf("in %s/@use-when: %w", el.Name.Lexical(), err)
	}
	return b, nil
}

// staticGlobal builds the global binding for a static declaration.
//
// A static variable is visible to ordinary expressions in the stylesheet as
// well as to static ones — static-001 writes {$static-param} in a template —
// so it becomes a global like any other. What makes it different is that its
// value is already known: it was computed in tree order by the static phase,
// and nothing at run time may change it, which is why a static xsl:param
// ignores Transform's Params.
func (c *compiler) staticGlobal(el *xdm.Node) (*Variable, error) {
	name := el.AttrValue("name")
	if name == "" {
		return nil, fmt.Errorf("%s requires a name attribute", el.Name.Lexical())
	}
	qn, err := resolveQNameAttr(el, name)
	if err != nil {
		return nil, err
	}
	v := &Variable{
		Name:     qn,
		IsParam:  el.Name.Local == "param",
		isStatic: true,
		baseURI:  el.BaseURI,
	}
	key := qn.Clark()
	for _, sv := range c.staticVars {
		if sv.name.Clark() == key {
			v.staticValue = sv.val
			return v, nil
		}
	}
	// The static phase visits exactly the declarations that survive
	// conditional inclusion, and compilation sees the same tree, so a name
	// missing here means the two walks disagreed rather than that the
	// stylesheet is at fault.
	return nil, fmt.Errorf("static %s $%s was not evaluated by the static phase",
		el.Name.Local, qn.Lexical())
}

// isModuleElement reports whether el is the element a stylesheet module is
// rooted at.
func isModuleElement(el *xdm.Node) bool {
	return el.Kind == xdm.KindElement && el.Name.URI == xdm.NSXSL &&
		isStylesheetRootName(el.Name.Local) &&
		(el.Parent == nil || el.Parent.Kind != xdm.KindElement ||
			el.Parent.Name.URI != xdm.NSXSL)
}

// forwardsAtDeep reports whether el is processed with forwards compatible
// behavior, reading the version from the nearest ancestor-or-self that
// declares one. forwardsAt asks the same question of a single element and
// takes the inherited answer as an argument, which this pass does not carry.
func forwardsAtDeep(el *xdm.Node) bool {
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind == xdm.KindElement && hasVersionAttr(cur) {
			return forwardsAt(cur, false)
		}
	}
	return false
}

// ignoredTopLevel reports whether ch, a child of a module element, is one
// section 3.9's first rule of forwards compatible behavior discards.
//
// That rule covers any XSLT element XSLT 3.0 does not allow in this position,
// known or not, provided the element's effective version puts it under
// forwards compatible behavior.
func ignoredTopLevel(ch *xdm.Node) bool {
	if ch.Name.URI != xdm.NSXSL || !forwardsAtDeep(ch) {
		return false
	}
	// Not inside an xsl:package: see inPackage. Discarding the element here
	// would leave the grammar check nothing to report, which is why
	// package-902 and -905 compiled clean rather than raising XTSE0010.
	if inPackage(ch) {
		return false
	}
	if _, known := xsltElements[ch.Name.Local]; !known {
		return true
	}
	return !xsltDeclarations[ch.Name.Local]
}

// redeclare walks a module tree that has already been pruned, re-evaluating
// only its static variable declarations and its nested module references.
//
// It is the second and later visit to a module the graph reaches more than
// once. The tree is shared, so pruning it again would be a no-op and
// expanding its shadow attributes again would re-evaluate expressions against
// a static context that has since moved on; only the declarations belong to
// the position in tree order this visit occupies.
func (p *staticPhase) redeclare(doc *xdm.Node) error {
	root := firstElement(doc)
	if root == nil || root.Kind != xdm.KindElement ||
		root.Name.URI != xdm.NSXSL || !isStylesheetRootName(root.Name.Local) {
		return nil
	}
	for _, ch := range root.ChildElements() {
		if ch.Name.URI != xdm.NSXSL {
			continue
		}
		switch ch.Name.Local {
		case "variable", "param":
			if staticDeclAllowed(ch) {
				if err := p.declare(ch); err != nil {
					return err
				}
			}
		case "include", "import":
			if err := p.includeModule(ch); err != nil {
				return err
			}
		}
	}
	return nil
}
