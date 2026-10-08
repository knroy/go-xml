package main

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/knroy/go-xml/c14n"
	"github.com/knroy/go-xml/xdm"
)

// runHelper is the cold-mode process for the parse and c14n areas, which the
// go-xml CLI has no subcommand for: benchrun re-executes itself as
// "benchrun -helper ENGINE SOURCE OUT".
func runHelper(engine, src, out string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	switch engine {
	case "go-xml":
		err = goxmlParse(data, w)
	case "encoding-xml":
		err = stdlibParse(data, w)
	default:
		err = fmt.Errorf("no helper for engine %q", engine)
	}
	if err != nil {
		return err
	}
	return w.Flush()
}

// goxmlParse parses with xdm and writes Canonical XML 1.0, as xmllint --c14n does.
func goxmlParse(data []byte, w io.Writer) error {
	tree, err := xdm.ParseString(string(data), xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		return err
	}
	return c14n.Write(w, tree.Root, c14n.Options{Algorithm: c14n.Inclusive10})
}

// stdlibParse tokenises with encoding/xml and re-emits the tokens as XML with
// prefixes as written, which the xml-c14n rule can then compare.
//
// ponytail: RawToken keeps prefixes (Token rewrites them to URIs) but does not
// check that end tags match; a baseline for parse speed, not a validator.
func stdlibParse(data []byte, w io.Writer) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	name := func(n xml.Name) string {
		if n.Space != "" {
			return n.Space + ":" + n.Local
		}
		return n.Local
	}
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			fmt.Fprintf(w, "<%s", name(t.Name))
			for _, a := range t.Attr {
				fmt.Fprintf(w, " %s=\"", name(a.Name))
				escape(w, a.Value, true)
				io.WriteString(w, `"`)
			}
			io.WriteString(w, ">")
		case xml.EndElement:
			fmt.Fprintf(w, "</%s>", name(t.Name))
		case xml.CharData:
			escape(w, string(t), false)
		case xml.ProcInst:
			if t.Target != "xml" {
				fmt.Fprintf(w, "<?%s %s?>", t.Target, t.Inst)
			}
		}
	}
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;",
		"\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
)

func escape(w io.Writer, s string, attr bool) {
	if attr {
		attrEscaper.WriteString(w, s)
	} else {
		textEscaper.WriteString(w, s)
	}
}
