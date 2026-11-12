// Package xmlconf runs the W3C XML Conformance Test Suite (xmlts20130923)
// through xdm.Parse and dtd.Validate.
package xmlconf

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/dtd"
	"github.com/knroy/go-xml/v2/internal/fileuri"
	"github.com/knroy/go-xml/v2/xdm"
)

// testCase is one <TEST> of the catalog.
type testCase struct {
	ID, Type, URI, Rec, Edition, Namespace, Version, Entities, Sections string
	file                                                                string // the case's path on disk
}

// TestXMLConf runs every in-scope case and logs "in-scope: N passed", which
// tests/check.sh reads for the TestXMLConf ratchet mark. The count alone
// cannot see one case start failing while another starts passing, so
// expected-pass.txt lists, by ID, every case that passes: a listed case that
// does not pass fails the test by name, and a passing case not listed is
// logged as newly passing. GOXML_XMLCONF_UPDATE=1 rewrites the list from the
// run instead; review its diff before committing it. Unlisted failing cases
// do not fail the test.
//
// The suite is fetched rather than committed (tests/fetch-xmlconf.sh, pinned
// by tests/xmlconf-corpus.txt). Absent, the test skips, unless
// GOXML_XMLCONF=1, which tests/check.sh sets after fetching, makes absence a
// failure.
//
// What a case asks, and what passes:
//
//   - not-wf: xdm.Parse must refuse it. Well-formedness is the parser's job,
//     so dtd.Load is not consulted.
//   - valid: xdm.Parse must accept it, and dtd.Load plus dtd.Validate must
//     report nothing.
//   - invalid: xdm.Parse must accept it (an invalid document is well formed),
//     and dtd.Load or dtd.Validate must report an error. A document with no
//     DOCTYPE is invalid by XML 1.0 §2.8 ("an XML document is valid if it
//     has an associated document type declaration"), so that counts too.
//
// Both parsers are given a resolver confined to the suite, so external
// entities and external subsets are read: the cases that need them
// (ENTITIES="general", "parameter" or "both") are in scope.
//
// Out of scope, with the reason logged: TYPE="error" (an error a processor
// may, but need not, report: XML 1.0 §1.2 "error"); XML 1.1 and Namespaces
// 1.1 recommendations (X6: the 1.1 character layer only); EDITION lists that
// omit 5 (X7: Fifth Edition names only); NAMESPACE="no" (cases correct only
// for a processor that is not namespace-aware, which xdm is not).
func TestXMLConf(t *testing.T) {
	if os.Getenv("GOXSLT_NO_SUITES") != "" {
		t.Skip("GOXSLT_NO_SUITES is set")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "xmlconf"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := loadCatalog(root)
	if err != nil {
		if os.Getenv("GOXML_XMLCONF") == "1" {
			t.Fatalf("%v (run tests/fetch-xmlconf.sh)", err)
		}
		t.Skipf("xmlconf absent; run tests/fetch-xmlconf.sh (%v)", err)
	}

	skipped := map[string]int{}
	failures := map[string][]string{} // by TYPE
	passed, failed := 0, 0
	passing := map[string]bool{}
	reasons := map[string]string{} // why a case failed, by ID
	for _, c := range cases {
		if why := outOfScope(c); why != "" {
			skipped[why]++
			continue
		}
		if msg := run(root, c); msg == "" {
			passed++
			passing[c.ID] = true
		} else {
			failed++
			reasons[c.ID] = msg
			failures[c.Type] = append(failures[c.Type], c.ID+" ["+c.Sections+"]: "+msg)
		}
	}
	inScope := passed + failed
	if inScope == 0 {
		t.Fatal("no case ran, which means the catalog reader or the filter is wrong")
	}
	t.Logf("xmlconf: %d cases, %d in scope, %d skipped", len(cases), inScope, len(cases)-inScope)
	whys := make([]string, 0, len(skipped))
	for why := range skipped {
		whys = append(whys, why)
	}
	sort.Strings(whys)
	for _, why := range whys {
		t.Logf("  skipped %4d  %s", skipped[why], why)
	}
	t.Logf("in-scope: %d passed, %d failed (%.2f%%)",
		passed, failed, 100*float64(passed)/float64(inScope))
	types := make([]string, 0, len(failures))
	for k := range failures {
		types = append(types, k)
	}
	sort.Strings(types)
	for _, k := range types {
		t.Logf("failing %s: %d", k, len(failures[k]))
		if testing.Verbose() {
			for _, f := range failures[k] {
				t.Logf("  %s", f)
			}
		}
	}
	checkExpected(t, passing, reasons)
}

const expectedFile = "expected-pass.txt"

// checkExpected compares the passing IDs with expected-pass.txt, or rewrites
// it when GOXML_XMLCONF_UPDATE=1.
func checkExpected(t *testing.T, passing map[string]bool, reasons map[string]string) {
	ids := make([]string, 0, len(passing))
	for id := range passing {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if os.Getenv("GOXML_XMLCONF_UPDATE") == "1" {
		if err := os.WriteFile(expectedFile, []byte(strings.Join(ids, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d IDs to %s; review the diff before committing it", len(ids), expectedFile)
		return
	}
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("%v (run with GOXML_XMLCONF_UPDATE=1 to create it)", err)
	}
	want := map[string]bool{}
	for _, id := range strings.Fields(string(data)) {
		want[id] = true
		if !passing[id] {
			why := reasons[id]
			if why == "" {
				why = "not run (absent from the catalog or out of scope)"
			}
			t.Errorf("regressed: %s: %s", id, why)
		}
	}
	for _, id := range ids {
		if !want[id] {
			t.Logf("newly passing: %s", id)
		}
	}
}

func outOfScope(c testCase) string {
	switch {
	case c.Type == "error":
		return `TYPE="error": optional for a processor to report`
	case strings.HasPrefix(c.Rec, "XML1.1") || strings.HasPrefix(c.Rec, "NS1.1") || c.Version == "1.1":
		return "XML 1.1 / Namespaces 1.1 (X6: partial 1.1)"
	case c.Edition != "" && !strings.Contains(c.Edition, "5"):
		return "edition other than the Fifth (X7)"
	case c.Namespace == "no":
		return `NAMESPACE="no": not for a namespace-aware processor`
	}
	return ""
}

// run returns "" for a pass and a short reason for a failure.
func run(root string, c testCase) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprintf("panic: %v", r)
		}
	}()
	src, err := os.ReadFile(c.file)
	if err != nil {
		return err.Error()
	}
	base := fileuri.Of(c.file)
	res := &suiteResolver{dtd.FileResolver{Root: root}}
	tree, perr := xdm.Parse(strings.NewReader(string(src)), xdm.ParseOptions{
		AllowDOCTYPE:     true,
		ExternalEntities: res,
		BaseURI:          base,
	})
	switch c.Type {
	case "not-wf":
		if perr == nil {
			return "accepted"
		}
		return ""
	}
	if perr != nil {
		return "refused: " + clip(perr.Error())
	}
	d, lerr := dtd.Load(tree.DocType, dtd.LoadOptions{Resolver: &res.r, BaseURI: base})
	var verr error
	if lerr == nil && d != nil {
		verr = dtd.Validate(tree.Root, d, dtd.Options{})
	}
	switch c.Type {
	case "valid":
		if lerr != nil {
			return "dtd.Load: " + clip(lerr.Error())
		}
		if verr != nil {
			return "dtd.Validate: " + clip(verr.Error())
		}
	case "invalid":
		if lerr == nil && d != nil && verr == nil {
			return "no validity error"
		}
	}
	return ""
}

func clip(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

// suiteResolver reads external entities for xdm through dtd.FileResolver, so
// the two parsers share one confinement to the suite directory.
type suiteResolver struct{ r dtd.FileResolver }

func (s *suiteResolver) ResolveEntity(systemID, publicID, base string) (io.ReadCloser, string, error) {
	return s.r.ResolveExternal(systemID, publicID, base)
}

var (
	entityDecl = regexp.MustCompile(`<!ENTITY\s+(\S+)\s+SYSTEM\s+"([^"]+)"\s*>`)
	testcases  = regexp.MustCompile(`(?s)<TESTCASES[^>]*>(.*?)</TESTCASES>`)
	entityRef  = regexp.MustCompile(`&([^;\s]+);`)
)

// loadCatalog reads xmlconf.xml. Its sub-catalogs are external entities,
// referenced inside <TESTCASES xml:base="..."> wrappers, so the master file is
// read textually and each sub-catalog with encoding/xml: the harness does not
// use the parser it measures to find out what to measure.
func loadCatalog(root string) ([]testCase, error) {
	master, err := os.ReadFile(filepath.Join(root, "xmlconf.xml"))
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, m := range entityDecl.FindAllStringSubmatch(string(master), -1) {
		files[m[1]] = m[2]
	}
	var out []testCase
	for _, m := range testcases.FindAllStringSubmatch(string(master), -1) {
		for _, r := range entityRef.FindAllStringSubmatch(m[1], -1) {
			f, ok := files[r[1]]
			if !ok {
				return nil, fmt.Errorf("xmlconf.xml: undeclared entity %q", r[1])
			}
			// Each case URI is relative to its sub-catalog's directory.
			// The wrapper's xml:base says the same for every catalog but
			// one: eduni-misc's names eduni/namespaces/misc/, which does
			// not exist, while the catalog and its cases are in
			// eduni/misc/. The directory is therefore taken from the file.
			cs, err := readSubCatalog(root, filepath.FromSlash(f), path.Dir(f)+"/")
			if err != nil {
				return nil, err
			}
			out = append(out, cs...)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("xmlconf.xml: no test cases found")
	}
	return out, nil
}

func readSubCatalog(root, file, base string) ([]testCase, error) {
	f, err := os.Open(filepath.Join(root, file))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	bases := []string{base}
	var out []testCase
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			b := bases[len(bases)-1]
			if v := attr(e, "base"); v != "" {
				b = resolveRef(b, v)
			}
			bases = append(bases, b)
			if e.Name.Local != "TEST" {
				continue
			}
			c := testCase{
				ID: attr(e, "ID"), Type: attr(e, "TYPE"), URI: attr(e, "URI"),
				Rec: attr(e, "RECOMMENDATION"), Edition: attr(e, "EDITION"),
				Namespace: attr(e, "NAMESPACE"), Version: attr(e, "VERSION"),
				Entities: attr(e, "ENTITIES"), Sections: attr(e, "SECTIONS"),
			}
			c.file = filepath.Join(root, filepath.FromSlash(resolveRef(b, c.URI)))
			out = append(out, c)
		case xml.EndElement:
			bases = bases[:len(bases)-1]
		}
	}
}

func attr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// resolveRef resolves a relative reference against a slash-separated base
// relative to the suite root.
func resolveRef(base, ref string) string {
	u, err := url.Parse(ref)
	if err != nil || u.IsAbs() {
		return ref
	}
	if strings.HasSuffix(base, "/") || base == "" {
		return path.Clean(base + ref)
	}
	return path.Clean(path.Join(path.Dir(base), ref))
}
