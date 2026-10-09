package xslt

// Streamable stylesheet functions: XSLT 3.0 §19.8.5.
//
// §19.8.5 gives every xsl:function a streamability category, declared in its
// streamability attribute and defaulting to unclassified. The category does
// two jobs at once, and this file implements both:
//
//   - It constrains the function body. Each category names the posture and
//     sweep the function result must have; a declared-streamable function
//     whose body does not meet them is not guaranteed-streamable.
//   - It determines the posture and sweep of a *call* on the function, which
//     is what lets the analysis carry on through a call site instead of
//     giving up there.
//
// Before this file, xslt/streamability.go's funcCall returned unknown() for
// every function outside the fn: namespace, which abandoned the whole
// enclosing construct. The single dispatch line added there hands a call on a
// declared stylesheet function to callProps below.
//
// Two supporting rules from §19.8.8 come with it, because §19.8.5 is
// unreachable without them:
//
//   - §19.8.8.12, variable references. A reference to the *first* parameter
//     of a declared-streamable function is not grounded; its posture comes
//     from the category. This is the rule that makes a function body's
//     posture depend on its category at all, and without it every body would
//     be trivially grounded and every category check would pass.
//   - §19.8.8.7, the simple map operator "!". The dominant call-site shape in
//     the suite is "path ! f:g(., 'x')", and "!" was previously unmodelled,
//     so the analysis abandoned the construct before ever reaching the call.

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// streamCategory is the streamability category of a stylesheet function
// (§19.8.5).
type streamCategory int

const (
	// catUnclassified is the default, and the only category permitted for a
	// zero-arity function. Its calls follow the general rules with
	// type-determined operand usage.
	catUnclassified streamCategory = iota
	catAbsorbing
	catInspection
	catFilter
	catShallowDescent
	catDeepDescent
	catAscent
)

func (c streamCategory) String() string {
	switch c {
	case catAbsorbing:
		return "absorbing"
	case catInspection:
		return "inspection"
	case catFilter:
		return "filter"
	case catShallowDescent:
		return "shallow-descent"
	case catDeepDescent:
		return "deep-descent"
	case catAscent:
		return "ascent"
	default:
		return "unclassified"
	}
}

// declaredStreamable reports whether the category is one other than
// unclassified (§19.8.5). Only a declared-streamable function has a streaming
// parameter, and only such a function constrains its own body.
func (c streamCategory) declaredStreamable() bool { return c != catUnclassified }

// parseStreamCategory maps the value of a streamability attribute to a
// category.
//
// §19.8.5 allows the category to be given as a QName in an
// implementation-defined namespace, and requires a processor that does not
// recognize such a category to "analyze the function as if
// streamability='unclassified' were specified". A value containing a colon is
// therefore treated as unclassified rather than rejected. Any other
// unrecognized value is some other check's error to report, and is likewise
// treated as unclassified here.
func parseStreamCategory(v string) streamCategory {
	switch v {
	case "absorbing":
		return catAbsorbing
	case "inspection":
		return catInspection
	case "filter":
		return catFilter
	case "shallow-descent":
		return catShallowDescent
	case "deep-descent":
		return catDeepDescent
	case "ascent":
		return catAscent
	default:
		return catUnclassified
	}
}

// streamFunc is one xsl:function declaration, reduced to what §19.8.5 needs.
type streamFunc struct {
	category streamCategory
	// params holds the declared "as" type of each xsl:param, in order, as
	// written. An empty string means the attribute was absent.
	params []string
	// asType is the declared return type of the function, or "" if absent.
	asType string
	// body is the xsl:function element itself.
	body *xdm.Node
}

// arity is the number of declared parameters.
func (f *streamFunc) arity() int { return len(f.params) }

// collectStreamFuncs indexes every xsl:function in the stylesheet by name and
// arity, which together identify the function a call resolves to.
func collectStreamFuncs(root *xdm.Node) map[funcKey]*streamFunc {
	out := map[funcKey]*streamFunc{}
	walkElements(root, func(el *xdm.Node) bool {
		if !isXSL(el, "function") {
			return true
		}
		name := el.AttrValue("name")
		if name == "" {
			return false
		}
		q, err := resolveQNameAttr(el, name)
		if err != nil {
			return false
		}
		f := &streamFunc{
			category: parseStreamCategory(el.AttrValue("streamability")),
			asType:   el.AttrValue("as"),
			body:     el,
		}
		for _, c := range el.ChildElements() {
			if isXSL(c, "param") {
				f.params = append(f.params, c.AttrValue("as"))
			}
		}
		// §19.8.5: "The only category permitted for a zero-arity function
		// is unclassified. All function calls to zero-arity stylesheet
		// functions are grounded and motionless." Recording the category as
		// unclassified is what makes the call grounded and motionless below.
		if len(f.params) == 0 {
			f.category = catUnclassified
		}
		out[keyOf(q, len(f.params))] = f
		// Do not descend: a nested xsl:function is not legal, and the body
		// is analysed separately.
		return false
	})
	return out
}

