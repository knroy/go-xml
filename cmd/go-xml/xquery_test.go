package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The xquery subcommand is driven through runXQuery, the entry point main
// really dispatches to, with every path built by filepath under t.TempDir so
// that the same cases run unchanged on Windows, where the query's base URI is
// file:///C:/... and a relative "at" or doc() must still resolve beside it.

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runQuery runs the subcommand with -o into the temp directory and returns
// what it wrote.
func runQuery(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	out := filepath.Join(dir, "out.txt")
	if err := runXQuery(append([]string{"-o", out}, args...)); err != nil {
		return "", err
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(got), nil
}

func TestXQueryOverInputDocument(t *testing.T) {
	dir := t.TempDir()
	q := filepath.Join(dir, "q", "count.xq")
	in := filepath.Join(dir, "in.xml")
	writeFile(t, q, `<n>{ count(//b) }</n>`)
	writeFile(t, in, `<r><b/><b/></r>`)

	got, err := runQuery(t, dir, "-q", q, in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<n>2</n>") {
		t.Errorf("output = %q, want <n>2</n>", got)
	}
}

// With no input there is no context item: a query that needs none runs, and
// its prolog's output declarations, external variables and -now are honoured.
func TestXQueryWithoutInput(t *testing.T) {
	dir := t.TempDir()
	q := filepath.Join(dir, "q.xq")
	writeFile(t, q, `
declare namespace output = "http://www.w3.org/2010/xslt-xquery-serialization";
declare option output:method "json";
declare variable $who external;
map { "who": $who, "day": string(current-date()) }`)

	got, err := runQuery(t, dir, "-q", q, "-p", "who=me",
		"-now", "2024-01-15T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"who":"me","day":"2024-01-15Z"}`; strings.TrimSpace(got) != want {
		t.Errorf("output = %q, want %q", got, want)
	}

	// A query that does need a context item says so rather than inventing one.
	path := filepath.Join(dir, "path.xq")
	writeFile(t, path, `//b`)
	if _, err := runQuery(t, dir, "-q", path); err == nil ||
		!strings.Contains(err.Error(), "XPDY0002") {
		t.Errorf("err = %v, want XPDY0002", err)
	}
}

func TestXQueryStaticErrorCarriesItsCode(t *testing.T) {
	dir := t.TempDir()
	q := filepath.Join(dir, "bad.xq")
	writeFile(t, q, `for $x in`)
	_, err := runQuery(t, dir, "-q", q)
	if err == nil || !strings.Contains(err.Error(), "XPST0003") {
		t.Errorf("err = %v, want XPST0003", err)
	}
}

// import module and fn:doc read beside the query with no flag, and nothing
// outside it until -allow-dir names the directory.
func TestXQueryReadsAreConfinedToAllowDir(t *testing.T) {
	dir := t.TempDir()
	qdir := filepath.Join(dir, "q")
	writeFile(t, filepath.Join(qdir, "near.xqm"),
		`module namespace u = "urn:u"; declare function u:d($x) { $x * 2 };`)
	writeFile(t, filepath.Join(dir, "far.xqm"),
		`module namespace f = "urn:f"; declare function f:d($x) { $x * 3 };`)
	writeFile(t, filepath.Join(dir, "far.xml"), `<far/>`)

	near := filepath.Join(qdir, "near.xq")
	writeFile(t, near, `import module namespace u = "urn:u" at "near.xqm"; u:d(21)`)
	got, err := runQuery(t, dir, "-q", near)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "42") {
		t.Errorf("output = %q, want 42", got)
	}

	cases := map[string]string{
		"module.xq": `import module namespace f = "urn:f" at "../far.xqm"; f:d(2)`,
		"doc.xq":    `doc("../far.xml")`,
	}
	for name, body := range cases {
		q := filepath.Join(qdir, name)
		writeFile(t, q, body)
		_, err := runQuery(t, dir, "-q", q)
		if err == nil || !strings.Contains(err.Error(), "outside the permitted directories") {
			t.Errorf("%s: err = %v, want a confinement refusal", name, err)
		}
		if _, err := runQuery(t, dir, "-q", q, "-allow-dir", dir); err != nil {
			t.Errorf("%s with -allow-dir: %v", name, err)
		}
	}
}

func TestXQueryRejectsSecondInput(t *testing.T) {
	dir := t.TempDir()
	q := filepath.Join(dir, "q.xq")
	writeFile(t, q, `1`)
	if err := runXQuery([]string{"-q", q, "a.xml", "b.xml"}); err == nil {
		t.Error("two inputs were accepted; only one can be the context item")
	}
}

