package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// stylesheetYes reads a boolean written in a stylesheet attribute.
//
// Section 3.4 spells such a boolean five ways: "yes"/"true"/"1" and
// "no"/"false"/"0", with surrounding whitespace stripped. The element table
// lists only yes and no for the xsl:mode attributes, so mode-0804 — which
// writes "true" and "1" — needs the full set here; xsl:evaluate/@schema-aware
// is declared a boolean outright and needs it for the same reason.
func stylesheetYes(v string) bool {
	switch strings.TrimSpace(v) {
	case "yes", "true", "1":
		return true
	}
	return false
}

// warn records one warning for the caller. A warning never stops the
// transform: it reports a condition the spec asks a processor to notice, and
// the transform's result is unaffected.
func (rt *runtime) warn(msg string) {
	if rt.warnings != nil {
		*rt.warnings = append(*rt.warnings, msg)
	}
}

// warnMultipleMatch reports the ambiguity xsl:mode/@warning-on-multiple-match
// asks about.
//
// The template list is sorted by (import precedence, priority, declaration
// order) and selection stops at the first match, so a second match is only a
// *conflict* when it ties the winner on both precedence and priority. A lower
// one lost on the merits and is not worth a warning.
func (s *Stylesheet) warnMultipleMatch(rt *runtime, node *xdm.Node,
	mode string, won *Template, next int, ctx *xpath.Context) error {

	if !s.modeWarnMultiple[mode] {
		return nil
	}
	for i := next; i < len(s.templates); i++ {
		t := s.templates[i]
		if t.importPrecedence != won.importPrecedence ||
			t.Priority != won.Priority {
			// Sorted order means everything past the first mismatch ranks
			// below the winner too.
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
			rt.warn(fmt.Sprintf(
				"more than one template rule matches %s in mode %s at the "+
					"same import precedence and priority",
				nodeLabel(node), modeLabel(mode)))
			return nil
		}
	}
	return nil
}

// warnNoMatch reports what xsl:mode/@warning-on-no-match asks about: a node
// that reached the built-in rules because no template rule matched it.
func (s *Stylesheet) warnNoMatch(rt *runtime, node *xdm.Node, mode string) {
	if !s.modeWarnNoMatch[mode] {
		return
	}
	rt.warn(fmt.Sprintf("no template rule matches %s in mode %s",
		nodeLabel(node), modeLabel(mode)))
}

// modeLabel names a mode for a diagnostic, spelling the unnamed one.
func modeLabel(mode string) string {
	if mode == "" {
		return "#unnamed"
	}
	return mode
}

// nodeLabel names a node for a diagnostic by kind and name.
func nodeLabel(node *xdm.Node) string {
	if node == nil {
		return "the context item"
	}
	switch node.Kind {
	case xdm.KindElement:
		return "element " + node.Name.Lexical()
	case xdm.KindAttribute:
		return "attribute " + node.Name.Lexical()
	case xdm.KindText:
		return "a text node"
	case xdm.KindComment:
		return "a comment"
	case xdm.KindPI:
		return "a processing instruction"
	case xdm.KindDocument:
		return "the document node"
	}
	return "a node"
}