// funcKey identifies a stylesheet function by expanded name and arity.
//
// The name is held as namespace URI and local part only. xdm.QName carries the
// prefix as well, and the prefix on a declaration ("f:g" on xsl:function) need
// not be the prefix at a call site, so keying on the whole QName would fail to
// match a call that is in fact a call on this function.
type funcKey struct {
	uri   string
	local string
	arity int
}

// keyOf builds the lookup key for a name and arity.
func keyOf(q xdm.QName, arity int) funcKey {
	return funcKey{uri: q.URI, local: q.Local, arity: arity}
}

// typePermitsSeveralNodes reports whether a declared type permits MORE THAN
// ONE node, which is the test §19.8.5.3 through §19.8.5.7 make on the
// streaming parameter: "If the declared type of the streaming parameter
// permits more than one node, the function is not guaranteed-streamable."
//
// It is the occurrence indicator that decides it, not the item type: "*" and
// "+" permit several, "?" and a bare type permit at most one, and an absent
// "as" attribute defaults to item()* and so permits several.
//
// A type that permits no nodes at all cannot permit several of them, so
// typePermitsNodes gates the answer -- an xs:string* parameter is not a
// streaming parameter and this rule has nothing to say about it.
func typePermitsSeveralNodes(as string) bool {
	if !typePermitsNodes(as) {
		return false
	}
	if as == "" {
		// Defaults to item()*.
		return true
	}
	t := as
	for len(t) > 0 && (t[len(t)-1] == ' ' || t[len(t)-1] == '\t' ||
		t[len(t)-1] == '\n' || t[len(t)-1] == '\r') {
		t = t[:len(t)-1]
	}
	if len(t) == 0 {
		return true
	}
	switch t[len(t)-1] {
	case '*', '+':
		return true
	}
	return false
}

// typePermitsNodes reports whether a declared sequence type permits nodes --
// the "non-empty intersection with U{N}" of §19.8.8.12.
//
// An absent "as" attribute permits nodes (the default is item()*). Otherwise
// the answer is decided syntactically, which is enough for the two shapes that
// matter: a type naming a node kind or item() permits nodes, and a type naming
// an atomic type does not. This is the direction that matters for
// correctness -- answering "permits nodes" when it does not would make a
// variable reference striding where the spec makes it grounded, which is the
// stricter of the two and so can only suppress an error, never invent one.
func typePermitsNodes(as string) bool {
	if as == "" {
		return true
	}
	t := stripOccurrence(as)
	switch t {
	case "item()", "node()", "element()", "document-node()", "attribute()",
		"text()", "comment()", "processing-instruction()", "namespace-node()":
		return true
	}
	// element(foo), document-node(element(x)), attribute(a), schema-element(x)
	// and the rest all name a node kind before the parenthesis.
	for _, kind := range []string{
		"element(", "document-node(", "attribute(", "text(", "comment(",
		"processing-instruction(", "namespace-node(", "schema-element(",
		"schema-attribute(", "node(",
	} {
		if len(t) >= len(kind) && t[:len(kind)] == kind {
			return true
		}
	}
	return false
}

// typePermitsChildren reports whether a declared type has a non-empty
// intersection with U{document-node(), element()} -- the test §19.8.5.5 and
// §19.8.5.6 apply to T0 and to the static type of the first argument.
//
// As with typePermitsNodes this is decided syntactically. A type that names
// only attributes, text, comments, or processing instructions cannot deliver a
// node with children; everything else is assumed to be able to.
func typePermitsChildren(as string) bool {
	if as == "" {
		return true
	}
	t := stripOccurrence(as)
	switch t {
	case "attribute()", "text()", "comment()", "processing-instruction()",
		"namespace-node()":
		return false
	}
	for _, kind := range []string{
		"attribute(", "text(", "comment(", "processing-instruction(",
		"namespace-node(", "schema-attribute(",
	} {
		if len(t) >= len(kind) && t[:len(kind)] == kind {
			return false
		}
	}
	if !typePermitsNodes(as) {
		// An atomic type delivers no nodes at all, so it delivers no node
		// with children.
		return false
	}
	return true
}

