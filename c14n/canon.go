package c14n

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// binding is one prefix-to-URI namespace binding. An empty prefix is the
// default namespace; an empty URI is an undeclaration.
type binding struct{ prefix, uri string }

// saved is one entry of a bindings map's undo log: the value a prefix had
// before an element changed it, so the change can be reversed on the way out.
type saved struct {
	prefix, uri string
	had         bool
}

// bindings is a prefix-to-URI map with an undo log. Namespace state is kept
// this way rather than as a stack searched from the top because both lookups
// run for every candidate prefix of every element: a stack made each one cost
// the number of bindings in scope, and a document declaring a thousand
// prefixes on its root took 29 seconds to canonicalize at 127 KB.
type bindings struct {
	m    map[string]string
	undo []saved
}

func (b *bindings) set(prefix, uri string) {
	old, had := b.m[prefix]
	b.undo = append(b.undo, saved{prefix, old, had})
	b.m[prefix] = uri
}

// restore reverses every set made since the log was mark entries long.
func (b *bindings) restore(mark int) {
	for i := len(b.undo) - 1; i >= mark; i-- {
		if u := b.undo[i]; u.had {
			b.m[u.prefix] = u.uri
		} else {
			delete(b.m, u.prefix)
		}
	}
	b.undo = b.undo[:mark]
}

// ancestor is one element on the path from the walk's root to the current
// element, with its membership, which C14N 1.1's xml:base fix-up needs.
type ancestor struct {
	n  *xdm.Node
	in bool
}

// canon is one canonicalization in progress. Its stacks are sized by element
// depth and are truncated rather than reallocated as the walk returns, so a
// steady-state element costs no allocation.
type canon struct {
	w        *bufio.Writer
	sw       *stickyWriter
	set      NodeSet
	all      bool // set is a Subtree: every node the walk reaches is a member
	excl     bool
	comments bool
	v11      bool
	prefixes []string
	maxDepth int

	scope    bindings // declarations in scope
	rendered bindings // declarations rendered by output ancestors
	path     []ancestor
	attrs    []*xdm.Node // scratch: the attribute axis being rendered
	nsOut    []binding   // scratch: the namespace axis being rendered

	// nsSet is the node set when it decides namespace-node membership
	// itself (a NamespaceSet), else nil. Only then are axes and util used.
	// axes holds, for each output element on the path, its namespace nodes
	// that are in the set: what the Canonical XML rule compares against.
	// util maps a prefix to the in-set namespace node value of the nearest
	// output ancestor that visibly utilised it (noNode when that ancestor's
	// node was not in the set): what the exclusive rule compares against.
	nsSet NamespaceSet
	axes  []map[string]string
	util  bindings

	// considered counts calls to consider: the work the namespace axis
	// costs, which TestNamespaceScopeNotQuadratic bounds.
	considered int
}

// noNode records in util that the utilising ancestor had no namespace node
// for the prefix in the set. No URI contains a NUL.
const noNode = "\x00"

func canonicalize(w io.Writer, ns NodeSet, opts Options) error {
	_, err := run(w, ns, opts)
	return err
}

