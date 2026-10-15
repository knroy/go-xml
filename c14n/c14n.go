// Package c14n implements Canonical XML 1.0, Exclusive Canonical XML 1.0
// and Canonical XML 1.1.
//
// Canonicalization produces a byte-exact serialization of a document or
// document subset, so that logically equivalent inputs yield identical
// octets. It is the foundation of XML Signature, and it is also useful
// for content-addressed storage, cache keys and semantic comparison.
//
// The algorithms differ chiefly in how they treat namespace declarations
// and xml:* attributes inherited from ancestors that are not themselves
// part of the output. Exclusive canonicalization renders only the
// namespace declarations an element visibly uses, which makes a signed
// subtree relocatable into another document; inclusive canonicalization
// renders every declaration in scope, which does not. Choose by the
// specification you are implementing, never by preference.
//
// # Input
//
// The input is an [xdm] tree, so everything the parser decides is decided
// once, there: line endings, character and entity references, CDATA
// sections, attribute value normalisation and DTD default attributes all
// arrive already resolved. The parser's defaults apply unchanged — in
// particular a DOCTYPE is refused unless the caller parsed with
// AllowDOCTYPE, and nothing is fetched from a network or a filesystem.
// C14N 1.1's xml:base fix-up is URI arithmetic only; it resolves nothing.
//
// Two inputs the specifications leave undefined are refused: a tree parsed
// from an XML 1.1 document (ErrXML11), and a namespace declaration binding a
// relative URI (ErrRelativeNamespaceURI), which C14N 1.0 and 1.1 section 2.1
// require an implementation to report as a failure.
//
// # Namespace nodes
//
// The specifications define their input as an XPath 1.0 node-set, which
// can contain some of an element's namespace nodes and not others. By
// default an element's namespace declarations participate exactly when the
// element itself is in the set: that is what a same-document reference, the
// enveloped-signature transform and every constructor here except one
// produce. A node set that decides namespace-node membership itself
// implements NamespaceSet, and then canonicalization follows the
// specifications' namespace-node rules literally — a partial namespace axis,
// and the namespace nodes of an element outside the set. FromXPathFilter,
// the XML-DSig XPath Filter transform, is the constructor that does.
package c14n

