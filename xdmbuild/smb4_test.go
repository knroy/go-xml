package xdmbuild_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmlimit"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xdmbuild"
)

// ceilingPolicy raises a text-store-style refusal (an internal/xdmlimit
// panic, which is how xdm reports its ceiling) from under the builder: from
// CountNodes once left reaches zero, and from DropEmptyText, which ToTree
// asks, when tree is set.
type ceilingPolicy struct {
	xsltLike
	left *int
	tree bool
}

func refusal() { xdmlimit.Refuse(fmt.Errorf("ceiling: %w", xdm.ErrResourceLimit)) }

func (p ceilingPolicy) CountNodes(int) error {
	if *p.left == 0 {
		refusal()
	}
	*p.left--
	return nil
}

func (p ceilingPolicy) DropEmptyText() bool {
	if p.tree {
		refusal()
	}
	return false
}

// TestSMB4NoErrorCallsLatchTheRefusal: a refusal raised under a builder call
// with no error return is latched, not panicked out of xdmbuild, and the next
// error-returning call (ToDocument, AddAttribute) reports it.
func TestSMB4NoErrorCallsLatchTheRefusal(t *testing.T) {
	src, err := xdm.ParseString(`<s><t>v</t></s>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		left int
		tree bool
		run  func(b *xdmbuild.Builder)
	}{
		{"AppendText", 0, false, func(b *xdmbuild.Builder) { b.AppendText("x") }},
		{"AppendValue", 1, false, func(b *xdmbuild.Builder) {
			b.StartElement(qn("e")).AppendValue(xdm.NewInteger(1))
		}},
		{"AppendNode", 1, false, func(b *xdmbuild.Builder) {
			b.StartElement(qn("e")).AppendNode(src.Root.FirstChild())
		}},
		{"StartElement", 0, false, func(b *xdmbuild.Builder) {
			b.StartElement(qn("e")).AppendValue(xdm.NewInteger(1))
		}},
		{"ToTree", 0, true, func(b *xdmbuild.Builder) {
			b.AppendValue(xdm.NewInteger(1))
			if b.ToTree() == nil {
				t.Error("ToTree returned nil")
			}
		}},
	}
	for _, c := range cases {
		left := c.left
		b := xdmbuild.New(ceilingPolicy{left: &left, tree: c.tree})
		c.run(b)
		if !errors.Is(b.Refused(), xdm.ErrResourceLimit) {
			t.Errorf("%s: Refused() = %v, want the latched ErrResourceLimit", c.name, b.Refused())
		}
		if _, err := b.ToDocument(); !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: ToDocument error = %v, want ErrResourceLimit", c.name, err)
		}
		if err := b.AddAttribute(qn("a"), "1"); !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: AddAttribute error = %v, want ErrResourceLimit", c.name, err)
		}
	}
}

// bytePolicy charges a copy's value bytes against limit.
type bytePolicy struct {
	xsltLike
	got   *int
	limit int
}

func (p bytePolicy) ChargeBytes(n int) error {
	*p.got += n
	if *p.got > p.limit {
		return fmt.Errorf("bytes: %w", xdm.ErrResourceLimit)
	}
	return nil
}

// TestSMB4AppendCopyChargesValueBytes: a tree node copied into content is
// charged its attribute and leaf value bytes, and a refusal latches before
// the copy is made.
func TestSMB4AppendCopyChargesValueBytes(t *testing.T) {
	src, err := xdm.ParseString(`<s a="xyz"><t>hello</t><!--c--></s>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got int
	e := xdmbuild.New(bytePolicy{got: &got, limit: 1 << 20}).StartElement(qn("e"))
	e.AppendNode(src.Root.FirstChild())
	if got != len("xyz")+len("hello")+len("c") {
		t.Errorf("charged %d bytes, want 9", got)
	}
	got = 0
	e = xdmbuild.New(bytePolicy{got: &got, limit: 4}).StartElement(qn("e"))
	e.AppendNode(src.Root.FirstChild())
	if !errors.Is(e.Refused(), xdm.ErrResourceLimit) || e.Open().FirstChild() != nil {
		t.Errorf("over the byte limit: Refused() = %v, copy made = %v",
			e.Refused(), e.Open().FirstChild() != nil)
	}
}
