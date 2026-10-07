package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// paramDoc parses an output:serialization-parameters element around body.
func paramDoc(t *testing.T, body string) *xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(`<output:serialization-parameters `+
		`xmlns:output="http://www.w3.org/2010/xslt-xquery-serialization" `+
		`xmlns:f="urn:f">`+body+`</output:serialization-parameters>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return docElement(tree.Root)
}

// TestParameterDocumentChecksValues pins that a parameter document is held to
// the schema fn:serialize holds its element form to (Serialization 3.1
// appendix B): a value outside the parameter's type is SEPM0017, and
// build-tree -- an xsl:output attribute, not a serialization parameter -- is
// refused. standalone="maybe" used to reach the
// XML declaration as written.
func TestParameterDocumentChecksValues(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{`<output:standalone value="maybe"/>`, "SEPM0017"},
		{`<output:indent value="maybe"/>`, "SEPM0017"},
		{`<output:method value="foo"/>`, "SEPM0017"},
		{`<output:json-node-output-method value="json"/>`, "SEPM0017"},
		{`<output:html-version value="five"/>`, "SEPM0017"},
		{`<output:encoding value="UTF 8"/>`, "SEPM0017"},
		{`<output:omit-xml-declaration value="omit"/>`, "SEPM0017"},
		{`<output:cdata-section-elements value="::x"/>`, "SEPM0017"},
		{`<output:build-tree value="yes"/>`, "SEPM0017"},
		{`<output:use-character-maps value="x"/>`, "SEPM0017"},
	} {
		var o OutputSettings
		err := ApplyParameterDocument(paramDoc(t, tc.body), &o)
		if err == nil || !strings.HasPrefix(err.Error(), tc.code) {
			t.Errorf("%s: got %v, want %s", tc.body, err, tc.code)
		}
	}
	// A value in the type is normalised, not refused.
	var o OutputSettings
	if err := ApplyParameterDocument(paramDoc(t,
		`<output:standalone value=" true "/><output:method value="Q{}text"/>`), &o); err != nil {
		t.Fatal(err)
	}
	if o.Standalone != "yes" || o.Method != "text" {
		t.Errorf("standalone=%q method=%q, want yes and text", o.Standalone, o.Method)
	}
	// So is an output declaration's value, with SEPM0016.
	if err := SetSerializationParam(&o, "standalone", "maybe"); err == nil ||
		!strings.HasPrefix(err.Error(), "SEPM0016") {
		t.Errorf("declared standalone=maybe: got %v, want SEPM0016", err)
	}
}

// TestParameterDocumentCharacterMapSchema pins the two places the readers
// were stricter than the schema: map-string is optional (§3.1 reads it with
// string(), so a missing one maps to ""), and an element in another namespace
// may follow the maps.
func TestParameterDocumentCharacterMapSchema(t *testing.T) {
	var o OutputSettings
	if err := ApplyParameterDocument(paramDoc(t, `<output:use-character-maps>`+
		`<output:character-map character="a"/><f:ext/></output:use-character-maps>`), &o); err != nil {
		t.Fatal(err)
	}
	if to, ok := o.InlineCharMap['a']; !ok || to != "" {
		t.Errorf("map=%v, want a mapped to the empty string", o.InlineCharMap)
	}
}
