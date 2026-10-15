package xsd_test

// The xsdnet.HTTPResolver limits at their edges, kept with the other limit
// boundary tests (limits_boundary_test.go) but in their own file and the
// external test package, because HTTPResolver lives in package xsdnet, which
// imports xsd.

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xsd/xsdnet"
)

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
			// httptest binds to loopback, which HTTPResolver refuses by
			// default; this test is about the MaxBytes boundary, not the
			// address policy.
			r := &xsdnet.HTTPResolver{MaxBytes: tt.max, AllowPrivateAddresses: true}
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

	// See the note in TestHTTPResolverMaxBytesBoundaries: loopback is
	// refused by default and this test is not about that.
	r := &xsdnet.HTTPResolver{MaxBytes: -1, AllowPrivateAddresses: true}
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

// checkBoundaryErr is limits_boundary_test.go's helper, which this external
// test package cannot reach.
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