// run is canonicalize, returning the walker so a test can read its counts.
func run(w io.Writer, ns NodeSet, opts Options) (*canon, error) {
	root := ns.Root()
	if root == nil {
		return nil, ErrUnsupportedNode
	}
	sw := &stickyWriter{w: w}
	_, all := ns.(subtree)
	c := &canon{
		w:        bufio.NewWriter(sw),
		sw:       sw,
		set:      ns,
		all:      all,
		excl:     opts.Algorithm.Exclusive(),
		comments: opts.Algorithm.WithComments(),
		v11:      opts.Algorithm == Inclusive11 || opts.Algorithm == Inclusive11WithComments,
		maxDepth: MaxDepth,
		scope:    bindings{m: map[string]string{}},
		rendered: bindings{m: map[string]string{}},
	}
	if c.maxDepth <= 0 {
		c.maxDepth = DefaultMaxDepth
	}
	if c.excl {
		c.prefixes = opts.InclusiveNamespacePrefixes
	}
	c.nsSet, _ = ns.(NamespaceSet)
	c.util = bindings{m: map[string]string{}}
	if t := root.Tree(); t != nil && t.XMLVersion == "1.1" {
		return nil, ErrXML11
	}
	// Bindings declared above the root are in scope for it. Their order here
	// is irrelevant: each prefix appears once, and output is sorted.
	if p := root.Parent; p != nil {
		for prefix, uri := range p.InScopeNamespaces() {
			if err := checkNamespaceURI(prefix, uri); err != nil {
				return nil, err
			}
			c.scope.m[prefix] = uri
		}
	}
	var err error
	switch root.Kind {
	case xdm.KindDocument:
		err = c.document(root)
	case xdm.KindElement:
		err = c.element(root, false, 1)
	case xdm.KindText, xdm.KindComment, xdm.KindPI:
		if c.member(root) {
			c.leaf(root)
		}
	default:
		return nil, ErrUnsupportedNode
	}
	if err == nil {
		err = c.w.Flush()
	}
	if c.sw.err != nil {
		return c, c.sw.err // unwrapped, as Write documents
	}
	return c, err
}

// stickyWriter remembers the first write error, so the walk can stop at the
// next element boundary instead of canonicalizing the rest into nothing.
type stickyWriter struct {
	w   io.Writer
	err error
}

// Write is called only by the bufio.Writer, which never writes again after an
// error, so the first error is the only one recorded.
func (s *stickyWriter) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	s.err = err
	return n, err
}

// put and putByte discard each write's result on purpose. The bufio.Writer
// keeps the first error it meets and fails every later write the same way,
// so checking after each byte would add a branch per byte and learn nothing:
// the kept error is read at every element boundary (element returns
// c.sw.err) and at the final Flush, and returned unwrapped from there.
// writeQName and writeEscaped follow the same rule.
func (c *canon) put(s string)   { _, _ = c.w.WriteString(s) }
func (c *canon) putByte(b byte) { _ = c.w.WriteByte(b) }

func (c *canon) contains(n *xdm.Node) bool { return c.all || c.set.Contains(n) }

// document renders the children of the document node. Comments and PIs
// outside the document element are separated from it by one #xA: after
// the node when it precedes the element, before it when it follows.
func (c *canon) document(d *xdm.Node) error {
	after := false
	for _, ch := range d.Children {
		if ch.Kind == xdm.KindElement {
			if err := c.element(ch, c.contains(d), 1); err != nil {
				return err
			}
			after = true
			continue
		}
		if !c.member(ch) {
			continue
		}
		if after {
			c.putByte('\n')
		}
		c.leaf(ch)
		if !after {
			c.putByte('\n')
		}
	}
	return nil
}

// member reports whether a text, comment or PI node is rendered: the only
// kinds that reach it, since elements are walked and attributes are not
// children.
func (c *canon) member(n *xdm.Node) bool {
	if n.Kind == xdm.KindComment && !c.comments {
		return false
	}
	return c.contains(n)
}

