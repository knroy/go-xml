package xdmbuild_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
)

// TestAppendTextNotQuadratic bounds the cost of merging many adjacent text
// nodes into one.
//
// XDM forbids two adjacent text children, so a builder appending the n-th
// piece of text to an element must merge it with the text already there.
// Doing that as "last.Value += s" allocates a fresh string of the whole
// merged length every time, which copies O(n^2) bytes in total.
//
// It matters beyond build latency because n is not the stylesheet author's to
// choose: it is the number of text nodes the SOURCE DOCUMENT has, and a
// three-line transform that only copies text reaches it. A 640 kB input of
// 40000 small text nodes produced 400 kB of output at a cost of 7.9 GB of
// allocation, so the multiplier is paid by whoever supplies the data.
//
// Measured on this machine through xslt.Transform, allocation for the whole
// transform, before the fix and after:
//
//	n=5000    149 MB -> 21 MB
//	n=10000   548 MB -> 42 MB
//	n=20000  2061 MB -> 85 MB
//	n=40000  7946 MB -> 171 MB
//
// Before, each doubling of n quadrupled the allocation -- the signature of
// quadratic concatenation. After, it doubles, which is linear.
//
// This test measures the builder directly rather than through a transform, so
// its figures are smaller than the table above: it allocates only the merged
// text, not a parsed source document and a result tree as well. At n=40000
// with 10-byte pieces it took 3.1 GB before the fix and 2 MB after. The
// budget is many times the post-fix figure so that a loaded machine or a
// different allocator does not fail it, and far under the pre-fix one so that
// a return to concatenation fails it decisively.
func TestAppendTextNotQuadratic(t *testing.T) {
	if testing.Short() {
		t.Skip("cost test")
	}
	// Allocation, unlike wall clock, is not inflated by the race detector's
	// instrumentation -- it counts the same bytes either way. The budget is
	// widened there anyway because the race lane runs under load alongside
	// other packages, and a little slack costs nothing: 64 MB is still more
	// than twenty times the post-fix figure and fifty times under the
	// pre-fix one.
	const budget = 64 << 20
	limit := uint64(budget)
	if raceEnabled {
		limit *= 2
	}

	const (
		n     = 40000
		piece = "xxxxxxxxxx"
	)

	b := xdmbuild.New(xsltLike{})
	el := b.StartElement(xdm.QName{Local: "out"})

	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for i := 0; i < n; i++ {
		el.AppendText(piece)
	}
	runtime.ReadMemStats(&m1)

	// The merge must actually have happened, or the cost figure is measuring
	// the wrong thing: n separate text children are cheap to build and would
	// pass a budget that says nothing about concatenation.
	kids := el.Open().Children
	if len(kids) != 1 || kids[0].Kind != xdm.KindText {
		t.Fatalf("got %d children, want one text node: the pieces were not "+
			"merged, so this test is not measuring the merge", len(kids))
	}
	if want := n * len(piece); len(kids[0].Value) != want {
		t.Fatalf("merged text is %d bytes, want %d", len(kids[0].Value), want)
	}

	if used := m1.TotalAlloc - m0.TotalAlloc; used > limit {
		t.Errorf("merging %d text pieces allocated %d MB, over the %d MB "+
			"budget.\nAdjacent text is being merged by re-concatenating the "+
			"whole run per piece again, which is O(n^2) in the number of "+
			"text nodes in the SOURCE document and so is driven by input "+
			"data.", n, used>>20, limit>>20)
	}
}

// TestAppendTextMergeShapes pins the tree that text merging produces, in the
// shapes where an accumulating buffer could plausibly differ from
// concatenation: a run continued after something else was appended between,
// zero-length pieces, a single piece, and two runs on sibling elements that
// must not bleed into each other.
//
// Every expectation here was captured from the concatenating implementation
// before the buffer replaced it, so a difference is a regression and not a
// revised opinion about what is right.
func TestAppendTextMergeShapes(t *testing.T) {
	// Each case appends its pieces to one open element; a piece naming an
	// element is appended as a child instead, which ends the text run.
	cases := []struct {
		name   string
		pieces []string
		want   []string // one entry per child: "t:VALUE" or "e:NAME"
	}{
		{"single", []string{"a"}, []string{"t:a"}},
		{"adjacent", []string{"a", "b", "c"}, []string{"t:abc"}},
		{"empty first", []string{"", "a"}, []string{"t:a"}},
		{"empty middle", []string{"a", "", "b"}, []string{"t:ab"}},
		{"empty last", []string{"a", ""}, []string{"t:a"}},
		{"all empty", []string{"", "", ""}, nil},
		{"whitespace", []string{"  ", "\t", "\n"}, []string{"t:  \t\n"}},
		{"split by element", []string{"a", "<e>", "b"},
			[]string{"t:a", "e:e", "t:b"}},
		{"run resumed after element", []string{"a", "b", "<e>", "c", "d"},
			[]string{"t:ab", "e:e", "t:cd"}},
		{"element first", []string{"<e>", "a", "b"}, []string{"e:e", "t:ab"}},
		{"two elements", []string{"a", "<e>", "<f>", "b"},
			[]string{"t:a", "e:e", "e:f", "t:b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := xdmbuild.New(xsltLike{})
			el := b.StartElement(xdm.QName{Local: "out"})
			for _, p := range tc.pieces {
				if strings.HasPrefix(p, "<") {
					el.StartElement(xdm.QName{
						Local: strings.Trim(p, "<>")})
					continue
				}
				el.AppendText(p)
			}
			got := describe(el.Open())
			if len(got) != len(tc.want) {
				t.Fatalf("children %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("children %q, want %q", got, tc.want)
				}
			}
		})
	}
}

