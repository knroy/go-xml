package xquery

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// jsonTreeValidator implements xpath.TreeValidator over the built-in schema
// for the XML representation of JSON.
//
// fn:json-to-xml with validate=true hands the tree it built to the schema
// layer to be typed, and the xpath package cannot reach that layer itself:
// xsd imports xpath, because schema documents carry XPath in their assertions
// and selectors, so the dependency cannot run the other way. xslt installs
// the same hook for the same reason; this is the query side of it.
type jsonTreeValidator struct{}

// ValidateJSONTree assesses the tree against F&O 3.1 §C.2 and annotates it.
//
// The schema is the built-in one rather than whatever the query imported.
// §17.5.3 names it outright — the annotations are those "that result from
// validation against the schema given at C.2" — so a query that never wrote
// "import schema" still gets a typed result, and one that imported an
// unrelated schema does not get its result assessed against that instead.
// What "import schema" decides is the separate question of whether the query
// may *name* j:mapType in a sequence type.
//
// Identity constraints are skipped, as in xslt: the schema's xs:unique on a
// map's keys is scoped to a validation root the function built and nothing
// else can see, and the duplicates option has already settled what happens to
// a repeated key.
func (jsonTreeValidator) ValidateJSONTree(doc *xdm.Node) error {
	schema, err := xsd.SchemaForJSON()
	if err != nil {
		return xdm.Errorf("FOJS0004", "%s", err.Error())
	}
	if err := schema.Validate(doc, xsd.ValidateOptions{
		Annotate:          true,
		SkipIDConstraints: true,
	}); err != nil {
		return fmt.Errorf(
			"the XML representation of the JSON input is not valid against "+
				"the schema for fn:json-to-xml: %w", err)
	}
	return nil
}
