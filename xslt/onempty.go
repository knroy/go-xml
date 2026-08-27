package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// The XSLT 3.0 conditional content instructions, section 8.4:
// xsl:where-populated, xsl:on-empty and xsl:on-non-empty.
//
// The three are one feature. All of them ask the same question — did this
// sequence constructor actually produce anything worth wrapping? — and differ
// only in what they do with the answer. They are implemented together because
// the answer is computed in one place, execSequence, which has to run the
// section 8.4.4 algorithm over the whole constructor rather than let each
// instruction decide for itself: whether an xsl:on-empty fires depends on what
// its *siblings* produced, which no instruction can see from inside Execute.
//
// Two different emptiness predicates are involved and they are not the same
// test. "Vacuous" (8.4.2) governs xsl:on-empty and xsl:on-non-empty;
// "deemed-empty" (8.4.1) governs xsl:where-populated and is strictly broader —
// an empty element is deemed-empty but is not vacuous. Conflating them makes
// <out><e/><xsl:on-empty select="1"/></out> emit the 1, which is wrong.

// onEmptyInstr implements xsl:on-empty.
//
// Execute is only ever reached through execSequence, which decides whether the
// instruction is triggered. Executing one directly — which happens when an
// xsl:on-empty is the whole body handed to a construct that does not route
// through the algorithm — is the "only instruction in the sequence
// constructor" case of 8.4.2, where it is always evaluated.
type onEmptyInstr struct {
	sel  *xpath.Compiled
	body []Instruction
}

func (i *onEmptyInstr) Execute(rt *runtime, out *outputBuilder) error {
	return evalConditionalContent(i.sel, i.body, rt, out)
}

// onNonEmptyInstr implements xsl:on-non-empty.
type onNonEmptyInstr struct {
	sel  *xpath.Compiled
	body []Instruction
}

func (i *onNonEmptyInstr) Execute(rt *runtime, out *outputBuilder) error {
	return evalConditionalContent(i.sel, i.body, rt, out)
}

// evalConditionalContent runs the select attribute or the contained sequence
// constructor, whichever the instruction has.
//
// Both instructions "have the same content model as xsl:sequence, and when it
// is evaluated, the same rules apply", so the select expression's result is
// appended as a sequence rather than being turned into a text node.
func evalConditionalContent(sel *xpath.Compiled, body []Instruction,
	rt *runtime, out *outputBuilder) error {

	if sel != nil {
		seq, err := sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		appendSequence(seq, out)
		return nil
	}
	return execSequence(body, rt, out)
}

// wherePopulatedInstr implements xsl:where-populated.
type wherePopulatedInstr struct {
	body []Instruction
}

// Execute evaluates the body and keeps only the items that are not
// deemed-empty, which is 8.4.1's $R[not(deemed-empty(.))].
//
// The body is evaluated into a detached builder rather than straight into out
// because the filter applies to the items the constructor produced, and once
// they have been appended to the real output they have been merged into the
// tree under construction and can no longer be inspected one by one.
func (i *wherePopulatedInstr) Execute(rt *runtime, out *outputBuilder) error {
	sub := newOutputBuilder()
	if err := execSequence(i.body, rt.temporaryOutput(), sub); err != nil {
		return err
	}
	for _, it := range sub.sequence() {
		if deemedEmpty(it) {
			continue
		}
		appendItem(out, it)
	}
	return nil
}

// deemedEmpty is 8.4.1's deemed-empty() function.
//
// It is deliberately more generous than vacuous(): a childless element or
// document node counts here and does not there. That is the point of
// xsl:where-populated — it exists to drop the wrapper element whose content
// turned out to be nothing, and the wrapper is exactly a childless element.
func deemedEmpty(it xdm.Item) bool {
	switch v := it.(type) {
	case *xdm.Node:
		switch v.Kind {
		case xdm.KindElement, xdm.KindDocument:
			// Attributes and namespaces do not save an element here: 8.4.1
			// says so explicitly, and the section's own example relies on it
			// — <ul class="my-list"> with no list items is dropped.
			return len(v.Children) == 0
		default:
			return v.StringValue() == ""
		}
	case *xdm.MapItem:
		return v.Len() == 0
	case *xdm.ArrayItem:
		// "an array where the result of flattening the array is either an
		// empty sequence, or a sequence in which every item is deemed empty".
		for _, m := range v.Members() {
			for _, sub := range m {
				if !deemedEmpty(sub) {
					return false
				}
			}
		}
		return true
	case *xdm.Atomic:
		return v.String() == ""
	}
	// A function item has no string value and is not one of the listed cases,
	// so it is never deemed empty.
	return false
}

