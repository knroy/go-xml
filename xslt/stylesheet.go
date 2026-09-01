package xslt

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xsd"
)

// Stylesheet is a compiled XSLT 2.0 stylesheet.
//
// Compilation is separated from execution so that a stylesheet compiles once
// and transforms many documents concurrently. Everything reachable from here
// is immutable after Compile returns; all per-transform state lives in the
// runtime context. That is what makes a compiled EN 16931 rule set — tens of
// megabytes — shareable rather than per-worker.
type Stylesheet struct {
	// templates are the match templates, pre-sorted by descending priority so
	// that selection is a linear scan that stops at the first match.
	templates []*Template
	// named indexes templates that have a name, for xsl:call-template.
	named map[string]*Template
	// globals are top-level variables and parameters, in declaration order.
	globals []*Variable
	// packageUses lists, for each package, the packages it uses, nearest
	// first. A reference resolves against the referencing package and then
	// against these, because a used package's components are visible to the
	// using one. Built from packageParent when compilation finishes.
	packageUses map[int][]int
	// funcs holds xsl:function declarations.
	funcs *xpath.Library
	// baseURI is where the principal stylesheet module was read from.
	//
	// It is the static base URI of every expression the stylesheet contains,
	// which is what fn:doc, fn:document and fn:resolve-uri resolve a relative
	// reference against when there is no context node to take one from — a
	// transform started from a named template has none.
	baseURI string
	// maxVersion is CompileOptions.MaxVersion, the XSLT version this
	// processor is acting as. Zero means uncapped, and so 3.0.
	//
	// A few error codes were renamed between 2.0 and 3.0 for conditions that
	// are otherwise identical, and the suite runs the same version="2.0"
	// stylesheet under both processors expecting each to use its own code.
	// The module's own @version therefore cannot decide it.
	maxVersion float64
	// globalContextItem is the xsl:global-context-item declaration, or nil.
	// It constrains the item the transformation is started with, which every
	// global variable is then evaluated against.
	globalContextItem *globalContextItemDecl
	// keys holds xsl:key declarations, grouped by name.
	keys map[string][]*keyDef

	// stripTypeAnnotations records that some module specified
	// input-type-annotations="strip", which is a property of the whole
	// stylesheet rather than of the module carrying it.
	stripTypeAnnotations bool

	// prefixes holds every namespace prefix declared anywhere in the
	// stylesheet, so that a name computed at run time can be expanded.
	//
	// It exists for fn:key, whose first argument is a lexical QName that the
	// specification says to resolve "in scope for the prefix" at the point of
	// the call — a context the function library does not receive. A
	// stylesheet-wide map is a superset of any one element's context, which
	// is the safe direction to err: it can only make a name resolve that a
	// stricter reading would have rejected, never the reverse.
	prefixes map[string]string
	// decimalFormats holds xsl:decimal-format declarations by Clark name;
	// the unnamed default is stored under "".
	decimalFormats map[string]*DecimalFormat
	// schema holds the components brought in by xsl:import-schema, or nil
	// when the stylesheet declares none.
	schema *xsd.Schema
	// attributeSets holds xsl:attribute-set declarations, several per name
	// when modules declare the same one.
	attributeSets map[string][]*attributeSet
	// namespaceAliases rewrites literal-result-element namespaces, mapping a
	// stylesheet URI to the result URI and prefix it becomes.
	namespaceAliases map[string]nsAlias
	// characterMaps holds xsl:character-map declarations by Clark name.
	characterMaps map[string]map[rune]string
	// activeCharMap is the substitution table named by xsl:output, flattened
	// at compile time.
	activeCharMap map[rune]string
	// strip and preserve control whitespace stripping of source documents.
	strip, preserve []xdm.QName
	// pkgSpace holds the same declarations filed by the package they appear
	// in, which is the scope 4.4 gives them for a document loaded by
	// fn:document, fn:doc or fn:collection. The flat pair above stays for the
	// principal source document, which the top-level package's declarations
	// strip, and for XTSE0270.
	pkgSpace map[int]*packageSpace
	// output holds the unnamed xsl:output settings, which configure the
	// principal result.
	output OutputSettings
	// namedOutputs holds named xsl:output definitions by Clark name, which
	// xsl:result-document/@format selects.
	namedOutputs map[string]*OutputSettings
	// modeNoMatch holds xsl:mode/@on-no-match by Clark mode name, which
	// selects which built-in template rules apply in that mode.
	//
	// A mode with no declaration is absent from the map and gets
	// "text-only-copy", the value section 6.6 makes the default. The map
	// stores only what a declaration set, so an xsl:mode that names a mode
	// without giving @on-no-match records nothing and the default still
	// stands.
	modeNoMatch map[string]string
	// modeNoMatchPrec records the import precedence of the declaration that
	// set each modeNoMatch entry, so a later-compiled module of lower
	// precedence cannot overwrite a higher one.
	//
	// A used package compiles after the package that uses it and below it in
	// precedence, so last-writer-wins gave the used package's xsl:mode the
	// final say over the using package's. package-019 declares
	// on-no-match="text-only-copy" and used a package declaring "fail",
	// which is how the transform came to fail with XTDE0555.
	modeNoMatchPrec map[string]int

	// modeWarnMultiple and modeWarnNoMatch hold the two boolean xsl:mode
	// warning attributes by Clark mode name, stored exactly as modeNoMatch is
	// and consulted at the same two points in template selection.
	modeWarnMultiple map[string]bool
	// modeFailMultiple is @on-multiple-match="fail", which turns the
	// ambiguity warning-on-multiple-match reports into the error XTDE0540.
	modeFailMultiple map[string]bool
	// modeTyped is @typed, which XTTE3100 and XTTE3110 make an assertion
	// about the type annotation of every node the mode is applied to.
	modeTyped       map[string]string
	modeWarnNoMatch map[string]bool
	// accumulators holds xsl:accumulator declarations by Clark name, and
	// accumOrder keeps the declaration order that fn:accumulator-before
	// resolves ties by. See accumulator.go.
	accumulators map[string]*accumulatorDef
	accumOrder   []string
	// modeAccums holds xsl:mode/@use-accumulators by Clark mode name, which
	// says which accumulators may be read while that mode is current.
	modeAccums map[string]*modeAccumulators
	// declaredModeNames holds every mode an xsl:mode declaration names, even
	// one that set no attribute this processor acts on. Declaring a mode is
	// itself meaningful — it is what makes the mode exist for XTDE0045 and
	// for @declared-modes — so the bare declaration has to be recorded.
	declaredModeNames map[string]bool
	// modeVisibility holds the winning xsl:mode/@visibility per Clark mode
	// name, and rootDefaultMode the mode the principal module's own
	// @default-mode names. Together they decide XTDE0045; see
	// modevisibility.go.
	modeVisibility map[string]string
	// modeVisibilityStated marks the modes whose visibility was written out
	// rather than defaulted; see modevisibility.go.
	modeVisibilityStated map[string]bool
	// functionVisibility is xsl:function/@visibility keyed by Clark name and
	// arity; see entryvisibility.go. It is what tells the target expression
	// of xsl:evaluate which stylesheet functions it may call.
	functionVisibility map[string]string

	// pkgFuncs is the set of stylesheet functions each package DECLARES,
	// keyed by package and then by Clark name plus arity, which is the set
	// 3.6.3.5 makes available to a dynamic reference -- fn:function-lookup,
	// fn:function-available and xsl:evaluate. The flat library the ordinary
	// function call resolves against is deliberately not that set: it is the
	// whole assembly, where a private function of a used package and a
	// component the using package overrode are indistinguishable.
	//
	// A function declared inside an xsl:override is filed under the package
	// that wrote the xsl:override, not the used package it substitutes into;
	// see overridingPackage. An abstract declaration is filed nowhere at all,
	// 3.6.3.5 excluding it explicitly.
	pkgFuncs map[int]map[string]xpath.Function

	// templateVisibility is xsl:template/@visibility per Clark name, which
	// decides whether an invocation may start at the template; see
	// entryvisibility.go.
	templateVisibility map[string]string
	rootDefaultMode    string
	// isPackage says the principal module is an xsl:package, which is what
	// gives mode visibility something to be relative to.
	isPackage bool
	// source is the stylesheet's own document, which document("") returns.
	//
	// Section 16.1 defines the zero-length URI as naming the document
	// containing the expression, so a stylesheet carrying its own lookup
	// tables as literal data — a long-standing XSLT 1.0 idiom that XSLT 2.0
	// keeps — reads them through it. Keeping the tree costs one reference
	// per compiled stylesheet.
	source *xdm.Node
}

