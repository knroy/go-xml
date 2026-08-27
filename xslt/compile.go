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
	// importDepth counts how many xsl:import declarations the compiler is
	// currently inside.
	//
	// XTSE3008 forbids xsl:use-package in a module reached by import, and
	// only by import: xsl:include is explicitly fine, because an included
	// module is part of the including one and shares its package, while an
	// imported module is a separate stylesheet whose manifest would belong
	// to nobody. A depth rather than a flag, since imports nest.
	importDepth int
	// usedPackageDepth counts how many used packages the compiler is inside.
	//
	// A used package's declarations rank below the using package's whatever
	// numeric precedence they were allocated: precedence rises with each
	// module compiled, and a used package compiles last, so the number alone
	// says the opposite of what section 3.5 requires.
	usedPackageDepth int
	// schemaHoisted records that the xsl:import-schema pre-pass has run, so
	// that the recursion for each included module does not repeat it from a
	// root that sees less than the first one did.
	schemaHoisted bool
	// aliasDecls records each xsl:namespace-alias with its import precedence,
	// for XTSE0810; charMapPrecedence does the same for xsl:character-map and
	// XTSE1580. Both are needed because the stylesheet keeps only the winning
	// declaration, which cannot say whether a losing one conflicted.
	aliasDecls        map[string]aliasDecl
	charMapPrecedence map[string]int

	// usedAttributeSets collects every name a use-attribute-sets attribute
	// refers to, for XTSE0710.
	usedAttributeSets []xdm.QName

	// patternFuncs collects every function call written in a match pattern,
	// for XPST0017. It is checked after every module has compiled, because an
	// xsl:function the pattern calls may be declared below it or in a module
	// imported afterwards.
	patternFuncs []patternFuncRef

	// varFuncs collects every function named in a variable's select
	// expression, for XPST0017, and is checked at the same point and for the
	// same reason as patternFuncs. See varfuncs.go.
	varFuncs []varFuncRef

	// inputTypeAnnotations is the value the modules so far have agreed on,
	// for XTSE0265. Empty means no module has stated one.
	inputTypeAnnotations string

	// keyCollations records the effective collation of each xsl:key name, for
	// XTSE1220.
	keyCollations map[string]string
	// keyComposites is the same for @composite, which XTSE1222 requires every
	// declaration of one key name to agree on.
	keyComposites map[string]bool
	// stripDecls and preserveDecls are the NameTests of every xsl:strip-space
	// and xsl:preserve-space with the precedence XTSE0270 compares. The
	// conflict is between two declarations that may be in different modules
	// and compiled in either order, so it is settled once at the end.
	stripDecls, preserveDecls []spaceDecl
	// accumPrecedence records the import precedence each accumulator name was
	// declared at, so that XTSE3350 fires only on a tie. See accumulator.go.
	accumPrecedence map[string]int
	// accumTies records every precedence each accumulator name was declared
	// at, and accumNames its lexical form for the diagnostic. XTSE3350 is
	// judged over these once every module has been compiled; see
	// checkAccumulatorConflicts.
	accumTies  map[string][]int
	accumNames map[string]xdm.QName
	// modeTies records every xsl:mode declaration of each mode: the import
	// precedence it was made at, and the normalised value of each attribute
	// it explicitly states. XTSE0545 is judged over these once every module
	// has been compiled; see checkModeConflicts.
	modeTies map[string][]modeDecl
	// modeVisibility records xsl:mode/@visibility per mode at the precedence
	// it was declared at. It decides which modes an invocation may name as
	// its initial mode; see modevisibility.go.
	modeVisibility map[string]modeVisibility

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
	// staticVars holds the value of every static variable and parameter in
	// the stylesheet, in tree order, as computed by the static phase; see
	// static.go. staticDone records the module trees that phase already
	// walked, so that compileModule does not repeat conditional inclusion
	// over one and evaluate its use-when expressions a second time.
	staticVars []staticVar
	staticDone map[*xdm.Node]bool

	// nextPrecedence allocates import precedence numbers. See compileModule:
	// precedence is a total order over the import tree rather than a depth,
	// so every module gets its own number in post-order.
	nextPrecedence int

	// lowPrecedence is the lowest import precedence reachable from the module
	// currently being compiled, i.e. the value nextPrecedence held when that
	// module's compilation began. Because a module's whole import tree is
	// numbered before the module itself takes a number, the tree occupies the
	// contiguous interval [lowPrecedence, precedence). Templates record it so
	// that xsl:apply-imports can ask "is this template in MY import tree"
	// rather than merely "is it below me"; see Template.lowPrecedence.
	//
	// Kept on the compiler and saved/restored around each module rather than
	// threaded through compileTopLevel, whose precedence parameter is already
	// passed to a dozen declaration compilers that have no use for a second
	// one.
	lowPrecedence int

	// funcPrecedence records the import precedence each function name and
	// arity was declared at, for XTSE0770.
	funcPrecedence map[string]int

	// preNumbered records the modules the include pre-pass has already
	// compiled. They must not be compiled a second time by the ordinary
	// walk: the second visit would allocate a *higher* precedence than the
	// including module already took, and an import that exists to be
	// overridden would then win. See numberIncludedImports.
	preNumbered map[string]bool
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
			// 3.8: in backwards-compatible mode an xsl:with-param naming a
			// parameter the template does not declare is ignored rather than
			// being XTSE0680. XSLT 1.0 had no such error, and every 1.0
			// stylesheet that passes a parameter defensively -- the DocBook
			// stylesheets do it throughout -- would fail to compile at all if
			// the rule applied to them. backwards-013 states it directly.
			if call.compat {
				continue
			}
			return fmt.Errorf(
				"XTSE0680: xsl:call-template passes parameter $%s to template %s, "+
					"which does not declare it",
				p.Name.Lexical(), call.name.Lexical())
		}
		// XTSE0690 is the mirror image, and static for the same reason: the
		// call names the template, so both sides are known at compile time.
		// A required non-tunnel parameter the call does not supply can never
		// be supplied, whichever way the transformation runs. Leaving it to
		// the runtime reported XTDE0700 instead, and only if the call was
		// actually reached.
		supplied := map[string]bool{}
		for _, p := range call.params {
			if !p.Tunnel {
				supplied[p.Name.Clark()] = true
			}
		}
		for _, p := range t.Params {
			if !p.Required || p.Tunnel || supplied[p.Name.Clark()] {
				continue
			}
			return fmt.Errorf(
				"XTSE0690: xsl:call-template does not supply required "+
					"parameter $%s of template %s",
				p.Name.Lexical(), call.name.Lexical())
		}
	}
	return nil
}

