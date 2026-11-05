package xslt

// A template rule whose body has several consumers of its element (lane L1).
//
// §19.8 lets one construct consume its context once: the planner refuses a
// rule body with two consuming instructions, two consuming value templates
// or tests, or an xsl:fork whose prongs each consume the element. At the
// document node the fork's prongs each get a cursor of their own (a tee,
// streamfork.go); below it a cursor cannot be teed, the bytes before the
// element being gone. Such a rule runs on a grounded copy of its element
// instead: the element, whole, under shallow copies of its ancestors
// (Cursor.Ground), the copies carrying their originals' ordinals, so that
// names, attributes, identity, order and generate-id are the tree's. The
// body runs off the cursor, as the tree's code runs it, on the copy: every
// consumer reads it as a tree, in the tree's order, with the tree's errors.
//
// Memory: one copy of the element at a time, which is at most the tree
// (the document element's copy costs what its tree costs, as a loop's
// grounded match does: readyOps); what the body makes the tree makes too.
//
// What a copy has not that the tree has: the document beside and above the
// element (siblings, the root's content), and the document-level state --
// accumulators (computed over the document, not the copy), keys and ids,
// and fn:path, which counts siblings. The DTD (unparsed entities) the copy is
// given from the element's view. The rule's
// mode is verified streamable (link), so the body, and the rules it reaches
// through streamable modes, read no siblings and nothing below the root but
// the element; the document-level state is ruled out by refusing any module
// that names it (accumulators aside: the copy carries their values,
// streamtee_ground_accum.go), or that has templates in other modules (an xsl:import,
// xsl:include or xsl:use-package), whose text the scan cannot see.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// groundReasons are the planner's refusals that mean several consumers of
// one element: the only ones a grounded copy answers.
var groundReasons = []string{
	"two consuming instructions in one sequence constructor",
	"two consuming attribute value templates",
	"two consuming tests in xsl:choose",
	"two consuming parts of a text value template",
	"several prongs consume an element",
	"current-group() outside the consuming path", // streamsplit.go: the group read beside its path
	"a second consuming operand",                 // streamsplit.go: map { "a": string(A), "b": string(B) }
}

// groundRuleExec returns the plan of the rule el for an element when its
// body was refused (why) for several consumers and may run on a grounded
// copy of the element; nil otherwise.
func groundRuleExec(el *xdm.Node, cl nodeClass, why string) *streamExec {
	root := documentElementOf(el)
	if cl != classElem || !groundReason(why) || !groundModuleOK(root) {
		return nil
	}
	x := newStreamExec()
	x.class, x.modes, x.rule, x.groundRule = cl, templateModeNames(el), true, true
	x.groundAccums = !moduleCallsNone(root, groundAccumFns) // streamtee_ground_accum.go
	x.groundTop = groundTopKeeps(el)
	x.kinds[streamtrace.KindGround]++
	return x
}

// groundTopMakes are the instructions that, at the top of a rule's body,
// write only new nodes or nothing: none returns a node of the copy.
var groundTopMakes = map[string]bool{"param": true, "variable": true, "value-of": true, "text": true,
	"element": true, "attribute": true, "comment": true, "processing-instruction": true,
	"namespace": true, "number": true, "copy": true, "message": true}

// groundTopKeeps reports whether the rule el may return a node of its
// grounded copy as an item of its result: an instruction at its top (or at
// an xsl:result-document's) other than groundTopMakes. A body whose top is
// literal result elements and such constructors returns only new nodes, as
// the tree's does, so at the top of a principal result it is the tree's.
func groundTopKeeps(el *xdm.Node) bool {
	for d := range el.Descendants() {
		if d.Kind() == xdm.KindElement && d.Name().URI == xdm.NSXSL && topOf(d) != "" && !groundTopMakes[d.Name().Local] {
			return true
		}
	}
	return false
}

func groundReason(why string) bool {
	for _, r := range groundReasons {
		if strings.Contains(why, r) {
			return true
		}
	}
	return false
}

// groundUnsafe are the names of what a grounded copy answers otherwise than
// the tree: document-level state, and positions among siblings.
var groundUnsafe = []string{"key", "id", "idref", "element-with-id", "path", "root"}

// groundModuleOK reports whether root, the module of a rule, is the whole
// stylesheet and names nothing of groundUnsafe as a function call.
func groundModuleOK(root *xdm.Node) bool {
	for ch := range root.Children() {
		if isXSL(ch, "import") || isXSL(ch, "include") || isXSL(ch, "use-package") {
			return false
		}
	}
	return moduleCallsNone(root, groundUnsafe)
}

// moduleCallsNone reports whether root's module names nothing of names as a
// function call.
func moduleCallsNone(root *xdm.Node, names []string) bool {
	ok := true
	check := func(s string) {
		if ok && callsAny(s, names) {
			ok = false
		}
	}
	for a := range root.Attrs() {
		check(a.Value())
	}
	for n := range root.Descendants() {
		switch n.Kind() {
		case xdm.KindElement:
			for a := range n.Attrs() {
				check(a.Value())
			}
		case xdm.KindText:
			check(n.Value())
		}
		if !ok {
			return false
		}
	}
	return ok
}

// callsAny reports whether s has one of names followed by "(" (blanks
// between allowed), not as the tail of a longer name: generate-id( is not
// id(.
func callsAny(s string, names []string) bool {
	for _, name := range names {
		for i := 0; ; {
			j := strings.Index(s[i:], name)
			if j < 0 {
				break
			}
			j += i
			i = j + len(name)
			if j > 0 && nameChar(s[j-1]) {
				continue
			}
			if rest := strings.TrimLeft(s[i:], " \t\r\n"); strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, "#") {
				return true
			}
		}
	}
	return false
}

func nameChar(c byte) bool {
	return c == '-' || c == '_' || c == '.' || c == ':' || c >= '0' && c <= '9' ||
		c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// groundRule runs t, a rule whose plan is groundRuleExec's, for the element
// at the cursor's start tag: on a grounded copy of it, off the cursor. rt is
// the rule's runtime, its focus the element's view.
func (run *streamRun) groundRule(rt *runtime, t *Template, params, tunnels paramValues, out *outputBuilder, accums bool) error {
	if run.used || run.d == 0 || run.c.At().Kind != xdmstream.StartElement {
		return errStreamPlan
	}
	run.used = true
	g, err := run.groundRuleCopy(accums) // streamtee_ground_accum.go
	if err != nil {
		return err
	}
	// A copy's tree has no DTD of its own (xdm deliver); the view's has the
	// document's (xdm newViews), whose unparsed entities the tree's
	// unparsed-entity-uri and -public-id read.
	if v, ok := rt.ctx.Item.(*xdm.Node); ok {
		g.Tree().CopyDTDFrom(v.Tree())
	}
	sub := rt
	if rt.sel != nil {
		s := *rt.sel
		s.item = g // xsl:next-match sees the rule's item (selection.item)
		sub = rt.withSel(s)
	}
	n := *sub
	n.ctx = withFocusCurrent(rt.ctx, g, int(rt.ctx.Position), int(rt.ctx.Size), rt.depth)
	return run.offCursor(func() error { return runTemplateBody(&n, t, params, tunnels, out) })
}
