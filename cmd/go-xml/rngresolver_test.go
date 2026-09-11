package main

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/relaxng"
	"github.com/knroy/go-xml/xdm"
)

// These cases were written against the CLI's own rngFileResolver, which was a
// second copy of relaxng.FileResolver's policy. The duplicate is gone and the
// CLI now runs the library resolver, but the requirements the cases encode --
// root confinement, traversal refusal, symlink escape, the unconfined
// command-line default -- belong to the CLI whichever type implements them, so
// they are kept here and re-pointed rather than deleted with their subject.
//
// One difference matters. rngFileResolver carried a "base" field and joined a
// relative href itself; relaxng.FileResolver does not, because the compiler
// resolves every href against the base URI in force before the Resolver is
// ever called (relaxng.resolveHref). cliResolve reproduces exactly that join,
// so these cases exercise the same strings the CLI's compiler really produces.
func cliResolve(base, href string) string {
	if u := strings.Index(href, "://"); u >= 0 {
		return href
	}
	if filepath.IsAbs(href) || strings.HasPrefix(href, "/") {
		return href
	}
	dir := base
	if i := strings.LastIndexByte(dir, '/'); i >= 0 {
		dir = dir[:i+1]
	} else {
		dir = ""
	}
	return path.Clean(dir + href)
}

// A grammar inside the root loads; the root is enforced at open time.
//
// The refusal must carry os.Root's own "escapes from parent" wording, because
// that is what proves the open refused rather than the string comparison above
// it. Pre-resolving the final component would leave os.Root nothing to refuse,
// and this assertion is what would catch that regression -- the planted-symlink
// assertion on its own would not, since the earlier check refuses a visible
// link too.
func TestRNGFileResolverRefusesSymlinkAtOpenTime(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.rng")
	if err := os.WriteFile(secret, []byte(`<element name="s"><empty/></element>`), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "sub.rng")
	if err := os.WriteFile(inside, []byte(`<element name="ok"><empty/></element>`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "main.rng")
	r := &relaxng.FileResolver{Root: root, MaxBytes: DefaultMaxRNGBytes}

	if _, err := r.ResolveSchema(cliResolve(base, "sub.rng")); err != nil {
		t.Fatalf("a grammar inside the root should load: %v", err)
	}

	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, inside); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := r.ResolveSchema(cliResolve(base, "sub.rng"))
	if err == nil {
		t.Fatal("a symlink out of the root should be refused")
	}
	if !strings.Contains(err.Error(), "escapes from parent") {
		t.Errorf("refusal did not come from os.Root at open time, so the "+
			"containment is a string check again: %v", err)
	}
}

