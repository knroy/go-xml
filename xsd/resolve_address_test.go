package xsd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// TestHTTPResolverRefusesPrivateAddresses is the address half of the SSRF
// guard: AllowHost is an allowlist of names, and a permitted name may resolve
// to loopback, to link-local, or into a private range. The refusal has to
// happen against the resolved address, which is what this asserts.
//
// httptest binds to 127.0.0.1, so a local server is itself the case: the test
// cannot use one at all unless the opt-out works, which is why both directions
// are checked here rather than only the refusal.
func TestHTTPResolverRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`))
	}))
	defer srv.Close()

	t.Run("refused by default", func(t *testing.T) {
		var r HTTPResolver
		rc, _, err := r.Resolve("", srv.URL+"/s.xsd", "")
		if rc != nil {
			rc.Close()
			t.Fatal("fetched a loopback address: the default must refuse it, " +
				"or a schemaLocation naming 127.0.0.1 reaches services bound " +
				"to the host itself")
		}
		if !errors.Is(err, ErrPrivateAddress) {
			t.Fatalf("err = %v, want it to wrap ErrPrivateAddress", err)
		}
	})

	t.Run("opt-out re-permits", func(t *testing.T) {
		r := HTTPResolver{AllowPrivateAddresses: true}
		rc, _, err := r.Resolve("", srv.URL+"/s.xsd", "")
		if err != nil {
			t.Fatalf("AllowPrivateAddresses did not re-permit the fetch: %v", err)
		}
		defer rc.Close()
		b := make([]byte, 64)
		n, _ := rc.Read(b)
		if !strings.Contains(string(b[:n]), "xs:schema") {
			t.Fatalf("body = %q, want the served schema", b[:n])
		}
	})

	// Supplying a Client is the common way to set a timeout, and it leaves
	// Transport nil. The filter must still apply there, or the guard is off
	// for most callers who touch the field at all.
	t.Run("caller client without a transport is still filtered", func(t *testing.T) {
		r := HTTPResolver{Client: &http.Client{}}
		rc, _, err := r.Resolve("", srv.URL+"/s.xsd", "")
		if rc != nil {
			rc.Close()
			t.Fatal("a Client with a nil Transport bypassed the address filter")
		}
		if !errors.Is(err, ErrPrivateAddress) {
			t.Fatalf("err = %v, want it to wrap ErrPrivateAddress", err)
		}
	})

	// A caller's own Transport is left alone: that is the documented hook for
	// a proxy or a pinned CA set, and silently wrapping it would override a
	// policy the caller set deliberately.
	t.Run("caller transport is not overridden", func(t *testing.T) {
		r := HTTPResolver{Client: &http.Client{Transport: http.DefaultTransport}}
		rc, _, err := r.Resolve("", srv.URL+"/s.xsd", "")
		if err != nil {
			t.Fatalf("caller-supplied transport was overridden: %v", err)
		}
		rc.Close()
	})
}

// TestIsPrivateAddr pins the ranges, including the ones a reader is most
// likely to think are ordinary public addresses.
func TestIsPrivateAddr(t *testing.T) {
	private := []string{
		"127.0.0.1", "127.1.2.3", "::1",
		// The cloud instance metadata service. This is the single address the
		// filter most exists to refuse: reaching it returns credentials.
		"169.254.169.254", "169.254.0.1",
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"fc00::1", "fd12:3456::1",
		"0.0.0.0", "::",
		// IPv4-mapped IPv6. Judged after unmapping, or ::ffff:127.0.0.1
		// passes as an ordinary global v6 address.
		"::ffff:127.0.0.1", "::ffff:169.254.169.254",
		"100.64.0.1", "100.127.255.255", // carrier-grade NAT
		"192.0.0.1",  // IETF protocol assignments
		"224.0.0.1",  // multicast
		"fe80::1",    // v6 link-local
	}
	for _, s := range private {
		ip, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", s, err)
		}
		if !isPrivateAddr(ip) {
			t.Errorf("isPrivateAddr(%s) = false, want true", s)
		}
	}

	public := []string{
		"8.8.8.8", "1.1.1.1", "93.184.216.34",
		"2001:4860:4860::8888",
		// Adjacent to but outside the private ranges, so an over-broad mask
		// shows up here rather than as a silently unreachable public host.
		"172.15.255.255", "172.32.0.1", "11.0.0.1",
		"100.63.255.255", "100.128.0.1",
		"192.0.1.1",
	}
	for _, s := range public {
		ip, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", s, err)
		}
		if isPrivateAddr(ip) {
			t.Errorf("isPrivateAddr(%s) = true, want false", s)
		}
	}
}
