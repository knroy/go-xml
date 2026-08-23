package xslt

import (
	"fmt"
	"sort"
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
	// the stack runs out. schemaSeen does the same for the xsl:import-schema
	// pre-pass, which walks the module graph separately and must not visit a
	// module twice.
	seen       map[string]bool
	schemaSeen map[string]bool
	// aliasDecls records each xsl:namespace-alias with its import precedence,
	// for XTSE0810; charMapPrecedence does the same for xsl:character-map and
	// XTSE1580. Both are needed because the stylesheet keeps only the winning
	// declaration, which cannot say whether a losing one conflicted.
	aliasDecls        map[string]aliasDecl
	charMapPrecedence map[string]int

	// usedAttributeSets collects every name a use-attribute-sets attribute
	// refers to, for XTSE0710.
	usedAttributeSets []xdm.QName

	// inputTypeAnnotations is the value the modules so far have agreed on,
	// for XTSE0265. Empty means no module has stated one.
	inputTypeAnnotations string

	// keyCollations records the effective collation of each xsl:key name, for
	// XTSE1220.
	keyCollations map[string]string

	// statedDecimalFormat records, per format name, which attributes an
	// xsl:decimal-format declaration actually named. XTSE1290 compares
	// declarations attribute by attribute, and the merged format kept in the
	// stylesheet cannot say which of its values were stated and which are
	// defaults.
	statedDecimalFormat map[string]map[string]bool

	// decimalFormatPrecedence is the import precedence of the declarations
	// that produced each stored format. XTSE1290 only forbids a conflict
	// between declarations at the *same* precedence; a higher-precedence
	// module overriding a lower one is the ordinary way an import is
	// customised.
	decimalFormatPrecedence map[string]int

	// decimalFormatConflicts holds XTSE1290 conditions that a
	// higher-precedence declaration may still override, checked once the
	// whole module graph is compiled.
	decimalFormatConflicts map[string]decimalFormatConflict

	// outputAttrs records the serialisation attributes declared for each
	// output definition, for XTSE1560. The empty key is the unnamed one.
	outputAttrs map[string]map[string][]outputAttrDecl

	// charMapIncludes records the use-character-maps of each
	// xsl:character-map, resolved after every module has compiled.
	charMapIncludes []charMapInclusion
	// calls records every xsl:call-template, so that XTSE0680 can be checked
	// once every template is known.
	calls []*callTemplateInstr
	// funcPrecedence records the import precedence each function name and
	// arity was declared at, for XTSE0770.
	funcPrecedence map[string]int
}

// checkCallTemplateParams implements XTSE0680.
//
// "It is a static error to pass a non-tunnel parameter named x to a template
// that does not have a template parameter named x." A tunnel parameter is
// exempt: passing one through a template that does not declare it is the whole
// point of tunnelling.
//
// It runs after every module, because the template being called may be
// declared below the call or in a module imported afterwards.
func (c *compiler) checkCallTemplateParams() error {
	for _, call := range c.calls {
		t, ok := c.sheet.named[call.name.Clark()]
		if !ok {
			// A call to a template that does not exist is XTSE0650, reported
			// where the call is executed rather than here.
			continue
		}
		// A tunnel template parameter is not a match for a non-tunnel
		// with-param. The two are separate bindings of the same name — a
		// tunnel parameter is supplied by an ancestor call and passed through
		// invisibly, and a non-tunnel one by the immediate caller — so a
		// template declaring only the tunnel form "does not have a template
		// parameter named x" in the sense the clause means.
		declared := map[string]bool{}
		for _, p := range t.Params {
			if p.Tunnel {
				continue
			}
			declared[p.Name.Clark()] = true
		}
		for _, p := range call.params {
			if p.Tunnel || declared[p.Name.Clark()] {
				continue
			}
			return fmt.Errorf(
				"XTSE0680: xsl:call-template passes parameter $%s to template %s, "+
					"which does not declare it",
				p.Name.Lexical(), call.name.Lexical())
		}
	}
	return nil
}