// Vectors that need no symlink privilege, so they execute on Windows rather
// than skipping. CLAUDE.md section 3: a check that does not run must not read
// as a check that passed.
//
// Every traversal target below is a file that really exists and really parses,
// planted by this test. That is the point: an href naming nothing is refused
// with ENOENT whether or not any confinement is in place, so a case built on a
// missing file passes with the protection deleted and proves nothing. The
// assertion is therefore on the *reason* -- the confinement said no -- and not
// merely on err != nil.
func TestRNGFileResolverRefusesUnprivilegedVectors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "main.rng")
	r := &relaxng.FileResolver{Root: root, MaxBytes: DefaultMaxRNGBytes}

	// A real, readable, well-formed grammar one level above the root. Both
	// "../secret.rng" and "sub/../../secret.rng" name it, so admitting either
	// is a genuine read of a file outside the grant rather than a failed open.
	grammar := []byte(`<element name="s" ` +
		`xmlns="http://relaxng.org/ns/structure/1.0"><empty/></element>`)
	if err := os.WriteFile(
		filepath.Join(filepath.Dir(root), "secret.rng"), grammar, 0o600); err != nil {
		t.Fatal(err)
	}
	// And one inside, so "main.rng::$DATA" names a stream of a file that is
	// really there.
	if err := os.WriteFile(base, grammar, 0o600); err != nil {
		t.Fatal(err)
	}

	// refusedBy reports whether err is a containment or scheme refusal rather
	// than an incidental failure such as "no such file".
	refusedBy := func(err error) bool {
		s := err.Error()
		return strings.Contains(s, "resolves outside root") ||
			strings.Contains(s, "escapes from parent") ||
			strings.Contains(s, "is not permitted") ||
			strings.Contains(s, "names the remote host")
	}

	for _, v := range []struct {
		name, href string
		// planted is true when the vector names something that really exists
		// here, so the refusal cannot be ENOENT in disguise.
		planted bool
	}{
		{"parent traversal", "../secret.rng", true},
		{"traversal through a subdir", "sub/../../secret.rng", true},
		{"deep traversal", "../../../../../../etc/passwd", false},
		// Only Windows gives "::$DATA" a meaning -- the default stream of the
		// file, which does exist here. Every other platform reads it as an
		// ordinary name that happens to contain colons, so it is refused as
		// "no such file" and cannot be marked planted: doing so would assert a
		// containment refusal that this platform has no reason to produce.
		{"alternate data stream", "main.rng::$DATA", false},
		{"drive absolute", `C:\Windows\win.ini`, false},
		{"drive absolute forward", "C:/Windows/win.ini", false},
		{"unix absolute", "/etc/passwd", false},
		{"UNC path", `\\server\share\secret.rng`, false},
		{"UNC forward", "//server/share/secret.rng", false},
		{"reserved device CON", "CON", false},
		{"reserved device NUL", "NUL", false},
		{"reserved device COM1", "COM1", false},
		{"reserved device AUX", "AUX", false},
		{"remote scheme", "http://example.invalid/s.rng", false},
		{"ftp scheme", "ftp://example.invalid/s.rng", false},
		{"data URI", "data:text/xml,<empty/>", false},
		// A file: URL with a remote authority. The authority must be refused,
		// not dropped: discarding it reads the same-named *local* path, which
		// is a read nobody asked for and a refusal that never happened. This
		// one is planted because dropping the authority here leaves
		// "/etc/passwd", a file that does exist -- so err != nil alone is
		// satisfied by the root confinement catching it afterwards, and the
		// authority check itself could be deleted unnoticed.
		{"remote file authority", "file://remote-host/etc/passwd", true},
	} {
		t.Run(v.name, func(t *testing.T) {
			_, err := r.ResolveSchema(cliResolve(base, v.href))
			if err == nil {
				t.Fatalf("%q was admitted", v.href)
			}
			if v.planted && !refusedBy(err) {
				t.Errorf("%q names a file that exists, and the refusal was not a "+
					"containment or scheme refusal: %v", v.href, err)
			}
		})
	}
}

// The positive case, so the refusals above cannot be met by refusing all.
func TestRNGFileResolverAdmitsPathsInsideRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	grammar := []byte(`<element name="ok"><empty/></element>`)
	if err := os.WriteFile(filepath.Join(sub, "in.rng"), grammar, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.rng"), grammar, 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "main.rng")
	r := &relaxng.FileResolver{Root: root, MaxBytes: DefaultMaxRNGBytes}

	// An href is a URI reference, so the separator is "/" on every platform.
	for _, href := range []string{"top.rng", "sub/in.rng", "./sub/in.rng", "sub/../top.rng"} {
		if _, err := r.ResolveSchema(cliResolve(base, href)); err != nil {
			t.Errorf("%q is inside the root and should load: %v", href, err)
		}
	}
}

