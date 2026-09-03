package xsd

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Every caller-settable limit in this package at its edges: zero, negative,
// one, exactly at the limit, exactly one over, and the largest values the type
// holds. See xdm/limits_boundary_test.go for why this class of test exists --
// the MaxBytes overflow it was written for hit HTTPResolver too, where it was
// worse: max+1 wrapped negative, io.LimitReader read that as "nothing left",
// and the resolver returned an EMPTY BODY WITH A NIL ERROR, so a schema loaded
// silently as empty rather than failing.

// depthSchema recurses: <r> may contain an <r>. It lets a document of any
// depth be built against one declaration.
const depthSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="r">
    <xs:complexType><xs:sequence>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
  </xs:element>
</xs:schema>`

func loadBoundarySchema(t *testing.T, src string) *Schema {
	t.Helper()
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing schema: %v", err)
	}
	s, err := Load(doc.Root, "", Options{})
	if err != nil {
		t.Fatalf("loading schema: %v", err)
	}
	return s
}

func TestValidateMaxDepthBoundaries(t *testing.T) {
	s := loadBoundarySchema(t, depthSchema)

	const depth = 5
	src := strings.Repeat("<r>", depth) + strings.Repeat("</r>", depth)
	// The parser's own MaxDepth is a separate knob on a separate call, so it
	// is left at its default here; five levels is far below it.
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}

	tests := []struct {
		name    string
		max     int
		wantErr string
	}{
		// Deliberate: zero means DefaultMaxDepth (1000), which matches
		// xdm.DefaultMaxDepth so a document the parser accepts is not then
		// refused by the validator for depth alone.
		{"zero is the default", 0, ""},
		// Deliberate, and NOT the same as xdm.ParseOptions.MaxDepth: the field
		// documents "a negative value means no limit", and validate.go
		// implements it with a `maxDepth > 0` guard. docs/options.md's blanket
		// claim that MaxDepth is everywhere the exception to the negative rule
		// is true of xdm only.
		{"negative is unlimited", -1, ""},
		{"the smallest limit refuses", 1, "element nesting exceeds 1 levels"},
		{"one under refuses", depth - 1, "element nesting exceeds 4 levels"},
		{"exactly at the limit is accepted", depth, ""},
		{"one over is accepted", depth + 1, ""},
		{"MaxInt does not overflow", math.MaxInt, ""},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Validate(doc.Root, ValidateOptions{MaxDepth: tt.max})
			checkBoundaryErr(t, err, tt.wantErr)
		})
	}
}

func TestValidateMaxErrorsBoundaries(t *testing.T) {
	// An element declared with empty content, so each child is one failure.
	s := loadBoundarySchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="r"><xs:complexType><xs:sequence/></xs:complexType></xs:element>
</xs:schema>`)

	doc, err := xdm.ParseString(`<r><a/><b/><c/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}

	// MaxErrors bounds how many failures are REPORTED, so the assertion is on
	// the count rather than on whether an error appeared at all. A document
	// this wrong must never validate clean at any of these settings.
	tests := []struct {
		name    string
		max     int
		wantMax int // the most errors that may be reported
	}{
		// Deliberate: zero means DefaultMaxErrors (100), well above what this
		// document produces, so every failure is reported.
		{"zero is the default", 0, DefaultMaxErrors},
		{"one reports exactly one", 1, 1},
		{"two reports at most two", 2, 2},
		{"a limit above the failure count reports them all", 100, 100},
		{"MaxInt does not overflow", math.MaxInt, math.MaxInt},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, math.MaxInt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Validate(doc.Root, ValidateOptions{MaxErrors: tt.max})
			if err == nil {
				t.Fatal("an invalid document validated clean")
			}
			ve, ok := err.(*ValidationErrors)
			if !ok {
				t.Fatalf("error is %T, want *ValidationErrors: %v", err, err)
			}
			if n := len(ve.Errors); n == 0 || n > tt.wantMax {
				t.Errorf("reported %d errors, want between 1 and %d", n, tt.wantMax)
			}
		})
	}
}

// A negative MaxErrors makes an invalid document validate as clean.
//
// BUG, recorded rather than fixed: fixing it is engine code, and this task is
// tests. The field's sibling in the dtd package -- dtd.Options.MaxErrors --
// documents "a negative value means no limit" and implements it with a
// `v.max > 0 &&` guard in dtd/validate.go. xsd/validate.go's fail() has no such
// guard:
//
//	if len(v.errs) >= v.opts.MaxErrors {   // xsd/validate.go
//		v.stopped = true
//		return
//	}
//
// With MaxErrors negative, `0 >= -1` holds on the very first failure, so
// validation stops before recording anything and Validate returns nil. The
// caller is told the document is valid.
//
// This is a silent pass, the same failure shape as the HTTPResolver overflow:
// the dangerous outcome is not an error, it is the absence of one. A caller
// copying the "-1 means no limit" idiom that works in xdm.ParseOptions and in
// dtd.Options gets a validator that approves everything.
//
// The fix is a `v.opts.MaxErrors > 0 &&` guard, matching dtd. Alternatively,
// document that negative is not supported here -- but silently approving
// invalid documents is not an acceptable reading of any value.
func TestValidateMaxErrorsNegativeApprovesInvalidDocuments(t *testing.T) {

	s := loadBoundarySchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="r"><xs:complexType><xs:sequence/></xs:complexType></xs:element>
</xs:schema>`)
	doc, err := xdm.ParseString(`<r><nope/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing instance: %v", err)
	}
	if err := s.Validate(doc.Root, ValidateOptions{MaxErrors: -1}); err == nil {
		t.Error("MaxErrors=-1 validated an invalid document clean; " +
			"negative means no limit in dtd.Options.MaxErrors and should here too")
	}
}

func TestHTTPResolverMaxBytesBoundaries(t *testing.T) {
	const body = "0123456789"
	const size = int64(len(body))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	tests := []struct {
		name    string
		max     int64
		wantErr string
	}{
		// Deliberate: zero means DefaultMaxSchemaBytes (16 MB).
		{"zero is the default", 0, ""},
		{"the smallest limit refuses", 1, "exceeds 1 bytes"},
		{"one under refuses", size - 1, "exceeds 9 bytes"},
		{"exactly at the limit is accepted", size, ""},
		{"one over is accepted", size + 1, ""},
		// The overflow. max+1 wrapped to a negative io.LimitReader limit and
		// the body came back empty with a nil error -- a schema that loaded as
		// if it declared nothing.
		{"MaxInt64 does not overflow", math.MaxInt64, ""},
		{"MaxInt64-1 is its neighbour", math.MaxInt64 - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &HTTPResolver{MaxBytes: tt.max}
			rc, _, err := r.Resolve("", srv.URL, "")
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			defer rc.Close()
			got, err := io.ReadAll(rc)
			checkBoundaryErr(t, err, tt.wantErr)
			// The silent-truncation guard: an accepted fetch must deliver the
			// whole document, not a prefix of it. Asserting only on the error
			// would have passed the very bug this test exists for, which
			// returned zero bytes and no error at all.
			if tt.wantErr == "" && string(got) != body {
				t.Errorf("body is %q (%d bytes), want %q (%d bytes)",
					got, len(got), body, len(body))
			}
		})
	}
}

// A negative MaxBytes refuses every fetch rather than meaning "no limit".
//
// Deliberate as far as the field is concerned: HTTPResolver.MaxBytes documents
// only that zero means DefaultMaxSchemaBytes, and adds that "a schema is not a
// stream, so an unbounded read is a way to be handed an unbounded allocation"
// -- so there is deliberately no unlimited setting here, unlike
// xdm.ParseOptions.MaxBytes where the caller is reading its own input.
//
// Pinned rather than changed. It is a refusal with a clear error naming the
// limit, which is the safe direction to fail in; the failure mode worth
// preventing is the opposite one, a negative limit silently admitting
// everything. docs/options.md now records that the "-1 means no limit" rule
// does not reach this field.
func TestHTTPResolverMaxBytesNegativeRefuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "0123456789")
	}))
	defer srv.Close()

	r := &HTTPResolver{MaxBytes: -1}
	rc, _, err := r.Resolve("", srv.URL, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err == nil {
		t.Fatalf("MaxBytes=-1 accepted %d bytes; want a refusal", len(got))
	}
	if !strings.Contains(err.Error(), "exceeds -1 bytes") {
		t.Errorf("error %q does not name the limit", err)
	}
}

// checkBoundaryErr mirrors xdm's checkLimit: a refusal must name the limit
// that fired, because err != nil alone also passes when the wrong limit trips.
func checkBoundaryErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("accepted input was refused: %v", err)
	case want != "" && err == nil:
		t.Errorf("input was accepted; want an error matching %q", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Errorf("error %q does not name the limit; want it to contain %q", err, want)
	}
}
