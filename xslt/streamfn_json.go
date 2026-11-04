package xslt

// Lane L2: fn:xml-to-json of a streamed node, written from the events.
//
// xml-to-json(N) of the context item (the document node or an element) or
// of a path of child steps from it is read off the cursor: the writer
// follows the element's events and writes the JSON text as xpath's
// xmlToJSON (fn_json.go) writes it from a tree, a map's keys held for the
// duplicate check and a leaf's text until its end tag -- what the tree
// holds of them and less, never the document. The result is the string the
// tree's call returns.
//
// Validity is what the tree's is; the error is not: any element that is not
// the JSON representation ends the run with errJSONStream, and the core
// runs the container on the tree, which raises its own error exactly
// (streamexec.go: an error the tree also raises is the tree's). So the
// writer must reject exactly what xmlToJSON rejects, and accept the rest
// with byte-identical text; the order of the checks does not matter.
//
// The options must be a map constructor of string literal keys and literal
// values, its indent entry true() or false(): it is then known at compile
// time and cannot fail (the tree decodes it before reading the node). The
// outer is zero-or-one($items): a path with a second match fails there,
// and the tree raises its XPTY0004. A match that may be a leaf, a path whose
// matches may nest or that has a tail, are refused.

import (
	"errors"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// jsonSplit says the split's matches are written as JSON (streamSplit.json).
type jsonSplit struct {
	indent bool
}

// errJSONStream ends a streamed xml-to-json whose input is not the JSON
// representation: the tree's run then raises the error.
var errJSONStream = errors.New("xml-to-json: not the JSON representation of a value")

// jsonCall is walk's case for fn:xml-to-json(N [, options]).
func (w *splitter) jsonCall(x *xpath.FuncCall, put func(xpath.Expr), use splitUse) (done, ok bool) {
	if x.Name.URI != fnNS || x.Name.Local != "xml-to-json" || len(x.Args) < 1 || len(x.Args) > 2 {
		return false, false
	}
	indent := false
	if len(x.Args) == 2 {
		var lit bool
		if indent, lit = jsonIndentOption(x.Args[1]); !lit {
			return true, w.refuse("xml-to-json() options that are not literal")
		}
	}
	if w.pe != nil {
		return true, w.refuse("a second consuming operand")
	}
	if w.group {
		return true, w.refuse("xml-to-json() in a group's body")
	}
	wrap := func(y xpath.Expr) {
		put(&xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "zero-or-one"}, Args: []xpath.Expr{y}})
	}
	var pp splitPathParts
	if _, ctx := x.Args[0].(*xpath.ContextItem); ctx {
		pp.ctxItem = true
	} else {
		var found bool
		if pp, found = w.pathOf(x.Args[0]); !found || pp.ctxItem || len(pp.tail) > 0 || pp.br != nil ||
			pp.mapR != nil || pp.skip > 0 || pp.oi != "" || pp.group {
			return true, w.refuse("xml-to-json() of " + x.Args[0].String())
		}
	}
	w.pe, w.put, w.use, w.path, w.json = x.Args[0], wrap, use, pp, &jsonSplit{indent: indent}
	return true, true
}

// jsonIndentOption reads a literal options map: its indent entry, and
// whether it is one (string literal keys, distinct; literal values; indent
// true() or false()).
func jsonIndentOption(e xpath.Expr) (indent, ok bool) {
	m, isMap := e.(*xpath.MapConstructor)
	if !isMap || len(m.Keys) != len(m.Values) {
		return false, false
	}
	seen := map[string]bool{}
	for i, k := range m.Keys {
		l, isLit := k.(*xpath.Literal)
		if !isLit || l.Val == nil || l.Val.Type != xdm.TypeString || seen[l.Val.String()] {
			return false, false
		}
		seen[l.Val.String()] = true
		v := m.Values[i]
		if l.Val.String() == "indent" {
			f, isCall := v.(*xpath.FuncCall)
			if !isCall || f.Name.URI != fnNS || len(f.Args) != 0 || f.Name.Local != "true" && f.Name.Local != "false" {
				return false, false
			}
			indent = f.Name.Local == "true"
			continue
		}
		if _, isLit := v.(*xpath.Literal); !isLit {
			return false, false
		}
	}
	return indent, true
}

