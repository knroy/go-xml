package xslt

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// textInstr emits literal text.
type textInstr struct{ text string }

func (i *textInstr) Execute(rt *runtime, out *outputBuilder) error {
	out.appendText(i.text)
	return nil
}

// blockInstr runs a nested sequence constructor.
type blockInstr struct{ body []Instruction }

func (i *blockInstr) Execute(rt *runtime, out *outputBuilder) error {
	return execSequence(i.body, rt, out)
}

// varInstr declares a variable. It is intercepted by execSequence, which
// rebinds the runtime for the following instructions; executing it directly is
// a no-op because a variable with no subsequent instructions has no effect.
type varInstr struct{ v *Variable }

func (i *varInstr) Execute(rt *runtime, out *outputBuilder) error { return nil }

// valueOfInstr implements xsl:value-of.
type valueOfInstr struct {
	sel          *xpath.Compiled
	body         []Instruction
	separator    string
	hasSeparator bool
}

func (i *valueOfInstr) Execute(rt *runtime, out *outputBuilder) error {
	var seq xdm.Sequence
	if i.sel != nil {
		v, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		seq = v
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt, sub); err != nil {
			return err
		}
		seq = sub.sequence()
	}

	// XSLT 1.0 took only the first item; 2.0 joins the whole sequence, with a
	// space separator when select is used and none otherwise. Defaulting to
	// the 2.0 behaviour matters for rule sets that rely on it, and the
	// separator attribute makes the choice explicit either way.
	sep := i.separator
	if !i.hasSeparator {
		if i.sel != nil {
			sep = " "
		} else {
			sep = ""
		}
	}
	out.appendText(stringJoin(seq, sep))
	return nil
}

// sequenceInstr implements xsl:sequence, which adds items to the result
// without converting them to text.
type sequenceInstr struct{ sel *xpath.Compiled }

func (i *sequenceInstr) Execute(rt *runtime, out *outputBuilder) error {
	seq, err := i.sel.Eval(rt.ctx)
	if err != nil {
		return err
	}
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			out.appendNode(v)
		case *xdm.Atomic:
			out.appendValue(v)
		}
	}
	return nil
}

// copyOfInstr implements xsl:copy-of, which deep-copies nodes.
type copyOfInstr struct {
	sel        *xpath.Compiled
	validation validationSpec
}

func (i *copyOfInstr) Execute(rt *runtime, out *outputBuilder) error {
	seq, err := i.sel.Eval(rt.ctx)
	if err != nil {
		return err
	}
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			// A deep copy is required: the result tree must not alias the
			// source, or a later instruction mutating one would change the
			// other.
			if v.Kind == xdm.KindDocument {
				for _, ch := range v.Children {
					c := deepCopy(ch)
					if err := i.validation.assess(rt, c); err != nil {
						return err
					}
					out.appendNode(c)
				}
				continue
			}
			if v.Kind == xdm.KindAttribute {
				if err := i.validation.assess(rt, v); err != nil {
					return err
				}
				if err := out.addAttribute(v.Name, v.Value); err != nil {
					return err
				}
				continue
			}
			c := deepCopy(v)
			// The copy is assessed rather than the original: validation may
			// annotate, and annotating the source document would leak a
			// property of this instruction into the tree everything else
			// still reads.
			if err := i.validation.assess(rt, c); err != nil {
				return err
			}
			out.appendNode(c)
		case *xdm.Atomic:
			out.appendValue(v)
		}
	}
	return nil
}

// deepCopy clones a subtree, detached from its original parent.
func deepCopy(n *xdm.Node) *xdm.Node {
	c := &xdm.Node{
		Kind:    n.Kind,
		Name:    n.Name,
		Value:   n.Value,
		BaseURI: n.BaseURI,
	}
	for _, ns := range n.Namespaces {
		c.AddNamespace(ns.Name.Local, ns.Value)
	}
	for _, a := range n.Attrs {
		c.AddAttr(&xdm.Node{Kind: xdm.KindAttribute, Name: a.Name, Value: a.Value})
	}
	for _, ch := range n.Children {
		c.AppendChild(deepCopy(ch))
	}
	return c
}

// copyInstr implements xsl:copy, a shallow copy of the context node.
type copyInstr struct {
	attrSets   []xdm.QName
	body       []Instruction
	validation validationSpec
}

