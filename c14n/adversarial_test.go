package c14n

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The cases in this file are the adversarial list of the design (section
// 11.3). Every expected output was derived by hand from the specification
// texts in testdata/c14n/specs, not captured from this implementation. Where
// a case is a whole document it is also cross-checked against libxml2's
// canonicalizer (the one xmlsec1 uses) when xmllint is installed; those
// cases say so. xmllint canonicalizes whole documents only and always keeps
// comments, so the cross-check runs the WithComments variant on inputs that
// have none.

// xmllintFlag maps an algorithm to xmllint's flag for its WithComments form.
var xmllintFlag = map[Algorithm]string{
	Inclusive10: "--c14n", Exclusive10: "--exc-c14n", Inclusive11: "--c14n11",
}

// crossCheck compares a whole-document canonicalization with xmllint's. It
// skips, rather than fails, when xmllint is absent: the hand-derived
// expectations are the test; libxml2 is a second opinion.
func crossCheck(t *testing.T, doc string, alg Algorithm, want string) {
	t.Helper()
	path, err := exec.LookPath("xmllint")
	if err != nil {
		return
	}
	f := filepath.Join(t.TempDir(), "in.xml")
	if err := os.WriteFile(f, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(path, xmllintFlag[alg], f).Output()
	if err != nil {
		t.Logf("xmllint %s: %v (not a failure)", xmllintFlag[alg], err)
		return
	}
	// On Windows xmllint writes stdout in text mode, which turns every LF
	// into CR-LF. A canonical form never holds a literal CR (C14N writes
	// one as &#xD;, and the parser folds line ends before that), so undoing
	// the translation cannot hide a real difference.
	out = bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))
	if string(out) != want {
		t.Errorf("xmllint %s disagrees with the hand-derived form:\nxmllint %q\n   want %q", xmllintFlag[alg], out, want)
	}
}

// docCase is a whole-document case with a hand-derived expected output for
// each algorithm family. 1.1 differs from 1.0 only in xml:* inheritance,
// which whole documents never trigger, so incl covers both.
type docCase struct {
	name, in, incl, excl string
}