// vacuous is 8.4.2's definition, which decides whether xsl:on-empty fires.
//
// The list is closed and shorter than deemed-empty's: a zero-length text node,
// a childless document node, an atomic value whose string is zero-length, and
// an array of vacuous items. Notably absent are the empty *element* and the
// whitespace-only text node, both of which count as content — the spec adds a
// note saying so, because both are the obvious wrong guess.
func vacuous(it xdm.Item) bool {
	switch v := it.(type) {
	case *xdm.Node:
		switch v.Kind {
		case xdm.KindDocument:
			return len(v.Children) == 0
		case xdm.KindText:
			return v.Value == ""
		case xdm.KindElement:
			// An element is content however empty it is.
			return false
		default:
			// Comments, PIs, attributes and namespace nodes are all
			// significant. The note in 8.4.2 is explicit that attribute and
			// namespace nodes prevent the trigger.
			return false
		}
	case *xdm.ArrayItem:
		for _, m := range v.Members() {
			for _, sub := range m {
				if !vacuous(sub) {
					return false
				}
			}
		}
		return true
	case *xdm.Atomic:
		return v.String() == ""
	}
	return false
}

// hasConditionalContent reports whether a compiled body needs the 8.4.4
// algorithm rather than a plain left-to-right run.
//
// execSequence is the hottest loop in the package — every template body, every
// branch of every xsl:choose goes through it — so the test is a scan for a
// type rather than anything the instructions have to carry, and the common
// answer of "no" costs one pass over a slice that is almost always short.
func hasConditionalContent(body []Instruction) bool {
	for _, instr := range body {
		switch instr.(type) {
		case *onEmptyInstr, *onNonEmptyInstr:
			return true
		}
	}
	return false
}