func (i *copyInstr) Execute(rt *runtime, out *outputBuilder) error {
	node, ok := rt.ctx.Item.(*xdm.Node)
	if !ok {
		return fmt.Errorf("XTTE0945: xsl:copy requires a node as the context item")
	}

	switch node.Kind {
	case xdm.KindElement:
		// Shallow: the element and its namespaces are copied, attributes and
		// children come from the body. That is the distinction from
		// xsl:copy-of, and it is what makes the identity-transform idiom work.
		sub := out.startElement(node.Name)
		for _, ns := range node.Namespaces {
			sub.open.AddNamespace(ns.Name.Local, ns.Value)
		}
		if err := applyAttributeSets(rt, i.attrSets, sub); err != nil {
			return err
		}
		if err := execSequence(i.body, rt, sub); err != nil {
			return err
		}
		// The copy is assessed once it is complete, since validity is a
		// property of the whole element and its content.
		return i.validation.assess(rt, sub.open)

	case xdm.KindDocument:
		return execSequence(i.body, rt, out)

	case xdm.KindText:
		out.appendText(node.Value)
		return nil

	case xdm.KindAttribute:
		if err := i.validation.assess(rt, node); err != nil {
			return err
		}
		return out.addAttribute(node.Name, node.Value)

	case xdm.KindComment:
		out.appendNode(&xdm.Node{Kind: xdm.KindComment, Value: node.Value})
		return nil

	case xdm.KindPI:
		out.appendNode(&xdm.Node{Kind: xdm.KindPI, Name: node.Name, Value: node.Value})
		return nil
	}
	return nil
}

// literalElemInstr emits a literal result element.
type literalElemInstr struct {
	name       xdm.QName
	attrs      []attrTemplate
	namespaces []nsBinding
	attrSets   []xdm.QName
	body       []Instruction
	// validation carries xsl:validation and xsl:type, which a literal result
	// element may have exactly as xsl:element may.
	validation validationSpec
}

type attrTemplate struct {
	name  xdm.QName
	value *avt
}

type nsBinding struct{ prefix, uri string }

func (i *literalElemInstr) Execute(rt *runtime, out *outputBuilder) error {
	sub := out.startElement(rt.sheet.aliasFor(i.name))
	for _, ns := range i.namespaces {
		sub.open.AddNamespace(ns.prefix, ns.uri)
	}
	// Attribute sets are applied before the element's own attributes, so a
	// literal attribute overrides one inherited from a set.
	if err := applyAttributeSets(rt, i.attrSets, sub); err != nil {
		return err
	}
	for _, a := range i.attrs {
		v, err := a.value.eval(rt)
		if err != nil {
			return err
		}
		if err := sub.addAttribute(a.name, v); err != nil {
			return err
		}
	}
	if err := execSequence(i.body, rt, sub); err != nil {
		return err
	}
	// Assessed once the element is complete, since validity is a property of
	// its content as well as its name.
	return i.validation.assess(rt, sub.open)
}

// elementInstr implements xsl:element, whose name is computed at run time.
type elementInstr struct {
	name      *avt
	namespace *avt
	scope     *xdm.Node
	attrSets  []xdm.QName
	body      []Instruction
	// validation is the validation or type attribute, which asks for the
	// constructed element to be assessed against the imported schema.
	validation validationSpec
}

func (i *elementInstr) Execute(rt *runtime, out *outputBuilder) error {
	nameStr, err := i.name.eval(rt)
	if err != nil {
		return err
	}
	qn, err := i.resolveName(rt, nameStr)
	if err != nil {
		return err
	}
	sub := out.startElement(qn)
	if qn.URI != "" {
		sub.open.AddNamespace(qn.Prefix, qn.URI)
	}
	if err := applyAttributeSets(rt, i.attrSets, sub); err != nil {
		return err
	}
	if err := execSequence(i.body, rt, sub); err != nil {
		return err
	}
	// The element is complete only now, so validity is assessed here rather
	// than at construction: a content model cannot be checked against
	// content that has not been built yet.
	return i.validation.assess(rt, sub.open)
}

