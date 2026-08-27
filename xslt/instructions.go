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
type varInstr struct {
	v *Variable
	// unused records that no instruction after this one in the same sequence
	// constructor mentions the name. Section 5.2 leaves such a variable
	// unevaluated, and the difference is observable: evaluating it can raise
	// an error the stylesheet never asked for. See compileNodes.
	unused bool
}

func (i *varInstr) Execute(rt *runtime, out *outputBuilder) error { return nil }

// valueOfInstr implements xsl:value-of.
type valueOfInstr struct {
	sel  *xpath.Compiled
	body []Instruction
	// separator is @separator as an attribute value template, nil when the
	// attribute is absent.
	separator    *avt
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
		if err := execSequence(i.body, rt.temporaryOutputBefore30(), sub); err != nil {
			return err
		}
		seq = sub.sequence()
	}

	// 3.8: in backwards-compatible mode xsl:value-of takes the string value
	// of the *first* item of the selected sequence and discards the rest,
	// which is the whole of what XSLT 1.0 could express -- select yielded a
	// node-set and string() of a node-set is the first node's. It applies only
	// to @select: content built by a sequence constructor is 2.0-only syntax
	// and has no 1.0 reading to be compatible with. backwards-006 pairs the
	// two versions in one stylesheet and requires exactly that split.
	// An explicit @separator disables it. The attribute is 2.0-only syntax --
	// 1.0 had no way to write it -- so a stylesheet that writes one is asking
	// for the whole sequence joined however its version reads, and
	// backwards-009 pairs a 1.0 and a 2.0 xsl:value-of with the same separator
	// and requires the same answer from both.
	if i.sel != nil && i.sel.CompatMode() && !i.hasSeparator && len(seq) > 1 {
		seq = seq[:1]
	}

	// XSLT 1.0 took only the first item; 2.0 joins the whole sequence, with a
	// space separator when select is used and none otherwise. Defaulting to
	// the 2.0 behaviour matters for rule sets that rely on it, and the
	// separator attribute makes the choice explicit either way.
	// Section 11.4 gives xsl:value-of the same two defaults as xsl:attribute:
	// a single space when the content comes from @select, and a zero-length
	// string when it comes from the sequence constructor.
	sep := ""
	if i.hasSeparator {
		v, err := i.separator.eval(rt)
		if err != nil {
			return err
		}
		sep = v
	} else if i.sel != nil {
		sep = " "
	}
	// A map, an array of non-atomizable items or a function item has no typed
	// value, so building simple content out of one is FOTY0013 rather than
	// the empty string the joiner would otherwise produce.
	if err := checkAtomizable(seq); err != nil {
		return err
	}
	out.appendText(constructedText(seq, sep))
	return nil
}

// sequenceInstr implements xsl:sequence, which adds items to the result
// without converting them to text.
// sequenceInstr implements xsl:sequence. Exactly one of sel and body is set;
// with neither, the instruction yields the empty sequence.
type sequenceInstr struct {
	sel *xpath.Compiled
	// body is the XSLT 3.0 content form. Its items are written straight to
	// out rather than into a sub-builder: xsl:sequence returns the items the
	// constructor produced, where a content-valued xsl:variable would wrap
	// them in a document node.
	body []Instruction
}

func (i *sequenceInstr) Execute(rt *runtime, out *outputBuilder) error {
	if i.sel == nil {
		return execSequence(i.body, rt, out)
	}
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
		default:
			// A function item, a map or an array. xsl:sequence returns
			// whatever its select produced, so all three reach here.
			if err := appendOpaqueItem(out, it); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyOfInstr implements xsl:copy-of, which deep-copies nodes.
type copyOfInstr struct {
	sel *xpath.Compiled
	// noNamespaces records copy-namespaces="no".
	noNamespaces bool
	// copyAccumulators records copy-accumulators="yes", which carries the
	// accumulator values of the copied nodes onto the copies. See
	// noteCopiedAccumulators in accumulator.go.
	copyAccumulators bool
	validation       validationSpec
	// baseURI is the base URI of the xsl:copy-of instruction itself. Section
	// 11.9.2 resolves a copied element's relative xml:base against it while
	// the copy is still parentless; once the copy is attached to a parent,
	// rebase takes over and the parent's base wins.
	baseURI string
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
				// The copy of a document node is a document node (11.9.1).
				// Flattening happens only where the copy is attached as the
				// content of an element (5.7.1), which appendNode decides;
				// destinations that keep a bare sequence — a variable
				// declared as="document-node()*", a function body — must see
				// the wrapper itself.
				c := copyDocumentNode(v)
				if i.noNamespaces {
					// §11.9.2's namespace rules are about the elements being
					// copied; a document node is only the wrapper they arrive
					// in, and it carries no declarations of its own. Leaving
					// this branch out let copy-of of a temporary tree keep the
					// declarations copy-namespaces="no" had asked to be
					// dropped -- copy-0614 and -0615 graft a whole $inner and
					// expect the inner element's own xmlns:s to go with them.
					for _, ch := range c.Children {
						if ch.Kind == xdm.KindElement {
							stripNamespaces(ch)
						}
					}
				}
				// The document node itself is assessed, not its children one
				// at a time. Section 11.9.2 validates the copy, and the copy
				// of a document node is a document node -- which is what
				// brings the DOCUMENT-LEVEL constraints into play. Assessing
				// each child instead reported the ID/IDREF failures with the
				// element codes XTTE1510/XTTE1515, where XTTE1555 is the code
				// for "document-level constraints are not satisfied".
				if err := i.validation.assess(rt, c); err != nil {
					return err
				}
				out.appendNode(c)
				continue
			}
			if v.Kind == xdm.KindAttribute {
				// XTTE0950, second sentence: copying an attribute with
				// namespace-sensitive content under validation="preserve" is
				// a type error "unless the parent element is also copied".
				// An attribute selected by xsl:copy-of arrives on its own --
				// its parent, if it has one, is not what this instruction is
				// copying -- so the exemption never applies on this path.
				// The prefix in the copied QName would keep pointing at a
				// binding the new parent element need not declare.
				if i.validation.mode == validatePreserve &&
					isNamespaceSensitiveType(v.TypeAnnotation) {
					return fmt.Errorf(
						"XTTE0950: xsl:copy-of with validation=\"preserve\" "+
							"cannot copy attribute %s on its own, because its "+
							"content is namespace-sensitive",
						v.Name.Lexical())
				}
				// Assessed on a copy, never on v. Validation strips type
				// annotations in place, and the default mode is "strip", so
				// assessing the source attribute erased the annotation from
				// the *input document* — which then answered differently for
				// every later expression that read it.
				//
				// Every other branch of this switch already assesses a copy;
				// this one did not, which is why copying attributes made
				// fn:idref appear one-directional: the first copy stripped
				// the annotations the later lookups needed.
				a := &xdm.Node{
					Kind:           xdm.KindAttribute,
					Name:           v.Name,
					Value:          v.Value,
					TypeAnnotation: v.TypeAnnotation,
				}
				if err := i.validation.assess(rt, a); err != nil {
					return err
				}
				if err := out.addAttributeTyped(a.Name, a.Value, a.TypeAnnotation); err != nil {
					return err
				}
				continue
			}
			if v.Kind == xdm.KindNamespace {
				// A namespace node joins the element's bindings rather than
				// its children. Appending it as a child put it nowhere the
				// namespace axis or the serialiser would ever look.
				if err := out.addNamespaceNode(v.Name.Local, v.Value); err != nil {
					return err
				}
				continue
			}
			// XTTE0950: copying a node with namespace-sensitive content
			// under copy-namespaces="no" and validation="preserve". The two
			// together ask for the QName's prefix to be kept and its
			// declaration to be thrown away, which leaves a value whose
			// validity depended on a binding that is no longer there.
			if i.noNamespaces && i.validation.mode == validatePreserve &&
				hasNamespaceSensitiveContent(v) {
				return fmt.Errorf(
					"XTTE0950: xsl:copy-of with copy-namespaces=\"no\" and "+
						"validation=\"preserve\" cannot copy %s, whose "+
						"content is namespace-sensitive", describeNode(v))
			}
			c := deepCopy(v)
			if i.copyAccumulators {
				rt.noteCopiedAccumulators(v, c)
			}
			if out.open == nil {
				// A parentless copy keeps its own xml:base, resolved against
				// the instruction's base URI rather than against the document
				// it came from. appendNode only rebases when there is a
				// parent to inherit from, so a copy that lands directly in a
				// variable would otherwise keep the source's resolved base.
				rebaseDetached(c, i.baseURI)
			}
			if v.Kind == xdm.KindElement {
				if i.noNamespaces {
					stripNamespaces(c)
				} else {
					// The copy carries only the declarations written on the
					// source element, so the ones it inherits from ancestors
					// it is being lifted away from are added here. Without
					// them a copied subtree loses the binding its own prefix
					// depends on.
					inheritNamespaces(c, v)
				}
			}

			// The copy is assessed rather than the original: validation may
			// annotate, and annotating the source document would leak a
			// property of this instruction into the tree everything else
			// still reads.
			if err := i.validation.assess(rt, c); err != nil {
				return err
			}
			out.appendNode(c)
			// After the copy has a parent, because the repair §5.8.3 needs
			// depends on what the destination declares: an element in no
			// namespace landing under one that declares a default has to
			// undeclare it, or the copy silently changes namespace.
			// copy-1220 grafts elements in no namespace into a
			// <doc xmlns="http://www.out.com/">.
			if c.Kind == xdm.KindElement {
				fixupNamespaces(c)
			}
		case *xdm.Atomic:
			out.appendValue(v)
		}
	}
	return nil
}

