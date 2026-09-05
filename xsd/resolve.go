package xsd

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A Resolver turns a schemaLocation into schema source.
//
// The location is a hint, not an identifier: §4.3.2 lets a processor use a
// catalogue, a preloaded schema, or nothing at all. Making resolution an
// interface rather than a built-in fetch is what lets a caller decide, since
// following a location means fetching whatever a document names.
type Resolver interface {
	// Resolve returns the contents of the schema document at location,
	// which is resolved relative to base when it is not absolute. The
	// namespace is the one the reference declared, or empty for an include.
	//
	// Returning a nil reader and a nil error means "no schema document" —
	// which for an include is not an error (§4.2.1), and the caller
	// distinguishes the cases.
	Resolve(namespace, location, base string) (io.ReadCloser, string, error)
}

// noResolverConfigured is the default where no location has been granted.
//
// It answers every request with an error rather than with (nil, nil). The
// contract reads a nil reader and a nil error as "no schema document", which
// for an xs:include is not a failure — the include is dropped and assembly
// succeeds. That is right for a caller who deliberately hardened, and wrong as
// a default: a schema that silently lost half its components validates
// documents against the half that is left. So the refusal is loud.
type noResolverConfigured struct{}

// errNoResolver marks the refusal as a configuration fault rather than a
// location that merely could not be found.
//
// The distinction decides whether assembly carries on. §4.2.1 makes an
// unresolvable include a hint that missed — "no corresponding inclusion is
// performed" — and dropping it is right when a resolver looked and came back
// empty. It is wrong when no resolver was ever configured: the schema then
// loses components for a reason that has nothing to do with the schema, and
// reports success. queueRef tests for this and reports it.
var errNoResolver = errors.New("no Resolver is configured")

// errRefusedByPolicy marks a refusal the configuration made deliberately, as
// against a location that was looked for and not found.
//
// It travels with errNoResolver for the same reason: §4.2.1 lets an
// unresolvable include be dropped, and doing so is right for a location that
// simply is not there — a remote URL with no network resolver is the usual
// case, and the W3C suite's own metadata schema depends on it being tolerated.
// It is wrong for a location the configuration refused on purpose. Dropping
// that one means a caller who set a Root gets a schema quietly missing whatever
// sat outside it, and is told the load succeeded.
var errRefusedByPolicy = errors.New("refused by the resolver's configuration")

// Resolve implements Resolver.
func (noResolverConfigured) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if location == "" {
		return nil, "", nil
	}
	return nil, "", fmt.Errorf(
		"schemaLocation %q cannot be resolved: %w (Options.Resolver); pass "+
			"a FileResolver, a MapResolver or an HTTPResolver to say what "+
			"this schema may read", location, errNoResolver)
}

// multiRootFileResolver reads from any of several directories.
//
// LoadFiles is given a list of paths that need not share a parent, so the
// grant its default makes is the set of the directories the caller named. A
// location is offered to each in turn and the first that admits it wins.
type multiRootFileResolver struct {
	roots []*FileResolver
}

