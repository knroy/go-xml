package xslt

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// l7Builtin assesses the cases of fn:root that §19.8.9.18 settles without
// the parts of its rule this analysis does not model; ok false leaves the
// call unmodelled, as before.
//
// "The zero-argument function root() is equivalent to root(.). Given the
// expression root(X), if the static type of X is U{document-node()}, and if
// its posture is striding, then root(X) is rewritten as X. Otherwise, it is
// rewritten as head((X)/ancestor-or-self::node())."
//
// A grounded X is X's posture and sweep: every node reached from a grounded
// node is grounded (§19.8.8.9), and head() keeps the posture. A grounded X
// standing for a node of the stream (the streaming parameter,
// streamedGrounded) is not taken: its ancestors are the stream's.
// sf-snapshot-0102's $s/root() is the case.
//
// A crawling or climbing X fails the "striding" condition, and a striding X
// whose static type (§19.1, staticType) is not U{document-node()} fails the
// type one, so both take the head() form: ancestor-or-self from any of the
// three is climbing and motionless (the §19.8.8.9 table), the path keeps X's
// sweep (§19.8.8.8) and head transmits. A striding X whose static type is not
// known -- the context item of a container whose context item type this
// analysis does not establish, say -- stays unmodelled: rewriting it either
// way could be wrong.
func (a *analyzer) l7Builtin(x *xpath.FuncCall) (props, bool) {
	if x.Name.Local != "root" || len(x.Args) > 1 {
		return props{}, false
	}
	var arg xpath.Expr = &xpath.ContextItem{}
	if len(x.Args) == 1 {
		arg = x.Args[0]
	}
	o := a.operandOf(arg, usageNavigation)
	switch {
	case !o.props.streamable():
		// A roaming X roams on any axis (§19.8.8.9), so the rewrite does.
		return roamingFreeRanging, true
	case o.streamedGrounded:
		return props{}, false
	case o.props.posture == postureGrounded:
		return o.props, true
	case o.props.posture == postureCrawling, o.props.posture == postureClimbing:
		return props{postureClimbing, o.props.sweep}, true
	}
	switch a.rootRewrite(a.staticType(arg)) {
	case rootAsX:
		return o.props, true
	case rootAsHead:
		return props{postureClimbing, o.props.sweep}, true
	}
	return props{}, false
}

// rootRule is which of §19.8.9.18's two rewrites applies to root(X) of a
// striding X.
type rootRule int8

const (
	rootUnknown rootRule = iota // X's static type is not known well enough
	rootAsX                     // "rewritten as X"
	rootAsHead                  // "rewritten as head((X)/ancestor-or-self::node())"
)

// rootRewrite decides §19.8.9.18's condition "if the static type of X is
// U{document-node()}" from X's static type t. An exact type decides it
// either way; an over-approximation decides it only when it excludes the
// document node, since every type it bounds then does too.
func (a *analyzer) rootRewrite(t itemType) rootRule {
	switch {
	case t.exact && t.u == uDocument:
		return rootAsX
	case t.exact || t.bound()&uDocument == 0:
		return rootAsHead
	}
	return rootUnknown
}

// uType is a §19.1 U-type -- "a set of fundamental item types" -- with the
// seven node kinds kept apart and every other fundamental item type (the
// atomic types and function(*)) folded into uOther, because the rules that
// read a static type here ask only about node kinds.
type uType uint8

const (
	uDocument uType = 1 << iota
	uElement
	uAttribute
	uText
	uComment
	uPI
	uNamespace
	uOther

	uNodes = uDocument | uElement | uAttribute | uText | uComment | uPI | uNamespace
	uAny   = uNodes | uOther
	// uTree is what the child and sibling axes reach (§19.1.1's
	// "U{element(), text(), comment(), processing-instruction()}").
	uTree = uElement | uText | uComment | uPI
)

