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
	// funcs holds xsl:function declarations.
	funcs *xpath.Library
	// baseURI is where the principal stylesheet module was read from.
	//
	// It is the static base URI of every expression the stylesheet contains,
	// which is what fn:doc, fn:document and fn:resolve-uri resolve a relative
	// reference against when there is no context node to take one from — a
	// transform started from a named template has none.
	baseURI string
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
	// accumulators holds xsl:accumulator declarations by Clark name, and
	// accumOrder keeps the declaration order that fn:accumulator-before
	// resolves ties by. See accumulator.go.
	accumulators map[string]*accumulatorDef
	accumOrder   []string
	// modeAccums holds xsl:mode/@use-accumulators by Clark mode name, which
	// says which accumulators may be read while that mode is current.
	modeAccums map[string]*modeAccumulators
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
	// asType is xsl:template/@as, which constrains what the template's
	// sequence constructor may produce. Section 6.1 applies the function
	// conversion rules to the result, so it converts as well as checks.
	asType *sequenceType
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
	// Version is xsl:output/@version; "5.0" selects the HTML5 doctype.
	Version string
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
	// The XPath version override is package state on the same terms and under
	// the same lock; see overrideXPathVersion.
	overrideXPathVersion = opts.XPathVersion
	defer func() { overrideXPathVersion = nil }()

	if err := c.compileDocument(doc, 0); err != nil {
		return nil, err
	}
	c.sheet.sortTemplates()
	// A global variable overridden by a higher-precedence declaration is not
	// evaluated at all, so the overridden bindings are dropped before the
	// stylesheet is handed back.
	c.pruneOverriddenGlobals()
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
	if err := c.checkAttributeSetRefs(); err != nil {
		return nil, err
	}
	// XPST0017 likewise: a match pattern may call an xsl:function declared
	// after it, or in a module imported later, so pattern function calls are
	// resolved once every declaration is in.
	if err := c.checkPatternFuncs(); err != nil {
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
	// xpathVersion is the version of XPath the expressions written on this
	// element are in. It is static in exactly the way the base URI and the
	// default collation are, and for the same reason: it is a property of
	// where the expression was written. A version="3.0" stylesheet writes
	// XPath 3.1, a version="2.0" one writes XPath 2.0, and an included module
	// keeps its own answer rather than the importing module's.
	xpathVersion xpath.Version
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

		xpathVersion: xpathVersionAt(el),
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
	c, err := xpath.CompileVersion(src, ns, v)
	if err != nil {
		return nil, err
	}
	if r, ok := ns.(*nsResolver); ok {
		if r.baseURI != "" {
			c = c.WithStaticBaseURI(r.baseURI)
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
	for _, t := range s.templates {
		for _, m := range t.Mode {
			if m == mode {
				return true
			}
		}
	}
	return false
}
