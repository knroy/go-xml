// Package dtdnote carries the validity errors package xdm's parser finds but
// a tree cannot show to package dtd's Validate, without adding exported API to
// xdm. Some of what a DTD constrains is gone once a document is a tree: which
// attribute values the parser normalized or defaulted, whether white space in
// element content was written as a character reference or a CDATA section,
// and so on. The parser sees those facts as it reads and records the errors
// they make; Validate reports them with its own.
//
// Package xdm sets Notes in its init, so it is non-nil wherever xdm is linked
// in. The values typed any are *xdm.Node: this package cannot import xdm,
// which imports it.
package dtdnote

import "iter"

// Notes yields the validity errors recorded while parsing the tree holding
// node, each with the node it concerns: an element, or the document node for
// an error about the document as a whole.
var Notes func(node any) iter.Seq2[any, string]
