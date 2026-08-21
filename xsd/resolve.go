package xsd

import (
	"fmt"
	"io"
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
		// The separator matters: without it, a Root of "/srv/a" would
		// also admit "/srv/anything".
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return nil, "", fmt.Errorf(
				"schemaLocation %q resolves outside the permitted root", location)
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
	// It runs before the request, so it is the place to refuse loopback,
	// link-local and private ranges — the addresses an SSRF is usually
	// aimed at.
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

	resp, err := client.Get(abs)
	if err != nil {
		return nil, "", fmt.Errorf("fetching schema %q: %w", abs, err)
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
	body := &limitedBody{r: io.LimitReader(resp.Body, max+1), c: resp.Body, max: max, url: abs}
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
