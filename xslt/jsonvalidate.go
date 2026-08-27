package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// jsonTreeValidator implements xpath.TreeValidator over the built-in schema
// for the XML representation of JSON.
//
// fn:json-to-xml with validate=true has to hand the tree it built to the
// schema layer to be typed, and the xpath package cannot reach that layer
// itself: xsd imports xpath, because schema documents carry XPath in their
// assertions and selectors, so the dependency cannot run the other way. This
// is the same interface-injection the SchemaTypes hook uses for the static
// side, and for the same reason.
type jsonTreeValidator struct{}

// ValidateJSONTree assesses the tree against F&O 3.1 §C.2 and annotates it.
//
// The schema is the built-in one rather than whatever the stylesheet
// imported. §17.5.3 names it outright — the annotations are those "that
// result from validation against the schema given at C.2" — so a stylesheet
// that never wrote xsl:import-schema still gets a typed result, and one that
// imported some unrelated schema does not get its result assessed against
// that instead. What xsl:import-schema decides is a separate question: it
// decides whether the stylesheet may *name* j:mapType in a sequence type,
// not whether the node carries it.
//
// Identity constraints are skipped. The schema's xs:unique on a map's keys is
// scoped to the validation root, which here is a document node the function
// built and nothing else can see; the duplicates option has already settled
// what happens to a repeated key, and reject is the default whenever
// validate=true, so a tree reaching here has none left to find.
func (jsonTreeValidator) ValidateJSONTree(doc *xdm.Node) error {
	schema, err := xsd.SchemaForJSON()
	if err != nil {
		return xdm.Errorf("FOJS0004", "%s", err.Error())
	}
	if err := schema.Validate(doc, xsd.ValidateOptions{
		Annotate:          true,
		SkipIDConstraints: true,
	}); err != nil {
		// A tree fn:json-to-xml built is valid against this schema by
		// construction, so reaching here means the construction rules and
		// the schema have drifted apart. Say which, rather than reporting a
		// bare cvc- code from a document the stylesheet never wrote.
		return fmt.Errorf(
			"the XML representation of the JSON input is not valid against "+
				"the schema for fn:json-to-xml: %w", err)
	}
	return nil
}
