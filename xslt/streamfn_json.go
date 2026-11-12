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
// Validity and the error are the tree's: the writer raises xmlToJSON's
// error, and of several the one xmlToJSON meets first, which is not always
// the first in document order (jsonWriter); with a path, the matches'
// count's XPTY0004 comes before any (jsonFinish).
//
// The options must be a map constructor of string literal keys and literal
// values, its indent entry true() or false(): it is then known at compile
// time and cannot fail (the tree decodes it before reading the node). A
// match that may be a leaf, or a path that has a tail, is refused. Matches
// that may nest are matched below the first one too, as the writer reads
// its events (jsonWriter.m), and counted.

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/knroy/go-xml/v2/internal/xdmsink"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// jsonSplit says the split's matches are written as JSON (streamSplit.json).
type jsonSplit struct {
	indent bool
}

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
		s.nests && !s.jsonElems() && !s.jsonOne() {
		return "xml-to-json() of matches not each read once at its start tag"
	}
	s.mode, s.later, s.keepLeaves, s.leafOnly = matchText, false, false, false
	return ""
}

// jsonElems reports whether P's last step matches elements only: the writer
// then has only start tags to match below a match (jsonWriter.nested).
func (s *streamSplit) jsonElems() bool {
	leaf, elem := testKinds(s.steps[len(s.steps)-1].test)
	return elem && !leaf
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
	jw := &jsonWriter{c: r.c, indent: r.s.json.indent, ctx: r.ctx}
	if err := jw.context(v, d); err != nil {
		return err
	}
	if err := jw.charge(); err != nil {
		return err
	}
	return r.take(xdm.One(xdm.NewString(jw.b.String())))
}

// context is jsonContext's writing.
func (jw *jsonWriter) context(v *xdm.Node, d int) error {
	jw.d0 = d
	if d > 0 {
		// Its view: the name and attributes, as the start tag has them.
		return jw.element(xdmstream.Event{Kind: xdmstream.StartElement, Depth: d, Node: v}, 0)
	}
	jw.open = append(jw.open, jsonOpen{text: true})
	seen := false
	for {
		ev, err := jw.next()
		if err != nil {
			return err
		}
		switch ev.Kind {
		case xdmstream.EndDocument:
			if !seen {
				return errJSONOneChild()
			}
			return nil
		case xdmstream.StartElement:
			if seen {
				return jw.fail(errJSONOneChild(), 0)
			}
			seen = true
			if err := jw.element(ev, 0); err != nil {
				return err
			}
		case xdmstream.Text:
			if strings.TrimSpace(ev.Node.Value()) != "" {
				return jw.fail(jw.textErr(0), 0)
			}
		}
	}
}

// errJSONOneChild is xmlToJSON's for a document node with no element child
// or more than one.
func errJSONOneChild() error {
	return xdm.Errorf("FOJS0006", "a document node holding JSON XML must have one element child")
}

// jsonMatch writes a match, at its start tag ev, its frame f: the first; a
// later one is passed over, and an empty string stands for it. The tree
// evaluates the path before it reads any match as JSON, so a second match
// raises its XPTY0004 whatever the first holds: the first match's error waits
// in r.jsonErr, and finish raises one or the other (jsonFinish).
func (r *splitRun) jsonMatch(ev xdmstream.Event, f splitFrame) error {
	nests := r.s.nests && !r.s.jsonOne()
	if len(r.items) > 0 {
		if !nests {
			if err := r.c.Skip(); err != nil {
				return err
			}
		} else {
			// Its matches below it count too: read to its end.
			jw := &jsonWriter{c: r.c, d0: ev.Depth, m: r, frames: []splitFrame{f}}
			if err := jw.fail(nil, 0); err != nil {
				return err
			}
			r.jsonMore += jw.more
		}
		return r.take(xdm.One(xdm.NewString("")))
	}
	v, err := r.c.View()
	if err != nil {
		return err
	}
	ev.Node = v // the predicates may have read the cursor since
	jw := &jsonWriter{c: r.c, indent: r.s.json.indent, d0: ev.Depth, ctx: r.ctx}
	if nests {
		jw.m, jw.frames = r, []splitFrame{f}
	}
	err = jw.element(ev, 0)
	r.jsonMore += jw.more
	switch {
	case err != nil && err == jw.invalid:
		r.jsonErr = err
		return r.take(xdm.One(xdm.NewString("")))
	case err != nil:
		return err
	}
	if err := jw.charge(); err != nil {
		return err
	}
	return r.take(xdm.One(xdm.NewString(jw.b.String())))
}

