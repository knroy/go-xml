package xslt

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// compiler holds state while compiling a stylesheet and its included modules.
type compiler struct {
	opts      CompileOptions
	sheet     *Stylesheet
	declOrder int
	// seen guards against include cycles, which would otherwise recurse until
	// the stack runs out.
	seen map[string]bool
}

// compileDocument compiles one stylesheet module at the given import
// precedence.
func (c *compiler) compileDocument(doc *xdm.Node, precedence int) error {
	root := firstElement(doc)
	if root == nil {
		return fmt.Errorf("stylesheet has no root element")
	}

	// Conditional element inclusion runs before anything else looks at the
	// tree. An excluded element must produce no error at all, so it has to be
	// gone before compilation can object to it.
	if err := applyUseWhen(doc); err != nil {
		return err
	}

	// The grammar checks run after conditional inclusion and before anything
	// is compiled, so that an element excluded by use-when is never asked
	// about — section 3.12 forbids reporting an error for one.
	if err := checkStaticGrammarTree(root, false); err != nil {
		return err
	}

	// A literal result element as the root is the abbreviated form: the whole
	// document is the body of a single template matching "/".
	if !isXSL(root, "stylesheet") && !isXSL(root, "transform") {
		if root.Attr(xdm.NSXSL, "version") == nil {
			return fmt.Errorf("root element %s is not xsl:stylesheet and has no xsl:version",
				root.Name.Lexical())
		}
		return c.compileSimplifiedStylesheet(root, precedence)
	}

	if v := root.AttrValue("version"); v != "" && !strings.HasPrefix(v, "1.") &&
		!strings.HasPrefix(v, "2.") && !strings.HasPrefix(v, "3.") {
		return fmt.Errorf("unsupported stylesheet version %q", v)
	}

	// xsl:import-schema is processed before anything else in the module.
	//
	// The schema is part of the *static context* of every expression in the
	// stylesheet, not of the declarations that happen to follow it: section
	// 3.13 puts no ordering constraint on top-level elements, so a stylesheet
	// may perfectly well declare a template using my:partNumberType above the
	// xsl:import-schema that defines it. Compiling in document order made that
	// stylesheet fail with XPST0051 while the same declarations in the other
	// order compiled.
	for _, el := range root.ChildElements() {
		if el.Name.URI == xdm.NSXSL && el.Name.Local == "import-schema" {
			if err := c.compileTopLevel(el, precedence); err != nil {
				return err
			}
		}
	}
	compileSchema = c.sheet.schema

	for _, el := range root.ChildElements() {
		if el.Name.URI == xdm.NSXSL && el.Name.Local == "import-schema" {
			continue
		}
		if err := c.compileTopLevel(el, precedence); err != nil {
			return err
		}
	}
	return nil
}

// compileSimplifiedStylesheet handles the literal-result-element form, where
// the document element is the template body rather than xsl:stylesheet.
func (c *compiler) compileSimplifiedStylesheet(root *xdm.Node, precedence int) error {
	pat, err := CompilePattern("/", newNSResolver(root, ""))
	if err != nil {
		return err
	}
	body, err := c.compileSequence(root, root)
	if err != nil {
		return err
	}
	c.declOrder++
	c.sheet.templates = append(c.sheet.templates, &Template{
		Match:            pat,
		Priority:         pat.Priority(),
		Body:             body,
		importPrecedence: precedence,
		declOrder:        c.declOrder,
	})
	return nil
}

