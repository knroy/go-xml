package relaxng

import (
	"fmt"
	"net/url"
	"path"
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
// The bound is on the *chain*, not the count: a schema may include many
// others, but a cycle — a includes b includes a — would not terminate, and a
// resolver reading from the network makes that a request loop rather than
// merely a hang.
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
