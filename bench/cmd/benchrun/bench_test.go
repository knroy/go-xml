package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSummarise(t *testing.T) {
	got := summarise([]int64{50, 10, 40, 20, 30})
	want := Stats{Median: 30, P25: 20, P75: 40, Min: 10}
	if got != want {
		t.Errorf("odd count: got %+v, want %+v", got, want)
	}
	// Even count interpolates: ranks 1.5, 0.75 and 2.25 of 10 20 30 40.
	got = summarise([]int64{40, 10, 30, 20})
	want = Stats{Median: 25, P25: 18, P75: 33, Min: 10}
	if got != want {
		t.Errorf("even count: got %+v, want %+v", got, want)
	}
	if got := summarise([]int64{7}); got != (Stats{7, 7, 7, 7}) {
		t.Errorf("one value: got %+v", got)
	}
	if got := summarise(nil); got != (Stats{}) {
		t.Errorf("empty: got %+v", got)
	}
	in := []int64{3, 1, 2}
	summarise(in)
	if !reflect.DeepEqual(in, []int64{3, 1, 2}) {
		t.Error("summarise reordered its input")
	}
}

func TestMaxrssBytes(t *testing.T) {
	for _, c := range []struct {
		goos string
		in   int64
		want int64
	}{
		{"darwin", 15417344, 15417344},
		{"linux", 15056, 15056 * 1024},
		{"freebsd", 2, 2048},
	} {
		if got := maxrssBytes(c.goos, c.in); got != c.want {
			t.Errorf("%s: got %d, want %d", c.goos, got, c.want)
		}
	}
}

func TestNormalise(t *testing.T) {
	for _, c := range []struct {
		rule, a, b string
		equal      bool
	}{
		{"none", "<a/>", "<a/>", true},
		{"none", "<a/>", "<a/>\n", false},
		{"whitespace", " x \n\t y ", "x y", true},
		{"whitespace", "xy", "x y", false},
		{"text", `<?xml version="1.0"?><a>x <b>y</b></a>`, "<r>x y</r>", true},
		{"text", "1 2 3", " 1  2 3\n", true},
		{"xml-c14n", `<?xml version="1.0" encoding="UTF-8"?><a y='2' x="1"><b/></a>`,
			"<?xml version=\"1.0\"?>\n<a x=\"1\" y=\"2\"><b></b></a>\n", true},
		{"xml-c14n", "<a><b/></a>", "<a> <b/></a>", false},
	} {
		a, err := normalise(c.rule, []byte(c.a))
		if err != nil {
			t.Fatalf("%s %q: %v", c.rule, c.a, err)
		}
		b, err := normalise(c.rule, []byte(c.b))
		if err != nil {
			t.Fatalf("%s %q: %v", c.rule, c.b, err)
		}
		if (string(a) == string(b)) != c.equal {
			t.Errorf("%s: %q vs %q: equal=%v, want %v", c.rule, a, b, !c.equal, c.equal)
		}
	}
	if _, err := normalise("xml-c14n", []byte("<a>")); err == nil {
		t.Error("xml-c14n accepted malformed output")
	}
}

func TestAgree(t *testing.T) {
	ok := func(out string) Outcome { return Outcome{Output: []byte(out)} }
	fail := func(log string) Outcome { return Outcome{Failed: true, Log: log} }
	for _, c := range []struct {
		name    string
		area    string
		gx, ref Outcome
		want    bool
	}{
		{"same output", "xslt", ok("<a/>"), ok("<a></a>"), true},
		{"different output", "xslt", ok("<a/>"), ok("<b/>"), false},
		{"both fail, no codes", "xslt", fail("boom"), fail("bang"), true},
		{"both fail, same code", "xquery", fail("err:XPTY0004 x"), fail("XPTY0004: y"), true},
		{"both fail, different codes", "xquery", fail("XPTY0004"), fail("FORG0001"), false},
		{"only go-xml fails", "xslt", fail("x"), ok("<a/>"), false},
		{"only reference fails", "xslt", ok("<a/>"), fail("x"), false},
		{"same verdict", "xsd", Outcome{Verdict: "invalid"}, Outcome{Verdict: "invalid"}, true},
		{"different verdict", "rng", Outcome{Verdict: "valid"}, Outcome{Verdict: "invalid"}, false},
	} {
		if got, note := agree(c.area, "xml-c14n", c.gx, c.ref); got != c.want {
			t.Errorf("%s: agree=%v (%s), want %v", c.name, got, note, c.want)
		}
	}
}

func TestVerdict(t *testing.T) {
	for _, c := range []struct {
		exitOK  bool
		log     string
		pattern string
		want    string
	}{
		{true, "", `\[Error\]`, "valid"},
		{true, "[Error] x.xml:3", `\[Error\]`, "invalid"}, // Xerces exits 0 on an invalid document
		{false, "x.xml: INVALID\n", `(?m): INVALID$`, "invalid"},
		{false, "loading schema: boom", `(?m): INVALID$`, "error"},
	} {
		if got := verdict(c.exitOK, c.log, c.pattern); got != c.want {
			t.Errorf("verdict(%v, %q): got %s, want %s", c.exitOK, c.log, got, c.want)
		}
	}
}

