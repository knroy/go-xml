package dtd

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// SECURITY (run-5). Bare elements of a type declaring R #REQUIRED attributes
// each walked all R declarations, even past MaxErrors: R=4000, E=4000 cost
// 370 MB. Past the cap the walk stops, so the cost no longer scales as R x E.
func TestSM10RequiredWalkStopsAtMaxErrors(t *testing.T) {
	const r, e = 2000, 4000
	var dt strings.Builder
	dt.WriteString(`<!DOCTYPE d [<!ELEMENT d (r)*><!ELEMENT r EMPTY><!ATTLIST r`)
	for i := range r {
		fmt.Fprintf(&dt, " a%d CDATA #REQUIRED", i)
	}
	dt.WriteString(">]>")
	src := dt.String() + "<d>" + strings.Repeat("<r/>", e) + "</d>"
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := check(t, src)
	runtime.ReadMemStats(&after)
	var es *Errors
	if !errors.As(err, &es) || len(es.Errors) != DefaultMaxErrors {
		t.Fatalf("got %v, want %d errors", err, DefaultMaxErrors)
	}
	// R x E alone is 8M pairs; the old walk allocated ~185 MB here.
	if got := after.TotalAlloc - before.TotalAlloc; got > 40<<20 {
		t.Fatalf("validation allocated %d MB; the #REQUIRED walk scales with elements again", got>>20)
	}
}

// Every #REQUIRED attribute present lets the walk skip idx.always, and one
// missing still reports, in declaration order.
func TestSM10RequiredStillReported(t *testing.T) {
	dt := `<!DOCTYPE r [<!ELEMENT r EMPTY><!ATTLIST r a CDATA #REQUIRED b CDATA #REQUIRED c CDATA #FIXED "x">]>`
	if err := check(t, dt+`<r b="1" a="2"/>`); err != nil {
		t.Fatalf("all present: %v", err)
	}
	err := check(t, dt+`<r c="y"/>`)
	if err == nil || !strings.Contains(err.Error(), "a is missing") ||
		strings.Index(err.Error(), "b is missing") < strings.Index(err.Error(), "a is missing") ||
		!strings.Contains(err.Error(), "#FIXED") {
		t.Fatalf("got %v", err)
	}
}

// SECURITY (run-5). Each compiled content model was held for the whole call,
// so D dense declarations near the edge budget kept D x ~54 MB live (858 MB at
// D=16 from a 67 KB subset). Models are matched one declaration at a time.
func TestSM10ContentModelsNotRetained(t *testing.T) {
	const d = 8
	model := "(a?" + strings.Repeat(",a?", 1399) + ")"
	var dt, body strings.Builder
	dt.WriteString(`<!DOCTYPE r [<!ELEMENT r ANY><!ELEMENT a EMPTY>`)
	body.WriteString("<r>")
	for k := range d {
		fmt.Fprintf(&dt, "<!ELEMENT e%d %s>", k, model)
		fmt.Fprintf(&body, "<e%d/><e%d><a/></e%d>", k, k, k)
	}
	src := dt.String() + "]>" + body.String() + "</r>"
	runtime.GC()
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > peak.Load() {
				peak.Store(ms.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	err := check(t, src)
	close(stop)
	<-done
	if err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	// Retained, the eight models held ~430 MB.
	if p := peak.Load(); p > 250<<20 {
		t.Fatalf("peak heap %d MB; compiled content models are retained across declarations", p>>20)
	}
}

// Deferred content-model failures keep their place in document order among
// the other failures, and the MaxErrors cap counts them.
func TestSM10ContentFailuresInDocumentOrder(t *testing.T) {
	src := `<!DOCTYPE r [<!ELEMENT r (x|y)*><!ELEMENT x (a)><!ELEMENT y (b)>` +
		`<!ELEMENT a EMPTY><!ELEMENT b EMPTY><!ATTLIST y n CDATA #REQUIRED>]>` +
		`<r><y n="1"><a/></y><x/><y/><x><a/></x><y n="2"><b/></y></r>`
	err := check(t, src)
	var es *Errors
	if !errors.As(err, &es) {
		t.Fatalf("got %v", err)
	}
	var got []string
	for _, e := range es.Errors {
		got = append(got, e.Message)
	}
	exp := []string{
		"element a is not permitted here in the content of y",
		"the content of x is incomplete",
		"the content of y is incomplete",
		"required attribute n is missing",
	}
	if strings.Join(got, "|") != strings.Join(exp, "|") {
		t.Fatalf("got %q\nwant %q", got, exp)
	}
	tree, err := xdm.ParseString(src, xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	dd, err := Parse(tree.DocType)
	if err != nil {
		t.Fatal(err)
	}
	err = Validate(tree.Root, dd, Options{MaxErrors: 2})
	if !errors.As(err, &es) || len(es.Errors) != 2 || es.Errors[1].Message != exp[1] {
		t.Fatalf("capped: %v", err)
	}
}