// stripOccurrence removes a trailing occurrence indicator and surrounding
// space from a sequence type.
func stripOccurrence(as string) string {
	t := as
	for len(t) > 0 && (t[len(t)-1] == ' ' || t[len(t)-1] == '\t' ||
		t[len(t)-1] == '\n' || t[len(t)-1] == '\r') {
		t = t[:len(t)-1]
	}
	if len(t) > 0 {
		switch t[len(t)-1] {
		case '*', '+', '?':
			t = t[:len(t)-1]
		}
	}
	for len(t) > 0 && (t[len(t)-1] == ' ' || t[len(t)-1] == '\t') {
		t = t[:len(t)-1]
	}
	for len(t) > 0 && (t[0] == ' ' || t[0] == '\t' ||
		t[0] == '\n' || t[0] == '\r') {
		t = t[1:]
	}
	return t
}

// typeDeterminedUsage gives the operand usage that a declared parameter type
// implies for the corresponding argument (§19.4).
//
// A parameter declared with an atomic type atomizes any node supplied to it,
// which is absorption. A parameter that permits nodes places no constraint the
// analysis can exploit, so the usage is navigation -- the answer §19.4 gives
// when what is done with the node is not known.
func typeDeterminedUsage(as string) usage {
	if typePermitsNodes(as) {
		return usageNavigation
	}
	return usageAbsorption
}

// varPosture gives the posture of a reference to the streaming parameter of a
// declared-streamable function (§19.8.8.12).
//
// singular says the reference is not inside a higher-order operand of any
// construct between it and the function body. The table is reproduced exactly
// as the spec gives it; the two categories whose answer does not depend on
// singularity (inspection and ascent for the "no" row, filter for both) are
// spelled out rather than collapsed, so that a change to the spec table maps
// to a change here line for line.
func varPosture(c streamCategory, singular bool) posture {
	// A reference that is NOT singular -- one inside a higher-order operand,
	// so the body may be evaluated once per item -- is roaming whatever the
	// category says. §19.8.4.18's note states the consequence directly: "any
	// variable reference within the body that is bound to a streaming
	// parameter of a containing stylesheet function will not be singular,
	// which in many cases will make the entire function non-streamable."
	//
	// This is checked before the category table because it overrides it.
	// su-absorbing-906 ("for $i in 1 to 3 return name($element[$i])") and
	// su-shallow-descent-903 ("(1 to 5) ! $n") are the cases that hold it
	// down: each wants XTSE3430, and gets it only because a non-singular
	// reference cannot be streamed.
	if !singular {
		return postureRoaming
	}
	// §19.8.5.2 through §19.8.5.7, "Rules for references to the streaming
	// parameter", one line per category. Every category gives striding except
	// ascent, which gives climbing; none gives grounded.
	//
	// Reading grounded here was the defect behind the su-absorbing and
	// su-inspection failures: a function whose body returns the streaming
	// parameter returns a NODE, and a node in a streamed tree is never
	// grounded. The body then cleared the "must be grounded" requirement its
	// own category imposes, and the analysis concluded streamable where
	// §19.8.5 requires XTSE3430.
	switch c {
	case catAbsorbing, catInspection, catFilter,
		catShallowDescent, catDeepDescent:
		return postureStriding
	case catAscent:
		// §19.8.5.7 is the one category that differs.
		return postureClimbing
	}
	return postureGrounded
}

// bodyRequirement is the constraint a category places on the posture and sweep
// of the function result (§19.8.5.2 through §19.8.5.7).
//
// postures lists the permitted postures and sweeps the permitted sweeps. A
// declared-streamable function whose result falls outside either list is not
// guaranteed-streamable.
type bodyRequirement struct {
	postures []posture
	sweeps   []sweep
}