// deepCopy clones a subtree, detached from its original parent.
//
// The type annotation travels with the copy. validation="preserve" is defined
// as keeping the types the source carried, and dropping them here left a
// preserved copy untyped, so "$v instance of element(e, xs:anyURI)" answered
// false for a node that had just been copied from a validated document.
// Stripping is done by the validation spec, which is the thing that knows
// whether the instruction asked for it.
func deepCopy(n *xdm.Node) *xdm.Node {
	c := &xdm.Node{
		Kind:           n.Kind,
		Name:           n.Name,
		Value:          n.Value,
		BaseURI:        n.BaseURI,
		TypeAnnotation: n.TypeAnnotation,
		IsID:           n.IsID,
		IsIDREFS:       n.IsIDREFS,
		// dm:nilled travels with the annotation on a COPY. A copy of an
		// assessed element is an element that was assessed: validation-1202
		// copies a nilled element with validation="preserve" and requires
		// nilled() to stay true, and fn:copy-of and fn:snapshot in
		// validation-1203 require the same. Only a NEWLY CONSTRUCTED element
		// starts unnilled, which is xsl:copy's case in validation-1204 —
		// there the annotation is preserved but the element itself is new.
		IsNilled: n.IsNilled,
	}
	for _, ns := range n.Namespaces {
		c.AddNamespace(ns.Name.Local, ns.Value)
	}
	for _, a := range n.Attrs {
		c.AddAttr(&xdm.Node{Kind: xdm.KindAttribute, Name: a.Name,
			Value: a.Value, TypeAnnotation: a.TypeAnnotation,
			IsID: a.IsID, IsIDREFS: a.IsIDREFS})
	}
	for _, ch := range n.Children {
		c.AppendChild(deepCopy(ch))
	}
	return c
}

// isNamespaceSensitiveType reports whether a type annotation names a type
// whose values carry a namespace prefix that must stay bound.
//
// XTTE0950 defines it as "its typed value contains an item of type xs:QName or
// xs:NOTATION or a type derived therefrom". Annotations are now qualified —
// a built-in keys under its bare local name, a schema type under {uri}local —
// so "QName" and "NOTATION" name the built-ins exactly, and a schema type
// that merely shares one of those local names no longer answers true. That
// distinction was previously unavailable, and it matters: schema-for-xslt20.xsd
// declares an xsl:QName which is a restriction of xs:Name and carries no
// namespace-sensitive value at all.
//
// "or a type derived therefrom" is answered by walking the derivation chain
// the schema recorded, which the qualified keys make exact end to end. The
// walk is bounded like every other in the engine, so a schema whose
// derivations formed a cycle cannot spin here.
func isNamespaceSensitiveType(ann string) bool {
	for i := 0; i < 32 && ann != ""; i++ {
		if ann == "QName" || ann == "NOTATION" {
			return true
		}
		next := xdm.DerivedBase(ann)
		if next == ann {
			return false
		}
		ann = next
	}
	return false
}

// hasNamespaceSensitiveContent reports whether a node's typed value, or that
// of anything it contains, is namespace-sensitive.
//
// The whole subtree is walked because the copy takes the whole subtree with
// it: a QName-valued attribute three elements down loses its binding exactly
// as one on the copied element itself would.
func hasNamespaceSensitiveContent(n *xdm.Node) bool {
	if n == nil {
		return false
	}
	if isNamespaceSensitiveType(n.TypeAnnotation) {
		return true
	}
	for _, a := range n.Attrs {
		if isNamespaceSensitiveType(a.TypeAnnotation) {
			return true
		}
	}
	for _, c := range n.Children {
		if hasNamespaceSensitiveContent(c) {
			return true
		}
	}
	return false
}

// copyDocumentNode deep-copies a document node onto a tree of its own.
//
// deepCopy alone produces a parentless node with no tree behind it, and two
// document properties live on the tree rather than on the node: the DOCTYPE
// text, from which fn:unparsed-entity-uri and fn:unparsed-entity-public-id
// read their declarations, and the tree identity that fn:generate-id and
// node comparison need. Section 11.9.1 says the copy of a document node is a
// document node; a document node with no unparsed-entity table is not the
// same document node, so the DOCTYPE travels with it. The base URI does too,
// for the same reason the xsl:copy branch carries it: a relative reference in
// the copy resolves against the document it came from.
func copyDocumentNode(n *xdm.Node) *xdm.Node {
	tree := xdm.NewTree()
	tree.Root.BaseURI = n.BaseURI
	tree.CopyDTDFrom(n.Tree())
	tree.Root.TypeAnnotation = n.TypeAnnotation
	for _, ch := range n.Children {
		tree.Root.AppendChild(deepCopy(ch))
	}
	tree.Finalize()
	return tree.Root
}

// inheritNamespaces gives a detached copy the bindings it used to inherit.
//
// Declarations the element already carries are left alone: they are the
// nearest ones, and an inherited binding for the same prefix is masked.
func inheritNamespaces(dst, src *xdm.Node) {
	have := map[string]bool{}
	for _, ns := range dst.Namespaces {
		have[ns.Name.Local] = true
	}
	scope := src.InScopeNamespaces()
	prefixes := make([]string, 0, len(scope))
	for p := range scope {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		if p == "xml" || have[p] {
			continue
		}
		dst.AddNamespace(p, scope[p])
	}
}

