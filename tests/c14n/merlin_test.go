package c14nsuite

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/c14n"
	"github.com/knroy/go-xml/v2/xdm"
)

// The Baltimore "Merlin" interop signatures (Merlin Hughes, 2002), vendored
// verbatim under testdata/merlin-*; see the PROVENANCE file in each. Every
// ds:Reference in the signature is followed through its transforms, and the
// canonical octets must equal the c14n-N.txt Baltimore published for
// reference N, and their SHA-1 the reference's DigestValue. The file after
// the last reference is the canonical SignedInfo.
//
// Baltimore's implementation is independent of this package, of libxml2 and
// of the C14N 1.1 interop vendors, and merlin-c14n-three exists to exercise
// partial namespace axes: an XPath filter that keeps some of an element's
// namespace nodes, then Canonical XML 1.0 (implicit, XML-DSig §4.4.3.2),
// Exclusive C14N, or Exclusive C14N with PrefixList="#default".

func TestMerlinC14NThree(t *testing.T) {
	runMerlin(t, "merlin-c14n-three", "signature.xml")
}

func TestMerlinExcC14NOne(t *testing.T) {
	runMerlin(t, "merlin-exc-c14n-one", "exc-signature.xml")
}

func runMerlin(t *testing.T, corpus, sigFile string) {
	dir := filepath.Join("testdata", corpus)
	src, err := os.ReadFile(filepath.Join(dir, sigFile))
	if err != nil {
		t.Fatal(err) // vendored: absence is a broken checkout, not a skip
	}
	tr, err := xdm.ParseString(string(src), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc := tr.Root
	si := firstElement(doc, "SignedInfo")
	if si == nil {
		t.Fatal("no SignedInfo")
	}
	refs := childElements(si, "Reference")
	if len(refs) == 0 {
		t.Fatal("no Reference")
	}
	octets, digests := 0, 0
	for i, ref := range refs {
		t.Run(fmt.Sprintf("ref-%d", i), func(t *testing.T) {
			set, opts := dereference(t, doc, ref)
			got, err := c14n.BytesNodeSet(set, opts)
			if err != nil {
				t.Fatal(err)
			}
			if checkOctets(t, dir, i, got) {
				octets++
			}
			sum := sha1.Sum(got)
			want := strings.TrimSpace(firstElement(ref, "DigestValue").StringValue())
			if d := base64.StdEncoding.EncodeToString(sum[:]); d != want {
				t.Errorf("digest %s, signature has %s", d, want)
			} else {
				digests++
			}
		})
	}
	t.Run("SignedInfo", func(t *testing.T) {
		cm := firstElement(si, "CanonicalizationMethod")
		got, err := c14n.Bytes(si, c14n.Options{Algorithm: c14n.Algorithm(attr(cm, "Algorithm"))})
		if err != nil {
			t.Fatal(err)
		}
		checkOctets(t, dir, len(refs), got)
	})
	t.Logf("%s: octets %d/%d, digests %d/%d", corpus, octets, len(refs), digests, len(refs))
}

// dereference applies a Reference's URI and transforms, returning the node
// set and the canonicalization that produces its digest input.
func dereference(t *testing.T, doc *xdm.Node, ref *xdm.Node) (c14n.NodeSet, c14n.Options) {
	t.Helper()
	var set c14n.NodeSet
	switch uri := attr(ref, "URI"); {
	case uri == "":
		// XML-DSig §4.4.3.3: the whole document, comments removed. The
		// algorithms used after it here all omit comments anyway.
		set = c14n.Document(doc)
	case strings.HasPrefix(uri, "#xpointer(id('") && strings.HasSuffix(uri, "'))"):
		// No DTD declares Id an ID; the signature's own Id attribute is
		// what Baltimore and Santuario resolve it to.
		id := strings.TrimSuffix(strings.TrimPrefix(uri, "#xpointer(id('"), "'))")
		el := findElement(doc, func(n *xdm.Node) bool { return attr(n, "Id") == id })
		if el == nil {
			t.Fatalf("no element with Id %q", id)
		}
		set = c14n.Subtree(el) // XPointer: comments kept
	default:
		t.Fatalf("unhandled URI %q", uri)
	}
	// XML-DSig §4.4.3.2: a node set left unconverted is canonicalized with
	// Canonical XML 1.0, omitting comments.
	opts := c14n.Options{Algorithm: c14n.Inclusive10}
	for _, tf := range childElements(firstElement(ref, "Transforms"), "Transform") {
		switch alg := c14n.Algorithm(attr(tf, "Algorithm")); {
		case alg == "http://www.w3.org/TR/1999/REC-xpath-19991116":
			xp := firstElement(tf, "XPath")
			var err error
			if set, err = c14n.FromXPathFilter(doc, xp.StringValue(), xp.InScopeNamespaces()); err != nil {
				t.Fatal(err)
			}
		case alg.Valid():
			opts.Algorithm = alg
			if in := firstElement(tf, "InclusiveNamespaces"); in != nil {
				opts.InclusiveNamespacePrefixes = c14n.ParsePrefixList(attr(in, "PrefixList"))
			}
		default:
			t.Fatalf("unhandled transform %s", alg)
		}
	}
	return set, opts
}

func checkOctets(t *testing.T, dir string, i int, got []byte) bool {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("c14n-%d.txt", i)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("octets differ from c14n-%d.txt\n got: %q\nwant: %q", i, got, want)
		return false
	}
	return true
}

func attr(n *xdm.Node, local string) string {
	for a := range n.Attrs() {
		if a.Name().Local == local && a.Name().URI == "" {
			return a.Value()
		}
	}
	return ""
}

func childElements(n *xdm.Node, local string) []*xdm.Node {
	var out []*xdm.Node
	if n == nil {
		return nil
	}
	for c := range n.Children() {
		if c.Kind() == xdm.KindElement && c.Name().Local == local {
			out = append(out, c)
		}
	}
	return out
}

func findElement(n *xdm.Node, f func(*xdm.Node) bool) *xdm.Node {
	if n.Kind() == xdm.KindElement && f(n) {
		return n
	}
	for c := range n.Children() {
		if e := findElement(c, f); e != nil {
			return e
		}
	}
	return nil
}
