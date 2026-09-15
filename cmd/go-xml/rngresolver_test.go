package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/internal/uripath"
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
//
// "Exactly" is the load-bearing word, and an earlier spelling of this helper
// did not manage it. It split the base with strings.LastIndexByte(dir, '/') --
// forward slash only -- while the base handed to it is an OS path from
// filepath.Join. On Unix the two coincide and the helper looked right. On
// Windows the base is C:\...\001\main.rng, which contains no forward slash at
// all, so the directory came out empty and every href resolved to a bare name:
// "top.rng" instead of the file beside the schema. That is not what the CLI
// does. validate.go hands the compiler fileURI(rngPath) -- an absolute file:
// URI -- and relaxng.joinRef then takes its URI branch, url.ResolveReference,
// never the relative-base split that the old helper had copied. The helper had
// copied the wrong half of joinRef.
//
// So the base is spelled as a file: URI first, exactly as validate.go spells
// it, and the join is the URI resolution the compiler really performs. The
// href stays a URI reference throughout -- its separator is "/" on every
// platform, which is true of the href and was never true of the base.
func cliResolve(base, href string) string {
	return cliResolveURI(fileURI(base), href)
}

// cliResolveURI is cliResolve with the base already spelled as a file: URI.
//
// It is split out for the same reason absPathToFileURI is split out of
// fileURI: fileURI calls filepath.Abs, which is host-relative, so a
// C:\...\main.rng base cannot be spelled on darwin and the Windows shape of
// this join would be untestable anywhere but Windows. Everything below is
// pure string work over a URI, so both platforms' bases can be driven through
// it from any host -- which is what TestCLIResolveJoinsAgainstOSPathBase does.
func cliResolveURI(baseURI, href string) string {
	// An href that names a scheme, or is absolute in any spelling the resolver
	// must judge for itself -- a drive path, a UNC name, a rooted Unix path --
	// is passed through untouched, so the refusal under test is the resolver's
	// and not an artefact of this join.
	if u, err := url.Parse(href); err == nil && u.IsAbs() {
		return href
	}
	if uripath.IsDriveLetterPath(href) || filepath.IsAbs(href) ||
		strings.HasPrefix(href, "/") || strings.HasPrefix(href, `\\`) {
		return href
	}
	b, err := url.Parse(baseURI)
	if err != nil || !b.IsAbs() {
		return href
	}
	r, err := url.Parse(href)
	if err != nil {
		return href
	}
	return b.ResolveReference(r).String()
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

	// Sanity: rooted at the parent, this very schema compiles. Without this
	// the case could be passing because the grammar is broken rather than
	// because the root held.
	if _, err := schemaValidator("", rng, "1.0", "", parent, 1); err != nil {
		t.Fatalf("the escape schema must compile under the parent root, or "+
			"the confined case proves nothing: %v", err)
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

// The Windows half of cliResolve's join, proved on any host.
//
// This case exists because the defect it guards was invisible to every darwin
// and Linux run of the suite above. Those cases build their base with
// filepath.Join(t.TempDir(), "main.rng"), so on this host the base is
// /tmp/.../main.rng and the old forward-slash split found its directory; the
// same code met C:\...\001\main.rng on Windows, found no "/" at all, and
// resolved "top.rng" to "top.rng". The whole suite was green here and five
// cases failed there.
//
// A host cannot be changed, so the host-relative step is removed instead:
// cliResolveURI takes the base already spelled as a URI, and the two spellings
// -- file:///tmp/T/001/main.rng and file:///C:/Users/.../001/main.rng -- are
// written out literally, which any platform can do. absPathToFileURI is the
// same function fileURI uses to produce them, so the Windows string here is
// the string Windows really produces, not an approximation of it.
//
// The assertion that matters is the first one: the result must still carry the
// base's directory. A join that discarded it returns the bare href, which is
// exactly the CI failure -- "top.rng" resolving outside the root.
func TestCLIResolveJoinsAgainstOSPathBase(t *testing.T) {
	for _, base := range []struct {
		name, dir string
	}{
		{"unix", "/tmp/T/001"},
		{"windows", "/C:/Users/runneradmin/AppData/Local/Temp/T/001"},
	} {
		t.Run(base.name, func(t *testing.T) {
			baseURI := absPathToFileURI(base.dir + "/main.rng")
			dirURI := absPathToFileURI(base.dir) + "/"
			for _, c := range []struct{ href, want string }{
				{"top.rng", dirURI + "top.rng"},
				{"sub/in.rng", dirURI + "sub/in.rng"},
				{"./sub/in.rng", dirURI + "sub/in.rng"},
				{"sub/../top.rng", dirURI + "top.rng"},
			} {
				got := cliResolveURI(baseURI, c.href)
				if got != c.want {
					t.Errorf("cliResolveURI(%q, %q) = %q, want %q",
						baseURI, c.href, got, c.want)
				}
				// The directory is the half the bug dropped, so assert it in
				// its own right: a future join that returned the href
				// unchanged would fail above, but a join that returned some
				// other rooted string should fail too.
				if !strings.HasPrefix(got, dirURI) {
					t.Errorf("cliResolveURI(%q, %q) = %q, which does not sit "+
						"under the base directory %q -- the base was discarded",
						baseURI, c.href, got, dirURI)
				}
			}
			// An escape must still compose as an escape rather than being
			// clamped, or the refusal cases above would be passing because
			// this helper quietly made every href safe.
			if got := cliResolveURI(baseURI, "../secret.rng"); strings.HasPrefix(got, dirURI) {
				t.Errorf("cliResolveURI(%q, %q) = %q, which was clamped inside "+
					"the base directory instead of escaping it",
					baseURI, "../secret.rng", got)
			}
			// And an absolute or scheme-bearing href reaches the resolver
			// untouched, so the resolver's own refusal is what the vector
			// cases observe.
			for _, href := range []string{
				`C:\Windows\win.ini`, "C:/Windows/win.ini", "/etc/passwd",
				`\\server\share\secret.rng`, "http://example.invalid/s.rng",
				"file://remote-host/etc/passwd", "data:text/xml,<empty/>",
			} {
				if got := cliResolveURI(baseURI, href); got != href {
					t.Errorf("cliResolveURI(%q, %q) = %q, want it passed through "+
						"unchanged so the resolver judges it", baseURI, href, got)
				}
			}
		})
	}
}
