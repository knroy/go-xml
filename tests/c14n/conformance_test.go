// Package c14nsuite holds the c14n conformance harnesses: the worked examples
// of the three Recommendations, the differential against libxml2's xmllint,
// and the W3C C14N 1.1 interop cases. They live here, beside the other suite
// harnesses, so that the corpus they read stays out of the library package.
package c14nsuite

// Conformance tests.
//
// TestC14NConformance runs every worked example transcribed from the three
// normative specifications (local copies in testdata/c14n/specs at the
// repository root) and compares octets with the canonical form the SPEC
// prints. Expected files are the spec's text, never this package's output.
//
// TestC14NDifferentialXMLLint and TestC14NGolden compare whole-document
// canonicalization of the handwritten corpus in testdata/diff with libxml2's
// xmllint. The goldens are xmllint's output; -update regenerates them from
// xmllint, never from this package.

import (
	"bytes"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/c14n"
	"github.com/knroy/go-xml/xdm"
)

var update = flag.Bool("update", false, "regenerate testdata/diff/*.golden from xmllint")

// specCase is one worked example under one algorithm.
type specCase struct {
	dir, name string // input is testdata/<dir>/<name>.xml
	// subset names testdata/<dir>/<subset>.xpath, the spec's document subset
	// expression; empty means the whole document.
	subset string
	ns     map[string]string // prefix bindings the spec states for subset
	alg    c14n.Algorithm
	want   string // expected output: testdata/<dir>/<name><want>
	// external admits external entities, served from testdata/<dir>.
	external bool
}

var ietf = map[string]string{"ietf": "http://www.ietf.org"}

func specCases() []specCase {
	var cs []specCase
	// Canonical XML 1.0 section 3, and Canonical XML 1.1 section 3.1-3.7.
	// C14N 1.1 repeats 3.1-3.7 verbatim (input, expression and canonical
	// forms are character-identical after rendering the HTML), so the same
	// files are run under both algorithms rather than duplicated.
	for _, v := range []struct{ plain, comments c14n.Algorithm }{
		{c14n.Inclusive10, c14n.Inclusive10WithComments},
		{c14n.Inclusive11, c14n.Inclusive11WithComments},
	} {
		cs = append(cs,
			// 3.1's external subset doc.dtd is not read: the parser skips an
			// external DTD subset when no resolver is given, as the spec's
			// assumed non-validating processor may.
			specCase{dir: "c14n10", name: "3.1-pis-comments", alg: v.plain, want: ".c14n"},
			specCase{dir: "c14n10", name: "3.1-pis-comments", alg: v.comments, want: ".c14n-comments"},
			specCase{dir: "c14n10", name: "3.2-whitespace", alg: v.plain, want: ".c14n"},
			specCase{dir: "c14n10", name: "3.3-start-end-tags", alg: v.plain, want: ".c14n"},
			specCase{dir: "c14n10", name: "3.4-char-mods", alg: v.plain, want: ".c14n"},
			// 3.5's world.txt is served by dirResolver; earth.gif is an
			// unparsed entity and is never read.
			specCase{dir: "c14n10", name: "3.5-entity-refs", alg: v.plain, want: ".c14n", external: true},
			specCase{dir: "c14n10", name: "3.6-utf8", alg: v.plain, want: ".c14n"},
			// The spec's XPath 1.0 expression is used unchanged: it is also
			// valid XPath 2.0, which is what c14n.FromXPath compiles.
			specCase{dir: "c14n10", name: "3.7-document-subset", subset: "3.7-document-subset", ns: ietf, alg: v.plain, want: ".c14n"},
			// Section 4.7 (both specs) gives two inputs and says only that
			// e3 must not take on e1's default namespace when e2 is omitted.
			// The expected form is derived from that sentence, not printed.
			// The subset expression is ours: omit e2 and nothing else.
			specCase{dir: "c14n10", name: "4.7-default-ns-a", subset: "4.7-default-ns-a", ns: map[string]string{"ab": "a:b"}, alg: v.plain, want: ".c14n"},
			specCase{dir: "c14n10", name: "4.7-default-ns-b", subset: "4.7-default-ns-b", ns: map[string]string{"ab": "a:b"}, alg: v.plain, want: ".c14n"},
		)
	}
	// Canonical XML 1.1 section 3.8. The expected output is the one erratum
	// E11-01 (2008-09-16) corrects it to, xml:base="bar/foo" on e3: the
	// Recommendation prints "something/bar/foo", which contradicts its own
	// section 2.4 ("only e2 in the example in Section 3.8") and duplicates
	// "something" once the output's base URIs are resolved.
	cs = append(cs, specCase{dir: "c14n11", name: "3.8-xml-attributes", subset: "3.8-xml-attributes", ns: ietf, alg: c14n.Inclusive11, want: ".c14n11"})

	// Exclusive XML Canonicalization section 2. The spec prints these
	// "except for line wrapping", with every line indented three columns for
	// display. Transcription: the three-column display indent is removed from
	// the inputs; in the expected forms each wrapped start tag is joined with
	// single spaces, and the text nodes are the input's own, verbatim, since
	// canonicalization never changes text whitespace.
	n1b := map[string]string{"n1": "http://b.example"}
	n1net := map[string]string{"n1": "http://example.net"}
	cs = append(cs,
		// 2.1: Canonical XML of the enveloped elem1.
		specCase{dir: "exc-c14n", name: "2.1-simple", subset: "2.1-simple", ns: n1b, alg: c14n.Inclusive10, want: ".c14n"},
		// 2.1: "The first document above is in canonical form."
		specCase{dir: "exc-c14n", name: "2.1-simple-standalone", alg: c14n.Inclusive10, want: ".c14n"},
		// 2.2: inclusive differs between the two envelopes, exclusive not.
		specCase{dir: "exc-c14n", name: "2.2-reenveloping-1", subset: "2.2-reenveloping-1", ns: n1net, alg: c14n.Inclusive10, want: ".c14n"},
		specCase{dir: "exc-c14n", name: "2.2-reenveloping-2", subset: "2.2-reenveloping-2", ns: n1net, alg: c14n.Inclusive10, want: ".c14n"},
		specCase{dir: "exc-c14n", name: "2.2-reenveloping-1", subset: "2.2-reenveloping-1", ns: n1net, alg: c14n.Exclusive10, want: ".exc-c14n"},
		specCase{dir: "exc-c14n", name: "2.2-reenveloping-2", subset: "2.2-reenveloping-2", ns: n1net, alg: c14n.Exclusive10, want: ".exc-c14n"},
	)
	return cs
}