// jsonAfter checks, once path has set s up, that each match is read once at
// its start tag: no tail or reducer; matches that cannot nest, or one match
// at most (a single step's [1] from the one context), which nothing after
// it can be. The matches are then written in matchText's place (walk), the
// context by run. A leaf match is not an element or document node: its run
// ends (leaf), and the tree raises the XPTY0004.
func (s *streamSplit) jsonAfter(w *splitter) string {
	s.json = w.json
	if s.ctxItem {
		return ""
	}
	if s.tail != nil || s.tailText != "" || s.red != "" || s.outermost || s.innermost || s.group ||
		s.skip != 0 || s.hasBranches() || w.kids || s.mode != matchText && s.mode != matchGround ||
		s.nests && !s.jsonOne() {
		return "xml-to-json() of matches not each read once at its start tag"
	}
	s.mode, s.later, s.keepLeaves, s.leafOnly = matchText, false, false, false
	return ""
}

// jsonOne reports whether P is one step with a positional [1]: one match
// at most, from the one context.
func (s *streamSplit) jsonOne() bool {
	if len(s.steps) != 1 {
		return false
	}
	for _, p := range s.steps[0].preds {
		if p.e == nil && p.pe == nil && p.at == 1 {
			return true
		}
	}
	return false
}

// jsonContext writes the context: the document node (d 0, before its first
// event) or the element at the start tag Next returned (d >= 1). It reads
// to the context's end.
func (r *splitRun) jsonContext(v *xdm.Node, d int) error {
	jw := &jsonWriter{c: r.c, indent: r.s.json.indent}
	if d > 0 {
		// Its view: the name and attributes, as the start tag has them.
		if err := jw.element(xdmstream.Event{Kind: xdmstream.StartElement, Depth: d, Node: v}, 0); err != nil {
			return err
		}
		return r.take(xdm.One(xdm.NewString(jw.b.String())))
	}
	seen := false
	for {
		ev, err := r.c.Next()
		if err != nil {
			return err
		}
		switch ev.Kind {
		case xdmstream.EndDocument:
			if !seen {
				return errJSONStream
			}
			return r.take(xdm.One(xdm.NewString(jw.b.String())))
		case xdmstream.StartElement:
			if seen {
				return errJSONStream
			}
			seen = true
			if err := jw.element(ev, 0); err != nil {
				return err
			}
		case xdmstream.Text:
			if strings.TrimSpace(ev.Node.Value()) != "" {
				return errJSONStream
			}
		}
	}
}

// jsonMatch writes a match, at its start tag ev: the first; a later one is
// skipped, and an empty string stands for it, which the outer's
// zero-or-one refuses.
func (r *splitRun) jsonMatch(ev xdmstream.Event) error {
	if len(r.items) > 0 {
		if err := r.c.Skip(); err != nil {
			return err
		}
		return r.take(xdm.One(xdm.NewString("")))
	}
	v, err := r.c.View()
	if err != nil {
		return err
	}
	ev.Node = v // the predicates may have read the cursor since
	jw := &jsonWriter{c: r.c, indent: r.s.json.indent}
	if err := jw.element(ev, 0); err != nil {
		return err
	}
	return r.take(xdm.One(xdm.NewString(jw.b.String())))
}

// jsonWriter writes the JSON text of elements read off c.
type jsonWriter struct {
	c      *xdmstream.Cursor
	b      strings.Builder
	indent bool
}

