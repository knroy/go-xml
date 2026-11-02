package xdmstream

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// doc carries what the events must report as a full parse has it: a prolog
// and epilog, namespaces declared and undeclared, xml:base, a DOCTYPE
// attribute default and entity, CDATA, mixed content, and text long enough
// for a block of its own.
func doc(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><!--pre--><?pp a?><!DOCTYPE a [<!ENTITY e "ent"><!ATTLIST r d CDATA "dflt">]>` +
		`<a xmlns="urn:a" xmlns:p="urn:p" xml:base="http://ex/base/"><!--c-->x<?pi v?>` +
		`<skip><r>not me</r></skip><b p:k="v" xml:base="sub/">`)
	for i := range n {
		fmt.Fprintf(&b, "\n  <r i=\"%d\"><p:c>&e;%d<![CDATA[<cd>]]></p:c><!--k-->", i, i)
		if i%7 == 3 {
			b.WriteString(strings.Repeat("long ", 3000))
		}
		b.WriteString("<d xmlns=\"\"/></r>text<q/>")
	}
	b.WriteString("</b><b><r i=\"last\"/></b></a><!--post--><?end?>")
	return b.String()
}

var opts = xdm.ParseOptions{AllowDOCTYPE: true, BaseURI: "http://ex/d.xml", DocumentURI: "http://ex/d.xml"}

// show is what a consumer may read at an event (see the package comment).
func show(e Event) string {
	n := e.Node
	switch e.Kind {
	case StartElement:
		var b strings.Builder
		fmt.Fprintf(&b, "%d <%v base=%q ns=%v", e.Depth, n.Name(), n.BaseURI(), n.InScopeNamespaces())
		for a := range n.Attrs() {
			fmt.Fprintf(&b, " @%v=%q", a.Name(), a.StringValue())
		}
		return b.String()
	case EndElement:
		return fmt.Sprintf("%d </%v>", e.Depth, n.Name())
	case Text, Comment, PI:
		return fmt.Sprintf("%d %v %v %q base=%q", e.Depth, e.Kind, n.Name(), n.StringValue(), n.BaseURI())
	}
	return "EndDocument"
}

// walk is the events of a full parse's tree, the subtrees of elements skip
// selects left out (their start and end kept).
func walk(n *xdm.Node, d int, skip func(*xdm.Node) bool, out *[]string) {
	for c := range n.Children() {
		switch c.Kind() {
		case xdm.KindElement:
			*out = append(*out, show(Event{StartElement, d, c}))
			if skip == nil || !skip(c) {
				walk(c, d+1, skip, out)
			}
			*out = append(*out, show(Event{EndElement, d, c}))
		case xdm.KindText:
			*out = append(*out, show(Event{Text, d, c}))
		case xdm.KindComment:
			*out = append(*out, show(Event{Comment, d, c}))
		case xdm.KindPI:
			*out = append(*out, show(Event{PI, d, c}))
		}
	}
}

func treeEvents(t *testing.T, src string, o xdm.ParseOptions, skip func(*xdm.Node) bool) []string {
	t.Helper()
	tree, err := xdm.ParseString(src, o)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	walk(tree.Root, 1, skip, &want)
	return append(want, "EndDocument")
}

// drain reads every event, calling at for each start tag to Skip, Ground or
// descend into it.
func drain(t *testing.T, c *Cursor, at func(Event) bool) []string {
	t.Helper()
	var got []string
	for {
		e, err := c.Next()
		if err != nil {
			t.Fatalf("after %d events: %v", len(got), err)
		}
		got = append(got, show(e))
		if e.Kind == EndDocument {
			break
		}
		if e.Kind == StartElement && at != nil && at(e) {
			if err := c.Skip(); err != nil {
				t.Fatal(err)
			}
			got = append(got, show(c.ev))
		}
	}
	if _, err := c.Next(); err != io.EOF {
		t.Fatalf("after EndDocument: %v", err)
	}
	return got
}

func same(t *testing.T, got, want []string) {
	t.Helper()
	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Fatalf("event %d of %d/%d:\ngot  %s\nwant %s", i, len(got), len(want), g, w)
		}
	}
}

func TestCursorEventsMatchTree(t *testing.T) {
	src := doc(40)
	c := Open(strings.NewReader(src), opts)
	defer c.Close()
	same(t, drain(t, c, nil), treeEvents(t, src, opts, nil))

	// With whitespace stripped, as Parse strips it.
	o := opts
	o.StripSpace = func(xdm.QName) bool { return true }
	c = Open(strings.NewReader(src), o)
	defer c.Close()
	same(t, drain(t, c, nil), treeEvents(t, src, o, nil))
}

// An entity whose replacement holds markup restarts the parse at the
// DOCTYPE (parseExpanded): the prolog before it is still reported once.
func TestCursorEntityRestart(t *testing.T) {
	src := `<!--one--><!DOCTYPE a [<!ENTITY m "<i>in</i>">]><a>&m;&m;</a>`
	c := Open(strings.NewReader(src), xdm.ParseOptions{AllowDOCTYPE: true})
	defer c.Close()
	same(t, drain(t, c, nil), treeEvents(t, src, xdm.ParseOptions{AllowDOCTYPE: true}, nil))
}

func TestCursorSkip(t *testing.T) {
	src := doc(40)
	skip := func(n *xdm.Node) bool { return n.Name().Local == "r" || n.Name().Local == "skip" }
	c := Open(strings.NewReader(src), opts)
	defer c.Close()
	same(t, drain(t, c, func(e Event) bool { return skip(e.Node) }), treeEvents(t, src, opts, skip))
}

// dump is a subtree as a full parse has it, with its ancestors.
func dump(n *xdm.Node) string {
	var b strings.Builder
	var rec func(n *xdm.Node, ind string)
	rec = func(n *xdm.Node, ind string) {
		fmt.Fprintf(&b, "%s%v %v %q base=%q ns=%v\n", ind, n.Kind(), n.Name(), n.StringValue(), n.BaseURI(), n.InScopeNamespaces())
		for a := range n.Attrs() {
			fmt.Fprintf(&b, "%s @%v=%q\n", ind, a.Name(), a.StringValue())
		}
		for c := range n.Children() {
			rec(c, ind+"  ")
		}
	}
	rec(n, "")
	for a := n.Parent(); a != nil; a = a.Parent() {
		fmt.Fprintf(&b, "^%v base=%q doc=%q ns=%v\n", a.Name(), a.BaseURI(), a.DocumentURI(), a.InScopeNamespaces())
		for at := range a.Attrs() {
			fmt.Fprintf(&b, "  @%v=%q\n", at.Name(), at.StringValue())
		}
	}
	return b.String()
}

// Ground is the element as a full parse has it, with its ancestors' names,
// namespaces, attributes and base URIs; a grounded copy outlives the
// parse. Text is its string value.
func TestCursorGroundAndText(t *testing.T) {
	src := doc(40)
	tree, err := xdm.ParseString(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	var want, wantText []string
	var find func(n *xdm.Node)
	find = func(n *xdm.Node) {
		for c := range n.Children() {
			if c.Kind() != xdm.KindElement {
				continue
			}
			switch c.Name().Local {
			case "r":
				want = append(want, dump(c))
				find(c)
			case "c":
				wantText = append(wantText, c.StringValue())
			case "skip":
			default:
				find(c)
			}
		}
	}
	find(tree.Root)

	c := Open(strings.NewReader(src), opts)
	defer c.Close()
	var got []*xdm.Node
	var gotText []string
	for {
		e, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == EndDocument {
			break
		}
		if e.Kind != StartElement {
			continue
		}
		switch e.Node.Name().Local {
		case "r":
			g, err := c.Ground()
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, g)
		case "skip":
			// A Ground inside it would not be one of want's.
			if err := c.Skip(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d grounded, want %d", len(got), len(want))
	}
	// Compared after the parse: a copy must survive the rest of it.
	for i, g := range got {
		if d := dump(g); d != want[i] {
			t.Fatalf("grounded %d differs\ngot\n%s\nwant\n%s", i, d, want[i])
		}
	}

	// GroundLater gives the same copies, in order.
	c = Open(strings.NewReader(src), opts)
	defer c.Close()
	got = got[:0]
	for {
		e, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == EndDocument {
			break
		}
		if e.Kind == StartElement && e.Node.Name().Local == "r" {
			if err := c.GroundLater(func(g *xdm.Node) error { got = append(got, g); return nil }); err != nil {
				t.Fatal(err)
			}
		}
		if e.Kind == StartElement && e.Node.Name().Local == "skip" {
			c.Skip()
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d grounded later, want %d", len(got), len(want))
	}
	for i, g := range got {
		if d := dump(g); d != want[i] {
			t.Fatalf("grounded later %d differs\ngot\n%s\nwant\n%s", i, d, want[i])
		}
	}

	c = Open(strings.NewReader(src), opts)
	defer c.Close()
	for {
		e, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == EndDocument {
			break
		}
		if e.Kind == StartElement && e.Node.Name().Local == "c" {
			s, err := c.Text()
			if err != nil {
				t.Fatal(err)
			}
			gotText = append(gotText, s)
		}
		if e.Kind == StartElement && e.Node.Name().Local == "skip" {
			c.Skip()
		}
	}
	same(t, gotText, wantText)
}

// Peek reads ahead one event without moving; at a start tag it descends.
func TestCursorPeek(t *testing.T) {
	c := Open(strings.NewReader(`<a><b/>t<c><d/></c></a>`), xdm.ParseOptions{})
	defer c.Close()
	var got []string
	for {
		p, err := c.Peek()
		if err != nil {
			t.Fatal(err)
		}
		if q, _ := c.Peek(); show(q) != show(p) {
			t.Fatal("a second Peek moved")
		}
		if c.ev.Kind == StartElement {
			// Decided by the Peek.
			if err := c.Skip(); err != errNotAtStart {
				t.Fatalf("Skip after Peek: %v", err)
			}
			// has-children, read off the lookahead.
			got = append(got, fmt.Sprintf("%s:%v", c.ev.Node.Name().Local, p.Kind != EndElement))
		}
		e, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if show(e) != show(p) {
			t.Fatalf("Next %s after Peek %s", show(e), show(p))
		}
		if e.Kind == EndDocument {
			break
		}
	}
	same(t, got, []string{"a:true", "b:false", "c:true", "d:false"})
}

// The observers see every element, those inside one skipped, grounded or
// read by Text too.
func TestCursorObserve(t *testing.T) {
	src := doc(10)
	tree, err := xdm.ParseString(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	var all func(n *xdm.Node, d int)
	all = func(n *xdm.Node, d int) {
		for c := range n.Children() {
			if c.Kind() == xdm.KindElement {
				want = append(want, fmt.Sprintf("%d <%v", d, c.Name()))
				all(c, d+1)
				want = append(want, fmt.Sprintf("%d </%v", d, c.Name()))
			}
		}
	}
	all(tree.Root, 1)
	c := Open(strings.NewReader(src), opts)
	defer c.Close()
	var got []string
	c.Observe(func(el *xdm.Node, d int, end bool) {
		got = append(got, fmt.Sprintf("%d %s%v", d, map[bool]string{false: "<", true: "</"}[end], el.Name()))
	})
	k := 0
	for {
		e, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == EndDocument {
			break
		}
		if e.Kind == StartElement && e.Depth == 3 {
			switch k++; k % 4 {
			case 0:
				err = c.Skip()
			case 1:
				_, err = c.Ground()
			case 2:
				_, err = c.Text()
			case 3:
				err = c.GroundLater(func(*xdm.Node) error { return nil })
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	same(t, got, want)
}

// The limits are the document's, counted across the whole stream, however
// its elements are consumed; their errors are Parse's.
func TestCursorLimitsSpanTheStream(t *testing.T) {
	src := "<a>" + strings.Repeat("<r><s/></r>", 100) + "</a>"
	deep := strings.Repeat("<d>", 60) + strings.Repeat("</d>", 60)
	for _, tc := range []struct {
		src string
		o   xdm.ParseOptions
	}{
		{src, xdm.ParseOptions{MaxNodes: 150}},
		{src, xdm.ParseOptions{MaxBytes: 600}},
		{deep, xdm.ParseOptions{MaxDepth: 50}},
	} {
		_, want := xdm.ParseString(tc.src, tc.o)
		if !errors.Is(want, xdm.ErrResourceLimit) {
			t.Fatalf("%+v: Parse: %v", tc.o, want)
		}
		for mode := range 5 {
			got := consume(tc.src, tc.o, mode)
			if got == nil || got.Error() != want.Error() || !errors.Is(got, xdm.ErrResourceLimit) {
				t.Fatalf("%+v mode %d: got %v, want %v", tc.o, mode, got, want)
			}
		}
	}
}

// consume reads src to its end, each element at depth 2 descended into
// (mode 0), skipped (1), grounded (2) or read by Text (3), and returns the
// error that stopped it; mode 4 grounds it for later.
func consume(src string, o xdm.ParseOptions, mode int) error {
	c := Open(strings.NewReader(src), o)
	defer c.Close()
	for {
		e, err := c.Next()
		if err != nil {
			return err
		}
		if e.Kind == EndDocument {
			return nil
		}
		if e.Kind != StartElement || e.Depth != 2 {
			continue
		}
		switch mode {
		case 1:
			err = c.Skip()
		case 2:
			_, err = c.Ground()
		case 3:
			_, err = c.Text()
		case 4:
			err = c.GroundLater(func(*xdm.Node) error { return nil })
		}
		if err != nil {
			return err
		}
	}
}

// A malformed document fails as Parse fails, with Parse's error, wherever
// the fault is and however the element around it is consumed.
func TestCursorMalformed(t *testing.T) {
	for _, src := range []string{
		"",
		"<a>",
		"<a><b></a>",
		"<a><b><c></c></a></b>",
		"<a></a><b/>",
		"<a/>text",
		"<a><r x='1' x='2'/></a>",
		"<a><r><p:x/></r></a>",
		"<a><r>&undeclared;</r></a>",
		"<a><r><?xml version='1.0'?></r></a>",
		"<a><r><?p:q?></r></a>",
		"<a><r>\x01</r></a>",
		"<!DOCTYPE a><a/>",
		"<a/><!DOCTYPE a>",
		"<a><r><!-- -- --></r></a>",
	} {
		_, want := xdm.ParseString(src, xdm.ParseOptions{})
		if want == nil {
			t.Fatalf("%q: Parse accepts it", src)
		}
		for mode := range 5 {
			if got := consume(src, xdm.ParseOptions{}, mode); got == nil || got.Error() != want.Error() {
				t.Fatalf("%q mode %d: got %v, want %v", src, mode, got, want)
			}
		}
	}
}

// Closing a cursor part way ends its parse; the cursor is spent.
func TestCursorClose(t *testing.T) {
	c := Open(strings.NewReader(doc(5)), opts)
	for range 10 {
		if _, err := c.Next(); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()
	if _, err := c.Next(); err != io.EOF {
		t.Fatalf("after Close: %v", err)
	}
}

// TestCursorFlush: GroundLater's copies are held for a batch; Flush, at an
// end tag, hands them over then, and the parse goes on as before.
func TestCursorFlush(t *testing.T) {
	src := `<a><b><r i="1">x</r><r i="2"/></b><b><r i="3"/></b><c/></a>`
	c := Open(strings.NewReader(src), opts)
	defer c.Close()
	var got []string
	var rest []string
	for {
		e, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == EndDocument {
			break
		}
		switch {
		case e.Kind == StartElement && e.Node.Name().Local == "r":
			if err := c.GroundLater(func(g *xdm.Node) error {
				got = append(got, g.AttrValue("i")+g.StringValue())
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := c.Flush(); err != nil {
				t.Fatal(err) // at the r's end: allowed, and delivers it
			}
			if n := len(got); n == 0 {
				t.Fatal("Flush at an end tag delivered nothing")
			}
		case e.Kind == StartElement:
			rest = append(rest, e.Node.Name().Local)
			if err := c.Flush(); err == nil {
				t.Fatal("Flush at a start tag: no error")
			}
		case e.Kind == EndElement && e.Node.Name().Local == "b":
			if err := c.Flush(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if want := "1x 2 3"; strings.Join(got, " ") != want {
		t.Fatalf("copies %q, want %q", strings.Join(got, " "), want)
	}
	if want := "a b b c"; strings.Join(rest, " ") != want {
		t.Fatalf("events %q, want %q", strings.Join(rest, " "), want)
	}
}

// TestCursorFlushBatched: without Flush the copies of small elements wait
// for their batch; with it they are all handed over at the end tag asked.
func TestCursorFlushBatched(t *testing.T) {
	var b strings.Builder
	b.WriteString("<a>")
	for range 3 {
		b.WriteString("<b>")
		for i := range 5 {
			fmt.Fprintf(&b, `<r i="%d"/>`, i)
		}
		b.WriteString("</b>")
	}
	b.WriteString("</a>")
	for _, flush := range []bool{false, true} {
		c := Open(strings.NewReader(b.String()), opts)
		n, atEnds := 0, []int{}
		for {
			e, err := c.Next()
			if err != nil {
				t.Fatal(err)
			}
			if e.Kind == EndDocument {
				break
			}
			if e.Kind == StartElement && e.Node.Name().Local == "r" {
				if err := c.GroundLater(func(*xdm.Node) error { n++; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			if e.Kind == EndElement && e.Node.Name().Local == "b" {
				if flush {
					if err := c.Flush(); err != nil {
						t.Fatal(err)
					}
				}
				atEnds = append(atEnds, n)
			}
		}
		c.Close()
		want := []int{0, 0, 0}
		if flush {
			want = []int{5, 10, 15}
		}
		if fmt.Sprint(atEnds) != fmt.Sprint(want) || n != 15 {
			t.Fatalf("flush %v: copies at each </b> %v, want %v; %d in all", flush, atEnds, want, n)
		}
	}
}

// TestCursorSkipDiscards: a skipped element is dropped as it is parsed, so
// the parse holds its open path, not its content -- skipping the document
// element of a large document costs what reading it event by event does,
// not the tree. Text still reads the element whole.
func TestCursorSkipDiscards(t *testing.T) {
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 20000 {
		fmt.Fprintf(&b, `<rec i="%d"><sub>text %d</sub><!--c--><sub/></rec>`, i, i)
	}
	b.WriteString("</doc>")
	src := b.String()
	held := 0
	c := Open(strings.NewReader(src), opts)
	defer c.Close()
	c.Observe(func(el *xdm.Node, _ int, _ bool) { held = max(held, el.Tree().Len()) })
	if ev, err := c.Next(); err != nil || ev.Kind != StartElement {
		t.Fatalf("first event %v, %v", ev, err)
	}
	if err := c.Skip(); err != nil {
		t.Fatal(err)
	}
	// The open path is doc, rec, sub and their attributes and text: a few
	// records, against 120,000 for the whole document.
	if held > 64 {
		t.Errorf("skipping the document element held %d records", held)
	}
	if ev, err := c.Next(); err != nil || ev.Kind != EndDocument {
		t.Errorf("after Skip: %v, %v", ev, err)
	}
	// Text holds the element whole until its end, and reads it.
	c2 := Open(strings.NewReader(`<doc><a>x<b>y</b></a>z</doc>`), opts)
	defer c2.Close()
	c2.Next()
	if s, err := c2.Text(); err != nil || s != "xyz" {
		t.Errorf("Text %q, %v", s, err)
	}
}