// conformanceRefusals are worked examples in the specs that are not run,
// with the reason. Nothing is skipped silently.
var conformanceRefusals = []struct{ example, reason string }{
	{"exc-c14n 3 (InclusiveNamespaces PrefixList=\"dsig soap #default\")",
		"shows only the ds:Transform markup; the spec gives no input document or canonical form"},
	{"exc-c14n 5.1 (<Foo/> moved from <Bar> into <Baz xmlns=...>)",
		"prose about interpretation in a target context; no canonical form is given"},
	{"exc-c14n 5.2 (serializing a lone attribute node)",
		"prose only; no input or canonical form is given"},
}

// dirResolver serves external entities from one testdata directory, by
// base name only: it cannot read outside it.
type dirResolver string

func (d dirResolver) ResolveEntity(systemID, _, _ string) (io.ReadCloser, string, error) {
	f, err := os.Open(filepath.Join(string(d), filepath.Base(filepath.FromSlash(systemID))))
	if err != nil {
		return nil, "", err
	}
	return f, "file:///c14n-testdata/" + filepath.Base(systemID), nil
}

func parseFile(t *testing.T, path string, resolver xdm.EntityResolver) *xdm.Node {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := xdm.ParseOptions{AllowDOCTYPE: true}
	if resolver != nil {
		opts.ExternalEntities = resolver
	}
	tree, err := xdm.ParseString(string(src), opts)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return tree.Root
}

func runSpecCase(t *testing.T, c specCase) {
	dir := filepath.Join("testdata", c.dir)
	var res xdm.EntityResolver
	if c.external {
		res = dirResolver(dir)
	}
	doc := parseFile(t, filepath.Join(dir, c.name+".xml"), res)
	want, err := os.ReadFile(filepath.Join(dir, c.name+c.want))
	if err != nil {
		t.Fatal(err)
	}
	set := c14n.Document(doc)
	if c.subset != "" {
		expr, err := os.ReadFile(filepath.Join(dir, c.subset+".xpath"))
		if err != nil {
			t.Fatal(err)
		}
		if set, err = c14n.FromXPath(doc, string(expr), c.ns); err != nil {
			t.Fatalf("subset expression: %v", err)
		}
	}
	got, err := c14n.BytesNodeSet(set, c14n.Options{Algorithm: c.alg})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("canonical form differs\n got: %q\nwant: %q", got, want)
	}
}

// joinCases are C14N 1.1 section 2.4's join-URI-References examples. The
// spec names the omitted outer value first, which is the base.
func TestC14NConformance(t *testing.T) {
	var pass, total int
	count := func(ok bool) {
		total++
		if ok {
			pass++
		}
	}

	for _, c := range specCases() {
		name := c.dir + "/" + c.name + "/" + algName(c.alg)
		count(t.Run(name, func(t *testing.T) { runSpecCase(t, c) }))
	}

	// C14N 1.1 section 2.4: "when the elements b and c are removed from the
	// following sample XML document, the correct result for the xml:base
	// attribute on element d would be "../../x"". Only that attribute is
	// specified, so only it (and a's untouched value) is asserted.
	count(t.Run("c14n11/2.4-xml-base-join/"+algName(c14n.Inclusive11), func(t *testing.T) {
		doc := parseFile(t, filepath.Join("testdata", "c14n11", "2.4-xml-base-join.xml"), nil)
		omitted := func(n *xdm.Node) bool {
			if n.Kind == xdm.KindAttribute {
				n = n.Parent
			}
			return n.Kind == xdm.KindElement && (n.Name.Local == "b" || n.Name.Local == "c")
		}
		got, err := c14n.BytesNodeSet(c14n.Func(doc, func(n *xdm.Node) bool { return !omitted(n) }), c14n.Options{Algorithm: c14n.Inclusive11})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`<a xml:base="foo/bar">`, `<d xml:base="../../x">`} {
			if !bytes.Contains(got, []byte(want)) {
				t.Errorf("output lacks %s\n got: %q", want, got)
			}
		}
	}))

	t.Logf("c14n conformance: %d/%d", pass, total)
	for _, r := range conformanceRefusals {
		t.Logf("documented refusal: %s: %s", r.example, r.reason)
	}
}