// jsonLeaf takes a leaf match n: the tree's xml-to-json of it raises
// XPTY0004, unless a second match raises the count's first.
func (r *splitRun) jsonLeaf(n *xdm.Node) error {
	if len(r.items) == 0 {
		r.jsonErr = xdm.ErrType("fn:xml-to-json expects an element or document node, got %s", n.Kind())
	}
	return r.take(xdm.One(xdm.NewString("")))
}

// jsonFinish raises, for xml-to-json of a path, the error the tree raises
// first: the matches' count's, then the first match's.
func (r *splitRun) jsonFinish() error {
	if n := len(r.items) + r.jsonMore; n > 1 {
		return xdm.Errorf("XPTY0004", "a sequence of %d items is not allowed as the first argument of "+
			"fn:xml-to-json(), which is declared node()?", n)
	}
	return r.jsonErr
}

// jsonWriter writes the JSON text of elements read off c. With m, each
// element it reads is matched against the split's path as walk would match
// it, frames the open elements' (the written match's first), and more counts
// the matches.
//
// It checks what xmlToJSON checks, in xmlToJSON's order: there a map or an
// array (or a null, or the document node) has its whole content checked for
// text before any member is read, and a member's key before its name. So the
// first error met in document order is not always the tree's: text later in
// an open container is reported before it. fail reads on to the context's
// end for that, holding nothing but open, the elements open from the context
// down (the context first, at depth d0).
type jsonWriter struct {
	c      *xdmstream.Cursor
	b      jsonBuf
	indent bool
	m      *splitRun
	frames []splitFrame
	more   int
	d0     int
	open   []jsonOpen
	// invalid is the error fail settled on: the input is not the JSON
	// representation (not the cursor's or a predicate's).
	invalid error
	// ctx is the evaluation the text held whole is charged to (charge), and
	// charged what of it is billed; nil charges nothing.
	ctx     *xpath.Context
	charged int
}

// charge checks for cancellation and bills the text held whole since the
// last call to the byte budget, as xpath's xml-to-json does: indentation
// writes 2 x depth bytes per member, so the text is not bounded by the input
// that was charged as it was read. container calls it per member, so a
// refusal lands within one member's output of the bound. A flushed writer's
// pieces are charged as they are handed on.
func (jw *jsonWriter) charge() error {
	if jw.ctx == nil {
		return nil
	}
	if err := jw.ctx.Err(); err != nil {
		return err
	}
	if jw.b.flush != nil {
		return nil
	}
	n := jw.b.sb.Len() - jw.charged
	jw.charged = jw.b.sb.Len()
	return jw.ctx.ChargeBytes(n)
}

// jsonOpen is an open element of the representation: its local name, and
// whether text in it is an error (a map, an array, a null, the document).
type jsonOpen struct {
	name string
	text bool
}

// next is the cursor's next event; with m, a start tag is matched below the
// innermost open element, and an end tag closes its frame.
func (jw *jsonWriter) next() (xdmstream.Event, error) {
	ev, err := jw.c.Next()
	if err != nil || jw.m == nil {
		return ev, err
	}
	switch ev.Kind {
	case xdmstream.StartElement:
		at := splitAt{r: jw.m, n: ev.Node}
		f, match, err := jw.m.s.enter(&jw.frames[len(jw.frames)-1], &at)
		if err != nil {
			return ev, err
		}
		if match {
			jw.more++
		}
		jw.frames = append(jw.frames, f)
	case xdmstream.EndElement:
		if ev.Depth > jw.d0 {
			jw.frames = jw.frames[:len(jw.frames)-1]
		}
	}
	return ev, nil
}

// textErr is the error of text in open element i.
func (jw *jsonWriter) textErr(i int) error {
	if jw.d0 == 0 && i == 0 {
		return xdm.Errorf("FOJS0006", "unexpected text under a document node holding JSON XML")
	}
	return xdm.Errorf("FOJS0006", "element %s may not contain text", jw.open[i].name)
}

