package xmltok_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/knroy/go-xml/internal/xmlfork"
	"github.com/knroy/go-xml/internal/xmltok"
)

// xmltok replaces internal/xmlfork as the tokeniser under xdm, and the switch
// is gated on this file: every document the repository can reach is
// tokenised by both, configured as xdm configures its decoder, and the two
// must agree on every token (type and every field, byte-exact), on
// InputOffset and IsVersion11 before every RawToken call, and on how the
// stream ends — EOF, or an error of the same kind on the same line at the
// same token. Error messages are counted when they differ but do not fail.
//
// Only the API xmltok shares with xmlfork's subset is used, so the harness
// cannot pass by reaching into either implementation.

// side is one tokeniser, reduced to what the harness compares. The token
// types of the two packages are distinct, so each is wrapped in closures
// rather than an interface.
type side struct {
	next      func() (any, error)
	offset    func() int64
	v11       func() bool
	setEntity func(map[string]string)
}

func forkSide(r io.Reader) side {
	d := xmlfork.NewDecoder(r)
	d.Strict = true
	d.CharsetReader = charsetReader
	return side{
		next:      func() (any, error) { return d.RawToken() },
		offset:    d.InputOffset,
		v11:       d.IsVersion11,
		setEntity: func(m map[string]string) { d.Entity = m },
	}
}

func tokSide(r io.Reader) side {
	d := xmltok.NewDecoder(r)
	d.Strict = true
	d.CharsetReader = charsetReader
	return side{
		next:      func() (any, error) { return d.RawToken() },
		offset:    d.InputOffset,
		v11:       d.IsVersion11,
		setEntity: func(m map[string]string) { d.Entity = m },
	}
}

// charsetReader is xdm/parse.go's policy: US-ASCII is checked to be seven-bit
// and passed through, ISO-8859-1 is widened byte to code point, and every
// other declared encoding is refused.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	b, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(charset) {
	case "us-ascii", "ascii", "iso-646", "us_ascii":
		for i, c := range b {
			if c > 0x7f {
				return nil, fmt.Errorf("declared encoding %s but byte %d at offset %d is not ASCII", charset, c, i)
			}
		}
		return bytes.NewReader(b), nil
	case "iso-8859-1", "latin1", "iso8859-1", "iso_8859-1":
		var out bytes.Buffer
		for _, c := range b {
			out.WriteRune(rune(c))
		}
		return bytes.NewReader(out.Bytes()), nil
	}
	return nil, fmt.Errorf("unsupported encoding %q", charset)
}

// xdmInput is what xdm's decodeReader hands the tokeniser: a UTF-8 BOM is
// dropped, and a UTF-16 document (BOM-marked) is decoded to UTF-8 with its
// encoding declaration removed. BOM-less UTF-16, which xdm also sniffs, is
// passed through and both sides refuse it identically.
func xdmInput(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case len(b)%2 == 0 && (bytes.HasPrefix(b, []byte{0xFE, 0xFF}) || bytes.HasPrefix(b, []byte{0xFF, 0xFE})):
		be := b[0] == 0xFE
		u := make([]uint16, len(b)/2-1)
		for i := range u {
			hi, lo := b[2+2*i], b[3+2*i]
			if !be {
				hi, lo = lo, hi
			}
			u[i] = uint16(hi)<<8 | uint16(lo)
		}
		s := string(utf16.Decode(u))
		if strings.HasPrefix(s, "<?xml") {
			if end := strings.Index(s, "?>"); end > 0 {
				s = encodingDecl.ReplaceAllString(s[:end], "") + s[end:]
			}
		}
		return []byte(s)
	}
	return b
}

var encodingDecl = regexp.MustCompile(`\s+encoding\s*=\s*("[^"]*"|'[^']*')`)

// internalEntity matches a general entity declared with a literal value. xdm
// installs internal-subset entities into Decoder.Entity when the DOCTYPE
// token arrives (xdm/parse.go, the xml.Directive case); this is the simplest
// faithful version of that. Parameter entities, external entities and values
// holding markup are left out because xdm never routes those through Entity.
// The values are passed verbatim rather than with xdm's &amp; decoding: both
// sides receive the same map, and the map is opaque text to a tokeniser.
var internalEntity = regexp.MustCompile(`<!ENTITY\s+([^\s%][^\s]*)\s+("([^"]*)"|'([^']*)')\s*>`)

func doctypeEntities(dir []byte) map[string]string {
	d := strings.TrimSpace(string(dir))
	if !strings.HasPrefix(d, "DOCTYPE") {
		return nil
	}
	var m map[string]string
	for _, g := range internalEntity.FindAllStringSubmatch(d, -1) {
		v := g[3] + g[4]
		if strings.Contains(v, "<") {
			continue
		}
		if m == nil {
			m = map[string]string{}
		}
		if _, dup := m[g[1]]; !dup { // first declaration binds, XML §4.2
			m[g[1]] = v
		}
	}
	return m
}