// bodyRequirements maps each declared-streamable category to the rule its
// function body must satisfy. The entries are transcribed from the "Rules for
// the function body" paragraph of each subsection.
//
// absorbing (§19.8.5.2). This comment previously quoted the Last Call draft,
// whose sentence is garbled -- "For the function to be grounded, and the sweep
// of the function result must be motionless or consuming" -- and reconstructed
// the intent from the surrounding prose. The Recommendation states it cleanly,
// so the reconstruction is no longer needed:
//
//	"For the function to be guaranteed-streamable, the type-adjusted posture
//	of the function body with respect to the declared return type must be
//	grounded, and the type-adjusted sweep of the function body with respect
//	to the declared return type must be motionless or consuming."
//
// Note what the LCWD's missing word cost: the quoted form reads as though
// GROUNDED were the conclusion, when it is one of the two premises and
// guaranteed-streamable is the conclusion. The reconstruction reached the
// right rule anyway, which the informal description confirms -- "The function
// must not return any streamed nodes".
var bodyRequirements = map[streamCategory]bodyRequirement{
	catAbsorbing: {
		postures: []posture{postureGrounded},
		sweeps:   []sweep{sweepMotionless, sweepConsuming},
	},
	catInspection: {
		postures: []posture{postureGrounded},
		sweeps:   []sweep{sweepMotionless},
	},
	catFilter: {
		postures: []posture{postureStriding, postureGrounded},
		sweeps:   []sweep{sweepMotionless},
	},
	catShallowDescent: {
		// §19.8.5.5: the body "must be striding"; unlike filter and ascent,
		// shallow-descent admits no grounded body.
		postures: []posture{postureStriding},
		sweeps:   []sweep{sweepMotionless, sweepConsuming},
	},
	catDeepDescent: {
		postures: []posture{postureCrawling, postureStriding, postureGrounded},
		sweeps:   []sweep{sweepMotionless, sweepConsuming},
	},
	catAscent: {
		postures: []posture{postureClimbing, postureGrounded},
		sweeps:   []sweep{sweepMotionless},
	},
}

// satisfiedBy reports whether the function result meets the requirement.
func (r bodyRequirement) satisfiedBy(p props) bool {
	okPosture := false
	for _, x := range r.postures {
		if p.posture == x {
			okPosture = true
		}
	}
	okSweep := false
	for _, x := range r.sweeps {
		if p.sweep == x {
			okSweep = true
		}
	}
	return okPosture && okSweep
}

// firstArgUsage gives the operand usage of the first argument of a call,
// per category (§19.8.5.2 through §19.8.5.7).
//
// Only the categories whose "Rules for function calls" fix a usage for the
// first argument appear; shallow-descent and deep-descent do not, because
// their call rules are computed directly rather than through the general
// rules.
func firstArgUsage(c streamCategory) (usage, bool) {
	switch c {
	case catAbsorbing:
		return usageAbsorption, true
	case catInspection:
		return usageInspection, true
	case catFilter:
		return usageTransmission, true
	case catAscent:
		return usageInspection, true
	}
	return usageNavigation, false
}

// checkStreamableFunctions raises XTSE3430 for a declared-streamable
// xsl:function whose body does not meet the posture and sweep its category
// requires (§19.8.5).
//
// As everywhere in this analysis, an error is reported only on a fully
// modelled verdict: a body containing one construct the analysis does not
// model yields no error at all, because the roaming it would otherwise derive
// says nothing about the stylesheet.
func checkStreamableFunctions(root *xdm.Node, funcs map[funcKey]*streamFunc) error {
	for _, f := range funcs {
		if !f.category.declaredStreamable() {
			continue
		}
		// The signature rule, which is checked before the body rule because
		// it is a property of the declaration alone and needs no analysis.
		//
		// §19.8.5.3 through §19.8.5.7 each carry the identical sentence: "If
		// the declared type of the streaming parameter permits more than one
		// node, the function is not guaranteed-streamable." §19.8.5.2
		// (absorbing) is the one exception -- "there are no constraints" --
		// which is why the category is consulted rather than the rule applied
		// to every declared-streamable function.
		//
		// This is a rule about the DECLARATION, so unlike the body rule it
		// cannot be defeated by an unmodelled construct: there is nothing to
		// model. That makes it the one place in this analysis where a verdict
		// is reported without first establishing that every construct was
		// understood.
		if f.category != catAbsorbing && f.arity() > 0 &&
			typePermitsSeveralNodes(f.params[0]) {
			return fmt.Errorf(
				"the streaming parameter of streamable stylesheet function "+
					"%s is declared %q, which permits more than one node, so "+
					"the %s category does not permit it and the function is "+
					"not guaranteed-streamable (XTSE3430)",
				f.body.AttrValue("name"), f.params[0], f.category)
		}
		req, ok := bodyRequirements[f.category]
		if !ok {
			continue
		}
		p, known := analyzeFunctionBody(f, funcs)
		if !known || req.satisfiedBy(p) {
			continue
		}
		return fmt.Errorf(
			"the body of streamable stylesheet function %s is %v and %v, "+
				"which the %s category does not permit, so the function is "+
				"not guaranteed-streamable (XTSE3430)",
			f.body.AttrValue("name"), p.posture, p.sweep, f.category)
	}
	return nil
}