// Template is a compiled xsl:template.
type Template struct {
	Match    *Pattern
	Name     xdm.QName
	HasName  bool
	Mode     []string // empty means the default (unnamed) mode
	Priority float64
	Params   []*Variable
	Body     []Instruction
	// contextItem is the xsl:template's xsl:context-item child, or nil when
	// it declares none. It is checked when the template is entered rather
	// than compiled into the body: a template that requires a context item
	// and is called without one must fail before its first instruction runs.
	contextItem *contextItemDecl
	// asType is xsl:template/@as, which constrains what the template's
	// sequence constructor may produce. Section 6.1 applies the function
	// conversion rules to the result, so it converts as well as checks.
	asType *sequenceType
	// abstract names the component this template declares when its
	// visibility is abstract, and is empty otherwise. An abstract template
	// has no body, so invoking it is XTDE3052; see abstractcomponent.go.
	abstract string
	// importPrecedence orders templates from imported stylesheets below those
	// of the importing one.
	importPrecedence int
	// lowPrecedence is the lowest import precedence in the import tree of the
	// module that declared this template — that is, the number given to the
	// deepest module this one reaches through xsl:import.
	//
	// xsl:apply-imports (section 6.7) considers only "the templates that were
	// imported, directly or indirectly, by the stylesheet module containing
	// the current template rule". Import precedence alone cannot express that:
	// two modules imported as siblings both rank below their importer, so a
	// scan that merely drops below the current precedence runs on into a
	// sibling's import tree that the current module never imported.
	//
	// compileModule numbers a module's whole import tree before the module
	// itself takes a number (post-order, see the comment there), so those
	// numbers form the contiguous half-open interval
	// [lowPrecedence, importPrecedence). Recording the low end turns the
	// import-tree question into a range test.
	lowPrecedence int
	// declOrder breaks ties between equal-priority templates: the last one
	// declared wins, per the spec's conflict-resolution rule.
	declOrder int
	// unionGroup identifies the xsl:template declaration a rule came from.
	//
	// compileTemplate splits a union pattern into one rule per branch so that
	// each branch gets its own default priority and its own place in
	// declaration order. Those rules are still a single template rule as far
	// as the spec is concerned, and 6.4 makes on-multiple-match="fail" an
	// error only when "more than one template rule" matches. Spec bug 30402
	// settled that a node matching two branches of one union pattern is not
	// such a conflict: mode-1516 matches "para[foo] | para[text()]" against a
	// para that has both a foo child and text, and expects the rule to run,
	// not XTDE0540. All the rules split from one declaration share the group,
	// so the ambiguity check can skip a candidate that is really the winner
	// seen through a different branch.
	unionGroup int
}

// Variable is a compiled xsl:variable, xsl:param or xsl:with-param.
type Variable struct {
	Name xdm.QName
	// Select is the value expression, or nil when the value comes from the
	// element's content.
	Select *xpath.Compiled
	// Body is the sequence constructor used when Select is absent. A variable
	// with content builds a temporary tree, which is why the two forms are
	// not interchangeable: "select" yields whatever the expression yields,
	// while content always yields a document node.
	Body []Instruction
	// Required marks a parameter that must be supplied.
	Required bool
	// IsParam distinguishes an xsl:param from an xsl:variable. The two are
	// compiled to the same structure because they evaluate identically, but
	// only a parameter can be supplied from outside, and section 10.1.1
	// gives the two different error codes when a declared "as" type rejects
	// the value: a parameter left unsupplied whose required type excludes
	// the empty sequence is XTDE0610, while a variable is only ever the
	// plain type error.
	IsParam bool
	// Tunnel marks a tunnel parameter, which passes through templates that do
	// not declare it.
	Tunnel bool
	// precedence is the import precedence of the module that declared this
	// variable, so that a duplicate at the *same* precedence can be
	// distinguished from a legitimate override at a higher one.
	precedence int
	// asType is the compiled "as" declaration, applied to the value when
	// present. XSLT converts the value to this type rather than merely
	// checking it, so it changes results and not just error messages.
	asType *sequenceType
	// baseURI is the base URI in force at the declaration, which the
	// temporary tree a content-valued variable builds takes as its own.
	baseURI string
	// isStatic marks a declaration carrying static="yes", whose value was
	// computed before compilation began and is held in staticValue. A static
	// parameter is not a runtime parameter: it was already bound when the
	// caller could still have supplied one, so Transform's Params must not
	// override it.
	isStatic    bool
	staticValue xdm.Sequence
	// deferred marks a global whose value is computed only if something
	// refers to it. An abstract variable is the case: its body raises
	// XTDE3052, which 3.5.3.2 makes the error for an invocation that "is
	// evaluated", so a variable nothing refers to must not raise at all.
	deferred bool
	// pkg is the package whose module declared this variable. Section 3.5.5
	// makes a component's identity belong to its package, and a diamond --
	// one package used by two routes, each overriding the same variable --
	// puts two live bindings of one name in the same stylesheet. The flat
	// name-keyed scope the runtime binds into cannot hold both, so the name
	// alone is not the identity: see use-package-175 and -176.
	pkg int
	// selectNS are the namespace bindings in force on the declaration, kept
	// so that globalRefs can expand a *prefixed* variable reference in the
	// select expression. Ordering globals by their dependencies is textual
	// (see globalRefs), and a prefixed name means nothing without these.
	//
	// $xsl:original inside an xsl:override is the case that needs it: the
	// prefix is rebound to a generated namespace, and the reference has to be
	// ordered after the renamed original, which is emitted *after* the
	// overriding declaration in the used package's tree.
	selectNS map[string]string
}

// keyDef is a compiled xsl:key.
type keyDef struct {
	match *Pattern
	// use is the @use expression. Exactly one of use and body is set:
	// section 16.3 allows the key value to be given either way, and requires
	// that it not be given both ways.
	use *xpath.Compiled
	// body is the sequence constructor form of the key value. It builds a
	// temporary tree per matched node, exactly as a variable's content does,
	// and the tree's string value is the key.
	body []Instruction
	// collation is the collation URI the key's values are compared under.
	// Section 16.3 makes key matching collation-sensitive, so a key declared
	// with a case-blind collation finds a node whose value differs from the
	// sought one only in case.
	collation string
	// composite is @composite, section 16.3. With it, a node is indexed
	// under the WHOLE sequence its use expression returns, taken as one key
	// value; without it, under each item of that sequence separately. The
	// lookup side mirrors it: the sought value is likewise one composite key
	// rather than a set of alternatives, so key('k', ('a','b')) with
	// composite="yes" asks for the single key ('a','b') and without it asks
	// for either 'a' or 'b'.
	composite bool
	// pkg is the package the declaration was written in. 3.5.5 makes keys
	// local to their package, so two packages may declare one name
	// differently and a call resolves to the declarations of the package the
	// CALL is written in. See keyDefsFor.
	pkg int
}