// fail settles on err, met with the elements open[:lim] still to check for
// text, as the tree checks them before it gets to err: it reads to the end of
// the context, and text in one of them that is open still replaces err with
// that element's error (the outermost's last). A document node's second
// element child replaces it too. With m, the matches below are counted.
func (jw *jsonWriter) fail(err error, lim int) error {
	open := len(jw.open)
	for {
		ev, e := jw.next()
		if e != nil {
			return e
		}
		switch ev.Kind {
		case xdmstream.EndDocument:
			jw.invalid = err
			return err
		case xdmstream.EndElement:
			i := ev.Depth - jw.d0
			if i == 0 {
				jw.invalid = err
				return err
			}
			open = min(open, i)
		case xdmstream.StartElement:
			if jw.d0 == 0 && ev.Depth == 1 && lim > 0 {
				err, lim = errJSONOneChild(), 0
			}
		case xdmstream.Text:
			if i := ev.Depth - 1 - jw.d0; i < open && i < lim && jw.open[i].text &&
				strings.TrimSpace(ev.Node.Value()) != "" {
				err, lim = jw.textErr(i), i
			}
		}
	}
}

// element writes the element whose start tag ev is, at JSON depth jd, and
// reads to its end tag.
func (jw *jsonWriter) element(ev xdmstream.Event, jd int) error {
	n, k := ev.Node, len(jw.open)
	if n.Name().URI != fnNS {
		return jw.fail(xdm.Errorf("FOJS0006", "element %s is not in the XPath functions namespace", n.Name().Local), k)
	}
	for a := range n.Attrs() {
		switch q := a.Name(); {
		case q.URI == fnNS:
			return jw.fail(xdm.Errorf("FOJS0006", "attribute %s is not allowed in the JSON namespace", q.Local), k)
		case q.URI != "":
		case q.Local == "key", q.Local == "escaped-key", q.Local == "escaped":
			// Read where xmlToJSON reads them: escaped on a string,
			// escaped-key on a map's member (key).
		default:
			return jw.fail(xdm.Errorf("FOJS0006", "attribute %q is not allowed on element %s", q.Local, n.Name().Local), k)
		}
	}
	kind := n.Name().Local
	switch kind {
	case "null", "boolean", "number", "string", "array", "map":
	default:
		return jw.fail(xdm.Errorf("FOJS0006", "element %s is not part of the JSON XML representation", kind), k)
	}
	jw.open = append(jw.open, jsonOpen{name: kind, text: kind == "null" || kind == "array" || kind == "map"})
	var err error
	switch kind {
	case "null":
		err = jw.null(k)
	case "array", "map":
		err = jw.container(kind == "map", k, jd)
	default:
		var txt string
		if txt, err = jw.leafText(k); err == nil {
			if err = jw.leaf(n, kind, txt); err != nil && k > 0 {
				err = jw.fail(err, k)
			} else if err != nil {
				jw.invalid = err // the context, read to its end tag
			}
		}
	}
	jw.open = jw.open[:k]
	return err
}

// null reads a null, open element k, to its end tag: text in it is an
// error, and so, unless there is text, is an element.
func (jw *jsonWriter) null(k int) error {
	for {
		ev, err := jw.next()
		if err != nil {
			return err
		}
		switch ev.Kind {
		case xdmstream.Text:
			if strings.TrimSpace(ev.Node.Value()) != "" {
				return jw.fail(jw.textErr(k), k)
			}
		case xdmstream.StartElement:
			return jw.fail(xdm.Errorf("FOJS0006", "element null must be empty"), k+1)
		case xdmstream.EndElement:
			jw.b.WriteString("null")
			return nil
		}
	}
}

// leafText reads a leaf, open element k, to its end tag: its text, comments
// and processing instructions passed over; an element child is not allowed.
func (jw *jsonWriter) leafText(k int) (string, error) {
	var b strings.Builder
	for {
		ev, err := jw.next()
		if err != nil {
			return "", err
		}
		switch ev.Kind {
		case xdmstream.Text:
			b.WriteString(ev.Node.Value())
		case xdmstream.StartElement:
			return "", jw.fail(xdm.Errorf("FOJS0006", "element %s may not have element children", jw.open[k].name), k)
		case xdmstream.EndElement:
			return b.String(), nil
		}
	}
}

