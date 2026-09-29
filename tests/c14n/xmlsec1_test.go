package c14nsuite

// Differential against xmlsec1.
//
// xmlsec1 has no "canonicalize this node set" command, but a signature
// reference is exactly that: a node set, a transform chain and a digest.
// `xmlsec1 --sign --store-references` prints every reference's pre-digest
// octets, so each comparison is a ds:Reference in a signature template, and
// its pre-digest buffer is compared byte for byte with this package's output
// for the same node set.
//
// The template is enveloped: it is inserted into the input document as the
// last child of the document element (or at a <?signature?> marker in
// testdata/xmlsec1), and every reference starts with the enveloped-signature
// transform, which removes it again. Enveloped rather than detached, because
// one file then exercises the same-document reference rules and the
// enveloped-signature transform, which ExcludeSubtree implements, and the
// input is xmlsec1's main document, whose DTD resolves id() in the §3.7
// filter; enveloping (the input inside a ds:Object) would change the
// namespaces the input inherits. Same-document references follow XML-DSig 1.1
// §4.4.3.3: URI="" drops comments and URI="#xpointer(/)" keeps them, so the
// WithComments algorithms use the latter.
//
// A subset is an XPath filter transform (XML-DSig §6.6.3): a boolean
// expression evaluated with each node as context. This package's side is
// c14n.Func evaluating the same expression per node with the xpath package,
// as TestW3CC14N11Interop does.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/c14n"
	"github.com/knroy/go-xml/xdm"
)

var allAlgs = []c14n.Algorithm{
	c14n.Inclusive10, c14n.Inclusive10WithComments,
	c14n.Exclusive10, c14n.Exclusive10WithComments,
	c14n.Inclusive11, c14n.Inclusive11WithComments,
}

// xsDifference is an input on which xmlsec1 is known to differ from this
// package, with the reason. xmlsec1 derives xmlsec1's exact octets from this
// package's for one reference, algorithm and PrefixList included; returning
// them unchanged means the difference does not arise under that reference.
// refusal, instead, is the error xmlsec1 stops with on the input.
type xsDifference struct {
	input, reason string
	xmlsec1       func(r xsRef, ours string) string
	refusal       string
}