// stripNamespaces removes the namespace nodes from a copied subtree, which is
// what copy-namespaces="no" asks for, and then puts back the ones the names
// still depend on.
//
// The declarations going is the point of the attribute; the ones a name needs
// coming back is §5.8.3 namespace fixup, which the constructed tree has to
// satisfy however it was built. Leaving them out was invisible in the output
// -- the serialiser writes what a name needs whether the tree says so or not
// -- but the namespace axis reads the tree, so in-scope-prefixes() on a copy
// answered with the bindings of wherever the copy landed. copy-0623 and
// copy-0627 ask exactly that question.
func stripNamespaces(n *xdm.Node) {
	n.Namespaces = nil
	fixupNamespaces(n)
	for _, c := range n.Children {
		if c.Kind == xdm.KindElement {
			stripNamespaces(c)
		}
	}
}

// fixupNamespaces declares on el whatever its own name and its attribute names
// need and cannot already see, which is the part of §5.8.3 that applies to an
// element whose declarations have just been taken away.
func fixupNamespaces(el *xdm.Node) {
	scope := el.InScopeNamespaces()
	need := func(prefix, uri string) {
		if uri == "" || uri == xdm.NSXML || scope[prefix] == uri {
			return
		}
		el.AddNamespace(prefix, uri)
		scope[prefix] = uri
	}
	if el.Name.URI == "" && el.Name.Prefix == "" && scope[""] != "" {
		// An unprefixed name in no namespace UNDECLARES the default
		// namespace when one is in scope: without the undeclaration the name
		// would read as being in whatever the parent declares. This is the
		// same repair xsl:element makes for a computed name, reached here by
		// a copy that lands under a parent declaring a default -- copy-1220
		// grafts elements in no namespace into a <doc xmlns="...">.
		el.AddNamespace("", "")
		scope[""] = ""
	}
	need(el.Name.Prefix, el.Name.URI)
	for _, a := range el.Attrs {
		// An attribute in a namespace must be prefixed: the default
		// declaration never applies to an attribute name, so a binding for
		// the empty prefix would not satisfy the constraint.
		if a.Name.URI != "" && a.Name.Prefix != "" {
			need(a.Name.Prefix, a.Name.URI)
		}
	}
}

// copyNamespacesTo adds every namespace node in scope on src to the element
// sub is building.
func copyNamespacesTo(sub *outputBuilder, src *xdm.Node) {
	scope := src.InScopeNamespaces()
	prefixes := make([]string, 0, len(scope))
	for p := range scope {
		prefixes = append(prefixes, p)
	}
	// Sorted so that the declarations come out in a stable order; the XDM
	// leaves the order of the namespace axis implementation-dependent, but an
	// order that varies between runs is not an order at all.
	sort.Strings(prefixes)
	for _, p := range prefixes {
		if p == "xml" {
			// The xml prefix is bound implicitly everywhere, so declaring it
			// would be redundant and is in fact forbidden in the output.
			continue
		}
		_ = sub.addNamespaceNode(p, scope[p])
	}
}

// copyInstr implements xsl:copy, a shallow copy of the context node.
type copyInstr struct {
	// sel is the XSLT 3.0 select attribute, naming the item to copy. Nil
	// means the context item, which is what 2.0 always copied.
	sel      *xpath.Compiled
	attrSets []xdm.QName
	// noNamespaces records copy-namespaces="no", which copies the element
	// without its namespace nodes. The default is to copy them all.
	noNamespaces bool
	// noInherit records inherit-namespaces="no", which stops the children
	// constructed by the body from acquiring the copy's namespace nodes.
	noInherit  bool
	body       []Instruction
	validation validationSpec
}

func (i *copyInstr) Execute(rt *runtime, out *outputBuilder) error {
	item := rt.ctx.Item
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		// "If the select expression returns an empty sequence, the xsl:copy
		// instruction returns an empty sequence, and the contained sequence
		// constructor is not evaluated."
		if len(seq) == 0 {
			return nil
		}
		if len(seq) > 1 {
			return fmt.Errorf(
				"XTTE3180: xsl:copy/@select selected %d items, and xsl:copy "+
					"copies a single item", len(seq))
		}
		item = seq[0]
		// The body of a document or element copy is evaluated "with the
		// selected item as the singleton focus" when select is present, so
		// the focus moves before anything below reads it.
		rt = rt.withCurrent(item, 1, 1)
	}
	node, ok := item.(*xdm.Node)
	if !ok {
		// "When the selected item is an atomic value or function item, the
		// xsl:copy instruction returns this value. The sequence constructor
		// is not evaluated." With no select there is no selected item to be
		// anything but a node, so the absent context item is still XTTE0945.
		if i.sel == nil {
			return fmt.Errorf(
				"XTTE0945: xsl:copy requires a node as the context item")
		}
		v, isAtomic := item.(*xdm.Atomic)
		if !isAtomic {
			// A function item. The result tree has no representation for
			// one, and every other instruction that could receive one says
			// so rather than dropping it silently.
			return fmt.Errorf(
				"XTDE0450: xsl:copy cannot copy a function item into a " +
					"result tree")
		}
		out.appendValue(v)
		return nil
	}

	switch node.Kind {
	case xdm.KindElement:
		// Shallow: the element and its namespaces are copied, attributes and
		// children come from the body. That is the distinction from
		// xsl:copy-of, and it is what makes the identity-transform idiom work.
		sub := out.startElement(node.Name)
		if out.open == nil && sub.open.BaseURI == "" {
			// Section 11.9.1: "the base URI of a node is copied". With no
			// parent to inherit from there is nothing else to take it from,
			// so the source node's base URI travels with the shallow copy.
			// An xml:base written into the body overrides it later, via the
			// same path any other attribute takes.
			sub.open.BaseURI = node.BaseURI
		}
		if !i.noNamespaces {
			// Section 11.9.1 copies "the namespace nodes of the element",
			// which is every binding in scope on it rather than only those it
			// declares itself. A copied element whose prefix was declared on
			// an ancestor otherwise lost the declaration it needs.
			copyNamespacesTo(sub, node)
		}
		if err := applyAttributeSets(rt, i.attrSets, sub); err != nil {
			return err
		}
		if err := execSequence(i.body, rt, sub); err != nil {
			return err
		}
		if i.noInherit {
			blockNamespaceInheritance(sub.open)
		}
		// The copy is assessed once it is complete, since validity is a
		// property of the whole element and its content.
		return i.validation.assess(rt, sub.open)

	case xdm.KindDocument:
		// Section 11.9.1: the result of xsl:copy over a document node is "a
		// new node that has the same node kind and name as the context node",
		// so a document node is constructed rather than the body simply being
		// run inline. The difference is invisible when the copy goes straight
		// into a result tree — a document node's children are flattened into
		// the parent either way — but it is exactly what a variable declared
		// as="document-node()*" is asking about.
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt, sub); err != nil {
			return err
		}
		doc, err := sub.toDocument()
		if err != nil {
			return err
		}
		// xsl:copy of a document node copies the properties of the document
		// as well as its children. Two of them are reachable only from the
		// tree rather than the node: the DOCTYPE, which carries the unparsed
		// entity declarations that fn:unparsed-entity-uri looks up, and the
		// base URI, which the copy would otherwise lose along with any
		// relative reference resolved against it. Both are needed together —
		// carrying the DOCTYPE alone leaves the entities declared but their
		// system identifiers unresolvable.
		if src := node.Tree(); src != nil {
			if dst := doc.Tree(); dst != nil {
				dst.DocType = src.DocType
			}
		}
		if doc.BaseURI == "" {
			doc.BaseURI = node.BaseURI
		}
		if err := i.validation.assess(rt, doc); err != nil {
			return err
		}
		out.appendNode(doc)
		return nil

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

	case xdm.KindNamespace:
		// A namespace node joins the element's bindings rather than its
		// children, exactly as it does for xsl:copy-of.
		return out.addNamespaceNode(node.Name.Local, node.Value)
	}
	return nil
}

