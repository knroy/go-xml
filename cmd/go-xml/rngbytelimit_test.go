package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Containment answers which files may be read, not how much of one.
//
// This resolver was the only one in the library with no byte limit -- dtd
// bounds at 4 MB, xsd at 16 MB, xslt at 64 MB -- so a file inside the
// permitted root was read whole: a fifo never finishes, and a large regular
// file is held in memory before the parser is given the chance to refuse it.
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

	r := &rngFileResolver{root: dir, base: filepath.Join(dir, "main.rng")}
	if _, err := r.ResolveSchema("big.rng"); err == nil {
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
	if err := os.WriteFile(
		filepath.Join(dir, "ok.rng"), []byte(grammar), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &rngFileResolver{root: dir, base: filepath.Join(dir, "main.rng")}
	if _, err := r.ResolveSchema("ok.rng"); err != nil {
		t.Errorf("a small schema must still load: %v", err)
	}
}
