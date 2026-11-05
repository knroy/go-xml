package xslt

// Lane MM8: values the alias flow knows hold no node (evaluate-046).
//
// The flow joins a built-in call's arguments into its result unless it knows
// better (aliasFlow.call). A function whose result is atomic, whatever its
// arguments, holds no node of a stream nor of a load: resolve-QName(@name, .)
// in an accumulator rule made the accumulator's map "hold a stream's node",
// and so every xsl:evaluate given that map an identity use (§10.4.2's target
// expression may load the streamed URI), refusing a whole stylesheet whose
// evaluated expressions are given only atomic values and loads.

import "github.com/knroy/go-xml/v2/xpath"

// aliasAtomicFns are fn functions whose result is atomic or empty.
var aliasAtomicFns = map[string]bool{
	"resolve-QName": true, "QName": true, "string": true, "data": true, "number": true, "boolean": true,
	"not": true, "true": true, "false": true, "exists": true, "empty": true, "count": true, "sum": true,
	"avg": true, "min": true, "max": true, "name": true, "local-name": true, "namespace-uri": true,
	"node-name": true, "concat": true, "string-join": true, "substring": true, "substring-before": true,
	"substring-after": true, "string-length": true, "normalize-space": true, "upper-case": true,
	"lower-case": true, "translate": true, "contains": true, "starts-with": true, "ends-with": true,
	"matches": true, "replace": true, "tokenize": true, "position": true, "last": true,
	"local-name-from-QName": true, "namespace-uri-from-QName": true, "prefix-from-QName": true,
	"in-scope-prefixes": true, "namespace-uri-for-prefix": true, "distinct-values": true,
	"deep-equal": true, "compare": true, "round": true, "floor": true, "ceiling": true, "abs": true,
	"format-number": true, "format-integer": true, "string-to-codepoints": true, "codepoints-to-string": true,
}

// aliasAtomic reports whether x, a built-in call, gives atomic values only:
// an xs constructor, or one of aliasAtomicFns.
func aliasAtomic(x *xpath.FuncCall) bool {
	return x.Name.URI == "http://www.w3.org/2001/XMLSchema" || x.Name.URI == fnNS && aliasAtomicFns[x.Name.Local]
}