// literalElemInstr emits a literal result element.
type literalElemInstr struct {
	name xdm.QName
	// baseURI is the base URI in force where the element was written, which
	// the constructed node inherits. Section 5.8: xml:base on any element
	// changes it, so this is the source element's own base rather than the
	// module's.
	baseURI    string
	attrs      []attrTemplate
	namespaces []nsBinding
	// excludedNamespaces holds the bindings in scope on the element that were
	// designated excluded, or are the XSLT namespace. They are kept because
	// section 11.1.4 restores any of them whose URI turns out to be a target
	// namespace URI of an xsl:namespace-alias.
	excludedNamespaces []nsBinding
	attrSets           []xdm.QName
	body               []Instruction
	// validation carries xsl:validation and xsl:type, which a literal result
	// element may have exactly as xsl:element may.
	validation validationSpec
}

type attrTemplate struct {
	name  xdm.QName
	value *avt
}

type nsBinding struct{ prefix, uri string }

// stampConstructedBaseURI records the stylesheet element's base URI on a
// newly constructed element, unless the element has been attached to a parent
// that already supplies one.
//
// XDM 3.0 §6.2 gives dm:base-uri for an element as the value of its xml:base
// attribute if it has one, otherwise the base URI of its parent, and only for
// a parentless element does it fall back to the base URI in force where the
// element was constructed. Stamping the stylesheet's URI unconditionally
// made that last case the only one: an element built inside an xsl:copy of a
// node from a source document reported the *stylesheet's* URI rather than
// the copied node's, because the walk up the ancestors stopped at the stamp.
//
// base-uri-053 is the case. It shallow-copies two documents read with
// fn:doc, puts a <z/> inside each copy, and requires base-uri() of that z to
// end in the copied document's name; every other node in that test was
// already right, and only the two constructed children were not.
//
// An xml:base attribute written by the stylesheet still wins, because it is
// added to the element after this and the accessor reads the attribute
// before the field.
func stampConstructedBaseURI(el *xdm.Node, base string) {
	if base == "" || el.Parent == nil {
		el.BaseURI = base
		return
	}
	for cur := el.Parent; cur != nil; cur = cur.Parent {
		if cur.BaseURI != "" {
			// The parent chain answers, so leaving the field empty is what
			// makes the element inherit rather than override.
			return
		}
	}
	el.BaseURI = base
}

func (i *literalElemInstr) Execute(rt *runtime, out *outputBuilder) error {
	sub := out.startElement(rt.sheet.aliasFor(i.name))
	stampConstructedBaseURI(sub.open, i.baseURI)
	for _, ns := range i.namespaces {
		// Section 11.1.4: aliasing rewrites the namespace nodes copied from
		// the literal result element, not only its name. Copying them
		// unaliased left the placeholder URI in the result beside the
		// namespace it was supposed to have been rewritten to.
		if a, ok := rt.sheet.namespaceAliases[ns.uri]; ok {
			if a.uri == "" {
				continue
			}
			sub.open.AddNamespace(a.prefix, a.uri)
			sub.noteDeclared(a.prefix, a.uri)
			continue
		}
		sub.open.AddNamespace(ns.prefix, ns.uri)
		sub.noteDeclared(ns.prefix, ns.uri)
	}
	// A namespace that exclusion would have dropped comes back if it is the
	// target of an alias: the point of aliasing onto, say, the XSLT namespace
	// is that the result must carry a usable binding for it, and the note in
	// section 11.1.4 says so explicitly — the rules "guarantee that there will
	// be a namespace node that binds the prefix xsl to the URI".
	for _, ns := range i.excludedNamespaces {
		if !rt.sheet.isAliasTarget(ns.uri) {
			continue
		}
		// A binding the aliasing loop above already produced is not repeated:
		// the result-prefix of the alias and an excluded declaration of the
		// same prefix name one namespace node, and emitting both would write
		// the same xmlns declaration twice on one element.
		if bound, ok := sub.open.LookupPrefix(ns.prefix); ok && bound == ns.uri {
			continue
		}
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
		// Section 11.1.4 lists exactly two things aliasing rewrites: the name
		// of a literal result element, and the name of an attribute specified
		// on one. An attribute in no namespace has no name to rewrite, so it
		// is left alone rather than picked up by an alias whose literal
		// namespace URI is null.
		an := a.name
		if an.URI != "" {
			an = rt.sheet.aliasFor(an)
		}
		if err := sub.addAttribute(an, v); err != nil {
			return err
		}
	}
	if err := execSequence(i.body, rt, sub); err != nil {
		return err
	}
	// §5.8.3: an element in a namespace must carry a namespace node for it.
	// exclude-result-prefixes is what makes this reachable -- it drops the
	// very declaration the element's own name depends on, and validation-1705
	// then cannot resolve the prefix in an xsi:type value against the tree.
	// The serialiser wrote the declaration anyway, so nothing about the
	// output changed; only the namespace axis was short.
	fixupNamespaces(sub.open)
	// Assessed once the element is complete, since validity is a property of
	// its content as well as its name.
	return i.validation.assess(rt, sub.open)
}

