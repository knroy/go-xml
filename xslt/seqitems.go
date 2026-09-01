package xslt

import (

	"github.com/knroy/go-xml/xdm"
)

// appendOpaqueItem adds a function item, a map or an array to the result, or
// reports that it cannot be added where it stands.
//
// None of the three is a node and none has a textual form, so 5.7.1 has no
// rule that would turn one into element content: inside an element under
// construction there is nowhere for it to go, and that is XTDE0450. At the top
// of a sequence it is an ordinary item, which is what makes an xsl:function
// declared as="function(*)" able to return one at all.
//
// Dropping such an item silently is what the switch in sequenceInstr did
// before: an xsl:sequence selecting an inline function produced the empty
// sequence, and the declared type then rejected the function the body had just
// built.
func appendOpaqueItem(out *outputBuilder, it xdm.Item) error {
	return out.AppendOpaque(it)
}