// xmlsec1Differences lists the inputs on which xmlsec1 (measured: 1.2.41 and
// 1.2.39, both over libxml2 2.9.14) differs from this package, each stated in
// docs/c14n.md, "Where xmlsec1 differs". The test fails if xmlsec1 produces anything other than the
// octets an entry derives, including this package's own output, so an entry
// must be removed the day xmlsec1 agrees. Nothing may be added here without a
// spec-grounded reason.
var xmlsec1Differences = []xsDifference{
	{input: "dtd-default-attr", reason: xsNoDTDDefaults, xmlsec1: xsDrop(` attr="dflt"`)},
	{input: "dtd-default-ns-attr", reason: xsNoDTDDefaults, xmlsec1: xsDrop(` p:a="v"`)},
	{input: "dtd-internal-entity", refusal: "Node XML_ENTITY_REF_NODE is invalid here",
		reason: "xmlsec1's command line does not replace entity references, and libxml2's canonicalizer refuses the entity reference node left in the tree; C14N 1.0 §1.1: \"Character and parsed entity references are replaced\""},
	{input: "c14n10/3.7-document-subset", reason: xsNoXMLSpaceDefault, xmlsec1: xsXMLSpaceDefault},
	{input: "c14n11/3.8-xml-attributes", reason: xsNoXMLSpaceDefault, xmlsec1: xsXMLSpaceDefault},
	{input: "xmlbase-c14n11spec-102", reason: xsNoXMLSpaceDefault, xmlsec1: xsXMLSpaceDefault},
	{input: "xmlbase-c14n11spec2-102", reason: xsNoXMLSpaceDefault, xmlsec1: xsXMLSpaceDefault},
	{input: "c14n10/4.7-default-ns-a",
		reason: "libxml2 models e2's xmlns=\"\" as a namespace node and, e2 being outside the node set, renders it as text; XPath 1.0 §5.4 gives an undeclaration no namespace node, so there is nothing to render (C14N 1.0 §4.7 expects only that e3 not take on e1's default namespace)",
		xmlsec1: func(r xsRef, ours string) string {
			if r.alg.Exclusive() && !slices.Contains(r.prefixes, "") {
				return ours // exclusive C14N renders only visibly utilised namespaces
			}
			return strings.Replace(ours, `<e1 xmlns="a:b"><e3`, `<e1 xmlns="a:b"> xmlns=""<e3`, 1)
		}},

	// Partial namespace axes (NamespaceSet). libxml2's exclusive canonicalizer
	// decides namespaces from its own stack of rendered declarations rather
	// than from the node set, which is the non-normative "constrained
	// implementation" of Exclusive C14N §3.1: it assumes an element's whole
	// namespace axis is in the set exactly when the element is. These inputs
	// are the ones where that assumption fails.
	{input: "undeclaring-element-dropped",
		reason: "libxml2 models e2's xmlns=\"\" as a namespace node and, e2 being outside the set, renders it as text; XPath 1.0 §5.4 gives an undeclaration no namespace node (the §4.7 difference above, reached through a partial axis)",
		xmlsec1: func(r xsRef, ours string) string {
			if r.alg.Exclusive() && !slices.Contains(r.prefixes, "") {
				return ours // #default not listed: e2, outside the set, renders nothing
			}
			return strings.Replace(ours, `<e1 xmlns="a:b">`, `<e1 xmlns="a:b"> xmlns=""`, 1)
		}},
	{input: "default-node-dropped-on-one-element",
		reason: "Exclusive C14N §3 renders xmlns=\"\" on a, which utilises the default namespace, has no default namespace node in the set, and whose nearest utilising output ancestor r has one; with #default listed, the Canonical XML rule does the same, and xmlsec1's own inclusive output for this input matches ours. libxml2's stack-based exclusive path omits it",
		xmlsec1: func(r xsRef, ours string) string {
			switch {
			case !r.alg.Exclusive():
				return ours
			case !slices.Contains(r.prefixes, ""):
				return strings.Replace(ours, `<a xmlns="">`, `<a>`, 1)
			}
			return strings.Replace(ours, `<a xmlns=""><b xmlns="urn:d">`, `<a><b>`, 1)
		}},
	{input: "default-node-dropped-rebound",
		reason: "Exclusive C14N §3 renders a's default namespace node (urn:2) although it is outside the set: the rule asks that a be in the set and utilise the default namespace, and that its nearest utilising output ancestor r not have the same node, which it does not (urn:1); libxml2 renders a prefixed node outside the set in that position (see prefix-node-dropped-everywhere) but not the default one. With #default listed, the Canonical XML rule gives xmlsec1's own inclusive output, which is ours; libxml2's exclusive output drops b's urn:2 altogether",
		xmlsec1: func(r xsRef, ours string) string {
			switch {
			case !r.alg.Exclusive():
				return ours
			case !slices.Contains(r.prefixes, ""):
				return strings.Replace(ours, `<a xmlns="urn:2">`, `<a>`, 1)
			}
			return strings.Replace(ours, `<a xmlns=""><b xmlns="urn:2">`, `<a><b>`, 1)
		}},
	{input: "prefix-node-dropped-everywhere",
		reason: "with bar on the PrefixList, Exclusive C14N §3 hands its namespace nodes to Canonical XML's rule, which renders only nodes in the set, and the filter removed every bar node; xmlsec1's own inclusive output for this input matches ours. libxml2 renders the visibly utilised prefix from its stack regardless of the list",
		xmlsec1: func(r xsRef, ours string) string {
			if !r.alg.Exclusive() || !slices.Contains(r.prefixes, "bar") {
				return ours
			}
			return strings.Replace(ours, `<foo:a `, `<foo:a xmlns:bar="urn:bar" `, 1)
		}},
}

const (
	xsNoDTDDefaults = "xmlsec1's command line parses without applying DTD default attributes; C14N 1.0 §1.1: \"Default attributes are added to each element\""
	// The inputs declare <!ATTLIST e2 xml:space (default|preserve) 'preserve'>
	// and omit e2, whose xml:space the inclusive algorithms carry onto e3.
	xsNoXMLSpaceDefault = xsNoDTDDefaults + ", so e2's defaulted xml:space=\"preserve\" is not inherited by e3 (the Recommendations' §3.7 and §3.8 print it); C14N 1.1 in libxml2 instead renders the attribute declaration as space=\"\""
)

func xsDrop(attr string) func(xsRef, string) string {
	return func(_ xsRef, ours string) string { return strings.Replace(ours, attr, "", 1) }
}

