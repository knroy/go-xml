//go:build !goxml_nohttp

package xsd

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPResolverHostPolicy(t *testing.T) {
	// AllowHost runs before the request, so a refused host means no
	// connection is attempted at all — which is what makes it usable to
	// block loopback and link-local addresses.
	r := &HTTPResolver{AllowHost: func(host string) bool { return host == "good.example" }}
	if _, _, err := r.Resolve("", "http://bad.example/s.xsd", ""); err == nil {
		t.Error("a host outside the policy should be refused")
	}
}

// TestHTTPResolverChecksRedirectHosts pins that AllowHost governs every host
// actually contacted, not only the one the schema names.
//
// A redirect is a second request to a host the caller never wrote down, and
// the check ran once, before the first request. So a document on a permitted
// host that answered 302 had the redirect followed and the body returned —
// the SSRF the field exists to prevent, reachable through any open redirector
// on an allowed host. The returned path was the original URL too, so a caller
// logging it never learned where the bytes came from.
func TestHTTPResolverChecksRedirectHosts(t *testing.T) {
	secret := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `<a/>`)
		}))
	defer secret.Close()

	// httptest binds 127.0.0.1; "localhost" is the same address under a
	// name the policy refuses, which is the point — the hop is what is
	// being checked, not the address.
	denied := strings.Replace(secret.URL, "127.0.0.1", "localhost", 1)
	open := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, denied+"/secret.xsd", http.StatusFound)
		}))
	defer open.Close()

	var asked []string
	// The servers are on loopback, which the dialler refuses by default;
	// this test is about AllowHost running on every redirect hop.
	r := &HTTPResolver{AllowPrivateAddresses: true, AllowHost: func(h string) bool {
		asked = append(asked, h)
		return h == "127.0.0.1"
	}}

	if _, _, err := r.Resolve("", open.URL+"/a.xsd", ""); err == nil {
		t.Error("a redirect to a host outside the policy should be refused")
	} else if !strings.Contains(err.Error(), "localhost") {
		t.Errorf("error %q does not name the host that was refused", err)
	}
	if len(asked) < 2 {
		t.Errorf("AllowHost was consulted for %v; it must run on every hop", asked)
	}

	// A redirect that stays inside the policy still works, and the path
	// reports where the document actually came from.
	inside := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a.xsd" {
				http.Redirect(w, r, "/real.xsd", http.StatusFound)
				return
			}
			io.WriteString(w, `<a/>`)
		}))
	defer inside.Close()

	rc, path, err := r.Resolve("", inside.URL+"/a.xsd", "")
	if err != nil {
		t.Fatalf("a redirect within the policy should be followed: %v", err)
	}
	rc.Close()
	if !strings.HasSuffix(path, "/real.xsd") {
		t.Errorf("path is %q, want the document's real origin", path)
	}
}

func TestHTTPResolverFallsBackToFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "local.xsd")
	if err := os.WriteFile(p, []byte(`<a/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &HTTPResolver{}
	rc, _, err := r.Resolve("", "local.xsd", filepath.Join(dir, "main.xsd"))
	if err != nil {
		t.Fatalf("a local location should still resolve: %v", err)
	}
	rc.Close()
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
			// httptest binds to loopback, which HTTPResolver refuses by
			// default; this test is about the MaxBytes boundary, not the
			// address policy.
			r := &HTTPResolver{MaxBytes: tt.max, AllowPrivateAddresses: true}
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
	r := &HTTPResolver{MaxBytes: -1, AllowPrivateAddresses: true}
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
