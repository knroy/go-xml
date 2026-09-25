package c14n

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// allAlgorithms is every supported algorithm, in a fixed order so that a
// failure names the same algorithm on every run.
var allAlgorithms = []Algorithm{
	Inclusive10, Inclusive10WithComments,
	Exclusive10, Exclusive10WithComments,
	Inclusive11, Inclusive11WithComments,
}

// parse parses s with the parser's defaults: those are what the package
// documentation promises canonicalization inherits (DOCTYPE refused, nothing
// fetched), so a test that loosened them would be testing a different input.
func parse(t testing.TB, s string) *xdm.Node {
	t.Helper()
	tr, err := xdm.ParseString(s, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tr.Root
}

// find returns the first element in document order whose local name is local.
func find(t testing.TB, n *xdm.Node, local string) *xdm.Node {
	t.Helper()
	var walk func(*xdm.Node) *xdm.Node
	walk = func(n *xdm.Node) *xdm.Node {
		if n.Kind == xdm.KindElement && n.Name.Local == local {
			return n
		}
		for _, c := range n.Children {
			if f := walk(c); f != nil {
				return f
			}
		}
		return nil
	}
	if f := walk(n); f != nil {
		return f
	}
	t.Fatalf("no element %q", local)
	return nil
}

func canonOf(t testing.TB, n *xdm.Node, alg Algorithm, prefixes ...string) string {
	t.Helper()
	b, err := Bytes(n, Options{Algorithm: alg, InclusiveNamespacePrefixes: prefixes})
	if err != nil {
		t.Fatalf("Bytes(%s): %v", alg, err)
	}
	return string(b)
}

func canonSet(t testing.TB, ns NodeSet, alg Algorithm, prefixes ...string) string {
	t.Helper()
	b, err := BytesNodeSet(ns, Options{Algorithm: alg, InclusiveNamespacePrefixes: prefixes})
	if err != nil {
		t.Fatalf("BytesNodeSet(%s): %v", alg, err)
	}
	return string(b)
}

// TestOctetRules covers the serialization rules every algorithm shares
// (C14N 1.0 section 1.1's summary, detailed in section 2.3). The inputs carry
// no namespaces, no comments and no subsetting, so all six algorithms must
// agree octet for octet; each case is run under all of them to show it.
func TestOctetRules(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// Section 2.1: the canonical form is UTF-8, and a byte order mark is
		// part of the encoding, not the document.
		{"utf8 no BOM", "\xEF\xBB\xBF<a>\u00e9\u4e2d</a>", "<a>\u00e9\u4e2d</a>"},
		// Line breaks are normalized to #xA on input (XML 1.0 section 2.11).
		{"CRLF and CR to LF", "<a>x\r\ny\rz</a>", "<a>x\ny\nz</a>"},
		// Text: & < > escaped; quotes, tab and newline left literal.
		{"text escapes", `<a>&amp;&lt;&gt;"'&#9;&#10;</a>`, "<a>&amp;&lt;&gt;\"'\t\n</a>"},
		// Only a character reference can put #xD into text; it must come
		// back as a reference or it would be normalized away on re-parse.
		{"text CR", `<a>&#13;</a>`, "<a>&#xD;</a>"},
		// Attributes: & < " #x9 #xA #xD escaped, '>' and "'" left literal.
		{"attr escapes", `<a b="&amp;&lt;&gt;&quot;'&#9;&#10;&#13;"/>`, `<a b="&amp;&lt;>&quot;'&#x9;&#xA;&#xD;"></a>`},
		// Attribute values are always delimited by '"'.
		{"single-quoted attr", `<a b='say "hi"'/>`, `<a b="say &quot;hi&quot;"></a>`},
		{"empty element", `<a><b/></a>`, `<a><b></b></a>`},
		// Whitespace inside tags is not significant and is normalized.
		{"tag whitespace", "<a   b = \"1\"\n c='2' ></a >", `<a b="1" c="2"></a>`},
		{"xml declaration dropped", `<?xml version="1.0" encoding="UTF-8"?><a/>`, `<a></a>`},
		{"char refs expanded", `<a b="&#x41;">&#65;&#x263A;</a>`, "<a b=\"A\">A\u263a</a>"},
		// Section 2.3 PIs: target, a space only when there is data, data.
		{"PI no data", `<a><?p?></a>`, `<a><?p?></a>`},
		{"PI data", `<a><?p  some data?></a>`, `<a><?p some data?></a>`},
		// Unqualified attributes have no namespace URI, the empty string,
		// so they sort first; then by local name.
		{"attr order no ns", `<a z="1" b="2" m="3"/>`, `<a b="2" m="3" z="1"></a>`},
		// Whitespace outside the document element is not part of the data
		// model; a PI before it is followed by exactly one #xA.
		{"doc-level whitespace", "<?p?>\n\n<a/>\n\n", "<?p?>\n<a></a>"},
		{"PI after doc element", "<a/>\n<?p d?>", "<a></a>\n<?p d?>"},
	}
	for _, tc := range cases {
		doc := parse(t, tc.in)
		for _, alg := range allAlgorithms {
			if got := canonOf(t, doc, alg); got != tc.want {
				t.Errorf("%s / %s:\n got %q\nwant %q", tc.name, alg, got, tc.want)
			}
		}
	}
}