// canon renders a token with every field, byte slices as hex. The package
// qualifier is dropped so the two packages' types compare equal.
func canon(tok any) string {
	s := fmt.Sprintf("%#v", tok)
	return strings.NewReplacer("xmlfork.", "", "xmltok.", "").Replace(s)
}

// ending is how a stream stopped. Msg is kept apart because it may differ.
type ending struct {
	kind string // "EOF", "SyntaxError", "panic" or the error's type
	line int
	msg  string
}

func endOf(err error) ending {
	var a *xmlfork.SyntaxError
	var b *xmltok.SyntaxError
	switch {
	case err == io.EOF:
		return ending{kind: "EOF"}
	case errors.As(err, &a):
		return ending{"SyntaxError", a.Line, a.Msg}
	case errors.As(err, &b):
		return ending{"SyntaxError", b.Line, b.Msg}
	}
	return ending{kind: fmt.Sprintf("%T", err), msg: err.Error()}
}

// step reads one token, turning a panic into an ending so one bad file does
// not take the whole run down.
func step(s side) (tok any, err error) {
	defer func() {
		if r := recover(); r != nil {
			tok, err = nil, fmt.Errorf("panic: %v", r)
		}
	}()
	return s.next()
}

// result is one comparison. diff is empty when the sides agree.
type result struct {
	diff     string
	index    int    // token index of the first difference, or of the ending
	end      ending // the reference side's ending
	msgDiffs bool   // both errored identically but with different messages
	doctype  bool   // a DOCTYPE was seen, so the entity pass is meaningful
}

// compare runs ref and cand in lockstep over src. With entities set, a
// DOCTYPE's internal general entities are installed on each side the moment
// that side returns the Directive, as xdm does.
func compare(src []byte, mkRef, mkCand func(io.Reader) side, entities bool) result {
	ref, cand := mkRef(bytes.NewReader(src)), mkCand(bytes.NewReader(src))
	var r result
	for i := 0; ; i++ {
		if a, b := ref.offset(), cand.offset(); a != b {
			return result{diff: fmt.Sprintf("InputOffset before token %d: ref %d, cand %d", i, a, b), index: i}
		}
		if a, b := ref.v11(), cand.v11(); a != b {
			return result{diff: fmt.Sprintf("IsVersion11 before token %d: ref %v, cand %v", i, a, b), index: i}
		}
		ta, ea := step(ref)
		tb, eb := step(cand)
		if ca, cb := canon(ta), canon(tb); ca != cb {
			return result{diff: fmt.Sprintf("token %d:\n  ref:  %s\n  cand: %s", i, clip(ca), clip(cb)), index: i}
		}
		if ea != nil || eb != nil {
			if ea == nil || eb == nil {
				return result{diff: fmt.Sprintf("token %d: ref err %v, cand err %v", i, ea, eb), index: i}
			}
			x, y := endOf(ea), endOf(eb)
			if x.kind != y.kind || x.line != y.line {
				return result{diff: fmt.Sprintf("ending at token %d: ref %s line %d (%s), cand %s line %d (%s)",
					i, x.kind, x.line, x.msg, y.kind, y.line, y.msg), index: i}
			}
			r.index, r.end, r.msgDiffs = i, x, x.msg != y.msg
			return r
		}
		if d, ok := ta.(xmlfork.Directive); ok {
			r.doctype = r.doctype || strings.HasPrefix(strings.TrimSpace(string(d)), "DOCTYPE")
			if entities {
				if m := doctypeEntities(d); m != nil {
					ref.setEntity(m)
					// A separate copy, so a candidate that writes to the
					// map cannot change what the reference sees.
					c := make(map[string]string, len(m))
					for k, v := range m {
						c[k] = v
					}
					cand.setEntity(c)
				}
			}
		}
	}
}

// compareBoth runs the plain pass and, when the document has a DOCTYPE, the
// entity pass. The first difference wins; with none, the entity pass's ending
// is reported, since that is the stream xdm actually reads.
func compareBoth(src []byte, mkRef, mkCand func(io.Reader) side) result {
	r := compare(src, mkRef, mkCand, false)
	if r.diff != "" || !r.doctype {
		return r
	}
	e := compare(src, mkRef, mkCand, true)
	if e.diff != "" {
		e.diff = "entity pass: " + e.diff
	}
	return e
}