// hostPackage is the package identity that travels with a compiled
// expression, as xpath.Context.StaticHost. It is a named type rather than a
// bare int so that nothing else can be mistaken for one.
type hostPackage int

// packageOf reads the package an expression was compiled in back out of the
// evaluation context, defaulting to the top-level package for an expression
// that carries nothing.
func packageOf(ctx *xpath.Context) int {
	if ctx == nil {
		return 0
	}
	if p, ok := ctx.StaticHost.(hostPackage); ok {
		return int(p)
	}
	return 0
}

// buildPackageUses inverts packageParent into, for each package, the packages
// it uses -- nearest first, then theirs, transitively.
//
// The order is the order visibility flows: a reference resolves against the
// package it is written in, then against what that package used, and so on
// outward. Depth-first from each package gives exactly that.
func buildPackageUses(parent map[int]int) map[int][]int {
	children := map[int][]int{}
	for child, p := range parent {
		children[p] = append(children[p], child)
	}
	for _, cs := range children {
		sort.Ints(cs)
	}
	out := map[int][]int{}
	for pkg := range children {
		seen := map[int]bool{pkg: true}
		var walk func(int)
		walk = func(p int) {
			for _, ch := range children[p] {
				if seen[ch] {
					continue
				}
				seen[ch] = true
				out[pkg] = append(out[pkg], ch)
				walk(ch)
			}
		}
		walk(pkg)
	}
	return out
}

// packageParent maps a used package to the package that used it.
//
// A component of a used package is visible to the using package, so a
// reference written there must reach it: template "b" of use-package-175 is
// declared in package B and refers to $v, which B's copy of D declares. The
// reference carries B, the binding carries D, and only this chain relates
// them. Guarded by compileMu with the rest of the package state.
var packageParent = map[int]int{}

// packageSpace is one package's whitespace declarations.
//
// Section 4.4 scopes them: "The effect of xsl:strip-space and
// xsl:preserve-space is local to the package in which they appear."
// document-2401 is the case that pins it -- two packages declare different
// stripping over the same file, and each must see its own.
type packageSpace struct {
	strip    []xdm.QName
	preserve []xdm.QName
}

// OutputSettings holds the xsl:output declaration.
type OutputSettings struct {
	Method        string // "xml", "html", "text"
	Indent        bool
	OmitXMLDecl   bool
	Encoding      string
	DocTypePublic string
	DocTypeSystem string
	CDataElements []xdm.QName
	Standalone    string
	// Version is xsl:output/@version. For the html method it selects the
	// HTML version; for xml and xhtml it is the XML version.
	Version string
	// HTMLVersion is xsl:output/@html-version, new in XSLT 3.0.
	//
	// It exists because @version cannot answer the question for XHTML: there
	// it states the version of XML, so an XHTML5 document is still
	// version="1.0" and the HTML version had nowhere to go. Section 9.1: if
	// html-version is absent "the html output method uses the value of the
	// version parameter in its place", so it overrides for html and is the
	// only source for xhtml.
	HTMLVersion string
	// UseCharacterMaps names the xsl:character-map declarations applied at
	// serialisation.
	UseCharacterMaps []xdm.QName
	// ByteOrderMark writes a BOM at the start of the output. It is "no" by
	// default for every method, which is what makes UTF-8 output usable by
	// readers that do not expect one.
	ByteOrderMark bool
	// IncludeContentType controls whether the HTML and XHTML methods insert
	// the content-type meta element. It defaults to true, which is why it is
	// stored as a pointer: an explicit "no" has to be distinguishable from
	// the attribute being absent.
	IncludeContentType *bool
	// EscapeURIAttributes controls percent-escaping of URI-valued attributes
	// in the HTML and XHTML methods. It defaults to true, and is a pointer
	// for the same reason.
	EscapeURIAttributes *bool
	// UndeclarePrefixes emits namespace undeclarations, which only XML 1.1
	// permits. This parser implements XML 1.0, so it is recorded and ignored.
	UndeclarePrefixes bool
	// ItemSeparator is xsl:output/@item-separator: the string inserted
	// between adjacent items of the result sequence during sequence
	// normalisation (XSLT 3.0 section 5.7.1 step 3).
	//
	// It is a pointer because an explicit zero-length separator is not the
	// same as the attribute being absent: absent means the default rule
	// (adjacent atomic values separated by a single space, nodes not
	// separated at all), while item-separator="" means every adjacency gets
	// nothing, including between two atomic values.
	ItemSeparator *string

	// BuildTree says whether the raw result is normalised into a final
	// result tree before it is delivered or serialised (XSLT 3.0 section
	// 2.3.6). Nil is the default, which depends on the method: yes for xml,
	// html, xhtml and text, no for json and adaptive.
	//
	// With build-tree="no" the raw sequence is serialised as it stands, so
	// item-separator has something to separate -- normalisation would have
	// merged the whole sequence into one tree first, which is why the note
	// in 27.1 says the separator "has no effect ... if the effective value
	// of build-tree is yes".
	BuildTree *bool
	// ParameterDocument is the URI of an external
	// output:serialization-parameters document whose parameters override the
	// ones written here. It is kept as a URI rather than resolved at compile
	// time because 25.1 says the document "should be read during run-time
	// evaluation of the stylesheet", so that a stylesheet deployed away from
	// where it was written still finds its own parameters.
	ParameterDocument string
	// ParameterDocumentBase is the base URI of the element that wrote
	// ParameterDocument, which is what a relative URI there resolves
	// against. It has to travel with the URI rather than be taken from the
	// principal module: an <xsl:output name="f" parameter-document="p.xml"/>
	// in a module reached by xsl:include names a document beside *that*
	// module, and output-0722 puts the included module and its parameter
	// document in a subdirectory to make the difference visible.
	ParameterDocumentBase string
	// InlineCharMap is a character map given by value rather than by name:
	// the output:use-character-maps element of a parameter document spells
	// its entries out, having no xsl:character-map declaration to point at.
	// It overrides UseCharacterMaps entirely, being the higher-precedence
	// source of the same parameter.
	InlineCharMap map[rune]string
	// AllowDuplicateNames is the JSON output method's parameter of that name:
	// with it unset, two map keys that render to the same JSON string are
	// SERE0022 rather than two entries in one object.
	AllowDuplicateNames bool
	// JSONNodeOutputMethod is the method a node nested inside a JSON value is
	// serialised with, JSON having no node type of its own. Empty is the
	// default, "xml".
	JSONNodeOutputMethod string
	// MediaType is the media type of the output. It affects no serialised
	// character; it is metadata a caller passes on.
	MediaType string
	// NormalizationForm names a Unicode normalisation applied to the output.
	// Only "none" is implemented; any other value the serialiser does not
	// support is a serialization error rather than something to ignore,
	// because output that was silently left unnormalised would be accepted
	// by a consumer that then compares it against a normalised form and
	// finds a spurious difference.
	NormalizationForm string
	// Version10Implicit records that the principal module declares version
	// "1.0" and this is the implicitly-created final result tree.
	//
	// It changes only the *default* output method. Under backwards
	// compatibility an XSLT 1.0 stylesheet has no xhtml method to select —
	// the method did not exist — so a result whose document element is html
	// in the XHTML namespace serialises as xml, not xhtml: URI-valued
	// attributes are left unescaped and no content-type meta element is
	// added. An explicit xsl:output/@method overrides this like any other
	// default, and xsl:result-document clears the flag, because the tree it
	// creates is not the implicit one.
	Version10Implicit bool
}

