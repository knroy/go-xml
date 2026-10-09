//go:build !goxml_nohttp

// HTTPResolver is the one part of the module that needs net/http, which
// brings TLS and x509 into every binary that links xsd. Building with
// -tags goxml_nohttp leaves this file out: a program that never fetches a
// schema over the network (the go-xml CLI is one) is smaller and starts
// faster, and FileResolver still refuses remote locations as before.

package xsd

import (
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// HTTPResolver resolves a schemaLocation over the network, falling back to the
// filesystem for locations that are not remote.
//
// Network resolution is off by default and this type is how a caller turns it
// on, because it hands control of what the process fetches to whoever wrote the
// schema. That is a considered trade rather than a scary-sounding one: a schema
// naming http://internal/admin makes the validator fetch it, and a
// schemaLocation taken from an instance document lets whoever supplied the
// document choose the schema it is judged against.
//
// The zero value is usable and applies the default limits.
type HTTPResolver struct {
	// Client fetches remote documents. When nil, a client with Timeout is
	// used. Supplying one is the hook for a caller that needs a proxy, a
	// pinned CA set, or a transport that refuses private address ranges.
	Client *http.Client

	// Timeout bounds a single fetch. Zero means DefaultFetchTimeout.
	Timeout time.Duration

	// MaxBytes bounds a fetched document. Zero means DefaultMaxSchemaBytes.
	// A schema is not a stream, so an unbounded read is a way to be handed
	// an unbounded allocation.
	MaxBytes int64

	// AllowHost, when non-nil, reports whether a host may be fetched from.
	// It runs before the request, and it is an allowlist of *names*.
	//
	// It is not an address check and must not be relied on as one. A name it
	// admits may resolve to loopback, link-local or a private range, and a
	// name checked here may resolve to something else by the time the
	// connection is made — DNS rebinding defeats a name check by
	// construction. Returning true for "schemas.example.com" says the name is
	// permitted, not that the connection goes anywhere trustworthy.
	//
	// The addresses themselves are refused by the dialler, at the point they
	// are known — see AllowPrivateAddresses. Use AllowHost to narrow the
	// namespace and the dialler to enforce the boundary.
	AllowHost func(host string) bool

	// AllowPrivateAddresses re-permits the address ranges that are refused by
	// default: loopback, link-local (including 169.254.169.254, the cloud
	// instance metadata address), unique-local, and the RFC1918 private
	// ranges. It is off by default, so the zero value refuses them.
	//
	// The check runs in the dialler, against the IP the connection is
	// actually being made to rather than the name written in the document.
	// That placement is what makes it a guarantee: a name resolves to an
	// address only at dial time, so checking the name earlier leaves a
	// rebinding window in which the name is re-resolved to a refused address
	// after it was approved. Checking the resolved address closes it, and it
	// covers redirects and every retry for free, because each connection is
	// dialled through the same place.
	//
	// Turn it on for a caller that genuinely fetches schemas from a private
	// network — an internal mirror, or a test server on loopback. It widens
	// what the process can be made to reach by whoever writes the schema, so
	// it is opt-in rather than a default.
	AllowPrivateAddresses bool

	// Files handles locations that are not remote. When nil, a FileResolver
	// with no root is used.
	Files Resolver
}

// Resolve implements Resolver.
func (r *HTTPResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if location == "" {
		return nil, "", nil
	}

	abs := location
	if base != "" {
		if b, err := url.Parse(base); err == nil {
			if u, err := url.Parse(location); err == nil {
				abs = b.ResolveReference(u).String()
			}
		}
	}
	if !isRemote(abs) {
		files := r.Files
		if files == nil {
			files = &FileResolver{}
		}
		return files.Resolve(namespace, location, base)
	}

	u, err := url.Parse(abs)
	if err != nil {
		return nil, "", fmt.Errorf("schemaLocation %q: %w", location, err)
	}
	if r.AllowHost != nil && !r.AllowHost(u.Hostname()) {
		return nil, "", fmt.Errorf(
			"schemaLocation %q: host %q is not permitted", location, u.Hostname())
	}

	client := r.Client
	if client == nil {
		timeout := r.Timeout
		if timeout == 0 {
			timeout = DefaultFetchTimeout
		}
		client = &http.Client{Timeout: timeout}
	}
	// The address filter goes on a copy of the transport, for the same reason
	// the redirect check goes on a copy of the client: r.Client may be one the
	// caller uses elsewhere, and installing a dialler on it would change the
	// behaviour of every other request made through it.
	//
	// A caller who supplied their own Transport has already chosen how
	// connections are made, so theirs is left alone -- that is the documented
	// hook for a proxy or a pinned CA set, and wrapping it here would silently
	// override a policy they set deliberately.
	if !r.AllowPrivateAddresses {
		c := *client
		if c.Transport == nil {
			c.Transport = newGuardedTransport()
			client = &c
		}
	}
	// A redirect is a second request to a host the caller never named, so
	// AllowHost has to run again on every hop. Checking only the URL written
	// in the schema left the policy trivially bypassed: a document on a
	// permitted host that answers 302 had the redirect followed and the body
	// returned, which is exactly the SSRF AllowHost exists to prevent.
	//
	// The check is installed on a copy, because r.Client may be a caller's
	// client used elsewhere and CheckRedirect is a field on the client
	// rather than the request.
	if r.AllowHost != nil {
		c := *client
		outer := c.CheckRedirect
		c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if !r.AllowHost(req.URL.Hostname()) {
				return fmt.Errorf(
					"schemaLocation %q: redirected to host %q, which is not permitted",
					location, req.URL.Hostname())
			}
			if outer != nil {
				return outer(req, via)
			}
			// net/http's own default, which this replaces.
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		}
		client = &c
	}

	resp, err := client.Get(abs)
	if err != nil {
		return nil, "", fmt.Errorf("fetching schema %q: %w", abs, err)
	}
	// The document's real origin, not the location that named it: a redirect
	// changes where relative references inside it resolve against, and a
	// caller logging the returned path should see where the bytes came from.
	if resp.Request != nil && resp.Request.URL != nil {
		abs = resp.Request.URL.String()
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf(
			"fetching schema %q: HTTP %s", abs, resp.Status)
	}

	max := r.MaxBytes
	if max == 0 {
		max = DefaultMaxSchemaBytes
	}
	// The limit is one byte over so that hitting it is distinguishable from
	// a document that happens to be exactly the maximum size.
	// max+1 overflows to a negative limit at math.MaxInt64, which
	// io.LimitReader reads as "nothing left": the largest limit a caller can
	// name returned an empty body with a nil error, which is worse than
	// refusing it. Saturate instead.
	lim := max
	if lim < math.MaxInt64 {
		lim++
	}
	body := &limitedBody{r: io.LimitReader(resp.Body, lim), c: resp.Body, max: max, url: abs}
	return body, abs, nil
}

