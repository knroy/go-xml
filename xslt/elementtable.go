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