// TestAppendTextRunsDoNotBleed checks that a text run left unfinished on one
// element does not continue onto another.
//
// This is the shape that looks like it should break the buffer, and the
// reason it cannot is worth pinning rather than trusting: the accumulator
// lives on the Builder, but StartElement returns a NEW Builder per element,
// so the three builders here have three independent accumulators and none of
// them can see another's. Returning to the first element resumes its own run.
//
// A single shared accumulator would fail this test, so it also guards the
// invariant appendTextTo asserts -- were these builders to share one, the
// interleaving below is exactly what would hand it a node it was not paired
// with.
func TestAppendTextRunsDoNotBleed(t *testing.T) {
	b := xdmbuild.New(xsltLike{})
	root := b.StartElement(xdm.QName{Local: "r"})

	first := root.StartElement(xdm.QName{Local: "a"})
	first.AppendText("11")
	second := root.StartElement(xdm.QName{Local: "b"})
	second.AppendText("22")
	// Back to the first element: its run was interrupted by everything
	// written into the second, so the buffer no longer belongs to it.
	first.AppendText("33")

	for _, want := range []struct{ name, text string }{
		{"a", "1133"}, {"b", "22"},
	} {
		var found *xdm.Node
		for _, c := range root.Open().Children {
			if c.Name.Local == want.name {
				found = c
			}
		}
		if found == nil {
			t.Fatalf("no <%s> child", want.name)
		}
		if got := describe(found); len(got) != 1 || got[0] != "t:"+want.text {
			t.Errorf("<%s> holds %q, want one text node %q: a text run has "+
				"bled between elements", want.name, got, want.text)
		}
	}
}

// TestAppendTextSnapshotsAreStable is the soundness half of the buffer, and
// the half that would fail silently. The merged value aliases the buffer
// rather than copying it -- a copy per append is the quadratic cost this
// change removes -- so a string handed to a caller shares memory with an
// array that later appends keep writing to.
//
// That is sound only because append never rewrites bytes it has already
// written: it extends past them, and when it reallocates it abandons the old
// array intact to whatever still aliases it. If that ever stopped holding,
// a value read mid-build would mutate afterwards under a caller who had
// every reason to treat a Go string as immutable -- so it is asserted rather
// than assumed. Nothing about the merge looks wrong when this breaks; the
// tree is right and a value read earlier is not.
func TestAppendTextSnapshotsAreStable(t *testing.T) {
	b := xdmbuild.New(xsltLike{})
	el := b.StartElement(xdm.QName{Local: "out"})

	// Sampled across many reallocations: the buffer doubles, so 5000 appends
	// cross every growth step the allocator takes.
	var snaps, want []string
	acc := ""
	for i := 0; i < 5000; i++ {
		el.AppendText("ab")
		acc += "ab"
		if i%97 == 0 {
			snaps = append(snaps, el.Open().Children[0].Value)
			want = append(want, acc)
		}
	}
	for i := range snaps {
		if snaps[i] != want[i] {
			t.Fatalf("a value read after %d bytes now reads as %d: the "+
				"buffer was rewritten under a string already handed out",
				len(want[i]), len(snaps[i]))
		}
	}
}

// TestAppendTextNormalPathHoldsTheInvariant runs the ordinary merge paths
// through appendTextTo and checks the result, which is the other half of the
// assertion's case: the guard is only acceptable if the normal path cannot
// trip it.
//
// The shapes here are the ones a transform actually produces -- a second
// piece on the same node, a run interrupted by an element and resumed, and a
// piece large enough to force the buffer to reallocate mid-run. That the
// assertion stays silent through all of them is the point; that it fires when
// the pairing really is broken is TestAppendTextAssertionFires, in-package,
// which is the only place it can be demonstrated because no exported call
// sequence reaches it.
func TestAppendTextNormalPathHoldsTheInvariant(t *testing.T) {
	b := xdmbuild.New(xsltLike{})
	el := b.StartElement(xdm.QName{Local: "out"})

	el.AppendText("a")
	el.AppendText("b")
	el.StartElement(xdm.QName{Local: "e"})
	el.AppendText("c")
	el.AppendText(strings.Repeat("d", 4096))

	got := describe(el.Open())
	want := []string{"t:ab", "e:e", "t:c" + strings.Repeat("d", 4096)}
	if len(got) != len(want) {
		t.Fatalf("children %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("child %d is %.16q..., want %.16q...", i, got[i], want[i])
		}
	}
}

// describe renders a node's children as "t:VALUE" for text and "e:NAME" for
// an element, so a test can compare whole child lists in one line.
func describe(n *xdm.Node) []string {
	var out []string
	for _, c := range n.Children {
		switch c.Kind {
		case xdm.KindText:
			out = append(out, "t:"+c.Value)
		default:
			out = append(out, "e:"+c.Name.Local)
		}
	}
	return out
}
