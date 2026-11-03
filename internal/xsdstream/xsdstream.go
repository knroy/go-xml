// Package xsdstream lets package xslt validate a document while a cursor
// (internal/xdmstream) reads it, with package xsd's validator, without adding
// exported API to xsd. Package xsd sets the hooks in its init.
//
// What a validator gives is what a typed copy of the document would: it
// writes each node's typing onto the parse's own records as the cursor meets
// them, so every view, grounded copy and leaf copy the cursor makes carries
// it, and adds default attributes to the start tags. Whether the document is
// valid is known only once the cursor has read it to its end: a run that
// meets a failure records it and goes on, and Err reports it.
package xsdstream

import (
	"context"

	"github.com/knroy/go-xml/v2/xdm"
)

// Root says what the validation root is assessed against.
type Root uint8

const (
	// Strict assesses the document element against its global declaration,
	// or the type its xsi:type names, and applies the ID/IDREF constraints
	// over the document: xsd's ValidateCopy of the element (lax with a
	// declaration or an xsi:type is the same run).
	Strict Root = iota + 1
	// Lax is Strict when the document element has a declaration or an
	// xsi:type, and otherwise assesses nothing.
	Lax
	// Type assesses the document element against Mode.Type, as [xsl:]type
	// does (xsd's ValidateAgainstTypeCopy): no declaration, no ID/IDREF
	// constraints.
	Type
)

// Mode is one validation of a document.
type Mode struct {
	Root Root
	Type xdm.QName // for Root Type
	// Doc says the validation root is the document node, not its element:
	// ignorable whitespace (in element-only content) is then dropped, as
	// xsd's typed copy of a document drops it (Validator.Drops).
	Doc bool
}

// Validator is one validation run over a cursor's events.
type Validator interface {
	// Observe is the cursor's Observer (xdmstream.Observer, without the
	// ordinal): it must be the first one the cursor has.
	Observe(n *xdm.Node, depth int, end bool)
	// Drops reports whether whitespace-only text directly inside el, the
	// innermost element open, is dropped as ignorable (Mode.Doc only).
	Drops(el *xdm.Node) bool
	// Stripped tells the run that whitespace-only text directly inside el,
	// the innermost element open, was stripped before Observe could see it
	// (xsl:strip-space on a document validated before it is stripped).
	Stripped(el *xdm.Node)
	// Err is the first failure met, or nil. After the document's end (the
	// observer's last call), nil says the document is valid.
	Err() error
}

// Open starts a validation of a document against schema (an *xsd.Schema).
var Open func(schema any, m Mode) Validator

// Refusal says why schema (an *xsd.Schema) cannot be validated on a stream
// exactly as the tree is, or "" when it can. It is a property of the schema's
// components, so a caller may remember it per schema.
var Refusal func(schema any) string

type sourceKey struct{}

// WithSource returns ctx carrying schema (an *xsd.Schema): a principal input
// read with xslt's TransformReader is validated against it, strictly, with the
// document node as the validation root. It is how the conformance harness asks
// for what it does for a tree (parse, xsd ValidateCopy, Transform), and is not
// API: there is none for validating a principal input.
func WithSource(ctx context.Context, schema any) context.Context {
	return context.WithValue(ctx, sourceKey{}, schema)
}

// Source returns the schema WithSource put in ctx, or nil.
func Source(ctx context.Context) any {
	if ctx == nil {
		return nil
	}
	return ctx.Value(sourceKey{})
}

// MarkSource tells resolver (an *xslt.FileResolver) that the document at
// uri, which it holds as a tree, is one the conformance harness validated
// against schema (an *xsd.Schema) with the document node as the root, and
// that a validating xsl:source-document may read it on a stream all the same,
// validating it so first. Package xslt sets it; it is not API.
var MarkSource func(resolver any, uri string, schema any)