func clip(s string) string {
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

// xmlExt lists the extensions read regardless of content. Any other file is
// read when, after an optional BOM and whitespace, its first byte is '<'.
var xmlExt = map[string]bool{
	".xml": true, ".xsd": true, ".xsl": true, ".xslt": true, ".rng": true,
	".xspec": true, ".xhtml": true, ".svg": true, ".wsdl": true,
}

func looksLikeXML(path string, b []byte) bool {
	if xmlExt[strings.ToLower(filepath.Ext(path))] {
		return true
	}
	if bytes.HasPrefix(b, []byte{0xFE, 0xFF}) || bytes.HasPrefix(b, []byte{0xFF, 0xFE}) {
		return true
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	b = bytes.TrimLeft(b, " \t\r\n")
	return len(b) > 0 && b[0] == '<'
}

type corpus struct{ name, dir string }

// corpora lists every tree the harness reads. The large suites are skipped
// under GOXSLT_NO_SUITES, as the conformance harnesses are, unless
// GOXML_TOKDIFF=1 asks for the full gate.
func corpora(t *testing.T) []corpus {
	base := os.Getenv("GOXSLT_TESTDATA")
	if base == "" {
		base = filepath.Join("..", "..", "testdata")
	}
	full := os.Getenv("GOXML_TOKDIFF") == "1"
	all := []corpus{
		{"c14n-local", filepath.Join("..", "..", "tests", "c14n", "testdata")},
		{"c14n", filepath.Join(base, "c14n")},
	}
	if full || os.Getenv("GOXSLT_NO_SUITES") == "" {
		for _, n := range []string{"xsdtests", "xslt30-test", "qt3tests", "xsltng", "xspec", "relaxng"} {
			all = append(all, corpus{n, filepath.Join(base, n)})
		}
	}
	var have []corpus
	for _, c := range all {
		if fi, err := os.Stat(c.dir); err == nil && fi.IsDir() {
			have = append(have, c)
		} else if full {
			t.Errorf("GOXML_TOKDIFF=1 but corpus %s is absent at %s", c.name, c.dir)
		} else {
			t.Logf("corpus %s absent at %s; skipped", c.name, c.dir)
		}
	}
	return have
}

func readAllow(t *testing.T) map[string]string {
	f, err := os.Open(filepath.Join("testdata", "tokdiff-allow.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	allow := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		path, reason, _ := strings.Cut(line, " ")
		if strings.TrimSpace(reason) == "" {
			t.Errorf("tokdiff-allow.txt: %s has no reason", path)
		}
		allow[path] = reason
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return allow
}

type tally struct{ files, same, differ, rejected, msgDiffs int }

func TestTokenDifferential(t *testing.T) {
	cs := corpora(t)
	if len(cs) == 0 {
		t.Skip("no corpus present")
	}
	allow := readAllow(t)
	type job struct{ key, path string }
	type finding struct{ key, diff string }

	var (
		mu       sync.Mutex
		findings []finding
		used     = map[string]bool{}
	)
	start := time.Now()
	for _, c := range cs {
		var jobs []job
		err := filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(c.dir, p)
			jobs = append(jobs, job{c.name + "/" + filepath.ToSlash(rel), p})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", c.dir, err)
		}

		var n tally
		t0 := time.Now()
		ch := make(chan job)
		var wg sync.WaitGroup
		for range runtime.GOMAXPROCS(0) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range ch {
					b, err := os.ReadFile(j.path)
					if err != nil {
						t.Errorf("%s: %v", j.key, err)
						continue
					}
					if !looksLikeXML(j.path, b) {
						continue
					}
					r := compareBoth(xdmInput(b), forkSide, tokSide)
					mu.Lock()
					n.files++
					switch {
					case r.diff != "" && allow[j.key] != "":
						used[j.key] = true
					case r.diff != "":
						n.differ++
						findings = append(findings, finding{j.key, r.diff})
					default:
						n.same++
						if r.end.kind != "EOF" {
							n.rejected++
						}
						if r.msgDiffs {
							n.msgDiffs++
						}
					}
					mu.Unlock()
				}
			}()
		}
		for _, j := range jobs {
			ch <- j
		}
		close(ch)
		wg.Wait()
		t.Logf("%-12s files %6d  identical %6d (both rejected same place %5d, msg differs %d)  differing %d  allowlisted %d  %v",
			c.name, n.files, n.same, n.rejected, n.msgDiffs, n.differ, n.files-n.same-n.differ, time.Since(t0).Round(time.Millisecond))
	}
	t.Logf("wall time %v", time.Since(start).Round(time.Millisecond))

	sort.Slice(findings, func(i, j int) bool { return findings[i].key < findings[j].key })
	for i, f := range findings {
		if i == 20 {
			t.Errorf("... and %d more differing files", len(findings)-20)
			break
		}
		t.Errorf("%s: %s", f.key, f.diff)
	}
	// Staleness guard: an allowlisted file in a corpus that ran must still
	// differ, or the entry is hiding nothing and goes.
	ran := map[string]bool{}
	for _, c := range cs {
		ran[c.name] = true
	}
	for k := range allow {
		corpusName, _, _ := strings.Cut(k, "/")
		if ran[corpusName] && !used[k] {
			t.Errorf("tokdiff-allow.txt: %s no longer differs (or is gone); remove it", k)
		}
	}
}

// diffSeeds are the snippets the fuzz target starts from: the places a
// rewritten tokeniser is most likely to drift.
var diffSeeds = []string{
	`<a><![CDATA[]]></a>`,
	`<a><![CDATA[x]]]]><![CDATA[>y]]></a>`,
	`<a>]]></a>`,
	`<a><![CDATA[unterminated</a>`,
	`<a>&#65;&#x42;&#x10FFFF;&#0;&#xD800;&#xFFFE;</a>`,
	`<?xml version="1.1"?><a b="&#x1;">&#x7;&#x1f;&#x85;</a>`,
	"<?xml version=\"1.1\"?><a>\x07</a>",
	"<?xml version=\"1.1\"?><a>x\u0085y\r\u0085z\u2028</a>",
	"<?xml version=\"1.0\"?><a>x\u0085y</a>",
	"<a b=\"x\r\ny\">1\r\n2\r3\n</a>",
	"<a>\r</a>",
	`<!DOCTYPE a [<!-- ]> --><!ENTITY e "v'>"><!ENTITY f 'w"'><!ELEMENT a ANY>]><a>&e;&f;</a>`,
	`<!DOCTYPE a SYSTEM "a.dtd" [<!ENTITY % p "x"><!ATTLIST a b CDATA "d">]><a/>`,
	`<!DOCTYPE a [<!ENTITY e "&#60;b/>">]><a b="&e;">&e;</a>`,
	`<?pi?><?pi data ?><a><?x y?></a><?xml-stylesheet href="s"?>`,
	`<?xml version="1.0" encoding="ISO-8859-1"?><a>` + "\xe9" + `</a>`,
	`<?xml version="1.0" encoding="us-ascii"?><a>` + "\xe9" + `</a>`,
	`<?xml version="1.0" encoding="EBCDIC"?><a/>`,
	`<?xml version='1.0' standalone='yes'?><a/>`,
	`<a x='&amp;&lt;&gt;&quot;&apos;' y="&undef;"/>`,
	`<p:a xmlns:p="u" p:b="1"><p:c/></p:a>`,
	`<a><!-- - --><!----><!-- -- --></a>`,
	`<a></b>`,
	"<a>\n\n<b\n/>\n</a>\n<",
}

// FuzzRawTokenDifferential holds xmltok to xmlfork on arbitrary input. It
// needs no testdata.
func FuzzRawTokenDifferential(f *testing.F) {
	for _, s := range diffSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 4096 {
			return
		}
		if r := compareBoth(src, forkSide, tokSide); r.diff != "" {
			t.Fatalf("%q: %s", src, r.diff)
		}
	})
}