// elementInstr implements xsl:element, whose name is computed at run time.
type elementInstr struct {
	name *avt
	// baseURI is the base URI in force at the xsl:element instruction, which
	// the constructed element inherits.
	baseURI   string
	namespace *avt
	scope     *xdm.Node
	attrSets  []xdm.QName
	body      []Instruction
	// validation is the validation or type attribute, which asks for the
	// constructed element to be assessed against the imported schema.
	validation validationSpec
	// noInherit records inherit-namespaces="no", which stops the children
	// of the constructed element acquiring its namespace nodes. §11.2 gives
	// xsl:element the attribute in the same terms §11.9 gives it to xsl:copy,
	// and it was only ever honoured on the latter: namespace-0913 writes it
	// on xsl:element and asserts the default namespace does not reach the
	// child.
	noInherit bool
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
	stampConstructedBaseURI(sub.open, i.baseURI)
	if qn.URI != "" {
		sub.open.AddNamespace(qn.Prefix, qn.URI)
	} else if qn.Prefix == "" {
		// An unprefixed name in no namespace UNDECLARES the default
		// namespace when one is in scope. The serialiser worked this out
		// separately and wrote xmlns="", but the tree never recorded it, so
		// the namespace axis kept reporting the inherited default binding.
		// InScopeNamespaces already honours an empty-valued entry as an
		// undeclaration; the constructor simply has to write one.
		if sub.open.Parent != nil && sub.open.Parent.InScopeNamespaces()[""] != "" {
			sub.open.AddNamespace("", "")
		}
	}
	if err := applyAttributeSets(rt, i.attrSets, sub); err != nil {
		return err
	}
	if err := execSequence(i.body, rt, sub); err != nil {
		return err
	}
	if i.noInherit {
		blockNamespaceInheritance(sub.open)
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
	trimmed := strings.TrimSpace(lex)
	// A leading colon splits into an empty prefix and a local name that is a
	// perfectly good NCName, so the checks below pass it. ":foo" is not a
	// lexical QName — the production requires the prefix to be present when
	// the colon is — and it is what "{$prefix}:foo" produces when $prefix is
	// empty, which is the ordinary way a stylesheet reaches this by accident.
	if strings.HasPrefix(trimmed, ":") {
		return xdm.QName{}, fmt.Errorf(
			"XTDE0820: computed name %q is not a valid QName", lex)
	}
	prefix, local := xdm.SplitQName(trimmed)
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
		// XTDE0835: "it is a non-recoverable dynamic error if the effective
		// value of the namespace attribute is not in the lexical space of the
		// xs:anyURI data type or if it is the string
		// http://www.w3.org/2000/xmlns/." The second clause is the same
		// prohibition xsl:namespace carries, and for the same reason: a
		// prefix bound to that URI produces a document no parser reads back.
		if uri == "http://www.w3.org/2000/xmlns/" {
			return xdm.QName{}, fmt.Errorf(
				"XTDE0835: xsl:element must not place a name in " +
					"http://www.w3.org/2000/xmlns/")
		}
		if uri != "" && !isLexicalAnyURI(uri) {
			return xdm.QName{}, fmt.Errorf(
				"XTDE0835: %q is not in the lexical space of xs:anyURI", uri)
		}
		if uri == "" {
			// namespace="" puts the name in *no* namespace. A prefix cannot
			// survive that — a prefixed name in no namespace is not
			// expressible — so it is dropped rather than carried onto a name
			// that would then serialise with a binding it does not have.
			return xdm.QName{Local: local}, nil
		}
		return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
	}
	// Section 11.2: with no namespace attribute the name is expanded using
	// the namespace declarations in effect for the xsl:element element,
	// "including any default namespace declaration". An unprefixed name is
	// therefore not automatically in no namespace — it lands wherever the
	// default namespace points, which is what makes a literal result element
	// and a computed one agree.
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
	// separator is @separator, and hasSeparator distinguishes an explicit
	// zero-length one from an absent attribute.
	separator    *avt
	hasSeparator bool
	sel          *xpath.Compiled
	scope        *xdm.Node
	body         []Instruction
	// validation carries [xsl:]validation and [xsl:]type, which assess the
	// constructed attribute exactly as they assess a constructed element.
	validation validationSpec
}

func (i *attributeInstr) Execute(rt *runtime, out *outputBuilder) error {
	nameStr, err := i.name.eval(rt)
	if err != nil {
		return err
	}
	qn, err := i.resolveName(rt, nameStr)
	if err != nil {
		return err
	}

	// Section 11.2: with no @separator the default is a single space when the
	// content comes from @select and a zero-length string when it comes from
	// the sequence constructor. The two defaults are not interchangeable —
	// xsl:copy-of of a node sequence into an attribute concatenates, while
	// select="1 to 5" produces "1 2 3 4 5".
	sep := ""
	if i.sel != nil {
		sep = " "
	}
	if i.hasSeparator {
		if sep, err = i.separator.eval(rt); err != nil {
			return err
		}
	}
	var value string
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		value = constructedText(seq, sep)
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt.temporaryOutputBefore30(), sub); err != nil {
			return err
		}
		value = constructedText(sub.sequence(), sep)
	}
	// Assessment happens before the attribute joins the output, so that a
	// failure reports the attribute the stylesheet asked for rather than
	// leaving an invalid one behind on the element. The assessed node's type
	// annotation is carried across: assessment is what gives the attribute a
	// type, and writing an untyped copy instead would make the validation
	// invisible to "instance of" and to schema-attribute() patterns.
	assessed := &xdm.Node{Kind: xdm.KindAttribute, Name: qn, Value: value}
	if err := i.validation.assess(rt, assessed); err != nil {
		return err
	}
	return out.addAttributeTyped(qn, value, assessed.TypeAnnotation)
}

// resolveName turns a computed attribute name into an expanded QName.
//
// It is separate from the element rule for one reason that matters: section
// 11.3 expands an attribute name "not including any default namespace
// declaration". An unprefixed attribute name is in no namespace whatever the
// default namespace says, which is the same rule XML itself applies to
// attributes written literally.
func (i *attributeInstr) resolveName(rt *runtime, lex string) (xdm.QName, error) {
	prefix, local := xdm.SplitQName(strings.TrimSpace(lex))
	if !xdm.IsNCName(local) || (prefix != "" && !xdm.IsNCName(prefix)) {
		return xdm.QName{}, fmt.Errorf(
			"XTDE0850: computed attribute name %q is not a valid QName", lex)
	}
	if i.namespace != nil {
		uri, err := i.namespace.eval(rt)
		if err != nil {
			return xdm.QName{}, err
		}
		if uri == xdm.NSXMLNS {
			return xdm.QName{}, fmt.Errorf(
				"XTDE0865: xsl:attribute/@namespace may not be the xmlns namespace")
		}
		if uri == "" {
			return xdm.QName{Local: local}, nil
		}
		return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
	}
	// XTDE0855: with no namespace attribute the name "xmlns" would ask for a
	// namespace declaration to be built as an attribute, which the data model
	// does not represent.
	if prefix == "" && local == "xmlns" {
		return xdm.QName{}, fmt.Errorf(
			"XTDE0855: xsl:attribute cannot create an attribute named xmlns")
	}
	if prefix == "" {
		return xdm.QName{Local: local}, nil
	}
	uri, ok := i.scope.LookupPrefix(prefix)
	if !ok {
		return xdm.QName{}, fmt.Errorf(
			"XTDE0860: unbound prefix %q in computed attribute name %q", prefix, lex)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}

// commentInstr implements xsl:comment.
//
// The content comes either from @select or from the sequence constructor; the
// two are alternatives, and a stylesheet writing both is rejected at compile
// time.
type commentInstr struct {
	sel  *xpath.Compiled
	body []Instruction
}

func (i *commentInstr) Execute(rt *runtime, out *outputBuilder) error {
	var text string
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		text = constructedText(seq, " ")
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt.temporaryOutputBefore30(), sub); err != nil {
			return err
		}
		text = constructedText(sub.sequence(), " ")
	}
	// Section 11.8: the processor "must insert a space after any occurrence of
	// - that is followed by another - or that ends the comment". This is a
	// repair the specification requires, not an error — rejecting the
	// stylesheet refused output XML can perfectly well represent.
	text = repairCommentText(text)
	out.appendNode(&xdm.Node{Kind: xdm.KindComment, Value: text})
	return nil
}

// repairCommentText makes a string legal as comment content.
//
// A "-" followed by another "-", or one at the very end, gets a space after
// it. Done in one left-to-right pass so that a run of hyphens is separated
// throughout rather than only at its first pair.
func repairCommentText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteByte(s[i])
		if s[i] == '-' && (i+1 == len(s) || s[i+1] == '-') {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// piInstr implements xsl:processing-instruction.
type piInstr struct {
	name *avt
	sel  *xpath.Compiled
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
	var text string
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		text = constructedText(seq, " ")
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt.temporaryOutputBefore30(), sub); err != nil {
			return err
		}
		text = constructedText(sub.sequence(), " ")
	}
	// Leading whitespace is not part of the content: the serialised form puts
	// a space after the target, so keeping it would double.
	text = strings.TrimLeft(text, " \t\r\n")
	// Section 11.6: an occurrence of "?>" in the content is repaired by
	// inserting a space between the "?" and the ">", which is what keeps a
	// computed processing instruction from closing itself early.
	text = strings.ReplaceAll(text, "?>", "? >")
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
		sub := rt.withCurrent(it, idx+1, size).clearCurrentRule()
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
	// errorCode is xsl:message/@error-code, an XSLT 3.0 addition naming the
	// code a terminating message raises in place of XTMM9000.
	errorCode *avt
	// errorCodeNS resolves the prefix of an error-code that was written as a
	// lexical QName. The attribute is an AVT, so the name is not known until
	// run time and the namespace context has to be carried to meet it.
	errorCodeNS *xdm.Node
	// xslt30 enables the 3.0 readings: the wider @terminate vocabulary, and
	// surviving a dynamic error raised while building the content.
	xslt30 bool
}