func (c *canon) element(e *xdm.Node, parentIn bool, depth int) error {
	if depth > c.maxDepth {
		return ErrDepthExceeded
	}
	in := c.contains(e)
	scopeMark, renderedMark, utilMark := len(c.scope.undo), len(c.rendered.undo), len(c.util.undo)
	for _, ns := range e.Namespaces {
		if err := checkNamespaceURI(ns.Name.Local, ns.Value); err != nil {
			return err
		}
		c.scope.set(ns.Name.Local, ns.Value)
	}
	c.path = append(c.path, ancestor{e, in})

	c.attrs = c.attrs[:0]
	for _, a := range e.Attrs {
		if c.contains(a) {
			c.attrs = append(c.attrs, a)
		}
	}
	axesMark := len(c.axes)
	switch {
	case in:
		c.putByte('<')
		writeQName(c.w, e.Name)
		if c.nsSet != nil {
			c.namespaceNodes(e, true)
		} else {
			c.namespaces(e, parentIn)
		}
		if !parentIn && !c.excl {
			c.inherit(e)
		}
	case c.nsSet != nil:
		// C14N 1.0 §2.3: an element outside the set still processes its
		// namespace axis, so its namespace nodes that ARE in the set are
		// rendered, into the parent's content. Under Exclusive C14N that
		// holds for the PrefixList's prefixes, which §3 hands to Canonical
		// XML's rules; its own rule needs the parent element in the set.
		c.namespaceNodes(e, false)
	}
	// C14N 1.0 §2.3: an element outside the set still processes its
	// attribute axis. Only a Func or FromXPath set can reach this with a
	// non-empty list, and what it produces is not well-formed; it is what
	// the specification says.
	c.writeAttrs()
	if in {
		c.putByte('>')
	}

	for _, ch := range e.Children {
		if ch.Kind == xdm.KindElement {
			if err := c.element(ch, in, depth+1); err != nil {
				return err
			}
		} else if c.member(ch) {
			c.leaf(ch)
		}
	}
	if in {
		c.put("</")
		writeQName(c.w, e.Name)
		c.putByte('>')
	}
	c.scope.restore(scopeMark)
	c.rendered.restore(renderedMark)
	c.util.restore(utilMark)
	c.axes = c.axes[:axesMark]
	c.path = c.path[:len(c.path)-1]
	return c.sw.err
}

// namespaces renders e's namespace axis and records what it rendered.
//
// Both algorithms reduce to one rule over different candidate prefixes: a
// candidate renders when its binding at e differs from the one the nearest
// output ancestor rendered. An absent default namespace is the binding "",
// which is what makes xmlns="" appear exactly when an output ancestor
// rendered a non-empty default and nowhere else.
//
// For inclusive canonicalization, an element whose parent was output needs to
// consider only its own declarations: the parent rendered every binding in its
// scope that differed from what was already rendered, so nothing inherited can
// differ now. Only an apex — an element whose parent is not output — scans
// the whole scope, which keeps the per-element cost to the element's own
// declarations rather than every binding in scope.
func (c *canon) namespaces(e *xdm.Node, parentIn bool) {
	c.nsOut = c.nsOut[:0]
	if c.excl {
		// Exclusive C14N §3: visibly utilised prefixes only — the element's
		// own, and those of its attributes in the set (an unprefixed
		// attribute is in no namespace, so it utilises nothing) — plus the
		// InclusiveNamespaces PrefixList.
		c.consider(e.Name.Prefix)
		for _, a := range c.attrs {
			if a.Name.Prefix != "" {
				c.consider(a.Name.Prefix)
			}
		}
		for _, p := range c.prefixes {
			c.consider(p)
		}
	} else if parentIn {
		for _, ns := range e.Namespaces {
			c.consider(ns.Name.Local)
		}
	} else {
		for prefix := range c.scope.m {
			c.consider(prefix)
		}
		c.consider("")
	}
	c.writeNamespaces()
}

// writeNamespaces renders c.nsOut, sorted by prefix, and records it as
// rendered.
func (c *canon) writeNamespaces() {
	// Candidates can repeat (a prefix used by the element and an attribute,
	// or listed too); sorting brings repeats together to drop them.
	slices.SortFunc(c.nsOut, func(a, b binding) int { return strings.Compare(a.prefix, b.prefix) })
	c.nsOut = slices.CompactFunc(c.nsOut, func(a, b binding) bool { return a.prefix == b.prefix })
	for _, b := range c.nsOut {
		c.put(" xmlns")
		if b.prefix != "" {
			c.putByte(':')
			c.put(b.prefix)
		}
		c.put(`="`)
		writeEscaped(c.w, b.uri, true)
		c.putByte('"')
	}
	for _, b := range c.nsOut {
		c.rendered.set(b.prefix, b.uri)
	}
}

