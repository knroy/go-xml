package xmlfork

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// The forked tokeniser is the front door for every untrusted document this
// engine reads, so a panic here is a denial of service for any embedder. The
// seeds lean on names, since the name tables are the only thing this fork
// changes from upstream: each of the 5e-only names below reaches a code path
// that upstream refused outright and that now runs to completion.
var tokenSeeds = []string{
	// Well-formed, ASCII.
	`<a/>`,
	`<a>text</a>`,
	`<a xmlns:p="u"><p:b p:c="1"/></a>`,
	`<a><![CDATA[<not markup & co>]]></a>`,
	`<a><!--c--><?pi data?>t</a>`,
	`<a>&#65;&amp;&lt;</a>`,

	// Names legal under 1.0 5e and not under 1e — the widened paths.
	"<DĲkstra/>",
	"<aĲ bĲ=\"1\"/>",
	"<\U00010000/>",
	"<à/>",
	"<Ⰰ:Ⰱ xmlns:Ⰰ=\"u\"/>",

	// Names that must still be refused, so the refusal path is fuzzed too.
	`<1a/>`,
	`<-a/>`,
	"<̀a/>",
	`<a b/>`,
	`<a<b/>`,
	`</>`,

	// Malformed.
	``,
	`<`,
	`<a>`,
	`<a></b>`,
	`<a b="1" b="2"/>`,
	`<a>&#xD800;</a>`,
	"<a>\x00</a>",
	"<a>\xff\xfe</a>",

	// XML 1.1: the version gate, the RestrictedChar reference/literal
	// split, and the §2.11 line ends. These reach the version11 paths,
	// which upstream has no equivalent of.
	`<?xml version="1.1"?><a>&#x7;</a>`,
	`<?xml version="1.0"?><a>&#x7;</a>`,
	"<?xml version=\"1.1\"?><a>\x07</a>",
	`<?xml version="1.1"?><a>&#0;</a>`,
	"<?xml version=\"1.1\"?><a>x\u0085y</a>",
	"<?xml version=\"1.1\"?><a>x\u2028y</a>",
	"<?xml version=\"1.1\"?><a>x\r\u0085y</a>",
	"<?xml version=\"1.1\"?><a>\u0085</a>",
	`<?xml version="1.1"?><a b="&#x1f;"/>`,
	`<?xml version="1.1"?><a><![CDATA[&#x7;]]></a>`,
	`<?xml version="1.2"?><a/>`,
	// A truncated declaration, and a stray <?xml?> after content, which
	// must not retroactively change the version.
	`<?xml version="1.1"`,
	`<?xml version="1.0"?><a/><?xml version="1.1"?>`,
}

// FuzzTokenNoPanic drives the tokeniser to exhaustion over arbitrary bytes.
// Reaching io.EOF or a *SyntaxError is a correct outcome; panicking is not,
// and neither is a token returned alongside a non-nil error.
func FuzzTokenNoPanic(f *testing.F) {
	for _, s := range tokenSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Token over %q panicked: %v", src, r)
			}
		}()
		d := NewDecoder(strings.NewReader(src))
		for n := 0; n < 10000; n++ {
			tok, err := d.Token()
			if err != nil {
				if tok != nil {
					t.Fatalf("Token over %q returned both %T and error %v", src, tok, err)
				}
				var se *SyntaxError
				if !errors.Is(err, io.EOF) && !errors.As(err, &se) {
					// Any other error is fine too, but it must not be nil
					// while also ending the loop.
					if err == nil {
						t.Fatalf("Token over %q ended with a nil error", src)
					}
				}
				return
			}
			if tok == nil {
				t.Fatalf("Token over %q returned nil token and nil error", src)
			}
			// CopyToken is what a caller retaining a token must use; it
			// reaches into every token's representation.
			if c := CopyToken(tok); c == nil {
				t.Fatalf("CopyToken of %T over %q returned nil", tok, src)
			}
		}
	})
}
