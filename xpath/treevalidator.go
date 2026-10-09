package xpath

import "github.com/knroy/go-xml/v2/xdm"

// TreeValidator validates a tree this package has just constructed, returning
// a copy carrying the type annotations the assessment produces.
//
// fn:json-to-xml is the one function in the library that must do this. F&O
// 3.1 §17.5.3 says of its validate option: true "indicates that the resulting
// XDM instance must be typed; that is, the element and attribute nodes must
// carry the type annotations that result from validation against the schema
// given at C.2 Schema for the result of fn:json-to-xml".
//
// It is an interface here rather than an *xsd.Schema for the same reason
// SchemaTypes is: xsd imports xpath, because schema documents contain XPath
// expressions in their assertions and selectors, so the dependency cannot run
// the other way. The xslt package supplies the implementation over the schema
// its xsl:import-schema declarations assembled.
//
// SchemaTypes is the sibling of this and answers a different question: it is
// consulted while an expression is being *parsed*, about names the static
// context knows, and it reaches this package as a property of the namespace
// resolver. This one is consulted while an expression is being *evaluated*,
// about a tree that exists, so it is a property of the dynamic context.
//
// A nil TreeValidator means the processor cannot validate. That is not a
// silent no-op: F&O 3.1 §17.5.3 raises FOJS0004 "if the value of the validate
// option is true and the processor does not support schema validation or
// typed data", so the caller reports it rather than returning an untyped tree
// that would fail every assertion the stylesheet then makes about it.
type TreeValidator interface {
	// ValidateJSONTree assesses a document node holding the XML
	// representation of JSON against the schema of F&O 3.1 §C.2, and returns
	// the typed copy of it, which is the function's result.
	//
	// An implementation that has the schema but finds the tree invalid
	// returns the error. That should not happen for a tree fn:json-to-xml
	// built — the construction rules of §17.4.2 produce a valid instance by
	// construction, and duplicates="retain", the one option that does not,
	// is already refused alongside validate=true — but a wrong answer is
	// worth a diagnosis rather than a silently untyped tree.
	ValidateJSONTree(doc *xdm.Node) (*xdm.Node, error)
}