func xsXMLSpaceDefault(r xsRef, ours string) string {
	switch r.alg {
	case c14n.Inclusive10, c14n.Inclusive10WithComments:
		return strings.Replace(ours, ` xml:space="preserve"`, "", 1)
	case c14n.Inclusive11, c14n.Inclusive11WithComments:
		ours = strings.Replace(ours, ` xml:space="preserve"`, "", 1)
		return strings.Replace(ours, ` id="E3"`, ` id="E3" space=""`, 1)
	}
	return ours // exclusive C14N does not inherit xml:* attributes
}

// xsRef is one ds:Reference: the canonicalization algorithm, an optional
// XPath filter with its prefix bindings, and an optional PrefixList.
type xsRef struct {
	alg      c14n.Algorithm
	filter   string
	ns       map[string]string
	prefixes []string // nil: no ec:InclusiveNamespaces
}

func escapeXML(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return strings.ReplaceAll(b.String(), `"`, "&#34;")
}

func (r xsRef) xml() string {
	uri := ""
	if r.alg.WithComments() {
		uri = "#xpointer(/)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<Reference URI="%s"><Transforms><Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"/>`, uri)
	if r.filter != "" {
		b.WriteString(`<Transform Algorithm="http://www.w3.org/TR/1999/REC-xpath-19991116"><XPath`)
		for _, p := range slices.Sorted(func(yield func(string) bool) {
			for p := range r.ns {
				if p != "" && p != "xml" && !yield(p) {
					return
				}
			}
		}) {
			fmt.Fprintf(&b, ` xmlns:%s="%s"`, p, escapeXML(r.ns[p]))
		}
		fmt.Fprintf(&b, `>%s</XPath></Transform>`, escapeXML(r.filter))
	}
	fmt.Fprintf(&b, `<Transform Algorithm="%s">`, r.alg)
	if r.prefixes != nil {
		fmt.Fprintf(&b, `<ec:InclusiveNamespaces xmlns:ec="http://www.w3.org/2001/10/xml-exc-c14n#" PrefixList="%s"/>`, c14n.FormatPrefixList(r.prefixes))
	}
	b.WriteString(`</Transform></Transforms><DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256"/><DigestValue/></Reference>`)
	return b.String()
}

func signatureTemplate(refs []xsRef) string {
	var b strings.Builder
	b.WriteString(`<Signature xmlns="http://www.w3.org/2000/09/xmldsig#"><SignedInfo><CanonicalizationMethod Algorithm="http://www.w3.org/2001/10/xml-exc-c14n#"/><SignatureMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#hmac-sha256"/>`)
	for _, r := range refs {
		b.WriteString(r.xml())
	}
	b.WriteString(`</SignedInfo><SignatureValue/></Signature>`)
	return b.String()
}

// envelop inserts sig as the last child of src's document element.
func envelop(t *testing.T, src string, doc *xdm.Node, sig string) string {
	t.Helper()
	root := documentElement(doc)
	qn := root.Name.Local
	if root.Name.Prefix != "" {
		qn = root.Name.Prefix + ":" + qn
	}
	if i := strings.LastIndex(src, "</"+qn); i >= 0 {
		return src[:i] + sig + src[i:]
	}
	// An empty-element root: nothing after it can hold "/>" in this corpus.
	i := strings.LastIndex(src, "/>")
	if i < 0 {
		t.Fatal("cannot find the document element's end")
	}
	return src[:i] + ">" + sig + "</" + qn + ">" + src[i+2:]
}

func documentElement(doc *xdm.Node) *xdm.Node {
	for _, c := range doc.Children {
		if c.Kind == xdm.KindElement {
			return c
		}
	}
	return nil
}

// xmlsec1Bin returns the path of xmlsec1, skipping the test when it is absent
// unless GOXML_C14N_XMLSEC1=1 demands it.
func xmlsec1Bin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("xmlsec1")
	if err == nil {
		return p
	}
	if os.Getenv("GOXML_C14N_XMLSEC1") == "1" {
		t.Fatalf("xmlsec1 required but not found: %v", err)
	}
	t.Skip("xmlsec1 not on PATH (set GOXML_C14N_XMLSEC1=1 to require it; tests/c14n-xmlsec1.sh runs it in Docker)")
	return ""
}

const (
	preStart = "== PreDigest data - start buffer:\n"
	preEnd   = "\n== PreDigest data - end buffer\n"
	resStart = "== Result - start buffer:\n"
	resEnd   = "\n== Result - end buffer\n"
)

