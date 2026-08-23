package xslt

import (
	"fmt"
	"sort"
	"strings"

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
	// importPrecedence orders templates from imported stylesheets below those
	// of the importing one.
	importPrecedence int
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
	// Tunnel marks a tunnel parameter, which passes through templates that do
	// not declare it.
	Tunnel bool
	// precedence is the import precedence of the module that declared this
	// variable, so that a duplicate at the *same* precedence can be
	// distinguished from a legitimate override at a higher one.
	precedence int
	// AsType is the compiled "as" declaration, applied to the value when
	// present. XSLT converts the value to this type rather than merely
	// checking it, so it changes results and not just error messages.
	AsType *sequenceType
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
	// MediaType is the media type of the output. It affects no serialised
	// character; it is metadata a caller passes on.
	MediaType string
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
			decimalFormats:   map[string]*DecimalFormat{},
			attributeSets:    map[string][]*attributeSet{},
			namespaceAliases: map[string]nsAlias{},
			characterMaps:    map[string]map[rune]string{},
			funcs:            newStylesheetFuncs(),
			baseURI:          stylesheetBase(doc, opts.BaseURI),
			output: OutputSettings{
				Method:   "xml",
				Encoding: "UTF-8",
			},
		},
	}
	// compileSchema is package state for the duration of this call; see its
	// declaration. Clearing it on the way out keeps one compilation from
	// leaking a schema into the next.
	compileSchema = nil
	defer func() { compileSchema = nil }()

	if err := c.compileDocument(doc, 0); err != nil {
		return nil, err
	}
	c.sheet.sortTemplates()
	// Character-map inclusion is resolved before the xsl:output tables are
	// flattened, and both after every module, so that a map may name one
	// declared later or in a module imported afterwards.
	if err := c.resolveCharacterMapIncludes(); err != nil {
		return nil, err
	}
	if err := c.checkCallTemplateParams(); err != nil {
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
	// StaticParams supplies values for top-level xsl:param at compile time.
	StaticParams map[string]string

	// SchemaResolver loads the schemas named by xsl:import-schema. Nil
	// disables loading by location, for the same reason a nil Resolver
	// disables xsl:include: following a location means fetching whatever
	// the stylesheet names. An inline <xs:schema> child needs no resolver.
	SchemaResolver xsd.Resolver
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
	// schema is what xsl:import-schema brought in, or nil. It makes the
	// stylesheet's imported types part of the static context, which is what
	// lets "instance of my:partNumberType" resolve at all.
	schema *xsd.Schema
}

// compileSchema is the schema in force while a stylesheet is being compiled.
//
// It is package state rather than a parameter because newNSResolver is called
// from a dozen places that build a resolver for one element and have no
// compiler in scope, and threading a schema through all of them would touch
// far more code than the feature is worth. Compilation is single-threaded
// through Compile, which sets and clears this around the whole run; a compiled
// Stylesheet holds its own schema and never reads this again.
var compileSchema *xsd.Schema

func newNSResolver(el *xdm.Node, defaultElementNS string) *nsResolver {
	return &nsResolver{
		bindings:  el.InScopeNamespaces(),
		defaultNS: defaultElementNS,
		baseURI:   el.BaseURI,
		schema:    compileSchema,
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
	c, err := xpath.Compile(src, ns)
	if err != nil {
		return nil, err
	}
	if r, ok := ns.(*nsResolver); ok && r.baseURI != "" {
		c = c.WithStaticBaseURI(r.baseURI)
	}
	return c, nil
}

func (r *nsResolver) ResolvePrefix(p string) (string, bool) {
	if p == "xml" {
		return xdm.NSXML, true
	}
	uri, ok := r.bindings[p]
	return uri, ok
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
	prefix, local := xdm.SplitQName(lex)
	if prefix == "" {
		return xdm.QName{Local: local}, nil
	}
	uri, ok := el.LookupPrefix(prefix)
	if !ok {
		return xdm.QName{}, fmt.Errorf("unbound namespace prefix %q in %q", prefix, lex)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
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
