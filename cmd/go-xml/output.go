package main

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// principalOutput is where one transform's principal result goes: stdout, or
// the -o file. The transform writes it as it is produced
// (TransformOptions.Output), so a streamed transform holds only what it has
// not yet written rather than its whole result.
//
// A file is written to a temporary file beside it and renamed over it only
// once the transform has succeeded, so a failed run leaves the -o file as it
// was rather than truncated or half written, and -o may name the input
// itself, which is still being read while the result is written.
type principalOutput struct {
	*holdWriter
	buf  *bufio.Writer
	tmp  *os.File // nil for stdout and for direct
	dest string   // the file tmp is renamed to
	mode fs.FileMode
	// direct is an -o file written in place: a device or pipe, or a file
	// whose directory is read-only.
	direct *os.File
}

// openPrincipal opens the principal output for one transform. label, if not
// empty, is written ahead of the result.
func openPrincipal(outPath, label string) (*principalOutput, error) {
	if outPath == "" {
		buf := bufio.NewWriter(os.Stdout)
		return &principalOutput{holdWriter: newHoldWriter(buf, label), buf: buf}, nil
	}
	// os.Create followed a symlink and wrote its target, keeping the target's
	// mode; renaming over the link would replace the link instead. So the
	// file renamed over is the one the link resolves to, and the mode is the
	// existing file's, or the one os.Create would have given a new file.
	dest, mode := outPath, newFileMode()
	if fi, err := os.Stat(outPath); err == nil {
		if !fi.Mode().IsRegular() {
			// A device or a pipe (-o /dev/null, a FIFO) cannot be replaced by
			// a rename, and has no contents to keep: it is written directly,
			// as os.Create wrote it.
			return openDirect(outPath, label)
		}
		if dest, err = filepath.EvalSymlinks(outPath); err != nil {
			return nil, err
		}
		mode = fi.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".*.tmp")
	if errors.Is(err, fs.ErrPermission) {
		// A rename needs a writable directory, which os.Create did not: a
		// writable file in a read-only directory is written in place, as
		// before, and a failed run then leaves it truncated.
		return openDirect(outPath, label)
	}
	if err != nil {
		return nil, err
	}
	buf := bufio.NewWriter(tmp)
	return &principalOutput{holdWriter: newHoldWriter(buf, label), buf: buf, tmp: tmp, dest: dest, mode: mode}, nil
}

// openDirect opens outPath to be written in place, as os.Create opens it.
func openDirect(outPath, label string) (*principalOutput, error) {
	f, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	buf := bufio.NewWriter(f)
	return &principalOutput{holdWriter: newHoldWriter(buf, label), buf: buf, direct: f}, nil
}

// commit writes what is still held and makes the output final: stdout is
// flushed, and a file is renamed over the -o path.
func (o *principalOutput) commit() error {
	if err := o.release(); err != nil {
		o.abort()
		return err
	}
	if err := o.buf.Flush(); err != nil {
		o.abort()
		return err
	}
	if o.direct != nil {
		return o.direct.Close()
	}
	if o.tmp == nil {
		return nil
	}
	if err := o.tmp.Chmod(o.mode); err != nil {
		o.abort()
		return err
	}
	if err := o.tmp.Close(); err != nil {
		os.Remove(o.tmp.Name())
		return err
	}
	if err := os.Rename(o.tmp.Name(), o.dest); err != nil {
		os.Remove(o.tmp.Name())
		return err
	}
	return nil
}

// abort drops the output of a failed transform. A file's temporary copy is
// removed, leaving the -o file untouched. On stdout, or a device or pipe, what is still held is
// dropped, but what the transform wrote past the hold is already out: a
// result written as it is produced cannot be taken back.
func (o *principalOutput) abort() {
	o.held, o.passed = nil, true
	if o.tmp != nil {
		o.tmp.Close()
		os.Remove(o.tmp.Name())
		return
	}
	o.buf.Flush()
	if o.direct != nil {
		o.direct.Close()
	}
}

// holdLimit is how much of a result holdWriter holds before passing it on.
// An empty principal result serializes to its XML declaration and nothing
// else, far below this, which is all discard ever needs to drop.
const holdLimit = 4 << 10

// holdWriter holds the first holdLimit bytes written to it, then passes
// everything through. It exists for an xsl:result-document with no href,
// which is the principal output (XSLT 3.0 §24.3): the engine still writes the
// empty principal result's serialization (its XML declaration) to Output,
// and the href-less document is serialized after the transform with its own
// settings. When one is returned the principal result was empty -- the engine
// raises XTDE1490 if it had content -- so what was held is that empty
// serialization, and discard drops it, as the CLI did before it wrote the
// principal result as it was produced.
//
// It also holds the -multiple label, so that a failed input in a batch
// writes no label with nothing after it.
type holdWriter struct {
	w      io.Writer
	label  string
	held   []byte
	passed bool
}

func newHoldWriter(w io.Writer, label string) *holdWriter {
	return &holdWriter{w: w, label: label}
}

func (h *holdWriter) Write(p []byte) (int, error) {
	if !h.passed {
		if len(h.held)+len(p) <= holdLimit {
			h.held = append(h.held, p...)
			return len(p), nil
		}
		if err := h.release(); err != nil {
			return 0, err
		}
	}
	return h.w.Write(p)
}

// release writes the label and what is held, and passes the rest through.
func (h *holdWriter) release() error {
	if h.passed {
		return nil
	}
	h.passed = true
	held := h.held
	h.held = nil
	if _, err := io.WriteString(h.w, h.label); err != nil {
		return err
	}
	_, err := h.w.Write(held)
	return err
}

// discard drops what is held, but not the label, and passes the rest
// through. It is a no-op once the hold has been released.
func (h *holdWriter) discard() error {
	if h.passed {
		return nil
	}
	h.held = nil
	return h.release()
}