// TestNamespaceAndAttributeOrder checks section 2.3's ordering: namespace
// declarations first, by prefix with the default first, then attributes by
// namespace URI and local name. Prefixes are chosen so that prefix order and
// URI order disagree, which is the mistake an implementation sorting
// attributes by qualified name makes.
func TestNamespaceAndAttributeOrder(t *testing.T) {
	doc := parse(t, `<a xmlns:z="urn:uz" xmlns:b="urn:ub" xmlns="urn:ud" z:x="1" b:y="2" c="3" a="4" xmlns:q="http://a" q:x="5"/>`)
	want := `<a xmlns="urn:ud" xmlns:b="urn:ub" xmlns:q="http://a" xmlns:z="urn:uz" a="4" c="3" q:x="5" b:y="2" z:x="1"></a>`
	for _, alg := range allAlgorithms {
		if got := canonOf(t, doc, alg); got != want {
			t.Errorf("%s:\n got %q\nwant %q", alg, got, want)
		}
	}
}

// TestComments: comments render only in the WithComments variants, and at
// document level they take the same #xA separators as PIs.
func TestComments(t *testing.T) {
	doc := parse(t, `<!--c--><a><!--d--></a><!--e-->`)
	for _, alg := range allAlgorithms {
		want := "<a></a>"
		if alg.WithComments() {
			want = "<!--c-->\n<a><!--d--></a>\n<!--e-->"
		}
		if got := canonOf(t, doc, alg); got != want {
			t.Errorf("%s: got %q want %q", alg, got, want)
		}
	}
}

func TestAlgorithmPredicates(t *testing.T) {
	cases := []struct {
		a                        Algorithm
		valid, excl, withComment bool
	}{
		{Inclusive10, true, false, false},
		{Inclusive10WithComments, true, false, true},
		{Exclusive10, true, true, false},
		{Exclusive10WithComments, true, true, true},
		{Inclusive11, true, false, false},
		{Inclusive11WithComments, true, false, true},
		{"", false, false, false},
		// A near miss: the 1.1 URI with the 1.0 fragment syntax. Accepting
		// it by prefix match would silently pick an algorithm.
		{"http://www.w3.org/2006/12/xml-c14n11#withcomments", false, false, false},
		{"http://www.w3.org/2001/10/xml-exc-c14n", false, false, false},
	}
	for _, c := range cases {
		if c.a.Valid() != c.valid || c.a.Exclusive() != c.excl || c.a.WithComments() != c.withComment {
			t.Errorf("%q: Valid=%v Exclusive=%v WithComments=%v, want %v %v %v",
				c.a, c.a.Valid(), c.a.Exclusive(), c.a.WithComments(), c.valid, c.excl, c.withComment)
		}
	}
}

