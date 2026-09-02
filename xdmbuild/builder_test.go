package xdmbuild_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
)

// xsltLike is the policy XSLT uses: a duplicate attribute replaces the
// earlier one silently.
type xsltLike struct{}

func (xsltLike) Err(f xdmbuild.Fault, d string) error {
	if f == xdmbuild.FaultDuplicateAttribute {
		return nil
	}
	return fmt.Errorf("XSLT-%d: %s", int(f), d)
}
func (xsltLike) InheritNamespaces() bool  { return true }
func (xsltLike) PreserveNamespaces() bool { return true }
func (xsltLike) PreserveTypes() bool      { return true }
func (xsltLike) DropEmptyText() bool      { return false }

// xqueryLike is the policy XQuery uses: a duplicate attribute is XQDY0025.
type xqueryLike struct{}

func (xqueryLike) Err(f xdmbuild.Fault, d string) error {
	switch f {
	case xdmbuild.FaultDuplicateAttribute:
		return fmt.Errorf("XQDY0025: %s", d)
	case xdmbuild.FaultAttrAfterChild:
		return fmt.Errorf("XQTY0024: %s", d)
	}
	return fmt.Errorf("XQ-%d: %s", int(f), d)
}
func (xqueryLike) InheritNamespaces() bool  { return true }
func (xqueryLike) PreserveNamespaces() bool { return true }
func (xqueryLike) PreserveTypes() bool      { return true }
func (xqueryLike) DropEmptyText() bool      { return true }

func qn(local string) xdm.QName { return xdm.QName{Local: local} }

// The one place the two languages disagree about behaviour rather than about
// a code. XSLT 3.0 §5.7.1 discards the earlier attribute; XQuery 3.1
// §3.9.1.3 raises XQDY0025 for the same sequence.
func TestDuplicateAttributeIsTheBehaviouralFork(t *testing.T) {
	build := func(p xdmbuild.Policy) error {
		b := xdmbuild.New(p)
		el := b.StartElement(qn("e"))
		if err := el.AddAttribute(qn("a"), "1"); err != nil {
			return err
		}
		return el.AddAttribute(qn("a"), "2")
	}
	if err := build(xsltLike{}); err != nil {
		t.Errorf("XSLT policy: want silent last-wins, got %v", err)
	}
	err := build(xqueryLike{})
	if err == nil {
		t.Fatal("XQuery policy: want XQDY0025, got no error")
	}
	if !strings.Contains(err.Error(), "XQDY0025") {
		t.Errorf("XQuery policy: want XQDY0025, got %v", err)
	}
}

// Under XSLT's policy the later value must actually win.
func TestDuplicateAttributeLastWins(t *testing.T) {
	b := xdmbuild.New(xsltLike{})
	el := b.StartElement(qn("e"))
	_ = el.AddAttribute(qn("a"), "first")
	_ = el.AddAttribute(qn("a"), "second")
	root := b.ToTree()
	got := root.Children[0].Attrs
	if len(got) != 1 {
		t.Fatalf("want 1 attribute, got %d", len(got))
	}
	if got[0].Value != "second" {
		t.Errorf("want the later value to win, got %q", got[0].Value)
	}
}

// The same structural fault reaches each language under its own code.
func TestAttrAfterChildIsReportedPerLanguage(t *testing.T) {
	build := func(p xdmbuild.Policy) error {
		b := xdmbuild.New(p)
		el := b.StartElement(qn("e"))
		el.AppendText("text")
		return el.AddAttribute(qn("a"), "1")
	}
	for _, c := range []struct {
		policy xdmbuild.Policy
		want   string
	}{
		{xsltLike{}, fmt.Sprintf("XSLT-%d", int(xdmbuild.FaultAttrAfterChild))},
		{xqueryLike{}, "XQTY0024"},
	} {
		err := build(c.policy)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %s, got %v", c.want, err)
		}
	}
}

// §5.7.1 and §3.9.1.3 are the same text here: a run of adjacent atomic values
// becomes one text node with a single space between each, while adjacent text
// nodes merge with none. The zero-length cases are the ones a naive
// implementation gets wrong, and XQuery states them explicitly.
func TestAtomicRunSpacing(t *testing.T) {
	for _, c := range []struct {
		name string
		vals []string
		want string
	}{
		{"two strings", []string{"a", "b"}, "a b"},
		{"three strings", []string{"a", "b", "c"}, "a b c"},
		{"both zero-length", []string{"", ""}, " "},
		{"one zero-length", []string{"a", ""}, "a "},
	} {
		b := xdmbuild.New(xsltLike{})
		el := b.StartElement(qn("e"))
		for _, v := range c.vals {
			el.AppendValue(xdm.NewString(v))
		}
		root := b.ToTree()
		var sb strings.Builder
		for _, ch := range root.Children[0].Children {
			sb.WriteString(ch.Value)
		}
		if got := sb.String(); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// A text node next to an atomic value is not part of the atomic run, so no
// separator goes between them.
func TestTextAdjacentToAtomicHasNoSeparator(t *testing.T) {
	b := xdmbuild.New(xsltLike{})
	el := b.StartElement(qn("e"))
	el.AppendText("x")
	el.AppendValue(xdm.NewString("a"))
	root := b.ToTree()
	var sb strings.Builder
	for _, ch := range root.Children[0].Children {
		sb.WriteString(ch.Value)
	}
	if got := sb.String(); got != "xa" {
		t.Errorf("got %q want %q", got, "xa")
	}
}

// A map, array or function item has no representation in element content.
func TestOpaqueItemRefusedInsideElement(t *testing.T) {
	b := xdmbuild.New(xqueryLike{})
	el := b.StartElement(qn("e"))
	m := xdm.NewMap()
	if err := el.AppendOpaque(m); err == nil {
		t.Error("want a fault for a map inside element content")
	}
	// At the top level the same item is an ordinary member of the sequence.
	top := xdmbuild.New(xqueryLike{})
	if err := top.AppendOpaque(m); err != nil {
		t.Errorf("top level: %v", err)
	}
	if len(top.Sequence()) != 1 {
		t.Errorf("want the map in the sequence, got %d items", len(top.Sequence()))
	}
}

// New has no default policy to fall back on.
func TestNewRequiresAPolicy(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want a panic for a nil Policy")
		}
	}()
	xdmbuild.New(nil)
}
