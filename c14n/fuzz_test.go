package c14n

import (
	"bytes"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// fuzzSeeds are the constructs where canonicalization makes decisions:
// namespace rendering and undeclaration, xml:* attributes, escaping, and
// document-level nodes. Kept short, because the seed corpus runs on every
// ordinary `go test`.
var fuzzSeeds = []string{
	`<a/>`,
	`<?p d?><!--c--><a b="1" a="2">t</a><!--e--><?q?>`,
	`<r xmlns="urn:d"><a xmlns=""><b/></a></r>`,
	`<r xmlns="urn:d"><p:a xmlns:p="urn:p" xmlns=""><b/></p:a></r>`,
	`<r xmlns:p="urn:1"><p:a><p:b xmlns:p="urn:2"><p:c/></p:b></p:a></r>`,
	`<r xmlns:c="urn:a" xmlns:a="urn:c" a:z="1" c:y="2" w="3"/>`,
	`<r xml:lang="en" xml:base="a/b/" xml:id="i"><m xml:base=".."><e xml:base="x"/></m></r>`,
	`<a>x<![CDATA[<&>]]>&#13;&#65;&gt;</a>`,
	"<a b=\"&#9;&#10;&#13;\t\n\r\n>&lt;&quot;\"/>",
	`<a xmlns:p="u"><b xmlns:p=""/></a>`,
	`<a><b><c><d><e><f/></e></d></c></b></a>`,
	`<!DOCTYPE a><a/>`,
	`<a><b></a>`,
}

// FuzzCanonicalizeNoPanic: whatever the parser accepts with its defaults,
// every algorithm must canonicalize without panicking, and the inclusive
// algorithms must be idempotent on whole documents. C14N 1.0 section 2.4
// (and C14N 1.1 likewise): canonicalizing the canonical form of a document
// "results in the same canonical form", since every decision has already
// been made; a difference means some input was rendered in a form that
// parses back to something else.
func FuzzCanonicalizeNoPanic(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		tr, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			return
		}
		doc := tr.Root
		var elem, first *xdm.Node
		for _, c := range doc.Children {
			if c.Kind == xdm.KindElement {
				elem = c
			}
		}
		if elem != nil {
			for _, c := range elem.Children {
				if c.Kind == xdm.KindElement {
					first = c
					break
				}
			}
		}
		for _, alg := range allAlgorithms {
			opts := Options{Algorithm: alg, InclusiveNamespacePrefixes: []string{"", "p"}}
			out, err := Bytes(doc, opts)
			if err != nil {
				// The only legitimate refusal for a parsed document is depth,
				// and the parser's limit is above ours.
				if err != ErrDepthExceeded { //nolint:errorlint
					t.Fatalf("%s: %v", alg, err)
				}
				continue
			}
			if elem != nil {
				if _, err := Bytes(elem, opts); err != nil && err != ErrDepthExceeded { //nolint:errorlint
					t.Fatalf("%s subtree: %v", alg, err)
				}
			}
			if first != nil {
				if _, err := BytesNodeSet(ExcludeSubtree(doc, first), opts); err != nil && err != ErrDepthExceeded { //nolint:errorlint
					t.Fatalf("%s exclude: %v", alg, err)
				}
			}
			if alg.Exclusive() {
				continue
			}
			tr2, err := xdm.ParseString(string(out), xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("%s: canonical form does not parse: %v\n in  %q\n out %q", alg, err, src, out)
			}
			again, err := Bytes(tr2.Root, opts)
			if err != nil {
				t.Fatalf("%s: re-canonicalize: %v", alg, err)
			}
			if !bytes.Equal(again, out) {
				t.Fatalf("%s: not idempotent\n in     %q\n once   %q\n twice  %q", alg, src, out, again)
			}
		}
	})
}