// TestDifferentialCanFail is the sabotage check: a candidate perturbed in
// each compared property must be reported, or the harness is green by
// construction.
func TestDifferentialCanFail(t *testing.T) {
	const doc = `<?xml version="1.1"?><!DOCTYPE a [<!ENTITY e "v">]><a>text&e;</a>`
	perturb := map[string]func(side) side{
		"drop a CharData byte": func(s side) side {
			next := s.next
			s.next = func() (any, error) {
				tok, err := next()
				if c, ok := tok.(xmltok.CharData); ok && len(c) > 0 {
					tok = c[1:]
				}
				return tok, err
			}
			return s
		},
		"offset off by one": func(s side) side {
			off := s.offset
			s.offset = func() int64 { return off() + 1 }
			return s
		},
		"version flipped": func(s side) side {
			v := s.v11
			s.v11 = func() bool { return !v() }
			return s
		},
		"entity map ignored": func(s side) side {
			s.setEntity = func(map[string]string) {}
			return s
		},
		"stops early": func(s side) side {
			next := s.next
			s.next = func() (any, error) {
				tok, err := next()
				if _, ok := tok.(xmltok.EndElement); ok {
					return nil, io.EOF
				}
				return tok, err
			}
			return s
		},
	}
	if r := compareBoth([]byte(doc), forkSide, tokSide); r.diff != "" || r.end.kind != "EOF" {
		t.Fatalf("unperturbed: diff %q, ending %v", r.diff, r.end)
	}
	for name, p := range perturb {
		mk := func(r io.Reader) side { return p(tokSide(r)) }
		if r := compareBoth([]byte(doc), forkSide, mk); r.diff == "" {
			t.Errorf("%s: harness reported no difference", name)
		} else {
			t.Logf("%s: %s", name, strings.ReplaceAll(r.diff, "\n", " "))
		}
	}
}
