package xdm

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/fileuri"
)

// TestDTDDeclarationGrammar holds the internal subset to the markup
// declaration grammar of XML 1.0 §2.8 and §3.2-§4.7 (X3). Each refused
// subset was accepted before: the package read a DTD for what it needed and
// skipped the rest, and §5.1 requires the whole internal subset checked for
// well-formedness by every processor.
func TestDTDDeclarationGrammar(t *testing.T) {
	bad := map[string]string{
		"element without space":   `<!ELEMENTr EMPTY>`,
		"element no model":        `<!ELEMENT r>`,
		"element bad keyword":     `<!ELEMENT r EMTPY>`,
		"mixed separator":         `<!ELEMENT r (a|b,c)>`,
		"mixed without star":      `<!ELEMENT r (#PCDATA|a)>`,
		"pcdata not first":        `<!ELEMENT r (a|#PCDATA)*>`,
		"empty group":             `<!ELEMENT r ()>`,
		"attlist missing default": `<!ATTLIST r a CDATA>`,
		"attlist bad type":        `<!ATTLIST r a STRING #IMPLIED>`,
		"attlist no space":        `<!ATTLIST r a CDATA #IMPLIEDb CDATA #IMPLIED>`,
		"fixed without value":     `<!ATTLIST r a CDATA #FIXED>`,
		"attribute default <":     `<!ATTLIST r a CDATA "<">`,
		"entity no value":         `<!ENTITY e>`,
		"entity unquoted":         `<!ENTITY e value>`,
		"pe with ndata":           `<!ENTITY % e SYSTEM "e" NDATA n>`,
		"pe ref in value":         `<!ENTITY e "%p;">`,
		"pe ref in declaration":   `<!ENTITY % p "CDATA"><!ATTLIST r a %p; #IMPLIED>`,
		"bare ampersand":          `<!ENTITY e "a & b">`,
		"public without system":   `<!ENTITY e PUBLIC "p">`,
		"bad pubid char":          `<!ENTITY e PUBLIC "a{b" "s">`,
		"notation no id":          `<!NOTATION n>`,
		"unknown declaration":     `<!ELEMENTS r EMPTY>`,
		"conditional in internal": `<![INCLUDE[<!ELEMENT r EMPTY>]]>`,
		"stray text":              `hello`,
		"stray percent":           `% p;`,
		"reserved pi":             `<?xml version="1.0"?>`,
		"trailing garbage":        `<!ELEMENT r EMPTY extra>`,
		"noncharacter in a pe":    "<!ENTITY % e \"\uFFFF\">",
		"control in an attlist":   "<!ATTLIST r a CDATA \"\x01\">",
	}
	for name, subset := range bad {
		_, err := ParseString("<!DOCTYPE r ["+subset+"]><r/>", ParseOptions{AllowDOCTYPE: true})
		if err == nil || !strings.Contains(err.Error(), "malformed DTD") {
			t.Errorf("%s: %s: err = %v, want malformed DTD", name, subset, err)
		}
	}
	for _, d := range []string{`DOCTYPEr`, `DOCTYPE`, `DOCTYPE r SYSTEM`, `DOCTYPE r PUBLIC "p"`, `DOCTYPE r [] x`} {
		if _, err := ParseString("<!"+d+"><r/>", ParseOptions{AllowDOCTYPE: true}); err == nil {
			t.Errorf("<!%s>: accepted", d)
		}
	}
	good := []string{
		`<!ELEMENT r EMPTY><!ELEMENT a ANY><!ELEMENT b (#PCDATA)><!ELEMENT c (#PCDATA)*>`,
		`<!ELEMENT d ( #PCDATA | a | b )*><!ELEMENT e ((a|b)+,c?,(d*))*>`,
		`<!ATTLIST r a CDATA #IMPLIED b ID #REQUIRED c (x|y|1) "x" d NOTATION (n) #IMPLIED e CDATA #FIXED 'v&amp;&#38;&#x26;'>`,
		`<!ATTLIST r>`,
		`<!ENTITY e "v"><!ENTITY f SYSTEM "f.xml"><!ENTITY g PUBLIC "-//A//B" 'g'><!ENTITY h SYSTEM "h" NDATA n>`,
		`<!ENTITY % p "x"> %p; <!ENTITY % q SYSTEM "q">`,
		`<!NOTATION n SYSTEM "n"><!NOTATION m PUBLIC "m"><!NOTATION o PUBLIC "o" "o">`,
		`<?pi data?><?pi?><!-- c -->`,
		"\n\t<!ELEMENT\nr\tEMPTY\n>\n",
	}
	for _, subset := range good {
		_, err := ParseString("<!DOCTYPE r ["+subset+"]><r/>", ParseOptions{AllowDOCTYPE: true})
		if err != nil && strings.Contains(err.Error(), "malformed DTD") {
			t.Errorf("%s: %v", subset, err)
		}
	}
	for _, d := range []string{`DOCTYPE r`, `DOCTYPE r[]`, `DOCTYPE r SYSTEM "r" `, `DOCTYPE r PUBLIC "p" 's' [ ] `} {
		_, err := ParseString("<!"+d+"><r/>", ParseOptions{AllowDOCTYPE: true})
		if err != nil && strings.Contains(err.Error(), "malformed DTD") {
			t.Errorf("<!%s>: %v", d, err)
		}
	}
}

// TestExternalSubsetGrammar: an external subset this package reads is held
// to [31] extSubsetDecl, and its text declaration to [77] TextDecl, while
// what only a parameter-entity reference can complete is left alone.
func TestExternalSubsetGrammar(t *testing.T) {
	for name, c := range map[string]struct {
		dtd string
		ok  bool
	}{
		"conditional sections":     {`<![INCLUDE[<!ELEMENT r EMPTY><![IGNORE[ junk <![ x ]]> ]]>]]>`, true},
		"keyword from a reference": {`<!ENTITY % k "INCLUDE"><![%k;[<!ELEMENT r EMPTY>]]>`, true},
		"reference in a decl":      {`<!ENTITY % t "CDATA"><!ATTLIST r a %t; #IMPLIED>`, true},
		"reference ends a decl":    {`<!ENTITY % e ">"><!ELEMENT r EMPTY %e;`, true},
		"text declaration":         {`<?xml encoding="UTF-8"?><!ELEMENT r EMPTY>`, true},
		"bad declaration":          {`<!ELEMENT r EMTPY>`, false},
		"unterminated include":     {`<![INCLUDE[<!ELEMENT r EMPTY>`, false},
		"standalone in text decl":  {`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><!ELEMENT r EMPTY>`, false},
		"text decl, no encoding":   {`<?xml version="1.0"?><!ELEMENT r EMPTY>`, false},
		"text decl not first":      {` <?xml encoding="UTF-8"?><!ELEMENT r EMPTY>`, false},
	} {
		dir := writeFiles(t, map[string]string{"r.dtd": c.dtd})
		_, err := ParseString(`<!DOCTYPE r SYSTEM "r.dtd"><r/>`, ParseOptions{
			AllowDOCTYPE: true, ExternalEntities: &dirResolver{root: dir},
			BaseURI: fileuri.Of(filepath.Join(dir, "doc.xml")),
		})
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok %v", name, err, c.ok)
		}
	}
}
