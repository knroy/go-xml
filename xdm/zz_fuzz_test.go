package xdm

import (
	"testing"
)

// parseSeeds are the constructs the parser is expected to handle, and
// malformed variants of each. They are shared by the targets in this file so
// that a seed added for one is exercised by the other.
//
// They are kept short and few: a Go fuzz target runs its whole seed corpus on
// every ordinary `go test`, so a large corpus is a tax on every build.
var parseSeeds = []string{
	// Well-formed.
	`<a/>`,
	`<a>text</a>`,
	`<a xmlns="u"><b/></a>`,
	`<a xmlns:p="u"><p:b p:c="1"/></a>`,
	`<a xmlns:p="u"><b xmlns:p="v"><p:c/></b></a>`,
	`<a xmlns:p="u"><b xmlns:p=""/></a>`,
	`<a><![CDATA[<not markup & co>]]></a>`,
	`<a><!--c--><?pi data?>t</a>`,
	`<a>&#65;&#x42;&amp;&lt;&gt;&quot;&apos;</a>`,
	`<a>x<b/>y<c/>z</a>`,
	`<a>]]&gt;</a>`,
	"<a>x\r\ny\rz\n</a>",
	`<a b="1" c='2' d="&#10;&#9;"/>`,
	`<_a.b-c xmlns:_p.q-r="u" _p.q-r:d="1"/>`,
	`<a><b><c><d><e/></d></c></b></a>`,
	"<?xml version=\"1.0\" encoding=\"UTF-8\"?><a/>",
	"<?xml version='1.1'?><a/>",

	// Malformed.
	``,
	`<`,
	`<a>`,
	`<a></b>`,
	`<a/><b/>`,
	`<a b=/>`,
	`<a b="1" b="2"/>`,
	`<p:a/>`,
	`<a>&undeclared;</a>`,
	`<a>&#xD800;</a>`,
	`<a><![CDATA[</a>`,
	`<a>]]></a>`,
	`<!DOCTYPE a><a/>`,
	`<a xmlns:xml="wrong"/>`,
	`<a xmlns=""/>`,
	"<a>\x00</a>",
	`<?xml version="9.9"?><a/>`,
}

// fuzzParseOptions is what every target here parses with. MaxBytes is left at
// the default; MaxNodes and MaxDepth are pulled well below theirs so that a
// pathological input costs milliseconds rather than seconds. A limit firing is
// a correct refusal, not a failure, so lowering them changes only the cost.
var fuzzParseOptions = ParseOptions{MaxDepth: 100, MaxNodes: 20000}

// ParseString must never panic, whatever the bytes. It is the front door for
// every untrusted document this engine reads, so a panic there is a denial of
// service for any embedder that parses input it did not write.
//
// The target asserts two things beyond "did not panic": a refusal arrives as
// an error value rather than a nil tree with a nil error, and an acceptance
// yields a document node that the accessors can walk without panicking.
func FuzzParseNoPanic(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseString(%q) panicked: %v", src, r)
			}
		}()
		tree, err := ParseString(src, fuzzParseOptions)
		if err != nil {
			// A refusal must be an error and nothing else: no tree may
			// come back alongside it for a caller to use by mistake.
			if tree != nil {
				t.Fatalf("ParseString(%q) returned both a tree and an error %v", src, err)
			}
			return
		}
		if tree == nil || tree.Root == nil {
			t.Fatalf("ParseString(%q) returned no error and no tree", src)
		}
		if k := tree.Root.Kind; k != KindDocument {
			t.Fatalf("ParseString(%q) root is %v, want a document node", src, k)
		}
		// Every accessor a consumer reaches for must survive the tree the
		// parser just built. Walking is where a malformed-but-accepted
		// document would show up as a broken parent or sibling link.
		walkNode(t, src, tree.Root, 0)
	})
}

// walkNode exercises the node accessors over a whole tree and checks the
// structural invariants that every consumer of a tree relies on.
func walkNode(t *testing.T, src string, n *Node, depth int) {
	t.Helper()
	if depth > 200 {
		t.Fatalf("ParseString(%q) built a tree deeper than the depth limit allows", src)
	}
	_ = n.StringValue()
	for _, a := range n.Attrs {
		if a.Parent != n {
			t.Fatalf("ParseString(%q): attribute %v is not parented to its element", src, a.Name)
		}
		_ = a.StringValue()
	}
	for _, ns := range n.Namespaces {
		if ns.Parent != n {
			t.Fatalf("ParseString(%q): namespace %v is not parented to its element", src, ns.Name)
		}
	}
	for _, c := range n.Children {
		if c.Parent != n {
			t.Fatalf("ParseString(%q): child %v is not parented to its element", src, c.Name)
		}
		walkNode(t, src, c, depth+1)
	}
}
