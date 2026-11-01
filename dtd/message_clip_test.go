package dtd

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A value quoted in a message is clipped: 200 mismatches against a 1 MB
// #FIXED value kept 105 MB in the errors. The message keeps its prefix.
func TestValidityMessagesClipLongValues(t *testing.T) {
	big := strings.Repeat("v", 1<<20)
	d, err := Parse(`<!DOCTYPE r [<!ELEMENT r ANY><!ELEMENT e EMPTY>
	<!ATTLIST e a CDATA #FIXED "` + big + `">]>`)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString("<r>"+strings.Repeat(`<e a="w"/>`, 200)+"</r>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var errs *Errors
	if err := Validate(tree.Root, d, Options{}); !errors.As(err, &errs) || len(errs.Errors) == 0 {
		t.Fatalf("expected Errors, got %v", err)
	}
	for _, e := range errs.Errors {
		if len(e.Message) > 1024+len("...") {
			t.Fatalf("message is %d bytes, want at most 1027", len(e.Message))
		}
		if !strings.HasPrefix(e.Message, `attribute a is #FIXED "vvvv`) || !strings.HasSuffix(e.Message, "...") {
			t.Fatalf("message lost its prefix or its clip marker: %.200q", e.Message)
		}
	}
}