// itemType is a static type as this analysis knows it. exact says u is the
// type §19.1 and §19.2 assign; otherwise u is only an upper bound (every
// value conforms to it). The zero value is unknown: an analyzer built without
// a context item type has one, so a context it was not told about is never
// taken for a known one.
type itemType struct {
	u     uType
	exact bool
}

// bound is the tightest set t guarantees: U{*} when nothing is known.
func (t itemType) bound() uType {
	if !t.exact && t.u == 0 {
		return uAny
	}
	return t.u
}

var docType = itemType{uDocument, true}

// staticType infers the §19.1 static type of e from its node-kind forms:
// ".", an axis step (§19.1.1), a path (the type of its last operand, "/" being
// U{document-node()}), a filter (its base), treat (the declared type), and
// the union, intersect and except operators and the comma (the union of its
// items). Anything else is not inferred and gets the zero, unknown type. One
// call is O(n) in the size of e.
func (a *analyzer) staticType(e xpath.Expr) itemType {
	switch x := e.(type) {
	case *xpath.ContextItem:
		return a.ctxType
	case *xpath.Step:
		return stepType(x, a.ctxType)
	case *xpath.PathExpr:
		cit := a.ctxType
		if x.Root {
			// "(root(self::node()) treat as document-node())" (§19.8.8.8)
			cit = docType
		}
		for _, s := range x.Steps {
			if st, ok := s.(*xpath.Step); ok {
				cit = stepType(st, cit)
				continue
			}
			in := *a
			in.ctxType = cit
			cit = in.staticType(s)
		}
		return cit
	case *xpath.FilterExpr:
		return a.staticType(x.Base)
	case *xpath.TreatExpr:
		return itemType{sequenceTypeU(x.Type), true}
	case *xpath.SequenceExpr:
		t := itemType{0, true}
		for _, it := range x.Items {
			t = unionType(t, a.staticType(it))
		}
		return t
	case *xpath.BinaryOp:
		l, r := a.staticType(x.Left), a.staticType(x.Right)
		switch x.Op {
		case "|", "union":
			return unionType(l, r)
		case "intersect":
			return itemType{l.bound() & r.bound(), l.exact && r.exact}
		case "except":
			return l
		}
	}
	return itemType{}
}

func unionType(l, r itemType) itemType {
	return itemType{l.bound() | r.bound(), l.exact && r.exact}
}

// stepType is §19.1.1: the intersection of K(A, CIT), the node kinds axis A
// reaches from the context item type, with T(NT), those the node test admits.
// An inexact CIT gives an inexact result; K is monotone in CIT, so the
// result still bounds the exact one.
func stepType(s *xpath.Step, cit itemType) itemType {
	c := cit.bound() & uNodes
	var k uType
	switch s.Axis {
	case xpath.AxisSelf:
		k = c
	case xpath.AxisAttribute:
		if c&uElement != 0 {
			k = uAttribute
		}
	case xpath.AxisNamespace:
		if c&uElement != 0 {
			k = uNamespace
		}
	case xpath.AxisChild, xpath.AxisDescendant:
		if c&(uElement|uDocument) != 0 {
			k = uTree
		}
	case xpath.AxisFollowingSibling, xpath.AxisPrecedingSibling, xpath.AxisFollowing, xpath.AxisPreceding:
		if c != uDocument {
			k = uTree
		}
	case xpath.AxisParent, xpath.AxisAncestor:
		if c != uDocument {
			k = uElement | uDocument
		}
	case xpath.AxisAncestorOrSelf:
		if c != uDocument {
			k = uElement | uDocument
		}
		k |= c
	case xpath.AxisDescendantOrSelf:
		if c&(uElement|uDocument) != 0 {
			k = uTree
		}
		k |= c
	default:
		return itemType{}
	}
	var t uType
	switch nt := s.Test.(type) {
	case *xpath.NameTest:
		// "the principal node kind of the specified axis"
		switch s.Axis {
		case xpath.AxisAttribute:
			t = uAttribute
		case xpath.AxisNamespace:
			t = uNamespace
		default:
			t = uElement
		}
	case *xpath.KindTest:
		t = kindTestU(nt)
	default:
		return itemType{}
	}
	return itemType{k & t, cit.exact}
}