func (i *messageInstr) Execute(rt *runtime, out *outputBuilder) error {
	var text string
	var value xdm.Sequence
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			if !i.xslt30 {
				return err
			}
			// 3.0: "if a dynamic error occurs while evaluating the content,
			// the error is not propagated"; the transformation carries on
			// with whatever the message would have said left unsaid.
			// message-0404 requires the result tree to be produced anyway.
			return nil
		}
		value, text = seq, stringJoin(seq, " ")
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt.temporaryOutputBefore30(), sub); err != nil {
			if !i.xslt30 {
				return err
			}
			return nil
		}
		value, text = sub.sequence(), constructedText(sub.sequence(), " ")
	}
	// Messages are collected rather than printed: a library writing to stderr
	// is a nuisance, and the caller may want them alongside the result.
	*rt.messages = append(*rt.messages, text)

	if i.terminate != nil {
		v, err := i.terminate.eval(rt)
		if err != nil {
			return err
		}
		// XTDE0030: "it is a non-recoverable dynamic error if the effective
		// value of an attribute written using curly brackets, in a position
		// where an attribute value template is permitted, is a value that is
		// not one of the permitted values for that attribute." The summary
		// gives terminate a closed set of two, and the static check cannot
		// look inside a template, so the effective value is checked here.
		terminate, ok := messageTerminate(v, i.xslt30)
		if !ok {
			// XTDE0030: "it is a non-recoverable dynamic error if the
			// effective value of an attribute written using curly brackets,
			// in a position where an attribute value template is permitted,
			// is a value that is not one of the permitted values for that
			// attribute."
			return fmt.Errorf(
				"XTDE0030: xsl:message/@terminate evaluated to %q, which is "+
					"not a permitted value", v)
		}
		if terminate {
			code, err := i.resolveErrorCode(rt)
			if err != nil {
				return err
			}
			return terminateError(code, text, value)
		}
	}
	return nil
}

// --- Sorting ----------------------------------------------------------------

// sortKey is a compiled xsl:sort.
type sortKey struct {
	sel *xpath.Compiled
	// body is the sequence-constructor form of the sort key, used when there
	// is no select attribute. Section 13.1 allows the key to be written
	// either way, and XTSE1015 forbids both at once.
	body  []Instruction
	order string // "ascending" or "descending"
	// dataType is "text" or "number" for the XSLT 1.0 forced conversions;
	// empty means the XSLT 2.0 default of comparing by the values' own type.
	dataType  string
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
	// The remaining attribute value templates, held unevaluated for the case
	// where the stylesheet computes them. Each is nil when the attribute was
	// absent or literal, in which case the plain field above already holds
	// the answer.
	orderAVT     *avt
	dataTypeAVT  *avt
	caseOrderAVT *avt
	langAVT      *avt
}

// resolve evaluates the attribute value templates of a sort key, returning a
// copy with the results filled in.
//
// The attributes cannot vary between the items being sorted — one sort has one
// ordering — so this runs once per sort rather than once per item.
func (s *sortKey) resolve(rt *runtime) (*sortKey, error) {
	if s.orderAVT == nil && s.dataTypeAVT == nil &&
		s.caseOrderAVT == nil && s.langAVT == nil {
		return s, nil
	}
	out := *s
	if s.orderAVT != nil {
		v, err := s.orderAVT.eval(rt)
		if err != nil {
			return nil, err
		}
		if v = strings.TrimSpace(v); v != "" {
			if err := checkSortOrder(v); err != nil {
				return nil, err
			}
			out.order = v
		}
	}
	if s.dataTypeAVT != nil {
		v, err := s.dataTypeAVT.eval(rt)
		if err != nil {
			return nil, err
		}
		out.dataType = strings.TrimSpace(v)
	}
	if s.caseOrderAVT != nil {
		v, err := s.caseOrderAVT.eval(rt)
		if err != nil {
			return nil, err
		}
		if v = strings.TrimSpace(v); v != "" {
			if err := checkCaseOrder(v); err != nil {
				return nil, err
			}
			out.caseOrder = v
		}
	}
	if s.langAVT != nil {
		v, err := s.langAVT.eval(rt)
		if err != nil {
			return nil, err
		}
		if v = strings.TrimSpace(v); v != "" {
			// A computed language tag that names no collation is XTDE0030
			// rather than a compile-time refusal: the stylesheet is
			// well-formed and only the value it produced is wrong.
			coll, err := newCollator(v)
			if err != nil {
				return nil, fmt.Errorf("XTDE0030: %w", err)
			}
			out.coll = coll
		}
	}
	return &out, nil
}