func algName(a c14n.Algorithm) string {
	switch a {
	case c14n.Inclusive10:
		return "c14n10"
	case c14n.Inclusive10WithComments:
		return "c14n10-comments"
	case c14n.Exclusive10:
		return "exc-c14n"
	case c14n.Exclusive10WithComments:
		return "exc-c14n-comments"
	case c14n.Inclusive11:
		return "c14n11"
	case c14n.Inclusive11WithComments:
		return "c14n11-comments"
	}
	return string(a)
}

// diffAlgs maps xmllint's canonicalization flags, all of which keep
// comments, to the algorithm each implements.
var diffAlgs = []struct {
	flag, suffix string
	alg          c14n.Algorithm
}{
	{"--c14n", "c14n", c14n.Inclusive10WithComments},
	{"--exc-c14n", "exc-c14n", c14n.Exclusive10WithComments},
	{"--c14n11", "c14n11", c14n.Inclusive11WithComments},
}

func diffInputs(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "diff", "*.xml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no differential corpus: %v", err)
	}
	return files
}

// xmllint returns the path of xmllint, skipping the test when it is absent
// unless GOXML_C14N_XMLLINT=1 demands it.
func xmllint(t *testing.T, required bool) string {
	t.Helper()
	p, err := exec.LookPath("xmllint")
	if err == nil {
		return p
	}
	if required || os.Getenv("GOXML_C14N_XMLLINT") == "1" {
		t.Fatalf("xmllint required but not found: %v", err)
	}
	t.Skip("xmllint not on PATH (set GOXML_C14N_XMLLINT=1 to require it)")
	return ""
}

func runXMLLint(t *testing.T, bin, flag, file string) []byte {
	t.Helper()
	out, err := exec.Command(bin, flag, file).Output()
	if err != nil {
		t.Fatalf("xmllint %s %s: %v", flag, file, err)
	}
	// On Windows xmllint writes stdout in text mode, which turns every LF
	// into CR-LF. A canonical form never holds a literal CR (C14N writes
	// one as &#xD;, and the parser folds line ends before that), so undoing
	// the translation cannot hide a real difference.
	return bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))
}

func canonFile(t *testing.T, file string, alg c14n.Algorithm) []byte {
	t.Helper()
	got, err := c14n.Bytes(parseFile(t, file, nil), c14n.Options{Algorithm: alg})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func diffName(file, suffix string) string {
	return strings.TrimSuffix(filepath.Base(file), ".xml") + "/" + suffix
}

// TestC14NDifferentialXMLLint compares this package with a live xmllint.
func TestC14NDifferentialXMLLint(t *testing.T) {
	bin := xmllint(t, false)
	var pass, total int
	for _, file := range diffInputs(t) {
		for _, a := range diffAlgs {
			total++
			if t.Run(diffName(file, a.suffix), func(t *testing.T) {
				want := runXMLLint(t, bin, a.flag, file)
				if got := canonFile(t, file, a.alg); !bytes.Equal(got, want) {
					t.Errorf("differs from xmllint %s\n got: %q\nwant: %q", a.flag, got, want)
				}
			}) {
				pass++
			}
		}
	}
	t.Logf("c14n differential vs xmllint: %d/%d", pass, total)
}

// TestC14NGolden compares this package with committed xmllint output, so the
// comparison runs where xmllint is not installed. With -update it first
// regenerates every golden from xmllint.
func TestC14NGolden(t *testing.T) {
	var bin string
	if *update {
		bin = xmllint(t, true)
	}
	var pass, total int
	for _, file := range diffInputs(t) {
		for _, a := range diffAlgs {
			golden := strings.TrimSuffix(file, ".xml") + "." + a.suffix + ".golden"
			if *update {
				if err := os.WriteFile(golden, runXMLLint(t, bin, a.flag, file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			total++
			if t.Run(diffName(file, a.suffix), func(t *testing.T) {
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatal(err)
				}
				if got := canonFile(t, file, a.alg); !bytes.Equal(got, want) {
					t.Errorf("differs from %s\n got: %q\nwant: %q", golden, got, want)
				}
			}) {
				pass++
			}
		}
	}
	t.Logf("c14n golden (xmllint): %d/%d", pass, total)
}
