// Package record stores what a conformance or corpus run actually produced,
// one entry per case, so that two checkouts can be compared output for output
// (tests/recdiff). It is the differential gate for changes that must not move
// observable behaviour: a suite's pass count can stay put while the bytes a
// case writes change, and only a recording sees that.
//
// It is off unless GOXSLT_RECORD_DIR is set, and then writes, per suite,
// <dir>/<suite>.tsv with one "key<TAB>sha256<TAB>length" line per case, and
// the bytes under <dir>/obj/<sha256[:2]>/<sha256>. Identical outputs are
// stored once. Nothing here may change a result: it only reads what the
// runner already has.
package record

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	dir = os.Getenv("GOXSLT_RECORD_DIR")
	mu  sync.Mutex
)

// Enabled reports whether GOXSLT_RECORD_DIR asks for a recording.
func Enabled() bool { return dir != "" }

// Write records data as the output of case key in suite. A failure to write
// is fatal: a recording with holes would compare as cases gone missing.
func Write(suite, key string, data []byte) {
	if dir == "" {
		return
	}
	if err := Store(dir, suite, key, data); err != nil {
		fmt.Fprintln(os.Stderr, "record:", err)
		os.Exit(3)
	}
}

// Store is Write into an explicit directory.
func Store(root, suite, key string, data []byte) error {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	obj := filepath.Join(root, "obj", h[:2], h)
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(obj); err != nil {
		if err := os.MkdirAll(filepath.Dir(obj), 0o755); err != nil {
			return err
		}
		// Written aside and renamed, so a concurrent process recording
		// another suite never reads a half-written object.
		tmp := fmt.Sprintf("%s.%d.tmp", obj, os.Getpid())
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, obj); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(root, suite+".tsv"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	key = strings.NewReplacer("\t", `\t`, "\n", `\n`, "\r", `\r`).Replace(key)
	_, err = fmt.Fprintf(f, "%s\t%s\t%d\n", key, h, len(data))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
