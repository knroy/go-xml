// Package xdmbuild constructs XDM sequences and trees from the results of a
// sequence constructor.
//
// XSLT and XQuery build result trees the same way. The rules in XSLT 3.0
// §5.7.1 and XQuery 3.1 §3.9.1.3 are, for the part that matters here, the same
// text: arrays are flattened, a run of adjacent atomic values becomes one text
// node with a single space between each, adjacent text nodes are merged with
// no separator, zero-length text nodes are dropped, a document node is
// replaced by its children, and an attribute or namespace node may not follow
// a node that is neither.
//
// What differs between the two languages is small and enumerable: five error
// codes, one genuine difference in behaviour, and the namespace and type
// policies a copy is made under. Those arrive through a Policy rather than
// being written into the builder, so this package names neither language.
package xdmbuild

// A Fault is a structural fault detected while building content.
//
// The builder reports what went wrong and leaves naming it to the caller,
// because the two languages that use this give the same fault different codes
// — and, for FaultDuplicateAttribute, different behaviour. Reporting the
// condition rather than the code is what lets one builder serve both.
type Fault int

const (
	// FaultAttrAfterChild is an attribute or namespace node added to an
	// element that already has a child which is neither.
	//
	// XSLT: XTDE0410. XQuery: XQTY0024.
	FaultAttrAfterChild Fault = iota

	// FaultAttrOnDocument is an attribute or namespace node appearing as the
	// content of a document node.
	//
	// XSLT: XTDE0420. XQuery: XPTY0004.
	FaultAttrOnDocument

	// FaultConflictingPrefix is one prefix bound to two different URIs on the
	// same element by the sequence being constructed.
	//
	// XSLT: XTDE0430. XQuery: XQDY0102.
	FaultConflictingPrefix

	// FaultDefaultNSOnNoNS is a default namespace declared on an element whose
	// own name is in no namespace.
	//
	// XSLT: XTDE0440. XQuery: XQDY0102.
	FaultDefaultNSOnNoNS

	// FaultFunctionItem is a function item, map or array appearing where the
	// content model admits only nodes and atomic values.
	//
	// XSLT: XTDE0450. XQuery: XQTY0105. In a serialization context both
	// languages raise SENR0001 instead, which is why this is a fault rather
	// than a fixed code.
	FaultFunctionItem

	// FaultDuplicateAttribute is two attributes with the same name on one
	// element.
	//
	// This is the one fault where the languages disagree about more than the
	// code. XSLT 3.0 §5.7.1 discards the earlier attribute silently — "if an
	// attribute A in the sequence has the same name as another attribute B
	// that appears later in the sequence, then attribute A is discarded".
	// XQuery 3.1 §3.9.1.3 raises XQDY0025.
	//
	// A Policy selects XSLT's behaviour by returning nil for this fault: the
	// builder then replaces the earlier attribute and carries on. Returning an
	// error selects XQuery's.
	FaultDuplicateAttribute
)

// String names a fault, for a message that has to describe one.
func (f Fault) String() string {
	switch f {
	case FaultAttrAfterChild:
		return "attribute or namespace node after a child"
	case FaultAttrOnDocument:
		return "attribute or namespace node in a document"
	case FaultConflictingPrefix:
		return "conflicting namespace prefix"
	case FaultDefaultNSOnNoNS:
		return "default namespace on an element in no namespace"
	case FaultFunctionItem:
		return "function item in content"
	case FaultDuplicateAttribute:
		return "duplicate attribute"
	}
	return "unknown fault"
}

// A Policy supplies what the builder cannot know by itself: how to name a
// structural fault, and the namespace and type rules a copy is made under.
//
// The namespace questions are asked as two independent booleans because the
// specifications ask them that way. XQuery's copy-namespaces has two axes,
// preserve/no-preserve and inherit/no-inherit, which vary independently;
// XSLT's xsl:element and xsl:copy carry inherit-namespaces and always
// preserve.
type Policy interface {
	// Err names a structural fault, or returns nil to accept it.
	//
	// Only FaultDuplicateAttribute is meaningfully acceptable: returning nil
	// there selects XSLT's rule that a later attribute replaces an earlier one
	// of the same name. Returning nil for any other fault lets the builder
	// carry on with content the data model does not admit, so a Policy should
	// name them all.
	//
	// detail describes the particular node or prefix at fault and is meant to
	// be quoted in the message.
	Err(f Fault, detail string) error

	// InheritNamespaces reports whether the namespaces in scope on a
	// constructed element are copied to elements copied beneath it.
	//
	// XSLT: xsl:element/@inherit-namespaces and xsl:copy/@inherit-namespaces.
	// XQuery: the inherit / no-inherit half of copy-namespaces.
	InheritNamespaces() bool

	// PreserveNamespaces reports whether a copied element keeps every
	// namespace that was in scope on the original, or only those used in the
	// names of the element and its attributes.
	//
	// XSLT has no way to ask for anything but the first, so an XSLT policy
	// returns true. XQuery: the preserve / no-preserve half of
	// copy-namespaces.
	PreserveNamespaces() bool

	// PreserveTypes reports whether a copied node keeps its type annotation
	// and its is-id, is-idrefs and nilled properties.
	//
	// XSLT: validation="preserve" against any other validation mode.
	// XQuery: construction mode preserve against strip.
	PreserveTypes() bool

	// DropEmptyText reports whether a text node that a document constructor
	// would be left holding is dropped when it is zero length.
	//
	// The two languages part company here, and only over a document node's
	// children -- inside an element both drop zero-length text, which
	// AppendText does for either.
	//
	// XQuery returns true. "document {'', document{''}, ''}" has no children
	// at all: every value contributes a zero-length text node, §3.9.1.3
	// removes each of them, and Constr-docnode-nested-4 counts the result
	// and expects zero.
	//
	// XSLT returns false, because the same construction is not empty there.
	// A run of adjacent atomic values is joined by a single space *before*
	// the removal rule applies, so two xsl:sequence instructions selecting
	// '' leave a text node holding one space -- content, not nothing. The
	// suite is emphatic about it: on-empty-115a builds exactly that document
	// and its xsl:on-empty fallback reads "WRONG! the document node contains
	// a space, it is not empty!!!", and seqtor-039a wants " | | " where
	// dropping the empties would give "||".
	//
	// So this is a real difference in the languages rather than a gap, which
	// is why it is asked rather than decided in the builder.
	DropEmptyText() bool
}