func TestExpandArgv(t *testing.T) {
	tmpl := []string{"java", "-s:{source}", "-xsl:{stylesheet}", "{args}", "{params}", "-o:{out}"}
	got := expandArgv(tmpl, map[string]string{"source": "", "stylesheet": "s.xsl", "out": "o"},
		map[string]string{"b": "2", "a": "1"}, []string{"-p", "{name}={value}"}, []string{"-x"})
	want := []string{"java", "-xsl:s.xsl", "-x", "-p", "a=1", "-p", "b=2", "-o:o"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseWorkload(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.xml", "b.xml", "c.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte("<x/>"), 0o644)
	}
	w, err := parseWorkload([]byte(`{"id":"w","area":"xslt","engines":["go-xml"],
		"items":[{"name":"g","stylesheet":"s.xsl","glob":"*.xml"},
		         {"area":"parse","source":"sub/d.xml"}]}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	var names, srcs, areas []string
	for _, it := range w.Items {
		names = append(names, it.Name)
		srcs = append(srcs, it.Source)
		areas = append(areas, it.Area)
	}
	if want := []string{"g/a.xml", "g/b.xml", "d.xml"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names %q, want %q", names, want)
	}
	if want := []string{"a.xml", "b.xml", "sub/d.xml"}; !reflect.DeepEqual(srcs, want) {
		t.Errorf("sources %q, want %q", srcs, want)
	}
	if want := []string{"xslt", "xslt", "parse"}; !reflect.DeepEqual(areas, want) {
		t.Errorf("areas %q, want %q", areas, want)
	}
	if w.XSDVersion != "1.1" || !reflect.DeepEqual(w.Modes, []string{"cold", "warm"}) {
		t.Errorf("defaults: xsd_version %q, modes %q", w.XSDVersion, w.Modes)
	}

	for name, bad := range map[string]string{
		"unknown field":   `{"id":"w","area":"xslt","engines":["go-xml"],"itmes":[]}`,
		"no id":           `{"area":"xslt","engines":["go-xml"]}`,
		"no go-xml":       `{"id":"w","area":"xslt","engines":["saxon-he"]}`,
		"bad area":        `{"id":"w","area":"xpath","engines":["go-xml"],"items":[{"source":"a.xml"}]}`,
		"bad normalise":   `{"id":"w","area":"xslt","normalise":"c14n","engines":["go-xml"]}`,
		"bad mode":        `{"id":"w","area":"xslt","modes":["hot"],"engines":["go-xml"]}`,
		"glob no matches": `{"id":"w","area":"parse","engines":["go-xml"],"items":[{"glob":"*.json"}]}`,
	} {
		if _, err := parseWorkload([]byte(bad), dir); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The committed workloads must parse; a malformed one would only show up
// when someone ran the benchmark.
func TestCommittedWorkloadsParse(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	files, _ := filepath.Glob(filepath.Join(root, "bench", "workloads", "*.json"))
	if len(files) == 0 {
		t.Fatal("no workloads found")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseWorkload(data, root); err != nil {
			// A glob over testdata/bench may legitimately match nothing
			// before fetch-inputs.sh has run.
			if strings.Contains(err.Error(), "matches nothing") {
				t.Logf("%s: %v", f, err)
				continue
			}
			t.Errorf("%s: %v", f, err)
		}
	}
}

// engines.json and fetch-engines.sh carry the same pins.
func TestPinsMatchFetchScript(t *testing.T) {
	engines, err := loadEngines(filepath.Join("..", "..", "engines.json"))
	if err != nil {
		t.Fatal(err)
	}
	fromJSON := map[string]string{}
	for _, e := range engines {
		for _, f := range e.Files {
			fromJSON[f.Path] = f.SHA256 + " " + f.URL
		}
	}
	sh, err := os.Open(filepath.Join("..", "..", "fetch-engines.sh"))
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	fromScript := map[string]string{}
	sc := bufio.NewScanner(sh)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 4 && f[0] == "fetch" {
			fromScript[f[1]] = f[2] + " " + strings.Replace(f[3], "$M", "https://repo1.maven.org/maven2", 1)
		}
	}
	if len(fromJSON) == 0 {
		t.Fatal("no pinned files in engines.json")
	}
	if !reflect.DeepEqual(fromJSON, fromScript) {
		a, _ := json.MarshalIndent(fromJSON, "", " ")
		b, _ := json.MarshalIndent(fromScript, "", " ")
		t.Errorf("pins differ\nengines.json: %s\nfetch-engines.sh: %s", a, b)
	}
}

func TestParseLoopOutput(t *testing.T) {
	res, err := parseLoopOutput([]byte("COMPILE 123\nRESULT 0 10\nRESULT 1 20\nRESULT 0 30\n"),
		[]byte("ERROR 1 boom here\n"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.compileNs != 123 || !reflect.DeepEqual(res.ns, [][]int64{{10, 30}, {20}}) || res.errs[1] != "boom here" {
		t.Errorf("got %+v", res)
	}
	if _, err := parseLoopOutput([]byte("RESULT 5 1\n"), nil, 2); err == nil {
		t.Error("out-of-range item index accepted")
	}
}

// The encoding/xml baseline's re-emitted XML must canonicalise to the same
// bytes as go-xml's own parse, or the parse check would always disagree.
func TestStdlibParseAgrees(t *testing.T) {
	doc := `<?xml version="1.0"?>
<r xmlns:p="urn:p" b="2" a="&lt;&quot;">t &amp; u<p:e p:x="1"/><?pi data?><!-- c --></r>`
	var gx, std strings.Builder
	if err := goxmlParse([]byte(doc), &gx); err != nil {
		t.Fatal(err)
	}
	if err := stdlibParse([]byte(doc), &std); err != nil {
		t.Fatal(err)
	}
	a, _ := normalise("xml-c14n", []byte(gx.String()))
	b, err := normalise("xml-c14n", []byte(std.String()))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("go-xml %q\nencoding/xml %q", a, b)
	}
}