// resolveName turns a computed lexical name into an expanded QName, using the
// explicit namespace attribute if given and the stylesheet's namespace
// context otherwise.
func (i *elementInstr) resolveName(rt *runtime, lex string) (xdm.QName, error) {
	prefix, local := xdm.SplitQName(strings.TrimSpace(lex))
	// Both halves have to be names, not merely non-empty. A computed name is
	// written to the output as-is, so an unchecked one is a hole rather than
	// a laxity: a name holding "><script>" serialises as markup, producing
	// output that is malformed or — under the HTML method — carries an
	// element the stylesheet never wrote. XTDE0820 is the error the spec
	// gives for a name that is not a QName.
	if !xdm.IsNCName(local) || (prefix != "" && !xdm.IsNCName(prefix)) {
		return xdm.QName{}, fmt.Errorf("XTDE0820: computed name %q is not a valid QName", lex)
	}
	if i.namespace != nil {
		uri, err := i.namespace.eval(rt)
		if err != nil {
			return xdm.QName{}, err
		}
		return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
	}
	if prefix == "" {
		return xdm.QName{Local: local}, nil
	}
	uri, ok := i.scope.LookupPrefix(prefix)
	if !ok {
		return xdm.QName{}, fmt.Errorf("XTDE0830: unbound prefix %q in computed name %q", prefix, lex)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}

// attributeInstr implements xsl:attribute.
type attributeInstr struct {
	name      *avt
	namespace *avt
	sel       *xpath.Compiled
	scope     *xdm.Node
	body      []Instruction
	// validation carries [xsl:]validation and [xsl:]type, which assess the
	// constructed attribute exactly as they assess a constructed element.
	validation validationSpec
}

func (i *attributeInstr) Execute(rt *runtime, out *outputBuilder) error {
	nameStr, err := i.name.eval(rt)
	if err != nil {
		return err
	}
	el := &elementInstr{name: i.name, namespace: i.namespace, scope: i.scope}
	qn, err := el.resolveName(rt, nameStr)
	if err != nil {
		return err
	}

	var value string
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		value = stringJoin(seq, " ")
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt, sub); err != nil {
			return err
		}
		value = stringJoin(sub.sequence(), "")
	}
	// Assessment happens before the attribute joins the output, so that a
	// failure reports the attribute the stylesheet asked for rather than
	// leaving an invalid one behind on the element.
	if err := i.validation.assess(rt,
		&xdm.Node{Kind: xdm.KindAttribute, Name: qn, Value: value}); err != nil {
		return err
	}
	return out.addAttribute(qn, value)
}

// commentInstr implements xsl:comment.
type commentInstr struct{ body []Instruction }

func (i *commentInstr) Execute(rt *runtime, out *outputBuilder) error {
	sub := newOutputBuilder()
	if err := execSequence(i.body, rt, sub); err != nil {
		return err
	}
	text := stringJoin(sub.sequence(), "")
	// "--" cannot appear in a comment and "-" cannot end one; the spec says
	// to raise an error rather than mangle the content silently.
	if strings.Contains(text, "--") || strings.HasSuffix(text, "-") {
		return fmt.Errorf("XTDE0450: comment content %q contains '--' or ends with '-'", text)
	}
	out.appendNode(&xdm.Node{Kind: xdm.KindComment, Value: text})
	return nil
}

// piInstr implements xsl:processing-instruction.
type piInstr struct {
	name *avt
	body []Instruction
}

func (i *piInstr) Execute(rt *runtime, out *outputBuilder) error {
	target, err := i.name.eval(rt)
	if err != nil {
		return err
	}
	// The content was checked for "?>" but the target was not checked at all,
	// and it is written to the output verbatim. A target of "a?><evil/><?b"
	// closed the instruction and opened an element, and the result *reparsed
	// cleanly* — a silently different tree, which is worse than malformed
	// output because nothing downstream notices. "xml" in any case is
	// reserved by the XML specification.
	target = strings.TrimSpace(target)
	if !xdm.IsNCName(target) || strings.EqualFold(target, "xml") {
		return fmt.Errorf(
			"XTDE0890: %q is not a valid processing instruction target", target)
	}
	sub := newOutputBuilder()
	if err := execSequence(i.body, rt, sub); err != nil {
		return err
	}
	text := stringJoin(sub.sequence(), "")
	if strings.Contains(text, "?>") {
		return fmt.Errorf("XTDE0890: processing instruction content contains '?>'")
	}
	out.appendNode(&xdm.Node{
		Kind:  xdm.KindPI,
		Name:  xdm.QName{Local: target},
		Value: text,
	})
	return nil
}

// ifInstr implements xsl:if.
type ifInstr struct {
	test *xpath.Compiled
	body []Instruction
}

