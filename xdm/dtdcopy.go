package xdm

// CopyDTDFrom gives t the DTD context of src: the DOCTYPE as written and the
// external subset text, if one was read.
//
// A tree built by copying nodes out of another -- xsl:copy-of over a document
// node, fn:snapshot over anything -- carries no DTD of its own, and so answers
// fn:unparsed-entity-uri with the empty string for entities the original
// declared. XSLT 3.0 27.2 says a snapshot's root "has the same unparsed
// entities as the tree from which it was taken", and the two-argument forms
// of those functions exist precisely so that such a copy can be asked.
//
// Only the declarations matter, not the identity of the tree: both fields are
// text, and both are read-only after parsing.
func (t *Tree) CopyDTDFrom(src *Tree) {
	if t == nil || src == nil {
		return
	}
	t.DocType = src.DocType
	t.externalSubset = src.externalSubset
}
