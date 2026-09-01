package xquery

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
	"github.com/knroy/go-xml/xpath"
)

// evalContext is what a constructor needs while it runs.
type evalContext struct {
	// xp is the context expressions are evaluated in.
	xp *xpath.Context
	// sc is the static context the query was parsed under, which carries the
	// construction mode a copy is made with.
	sc *staticContext
}

// builderRef is the builder a constructor appends to.
//
// It exists so that the nested builder StartElement returns can be threaded
// through the node tree without every eval method having to return one.
type builderRef struct{ b *xdmbuild.Builder }

// policy names the structural faults of content construction the way XQuery
// names them.
//
// The two languages sharing xdmbuild differ in five codes and one behaviour,
// and this is XQuery's half of that. The behaviour is the duplicate
// attribute: XSLT discards the earlier one silently, and XQuery raises
// XQDY0025, so this returns an error where the XSLT policy returns nil.
type policy struct{ sc *staticContext }

func (p policy) Err(f xdmbuild.Fault, detail string) error {
	switch f {
	case xdmbuild.FaultDuplicateAttribute:
		return fmt.Errorf("XQDY0025: %s", detail)
	case xdmbuild.FaultAttrAfterChild:
		return fmt.Errorf("XQTY0024: %s", detail)
	case xdmbuild.FaultAttrOnDocument:
		return fmt.Errorf("XPTY0004: %s", detail)
	case xdmbuild.FaultConflictingPrefix, xdmbuild.FaultDefaultNSOnNoNS:
		return fmt.Errorf("XQDY0102: %s", detail)
	case xdmbuild.FaultFunctionItem:
		return fmt.Errorf("XQTY0105: %s", detail)
	}
	return fmt.Errorf("XQTY0024: %s", detail)
}

// InheritNamespaces and PreserveNamespaces are the two halves of
// copy-namespaces, which vary independently and which "declare
// copy-namespaces" sets. A nil static context is the specification's default,
// which is preserve and inherit.
func (p policy) InheritNamespaces() bool  { return p.sc == nil || p.sc.inheritNS }
func (p policy) PreserveNamespaces() bool { return p.sc == nil || p.sc.preserveNS }

// PreserveTypes follows the construction mode, which "declare construction"
// sets and which defaults to preserve.
func (p policy) PreserveTypes() bool {
	return p.sc == nil || p.sc.construction == PreserveTypes
}

func (n *literalText) eval(out *builderRef, ctx *evalContext) error {
	out.b.AppendText(n.text)
	return nil
}

// eval for an enclosed expression normalises its value into content, which is
// §3.9.1.3's rule and the same one XSLT applies: a node is copied in, an
// atomic value becomes text, and a run of adjacent atomic values is separated
// by single spaces. The builder does the separating, because only it knows
// what was appended last.
func (n *enclosed) eval(out *builderRef, ctx *evalContext) error {
	if n.items != nil {
		for _, it := range n.items {
			if err := it.eval(out, ctx); err != nil {
				return err
			}
		}
		return nil
	}
	if n.expr == nil {
		return nil
	}
	seq, err := n.expr.compiled.Eval(ctx.xp)
	if err != nil {
		return err
	}
	return appendSequence(out, seq)
}

// sequence evaluates an enclosed expression to a flat sequence, for the
// callers that need its value rather than its contribution to a tree.
func (n *enclosed) sequence(ctx *evalContext) (xdm.Sequence, error) {
	if n.expr != nil {
		return n.expr.compiled.Eval(ctx.xp)
	}
	if n.items == nil {
		return nil, nil
	}
	// A node that has a value of its own answers with it directly. Routing it
	// through a builder would be lossy in exactly the way that matters here:
	// the builder's job is to turn a sequence into content, so it converts an
	// atomic value to text and merges it with its neighbours. "{ switch (1)
	// case 1 return 2 default return 3 }" is the xs:integer 2, not the text
	// node "2", and "instance of xs:integer" in the suite asks the question.
	if len(n.items) == 1 {
		if v, ok := n.items[0].(valueNode); ok {
			return v.sequence(ctx)
		}
	}
	inner := xdmbuild.New(policy{sc: ctx.sc})
	ref := &builderRef{b: inner}
	for _, it := range n.items {
		if err := it.eval(ref, ctx); err != nil {
			return nil, err
		}
	}
	return inner.Sequence(), nil
}