// execConditionalSequence runs a sequence constructor that contains
// xsl:on-empty or xsl:on-non-empty, following the algorithm of section 8.4.4.
//
// The algorithm's three variables are kept verbatim: R the result so far, L
// the xsl:on-non-empty instructions still waiting to learn whether they fire,
// and F whether a non-vacuous item has been seen. Keeping the spec's names
// makes the correspondence checkable against the text, which matters because
// the ordering rules here are subtle — a deferred xsl:on-non-empty is emitted
// at the position it occupied in the source, not where the triggering item
// appeared.
func execConditionalSequence(body []Instruction, rt *runtime, out *outputBuilder) error {
	// Ordinary instructions write into a detached builder so that their
	// results can be discarded wholesale if an xsl:on-empty turns out to be
	// triggered. Writing straight to out would make that undoable.
	//
	// A detached builder also puts attribute nodes into the item list instead
	// of onto the enclosing element, which is what lets the vacuity test see
	// them. That is required: 8.4.2 says attributes created by the sequence
	// constructor are significant, while attributes coming from the literal
	// result element or from use-attribute-sets are not — and those are
	// applied to the real builder, so the split falls out correctly.
	type pending struct {
		instr Instruction
		at    int // index in R where the results belong
	}
	var (
		r       xdm.Sequence
		l       []pending
		f       bool
		onEmpty Instruction
	)

	// flush evaluates the deferred xsl:on-non-empty instructions, in order,
	// splicing each one's result into the position it held in the source.
	flush := func() error {
		if len(l) == 0 {
			return nil
		}
		// Later insertions are done first so that the recorded indices stay
		// valid: splicing at a low index would shift everything after it.
		for i := len(l) - 1; i >= 0; i-- {
			p := l[i]
			sub := newOutputBuilder()
			if err := p.instr.Execute(rt, sub); err != nil {
				return err
			}
			items := sub.sequence()
			if len(items) == 0 {
				continue
			}
			tail := append(xdm.Sequence(nil), r[p.at:]...)
			r = append(append(r[:p.at:p.at], items...), tail...)
		}
		l = nil
		return nil
	}

	for _, instr := range body {
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		// A variable declared here is in scope for the rest of the
		// constructor exactly as in execSequence; the conditional
		// instructions do not change scoping.
		if v, ok := instr.(*varInstr); ok {
			if v.unused {
				continue
			}
			val, err := evalVariable(v.v, rt)
			if err != nil {
				return err
			}
			rt = rt.withVar(v.v.Name, val)
			continue
		}

		switch ci := instr.(type) {
		case *onNonEmptyInstr:
			if f {
				sub := newOutputBuilder()
				if err := ci.Execute(rt, sub); err != nil {
					return err
				}
				r = append(r, sub.sequence()...)
			} else {
				l = append(l, pending{instr: ci, at: len(r)})
			}
			continue

		case *onEmptyInstr:
			// 8.4.2 makes xsl:on-empty the last thing that can produce
			// content, so its decision can be taken here and applied once the
			// loop has finished. Taking it now would be wrong only if a later
			// sibling could still write something, and the grammar check
			// rejects that stylesheet as XTSE0010.
			if !f {
				onEmpty = ci
			}
			continue
		}

		sub := newOutputBuilder()
		if err := instr.Execute(rt, sub); err != nil {
			return err
		}
		for _, it := range sub.sequence() {
			if !f && !vacuous(it) {
				// The transition to "non-empty" happens before the item that
				// caused it is appended, so a deferred header lands in front
				// of the first real content rather than after it.
				if err := flush(); err != nil {
					return err
				}
				f = true
			}
			r = append(r, it)
		}
	}

	if !f && onEmpty != nil {
		// "the existing contents of R are discarded, the instruction is
		// evaluated, and its results are appended to R". Everything collected
		// so far was vacuous by definition, so nothing observable is lost.
		r = nil
		sub := newOutputBuilder()
		if err := onEmpty.Execute(rt, sub); err != nil {
			return err
		}
		r = append(r, sub.sequence()...)
	} else if !f {
		// No xsl:on-empty and nothing non-vacuous: the pending
		// xsl:on-non-empty instructions never fire, and 8.4.3 says their
		// results are simply not included.
		l = nil
	}

	for _, it := range r {
		if err := appendItemChecked(out, it); err != nil {
			return err
		}
	}
	return nil
}

// appendItem puts one already-constructed item back onto a builder.
func appendItem(out *outputBuilder, it xdm.Item) {
	switch v := it.(type) {
	case *xdm.Node:
		out.appendNode(v)
	case *xdm.Atomic:
		out.appendValue(v)
	default:
		out.items = append(out.items, it)
	}
}

// appendItemChecked is appendItem for items that came out of a detached
// builder, where an attribute node has to be re-offered as an attribute rather
// than as a child.
//
// The detour exists because the detached builder deliberately has no open
// element, so xsl:attribute left its result in the item list. Appending that
// node to the real builder would make it a child of the element under
// construction, which is not a thing an attribute may be.
func appendItemChecked(out *outputBuilder, it xdm.Item) error {
	if n, ok := it.(*xdm.Node); ok && n.Kind == xdm.KindAttribute {
		return out.addAttributeTyped(n.Name, n.Value, n.TypeAnnotation)
	}
	appendItem(out, it)
	return nil
}

// compileOnEmpty compiles xsl:on-empty, xsl:on-non-empty and
// xsl:where-populated.
func (c *compiler) compileOnEmpty(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	switch n.Name.Local {
	case "where-populated":
		body, err := c.compileSequence(n, n)
		if err != nil {
			return nil, err
		}
		return &wherePopulatedInstr{body: body}, nil
	}

	// xsl:on-empty and xsl:on-non-empty share xsl:sequence's content model,
	// including its rule that select and a sequence constructor are mutually
	// exclusive.
	var sel *xpath.Compiled
	if a := n.Attr("", "select"); a != nil {
		var err error
		if sel, err = compileExpr(a.Value, ns); err != nil {
			return nil, err
		}
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	if sel != nil && len(body) > 0 {
		return nil, fmt.Errorf("XTSE3185: %s has both a select attribute and content",
			n.Name.Lexical())
	}

	if n.Name.Local == "on-empty" {
		return &onEmptyInstr{sel: sel, body: body}, nil
	}
	return &onNonEmptyInstr{sel: sel, body: body}, nil
}
