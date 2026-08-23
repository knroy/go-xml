package xslt

// The XSLT 2.0 element grammar, from the element syntax summaries of the
// specification.
//
// Every XSLT element is defined there by a summary naming its attributes, which
// are required or optional, and for many of them the exact set of values
// permitted. Three of the commonest static errors are decided by that table
// alone:
//
//   - XTSE0010, an element that is not an XSLT element or is not allowed here;
//   - XTSE0020, an attribute whose value is not one the summary permits;
//   - XTSE0090, an attribute the summary does not allow on that element.
//
// Hand-writing 49 elements and 170 attributes invites exactly the kind of
// transcription error that is hard to see in review, so this table was
// extracted mechanically from the specification"s own markup rather than
// typed. It is checked by the conformance suite, which is what would catch a
// mistake in the extraction.
//
// Enumerations here are only those the summary states as a closed set of
// quoted alternatives. An attribute whose value is an expression, a QName or a
// URI is recorded as having no enumeration, because its validity is not a
// question this table can answer.
type elementDef struct {
	// attrs maps an attribute name to what the summary says about it.
	attrs map[string]attrDef
}

type attrDef struct {
	required bool
	// values is the closed set of permitted values, or nil when the summary
	// does not give one.
	values []string
	// avt marks an attribute value template, whose value may be "{...}" and
	// therefore cannot be checked against the enumeration at compile time.
	avt bool
}