// TestAlgorithmErrors: there is no default algorithm, and an unknown one is
// refused with a sentinel that survives wrapping.
func TestAlgorithmErrors(t *testing.T) {
	doc := parse(t, `<a/>`)
	var buf bytes.Buffer
	if err := Write(&buf, doc, Options{}); !errors.Is(err, ErrNoAlgorithm) {
		t.Errorf("empty algorithm: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q before refusing", buf.String())
	}
	if _, err := Bytes(doc, Options{Algorithm: "urn:nope"}); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Errorf("unknown algorithm: %v", err)
	}
	if _, err := BytesNodeSet(Document(doc), Options{Algorithm: "urn:nope"}); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Errorf("unknown algorithm (node set): %v", err)
	}
	if _, err := Digest(sha256.New(), doc, Options{}); !errors.Is(err, ErrNoAlgorithm) {
		t.Errorf("Digest: %v", err)
	}
	if _, err := DigestNodeSet(sha256.New(), Document(doc), Options{}); !errors.Is(err, ErrNoAlgorithm) {
		t.Errorf("DigestNodeSet: %v", err)
	}
	if _, err := Equal(doc, doc, Options{}); !errors.Is(err, ErrNoAlgorithm) {
		t.Errorf("Equal: %v", err)
	}
}

// TestUnsupportedNode: an attribute, a namespace node or nil has no
// canonical form of its own.
func TestUnsupportedNode(t *testing.T) {
	doc := parse(t, `<a xmlns:p="urn:u" b="1">t</a>`)
	a := find(t, doc, "a")
	opts := Options{Algorithm: Inclusive10}
	for name, n := range map[string]*xdm.Node{
		"nil": nil, "attribute": a.Attrs[0], "namespace": a.Namespaces[0], "text": a.Children[0],
	} {
		if _, err := Bytes(n, opts); !errors.Is(err, ErrUnsupportedNode) {
			t.Errorf("Bytes(%s): %v", name, err)
		}
	}
	for name, ns := range map[string]NodeSet{
		"nil root": Func(nil, func(*xdm.Node) bool { return true }),
		"attr":     Func(a.Attrs[0], func(*xdm.Node) bool { return true }),
		"ns":       Func(a.Namespaces[0], func(*xdm.Node) bool { return true }),
	} {
		if _, err := BytesNodeSet(ns, opts); !errors.Is(err, ErrUnsupportedNode) {
			t.Errorf("BytesNodeSet(%s): %v", name, err)
		}
	}
	if _, err := Equal(a.Attrs[0], doc, opts); !errors.Is(err, ErrUnsupportedNode) {
		t.Errorf("Equal(attr, doc): %v", err)
	}
	if _, err := Equal(doc, a.Attrs[0], opts); !errors.Is(err, ErrUnsupportedNode) {
		t.Errorf("Equal(doc, attr): %v", err)
	}
}

// TestNodeSetRootedAtLeaf: a node set may be rooted at a text, comment or PI
// node (an XPath expression can select exactly one); its canonical form is
// that node's own.
func TestNodeSetRootedAtLeaf(t *testing.T) {
	doc := parse(t, `<a>x&gt;&#13;<!--c--><?p d?></a>`)
	a := find(t, doc, "a")
	all := func(*xdm.Node) bool { return true }
	for i, want := range []string{"x&gt;&#xD;", "<!--c-->", "<?p d?>"} {
		if got := canonSet(t, Func(a.Children[i], all), Inclusive10WithComments); got != want {
			t.Errorf("child %d: got %q want %q", i, got, want)
		}
	}
	// Without comments, a comment root renders nothing: C14N 1.0 section 1.1
	// defines the without-comments form as the node-set with comment nodes
	// removed, whatever node the set happens to be rooted at.
	if got := canonSet(t, Func(a.Children[1], all), Inclusive10); got != "" {
		t.Errorf("comment without comments: got %q", got)
	}
	// A root that is not itself a member renders nothing (section 2.3: only
	// nodes in the node-set are rendered).
	none := func(*xdm.Node) bool { return false }
	for i := range 3 {
		if got := canonSet(t, Func(a.Children[i], none), Inclusive10WithComments); got != "" {
			t.Errorf("non-member child %d rendered %q", i, got)
		}
	}
}