// analyzeFunctionBody computes the posture and sweep of a function's result,
// and whether every construct in the body was modelled.
//
// §19.8.5 defines the function result as the type-adjusted posture and sweep
// of the sequence constructor inside xsl:function. Only the shape the suite
// uses is modelled: a body whose single instruction is an xsl:sequence or
// xsl:value-of with a select expression. A body of any other shape -- several
// instructions, a literal result element, an xsl:choose -- needs the §19.8.3
// rules for sequence constructors, which are not implemented, so it reports
// "not known" and no error is raised for it.
func analyzeFunctionBody(f *streamFunc, funcs map[funcKey]*streamFunc) (props, bool) {
	var instr *xdm.Node
	for _, c := range f.body.ChildElements() {
		if isXSL(c, "param") {
			continue
		}
		if instr != nil {
			// More than one instruction in the body.
			return roamingFreeRanging, false
		}
		instr = c
	}
	if instr == nil {
		// An empty body returns the empty sequence: grounded and motionless.
		return groundedMotionless, true
	}
	if !isXSL(instr, "sequence") && !isXSL(instr, "value-of") {
		return roamingFreeRanging, false
	}
	sel := instr.Attr("", "select")
	if sel == nil {
		return roamingFreeRanging, false
	}
	ns := newNSResolver(instr, xpathDefaultNamespace(instr))
	expr, err := xpath.ParseVersion(sel.Value(), ns, xpathVersionAt(instr))
	if err != nil {
		return roamingFreeRanging, false
	}

	// A recursive body needs no iteration to reach its fixed point, because
	// §19.8.5 breaks the circularity itself. The rules for a function *call*
	// read only the callee's declared category and the postures of the
	// argument expressions; they never consult the callee's body. That is
	// exactly what §19.8.5.2's third example relies on: analysing the
	// recursive call f:outline(*) "is possible because it is known to be a
	// call on an absorbing stylesheet function". The category is declared, so
	// the recursive call is assessed like any other and one pass is already
	// the fixed point -- and one pass is what guarantees termination, since
	// no call site re-enters analyzeFunctionBody. Mutual recursion is covered
	// by the same argument, whatever the length of the cycle.
	//
	// The analysis used to withhold a failing verdict on a recursive body,
	// because §19.10 makes XTSE3430 optional and su-ascent-A's recursive
	// bodies came out striding where §19.8.5.7 permits climbing. With the
	// ascent call rule read by intent (callAscent) those bodies are climbing,
	// and a recursive function is judged like any other: su-absorbing-908
	// needs the refusal, and removing the asymmetry measured neutral.

	// §19.6 gives the body of a stylesheet function no context item, so a
	// reference to "." inside it is an error some other check reports. The
	// context posture is set to roaming so that any construct that does
	// depend on the focus is not silently treated as streamable.
	a := &analyzer{
		ctxPosture:        postureRoaming,
		ctxAllowsChildren: true,
		known:             true,
		funcs:             funcs,
		decl:              instr,
	}
	// The streaming parameter is the first parameter, and only when the
	// function is declared-streamable and that parameter's declared type
	// permits nodes (§19.8.8.12).
	if f.category.declaredStreamable() && f.arity() > 0 && typePermitsNodes(f.params[0]) {
		if q, ok := firstParamName(f.body); ok {
			a.streamingParam = q
			a.paramCategory = f.category
			a.hasStreamParam = true
			a.paramSeveral = typePermitsSeveralNodes(f.params[0])
		}
	}
	p := a.expr(expr)
	if !a.known {
		return roamingFreeRanging, false
	}

	// xsl:value-of builds a text node from the atomized value, so its result
	// is grounded whatever the select expression's own posture; the sweep is
	// what absorbing the select costs.
	if isXSL(instr, "value-of") {
		p = combine([]operand{{
			props:          p,
			usage:          usageAbsorption,
			allowsChildren: true,
		}}, false)
	}

	// §19.8.5: "The posture and sweep of the function result are the
	// type-adjusted posture and sweep of the sequence constructor contained
	// within the xsl:function element, given the declared return type of the
	// function, which defaults to item()*."
	//
	// §19.2 defines type-adjustment as the general rules applied to a
	// construct whose single operand is this one, with the type-determined
	// usage of the required type. A declared return type of xs:string
	// therefore atomizes the body's result, which is what turns a striding
	// body into a grounded one -- and, when the body can deliver a node with
	// children, what turns motionless into consuming.
	res := typeAdjust(p, f.asType, bodyAllowsChildren(instr, expr))

	return res, true
}

// satisfiesCategory reports whether a function result meets the body rule of
// the function's declared category. A category with no entry in
// bodyRequirements imposes no rule, so nothing can fail it.
func satisfiesCategory(f *streamFunc, p props) bool {
	req, ok := bodyRequirements[f.category]
	if !ok {
		return true
	}
	return req.satisfiedBy(p)
}