// fn:load-xquery-module reads its module through the same confined resolver as
// import module, from a query and from a stylesheet alike: a hint beside the
// caller is read, one outside the roots is refused until -allow-dir names it.
func TestLoadXQueryModuleIsConfinedToAllowDir(t *testing.T) {
	dir := t.TempDir()
	qdir := filepath.Join(dir, "q")
	writeFile(t, filepath.Join(qdir, "near.xqm"),
		`module namespace u = "urn:u"; declare function u:d($x) { $x * 2 };`)
	writeFile(t, filepath.Join(dir, "far.xqm"),
		`module namespace u = "urn:u"; declare function u:d($x) { $x * 3 };`)
	call := func(hint string) string {
		return `load-xquery-module("urn:u", map{"location-hints": "` + hint +
			`"})("functions")(QName("urn:u", "d"))(1)(21)`
	}

	q := filepath.Join(qdir, "near.xq")
	writeFile(t, q, call("near.xqm"))
	if got, err := runQuery(t, dir, "-q", q); err != nil || !strings.Contains(got, "42") {
		t.Errorf("query, module beside it: %q, %v; want 42", got, err)
	}
	q = filepath.Join(qdir, "far.xq")
	writeFile(t, q, call("../far.xqm"))
	if _, err := runQuery(t, dir, "-q", q); err == nil ||
		!strings.Contains(err.Error(), "outside the permitted directories") {
		t.Errorf("query, module outside the roots: err = %v, want a confinement refusal", err)
	}
	if got, err := runQuery(t, dir, "-q", q, "-allow-dir", dir); err != nil || !strings.Contains(got, "63") {
		t.Errorf("query with -allow-dir: %q, %v; want 63", got, err)
	}

	bin := buildCLI(t)
	sheet := func(hint string) string {
		xsl := filepath.Join(qdir, "s.xsl")
		writeFile(t, xsl, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:template name="xsl:initial-template"><n><xsl:value-of select='`+call(hint)+`'/></n></xsl:template>
</xsl:stylesheet>`)
		return xsl
	}
	out, err := exec.Command(bin, "-xsl", sheet("near.xqm"),
		"-initial-template", initialTemplate).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "<n>42</n>") {
		t.Errorf("stylesheet, module beside it: %v\n%s", err, out)
	}
	out, err = exec.Command(bin, "-xsl", sheet("../far.xqm"),
		"-initial-template", initialTemplate).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "FOQM0002") {
		t.Errorf("stylesheet, module outside the roots was read: %v\n%s", err, out)
	}
}

// fn:transform from a query reads its stylesheet and source through the
// query's confined resolver, exactly as fn:doc does: beside the query with no
// flag, and outside it only once -allow-dir names the directory.
func TestXQueryFnTransformIsConfinedToAllowDir(t *testing.T) {
	dir := t.TempDir()
	qdir := filepath.Join(dir, "q")
	sheet := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
  <xsl:template match="/"><got><xsl:value-of select="/r"/></got></xsl:template>
</xsl:stylesheet>`
	writeFile(t, filepath.Join(qdir, "s.xsl"), sheet)
	writeFile(t, filepath.Join(qdir, "in.xml"), `<r>near</r>`)
	writeFile(t, filepath.Join(dir, "far.xsl"), sheet)
	writeFile(t, filepath.Join(dir, "far.xml"), `<r>far</r>`)
	query := func(name, xsl, src string) string {
		q := filepath.Join(qdir, name)
		writeFile(t, q, `transform(map{'stylesheet-location': '`+xsl+
			`', 'source-location': '`+src+`'})?output`)
		return q
	}

	got, err := runQuery(t, dir, "-q", query("near.xq", "s.xsl", "in.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<got>near</got>") {
		t.Errorf("output = %q, want <got>near</got>", got)
	}

	for name, q := range map[string]string{
		"stylesheet-location": query("xsl.xq", "../far.xsl", "in.xml"),
		"source-location":     query("src.xq", "s.xsl", "../far.xml"),
	} {
		_, err := runQuery(t, dir, "-q", q)
		if err == nil || !strings.Contains(err.Error(), "FOXT0002") ||
			!strings.Contains(err.Error(), "outside the permitted directories") {
			t.Errorf("%s outside the roots: err = %v, want a FOXT0002 confinement refusal", name, err)
		}
		if _, err := runQuery(t, dir, "-q", q, "-allow-dir", dir); err != nil {
			t.Errorf("%s with -allow-dir: %v", name, err)
		}
	}
}
