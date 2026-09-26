package c14n

import (
	"bytes"
	"errors"
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
	// Parsed, then refused: a relative namespace URI (the input the fuzzer
	// found once that refusal existed) and XML 1.1.
	`<a xmlns="0"/>`,
	`<?xml version="1.1"?><a/>`,
	// Partial namespace axes for the FromXPathFilter checks.
	`<e1 xmlns="a:b"><p xmlns:p="urn:p" xmlns=""><p:e3/></p></e1>`,
	`<r xmlns="urn:1"><a xmlns="urn:2"><b/></a></r>`,
}

// refused reports whether err is a refusal the specifications require or
// allow for a document the parser accepted, rather than a failure.
func refused(err error) bool {
	return errors.Is(err, ErrDepthExceeded) || errors.Is(err, ErrRelativeNamespaceURI) || errors.Is(err, ErrXML11)
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
		// The namespace-node path: keeping every node must reproduce the
		// default path exactly, and filters that split namespace axes must
		// canonicalize without panicking.
		all, errAll := FromXPathFilter(doc, "true()", nil)
		split1, err1 := FromXPathFilter(doc, "not(name()='p')", nil)
		split2, err2 := FromXPathFilter(doc, "count(ancestor::*) mod 2 = 0", nil)
		if errAll != nil || err1 != nil || err2 != nil {
			t.Fatalf("FromXPathFilter: %v %v %v", errAll, err1, err2)
		}
		for _, alg := range allAlgorithms {
			opts := Options{Algorithm: alg, InclusiveNamespacePrefixes: []string{"", "p"}}
			want, wantErr := Bytes(doc, opts)
			got, gotErr := BytesNodeSet(all, opts)
			if (wantErr == nil) != (gotErr == nil) || !bytes.Equal(got, want) {
				t.Fatalf("%s: true() filter differs from the document\n in   %q\n doc  %q %v\n all  %q %v", alg, src, want, wantErr, got, gotErr)
			}
			for _, set := range []NodeSet{split1, split2} {
				if _, err := BytesNodeSet(set, opts); err != nil && !refused(err) {
					t.Fatalf("%s split: %v", alg, err)
				}
			}
		}

		for _, alg := range allAlgorithms {
			opts := Options{Algorithm: alg, InclusiveNamespacePrefixes: []string{"", "p"}}
			out, err := Bytes(doc, opts)
			if err != nil {
				// A parsed document may still be refused: nesting past
				// MaxDepth, a relative namespace URI (C14N section 2.1), or
				// XML 1.1, for which canonical XML is not defined.
				if !refused(err) {
					t.Fatalf("%s: %v", alg, err)
				}
				continue
			}
			if elem != nil {
				if _, err := Bytes(elem, opts); err != nil && !refused(err) {
					t.Fatalf("%s subtree: %v", alg, err)
				}
			}
			if first != nil {
				if _, err := BytesNodeSet(ExcludeSubtree(doc, first), opts); err != nil && !refused(err) {
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