// compileTopLevel compiles one top-level declaration.
func (c *compiler) compileTopLevel(el *xdm.Node, precedence int) error {
	if el.Name.URI != xdm.NSXSL {
		// A non-XSL top-level element is a user-defined data element, which
		// the spec says to ignore rather than reject: stylesheets legitimately
		// carry their own configuration elements up here.
		return nil
	}

	switch el.Name.Local {
	case "template":
		return c.compileTemplate(el, precedence)
	case "variable", "param":
		v, err := c.compileVariable(el)
		if err != nil {
			return err
		}
		if el.Name.Local == "param" {
			if s, ok := c.opts.StaticParams[v.Name.Clark()]; ok {
				lit := "'" + strings.ReplaceAll(s, "'", "''") + "'"
				comp, err := xpath.Compile(lit, newNSResolver(el, ""))
				if err != nil {
					return err
				}
				v.Select, v.Body = comp, nil
			}
		}
		c.sheet.globals = append(c.sheet.globals, v)
		return nil
	case "output":
		return c.compileOutput(el)
	case "key":
		return c.compileKey(el)
	case "function":
		return c.compileFunction(el)
	case "strip-space", "preserve-space":
		return c.compileSpaceControl(el)
	case "include":
		return c.compileInclude(el, precedence)
	case "import":
		// Imported modules sit one precedence level below the importer, so
		// their templates lose ties against it.
		return c.compileInclude(el, precedence-1)
	case "decimal-format":
		return c.compileDecimalFormat(el)
	case "attribute-set":
		return c.compileAttributeSet(el, precedence)
	case "namespace-alias":
		return c.compileNamespaceAlias(el)
	case "character-map":
		return c.compileCharacterMap(el)
	case "import-schema":
		return c.compileImportSchema(el)
	}
	// An unrecognised xsl: element at the top level is an error, not something
	// to skip. The spec reserves the whole namespace, so anything unknown in
	// it is either a typo — "xsl:tempalte" would otherwise be dropped and the
	// stylesheet would run producing quietly wrong output — or a version of
	// XSLT this engine does not implement. Either way the author needs to know.
	return fmt.Errorf(
		"unknown top-level element xsl:%s (XTSE0010)", el.Name.Local)
}

func (c *compiler) compileTemplate(el *xdm.Node, precedence int) error {
	t := &Template{importPrecedence: precedence}
	c.declOrder++
	t.declOrder = c.declOrder

	ns := newNSResolver(el, "")

	if m := el.AttrValue("match"); m != "" {
		pat, err := CompilePattern(m, ns)
		if err != nil {
			return err
		}
		t.Match = pat
		t.Priority = pat.Priority()
	}
	if n := el.AttrValue("name"); n != "" {
		qn, err := resolveQNameAttr(el, n)
		if err != nil {
			return err
		}
		t.Name, t.HasName = qn, true
	}
	if t.Match == nil && !t.HasName {
		return fmt.Errorf("xsl:template must have a match or name attribute")
	}
	// An explicit priority overrides the computed default.
	if p := el.AttrValue("priority"); p != "" {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return fmt.Errorf("invalid template priority %q: %w", p, err)
		}
		t.Priority = v
	}
	if m := el.AttrValue("mode"); m != "" {
		t.Mode = strings.Fields(m)
	}

	// Leading xsl:param children declare the template's parameters; they must
	// precede the body.
	children := el.ChildElements()
	i := 0
	for ; i < len(children); i++ {
		if !isXSL(children[i], "param") {
			break
		}
		v, err := c.compileVariable(children[i])
		if err != nil {
			return err
		}
		t.Params = append(t.Params, v)
	}

	body, err := c.compileSequenceFrom(el, el, i)
	if err != nil {
		return err
	}
	t.Body = body

	if t.HasName {
		c.sheet.named[t.Name.Clark()] = t
	}
	if t.Match != nil {
		c.sheet.templates = append(c.sheet.templates, t)
	}
	return nil
}

func (c *compiler) compileVariable(el *xdm.Node) (*Variable, error) {
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
		Required: el.AttrValue("required") == "yes",
		Tunnel:   el.AttrValue("tunnel") == "yes",
	}
	if as := el.AttrValue("as"); as != "" {
		t, err := compileSequenceType(as, newNSResolver(el, ""))
		if err != nil {
			return nil, fmt.Errorf("in %s/@as: %w", el.Name.Lexical(), err)
		}
		v.AsType = t
	}

	if sel := el.AttrValue("select"); sel != "" {
		comp, err := xpath.Compile(sel, newNSResolver(el, ""))
		if err != nil {
			return nil, fmt.Errorf("in %s/@select: %w", el.Name.Lexical(), err)
		}
		v.Select = comp
		if len(el.ChildElements()) > 0 {
			return nil, fmt.Errorf("%s has both a select attribute and content",
				el.Name.Lexical())
		}
		return v, nil
	}

	body, err := c.compileSequence(el, el)
	if err != nil {
		return nil, err
	}
	v.Body = body
	return v, nil
}

