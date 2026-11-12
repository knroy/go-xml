package xdmstream

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// rewindDoc is many siblings under a few levels, with text short and long
// (some long enough for a block of its own), shared and unshared whitespace
// and attributes: every element close takes back what its content stored.
func rewindDoc(n int, seed int64) string {
	r := rand.New(rand.NewSource(seed))
	var b strings.Builder
	b.WriteString("<root>\n")
	for i := range n {
		ws := "\n" + strings.Repeat(" ", r.Intn(70))
		if r.Intn(3) == 0 { // more distinct runs than a table's home holds
			w := []byte(ws + "  \t\t \t \t\t ")
			for k := 1; k < len(w); k++ {
				w[k] = " \t"[r.Intn(2)]
			}
			ws = string(w)
		}
		txt := strings.Repeat(fmt.Sprintf("t%d-", i), 1+r.Intn(12))
		if r.Intn(400) == 0 {
			txt = strings.Repeat("L", 9000+r.Intn(30000))
		}
		fmt.Fprintf(&b, `%s<item id="%d" k="%s"><a>%s</a>%s<b x="%d">%s<c/>%s</b></item>`,
			ws, i, txt[:min(len(txt), 9)], txt, ws, i, txt, strings.Repeat(" ", r.Intn(5)))
	}
	b.WriteString("\n</root>")
	return b.String()
}

// TestRewindKeepsWhatWasRead holds every string, view and grounded copy the
// cursor hands out to the end of the document, as Event says a consumer
// may: what each read as when it was handed out it must still read, however
// many times the parse has taken its store back since.
func TestRewindKeepsWhatWasRead(t *testing.T) {
	for seed := int64(1); seed <= 4; seed++ {
		src := rewindDoc(3000, seed)
		r := rand.New(rand.NewSource(seed))
		type held struct {
			get  func() string
			want string
		}
		var all []held
		hold := func(get func() string) {
			all = append(all, held{get, strings.Clone(get())})
		}
		c := Open(strings.NewReader(src), xdm.ParseOptions{})
		for {
			ev, err := c.Next()
			if err != nil {
				t.Fatal(err)
			}
			if ev.Kind == EndDocument {
				break
			}
			switch ev.Kind {
			case Text:
				s := ev.Node.StringValue()
				hold(func() string { return s })
			case StartElement:
				for a := range ev.Node.Attrs() {
					s := a.StringValue()
					hold(func() string { return s })
				}
				if ev.Depth < 2 {
					continue
				}
				switch r.Intn(4) {
				case 0:
					v, err := c.View()
					if err != nil {
						t.Fatal(err)
					}
					hold(func() string {
						var b strings.Builder
						for a := range v.Attrs() {
							b.WriteString(a.StringValue() + "|")
						}
						return b.String()
					})
				case 1:
					g, err := c.Ground()
					if err != nil {
						t.Fatal(err)
					}
					hold(g.StringValue)
				case 2:
					s, err := c.Text()
					if err != nil {
						t.Fatal(err)
					}
					hold(func() string { return s })
				}
			}
		}
		c.Close()
		runtime.GC()
		if got, want := drainValues(t, src), treeValues(t, src); got != want {
			t.Fatalf("seed %d: the drain's values differ from the tree's", seed)
		}
		for i, h := range all {
			if got := h.get(); got != h.want {
				t.Fatalf("seed %d: value %d of %d read %q when handed out, %q at the end", seed, i, len(all), trunc(h.want), trunc(got))
			}
		}
	}
}

// drainValues is every attribute and text value of the document, in order,
// as a drain reads them.
func drainValues(t *testing.T, src string) string {
	var b strings.Builder
	c := Open(strings.NewReader(src), xdm.ParseOptions{})
	defer c.Close()
	for {
		ev, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Kind {
		case EndDocument:
			return b.String()
		case StartElement:
			for a := range ev.Node.Attrs() {
				b.WriteString(a.StringValue() + "\x00")
			}
		case Text:
			b.WriteString(ev.Node.StringValue() + "\x00")
		}
	}
}

// treeValues is drainValues' over the parsed tree.
func treeValues(t *testing.T, src string) string {
	tr, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		for a := range n.Attrs() {
			b.WriteString(a.StringValue() + "\x00")
		}
		for c := range n.Children() {
			switch c.Kind() {
			case xdm.KindElement:
				walk(c)
			case xdm.KindText:
				b.WriteString(c.StringValue() + "\x00")
			}
		}
	}
	walk(tr.Root)
	return b.String()
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// TestDrainStoresTextOnce: a drain stores the document's text about once.
// Each element's close takes back what its content stored; the store went
// on from a fresh 64-byte block every time, and re-stored the whitespace it
// had shared, which made a drain allocate several times the text (A3-2).
func TestDrainStoresTextOnce(t *testing.T) {
	var b strings.Builder
	b.WriteString("<root>")
	text := 0
	for i := range 20000 {
		v := fmt.Sprintf("value %05d of an item", i)
		fmt.Fprintf(&b, "\n%s<item>%s</item>", strings.Repeat(" ", 60), v)
		text += len(v)
	}
	b.WriteString("\n</root>")
	src := b.String()
	drain := func() {
		c := Open(strings.NewReader(src), xdm.ParseOptions{})
		defer c.Close()
		for {
			ev, err := c.Next()
			if err != nil {
				t.Fatal(err)
			}
			if ev.Kind == EndDocument {
				return
			}
		}
	}
	drain()
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	drain()
	runtime.ReadMemStats(&m1)
	// The whitespace runs (61 bytes, 20000 of them) are one shared value.
	if got, limit := m1.TotalAlloc-m0.TotalAlloc, uint64(text+text/2+256<<10); got > limit {
		t.Fatalf("drain allocated %d bytes for %d bytes of text, want at most %d", got, text, limit)
	}
}
