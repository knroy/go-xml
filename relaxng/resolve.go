package relaxng

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Resolver fetches a schema document named by an <externalRef> or <include>.
//
// It is an interface, and there is no default implementation, for the same
// reason DOCTYPE is refused by default and xsi:schemaLocation is ignored: an
// href in a schema is an instruction to go and read something, and where that
// read is allowed to reach is the caller's decision, not the schema author's.
// A caller that wants files supplies one that reads files; a caller that wants
// nothing supplies nothing, and every href is refused with an error that says
// so.
//
// href is the value written in the schema, already resolved against the base
// URI in force — xml:base and the location the schema was loaded from — so an
// implementation receives one absolute reference rather than having to track
// the nesting itself.
type Resolver interface {
	ResolveSchema(href string) (*xdm.Node, error)
}

// FileResolver is a filesystem-backed Resolver.
//
// Root is a capability grant, not a cosmetic path prefix: with it set, every
// reference must remain below that directory even if a schema uses xml:base,
// .., an absolute file URL, or a symlink changed between validation and open.
// os.Root enforces the last property at open time. An empty Root intentionally
// remains unconfined for command-line callers that explicitly choose it.
// Network schemes are never fetched; a caller needing them must write a
// resolver with an explicit host and transport policy.
type FileResolver struct {
	// Root confines reads when non-empty.
	Root string
	// MaxBytes bounds one fetched schema. Zero uses defaultMaxSchemaBytes.
	// A negative value refuses every read rather than disabling the bound.
	MaxBytes int64
}

// defaultMaxSchemaBytes matches xdm's document limit, so the resolver rejects
// an oversized file before allocation while Parse remains the backstop.
const defaultMaxSchemaBytes int64 = 64 << 20

// ResolveSchema implements Resolver.
func (r *FileResolver) ResolveSchema(href string) (*xdm.Node, error) {
	u, err := url.Parse(href)
	if err != nil {
		return nil, fmt.Errorf("relaxng: invalid schema URI %q: %w", href, err)
	}
	if u.Scheme != "" && u.Scheme != "file" {
		return nil, fmt.Errorf("relaxng: remote schema URI %q is not permitted", href)
	}
	// A file: URL may carry an authority, and only an empty one or "localhost"
	// names this machine. Anything else names a *remote* host — a UNC share on
	// Windows, an SMB or NFS mount elsewhere — and reading it is the network
	// fetch this resolver exists to refuse. Taking u.Path alone would discard
	// the authority and silently read the same-named local path instead, which
	// is both a read the caller never asked for and a refusal that never
	// happened.
	if u.Scheme == "file" && u.Host != "" && u.Host != "localhost" {
		return nil, fmt.Errorf(
			"relaxng: schema URI %q names the remote host %q; only local files "+
				"are permitted", href, u.Host)
	}
	// file: URLs are URI syntax; after this point every path is handled by the
	// same confinement and byte-accounting path as a bare filesystem reference.
	p := href
	if u.Scheme == "file" {
		p = u.Path
	}
	var f *os.File
	if r.Root != "" {
		root, err := filepath.Abs(r.Root)
		if err != nil {
			return nil, err
		}
		// Resolve the root once for comparison. Do not resolve the final path:
		// following its symlink before OpenRoot would recreate a check-then-open
		// race and let a link point outside after this check succeeds.
		if root, err = filepath.EvalSymlinks(root); err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		// Compare the parent on the same resolved spelling as Root (macOS /var
		// commonly aliases /private/var); leave the leaf unresolved so OpenRoot
		// remains the enforcement against a symlink escape.
		if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			abs = filepath.Join(dir, filepath.Base(abs))
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("relaxng: schema URI %q resolves outside root %q", href, root)
		}
		rt, err := os.OpenRoot(root)
		if err != nil {
			return nil, err
		}
		defer rt.Close()
		f, err = rt.Open(filepath.ToSlash(rel))
		if err != nil {
			return nil, fmt.Errorf("relaxng: open %q: %w", href, err)
		}
	} else {
		f, err = os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("relaxng: open %q: %w", href, err)
		}
	}
	defer f.Close()
	// Read one byte over the limit. A truncated schema is never a safe schema:
	// it can parse as a different grammar, so over-limit is a loud resource error.
	max := r.MaxBytes
	if max == 0 {
		max = defaultMaxSchemaBytes
	}
	if max < 0 {
		return nil, fmt.Errorf("relaxng: schema byte limit is negative")
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("relaxng: schema %q exceeds %d bytes: %w", href, max, xdm.ErrResourceLimit)
	}
	// Preserve the resolved document location as the base URI. Nested includes
	// are compiled with this value, so sibling references remain sibling reads.
	tree, err := xdm.ParseString(string(b), xdm.ParseOptions{BaseURI: p, MaxBytes: max})
	if err != nil {
		return nil, fmt.Errorf("relaxng: parse %q: %w", href, err)
	}
	return tree.Root, nil
}