var docCases = []docCase{
	{
		// Declared on the root, used only on a grandchild: exclusive renders
		// it where it is visibly utilized (Exclusive C14N section 3).
		name: "ns used only on descendant",
		in:   `<r xmlns:p="urn:p"><a><p:b/></a></r>`,
		incl: `<r xmlns:p="urn:p"><a><p:b></p:b></a></r>`,
		excl: `<r><a><p:b xmlns:p="urn:p"></p:b></a></r>`,
	},
	{
		name: "ns declared never used",
		in:   `<r xmlns:u="urn:u"><a/></r>`,
		incl: `<r xmlns:u="urn:u"><a></a></r>`,
		excl: `<r><a></a></r>`,
	},
	{
		// p:d is back under urn:1, which its nearest output ancestor that
		// utilizes p (p:a) already rendered, so nothing is redeclared.
		name: "prefix shadowed",
		in:   `<r xmlns:p="urn:1"><p:a><p:b xmlns:p="urn:2"><p:c/></p:b><p:d/></p:a></r>`,
		incl: `<r xmlns:p="urn:1"><p:a><p:b xmlns:p="urn:2"><p:c></p:c></p:b><p:d></p:d></p:a></r>`,
		excl: `<r><p:a xmlns:p="urn:1"><p:b xmlns:p="urn:2"><p:c></p:c></p:b><p:d></p:d></p:a></r>`,
	},
	{
		// C14N 1.0 section 2.3: xmlns="" is emitted only when the nearest
		// output ancestor has a non-empty default. b's nearest output
		// ancestor is a, which has none, so b gets nothing.
		name: "no-namespace element under default",
		in:   `<r xmlns="urn:d"><a xmlns=""><b/></a><c/></r>`,
		incl: `<r xmlns="urn:d"><a xmlns=""><b></b></a><c></c></r>`,
		excl: `<r xmlns="urn:d"><a xmlns=""><b></b></a><c></c></r>`,
	},
	{
		// The goxmldsig #92 class. p:a does not utilize the default
		// namespace, so exclusive renders nothing for it there; b does, and
		// the nearest output ancestor utilizing it (r) rendered urn:d, so
		// b needs xmlns="". Inclusive puts xmlns="" on p:a instead.
		name: "xmlns empty skips a prefixed element",
		in:   `<r xmlns="urn:d"><p:a xmlns:p="urn:p" xmlns=""><b/></p:a></r>`,
		incl: `<r xmlns="urn:d"><p:a xmlns="" xmlns:p="urn:p"><b></b></p:a></r>`,
		excl: `<r xmlns="urn:d"><p:a xmlns:p="urn:p"><b xmlns=""></b></p:a></r>`,
	},
	{
		// No default namespace anywhere: xmlns="" must never appear.
		name: "no default anywhere",
		in:   `<p:r xmlns:p="urn:p"><a><p:b><c/></p:b></a></p:r>`,
		incl: `<p:r xmlns:p="urn:p"><a><p:b><c></c></p:b></a></p:r>`,
		excl: `<p:r xmlns:p="urn:p"><a><p:b><c></c></p:b></a></p:r>`,
	},
	{
		// Attribute order is by namespace URI, and prefixes are chosen so
		// that prefix order is the reverse of URI order.
		name: "attributes across three namespaces",
		in:   `<r xmlns:c="urn:a" xmlns:a="urn:c" xmlns:b="urn:b" a:z="1" c:y="2" b:x="3" w="4" b:a="5"/>`,
		incl: `<r xmlns:a="urn:c" xmlns:b="urn:b" xmlns:c="urn:a" w="4" c:y="2" b:a="5" b:x="3" a:z="1"></r>`,
		excl: `<r xmlns:a="urn:c" xmlns:b="urn:b" xmlns:c="urn:a" w="4" c:y="2" b:a="5" b:x="3" a:z="1"></r>`,
	},
	{
		// An unprefixed attribute is in no namespace and so does not
		// visibly utilize the default namespace.
		name: "unprefixed attribute does not use default",
		in:   `<p:r xmlns:p="urn:p" xmlns="urn:d" a="1"/>`,
		incl: `<p:r xmlns="urn:d" xmlns:p="urn:p" a="1"></p:r>`,
		excl: `<p:r xmlns:p="urn:p" a="1"></p:r>`,
	},
	{
		name: "CDATA adjacent to text and char refs",
		in:   `<a>x<![CDATA[<&>]]>&#65;&amp;y<![CDATA[]]>z</a>`,
		incl: `<a>x&lt;&amp;&gt;A&amp;yz</a>`,
		excl: `<a>x&lt;&amp;&gt;A&amp;yz</a>`,
	},
	{
		name: "gt in text and attribute",
		in:   `<a b="x>y">1 &gt; 0 ></a>`,
		incl: `<a b="x>y">1 &gt; 0 &gt;</a>`,
		excl: `<a b="x>y">1 &gt; 0 &gt;</a>`,
	},
	{
		// XML 1.0 section 3.3.3: literal tab, newline and CR/LF in an
		// attribute value normalize to one space each (the CRLF is one line
		// end first). Character references survive normalization and must
		// be re-escaped, or a re-parse would normalize them away.
		name: "attribute whitespace",
		in:   "<a b=\"x\ty\nz\r\nw\" c=\"&#9;&#10;&#13;\"/>",
		incl: `<a b="x y z w" c="&#x9;&#xA;&#xD;"></a>`,
		excl: `<a b="x y z w" c="&#x9;&#xA;&#xD;"></a>`,
	},
	{
		// xml:* attributes are ordinary attributes when nothing is omitted;
		// their namespace URI sorts after "urn:".
		name: "xml attributes in place",
		in:   `<a xml:lang="en" xmlns:z="urn:z" z:b="1" c="2"/>`,
		incl: `<a xmlns:z="urn:z" c="2" xml:lang="en" z:b="1"></a>`,
		excl: `<a xmlns:z="urn:z" c="2" xml:lang="en" z:b="1"></a>`,
	},
}