// namespaceNodes renders e's namespace axis when the node set decides
// namespace-node membership itself; in says whether e is in the set.
//
// Two rules apply, by prefix. Canonical XML's (C14N 1.0 §2.3) covers every
// prefix under the inclusive algorithms and the PrefixList's prefixes under
// Exclusive C14N, which §3 hands to it: a namespace node of e that is in the
// set renders unless "the nearest ancestor element of the node's parent
// element that is in the node-set has a namespace node in the node-set with
// the same local name and value". That ancestor's in-set nodes are the top
// of c.axes; with partial axes they are not what was rendered, so this path
// cannot use c.rendered. Exclusive C14N's own rule covers the rest: a
// namespace node in the set whose prefix e visibly utilises renders unless
// the nearest output ancestor that visibly utilises it has "a namespace node
// in the node-set with the same namespace prefix and value". A node outside
// the set never renders, "even if its parent node is included" (§1.1).
func (c *canon) namespaceNodes(e *xdm.Node, in bool) {
	c.nsOut = c.nsOut[:0]
	listed := func(prefix string) bool { return !c.excl || slices.Contains(c.prefixes, prefix) }
	var anc map[string]string
	if n := len(c.axes); n > 0 {
		anc = c.axes[n-1]
	}
	var axis map[string]string
	if in {
		axis = map[string]string{}
	}
	hasDefault := false
	for prefix, uri := range c.scope.m {
		// An empty URI is an undeclaration: no namespace node exists for it.
		// The xml node is never rendered ("omit namespace node with local
		// name xml").
		if uri == "" || prefix == "xml" || !c.nsSet.ContainsNamespace(e, prefix) {
			continue
		}
		if in {
			axis[prefix] = uri
		}
		if prefix == "" {
			hasDefault = true
		}
		if !listed(prefix) {
			continue
		}
		if v, ok := anc[prefix]; ok && v == uri {
			continue
		}
		c.nsOut = append(c.nsOut, binding{prefix, uri})
	}
	// xmlns="" under Canonical XML's rule: e in the set, no default
	// namespace node of e in the set, and the nearest ancestor in the set
	// has one.
	if in && listed("") && !hasDefault && anc[""] != "" {
		c.nsOut = append(c.nsOut, binding{"", ""})
	}
	if c.excl && in {
		c.exclusiveUtilised(e, axis)
	}
	if in {
		c.axes = append(c.axes, axis)
	}
	c.writeNamespaces()
}

// exclusiveUtilised applies Exclusive C14N §3's own rule to the prefixes e
// visibly utilises that the PrefixList does not name, and records what e
// utilised for its descendants. axis is e's in-set namespace nodes.
func (c *canon) exclusiveUtilised(e *xdm.Node, axis map[string]string) {
	var used []string
	add := func(p string) {
		if p != "xml" && !slices.Contains(c.prefixes, p) && !slices.Contains(used, p) {
			used = append(used, p)
		}
	}
	add(e.Name.Prefix)
	for _, a := range c.attrs {
		if a.Name.Prefix != "" {
			add(a.Name.Prefix)
		}
	}
	for _, p := range used {
		uri := c.scope.m[p]
		node, inSet := axis[p]
		// The value the nearest utilising output ancestor had for p, as
		// its in-set node; absent when no output ancestor utilised p.
		prev, seen := c.util.m[p]
		switch {
		case inSet:
			// The namespace node is in the set. It renders unless the
			// nearest utilising ancestor has the same node in the set; with
			// no such ancestor, unless the prefix is already rendered with
			// this value. A node outside the set never renders (§1.1).
			if (seen && prev != uri) || (!seen && c.rendered.m[p] != uri) {
				c.nsOut = append(c.nsOut, binding{p, uri})
			}
		case p == "":
			// No default namespace node in the set (none in scope, or one
			// outside the set). xmlns="" when e utilises the default
			// namespace and the nearest utilising output ancestor has a
			// non-empty default node in the set.
			if seen && prev != noNode && prev != "" {
				c.nsOut = append(c.nsOut, binding{"", ""})
			}
		}
		if inSet {
			c.util.set(p, node)
		} else {
			c.util.set(p, noNode)
		}
	}
}

