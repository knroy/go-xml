package xmltok

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// render drains a Decoder into one line per token, in a notation close to the
// markup: <p:a k="v"> </p:a> "text" <!--c--> <?t inst?> <!D>. It stops at the
// first error and returns it.
func render(d *Decoder) (string, error) {
	var sb strings.Builder
	for {
		t, err := d.RawToken()
		if err == io.EOF {
			return sb.String(), nil
		}
		if err != nil {
			return sb.String(), err
		}
		switch t := t.(type) {
		case *StartElement:
			sb.WriteString("<" + qname(t.Name))
			for _, a := range t.Attr {
				fmt.Fprintf(&sb, " %s=%q", qname(a.Name), a.Value)
			}
			sb.WriteString(">")
		case *EndElement:
			sb.WriteString("</" + qname(t.Name) + ">")
		case *CharData:
			fmt.Fprintf(&sb, "%q", *t)
		case *Comment:
			sb.WriteString("<!--" + string(*t) + "-->")
		case *ProcInst:
			sb.WriteString("<?" + t.Target + " " + string(t.Inst) + "?>")
		case *Directive:
			sb.WriteString("<!" + string(*t) + ">")
		}
	}
}

func qname(n Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// tokenCase is a document and either the tokens it reads as or the error,
// with its line, that stops it. Every case is run twice: read whole, and a
// byte at a time, which puts each construct across a window refill.
type tokenCase struct {
	name, src string
	want      string // rendered tokens, when wantErr is ""
	wantErr   string // the error's full text
}

func runCases(t *testing.T, cases []tokenCase) {
	t.Helper()
	for _, c := range cases {
		for _, oneByte := range []bool{false, true} {
			var r io.Reader = strings.NewReader(c.src)
			if oneByte {
				r = iotest.OneByteReader(r)
			}
			got, err := render(NewDecoder(r))
			switch {
			case c.wantErr != "":
				if err == nil || err.Error() != c.wantErr {
					t.Errorf("%s (one byte %v): error %v, want %s", c.name, oneByte, err, c.wantErr)
				}
			case err != nil:
				t.Errorf("%s (one byte %v): %v", c.name, oneByte, err)
			case got != c.want:
				t.Errorf("%s (one byte %v):\n got %s\nwant %s", c.name, oneByte, got, c.want)
			}
		}
	}
}

func syntax(line int, msg string) string {
	return (&SyntaxError{Msg: msg, Line: line}).Error()
}

func TestText(t *testing.T) {
	runCases(t, []tokenCase{
		{name: "plain", src: "<a>hi</a>", want: `<a>"hi"</a>`},
		{name: "top-level text", src: "x<a/>y", want: `"x"<a></a>"y"`},
		{name: "crlf", src: "<a>1\r\n2\r3\r\r\n4</a>", want: `<a>"1\n2\n3\n\n4"</a>`},
		{name: "predefined", src: "<a>&lt;&gt;&amp;&apos;&quot;</a>", want: `<a>"<>&'\""</a>`},
		{name: "char refs", src: "<a>&#65;&#x42;&#x0043;&#xe9;</a>", want: `<a>"ABCé"</a>`},
		{name: "ref to cr is not normalised", src: "<a>&#13;\n</a>", want: `<a>"\r\n"</a>`},
		{name: "escaped ]]>", src: "<a>]]&gt;</a>", want: `<a>"]]>"</a>`},
		{name: "lone > and ]", src: "<a>]>]]</a>", want: `<a>"]>]]"</a>`},
		{name: "]]>", src: "<a>\n]]></a>", wantErr: syntax(2, "unescaped ]]> not in CDATA section")},
		{name: "uppercase X", src: "<a>&#X41;</a>", wantErr: syntax(1, "invalid character entity &# (no semicolon)")},
		{name: "empty ref", src: "<a>&#;</a>", wantErr: syntax(1, "invalid character entity &#;")},
		{name: "huge ref", src: "<a>&#99999999999999999999999;</a>", wantErr: syntax(1, "invalid character entity &#99999999999999999999999;")},
		{name: "NUL ref", src: "<a>&#0;</a>", wantErr: syntax(1, "illegal character code U+0000")},
		{name: "surrogate ref", src: "<a>&#xD800;</a>", wantErr: syntax(1, "illegal character code U+D800")},
		{name: "unknown entity", src: "<a>&nbsp;</a>", wantErr: syntax(1, "invalid character entity &nbsp;")},
		{name: "no semicolon", src: "<a>&amp </a>", wantErr: syntax(1, "invalid character entity &amp (no semicolon)")},
		{name: "bare &", src: "<a>&;</a>", wantErr: syntax(1, "invalid character entity &;")},
		{name: "invalid utf-8", src: "<a>\xff</a>", wantErr: syntax(1, "invalid UTF-8")},
		{name: "literal control", src: "<a>\x01</a>", wantErr: syntax(1, "illegal character code U+0001")},
		{name: "char error at end of run", src: "<a>\x01\n\n</a>", wantErr: syntax(3, "illegal character code U+0001")},
		{name: "eof in ref", src: "<a>&am", wantErr: syntax(1, "unexpected EOF")},
	})
}

func TestXML11(t *testing.T) {
	const v11 = `<?xml version="1.1"?>`
	const pi = `<?xml version="1.1"?>`
	runCases(t, []tokenCase{
		{name: "restricted ref", src: v11 + "<a>&#x7;</a>", want: pi + `<a>"\a"</a>`},
		{name: "restricted ref 1.0", src: "<a>&#x7;</a>", wantErr: syntax(1, "illegal character code U+0007")},
		{name: "restricted literal", src: v11 + "<a>\x07</a>", wantErr: syntax(1, "illegal character code U+0007")},
		{name: "NUL ref", src: v11 + "<a>&#0;</a>", wantErr: syntax(1, "illegal character code U+0000")},
		{name: "NEL", src: v11 + "<a>x\u0085y</a>", want: pi + `<a>"x\ny"</a>`},
		{name: "LS", src: v11 + "<a>x\u2028y</a>", want: pi + `<a>"x\ny"</a>`},
		{name: "CR NEL", src: v11 + "<a>x\r\u0085y</a>", want: pi + `<a>"x\ny"</a>`},
		{name: "NEL ref kept", src: v11 + "<a>&#x85;</a>", want: pi + `<a>"\u0085"</a>`},
		{name: "NEL 1.0 kept", src: "<a>x\u0085y</a>", want: `<a>"x\u0085y"</a>`},
		{name: "NEL in attr", src: v11 + "<a b='\u0085'/>", want: pi + `<a b="\n"></a>`},
		{name: "1.x is 1.0", src: `<?xml version="1.7"?><a>&#x7;</a>`, wantErr: syntax(1, "illegal character code U+0007")},
		{name: "1.10 is 1.0", src: `<?xml version="1.10"?><a/>`, want: `<?xml version="1.10"?><a></a>`},
		{name: "unsupported", src: `<?xml version="2.0"?><a/>`, wantErr: `xml: unsupported version "2.0"; only versions 1.x are supported`},
		{name: "no digits", src: `<?xml version="1."?><a/>`, wantErr: `xml: unsupported version "1."; only versions 1.x are supported`},
		{name: "not digits", src: `<?xml version="1.1a"?><a/>`, wantErr: `xml: unsupported version "1.1a"; only versions 1.x are supported`},
		{name: "restricted literal in comment", src: v11 + "<!--\x07-->", wantErr: syntax(1, "illegal character code U+0007")},
		{name: "restricted literal in pi", src: v11 + "<?t \x07?>", wantErr: syntax(1, "illegal character code U+0007")},
		{name: "NEL in comment", src: v11 + "<!--\u0085-->", want: pi + "<!--\u0085-->"},
		{name: "stray decl", src: `<?xml version="1.0"?><a>` + `<?xml version="1.1"?>` + "\x07</a>",
			wantErr: syntax(1, "illegal character code U+0007")},
	})
	d := NewDecoder(strings.NewReader(v11 + "<a/>"))
	d.RawToken()
	if !d.IsVersion11() {
		t.Error("IsVersion11 after a 1.1 declaration = false")
	}
}

func TestTags(t *testing.T) {
	runCases(t, []tokenCase{
		{name: "attrs", src: `<a x="1" y='2' p:z = "3"></a>`, want: `<a x="1" y="2" p:z="3"></a>`},
		{name: "empty", src: `<a/>`, want: `<a></a>`},
		{name: "prefixed", src: `<p:a></p:a>`, want: `<p:a></p:a>`},
		{name: "edge colons", src: `<:a b:="1"/>`, want: `<:a b:="1"></:a>`},
		{name: "attr ws kept", src: "<a b='\t\r\nc'/>", want: `<a b="\t\nc"></a>`},
		{name: "attr refs", src: `<a b="&lt;&#65;"/>`, want: `<a b="<A"></a>`},
		{name: "attr ]]>", src: `<a b="]]>"/>`, want: `<a b="]]>"></a>`},
		{name: "no space between attrs", src: `<a b="1"c="2"/>`, wantErr: syntax(1, "expected white space between attributes")},
		{name: "no space between attrs, single quotes", src: `<a b='1'c='2'>`, wantErr: syntax(1, "expected white space between attributes")},
		{name: "any S between attrs", src: "<a b='1'\tc='2'\nd='3'\r\ne='4'/>", want: `<a b="1" c="2" d="3" e="4"></a>`},
		{name: "no space before >", src: `<a b="1">`, want: `<a b="1">`},
		{name: "no space before />", src: `<a b="1"/>`, want: `<a b="1"></a>`},
		{name: "5e name", src: "<Dĳkstra/>", want: "<Dĳkstra></Dĳkstra>"},
		{name: "end tag space", src: "<a></a \n>", want: "<a></a>"},
		{name: "two colons", src: `<a:b:c/>`, wantErr: syntax(1, "expected element name after <")},
		{name: "bad start", src: `<1a/>`, wantErr: syntax(1, "invalid XML name: 1a")},
		{name: "combining start", src: "<\u0300a/>", wantErr: syntax(1, "invalid XML name: \u0300a")},
		{name: "no name", src: `< a/>`, wantErr: syntax(1, "expected element name after <")},
		{name: "no end name", src: `</>`, wantErr: syntax(1, "expected element name after </")},
		{name: "junk in end", src: `</a b>`, wantErr: syntax(1, "invalid characters between </a and >")},
		{name: "bad empty", src: `<a/ >`, wantErr: syntax(1, "expected /> in element")},
		{name: "no =", src: `<a b>`, wantErr: syntax(1, "attribute name without = in element")},
		{name: "unquoted", src: `<a b=c>`, wantErr: syntax(1, "unquoted or missing attribute value in element")},
		{name: "< in attr", src: `<a b="<"/>`, wantErr: syntax(1, "unescaped < inside quoted string")},
		{name: "bad attr name", src: `<a ="1"/>`, wantErr: syntax(1, "expected attribute name in element")},
		{name: "eof in tag", src: "<a\n", wantErr: syntax(2, "unexpected EOF")},
		{name: "eof in attr", src: `<a b="x`, wantErr: syntax(1, "unexpected EOF")},
		{name: "unmatched end is xdm's", src: `<a></b>`, want: `<a></b>`},
	})
}

func TestMarkup(t *testing.T) {
	runCases(t, []tokenCase{
		{name: "comment", src: "<!-- c\r\n-->", want: "<!-- c\r\n-->"},
		{name: "empty comment", src: "<!---->", want: "<!---->"},
		{name: "dash comment", src: "<!---a-->", want: "<!---a-->"},
		{name: "double dash", src: "<!-- a -- b -->", wantErr: syntax(1, `invalid sequence "--" not allowed in comments`)},
		{name: "bad comment open", src: "<!-a-->", wantErr: syntax(1, "invalid sequence <!- not part of <!--")},
		{name: "eof in comment", src: "<!-- a -", wantErr: syntax(1, "unexpected EOF")},
		{name: "comment ending --->", src: "<!-- a --->", wantErr: syntax(1, `invalid sequence "--" not allowed in comments`)},
		{name: "comment form feed", src: "<!-- \f -->", wantErr: syntax(1, "illegal character code U+000C")},
		{name: "comment NUL", src: "<!--\n\x00-->", wantErr: syntax(2, "illegal character code U+0000")},
		{name: "comment U+FFFF", src: "<!-- \uffff -->", wantErr: syntax(1, "illegal character code U+FFFF")},
		{name: "comment invalid utf-8", src: "<!-- \xc3 -->", wantErr: syntax(1, "invalid UTF-8")},
		{name: "comment non-ascii", src: "<!-- é\t\U0001F600 -->", want: "<!-- é\t\U0001F600 -->"},
		{name: "pi", src: "<?t  some ? data?>", want: "<?t some ? data?>"},
		{name: "pi empty", src: "<?t?>", want: "<?t ?>"},
		{name: "pi no target", src: "<? t?>", wantErr: syntax(1, "expected target name after <?")},
		{name: "pi no space after target", src: "<?t+++?>", wantErr: syntax(1, "expected white space after processing instruction target t")},
		{name: "pi target runs into data", src: "<?xmlversion='1.0'?>", wantErr: syntax(1, "expected white space after processing instruction target xmlversion")},
		{name: "pi ? after target", src: "<?t?x?>", wantErr: syntax(1, "expected white space after processing instruction target t")},
		{name: "pi tab after target", src: "<?t\tx?>", want: "<?t x?>"},
		{name: "pi form feed", src: "<?t a\fb?>", wantErr: syntax(1, "illegal character code U+000C")},
		{name: "pi U+FFFF", src: "<?t \uffff?>", wantErr: syntax(1, "illegal character code U+FFFF")},
		{name: "pi invalid utf-8", src: "<?t \xff?>", wantErr: syntax(1, "invalid UTF-8")},
		{name: "pi non-ascii", src: "<?t é\U0001F600?>", want: "<?t é\U0001F600?>"},
		{name: "cdata", src: "<a><![CDATA[<b>&amp;\r\n]]]></a>", want: `<a>"<b>&amp;\n]"</a>`},
		{name: "empty cdata", src: "<![CDATA[]]>", want: `""`},
		{name: "cdata checked", src: "<![CDATA[\x01]]>", wantErr: syntax(1, "illegal character code U+0001")},
		{name: "eof in cdata", src: "<![CDATA[x", wantErr: syntax(1, "unexpected EOF in CDATA section")},
		{name: "bad cdata", src: "<![CDATX[", wantErr: syntax(1, "invalid <![ sequence")},
		{name: "doctype", src: `<!DOCTYPE a [<!ENTITY e "x>y"><!ATTLIST a b CDATA '>'>]>`,
			want: `<!DOCTYPE a [<!ENTITY e "x>y"><!ATTLIST a b CDATA '>'>]>`},
		{name: "doctype comment", src: "<!DOCTYPE a [<!-- > -- --><!ELEMENT a ANY>]>",
			want: "<!DOCTYPE a [ <!ELEMENT a ANY>]>"},
		{name: "doctype <!- not comment", src: "<!DOCTYPE a [<!-x>]>", want: "<!DOCTYPE a [<!-x>]>"},
		{name: "first byte is literal", src: "<!>a>", want: "<!>a>"},
		{name: "eof in doctype", src: "<!DOCTYPE a [", wantErr: syntax(1, "unexpected EOF")},
	})
}

// TestEntityIsLazy fills Entity between calls, as xdm does once it has read
// the internal subset. The replacement is text, not markup, and the character
// check still sees it.
func TestEntityIsLazy(t *testing.T) {
	d := NewDecoder(strings.NewReader(`<!DOCTYPE a><a>&e;&f;</a>`))
	if _, err := d.RawToken(); err != nil {
		t.Fatal(err)
	}
	d.Entity = map[string]string{"e": "<b/>", "f": "\x01"}
	d.RawToken()
	if _, err := d.RawToken(); err == nil || err.Error() != syntax(1, "illegal character code U+0001") {
		t.Fatalf("control character in replacement text: %v", err)
	}

	d = NewDecoder(strings.NewReader(`<a>&e;</a>`))
	d.Entity = map[string]string{"e": "<b/>"}
	got, err := render(d)
	if err != nil || got != `<a>"<b/>"</a>` {
		t.Errorf("got %s, %v", got, err)
	}
}

// TestLiteral: CharData written out as it stands is Literal; a reference
// anywhere in it, or a CDATA section, is not, whatever text results.
func TestLiteral(t *testing.T) {
	cases := []struct {
		src  string
		want []bool // Literal after each CharData
	}{
		{" \t\r\n<a/>x", []bool{true, true}},
		{"&#32;<a/>&#x20;", []bool{false, false}},
		{"<a/> &amp; ", []bool{false}},
		{"<a/>&e;", []bool{false}},
		{"<![CDATA[]]><a/><![CDATA[ ]]> ", []bool{false, false, true}},
		{" <![CDATA[]]><a/>", []bool{true, false}},
		{"<a b='&#32;'/> ", []bool{true}},
		{"<a>&lt;</a>\n", []bool{false, true}},
	}
	for _, c := range cases {
		for _, oneByte := range []bool{false, true} {
			var r io.Reader = strings.NewReader(c.src)
			if oneByte {
				r = iotest.OneByteReader(r)
			}
			d := NewDecoder(r)
			d.Entity = map[string]string{"e": ""}
			var got []bool
			for {
				tok, err := d.RawToken()
				if err != nil {
					break
				}
				if _, ok := tok.(*CharData); ok {
					got = append(got, d.Literal())
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("%q (one byte %v): Literal %v, want %v", c.src, oneByte, got, c.want)
			}
		}
	}
}

func TestEncoding(t *testing.T) {
	src := "<?xml version='1.0' encoding='ISO-8859-1'?><a>\xe9</a>"
	d := NewDecoder(strings.NewReader(src))
	var called string
	d.CharsetReader = func(cs string, r io.Reader) (io.Reader, error) {
		called = cs
		return latinOnly(cs, r)
	}
	got, err := render(d)
	if err != nil || called != "ISO-8859-1" || got != `<?xml version='1.0' encoding='ISO-8859-1'?><a>"é"</a>` {
		t.Errorf("latin-1: %s, %v, CharsetReader(%q)", got, err, called)
	}

	for _, enc := range []string{"utf-8", "UTF-8", "Utf-8"} {
		d := NewDecoder(strings.NewReader(`<?xml version="1.0" encoding="` + enc + `"?><a/>`))
		d.CharsetReader = func(string, io.Reader) (io.Reader, error) {
			t.Errorf("CharsetReader called for %s", enc)
			return nil, errors.New("no")
		}
		render(d)
	}

	d = NewDecoder(strings.NewReader(`<?xml encoding="latin1"?><a/>`))
	if _, err := render(d); err == nil || err.Error() != `xml: encoding "latin1" declared but Decoder.CharsetReader is nil` {
		t.Errorf("nil CharsetReader: %v", err)
	}
	d = NewDecoder(strings.NewReader(`<?xml encoding="ebcdic"?><a/>`))
	d.CharsetReader = latinOnly
	if _, err := render(d); err == nil || !strings.HasPrefix(err.Error(), `xml: opening charset "ebcdic": `) {
		t.Errorf("refused charset: %v", err)
	}
}

// TestDeclValue pins the substring reading of the declaration's
// pseudo-attributes, quirks included.
func TestDeclValue(t *testing.T) {
	cases := []struct{ decl, key, want string }{
		{`version="1.0" encoding='x'`, "encoding", "x"},
		{`version = "1.0"`, "version", ""},
		{`myencoding="x"`, "encoding", "x"},
		{`version=1 version="2"`, "version", "2"},
		{`version=version="2"`, "version", ""},
		{`version="1.0`, "version", ""},
		{`version=`, "version", ""},
	}
	for _, c := range cases {
		if got := declValue(c.decl, c.key); got != c.want {
			t.Errorf("declValue(%q, %q) = %q, want %q", c.decl, c.key, got, c.want)
		}
	}
}

func TestInputOffset(t *testing.T) {
	const src = "<a b='1'>text<!--c--><b/></a>"
	d := NewDecoder(strings.NewReader(src))
	var got []int64
	for {
		if _, err := d.RawToken(); err != nil {
			break
		}
		got = append(got, d.InputOffset())
	}
	want := fmt.Sprint([]int64{9, 13, 21, 25, 25, 29})
	if fmt.Sprint(got) != want {
		t.Errorf("offsets %v, want %s", got, want)
	}
}

// TestErrorIsSticky: after an error, the Decoder returns it again rather than
// reading on from an arbitrary point.
func TestErrorIsSticky(t *testing.T) {
	d := NewDecoder(strings.NewReader("<a>&bad;</a><b/>"))
	d.RawToken()
	_, err1 := d.RawToken()
	_, err2 := d.RawToken()
	if err1 == nil || err1 != err2 {
		t.Errorf("errors %v then %v", err1, err2)
	}
	d = NewDecoder(strings.NewReader("<a/>"))
	render(d)
	if _, err := d.RawToken(); err != io.EOF {
		t.Errorf("after the end: %v", err)
	}
}

// TestReaderError: a failing reader's error is returned as it is, after the
// bytes read before it.
func TestReaderError(t *testing.T) {
	boom := errors.New("boom")
	d := NewDecoder(io.MultiReader(strings.NewReader("<a>x"), iotest.ErrReader(boom)))
	got, err := render(d)
	if err != boom || got != `<a>"x"` {
		t.Errorf("got %s, %v", got, err)
	}
}

// seeds lean on the constructs whose edges have hand-written code: refill
// boundaries are reached by the fuzzers' byte-at-a-time variant.
var seeds = []string{
	``, `<`, `<a>`, `<a/>`, `<a>text</a>`, `<a b="1" c='2'/>`,
	`<a xmlns:p="u"><p:b p:c="1"/></a>`,
	`<a><![CDATA[<not markup & co>]]></a>`,
	`<a><!--c--><?pi data?>t</a>`,
	`<a>&#65;&amp;&lt;&#x10FFFF;&#1114112;</a>`,
	"<DĲkstra/>", "<\U00010000/>", `<1a/>`, "<̀a/>", `<a b/>`, `</>`,
	`<a>&#xD800;</a>`, "<a>\x00</a>", "<a>\xff\xfe</a>", "<a>]]></a>",
	"<a>\r\n\r</a>", "<a b='\r\n'/>",
	`<?xml version="1.1"?><a>&#x7;</a>`, "<?xml version=\"1.1\"?><a>\x07</a>",
	"<?xml version=\"1.1\"?><a>x\r\u0085y\u2028</a>",
	`<?xml version="1.2"?><a/>`, `<?xml version="1.1"`,
	`<?xml version="1.0"?><a/><?xml version="1.1"?>`,
	`<?xml encoding="latin1"?><a>x</a>`,
	`<!DOCTYPE a [<!ENTITY e "x>y"><!-- > --><!ATTLIST a b CDATA '>'>]><a>&e;</a>`,
	"<!DOCTYPE a [<<<!-x>]>",
	"<a>&e;&bad;&br;>&u;&hi;</a>",
	`<a b="1"c="2"/>`, "<?t+?>", "<!--\f-->", "<?t \f?>", "&#32;<![CDATA[]]><a/>",
}

// FuzzRawTokenNoPanic drives the tokeniser to exhaustion over arbitrary
// bytes. Ending in io.EOF or an error is correct; panicking, returning a token
// with an error, or returning neither is not.
func FuzzRawTokenNoPanic(f *testing.F) {
	for _, s := range seeds {
		f.Add(s, false)
	}
	f.Fuzz(func(t *testing.T, src string, oneByte bool) {
		var r io.Reader = strings.NewReader(src)
		if oneByte {
			r = iotest.OneByteReader(r)
		}
		d := NewDecoder(r)
		d.Entity = map[string]string{"e": "x"}
		d.CharsetReader = latinOnly
		for {
			tok, err := d.RawToken()
			if err != nil {
				if tok != nil {
					t.Fatalf("%q: token %T with error %v", src, tok, err)
				}
				return
			}
			if tok == nil {
				t.Fatalf("%q: nil token and nil error", src)
			}
			if d.InputOffset() > int64(len(src))*2 {
				t.Fatalf("%q: offset %d past the input", src, d.InputOffset())
			}
		}
	})
}

// latinOnly is xdm's charsetReader in miniature: US-ASCII passes through,
// ISO-8859-1 is widened byte for byte, and anything else is refused.
func latinOnly(charset string, input io.Reader) (io.Reader, error) {
	b, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(charset) {
	case "us-ascii", "ascii", "iso-646", "us_ascii":
		return bytes.NewReader(b), nil
	case "iso-8859-1", "latin1", "iso8859-1", "iso_8859-1":
		var out bytes.Buffer
		for _, c := range b {
			out.WriteRune(rune(c))
		}
		return &out, nil
	}
	return nil, fmt.Errorf("unsupported encoding %q", charset)
}

// TestRawTokenDoesNotAllocatePerToken pins that a token costs no allocation:
// RawToken returns pointers to values the Decoder reuses, where returning a
// struct or slice in the Token interface copied each one to the heap. 1,000
// repetitions of eight tokens took about 8,000 allocations that way.
func TestRawTokenDoesNotAllocatePerToken(t *testing.T) {
	doc := "<r>" + strings.Repeat("<e>text</e><!--c--><?p x?><![CDATA[d]]><f/>", 1000) + "</r>"
	allocs := testing.AllocsPerRun(5, func() {
		d := NewDecoder(strings.NewReader(doc))
		for {
			if _, err := d.RawToken(); err != nil {
				break
			}
		}
	})
	if allocs > 100 {
		t.Errorf("tokenizing 8,002 tokens took %.0f allocations, want at most 100", allocs)
	}
}