func TestAdversarialDocuments(t *testing.T) {
	for _, tc := range docCases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parse(t, tc.in)
			for _, alg := range allAlgorithms {
				want := tc.incl
				if alg.Exclusive() {
					want = tc.excl
				}
				if got := canonOf(t, doc, alg); got != want {
					t.Errorf("%s:\n got %q\nwant %q", alg, got, want)
				}
			}
			// Cross-checked against xmllint (libxml2 2.9.13 locally).
			crossCheck(t, tc.in, Inclusive10, tc.incl)
			crossCheck(t, tc.in, Inclusive11, tc.incl)
			crossCheck(t, tc.in, Exclusive10, tc.excl)
		})
	}
}

// TestPIAndCommentPlacement: PI before the document element, comment inside
// a subtree. Cross-checked against xmllint for the whole document.
func TestPIAndCommentPlacement(t *testing.T) {
	src := `<?pi a?><!--top--><r><!--in--><a/></r><!--end--><?z?>`
	doc := parse(t, src)
	r := find(t, doc, "r")
	whole := "<?pi a?>\n<!--top-->\n<r><!--in--><a></a></r>\n<!--end-->\n<?z?>"
	for _, alg := range allAlgorithms {
		wantDoc, wantSub := "<?pi a?>\n<r><a></a></r>\n<?z?>", "<r><a></a></r>"
		if alg.WithComments() {
			wantDoc, wantSub = whole, "<r><!--in--><a></a></r>"
		}
		if got := canonOf(t, doc, alg); got != wantDoc {
			t.Errorf("%s doc: got %q want %q", alg, got, wantDoc)
		}
		if got := canonOf(t, r, alg); got != wantSub {
			t.Errorf("%s subtree: got %q want %q", alg, got, wantSub)
		}
	}
	crossCheck(t, src, Inclusive10, whole)
	crossCheck(t, src, Exclusive10, whole)
	crossCheck(t, src, Inclusive11, whole)
}