// typeAdjust applies §19.2's type-adjustment to a function *result*: the
// general rules over a single operand carrying the type-determined usage of
// the declared return type.
//
// It is applied only for a return type that atomizes -- one whose
// type-determined usage is absorption. §19.2 gives every other type the usage
// navigation, and navigation from a streamed posture is free-ranging (§19.8.1),
// which applied here would make the result of *every* node-returning function
// roaming. That cannot be what §19.8.5 means: the filter, shallow-descent,
// deep-descent and ascent categories each name a non-grounded posture their
// result is permitted to have, and a striding result declared "as node()*" is
// the ordinary case those categories exist to describe -- su-filter-101a and
// su-ascent-A in the W3C suite are exactly it, and both are valid stylesheets
// the catalog expects to run.
//
// The reading this implements is that the type-determined usage is a rule
// about how a *containing construct* uses an operand it received, and a
// declared return type performs no navigation on the value it merely passes
// out. Where the return type does atomize, the adjustment is real and is
// applied: that is what turns a striding body into a grounded one.
func typeAdjust(p props, as string, allowsChildren bool) props {
	if as == "" {
		return p
	}
	if typeDeterminedUsage(as) != usageAbsorption {
		return p
	}
	return combine([]operand{{
		props:          p,
		usage:          usageAbsorption,
		allowsChildren: allowsChildren,
	}}, false)
}

// bodyAllowsChildren reports whether the body's result can include a node with
// children, which decides whether an absorbing type-adjustment costs a read of
// the stream (§19.8.1).
func bodyAllowsChildren(instr *xdm.Node, e xpath.Expr) bool {
	if isXSL(instr, "value-of") {
		// The result is a text node, which has no children.
		return false
	}
	a := &analyzer{ctxAllowsChildren: true, known: true}
	return a.allowsChildren(e)
}

// localOf returns the part of a lexical QName after the prefix.
func localOf(lex string) string {
	if i := strings.LastIndexByte(lex, ':'); i >= 0 {
		return lex[i+1:]
	}
	return lex
}

// firstParamName returns the expanded name of the first xsl:param of an
// xsl:function.
func firstParamName(fn *xdm.Node) (xdm.QName, bool) {
	for _, c := range fn.ChildElements() {
		if !isXSL(c, "param") {
			continue
		}
		q, err := resolveQNameAttr(c, c.AttrValue("name"))
		if err != nil {
			return xdm.QName{}, false
		}
		return q, true
	}
	return xdm.QName{}, false
}

// varRef applies §19.8.8.12 to a variable reference.
//
// The sweep is always motionless. The posture is grounded unless the reference
// is to the streaming parameter -- the first parameter of the
// declared-streamable function whose body is being analysed -- in which case
// the category and the reference's singularity decide it.
// A range variable of a quantified expression is the one exception, and it is
// data flow rather than a rule of §19.8.8.12: the binding sequence's posture
// was recorded when the binding was entered, and the reference is given it.
// See analyzer.vars and quantifiedExpr. A variable not in that environment is
// grounded, exactly as before.
func (a *analyzer) varRef(x *xpath.VarRef) props {
	if p, ok := a.vars[x.Name]; ok {
		// The sweep stays motionless whatever the binding's own sweep was:
		// naming a value already bound reads nothing further. What the
		// posture carries is where the nodes SIT, which is what the steps
		// that follow the reference are assessed against.
		return props{p.posture, sweepMotionless}
	}
	if !a.hasStreamParam {
		return groundedMotionless
	}
	if x.Name.URI != a.streamingParam.URI || x.Name.Local != a.streamingParam.Local {
		// Any other variable is grounded: a streamed node cannot be bound to
		// a global variable, nor to an xsl:variable in a streamable context.
		return groundedMotionless
	}
	// §19.8.5.2: "If the declared type of the streaming parameter permits
	// more than one node, then a variable reference referring to the
	// streaming parameter is striding and consuming." su-absorbing-908.
	if a.paramCategory == catAbsorbing && a.paramSeveral && !a.higherOrder {
		return props{postureStriding, sweepConsuming}
	}
	return props{varPosture(a.paramCategory, !a.higherOrder), sweepMotionless}
}