// Resolve implements Resolver.
func (r multiRootFileResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if location == "" {
		return nil, "", nil
	}
	var firstErr error
	for _, fr := range r.roots {
		rc, resolved, err := fr.Resolve(namespace, location, base)
		if err == nil {
			return rc, resolved, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, "", firstErr
}

// rootedFileResolver confines a default FileResolver to the directories the
// caller's own arguments named.
func rootedFileResolver(paths []string) Resolver {
	seen := map[string]bool{}
	var rs []*FileResolver
	for _, p := range paths {
		d := filepath.Dir(p)
		if seen[d] {
			continue
		}
		seen[d] = true
		rs = append(rs, &FileResolver{Root: d})
	}
	if len(rs) == 1 {
		return rs[0]
	}
	return multiRootFileResolver{roots: rs}
}

// FileResolver resolves a schemaLocation against the filesystem.
//
// It is the default because it is the case that cannot surprise anyone: a
// schema that includes a file beside it keeps working, and nothing leaves the
// machine.
type FileResolver struct {
	// Root, when set, confines resolution to a directory. A location that
	// escapes it — through "..", a symlink, or an absolute path — is
	// refused. Leaving it empty permits any readable path, which is the
	// right default for a command-line tool and the wrong one for a server.
	Root string
}

// Resolve implements Resolver.
func (r *FileResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if location == "" {
		return nil, "", nil
	}
	if isRemote(location) {
		return nil, "", fmt.Errorf(
			"schemaLocation %q is a remote URL and network resolution is not "+
				"enabled; see HTTPResolver", location)
	}

	// A location is a URI reference, so a file: URL and a bare path both
	// have to work.
	p := location
	if u, err := url.Parse(location); err == nil && u.Scheme == "file" {
		p = u.Path
	}

	if !filepath.IsAbs(p) && base != "" {
		p = filepath.Join(filepath.Dir(base), p)
	}
	p = filepath.Clean(p)

	if r.Root != "" {
		root, err := filepath.Abs(r.Root)
		if err != nil {
			return nil, "", err
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, "", err
		}
		// Symlinks are resolved on both sides before the comparison, or a
		// link planted inside the root reads whatever it points at: the
		// path passes the containment check and os.Open then follows the
		// link out. xslt's FileResolver has always done this; this one
		// documented it and did not.
		//
		// A path that does not exist keeps its unresolved form so that the
		// failure is a clean "no such file" from os.Open rather than an
		// error from EvalSymlinks. The root is resolved the same way, since
		// a root reached through a symlink would otherwise never match the
		// resolved target.
		//
		// This resolves and then opens, which is the shape a TOCTOU race
		// attacks — xslt's resolver uses os.OpenRoot instead and enforces at
		// open time. The window does not close here because what gets opened
		// is the RESOLVED path (p = abs below): every link has already been
		// followed, so a link that passed the check is not traversed again
		// and cannot be swung underneath. Exploiting what remains means
		// replacing a directory component of the resolved path between the
		// two steps, which needs write access inside the root — and an
		// attacker holding that can just write the file. The hostile party
		// here is a *document*, which names a location and cannot touch the
		// filesystem at all. See docs/security.md, "All resolution defaults
		// are closed".
		if x, err := filepath.EvalSymlinks(root); err == nil {
			root = x
		}
		if x, err := filepath.EvalSymlinks(abs); err == nil {
			abs = x
		}
		// The separator matters: without it, a Root of "/srv/a" would
		// also admit "/srv/anything".
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return nil, "", fmt.Errorf(
				"schemaLocation %q resolves outside the permitted root: %w",
				location, errRefusedByPolicy)
		}
		p = abs
	}

	f, err := os.Open(p)
	if err != nil {
		return nil, "", err
	}
	return f, p, nil
}

// isRemote reports whether a location names something to be fetched over the
// network rather than read from disk.
func isRemote(location string) bool {
	u, err := url.Parse(location)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp":
		return true
	}
	return false
}

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
	// To refuse the addresses an SSRF is aimed at, filter them where they are
	// known: a Transport with a DialContext (or Control) that inspects the
	// resolved IP and refuses the ranges you do not want. That check sees the
	// address actually being dialled, which is the only place the guarantee
	// can be made. Use AllowHost to narrow the namespace and the dialler to
	// enforce the boundary.
	AllowHost func(host string) bool

	// Files handles locations that are not remote. When nil, a FileResolver
	// with no root is used.
	Files Resolver
}

// Defaults for HTTPResolver.
const (
	// DefaultFetchTimeout bounds one network fetch.
	DefaultFetchTimeout = 30 * time.Second
	// DefaultMaxSchemaBytes bounds one fetched schema document. Real
	// schemas are far smaller; the W3C's own largest is under 200 kB.
	DefaultMaxSchemaBytes = 16 << 20
)

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

// MapResolver resolves from an in-memory table, for callers that know every
// schema in advance.
//
// It is the resolver to reach for in a server: nothing is fetched, nothing is
// read from disk, and a schema naming a location that is not in the table is an
// error rather than a request.
type MapResolver struct {
	// ByLocation maps a schemaLocation to schema source.
	ByLocation map[string]string
	// ByNamespace maps a target namespace to schema source, used when an
	// import gives a namespace but no location.
	ByNamespace map[string]string

	mu sync.RWMutex
}

// Resolve implements Resolver.
func (r *MapResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if location != "" {
		if src, ok := r.ByLocation[location]; ok {
			return io.NopCloser(strings.NewReader(src)), location, nil
		}
	}
	if src, ok := r.ByNamespace[namespace]; ok {
		return io.NopCloser(strings.NewReader(src)), namespace, nil
	}
	return nil, "", nil
}
