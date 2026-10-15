package xquery_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xquery"
	_ "github.com/knroy/go-xml/v2/xslt" // registers fn:transform
)

// A transform a query starts with fn:transform is held to the query's
// Env.MaxItems: the nested runtime mints a Context of its own, and the
// bound must travel with the item counter it adopts.
func TestNestedTransformKeepsTheQueryItemBound(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template name="go"><n><xsl:value-of select="count((1 to 11)[. ge 0])"/></n></xsl:template>
</xsl:stylesheet>`
	run := func(limit int) (xdm.Sequence, error) {
		ctx := xpath.NewContext(nil, xpath.Builtins())
		ctx = ctx.WithEnv(func(e *xpath.Env) { e.MaxItems = limit })
		ctx = ctx.WithVar(xdm.QName{Local: "s"}, xdm.One(xdm.NewString(sheet)))
		return xquery.Eval(`declare variable $s external;
transform(map{'stylesheet-text': $s, 'initial-template': QName('', 'go'),
  'delivery-format': 'raw'})?output`, ctx, xquery.Options{})
	}
	if _, err := run(0); err != nil {
		t.Fatalf("under the default bound: %v", err)
	}
	_, err := run(10)
	if err == nil || !strings.Contains(err.Error(), "the 10 item limit") {
		t.Errorf("the nested transform escaped the query's bound of 10: %v", err)
	}
}
