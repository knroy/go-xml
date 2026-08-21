package xslt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
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
	// keys holds xsl:key declarations, grouped by name.
	keys map[string][]*keyDef
	// decimalFormats holds xsl:decimal-format declarations by Clark name;
	// the unnamed default is stored under "".
	decimalFormats map[string]*DecimalFormat
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
	// AsType is the compiled "as" declaration, applied to the value when
	// present. XSLT converts the value to this type rather than merely
	// checking it, so it changes results and not just error messages.
	AsType *sequenceType
}

// keyDef is a compiled xsl:key.
type keyDef struct {
	match *Pattern
	use   *xpath.Compiled
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

// Compile compiles a stylesheet from a parsed XSLT document.
func Compile(doc *xdm.Node, opts CompileOptions) (*Stylesheet, error) {
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
			output: OutputSettings{
				Method:   "xml",
				Encoding: "UTF-8",
			},
		},
	}
	if err := c.compileDocument(doc, 0); err != nil {
		return nil, err
	}
	c.sheet.sortTemplates()
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
}

func newNSResolver(el *xdm.Node, defaultElementNS string) *nsResolver {
	return &nsResolver{
		bindings:  el.InScopeNamespaces(),
		defaultNS: defaultElementNS,
	}
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