func (i *ifInstr) Execute(rt *runtime, out *outputBuilder) error {
	ok, err := i.test.EvalBool(rt.ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return execSequence(i.body, rt, out)
}

// chooseInstr implements xsl:choose.
type chooseInstr struct {
	whens     []chooseBranch
	otherwise []Instruction
}

type chooseBranch struct {
	test *xpath.Compiled
	body []Instruction
}

func (i *chooseInstr) Execute(rt *runtime, out *outputBuilder) error {
	for _, w := range i.whens {
		ok, err := w.test.EvalBool(rt.ctx)
		if err != nil {
			return err
		}
		if ok {
			return execSequence(w.body, rt, out)
		}
	}
	if i.otherwise != nil {
		return execSequence(i.otherwise, rt, out)
	}
	return nil
}

// forEachInstr implements xsl:for-each.
type forEachInstr struct {
	sel   *xpath.Compiled
	sorts []*sortKey
	body  []Instruction
}

func (i *forEachInstr) Execute(rt *runtime, out *outputBuilder) error {
	seq, err := i.sel.Eval(rt.ctx)
	if err != nil {
		return err
	}
	if len(i.sorts) > 0 {
		seq, err = applySorts(rt, seq, i.sorts)
		if err != nil {
			return err
		}
	}
	size := len(seq)
	for idx, it := range seq {
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		sub := rt.withCurrent(it, idx+1, size)
		if err := execSequence(i.body, sub, out); err != nil {
			return err
		}
	}
	return nil
}

// messageInstr implements xsl:message.
type messageInstr struct {
	sel       *xpath.Compiled
	terminate *avt
	body      []Instruction
}

func (i *messageInstr) Execute(rt *runtime, out *outputBuilder) error {
	var text string
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		text = stringJoin(seq, " ")
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt, sub); err != nil {
			return err
		}
		text = stringJoin(sub.sequence(), "")
	}
	// Messages are collected rather than printed: a library writing to stderr
	// is a nuisance, and the caller may want them alongside the result.
	*rt.messages = append(*rt.messages, text)

	if i.terminate != nil {
		v, err := i.terminate.eval(rt)
		if err != nil {
			return err
		}
		if v == "yes" || v == "true" || v == "1" {
			return fmt.Errorf("XTMM9000: %s", text)
		}
	}
	return nil
}

// --- Sorting ----------------------------------------------------------------

// sortKey is a compiled xsl:sort.
type sortKey struct {
	sel       *xpath.Compiled
	order     string // "ascending" or "descending"
	dataType  string // "text" or "number"
	caseOrder string
	// coll orders text by the conventions of a language when xsl:sort/@lang
	// names one; nil means codepoint order.
	coll *collator
	// collAVT is @collation before evaluation, for the case where it is an
	// attribute value template naming a collation the stylesheet computes.
	collAVT *avt
	// strColl is the collation named by @collation, used when no @lang is
	// given. Accepting the attribute and then sorting by codepoint anyway is
	// exactly the silent-wrong-answer this engine exists to avoid.
	strColl xpath.Collation
}

// applySorts orders a sequence by the given sort keys.
//
// Sort keys are computed once per item up front rather than inside the
// comparison function. A comparison-time evaluation would re-run the key
// expression O(n log n) times instead of n, and those expressions are often
// paths into the document.
func applySorts(rt *runtime, seq xdm.Sequence, sorts []*sortKey) (xdm.Sequence, error) {
	n := len(seq)
	if n < 2 {
		return seq, nil
	}

	type entry struct {
		item xdm.Item
		keys []sortValue
		idx  int
	}
	entries := make([]entry, n)

	// A collation named by an attribute value template is resolved once for
	// the whole sort rather than per item: it cannot vary between items, and
	// resolving it per comparison would parse the same URI n log n times.
	resolved := make([]xpath.Collation, len(sorts))
	for k, s := range sorts {
		resolved[k] = s.strColl
		if s.strColl == nil && s.collAVT != nil {
			uri, err := s.collAVT.eval(rt)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(uri) != "" {
				c, err := xpath.ResolveCollation(uri)
				if err != nil {
					return nil, fmt.Errorf("xsl:sort/@collation: %w", err)
				}
				resolved[k] = c
			}
		}
	}

	for i, it := range seq {
		e := entry{item: it, idx: i, keys: make([]sortValue, len(sorts))}
		sub := rt.withFocus(it, i+1, n)
		for k, s := range sorts {
			v, err := s.sel.Eval(sub.ctx)
			if err != nil {
				return nil, err
			}
			e.keys[k] = makeSortValue(v, s, resolved[k])
		}
		entries[i] = e
	}

	var sortErr error
	sort.SliceStable(entries, func(a, b int) bool {
		for k, s := range sorts {
			cmp := compareSortValues(entries[a].keys[k], entries[b].keys[k])
			if cmp == 0 {
				continue
			}
			if s.order == "descending" {
				cmp = -cmp
			}
			return cmp < 0
		}
		// Equal on every key: preserve the original order, which is what
		// makes the sort stable and the output reproducible.
		return entries[a].idx < entries[b].idx
	})
	if sortErr != nil {
		return nil, sortErr
	}

	out := make(xdm.Sequence, n)
	for i, e := range entries {
		out[i] = e.item
	}
	return out, nil
}

