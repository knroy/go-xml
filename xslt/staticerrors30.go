package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xsd"
)

// Static and dynamic checks introduced by XSLT 3.0 that have no natural home
// in the declaration they constrain, or that constrain a declaration compiled
// in more than one place.

// checkImportSchemaInline applies XTSE0215 to an xsl:import-schema that
// carries an inline xs:schema.
//
// Section 3.14: the inline schema IS the schema, so a schema-location naming
// a second one leaves no rule for which of the two wins, and a namespace
// attribute that disagrees with the inline schema's targetNamespace asserts
// something the schema itself contradicts. An absent targetNamespace means no
// namespace, which only an absent (or empty) namespace attribute agrees with.
func checkImportSchemaInline(el, inline *xdm.Node) error {
	if inline == nil {
		return nil
	}
	if el.Attr("", "schema-location") != nil {
		return fmt.Errorf(
			"XTSE0215: xsl:import-schema contains an xs:schema element and " +
				"also has a schema-location attribute")
	}
	a := el.Attr("", "namespace")
	if a == nil {
		return nil
	}
	declared := strings.TrimSpace(a.Value)
	target := strings.TrimSpace(inline.AttrValue("targetNamespace"))
	if declared != target {
		return fmt.Errorf(
			"XTSE0215: xsl:import-schema/@namespace is %q but the contained "+
				"xs:schema has targetNamespace %q", declared, target)
	}
	return nil
}

// checkKeyComposite applies XTSE1222 to two xsl:key declarations of the same
// name.
//
// Section 16.3 makes the composite attribute a property of the key rather
// than of the declaration: composite="yes" indexes a node under the whole
// sequence its use expression returns, composite="no" under each item
// separately, and one index cannot be built both ways. This mirrors the
// XTSE1220 check on collations immediately above the call site.
func (c *compiler) checkKeyComposite(el *xdm.Node, qn xdm.QName) error {
	composite := isYes(el.AttrValue("composite"))
	if prev, ok := c.keyComposites[qn.Clark()]; ok && prev != composite {
		return fmt.Errorf(
			"XTSE1222: the xsl:key declarations named %s give different "+
				"effective values for the composite attribute", qn.Lexical())
	}
	if c.keyComposites == nil {
		c.keyComposites = map[string]bool{}
	}
	c.keyComposites[qn.Clark()] = composite
	return nil
}

// checkCatchSelect applies XTSE3150 to an xsl:catch.
//
// Section 12.3 allows the recovery value to be given either by @select or by
// a sequence constructor, and giving both leaves no rule for reconciling
// them — the same condition XTSE1205 states for xsl:key.
func checkCatchSelect(n *xdm.Node) error {
	if n.Attr("", "select") == nil {
		return nil
	}
	for _, ch := range n.Children {
		switch ch.Kind {
		case xdm.KindElement, xdm.KindComment, xdm.KindPI:
			return fmt.Errorf(
				"XTSE3150: xsl:catch has a select attribute and is not empty")
		case xdm.KindText:
			// Indentation is not content, for the same reason it is not
			// content in a static declaration: every real stylesheet has it.
			if strings.TrimSpace(ch.Value) != "" {
				return fmt.Errorf(
					"XTSE3150: xsl:catch has a select attribute and is not empty")
			}
		}
	}
	return nil
}

// checkStripPreserveConflict applies XTSE0270 across a whole package.
//
// Section 4.4: "the same NameTest appears in both an xsl:strip-space and an
// xsl:preserve-space declaration ... if both have the same import precedence".
// Two NameTests are the same if they match the same set of names, which the
// expanded QName already answers — a wildcard is recorded with an empty local
// or URI, so comparing the expanded forms compares the name sets.
//
// The check runs over the collected declarations rather than at the point one
// is compiled because a conflict is between two declarations that may be in
// different modules and may be compiled in either order.
func checkStripPreserveConflict(strip, preserve []spaceDecl) error {
	// XSLT 2.0 made this the recoverable XTRE0270, whose recovery action is to
	// let the last declaration win, and strip-space-019 asserts the recovered
	// output. 3.0 promoted it to a static error. Which applies is a property
	// of the processor rather than of the module: strip-space-019 is scoped
	// to 1.0 and 2.0 only, and 019a repeats it for 3.0 demanding XTSE0270.
	if !processorAtLeast30() {
		return nil
	}
	seen := map[string]int{}
	for _, s := range strip {
		seen[s.key()] = s.precedence
	}
	for _, p := range preserve {
		if prec, ok := seen[p.key()]; ok && prec == p.precedence {
			return fmt.Errorf(
				"XTSE0270: %s appears in both xsl:strip-space and "+
					"xsl:preserve-space at the same import precedence",
				p.display())
		}
	}
	return nil
}