func TestParsePrefixList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"#default", []string{""}},
		{" p  q\tp #default\n#default q ", []string{"p", "q", ""}},
	}
	for _, c := range cases {
		if got := ParsePrefixList(c.in); !slices.Equal(got, c.want) {
			t.Errorf("ParsePrefixList(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := FormatPrefixList([]string{"", "p", "q"}); got != "#default p q" {
		t.Errorf("FormatPrefixList = %q", got)
	}
	if got := FormatPrefixList(nil); got != "" {
		t.Errorf("FormatPrefixList(nil) = %q", got)
	}
	for _, s := range []string{"#default p q", "a", ""} {
		if got := FormatPrefixList(ParsePrefixList(s)); got != s {
			t.Errorf("round trip %q -> %q", s, got)
		}
	}
}

// TestPrefixListIgnoredByInclusive: the documented reason is that a caller
// forwarding a transform's parameters should not have to branch, so passing
// a PrefixList to an inclusive algorithm must neither fail nor change a byte.
func TestPrefixListIgnoredByInclusive(t *testing.T) {
	doc := parse(t, `<r xmlns="urn:d" xmlns:p="urn:p" xmlns:u="urn:u"><a><p:b/></a></r>`)
	a := find(t, doc, "a")
	for _, alg := range allAlgorithms {
		plain := canonOf(t, a, alg)
		with := canonOf(t, a, alg, "u", "", "nosuch")
		if alg.Exclusive() {
			if plain == with {
				t.Errorf("%s: PrefixList had no effect: %q", alg, plain)
			}
			continue
		}
		if plain != with {
			t.Errorf("%s: PrefixList changed output:\n%q\n%q", alg, plain, with)
		}
	}
}

// TestDigest: Digest is Bytes fed through the hash, nothing more.
func TestDigest(t *testing.T) {
	doc := parse(t, `<r xmlns:p="urn:p"><p:a x="1">t</p:a><s/></r>`)
	s := find(t, doc, "s")
	for _, alg := range allAlgorithms {
		opts := Options{Algorithm: alg}
		b, _ := Bytes(doc, opts)
		want := sha256.Sum256(b)
		got, err := Digest(sha256.New(), doc, opts)
		if err != nil || !bytes.Equal(got, want[:]) {
			t.Errorf("%s Digest = %x, %v; want %x", alg, got, err, want)
		}
		ns := ExcludeSubtree(doc, s)
		b, _ = BytesNodeSet(ns, opts)
		want = sha256.Sum256(b)
		got, err = DigestNodeSet(sha256.New(), ns, opts)
		if err != nil || !bytes.Equal(got, want[:]) {
			t.Errorf("%s DigestNodeSet = %x, %v; want %x", alg, got, err, want)
		}
	}
}

// TestBytesNodeSetDocument: Document(x) is the whole tree whatever node of it
// x is, and canonicalizes like Bytes on the document node.
func TestBytesNodeSetDocument(t *testing.T) {
	doc := parse(t, `<?p?><r xmlns:p="urn:p"><p:a><deep/></p:a></r><!--c-->`)
	deep := find(t, doc, "deep")
	for _, alg := range allAlgorithms {
		want := canonOf(t, doc, alg)
		if got := canonSet(t, Document(deep), alg); got != want {
			t.Errorf("%s: Document(deep) = %q, want %q", alg, got, want)
		}
		if got := canonSet(t, Document(doc), alg); got != want {
			t.Errorf("%s: Document(doc) = %q, want %q", alg, got, want)
		}
	}
}

func TestEqual(t *testing.T) {
	opts := Options{Algorithm: Inclusive10}
	eq := func(a, b string) bool {
		t.Helper()
		ok, err := Equal(parse(t, a), parse(t, b), opts)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	// Serialization differences C14N erases: quote style, attribute order,
	// empty-element syntax, character references, CDATA, redundant
	// declarations and the XML declaration.
	if !eq(`<?xml version="1.0"?><a  c='2' b="1"><x/><![CDATA[&]]></a>`,
		`<a b="1" c="2"><x></x>&amp;</a>`) {
		t.Error("equivalent documents compared unequal")
	}
	if !eq(`<a xmlns:p="urn:u"><p:b xmlns:p="urn:u"/></a>`, `<a xmlns:p="urn:u"><p:b/></a>`) {
		t.Error("redundant declaration made documents unequal")
	}
	if eq(`<a b="1"/>`, `<a b="2"/>`) {
		t.Error("different values compared equal")
	}
	// A proper prefix in either direction: the shorter form ending early
	// must not read as equality.
	if eq(`<a><b/></a>`, `<a><b/><c/></a>`) || eq(`<a><b/><c/></a>`, `<a><b/></a>`) {
		t.Error("prefix compared equal")
	}
	if eq(`<a/>`, `<a>x</a>`) || eq(`<a>x</a>`, `<a/>`) {
		t.Error("empty vs text compared equal")
	}
}

// errSink is the caller's own sentinel; Write must hand it back unwrapped.
var errSink = errors.New("sink failed")

// failWriter accepts limit bytes and then fails every call, counting the
// calls it receives after the first failure.
type failWriter struct {
	limit, n   int
	callsAfter int
	failedOnce bool
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.failedOnce {
		f.callsAfter++
		return 0, errSink
	}
	if f.n+len(p) > f.limit {
		k := f.limit - f.n
		f.n = f.limit
		f.failedOnce = true
		return k, errSink
	}
	f.n += len(p)
	return len(p), nil
}

func TestWriteErrorUnwrapped(t *testing.T) {
	small := parse(t, `<a>x</a>`)
	var sb strings.Builder
	sb.WriteString(`<r xmlns:p="urn:p">`)
	for i := 0; i < 20000; i++ {
		sb.WriteString(`<p:item k="v">some text &amp; more</p:item>`)
	}
	sb.WriteString(`</r>`)
	large := parse(t, sb.String())
	full, err := Bytes(large, Options{Algorithm: Inclusive10})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		doc   *xdm.Node
		limit int
	}{
		{"fails at flush", small, 0},
		{"fails after N bytes", large, 10000},
		{"fails near the end", large, len(full) - 100},
	} {
		for _, alg := range allAlgorithms {
			w := &failWriter{limit: tc.limit}
			err := Write(w, tc.doc, Options{Algorithm: alg})
			if err != errSink { //nolint:errorlint // unwrapped is the contract
				t.Errorf("%s / %s: err = %v, want errSink itself", tc.name, alg, err)
			}
			if !errors.Is(err, errSink) {
				t.Errorf("%s / %s: errors.Is failed", tc.name, alg)
			}
			// The walk must stop rather than canonicalize the rest of a
			// large document into a writer that has already failed.
			if w.callsAfter != 0 {
				t.Errorf("%s / %s: %d writes after failure", tc.name, alg, w.callsAfter)
			}
		}
	}
	// Digest's hash never fails, but the Equal path uses its own writer and
	// must not confuse a real error with a mismatch.
	if _, err := Digest(sha256.New(), large, Options{Algorithm: Exclusive10}); err != nil {
		t.Fatal(err)
	}
}

// TestFuncCalledAtMostOnce: Func documents one call per node, which lets a
// caller back it with an expensive predicate.
func TestFuncCalledAtMostOnce(t *testing.T) {
	doc := parse(t, `<?p?><!--c--><r xmlns="urn:d" xmlns:p="urn:p" xml:lang="en" a="1"><p:x p:b="2" c="3">t<!--i--><?q?></p:x><y xml:base="b/"><z/></y></r><!--t-->`)
	for _, alg := range allAlgorithms {
		calls := map[*xdm.Node]int{}
		n := 0
		ns := Func(doc, func(x *xdm.Node) bool {
			calls[x]++
			n++
			return n%3 != 0 // an irregular subset exercises every branch
		})
		if _, err := BytesNodeSet(ns, Options{Algorithm: alg, InclusiveNamespacePrefixes: []string{"p", ""}}); err != nil {
			t.Fatal(err)
		}
		for x, c := range calls {
			if c > 1 {
				t.Errorf("%s: %v %q called %d times", alg, x.Kind, x.Name.Local, c)
			}
		}
	}
}

const dsigNS = "http://www.w3.org/2000/09/xmldsig#"

// envelopedXPath is the XML-DSig enveloped-signature transform written as an
// XPath filter (XML-DSig section 6.6.4 notes the two are equivalent).
const envelopedXPath = `(//. | //@* | //namespace::*)[not(ancestor-or-self::ds:Signature)]`

func TestFromXPathEnvelopedSignature(t *testing.T) {
	doc := parse(t, `<?p?><doc xmlns="urn:d" xmlns:ds="`+dsigNS+`" xml:lang="en"><!--c--><item id="1">v</item><ds:Signature><ds:SignedInfo a="b"/><ds:Value>x</ds:Value></ds:Signature><tail/></doc>`)
	sig := find(t, doc, "Signature")
	ns, err := FromXPath(doc, envelopedXPath, map[string]string{"ds": dsigNS})
	if err != nil {
		t.Fatal(err)
	}
	for _, alg := range allAlgorithms {
		got := canonSet(t, ns, alg)
		want := canonSet(t, ExcludeSubtree(doc, sig), alg)
		if got != want {
			t.Errorf("%s: XPath form\n%q\nExcludeSubtree\n%q", alg, got, want)
		}
	}
	// Hand-derived for C14N 1.0 with comments: the signature and its
	// descendants vanish, everything else is the whole document.
	want := "<?p?>\n<doc xmlns=\"urn:d\" xmlns:ds=\"" + dsigNS + "\" xml:lang=\"en\"><!--c--><item id=\"1\">v</item><tail></tail></doc>"
	if got := canonSet(t, ExcludeSubtree(doc, sig), Inclusive10WithComments); got != want {
		t.Errorf("enveloped:\n got %q\nwant %q", got, want)
	}
}

func TestFromXPathErrors(t *testing.T) {
	doc := parse(t, `<a xml:lang="en"><b/></a>`)
	if _, err := FromXPath(doc, "((", nil); err == nil {
		t.Error("syntax error not reported")
	}
	if _, err := FromXPath(doc, "//u:b", nil); err == nil {
		t.Error("unbound prefix not reported")
	}
	if _, err := FromXPath(doc, "error()", nil); err == nil {
		t.Error("dynamic error not reported")
	}
	// The xml prefix is bound without being passed in.
	ns, err := FromXPath(doc, "//@xml:lang | //b", nil)
	if err != nil {
		t.Fatal(err)
	}
	// a is out, so its attribute axis is still processed (section 2.3).
	// b's parent is omitted, so C14N 1.0 section 2.4 also copies a's
	// xml:lang onto b.
	if got := canonSet(t, ns, Inclusive10); got != ` xml:lang="en"<b xml:lang="en"></b>` {
		t.Errorf("got %q", got)
	}
	// Atomic results select nothing.
	ns, err = FromXPath(doc, "1, 'x'", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := canonSet(t, ns, Inclusive10); got != "" {
		t.Errorf("atomic result rendered %q", got)
	}
}

// TestElementOutAttributeIn: C14N 1.0 section 2.3, "If the element is not in
// the node-set, then the result is obtained by processing the namespace axis,
// then the attribute axis, then processing the child nodes of the element
// that are in the node-set". The output is not well-formed and is what the
// specification requires; an implementation that skips the attribute axis
// of an omitted element disagrees with every conformant one.
func TestElementOutAttributeIn(t *testing.T) {
	doc := parse(t, `<a><b c="1" d="2">t</b></a>`)
	b := find(t, doc, "b")
	ns := Func(doc, func(n *xdm.Node) bool {
		return n != b && !(n.Kind == xdm.KindAttribute && n.Name.Local == "d")
	})
	for _, alg := range allAlgorithms {
		if got, want := canonSet(t, ns, alg), `<a> c="1"t</a>`; got != want {
			t.Errorf("%s: got %q want %q", alg, got, want)
		}
	}
}

// TestSubtreeScope: an element subtree inherits its in-scope namespaces
// from ancestors above the apex in the inclusive algorithms, and the xml
// prefix is never rendered.
func TestSubtreeScope(t *testing.T) {
	doc := parse(t, `<r xmlns="urn:d" xmlns:p="urn:p" xmlns:xml="http://www.w3.org/XML/1998/namespace"><a/></r>`)
	a := find(t, doc, "a")
	if got, want := canonOf(t, a, Inclusive10), `<a xmlns="urn:d" xmlns:p="urn:p"></a>`; got != want {
		t.Errorf("inclusive: got %q want %q", got, want)
	}
	if got, want := canonOf(t, a, Exclusive10), `<a xmlns="urn:d"></a>`; got != want {
		t.Errorf("exclusive: got %q want %q", got, want)
	}
}

// Sanity: the helpers above must not hide an io.Writer that returns short
// writes without an error; bufio reports that as io.ErrShortWrite.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestShortWrite(t *testing.T) {
	err := Write(shortWriter{}, parse(t, `<a>xxxx</a>`), Options{Algorithm: Inclusive10})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("got %v", err)
	}
}

