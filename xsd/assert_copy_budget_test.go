package xsd

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// nestedAssertSchema declares s, an asserted type that nests itself, beside
// x, a leaf. Every <s> in the instance therefore copies its whole subtree for
// its assertion.
const nestedAssertSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
	`<xs:complexType name="E"/>` +
	`<xs:complexType name="T"><xs:choice minOccurs="0" maxOccurs="unbounded">` +
	`<xs:element ref="s"/><xs:element name="x" type="E"/></xs:choice>` +
	`<xs:assert test="exists(.)"/></xs:complexType>` +
	`<xs:element name="s" type="T"/></xs:schema>`

// nestedAssertDoc is depth nested <s> elements with leaves <x/> inside the
// innermost.
func nestedAssertDoc(t *testing.T, depth, leaves int) *xdm.Node {
	t.Helper()
	var b strings.Builder
	b.WriteString(strings.Repeat("<s>", depth))
	b.WriteString(strings.Repeat("<x/>", leaves))
	b.WriteString(strings.Repeat("</s>", depth))
	doc, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return doc.Root
}

// The scope copy of every asserted ancestor is charged to the run's node
// budget. Here 50 levels over 41,000 leaves copy about 2.05 million nodes in
// all, past xpath.MaxNodes, although each copy alone is well under it.
func TestNestedAssertionCopiesAreBudgeted(t *testing.T) {
	s := loadAssertionSchema(t, nestedAssertSchema)
	if err := s.Validate(nestedAssertDoc(t, 1, 41000), ValidateOptions{}); err != nil {
		t.Fatalf("one asserted level over 41,000 leaves was refused: %v", err)
	}
	err := s.Validate(nestedAssertDoc(t, 50, 41000), ValidateOptions{})
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("Validate = %v, want the node budget's refusal", err)
	}
	if !strings.Contains(err.Error(), "result-tree nodes") {
		t.Errorf("message %q is not the node-budget refusal", err)
	}
}

// cancelAfter is a context whose Err reports Canceled from call n onward, and
// records the heap allocated by then.
type cancelAfter struct {
	context.Context
	n, calls  int64
	allocated uint64
}

func (c *cancelAfter) Err() error {
	if atomic.AddInt64(&c.calls, 1) < c.n {
		return nil
	}
	if c.allocated == 0 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		c.allocated = m.TotalAlloc
	}
	return context.Canceled
}

// Once the run is cancelled, the asserted ancestors unwinding past the stop
// must not copy their subtrees. The context turns cancelled at the first leaf,
// so before the fix all 200 copies (about a million nodes) were built after it.
func TestAssertionCopySkippedAfterCancel(t *testing.T) {
	s := loadAssertionSchema(t, nestedAssertSchema)
	const depth = 200
	doc := nestedAssertDoc(t, depth, 5000)
	ctx := &cancelAfter{Context: context.Background(), n: depth + 1}
	err := s.ValidateContext(ctx, doc, ValidateOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateContext = %v, want context.Canceled", err)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if after := m.TotalAlloc - ctx.allocated; after > 16<<20 {
		t.Errorf("%d MB allocated after the context was cancelled", after>>20)
	}
}

// allocatedBy returns the bytes f allocates.
func allocatedBy(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// nsDecls returns k distinct namespace declarations.
func nsDecls(k int) string {
	var b strings.Builder
	for i := 0; i < k; i++ {
		fmt.Fprintf(&b, ` xmlns:p%d="urn:x:%d"`, i, i)
	}
	return b.String()
}

// An assertion's scope copy builds its namespace frame once. One AddNamespace
// per binding copied and interned the frame built so far, so 2,000
// declarations on the asserted element allocated about 600 MB.
func TestAssertionScopeNamespacesLinear(t *testing.T) {
	s := loadAssertionSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
		`<xs:element name="r"><xs:complexType><xs:sequence/>`+
		`<xs:assert test="true()"/></xs:complexType></xs:element></xs:schema>`)
	doc, err := xdm.ParseString(`<r`+nsDecls(2000)+`/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var verr error
	n := allocatedBy(func() { verr = s.Validate(doc.Root, ValidateOptions{}) })
	if verr != nil {
		t.Fatal(verr)
	}
	if n > 32<<20 {
		t.Errorf("validating 2,000 declarations allocated %d MB", n>>20)
	}
}

// A type alternative's scope copy carries the in-scope bindings the same way:
// declared once on the root, they were copied quadratically for every child.
func TestAlternativeScopeNamespacesLinear(t *testing.T) {
	s := loadAssertionSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
		`<xs:element name="r"><xs:complexType><xs:sequence>`+
		`<xs:element name="c" maxOccurs="unbounded">`+
		`<xs:alternative test="@a = '1'" type="xs:string"/>`+
		`<xs:alternative type="xs:anyType"/></xs:element>`+
		`</xs:sequence></xs:complexType></xs:element></xs:schema>`)
	doc, err := xdm.ParseString(`<r`+nsDecls(1000)+`>`+strings.Repeat("<c/>", 20)+`</r>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var verr error
	n := allocatedBy(func() { verr = s.Validate(doc.Root, ValidateOptions{}) })
	if verr != nil {
		t.Fatal(verr)
	}
	if n > 32<<20 {
		t.Errorf("20 alternatives under 1,000 declarations allocated %d MB", n>>20)
	}
}