func (c *canon) consider(prefix string) {
	c.considered++
	// The xml prefix is bound by definition and never rendered (C14N 1.0
	// §2.3, "omit namespace node with local name xml").
	if prefix == "xml" {
		return
	}
	uri := c.scope.m[prefix]
	if prefix != "" && uri == "" {
		return // not in scope: a PrefixList entry naming nothing renders nothing
	}
	if c.rendered.m[prefix] == uri {
		return
	}
	c.nsOut = append(c.nsOut, binding{prefix, uri})
}

// hasScheme reports whether s begins with an RFC 3986 section 3.1 scheme and
// its colon — ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ) ":" — which is what
// makes a URI absolute (section 4.3). It is a scan rather than a regexp
// because it runs once per namespace declaration: a regexp's matcher comes
// from a sync.Pool, and where the pool drops entries, as it deliberately does
// under the race detector, every match allocated.
func hasScheme(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c&^0x20 && c&^0x20 <= 'Z':
		case i == 0:
			return false
		case '0' <= c && c <= '9', c == '+', c == '-', c == '.':
		case c == ':':
			return true
		default:
			return false
		}
	}
	return false
}

// checkNamespaceURI enforces C14N 1.0 and 1.1 section 2.1: "implementations
// of XML canonicalization MUST report an operation failure on documents
// containing relative namespace URIs". It is applied to every declaration
// the walk visits and every binding in scope above its start — everything
// whose declarations can reach the output. An empty URI is an undeclaration,
// not a URI. The parser still accepts relative URIs, which Namespaces in XML
// deprecates rather than forbids; only canonicalization refuses them.
func checkNamespaceURI(prefix, uri string) error {
	if uri == "" || hasScheme(uri) {
		return nil
	}
	if prefix == "" {
		return fmt.Errorf("%w: xmlns=%q", ErrRelativeNamespaceURI, uri)
	}
	return fmt.Errorf("%w: xmlns:%s=%q", ErrRelativeNamespaceURI, prefix, uri)
}

// inherit adds to c.attrs the xml:* attributes an inclusive canonicalization
// carries onto an element whose parent is not in the set (C14N 1.0 §2.4,
// C14N 1.1 §2.4).
//
// Exclusive canonicalization never calls this. Its omission is the whole
// point of that algorithm (Exclusive C14N §3): propagating xml:lang or
// xml:base into a subtree is what stops it being relocatable into another
// document. Adding it there looks like a fix and breaks interoperability
// with every conformant implementation.
func (c *canon) inherit(e *xdm.Node) {
	if c.v11 {
		// The apex keeps its OWN simple inheritable attributes and xml:base
		// even when the node set excludes them. Section 2.4 read literally
		// merges only "the nodes of E's attribute axis that are in the
		// node-set"; the XML Security WG's interop case
		// xmlbase-c14n11spec3-103 expects <a xml:base="foo/bar"> for a kept
		// without its attribute, all five implementations in that round
		// signed exactly that, and xmlsec1 1.2.41 was measured doing the
		// same. The WG's result is followed.
		for _, at := range e.Attrs {
			if at.Name.URI == xdm.NSXML && (c.inheritable(at.Name.Local) || at.Name.Local == "base") &&
				!hasXMLAttr(c.attrs, at.Name.Local) {
				c.attrs = append(c.attrs, at)
			}
		}
	}
	for a := e.Parent; a != nil && a.Kind == xdm.KindElement; a = a.Parent {
		for _, at := range a.Attrs {
			if at.Name.URI != xdm.NSXML || !c.inheritable(at.Name.Local) ||
				hasXMLAttr(e.Attrs, at.Name.Local) || hasXMLAttr(c.attrs, at.Name.Local) {
				continue
			}
			c.attrs = append(c.attrs, at)
		}
	}
	if c.v11 {
		c.fixBase(e)
	}
}

