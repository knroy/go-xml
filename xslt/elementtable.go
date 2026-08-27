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
	// since30 marks an element XSLT 3.0 introduced.
	//
	// A 2.0 stylesheet using one must be told it is not an XSLT element at
	// all -- XTSE0010, the same error every other conforming 2.0 processor
	// raises -- rather than have it quietly recognised because this engine
	// happens to implement 3.0 as well. package-version-912a is exactly that
	// test: it writes xsl:package in a version="2.0" stylesheet and expects
	// XTSE0010, and without the flag the table answered "known element".
	//
	// Forwards compatibility still applies ahead of this: a 2.0 stylesheet
	// under xsl:version="3.0" is in forwards-compatible mode, and section
	// 3.9 has such an element ignored rather than rejected.
	since30 bool
}

type attrDef struct {
	required bool
	// values is the closed set of permitted values, or nil when the summary
	// does not give one.
	values []string
	// avt marks an attribute value template, whose value may be "{...}" and
	// therefore cannot be checked against the enumeration at compile time.
	avt bool
	// since30 marks an attribute XSLT 3.0 added to an element that existed
	// before it. The element-level flag of the same name cannot express this:
	// xsl:variable is a 2.0 element, but its static attribute is not a 2.0
	// attribute, and a 2.0 stylesheet writing one must get XTSE0090.
	since30 bool
	// optional30 marks a required attribute that XSLT 3.0 made optional,
	// because 3.0 gave the element a second way to say the same thing.
	// xsl:sequence/@select is the case: 3.0 lets a sequence constructor
	// stand in for it, while 2.0 still requires the attribute.
	optional30 bool
	// processor30 is since30 for an attribute whose availability follows the
	// processor rather than the module. XSLT 3.0's new serialisation
	// attributes are the case: result-document-0302 writes build-tree in a
	// version="2.0" module and is scoped XSLT30+, exactly as message-0009
	// writes terminate="true" there -- what decides is whether the processor
	// implements 3.0, not what the module's @version happens to say.
	processor30 bool
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
	"package": {since30: true, attrs: map[string]attrDef{
		"id":                         {},
		"name":                       {},
		"package-version":            {},
		"version":                    {},
		"declared-modes":             {values: []string{"yes", "no", "true", "false", "1", "0"}},
		"extension-element-prefixes": {},
		"exclude-result-prefixes":    {},
		"xpath-default-namespace":    {},
		"default-validation":         {values: []string{"preserve", "strip"}},
		"default-collation":          {},
		"default-mode":               {},
		"input-type-annotations":     {values: []string{"preserve", "strip", "unspecified"}},
		"use-package":                {},
	}},
	// The package-composition elements, XSLT 3.0 section 3.5. All carry
	// since30: an earlier stylesheet using one must be told it is not an
	// XSLT element, which is what every conforming 2.0 processor says.
	"use-package": {since30: true, attrs: map[string]attrDef{
		"name":            {required: true},
		"package-version": {},
	}},
	"expose": {since30: true, attrs: map[string]attrDef{
		"component":  {required: true},
		"names":      {required: true},
		"visibility": {required: true, values: []string{
			"public", "private", "final", "abstract", "hidden"}},
	}},
	"accept": {since30: true, attrs: map[string]attrDef{
		"component":  {required: true},
		"names":      {required: true},
		"visibility": {required: true, values: []string{
			"public", "private", "final", "abstract", "hidden"}},
	}},
	"override": {since30: true, attrs: map[string]attrDef{}},
	"original": {since30: true, attrs: map[string]attrDef{}},
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
	// xsl:mode is an XSLT 3.0 declaration, and 2.0 has no summary for it.
	// It is listed here because everything it can say is configuration a
	// non-streaming XSLT 2.0 processor is free to ignore: streamable="yes"
	// asks for a streaming evaluation, which the specification always allows
	// a processor to satisfy by building the tree instead, and the remaining
	// attributes name defaults that 2.0 already fixes. Rejecting it under
	// XTSE0010 would refuse a stylesheet whose 2.0 meaning is unambiguous,
	// so the declaration is accepted and then does nothing. The attributes
	// are still checked, so a misspelling inside it is not swallowed.
	"mode": {attrs: map[string]attrDef{
		"name":                      {},
		"streamable":                {values: []string{"yes", "no"}},
		"use-accumulators":          {},
		"on-no-match":               {values: []string{"deep-copy", "shallow-copy", "deep-skip", "shallow-skip", "text-only-copy", "fail"}},
		"on-multiple-match":         {values: []string{"use-last", "fail"}},
		"warning-on-no-match":       {values: []string{"yes", "no"}},
		"warning-on-multiple-match": {values: []string{"yes", "no"}},
		// XSLT 3.0 section 6.6: @typed says whether the mode expects typed
		// input. "lax" and "strict" join the booleans, so the vocabulary is
		// not the plain yes/no one.
		"typed": {values: []string{"yes", "no", "true", "false", "1", "0", "lax", "strict", "unspecified"}},
		// A mode declaration's visibility is drawn from the package
		// vocabulary minus "abstract": a mode has no signature to leave
		// unimplemented.
		"visibility": {values: []string{"public", "private", "final", "hidden"}},
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
	"accumulator": {since30: true, attrs: map[string]attrDef{
		"name":          {required: true},
		"initial-value": {required: true},
		"as":            {},
		"streamable":    {values: []string{"yes", "no", "true", "false", "1", "0"}},
	}},
	"accumulator-rule": {since30: true, attrs: map[string]attrDef{
		"match":    {required: true},
		"phase":    {values: []string{"start", "end"}},
		"select":   {},
		"priority": {},
	}},
	"template": {attrs: map[string]attrDef{
		"match":    {},
		"name":     {},
		"priority": {},
		"mode":     {},
		"as":       {},
		// XSLT 3.0 section 3.5: a package declaration states whether it is
		// visible outside the package. Accepted and ignored — this processor
		// compiles a package as a stylesheet, where everything is visible.
		"visibility": {values: []string{"public", "private", "final", "abstract", "hidden"}},
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
	// static marks a declaration whose value is computed before the
	// stylesheet is analysed; see static.go. It is meaningful only on a
	// top-level declaration, and 9.5 makes it XTSE0020 anywhere else — a
	// rule the element grammar cannot state, since it does not know where in
	// the tree the element sits, so it is checked with the other static
	// errors.
	"variable": {attrs: map[string]attrDef{
		"name":   {required: true},
		"select": {},
		"as":     {},
		"static": {since30: true, values: []string{"yes", "no", "true", "false", "1", "0"}},
		// 9.1's signature carries visibility; 9.2's for xsl:param does not,
		// which is why the two entries differ. Where the declaration may
		// appear is varparamattrs.go's rule, not the table's.
		"visibility": {since30: true, values: []string{"public", "private", "final", "abstract", "hidden"}},
	}},
	"param": {attrs: map[string]attrDef{
		"name":     {required: true},
		"select":   {},
		"as":       {},
		"required": {values: []string{"yes", "no"}},
		"tunnel":   {values: []string{"yes", "no"}},
		"static":   {since30: true, values: []string{"yes", "no", "true", "false", "1", "0"}},
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
		"override": {values: []string{"yes", "no", "true", "false", "1", "0"}},
		// 3.0 renamed @override to @override-extension-function and added
		// @new-each-time, which says whether two calls with the same
		// arguments may share one result. Neither changes what this
		// processor does -- it evaluates every call -- but refusing them
		// rejected a legal 3.0 stylesheet outright.
		//
		// processor30 rather than since30: both say what the *processor* may
		// do with a call, not what the module's grammar contains, and
		// function-1032 writes new-each-time in a version="2.0" module scoped
		// XSLT30+ -- exactly as message-0009 writes terminate="true" there.
		"override-extension-function": {
			processor30: true,
			values:      []string{"yes", "no", "true", "false", "1", "0"},
		},
		// "maybe" is the third value, and the default: it leaves the
		// processor free to reuse a result or not.
		"new-each-time": {
			processor30: true,
			values:      []string{"yes", "no", "true", "false", "1", "0", "maybe"},
		},
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
	"text": {attrs: map[string]attrDef{
		"disable-output-escaping": {values: []string{"yes", "no"}},
	}},
	"value-of": {attrs: map[string]attrDef{
		"select":                  {},
		"separator":               {avt: true},
		"disable-output-escaping": {values: []string{"yes", "no"}},
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
		"select":             {since30: true},
		"copy-namespaces":    {values: []string{"yes", "no"}},
		"inherit-namespaces": {values: []string{"yes", "no"}},
		"use-attribute-sets": {},
		"type":               {},
		"validation":         {values: []string{"strict", "lax", "preserve", "strip"}},
	}},
	"copy-of": {attrs: map[string]attrDef{
		"select":          {required: true},
		"copy-namespaces": {values: []string{"yes", "no"}},
		// XSLT 3.0 section 18.3: the copy carries the accumulator values of
		// the node it was copied from, which nothing else can supply — the
		// copy's own tree is all the document the rules would see.
		"copy-accumulators": {values: []string{"yes", "no"}},
		"type":              {},
		"validation":        {},
	}},
	// xsl:evaluate is an XSLT 3.0 instruction, listed here for the same reason
	// xsl:mode is: rejecting it under XTSE0010 is the wrong error at the wrong
	// moment. Section 10.4 defines it entirely in terms of machinery a 2.0
	// processor already has — an XPath expression compiled from a string,
	// against a static context the instruction itself describes — so it can be
	// supported rather than refused. The suite treats it as reachable from a
	// version="2.0" stylesheet: system-property-022 and collations-0128 are
	// both XSLT20+ tests whose subject is a *different* error, or none, and
	// neither is observable while the element is a static error.
	// xsl:iterate and its three companions are XSLT 3.0, and are here for the
	// reason xsl:evaluate is: an XSLT 2.0 processor can execute them exactly,
	// because section 8.4 defines the loop in terms of a sequence, a
	// parameter set and two escape hatches, none of which is new machinery.
	// number-1004 is the case that makes the difference visible — an XSLT 2.0
	// test whose subject is the XTTE0990 that xsl:number raises inside
	// xsl:on-completion, where section 8.4 makes the focus absent. That error
	// is unreachable while the surrounding element is refused outright.
	"iterate": {attrs: map[string]attrDef{
		"select": {required: true},
	}},
	"next-iteration": {attrs: map[string]attrDef{}},
	"break": {attrs: map[string]attrDef{
		"select": {},
	}},
	"on-completion": {attrs: map[string]attrDef{
		"select": {},
	}},
	// The XSLT 3.0 conditional content instructions, section 8.4. Both
	// xsl:on-empty and xsl:on-non-empty share xsl:sequence's summary;
	// xsl:where-populated has no attributes at all.
	// The map constructor instructions of section 14. xsl:map takes no
	// attributes; on xsl:map-entry the key is required and select is the
	// alternative to a sequence constructor.
	"map": {since30: true, attrs: map[string]attrDef{}},
	"map-entry": {since30: true, attrs: map[string]attrDef{
		"key":    {required: true},
		"select": {},
	}},
	"on-empty": {since30: true, attrs: map[string]attrDef{
		"select": {},
	}},
	"on-non-empty": {since30: true, attrs: map[string]attrDef{
		"select": {},
	}},
	"where-populated": {since30: true, attrs: map[string]attrDef{}},
	// xsl:fork, section 16. It has no attributes; its content model is the
	// alternation in 16.1, which compileFork checks.
	"fork": {since30: true, attrs: map[string]attrDef{}},
	// The merging instructions of section 15. Every one of them is XSLT 3.0
	// only, so a version="2.0" stylesheet writing one gets XTSE0010 rather
	// than being told xsl:merge is an element it may use.
	"merge": {since30: true, attrs: map[string]attrDef{}},
	"merge-source": {since30: true, attrs: map[string]attrDef{
		"name":            {},
		"for-each-item":   {},
		"for-each-source": {},
		// The summary types select without a "?", so it is required: with no
		// anchor there is nothing else an xsl:merge-source could select, and
		// merge-032b writes one without it and requires XTSE0010.
		"select":           {required: true},
		"streamable":       {values: []string{"yes", "no", "true", "false", "1", "0"}},
		"use-accumulators": {},
		"sort-before-merge": {
			values: []string{"yes", "no", "true", "false", "1", "0"}},
		"validation": {values: []string{"strict", "lax", "preserve", "strip"}},
		"type":       {},
	}},
	// xsl:merge-key is xsl:sort's summary without @stable: 15.5 states the
	// exception in as many words, and merge-010 is the test that writes
	// stable="yes" on one and requires XTSE0090.
	"merge-key": {since30: true, attrs: map[string]attrDef{
		"select":     {},
		"lang":       {avt: true},
		"order":      {values: []string{"ascending", "descending"}, avt: true},
		"collation":  {avt: true},
		"case-order": {values: []string{"upper-first", "lower-first"}, avt: true},
		"data-type":  {avt: true},
	}},
	"merge-action": {since30: true, attrs: map[string]attrDef{}},
	"evaluate": {attrs: map[string]attrDef{
		"xpath":             {required: true},
		"as":                {},
		"base-uri":          {avt: true},
		"with-params":       {},
		"context-item":      {},
		"namespace-context": {},
		"schema-aware":      {avt: true},
	}},
	// XSLT 3.0 section 10.1.1, a child of xsl:template only.
	"context-item": {since30: true, attrs: map[string]attrDef{
		"as":  {},
		"use": {values: []string{"required", "optional", "absent"}},
	}},
	"sequence": {attrs: map[string]attrDef{
		"select": {required: true, optional30: true},
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
		"start-at":           {avt: true, since30: true},
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
		// composite="yes" makes the whole key sequence one grouping key.
		// XSLT 3.0 only: a 2.0 stylesheet naming it must still be told so.
		"composite": {since30: true,
			values: []string{"yes", "no", "true", "false", "1", "0"}},
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
		"select": {},
		// 3.0 declares terminate as an AVT yielding xs:boolean, which widens
		// the set to the four lexical forms of the type. That widening is
		// applied by allowsBoolAliases rather than listed here, so the
		// enumeration stays the 2.0 pair and a 2.0 module is held to it.
		"terminate": {values: []string{"yes", "no"}, avt: true},
		// Not marked since30: message-0009 writes @error-code on a
		// version="2.0" module and is scoped XSLT30+, so the attribute
		// follows the processor's version rather than the module's. The
		// XSLT 2.0 run rejects it because that processor's own suite has no
		// such case, and the 2.0 grammar is unchanged for every module a 2.0
		// processor sees.
		"error-code": {avt: true},
	}},
	"fallback": {attrs: map[string]attrDef{}},
	"try": {since30: true, attrs: map[string]attrDef{
		"select":          {},
		"rollback-output": {values: []string{"yes", "no"}},
	}},
	"catch": {since30: true, attrs: map[string]attrDef{
		"errors": {},
		"select": {},
	}},
	// xsl:source-document reads a document by URI and evaluates its body with
	// that document as the context item. streamable asks for streamed
	// evaluation, which this engine does not do; 18.1 defines the result as
	// that of the non-streaming process either way, so the attribute is
	// accepted and the instruction evaluated conventionally.
	"source-document": {since30: true, attrs: map[string]attrDef{
		"href":             {required: true, avt: true},
		"streamable":       {values: []string{"yes", "no", "true", "false", "1", "0"}},
		"use-accumulators": {},
		"validation":       {values: []string{"strict", "lax", "preserve", "strip"}},
		"type":             {},
	}},
	"result-document": {attrs: map[string]attrDef{
		// build-tree says whether the raw result is normalised into a final
		// result tree or serialised as the sequence it is; see 2.3.6. XSLT
		// 3.0 added it, along with the two JSON serialisation parameters.
		"build-tree":              {processor30: true, avt: true, values: []string{"yes", "no", "true", "false", "1", "0"}},
		"allow-duplicate-names":   {processor30: true, avt: true, values: []string{"yes", "no", "true", "false", "1", "0"}},
		"json-node-output-method": {processor30: true, avt: true},
		"parameter-document":      {processor30: true, avt: true},
		"item-separator":          {avt: true},
		"html-version":            {avt: true},
		"suppress-indentation":    {avt: true},
		"format":                  {avt: true},
		"href":                    {avt: true},
		"validation":              {values: []string{"strict", "lax", "preserve", "strip"}},
		"type":                    {},
		"method":                  {avt: true},
		"byte-order-mark":         {values: []string{"yes", "no"}, avt: true},
		"cdata-section-elements":  {avt: true},
		"doctype-public":          {avt: true},
		"doctype-system":          {avt: true},
		"encoding":                {avt: true},
		"escape-uri-attributes":   {values: []string{"yes", "no"}, avt: true},
		"include-content-type":    {values: []string{"yes", "no"}, avt: true},
		"indent":                  {values: []string{"yes", "no"}, avt: true},
		"media-type":              {avt: true},
		"normalization-form":      {avt: true},
		"omit-xml-declaration":    {values: []string{"yes", "no"}, avt: true},
		"standalone":              {values: []string{"yes", "no", "omit"}, avt: true},
		"undeclare-prefixes":      {values: []string{"yes", "no"}, avt: true},
		"use-character-maps":      {},
		"output-version":          {avt: true},
	}},
	"output": {attrs: map[string]attrDef{
		"name":   {},
		"method": {},
		// html-version selects between the HTML 4 and HTML 5 serialisation
		// rules. It was added after XSLT 2.0, but the test suite uses it in
		// tests declared XSLT20+, and rejecting an attribute a stylesheet may
		// legitimately carry is worse than reading one this version does not
		// otherwise act on.
		"html-version": {},
		// suppress-indentation names elements whose content is not
		// re-indented. Like html-version it postdates XSLT 2.0, and is read
		// rather than rejected for the same reason.
		"suppress-indentation": {},
		// item-separator is the string written between adjacent items of the
		// sequence being serialised. It is an XSLT 3.0 / Serialization 3.1
		// parameter, accepted here because the suite carries it in tests
		// declared XSLT20+ and rejecting the whole stylesheet would hide the
		// condition those tests are about.
		//
		// It is applied during sequence normalisation (5.7.1 step 3), in the
		// tree rather than at serialisation: validation-0214 asks for
		// XTTE1550 from validating the result document, and the error is due
		// precisely because the inserted separators are text nodes at
		// document level. A separator written only by the serialiser would
		// leave that document schema-valid.
		"item-separator":         {},
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
	// seqCtor30 marks an element whose content became a sequence constructor
	// in XSLT 3.0 and was something narrower before. xsl:sequence is the
	// case: 2.0 required the select attribute and allowed only xsl:fallback
	// children, while 3.0 makes the two forms alternatives.
	seqCtor30 bool
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
	"try":                    {seqCtor: true, pcdata: false, kids: map[string]bool{"catch": true, "fallback": true}, model: "(sequence-constructor, xsl:catch+)"},
	"catch":                  {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"for-each":               {seqCtor: true, pcdata: false, kids: map[string]bool{"sort": true}, model: "(xsl:sort*, sequence-constructor)"},
	"for-each-group":         {seqCtor: true, pcdata: false, kids: map[string]bool{"sort": true}, model: "(xsl:sort*, sequence-constructor)"},
	"function":               {seqCtor: true, pcdata: false, kids: map[string]bool{"param": true}, model: "(xsl:param*, sequence-constructor)"},
	"if":                     {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"iterate":                {seqCtor: true, pcdata: false, kids: map[string]bool{"param": true, "on-completion": true}, model: "(xsl:param*, xsl:on-completion?, sequence-constructor)"},
	"next-iteration":         {seqCtor: false, pcdata: false, kids: map[string]bool{"with-param": true}, model: "xsl:with-param*"},
	"break":                  {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"on-completion":          {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"map":                    {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"map-entry":              {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"on-empty":               {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"on-non-empty":           {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"where-populated":        {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"fork":                   {seqCtor: false, pcdata: false, kids: map[string]bool{"sequence": true, "for-each-group": true, "fallback": true}, model: "(xsl:fallback*, ((xsl:sequence, xsl:fallback*)* | (xsl:for-each-group, xsl:fallback*)))"},
	"merge":                  {seqCtor: false, pcdata: false, kids: map[string]bool{"merge-source": true, "merge-action": true, "fallback": true}, model: "(xsl:merge-source+, xsl:merge-action, xsl:fallback*)"},
	"merge-source":           {seqCtor: false, pcdata: false, kids: map[string]bool{"merge-key": true}, model: "xsl:merge-key+"},
	"merge-key":              {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"merge-action":           {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"evaluate":               {seqCtor: false, pcdata: false, kids: map[string]bool{"with-param": true, "fallback": true}, model: "(xsl:with-param | xsl:fallback)*"},
	"import":                 {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"import-schema":          {seqCtor: false, foreign: "schema", pcdata: false, kids: nil, model: "xs:schema?"},
	"include":                {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"key":                    {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"matching-substring":     {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"message":                {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"namespace":              {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"mode":                   {seqCtor: false, pcdata: false, kids: nil, model: ""},
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
	"source-document":        {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"sequence":               {seqCtor30: true, pcdata: false, kids: map[string]bool{"fallback": true}, model: "xsl:fallback*"},
	"sort":                   {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"strip-space":            {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"stylesheet":             {seqCtor: false, decls: true, pcdata: false, kids: map[string]bool{"import": true}, model: "(xsl:import*, other-declarations)"},
	"package":                {seqCtor: false, decls: true, pcdata: false, kids: map[string]bool{"import": true, "use-package": true, "expose": true}, model: "(xsl:import*, xsl:use-package*, xsl:expose*, other-declarations)"},
	"use-package":            {seqCtor: false, pcdata: false, kids: map[string]bool{"accept": true, "override": true}, model: "(xsl:accept | xsl:override)*"},
	"expose":                 {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"accept":                 {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"override":               {seqCtor: false, decls: true, pcdata: false, kids: nil, model: "declarations"},
	"original":               {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"accumulator":            {seqCtor: false, pcdata: false, kids: map[string]bool{"accumulator-rule": true}, model: "xsl:accumulator-rule+"},
	"accumulator-rule":       {seqCtor: true, pcdata: false, kids: nil, model: "sequence-constructor"},
	"context-item":           {seqCtor: false, pcdata: false, kids: nil, model: ""},
	"template":               {seqCtor: true, pcdata: false, kids: map[string]bool{"param": true, "context-item": true}, model: "(xsl:context-item?, xsl:param*, sequence-constructor)"},
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
	"evaluate":               true,
	"iterate":                true,
	"map":                    true,
	"map-entry":              true,
	"on-empty":               true,
	"on-non-empty":           true,
	"where-populated":        true,
	"fork":                   true,
	"merge":                  true,
	"next-iteration":         true,
	"break":                  true,
	"fallback":               true,
	"try":                    true,
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
	"source-document":        true,
	"text":                   true,
	"value-of":               true,
	"variable":               true,
}

// xsltDeclarations is the "declaration" category: the elements permitted as
// children of xsl:stylesheet, which the syntax summary abbreviates to
// "other-declarations" rather than naming.
var xsltDeclarations = map[string]bool{
	"accumulator":     true,
	"attribute-set":   true,
	"character-map":   true,
	"decimal-format":  true,
	"function":        true,
	"import":          true,
	"import-schema":   true,
	"include":         true,
	"key":             true,
	"mode":            true,
	"namespace-alias": true,
	"output":          true,
	"param":           true,
	"preserve-space":  true,
	"strip-space":     true,
	"template":        true,
	"variable":        true,
}

// qnameAttrDef records that an attribute's value is a QName, or a
// whitespace-separated list of them, as the element syntax summaries say.
type qnameAttrDef struct {
	// list marks the "qnames" type, whose value is a whitespace-separated
	// list rather than a single name.
	list bool
	// avt marks a summary that writes the type inside curly brackets, which
	// is how it says the attribute is an attribute value template. Where the
	// summary does *not* write the brackets, a value containing "{" is not a
	// template but simply not a QName, and so is a static error.
	avt bool
	// code overrides the error raised for a value outside the lexical space.
	//
	// XTSE0020 is the general rule, but where the spec devotes a numbered
	// error to one attribute's name being unusable — XTDE0820 for
	// xsl:element/@name, XTDE1460 for xsl:result-document/@format — that code
	// is the one the specific clause assigns, and it takes precedence over
	// the general one. Both are dynamic errors, because the value is in
	// general an attribute value template; a processor "may optionally signal
	// this as a static error" when the value is a literal, which is what
	// reporting it here amounts to.
	code string
}

// qnameAttrs is the set of attributes whose summary gives their type as
// "qname" or "qnames", extracted from the same element syntax summaries as
// the tables above.
//
// It answers half of XTSE0020 that the enumeration cannot: an attribute with
// no closed set of values still has a lexical space, and a value outside it
// is "not one of the permitted values for that attribute". The distinction
// the table carries is whether the summary brackets the type, because only a
// bracketed one may hold a curly-bracket template.
var qnameAttrs = map[string]map[string]qnameAttrDef{
	"attribute":       {"name": {avt: true}, "type": {}},
	"attribute-set":   {"name": {}, "use-attribute-sets": {list: true, code: "XTSE0710"}},
	"call-template":   {"name": {}},
	"character-map":   {"name": {}, "use-character-maps": {list: true}},
	"copy":            {"use-attribute-sets": {list: true, code: "XTSE0710"}, "type": {}},
	"copy-of":         {"type": {}},
	"decimal-format":  {"name": {}},
	"document":        {"type": {}},
	"element":         {"name": {avt: true, code: "XTDE0820"}, "use-attribute-sets": {list: true, code: "XTSE0710"}, "type": {}},
	"function":        {"name": {}},
	"key":             {"name": {}},
	"output":          {"name": {}, "cdata-section-elements": {list: true}, "use-character-maps": {list: true}},
	"param":           {"name": {}},
	"result-document": {"format": {avt: true, code: "XTDE1460"}, "type": {}, "cdata-section-elements": {list: true, avt: true}, "use-character-maps": {list: true}},
	"source-document": {"type": {}},
	"template":        {"name": {}},
	"variable":        {"name": {}},
	"with-param":      {"name": {}},
}