// limitedBody fails the read that exceeds the size limit, rather than
// truncating silently. A truncated schema would parse as a different, smaller
// schema, which is a worse outcome than an error.
type limitedBody struct {
	r   io.Reader
	c   io.Closer
	max int64
	n   int64
	url string
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += int64(n)
	if b.n > b.max {
		return n, fmt.Errorf("schema %q exceeds %d bytes", b.url, b.max)
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.c.Close() }

// newGuardedTransport returns a Transport that refuses to connect to the
// address ranges an SSRF is aimed at.
//
// The check is in Control rather than DialContext because Control runs after
// the name has been resolved and after the address to dial has been chosen,
// but before the connection is made. That is the narrowest point at which the
// real address is known: a host with several A records is checked per address
// as each is tried, so a name that resolves to both a public and a loopback
// address cannot reach the loopback one by having the first attempt fail. It
// is also why this closes the DNS-rebinding window that a name check cannot:
// the address seen here is the one being connected to, not one resolved
// earlier and re-resolved since.
func newGuardedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	d := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("parsing dial address %q: %w", address, err)
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				// Control is handed a literal address, never a name. If that
				// ever stops being true, refusing is the safe direction:
				// admitting an address that could not be parsed would let
				// whatever produced it past the check.
				return fmt.Errorf("dial address %q is not an IP: %w", host, err)
			}
			if isPrivateAddr(ip) {
				return fmt.Errorf("refusing to dial %s: %w", ip, ErrPrivateAddress)
			}
			return nil
		},
	}
	t.DialContext = d.DialContext
	return t
}

// isPrivateAddr reports whether ip is in a range HTTPResolver refuses by
// default.
//
// The IPv4-mapped form of an IPv6 address is unmapped first, so
// ::ffff:127.0.0.1 is judged as 127.0.0.1 rather than slipping through as an
// ordinary global v6 address.
func isPrivateAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback(), ip.IsUnspecified(),
		ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		// Loopback reaches services bound to the host itself. The
		// unspecified address (0.0.0.0, ::) is a route to the local host on
		// most stacks. Link-local covers 169.254.0.0/16, which is where the
		// cloud instance metadata service lives (169.254.169.254) and is the
		// single address this filter most exists to refuse.
		return true
	case ip.IsPrivate():
		// 10/8, 172.16/12, 192.168/16, and the v6 unique-local fc00::/7.
		return true
	}
	if ip.Is4() {
		b := ip.As4()
		// 100.64.0.0/10, the carrier-grade NAT range, which addresses other
		// tenants rather than the public internet.
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return true
		}
		// 192.0.0.0/24, IETF protocol assignments.
		if b[0] == 192 && b[1] == 0 && b[2] == 0 {
			return true
		}
	}
	return false
}

// The resolvers a refusal names, and where it points for remote locations.
// The goxml_nohttp build has its own wording, since HTTPResolver is absent.
const (
	resolverChoices = "a FileResolver, a MapResolver or an HTTPResolver"
	remoteHint      = "see HTTPResolver"
)
