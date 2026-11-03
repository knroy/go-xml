package xslt

// Validating a streamed document as the cursor reads it (lane H1): what
// assessSource does to the tree of an xsl:source-document or a merge
// source, and what the conformance harness does to a principal input it
// gives as validated.
//
// The validator (xsd, through internal/xsdstream) is the cursor's first
// observer: it types the parse's records as it meets them, so every view,
// grounded copy and leaf copy of them carries the typing the tree's typed
// copy has, and it adds the attributes the schema supplies by default to
// the start tags. A document is known to be valid only at its end; until
// then the run goes on, and a failure is the run's error: the container
// re-runs on the tree (executeStream, the merge's re-run), which raises the
// tree's error with its code, its message and in its order -- including
// when the body failed first, since the tree validates before it runs the
// body. So the stream must be exact on a valid document and exact about
// whether a document is valid; the error text is the tree's.
//
// What the tree's copy is besides typed is the stream's too: no document
// URI (streamDocAs), no DTD (a document whose prolog may hold a DOCTYPE is
// left to the tree, decided before anything runs: ENTITY validation reads
// the DTD the copy does not have), and ignorable whitespace kept for an
// element-rooted validation and dropped for a document-rooted one.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xsdstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xsd"
)

// A document the harness gives as validated, and preloads as a typed tree,
// may be read again on a stream by a validating xsl:source-document: the
// harness marks it (xsdstream.MarkSource), and the stream repeats its
// validation first.
func init() {
	xsdstream.MarkSource = func(resolver any, uri string, schema any) {
		r, ok := resolver.(*FileResolver)
		if !ok || schema == nil {
			return
		}
		path, err := r.resolvePath(uri, "")
		if err != nil {
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.preValid == nil {
			r.preValid = map[string]any{}
		}
		r.preValid[path] = schema
	}
}

// preValidation is the schema the harness marked the document at uri with,
// or nil.
func (r *FileResolver) preValidation(uri, base string) any {
	path, err := r.resolvePath(uri, base)
	if err != nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.preValid[path]
}

// streamValidation is how a document is validated on the stream.
type streamValidation struct {
	// schema and mode are the validation, when there is an assessment;
	// pre is a validation that comes first, the document node the root:
	// the harness's (preloadSources, annotate), with its own schema.
	schema, pre any
	mode        xsdstream.Mode
	// docIDs is the ID/IDREF check XSLT applies over the typed document
	// (checkDocumentIDs): validation strict and lax.
	docIDs bool
	// marked says the resolver holds the tree the harness validated, which
	// the stream reads again (wantsBursts would decline it).
	marked bool
	// strips is input-type-annotations="strip" on an xsl:source-document's
	// document (stripInputAnnotations), applied once it is validated.
	strips bool
	// main is the principal input's validation (pre the stylesheet's
	// schema): its mode, and its error (sourceError).
	main SourceValidation
}

// streamedValidation says how spec's validation of the document at href is
// made on the stream: nil for none (validation="strip", the default); ok
// false when it cannot be, and the tree does it.
//
// strips says the caller strips the validated document's annotations when
// the stylesheet asks (xsl:source-document does; the tree's merge does not).
func (spec validationSpec) streamedValidation(rt *runtime, br burstResolver, href, base string, strips bool) (sv *streamValidation, ok bool) {
	if spec.isDefault() {
		return nil, true
	}
	fr, isFile := br.(*FileResolver)
	if !isFile || fr.prologMayHaveDTD(href, base) {
		return nil, false
	}
	sv = &streamValidation{pre: fr.preValidation(href, base), strips: strips && rt.sheet.stripTypeAnnotations}
	sv.marked = sv.pre != nil
	schema := any(nil)
	if rt.sheet.schema != nil {
		schema = rt.sheet.schema
	}
	switch {
	case spec.typeName != nil:
		if schema == nil {
			if spec.typeName.URI != xdm.NSXS {
				return nil, false // XTSE1660, from the tree
			}
			schema = xsd.NewSchema()
		}
		if spec.typeName.URI == xdm.NSXS {
			switch spec.typeName.Local {
			case "untyped", "anyType":
				schema = nil // assess strips: untyped
			case "untypedAtomic":
				return nil, false
			}
		}
		sv.mode = xsdstream.Mode{Root: xsdstream.Type, Type: *spec.typeName}
	case spec.mode == validatePreserve:
		schema = nil // the copy, as read
	case spec.mode == validateLax:
		sv.mode, sv.docIDs = xsdstream.Mode{Root: xsdstream.Lax}, true
	case spec.mode == validateStrict:
		if schema == nil {
			return nil, false // XTSE1660, from the tree
		}
		sv.mode, sv.docIDs = xsdstream.Mode{Root: xsdstream.Strict}, true
	}
	if schema == nil {
		sv.docIDs = false
	} else if xsdstream.Refusal(schema) != "" {
		return nil, false
	}
	if sv.pre != nil && xsdstream.Refusal(sv.pre) != "" {
		return nil, false
	}
	sv.schema = schema
	return sv, true
}

// attach makes sv the cursor's first observer, strip (xsl:strip-space) or
// nil the whitespace it strips, and returns what reports the outcome once
// the cursor has read the document to its end.
func (sv *streamValidation) attach(c *xdmstream.Cursor, strip func(*xdm.Node) bool) func() error {
	var vs []xsdstream.Validator
	if sv.pre != nil {
		vs = append(vs, xsdstream.Open(sv.pre, rootMode(sv.main)))
	}
	if sv.schema != nil {
		vs = append(vs, xsdstream.Open(sv.schema, sv.mode))
	}
	var ids *docIDs
	if sv.docIDs {
		ids = &docIDs{owners: map[string]uint64{}}
	}
	stripped := error(nil)
	c.Observe(func(n *xdm.Node, d int, end bool, ord uint64) {
		for _, v := range vs {
			v.Observe(n, d, end)
		}
		if ids != nil {
			ids.observe(n, d, end, ord)
		}
		if sv.strips && stripped == nil {
			stripped = stripAnnotationsOf(n, end)
		}
	})
	c.StripWhitespace(validatedStripper(vs, strip, sv.pre != nil))
	return func() error {
		for _, v := range vs {
			if err := v.Err(); err != nil {
				if sv.main != 0 {
					return sourceError(sv.main, err)
				}
				return fmt.Errorf("XTTE1510: the streamed document is not valid: %w", err)
			}
		}
		if ids != nil && ids.err != nil {
			return ids.err
		}
		return stripped
	}
}

// stripAnnotationsOf is stripAnnotationCopy for n, an element of the parse
// at its start tag or its end, once the validation has typed it: its typing
// and its attributes' cleared but for is-id and is-idrefs, no type
// environment, and xsi:nil taken off the start tag.
func stripAnnotationsOf(n *xdm.Node, end bool) error {
	if n == nil || n.Kind() != xdm.KindElement {
		return nil
	}
	strip := func(x *xdm.Node) {
		x.StripTyping()
		x.SetTypeEnv(nil)
	}
	strip(n)
	if end {
		return nil
	}
	var nils []*xdm.Node
	for a := range n.Attrs() {
		if a.Name().URI == xdm.NSXSI && a.Name().Local == "nil" {
			nils = append(nils, a)
			continue
		}
		strip(a)
	}
	for _, a := range nils {
		if !xdmburst.RemoveAttr(n, a) {
			return fmt.Errorf("streaming: xsi:nil cannot be taken off the start tag")
		}
	}
	return nil
}

// validatedStripper is the cursor's whitespace stripping under validation:
// ignorable whitespace a document-rooted validation drops, then strip's.
// With first (a validation made before the stylesheet strips, as the harness
// makes it), strip spares an element annotated with a simple type
// (stripCopyNode's exemption) and tells the validation what it stripped.
func validatedStripper(vs []xsdstream.Validator, strip func(*xdm.Node) bool, first bool) func(*xdm.Node) bool {
	return func(el *xdm.Node) bool {
		for _, v := range vs {
			if v.Drops(el) {
				return true
			}
		}
		if strip == nil || !strip(el) {
			return false
		}
		if first {
			if xdm.HasSimpleTypeAnnotation(el.TypeAnnotation()) {
				return false
			}
			vs[0].Stripped(el)
		}
		return true
	}
}

// docIDs is checkDocumentIDs over the stream: the annotations are those
// the validator has written by the time it is told of a node.
type docIDs struct {
	owners map[string]uint64 // ID value -> its element's ordinal
	refs   []string
	dupe   string
	// text collects the string value of each open element annotated as an
	// ID or a reference (they have simple content), per depth.
	text []*strings.Builder
	err  error
}

func (x *docIDs) observe(n *xdm.Node, d int, end bool, ord uint64) {
	if x.err != nil {
		return
	}
	switch {
	case n == nil:
		if x.dupe != "" {
			x.err = fmt.Errorf("XTTE1555: ID value %q is defined more than once in the document", x.dupe)
			return
		}
		for _, r := range x.refs {
			if _, ok := x.owners[r]; !ok {
				x.err = fmt.Errorf("XTTE1555: IDREF %q does not match any ID in the document", r)
				return
			}
		}
	case n.Kind() == xdm.KindElement && !end:
		for a := range n.Attrs() {
			x.note(ord, a.TypeAnnotation(), a.Value())
		}
		for len(x.text) <= d {
			x.text = append(x.text, nil)
		}
		x.text[d] = nil
		switch n.TypeAnnotation() {
		case "ID", "IDREF", "IDREFS":
			x.text[d] = &strings.Builder{}
		}
	case n.Kind() == xdm.KindElement:
		if d < len(x.text) && x.text[d] != nil {
			x.note(ord, n.TypeAnnotation(), x.text[d].String())
			x.text[d] = nil
		}
	case n.Kind() == xdm.KindText && d-1 < len(x.text) && x.text[d-1] != nil:
		x.text[d-1].WriteString(n.Value())
	}
}

// note is checkDocumentIDs's noteID and noteRefs for a node of the element
// whose ordinal is owner.
func (x *docIDs) note(owner uint64, ann, value string) {
	switch ann {
	case "ID":
		if prev, seen := x.owners[value]; !seen {
			x.owners[value] = owner
		} else if prev != owner && x.dupe == "" {
			x.dupe = value
		}
	case "IDREF":
		x.refs = append(x.refs, value)
	case "IDREFS":
		x.refs = append(x.refs, strings.Fields(value)...)
	}
}

// prologMayHaveDTD reports whether the document at href may have a
// DOCTYPE: one before its first element, or a prolog this cannot read
// (a UTF-16 one, or one past what it looks at).
func (r *FileResolver) prologMayHaveDTD(href, base string) bool {
	path, err := r.resolvePath(href, base)
	if err != nil {
		return false // the stream reports it as the tree would
	}
	may := true
	_ = r.openConfined(path, func(f *os.File) error {
		may = prologDTD(bufio.NewReaderSize(io.LimitReader(f, 1<<16), 1<<12))
		return nil
	})
	return may
}

// prologDTD reads a prolog up to its first element: false only when it
// meets that element with no DOCTYPE before it.
func prologDTD(r *bufio.Reader) bool {
	b, _ := r.Peek(2)
	if len(b) == 2 && (b[0] == 0xFE && b[1] == 0xFF || b[0] == 0xFF && b[1] == 0xFE) {
		return true
	}
	if b, _ := r.Peek(3); string(b) == "\xEF\xBB\xBF" {
		r.Discard(3)
	}
	for {
		c, err := r.ReadByte()
		if err != nil {
			return true
		}
		if c != '<' {
			continue
		}
		next, err := r.Peek(1)
		if err != nil {
			return true
		}
		switch next[0] {
		case '?':
			if !skipPast(r, "?>") {
				return true
			}
		case '!':
			if b, _ := r.Peek(3); string(b) == "!--" {
				if !skipPast(r, "-->") {
					return true
				}
				continue
			}
			return true // <!DOCTYPE
		default:
			return false // the first element
		}
	}
}

// skipPast reads r past the next end.
func skipPast(r *bufio.Reader, end string) bool {
	k := 0
	for {
		c, err := r.ReadByte()
		if err != nil {
			return false
		}
		switch {
		case c == end[k]:
			k++
			if k == len(end) {
				return true
			}
		case c == end[0]:
			k = 1
		default:
			k = 0
		}
	}
}

// typedValue reports whether a split that atomizes the element el, a view
// or the parse's record at its start tag, must take its typed value: el
// carries a type (only a validated stream types its records) and the split
// does not want its string value.
func typedValue(s *streamSplit, el *xdm.Node) bool {
	return s.tailText != "string" && el != nil && el.Kind() == xdm.KindElement &&
		xdm.TypingOf(el) != (xdm.Typing{})
}

// groundAtomized is the typed value of the element the cursor is at: its
// grounded copy, which has its typing, atomized as the tree atomizes it
// (FOTY0012 for element-only content).
func groundAtomized(c *xdmstream.Cursor) (xdm.Sequence, error) {
	g, err := c.Ground()
	if err != nil {
		return nil, err
	}
	return xdm.AtomizeChecked(xdm.One(g))
}

// The principal input with TransformOptions.SourceValidation: validated
// against the stylesheet's schema, the document node the root, as an xsd
// typed copy of the document is made, before xsl:strip-space (which then
// spares simple content: validatedStripper's first). TransformReader
// validates on the stream when it streams and the schema allows; Transform,
// and TransformReader otherwise, makes the typed copy. An invalid document
// is the same error both ways: the failures the event validator meets,
// which on the tree path it is made to meet by reading the document again
// (sourceInvalid), positions left out (a stream's parse and a tree's differ
// in them).

// sourceValidationStreams reports whether the validation mode asks for can
// be made on the stream.
func (s *Stylesheet) sourceValidationStreams(mode SourceValidation) bool {
	switch {
	case mode == 0:
		return true
	case s.schema == nil:
		return mode == SourceLax // lax without a schema assesses nothing
	}
	return xsdstream.Refusal(s.schema) == ""
}

// rootMode is the event validator's mode for mode.
func rootMode(mode SourceValidation) xsdstream.Mode {
	if mode == SourceLax {
		return xsdstream.Mode{Root: xsdstream.Lax, Doc: true}
	}
	return xsdstream.Mode{Root: xsdstream.Strict, Doc: true}
}

// validateMain attaches the validation mode asks for to c, the principal
// input's cursor, and returns what reports its outcome once c has been read
// to its end.
func (s *Stylesheet) validateMain(mode SourceValidation, c *xdmstream.Cursor) func() error {
	if mode == 0 || s.schema == nil {
		return func() error { return nil }
	}
	var strip func(*xdm.Node) bool
	if len(s.strip) > 0 {
		strip = s.whitespaceStripper(0)
	}
	return (&streamValidation{pre: s.schema, main: mode}).attach(c, strip)
}

// validatedSource is source as the validation mode asks for leaves it: the
// typed copy of the document.
func (s *Stylesheet) validatedSource(source *xdm.Node, mode SourceValidation) (*xdm.Node, error) {
	if source == nil || source.Kind() != xdm.KindDocument {
		return nil, fmt.Errorf("SourceValidation: the source must be a document node")
	}
	if s.schema == nil {
		if mode == SourceLax {
			return source, nil
		}
		return nil, fmt.Errorf("XTSE1660: validation requires a schema; none was imported")
	}
	if els := source.ChildElements(); mode == SourceLax && (len(els) == 0 || !s.schema.CanAssessStrictly(els[0])) {
		return source, nil
	}
	typed, err := s.schema.ValidateCopy(source, xsd.ValidateOptions{})
	if err != nil {
		return nil, s.sourceInvalid(source, mode, err)
	}
	return typed, nil
}

// sourceInvalid is the error of source, which the typed copy found invalid
// with err: the failures the event validator meets reading it, as a stream
// of it would have met them.
func (s *Stylesheet) sourceInvalid(source *xdm.Node, mode SourceValidation, err error) error {
	var b strings.Builder
	if Serialize(&b, xdm.One(source), OutputSettings{Method: "xml"}, nil) == nil {
		c := xdmstream.Open(strings.NewReader(b.String()), xdm.ParseOptions{MaxBytes: -1, MaxNodes: -1})
		defer c.Close()
		v := xsdstream.Open(s.schema, rootMode(mode))
		c.Observe(func(n *xdm.Node, d int, end bool, _ uint64) { v.Observe(n, d, end) })
		c.StripWhitespace(v.Drops)
		if drainTo(c) == nil && v.Err() != nil {
			err = v.Err()
		}
	}
	return sourceError(mode, err)
}

// sourceError is the principal input's validation error: its code, and the
// failures without their positions.
func sourceError(mode SourceValidation, err error) error {
	code := "XTTE1510"
	if mode == SourceLax {
		code = "XTTE1515"
	}
	var errs *xsd.ValidationErrors
	if errors.As(err, &errs) {
		c := &xsd.ValidationErrors{}
		for _, e := range errs.Errors {
			x := *e
			x.Line, x.Column = 0, 0
			c.Errors = append(c.Errors, &x)
		}
		err = c
	}
	return fmt.Errorf("%s: the principal input is not valid: %w", code, err)
}

// drainTo reads c to its end.
func drainTo(c *xdmstream.Cursor) error {
	for {
		ev, err := c.Next()
		switch {
		case err == io.EOF:
			return nil
		case err != nil:
			return err
		case ev.Kind == xdmstream.EndDocument:
			return nil
		}
	}
}

// stream reads d on a cursor fn reads: as the tree's validated copy of it
// is, with no document URI, when it is validated.
func (d *mergeDoc) stream(ctx *xpath.Context, fn func(*xdmstream.Cursor) error) error {
	if d.vin == nil {
		return d.br.streamDoc(ctx, d.href, d.base, fn)
	}
	return d.br.(*FileResolver).streamDocAs(ctx, d.href, d.base, false, fn)
}