// xsltElements is the grammar, keyed by local name.
var xsltElements = map[string]elementDef{
	"stylesheet": {attrs: map[string]attrDef{
		"id":                         {},
		"extension-element-prefixes": {},
		"exclude-result-prefixes":    {},
		"version":                    {required: true},
		"xpath-default-namespace":    {},
		"default-validation":         {values: []string{"preserve", "strip"}},
		"default-collation":          {},
		"input-type-annotations":     {values: []string{"preserve", "strip", "unspecified"}},
	}},
	"transform": {attrs: map[string]attrDef{
		"id":                         {},
		"extension-element-prefixes": {},
		"exclude-result-prefixes":    {},
		"version":                    {required: true},
		"xpath-default-namespace":    {},
		"default-validation":         {values: []string{"preserve", "strip"}},
		"default-collation":          {},
		"input-type-annotations":     {values: []string{"preserve", "strip", "unspecified"}},
	}},
	"include": {attrs: map[string]attrDef{
		"href": {required: true},
	}},
	"import": {attrs: map[string]attrDef{
		"href": {required: true},
	}},
	"import-schema": {attrs: map[string]attrDef{
		"namespace":       {},
		"schema-location": {},
	}},
	"strip-space": {attrs: map[string]attrDef{
		"elements": {required: true},
	}},
	"preserve-space": {attrs: map[string]attrDef{
		"elements": {required: true},
	}},
	"template": {attrs: map[string]attrDef{
		"match":    {},
		"name":     {},
		"priority": {},
		"mode":     {},
		"as":       {},
	}},
	"apply-templates": {attrs: map[string]attrDef{
		"select": {},
		"mode":   {},
	}},
	"apply-imports": {attrs: map[string]attrDef{}},
	"next-match":    {attrs: map[string]attrDef{}},
	"for-each": {attrs: map[string]attrDef{
		"select": {required: true},
	}},
	"if": {attrs: map[string]attrDef{
		"test": {required: true},
	}},
	"choose": {attrs: map[string]attrDef{}},
	"when": {attrs: map[string]attrDef{
		"test": {required: true},
	}},
	"otherwise": {attrs: map[string]attrDef{}},
	"variable": {attrs: map[string]attrDef{
		"name":   {required: true},
		"select": {},
		"as":     {},
	}},
	"param": {attrs: map[string]attrDef{
		"name":     {required: true},
		"select":   {},
		"as":       {},
		"required": {values: []string{"yes", "no"}},
		"tunnel":   {values: []string{"yes", "no"}},
	}},
	"call-template": {attrs: map[string]attrDef{
		"name": {required: true},
	}},
	"with-param": {attrs: map[string]attrDef{
		"name":   {required: true},
		"select": {},
		"as":     {},
		"tunnel": {values: []string{"yes", "no"}},
	}},
	"attribute-set": {attrs: map[string]attrDef{
		"name":               {required: true},
		"use-attribute-sets": {},
	}},
	"function": {attrs: map[string]attrDef{
		"name":     {required: true},
		"as":       {},
		"override": {values: []string{"yes", "no"}},
	}},
	"namespace-alias": {attrs: map[string]attrDef{
		"stylesheet-prefix": {required: true},
		"result-prefix":     {required: true},
	}},
	"element": {attrs: map[string]attrDef{
		"name":               {required: true, avt: true},
		"namespace":          {avt: true},
		"inherit-namespaces": {values: []string{"yes", "no"}},
		"use-attribute-sets": {},
		"type":               {},
		"validation":         {values: []string{"strict", "lax", "preserve", "strip"}},
	}},
	"attribute": {attrs: map[string]attrDef{
		"name":       {required: true, avt: true},
		"namespace":  {avt: true},
		"select":     {},
		"separator":  {avt: true},
		"type":       {},
		"validation": {values: []string{"strict", "lax", "preserve", "strip"}},
	}},
	"text": {attrs: map[string]attrDef{}},
	"value-of": {attrs: map[string]attrDef{
		"select":    {},
		"separator": {avt: true},
	}},
	"document": {attrs: map[string]attrDef{
		"validation": {values: []string{"strict", "lax", "preserve", "strip"}},
		"type":       {},
	}},
	"processing-instruction": {attrs: map[string]attrDef{
		"name":   {required: true, avt: true},
		"select": {},
	}},
	"namespace": {attrs: map[string]attrDef{
		"name":   {required: true, avt: true},
		"select": {},
	}},
	"comment": {attrs: map[string]attrDef{
		"select": {},
	}},
	"copy": {attrs: map[string]attrDef{
		"copy-namespaces":    {values: []string{"yes", "no"}},
		"inherit-namespaces": {values: []string{"yes", "no"}},
		"use-attribute-sets": {},
		"type":               {},
		"validation":         {values: []string{"strict", "lax", "preserve", "strip"}},
	}},
	"copy-of": {attrs: map[string]attrDef{
		"select":          {required: true},
		"copy-namespaces": {values: []string{"yes", "no"}},
		"type":            {},
		"validation":      {},
	}},
	"sequence": {attrs: map[string]attrDef{
		"select": {required: true},
	}},
	"number": {attrs: map[string]attrDef{
		"value":              {},
		"select":             {},
		"level":              {values: []string{"single", "multiple", "any"}},
		"count":              {},
		"from":               {},
		"format":             {avt: true},
		"lang":               {avt: true},
		"letter-value":       {values: []string{"alphabetic", "traditional"}, avt: true},
		"ordinal":            {avt: true},
		"grouping-separator": {avt: true},
		"grouping-size":      {avt: true},
	}},
	"sort": {attrs: map[string]attrDef{
		"select":     {},
		"lang":       {avt: true},
		"order":      {values: []string{"ascending", "descending"}, avt: true},
		"collation":  {avt: true},
		"stable":     {values: []string{"yes", "no"}, avt: true},
		"case-order": {values: []string{"upper-first", "lower-first"}, avt: true},
		"data-type":  {avt: true},
	}},
	"perform-sort": {attrs: map[string]attrDef{
		"select": {},
	}},
	"for-each-group": {attrs: map[string]attrDef{
		"select":              {required: true},
		"group-by":            {},
		"group-adjacent":      {},
		"group-starting-with": {},
		"group-ending-with":   {},
		"collation":           {avt: true},
	}},
	"analyze-string": {attrs: map[string]attrDef{
		"select": {required: true},
		"regex":  {required: true, avt: true},
		"flags":  {avt: true},
	}},
	"matching-substring":     {attrs: map[string]attrDef{}},
	"non-matching-substring": {attrs: map[string]attrDef{}},
	"key": {attrs: map[string]attrDef{
		"name":      {required: true},
		"match":     {required: true},
		"use":       {},
		"collation": {},
	}},
	"decimal-format": {attrs: map[string]attrDef{
		"name":               {},
		"decimal-separator":  {},
		"grouping-separator": {},
		"infinity":           {},
		"minus-sign":         {},
		"NaN":                {},
		"percent":            {},
		"per-mille":          {},
		"zero-digit":         {},
		"digit":              {},
		"pattern-separator":  {},
	}},
	"message": {attrs: map[string]attrDef{
		"select":    {},
		"terminate": {values: []string{"yes", "no"}, avt: true},
	}},
	"fallback": {attrs: map[string]attrDef{}},
	"result-document": {attrs: map[string]attrDef{
		"format":                 {avt: true},
		"href":                   {avt: true},
		"validation":             {values: []string{"strict", "lax", "preserve", "strip"}},
		"type":                   {},
		"method":                 {avt: true},
		"byte-order-mark":        {values: []string{"yes", "no"}, avt: true},
		"cdata-section-elements": {avt: true},
		"doctype-public":         {avt: true},
		"doctype-system":         {avt: true},
		"encoding":               {avt: true},
		"escape-uri-attributes":  {values: []string{"yes", "no"}, avt: true},
		"include-content-type":   {values: []string{"yes", "no"}, avt: true},
		"indent":                 {values: []string{"yes", "no"}, avt: true},
		"media-type":             {avt: true},
		"normalization-form":     {avt: true},
		"omit-xml-declaration":   {values: []string{"yes", "no"}, avt: true},
		"standalone":             {values: []string{"yes", "no", "omit"}, avt: true},
		"undeclare-prefixes":     {values: []string{"yes", "no"}, avt: true},
		"use-character-maps":     {},
		"output-version":         {avt: true},
	}},
	"output": {attrs: map[string]attrDef{
		"name":                   {},
		"method":                 {},
		"byte-order-mark":        {values: []string{"yes", "no"}},
		"cdata-section-elements": {},
		"doctype-public":         {},
		"doctype-system":         {},
		"encoding":               {},
		"escape-uri-attributes":  {values: []string{"yes", "no"}},
		"include-content-type":   {values: []string{"yes", "no"}},
		"indent":                 {values: []string{"yes", "no"}},
		"media-type":             {},
		"normalization-form":     {},
		"omit-xml-declaration":   {values: []string{"yes", "no"}},
		"standalone":             {values: []string{"yes", "no", "omit"}},
		"undeclare-prefixes":     {values: []string{"yes", "no"}},
		"use-character-maps":     {},
		"version":                {},
	}},
	"character-map": {attrs: map[string]attrDef{
		"name":               {required: true},
		"use-character-maps": {},
	}},
	"output-character": {attrs: map[string]attrDef{
		"character": {required: true},
		"string":    {required: true},
	}},
}