// element writes the element whose start tag ev is, at JSON depth jd, and
// reads to its end tag.
func (jw *jsonWriter) element(ev xdmstream.Event, jd int) error {
	n, d := ev.Node, ev.Depth
	if n.Name().URI != fnNS {
		return errJSONStream
	}
	for a := range n.Attrs() {
		switch q := a.Name(); {
		case q.URI == fnNS:
			return errJSONStream
		case q.URI != "":
		case q.Local == "key", q.Local == "escaped-key", q.Local == "escaped":
			// Read where xmlToJSON reads them: escaped on a string,
			// escaped-key on a map's member (key).
		default:
			return errJSONStream
		}
	}
	switch kind := n.Name().Local; kind {
	case "null", "boolean", "number", "string":
		esc := false
		if v, has := jsonAttr(n, "escaped"); has && kind == "string" {
			var ok bool
			if esc, ok = jsonBool(v); !ok {
				return errJSONStream
			}
		}
		txt, err := jw.leafText(d)
		if err != nil {
			return err
		}
		return jw.leaf(kind, txt, esc)
	case "array", "map":
		return jw.container(kind == "map", d, jd)
	}
	return errJSONStream
}

// leafText reads a leaf's content to its end tag: its text, comments and
// processing instructions passed over; an element child is not allowed.
func (jw *jsonWriter) leafText(d int) (string, error) {
	var b strings.Builder
	for {
		ev, err := jw.c.Next()
		if err != nil {
			return "", err
		}
		switch ev.Kind {
		case xdmstream.Text:
			b.WriteString(ev.Node.Value())
		case xdmstream.StartElement:
			return "", errJSONStream
		case xdmstream.EndElement:
			if ev.Depth == d {
				return b.String(), nil
			}
		case xdmstream.EndDocument:
			return "", errJSONStream
		}
	}
}

// leaf writes a null, boolean, number or string of text txt.
func (jw *jsonWriter) leaf(kind, txt string, esc bool) error {
	t := strings.TrimSpace(txt)
	switch kind {
	case "null":
		if t != "" {
			return errJSONStream
		}
		jw.b.WriteString("null")
	case "boolean":
		v, ok := jsonBool(txt)
		if !ok {
			return errJSONStream
		}
		jw.b.WriteString(strconv.FormatBool(v))
	case "number":
		f, err := strconv.ParseFloat(t, 64)
		if err != nil || f != f || f > 1.7976931348623157e308 || f < -1.7976931348623157e308 {
			return errJSONStream
		}
		jw.b.WriteString(xdm.NewDouble(f).String())
	case "string":
		if !jsonLiteral(&jw.b, txt, esc) {
			return errJSONStream
		}
	}
	return nil
}

// container writes a map or an array whose start tag, at depth d, is the
// current event, its members at JSON depth jd+1.
func (jw *jsonWriter) container(isMap bool, d, jd int) error {
	open, end := byte('['), byte(']')
	if isMap {
		open, end = '{', '}'
	}
	jw.b.WriteByte(open)
	seen := map[string]bool{}
	kids := 0
	for {
		ev, err := jw.c.Next()
		if err != nil {
			return err
		}
		switch ev.Kind {
		case xdmstream.Text:
			if strings.TrimSpace(ev.Node.Value()) != "" {
				return errJSONStream
			}
		case xdmstream.EndDocument:
			return errJSONStream
		case xdmstream.EndElement:
			if ev.Depth != d {
				return errJSONStream // cannot happen: a member is read to its end
			}
			if kids > 0 {
				jw.newline(jd)
			}
			jw.b.WriteByte(end)
			return nil
		case xdmstream.StartElement:
			if kids > 0 {
				jw.b.WriteByte(',')
			}
			kids++
			jw.newline(jd + 1)
			if isMap {
				if !jw.key(ev.Node, seen) {
					return errJSONStream
				}
			}
			if err := jw.element(ev, jd+1); err != nil {
				return err
			}
		}
	}
}