// TestSubtreeContains checks Subtree's membership as a caller sees it through
// the NodeSet interface. The canonicalizer never asks, because every node its
// walk reaches is inside the subtree, so nothing else exercises it.
func TestSubtreeContains(t *testing.T) {
	tr, err := xdm.ParseString(`<r><a x="1"><b/></a><c/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := tr.Root.Children[0]
	a, c := r.Children[0], r.Children[1]
	set := Subtree(a)
	for _, tc := range []struct {
		n    *xdm.Node
		want bool
	}{
		{a, true}, {a.Attrs[0], true}, {a.Children[0], true},
		{r, false}, {c, false}, {tr.Root, false},
	} {
		if got := set.Contains(tc.n); got != tc.want {
			t.Errorf("Contains(%v %s) = %v, want %v", tc.n.Kind, tc.n.Name.Local, got, tc.want)
		}
	}
}

// TestRelativeNamespaceURI is C14N 1.0 and 1.1 section 2.1: "implementations
// of XML canonicalization MUST report an operation failure on documents
// containing relative namespace URIs". An undeclaration is not a URI, and a
// URI with a scheme is absolute however unusual its spelling.
func TestRelativeNamespaceURI(t *testing.T) {
	for _, doc := range []string{
		`<a xmlns="foo"/>`,
		`<a xmlns:p="rel/x"><p:b/></a>`,
		`<a><b xmlns:p="../up"/></a>`, // below the apex
		`<a xmlns:p="1bad:x"/>`,       // a scheme cannot start with a digit
		`<a xmlns:p="#frag"/>`,        // a fragment alone is relative
		`<a xmlns:p="urn:x"><b xmlns:q="q"/></a>`,
	} {
		for _, alg := range allAlgorithms {
			if _, err := Bytes(parse(t, doc), Options{Algorithm: alg}); !errors.Is(err, ErrRelativeNamespaceURI) {
				t.Errorf("%s under %s: err %v, want ErrRelativeNamespaceURI", doc, alg, err)
			}
		}
	}
	// A binding declared ABOVE the start node is in scope for it, and fails
	// too, even though the declaring element is not canonicalized.
	b := find(t, parse(t, `<a xmlns:p="rel"><b/></a>`), "b")
	if _, err := Bytes(b, Options{Algorithm: Exclusive10}); !errors.Is(err, ErrRelativeNamespaceURI) {
		t.Errorf("binding above apex: err %v, want ErrRelativeNamespaceURI", err)
	}
	for _, doc := range []string{
		`<a xmlns="urn:x"><b xmlns=""/></a>`,
		`<a xmlns:p="http://example.org/p" xmlns:q="tag:x.org,2026:q" xmlns:r="C:/x"/>`,
		`<a xmlns:p="a+b.c-d:e"/>`,
	} {
		if _, err := Bytes(parse(t, doc), Options{Algorithm: Inclusive10}); err != nil {
			t.Errorf("%s: %v", doc, err)
		}
	}
}

// TestXML11Refused: Canonical XML "is applicable to XML 1.0 ... It is not
// defined for XML 1.1" (C14N 1.1 abstract), and 1.0 and Exclusive C14N share
// that data model. Canonicalizing 1.1 input would write a referenced control
// character raw, which is well-formed in neither version.
func TestXML11Refused(t *testing.T) {
	doc := parse(t, "<?xml version=\"1.1\"?><a>x&#1;y</a>")
	for _, alg := range allAlgorithms {
		if _, err := Bytes(doc, Options{Algorithm: alg}); !errors.Is(err, ErrXML11) {
			t.Errorf("%s: err %v, want ErrXML11", alg, err)
		}
		if _, err := Bytes(find(t, doc, "a"), Options{Algorithm: alg}); !errors.Is(err, ErrXML11) {
			t.Errorf("%s, element: err %v, want ErrXML11", alg, err)
		}
	}
	if _, err := Bytes(parse(t, `<?xml version="1.0"?><a/>`), Options{Algorithm: Inclusive11}); err != nil {
		t.Errorf("XML 1.0: %v", err)
	}
	// A tree built rather than parsed declares no version and is read as 1.0.
	tr := xdm.NewTree()
	tr.Root.AppendChild(&xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "a"}})
	if got, err := Bytes(tr.Root, Options{Algorithm: Inclusive10}); err != nil || string(got) != "<a></a>" {
		t.Errorf("built tree: %q, %v", got, err)
	}
}

// TestApexOwnXMLAttributes pins the one place the implementation follows the
// XML Security WG's interop result (xmlbase-c14n11spec3-103) over a literal
// reading of C14N 1.1 section 2.4: an apex keeps its own xml:lang, xml:space
// and xml:base when the node set drops them. xml:id is not a simple
// inheritable attribute and stays dropped; C14N 1.0 and Exclusive C14N are
// unchanged.
func TestApexOwnXMLAttributes(t *testing.T) {
	doc := parse(t, `<r><e xml:lang="en" xml:space="preserve" xml:base="b/" xml:id="i" k="v"/></r>`)
	elementsOnly := Func(doc, func(n *xdm.Node) bool {
		return n.Kind != xdm.KindAttribute && n.Name.Local != "r"
	})
	for alg, want := range map[Algorithm]string{
		Inclusive11: `<e xml:base="b/" xml:lang="en" xml:space="preserve"></e>`,
		Inclusive10: `<e></e>`,
		Exclusive10: `<e></e>`,
	} {
		if got := canonSet(t, elementsOnly, alg); got != want {
			t.Errorf("%s:\n got %q\nwant %q", alg, got, want)
		}
	}
}

// TestConcurrentUse canonicalizes one tree from several goroutines at once,
// with every algorithm and a shared node set: the package documents that a
// tree and a NodeSet may be shared, and -race is what checks the claim.
func TestConcurrentUse(t *testing.T) {
	doc := parse(t, `<r xmlns="urn:d" xmlns:p="urn:p" xml:lang="en"><p:a x="1"><b>t</b><!--c--></p:a><s/></r>`)
	set, err := FromXPath(doc, `//*[not(self::d:s)] | //@* | //text()`, map[string]string{"d": "urn:d"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[Algorithm][2]string{}
	for _, alg := range allAlgorithms {
		whole, _ := Bytes(doc, Options{Algorithm: alg})
		sub, _ := BytesNodeSet(set, Options{Algorithm: alg})
		want[alg] = [2]string{string(whole), string(sub)}
	}
	done := make(chan error)
	for g := 0; g < 8; g++ {
		go func() {
			for i := 0; i < 50; i++ {
				for _, alg := range allAlgorithms {
					whole, err := Bytes(doc, Options{Algorithm: alg})
					if err == nil && string(whole) != want[alg][0] {
						err = errors.New("whole-document output changed under concurrency")
					}
					sub, err2 := BytesNodeSet(set, Options{Algorithm: alg})
					if err == nil && err2 == nil && string(sub) != want[alg][1] {
						err = errors.New("subset output changed under concurrency")
					}
					if err == nil {
						err = err2
					}
					if err != nil {
						done <- err
						return
					}
				}
			}
			done <- nil
		}()
	}
	for g := 0; g < 8; g++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

// TestHasScheme pins the RFC 3986 section 3.1 scheme scan at its edges.
func TestHasScheme(t *testing.T) {
	for s, want := range map[string]bool{
		"urn:x": true, "http://a": true, "a+b.c-d:e": true, "Z9:": true, "x:": true,
		"": false, ":x": false, "1a:x": false, "+a:x": false, "rel/x": false,
		"a/b:c": false, "a b:c": false, "#f": false, "é:x": false, "a_b:x": false,
	} {
		if got := hasScheme(s); got != want {
			t.Errorf("hasScheme(%q) = %v, want %v", s, got, want)
		}
	}
}