// contentModels records what each XSLT element may contain, taken from the
// Content line of the specification's element syntax summaries and extracted
// mechanically from the same markup as the attribute table above.
//
// seqCtor means a sequence constructor is allowed, which admits any
// instruction, any literal result element and text. kids lists the XSLT
// elements the model names explicitly; when there is no sequence
// constructor that list is exhaustive, which is what makes an xsl:sort
// inside xsl:apply-imports a static error rather than a no-op.
type contentModel struct {
	seqCtor bool
	// decls marks the two elements whose model says "other-declarations",
	// which stands for the whole declaration category rather than naming an
	// element. Reading it as a literal name refused every stylesheet that
	// contained an xsl:template.
	decls bool
	// foreign is the local name of a non-XSLT element the model names, which
	// is xs:schema inside xsl:import-schema and nothing else.
	foreign string
	pcdata  bool
	kids    map[string]bool
	model   string
}

var contentModels = map[string]contentModel{
	"analyze-string":         {seqCtor: false, pcdata: false, kids: map[string]bool{"fallback": true, "matching-substring": true, "non-matching-substring": true}, model: "(xsl:matching-substring?, xsl:non-matching-substring?, xsl:fallback*)"},
	"apply-imports":          {seqCtor: false, pcdata: false, kids: map[string]bool{"with-param": true}, model: "xsl:with-param*"},
	"apply-templates":        {seqCtor: false, pcdata: false, kids: map[string]bool{"sort": true, "with-param": true}, model: "(xsl:sort | xsl:with-param)*"},
	"attribute":              {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"attribute-set":          {seqCtor: false, pcdata: false, kids: map[string]bool{"attribute": true}, model: "xsl:attribute*"},
	"call-template":          {seqCtor: false, pcdata: false, kids: map[string]bool{"with-param": true}, model: "xsl:with-param*"},
	"character-map":          {seqCtor: false, pcdata: false, kids: map[string]bool{"output-character": true}, model: "(xsl:output-character*)"},
	"choose":                 {seqCtor: false, pcdata: false, kids: map[string]bool{"otherwise": true, "when": true}, model: "(xsl:when+, xsl:otherwise?)"},
	"comment":                {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"copy":                   {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"copy-of":                {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"decimal-format":         {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"document":               {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"element":                {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"fallback":               {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"for-each":               {seqCtor: true, pcdata: false, kids: map[string]bool{"sort": true}, model: "(xsl:sort*, sequence-constructor)"},
	"for-each-group":         {seqCtor: true, pcdata: false, kids: map[string]bool{"sort": true}, model: "(xsl:sort*, sequence-constructor)"},
	"function":               {seqCtor: true, pcdata: false, kids: map[string]bool{"param": true}, model: "(xsl:param*, sequence-constructor)"},
	"if":                     {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"import":                 {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"import-schema":          {seqCtor: false, foreign: "schema", pcdata: false, kids: nil, model: "xs:schema?"},
	"include":                {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"key":                    {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"matching-substring":     {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"message":                {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"namespace":              {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"namespace-alias":        {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"next-match":             {seqCtor: false, pcdata: false, kids: map[string]bool{"fallback": true, "with-param": true}, model: "(xsl:with-param | xsl:fallback)*"},
	"non-matching-substring": {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"number":                 {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"otherwise":              {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"output":                 {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"output-character":       {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"param":                  {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"perform-sort":           {seqCtor: true, pcdata: false, kids: map[string]bool{"sort": true}, model: "(xsl:sort+, sequence-constructor)"},
	"preserve-space":         {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"processing-instruction": {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"result-document":        {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"sequence":               {seqCtor: false, pcdata: false, kids: map[string]bool{"fallback": true}, model: "xsl:fallback*"},
	"sort":                   {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"strip-space":            {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"stylesheet":             {seqCtor: false, decls: true, pcdata: false, kids: map[string]bool{"import": true}, model: "(xsl:import*, other-declarations)"},
	"template":               {seqCtor: true, pcdata: false, kids: map[string]bool{"param": true}, model: "(xsl:param*, sequence-constructor)"},
	"text":                   {seqCtor: false, pcdata: true, kids: nil, model: "#PCDATA"},
	"transform":              {seqCtor: false, decls: true, pcdata: false, kids: map[string]bool{"import": true}, model: "(xsl:import*, other-declarations)"},
	"value-of":               {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"variable":               {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"when":                   {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"with-param":             {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
}

// xsltInstructions is the "instruction" category of the element syntax
// summaries: the elements that may appear in a sequence constructor.
//
// xsl:variable and xsl:param are included even though the summaries file them
// as declarations, because both are also permitted in a sequence constructor
// where they bind a local name.
var xsltInstructions = map[string]bool{
	"analyze-string":         true,
	"apply-imports":          true,
	"apply-templates":        true,
	"attribute":              true,
	"call-template":          true,
	"choose":                 true,
	"comment":                true,
	"copy":                   true,
	"copy-of":                true,
	"document":               true,
	"element":                true,
	"fallback":               true,
	"for-each":               true,
	"for-each-group":         true,
	"if":                     true,
	"message":                true,
	"namespace":              true,
	"next-match":             true,
	"number":                 true,
	"param":                  true,
	"perform-sort":           true,
	"processing-instruction": true,
	"result-document":        true,
	"sequence":               true,
	"text":                   true,
	"value-of":               true,
	"variable":               true,
}

// xsltDeclarations is the "declaration" category: the elements permitted as
// children of xsl:stylesheet, which the syntax summary abbreviates to
// "other-declarations" rather than naming.
var xsltDeclarations = map[string]bool{
	"attribute-set":   true,
	"character-map":   true,
	"decimal-format":  true,
	"function":        true,
	"import":          true,
	"import-schema":   true,
	"include":         true,
	"key":             true,
	"namespace-alias": true,
	"output":          true,
	"param":           true,
	"preserve-space":  true,
	"strip-space":     true,
	"template":        true,
	"variable":        true,
}