// spaceDecl is one NameTest from an xsl:strip-space or xsl:preserve-space,
// carried with the import precedence XTSE0270 compares.
type spaceDecl struct {
	name       xdm.QName
	precedence int
}

// key identifies the set of names the NameTest matches. A wildcard half is
// recorded as an empty string by the parser, which is also how "no namespace"
// and "any local name" are recorded, so the two halves are joined with a
// separator that cannot occur in either.
func (d spaceDecl) key() string { return d.name.URI + "\x00" + d.name.Local }

func (d spaceDecl) display() string {
	if d.name.Local == "*" && d.name.URI == "" {
		return `the NameTest "*"`
	}
	return "the NameTest " + d.name.Lexical()
}

// inlineSchemaOf returns the xs:schema child of an xsl:import-schema, if any.
func inlineSchemaOf(el *xdm.Node) *xdm.Node {
	for _, child := range el.ChildElements() {
		if child.IsElement(xsd.NSSchema, "schema") {
			return child
		}
	}
	return nil
}

// checkOverrideTemplates applies XTSE3440 and XTSE3460 to the template rules
// declared inside an xsl:use-package's xsl:override.
//
// Both rules exist because an overriding template rule replaces one named
// component of the used package, so it has to identify exactly one mode and
// has to have a way of reaching the component it replaces.
//
// XTSE3440 (section 3.5.2): the mode list may not contain #all or #unnamed,
// may not contain #default when the default mode is the unnamed mode, and may
// not be omitted when the default mode is the unnamed mode. A component of the
// used package belongs to one named mode; #all and #unnamed do not name one.
//
// XTSE3460: xsl:apply-imports inside such a rule has no meaning -- import
// precedence is a property of the using package, not of the overridden
// component -- so xsl:next-match is the instruction that reaches it.
//
// The walk is over the stylesheet tree because this engine does not resolve
// xsl:use-package: the rules are entirely structural, and answering them does
// not require knowing which package is being used.
func checkOverrideTemplates(root *xdm.Node) error {
	var walk func(n *xdm.Node) error
	walk = func(n *xdm.Node) error {
		for _, ch := range n.ChildElements() {
			if isXSL(ch, "override") {
				for _, decl := range ch.ChildElements() {
					if !isXSL(decl, "template") ||
						decl.Attr("", "match") == nil {
						continue
					}
					if err := checkOverrideRule(decl); err != nil {
						return err
					}
				}
				continue
			}
			if err := walk(ch); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// checkOverrideRule applies XTSE3440 and XTSE3460 to one template rule.
func checkOverrideRule(decl *xdm.Node) error {
	dflt, err := defaultModeAt(decl)
	if err != nil {
		return err
	}
	mode := decl.Attr("", "mode")
	if mode == nil {
		if dflt == "" {
			return fmt.Errorf(
				"XTSE3440: a template rule in xsl:override omits the mode " +
					"attribute and the default mode is the unnamed mode")
		}
	} else {
		for _, tok := range strings.Fields(mode.Value) {
			switch tok {
			case "#all", "#unnamed":
				return fmt.Errorf(
					"XTSE3440: a template rule in xsl:override names mode %s",
					tok)
			case "#default":
				if dflt == "" {
					return fmt.Errorf(
						"XTSE3440: a template rule in xsl:override names " +
							"mode #default and the default mode is the " +
							"unnamed mode")
				}
			}
		}
	}
	return findApplyImports(decl)
}

// findApplyImports reports XTSE3460 for an xsl:apply-imports anywhere in a
// template rule's body. It does not descend into a nested xsl:template,
// because a rule declared inside one is not this rule.
func findApplyImports(n *xdm.Node) error {
	for _, ch := range n.ChildElements() {
		if isXSL(ch, "apply-imports") {
			return fmt.Errorf(
				"XTSE3460: xsl:apply-imports appears in a template rule " +
					"declared within xsl:override; use xsl:next-match")
		}
		if isXSL(ch, "template") {
			continue
		}
		if err := findApplyImports(ch); err != nil {
			return err
		}
	}
	return nil
}

// checkIterateParam applies XTSE3520 to one xsl:param of an xsl:iterate.
//
// Section 8.4: an xsl:iterate parameter is given its value by the
// xsl:next-iteration of the previous cycle, and its initial value by the
// declaration itself. A parameter with no explicit default is only an error
// when its implicit default cannot be the parameter's value.
//
// Section 9.2 defines that narrowly: the implicit default is the empty
// sequence when there is an as attribute and a zero-length string when there
// is not, and the parameter is "implicitly mandatory" only if that value
// cannot be converted to the required type -- "if it has an as attribute
// which does not permit the empty sequence". <xsl:param name="p"
// as="xs:string?"/> therefore starts at the empty sequence and is perfectly
// legal; treating every defaultless parameter as mandatory refused it.
//
// required="yes" is not a way out either; 8.4 forbids the attribute on an
// xsl:iterate parameter, which the element table already refuses.
func checkIterateParam(ch *xdm.Node) error {
	if ch.Attr("", "select") != nil {
		return nil
	}
	if a := ch.Attr("", "as"); a == nil || sequenceTypePermitsEmpty(a.Value) {
		return nil
	}
	for _, k := range ch.Children {
		switch k.Kind {
		case xdm.KindElement, xdm.KindComment, xdm.KindPI:
			return nil
		case xdm.KindText:
			if strings.TrimSpace(k.Value) != "" {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"XTSE3520: xsl:param %q of xsl:iterate supplies no default value, "+
			"which makes it implicitly mandatory", ch.AttrValue("name"))
}

// unboundTypePrefixError is XTDE1428's second half: a lexically valid QName
// whose prefix nothing binds. It parallels unboundPrefixError, which says the
// same thing for function-available's XTDE1400.
func unboundTypePrefixError(name string) error {
	prefix, _ := xdm.SplitQName(name)
	return fmt.Errorf(
		"XTDE1428: type-available(%q): no namespace declaration is in scope "+
			"for prefix %q", name, prefix)
}

// groupingKeyVar marks that the grouping in scope has a grouping key at all.
//
// XTDE1071 distinguishes "no grouping" from "a grouping whose key is absent":
// group-starting-with and group-ending-with partition by position, so there is
// no key to report, where group-by and group-adjacent always have one. Both
// bind the key sequence to nil in the first case, so a separate marker is what
// tells them apart -- the same reason groupingScopeVar exists beside the group
// itself.
var groupingKeyVar = xdm.QName{URI: internalNS, Local: "grouping-key-present"}

// withGroupingKeyPresence records whether the grouping form supplies a key.
func (rt *runtime) withGroupingKeyPresence(present bool) *runtime {
	if !present {
		return rt.withVar(groupingKeyVar, nil)
	}
	return rt.withVar(groupingKeyVar, xdm.One(xdm.NewBoolean(true)))
}

// groupingKeyPresent reports whether the grouping in scope has a key.
func groupingKeyPresent(ctx *xpath.Context) bool {
	seq, _ := ctx.LookupVar(groupingKeyVar)
	return len(seq) > 0
}

// failMultipleMatch applies XTDE0540 to a mode declared
// on-multiple-match="fail".
//
// The conflict-resolution algorithm normally picks the last of the tied rules
// and carries on; 6.4 lets a mode ask for the ambiguity to be reported
// instead. The tie is the same one warning-on-multiple-match reports -- the
// list is sorted by (import precedence, priority, declaration order) and
// selection stops at the first match, so a later rule is a conflict only when
// it ties the winner on both precedence and priority.
func (s *Stylesheet) failMultipleMatch(node *xdm.Node, mode string,
	won *Template, next int, ctx *xpath.Context) error {

	if !s.modeFailMultiple[mode] {
		return nil
	}
	for i := next; i < len(s.templates); i++ {
		t := s.templates[i]
		if t.importPrecedence != won.importPrecedence ||
			t.Priority != won.Priority {
			return nil
		}
		// Branches of one union pattern are one template rule, so a node
		// matching two of them is not a conflict (spec bug 30402, test
		// mode-1516). compileTemplate stamps every rule split from the same
		// xsl:template with the declaration's own declOrder, which is what
		// makes them recognisable here as the winner seen again.
		if t.unionGroup == won.unionGroup {
			continue
		}
		if !t.matchesMode(mode) {
			continue
		}
		ok, err := t.Match.Matches(node, ctx)
		if err != nil {
			return err
		}
		if ok {
			return fmt.Errorf(
				"XTDE0540: more than one template rule matches %s in mode "+
					"%s at the same import precedence and priority, and the "+
					"mode declares on-multiple-match=\"fail\"",
				nodeLabel(node), modeLabel(mode))
		}
	}
	return nil
}

// currentAbsentVar marks a dynamic function call, across which XTDE1360 makes
// fn:current() behave "as if the context item is absent".
//
// A dynamic call carries no XSLT focus with it: 24.3 already clears the
// current output URI the same way, and current#0() is the example the spec
// itself gives. Clearing the current-node binding alone is not enough,
// because fn:current() falls back to the context item when nothing bound it
// -- which is right for a bare XPath evaluation and wrong here -- so the
// crossing is recorded rather than inferred.
var currentAbsentVar = xdm.QName{URI: internalNS, Local: "current-absent"}

func init() {
	xpath.ClearedOnDynamicCall = append(
		xpath.ClearedOnDynamicCall, currentVar)
	xpath.MarkedOnDynamicCall = append(
		xpath.MarkedOnDynamicCall, currentAbsentVar)
}

// currentIsAbsent reports whether a dynamic call is being evaluated.
func currentIsAbsent(ctx *xpath.Context) bool {
	seq, _ := ctx.LookupVar(currentAbsentVar)
	return len(seq) > 0
}

// checkModeTyped applies XTTE3100 and XTTE3110 to one node selected by an
// xsl:apply-templates.
//
// Section 6.6 lets a mode declare what it expects of the nodes reaching it.
// typed="yes"/"strict"/"lax" says the mode's template rules were written
// against a schema, so an untyped element or attribute is a type error;
// typed="no" says the opposite, and a typed one is the error. Both are about
// the node's own annotation rather than about the tree it came from -- a
// stylesheet may perfectly well build a validated element inside an
// unvalidated result and apply templates to it, which is what error-3100a and
// error-3110a do.
func (s *Stylesheet) checkModeTyped(node *xdm.Node, mode string) error {
	want, ok := s.modeTyped[mode]
	if !ok {
		return nil
	}
	if node.Kind != xdm.KindElement && node.Kind != xdm.KindAttribute {
		return nil
	}
	untyped := node.TypeAnnotation == "" ||
		node.TypeAnnotation == "{"+xdm.NSXS+"}untyped" ||
		node.TypeAnnotation == "{"+xdm.NSXS+"}untypedAtomic"
	switch want {
	case "no":
		if !untyped {
			return fmt.Errorf(
				"XTTE3110: mode %s declares typed=\"no\" but %s has type %s",
				modeLabel(mode), nodeLabel(node), node.TypeAnnotation)
		}
	default:
		if untyped {
			return fmt.Errorf(
				"XTTE3100: mode %s declares typed=%q but %s is untyped",
				modeLabel(mode), want, nodeLabel(node))
		}
	}
	return nil
}

// sequenceTypePermitsEmpty reports whether a sequence type written in an as
// attribute admits the empty sequence.
//
// The occurrence indicator is the whole question, and it is the last
// non-space character of the type, so the answer is lexical: no expression
// parser is needed and none is available where the static checks run.
func sequenceTypePermitsEmpty(as string) bool {
	as = strings.TrimSpace(as)
	if as == "" {
		return true
	}
	switch as[len(as)-1] {
	case '?', '*':
		return true
	}
	return as == "empty-sequence()"
}

// checkTypedStrictPatterns applies XTSE3105.
//
// "It is a static error if a template rule applicable to a mode that is
// defined with typed="strict" uses a match pattern that contains a
// RelativePathExprP whose first StepExprP is an AxisStepP whose ForwardStepP
// uses an axis whose principal node kind is Element and whose NodeTest is an
// EQName that does not correspond to the name of any global element
// declaration in the in-scope schema components."
//
// Every clause of that sentence narrows it, and the narrowing is the whole
// point -- section 6.6.2 says what the rule is FOR: under typed="strict" a
// name test in the first step "is interpreted as schema-element(E)", so a name
// with no global declaration could never be interpreted at all. A name in any
// LATER step keeps its ordinary meaning, and match-224 is written to prove it:
// "my:userNode/total-garbage" names an undeclared element in its second step
// and is documented in the test as "not an error, but could generate a
// warning".
//
// So the check applies only to:
//
//   - the FIRST step of the pattern, never a later one;
//   - a step on an axis whose principal node kind is Element, which excludes
//     the attribute and namespace axes;
//   - a NodeTest that is an EQName, which excludes every wildcard -- "*",
//     "prefix:*" and "*:local" all fall to the spec's "Otherwise" branch,
//     along with the kind tests;
//   - a mode the stylesheet declares typed="strict", and only that value:
//     "lax" and "yes" have their own, weaker rules.
func (c *compiler) checkTypedStrictPatterns() error {
	if c.sheet.schema == nil || len(c.sheet.modeTyped) == 0 {
		return nil
	}
	strict := false
	for _, v := range c.sheet.modeTyped {
		if v == "strict" {
			strict = true
			break
		}
	}
	if !strict {
		return nil
	}
	for _, t := range c.sheet.templates {
		if t.Match == nil {
			continue
		}
		for _, mode := range templateModes(t) {
			if c.sheet.modeTyped[mode] != "strict" {
				continue
			}
			if name, ok := t.Match.firstStepElementName(); ok {
				if _, declared := c.sheet.schema.Elements[name]; !declared {
					return fmt.Errorf(
						"XTSE3105: mode %s is declared typed=\"strict\", so "+
							"the name %s in the first step of match=%q is "+
							"interpreted as schema-element(%s), but no global "+
							"element declaration has that name",
						modeLabel(mode), name.Lexical(), t.Match.src,
						name.Lexical())
				}
			}
		}
	}
	return nil
}

// templateModes returns the modes a template rule applies to, with the
// unnamed mode spelled as the empty string.
func templateModes(t *Template) []string {
	if len(t.Mode) == 0 {
		return []string{""}
	}
	return t.Mode
}

// firstStepElementName returns the expanded name of the pattern's first step
// when that step is an EQName test on an element axis, which is the only shape
// XTSE3105 speaks about.
//
// It answers for a pattern with exactly one alternative. A union is split into
// one rule per branch before this runs, so each branch arrives here on its
// own.
func (p *Pattern) firstStepElementName() (xdm.QName, bool) {
	if len(p.alts) != 1 || len(p.general) != 0 {
		return xdm.QName{}, false
	}
	a := p.alts[0]
	// An id() or key() pattern has no first AxisStepP at all.
	if a.call != nil || len(a.steps) == 0 {
		return xdm.QName{}, false
	}
	s := a.steps[0]
	// The attribute and namespace axes have a principal node kind other than
	// Element, which the spec's own "Otherwise" branch excludes.
	if s.attribute || s.namespace {
		return xdm.QName{}, false
	}
	nt, ok := s.nodeTest.(*xpath.NameTest)
	// A wildcard is not an EQName: "*", "prefix:*" and "*:local" are all
	// excluded, as is every kind test, which is not a NameTest at all.
	if !ok || nt.AnyURI || nt.AnyLocal {
		return xdm.QName{}, false
	}
	return xdm.QName{URI: nt.Name.URI, Local: nt.Name.Local}, true
}