// simpleMap applies §19.8.8.7 to the simple map operator.
//
// "The posture of the expression is the posture of the right-hand operand,
// assessed with a context posture and type set to the posture and type of the
// left-hand operand. The sweep of the expression is the wider of the sweeps
// of the two operands."
//
// This was quoted as "the posture AND SWEEP of the expression are the posture
// and sweep of the right-hand operand", which states the sweep half backwards:
// the sweep is the wider of the two, so a consuming left operand is not
// discarded by a motionless right one. The code takes the wider sweep; only
// the quotation was wrong. The left operand is assessed
// first, in the current context; a left operand that is itself not streamable
// makes the whole expression so.
//
// The right operand is evaluated once per item of the left, which makes it a
// higher-order operand for the purposes of §19.8.8.12's singularity test.
func (a *analyzer) simpleMap(x *xpath.SimpleMap) props {
	left := a.expr(x.Left)
	if !left.streamable() {
		return left
	}
	inner := &analyzer{
		ctxPosture:        left.posture,
		ctxAllowsChildren: a.allowsChildren(x.Left),
		known:             a.known,
		funcs:             a.funcs,
		streamingParam:    a.streamingParam,
		paramCategory:     a.paramCategory,
		hasStreamParam:    a.hasStreamParam,
		paramSeveral:      a.paramSeveral,
		higherOrder:       true,
		// "$input ! path()" navigates away from the very node $input
		// denotes, so the context item on the right inherits the left
		// operand's streamed-but-grounded standing (§19.8.8.12, and the
		// note under §19.8.5). Without this the navigation usage would
		// meet a grounded context and cost nothing.
		ctxStreamedGrounded: a.isStreamingParamRef(x.Left),
		// §19.8.9.3 asks about the OUTERMOST containing XPath expression, so
		// the right-hand side of "!" -- still the same XPath expression --
		// keeps the answer the left-hand side had.
		currentPosture:        a.currentPosture,
		currentAllowsChildren: a.currentAllowsChildren,
		currentInScope:        a.currentInScope,
		vars:                  a.vars,
		decl:                  a.decl,
	}
	p := inner.expr(x.Right)
	a.known = a.known && inner.known
	return p
}

// callProps gives the posture and sweep of a call on a stylesheet function
// (§19.8.5), and whether the analysis models it.
//
// The analyser reaches this through funcCall in streamability.go. A call on a
// function this stylesheet does not declare -- an extension function, or one
// from a package that is not being analysed -- is not modelled, and returns
// false so that the caller abandons the enclosing construct rather than
// reporting an error.
func (a *analyzer) callProps(x *xpath.FuncCall) (props, bool) {
	if a.funcs == nil {
		return roamingFreeRanging, false
	}
	f, ok := a.funcs[keyOf(x.Name, len(x.Args))]
	if !ok {
		return roamingFreeRanging, false
	}
	// §19.8.5: all calls to zero-arity stylesheet functions are grounded and
	// motionless. collectStreamFuncs has already forced such a function to
	// the unclassified category, but the result is stated outright here
	// because the general rules would otherwise have to derive it from an
	// empty operand list.
	if f.arity() == 0 {
		return groundedMotionless, true
	}

	switch f.category {
	case catUnclassified:
		// §19.8.5.1: the general rules apply, with the operand usage of each
		// argument being the type-determined usage from the declared type of
		// the corresponding parameter.
		return a.callByGeneralRules(x, f, usageNavigation, false)

	case catAbsorbing:
		// §19.8.5.2: "If the first argument is crawling then the function
		// call is roaming and free-ranging; otherwise the general
		// streamability rules apply", with the first argument's usage being
		// absorption.
		if a.expr(x.Args[0]).posture == postureCrawling {
			return roamingFreeRanging, true
		}
		return a.callByGeneralRules(x, f, usageAbsorption, true)

	case catInspection:
		// §19.8.5.3: the general rules, first argument usage inspection.
		return a.callByGeneralRules(x, f, usageInspection, true)

	case catFilter:
		// §19.8.5.4: the general rules, first argument usage transmission.
		return a.callByGeneralRules(x, f, usageTransmission, true)

	case catAscent:
		return a.callAscent(x, f)

	case catShallowDescent, catDeepDescent:
		return a.callDescent(x, f)
	}
	return roamingFreeRanging, false
}

// callByGeneralRules assesses a call under §19.8.1, giving the first argument
// the supplied usage when firstFixed is set, and every other argument the
// type-determined usage from its parameter's declared type.
func (a *analyzer) callByGeneralRules(
	x *xpath.FuncCall, f *streamFunc, firstUsage usage, firstFixed bool,
) (props, bool) {
	ops := make([]operand, 0, len(x.Args))
	for i, arg := range x.Args {
		u := typeDeterminedUsage(f.params[i])
		if i == 0 && firstFixed {
			u = firstUsage
		}
		ops = append(ops, a.operandOf(arg, u))
	}
	return combine(ops, false), true
}