// valueNode is a node whose value is a sequence rather than a contribution to
// a tree.
//
// The XQuery-only expression forms are all of this kind: a switch returns
// whatever its chosen clause returns, which may be an atomic value, a
// function item or a map — none of which survives a round trip through the
// content builder. A constructor is deliberately not one of these: its value
// really is the node it builds, and the builder is how it is built.
type valueNode interface {
	node
	sequence(ctx *evalContext) (xdm.Sequence, error)
}

func appendSequence(out *builderRef, seq xdm.Sequence) error {
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			out.b.AppendNode(v)
		case *xdm.Atomic:
			out.b.AppendValue(v)
		default:
			if err := out.b.AppendOpaque(it); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *element) eval(out *builderRef, ctx *evalContext) error {
	name := n.name
	if n.nameExpr != nil {
		var err error
		name, err = evalNodeName(n.nameExpr, ctx, true)
		if err != nil {
			return err
		}
	}
	sub := &builderRef{b: out.b.StartElement(name)}
	if el := sub.b.Open(); el != nil && n.baseURI != "" {
		el.BaseURI = n.baseURI
	}
	// Namespace declarations are applied before anything else, so that they
	// are in scope for the attributes and the content.
	for _, ns := range n.namespaces {
		if err := sub.b.AddNamespace(ns.prefix, ns.uri); err != nil {
			return err
		}
		sub.b.NoteDeclared(ns.prefix, ns.uri)
	}
	for _, a := range n.attrs {
		if err := a.eval(sub, ctx); err != nil {
			return err
		}
	}
	for _, c := range n.content {
		if err := c.eval(sub, ctx); err != nil {
			return err
		}
	}
	return nil
}

// eval for an attribute joins its parts into one string.
//
// §3.9.1.1 is precise about the joining: each enclosed expression is atomised,
// its values are cast to strings and separated by single spaces, and the
// results of adjacent parts are concatenated with no separator at all. So
// id="a{1,2}b" is "a1 2b" rather than "a 1 2 b".
func (a *attribute) eval(out *builderRef, ctx *evalContext) error {
	name := a.name
	if a.nameExpr != nil {
		var err error
		name, err = evalNodeName(a.nameExpr, ctx, false)
		if err != nil {
			return err
		}
	}
	var sb strings.Builder
	for _, part := range a.value {
		switch v := part.(type) {
		case *literalText:
			sb.WriteString(v.text)
		case *enclosed:
			if v.expr == nil {
				continue
			}
			seq, err := v.expr.compiled.Eval(ctx.xp)
			if err != nil {
				return err
			}
			s, err := joinAtomized(seq)
			if err != nil {
				return err
			}
			sb.WriteString(s)
		}
	}
	return out.b.AddAttribute(name, sb.String())
}

// joinAtomized atomises a sequence and joins it with single spaces, which is
// what an attribute value and a computed text constructor both want.
func joinAtomized(seq xdm.Sequence) (string, error) {
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(atoms))
	for _, it := range atoms {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			return "", fmt.Errorf(
				"XPTY0004: %s cannot be used as a string", it.TypeName())
		}
		parts = append(parts, a.String())
	}
	return strings.Join(parts, " "), nil
}

func (n *comment) eval(out *builderRef, ctx *evalContext) error {
	text, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	if strings.Contains(text, "--") || strings.HasSuffix(text, "-") {
		return fmt.Errorf("XQDY0072: a comment may not contain %q "+
			"or end with %q", "--", "-")
	}
	out.b.AppendNode(&xdm.Node{Kind: xdm.KindComment, Value: text})
	return nil
}

func (n *pi) eval(out *builderRef, ctx *evalContext) error {
	target := n.target
	if n.targetExpr != nil {
		seq, err := n.targetExpr.compiled.Eval(ctx.xp)
		if err != nil {
			return err
		}
		s, err := joinAtomized(seq)
		if err != nil {
			return err
		}
		target = s
		if !xdm.IsNCName(target) {
			return fmt.Errorf(
				"XQDY0041: %q is not a valid processing-instruction target",
				target)
		}
	}
	text, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	if strings.Contains(text, "?>") {
		return fmt.Errorf(
			"XQDY0026: a processing instruction may not contain %q", "?>")
	}
	out.b.AppendNode(&xdm.Node{Kind: xdm.KindPI,
		Name: xdm.QName{Local: target}, Value: text})
	return nil
}