// Instruction is one compiled XSLT instruction.
//
// Instructions write to an output builder rather than returning values,
// because an XSLT sequence constructor produces a *stream* of nodes and
// atomic values: xsl:element opens a node that subsequent instructions add
// children to. Returning trees from each instruction and concatenating them
// would mean building and copying the same subtree repeatedly.
type Instruction interface {
	// Execute runs the instruction, appending to out.
	Execute(rt *runtime, out *outputBuilder) error
}

// stylesheetBase is where the principal module was read from: the document's
// own base URI, or the one the caller supplied when the document came from
// somewhere with no location of its own.
func stylesheetBase(doc *xdm.Node, opt string) string {
	// xml:base on the stylesheet element overrides where the module was read
	// from, and it is the static base URI that fn:static-base-uri returns and
	// that every relative reference in the module resolves against. Reading
	// the document node alone ignored it, so a stylesheet that declared its
	// own base got the file it happened to be loaded from instead.
	if doc != nil {
		if root := firstElement(doc); root != nil && root.BaseURI != "" {
			return root.BaseURI
		}
		if doc.BaseURI != "" {
			return doc.BaseURI
		}
	}
	return opt
}

// Compile compiles a stylesheet from a parsed XSLT document.
func Compile(doc *xdm.Node, opts CompileOptions) (*Stylesheet, error) {
	// See xsd.Schema.Validate: a nil document is a caller's mistake, but a
	// library that panics on one takes the caller's process with it.
	if doc == nil {
		return nil, fmt.Errorf("no stylesheet document to compile")
	}
	c := &compiler{
		opts: opts,
		sheet: &Stylesheet{
			named:            map[string]*Template{},
			keys:             map[string][]*keyDef{},
			prefixes:         map[string]string{},
			decimalFormats:   map[string]*DecimalFormat{},
			attributeSets:    map[string][]*attributeSet{},
			namespaceAliases: map[string]nsAlias{},
			characterMaps:    map[string]map[rune]string{},
			funcs:            newStylesheetFuncs(),
			baseURI:          stylesheetBase(doc, opts.BaseURI),
			maxVersion:       opts.MaxVersion,
			// Method is deliberately left empty. Its default is not "xml"
			// but a choice made from the result tree — a document whose
			// first element is <html> defaults to the html or xhtml method
			// — and that tree does not exist until the transform has run.
			// Filling it in here would make every stylesheet that omits
			// xsl:output/@method serialise as XML.
			output: OutputSettings{
				Encoding: "UTF-8",
			},
		},
	}
	// compileSchema is package state for the duration of this call; see its
	// declaration. The lock makes concurrent Compile calls safe, and clearing
	// it on the way out keeps one compilation from leaking a schema into the
	// next.
	compileMu.Lock()
	defer compileMu.Unlock()
	compileSchema = nil
	defer func() { compileSchema = nil }()
	compilePackage = 0
	defer func() { compilePackage = 0 }()
	// The record of which package wrote each overriding declaration is
	// package state on the same terms, and is cleared at both ends so that
	// one compilation cannot answer a question about another's nodes.
	overridingDecls = nil
	defer func() { overridingDecls = nil }()
	// So is the memo of which package trees make a dynamic component
	// reference; see makesDynamicReference.
	dynamicRefCache = nil
	defer func() { dynamicRefCache = nil }()
	// Which package used which is package state on the same terms; see
	// packageParent.
	packageParent = map[int]int{}
	defer func() { packageParent = map[int]int{} }()
	// The XPath version override is package state on the same terms and under
	// the same lock; see overrideXPathVersion.
	overrideXPathVersion = opts.XPathVersion
	defer func() { overrideXPathVersion = nil }()
	// So is the processor's own version, which a few grammar rules turn on;
	// see compileMaxVersion.
	compileMaxVersion = opts.MaxVersion
	defer func() { compileMaxVersion = 0 }()

	if err := c.compileDocument(doc, 0); err != nil {
		return nil, err
	}
	c.sheet.sortTemplates()
	// A global variable overridden by a higher-precedence declaration is not
	// evaluated at all, so the overridden bindings are dropped before the
	// stylesheet is handed back.
	if err := c.checkAccumulatorConflicts(); err != nil {
		return nil, err
	}
	if err := c.checkModeConflicts(); err != nil {
		return nil, err
	}
	// XTSE3105 needs every xsl:mode and the whole imported schema in hand, so
	// it runs here rather than while a template is being compiled.
	if err := c.checkTypedStrictPatterns(); err != nil {
		return nil, err
	}
	c.publishModeVisibility()
	if err := checkStripPreserveConflict(
		c.stripDecls, c.preserveDecls); err != nil {
		return nil, err
	}
	c.pruneOverriddenGlobals()
	c.deferDependentGlobals()
	c.sheet.packageUses = buildPackageUses(packageParent)
	// Character-map inclusion is resolved before the xsl:output tables are
	// flattened, and both after every module, so that a map may name one
	// declared later or in a module imported afterwards.
	if err := c.resolveCharacterMapIncludes(); err != nil {
		return nil, err
	}
	if err := c.checkCallTemplateParams(); err != nil {
		return nil, err
	}
	// XTSE1560 is checked here rather than as each xsl:output compiles,
	// because a declaration at a higher import precedence silences a conflict
	// and is compiled after the modules it overrides.
	if err := c.checkOutputConflicts(); err != nil {
		return nil, err
	}
	// XTSE0710 is checked here for the same reason: an attribute set may name
	// one declared in a module compiled after it.
	if err := c.checkAttributeSetCycles(); err != nil {
		return nil, err
	}
	if err := c.checkAttributeSetRefs(); err != nil {
		return nil, err
	}
	// XPST0017 likewise: a match pattern may call an xsl:function declared
	// after it, or in a module imported later, so pattern function calls are
	// resolved once every declaration is in.
	if err := c.checkPatternFuncs(); err != nil {
		return nil, err
	}
	// A variable's select expression is resolved here for the same reason,
	// and because nothing else ever resolves one that no expression uses.
	if err := c.checkVariableFuncs(); err != nil {
		return nil, err
	}
	// XTSE1290 likewise: two imported xsl:decimal-format declarations may
	// conflict with each other and still be harmless, because the importing
	// module overrides both.
	if err := c.checkDecimalFormatConflicts(); err != nil {
		return nil, err
	}
	// XTSE1300 is checked on the assembled format rather than on each
	// declaration, since a format may be built from several of them.
	if err := c.checkDecimalFormatSymbols(); err != nil {
		return nil, err
	}
	// Character maps are resolved last, so that an xsl:output in the
	// importing module can name a map declared in an imported one.
	//
	// The principal xsl:output belongs to the top-level package, and 3.5.5
	// makes a character map local to the package that declares it, so a name
	// that only a used package declares is not in scope here however plainly
	// it sits in the flat table. use-package-106 names such a map and wants
	// XTSE1590 for it.
	for _, n := range c.sheet.output.UseCharacterMaps {
		if c.packageLocalCharMaps[n.Clark()] {
			return nil, fmt.Errorf(
				"XTSE1590: no xsl:character-map named %q is in scope; it is "+
					"declared in a used package, and 3.5.5 makes character "+
					"maps local to the package declaring them", n.Lexical())
		}
	}
	if err := c.sheet.resolveOutputCharacterMaps(c.sheet.output.UseCharacterMaps); err != nil {
		return nil, err
	}
	return c.sheet, nil
}