// TestApexNamespaces: subtree apexes, where the algorithms differ most.
func TestApexNamespaces(t *testing.T) {
	doc := parse(t, `<r xmlns="urn:d" xmlns:p="urn:p" xmlns:u="urn:u"><p:a><p:b/><c/></p:a><x><y xmlns=""/></x></r>`)
	a, x, y := find(t, doc, "a"), find(t, doc, "x"), find(t, doc, "y")
	cases := []struct {
		name     string
		n        *xdm.Node
		alg      Algorithm
		prefixes []string
		want     string
	}{
		// Inclusive: the apex renders its whole in-scope namespace axis.
		{"incl apex", a, Inclusive10, nil, `<p:a xmlns="urn:d" xmlns:p="urn:p" xmlns:u="urn:u"><p:b></p:b><c></c></p:a>`},
		// Exclusive: only p at the apex; c utilizes the default namespace
		// and no output ancestor rendered it, so c renders it.
		{"excl apex", a, Exclusive10, nil, `<p:a xmlns:p="urn:p"><p:b></p:b><c xmlns="urn:d"></c></p:a>`},
		// PrefixList #default: the default is rendered at the apex by the
		// inclusive rule, so c no longer needs it.
		{"excl #default", a, Exclusive10, []string{""}, `<p:a xmlns="urn:d" xmlns:p="urn:p"><p:b></p:b><c></c></p:a>`},
		// A PrefixList entry naming an in-scope, unused prefix renders it.
		{"excl unused listed", a, Exclusive10, []string{"u"}, `<p:a xmlns:p="urn:p" xmlns:u="urn:u"><p:b></p:b><c xmlns="urn:d"></c></p:a>`},
		// A PrefixList entry naming nothing in scope renders nothing and is
		// not an error.
		{"excl listed not in scope", a, Exclusive10, []string{"nosuch"}, `<p:a xmlns:p="urn:p"><p:b></p:b><c xmlns="urn:d"></c></p:a>`},
		// y undeclares the default. At a subtree apex there is no output
		// ancestor with a non-empty default, so xmlns="" is not emitted.
		{"incl apex undeclared default", y, Inclusive10, nil, `<y xmlns:p="urn:p" xmlns:u="urn:u"></y>`},
		{"excl apex undeclared default", y, Exclusive10, nil, `<y></y>`},
		{"excl apex undeclared default listed", y, Exclusive10, []string{""}, `<y></y>`},
		// Rendered from x: now x rendered urn:d, so y needs xmlns="".
		{"excl undeclared below apex", x, Exclusive10, nil, `<x xmlns="urn:d"><y xmlns=""></y></x>`},
		{"incl undeclared below apex", x, Inclusive11, nil, `<x xmlns="urn:d" xmlns:p="urn:p" xmlns:u="urn:u"><y xmlns=""></y></x>`},
	}
	for _, c := range cases {
		if got := canonOf(t, c.n, c.alg, c.prefixes...); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// TestPrefixUsedOnlyByExcludedAttribute: visible utilization counts only
// attributes in the node-set (Exclusive C14N section 3, "the attributes in
// the node-set of E"), so removing p:x removes the need for xmlns:p.
func TestPrefixUsedOnlyByExcludedAttribute(t *testing.T) {
	doc := parse(t, `<r xmlns:p="urn:p"><a p:x="1" y="2"/></r>`)
	ns := Func(doc, func(n *xdm.Node) bool {
		return !(n.Kind == xdm.KindAttribute && n.Name.Local == "x")
	})
	if got, want := canonSet(t, ns, Exclusive10), `<r><a y="2"></a></r>`; got != want {
		t.Errorf("exclusive: got %q want %q", got, want)
	}
	if got, want := canonSet(t, ns, Inclusive10), `<r xmlns:p="urn:p"><a y="2"></a></r>`; got != want {
		t.Errorf("inclusive: got %q want %q", got, want)
	}
	// And with p:x kept, exclusive renders the declaration on a.
	if got, want := canonOf(t, doc, Exclusive10), `<r><a xmlns:p="urn:p" y="2" p:x="1"></a></r>`; got != want {
		t.Errorf("exclusive whole: got %q want %q", got, want)
	}
}

// TestXMLAttributeInheritance: xml:lang, xml:space, xml:id and xml:base on
// an omitted ancestor, across the three algorithms. C14N 1.0 section 2.4
// copies every xml:* attribute; C14N 1.1 section 2.4 copies only xml:lang and
// xml:space, drops xml:id and fixes up xml:base; Exclusive C14N section 3
// copies nothing.
func TestXMLAttributeInheritance(t *testing.T) {
	doc := parse(t, `<r xml:lang="en" xml:space="preserve" xml:id="i1" xml:base="http://e.org/x/"><m xml:lang="fr" xml:base="y/"><e xml:space="default"><f/></e></m></r>`)
	e := find(t, doc, "e")
	cases := map[Algorithm]string{
		// Nearest occurrence wins (m's lang and base over r's); e's own
		// xml:space suppresses the inherited one; xml:id comes from r.
		Inclusive10: `<e xml:base="y/" xml:id="i1" xml:lang="fr" xml:space="default"><f></f></e>`,
		// Base is r's and m's joined: "y/" against "http://e.org/x/".
		Inclusive11: `<e xml:base="http://e.org/x/y/" xml:lang="fr" xml:space="default"><f></f></e>`,
		Exclusive10: `<e xml:space="default"><f></f></e>`,
	}
	for alg, want := range cases {
		if got := canonOf(t, e, alg); got != want {
			t.Errorf("%s:\n got %q\nwant %q", alg, got, want)
		}
	}
	// Only the apex inherits: f's parent e is in the set.
	f := find(t, doc, "f")
	if got, want := canonOf(t, f, Inclusive10), `<f xml:base="y/" xml:id="i1" xml:lang="fr" xml:space="default"></f>`; got != want {
		t.Errorf("f 1.0: got %q want %q", got, want)
	}
}

// TestXMLBaseFixup: the worked examples of C14N 1.1 section 2.4.
func TestXMLBaseFixup(t *testing.T) {
	// "when the elements b and c are removed ... the correct result for
	// the xml:base attribute on element d would be ../../x".
	doc := parse(t, `<a xml:base="foo/bar"><b xml:base=".."><c xml:base=".."><d xml:base="x"/></c></b></a>`)
	b, c := find(t, doc, "b"), find(t, doc, "c")
	ns := omit(doc, b, c)
	if got, want := canonSet(t, ns, Inclusive11), `<a xml:base="foo/bar"><d xml:base="../../x"></d></a>`; got != want {
		t.Errorf("1.1:\n got %q\nwant %q", got, want)
	}
	// 1.0 copies only when E lacks the attribute; d has its own.
	if got, want := canonSet(t, ns, Inclusive10), `<a xml:base="foo/bar"><d xml:base="x"></d></a>`; got != want {
		t.Errorf("1.0:\n got %q\nwant %q", got, want)
	}

	// "abc/" and "../" result in "": the attribute must not be rendered.
	doc = parse(t, `<r xml:base="abc/"><e xml:base="../"/></r>`)
	e := find(t, doc, "e")
	if got, want := canonOf(t, e, Inclusive11), `<e></e>`; got != want {
		t.Errorf("empty result: got %q want %q", got, want)
	}
	if got, want := canonOf(t, e, Inclusive10), `<e xml:base="../"></e>`; got != want {
		t.Errorf("1.0 verbatim: got %q want %q", got, want)
	}

	// The fix-up is performed only if an omitted element had xml:base.
	// Specifically it is not performed when the element is present but its
	// attribute is removed: here e's own xml:base is out of the set and
	// nothing above it had one, so no value is joined. The attribute itself
	// is still rendered, verbatim: e is the apex, and the XML Security WG's
	// interop case xmlbase-c14n11spec3-103 keeps an apex's own xml:base when
	// the node set drops it (see inherit in canon.go).
	doc = parse(t, `<r><e xml:base="k/"/></r>`)
	ns = Func(doc, func(n *xdm.Node) bool {
		return n.Kind != xdm.KindAttribute && n.Name.Local != "r"
	})
	if got, want := canonSet(t, ns, Inclusive11), `<e xml:base="k/"></e>`; got != want {
		t.Errorf("attribute removed: got %q want %q", got, want)
	}

	// Only contiguously omitted ancestors contribute: r is output, so its
	// base is not joined into e's (m is the only omitted ancestor).
	doc = parse(t, `<r xml:base="http://h/a/"><m xml:base="b/"><e/></m></r>`)
	m := find(t, doc, "m")
	ns = omit(doc, m)
	if got, want := canonSet(t, ns, Inclusive11), `<r xml:base="http://h/a/"><e xml:base="b/"></e></r>`; got != want {
		t.Errorf("contiguous:\n got %q\nwant %q", got, want)
	}
}

// omit is doc's whole tree minus the given elements and their attributes,
// but not their descendants: the shape that makes a set discontiguous.
func omit(doc *xdm.Node, els ...*xdm.Node) NodeSet {
	return Func(doc, func(n *xdm.Node) bool {
		for _, e := range els {
			if n == e || n.Kind == xdm.KindAttribute && n.Parent == e {
				return false
			}
		}
		return true
	})
}

// TestJoinURIReferences exercises join-URI-References directly: the three
// examples the specification gives, and RFC 3986 section 5.4's normal
// examples, which the modified algorithm must still get right when the base
// is absolute.
func TestJoinURIReferences(t *testing.T) {
	cases := []struct{ base, ref, want string }{
		// C14N 1.1 section 2.4.
		{"abc/", "../", ""},
		{"../", "../", "../../"},
		{"..", "..", "../../"},
		// Only leading ".." segments survive; this one cancels "foo".
		{"foo/bar", "../x", "x"},
		// RFC 3986 section 5.4.1, base http://a/b/c/d;p?q.
		{"http://a/b/c/d;p?q", "g:h", "g:h"},
		{"http://a/b/c/d;p?q", "g", "http://a/b/c/g"},
		{"http://a/b/c/d;p?q", "./g", "http://a/b/c/g"},
		{"http://a/b/c/d;p?q", "g/", "http://a/b/c/g/"},
		{"http://a/b/c/d;p?q", "/g", "http://a/g"},
		{"http://a/b/c/d;p?q", "//g", "http://g"},
		{"http://a/b/c/d;p?q", "?y", "http://a/b/c/d;p?y"},
		{"http://a/b/c/d;p?q", "g?y", "http://a/b/c/g?y"},
		{"http://a/b/c/d;p?q", "", "http://a/b/c/d;p?q"},
		{"http://a/b/c/d;p?q", ".", "http://a/b/c/"},
		{"http://a/b/c/d;p?q", "./", "http://a/b/c/"},
		{"http://a/b/c/d;p?q", "..", "http://a/b/"},
		{"http://a/b/c/d;p?q", "../g", "http://a/b/g"},
		{"http://a/b/c/d;p?q", "../..", "http://a/"},
		{"http://a/b/c/d;p?q", "../../g", "http://a/g"},
		// RFC 3986 section 5.4.2, abnormal: ".." above the root of an
		// absolute path is dropped.
		{"http://a/b/c/d;p?q", "../../../g", "http://a/g"},
		{"http://a/b/c/d;p?q", "/./g", "http://a/g"},
		// The fragment of the reference is ignored (C14N 1.1's change).
		{"http://a/b/c/d;p?q", "g#s", "http://a/b/c/g"},
		// Merge with an authority and an empty base path (RFC 3986 5.2.3).
		{"http://a", "g", "http://a/g"},
		// Consecutive slashes collapse.
		{"a//b/", "c", "a/b/c"},
	}
	for _, c := range cases {
		if got := joinURIReferences(c.base, c.ref); got != c.want {
			t.Errorf("join(%q, %q) = %q, want %q", c.base, c.ref, got, c.want)
		}
	}
}

// TestDiscontiguous: sets with holes, where an output element's nearest
// output ancestor is not its parent.
func TestDiscontiguous(t *testing.T) {
	// Two sibling subtrees, one excluded (the enveloped-signature shape).
	doc := parse(t, `<r xmlns:p="urn:p"><p:s1 a="0"><x/></p:s1><p:s2 a="1"><y/></p:s2></r>`)
	s1, s2 := find(t, doc, "s1"), find(t, doc, "s2")
	r := find(t, doc, "r")
	checks := []struct {
		name string
		ns   NodeSet
		alg  Algorithm
		want string
	}{
		{"incl s1 excluded", ExcludeSubtree(doc, s1), Inclusive10, `<r xmlns:p="urn:p"><p:s2 a="1"><y></y></p:s2></r>`},
		{"excl s1 excluded", ExcludeSubtree(doc, s1), Exclusive10, `<r><p:s2 xmlns:p="urn:p" a="1"><y></y></p:s2></r>`},
		// Rooted at an element: its ancestors' declarations are still in
		// scope for the inclusive algorithms.
		{"incl element root", ExcludeSubtree(r, s2), Inclusive11, `<r xmlns:p="urn:p"><p:s1 a="0"><x></x></p:s1></r>`},
		{"exclude the root", ExcludeSubtree(r, r), Inclusive10, ``},
		{"exclude outside root", ExcludeSubtree(s1, s2), Exclusive10, `<p:s1 xmlns:p="urn:p" a="0"><x></x></p:s1>`},
	}
	for _, c := range checks {
		if got := canonSet(t, c.ns, c.alg); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}

	// An omitted middle element that changes the namespace context. m
	// undeclares the default, adds q, redeclares p and carries xml:lang.
	doc = parse(t, `<r xmlns="urn:d" xmlns:p="urn:1"><m xmlns="" xmlns:q="urn:q" xmlns:p="urn:2" xml:lang="fr"><e p:a="1"/></m></r>`)
	m := find(t, doc, "m")
	ns := omit(doc, m)
	want := map[Algorithm]string{
		// e: default absent while r rendered urn:d -> xmlns=""; p differs
		// from r's; q not rendered by any output ancestor; m's xml:lang
		// copied because e's parent is omitted, and sorted before p:a
		// because "http://www.w3.org/..." < "urn:2".
		Inclusive10: `<r xmlns="urn:d" xmlns:p="urn:1"><e xmlns="" xmlns:p="urn:2" xmlns:q="urn:q" xml:lang="fr" p:a="1"></e></r>`,
		Inclusive11: `<r xmlns="urn:d" xmlns:p="urn:1"><e xmlns="" xmlns:p="urn:2" xmlns:q="urn:q" xml:lang="fr" p:a="1"></e></r>`,
		// e utilizes the default (unprefixed) and p; not q; no xml:lang.
		Exclusive10: `<r xmlns="urn:d"><e xmlns="" xmlns:p="urn:2" p:a="1"></e></r>`,
	}
	for alg, w := range want {
		if got := canonSet(t, ns, alg); got != w {
			t.Errorf("middle omitted %s:\n got %q\nwant %q", alg, got, w)
		}
	}

	// The document node itself omitted while its element is kept: the
	// document-level PI/comment rules do not depend on it.
	doc = parse(t, `<?p?><r/>`)
	ns = Func(doc, func(n *xdm.Node) bool { return n.Kind != xdm.KindDocument })
	if got, want := canonSet(t, ns, Inclusive10), "<?p?>\n<r></r>"; got != want {
		t.Errorf("document node omitted: got %q want %q", got, want)
	}
}

// TestMaxDepthBoundaries: MaxDepth counts elements from the node
// canonicalization starts at, the document element being depth 1.
func TestMaxDepthBoundaries(t *testing.T) {
	nested := func(n int) *xdm.Node {
		src := strings.Repeat("<e>", n) + strings.Repeat("</e>", n)
		// The parser's own limit (xdm.DefaultMaxDepth, 1000) is above every
		// depth used here, so the defaults parse all of them.
		return parse(t, src)
	}
	opts := Options{Algorithm: Exclusive10}
	for _, tc := range []struct {
		depth int
		ok    bool
	}{{400, true}, {500, true}, {501, false}, {600, false}} {
		_, err := Bytes(nested(tc.depth), opts)
		if tc.ok && err != nil {
			t.Errorf("depth %d: %v", tc.depth, err)
		}
		if !tc.ok && !errors.Is(err, ErrDepthExceeded) {
			t.Errorf("depth %d: err = %v, want ErrDepthExceeded", tc.depth, err)
		}
	}

	// Depth is relative to the start: a subtree 300 levels down in a
	// 600-level document is only 301 deep.
	doc := nested(600)
	inner := doc.FirstChild()
	for range 299 {
		inner = inner.FirstChild()
	}
	if _, err := Bytes(inner, opts); err != nil {
		t.Errorf("inner subtree: %v", err)
	}

	// Raising MaxDepth admits the deeper document.
	old := MaxDepth
	t.Cleanup(func() { MaxDepth = old })
	MaxDepth = 700
	b, err := Bytes(doc, opts)
	if err != nil {
		t.Fatalf("raised MaxDepth: %v", err)
	}
	if want := strings.Repeat("<e>", 600) + strings.Repeat("</e>", 600); string(b) != want {
		t.Error("raised MaxDepth: wrong output")
	}

	// The repository's limit boundaries (docs/options.md): zero and negative
	// mean the default rather than refusing everything, 1 admits exactly one
	// level, and the largest value a caller can name is permissive.
	for _, tc := range []struct {
		max, depth int
		ok         bool
	}{
		{0, 500, true}, {0, 501, false},
		{-1, 500, true}, {-1, 501, false},
		{math.MinInt, 500, true},
		{1, 1, true}, {1, 2, false},
		{math.MaxInt, 600, true},
	} {
		MaxDepth = tc.max
		_, err := Bytes(nested(tc.depth), opts)
		if tc.ok && err != nil {
			t.Errorf("MaxDepth %d, depth %d: %v", tc.max, tc.depth, err)
		}
		if !tc.ok && !errors.Is(err, ErrDepthExceeded) {
			t.Errorf("MaxDepth %d, depth %d: err = %v, want ErrDepthExceeded", tc.max, tc.depth, err)
		}
	}
}

// TestDeterminism: map iteration order is random per run in Go; any that
// leaked into the output would show up as a differing byte within a
// thousand runs. The input is rooted at an inner element so that the
// in-scope namespaces above the apex (read from a map) matter.
func TestDeterminism(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<r xmlns="urn:d"`)
	for i := 0; i < 26; i++ {
		c := string(rune('a' + i))
		sb.WriteString(` xmlns:` + c + `="urn:` + c + `" ` + c + `:x="` + c + `"`)
	}
	sb.WriteString(` xml:lang="en" xml:base="b/"><m xml:id="i" xmlns:z="urn:zz"><k:e xmlns:k="urn:k2" z:y="1" q:y="2" a:a="3">`)
	sb.WriteString(`<z:f/><g xmlns=""/></k:e></m></r>`)
	doc := parse(t, sb.String())
	e := find(t, doc, "e")
	for _, alg := range allAlgorithms {
		for _, n := range []*xdm.Node{doc, e} {
			opts := Options{Algorithm: alg, InclusiveNamespacePrefixes: []string{"q", "", "c", "b"}}
			first, err := Bytes(n, opts)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 1000; i++ {
				b, _ := Bytes(n, opts)
				if !bytes.Equal(b, first) {
					t.Fatalf("%s run %d differs:\n%q\n%q", alg, i, b, first)
				}
			}
		}
	}
}

// TestDOCTYPERefused: the entity-expansion attack never reaches this
// package, because the parser's defaults refuse the DOCTYPE outright.
func TestDOCTYPERefused(t *testing.T) {
	lol := `<?xml version="1.0"?>
<!DOCTYPE lolz [
 <!ENTITY lol "lol">
 <!ENTITY lol1 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
 <!ENTITY lol2 "&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;">
 <!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">
 <!ENTITY lol4 "&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;">
 <!ENTITY lol5 "&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;">
 <!ENTITY lol6 "&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;">
 <!ENTITY lol7 "&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;">
 <!ENTITY lol8 "&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;">
 <!ENTITY lol9 "&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;">
]>
<lolz>&lol9;</lolz>`
	tr, err := xdm.ParseString(lol, xdm.ParseOptions{})
	if err == nil || tr != nil {
		t.Fatalf("billion laughs parsed with default options: tree=%v err=%v", tr != nil, err)
	}
}

// TestNamespaceScopeNotQuadratic bounds how inclusive canonicalization's cost
// grows with the number of namespace bindings in scope.
//
// Each element's namespace axis used to be computed by scanning every binding
// in scope and looking each up in two stacks, so a document declaring many
// prefixes on its root cost (elements × bindings²): 1,000 declarations over
// 20,000 elements, 127 KB in all, took 29 seconds. Now only an apex scans the
// scope, other elements consider their own declarations, and lookups are map
// reads.
//
// So an element that declares nothing should cost the same whatever is in
// scope. Scanning the scope at every element instead is O(elements ×
// bindings) — linear in either alone, but both grow with the input, so it is
// quadratic in the document all the same.
//
// The work is CPU, not allocation, so the test counts it: calls to consider,
// one per binding examined. At 1,000 bindings over 20,000 elements that is
// about 1,000 now, and 20 million with the per-element scan restored; the
// bound is the element count plus the declarations. It used to compare a
// ratio of times, which a loaded runner can still skew; a count cannot be.
func TestNamespaceScopeNotQuadratic(t *testing.T) {
	const decls, elements = 1000, 20000
	var b strings.Builder
	b.WriteString("<r")
	for i := 0; i < decls; i++ {
		fmt.Fprintf(&b, ` xmlns:p%d="urn:%d"`, i, i)
	}
	b.WriteString(">")
	for i := 0; i < elements/2; i++ {
		b.WriteString("<c><d/></c>")
	}
	b.WriteString("</r>")
	c, err := run(io.Discard, Document(parse(t, b.String())), Options{Algorithm: Inclusive10})
	if err != nil {
		t.Fatal(err)
	}
	if bound := elements + decls; c.considered > bound {
		t.Errorf("%d bindings over %d elements examined %d bindings, bound %d: "+
			"elements that declare nothing are scanning the scope again", decls, elements, c.considered, bound)
	}
}