func (n *textNode) eval(out *builderRef, ctx *evalContext) error {
	text, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	// A text constructor whose content is empty produces no node at all,
	// rather than an empty one the data model does not admit.
	if text == "" {
		return nil
	}
	out.b.AppendNode(&xdm.Node{Kind: xdm.KindText, Value: text})
	return nil
}

func (n *document) eval(out *builderRef, ctx *evalContext) error {
	inner := xdmbuild.New(policy{sc: ctx.sc})
	sub := &builderRef{b: inner}
	for _, c := range n.content {
		if err := c.eval(sub, ctx); err != nil {
			return err
		}
	}
	doc, err := inner.ToDocument()
	if err != nil {
		return err
	}
	out.b.AppendNode(doc)
	return nil
}

// contentString evaluates content that becomes a string rather than nodes,
// which is what a comment, a processing instruction and a text constructor
// each hold.
func contentString(content []node, ctx *evalContext) (string, error) {
	// The content is one sequence, and §3.9.3 joins it once: the values of
	// "text {1,2}" are separated by a single space, and so are the values of
	// "text {1}, {2}". Joining each item on its own would lose the separator
	// between them, so the whole content is flattened first.
	var seq xdm.Sequence
	for _, c := range content {
		switch v := c.(type) {
		case *literalText:
			seq = append(seq, xdm.NewString(v.text))
		case *enclosed:
			s, err := v.sequence(ctx)
			if err != nil {
				return "", err
			}
			seq = append(seq, s...)
		default:
			return "", fmt.Errorf(
				"XPTY0004: this content may not contain a constructed node")
		}
	}
	return joinAtomized(seq)
}

// evalNodeName computes the name of a computed constructor.
//
// §3.9.3.1: the value is atomised and must be a QName, a string or an
// untypedAtomic. A string is resolved against the statically known
// namespaces, taking the default element namespace when it has no prefix and
// the constructor makes an element. Failure here is dynamic — XQDY0074 —
// where the same failure in a direct constructor is static, because a direct
// constructor's name is written in the query and a computed one's is not.
func evalNodeName(e *compiledExpr, ctx *evalContext, isElement bool) (xdm.QName, error) {
	seq, err := e.compiled.Eval(ctx.xp)
	if err != nil {
		return xdm.QName{}, err
	}
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return xdm.QName{}, err
	}
	if len(atoms) != 1 {
		return xdm.QName{}, fmt.Errorf(
			"XPTY0004: the name of a constructed node must be a single value")
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return xdm.QName{}, fmt.Errorf(
			"XPTY0004: the name of a constructed node must be a QName or a string")
	}
	if a.Type == xdm.TypeQName {
		if q := a.QName(); q != nil {
			return *q, nil
		}
	}
	switch a.Type {
	case xdm.TypeString, xdm.TypeUntypedAtomic:
	default:
		return xdm.QName{}, fmt.Errorf(
			"XPTY0004: the name of a constructed node must be a QName or a string")
	}
	lex := a.String()
	prefix, local := "", lex
	if i := strings.IndexByte(lex, ':'); i >= 0 {
		prefix, local = lex[:i], lex[i+1:]
	}
	if !xdm.IsNCName(local) || (prefix != "" && !xdm.IsNCName(prefix)) {
		return xdm.QName{}, fmt.Errorf("XQDY0074: %q is not a lexical QName", lex)
	}
	if isElement {
		q, err := ctx.sc.resolveElementName(prefix, local)
		if err != nil {
			return xdm.QName{}, fmt.Errorf("XQDY0074: %v", err)
		}
		return q, nil
	}
	q, err := ctx.sc.resolveAttributeName(prefix, local)
	if err != nil {
		return xdm.QName{}, fmt.Errorf("XQDY0074: %v", err)
	}
	return q, nil
}
