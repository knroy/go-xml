package xdm

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
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
		// Small bursts share a tree (burstBatch); either way they order
		// as they came.
		if i == 0 {
			continue
		}
		if p := got[i-1]; p.Tree() == r.Tree() && p.self >= r.self ||
			p.Tree() != r.Tree() && p.Tree().ident() >= r.Tree().ident() {
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
		steps:  []xdmburst.Step{{Local: "a"}, {Local: "b"}, {Local: "r"}},
		fn:     func(*Node) error { n++; return nil },
		states: []uint64{1},
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

// A burst's tree does not carry the parse's name and frame indexes, which
// cover every name and declaration of the stream so far: copying them made
// the cost of each burst grow with the stream. Its names still resolve.
func TestParseBurstsCopyNoIndexes(t *testing.T) {
	var b strings.Builder
	b.WriteString("<a>")
	for i := range 200 {
		fmt.Fprintf(&b, `<r xmlns:p%d="urn:%d"><n%d/></r>`, i, i, i)
	}
	b.WriteString("</a>")
	i := 0
	seen := map[*Tree]bool{}
	err := ParseBursts(strings.NewReader(b.String()), ParseOptions{}, []QName{{Local: "a"}, {Local: "r"}},
		func(r *Node) error {
			c := r.tree
			// Checked on a tree's first burst: the intern below builds one.
			if !seen[c] && (c.nameIx != nil || c.frameOne != nil || c.frameMany != nil) {
				t.Fatalf("burst %d: copied indexes: %d names, %d+%d frames", i, len(c.nameIx), len(c.frameOne), len(c.frameMany))
			}
			if got, want := r.FirstChild().Name().Local, fmt.Sprintf("n%d", i); got != want {
				t.Fatalf("burst %d: child %q, want %q", i, got, want)
			}
			seen[c] = true
			if got := c.intern(QName{Local: fmt.Sprintf("n%d", i)}); got != r.FirstChild().name {
				t.Fatalf("burst %d: intern finds %d, the child has %d", i, got, r.FirstChild().name)
			}
			i++
			return nil
		})
	if err != nil || i != 200 {
		t.Fatalf("%d bursts, %v", i, err)
	}
}

// The comments and processing instructions around the document element are
// not copied into every burst, which made the cost prolog x records.
func TestParseBurstsNoProlog(t *testing.T) {
	src := strings.Repeat("<!--c--><?p x?>", 100) + "<a>" + strings.Repeat("<r><x/></r>", 50) + "</a><!--post-->"
	n := 0
	err := ParseBursts(strings.NewReader(src), ParseOptions{}, []QName{{Local: "a"}, {Local: "r"}},
		func(r *Node) error {
			n++
			// The document node holds a, and a only bursts: r and x.
			kids := uint32(0)
			for range r.Parent().Children() {
				kids++
			}
			if got, want := r.tree.n, 2+2*kids; got != want {
				t.Fatalf("burst %d: the tree holds %d records, want %d", n, got, want)
			}
			return nil
		})
	if err != nil || n != 50 {
		t.Fatalf("%d bursts, %v", n, err)
	}
	// Still checked: a PI target with a colon in the prolog.
	err = ParseBursts(strings.NewReader("<?a:b?><a/>"), ParseOptions{}, []QName{{Local: "a"}, {Local: "r"}},
		func(*Node) error { return nil })
	if err == nil {
		t.Fatal("a colon in a prolog PI target: no error")
	}
}

// The names and namespace frames that the dropped content interns are taken
// back as the stream goes, as its text is, and the bursts kept from before
// still read theirs.
func TestParseBurstsNamesStaySmall(t *testing.T) {
	const n = 5000
	var b strings.Builder
	b.WriteString("<a>")
	for i := range n {
		fmt.Fprintf(&b, `<r xmlns:p%d="urn:%d"><n%d p%d:k="v"/></r><x%d xmlns:q="urn:q%d"/>`, i, i, i, i, i, i)
	}
	b.WriteString("</a>")
	var got []*Node
	opts := ParseOptions{bursts: &burster{
		steps:  []xdmburst.Step{{Local: "a"}, {Local: "r"}},
		fn:     func(r *Node) error { got = append(got, r); return nil },
		states: []uint64{1},
	}}
	tree, err := parse(strings.NewReader(b.String()), "", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.names) > 3*burstSlack || len(tree.frames) > 2*burstSlack || len(tree.frameData) > 2*burstSlack {
		t.Fatalf("after %d records the parse holds %d names and %d frames (%d bindings)",
			n, len(tree.names), len(tree.frames), len(tree.frameData))
	}
	if len(got) != n {
		t.Fatalf("%d bursts", len(got))
	}
	for i, r := range got {
		c := r.FirstChild()
		a := c.AttrAt(0)
		ns := r.InScopeNamespaces()
		if c.Name().Local != fmt.Sprintf("n%d", i) || a.Name().URI != fmt.Sprintf("urn:%d", i) ||
			ns[fmt.Sprintf("p%d", i)] != fmt.Sprintf("urn:%d", i) {
			t.Fatalf("burst %d reads %v @%v %v", i, c.Name(), a.Name(), ns)
		}
	}
}

// A large burst's records are taken over by its tree, not copied while the
// parse still holds them, and stay intact as the parse goes on.
func TestParseBurstsLargeNotCopied(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE a [<!ATTLIST c id ID #IMPLIED>]><a>`)
	for i := range 6 {
		n := 3
		if i%2 == 0 {
			n = 20000
		}
		fmt.Fprintf(&b, `<r i="%d">`, i)
		for j := range n {
			fmt.Fprintf(&b, `<c id="c%d-%d" xml:base="b%d/">t</c>`, i, j, j%3)
		}
		b.WriteString("</r>")
	}
	b.WriteString("</a>")
	src := b.String()
	opts := ParseOptions{AllowDOCTYPE: true}
	path := []QName{{Local: "a"}, {Local: "r"}}
	tree, err := ParseString(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	var got []*Node
	burst := allocated(func() {
		err = ParseBursts(strings.NewReader(src), opts, path, func(r *Node) error { got = append(got, r); return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	whole := allocated(func() { _, err = ParseString(src, opts) })
	if burst > whole*5/4 {
		t.Fatalf("bursts allocated %d bytes, the whole tree %d", burst, whole)
	}
	i := 0
	for r := range tree.Root.FirstChild().Children() {
		if g, w := dump(got[i])+ancestry(got[i]), dump(r)+ancestry(r); g != w {
			t.Fatalf("burst %d differs\nwant\n%.2000s\ngot\n%.2000s", i, w, g)
		}
		if c := got[i].LastChild(); !c.AttrAt(0).IsID() || c.BaseURI() != r.LastChild().BaseURI() {
			t.Fatalf("burst %d: the last child lost its ID type or base URI", i)
		}
		i++
	}
}

// A pattern with descendant steps and wildcards delivers each outermost
// match once, in document order, with the matches nested in it inside it
// and its ancestors' names and attributes as in the whole tree.
func TestParseBurstsPattern(t *testing.T) {
	const src = `<doc xmlns:q="urn:q" id="d"><rec k="1"><a/><rec k="2"><rec k="3"/></rec></rec>` +
		`<g id="g"><rec k="4"><b><rec k="5"/></b></rec></g><q:rec k="6"/><g><g><q:x k="7"/></g></g><rec k="8"/></doc>`
	for _, tc := range []struct {
		name  string
		steps []xdmburst.Step
		want  string
	}{
		{"//rec", []xdmburst.Step{{Local: "rec", Descendant: true}}, "1(2,3)/docd 4(5)/docd/gg 8/docd"},
		{"doc//rec", []xdmburst.Step{{Local: "doc"}, {Local: "rec", Descendant: true}}, "1(2,3)/docd 4(5)/docd/gg 8/docd"},
		{"/*/*/rec", []xdmburst.Step{{AnyURI: true, AnyLocal: true}, {AnyURI: true, AnyLocal: true}, {Local: "rec"}}, "2(3)/docd/rec1 4(5)/docd/gg"},
		{"/*/*:rec", []xdmburst.Step{{AnyURI: true, AnyLocal: true}, {AnyURI: true, Local: "rec"}}, "1(2,3)/docd 6/docd 8/docd"},
		{"//q:*", []xdmburst.Step{{URI: "urn:q", AnyLocal: true, Descendant: true}}, "6/docd 7/docd/g/g"},
		{"//g/rec", []xdmburst.Step{{Local: "g", Descendant: true}, {Local: "rec"}}, "4(5)/docd/gg"},
		{"//*", []xdmburst.Step{{AnyURI: true, AnyLocal: true, Descendant: true}}, "d(1,2,3,g,4,5,6,,,7,8)/"},
	} {
		var got []string
		err := parseBursts(strings.NewReader(src), ParseOptions{}, tc.steps, func(r *Node) error {
			key := func(n *Node) string {
				if a := n.Attr("", "k"); a != nil {
					return a.Value()
				}
				if a := n.Attr("", "id"); a != nil {
					return a.Value()
				}
				return ""
			}
			var in []string
			for d := range r.Descendants() {
				if d.Kind() == KindElement && d.Name().Local != "a" && d.Name().Local != "b" {
					in = append(in, key(d))
				}
			}
			s := key(r)
			if len(in) > 0 {
				s += "(" + strings.Join(in, ",") + ")"
			}
			anc := ""
			for p := r.Parent(); p != nil && p.Kind() == KindElement; p = p.Parent() {
				anc = "/" + p.Name().Local + key(p) + anc
			}
			if r.Parent().Kind() == KindDocument {
				anc = "/"
			}
			got = append(got, s+anc)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if g := strings.Join(got, " "); g != tc.want {
			t.Errorf("%s: bursts %q, want %q", tc.name, g, tc.want)
		}
	}
}

// Under a deep spine, small bursts share copies of their ancestors: the
// ancestors copied stay in proportion to the elements read, rather than
// growing with bursts x depth.
func TestParseBurstsSpineCopiedOnce(t *testing.T) {
	const depth, n = 300, 3000
	for _, wrap := range []bool{false, true} {
		var b strings.Builder
		b.WriteString(strings.Repeat(`<w a="1">`, depth))
		for i := range n {
			if wrap {
				b.WriteString("<g>")
			}
			fmt.Fprintf(&b, `<rec k="%d"><x/></rec>`, i)
			if wrap {
				b.WriteString("</g>")
			}
		}
		b.WriteString(strings.Repeat("</w>", depth))
		got := 0
		bs := &burster{steps: []xdmburst.Step{{Local: "rec", Descendant: true}}, states: []uint64{1}, gens: []uint64{0},
			fn: func(r *Node) error {
				// Each burst still has its whole ancestry.
				d := 0
				for p := r.Parent(); p != nil && p.Kind() == KindElement; p = p.Parent() {
					d++
				}
				if want := depth + map[bool]int{false: 0, true: 1}[wrap]; d != want || r.Attr("", "k").Value() != fmt.Sprint(got) {
					t.Fatalf("burst %d: %d ancestors, want %d", got, d, want)
				}
				got++
				return nil
			}}
		if _, err := parse(strings.NewReader(b.String()), "", ParseOptions{bursts: bs}); err != nil {
			t.Fatal(err)
		}
		opened := depth + 2*n
		if wrap {
			opened += n
		}
		if got != n || bs.spineCopies > opened {
			t.Errorf("wrap %v: %d bursts; %d ancestor records copied for %d elements read", wrap, got, bs.spineCopies, opened)
		}
	}
}
