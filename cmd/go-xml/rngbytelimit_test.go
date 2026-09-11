package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/relaxng"
	"github.com/knroy/go-xml/xdm"
)

// Containment answers which files may be read, not how much of one.
//
// The RELAX NG resolver was once the only one in the library with no byte
// limit -- dtd bounds at 4 MB, xsd at 16 MB, xslt at 64 MB -- so a file inside
// the permitted root was read whole: a fifo never finishes, and a large regular
// file is held in memory before the parser is given the chance to refuse it.
// The bound now lives in relaxng.FileResolver; what the CLI owns is choosing
// the figure, and that choice is what this asserts.
func TestRNGResolverRefusesAnOversizeSchema(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.rng")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// One byte over, so the bound itself is accepted and the next refused.
	if _, err := f.WriteString(strings.Repeat("x", DefaultMaxRNGBytes+1)); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	r := &relaxng.FileResolver{Root: dir, MaxBytes: DefaultMaxRNGBytes}
	if _, err := r.ResolveSchema(big); err == nil {
		t.Fatal("an oversize schema was read whole with no refusal")
	} else if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell the refusal from a malformed schema", err)
	}
}

// The other direction: the bound must not refuse an ordinary schema.
func TestRNGResolverStillReadsASmallSchema(t *testing.T) {
	dir := t.TempDir()
	const grammar = `<element name="a" ` +
		`xmlns="http://relaxng.org/ns/structure/1.0"><empty/></element>`
	ok := filepath.Join(dir, "ok.rng")
	if err := os.WriteFile(ok, []byte(grammar), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &relaxng.FileResolver{Root: dir, MaxBytes: DefaultMaxRNGBytes}
	if _, err := r.ResolveSchema(ok); err != nil {
		t.Errorf("a small schema must still load: %v", err)
	}
}

// The CLI must install a *bounded* resolver, not merely a confined one.
//
// A resolver built with MaxBytes left at zero would take the library's 64 MB
// default and this would still pass; the exact-boundary pair below is what
// distinguishes the CLI's own 16 MB figure from it. Written against the
// resolver schemaValidator really constructs, so deleting the MaxBytes field
// from validate.go fails here.
func TestValidateResolverCarriesTheCLIByteCeiling(t *testing.T) {
	dir := t.TempDir()
	r := cliRNGResolver(dir)

	atLimit := filepath.Join(dir, "at.rng")
	if err := os.WriteFile(atLimit,
		[]byte(strings.Repeat("x", DefaultMaxRNGBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exactly at the bound is admitted by the *resolver*; it then fails to
	// parse, which is a different error and proves the read happened.
	if _, err := r.ResolveSchema(atLimit); errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("a schema exactly at the %d byte bound was refused as oversize",
			DefaultMaxRNGBytes)
	}

	over := filepath.Join(dir, "over.rng")
	if err := os.WriteFile(over,
		[]byte(strings.Repeat("x", DefaultMaxRNGBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveSchema(over); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("one byte over the CLI bound = %v, want ErrResourceLimit; the "+
			"CLI is running the library's 64 MB default, not its own figure", err)
	}
}