// runXMLSec1 signs env and returns each reference's pre-digest octets, in
// SignedInfo order, or, when xmlsec1 fails, its diagnostics. The digest
// xmlsec1 printed after each buffer is checked against the buffer, so a parse
// of the dump that is off by one octet fails.
func runXMLSec1(t *testing.T, bin, env string, n int) (bufs [][]byte, refused string) {
	t.Helper()
	dir := t.TempDir()
	in, key := filepath.Join(dir, "in.xml"), filepath.Join(dir, "key.bin")
	if err := os.WriteFile(in, []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("go-xml c14n differential"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd := exec.Command(bin, "--sign", "--hmackey", key, "--store-references", in)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Sprintf("%v\n%s", err, stderr.Bytes())
	}
	for {
		i := bytes.Index(out, []byte(preStart))
		if i < 0 {
			break
		}
		out = out[i+len(preStart):]
		j := bytes.Index(out, []byte(preEnd))
		if j < 0 {
			t.Fatal("unterminated pre-digest buffer")
		}
		data := out[:j]
		out = out[j+len(preEnd):]
		if !bytes.HasPrefix(out, []byte(resStart)) {
			t.Fatal("pre-digest buffer not followed by its digest")
		}
		out = out[len(resStart):]
		k := bytes.Index(out, []byte(resEnd))
		sum := sha256.Sum256(data)
		if k < 0 || string(out[:k]) != base64.StdEncoding.EncodeToString(sum[:]) {
			t.Fatalf("extracted pre-digest buffer does not hash to xmlsec1's digest: %q", data)
		}
		bufs = append(bufs, data)
	}
	if len(bufs) != n {
		t.Fatalf("xmlsec1 printed %d references, want %d", len(bufs), n)
	}
	return bufs, ""
}

// xsTally counts comparisons per group.
type xsTally struct {
	groups              []string
	total, same, differ map[string]int
	seen, used          map[string]bool // inputs compared; inputs that differed as documented
}

// compare runs one comparison: ours against xmlsec1's octets for reference r
// of input, or against xmlsec1's failure when refused is set.
func (x *xsTally) compare(t *testing.T, group, input string, r xsRef, theirs []byte, refused string, ours func() ([]byte, error)) {
	if x.total[group] == 0 {
		x.groups = append(x.groups, group)
	}
	x.total[group]++
	x.seen[input] = true
	var d *xsDifference
	for i := range xmlsec1Differences {
		if xmlsec1Differences[i].input == input {
			d = &xmlsec1Differences[i]
		}
	}
	t.Run(group+"/"+input+"/"+refName(r), func(t *testing.T) {
		got, err := ours()
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case refused != "" && d != nil && d.refusal != "" && strings.Contains(refused, d.refusal):
			x.differ[group]++
			x.used[input] = true
			t.Logf("expected difference: %s", d.reason)
		case refused != "":
			t.Fatalf("xmlsec1 failed: %s", refused)
		case d != nil && d.refusal != "":
			t.Fatalf("xmlsec1 no longer refuses the input; remove the expected difference (%s)", d.reason)
		case d != nil && d.xmlsec1(r, string(got)) != string(got):
			if want := d.xmlsec1(r, string(got)); string(theirs) != want {
				if bytes.Equal(got, theirs) {
					t.Fatalf("xmlsec1 now agrees; remove the expected difference (%s)", d.reason)
				}
				t.Fatalf("xmlsec1 differs, but not as documented\n   ours: %q\nxmlsec1: %q\n   want: %q", got, theirs, want)
			}
			x.differ[group]++
			x.used[input] = true
			t.Logf("expected difference: %s", d.reason)
		case !bytes.Equal(got, theirs):
			t.Errorf("differs from xmlsec1\n   ours: %q\nxmlsec1: %q", got, theirs)
		default:
			x.same[group]++
		}
	})
}

// at returns reference i's octets, nil when xmlsec1 refused the input.
func at(bufs [][]byte, i int) []byte {
	if bufs == nil {
		return nil
	}
	return bufs[i]
}

func algRefs(filter string, ns map[string]string) []xsRef {
	refs := make([]xsRef, 0, len(allAlgs)+2)
	for _, a := range allAlgs {
		refs = append(refs, xsRef{alg: a, filter: filter, ns: ns})
	}
	return refs
}

