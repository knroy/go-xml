package xsdnet

import (
	"io"
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
