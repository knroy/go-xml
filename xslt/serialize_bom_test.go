package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// bom is the byte order mark, written as an escape because Go rejects the
// character itself anywhere but the very start of a source file.
const bom = "\uFEFF"

// TestByteOrderMarkEveryMethod pins byte-order-mark="yes" to every output
// method, not only the XML-family ones.
//
// XSLT 3.0 §27.1 says the parameter decides "whether a byte order mark is
// written at the start of the file" and confines it to no method. The json and
// adaptive methods return from serialize() before the BOM the other methods
// write, so a request for one there was accepted and silently dropped -- no
// mark, and no error to say so. Every byte-order-mark case in the W3C suite
// selects xml or xhtml, which is why nothing caught it.
func TestByteOrderMarkEveryMethod(t *testing.T) {
	for _, method := range []string{"xml", "xhtml", "html", "text", "json", "adaptive"} {
		t.Run(method, func(t *testing.T) {
			var sb strings.Builder
			opts := OutputSettings{
				Method:        method,
				ByteOrderMark: true,
				OmitXMLDecl:   true,
			}
			if err := Serialize(&sb, xdm.One(xdm.NewString("hi")), opts, nil); err != nil {
				t.Fatalf("Serialize: %v", err)
			}
			if got := sb.String(); !strings.HasPrefix(got, bom) {
				t.Errorf("byte-order-mark=yes wrote no mark for method=%q: %q",
					method, got)
			}
		})
	}
}

// TestByteOrderMarkPrecedesAdaptiveDeclaration pins the ORDER for the adaptive
// method, which writes an XML declaration of its own.
//
// The mark is what tells a reader how to decode the declaration, so it has to
// come first. Writing it after would produce a declaration the reader cannot
// find the encoding of.
func TestByteOrderMarkPrecedesAdaptiveDeclaration(t *testing.T) {
	var sb strings.Builder
	opts := OutputSettings{Method: "adaptive", ByteOrderMark: true}
	if err := Serialize(&sb, xdm.One(xdm.NewString("hi")), opts, nil); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if got := sb.String(); !strings.HasPrefix(got, bom+"<?xml") {
		t.Errorf("the mark does not precede the adaptive declaration: %q", got)
	}
}

// TestUnsupportedHTMLVersionIsReported pins SESU0013 to the EFFECTIVE html
// version rather than to @version alone.
//
// XSLT 3.0 §27.1 on html-version: "The set of permitted values, and the
// default value, are implementation-defined. A serialization error will be
// reported if the requested version is not supported by the implementation."
// Its Note adds that the html method falls back to @version only when
// html-version is absent -- so html-version is the parameter that decides, and
// it is the one a 3.0 stylesheet writes. The check read @version instead, so
// html-version="7" was accepted and quietly served HTML 4 rules.
func TestUnsupportedHTMLVersionIsReported(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    OutputSettings
		wantErr bool
	}{
		{"version unsupported", OutputSettings{Method: "html", Version: "7"}, true},
		{"html-version unsupported", OutputSettings{Method: "html", HTMLVersion: "7"}, true},
		{"html-version supported", OutputSettings{Method: "html", HTMLVersion: "4.01"}, false},
		{"html-version 5", OutputSettings{Method: "html", HTMLVersion: "5"}, false},
		// html-version overrides version, so a supported one silences an
		// unsupported fallback that no longer applies.
		{"html-version wins", OutputSettings{Method: "html", HTMLVersion: "5", Version: "7"}, false},
		// For xhtml, @version is the version of XML and html-version is not
		// checked against the html method's table.
		{"xhtml untouched", OutputSettings{Method: "xhtml", HTMLVersion: "7"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			err := Serialize(&sb, xdm.One(xdm.NewString("hi")), tc.opts, nil)
			if got := err != nil; got != tc.wantErr {
				t.Fatalf("error=%v, want error=%v (err=%v)", got, tc.wantErr, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "SESU0013") {
				t.Errorf("want SESU0013, got %v", err)
			}
		})
	}
}