// A Resolver owns containment, and must not assume the href it receives has
// been made safe.
//
// The reference is resolved against the base URI in force, which composes
// xml:base and where the schema was loaded from — but resolution is not
// sanitisation. Measured against a base of "schemas/main.rng":
//
//	../../../../etc/passwd  ->  ../../../etc/passwd
//	/etc/passwd             ->  schemas/etc/passwd
//
// and against "https://ex.com/a/main.rng":
//
//	//169.254.169.254/x.rng ->  https://169.254.169.254/x.rng
//	../../../etc/passwd     ->  https://ex.com/etc/passwd
//
// So ".." survives, a scheme-relative reference inherits the scheme and
// reaches a host of the schema's choosing, and URI resolution flattens a path
// only as far as the base allows.
//
// An xml:base goes further than either: it replaces the base outright, scheme
// included. A schema loaded from "file:///srv/schemas/main.rng" that carries
//
//	xml:base="http://169.254.169.254/latest/"   href="meta.rng"
//	  ->  http://169.254.169.254/latest/meta.rng
//
// hands the resolver an http URL, so a resolver that decides by prefix-testing
// the string for "file://" is testing something the schema controls.
//
// That is deliberate: this package cannot know whether a caller's schemas live
// in one directory, several, or behind an HTTP endpoint where ".." is
// meaningless. What it can do is say plainly that the check belongs to the
// implementation. A file-backed resolver should resolve to an absolute path
// and verify it is inside the intended root; an HTTP one should check the host
// against an allowlist rather than a prefix.

// Options configure compilation.
type Options struct {
	// Resolver supplies the documents named by <externalRef> and <include>.
	// When nil, both are refused.
	Resolver Resolver
	// BaseURI is the location the schema itself was read from, against which
	// a relative href is resolved. It may be empty when the schema came from
	// somewhere with no location, in which case only absolute hrefs work.
	BaseURI string
}

// maxIncludeDepth bounds how deeply schemas may include one another.
//
// It is a *resource* bound and nothing more. A cycle — a includes b includes
// a — is a semantic defect in the schema, and it is detected as one, by the
// active set of hrefs in activeHrefs: a depth counter cannot tell a cycle from
// a legal chain that happens to be long, so using one to infer the other both
// mislabels the deep chain and reports the cycle at the wrong href.
//
// The bound stays because it is genuinely earned here, unlike a bound that is
// only standing in for cycle detection: an <include> reaches a Resolver that
// may read a file or the network, and a chain deep enough to matter costs a
// fetch per level even when every href is distinct. Exceeding it says
// "resource limit exceeded", which is a different failure from a cycle and a
// caller must be able to tell them apart.
const maxIncludeDepth = 40

// resolveHref turns the href written on n into the reference a Resolver sees.
//
// The base is the nearest xml:base above n, falling back to where the schema
// was loaded from. This is the one place the two are combined, so that a
// nested <div xml:base="sub/"> composes the way the spec says.
func resolveHref(n *xdm.Node, href, docBase string) (string, error) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", fmt.Errorf("relaxng: <%s> has an empty href", n.Name.Local)
	}
	if strings.Contains(href, "#") {
		// §4.5: an href is a URI reference with no fragment. A fragment would
		// name part of a document, and a schema is included whole.
		return "", fmt.Errorf(
			"relaxng: <%s href=%q> has a fragment identifier", n.Name.Local, href)
	}
	base := baseInForce(n, docBase)
	if base == "" {
		return href, nil
	}
	return joinRef(base, href), nil
}

// joinRef resolves a reference against a base.
//
// A base that names a scheme is resolved by URI rules. One that does not is a
// bare relative path — which is what a schema loaded from a file, or a suite
// that names its resources "sub/x", actually has — and URI resolution would
// turn "a.rng" against "b.rng" into "/a.rng". Treating it as a path keeps the
// two cases from contaminating each other.
func joinRef(base, ref string) string {
	if u, err := url.Parse(ref); err == nil && u.IsAbs() {
		return ref
	}
	if b, err := url.Parse(base); err == nil && b.IsAbs() {
		if r, err := url.Parse(ref); err == nil {
			return b.ResolveReference(r).String()
		}
	}
	// A relative base: the reference is resolved against its directory.
	dir := base
	if i := strings.LastIndexByte(dir, '/'); i >= 0 {
		dir = dir[:i+1]
	} else {
		dir = ""
	}
	return path.Clean(dir + ref)
}

// baseInForce composes the xml:base attributes on and above n.
//
// They nest: a <div xml:base="sub2"> inside a <div xml:base="sub1/"> resolves
// against it, so the walk goes outward collecting bases and then resolves them
// innermost-last.
func baseInForce(n *xdm.Node, docBase string) string {
	var bases []string
	for cur := n; cur != nil && cur.Kind == xdm.KindElement; cur = cur.Parent {
		for _, a := range cur.Attrs {
			if a.Name.URI == xdm.NSXML && a.Name.Local == "base" {
				bases = append(bases, a.Value)
			}
		}
	}
	out := docBase
	for i := len(bases) - 1; i >= 0; i-- {
		out = joinBase(out, bases[i])
	}
	return out
}

func joinBase(base, rel string) string {
	if base == "" {
		return rel
	}
	return joinRef(base, rel)
}