// applySorts orders a sequence by the given sort keys.
//
// Sort keys are computed once per item up front rather than inside the
// comparison function. A comparison-time evaluation would re-run the key
// expression O(n log n) times instead of n, and those expressions are often
// paths into the document.
func applySorts(rt *runtime, seq xdm.Sequence, sorts []*sortKey) (xdm.Sequence, error) {
	n := len(seq)

	type entry struct {
		item xdm.Item
		keys []sortValue
		idx  int
	}
	entries := make([]entry, n)

	// The attribute value templates are resolved once for the whole sort
	// rather than per item: they cannot vary between items, and resolving
	// them per comparison would parse the same URI n log n times.
	sorts = append([]*sortKey(nil), sorts...)
	for k, s := range sorts {
		r, err := s.resolve(rt)
		if err != nil {
			return nil, err
		}
		sorts[k] = r
	}
	resolved, err := resolveSortCollations(rt, sorts)
	if err != nil {
		return nil, err
	}

	// Nothing to order, but the attributes above were still validated: a
	// stylesheet naming an unrecognised collation is in error whether or not
	// the sequence it sorts happens to be short enough that the ordering
	// never matters. The key expressions are not evaluated, since their
	// values could not change the answer.
	if n < 2 {
		return seq, nil
	}

	for i, it := range seq {
		e := entry{item: it, idx: i, keys: make([]sortValue, len(sorts))}
		// Section 16.6.1: current() is "the item that was the context item at
		// the point where the expression was invoked from the XSLT
		// stylesheet". A sort key expression is invoked from xsl:sort with
		// the item being sorted as the context item, so that item is also
		// the current item — a predicate inside the key such as
		// "[@name=current()/@month]" must see it and not whatever node was
		// current in the enclosing template.
		sub := rt.withCurrent(it, i+1, n)
		for k, s := range sorts {
			v, err := s.evalKey(sub)
			if err != nil {
				return nil, err
			}
			sv, err := makeSortValue(v, s, resolved[k], rt.ctx.ImplicitTimezone,
				rt.sheet.output.Version10Implicit)
			if err != nil {
				return nil, err
			}
			e.keys[k] = sv
		}
		entries[i] = e
	}

	var sortErr error
	sort.SliceStable(entries, func(a, b int) bool {
		for k, s := range sorts {
			cmp := compareSortValues(entries[a].keys[k], entries[b].keys[k])
			if cmp == sortIncomparable {
				// The comparison function cannot fail, so the error is
				// recorded and reported once the sort has finished. Its
				// result is discarded, so the arbitrary ordering it gives
				// these two never reaches the caller.
				if sortErr == nil {
					sortErr = fmt.Errorf(
						"XTDE1030: two sort key values cannot be compared "+
							"with the lt operator (%s and %s)",
						entries[a].keys[k].typeName(),
						entries[b].keys[k].typeName())
				}
				return false
			}
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
	// collFoldKey is collKey for the case-folded form of str, present only
	// when @lang and @case-order are both given. See where it is built.
	collFoldKey []byte
	// collKey is the locale-aware sort key for str, precomputed because a
	// collator is stateful and cannot be shared across the comparisons that
	// sort.Slice runs. Comparing two of these byte slices is equivalent to
	// asking the collator to compare the strings, and is safe concurrently.
	collKey []byte
	// empty marks an absent key, which sorts before everything else.
	empty bool
	// atom is the atomized sort key value, kept for the XSLT 2.0 default
	// where the comparison follows the value's own type rather than a
	// conversion the stylesheet asked for. nil when data-type forced a
	// conversion, or when the value was not a single atomic item.
	atom *xdm.Atomic
	// cmpAtom is the same value, but retained even for the string types,
	// which take the collation path for ORDERING and so must leave atom nil.
	//
	// XTDE1030 is about whether the pair is comparable at all, and that
	// question has to be answered for a string against an integer just as
	// much as for a date against an integer — the "lt" operator raises
	// XPTY0004 for both. Keying the check off atom alone meant a sort of
	// (1 to 5, 'fred') never reached the branch, because 'fred' arrived with
	// atom already cleared. error-1030a is exactly that sort.
	cmpAtom *xdm.Atomic
	// implicitTZ is needed to order the date/time types, whose comparison
	// depends on the timezone an untimezoned value is taken to be in.
	implicitTZ int
}

// makeSortValue precomputes one sort key.
//
// It returns an error rather than a value for XTTE1020: section 13.1.3 says
// that if a sort key value, after atomization and any data-type conversion,
// is a sequence of more than one item then "with XSLT 1.0 behavior the
// effective sort key value is the first item in the sequence. In other cases,
// this is a type error." Taking seq[:1] unconditionally applied the 1.0 rule
// in 2.0 mode as well, so a key of (.,.,.) sorted silently instead of being
// rejected.
func makeSortValue(seq xdm.Sequence, s *sortKey, coll xpath.Collation, implicitTZ int, backwards bool) (sortValue, error) {
	atoms := xdm.Atomize(seq)
	if len(atoms) == 0 {
		return sortValue{empty: true}, nil
	}
	if len(atoms) > 1 && !backwards {
		return sortValue{}, fmt.Errorf(
			"XTTE1020: a sort key value is a sequence of %d items; only "+
				"XSLT 1.0 behaviour takes the first item", len(atoms))
	}
	text := stringJoin(seq[:1], "")
	if s.dataType == "number" {
		a := xdm.NewUntypedAtomic(text)
		conv, err := xpath.CastAtomic(a, xdm.TypeDouble)
		if err != nil {
			// A non-numeric value sorts as NaN, which the comparison places
			// before all numbers rather than erroring the whole transform.
			return sortValue{numeric: true, num: nan()}, nil
		}
		return sortValue{numeric: true, num: conv.Float64()}, nil
	}

	v := sortValue{str: text, strColl: coll, implicitTZ: implicitTZ}
	// With no data-type, the value keeps its own type and the comparison is
	// the XPath "lt" operator: a sequence of xs:integer sorts numerically,
	// dates sort chronologically. Only a single atomic value can be ordered
	// that way; anything else falls back to the string form.
	//
	// xs:untypedAtomic is excluded deliberately — section 13.1.2 says such
	// values are cast to xs:string, which is what the string path already
	// does, and treating them as typed would sort unparsed text numerically.
	if s.dataType == "" && len(atoms) == 1 {
		if a, ok := atoms[0].(*xdm.Atomic); ok {
			v.atom = a
			v.cmpAtom = a
			// xs:untypedAtomic orders as a string -- 13.1.2 casts it to one,
			// which is what the string path already does -- but it is still
			// kept in cmpAtom, because the comparability question below is
			// asked of every pair and "lt" does raise for an untyped value
			// against a date or a duration. sort-080 is that pair.
			if a.Type == xdm.TypeUntypedAtomic {
				v.atom = nil
			}
			// A string-valued key still goes through the collation rules
			// below, so only the non-string types take the typed path for
			// ordering. cmpAtom keeps the value regardless, because the
			// comparability question above it is asked of every pair.
			if isStringSortType(a.Type) {
				v.atom = nil
			}
		}
	}
	if s.coll != nil {
		v.collKey = s.coll.key(text)
		if s.caseOrder == "upper-first" || s.caseOrder == "lower-first" {
			// case-order is the caseFirst tailoring: it decides which of a
			// pair that agrees on every other weight comes first. x/text
			// exposes no caseFirst option, so the tailoring is reconstructed
			// by ordering on a case-insensitive key and letting case break
			// the exact ties. The case-folded key is taken through the same
			// collator so the language's own placement of accented letters
			// still governs the primary ordering.
			v.collFoldKey = s.coll.key(strings.ToLower(text))
		}
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
	return v, nil
}

// typeName names the sort key's type for an XTDE1030 message, reading either
// of the two atom fields — the string types leave atom nil and keep cmpAtom.
func (v sortValue) typeName() string {
	switch {
	case v.atom != nil:
		return v.atom.TypeName()
	case v.cmpAtom != nil:
		return v.cmpAtom.TypeName()
	}
	return "untyped"
}

// comparableSortTypes reports whether "lt" accepts the pair. Only the numeric
// types promote across type boundaries; string against anything else, and any
// other cross-type pair, is XPTY0004 and therefore XTDE1030.
func comparableSortTypes(a, b xdm.TypeCode) bool {
	if a == b {
		return true
	}
	return a.IsNumeric() && b.IsNumeric()
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
	// already places accented and cased letters where that language expects.
	if a.collKey != nil && b.collKey != nil {
		// @case-order alongside @lang does not fight the collation, it
		// tailors it: 13.1.3 makes case-order the choice of which of two
		// values that are "equal apart from case" comes first, which is
		// exactly the caseFirst tailoring UCA defines as a tertiary-level
		// setting. So the case-insensitive keys order the pair, and case
		// only breaks a tie the language's own rules left.
		if a.caseOrder && b.caseOrder &&
			a.collFoldKey != nil && b.collFoldKey != nil {
			if c := bytes.Compare(a.collFoldKey, b.collFoldKey); c != 0 {
				return c
			}
			if c := bytes.Compare(a.collKey, b.collKey); c == 0 {
				return 0
			}
			c := strings.Compare(a.str, b.str)
			if c == 0 {
				return 0
			}
			// Codepoint order puts uppercase first, so "upper-first" keeps
			// that sign and "lower-first" inverts it.
			if a.upperFirst {
				return c
			}
			return -c
		}
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

	// Two typed values compare by their own type, which is the XSLT 2.0
	// default. A pair that cannot be ordered together — a string against a
	// number, say — falls through to the string comparison rather than
	// failing the transform, because section 13.1.2 makes that an error only
	// when the values genuinely have no ordering and the suite expects the
	// sort to complete.
	if a.atom != nil && b.atom != nil {
		if c, ok := compareAtoms(a.atom, b.atom, a.implicitTZ); ok {
			return c
		}
		// XTDE1030: "it is a non-recoverable dynamic error if, for any sort
		// key component, the set of sort key values ... contains a pair of
		// ordinary values for which the result of the XPath lt operator is an
		// error." Two values of unrelated types — an integer against a string
		// — are exactly that pair, and ordering them by string form invents
		// an answer the stylesheet did not ask for.
		//
		// The exception is xs:untypedAtomic, which the "lt" operator promotes
		// to the other operand's type rather than refusing: a sort key read
		// from an unvalidated document is untyped, and that is the ordinary
		// case rather than an error.
		// Two values of the *same* type are always orderable in principle, so
		// a refusal there is a gap in compareAtoms rather than the error the
		// clause describes; the durations are the pair that exposed it, and
		// reporting XTDE1030 for them lost two tests that sort perfectly
		// well. Only a pair of genuinely unrelated types is the error.
		//
		// xs:duration is the exception to that exception: two values of that
		// one type are still not orderable, because F&O 2.0 defines "lt" for
		// yearMonthDuration and dayTimeDuration but never for xs:duration —
		// its months and its seconds have no fixed ratio, so P1M against P30D
		// has no answer. Naming the type rather than dropping the a != b test
		// altogether is what keeps the two tests that a blanket same-type
		// refusal previously cost.
		if untypedSortPairErrs(a.atom, b.atom) {
			return sortIncomparable
		}
		if !isUntyped(a.atom) && !isUntyped(b.atom) &&
			(a.atom.Type != b.atom.Type ||
				a.atom.Type == xdm.TypeDuration) {
			return sortIncomparable
		}
	}

	// The same comparability test, for the pairs where at least one side took
	// the string path and so left atom nil. A string against an integer is
	// the pair XTDE1030 names — "lt" raises XPTY0004 for it — and without
	// this the two would silently be ordered by their string forms.
	//
	// Untyped values are excluded for the same reason as above: "lt" promotes
	// them to the other operand's type rather than refusing, and an unvalidated
	// document's keys are all untyped. Equal types are excluded because they
	// are orderable in principle, so a refusal there would be a gap in the
	// comparison rather than this error.
	if a.cmpAtom != nil && b.cmpAtom != nil && a.atom == nil != (b.atom == nil) &&
		(untypedSortPairErrs(a.cmpAtom, b.cmpAtom) ||
			(!isUntyped(a.cmpAtom) && !isUntyped(b.cmpAtom) &&
				!comparableSortTypes(a.cmpAtom.Type, b.cmpAtom.Type))) {
		return sortIncomparable
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

// documentInstr implements xsl:document.
//
// Section 11.5: the instruction builds a *document node* whose children are
// what its sequence constructor produced. That is the whole point of it — a
// sequence constructor otherwise yields a bare sequence, and a stylesheet
// needing a document node to hand to fn:doc, to validate, or to bind to a
// variable declared as="document-node()" has no other way to make one.
//
// Compiling it as a plain block dropped the node, so the content appeared in
// the result but the document node itself never existed.
type documentInstr struct {
	body       []Instruction
	validation validationSpec
}

func (i *documentInstr) Execute(rt *runtime, out *outputBuilder) error {
	sub := newOutputBuilder()
	if err := execSequence(i.body, rt, sub); err != nil {
		return err
	}
	doc, err := sub.toDocument()
	if err != nil {
		return err
	}
	if err := i.validation.assess(rt, doc); err != nil {
		return err
	}
	out.appendNode(doc)
	return nil
}

// isStringSortType reports whether a type is ordered by the collation rules of
// section 13.1.3 rather than by its own comparison operator.
func isStringSortType(t xdm.TypeCode) bool {
	return t == xdm.TypeString || t == xdm.TypeAnyURI ||
		t == xdm.TypeUntypedAtomic
}

// compareAtoms orders two atomic sort key values by their own type, reporting
// whether the pair has an ordering at all.
//
// NaN is ordered rather than left unordered: section 13.1.2 makes NaN values
// equal to each other and less than every other number, which is not what the
// "lt" operator does but is what sorting needs to stay a total order.
func compareAtoms(a, b *xdm.Atomic, implicitTZ int) (int, bool) {
	switch {
	case a.Type.IsNumeric() && b.Type.IsNumeric():
		an, bn := a.Float64(), b.Float64()
		switch {
		case an != an && bn != bn:
			return 0, true
		case an != an:
			return -1, true
		case bn != bn:
			return 1, true
		}
		// Comparing as doubles loses precision for large integers and
		// decimals, so an exact comparison is used when both sides can give
		// one and the doubles came out equal.
		//
		// Equality must return 0. Falling through to 1 made every pair of
		// equal keys compare as "greater", which destroys the stability
		// xsl:sort requires: five values that all round to the same number
		// came back shuffled instead of in document order.
		switch {
		case an < bn:
			return -1, true
		case an > bn:
			return 1, true
		}
		if ar, br := a.Rat(), b.Rat(); ar != nil && br != nil {
			return ar.Cmp(br), true
		}
		return 0, true

	case a.Type == xdm.TypeBoolean && b.Type == xdm.TypeBoolean:
		av, bv := 0, 0
		if a.Bool() {
			av = 1
		}
		if b.Bool() {
			bv = 1
		}
		return av - bv, true

	case a.Type == b.Type && a.DateTimeVal() != nil && b.DateTimeVal() != nil:
		return xdm.CompareDT(a.DateTimeVal(), b.DateTimeVal(), implicitTZ), true

	case a.Type == b.Type && a.Type != xdm.TypeDuration &&
		a.DurationVal() != nil && b.DurationVal() != nil:
		// Durations were not handled at all, so xsl:sort over
		// xs:yearMonthDuration fell through to a string comparison and put
		// P11M before P1M.
		//
		// Only xs:yearMonthDuration against xs:yearMonthDuration, and
		// xs:dayTimeDuration against xs:dayTimeDuration, are ordered here:
		// those are the only two duration orderings F&O 2.0 defines
		// (op:yearMonthDuration-less-than, op:dayTimeDuration-less-than).
		//
		// Bare xs:duration is excluded even though both sides have the SAME
		// type, which is the case the a.Type == b.Type test alone gets wrong.
		// xs:duration carries a months component and a seconds component with
		// no fixed ratio between them, so P1M against P30D has no answer and
		// "lt" on the pair raises XPTY0004 — making it the XTDE1030 pair, not
		// an ordering to invent. The mixed yearMonth/dayTime case is excluded
		// by a.Type == b.Type for the same reason.
		ad, bd := a.DurationVal(), b.DurationVal()
		if am, bm := ad.SignedMonths(), bd.SignedMonths(); am != bm {
			if am < bm {
				return -1, true
			}
			return 1, true
		}
		return ad.SignedSeconds().Cmp(bd.SignedSeconds()), true
	}
	return 0, false
}

// sortIncomparable is the value compareSortValues returns for a pair the XPath
// lt operator cannot order.
//
// It is a sentinel rather than an error return because the function is called
// from sort.SliceStable's comparison, whose signature has no room for one. The
// value is outside the -1..1 that a real comparison produces, so no caller can
// mistake it for an ordering.
const sortIncomparable = -2

// isUntyped reports whether an atomic value is xs:untypedAtomic, which the
// comparison operators promote rather than refuse.
func isUntyped(a *xdm.Atomic) bool {
	return a.Type == xdm.TypeUntypedAtomic
}
