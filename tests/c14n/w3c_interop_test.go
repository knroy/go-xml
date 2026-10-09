package c14nsuite

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/c14n"
	"github.com/knroy/go-xml/v2/xdm"
)

// TestW3CC14N11Interop runs the XML Security WG's Canonical XML 1.1 interop
// cases from https://www.w3.org/2007/xmlsec/interop/xmldsig/c14n11/. They are
// third-party material, so they are not in the repository: tests/fetch-c14n.sh
// downloads them into testdata/c14n/w3c-c14n11, pinned by digest in
// tests/c14n-corpus.txt. Absent, the test skips — unless GOXML_C14N_W3C=1,
// which CI and tests/check.sh set, makes absence a failure.
//
// Each case is an input document, an XPath filter transform (XML-DSig
// §6.6.3: the expression is a boolean evaluated with each node as context),
// then C14N 1.1 and a SHA-1 digest. The expected result is not ours: five
// independent implementations (IAIK, IBM, Oracle, Sun, UPC) each signed the
// case, and the report records that all five agreed. Our digest is checked
// against every one of their DigestValues, and our octets against the
// canonical form IAIK (or UPC) published where one exists.
func TestW3CC14N11Interop(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "c14n", "w3c-c14n11")
	list, err := os.ReadFile(filepath.Join(dir, "testcases"))
	if err != nil {
		if os.Getenv("GOXML_C14N_W3C") == "1" {
			t.Fatalf("%v (run tests/fetch-c14n.sh)", err)
		}
		t.Skipf("W3C interop cases absent; run tests/fetch-c14n.sh (%v)", err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	parse := func(name string) *xdm.Node {
		tr, err := xdm.ParseString(read(name), xdm.ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return tr.Root
	}
	cases, passed := strings.Fields(string(list)), 0
	for _, name := range cases {
		ok := t.Run(name, func(t *testing.T) {
			tmpl := parse(name + "-template.xml")
			ref := firstElement(tmpl, "Reference")
			xp := firstElement(tmpl, "XPath")
			if ref == nil || xp == nil {
				t.Fatal("template has no Reference or XPath")
			}
			var input string
			for a := range ref.Attrs() {
				if a.Name().Local == "URI" {
					input = a.Value()
				}
			}
			// The template's transform is the XML-DSig XPath Filter, which
			// FromXPathFilter implements: namespace nodes included.
			set, err := c14n.FromXPathFilter(parse(input), xp.StringValue(), xp.InScopeNamespaces())
			if err != nil {
				t.Fatal(err)
			}
			got, err := c14n.BytesNodeSet(set, c14n.Options{Algorithm: c14n.Inclusive11})
			if err != nil {
				t.Fatal(err)
			}

			for _, f := range []string{name + "-IAIK-ref0.digestinput", name + "-UPC.output"} {
				if want, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
					// UPC's .output file ends in a newline that the digest UPC
					// signed does not cover; the digest check below is exact.
					if strings.HasSuffix(f, ".output") {
						want = bytes.TrimSuffix(want, []byte("\n"))
					}
					if string(got) != string(want) {
						t.Errorf("octets differ from %s\n got: %q\nwant: %q", f, got, want)
					}
				}
			}
			sum := sha1.Sum(got)
			digest := base64.StdEncoding.EncodeToString(sum[:])
			for _, vendor := range []string{"IAIK", "IBM", "ORCL", "SUN", "UPC"} {
				f := name + "-" + vendor + ".xml"
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					continue // UPC did not submit every case
				}
				dv := firstElement(parse(f), "DigestValue")
				if dv == nil {
					t.Errorf("%s: no DigestValue", f)
					continue
				}
				if want := strings.TrimSpace(dv.StringValue()); digest != want {
					t.Errorf("digest %s, %s signed %s", digest, vendor, want)
				}
			}
		})
		if ok {
			passed++
		}
	}
	t.Logf("W3C C14N 1.1 interop: %d/%d", passed, len(cases))
}

// firstElement returns the first element in document order whose local name
// is local.
func firstElement(n *xdm.Node, local string) *xdm.Node {
	if n.Kind() == xdm.KindElement && n.Name().Local == local {
		return n
	}
	for c := range n.Children() {
		if f := firstElement(c, local); f != nil {
			return f
		}
	}
	return nil
}
