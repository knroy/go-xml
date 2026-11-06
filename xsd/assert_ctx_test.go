package xsd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// ValidateContext's deadline must reach inside an assertion. The uniqueness
// idiom below is quadratic in the number of items, and the run used to
// ignore the deadline until the assertion finished — seconds later — and
// then return a verdict instead of context.DeadlineExceeded.
func TestAssertionHonoursValidateDeadline(t *testing.T) {
	sd, err := xdm.ParseString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
		`<xs:element name="r"><xs:complexType><xs:sequence>`+
		`<xs:element name="item" type="xs:string" maxOccurs="unbounded"/></xs:sequence>`+
		`<xs:assert test="every $i in item satisfies count(item[. = $i]) eq 1"/>`+
		`</xs:complexType></xs:element></xs:schema>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(sd.Root, "", Options{Version: Version11})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("<r>")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "<item>v%d</item>", i)
	}
	b.WriteString("</r>")
	doc, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- s.ValidateContext(ctx, doc.Root, ValidateOptions{}) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ValidateContext = %v after %v, want context.DeadlineExceeded",
				err, time.Since(start))
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("the 30ms deadline stopped the run only after %v", d)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("ValidateContext ignored its deadline for 20s")
	}
	// The same run under no deadline is a verdict: the items are distinct.
	small, _ := xdm.ParseString("<r><item>a</item><item>b</item></r>", xdm.ParseOptions{})
	if err := s.ValidateContext(context.Background(), small.Root, ValidateOptions{}); err != nil {
		t.Errorf("distinct items must satisfy the assertion: %v", err)
	}
}
