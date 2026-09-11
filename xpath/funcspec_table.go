package xpath

import (
	"strings"
	"sync"

	"github.com/knroy/go-xml/xdm"
)

// This file holds the FunctionSpec table and the families that have been
// migrated onto it.
//
// The plan requires this to land "in reviewable family-sized commits, not as
// an untestable 322-entry hand edit". What is here is therefore the mechanism
// plus one migrated family — the seventeen entries builtinSignatures already
// carried — which is what proves the path from a declared type to an
// XPTY0004 at call binding actually runs. The normalized data source for the
// remaining families is xpath/spec/function-signatures.json, extracted from
// the vendored Recommendation by cmd/genfunctions; see funcspec_manifest.go.
//
// To migrate a family, a family agent adds its keys to specSignatures below,
// using the spellings the manifest already holds for them, and runs the QT3
// lanes. Nothing else changes: registration, lookup and the callbacks are
// untouched, because a spec constrains an existing registration rather than
// replacing it.
//
// A key is "local/arity" for an fn: function and "prefix:local/arity" for one
// of the other three namespaces the manifest covers. Both forms are read by
// splitSpecEntryKey, which the enforcement test reads them through as well.

// specSignatures is the migrated portion of the manifest, keyed by
// "local/arity" for fn: and by "prefix:local/arity" for the math:, map: and
// array: namespaces, as the return type followed by the parameter types.
//
// It is seeded from builtinSignatures — the same seventeen entries, in the
// same spelling and the same order — because those were already written
// against the specification's function summary and already read by function
// subtyping. Reusing them means the first family to go through the new path
// is one whose declared types were reviewed before, so a behaviour change
// here would be a defect in the mechanism rather than in the data.
//
// Every entry below is verified against xpath/spec/function-signatures.json
// by TestMigratedSignaturesMatchManifest, so a hand-edited spelling that
// disagrees with the Recommendation fails the build rather than silently
// constraining a function wrongly.
// The fn:substring and fn:subsequence rows are added on top of those
// seventeen, and they are what make the EMPTY arm of the check live. The
// seventeen all declare their one parameter "?" or "*", so they exercise only
// the too-many-items arm; migrating a family whose proforma has a
// non-nullable parameter is what puts the twelve-function defect class — an
// empty sequence passed where F&O declares no "?" — behind the mechanism
// instead of behind twelve hand-written guards. These two are precisely the
// functions commit 7668773 fixed by hand, so the structural check and the
// hand fix now answer the same question, and the hand fix's tests are what
// prove they answer it the same way.
var specSignatures = func() map[string][]string {
	m := make(map[string][]string, len(builtinSignatures)+71)
	for k, v := range builtinSignatures {
		m[k] = v
	}
	m["substring/2"] = []string{"xs:string", "xs:string?", "xs:double"}
	m["substring/3"] = []string{"xs:string", "xs:string?", "xs:double", "xs:double"}
	m["subsequence/2"] = []string{"item()*", "item()*", "xs:double"}
	m["subsequence/3"] = []string{"item()*", "item()*", "xs:double", "xs:double"}

	// The numeric family, F&O 3.1 4.4 and 4.5, taken from the manifest.
	//
	// Most of its parameters are declared "?" or "*", so they constrain
	// nothing new; the rows that do are fn:round/2 and
	// fn:round-half-to-even/2, whose $precision is xs:integer with no "?",
	// and fn:min/2 and fn:max/2, whose $collation is xs:string with no "?".
	// Those four are the empty-sequence arm this family adds, and they are
	// the same defect class commit 7668773 fixed by hand for fn:round/1.
	m["abs/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["ceiling/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["floor/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["round/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["round/2"] = []string{"xs:numeric?", "xs:numeric?", "xs:integer"}
	m["round-half-to-even/1"] = []string{"xs:numeric?", "xs:numeric?"}
	m["round-half-to-even/2"] = []string{"xs:numeric?", "xs:numeric?", "xs:integer"}
	m["avg/1"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*"}
	m["min/1"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*"}
	m["min/2"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*", "xs:string"}
	m["max/1"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*"}
	m["max/2"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*", "xs:string"}
	m["sum/1"] = []string{"xs:anyAtomicType", "xs:anyAtomicType*"}
	m["sum/2"] = []string{"xs:anyAtomicType?", "xs:anyAtomicType*", "xs:anyAtomicType?"}

	// The string family, F&O 3.1 5.2 to 5.6, excluding the regex functions
	// of 5.6.1 onwards: those take a $flags and a $pattern whose proformas
	// interact with the regex engine's own diagnostics, so they belong to a
	// later commit that can compare FORX error codes.
	//
	// The rows that constrain anything new are the collation arguments --
	// $collation is xs:string with no "?" throughout -- and fn:translate/3,
	// whose $map and $trans are xs:string where $arg is xs:string?.
	m["upper-case/1"] = []string{"xs:string", "xs:string?"}
	m["lower-case/1"] = []string{"xs:string", "xs:string?"}
	m["translate/3"] = []string{"xs:string", "xs:string?", "xs:string", "xs:string"}
	m["string-join/1"] = []string{"xs:string", "xs:anyAtomicType*"}
	m["string-join/2"] = []string{"xs:string", "xs:anyAtomicType*", "xs:string"}
	m["substring-before/2"] = []string{"xs:string", "xs:string?", "xs:string?"}
	m["substring-before/3"] = []string{"xs:string", "xs:string?", "xs:string?", "xs:string"}
	m["substring-after/2"] = []string{"xs:string", "xs:string?", "xs:string?"}
	m["substring-after/3"] = []string{"xs:string", "xs:string?", "xs:string?", "xs:string"}
	m["contains/2"] = []string{"xs:boolean", "xs:string?", "xs:string?"}
	m["contains/3"] = []string{"xs:boolean", "xs:string?", "xs:string?", "xs:string"}
	m["starts-with/2"] = []string{"xs:boolean", "xs:string?", "xs:string?"}
	m["starts-with/3"] = []string{"xs:boolean", "xs:string?", "xs:string?", "xs:string"}
	m["ends-with/2"] = []string{"xs:boolean", "xs:string?", "xs:string?"}
	m["ends-with/3"] = []string{"xs:boolean", "xs:string?", "xs:string?", "xs:string"}
	m["compare/2"] = []string{"xs:integer?", "xs:string?", "xs:string?"}
	m["compare/3"] = []string{"xs:integer?", "xs:string?", "xs:string?", "xs:string"}
	m["codepoint-equal/2"] = []string{"xs:boolean?", "xs:string?", "xs:string?"}
	m["codepoints-to-string/1"] = []string{"xs:string", "xs:integer*"}
	m["string-to-codepoints/1"] = []string{"xs:integer*", "xs:string?"}
	m["normalize-unicode/1"] = []string{"xs:string", "xs:string?"}
	m["normalize-unicode/2"] = []string{"xs:string", "xs:string?", "xs:string"}
	m["encode-for-uri/1"] = []string{"xs:string", "xs:string?"}
	m["iri-to-uri/1"] = []string{"xs:string", "xs:string?"}
	m["escape-html-uri/1"] = []string{"xs:string", "xs:string?"}

	// The temporal family, F&O 3.1 8.2 to 8.4: the component extraction
	// functions, the timezone adjustments and fn:dateTime.
	//
	// Every parameter here is declared "?" except the $timezone of the three
	// adjustments, which is xs:dayTimeDuration? as well -- the absent
	// timezone is meaningful for those. So this family adds no
	// empty-sequence refusal at all; what it adds is the too-many-items arm
	// for thirty-six functions that previously took a sequence of any length
	// where the spec declares at most one item.
	m["year-from-dateTime/1"] = []string{"xs:integer?", "xs:dateTime?"}
	m["month-from-dateTime/1"] = []string{"xs:integer?", "xs:dateTime?"}
	m["day-from-dateTime/1"] = []string{"xs:integer?", "xs:dateTime?"}
	m["hours-from-dateTime/1"] = []string{"xs:integer?", "xs:dateTime?"}
	m["minutes-from-dateTime/1"] = []string{"xs:integer?", "xs:dateTime?"}
	m["seconds-from-dateTime/1"] = []string{"xs:decimal?", "xs:dateTime?"}
	m["timezone-from-dateTime/1"] = []string{"xs:dayTimeDuration?", "xs:dateTime?"}
	m["year-from-date/1"] = []string{"xs:integer?", "xs:date?"}
	m["month-from-date/1"] = []string{"xs:integer?", "xs:date?"}
	m["day-from-date/1"] = []string{"xs:integer?", "xs:date?"}
	m["timezone-from-date/1"] = []string{"xs:dayTimeDuration?", "xs:date?"}
	m["hours-from-time/1"] = []string{"xs:integer?", "xs:time?"}
	m["minutes-from-time/1"] = []string{"xs:integer?", "xs:time?"}
	m["seconds-from-time/1"] = []string{"xs:decimal?", "xs:time?"}
	m["timezone-from-time/1"] = []string{"xs:dayTimeDuration?", "xs:time?"}
	m["years-from-duration/1"] = []string{"xs:integer?", "xs:duration?"}
	m["months-from-duration/1"] = []string{"xs:integer?", "xs:duration?"}
	m["days-from-duration/1"] = []string{"xs:integer?", "xs:duration?"}
	m["hours-from-duration/1"] = []string{"xs:integer?", "xs:duration?"}
	m["minutes-from-duration/1"] = []string{"xs:integer?", "xs:duration?"}
	m["seconds-from-duration/1"] = []string{"xs:decimal?", "xs:duration?"}
	m["adjust-dateTime-to-timezone/1"] = []string{"xs:dateTime?", "xs:dateTime?"}
	m["adjust-dateTime-to-timezone/2"] = []string{"xs:dateTime?", "xs:dateTime?", "xs:dayTimeDuration?"}
	m["adjust-date-to-timezone/1"] = []string{"xs:date?", "xs:date?"}
	m["adjust-date-to-timezone/2"] = []string{"xs:date?", "xs:date?", "xs:dayTimeDuration?"}
	m["adjust-time-to-timezone/1"] = []string{"xs:time?", "xs:time?"}
	m["adjust-time-to-timezone/2"] = []string{"xs:time?", "xs:time?", "xs:dayTimeDuration?"}
	m["dateTime/2"] = []string{"xs:dateTime?", "xs:date?", "xs:time?"}

	// The node and accessor family, F&O 3.1 5.1 and 14: the accessors that
	// take a node, the zero-argument forms that read the context item, and
	// the id/idref lookups.
	//
	// The rows that constrain anything new are fn:in-scope-prefixes/1,
	// whose $element is element() with no "?"; fn:id/2, fn:idref/2,
	// fn:element-with-id/2 and fn:lang/2, whose $node is node() with no
	// "?"; and fn:namespace-uri-for-prefix/2 and fn:resolve-QName/2, whose
	// $element is likewise element(). Those seven are this family's
	// empty-sequence arm. The zero-arity forms declare no parameters at
	// all, so they add a call-binding row that can only ever succeed --
	// they are here so the family is the whole of F&O 5.1 and 14 rather
	// than the part of it that happens to refuse something.
	m["name/0"] = []string{"xs:string"}
	m["local-name/0"] = []string{"xs:string"}
	m["namespace-uri/0"] = []string{"xs:anyURI"}
	m["string/0"] = []string{"xs:string"}
	m["number/0"] = []string{"xs:double"}
	m["string-length/0"] = []string{"xs:integer"}
	m["normalize-space/0"] = []string{"xs:string"}
	m["data/0"] = []string{"xs:anyAtomicType*"}
	m["root/0"] = []string{"node()"}
	m["node-name/0"] = []string{"xs:QName?"}
	m["node-name/1"] = []string{"xs:QName?", "node()?"}
	m["nilled/0"] = []string{"xs:boolean?"}
	m["nilled/1"] = []string{"xs:boolean?", "node()?"}
	m["base-uri/0"] = []string{"xs:anyURI?"}
	m["base-uri/1"] = []string{"xs:anyURI?", "node()?"}
	m["document-uri/0"] = []string{"xs:anyURI?"}
	m["document-uri/1"] = []string{"xs:anyURI?", "node()?"}
	m["path/0"] = []string{"xs:string?"}
	m["path/1"] = []string{"xs:string?", "node()?"}
	m["has-children/0"] = []string{"xs:boolean"}
	m["has-children/1"] = []string{"xs:boolean", "node()?"}
	m["generate-id/0"] = []string{"xs:string"}
	m["generate-id/1"] = []string{"xs:string", "node()?"}
	m["innermost/1"] = []string{"node()*", "node()*"}
	m["outermost/1"] = []string{"node()*", "node()*"}
	m["in-scope-prefixes/1"] = []string{"xs:string*", "element()"}
	m["id/1"] = []string{"element()*", "xs:string*"}
	m["id/2"] = []string{"element()*", "xs:string*", "node()"}
	m["idref/1"] = []string{"node()*", "xs:string*"}
	m["idref/2"] = []string{"node()*", "xs:string*", "node()"}
	m["element-with-id/1"] = []string{"element()*", "xs:string*"}
	m["element-with-id/2"] = []string{"element()*", "xs:string*", "node()"}
	m["lang/1"] = []string{"xs:boolean", "xs:string?"}
	m["lang/2"] = []string{"xs:boolean", "xs:string?", "node()"}

	// The sequence family, F&O 3.1 14.1 to 14.4: the functions over
	// sequences of items and of atomic values.
	//
	// The rows that constrain anything new are the $collation arguments of
	// fn:distinct-values/2, fn:index-of/3 and fn:deep-equal/3, which are
	// xs:string with no "?"; the $position of fn:insert-before/3 and
	// fn:remove/2 and the $target of fn:index-of/2, which are xs:integer
	// and xs:anyAtomicType respectively and likewise non-nullable; and
	// fn:trace/2's $label. fn:exactly-one, fn:one-or-more and
	// fn:zero-or-one take item()*, so they add only the too-many-items arm
	// -- their own cardinality refusal is FORG0003..0005 raised in the
	// callback, which this leaves untouched.
	m["distinct-values/1"] = []string{"xs:anyAtomicType*", "xs:anyAtomicType*"}
	m["distinct-values/2"] = []string{"xs:anyAtomicType*", "xs:anyAtomicType*", "xs:string"}
	m["index-of/2"] = []string{"xs:integer*", "xs:anyAtomicType*", "xs:anyAtomicType"}
	m["index-of/3"] = []string{"xs:integer*", "xs:anyAtomicType*", "xs:anyAtomicType", "xs:string"}
	m["insert-before/3"] = []string{"item()*", "item()*", "xs:integer", "item()*"}
	m["remove/2"] = []string{"item()*", "item()*", "xs:integer"}
	m["unordered/1"] = []string{"item()*", "item()*"}
	m["exactly-one/1"] = []string{"item()", "item()*"}
	m["one-or-more/1"] = []string{"item()+", "item()*"}
	m["zero-or-one/1"] = []string{"item()?", "item()*"}
	m["deep-equal/2"] = []string{"xs:boolean", "item()*", "item()*"}
	m["deep-equal/3"] = []string{"xs:boolean", "item()*", "item()*", "xs:string"}
	m["trace/1"] = []string{"item()*", "item()*"}
	m["trace/2"] = []string{"item()*", "item()*", "xs:string"}

	// The higher-order family, F&O 3.1 16.1 and 16.2, plus the function
	// reflection of 2.9.
	//
	// This is the first family whose parameters are function tests rather
	// than atomic types, so it is the first to exercise a declared
	// function(...) spelling at call binding. Every $f here is
	// non-nullable, as is fn:function-lookup's $name and $arity and
	// fn:apply's $array, so the empty-sequence arm is live throughout.
	// fn:sort's $collation is xs:string? -- the one nullable argument in
	// the family, and deliberately so: an absent collation means the
	// default, which fn:sort/3's $key cannot say.
	m["for-each/2"] = []string{"item()*", "item()*", "function(item()) as item()*"}
	m["filter/2"] = []string{"item()*", "item()*", "function(item()) as xs:boolean"}
	m["fold-left/3"] = []string{"item()*", "item()*", "item()*", "function(item()*, item()) as item()*"}
	m["fold-right/3"] = []string{"item()*", "item()*", "item()*", "function(item(), item()*) as item()*"}
	m["for-each-pair/3"] = []string{"item()*", "item()*", "item()*", "function(item(), item()) as item()*"}
	m["sort/1"] = []string{"item()*", "item()*"}
	m["sort/2"] = []string{"item()*", "item()*", "xs:string?"}
	m["sort/3"] = []string{"item()*", "item()*", "xs:string?", "function(item()) as xs:anyAtomicType*"}
	m["apply/2"] = []string{"item()*", "function(*)", "array(*)"}
	m["function-arity/1"] = []string{"xs:integer", "function(*)"}
	m["function-name/1"] = []string{"xs:QName?", "function(*)"}
	m["function-lookup/2"] = []string{"function(*)?", "xs:QName", "xs:integer"}

	// The QName and URI family, F&O 3.1 5.4, 10.1 and 13, plus the
	// collation and language accessors of 13.
	//
	// The rows that constrain anything new are fn:QName/2's $paramURI,
	// fn:resolve-QName/2's and fn:namespace-uri-for-prefix/2's $element,
	// fn:resolve-uri/2's $base, fn:collation-key's $key and $collation,
	// and fn:environment-variable/1's $name -- all declared without "?".
	// The three *-from-QName accessors and fn:resolve-uri/1 take a
	// nullable argument, so they add only the too-many-items arm.
	m["QName/2"] = []string{"xs:QName", "xs:string?", "xs:string"}
	m["local-name-from-QName/1"] = []string{"xs:NCName?", "xs:QName?"}
	m["prefix-from-QName/1"] = []string{"xs:NCName?", "xs:QName?"}
	m["namespace-uri-from-QName/1"] = []string{"xs:anyURI?", "xs:QName?"}
	m["resolve-QName/2"] = []string{"xs:QName?", "xs:string?", "element()"}
	m["namespace-uri-for-prefix/2"] = []string{"xs:anyURI?", "xs:string?", "element()"}
	m["resolve-uri/1"] = []string{"xs:anyURI?", "xs:string?"}
	m["resolve-uri/2"] = []string{"xs:anyURI?", "xs:string?", "xs:string"}
	m["static-base-uri/0"] = []string{"xs:anyURI?"}
	m["default-collation/0"] = []string{"xs:string"}
	m["default-language/0"] = []string{"xs:language"}
	m["collation-key/1"] = []string{"xs:base64Binary", "xs:string"}
	m["collation-key/2"] = []string{"xs:base64Binary", "xs:string", "xs:string"}
	m["environment-variable/1"] = []string{"xs:string?", "xs:string"}
	m["available-environment-variables/0"] = []string{"xs:string*"}

	// The input and document family, F&O 3.1 13 and 14.5: the functions
	// that read an external resource and the parse/serialize pair.
	//
	// The rows that constrain anything new are the $encoding of
	// fn:unparsed-text and its two siblings, the $options of
	// fn:load-xquery-module/2, and fn:load-xquery-module/1's and
	// fn:transform/1's own argument -- all declared without "?". The $href
	// and $uri arguments throughout are xs:string?, since an absent URI
	// selects the default resource rather than being an error, so those
	// add only the too-many-items arm. fn:serialize/2's $params is
	// item()?, which is the one row here whose nullability is the point.
	m["doc/1"] = []string{"document-node()?", "xs:string?"}
	m["doc-available/1"] = []string{"xs:boolean", "xs:string?"}
	m["collection/0"] = []string{"item()*"}
	m["collection/1"] = []string{"item()*", "xs:string?"}
	m["uri-collection/0"] = []string{"xs:anyURI*"}
	m["uri-collection/1"] = []string{"xs:anyURI*", "xs:string?"}
	m["unparsed-text/1"] = []string{"xs:string?", "xs:string?"}
	m["unparsed-text/2"] = []string{"xs:string?", "xs:string?", "xs:string"}
	m["unparsed-text-lines/1"] = []string{"xs:string*", "xs:string?"}
	m["unparsed-text-lines/2"] = []string{"xs:string*", "xs:string?", "xs:string"}
	m["unparsed-text-available/1"] = []string{"xs:boolean", "xs:string?"}
	m["unparsed-text-available/2"] = []string{"xs:boolean", "xs:string?", "xs:string"}
	m["parse-xml/1"] = []string{"document-node(element(*))?", "xs:string?"}
	m["parse-xml-fragment/1"] = []string{"document-node()?", "xs:string?"}
	m["parse-ietf-date/1"] = []string{"xs:dateTime?", "xs:string?"}
	m["serialize/1"] = []string{"xs:string", "item()*"}
	m["serialize/2"] = []string{"xs:string", "item()*", "item()?"}
	m["load-xquery-module/1"] = []string{"map(*)", "xs:string"}
	m["load-xquery-module/2"] = []string{"map(*)", "xs:string", "map(*)"}
	m["transform/1"] = []string{"map(*)", "map(*)"}

	// The JSON family, F&O 3.1 17.4 to 17.6, plus fn:random-number-generator
	// of 14.5, which returns a map and belongs to the same map-valued group.
	//
	// Every $options here is map(*) with no "?", so this family's
	// empty-sequence arm is live on all four two-argument forms: an empty
	// sequence passed where F&O declares a required map is the defect class
	// the mechanism exists to catch. The $json-text and $input arguments are
	// nullable -- an empty sequence there yields an empty result rather than
	// an error -- so they add only the too-many-items arm.
	m["parse-json/1"] = []string{"item()?", "xs:string?"}
	m["parse-json/2"] = []string{"item()?", "xs:string?", "map(*)"}
	m["json-doc/1"] = []string{"item()?", "xs:string?"}
	m["json-doc/2"] = []string{"item()?", "xs:string?", "map(*)"}
	m["json-to-xml/1"] = []string{"document-node()?", "xs:string?"}
	m["json-to-xml/2"] = []string{"document-node()?", "xs:string?", "map(*)"}
	m["xml-to-json/1"] = []string{"xs:string?", "node()?"}
	m["xml-to-json/2"] = []string{"xs:string?", "node()?", "map(*)"}
	m["random-number-generator/0"] = []string{"map(xs:string, item())"}
	m["random-number-generator/1"] = []string{"map(xs:string, item())", "xs:anyAtomicType?"}

	// The context, boolean and error family, F&O 3.1 7.1, 8.1, 14.6 and
	// 5.3.3 -- the rows that remain once the regex and format-* groups are
	// set aside.
	//
	// Most of these declare no parameters at all: the context accessors and
	// the two boolean constants take nothing, so their call-binding row can
	// only ever succeed. They are here so the family is complete rather
	// than only the part that refuses something. The rows that do constrain
	// are fn:error/2's $description and /3's $error-object, and
	// fn:contains-token's $token and $collation, all declared without "?".
	// fn:error's $code is xs:QName? -- nullable, because fn:error() with no
	// code is legal and raises FOER0000.
	m["true/0"] = []string{"xs:boolean"}
	m["false/0"] = []string{"xs:boolean"}
	m["position/0"] = []string{"xs:integer"}
	m["last/0"] = []string{"xs:integer"}
	m["current-date/0"] = []string{"xs:date"}
	m["current-time/0"] = []string{"xs:time"}
	m["current-dateTime/0"] = []string{"xs:dateTimeStamp"}
	m["implicit-timezone/0"] = []string{"xs:dayTimeDuration"}
	m["error/0"] = []string{"none"}
	m["error/1"] = []string{"none", "xs:QName?"}
	m["error/2"] = []string{"none", "xs:QName?", "xs:string"}
	m["error/3"] = []string{"none", "xs:QName?", "xs:string", "item()*"}
	m["contains-token/2"] = []string{"xs:boolean", "xs:string*", "xs:string"}
	m["contains-token/3"] = []string{"xs:boolean", "xs:string*", "xs:string", "xs:string"}
	// math:pi is the first entry to use a prefixed key, and it is what
	// proves the prefixed path is live rather than dead code: under the
	// fn:-only expansion this key constrained a non-existent fn:pi. It is
	// nullary, so it constrains no argument and can change no behaviour,
	// which is what makes it the safe entry to land with the mechanism
	// rather than with the math: family.
	m["math:pi/0"] = []string{"xs:double"}

	// The math: family, F&O 3.1 4.8. Every one of these is already guarded
	// by hand inside fn_math.go, so the declared types re-derive refusals
	// that the callbacks already produce rather than adding new ones: the
	// value here is that the refusal now comes from the manifest, where a
	// mistyped occurrence indicator fails TestMigratedSignaturesMatchManifest
	// instead of silently constraining a function wrongly.
	//
	// The eleven unary functions and math:sqrt declare xs:double?, so they
	// constrain nothing. math:atan2 declares both parameters xs:double and
	// math:pow declares $y as xs:numeric, all three without "?", which is
	// the empty-sequence arm -- and fn_math.go already raises XPTY0004 for
	// exactly those three.
	m["math:acos/1"] = []string{"xs:double?", "xs:double?"}
	m["math:asin/1"] = []string{"xs:double?", "xs:double?"}
	m["math:atan/1"] = []string{"xs:double?", "xs:double?"}
	m["math:atan2/2"] = []string{"xs:double", "xs:double", "xs:double"}
	m["math:cos/1"] = []string{"xs:double?", "xs:double?"}
	m["math:exp/1"] = []string{"xs:double?", "xs:double?"}
	m["math:exp10/1"] = []string{"xs:double?", "xs:double?"}
	m["math:log/1"] = []string{"xs:double?", "xs:double?"}
	m["math:log10/1"] = []string{"xs:double?", "xs:double?"}
	m["math:pow/2"] = []string{"xs:double?", "xs:double?", "xs:numeric"}
	m["math:sin/1"] = []string{"xs:double?", "xs:double?"}
	m["math:sqrt/1"] = []string{"xs:double?", "xs:double?"}
	m["math:tan/1"] = []string{"xs:double?", "xs:double?"}
	return m
}()

// functionSpecs is the parsed table, built once and shared, keyed by
// specKey(name, arity).
//
// It is separate from Library.fns rather than a field on Function because a
// Function value is copied on every Lookup: hanging parsed types off it would
// copy a slice header per call for data that never varies. The table is
// immutable after construction, so a lookup needs no lock.
var (
	functionSpecsOnce sync.Once
	functionSpecs     map[string][]SequenceType
)

// lookupSpecParams returns the declared parameter types of a function, if the
// manifest covers it.
//
// A miss is the ordinary case while migration is in progress and means "bind
// this call exactly as before". That is what makes filling a family a pure
// narrowing rather than a behaviour change with a blast radius.
func lookupSpecParams(name xdm.QName, arity int) ([]SequenceType, bool) {
	functionSpecsOnce.Do(buildFunctionSpecs)
	p, ok := functionSpecs[specKey(name, arity)]
	return p, ok
}

// buildFunctionSpecs parses the migrated spellings once.
//
// A spelling this package cannot parse is skipped rather than panicking: an
// unparseable declared type must constrain nothing, on the same reasoning
// that makes an unknown spelling subsume only itself in subtype.go. The
// enforcement test reports such an entry, so it cannot hide.
func buildFunctionSpecs() {
	functionSpecs = map[string][]SequenceType{}
	for key, sig := range specSignatures {
		name, arity, ok := splitSpecEntryKey(key)
		if !ok || len(sig) < 1 {
			continue
		}
		params, ok := parseSpellings(sig[1:]) // sig[0] is the return type
		if !ok || len(params) != arity {
			continue
		}
		functionSpecs[specKey(name, arity)] = params
	}
}

// splitSpecEntryKey reads a specSignatures key into the name it constrains.
//
// The key is "local/arity", and an unprefixed local name means fn:, which is
// why the 207 entries migrated before this existed did not have to change. A
// key may also carry one of the manifest's own prefixes -- "math:pow/2",
// "map:get/2", "array:size/1" -- which is what makes the map:, array: and
// math: rows reachable at all. Before this, every key was expanded into the
// fn: namespace unconditionally, so a "get/2" meant for map:get constrained a
// non-existent fn:get, left map:get untouched, and was rejected by
// TestMigratedSignaturesMatchManifest as a name the manifest does not
// describe.
//
// TestMigratedSignaturesMatchManifest reads keys through this same function,
// so the table and the test cannot disagree about what a key means. That
// shared reading is the point: the fn:-only assumption was duplicated in both
// places, and a fix to one of them alone would have left the other wrong.
func splitSpecEntryKey(key string) (xdm.QName, int, bool) {
	slash := strings.IndexByte(key, '/')
	if slash < 0 || slash+1 >= len(key) {
		return xdm.QName{}, 0, false
	}
	spelling, digits := key[:slash], key[slash+1:]
	arity := 0
	for _, c := range digits {
		if c < '0' || c > '9' {
			return xdm.QName{}, 0, false
		}
		arity = arity*10 + int(c-'0')
	}
	if strings.IndexByte(spelling, ':') < 0 {
		return xdm.QName{URI: xdm.NSFN, Local: spelling}, arity, true
	}
	name, ok := parseSpecName(spelling)
	if !ok {
		return xdm.QName{}, 0, false
	}
	return name, arity, true
}

// parseSpellings parses a run of sequence-type spellings, answering false if
// any of them is one this package cannot read.
func parseSpellings(spellings []string) ([]SequenceType, bool) {
	out := make([]SequenceType, 0, len(spellings))
	for _, s := range spellings {
		st, err := ParseSequenceType(s, nil)
		if err != nil {
			return nil, false
		}
		out = append(out, st)
	}
	return out, true
}