// compileDocument compiles one stylesheet module at the given import
// precedence.
func (c *compiler) compileDocument(doc *xdm.Node, precedence int) error {
	collectPrefixes(doc, c.sheet.prefixes)
	if err := c.checkInputTypeAnnotations(doc); err != nil {
		return err
	}
	root := firstElement(doc)
	if root == nil {
		return fmt.Errorf("stylesheet has no root element")
	}

	// The principal module's own tree is what document("") returns. It is
	// recorded before conditional inclusion prunes anything, since the
	// document a stylesheet reads through document("") is the one on disk.
	if c.sheet.source == nil {
		c.sheet.source = doc
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
	// The static rules that need more than the element grammar: lexical
	// forms, sibling order, reserved namespaces. See staticerrors.go.
	if err := checkStaticErrors(root); err != nil {
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
	// The pre-pass reaches into included and imported modules as well, because
	// the in-scope schema components are a property of the *stylesheet*, not
	// of the module that declared them: section 3.14 says importing components
	// in one module makes them available throughout. A schema imported only in
	// a secondary module was invisible to the primary one, so an expression
	// there naming one of its types was XPST0051.
	if err := c.hoistImportSchema(root); err != nil {
		return err
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
	// The document element is itself a literal result element, not merely the
	// container of one: "<out xsl:version='2.0'>...</out>" produces <out>.
	// Compiling only its children dropped the outermost element from every
	// simplified stylesheet.
	instr, err := c.compileLiteralElement(root)
	if err != nil {
		return err
	}
	body := []Instruction{instr}
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
				comp, err := compileExpr(lit, newNSResolver(el, ""))
				if err != nil {
					return err
				}
				v.Select, v.Body = comp, nil
			}
		}
		// XTSE0630: two bindings of a global variable may not share a name
		// at the same import precedence. A higher precedence legitimately
		// overrides a lower one, so only a tie is an error.
		for _, prev := range c.sheet.globals {
			if prev.Name.Clark() == v.Name.Clark() &&
				prev.precedence == precedence {
				return fmt.Errorf(
					"XTSE0630: two global variables are named %s at the same "+
						"import precedence", v.Name.Lexical())
			}
		}
		v.precedence = precedence
		c.sheet.globals = append(c.sheet.globals, v)
		return nil
	case "output":
		return c.compileOutput(el, precedence)
	case "key":
		return c.compileKey(el)
	case "function":
		return c.compileFunction(el, precedence)
	case "strip-space", "preserve-space":
		return c.compileSpaceControl(el)
	case "include":
		return c.compileInclude(el, precedence)
	case "import":
		// Imported modules sit one precedence level below the importer, so
		// their templates lose ties against it.
		return c.compileInclude(el, precedence-1)
	case "decimal-format":
		return c.compileDecimalFormat(el, precedence)
	case "attribute-set":
		return c.compileAttributeSet(el, precedence)
	case "namespace-alias":
		return c.compileNamespaceAlias(el, precedence)
	case "character-map":
		return c.compileCharacterMap(el, precedence)
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
		return fmt.Errorf("XTSE0500: xsl:template must have a match or name attribute")
	}
	// The other half of XTSE0500: "an xsl:template element that has no match
	// attribute must have no mode attribute and no priority attribute." Both
	// only mean anything for a template rule — a named template is invoked by
	// name, so neither the mode it would match in nor the priority it would
	// win by has any effect, and specifying one is a mistake about how the
	// template will be reached.
	if t.Match == nil {
		for _, a := range []string{"mode", "priority"} {
			if el.Attr("", a) != nil {
				return fmt.Errorf(
					"XTSE0500: xsl:template has no match attribute, so it "+
						"must have no %s attribute", a)
			}
		}
	}
	// An explicit priority overrides the computed default.
	if p := el.AttrValue("priority"); p != "" {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return fmt.Errorf("invalid template priority %q: %w", p, err)
		}
		t.Priority = v
	}
	// The attribute is looked up rather than its value, because mode="" is
	// itself one of the errors: "it is a static error if the list is empty".
	// Testing the value for "" cannot tell an empty list from an absent
	// attribute, so the empty one went unreported.
	if ma := el.Attr("", "mode"); ma != nil {
		modes := strings.Fields(ma.Value)
		// XTSE0550: the list may not be empty, may not repeat a token, and
		// "#all" may not appear beside anything else.
		if len(modes) == 0 {
			return fmt.Errorf("XTSE0550: xsl:template/@mode is empty")
		}
		seen := map[string]bool{}
		for _, tok := range modes {
			if seen[tok] {
				return fmt.Errorf(
					"XTSE0550: xsl:template/@mode names %q more than once", tok)
			}
			seen[tok] = true
		}
		if seen["#all"] && len(modes) > 1 {
			return fmt.Errorf(
				"XTSE0550: xsl:template/@mode=#all cannot appear with other modes")
		}
		// "or if the list contains an invalid token": a mode name is a QName,
		// and the two hash tokens are the only other things permitted.
		for _, tok := range modes {
			if tok == "#all" || tok == "#default" {
				continue
			}
			if !isLexicalQName(tok) {
				return fmt.Errorf(
					"XTSE0550: xsl:template/@mode names %q, which is not a "+
						"mode name", tok)
			}
		}
		// Mode names are expanded QNames, not lexical ones: two prefixes bound
		// to the same URI name one mode, and a stylesheet that uses them
		// interchangeably must dispatch to the same rules. The pseudo-modes
		// are left as written because they are not names at all.
		for i, tok := range modes {
			if tok == "#all" || tok == "#default" {
				continue
			}
			qn, err := resolveQNameAttr(el, tok)
			if err != nil {
				return err
			}
			modes[i] = xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()
		}
		t.Mode = modes
	}

	if as := el.AttrValue("as"); as != "" {
		at, err := compileSequenceType(as, newNSResolver(el, ""))
		if err != nil {
			return fmt.Errorf("in xsl:template/@as: %w", err)
		}
		t.AsType = at
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
		// XTSE0580: two parameters of a template may not share a name.
		for _, prev := range t.Params {
			if prev.Name.Clark() == v.Name.Clark() {
				return fmt.Errorf(
					"XTSE0580: xsl:template has two parameters named %s",
					v.Name.Lexical())
			}
		}
		t.Params = append(t.Params, v)
	}

	body, err := c.compileSequenceFrom(el, el, i)
	if err != nil {
		return err
	}
	t.Body = body

	if t.HasName {
		// XTSE0660: two templates may not share a name at the same import
		// precedence. A module of higher precedence legitimately overrides
		// one of lower, which is what import is for, so only a tie is an
		// error.
		if prev, dup := c.sheet.named[t.Name.Clark()]; dup &&
			prev.importPrecedence == t.importPrecedence {
			return fmt.Errorf(
				"XTSE0660: two templates are named %s at the same import precedence",
				t.Name.Lexical())
		}
		if prev, dup := c.sheet.named[t.Name.Clark()]; !dup ||
			t.importPrecedence >= prev.importPrecedence {
			c.sheet.named[t.Name.Clark()] = t
		}
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
		baseURI:  el.BaseURI,
	}
	if as := el.AttrValue("as"); as != "" {
		t, err := compileSequenceType(as, newNSResolver(el, ""))
		if err != nil {
			return nil, fmt.Errorf("in %s/@as: %w", el.Name.Lexical(), err)
		}
		v.AsType = t
	}

	if sel := el.AttrValue("select"); sel != "" {
		comp, err := compileExpr(sel, newNSResolver(el, ""))
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

func (c *compiler) compileOutput(el *xdm.Node, precedence int) error {
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
	c.recordOutputAttrs(el, precedence)
	return applyOutputAttrs(el, o)
}

// recordOutputAttrs notes one xsl:output's serialisation attributes for the
// XTSE1560 check.
//
// "It is a static error if two xsl:output declarations within an output
// definition specify explicit values for the same attribute (other than
// cdata-section-elements and use-character-maps), with the values of the
// attributes being not equal, unless there is another xsl:output declaration
// within the same output definition that specifies the attribute with higher
// import precedence."
//
// Two exclusions are in that sentence and both matter. The two named
// attributes are cumulative — a second declaration adds elements to the list
// rather than replacing it — so they cannot conflict. And a declaration at a
// higher import precedence settles the question, which is what makes an
// importing stylesheet able to override an imported one's indent setting
// without the two being an error.
//
// The check itself is deferred to checkOutputConflicts, because the escape
// clause looks forward: a module is compiled before the module that imports
// it, so the higher-precedence declaration that settles a conflict is not
// seen until after the conflicting pair. Reporting on sight refused
// output-0175, where two imported modules disagree and the importer resolves
// them.
func (c *compiler) recordOutputAttrs(el *xdm.Node, precedence int) {
	definition := ""
	if name := el.AttrValue("name"); name != "" {
		// An unresolvable name is reported where the declaration is
		// otherwise compiled; here it only groups declarations, and the
		// lexical form groups them just as well.
		if qn, err := resolveQNameAttr(el, name); err == nil {
			definition = qn.Clark()
		} else {
			definition = name
		}
	}
	if c.outputAttrs == nil {
		c.outputAttrs = map[string]map[string][]outputAttrDecl{}
	}
	seen := c.outputAttrs[definition]
	if seen == nil {
		seen = map[string][]outputAttrDecl{}
		c.outputAttrs[definition] = seen
	}
	for _, a := range el.Attrs {
		if a.Name.URI != "" || a.Name.Local == "name" {
			continue
		}
		switch a.Name.Local {
		case "cdata-section-elements", "use-character-maps":
			continue
		}
		seen[a.Name.Local] = append(seen[a.Name.Local],
			outputAttrDecl{value: a.Value, precedence: precedence})
	}
}

// checkOutputConflicts reports XTSE1560 once every module has been compiled.
func (c *compiler) checkOutputConflicts() error {
	// The definitions and their attributes are walked in sorted order so
	// that a stylesheet with two independent conflicts reports the same one
	// on every run; a map walk would pick either.
	for _, definition := range sortedKeys(c.outputAttrs) {
		attrs := c.outputAttrs[definition]
		for _, name := range sortedKeys(attrs) {
			decls := attrs[name]
			highest := decls[0].precedence
			for _, d := range decls {
				if d.precedence > highest {
					highest = d.precedence
				}
			}
			// Only the declarations at the highest precedence can conflict:
			// any lower one is overridden by them, which is exactly the
			// escape the clause gives.
			var value string
			first := true
			for _, d := range decls {
				if d.precedence != highest {
					continue
				}
				if first {
					value, first = d.value, false
					continue
				}
				if d.value != value {
					return fmt.Errorf(
						"XTSE1560: two xsl:output declarations give %s "+
							"different values (%q and %q) at the same import "+
							"precedence", name, value, d.value)
				}
			}
		}
	}
	return nil
}

// sortedKeys returns a map's keys in order, so that a check over a map
// reports the same finding on every run.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// outputAttrDecl records one serialisation attribute as declared, for the
// XTSE1560 check. The precedence is kept because a higher one silences the
// conflict rather than participating in it.
type outputAttrDecl struct {
	value      string
	precedence int
}

// applyOutputAttrs reads the serialisation attributes from el into o.
//
// It is shared by xsl:output and xsl:result-document, which accept the same
// set: the instruction may override method, indent, encoding and the rest for
// one document. Reading them in two places would let the two drift.
func applyOutputAttrs(el *xdm.Node, o *OutputSettings) error {
	return applyOutputValues(el, el.AttrValue, o)
}

// applyOutputValues is applyOutputAttrs with the values supplied by the
// caller.
//
// xsl:result-document's serialisation attributes are attribute value
// templates, so their effective values are only known when the instruction
// runs; el is still needed because use-character-maps and
// cdata-section-elements name QNames that resolve against the stylesheet's
// namespace context.
func applyOutputValues(el *xdm.Node, value func(string) string, o *OutputSettings) error {
	if v := value("method"); v != "" {
		o.Method = v
	}
	if v := value("indent"); v != "" {
		o.Indent = v == "yes"
	}
	if v := value("omit-xml-declaration"); v != "" {
		o.OmitXMLDecl = v == "yes"
	}
	if v := value("encoding"); v != "" {
		o.Encoding = v
	}
	// A zero-length value for these two overrides an inherited one to
	// absent, which is the only way a stylesheet can cancel a doctype set by
	// a module it imports. So presence is tested rather than non-emptiness.
	if el.Attr("", "doctype-public") != nil {
		o.DocTypePublic = value("doctype-public")
	}
	if el.Attr("", "doctype-system") != nil {
		o.DocTypeSystem = value("doctype-system")
	}
	if v := value("standalone"); v != "" {
		// "omit" is the way to say "no standalone declaration", so it is
		// normalised to the absent state rather than written out literally.
		if v == "omit" {
			v = ""
		}
		o.Standalone = v
	}
	if v := value("version"); v != "" {
		o.Version = v
	}
	if v := value("byte-order-mark"); v != "" {
		o.ByteOrderMark = v == "yes"
	}
	if v := value("include-content-type"); v != "" {
		b := v == "yes"
		o.IncludeContentType = &b
	}
	if v := value("escape-uri-attributes"); v != "" {
		b := v == "yes"
		o.EscapeURIAttributes = &b
	}
	if v := value("undeclare-prefixes"); v != "" {
		o.UndeclarePrefixes = v == "yes"
	}
	if v := value("media-type"); v != "" {
		o.MediaType = v
	}
	if v := value("normalization-form"); v != "" {
		o.NormalizationForm = v
	}
	if v := value("use-character-maps"); v != "" {
		for _, n := range strings.Fields(v) {
			qn, err := resolveQNameAttr(el, n)
			if err != nil {
				return err
			}
			o.UseCharacterMaps = append(o.UseCharacterMaps, qn)
		}
	}
	if v := value("cdata-section-elements"); v != "" {
		for _, n := range strings.Fields(v) {
			// An unprefixed name here takes the default namespace rather
			// than no namespace. These names refer to elements in the result
			// document, so they follow the convention of a literal result
			// element rather than the one for QNames elsewhere in a
			// stylesheet — the specification calls the difference out.
			qn, err := resolveResultQNameAttr(el, n)
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
	// An absent @collation falls back to the default collation in force at
	// the declaration, which is what [xsl:]default-collation sets.
	keyColl := el.AttrValue("collation")
	if keyColl == "" {
		keyColl = ns.collation
	}
	// XTSE1210: "it is a static error if the xsl:key declaration has a
	// collation attribute whose value ... is not a URI recognized by the
	// implementation as referring to a collation." Only the explicit
	// attribute is checked here — an unrecognised *default* collation is
	// XTSE0125 and belongs where the default is declared.
	if v := strings.TrimSpace(el.AttrValue("collation")); v != "" {
		if _, err := xpath.ResolveCollation(v); err != nil {
			return fmt.Errorf(
				"XTSE1210: xsl:key/@collation=%q is not a collation this "+
					"implementation recognises", v)
		}
	}
	// XTSE1220: "it is a static error if there are several xsl:key
	// declarations in the stylesheet with the same key name and different
	// effective collations." A key is one index, and two declarations that
	// disagree about how its values compare cannot both be honoured.
	if prev, ok := c.keyCollations[qn.Clark()]; ok && prev != keyColl {
		return fmt.Errorf(
			"XTSE1220: the xsl:key declarations named %s specify different "+
				"collations (%q and %q)", qn.Lexical(), prev, keyColl)
	}
	if c.keyCollations == nil {
		c.keyCollations = map[string]string{}
	}
	c.keyCollations[qn.Clark()] = keyColl

	k := &keyDef{match: pat, collation: keyColl}
	hasBody := len(el.ChildElements()) > 0
	switch {
	case use != "" && hasBody:
		// Section 16.3: giving the value both ways leaves no rule for
		// reconciling them.
		return fmt.Errorf(
			"XTSE1205: xsl:key has both a use attribute and a sequence constructor")
	case use != "":
		if k.use, err = compileExpr(use, ns); err != nil {
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
func (c *compiler) compileFunction(el *xdm.Node, precedence int) error {
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
		// XTSE0580: two parameters of a function may not share a name.
		for _, prev := range params {
			if prev.Name.Clark() == p.Name.Clark() {
				return fmt.Errorf(
					"XTSE0580: xsl:function %s has two parameters named %s",
					qn.Lexical(), p.Name.Lexical())
			}
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
	// XTSE0770: two functions may not share a name and arity at the same
	// import precedence. A higher precedence legitimately overrides a lower
	// one, so only a tie is an error.
	key := fmt.Sprintf("%s#%d", qn.Clark(), len(params))
	if c.funcPrecedence == nil {
		c.funcPrecedence = map[string]int{}
	}
	if prev, dup := c.funcPrecedence[key]; dup && prev == precedence {
		return fmt.Errorf(
			"XTSE0770: two functions are named %s with %d arguments at the "+
				"same import precedence", qn.Lexical(), len(params))
	}
	c.funcPrecedence[key] = precedence

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
		case strings.HasPrefix(n, "*:"):
			// "*:local" matches that local name in any namespace. It is a
			// wildcard rather than a prefixed name, so resolving "*" as a
			// prefix reported an unbound-prefix error for a legal token.
			qn = xdm.QName{URI: "*", Local: strings.TrimPrefix(n, "*:")}
		case strings.HasSuffix(n, ":*"):
			prefix := strings.TrimSuffix(n, ":*")
			uri, ok := el.LookupPrefix(prefix)
			if !ok {
				return fmt.Errorf("XTSE0280: unbound prefix %q in %s/@elements",
					prefix, el.Name.Lexical())
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

// hoistImportSchema processes every xsl:import-schema reachable from a module,
// following xsl:include and xsl:import to do it.
//
// It runs before any expression is compiled, so that the whole stylesheet's
// schema is in the static context of every module. The traversal is guarded
// against cycles by the same seen-set the ordinary module walk uses; a module
// that cannot be resolved is passed over silently here, because the ordinary
// walk will report it with the right error code and reporting it twice would
// change which error the caller sees.
func (c *compiler) hoistImportSchema(root *xdm.Node) error {
	if c.schemaSeen == nil {
		c.schemaSeen = map[string]bool{}
	}
	for _, el := range root.ChildElements() {
		if el.Name.URI != xdm.NSXSL {
			continue
		}
		switch el.Name.Local {
		case "import-schema":
			if err := c.compileImportSchema(el); err != nil {
				return err
			}
		case "include", "import":
			href := el.AttrValue("href")
			if href == "" || c.opts.Resolver == nil {
				continue
			}
			base := el.BaseURI
			if base == "" {
				base = c.opts.BaseURI
			}
			if i := strings.IndexByte(href, '#'); i >= 0 {
				href = href[:i]
			}
			doc, resolved, err := c.opts.Resolver.ResolveModule(href, base)
			if err != nil || doc == nil || c.schemaSeen[resolved] {
				continue
			}
			c.schemaSeen[resolved] = true
			sub := firstElement(doc)
			if sub == nil {
				continue
			}
			if err := c.hoistImportSchema(sub); err != nil {
				return err
			}
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
	// A fragment identifier selects a part *within* the retrieved resource,
	// so it is not part of the name of the resource to fetch. Passing it
	// through made the resolver look for a file whose name contained "#".
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	doc, resolved, err := c.opts.Resolver.ResolveModule(href, base)
	if err != nil {
		// XTSE0165: the processor could not retrieve the resource the href
		// names, or what it retrieved is not a stylesheet module.
		return fmt.Errorf("XTSE0165: %s %q: %w", el.Name.Lexical(), href, err)
	}
	if c.seen[resolved] {
		// A module that includes or imports itself, directly or indirectly,
		// has its own error code, and they differ between the two: XTSE0180
		// for xsl:include, XTSE0210 for xsl:import.
		code := "XTSE0180"
		if el.Name.Local == "import" {
			code = "XTSE0210"
		}
		return fmt.Errorf("%s: circular %s of %q", code, el.Name.Local, resolved)
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

// collectPrefixes records every namespace prefix declared in a module.
//
// A prefix declared twice for different URIs keeps the first, which is
// arbitrary but harmless: this map exists only so that a name computed at run
// time can be expanded at all, and a stylesheet that binds one prefix two ways
// and then computes a name with it has not said which it meant.
func collectPrefixes(n *xdm.Node, into map[string]string) {
	if n.Kind == xdm.KindElement {
		for _, ns := range n.Namespaces {
			p := ns.Name.Local
			if p == "" {
				continue
			}
			if _, seen := into[p]; !seen {
				into[p] = ns.Value
			}
		}
	}
	for _, c := range n.Children {
		collectPrefixes(c, into)
	}
}

// checkInputTypeAnnotations applies XTSE0265 across the stylesheet's modules.
//
// "It is a static error if there is a stylesheet module in the stylesheet that
// specifies input-type-annotations="strip" and another stylesheet module that
// specifies input-type-annotations="preserve"." The two say opposite things
// about the source document, and the attribute is a property of the whole
// stylesheet rather than of the module it appears on, so there is no rule of
// precedence that could reconcile them — unlike almost every other conflict
// between modules, which import precedence settles.
//
// The third value, "unspecified", is the default and participates in no
// conflict, which is why only the two named ones are recorded.
func (c *compiler) checkInputTypeAnnotations(doc *xdm.Node) error {
	root := firstElement(doc)
	if root == nil || root.Name.URI != xdm.NSXSL {
		return nil
	}
	a := root.Attr("", "input-type-annotations")
	if a == nil {
		return nil
	}
	v := strings.TrimSpace(a.Value)
	if v != "strip" && v != "preserve" {
		return nil
	}
	if c.inputTypeAnnotations != "" && c.inputTypeAnnotations != v {
		return fmt.Errorf(
			"XTSE0265: one stylesheet module specifies "+
				"input-type-annotations=%q and another specifies %q",
			c.inputTypeAnnotations, v)
	}
	c.inputTypeAnnotations = v
	return nil
}
