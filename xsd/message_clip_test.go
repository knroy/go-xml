package xsd

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A name or value the document supplies is clipped before it reaches a
// ValidationError. 200 foreign attributes in a 1 MB namespace kept 105 MB in
// the errors and allocated 423 MB building them; clipped, under 1 MB of each.
// The allocation bound catches attrName and fail independently: without the
// clip in attrName it is about 212 MB, without the one in fail about 105 MB.
func TestValidationMessagesClipDocumentStrings(t *testing.T) {
	s := mustParseSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root"><xs:complexType/></xs:element>
	</xs:schema>`)
	uri := strings.Repeat("u", 1<<20)
	var b strings.Builder
	b.WriteString(`<root xmlns:f="` + uri + `"`)
	for i := 0; i < 200; i++ {
		b.WriteString(` f:a` + benchItoa(i) + `="x"`)
	}
	b.WriteString(`/>`)
	tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	err = s.Validate(tree.Root, ValidateOptions{})
	runtime.ReadMemStats(&m1)
	var ve *ValidationErrors
	if !errors.As(err, &ve) || len(ve.Errors) == 0 {
		t.Fatalf("expected ValidationErrors, got %v", err)
	}
	for _, e := range ve.Errors {
		if len(e.Message) > maxMessage+len("...") {
			t.Fatalf("message is %d bytes, want at most %d", len(e.Message), maxMessage+3)
		}
		if !strings.Contains(e.Message, "{uuuu") || !strings.Contains(e.Message, "...") {
			t.Fatalf("message lost its prefix or its clip marker: %.200q", e.Message)
		}
	}
	if got := m1.TotalAlloc - m0.TotalAlloc; got > 16<<20 {
		t.Errorf("validation allocated %d MB, want under 16 MB", got>>20)
	}
}