// compileDocument compiles one stylesheet module at the given import
// precedence.
func (c *compiler) compileDocument(doc *xdm.Node, precedence int) error {
	return c.compileModule(doc, precedence, false)
}

// compileIncludedDocument compiles an xsl:include's target, whose top-level
// declarations take the including module's precedence.
func (c *compiler) compileIncludedDocument(doc *xdm.Node, precedence int) error {
	return c.compileModule(doc, precedence, true)
}

func (c *compiler) compileModule(doc *xdm.Node, precedence int, fixed bool) error {
	// An xsl:include's declarations behave as if written in the including
	// module, so they belong to the includer's import tree and keep its low
	// end, exactly as they keep its precedence. A module reached by
	// xsl:import starts a tree of its own at whatever number is next free.
	if !fixed {
		saved := c.lowPrecedence
		c.lowPrecedence = c.nextPrecedence
		defer func() { c.lowPrecedence = saved }()
	}
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
		// @default-mode on the principal module's own root names a mode the
		// package expects to be entered in, which makes it an eligible
		// initial mode however private it is; see modevisibility.go. Only
		// this root counts, so it is read here rather than at each use.
		if dm, err := defaultModeAt(root); err == nil {
			c.sheet.rootDefaultMode = dm
		}
		c.sheet.isPackage = isXSL(root, "package")
		// The principal module's declared version decides the default output
		// method for the implicit result tree. Under backwards compatibility
		// there is no xhtml method to default to, so an XHTML-rooted result
		// serialises as xml. Recorded here rather than at serialisation time
		// because only this module's version counts: an included 1.0 module
		// does not put a 2.0 stylesheet into backwards-compatible output.
		version := root.AttrValue("version")
		if version == "" {
			if a := root.Attr(xdm.NSXSL, "version"); a != nil {
				version = a.Value
			}
		}
		c.sheet.output.Version10Implicit = strings.TrimSpace(version) == "1.0"
	}

	// Conditional element inclusion runs before anything else looks at the
	// tree. An excluded element must produce no error at all, so it has to be
	// gone before compilation can object to it. The static phase does it, for
	// the whole module graph at once, because a use-when may name a static
	// variable declared in another module and the two have to be evaluated in
	// one traversal; see static.go. A module it has not reached — a nested
	// include whose href it could not resolve, say — is pruned here so that
	// no tree ever reaches the grammar checks unpruned.
	// XTSE3440 and XTSE3460 are read off the tree before the static phase,
	// because both are purely structural: they ask about the shape of an
	// xsl:override's template rules rather than about anything composition
	// computes, so reading them here costs nothing and needs no package to
	// have been resolved.
	if err := checkOverrideTemplates(root); err != nil {
		return err
	}
	if !c.staticDone[doc] {
		if err := c.runStaticPhase(doc); err != nil {
			return err
		}
	}

	// XSLT 3.0 added xsl:package. A host running as an XSLT 2.0 processor has
	// to refuse it, and the stylesheet's own @version cannot say which it is —
	// a 3.0 package routinely declares version="2.0" — so the cap decides.
	// It is checked before the grammar rules, because those know xsl:package
	// and would report a rule *inside* it rather than the element itself.
	if isXSL(root, "package") && c.opts.MaxVersion != 0 && c.opts.MaxVersion < 3.0 {
		return fmt.Errorf(
			"xsl:package is not an XSLT %g element (XTSE0010)", c.opts.MaxVersion)
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
	if root.Name.URI != xdm.NSXSL || !isStylesheetRootName(root.Name.Local) {
		// XTSE0150 names this exact condition: "a literal result element that
		// is used as the outermost element of a simplified stylesheet module
		// must have an xsl:version attribute". The unprefixed spelling does
		// not count — on a non-XSLT element the attribute must be in the XSLT
		// namespace for it to be the stylesheet's version rather than an
		// ordinary attribute of the output.
		if root.Attr(xdm.NSXSL, "version") == nil {
			return fmt.Errorf(
				"XTSE0150: %s is the outermost element of a simplified "+
					"stylesheet and has no xsl:version attribute",
				root.Name.Lexical())
		}
		if !fixed {
			precedence = c.nextPrecedence
			c.nextPrecedence++
		}
		return c.compileSimplifiedStylesheet(root, precedence)
	}

	// Section 3.9: a version greater than 2.0 is not an error — it enables
	// forwards-compatible processing, under which unknown XSLT constructs are
	// ignored or handled by xsl:fallback. Rejecting such a stylesheet outright
	// contradicts the whole point of the mechanism. Whether the value is a
	// number at all is XTSE0110, checked in checkStylesheetElement, so nothing
	// remains to reject here.

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
	// Once, from the outermost module. compileDocument recurses for every
	// xsl:include and xsl:import, and re-running the pre-pass there would
	// gather only what *that* module can reach — so an expression in an
	// imported module naming a type the importing module declared was
	// XPST0051, even though the components are a property of the stylesheet
	// rather than of the module that declared them.
	if !c.schemaHoisted {
		c.schemaHoisted = true
		if err := c.hoistImportSchema(root); err != nil {
			return err
		}
	}
	compileSchema = c.sheet.schema

	// Package composition runs before this module's own declarations are
	// compiled, so that a component accepted from a used package is in scope
	// for every reference regardless of where the xsl:use-package sits:
	// section 3.5 puts no ordering constraint on the manifest beyond
	// xsl:import coming first.
	if err := c.compileUsePackages(root, precedence); err != nil {
		return err
	}

	// XTSE1520 and XTSE1530 need the in-scope schema components, which only
	// exist once xsl:import-schema has been hoisted, so the type attributes
	// are checked here rather than with the rest of the static rules.
	if err := checkTypeAttributes(root, c.sheet.schema); err != nil {
		return err
	}

	// Import precedence is a total order over the import tree, not a depth.
	// Section 3.10.2: a module has higher precedence than every module it
	// imports, and among the modules a stylesheet imports directly the later
	// xsl:import has higher precedence than the earlier one. Numbering by
	// depth alone gave two sibling imports the same precedence, which turned
	// a legitimate override into a spurious XTSE0630/XTSE0660 and let the
	// first declaration win where the second should have.
	//
	// A post-order walk produces exactly that order: every module an import
	// pulls in is numbered before the importer, and the imports are visited
	// in document order, so the counter rises with precedence. Section 3.7
	// requires xsl:import to come before every other declaration, so the
	// imports are all reached in this first pass before anything else needs
	// a number.
	children := root.ChildElements()
	for _, el := range children {
		if isXSL(el, "import") {
			if err := c.compileInclude(el, 0); err != nil {
				return err
			}
		}
	}
	// An xsl:include's own xsl:import declarations are numbered here too,
	// before this module takes its number.
	//
	// The included module's declarations share this module's precedence, so
	// anything that module imports must rank *below* it. Numbering those
	// imports where the include is compiled — after this module already took
	// a number — gave them a higher one instead, and an import that exists to
	// be overridden then won. The traversal reaches through nested includes
	// for the same reason.
	if err := c.numberIncludedImports(root); err != nil {
		return err
	}
	if !fixed {
		precedence = c.nextPrecedence
		c.nextPrecedence++
	}

	for _, el := range children {
		if el.Name.URI == xdm.NSXSL &&
			(el.Name.Local == "import-schema" || el.Name.Local == "import") {
			continue
		}
		if err := c.compileTopLevel(el, precedence); err != nil {
			return err
		}
	}
	// The template rules an xsl:override contributes take this module's
	// precedence, which is only settled above.
	if err := c.compileOverrideRules(root, precedence); err != nil {
		return err
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
		lowPrecedence:    c.lowPrecedence,
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
	case "global-context-item":
		return c.compileGlobalContextItem(el)
	case "template":
		return c.compileTemplate(el, precedence)
	case "variable", "param":
		// A static declaration was evaluated by the static phase, before
		// anything else in the stylesheet was looked at. Its select
		// expression is a static expression and does not compile in the
		// ordinary static context — it may name another static variable,
		// which is not a global — so the value the pass computed is taken
		// here rather than the expression recompiled.
		// The gate is the MODULE's version, not the declaration's own. A
		// version attribute on the declaration states a compatibility mode
		// for the expressions written on it, not whether static="yes" means
		// anything; the static phase already used moduleAtLeast30 to decide
		// that, and reading a different version here made the two walks
		// disagree -- static-015 writes version="1.0" on one of three
		// otherwise identical static parameters and requires all three to
		// keep the value the caller supplied.
		// The test is staticDeclAllowed's rather than the module version's so
		// that it admits exactly the declarations the static phase bound. A
		// declaration the phase evaluated but this arm rejected would be
		// compiled a second time as an ordinary global, and a parameter whose
		// value only ever came from the caller would then be reported unset:
		// function-1025's static param in a version="2.0" module did exactly
		// that.
		if staticDeclAllowed(el) {
			v, err := c.staticGlobal(el)
			if err != nil {
				return err
			}
			v.precedence = precedence
			c.sheet.globals = append(c.sheet.globals, v)
			return nil
		}
		v, err := c.compileVariable(el)
		if err != nil {
			return err
		}
		if el.Name.Local == "param" {
			v.IsParam = true
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
		return c.compileSpaceControl(el, precedence)
	case "include":
		// An included module's declarations behave as if written in the
		// including module, so they share its precedence. compileDocument
		// allocates a fresh number for every module it compiles, so the
		// included module's own declarations are given the includer's
		// precedence explicitly here rather than through the recursion.
		return c.compileIncludeAt(el, precedence)
	case "import":
		// Imports are compiled in the first pass of compileDocument, which
		// numbers them, so nothing is left to do here.
		return nil
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
	case "accumulator":
		return c.compileAccumulator(el, precedence)
	case "mode":
		// @streamable requests a streaming evaluation, and a processor is
		// always free to answer that request by building the tree, which is
		// what this one does — so it is accepted and ignored. @on-no-match
		// is not ignorable: it selects which built-in template rules apply,
		// and the default (text-only-copy) produces visibly different output
		// from shallow-copy or deep-skip. Its attributes were checked by the
		// element table before this point.
		return c.compileMode(el, precedence)
	}
	// Section 3.9: where forwards-compatible behaviour is enabled, an XSLT
	// element that XSLT 2.0 does not allow as a child of xsl:stylesheet "must
	// be ignored" — and its content with it, so nothing inside is compiled or
	// checked either.
	if forwardsMode(el) {
		return nil
	}
	// An unrecognised xsl: element at the top level is an error, not something
	// to skip. The spec reserves the whole namespace, so anything unknown in
	// it is either a typo — "xsl:tempalte" would otherwise be dropped and the
	// stylesheet would run producing quietly wrong output — or a version of
	// XSLT this engine does not implement. Either way the author needs to know.
	return fmt.Errorf(
		"unknown top-level element xsl:%s (XTSE0010)", el.Name.Local)
}

// patternFuncRef is one function call found in a match pattern, recorded with
// enough context to resolve and report it once compilation has finished.
type patternFuncRef struct {
	name  xdm.QName
	arity int
	pat   string
}

// notePatternFuncs records the function calls written in a pattern so that
// checkPatternFuncs can resolve them later.
//
// Section 5.5.3 makes a pattern a restricted path expression, and a call in a
// predicate is an ordinary function call subject to the ordinary static rule:
// XPST0017 if no function of that name and arity is in scope. Nothing else
// catches it. A pattern is only ever *matched*, never evaluated for a value,
// and a template whose pattern never matches anything is silently skipped — so
// an undeclared function in a predicate would otherwise turn a stylesheet the
// author got wrong into one that quietly produces the fallback output.
//
// Names that cannot be resolved to a URI here are dropped rather than
// reported: an unbound prefix is XTSE0080/XPST0081 and is diagnosed where
// prefixes are resolved, and guessing at it here would report the wrong code.
func (c *compiler) notePatternFuncs(pat string, ns *nsResolver) {
	// patternFuncCalls counts arguments by scanning the text, so a comment
	// between the parentheses reads as one: match-246a writes "true((:2:))",
	// which is a nullary call the scan saw as unary.
	for _, call := range patternFuncCalls(stripXPathComments(pat)) {
		prefix, local := splitPatternQName(call.name)
		uri := xdm.NSFN
		if prefix != "" {
			bound, ok := ns.bindings[prefix]
			if !ok {
				continue
			}
			uri = bound
		}
		c.patternFuncs = append(c.patternFuncs, patternFuncRef{
			name:  xdm.QName{Prefix: prefix, URI: uri, Local: local},
			arity: call.arity,
			pat:   pat,
		})
	}
}

// checkPatternFuncs reports XPST0017 for a function called in a match pattern
// that no function library and no xsl:function declares.
//
// It runs after every module has compiled, for the same reason XTSE0710 does:
// the declaration may come later than the use.
func (c *compiler) checkPatternFuncs() error {
	for _, r := range c.patternFuncs {
		if r.name.URI == xdm.NSFN && runtimeFuncNames[r.name.Local] {
			// Bound per transform rather than at compile time; see
			// runtimeFuncNames.
			continue
		}
		if _, ok := c.sheet.funcs.Lookup(r.name, r.arity); ok {
			continue
		}
		return fmt.Errorf(
			"XPST0017: pattern %q calls %s with %d argument(s), but no "+
				"function is declared with that name and arity",
			r.pat, r.name.Lexical(), r.arity)
	}
	return nil
}

func (c *compiler) compileTemplate(el *xdm.Node, precedence int) error {
	t := &Template{importPrecedence: precedence, lowPrecedence: c.lowPrecedence}
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
		c.notePatternFuncs(m, ns)
	}
	if n := el.AttrValue("name"); n != "" {
		qn, err := resolveQNameAttr(el, n)
		if err != nil {
			return err
		}
		t.Name, t.HasName = qn, true
		c.recordTemplateVisibility(el, qn)
	}
	if err := checkEntryVisibility(el); err != nil {
		return err
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
	explicitPriority := false
	if p := el.AttrValue("priority"); p != "" {
		explicitPriority = true
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
			if tok == "#all" || tok == "#default" || tok == "#unnamed" {
				continue
			}
			if !isLexicalQName(tok) && !isEQName(tok) {
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
			if tok == "#all" {
				continue
			}
			// "#unnamed" always names the unnamed mode; "#default" names
			// whatever [xsl:]default-mode puts in scope here, which is the
			// unnamed mode when none is. Both are resolved now rather than
			// left as tokens, because the mode a rule belongs to is a static
			// property of where the rule was written.
			if tok == "#unnamed" {
				modes[i] = ""
				continue
			}
			if tok == "#default" {
				dm, err := defaultModeAt(el)
				if err != nil {
					return err
				}
				modes[i] = dm
				continue
			}
			qn, err := resolveQNameAttr(el, tok)
			if err != nil {
				return err
			}
			modes[i] = xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()
		}
		t.Mode = modes
	} else if dm, err := defaultModeAt(el); err != nil {
		return err
	} else if dm != "" {
		// A rule with no @mode belongs to the default mode, which
		// [xsl:]default-mode may have moved off the unnamed one. Leaving
		// Mode empty here put the rule in the unnamed mode instead, so a
		// stylesheet that sets default-mode at the top and then writes plain
		// rules found none of them.
		t.Mode = []string{dm}
	}

	if as := el.AttrValue("as"); as != "" {
		at, err := compileSequenceType(as, newNSResolver(el, ""))
		if err != nil {
			return fmt.Errorf("in xsl:template/@as: %w", err)
		}
		t.asType = at
	}

	// Leading xsl:param children declare the template's parameters; they must
	// precede the body. An xsl:context-item, when present, precedes even
	// those: section 10.1.1 puts it first among the children.
	children := el.ChildElements()
	i := 0
	// Non-whitespace text before the declaration starts the sequence
	// constructor, so an xsl:context-item after it is no longer first in the
	// content model even though it is the first child ELEMENT.
	// context-item-908 writes "banana" above one and expects XTSE0010.
	textFirst := false
	for _, ch := range el.Children {
		if ch.Kind == xdm.KindElement {
			break
		}
		if ch.Kind == xdm.KindText && !xdm.IsXMLWhitespace(ch.Value) {
			textFirst = true
			break
		}
	}
	if !textFirst && i < len(children) && isXSL(children[i], "context-item") {
		ci, err := compileContextItem(children[i], el)
		if err != nil {
			return err
		}
		t.contextItem = ci
		i++
	}
	// XTSE0010: the content model is (xsl:context-item?, xsl:param*,
	// sequence-constructor), so one anywhere but first is out of place.
	// Ignoring it silently accepted a template that declared a required
	// context item and then never checked for one.
	for _, ch := range children[i:] {
		if isXSL(ch, "context-item") {
			return fmt.Errorf(
				"xsl:template may not contain xsl:context-item here: its " +
					"content is (xsl:context-item?, xsl:param*, " +
					"sequence-constructor) (XTSE0010)")
		}
	}
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
		// A union pattern is several template rules sharing one body: each
		// branch gets its own default priority and its own place in
		// declaration order, so that a later branch wins a tie against an
		// earlier one and xsl:next-match can re-select the rule on a
		// different branch. Sharing the body keeps the params and sequence
		// constructor compiled once.
		// An explicit priority is stated once for the whole rule, which keeps
		// the union fused: next-match-024 writes the same union as
		// next-match-023 with priority="1" and expects the body to run once,
		// not once per branch.
		alts := []*Pattern{t.Match}
		if !explicitPriority {
			alts = t.Match.Alternatives()
		}
		group := t.declOrder
		for i, alt := range alts {
			rule := t
			if i > 0 {
				copyRule := *t
				rule = &copyRule
				c.declOrder++
				rule.declOrder = c.declOrder
			}
			rule.unionGroup = group
			rule.Match = alt
			if !explicitPriority {
				rule.Priority = alt.Priority()
			}
			c.sheet.templates = append(c.sheet.templates, rule)
		}
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
		Required: yesAttr(el, "required"),
		Tunnel:   yesAttr(el, "tunnel"),
		baseURI:  el.BaseURI,
	}
	if as := el.AttrValue("as"); as != "" {
		t, err := compileSequenceType(as, newNSResolver(el, ""))
		if err != nil {
			return nil, fmt.Errorf("in %s/@as: %w", el.Name.Lexical(), err)
		}
		v.asType = t
	}

	if sel := el.AttrValue("select"); sel != "" {
		comp, err := compileExpr(sel, newNSResolver(el, ""))
		if err != nil {
			return nil, fmt.Errorf("in %s/@select: %w", el.Name.Lexical(), err)
		}
		v.Select = comp
		c.noteVariableFuncs(comp, el.Name.Lexical()+" $"+qn.Lexical())
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
	if err := checkLiteralHTMLVersion(el); err != nil {
		return err
	}
	return applyOutputValues(el, el.AttrValue, o)
}

// checkLiteralHTMLVersion applies XTSE0020 to a literal @html-version that is
// not the xs:decimal the attribute is declared to be.
//
// It is the same rule and the same wording as any other attribute whose fixed
// value is outside the permitted set, and it is decided entirely from the
// stylesheet text -- so it is a compile-time check even on
// xsl:result-document, whose serialisation attributes are otherwise resolved
// when the instruction runs. result-document-0243 writes html-version="five"
// and expects the stylesheet to be rejected; deferring it meant nothing ever
// looked, because the effective value is only consulted by the html method
// and the document there is xhtml.
//
// Only a literal is checked: a value written with curly brackets is excluded
// by the error's own definition, being a value the stylesheet does not state.
func checkLiteralHTMLVersion(el *xdm.Node) error {
	v := el.AttrValue("html-version")
	if v == "" || strings.Contains(v, "{") {
		return nil
	}
	if _, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil {
		return fmt.Errorf(
			"XTSE0020: %s/@html-version value %q is not a decimal number",
			el.Name.Lexical(), v)
	}
	return nil
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
	// XSLT 3.0 declares every yes/no serialisation attribute as a boolean,
	// which admits true/false/1/0 as synonyms. The static check already lets
	// a 3.0 module write them; reading them here as anything other than
	// "yes" turned indent="true" into indent="no" and wrote
	// standalone="true" straight into the XML declaration, which is not a
	// value the declaration may carry at all.
	// The whitespace is collapsed first. These attributes are declared with
	// XSD types derived from xs:NMTOKEN, whose whitespace facet is
	// "collapse", so standalone=" false " is the boolean false and not an
	// unrecognised string -- and an attribute value template makes the
	// spelling a run-time value that no static check has trimmed.
	yes := func(v string) bool {
		v = strings.TrimSpace(v)
		if alias, ok := boolAliases[v]; ok {
			v = alias
		}
		return v == "yes"
	}
	if v := value("method"); v != "" {
		// @method is declared as an XSD type derived from xs:QName, whose
		// whitespace facet is "collapse", so method=" xhtml " names the
		// xhtml method -- output-0221 writes exactly that, with @html-version
		// spaced the same way. Keeping the spaces made every comparison
		// against "xhtml" fail and the document came out as plain XML.
		v = strings.TrimSpace(v)
		o.Method = v
	}
	if v := value("indent"); v != "" {
		o.Indent = yes(v)
	}
	if v := value("omit-xml-declaration"); v != "" {
		o.OmitXMLDecl = yes(v)
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
	// Presence rather than non-emptiness: item-separator="" is a meaningful
	// value (no separator anywhere, not even between atomic values), so it
	// must be distinguishable from the attribute being absent.
	if el.Attr("", "item-separator") != nil {
		v := value("item-separator")
		// "#absent" overrides a separator obtained from the output
		// definition and leaves the parameter unset. The rule exists because
		// item-separator="" is itself a meaningful value -- no separator
		// anywhere, not even the default space between atomic values -- so
		// there is no empty spelling left to mean "not set".
		if v == "#absent" {
			o.ItemSeparator = nil
		} else {
			o.ItemSeparator = &v
		}
	}
	if v := value("build-tree"); v != "" {
		b := yes(v)
		o.BuildTree = &b
	}
	if v := value("allow-duplicate-names"); v != "" {
		o.AllowDuplicateNames = yes(v)
	}
	if v := value("json-node-output-method"); v != "" {
		o.JSONNodeOutputMethod = strings.TrimSpace(v)
	}
	if v := value("parameter-document"); v != "" {
		o.ParameterDocument = strings.TrimSpace(v)
	}
	if v := value("standalone"); v != "" {
		// "omit" is the way to say "no standalone declaration", so it is
		// normalised to the absent state rather than written out literally.
		// The declaration itself admits only "yes" and "no", so a 3.0
		// module's true/false/1/0 is normalised to one of those.
		v = strings.TrimSpace(v)
		switch {
		case v == "omit":
			v = ""
		default:
			if alias, ok := boolAliases[v]; ok {
				v = alias
			}
		}
		o.Standalone = v
	}
	if v := value("version"); v != "" {
		o.Version = v
	}
	if v := value("html-version"); v != "" {
		o.HTMLVersion = strings.TrimSpace(v)
	}
	if v := value("byte-order-mark"); v != "" {
		o.ByteOrderMark = yes(v)
	}
	if v := value("include-content-type"); v != "" {
		b := yes(v)
		o.IncludeContentType = &b
	}
	if v := value("escape-uri-attributes"); v != "" {
		b := yes(v)
		o.EscapeURIAttributes = &b
	}
	if v := value("undeclare-prefixes"); v != "" {
		o.UndeclarePrefixes = yes(v)
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
	if err := c.checkKeyComposite(el, qn); err != nil {
		return err
	}

	k := &keyDef{
		match: pat, collation: keyColl,
		composite: isYes(el.AttrValue("composite")),
	}
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
		// colliding with future builtins. The EQName spelling Q{}local names
		// no namespace just as an unprefixed name does, so it is this error
		// and not a lexical one.
		return fmt.Errorf(
			"XTSE0740: xsl:function name %q must be in a namespace", name)
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

	c.recordFunctionVisibility(el, qn, len(params))

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
	// A named simple type in an imported schema brings its own constructor
	// function of the same expanded name and arity 1 into the static context
	// (XPath 2.0 section 3.1.5 / XSLT 2.0 section 3.14). A stylesheet function
	// that collides with one is the same clash XTSE0770 describes — two
	// functions of the same name, arity and import precedence — and the suite
	// says as much: type-functions-0503 expects XTSE0770 with the note "the
	// error isn't explicit in the XSLT spec, but this is the closest it gets".
	//
	// Restricted to arity 1 because that is the only arity a constructor
	// function has, and to *simple* types because complex types have no
	// constructor. The reserved-namespace check above already keeps a
	// stylesheet function out of the XSD namespace, so this can only ever
	// fire on a user-declared type in a namespace the stylesheet imported.
	if len(params) == 1 && c.sheet.schema != nil && c.sheet.schema.HasSimpleType(qn) {
		return fmt.Errorf(
			"XTSE0770: xsl:function %s conflicts with the constructor "+
				"function of the imported schema type of the same name",
			qn.Lexical())
	}
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
		Name:      qn,
		Arity:     len(params),
		Call:      fn.call,
		Signature: declaredSignature(fn.returns, params),
	})
	return nil
}

func (c *compiler) compileSpaceControl(el *xdm.Node, precedence int) error {
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
		case strings.HasPrefix(n, "Q{") && strings.HasSuffix(n, "}*"):
			// Q{uri}* is the braced-URI spelling of prefix:*. The NameTest
			// grammar admits it wherever it admits the prefixed form, and it
			// names its namespace directly, so no prefix need be in scope.
			qn = xdm.QName{URI: n[2 : len(n)-2], Local: "*"}
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
			// Section 5.2 lists the elements attribute of xsl:strip-space and
			// xsl:preserve-space among the places where an unprefixed element
			// name takes the effective [xsl:]xpath-default-namespace rather
			// than no namespace.
			if qn.Prefix == "" && qn.URI == "" {
				qn.URI = xpathDefaultNamespaceAt(el)
			}
		}
		// The precedence is kept alongside for XTSE0270, which only counts a
		// name appearing in both lists as a conflict when the two
		// declarations have the same import precedence.
		d := spaceDecl{name: qn, precedence: precedence}
		if el.Name.Local == "strip-space" {
			c.stripDecls = append(c.stripDecls, d)
			c.sheet.strip = append(c.sheet.strip, qn)
		} else {
			c.preserveDecls = append(c.preserveDecls, d)
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
			fragment := ""
			if i := strings.IndexByte(href, '#'); i >= 0 {
				fragment, href = href[i+1:], href[:i]
			}
			doc, resolved, err := c.opts.Resolver.ResolveModule(href, base)
			if err != nil || doc == nil || c.schemaSeen[resolved] {
				continue
			}
			c.schemaSeen[resolved] = true
			sub := embeddedModule(doc, fragment)
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

// compileIncludeAt compiles an xsl:include, whose declarations take the
// including module's precedence rather than one of their own.
func (c *compiler) compileIncludeAt(el *xdm.Node, precedence int) error {
	return c.compileIncludeImpl(el, precedence, true)
}

func (c *compiler) compileInclude(el *xdm.Node, precedence int) error {
	return c.compileIncludeImpl(el, precedence, false)
}

func (c *compiler) compileIncludeImpl(el *xdm.Node, precedence int, forcePrecedence bool) error {
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
	// It is kept, though: for xsl:include and xsl:import it names the
	// embedded module to take once the host document is in hand.
	fragment := ""
	if i := strings.IndexByte(href, '#'); i >= 0 {
		fragment, href = href[i+1:], href[:i]
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
	if c.preNumbered[resolved] && !forcePrecedence {
		// An xsl:import the include pre-pass already handled. It has its
		// number and its declarations; compiling it again here would give
		// it a second, higher one.
		return nil
	}
	c.seen[resolved] = true
	defer delete(c.seen, resolved)

	if fragment != "" {
		if sub := embeddedModule(doc, fragment); sub != nil {
			doc = sub
		}
	}
	if forcePrecedence {
		// xsl:include: the module's declarations take the includer's
		// precedence, so its own allocation is overridden afterwards. The
		// counter still advances, which only leaves a gap.
		return c.compileIncludedDocument(doc, precedence)
	}
	// Everything below this point is inside an xsl:import, which is what
	// XTSE3008 asks about. See importDepth.
	c.importDepth++
	defer func() { c.importDepth-- }()
	return c.compileDocument(doc, precedence)
}

// importHref is the resource half of an xsl:import href, without the fragment
// identifier that selects an embedded module within it.
func importHref(el *xdm.Node) string {
	href := el.AttrValue("href")
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	return href
}

// importBase is the base URI an xsl:import's href resolves against.
func importBase(c *compiler, el *xdm.Node) string {
	if el.BaseURI != "" {
		return el.BaseURI
	}
	return c.opts.BaseURI
}

// numberIncludedImports allocates import precedence for the modules an
// xsl:include chain imports, ahead of the including module's own number.
//
// It only assigns numbers; the declarations themselves are compiled later, by
// the ordinary walk. The seen-set is the same one the module walk uses, so a
// cycle is caught here rather than recursing forever, and a module already
// numbered is not numbered twice.
func (c *compiler) numberIncludedImports(root *xdm.Node) error {
	if c.opts.Resolver == nil {
		return nil
	}
	for _, el := range root.ChildElements() {
		if !isXSL(el, "include") {
			continue
		}
		href := el.AttrValue("href")
		if href == "" {
			continue
		}
		base := el.BaseURI
		if base == "" {
			base = c.opts.BaseURI
		}
		fragment := ""
		if i := strings.IndexByte(href, '#'); i >= 0 {
			fragment, href = href[i+1:], href[:i]
		}
		doc, resolved, err := c.opts.Resolver.ResolveModule(href, base)
		if err != nil || doc == nil {
			// A module that cannot be retrieved is reported by the ordinary
			// walk, with the error code the spec gives it. Reporting it here
			// as well would change which error the caller sees.
			continue
		}
		if c.seen[resolved] {
			continue
		}
		sub := embeddedModule(doc, fragment)
		if sub == nil {
			continue
		}
		if c.seen == nil {
			c.seen = map[string]bool{}
		}
		c.seen[resolved] = true
		for _, kid := range sub.ChildElements() {
			if !isXSL(kid, "import") {
				continue
			}
			if err := c.compileInclude(kid, 0); err != nil {
				delete(c.seen, resolved)
				return err
			}
			if c.preNumbered == nil {
				c.preNumbered = map[string]bool{}
			}
			if _, target, err := c.opts.Resolver.ResolveModule(
				importHref(kid), importBase(c, kid)); err == nil {
				c.preNumbered[target] = true
			}
		}
		err = c.numberIncludedImports(sub)
		delete(c.seen, resolved)
		if err != nil {
			return err
		}
	}
	return nil
}

// --- helpers ----------------------------------------------------------------

// embeddedModule finds the stylesheet module a fragment identifier names.
//
// Section 3.8 lets a stylesheet module live *inside* a host XML document
// rather than being the whole of one: xsl:include href="doc.xml#embedded"
// retrieves doc.xml and then takes the element whose ID is "embedded" as the
// module. Without this the document element is taken instead, and a host whose
// root is not xsl:stylesheet is diagnosed as a simplified stylesheet missing
// its xsl:version — which is what include-0102 and include-0103 reported.
//
// Both spellings of an ID are matched by name: xml:id is an ID by definition,
// and a plain id attribute is one only because an ATTLIST declares it so —
// which this engine does not record, so the name is taken at face value. That
// is looser than the spec, but a stylesheet naming a fragment that is not an
// ID is already malformed, and the host documents in the suite use one
// spelling each.
func embeddedModule(doc *xdm.Node, id string) *xdm.Node {
	if id == "" {
		return firstElement(doc)
	}
	var walk func(n *xdm.Node) *xdm.Node
	walk = func(n *xdm.Node) *xdm.Node {
		if n.Kind == xdm.KindElement {
			for _, a := range n.Attrs {
				if a.Value != id {
					continue
				}
				if a.Name.Local == "id" &&
					(a.Name.URI == "" || a.Name.URI == xdm.NSXML) {
					return n
				}
			}
		}
		for _, k := range n.Children {
			if got := walk(k); got != nil {
				return got
			}
		}
		return nil
	}
	if got := walk(doc); got != nil {
		return got
	}
	// A fragment that names nothing falls back to the document element, so
	// that a mis-typed identifier is reported by the ordinary module checks
	// rather than as a bare nil.
	return firstElement(doc)
}

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
	// "Stripping of type annotations takes place if at least one stylesheet
	// module in the stylesheet specifies input-type-annotations='strip'", so
	// the flag is set by any module that asks for it and never cleared by a
	// later one. A module saying "preserve" alongside one saying "strip" is
	// the XTSE0265 above, not a retraction.
	if v == "strip" && c.sheet != nil {
		c.sheet.stripTypeAnnotations = true
	}
	return nil
}

// pruneOverriddenGlobals drops every global variable a higher-precedence
// declaration of the same name overrides.
//
// Section 9.5: "if there is more than one binding of a global variable with
// the same name, then all but one of them must have lower import precedence".
// The lower-precedence bindings are not merely unused — they are not
// evaluated at all, so a lower-precedence binding whose select expression
// would fail is harmless. Keeping them in the list made evaluation order
// decide which value a global took, so a stylesheet that imported a module
// declaring the same parameter got the imported value.
func (c *compiler) pruneOverriddenGlobals() {
	best := make(map[string]int, len(c.sheet.globals))
	for _, g := range c.sheet.globals {
		k := g.Name.Clark()
		if p, ok := best[k]; !ok || g.precedence > p {
			best[k] = g.precedence
		}
	}
	kept := c.sheet.globals[:0]
	for _, g := range c.sheet.globals {
		if g.precedence == best[g.Name.Clark()] {
			kept = append(kept, g)
		}
	}
	c.sheet.globals = kept
}

// compileMode records the parts of an xsl:mode declaration this processor acts
// on. Only @on-no-match is one of them; see the switch that calls this.
//
// The mode name is expanded like any other, and an absent @name declares the
// unnamed mode, which is the empty Clark name. "#default" spells the same
// thing and "#unnamed" is its XSLT 3.0 synonym, so both normalise to it.
func (c *compiler) compileMode(el *xdm.Node, precedence int) error {
	name := ""
	if na := el.Attr("", "name"); na != nil {
		tok := strings.TrimSpace(na.Value)
		switch tok {
		case "", "#default", "#unnamed":
		default:
			qn, err := resolveQNameAttr(el, tok)
			if err != nil {
				return err
			}
			name = xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()
		}
	}
	if nm := el.Attr("", "on-no-match"); nm != nil {
		if c.sheet.modeNoMatch == nil {
			c.sheet.modeNoMatch = map[string]string{}
			c.sheet.modeNoMatchPrec = map[string]int{}
		}
		// Higher import precedence wins, and an equal one is the later
		// declaration in the same module, which the spec's conflict rule
		// also gives to the last. See modeNoMatchPrec.
		// A declaration inside a used package never displaces one from the
		// using package. Within either, the ordinary rule applies: higher
		// import precedence wins, and an equal one is the later declaration.
		rank := precedence
		if c.usedPackageDepth > 0 {
			rank = -1 - c.usedPackageDepth
		}
		if prev, seen := c.sheet.modeNoMatchPrec[name]; !seen || rank >= prev {
			c.sheet.modeNoMatch[name] = strings.TrimSpace(nm.Value)
			c.sheet.modeNoMatchPrec[name] = rank
		}
	}
	if a := el.Attr("", "typed"); a != nil {
		if c.sheet.modeTyped == nil {
			c.sheet.modeTyped = map[string]string{}
		}
		c.sheet.modeTyped[name] = strings.TrimSpace(a.Value)
	}
	if a := el.Attr("", "on-multiple-match"); a != nil {
		if c.sheet.modeFailMultiple == nil {
			c.sheet.modeFailMultiple = map[string]bool{}
		}
		c.sheet.modeFailMultiple[name] = strings.TrimSpace(a.Value) == "fail"
	}
	if a := el.Attr("", "warning-on-multiple-match"); a != nil {
		if c.sheet.modeWarnMultiple == nil {
			c.sheet.modeWarnMultiple = map[string]bool{}
		}
		c.sheet.modeWarnMultiple[name] = stylesheetYes(a.Value)
	}
	if a := el.Attr("", "warning-on-no-match"); a != nil {
		if c.sheet.modeWarnNoMatch == nil {
			c.sheet.modeWarnNoMatch = map[string]bool{}
		}
		c.sheet.modeWarnNoMatch[name] = stylesheetYes(a.Value)
	}
	if err := checkEntryVisibility(el); err != nil {
		return err
	}
	if c.sheet.declaredModeNames == nil {
		c.sheet.declaredModeNames = map[string]bool{}
	}
	for _, m := range modeNamesOf(el) {
		// XTSE0545 is about *conflicting values*, one attribute at a time:
		// "it is a static error if ... a package explicitly specifies two
		// conflicting values for the same attribute in different xsl:mode
		// declarations having the same import precedence, unless there is
		// another definition of the same attribute with higher import
		// precedence". Two declarations that agree leave no ambiguity to
		// report, so the values are recorded alongside the precedence and
		// the tie is judged after every module has been seen; see
		// checkModeConflicts.
		if c.modeTies == nil {
			c.modeTies = map[string][]modeDecl{}
		}
		attrs, err := modeDeclAttrs(el)
		if err != nil {
			return err
		}
		c.modeTies[m] = append(c.modeTies[m],
			modeDecl{prec: precedence, attrs: attrs})
		c.sheet.declaredModeNames[m] = true
	}
	c.recordModeVisibility(el, precedence)
	return c.compileModeAccumulators(el, name)
}