// sortValue is a precomputed sort key.
type sortValue struct {
	numeric bool
	num     float64
	str     string
	// fold is the case-insensitive form, compared first when case-order is
	// set so that "a" and "A" sort adjacently rather than in separate blocks.
	fold string
	// upperFirst selects which of an otherwise-equal pair wins.
	upperFirst bool
	caseOrder  bool
	// strColl is the collation from xsl:sort/@collation, if one was named.
	strColl xpath.Collation
	// collKey is the locale-aware sort key for str, precomputed because a
	// collator is stateful and cannot be shared across the comparisons that
	// sort.Slice runs. Comparing two of these byte slices is equivalent to
	// asking the collator to compare the strings, and is safe concurrently.
	collKey []byte
	// empty marks an absent key, which sorts before everything else.
	empty bool
}

func makeSortValue(seq xdm.Sequence, s *sortKey, coll xpath.Collation) sortValue {
	if len(seq) == 0 {
		return sortValue{empty: true}
	}
	text := stringJoin(seq[:1], "")
	if s.dataType == "number" {
		a := xdm.NewUntypedAtomic(text)
		conv, err := xpath.CastAtomic(a, xdm.TypeDouble)
		if err != nil {
			// A non-numeric value sorts as NaN, which the comparison places
			// before all numbers rather than erroring the whole transform.
			return sortValue{numeric: true, num: nan()}
		}
		return sortValue{numeric: true, num: conv.Float64()}
	}

	v := sortValue{str: text, strColl: coll}
	if s.coll != nil {
		v.collKey = s.coll.key(text)
	}
	// case-order only has an effect on text sorts; without it a plain
	// codepoint comparison puts every uppercase letter before every
	// lowercase one, which is rarely what an author wants.
	switch s.caseOrder {
	case "upper-first":
		v.caseOrder, v.upperFirst = true, true
		v.fold = strings.ToLower(text)
	case "lower-first":
		v.caseOrder, v.upperFirst = true, false
		v.fold = strings.ToLower(text)
	}
	return v
}

func compareSortValues(a, b sortValue) int {
	switch {
	case a.empty && b.empty:
		return 0
	case a.empty:
		return -1
	case b.empty:
		return 1
	}
	// A collation named by @collation orders the text its own way. This runs
	// before the case-order folding below, because a collation that already
	// ignores case would fight it.
	if a.strColl != nil && b.strColl != nil {
		// Values equal under the collation compare equal, full stop. The sort
		// is stable, so they keep document order — which is what the spec
		// requires and what Saxon produces. Falling back to codepoint order
		// here would reorder "A" and "a" against each other even though the
		// collation says they are the same, giving A,a,B,b where the answer
		// is A,a,b,B.
		return a.strColl.Compare(a.str, b.str)
	}

	// A language-sensitive collation replaces codepoint order entirely: it
	// already places accented and cased letters where that language expects,
	// so applying the case-order folding on top would fight it.
	if a.collKey != nil && b.collKey != nil {
		if c := bytes.Compare(a.collKey, b.collKey); c != 0 {
			return c
		}
		// Equal under the collation but not identical: fall through to
		// codepoint order so the sort stays deterministic.
		return strings.Compare(a.str, b.str)
	}

	// With case-order set, values are ordered case-insensitively first and the
	// case distinction only breaks ties.
	if a.caseOrder && b.caseOrder {
		if c := strings.Compare(a.fold, b.fold); c != 0 {
			return c
		}
		c := strings.Compare(a.str, b.str)
		if c == 0 {
			return 0
		}
		// Codepoint order puts uppercase first, so "upper-first" keeps that
		// sign and "lower-first" inverts it.
		if a.upperFirst {
			return c
		}
		return -c
	}

	if a.numeric && b.numeric {
		an, bn := a.num, b.num
		switch {
		case an != an && bn != bn:
			return 0 // both NaN
		case an != an:
			return -1
		case bn != bn:
			return 1
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	}
	return strings.Compare(a.str, b.str)
}

func nan() float64 {
	var z float64
	return z / z
}
