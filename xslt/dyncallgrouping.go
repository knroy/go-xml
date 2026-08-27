package xslt

import "github.com/knroy/go-xml/xpath"

// The XSLT part of the dynamic context does not survive a dynamic function
// call.
//
// Section 5.3.4: a named function reference retains the XPath static and
// dynamic context at the point of the reference, "but this rule does not
// extend to the XSLT extensions to the dynamic context defined in this
// section. If a dynamic function call is made that depends on the XSLT part
// of the dynamic context (for example, regex-group#1(2)), then the relevant
// components of the context are cleared."
//
// So a reference captured inside one xsl:analyze-string and invoked inside
// another must not see either one's captured substrings: regex-090 and -091
// nest two analyze-strings and require the zero-length string, not the outer
// match. The same holds for grouping: for-each-group-090 binds
// current-group#0 in a global and calls it inside a grouping, and XTDE1061
// is the answer because the function has no grouping of its own.
//
// The current template rule and the current output URI are cleared by the
// same mechanism already; see staticerrors30.go and outputfuncs.go.
func init() {
	// groupingScopeVar is the marker that tells "no grouping" apart from "an
	// empty group", so clearing it is what turns current-group#0() into the
	// XTDE1061 that for-each-group-090 requires rather than an empty answer.
	xpath.ClearedOnDynamicCall = append(xpath.ClearedOnDynamicCall,
		regexGroupsVar, currentGroupVar, currentGroupingKeyVar,
		groupingKeyVar, groupingScopeVar)
}