// CompileOptions configures compilation.
type CompileOptions struct {
	// Resolver loads xsl:include and xsl:import targets. Nil disables them,
	// which is the safe default for untrusted stylesheets.
	Resolver ModuleResolver
	// BaseURI of the stylesheet, for resolving relative include paths.
	BaseURI string
	// StaticParams supplies values for static stylesheet parameters — a
	// top-level xsl:param carrying static="yes" — keyed by the parameter's
	// {uri}local name. A static parameter is bound before static analysis
	// begins, so its value has to come from the caller rather than from
	// Transform's runtime Params.
	StaticParams map[string]xdm.Sequence

	// SchemaResolver loads the schemas named by xsl:import-schema. Nil
	// disables loading by location, for the same reason a nil Resolver
	// disables xsl:include: following a location means fetching whatever
	// the stylesheet names. An inline <xs:schema> child needs no resolver.
	SchemaResolver xsd.Resolver

	// SchemaParseOptions are passed to the XML parser for each schema
	// document xsl:import-schema loads, including the ones those documents
	// import in turn.
	//
	// The zero value refuses a DOCTYPE, which is the right default for the
	// same reason it is in xsd.Options: a schema has no use for one, and it
	// is the entry point for entity expansion attacks. A host that must
	// read a schema carrying one -- the W3C's own schema for schemas
	// declares its entities that way -- sets AllowDOCTYPE here, and thereby
	// says so deliberately rather than having it decided for it.
	SchemaParseOptions xdm.ParseOptions

	// PackageResolver loads the packages named by xsl:use-package. Nil
	// disables package composition, for the same reason a nil Resolver
	// disables xsl:include: resolving a package name means fetching whatever
	// the stylesheet asks for, and a host running an untrusted stylesheet
	// should decide what that can reach.
	PackageResolver PackageResolver

	// XPathVersion pins the version of XPath every expression in the
	// stylesheet is compiled in, overriding what the stylesheet declares.
	//
	// Nil, the default, derives it from the version attribute the way the
	// specification requires: 1.0 and 2.0 are XPath 2.0, 3.0 is XPath 3.1,
	// since XSLT 3.0 section 2.2 requires an XPath 3.1 processor.
	//
	// Setting it is for a host that must decide the language itself rather
	// than let the document decide — pinning XPath20 to run an untrusted
	// stylesheet under a smaller surface, or raising a stylesheet that
	// declares 2.0 but is known to want the later functions. It is a
	// deliberate departure from conformance in both directions, which is why
	// nothing sets it implicitly.
	XPathVersion *xpath.Version

	// MaxVersion caps the XSLT version whose constructs the compiler will
	// accept, whatever the stylesheet's own @version says.
	//
	// It exists for the constructs a later version *adds* to the language
	// rather than changes in it. xsl:package is the case that forces it: a
	// package written to XSLT 3.0 routinely carries version="2.0", because
	// the version attribute states the XSLT version of the *expressions*, not
	// of the packaging — so the attribute cannot tell an XSLT 2.0 processor,
	// for which xsl:package is XTSE0010, apart from a 3.0 one for which it is
	// the module element. Only the host knows which it is running as.
	//
	// Zero, the default, imposes no cap and accepts everything implemented.
	MaxVersion float64
}

// ModuleResolver loads an included or imported stylesheet module.
type ModuleResolver interface {
	ResolveModule(href, base string) (*xdm.Node, string, error)
}

// sortTemplates orders templates for selection: highest import precedence
// first, then highest priority, then latest declaration.
//
// Sorting once at compile time turns template selection into a scan that can
// stop at the first match, instead of a full pass computing the maximum on
// every node.
func (s *Stylesheet) sortTemplates() {
	sort.SliceStable(s.templates, func(i, j int) bool {
		a, b := s.templates[i], s.templates[j]
		if a.importPrecedence != b.importPrecedence {
			return a.importPrecedence > b.importPrecedence
		}
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.declOrder > b.declOrder
	})
}

// Output returns the stylesheet's output settings.
func (s *Stylesheet) Output() OutputSettings { return s.output }

// nsResolver adapts an element's in-scope namespaces to the xpath package's
// resolver interface.
//
// Each XPath expression in a stylesheet is compiled against the namespace
// context of the element it appears on, so a resolver is built per element
// rather than per stylesheet.
type nsResolver struct {
	bindings  map[string]string
	defaultNS string
	// baseURI is the base URI of the element this resolver was built for,
	// which is the static base URI of the expressions written on it.
	baseURI string
	// collation is the default collation in force at that element, from the
	// nearest ancestor-or-self carrying [xsl:]default-collation. It is a
	// static property, inherited exactly as the base URI is.
	collation string
	// compat is XPath 1.0 compatibility mode, in force for the expressions
	// written on the element this resolver was built for. It is a static
	// property inherited exactly as the base URI and the default collation
	// are, and compileExpr binds it to every expression it compiles.
	compat bool
	// schema is what xsl:import-schema brought in, or nil. It makes the
	// stylesheet's imported types part of the static context, which is what
	// lets "instance of my:partNumberType" resolve at all.
	schema *xsd.Schema
	// pkg identifies the package the element belongs to, which decides which
	// xsl:strip-space declarations apply to a document the expressions on it
	// load. Section 4.4: "The effect of xsl:strip-space and
	// xsl:preserve-space is local to the package in which they appear.
	// Declarations within a library package only affect the handling of
	// documents loaded using a call on the document, doc, or collection
	// functions ... appearing lexically within the same package."
	//
	// Lexically, so it is a static property of where the call is written --
	// exactly like the base URI and the default collation beside it -- and
	// not of which package's code is on the stack at the time.
	pkg int
	// xpathVersion is the version of XPath the expressions written on this
	// element are in. It is static in exactly the way the base URI and the
	// default collation are, and for the same reason: it is a property of
	// where the expression was written. A version="3.0" stylesheet writes
	// XPath 3.1, a version="2.0" one writes XPath 2.0, and an included module
	// keeps its own answer rather than the importing module's.
	xpathVersion xpath.Version
	// xsltVersion is the XSLT version declared on or above the element this
	// resolver was built for. It is not the same question xpathVersion
	// answers: forwards-compatible mode raises the XPath version to the
	// latest implemented while leaving the XSLT grammar at 2.0, and the
	// pattern grammar follows the XSLT version. See patternsAllow30.
	xsltVersion float64
}

// compileSchema is the schema in force while a stylesheet is being compiled.
//
// It is package state rather than a parameter because newNSResolver is called
// from a dozen places that build a resolver for one element and have no
// compiler in scope, and threading a schema through all of them would touch
// far more code than the feature is worth. A compiled Stylesheet holds its
// own schema and never reads this again.
//
// compileMu makes that safe. Compile is a public entry point, so nothing stops
// a caller compiling two stylesheets at once, and package state written
// without a lock is a data race whether or not a test happens to exercise it.
// Compilation is not on any hot path — a stylesheet compiles once and
// transforms many documents — so serialising it costs nothing that matters.
var (
	compileMu     sync.Mutex
	compileSchema *xsd.Schema
	// compilePackage identifies the package whose module is being compiled,
	// for the same reason and by the same mechanism as compileSchema. It is
	// what makes whitespace stripping package-scoped: 4.4 decides which
	// declarations apply from where the CALL is written, lexically, so the
	// answer has to be captured at compile time and travel with the
	// expression.
	//
	// Zero is the top-level package. A used package gets a serial of its own
	// so that two library packages are distinguishable from each other as
	// well as from the top level.
	compilePackage int
)