// kindTestU is §19.1's mapping of a KindTest to a U-type.
func kindTestU(kt *xpath.KindTest) uType {
	if kt.Any {
		return uNodes
	}
	switch kt.Kind {
	case xdm.KindDocument:
		return uDocument
	case xdm.KindElement:
		return uElement
	case xdm.KindAttribute:
		return uAttribute
	case xdm.KindText:
		return uText
	case xdm.KindComment:
		return uComment
	case xdm.KindPI:
		return uPI
	case xdm.KindNamespace:
		return uNamespace
	}
	return uNodes
}

// sequenceTypeU is §19.1's mapping from a SequenceType to a U-type, which
// "ignores the occurrence indicator".
func sequenceTypeU(st xpath.SequenceType) uType {
	switch {
	case st.Empty, st.IsErrorType:
		// "empty-sequence() maps to U{}", "The QName xs:error maps to U{}"
		return 0
	case st.ItemType != nil:
		if kt, ok := st.ItemType.(*xpath.KindTest); ok {
			return kindTestU(kt)
		}
		return uAny
	case st.HasAtomicType || st.IsFunctionTest || st.IsMapTest || st.IsArrayTest || st.IsNumericType:
		return uOther
	}
	return uAny
}

// containerItemType is §19.2's context item type for the sequence
// constructor of el: "If the focus-setting container of C is an
// xsl:source-document instruction, then the context item type is
// U{document-node()}. If the focus-setting container of C is a template
// rule, then the context item type is the match type of the match pattern of
// the template rule". Any other container's is left unknown.
func containerItemType(el *xdm.Node) itemType {
	switch {
	case isXSL(el, "source-document"), isXSL(el, "stream"):
		return docType
	case isXSL(el, "template") && el.Attr("", "match") != nil:
		ns := newNSResolver(el, xpathDefaultNamespace(el))
		e, err := xpath.ParseVersion(el.AttrValue("match"), ns, xpathVersionAt(el))
		if err != nil {
			return itemType{}
		}
		return matchType(e)
	}
	return itemType{}
}

// matchType is §19.2's "match type of a pattern", "the inferred static type
// of the pattern's equivalent expression", for the pattern forms whose type
// does not depend on the child-or-top axis: "/", a document-node() test, and
// a path whose last step names its node kind (a name test or a kind test
// other than node()); unions combine. "para[1]" is U{element()} and
// "@code[.='x']" U{attribute()} (§19.2's examples): predicates do not count.
// Anything else -- node(), a function call, a variable -- is unknown.
func matchType(e xpath.Expr) itemType {
	switch x := e.(type) {
	case *xpath.PathExpr:
		if len(x.Steps) == 0 {
			if x.Root {
				return docType
			}
			return itemType{}
		}
		return matchType(x.Steps[len(x.Steps)-1])
	case *xpath.Step:
		if kt, ok := x.Test.(*xpath.KindTest); ok && kt.Any {
			return itemType{}
		}
		if kt, ok := x.Test.(*xpath.KindTest); ok && kt.Kind == xdm.KindDocument && !x.Explicit {
			// The abbreviated child step is child-or-top in a pattern
			// (§5.5.3), so document-node() matches the document node.
			return docType
		}
		switch x.Axis {
		case xpath.AxisChild, xpath.AxisAttribute, xpath.AxisDescendant:
			// Every node these reach has a parent, so the type is the node
			// test's: §19.1.1 with a context item type that admits the
			// parent.
			return stepType(x, itemType{uElement | uDocument, true})
		}
		return itemType{}
	case *xpath.BinaryOp:
		if x.Op == "|" || x.Op == "union" {
			l, r := matchType(x.Left), matchType(x.Right)
			if l.exact && r.exact {
				return unionType(l, r)
			}
		}
	}
	return itemType{}
}