// leaf writes a boolean, number or string n of text txt, or returns
// xmlToJSON's error for it.
func (jw *jsonWriter) leaf(n *xdm.Node, kind, txt string) error {
	t := strings.TrimSpace(txt)
	switch kind {
	case "boolean":
		v, ok := jsonBool(txt)
		if !ok {
			return xdm.Errorf("FOJS0006", "%q is not a valid boolean", txt)
		}
		jw.b.WriteString(strconv.FormatBool(v))
	case "number":
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return xdm.Errorf("FOJS0006", "%q is not a valid number", t)
		}
		if f != f || f > 1.7976931348623157e308 || f < -1.7976931348623157e308 {
			return xdm.Errorf("FOJS0006", "%q has no JSON representation", t)
		}
		jw.b.WriteString(xdm.NewDouble(f).String())
	case "string":
		esc, err := jsonBoolAttr(n, "escaped")
		if err != nil {
			return err
		}
		return jsonLiteral(&jw.b, txt, esc)
	}
	return nil
}

// container writes a map or an array, open element k, whose start tag is
// the current event, its members at JSON depth jd+1.
func (jw *jsonWriter) container(isMap bool, k, jd int) error {
	open, end := byte('['), byte(']')
	if isMap {
		open, end = '{', '}'
	}
	jw.b.WriteByte(open)
	seen := map[string]bool{}
	kids := 0
	for {
		ev, err := jw.next()
		if err != nil {
			return err
		}
		switch ev.Kind {
		case xdmstream.Text:
			if strings.TrimSpace(ev.Node.Value()) != "" {
				return jw.fail(jw.textErr(k), k)
			}
		case xdmstream.EndElement:
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
				if err := jw.key(ev.Node, seen); err != nil {
					return jw.fail(err, k+1)
				}
			}
			if err := jw.element(ev, jd+1); err != nil {
				return err
			}
			jw.b.spill(false)
			if err := jw.charge(); err != nil {
				return err
			}
		}
	}
}

// key writes the key of a map's member n and its colon, or returns
// xmlToJSON's error for it: it is there, its escaped-key is a boolean, its
// escapes are, no other member has it.
func (jw *jsonWriter) key(n *xdm.Node, seen map[string]bool) error {
	k, ok := jsonAttr(n, "key")
	if !ok {
		return xdm.Errorf("FOJS0006", "every child of a map element must have a key attribute")
	}
	esc, err := jsonBoolAttr(n, "escaped-key")
	if err != nil {
		return err
	}
	plain := k
	if esc {
		if plain, err = jsonUnescape(k); err != nil {
			return err
		}
	}
	if seen[plain] {
		return xdm.Errorf("FOJS0006", "duplicate key %q in a map element", plain)
	}
	seen[plain] = true
	if err := jsonLiteral(&jw.b, k, esc); err != nil {
		return err
	}
	jw.b.WriteByte(':')
	if jw.indent {
		jw.b.WriteByte(' ')
	}
	return nil
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

// jsonBoolAttr reads n's escaped or escaped-key, as xpath's jsonBooleanAttr
// does: false when it is absent.
func jsonBoolAttr(n *xdm.Node, local string) (bool, error) {
	v, has := jsonAttr(n, local)
	if !has {
		return false, nil
	}
	b, ok := jsonBool(v)
	if !ok {
		return false, xdm.Errorf("FOJS0006", "%q is not a valid xs:boolean for the %s attribute", v, local)
	}
	return b, nil
}

// jsonLiteral writes s as a quoted JSON string, as xpath's
// jsonStringLiteral does: with escaped, its escapes checked and copied as
// written. The error is jsonStringLiteral's.
func jsonLiteral(b *jsonBuf, s string, escaped bool) error {
	b.WriteByte('"')
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if escaped && c == '\\' {
			if i+1 >= len(rs) {
				return errJSONEndSlash()
			}
			switch rs[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				b.WriteRune(c)
				b.WriteRune(rs[i+1])
				i++
				continue
			case 'u':
				if i+5 >= len(rs) {
					return errJSONShortU()
				}
				for j := i + 2; j < i+6; j++ {
					if !isHexRune(rs[j]) {
						return xdm.Errorf("FOJS0007", "%q is not a hexadecimal digit in a \\u escape", string(rs[j]))
					}
				}
				for j := i; j < i+6; j++ {
					b.WriteRune(rs[j])
				}
				i += 5
				continue
			}
			return xdm.Errorf("FOJS0007", "invalid escape sequence \\%s", string(rs[i+1]))
		}
		jsonChar(b, c)
	}
	b.WriteByte('"')
	return nil
}

// errJSONEndSlash and errJSONShortU are xpath's escape errors.
func errJSONEndSlash() error { return xdm.Errorf("FOJS0007", "a string may not end with a backslash") }

