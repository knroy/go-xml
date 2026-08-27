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
// declaration itself. A parameter with no select, no content and no
// required="yes" is "implicitly mandatory" -- there is nothing to start it
// from and no instruction that could supply one -- so the spec makes it a
// static error rather than letting the first cycle fail.
//
// required="yes" is not a way out either; 8.4 forbids the attribute on an
// xsl:iterate parameter, which the element table already refuses.
func checkIterateParam(ch *xdm.Node) error {
	if ch.Attr("", "select") != nil {
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