// key writes the key of a map's member n and its colon, checking it is
// there, its escaped-key, and that no other member has it.
func (jw *jsonWriter) key(n *xdm.Node, seen map[string]bool) bool {
	k, ok := jsonAttr(n, "key")
	if !ok {
		return false
	}
	esc := false
	if v, has := jsonAttr(n, "escaped-key"); has {
		if esc, ok = jsonBool(v); !ok {
			return false
		}
	}
	plain := k
	if esc {
		if plain, ok = jsonUnescape(k); !ok {
			return false
		}
	}
	if seen[plain] {
		return false
	}
	seen[plain] = true
	if !jsonLiteral(&jw.b, k, esc) {
		return false
	}
	jw.b.WriteByte(':')
	if jw.indent {
		jw.b.WriteByte(' ')
	}
	return true
}

func (jw *jsonWriter) newline(depth int) {
	if !jw.indent {
		return
	}
	jw.b.WriteByte('\n')
	for range depth {
		jw.b.WriteString("  ")
	}
}

// jsonAttr is the value of n's no-namespace attribute local.
func jsonAttr(n *xdm.Node, local string) (string, bool) {
	for a := range n.Attrs() {
		if a.Name().URI == "" && a.Name().Local == local {
			return a.Value(), true
		}
	}
	return "", false
}

// jsonBool reads an xs:boolean of escaped, escaped-key or a boolean's text.
func jsonBool(v string) (val, ok bool) {
	switch strings.TrimSpace(v) {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return false, false
}

// jsonLiteral writes s as a quoted JSON string, as xpath's
// jsonStringLiteral does: with escaped, its escapes checked and copied as
// written.
func jsonLiteral(b *strings.Builder, s string, escaped bool) bool {
	b.WriteByte('"')
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if escaped && c == '\\' {
			if i+1 >= len(rs) {
				return false
			}
			switch rs[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				b.WriteRune(c)
				b.WriteRune(rs[i+1])
				i++
				continue
			case 'u':
				if i+5 >= len(rs) {
					return false
				}
				for j := i + 2; j < i+6; j++ {
					if !isHexRune(rs[j]) {
						return false
					}
				}
				for j := i; j < i+6; j++ {
					b.WriteRune(rs[j])
				}
				i += 5
				continue
			}
			return false
		}
		jsonChar(b, c)
	}
	b.WriteByte('"')
	return true
}

func isHexRune(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// jsonChar writes one character of a string, escaped as xpath's
// writeJSONChar escapes it.
func jsonChar(b *strings.Builder, c rune) {
	switch c {
	case '"':
		b.WriteString("\\\"")
	case '\\':
		b.WriteString("\\\\")
	case '/':
		b.WriteString("\\/")
	case 0x08:
		b.WriteString("\\b")
	case 0x09:
		b.WriteString("\\t")
	case 0x0A:
		b.WriteString("\\n")
	case 0x0C:
		b.WriteString("\\f")
	case 0x0D:
		b.WriteString("\\r")
	default:
		if c < 0x20 || c >= 0x7F && c <= 0x9F {
			h := strings.ToUpper(strconv.FormatInt(int64(c), 16))
			b.WriteString("\\u" + strings.Repeat("0", max(0, 4-len(h))) + h)
			return
		}
		b.WriteRune(c)
	}
}

// jsonUnescape decodes a key's escapes for the duplicate check, as xpath's
// unescapeJSONString does; false where that fails.
func jsonUnescape(s string) (string, bool) {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\\' {
			b.WriteRune(rs[i])
			continue
		}
		if i+1 >= len(rs) {
			return "", false
		}
		switch rs[i+1] {
		case '"', '\\', '/':
			b.WriteRune(rs[i+1])
		case 'b':
			b.WriteRune(0x08)
		case 'f':
			b.WriteRune(0x0C)
		case 'n':
			b.WriteRune(0x0A)
		case 'r':
			b.WriteRune(0x0D)
		case 't':
			b.WriteRune(0x09)
		case 'u':
			if i+5 >= len(rs) {
				return "", false
			}
			v, err := strconv.ParseInt(string(rs[i+2:i+6]), 16, 32)
			if err != nil {
				return "", false
			}
			b.WriteRune(rune(v))
			i += 4
		default:
			return "", false
		}
		i++
	}
	return b.String(), true
}