// declaredPrefixes is every prefix in scope anywhere in doc, the default
// namespace as "", xml excluded: the widest PrefixList for the document.
func declaredPrefixes(doc *xdm.Node) []string {
	set := map[string]bool{"": true}
	var walk func(*xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Kind == xdm.KindElement {
			for p := range n.InScopeNamespaces() {
				if p != "xml" {
					set[p] = true
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(doc)
	return slices.Sorted(func(yield func(string) bool) {
		for p := range set {
			if !yield(p) {
				return
			}
		}
	})
}

// absentPrefix is bound nowhere in the corpus. Exclusive C14N §3 applies the
// PrefixList to namespace nodes, and there is none with this prefix, so naming
// it must change nothing.
const absentPrefix = "nosuchprefix"

// withPrefixLists appends the two exclusive algorithms carrying a PrefixList:
// prefixes, then absentPrefix alone, after #default, and after the first
// declared prefix other than the default.
func withPrefixLists(refs []xsRef, prefixes []string) []xsRef {
	lists := [][]string{prefixes, {absentPrefix}, {"", absentPrefix}}
	if i := slices.IndexFunc(prefixes, func(p string) bool { return p != "" }); i >= 0 {
		lists = append(lists, []string{prefixes[i], absentPrefix})
	}
	for _, l := range lists {
		for _, a := range []c14n.Algorithm{c14n.Exclusive10, c14n.Exclusive10WithComments} {
			r := refs[0]
			r.alg, r.prefixes = a, l
			refs = append(refs, r)
		}
	}
	return refs
}

// checkAbsentInert fails when naming absentPrefix changes this package's
// output: the list with it must canonicalize as the list without it.
func checkAbsentInert(t *testing.T, input string, set c14n.NodeSet, r xsRef) {
	t.Helper()
	if !slices.Contains(r.prefixes, absentPrefix) {
		return
	}
	without := slices.DeleteFunc(slices.Clone(r.prefixes), func(p string) bool { return p == absentPrefix })
	with, err1 := c14n.BytesNodeSet(set, c14n.Options{Algorithm: r.alg, InclusiveNamespacePrefixes: r.prefixes})
	want, err2 := c14n.BytesNodeSet(set, c14n.Options{Algorithm: r.alg, InclusiveNamespacePrefixes: without})
	if err1 != nil || err2 != nil || !bytes.Equal(with, want) {
		t.Errorf("%s %s: naming %s changed the output (%v, %v)\n   with: %q\nwithout: %q", input, refName(r), absentPrefix, err1, err2, with, want)
	}
}

func refName(r xsRef) string {
	switch {
	case slices.Contains(r.prefixes, absentPrefix):
		return algName(r.alg) + "+prefixlist=" + c14n.FormatPrefixList(r.prefixes)
	case r.prefixes != nil:
		return algName(r.alg) + "+prefixlist"
	}
	return algName(r.alg)
}

func parseSource(t *testing.T, src string) *xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	return tree.Root
}

// filterSet is the node set an XPath filter transform selects from doc.
func filterSet(t *testing.T, doc *xdm.Node, filter string, ns map[string]string) c14n.NodeSet {
	t.Helper()
	if filter == "" {
		return c14n.Document(doc)
	}
	// FromXPathFilter is the transform itself: the filter decides every
	// node, namespace nodes included, which is what xmlsec1 does too.
	set, err := c14n.FromXPathFilter(doc, filter, ns)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// subsetCase is an input document and an XPath filter expression.
type subsetCase struct {
	group, name, src, filter string
	ns                       map[string]string
}

func subsetCases(t *testing.T) []subsetCase {
	// Filters that split an element's namespace axis: the inputs a node set
	// whose namespace declarations follow their element cannot express,
	// canonicalized through NamespaceSet (C14N 1.0 §2.3, Exclusive C14N
	// §3). Every filter is XPath 1.0, so libxml2 evaluates the same one; a
	// namespace node's name() is its prefix, and ".." its element.
	cs := []subsetCase{
		{"ns-partial", "element-dropped-namespaces-kept",
			`<e1 xmlns="a:b"><e2 xmlns:foo="urn:foo"><e3/></e2></e1>`,
			`not(self::ab:e2)`, map[string]string{"ab": "a:b"}},
		{"ns-partial", "prefix-node-dropped-everywhere",
			`<r xmlns:foo="urn:foo" xmlns:bar="urn:bar"><foo:a bar:x="1"><b/></foo:a></r>`,
			`not(name()='bar')`, nil},
		{"ns-partial", "default-node-dropped-on-one-element",
			`<r xmlns="urn:d"><a><b/></a><c/></r>`,
			`not(count(.|../namespace::*)=count(../namespace::*) and name()='' and local-name(..)='a')`, nil},
		{"ns-partial", "default-node-dropped-rebound",
			`<r xmlns="urn:1"><a xmlns="urn:2"><b/></a></r>`,
			`not(count(.|../namespace::*)=count(../namespace::*) and name()='' and local-name(..)='a')`, nil},
		{"ns-partial", "undeclaring-element-dropped",
			`<e1 xmlns="a:b"><e2 xmlns="" xmlns:foo="urn:foo"><e3/></e2></e1>`,
			`not(self::e2)`, nil},
	}
	// The spec examples with a document subset. Their .xpath files are
	// XPath 1.0 node-set expressions of the form (//. | //@* |
	// //namespace::*)[P]; the filter transform takes P.
	seen := map[string]bool{}
	for _, c := range specCases() {
		if c.subset == "" || seen[c.dir+"/"+c.name] {
			continue
		}
		seen[c.dir+"/"+c.name] = true
		expr, err := os.ReadFile(filepath.Join("testdata", c.dir, c.subset+".xpath"))
		if err != nil {
			t.Fatal(err)
		}
		e := string(expr)
		i, j := strings.Index(e, "["), strings.LastIndex(e, "]")
		if !strings.HasPrefix(e, "(//. | //@* | //namespace::*)") || i < 0 || j < i {
			t.Fatalf("%s.xpath is not of the form (//. | //@* | //namespace::*)[P]", c.subset)
		}
		src, err := os.ReadFile(filepath.Join("testdata", c.dir, c.name+".xml"))
		if err != nil {
			t.Fatal(err)
		}
		cs = append(cs, subsetCase{"spec-subset", c.dir + "/" + c.name, string(src), strings.TrimSpace(e[i+1 : j]), c.ns})
	}

	// The W3C C14N 1.1 interop cases: each template names an input
	// document and an XPath filter.
	dir := filepath.Join("..", "..", "testdata", "c14n", "w3c-c14n11")
	list, err := os.ReadFile(filepath.Join(dir, "testcases"))
	if err != nil {
		if os.Getenv("GOXML_C14N_W3C") == "1" {
			t.Fatalf("%v (run tests/fetch-c14n.sh)", err)
		}
		t.Logf("W3C interop cases absent, not compared; run tests/fetch-c14n.sh (%v)", err)
		return cs
	}
	for _, name := range strings.Fields(string(list)) {
		tb, err := os.ReadFile(filepath.Join(dir, name+"-template.xml"))
		if err != nil {
			t.Fatal(err)
		}
		tmpl := parseSource(t, string(tb))
		ref, xp := firstElement(tmpl, "Reference"), firstElement(tmpl, "XPath")
		if ref == nil || xp == nil {
			t.Fatalf("%s: template has no Reference or XPath", name)
		}
		var input string
		for _, a := range ref.Attrs {
			if a.Name.Local == "URI" {
				input = a.Value
			}
		}
		src, err := os.ReadFile(filepath.Join(dir, input))
		if err != nil {
			t.Fatal(err)
		}
		cs = append(cs, subsetCase{"w3c-c14n11", name, string(src), xp.StringValue(), xp.InScopeNamespaces()})
	}
	return cs
}

// TestC14NDifferentialXMLSec1 compares this package with a live xmlsec1.
func TestC14NDifferentialXMLSec1(t *testing.T) {
	bin := xmlsec1Bin(t)
	x := &xsTally{total: map[string]int{}, same: map[string]int{}, differ: map[string]int{}, seen: map[string]bool{}, used: map[string]bool{}}

	// Whole documents: the xmllint corpus, six algorithms, plus Exclusive
	// C14N with a PrefixList naming every prefix the document declares. The
	// same pre-digest octets are also compared with ExcludeSubtree over the
	// enveloped document, which is what the enveloped-signature transform
	// denotes.
	for _, file := range diffInputs(t) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		doc := parseSource(t, string(src))
		refs := withPrefixLists(algRefs("", nil), declaredPrefixes(doc))
		env := envelop(t, string(src), doc, signatureTemplate(refs))
		bufs, refused := runXMLSec1(t, bin, env, len(refs))
		envDoc := parseSource(t, env)
		sig := firstElement(envDoc, "Signature")
		base := strings.TrimSuffix(filepath.Base(file), ".xml")
		for i, r := range refs {
			opts := c14n.Options{Algorithm: r.alg, InclusiveNamespacePrefixes: r.prefixes}
			if r.prefixes != nil {
				checkAbsentInert(t, base, c14n.Document(doc), r)
				x.compare(t, "prefixlist", base, r, at(bufs, i), refused, func() ([]byte, error) { return c14n.Bytes(doc, opts) })
				continue
			}
			x.compare(t, "document", base, r, at(bufs, i), refused, func() ([]byte, error) { return c14n.Bytes(doc, opts) })
			x.compare(t, "enveloped", base, r, at(bufs, i), refused, func() ([]byte, error) {
				return c14n.BytesNodeSet(c14n.ExcludeSubtree(envDoc, sig), opts)
			})
		}
	}

	// Enveloped signatures in the middle of a document, where removing the
	// ds:Signature leaves the whitespace around it.
	marked, err := filepath.Glob(filepath.Join("testdata", "xmlsec1", "*.xml"))
	if err != nil || len(marked) == 0 {
		t.Fatalf("no testdata/xmlsec1 inputs: %v", err)
	}
	for _, file := range marked {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		refs := algRefs("", nil)
		env := strings.Replace(string(src), "<?signature?>", signatureTemplate(refs), 1)
		if env == string(src) {
			t.Fatalf("%s has no <?signature?> marker", file)
		}
		bufs, refused := runXMLSec1(t, bin, env, len(refs))
		envDoc := parseSource(t, env)
		sig := firstElement(envDoc, "Signature")
		base := strings.TrimSuffix(filepath.Base(file), ".xml")
		for i, r := range refs {
			x.compare(t, "enveloped", base, r, at(bufs, i), refused, func() ([]byte, error) {
				return c14n.BytesNodeSet(c14n.ExcludeSubtree(envDoc, sig), c14n.Options{Algorithm: r.alg})
			})
		}
	}

	// Subsets: an XPath filter after the enveloped-signature transform.
	for _, c := range subsetCases(t) {
		doc := parseSource(t, c.src)
		refs := withPrefixLists(algRefs(c.filter, c.ns), declaredPrefixes(doc))
		env := envelop(t, c.src, doc, signatureTemplate(refs))
		bufs, refused := runXMLSec1(t, bin, env, len(refs))
		set := filterSet(t, doc, c.filter, c.ns)
		for i, r := range refs {
			group := c.group
			if r.prefixes != nil {
				group = "prefixlist"
			}
			checkAbsentInert(t, c.name, set, r)
			opts := c14n.Options{Algorithm: r.alg, InclusiveNamespacePrefixes: r.prefixes}
			x.compare(t, group, c.name, r, at(bufs, i), refused, func() ([]byte, error) { return c14n.BytesNodeSet(set, opts) })
		}
	}

	for _, g := range x.groups {
		t.Logf("c14n differential vs xmlsec1, %s: %d compared, %d identical, %d documented differences",
			g, x.total[g], x.same[g], x.differ[g])
	}
	for _, d := range xmlsec1Differences {
		if x.seen[d.input] && !x.used[d.input] {
			t.Errorf("expected difference for %s never arose; remove it", d.input)
		}
	}
}

// genThroughputDoc is c14n/bench_test.go's genDoc, repeated here because that
// generator is internal to package c14n: the same record, so the throughput
// comparison runs on the inputs the package benchmarks measure.
func genThroughputDoc(size int) string {
	var sb strings.Builder
	sb.Grow(size + 256)
	sb.WriteString(`<r xmlns="urn:r" xmlns:p="urn:p" xmlns:q="urn:q" xml:lang="en">`)
	for i := 0; sb.Len() < size; i++ {
		fmt.Fprintf(&sb, `<p:item id="%d" p:k="v&amp;w" q:z="1"><name>n &lt; %d</name><q:x xmlns:q="urn:q2"/><!--c--><?pi d?></p:item>`, i, i)
	}
	sb.WriteString(`</r>`)
	return sb.String()
}

// TestC14NThroughputXMLSec1 measures this package against xmlsec1 on the
// inputs c14n's benchmarks use, with an enveloped signature over the whole
// document: enveloped-signature transform, Inclusive10, SHA-256. It is a
// measurement, not an assertion, and runs only when GOXML_C14N_XMLSEC1_BENCH=1
// (tests/c14n-xmlsec1.sh bench).
//
// Two comparisons, each like with like. Including the parse: xmlsec1
// --repeat re-parses the file on every iteration, so the wall time of R
// iterations less that of one, over R-1, is parse + sign without process
// start-up, set against xdm.ParseString + DigestNodeSet here. Excluding it:
// xmlsec1 reports the time its iterations spent signing ("Executed R tests in
// X msec", which does not cover the parse), set against DigestNodeSet over an
// already parsed tree. xmlsec1's figure also covers canonicalizing and
// HMAC-ing SignedInfo, a few hundred bytes. Each figure is the fastest of
// several runs.
func TestC14NThroughputXMLSec1(t *testing.T) {
	if os.Getenv("GOXML_C14N_XMLSEC1_BENCH") != "1" {
		t.Skip("set GOXML_C14N_XMLSEC1_BENCH=1 to measure")
	}
	bin := xmlsec1Bin(t)
	dir := t.TempDir()
	in, key := filepath.Join(dir, "in.xml"), filepath.Join(dir, "key.bin")
	if err := os.WriteFile(key, []byte("go-xml c14n throughput"), 0o644); err != nil {
		t.Fatal(err)
	}
	fastest := func(runs int, f func()) time.Duration {
		best := time.Duration(1<<63 - 1)
		for range runs {
			start := time.Now()
			f()
			best = min(best, time.Since(start))
		}
		return best
	}
	// xmlsec runs R iterations, returning the wall time and xmlsec1's own
	// signing time.
	xmlsec := func(r int) (wall, sign time.Duration) {
		wall = fastest(3, func() {
			out, err := exec.Command(bin, "--sign", "--repeat", fmt.Sprint(r), "--hmackey", key, "--output", os.DevNull, in).CombinedOutput()
			if err != nil {
				t.Fatalf("xmlsec1: %v\n%s", err, out)
			}
			var n int
			var ms float64
			if _, err := fmt.Sscanf(string(out), "Executed %d tests in %f msec", &n, &ms); err != nil || n != r {
				t.Fatalf("unexpected xmlsec1 report %q: %v", out, err)
			}
			if d := time.Duration(ms * float64(time.Millisecond)); sign == 0 || d < sign {
				sign = d
			}
		})
		return wall, sign
	}
	sig := signatureTemplate([]xsRef{{alg: c14n.Inclusive10}})
	opts := c14n.Options{Algorithm: c14n.Inclusive10}
	for _, size := range []int{1 << 10, 100 << 10, 10 << 20} {
		src := genThroughputDoc(size)
		env := src[:len(src)-len("</r>")] + sig + "</r>"
		if err := os.WriteFile(in, []byte(env), 0o644); err != nil {
			t.Fatal(err)
		}
		r := max(2, (200<<20)/len(env)) // about 200 MB of input per measurement
		r = min(r, 2000)
		w1, _ := xmlsec(1)
		wr, signR := xmlsec(r)
		xsParse := (wr - w1) / time.Duration(r-1)
		xsSign := signR / time.Duration(r)

		var doc *xdm.Node
		digest := func() {
			if _, err := c14n.DigestNodeSet(sha256.New(), c14n.ExcludeSubtree(doc, firstElement(doc, "Signature")), opts); err != nil {
				t.Fatal(err)
			}
		}
		runs := max(5, min(r, 200))
		oursParse := fastest(runs, func() {
			tree, err := xdm.ParseString(env, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			doc = tree.Root
			digest()
		})
		oursSign := fastest(runs, digest)

		mbps := func(d time.Duration) float64 { return float64(len(env)) / (1 << 20) / d.Seconds() }
		t.Logf("%8d bytes, with parse: c14n %v (%.0f MB/s), xmlsec1 %v (%.0f MB/s), ratio %.2f",
			len(env), oursParse, mbps(oursParse), xsParse, mbps(xsParse), oursParse.Seconds()/xsParse.Seconds())
		t.Logf("%8d bytes, without parse: c14n %v (%.0f MB/s), xmlsec1 %v (%.0f MB/s), ratio %.2f",
			len(env), oursSign, mbps(oursSign), xsSign, mbps(xsSign), oursSign.Seconds()/xsSign.Seconds())
	}
}