func newNSResolver(el *xdm.Node, defaultElementNS string) *nsResolver {
	// [xsl:]xpath-default-namespace is a standard attribute, so it can sit on
	// any ancestor — including a literal result element — and applies to every
	// expression within. Reading it only from the stylesheet element left an
	// unprefixed type or element name in a nested xsl:variable resolving to no
	// namespace, which is XPST0051 against the schema that defines it.
	//
	// An explicit argument still wins: xsl:key and friends pass the namespace
	// the spec fixes for them rather than inheriting one.
	if defaultElementNS == "" {
		defaultElementNS = xpathDefaultNamespaceAt(el)
	}
	return &nsResolver{
		bindings:  el.InScopeNamespaces(),
		defaultNS: defaultElementNS,
		baseURI:   el.BaseURI,
		collation: defaultCollationAt(el),
		compat:    compatModeAt(el),
		schema:    compileSchema,
		pkg:       overridingPackage(el, compilePackage),

		xpathVersion: xpathVersionAt(el),
		xsltVersion:  declaredXSLTVersion(el),
	}
}

// LookupSchemaDeclaration implements xpath.SchemaTypes.
//
// schema-element(us:address) names the global element declaration us:address,
// so the question is whether the imported schema declares that name — not
// whether any element happens to be called it.
func (r *nsResolver) LookupSchemaDeclaration(name xdm.QName, attribute bool) bool {
	if r.schema == nil {
		return false
	}
	if attribute {
		_, ok := r.schema.Attributes[name]
		return ok
	}
	_, ok := r.schema.Elements[name]
	return ok
}

// SchemaDeclarationType implements xpath.SchemaTypes.
//
// Only a *named* type is reported. A declaration using an inline anonymous
// type has no name for the node test to compare against, and inventing one
// would make the comparison fail for every node rather than pass for the
// right ones.
func (r *nsResolver) SchemaDeclarationType(name xdm.QName, attribute bool) (string, bool) {
	if r.schema == nil {
		return "", false
	}
	var t xsd.Type
	if attribute {
		d, ok := r.schema.Attributes[name]
		if !ok || d == nil || d.Type == nil {
			return "", false
		}
		t = d.Type
	} else {
		d, ok := r.schema.Elements[name]
		if !ok || d == nil || d.Type == nil {
			return "", false
		}
		t = d.Type
	}
	local := t.TypeName().Local
	if local == "" {
		return "", false
	}
	return local, true
}

// ValidateSchemaValue implements xpath.SchemaTypes.
func (r *nsResolver) ValidateSchemaValue(name xdm.QName, value string) (bool, error) {
	if r.schema == nil || !r.schema.HasSimpleType(name) {
		return false, nil
	}
	return true, r.schema.ValidateValue(value, name)
}

// SubstitutionGroupMembers implements xpath.SchemaTypes.
//
// The schema has already computed the transitive closure and cached it on the
// head declaration, so this is a lookup rather than a walk. Only the names are
// handed back: the node test compares names, and returning declarations would
// leak xsd types into the xpath package, which cannot import xsd.
func (r *nsResolver) SubstitutionGroupMembers(name xdm.QName) []xdm.QName {
	if r.schema == nil {
		return nil
	}
	head, ok := r.schema.Elements[name]
	if !ok {
		return nil
	}
	members := head.Substitutable()
	if len(members) == 0 {
		return nil
	}
	out := make([]xdm.QName, 0, len(members))
	for _, d := range members {
		// Only the URI and local name: a QName is compared as a whole
		// struct, and the prefix a schema was written with is rarely the
		// stylesheet's.
		out = append(out, xdm.QName{URI: d.Name.URI, Local: d.Name.Local})
	}
	return out
}

// LookupSchemaType implements xpath.SchemaTypes.
//
// A type an imported schema defines is in the static context, so a stylesheet
// may write "instance of my:partNumberType" exactly as it writes
// "instance of xs:integer". Without this the name is XPST0051 and the whole
// stylesheet fails to compile, which is why importing a schema and then never
// naming one of its types was the only case that worked.
func (r *nsResolver) LookupSchemaType(name xdm.QName) (xdm.TypeCode, bool, bool) {
	if r.schema == nil {
		return 0, false, false
	}
	t, ok := r.schema.Types[name]
	if !ok {
		return 0, false, false
	}
	// Only an atomic simple type erases to a primitive. A complex type, a
	// list or a union is a real type — the name resolves — but there is no
	// single code that describes its values, so it is reported as known and
	// non-atomic rather than guessed at.
	st, ok := t.(*xsd.SimpleType)
	if !ok || st.Variety != xsd.VarietyAtomic || st.Primitive == nil {
		return 0, false, true
	}
	if st.Primitive.Name.Local == "NOTATION" && st.Primitive.Name.URI == xsd.NSSchema {
		// A value of a type derived from xs:NOTATION is a QName, not a
		// string: XML Schema gives xs:NOTATION the same value space as
		// xs:QName, so two notation values are equal when their expanded
		// names are equal however they were spelled. Erasing to xs:string —
		// which is what the built-in table does, since the abstract type
		// cannot be cast to directly — made one:mp3 and first:mp3 compare
		// unequal even though both prefixes bind the same namespace.
		return xdm.TypeQName, true, true
	}
	// The nearest built-in *ancestor*, not the XSD primitive. They differ
	// wherever the built-in hierarchy has steps below a primitive: a
	// restriction of xs:integer has xs:decimal as its primitive, so erasing
	// to the primitive made "cast as my:derivedInteger" produce an
	// xs:decimal, and "instance of xs:integer" then answered false for the
	// value the cast had just produced.
	for cur := st; cur != nil; {
		if cur.Name.URI == xsd.NSSchema && cur.Name.Local != "" {
			if code, ok := xpath.BuiltinAtomicTypeCode(cur.Name.Local); ok {
				return code, true, true
			}
		}
		base, ok := cur.Base.(*xsd.SimpleType)
		if !ok || base == cur {
			break
		}
		cur = base
	}
	code, ok := xpath.BuiltinAtomicTypeCode(st.Primitive.Name.Local)
	if !ok {
		return 0, false, true
	}
	return code, true, true
}