// inheritable reports whether xml:local is copied from an ancestor. C14N 1.0
// copies every xml:* attribute. C14N 1.1 copies only the simple inheritable
// ones: xml:id is not inherited, because an ID copied onto a descendant is
// no longer unique, and xml:base is fixed up rather than copied.
func (c *canon) inheritable(local string) bool {
	return !c.v11 || local == "lang" || local == "space"
}

func hasXMLAttr(attrs []*xdm.Node, local string) bool {
	for _, a := range attrs {
		if a.Name.URI == xdm.NSXML && a.Name.Local == local {
			return true
		}
	}
	return false
}

// fixBase performs C14N 1.1 §2.4's xml:base fix-up on e, whose parent is not
// in the set. It applies only when one of the contiguously omitted ancestors
// carried xml:base; their values and e's own are joined innermost-last.
func (c *canon) fixBase(e *xdm.Node) {
	var bases []string // innermost first
	i := len(c.path) - 2
	a := e.Parent
	for ; a != nil && a.Kind == xdm.KindElement; a = a.Parent {
		if i >= 0 {
			if c.path[i].in {
				break
			}
			i--
		}
		if v, ok := xmlAttr(a, "base"); ok {
			bases = append(bases, v)
		}
	}
	if len(bases) == 0 {
		return
	}
	v, own := xmlAttr(e, "base")
	if !own {
		v, bases = bases[0], bases[1:]
	}
	for _, b := range bases {
		v = joinURIReferences(b, v)
	}
	c.attrs = slices.DeleteFunc(c.attrs, func(a *xdm.Node) bool {
		return a.Name.URI == xdm.NSXML && a.Name.Local == "base"
	})
	if v != "" {
		c.attrs = append(c.attrs, &xdm.Node{
			Kind:  xdm.KindAttribute,
			Name:  xdm.QName{Prefix: "xml", Local: "base", URI: xdm.NSXML},
			Value: v,
		})
	}
}

