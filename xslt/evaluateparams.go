package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// bindWithParams binds the variables the @with-params map supplies into rt.
//
// Section 10.4.2 lets xsl:evaluate name its parameters dynamically as well as
// statically: @with-params is an expression of type map(xs:QName, item()*),
// and where a name appears both there and on an xsl:with-param child the map
// wins. The caller therefore binds the children first and this last, so the
// map's binding simply shadows the child's.
func (i *evaluateInstr) bindWithParams(rt, sub *runtime) (*runtime, error) {
	if i.withParams == nil {
		return sub, nil
	}
	seq, err := i.withParams.Eval(rt.ctx)
	if err != nil {
		return nil, fmt.Errorf("in xsl:evaluate/@with-params: %w", err)
	}
	if len(seq) != 1 {
		return nil, fmt.Errorf("XTTE3165: the with-params attribute of "+
			"xsl:evaluate selected %d items, not one map", len(seq))
	}
	m, ok := seq[0].(*xdm.MapItem)
	if !ok {
		return nil, fmt.Errorf("XTTE3165: the with-params attribute of " +
			"xsl:evaluate is not a map")
	}
	err = m.Entries(func(key *xdm.Atomic, val xdm.Sequence) error {
		qn := key.QName()
		if qn == nil {
			// The declared key type is xs:QName, and a key of any other type
			// leaves the parameter unnameable rather than merely unused.
			return fmt.Errorf("XTTE3165: a key of the with-params map of "+
				"xsl:evaluate is %s, not xs:QName", key.TypeName())
		}
		sub = sub.withVar(*qn, val)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sub, nil
}