func (c *compiler) compileOutput(el *xdm.Node) error {
	// An xsl:output with a name declares a *named* output definition, which
	// xsl:result-document/@format selects. The unnamed one configures the
	// principal result. A named definition starts from the principal
	// settings so that it only has to state what it changes, which is how
	// stylesheets in practice write them.
	o := &c.sheet.output
	if name := el.AttrValue("name"); name != "" {
		qn, err := resolveQNameAttr(el, name)
		if err != nil {
			return err
		}
		key := qn.Clark()
		if c.sheet.namedOutputs == nil {
			c.sheet.namedOutputs = map[string]*OutputSettings{}
		}
		if existing, ok := c.sheet.namedOutputs[key]; ok {
			o = existing
		} else {
			cp := c.sheet.output
			c.sheet.namedOutputs[key] = &cp
			o = &cp
		}
	}
	return applyOutputAttrs(el, o)
}

// applyOutputAttrs reads the serialisation attributes from el into o.
//
// It is shared by xsl:output and xsl:result-document, which accept the same
// set: the instruction may override method, indent, encoding and the rest for
// one document. Reading them in two places would let the two drift.
func applyOutputAttrs(el *xdm.Node, o *OutputSettings) error {
	if v := el.AttrValue("method"); v != "" {
		o.Method = v
	}
	if v := el.AttrValue("indent"); v != "" {
		o.Indent = v == "yes"
	}
	if v := el.AttrValue("omit-xml-declaration"); v != "" {
		o.OmitXMLDecl = v == "yes"
	}
	if v := el.AttrValue("encoding"); v != "" {
		o.Encoding = v
	}
	if v := el.AttrValue("doctype-public"); v != "" {
		o.DocTypePublic = v
	}
	if v := el.AttrValue("doctype-system"); v != "" {
		o.DocTypeSystem = v
	}
	if v := el.AttrValue("standalone"); v != "" {
		o.Standalone = v
	}
	if v := el.AttrValue("version"); v != "" {
		o.Version = v
	}
	if v := el.AttrValue("use-character-maps"); v != "" {
		for _, n := range strings.Fields(v) {
			qn, err := resolveQNameAttr(el, n)
			if err != nil {
				return err
			}
			o.UseCharacterMaps = append(o.UseCharacterMaps, qn)
		}
	}
	if v := el.AttrValue("cdata-section-elements"); v != "" {
		for _, n := range strings.Fields(v) {
			qn, err := resolveQNameAttr(el, n)
			if err != nil {
				return err
			}
			o.CDataElements = append(o.CDataElements, qn)
		}
	}
	return nil
}

func (c *compiler) compileKey(el *xdm.Node) error {
	name := el.AttrValue("name")
	match := el.AttrValue("match")
	use := el.AttrValue("use")
	if name == "" || match == "" {
		return fmt.Errorf("xsl:key requires name and match attributes")
	}
	qn, err := resolveQNameAttr(el, name)
	if err != nil {
		return err
	}
	ns := newNSResolver(el, "")
	pat, err := CompilePattern(match, ns)
	if err != nil {
		return err
	}
	k := &keyDef{match: pat}
	hasBody := len(el.ChildElements()) > 0
	switch {
	case use != "" && hasBody:
		// Section 16.3: giving the value both ways leaves no rule for
		// reconciling them.
		return fmt.Errorf(
			"XTSE1205: xsl:key has both a use attribute and a sequence constructor")
	case use != "":
		if k.use, err = xpath.Compile(use, ns); err != nil {
			return err
		}
	case hasBody:
		// The value may be given as content instead, which is what lets a
		// key be computed by anything a sequence constructor can express —
		// an xsl:choose over the matched node, say, rather than a single
		// expression.
		if k.body, err = c.compileSequence(el, el); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"XTSE1205: xsl:key needs a use attribute or a sequence constructor")
	}
	c.sheet.keys[qn.Clark()] = append(c.sheet.keys[qn.Clark()], k)
	return nil
}

