package xdm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// burstDoc exercises what a burst must carry over from the whole tree: an
// inherited namespace and xml:base, attribute defaults from the DOCTYPE, an
// entity, text long enough to get a text block of its own, and spine content
// (text, comments, PIs, off-path elements) that must be dropped.
func burstDoc(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><!DOCTYPE a [<!ENTITY e "ent"><!ATTLIST r d CDATA "dflt">]>` +
		`<!--pre--><a xmlns="urn:a" xmlns:p="urn:p" xml:base="http://ex/base/"><!--c-->x<?pi v?>` +
		`<skip><r>not me</r></skip><b p:k="v" xml:base="sub/">`)
	for i := range n {
		fmt.Fprintf(&b, "\n  <r i=\"%d\"><p:c>&e;%d</p:c><!--k-->", i, i)
		if i%7 == 3 {
			b.WriteString(strings.Repeat("long ", 3000))
		}
		b.WriteString("<d xmlns=\"\"/></r>text<q/>")
	}
	b.WriteString("</b><b><r i=\"last\"/></b></a><!--post-->")
	return b.String()
}

func TestParseBurstsMatchesTree(t *testing.T) {
	src := burstDoc(40)
	opts := ParseOptions{AllowDOCTYPE: true, BaseURI: "http://ex/d.xml", DocumentURI: "http://ex/d.xml"}
	tree, err := ParseString(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for a := range tree.Root.Children() {
		if a.Kind() != KindElement {
			continue
		}
		for b := range a.Children() {
			if b.Kind() != KindElement || b.Name().Local != "b" {
				continue
			}
			for r := range b.Children() {
				if r.Kind() == KindElement && r.Name().Local == "r" {
					want = append(want, dump(r)+ancestry(r))
				}
			}
		}
	}
	path := []QName{{URI: "urn:a", Local: "a"}, {URI: "urn:a", Local: "b"}, {URI: "urn:a", Local: "r"}}
	var got []*Node
	err = ParseBursts(strings.NewReader(src), opts, path, func(r *Node) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d bursts, want %d", len(got), len(want))
	}
	// Compared after the parse: a kept burst must survive the rest of it.
	for i, r := range got {
		if g := dump(r) + ancestry(r); g != want[i] {
			t.Fatalf("burst %d differs\nwant\n%s\ngot\n%s", i, want[i], g)
		}
		if i > 0 && got[i-1].Tree().ident() >= r.Tree().ident() {
			t.Fatalf("burst %d does not order after burst %d", i, i-1)
		}
	}
}

func ancestry(n *Node) string {
	var b strings.Builder
	for a := n.Parent(); a != nil; a = a.Parent() {
		fmt.Fprintf(&b, "^%v %q base=%q doc=%q ns=%v\n", a.Name(), a.Value(), a.BaseURI(), a.DocumentURI(),
			a.InScopeNamespaces())
		for at := range a.Attrs() {
			fmt.Fprintf(&b, "  @%v=%q\n", at.Name(), at.Value())
		}
	}
	return b.String()
}

// The limits are the document's, not each burst's.
func TestParseBurstsLimitsSpanTheStream(t *testing.T) {
	src := "<a>" + strings.Repeat("<r/>", 100) + "</a>"
	path := []QName{{Local: "a"}, {Local: "r"}}
	n := 0
	err := ParseBursts(strings.NewReader(src), ParseOptions{MaxNodes: 50}, path, func(*Node) error { n++; return nil })
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("MaxNodes over the stream: got %v after %d bursts", err, n)
	}
	err = ParseBursts(strings.NewReader(src), ParseOptions{MaxBytes: 200}, path, func(*Node) error { return nil })
	if err == nil {
		t.Fatal("MaxBytes over the stream: no error")
	}
	// Malformed after the bursts is still an error.
	err = ParseBursts(strings.NewReader("<a><r/><r/></b>"), ParseOptions{}, path, func(*Node) error { return nil })
	if err == nil {
		t.Fatal("unbalanced end tag after bursts: no error")
	}
	stop := errors.New("stop")
	err = ParseBursts(strings.NewReader(src), ParseOptions{}, path, func(*Node) error { return stop })
	if err != stop {
		t.Fatalf("fn's error: got %v", err)
	}
}

// The spine does not grow: what a burst and the text between bursts leave
// behind in the parse's own tree is taken back.
func TestParseBurstsSpineStaysSmall(t *testing.T) {
	var b strings.Builder
	b.WriteString("<a><b>")
	for i := range 20000 {
		fmt.Fprintf(&b, "<r i='%d'>%s</r>\n<!--x-->", i, strings.Repeat("v", i%50))
	}
	b.WriteString("</b></a>")
	n := 0
	opts := ParseOptions{bursts: &burster{
		path: []QName{{Local: "a"}, {Local: "b"}, {Local: "r"}},
		fn:   func(*Node) error { n++; return nil },
	}}
	tree, err := parse(strings.NewReader(b.String()), "", opts)
	if err != nil {
		t.Fatal(err)
	}
	if n != 20000 {
		t.Fatalf("%d bursts", n)
	}
	// Left behind: the document node, and the bytes of nothing.
	var bytes int
	for _, blk := range tree.text.blocks {
		bytes += len(blk)
	}
	if tree.n > 2 || bytes > 0 {
		t.Fatalf("the spine kept %d records and %d text bytes", tree.n, bytes)
	}
}