// callAscent applies the call rules of §19.8.5.7.
//
// The spec lists four tests after computing P0 and S0 by the general rules
// with an inspection usage on the first argument. Two of them ("If P0 is
// roaming or S0 is free-ranging" and the later "If P0 is roaming") overlap;
// both are kept, in the order given, so the code reads against the spec.
func (a *analyzer) callAscent(x *xpath.FuncCall, f *streamFunc) (props, bool) {
	p0, ok := a.callByGeneralRules(x, f, usageInspection, true)
	if !ok {
		return roamingFreeRanging, false
	}
	r := a.ascentFromP0(p0)
	// The general rules give an all-motionless call grounded, which would
	// leave §19.8.5.7's last clause, "Otherwise, the function call is climbing
	// and motionless", unreachable. Read by its evident intent, a streamed
	// first argument makes the call climbing (su-ascent-903).
	if r == groundedMotionless {
		if arg := a.expr(x.Args[0]); arg.posture != postureGrounded {
			return props{postureClimbing, sweepMotionless}, true
		}
	}
	return r, true
}

// ascentFromP0 applies the tests §19.8.5.7 makes on P0 and S0 once they have
// been obtained by the general rules.
//
// It is separate from callAscent so that each test can be asserted directly.
// The W3C suite reaches only the first of them -- every other ascent case is
// already rejected by the body check -- and a rule the corpus never exercises
// is one a unit test has to cover instead.
func (a *analyzer) ascentFromP0(p0 props) props {
	if p0.posture == postureRoaming || p0.sweep == sweepFreeRanging {
		return roamingFreeRanging
	}
	// An ascending function may not consume: it returns ancestors, which are
	// reachable without moving the input position.
	if p0.sweep != sweepMotionless {
		return roamingFreeRanging
	}
	if p0.posture == postureGrounded {
		return groundedMotionless
	}
	return props{postureClimbing, sweepMotionless}
}

// callDescent applies the call rules of §19.8.5.5 (shallow-descent) and
// §19.8.5.6 (deep-descent), which differ only in the posture they give a call
// whose first argument is not grounded.
func (a *analyzer) callDescent(x *xpath.FuncCall, f *streamFunc) (props, bool) {
	// T0 is the declared type of the first parameter; P0 and S0 are the
	// type-adjusted posture and sweep of the first argument.
	t0 := f.params[0]
	arg0 := a.expr(x.Args[0])
	p0, s0 := arg0.posture, arg0.sweep
	if !typePermitsNodes(t0) {
		// A first parameter that admits no nodes atomizes whatever is
		// supplied, so the type-adjusted posture is grounded.
		p0 = postureGrounded
	}

	// "If P0 is not striding or grounded, the function call is roaming and
	// free-ranging."
	if p0 != postureStriding && p0 != postureGrounded {
		return roamingFreeRanging, true
	}

	// C is the construct whose operands are the arguments after the first,
	// each with the type-determined usage of its parameter. With only one
	// argument, P1 is grounded and S1 motionless.
	p1, s1 := postureGrounded, sweepMotionless
	if len(x.Args) > 1 {
		rest := make([]operand, 0, len(x.Args)-1)
		for i := 1; i < len(x.Args); i++ {
			rest = append(rest, a.operandOf(x.Args[i], typeDeterminedUsage(f.params[i])))
		}
		c := combine(rest, false)
		p1, s1 = c.posture, c.sweep
	}

	// "If P1 is not grounded, the function call is roaming and free-ranging."
	if p1 != postureGrounded {
		return roamingFreeRanging, true
	}
	// "If S0 and S1 are both consuming, or if either is free-ranging, then
	// the function call is roaming and free-ranging."
	if s0 == sweepConsuming && s1 == sweepConsuming {
		return roamingFreeRanging, true
	}
	if s0 == sweepFreeRanging || s1 == sweepFreeRanging {
		return roamingFreeRanging, true
	}
	// "If P0 is grounded, then the posture of the function call is grounded,
	// and the sweep is the wider of S0 and S1."
	if p0 == postureGrounded {
		return props{postureGrounded, wider(s0, s1)}, true
	}

	// Otherwise the posture is P0 for shallow-descent and crawling for
	// deep-descent, and the sweep is decided by whether either the declared
	// type of the first parameter or the static type of the first argument
	// rules out nodes with children.
	post := p0
	if f.category == catDeepDescent {
		post = postureCrawling
	}
	sw := sweepConsuming
	if !typePermitsChildren(t0) || !a.allowsChildren(x.Args[0]) {
		sw = s0
	}
	return props{post, sw}, true
}