// compileFunction compiles an xsl:function declaration into a callable
// registered in the stylesheet's function library.
func (c *compiler) compileFunction(el *xdm.Node) error {
	name := el.AttrValue("name")
	if name == "" {
		return fmt.Errorf("xsl:function requires a name attribute")
	}
	qn, err := resolveQNameAttr(el, name)
	if err != nil {
		return err
	}
	if qn.URI == "" {
		// The spec requires a namespace, to keep user functions from
		// colliding with future builtins.
		return fmt.Errorf("xsl:function name %q must be in a namespace", name)
	}

	var params []*Variable
	children := el.ChildElements()
	i := 0
	for ; i < len(children); i++ {
		if !isXSL(children[i], "param") {
			break
		}
		p, err := c.compileVariable(children[i])
		if err != nil {
			return err
		}
		params = append(params, p)
	}
	body, err := c.compileSequenceFrom(el, el, i)
	if err != nil {
		return err
	}

	fn := &userFunction{name: qn, params: params, body: body}
	// The function's own "as" declaration converts the returned value, which
	// matters for the same reason the parameter declarations do.
	if as := el.AttrValue("as"); as != "" {
		t, err := compileSequenceType(as, newNSResolver(el, ""))
		if err != nil {
			return fmt.Errorf("in xsl:function %s/@as: %w", name, err)
		}
		fn.returns = t
	}
	c.sheet.funcs.Add(xpath.Function{
		Name:  qn,
		Arity: len(params),
		Call:  fn.call,
	})
	return nil
}

func (c *compiler) compileSpaceControl(el *xdm.Node) error {
	elems := el.AttrValue("elements")
	for _, n := range strings.Fields(elems) {
		var qn xdm.QName
		switch {
		case n == "*":
			qn = xdm.QName{Local: "*"}
		case strings.HasSuffix(n, ":*"):
			prefix := strings.TrimSuffix(n, ":*")
			uri, ok := el.LookupPrefix(prefix)
			if !ok {
				return fmt.Errorf("unbound prefix %q in %s/@elements", prefix, el.Name.Lexical())
			}
			qn = xdm.QName{URI: uri, Local: "*"}
		default:
			var err error
			if qn, err = resolveQNameAttr(el, n); err != nil {
				return err
			}
		}
		if el.Name.Local == "strip-space" {
			c.sheet.strip = append(c.sheet.strip, qn)
		} else {
			c.sheet.preserve = append(c.sheet.preserve, qn)
		}
	}
	return nil
}

func (c *compiler) compileInclude(el *xdm.Node, precedence int) error {
	href := el.AttrValue("href")
	if href == "" {
		return fmt.Errorf("%s requires an href attribute", el.Name.Lexical())
	}
	if c.opts.Resolver == nil {
		return fmt.Errorf("%s %q: module loading is disabled (no resolver configured)",
			el.Name.Lexical(), href)
	}
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	base := el.BaseURI
	if base == "" {
		base = c.opts.BaseURI
	}
	doc, resolved, err := c.opts.Resolver.ResolveModule(href, base)
	if err != nil {
		return fmt.Errorf("%s %q: %w", el.Name.Lexical(), href, err)
	}
	if c.seen[resolved] {
		return fmt.Errorf("circular %s of %q", el.Name.Local, resolved)
	}
	c.seen[resolved] = true
	defer delete(c.seen, resolved)

	return c.compileDocument(doc, precedence)
}

// --- helpers ----------------------------------------------------------------

func firstElement(n *xdm.Node) *xdm.Node {
	if n.Kind == xdm.KindElement {
		return n
	}
	for _, c := range n.Children {
		if c.Kind == xdm.KindElement {
			return c
		}
	}
	return nil
}

func isXSL(n *xdm.Node, local string) bool {
	return n.Kind == xdm.KindElement && n.Name.URI == xdm.NSXSL && n.Name.Local == local
}
