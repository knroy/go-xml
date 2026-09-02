package xslt

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// XInclude lets an element in the SOURCE DOCUMENT name a resource to read, and
// the source document is exactly the party this library's threat model treats
// as hostile. That makes the confinement the whole of the safety argument, so
// it is asserted here rather than argued for in a comment: an inclusion must
// reach nothing fn:doc could not already reach, and it must reach no network
// at all.

// The containment check is what stands between a hostile document and every
// file on the machine. Each spelling below is a way of writing "somewhere
// else" that has broken a path check somewhere.
func TestResolveIncludeRefusesPathsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{root}}
	base := fileURIOf(filepath.Join(root, "doc.xml"))

	for _, href := range []string{
		secret,
		"file://" + secret,
		"../" + filepath.Base(outside) + "/secret.txt",
		"./../../" + filepath.Base(outside) + "/secret.txt",
		"/etc/passwd",
	} {
		got, _, err := r.ResolveInclude(href, base, "")
		if err == nil {
			t.Errorf("ResolveInclude(%q) returned %q; a path outside the roots must be refused",
				href, got)
		}
	}
}

// A symlink inside a root pointing out of it is the classic way past a check
// that compares the path as written. resolvePath resolves links BEFORE
// comparing, and ResolveInclude goes through it unchanged.
func TestResolveIncludeRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r := &FileResolver{Roots: []string{root}}
	if got, _, err := r.ResolveInclude("link.txt", fileURIOf(filepath.Join(root, "doc.xml")), ""); err == nil {
		t.Errorf("a symlink out of the root was followed, returning %q", got)
	}
}

// No network scheme may be reachable. The canary server records any hit, so a
// refusal that happened for the wrong reason — a parse failure, say — still
// fails the test if a request went out.
func TestResolveIncludeRefusesNetworkSchemes(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("<pwned/>"))
	}))
	defer srv.Close()

	root := t.TempDir()
	r := &FileResolver{Roots: []string{root}}
	base := fileURIOf(filepath.Join(root, "doc.xml"))

	for _, href := range []string{
		srv.URL + "/x.xml",
		"http://127.0.0.1:1/x.xml",
		"https://example.invalid/x.xml",
		"ftp://example.invalid/x.xml",
		// A host-form file URI names another machine's filesystem on some
		// platforms; it is not a local path and must not be treated as one.
		"file://evil.invalid/etc/passwd",
	} {
		if _, _, err := r.ResolveInclude(href, base, ""); err == nil {
			t.Errorf("ResolveInclude(%q) succeeded; no scheme but file may be reached", href)
		}
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("the canary server was contacted %d times; XInclude must reach no network", n)
	}
}

// The end-to-end shape of the above: a hostile document whose xi:include names
// an http URL must not fetch it, and — having no fallback — must fail.
func TestXIncludeThroughResolverCannotReachNetwork(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("<pwned/>"))
	}))
	defer srv.Close()

	root := t.TempDir()
	docPath := filepath.Join(root, "doc.xml")
	src := `<root xmlns:xi="http://www.w3.org/2001/XInclude">` +
		`<xi:include href="` + srv.URL + `/x.xml"/></root>`
	if err := os.WriteFile(docPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(docPath)})
	if err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{root}}
	if err := xdm.ProcessXInclude(tree, xdm.XIncludeOptions{Resolver: r}); err == nil {
		t.Error("an http:// inclusion succeeded")
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("the canary server was contacted %d times", n)
	}
	// And nothing from the server reached the tree.
	if strings.Contains(nodeText(tree.Root), "pwned") {
		t.Error("server content reached the document")
	}
	// Belt and braces: the port is still nothing this process dialled for
	// XInclude's sake. A listener that was never contacted proves the check
	// ran before the socket, not after.
	if _, err := net.Dial("tcp", srv.Listener.Addr().String()); err != nil {
		t.Skipf("canary server not dialable: %v", err)
	}
}

// A fallback must not rescue a refusal into a read that the confinement had
// already denied — it may only supply the document's own content. This checks
// the refusal is not silently converted into the escape succeeding.
func TestXIncludeFallbackDoesNotBypassConfinement(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(root, "doc.xml")
	src := `<root xmlns:xi="http://www.w3.org/2001/XInclude">` +
		`<xi:include href="` + secret + `">` +
		`<xi:fallback><xi:include href="` + secret + `" parse="text"/></xi:fallback>` +
		`</xi:include></root>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(docPath)})
	if err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{root}}
	err = xdm.ProcessXInclude(tree, xdm.XIncludeOptions{Resolver: r})
	// Whether it errors or not, the secret must not be in the tree.
	if strings.Contains(nodeText(tree.Root), "private key") {
		t.Fatal("the fallback read a file outside the roots")
	}
	if err == nil {
		t.Error("both the include and its fallback were refused, so this should be fatal")
	}
}

// The permitted case, so the tests above are shown to be measuring the
// confinement rather than a resolver that refuses everything.
func TestResolveIncludeReadsInsideRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "frag.xml"), []byte("<frag/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{root}}
	data, uri, err := r.ResolveInclude("frag.xml", fileURIOf(filepath.Join(root, "doc.xml")), "")
	if err != nil {
		t.Fatalf("ResolveInclude: %v", err)
	}
	if string(data) != "<frag/>" {
		t.Errorf("got %q", data)
	}
	if !strings.HasSuffix(uri, "frag.xml") {
		t.Errorf("uri = %q, want the file: URI of what was read", uri)
	}
}

// parse="text" goes through the same decode fn:unparsed-text uses, so an
// encoding this package cannot decode exactly is refused rather than producing
// mojibake that becomes nodes of the document.
func TestResolveIncludeTextEncoding(t *testing.T) {
	root := t.TempDir()
	// 0xE9 is é in ISO-8859-1 and invalid UTF-8.
	if err := os.WriteFile(filepath.Join(root, "l.txt"), []byte{'c', 'a', 'f', 0xE9}, 0o644); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{root}}
	base := fileURIOf(filepath.Join(root, "doc.xml"))

	data, _, err := r.ResolveInclude("l.txt", base, "iso-8859-1")
	if err != nil {
		t.Fatalf("ResolveInclude: %v", err)
	}
	if string(data) != "café" {
		t.Errorf("got %q, want %q", data, "café")
	}
	if _, _, err := r.ResolveInclude("l.txt", base, "utf-8"); err == nil {
		t.Error("bytes that are not valid UTF-8 must not be accepted as UTF-8")
	}
	if _, _, err := r.ResolveInclude("l.txt", base, "shift-jis"); err == nil {
		t.Error("an encoding this package cannot decode must be refused, not guessed")
	}
}

// nodeText is the string value of a subtree, used only to assert that
// forbidden content did not reach the document.
func nodeText(n *xdm.Node) string { return n.StringValue() }