func errJSONShortU() error {
	return xdm.Errorf("FOJS0007", "a \\u escape needs four hexadecimal digits")
}

func isHexRune(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// jsonChar writes one character of a string, escaped as xpath's
// writeJSONChar escapes it.
func jsonChar(b *jsonBuf, c rune) {
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
// unescapeJSONString does, with its error.
func jsonUnescape(s string) (string, error) {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\\' {
			b.WriteRune(rs[i])
			continue
		}
		if i+1 >= len(rs) {
			return "", errJSONEndSlash()
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
				return "", errJSONShortU()
			}
			v, err := strconv.ParseInt(string(rs[i+2:i+6]), 16, 32)
			if err != nil {
				return "", xdm.Errorf("FOJS0007", "invalid \\u escape")
			}
			b.WriteRune(rune(v))
			i += 4
		default:
			return "", xdm.Errorf("FOJS0007", "invalid escape sequence \\%s", string(rs[i+1]))
		}
		i++
	}
	return b.String(), nil
}

// jsonBuf is what a jsonWriter writes: held whole, or, with flush, handed
// on in pieces of whole characters, about jsonChunk bytes each (spill).
type jsonBuf struct {
	sb    strings.Builder
	p     []byte
	flush func([]byte)
}

const jsonChunk = 32 << 10

func (j *jsonBuf) WriteByte(c byte) error {
	if j.flush != nil {
		j.p = append(j.p, c)
		return nil
	}
	return j.sb.WriteByte(c)
}

func (j *jsonBuf) WriteString(s string) {
	if j.flush != nil {
		j.p = append(j.p, s...)
		return
	}
	j.sb.WriteString(s)
}

func (j *jsonBuf) WriteRune(r rune) {
	if j.flush != nil {
		j.p = utf8.AppendRune(j.p, r)
		return
	}
	j.sb.WriteRune(r)
}

func (j *jsonBuf) String() string { return j.sb.String() }

// spill hands on what is written, all of it or once it is a chunk.
func (j *jsonBuf) spill(all bool) {
	if j.flush != nil && len(j.p) > 0 && (all || len(j.p) >= jsonChunk) {
		j.flush(j.p)
		j.p = j.p[:0]
	}
}

// jsonWhole reports whether op is xml-to-json(.) and nothing else: the
// context's JSON text is the whole of its value (zero-or-one of one string).
func jsonWhole(op *streamOp) bool {
	s := op.split
	if s == nil || s.json == nil || !s.ctxItem || s.cond != nil || s.kids || len(s.checks) > 0 ||
		s.red != "" || s.docCopy || op.tee != nil || op.eventPop || op.inGroup || copiedPopulation(op) {
		return false
	}
	f, ok := s.outer.Expr().(*xpath.FuncCall)
	return ok && f.Name.URI == fnNS && f.Name.Local == "zero-or-one" && len(f.Args) == 1 &&
		isVarRef(f.Args[0], s.outerVar)
}

// jsonOut is xsl:value-of select="xml-to-json(.)" (jsonWhole) writing into
// out, when out can take its text as it is made (xdmsink.Text: the text
// method, nothing held), which then is never held whole (audit A3-5). The
// bytes are charged as the value-of charges its text, piece by piece; a
// charge refused stops the writing, and the error is raised once the JSON
// is read, unless reading it raises one first, as the tree's xml-to-json
// would before the value-of. What was written before an error stays
// written (TransformOptions.Output).
func (run *streamRun) jsonOut(rt *runtime, op *streamOp, out *outputBuilder) (bool, error) {
	if run.pre != nil || run.used || run.groupAt(op) != nil {
		return false, nil // evalOp's
	}
	w := xdmsink.Text(out)
	if w == nil {
		return false, nil
	}
	run.used = true
	var v *xdm.Node
	if run.d > 0 {
		var err error
		if v, err = run.c.View(); err != nil {
			return true, err
		}
	}
	var cerr error
	jw := &jsonWriter{c: run.c, indent: op.split.json.indent, ctx: rt.ctx}
	jw.b.flush = func(p []byte) {
		if cerr == nil {
			cerr = rt.ctx.ChargeBytes(len(p))
		}
		if cerr == nil {
			w(p)
		}
	}
	if err := jw.context(v, run.d); err != nil {
		return true, err
	}
	jw.b.spill(true)
	return true, cerr
}