func xmlAttr(e *xdm.Node, local string) (string, bool) {
	for _, a := range e.Attrs {
		if a.Name.URI == xdm.NSXML && a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// writeAttrs renders c.attrs sorted by namespace URI, then local name. Go's
// string comparison is by byte, which for UTF-8 is by code point — the
// order the specifications require. No collation belongs here.
func (c *canon) writeAttrs() {
	slices.SortFunc(c.attrs, func(a, b *xdm.Node) int {
		if n := strings.Compare(a.Name.URI, b.Name.URI); n != 0 {
			return n
		}
		return strings.Compare(a.Name.Local, b.Name.Local)
	})
	for _, a := range c.attrs {
		c.putByte(' ')
		writeQName(c.w, a.Name)
		c.put(`="`)
		writeEscaped(c.w, a.Value, true)
		c.putByte('"')
	}
}

func (c *canon) leaf(n *xdm.Node) {
	switch n.Kind {
	case xdm.KindText:
		writeEscaped(c.w, n.Value, false)
	case xdm.KindComment:
		c.put("<!--")
		c.put(n.Value)
		c.put("-->")
	case xdm.KindPI:
		c.put("<?")
		c.put(n.Name.Local)
		if n.Value != "" {
			c.putByte(' ')
			c.put(n.Value)
		}
		c.put("?>")
	}
}

func writeQName(w *bufio.Writer, q xdm.QName) {
	if q.Prefix != "" {
		_, _ = w.WriteString(q.Prefix)
		_ = w.WriteByte(':')
	}
	_, _ = w.WriteString(q.Local)
}

// writeEscaped writes s with the text or attribute escaping of C14N 1.0
// §2.3. The two are deliberately asymmetric: text escapes '>' and leaves
// '"', tab and newline alone; attribute values do the reverse. Do not unify.
func writeEscaped(w *bufio.Writer, s string, attr bool) {
	start := 0
	for i := 0; i < len(s); i++ {
		var rep string
		switch s[i] {
		case '&':
			rep = "&amp;"
		case '<':
			rep = "&lt;"
		case '>':
			if attr {
				continue
			}
			rep = "&gt;"
		case '"':
			if !attr {
				continue
			}
			rep = "&quot;"
		case '\t':
			if !attr {
				continue
			}
			rep = "&#x9;"
		case '\n':
			if !attr {
				continue
			}
			rep = "&#xA;"
		case '\r':
			rep = "&#xD;"
		default:
			continue
		}
		_, _ = w.WriteString(s[start:i])
		_, _ = w.WriteString(rep)
		start = i + 1
	}
	_, _ = w.WriteString(s[start:])
}

// uriRE is RFC 3986 appendix B's reference-splitting expression.
var uriRE = regexp.MustCompile(`^(([^:/?#]+):)?(//([^/?#]*))?([^?#]*)(\?([^#]*))?(#(.*))?$`)

// joinURIReferences is C14N 1.1 §2.4's join-URI-References: RFC 3986 §5.2
// resolution of ref against base, modified so that two relative values
// combine into a relative value. The base needs no scheme, a trailing ".."
// counts as "../", leading "../" segments survive dot removal, and the
// reference's fragment is dropped. Pure string arithmetic: nothing is
// resolved or fetched.
func joinURIReferences(base, ref string) string {
	b, r := uriRE.FindStringSubmatch(base), uriRE.FindStringSubmatch(ref)
	bPath := b[5]
	if bPath == ".." || strings.HasSuffix(bPath, "/..") {
		bPath += "/"
	}
	var scheme, auth, path, query string
	var hasAuth, hasQuery bool
	switch {
	case r[1] != "":
		scheme, auth, hasAuth, path, query, hasQuery = r[2], r[4], r[3] != "", removeDots(r[5]), r[7], r[6] != ""
	case r[3] != "":
		scheme, auth, hasAuth, path, query, hasQuery = b[2], r[4], true, removeDots(r[5]), r[7], r[6] != ""
	default:
		scheme, auth, hasAuth = b[2], b[4], b[3] != ""
		switch {
		case r[5] == "":
			path = bPath
			query, hasQuery = b[7], b[6] != ""
			if r[6] != "" {
				query, hasQuery = r[7], true
			}
		case strings.HasPrefix(r[5], "/"):
			path, query, hasQuery = removeDots(r[5]), r[7], r[6] != ""
		default:
			// RFC 3986 §5.2.3 merge.
			if hasAuth && bPath == "" {
				path = "/" + r[5]
			} else {
				path = bPath[:strings.LastIndex(bPath, "/")+1] + r[5]
			}
			path, query, hasQuery = removeDots(path), r[7], r[6] != ""
		}
	}
	var sb strings.Builder
	if scheme != "" {
		sb.WriteString(scheme + ":")
	}
	if hasAuth {
		sb.WriteString("//" + auth)
	}
	sb.WriteString(path)
	if hasQuery {
		sb.WriteString("?" + query)
	}
	return sb.String()
}

// removeDots is RFC 3986 §5.2.4 as C14N 1.1 modifies it: leading ".."
// segments of a relative path are kept, runs of "/" collapse to one, and a
// trailing ".." gains a "/".
func removeDots(p string) string {
	abs := strings.HasPrefix(p, "/")
	segs := strings.Split(p, "/")
	var out []string
	trailing := false
	for _, s := range segs {
		trailing = false
		switch s {
		case "":
			continue
		case ".":
			trailing = true
		case "..":
			trailing = true
			if len(out) > 0 && out[len(out)-1] != ".." {
				out = out[:len(out)-1]
			} else if !abs {
				out = append(out, "..")
			}
		default:
			out = append(out, s)
		}
	}
	if strings.HasSuffix(p, "/") {
		trailing = true
	}
	res := strings.Join(out, "/")
	if abs {
		res = "/" + res
	}
	if trailing && len(out) > 0 {
		res += "/"
	}
	return res
}
