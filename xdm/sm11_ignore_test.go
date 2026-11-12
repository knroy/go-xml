package xdm

import (
	"io"
	"strings"
	"testing"
	"time"
)

// TestSM11UnterminatedIgnoreLinear: the parameter-entity pre-pass stops at an
// unterminated IGNORE section instead of rescanning the tail from every later
// "<![", and reads a PI to "?>" rather than its first ">". k=20000 took
// seconds when the pass was quadratic.
func TestSM11UnterminatedIgnoreLinear(t *testing.T) {
	if _, _, end := ignoredSection("<![IGNORE[ x <![IGNORE[", 0, nil); end != -1 {
		t.Fatalf("unterminated section: end = %d, want -1", end)
	}
	if got := endOfDTDConstruct("<?pi > x ?>y", 0); got != len("<?pi > x ?>") {
		t.Fatalf("PI end = %d, want %d", got, len("<?pi > x ?>"))
	}
	const k = 20000
	opts := ParseOptions{AllowDOCTYPE: true, ExternalEntities: refusingResolver{}}
	pi := `<!DOCTYPE r [<!ENTITY % e ""><?pi > ` + strings.Repeat("<![IGNORE[", k) + ` ?>]><r/>`
	t0 := time.Now()
	if _, err := ParseString(pi, opts); err != nil {
		t.Fatalf("PI holding section starts: %v", err)
	}
	// An external subset reaches the pre-pass before its grammar check.
	ext := ParseOptions{AllowDOCTYPE: true, BaseURI: "mem:doc.xml",
		ExternalEntities: sm11Ext(`<!ENTITY % e "">` + strings.Repeat("<![IGNORE[", k))}
	if _, err := ParseString(`<!DOCTYPE r SYSTEM "ext.dtd"><r/>`, ext); err == nil {
		t.Fatal("unterminated IGNORE section accepted")
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("pre-pass took %v for k=%d; it is meant to be linear", d, k)
	}
}

// sm11Ext serves one fixed text for every external entity.
type sm11Ext string

func (s sm11Ext) ResolveEntity(_, _, _ string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader(string(s))), "mem:ext.dtd", nil
}