// SchemaUnionMemberNames implements xpath.SchemaUnionNames.
//
// It answers with the names a validated NODE could be annotated with, where
// SchemaUnionMemberTypes answers with the built-in codes an unannotated VALUE
// could carry. A node validated against a union is annotated with the member
// that accepted it, so match-232's age="44" — declared as a union of
// my:partNumberType and xs:integer — is annotated "integer", and the union's
// own name appears nowhere on it.
//
// Purity is deliberately NOT required here, unlike SchemaUnionMemberTypes.
// That constraint exists to stop a member VALUE substituting for a faceted
// union it may not satisfy, which is the XSD 1.0 error XSD 1.1 3.16.6.3
// corrected. A node reaching this test was actually validated against the
// union, so the schema has already established that it satisfies the union's
// facets; the unsound substitution has no way to arise.
//
// Only ATOMIC members are reported, because only they are ever recorded as a
// node's annotation. See the walk below.
func (r *nsResolver) SchemaUnionMemberNames(name xdm.QName) ([]string, bool) {
	if r.schema == nil {
		return nil, false
	}
	t, ok := r.schema.Types[name]
	if !ok {
		return nil, false
	}
	st, ok := t.(*xsd.SimpleType)
	if !ok || st.Variety != xsd.VarietyUnion {
		return nil, false
	}
	var out []string
	seen := map[xdm.QName]bool{name: true}
	var walk func(u *xsd.SimpleType, depth int)
	walk = func(u *xsd.SimpleType, depth int) {
		// A union whose members form a cycle is not a schema this can answer
		// for; the bound stops a malformed one from recursing forever rather
		// than limiting a real one.
		if depth > 32 {
			return
		}
		for _, m := range u.MemberTypes {
			if m == nil || seen[m.Name] {
				continue
			}
			seen[m.Name] = true
			// A member that is itself a union contributes its own members
			// too: 2.5.5 speaks of the TRANSITIVE membership, and a node may
			// have been validated against a member of a member. The union
			// itself is not contributed — nothing is annotated with it when
			// its own members are atomic, and when they are not, the
			// exclusion below is the point.
			if m.Variety == xsd.VarietyUnion {
				walk(m, depth+1)
				continue
			}
			// ONLY AN ATOMIC MEMBER. Validation records the member that
			// accepted the value only when that member is atomic; a union
			// over LIST types annotates the node with the union itself, so
			// there is no member-annotated node for the caller to admit and
			// naming the members would only let a sibling match. match-197
			// has <listUnion> annotated my:listUnionType and <simpleUserList>
			// annotated my:myListType — a member of that very union — and
			// element(*, my:listUnionType) must select the first alone.
			if m.Variety != xsd.VarietyAtomic {
				continue
			}
			// A built-in is annotated by its bare local name, a schema type
			// by its expanded {uri}local key. AnnotationName draws exactly
			// that distinction, so the name is handed to it rather than being
			// spelled out here.
			if m.Name.Local != "" {
				out = append(out, xdm.AnnotationName(m.Name.URI, m.Name.Local))
			}
		}
	}
	walk(st, 0)
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// SchemaTypeIsList implements xpath.SchemaListTypes.
//
// It exists so that "castable as" against a schema-defined list type answers
// true or false rather than raising the atomic-target static error. The
// validity of a particular value is still ValidateSchemaValue's answer; this
// only says which kind of type the name denotes.
func (r *nsResolver) SchemaTypeIsList(name xdm.QName) (xdm.QName, bool) {
	if r.schema == nil {
		return xdm.QName{}, false
	}
	return r.schema.IsListSimpleType(name)
}

// SchemaUnionMemberTypes implements xpath.SchemaUnionTypes.
//
// XPath 3.1 2.5.5 makes union membership a clause of derives-from in its own
// right, so a value whose type is one of a pure union's members is an instance
// of the union without ever having been validated against it. LookupSchemaType
// cannot express that — it returns one primitive, and a union has none — so
// the members are resolved here instead.
//
// Purity is enforced rather than assumed. 2.5 admits a union as an item type
// only when it carries no facets and has no list type anywhere in its
// transitive membership; a union failing either returns false and so matches
// nothing. That is deliberately the strict direction: XSD 1.1 3.16.6.3 fixed
// an XSD 1.0 error by which a member could substitute for a faceted union it
// does not actually satisfy, and being permissive here would reintroduce it.
func (r *nsResolver) SchemaUnionMemberTypes(name xdm.QName) ([]xdm.TypeCode, bool) {
	if r.schema == nil {
		return nil, false
	}
	t, ok := r.schema.Types[name]
	if !ok {
		return nil, false
	}
	st, ok := t.(*xsd.SimpleType)
	if !ok || st.Variety != xsd.VarietyUnion {
		return nil, false
	}
	var walk func(u *xsd.SimpleType, depth int) ([]xdm.TypeCode, bool)
	walk = func(u *xsd.SimpleType, depth int) ([]xdm.TypeCode, bool) {
		// A union whose members form a cycle is not a schema this can
		// answer for, and the bound is what keeps a malformed one from
		// recursing forever rather than being a limit on real schemas.
		if depth > 32 || !u.Facets.IsEmpty() {
			return nil, false
		}
		var out []xdm.TypeCode
		for _, m := range u.MemberTypes {
			if m == nil {
				return nil, false
			}
			switch m.Variety {
			case xsd.VarietyUnion:
				sub, pure := walk(m, depth+1)
				if !pure {
					return nil, false
				}
				out = append(out, sub...)
			case xsd.VarietyAtomic:
				// The member is matched by the built-in it erases to,
				// because that is the code an unannotated value carries.
				// A member that is itself a restriction contributes its
				// nearest built-in ancestor, which is what LookupSchemaType
				// resolves for the same reason.
				code, isAtomic, ok := r.LookupSchemaType(m.Name)
				if !ok || !isAtomic {
					if c, found := xpath.BuiltinAtomicTypeCode(m.Name.Local); found &&
						m.Name.URI == xsd.NSSchema {
						out = append(out, c)
						continue
					}
					return nil, false
				}
				out = append(out, code)
			default:
				// VarietyList. 2.5 excludes a union with a list type
				// anywhere in its transitive membership outright.
				return nil, false
			}
		}
		return out, true
	}
	members, pure := walk(st, 0)
	if !pure || len(members) == 0 {
		return nil, false
	}
	return members, true
}

// compileExpr compiles an expression in an element's namespace context and
// binds the element's base URI to it.
//
// The static base URI is a property of the element the expression is written
// on, not of the module: section 5.8 makes xml:base on any element change it
// for everything within. Compiling through here is what keeps that true
// without threading a base URI through every call site.
func compileExpr(src string, ns xpath.NamespaceResolver) (*xpath.Compiled, error) {
	// The version is read from the resolver before compiling rather than
	// applied after, because it decides how the expression *parses*: an
	// inline function or a map constructor is a syntax error in 2.0, so
	// compiling first and versioning afterwards would reject a legal 3.0
	// stylesheet before there was anything to version.
	v := xpath.XPath20
	if r, ok := ns.(*nsResolver); ok {
		v = r.xpathVersion
	}
	// A named function reference resolves at the processor's version, not the
	// module's: "#N" names a function, and which functions exist is already a
	// processor property (see xpath.Context.LibraryVersion). The XSLT suite
	// runs version="2.0" modules scoped XSLT30+ that write "current-group#0"
	// and "system-property#1", and a 3.0 processor must resolve them. Every
	// other 3.0 construct stays gated on the module's own declaration.
	refFloor := xpath.Version(0)
	if processorAtLeast30() {
		refFloor = xpath.XPath31
	}
	c, err := xpath.CompileVersionRefFloor(src, ns, v, refFloor)
	if err != nil {
		return nil, err
	}
	if r, ok := ns.(*nsResolver); ok {
		if r.baseURI != "" {
			c = c.WithStaticBaseURI(r.baseURI)
		}
		// The package the expression was written in, for the whitespace
		// declarations 4.4 scopes to it. Bound even for the top-level
		// package, whose zero would otherwise be indistinguishable from
		// "nothing attached".
		if r.pkg >= 0 {
			c = c.WithStaticHost(hostPackage(r.pkg))
		}
		// [xsl:]default-collation is static in the same way, and is what the
		// collation-taking functions use when given no collation argument.
		if r.collation != "" {
			if coll, err := xpath.ResolveCollation(r.collation); err == nil {
				c = c.WithDefaultCollation(coll)
			}
		}
		// XSLT 1.0 backwards compatibility is static in the same way, and
		// binding it here is what puts every expression written inside a
		// version="1.0" scope -- and only those -- under the appendix B.1
		// coercion rules.
		c = c.WithCompatMode(r.compat)
	}
	return c, nil
}

func (r *nsResolver) ResolvePrefix(p string) (string, bool) {
	if p == "xml" {
		return xdm.NSXML, true
	}
	if uri, ok := r.bindings[p]; ok {
		return uri, ok
	}
	// XSLT 3.0 section 3.1 predeclares four prefixes in every stylesheet, so
	// that map:get and math:pi can be written without an xmlns declaration.
	// They are only defaults: a stylesheet that binds the prefix itself wins,
	// which is why they are consulted after the explicit bindings rather than
	// merged into them.
	//
	// Only for a 3.0 stylesheet. Predeclaring them for 2.0 would resolve a
	// prefix that a conforming 2.0 processor reports as unbound, and would do
	// it silently -- the stylesheet would work here and fail elsewhere.
	if r.xpathVersion.AtLeast31() {
		switch p {
		case "map":
			return xdm.NSMap, true
		case "array":
			return xdm.NSArray, true
		case "math":
			return xdm.NSMath, true
		case "err":
			return xdm.NSErr, true
		}
	}
	return "", false
}

func (r *nsResolver) DefaultElementNamespace() string  { return r.defaultNS }
func (r *nsResolver) DefaultFunctionNamespace() string { return xdm.NSFN }

// resolveQNameAttr resolves a lexical QName found in a stylesheet attribute
// against the element's namespace context.
//
// An unprefixed QName in an attribute value is in *no* namespace, unlike an
// unprefixed element name test. This is the rule that makes
// name="foo" and <foo> mean different things inside a default namespace.
func resolveQNameAttr(el *xdm.Node, lex string) (xdm.QName, error) {
	lex = strings.TrimSpace(lex)
	// The EQName form Q{uri}local carries its own namespace, so no prefix has
	// to be in scope for it. The suite writes it wherever a QName is
	// accepted, and rejecting it reported a perfectly well-formed name as an
	// unbound prefix "Q{http".
	if strings.HasPrefix(lex, "Q{") {
		if end := strings.IndexByte(lex, '}'); end > 0 {
			uri, local := lex[2:end], lex[end+1:]
			if !xdm.IsNCName(local) {
				return xdm.QName{}, fmt.Errorf(
					"XTSE0020: %q is not a valid EQName", lex)
			}
			return xdm.QName{URI: uri, Local: local}, nil
		}
	}
	prefix, local := xdm.SplitQName(lex)
	if prefix == "" {
		return xdm.QName{Local: local}, nil
	}
	uri, ok := el.LookupPrefix(prefix)
	if !ok {
		return xdm.QName{}, fmt.Errorf(
			"XTSE0280: unbound namespace prefix %q in %q", prefix, lex)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}

// resolveResultQNameAttr resolves a QName that names an element of the result
// document, where an unprefixed name takes the default namespace.
//
// This differs from resolveQNameAttr, which leaves an unprefixed name in no
// namespace. Only cdata-section-elements uses this rule, because only it
// names elements that the stylesheet also writes as literal result elements —
// where xmlns="uri" does apply.
func resolveResultQNameAttr(el *xdm.Node, lex string) (xdm.QName, error) {
	lex = strings.TrimSpace(lex)
	if prefix, local := xdm.SplitQName(lex); prefix == "" {
		uri, _ := el.LookupPrefix("")
		return xdm.QName{URI: uri, Local: local}, nil
	}
	return resolveQNameAttr(el, lex)
}

// newStylesheetFuncs builds the function library a stylesheet sees.
//
// It is the XPath 2.0 library plus the functions XSLT 2.0 adds — fn:unparsed-text
// and the fn:format-date family. Those are defined by the XSLT specification
// rather than the XPath one, so they belong here rather than in xpath.Builtins:
// a plain XPath 2.0 processor is required to report XPST0017 for them.
func newStylesheetFuncs() *xpath.Library {
	l := xpath.NewLibrary(xpath.Builtins())
	xpath.RegisterXSLTFuncs(l)
	return l
}

// xpathDefaultNamespaceAt returns the namespace unprefixed element and type
// names in an XPath expression take, at the given element.
//
// The attribute is spelled xsl:xpath-default-namespace on a literal result
// element and xpath-default-namespace on an XSLT element, and the innermost
// occurrence wins — the same scoping [xsl:]default-collation has.
func xpathDefaultNamespaceAt(el *xdm.Node) string {
	for n := el; n != nil; n = n.Parent {
		if n.Kind != xdm.KindElement {
			continue
		}
		if n.Name.URI == xdm.NSXSL {
			if a := n.Attr("", "xpath-default-namespace"); a != nil {
				return a.Value
			}
		}
		if a := n.Attr(xdm.NSXSL, "xpath-default-namespace"); a != nil {
			return a.Value
		}
	}
	return ""
}

// defaultCollationAt returns the default collation in force at an element.
//
// [xsl:]default-collation is a standard attribute: it applies to the element
// it is on and to everything within, and an inner one overrides an outer. The
// value is a whitespace-separated list of candidate URIs, of which the first
// the implementation recognises is used — which is how a stylesheet names a
// preferred collation and a fallback in one attribute.
func defaultCollationAt(el *xdm.Node) string {
	for n := el; n != nil; n = n.Parent {
		if n.Kind != xdm.KindElement {
			continue
		}
		v := ""
		if n.Name.URI == xdm.NSXSL {
			if a := n.Attr("", "default-collation"); a != nil {
				v = a.Value
			}
		}
		if v == "" {
			if a := n.Attr(xdm.NSXSL, "default-collation"); a != nil {
				v = a.Value
			}
		}
		if v == "" {
			continue
		}
		for _, uri := range strings.Fields(v) {
			if _, err := xpath.ResolveCollation(uri); err == nil {
				return uri
			}
		}
		// None recognised: the declaration is still in force and shadows any
		// outer one, so the codepoint collation applies rather than an
		// ancestor's choice.
		return ""
	}
	return ""
}

// declaresMode reports whether any template names mode as an explicit mode.
//
// It backs XTDE0045, whose wording is that the initial mode must "match the
// expanded-QName in the mode attribute of any template". The pseudo-tokens
// "#all" and "#default" are deliberately not names: a template with
// mode="#all" applies in the mode but does not declare it, which is the
// resolution recorded in W3C bugzilla 3690 and what initial-mode-002 asserts.
// Mode names in Template.Mode are already Clark-form, so mode must be too.
func (s *Stylesheet) declaresMode(mode string) bool {
	// XSLT 3.0 added xsl:mode, which declares a mode in its own right: a mode
	// that is declared but has no template rules is a legitimate mode whose
	// on-no-match action is the whole of its behaviour, and running the
	// transform in it is exactly what mode-1405 does.
	if _, ok := s.modeNoMatch[mode]; ok {
		return true
	}
	if _, ok := s.modeAccums[mode]; ok {
		return true
	}
	if s.declaredModeNames[mode] {
		return true
	}
	for _, t := range s.templates {
		for _, m := range t.Mode {
			if m == mode {
				return true
			}
		}
	}
	return false
}

// keyDefsFor returns the xsl:key declarations of one name that are in scope
// for a call written in pkg.
//
// Section 3.5.5: keys "within a package have local scope within that package
// -- they are all effectively private". So two packages may declare the same
// key name with different match and use expressions, and neither sees the
// other's. override-misc-004 is exactly that: the used package indexes on the
// element's content and the using package on its name, both under "k", and
// each package's own template must get its own index.
//
// A package with no declaration of the name falls back to nothing rather than
// to another package's, which is what makes the scoping real. The fallback
// that does exist is for modules that never set a package at all: everything
// compiled outside any xsl:use-package carries package zero, so a plain
// stylesheet keeps every key it declares.
func (s *Stylesheet) keyDefsFor(pkg int, name string) []*keyDef {
	all := s.keys[name]
	if len(all) == 0 {
		return nil
	}
	var out []*keyDef
	for _, k := range all {
		if k.pkg == pkg {
			out = append(out, k)
		}
	}
	return out
}