import (
	"bytes"
	"errors"
	"fmt"
	"hash"
	"io"
	"slices"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// Algorithm identifies a canonicalization algorithm by its W3C URI.
//
// The URI is the identifier because that is how every consuming
// specification names the algorithm: a value read out of a
// ds:CanonicalizationMethod element can be passed through unchanged.
type Algorithm string

const (
	Inclusive10             Algorithm = "http://www.w3.org/TR/2001/REC-xml-c14n-20010315"
	Inclusive10WithComments Algorithm = "http://www.w3.org/TR/2001/REC-xml-c14n-20010315#WithComments"
	Exclusive10             Algorithm = "http://www.w3.org/2001/10/xml-exc-c14n#"
	Exclusive10WithComments Algorithm = "http://www.w3.org/2001/10/xml-exc-c14n#WithComments"
	Inclusive11             Algorithm = "http://www.w3.org/2006/12/xml-c14n11"
	Inclusive11WithComments Algorithm = "http://www.w3.org/2006/12/xml-c14n11#WithComments"
)

// Valid reports whether a is one of the six supported algorithms.
func (a Algorithm) Valid() bool {
	switch a {
	case Inclusive10, Inclusive10WithComments, Exclusive10, Exclusive10WithComments,
		Inclusive11, Inclusive11WithComments:
		return true
	}
	return false
}

// Exclusive reports whether a is one of the exclusive variants.
func (a Algorithm) Exclusive() bool {
	return a == Exclusive10 || a == Exclusive10WithComments
}

// WithComments reports whether a retains comments.
func (a Algorithm) WithComments() bool {
	return a == Inclusive10WithComments || a == Exclusive10WithComments || a == Inclusive11WithComments
}

// Options configures a canonicalization.
//
// The zero Options is not valid: Algorithm must be set. There is no
// default algorithm, deliberately. Specifications that use
// canonicalization always name the algorithm, and a library default
// invites the single most damaging error in this domain, which is
// silently canonicalizing with the wrong one and producing a signature
// that no peer accepts and no local test detects.
type Options struct {
	// Algorithm is required.
	Algorithm Algorithm

	// InclusiveNamespacePrefixes is the PrefixList of an exclusive
	// canonicalization: the prefixes to render even when not visibly
	// utilised. The empty string denotes the default namespace, which
	// appears on the wire as "#default".
	//
	// Ignored by the inclusive algorithms. Passing it with an inclusive
	// algorithm is not an error, because a caller forwarding a parsed
	// transform's parameters should not have to branch.
	InclusiveNamespacePrefixes []string
}

// MaxDepth bounds element nesting below the node a canonicalization starts
// from; deeper input fails with ErrDepthExceeded. It is a variable rather than
// a constant so a caller with unusual input can raise it knowingly. It is read
// at the start of each canonicalization; changing it concurrently with one is
// a data race.
//
// Zero or negative means DefaultMaxDepth, as xdm.ParseOptions.MaxDepth does: a
// bound of zero or below would refuse every document, and there is no
// unlimited setting because the walk recurses. Raise it with a number.
var MaxDepth = DefaultMaxDepth

// DefaultMaxDepth is MaxDepth's initial value, and what zero or a negative
// value means.
const DefaultMaxDepth = 500

var (
	// ErrUnsupportedAlgorithm is returned for an Algorithm outside the
	// six constants.
	ErrUnsupportedAlgorithm = errors.New("c14n: unsupported algorithm")

	// ErrNoAlgorithm is returned when Options.Algorithm is empty.
	ErrNoAlgorithm = errors.New("c14n: no algorithm specified")

	// ErrUnsupportedNode is returned when the input starts at a node kind
	// that has no canonical form on its own, such as an attribute.
	ErrUnsupportedNode = errors.New("c14n: node kind has no canonical form")

	// ErrDepthExceeded is returned when element nesting exceeds MaxDepth.
	ErrDepthExceeded = errors.New("c14n: maximum depth exceeded")

	// ErrRelativeNamespaceURI is returned when a namespace declaration that
	// can reach the output binds a relative URI. C14N 1.0 and 1.1 section
	// 2.1: implementations "MUST report an operation failure on documents
	// containing relative namespace URIs".
	ErrRelativeNamespaceURI = errors.New("c14n: relative namespace URI")

	// ErrXML11 is returned for a tree parsed from an XML 1.1 document.
	// Canonical XML 1.1 "is applicable to XML 1.0 ... It is not defined for
	// XML 1.1", and 1.0 and Exclusive C14N share its XML 1.0 data model.
	ErrXML11 = errors.New("c14n: XML 1.1 input has no canonical form")
)

// ParsePrefixList parses the space-separated PrefixList attribute value
// of an ec:InclusiveNamespaces element, mapping "#default" to the empty
// string. Repeated and empty tokens are ignored.
func ParsePrefixList(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if f == "#default" {
			f = ""
		}
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// FormatPrefixList is the inverse of ParsePrefixList.
func FormatPrefixList(prefixes []string) string {
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		if p == "" {
			p = "#default"
		}
		out[i] = p
	}
	return strings.Join(out, " ")
}

// Bytes returns the canonical form of n.
//
// n may be a document node, in which case the whole document is
// canonicalized, or an element node, in which case the element and its
// descendants are, with the namespace and xml:* axis treatment the
// chosen algorithm specifies for a subset apex.
func Bytes(n *xdm.Node, opts Options) ([]byte, error) {
	var b bytes.Buffer
	err := Write(&b, n, opts)
	return b.Bytes(), err
}

// Digest canonicalizes n and writes the result into h, returning h's sum.
//
// This is the form signature verification wants. It streams, so the
// canonical octets are never materialized.
//
//	sum, err := c14n.Digest(sha256.New(), elem, c14n.Options{
//	    Algorithm: c14n.Exclusive10,
//	})
func Digest(h hash.Hash, n *xdm.Node, opts Options) ([]byte, error) {
	if err := Write(h, n, opts); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// Equal reports whether two nodes have identical canonical forms under
// opts. It stops at the first differing octet.
//
// This is the non-security use: semantic equality of two XML documents,
// independent of insignificant serialization differences.
func Equal(a, b *xdm.Node, opts Options) (bool, error) {
	// ponytail: a's canonical form is buffered and b's streamed against it;
	// two concurrent streams would need a goroutine for no measured gain.
	want, err := Bytes(a, opts)
	if err != nil {
		return false, err
	}
	cw := &compareWriter{want: want}
	switch err := Write(cw, b, opts); {
	case err == errMismatch:
		return false, nil
	case err != nil:
		return false, err
	}
	return len(cw.want) == 0, nil
}

var errMismatch = errors.New("c14n: canonical forms differ")

// compareWriter consumes want as it is written to, failing on the first
// octet that differs.
type compareWriter struct{ want []byte }

func (c *compareWriter) Write(p []byte) (int, error) {
	if !bytes.HasPrefix(c.want, p) {
		return 0, errMismatch
	}
	c.want = c.want[len(p):]
	return len(p), nil
}

// Write canonicalizes n and streams the result to w.
//
// n must be a document or element node. Output is written incrementally:
// memory use is bounded by the document's element depth and the attribute
// count of one element, not by its size. A write error from w is returned
// unwrapped, so callers can use errors.Is against their own sentinel values.
func Write(w io.Writer, n *xdm.Node, opts Options) error {
	if n == nil || (n.Kind() != xdm.KindDocument && n.Kind() != xdm.KindElement) {
		return ErrUnsupportedNode
	}
	return WriteNodeSet(w, Subtree(n), opts)
}

// WriteNodeSet canonicalizes an arbitrary document subset.
//
// See NodeSet for how membership is determined, and the package
// documentation for the one limitation on namespace node membership.
func WriteNodeSet(w io.Writer, ns NodeSet, opts Options) error {
	if opts.Algorithm == "" {
		return ErrNoAlgorithm
	}
	if !opts.Algorithm.Valid() {
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, string(opts.Algorithm))
	}
	return canonicalize(w, ns, opts)
}

// BytesNodeSet mirrors Bytes for a subset.
func BytesNodeSet(ns NodeSet, opts Options) ([]byte, error) {
	var b bytes.Buffer
	err := WriteNodeSet(&b, ns, opts)
	return b.Bytes(), err
}

// DigestNodeSet mirrors Digest for a subset.
func DigestNodeSet(h hash.Hash, ns NodeSet, opts Options) ([]byte, error) {
	if err := WriteNodeSet(h, ns, opts); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// NodeSet identifies the nodes to canonicalize.
//
// A NodeSet is a set of element, attribute, text, comment and processing
// instruction nodes, and the document node. Namespace declarations are not
// members in their own right: an element's declarations participate exactly
// when the element does. See the package documentation.
//
// Implementations must be safe for concurrent use and must not change
// while a canonicalization is in progress.
type NodeSet interface {
	// Root returns a node whose subtree contains every member, normally the
	// document node. Canonicalization walks the subtree in document order
	// from here; namespace and xml:* inheritance still consult ancestors
	// above it.
	Root() *xdm.Node

	// Contains reports whether n is a member.
	Contains(n *xdm.Node) bool
}

// Subtree returns the node set consisting of n and all its descendants,
// with their attributes. This is the set a same-document reference of
// the form "#id" denotes.
func Subtree(n *xdm.Node) NodeSet { return subtree{n} }

// Document returns the node set consisting of the entire document that
// contains d, including the document node.
func Document(d *xdm.Node) NodeSet { return subtree{top(d)} }

// ExcludeSubtree returns every node in root's subtree except exclude and
// its descendants.
//
// This is the XML-DSig enveloped-signature transform: the whole document
// minus the ds:Signature element that contains the reference.
func ExcludeSubtree(root, exclude *xdm.Node) NodeSet { return excludeSet{root, exclude} }

// Func returns a node set defined by a predicate over root's subtree.
//
// f is called at most once per node during a canonicalization, in
// document order. It must be deterministic and free of side effects.
func Func(root *xdm.Node, f func(*xdm.Node) bool) NodeSet { return funcSet{root, f} }

// FromXPath evaluates expr against doc and returns the resulting nodes
// as a node set rooted at the top of doc's tree.
//
// ns supplies prefix bindings for the expression, which is compiled as
// XPath 2.0 (an XPath 1.0 transform expression is almost always also valid
// 2.0). Namespace nodes the expression returns are ignored; see the package
// documentation. ExcludeSubtree is faster and should be preferred where it
// applies.
func FromXPath(doc *xdm.Node, expr string, ns map[string]string) (NodeSet, error) {
	c, err := xpath.Compile(expr, prefixMap(ns))
	if err != nil {
		return nil, err
	}
	seq, err := c.Eval(xpath.NewContext(doc, xpath.Builtins()))
	if err != nil {
		return nil, err
	}
	m := make(map[*xdm.Node]bool, len(seq))
	for _, it := range seq {
		if n, ok := it.(*xdm.Node); ok && n.Kind() != xdm.KindNamespace {
			m[n] = true
		}
	}
	return funcSet{top(doc), func(n *xdm.Node) bool { return m[n] }}, nil
}

// NamespaceSet is a NodeSet that decides namespace-node membership itself,
// rather than letting each element's namespace declarations follow the
// element. Canonicalization then applies C14N 1.0 section 2.3 and Exclusive
// C14N section 3 to namespace nodes as members in their own right: an
// element may keep some of its namespace nodes and not others, and an
// element outside the set still renders the namespace nodes of it that are
// in the set.
//
// Most callers never need it; FromXPathFilter returns one.
type NamespaceSet interface {
	NodeSet

	// ContainsNamespace reports whether the namespace node binding prefix
	// on elem is a member. The empty prefix is the default namespace, which
	// in the XPath 1.0 data model has a namespace node only while a
	// non-empty default is in scope.
	ContainsNamespace(elem *xdm.Node, prefix string) bool
}

// FromXPathFilter is the XML-DSig XPath Filter transform
// (http://www.w3.org/TR/1999/REC-xpath-19991116): filter is evaluated as a
// boolean with each node of doc's tree as the context node — element,
// attribute, text, comment, processing instruction and namespace nodes
// alike — and the node set holds the nodes for which it is true.
//
// Because namespace nodes are filtered like any other, the result is a
// NamespaceSet and may keep part of an element's namespace axis. FromXPath,
// by contrast, takes the nodes an expression returns and lets namespace
// declarations follow their elements. The filter is compiled as XPath 2.0,
// with ns supplying its prefix bindings; the XML-DSig here() function is not
// provided.
func FromXPathFilter(doc *xdm.Node, filter string, ns map[string]string) (NodeSet, error) {
	c, err := xpath.Compile(filter, prefixMap(ns))
	if err != nil {
		return nil, err
	}
	root := top(doc)
	seq, err := allNodes.Eval(xpath.NewContext(root, xpath.Builtins()))
	if err != nil {
		return nil, err
	}
	set := filterSet{root: root, nodes: map[*xdm.Node]bool{}, ns: map[nsKey]bool{}}
	for _, it := range seq {
		n := it.(*xdm.Node) // a union of path expressions yields only nodes
		keep, err := c.EvalBool(xpath.NewContext(n, xpath.Builtins()))
		if err != nil {
			return nil, err
		}
		switch {
		case !keep:
		case n.Kind() == xdm.KindNamespace:
			set.ns[nsKey{n.Parent(), n.Name().Local}] = true
		default:
			set.nodes[n] = true
		}
	}
	return set, nil
}

// allNodes is XML-DSig's input node-set for a same-document XPath Filter:
// every node of the document, namespace nodes included.
var allNodes = xpath.MustCompile("//. | //@* | //namespace::*", prefixMap(nil))

type nsKey struct {
	elem   *xdm.Node
	prefix string
}

type filterSet struct {
	root  *xdm.Node
	nodes map[*xdm.Node]bool
	ns    map[nsKey]bool
}

func (s filterSet) Root() *xdm.Node           { return s.root }
func (s filterSet) Contains(x *xdm.Node) bool { return s.nodes[x] }
func (s filterSet) ContainsNamespace(e *xdm.Node, prefix string) bool {
	return s.ns[nsKey{e, prefix}]
}

type subtree struct{ n *xdm.Node }

func (s subtree) Root() *xdm.Node           { return s.n }
func (s subtree) Contains(x *xdm.Node) bool { return within(x, s.n) }

type excludeSet struct{ root, ex *xdm.Node }

func (s excludeSet) Root() *xdm.Node { return s.root }

// ponytail: an ancestor walk, O(depth) per node; switch to Order() ranges if
// deep documents ever show up in a profile.
func (s excludeSet) Contains(x *xdm.Node) bool { return !within(x, s.ex) && within(x, s.root) }

type funcSet struct {
	root *xdm.Node
	f    func(*xdm.Node) bool
}

func (s funcSet) Root() *xdm.Node           { return s.root }
func (s funcSet) Contains(x *xdm.Node) bool { return s.f(x) }

// within reports whether x is top or one of its descendants, attributes
// included.
func within(x, top *xdm.Node) bool {
	for ; x != nil; x = x.Parent() {
		if x == top {
			return true
		}
	}
	return false
}

func top(n *xdm.Node) *xdm.Node {
	for n != nil && n.Parent() != nil {
		n = n.Parent()
	}
	return n
}

// prefixMap adapts FromXPath's bindings to xpath.NamespaceResolver.
type prefixMap map[string]string

func (m prefixMap) ResolvePrefix(p string) (string, bool) {
	if p == "xml" {
		return xdm.NSXML, true
	}
	u, ok := m[p]
	return u, ok
}
func (prefixMap) DefaultElementNamespace() string  { return "" }
func (prefixMap) DefaultFunctionNamespace() string { return xdm.NSFN }
