package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fn:unparsed-text hands a stylesheet the raw bytes of whatever it names, so
// the resolver must not read anything until the caller has said so, and must
// then read only inside its roots. Each of those is asserted here rather than
// left to a comment: the default is the only thing standing between a
// production caller and a file-disclosure primitive.

func TestFileResolverUnparsedTextOffByDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{dir}}
	if _, err := r.ResolveText("a.txt", fileURIOf(filepath.Join(dir, "s.xsl")), ""); err == nil {
		t.Fatal("ResolveText read a file with UnparsedText unset; the default must refuse")
	}
}

func TestFileResolverReadsTextWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{dir}, UnparsedText: true}
	got, err := r.ResolveText("a.txt", fileURIOf(filepath.Join(dir, "s.xsl")), "")
	if err != nil {
		t.Fatalf("ResolveText: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

// The whole point of routing through resolvePath rather than calling
// os.ReadFile: a path outside every root is refused even when the function is
// enabled. Enabling unparsed-text grants the function, not the filesystem.
func TestFileResolverRefusesTextOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{root}, UnparsedText: true}

	base := fileURIOf(filepath.Join(root, "s.xsl"))
	for _, href := range []string{
		secret,
		"../" + filepath.Base(outside) + "/secret.txt",
		"file://" + secret,
	} {
		got, err := r.ResolveText(href, base, "")
		if err == nil {
			t.Errorf("ResolveText(%q) returned %q; a path outside the roots must be refused",
				href, got)
		}
	}
}

// A symlink inside a root pointing outside it is the classic bypass, and the
// only thing that stops it is resolving the link *before* the containment
// check. resolvePath does that already; this pins that ResolveText goes
// through it rather than around it.
func TestFileResolverRefusesSymlinkedText(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	r := &FileResolver{Roots: []string{root}, UnparsedText: true}
	got, err := r.ResolveText("link.txt", fileURIOf(filepath.Join(root, "s.xsl")), "")
	if err == nil {
		t.Fatalf("ResolveText followed a symlink out of the root and returned %q", got)
	}
}

// An http: URI is an SSRF primitive, and it is rejected on the scheme before
// the filesystem is touched -- the same gate every other read here goes
// through, not a second one written separately.
func TestFileResolverRefusesNonFileSchemeText(t *testing.T) {
	r := &FileResolver{Roots: []string{t.TempDir()}, UnparsedText: true}
	_, err := r.ResolveText("http://example.com/a.txt", "", "")
	if err == nil {
		t.Fatal("ResolveText accepted an http: URI")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Errorf("err = %v, want a scheme refusal", err)
	}
}

// Bytes that are not valid UTF-8 are reported rather than returned. A Go
// string holding them would push the failure into the serialiser, where it
// reads as a bug in this engine instead of as a property of the input.
//
// The code is FOUT1190, which is what fn:unparsed-text's callers expect:
// fn-unparsed-text-045 and -048 read an iso-8859-1 file with no encoding
// argument and accept the decoded string or FOUT1190, and nothing else.
// fn:json-doc restates it as FOUT1200 on its own side, where the JSON cases
// require that code instead.
func TestFileResolverRefusesUndecodableText(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.txt"), []byte{0xff, 0xfe, 0x41}, 0o644); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{dir}, UnparsedText: true}
	base := fileURIOf(filepath.Join(dir, "s.xsl"))
	_, err := r.ResolveText("bad.txt", base, "")
	if err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
	if !strings.Contains(err.Error(), "FOUT1190") {
		t.Errorf("err = %v, want FOUT1190", err)
	}
}

// A character XML does not permit is fn:unparsed-text's rule rather than the
// resolver's, so the resolver hands the text back and the function rejects
// it. The split is what lets fn:json-doc read the same file: a JSON text may
// hold U+FFFF, and an unescaped control character in one is FOJS0001 from the
// JSON parser rather than a decoding error raised before the parser runs.
func TestFileResolverReturnsNonXMLCharacters(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ctl.txt"), []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{dir}, UnparsedText: true}
	base := fileURIOf(filepath.Join(dir, "s.xsl"))
	got, err := r.ResolveText("ctl.txt", base, "")
	if err != nil {
		t.Fatalf("ResolveText: %v", err)
	}
	if got != "a\x00b" {
		t.Errorf("ResolveText = %q, want the bytes unchanged", got)
	}
}

// An encoding this package cannot decode is an error, not a silent
// reinterpretation: returning Shift-JIS bytes as if they were UTF-8 produces
// wrong output with nothing to indicate it.
func TestFileResolverRefusesUnsupportedTextEncoding(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{dir}, UnparsedText: true}
	_, err := r.ResolveText("a.txt", fileURIOf(filepath.Join(dir, "s.xsl")), "Shift_JIS")
	if err == nil {
		t.Fatal("an unsupported encoding was accepted")
	}
	if !strings.Contains(err.Error(), "FOUT1190") {
		t.Errorf("err = %v, want FOUT1190", err)
	}
}