// An empty root is the documented unconfined command-line case and must keep
// reading any readable path, or hardening the rooted case would have silently
// broken the default one.
func TestRNGFileResolverUnrootedStillReads(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "top.rng")
	if err := os.WriteFile(p, []byte(`<element name="ok"><empty/></element>`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "main.rng")
	r := &relaxng.FileResolver{MaxBytes: DefaultMaxRNGBytes}
	if _, err := r.ResolveSchema(cliResolve(base, "top.rng")); err != nil {
		t.Errorf("an unrooted resolver should read any readable path: %v", err)
	}
	// Unconfined is not unconditional: a remote scheme, and a file: URL naming
	// a remote host, stay refused with no Root to lean on.
	//
	// The reason is asserted, not merely the error. Unrooted there is no
	// confinement to fall back on, so dropping the authority instead of
	// refusing it opens and reads the local "/etc/passwd" and fails only later,
	// at the parse -- an unauthorised read that err != nil would report as a
	// pass. Requiring the refusal to name the scheme or the host is what keeps
	// this case honest.
	for _, href := range []string{
		"http://example.invalid/s.rng", "file://remote-host/etc/passwd",
	} {
		_, err := r.ResolveSchema(href)
		if err == nil {
			t.Errorf("an unrooted resolver admitted %q", href)
			continue
		}
		if !strings.Contains(err.Error(), "is not permitted") &&
			!strings.Contains(err.Error(), "names the remote host") {
			t.Errorf("an unrooted resolver refused %q, but not as a scheme or "+
				"host refusal -- it may have read a local file first: %v", href, err)
		}
	}
}

// The CLI must run the library resolver, not a second copy of its policy.
//
// This is the finding itself, asserted end to end rather than described. It
// drives schemaValidator -- the real entry point "go-xml validate -rng" uses --
// with an <include> that escapes -root, and requires the refusal. Nothing here
// constructs a resolver, so it holds whichever type validate.go installs, and
// it fails if that type ever stops confining.
func TestValidateConfinesIncludesToRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// The escape target is a real, well-formed grammar sitting one level above
	// -root. With confinement removed it compiles cleanly, so this case fails
	// the moment the protection goes -- unlike an href naming a missing file,
	// which errors either way and would pass sabotage.
	secret := filepath.Join(parent, "secret.rng")
	if err := os.WriteFile(secret,
		[]byte(`<grammar xmlns="http://relaxng.org/ns/structure/1.0">`+
			`<start><element name="ok"><empty/></element></start></grammar>`),
		0o600); err != nil {
		t.Fatal(err)
	}
	rng := filepath.Join(root, "main.rng")
	if err := os.WriteFile(rng,
		[]byte(`<grammar xmlns="http://relaxng.org/ns/structure/1.0">`+
			`<include href="../secret.rng"/></grammar>`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Sanity: unconfined, this very schema compiles. Without this the case
	// could be passing because the grammar is broken rather than because the
	// root held.
	if _, err := schemaValidator("", rng, "1.0", "", "", 1); err != nil {
		t.Fatalf("the escape schema must compile when unconfined, or the "+
			"confined case proves nothing: %v", err)
	}

	_, err := schemaValidator("", rng, "1.0", "", root, 1)
	if err == nil {
		t.Fatal("an <include> escaping -root was admitted by the CLI")
	}
	if !strings.Contains(err.Error(), "resolves outside root") &&
		!strings.Contains(err.Error(), "escapes from parent") {
		t.Errorf("the CLI refused, but not as a containment failure: %v", err)
	}
}

// And the positive half, so the case above cannot be met by refusing every
// include: one inside -root must still compile and validate.
func TestValidateAdmitsIncludesInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inc.rng"),
		[]byte(`<grammar xmlns="http://relaxng.org/ns/structure/1.0">`+
			`<start><element name="ok"><empty/></element></start></grammar>`),
		0o600); err != nil {
		t.Fatal(err)
	}
	rng := filepath.Join(root, "main.rng")
	if err := os.WriteFile(rng,
		[]byte(`<grammar xmlns="http://relaxng.org/ns/structure/1.0">`+
			`<include href="inc.rng"/></grammar>`), 0o600); err != nil {
		t.Fatal(err)
	}
	validate, err := schemaValidator("", rng, "1.0", "", root, 1)
	if err != nil {
		t.Fatalf("an <include> inside -root should compile: %v", err)
	}
	doc, err := xdm.ParseString(`<ok/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(doc.Root); err != nil {
		t.Errorf("a document matching the included grammar should validate: %v", err)
	}
}
