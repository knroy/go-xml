package xdm

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// bigBody is n elements of a few dozen bytes each, enough that the source is
// far larger than the decoder's read-ahead.
func bigBody(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "<e i=\"%d\">text %d</e>\n", i, i)
	}
	return b.String()
}

// allocated returns the bytes f allocated.
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// AllowDOCTYPE keeps a copy of the source in case an entity forces a re-parse
// from the DOCTYPE, and the entity charge reader buffers what the decoder
// reads ahead of the entity table. Both are needed only until the document
// element opens. Kept for the whole parse, they cost a document with no
// DOCTYPE two extra copies of itself: fn:doc and fn:parse-xml always set the
// option, so every document they read paid it.
func TestAllowDOCTYPEWithoutDOCTYPEKeepsNoCopy(t *testing.T) {
	doc := "<r>\n" + bigBody(100000) + "</r>\n"
	parse := func(opts ParseOptions) func() {
		opts.MaxBytes, opts.MaxNodes = -1, -1
		return func() {
			if _, err := ParseString(doc, opts); err != nil {
				t.Fatal(err)
			}
		}
	}
	plain := allocated(parse(ParseOptions{}))
	withDT := allocated(parse(ParseOptions{AllowDOCTYPE: true}))
	// Before, the extra was about four times the document (two copies, each
	// grown by doubling). A tenth of it is room for noise.
	if extra := int64(withDT) - int64(plain); extra > int64(len(doc))/10 {
		t.Errorf("AllowDOCTYPE with no DOCTYPE allocated %d bytes more than without (document %d bytes)",
			extra, len(doc))
	}
}

// The paths that still need the copy and the charge reader keep them: an
// entity with markup is re-parsed from the DOCTYPE, references far into a
// large body are still charged, and positions are still tracked to the end.
func TestSourceCopyStillServesItsUsers(t *testing.T) {
	body := bigBody(20000)
	opts := ParseOptions{AllowDOCTYPE: true, MaxBytes: -1, MaxNodes: -1}

	t.Run("markup entity re-parse", func(t *testing.T) {
		doc := `<!DOCTYPE r [<!ENTITY m "<b>x</b>">]><r>` + body + `&m;</r>`
		tr, err := ParseString(doc, opts)
		if err != nil {
			t.Fatal(err)
		}
		kids := tr.Root.Children[0].Children
		if last := kids[len(kids)-1]; last.Kind != KindElement || last.Name.Local != "b" {
			t.Fatalf("last child is %v %q, want element b", last.Kind, last.Name.Local)
		}
	})

	t.Run("references charged after the root opens", func(t *testing.T) {
		refs := strings.Repeat("&e;", 3000)
		doc := `<!DOCTYPE r [<!ENTITY e "` + strings.Repeat("A", 1000) + `">]><r>` + body + refs + `</r>`
		_, err := ParseString(doc, opts)
		if !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("3 MB of expansion after a large body: err = %v, want ErrResourceLimit", err)
		}
	})

	t.Run("positions", func(t *testing.T) {
		o := opts
		o.TrackPositions = true
		tr, err := ParseString("<r>\n"+body+"<last/></r>", o)
		if err != nil {
			t.Fatal(err)
		}
		kids := tr.Root.Children[0].Children
		line, col, ok := kids[len(kids)-1].Position()
		if want := 20000 + 2; !ok || line != want || col != 1 {
			t.Fatalf("last element at line %d col %d (ok=%v), want line %d col 1", line, col, ok, want)
		}
	})
}

// TrackPositions keeps the whole source to count lines in. A reader that
// knows its length lets that copy be allocated once at its final size,
// rather than grown by doubling to about twice the document in all.
func TestTrackedCopyIsSizedOnce(t *testing.T) {
	doc := "<r>\n" + bigBody(100000) + "</r>\n"
	parse := func(track bool) func() {
		return func() {
			if _, err := ParseString(doc, ParseOptions{TrackPositions: track, MaxBytes: -1, MaxNodes: -1}); err != nil {
				t.Fatal(err)
			}
		}
	}
	plain := allocated(parse(false))
	tracked := allocated(parse(true))
	extra := int64(tracked) - int64(plain)
	t.Logf("tracked copy cost %d bytes for a %d-byte document", extra, len(doc))
	if extra > int64(len(doc))*5/4 {
		t.Errorf("TrackPositions allocated %d bytes more than without (document %d bytes), want about one copy",
			extra, len(doc))
	}
}
