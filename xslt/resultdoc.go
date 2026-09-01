package xslt

import (
	"fmt"
	"io"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// SecondaryResult is one document produced by xsl:result-document.
//
// It is kept separate from the principal result rather than merged into it:
// the whole point of the instruction is that the stylesheet author wants two
// distinct documents, and folding them together would give a caller expecting
// several outputs a single plausible-looking wrong one.
type SecondaryResult struct {
	// Href is the resolved @href value, as written by the stylesheet. It is
	// the caller's choice what to do with it — this engine never writes to
	// the filesystem on a stylesheet's behalf, since a transform that can
	// create files anywhere the process can write is a hazard the caller
	// should be the one to opt into.
	Href string
	// BaseURI is Href resolved against the base output URI, which is the
	// base URI of every node in this document that does not override it with
	// xml:base.
	//
	// Section 19.1 makes the base output URI implementation-defined when the
	// caller does not supply one, and the stylesheet's own location is the
	// only URI this engine has: an @href of "out/second.xml" written in a
	// stylesheet read from .../foo.xsl means .../out/second.xml, which is
	// also where a caller honouring the href would write it. Leaving it
	// empty made base-uri() answer "" for every node in a secondary result,
	// and made a relative xml:base inside one resolve against nothing.
	BaseURI string
	// Nodes is the result sequence for this document.
	Nodes xdm.Sequence
	// Output holds the serialisation settings that apply to this document,
	// taken from @format and any serialisation attributes on the instruction.
	Output OutputSettings
	// charMap is the substitution table @use-character-maps names, already
	// flattened. It is resolved when the document is produced rather than
	// when it is serialised because only the stylesheet can resolve a
	// character-map name, and a caller holding a SecondaryResult no longer
	// has it.
	charMap map[rune]string
}

// resultDocumentInstr implements xsl:result-document.
type resultDocumentInstr struct {
	href *avt
	// format is @format, an attribute value template naming the xsl:output
	// declaration to use. It is resolved when the instruction runs, not when
	// it compiles: the value may be computed, and xsl:output is a top-level
	// declaration that may be written after the template that uses it.
	format *avt
	// overrides is the instruction element itself, whose serialisation
	// attributes take precedence over the selected definition. They are
	// attribute value templates, so the compiled forms are kept beside it and
	// the element is retained only for resolving the QNames they may name.
	overrides    *xdm.Node
	overrideAVTs map[string]*avt
	body         []Instruction
	// pkg is the package the instruction was written in, which decides whose
	// xsl:output and xsl:character-map declarations its @format and
	// @use-character-maps name: 3.5.5 makes both local to the declaring
	// package. See aliasKey.
	pkg int
	// validation is @validation or @type, which asks for the result tree to
	// be assessed as a document node before it is written.
	validation validationSpec
}

// settings selects the output definition this instruction writes with.
func (i *resultDocumentInstr) settings(rt *runtime) (OutputSettings, error) {
	out := rt.sheet.output
	if i.format != nil {
		lex, err := i.format.eval(rt)
		if err != nil {
			return out, err
		}
		if lex = strings.TrimSpace(lex); lex != "" {
			qn, err := resolveQNameAttr(i.overrides, lex)
			if err != nil {
				// XTDE1460 covers the whole of "not a valid lexical QName, or
				// ... does not match the expanded-QName of an output
				// definition", so a name that cannot even be resolved — an
				// unbound prefix, a malformed lexical form — is reported under
				// it too, not under the generic code resolveQNameAttr picks
				// for its many other callers.
				return out, fmt.Errorf(
					"XTDE1460: xsl:result-document/@format=%q is not a usable "+
						"QName: %w", lex, err)
			}
			named, ok := rt.sheet.namedOutputIn(
				i.pkg, xdm.QName{URI: qn.URI, Local: qn.Local}.Clark())
			if !ok {
				return out, fmt.Errorf(
					"XTDE1460: xsl:result-document/@format names no xsl:output "+
						"declaration named %q", lex)
			}
			out = *named
		}
	}
	// A tree produced by xsl:result-document is an explicit result tree, so
	// the 1.0 backwards-compatibility rule that would default its output
	// method to xml does not apply to it however the stylesheet is versioned.
	// Clearing the flag after the named-format lookup covers the copy taken
	// from a named xsl:output as well as the one from the unnamed default.
	out.Version10Implicit = false
	if i.overrides != nil {
		// The effective values are computed here rather than read from the
		// element: every serialisation attribute of this instruction is an
		// attribute value template, and reading the literal text made
		// doctype-system="{doc/foo}" a doctype of "{doc/foo}".
		value := func(name string) string {
			a, ok := i.overrideAVTs[name]
			if !ok {
				return ""
			}
			v, err := a.eval(rt)
			if err != nil {
				return ""
			}
			return v
		}
		if err := applyOutputValues(i.overrides, value, &out); err != nil {
			return out, err
		}
	}
	// Last, so that its parameters win over both the instruction's own
	// attributes and the output definition they were applied to -- which is
	// the precedence 25.1 states. The URI is resolved against the base URI of
	// the xsl:result-document element, which is where the attribute was
	// written.
	base := rt.sheet.baseURI
	if i.overrides != nil && i.overrides.BaseURI != "" {
		base = i.overrides.BaseURI
	}
	// The URI is resolved against the base URI of the element that wrote the
	// attribute, which is not always the xsl:result-document: a named
	// xsl:output selected by @format may sit in an included module in
	// another directory, and output-0722 does exactly that.
	if out.ParameterDocumentBase != "" {
		base = out.ParameterDocumentBase
	}
	if err := applyParameterDocument(rt, &out, base); err != nil {
		return out, err
	}
	return out, nil
}

func (i *resultDocumentInstr) Execute(rt *runtime, out *outputBuilder) error {
	href := ""
	if i.href != nil {
		v, err := i.href.eval(rt)
		if err != nil {
			return err
		}
		href = v
	}

	// XTDE1480: a result document cannot be created while building a
	// temporary tree. There is no final result tree for it to sit beside,
	// and the specification makes this an error rather than letting the
	// output vanish.
	if rt.temporary {
		return fmt.Errorf(
			"XTDE1480: xsl:result-document cannot be evaluated in temporary " +
				"output state")
	}

	// The body builds into its own builder, so nothing it produces reaches
	// the principal result. Passing `out` here is exactly the merging bug
	// this instruction used to be rejected to avoid.
	sub := newOutputBuilder()
	// The separator this document's output definition asks for is part of
	// sequence normalisation, so the builder applies it as the tree is
	// formed rather than the serialiser painting it on afterwards. The
	// settings are resolved early for that reason alone; the value is used
	// again below and recorded with the document.
	sepSettings, serr := i.settings(rt)
	if serr != nil {
		return serr
	}
	sub.SetItemSeparator(sepSettings.ItemSeparator)
	// execSequence rather than a bare loop: the body is a sequence
	// constructor, so an xsl:variable inside it is in scope for the
	// instructions that follow, and a plain Execute loop never binds it —
	// which made every reference to such a variable XPST0008.
	// The destination is resolved before the body runs, because the body is
	// what fn:current-output-uri answers inside.
	resolvedHref := i.destination(rt, href)
	if err := execSequence(i.body, rt.withOutputURI(resolvedHref), sub); err != nil {
		return err
	}

	// The tree is assessed as a document node before it is recorded, so that
	// an invalid result is an error rather than a file written and then
	// complained about. With build-tree="no" -- which is the default for the
	// json and adaptive methods -- there is no tree to assess: the raw
	// sequence is delivered as it stands, and forming a document node from it
	// is the very step 2.3.6 says is not taken. See buildsTree.
	if sepSettings.buildsTree() {
		doc, derr := sub.ToDocument()
		if derr != nil {
			return derr
		}
		if err := i.validation.assess(rt, doc); err != nil {
			return err
		}
		// The assessment annotated the document toDocument built, and that
		// document is a COPY: toTree deep-copies every node it adopts, so
		// that a constructed element is not re-parented out of whatever else
		// holds it. The nodes recorded below are the originals, which means
		// a successful validation left them exactly as untyped as they were.
		//
		// si-result-document-116 writes <in>2.1</in> under type="xs:decimal"
		// and then asks "/in instance of element(*, xs:decimal)" of the
		// recorded document; the annotation existed only on the discarded
		// copy, so the answer was false for a tree the processor had just
		// validated. The annotations are carried back rather than the
		// document being recorded in place of the sequence, because the
		// recorded sequence has to keep the item separators the document
		// node does not carry -- see the comment on nodes below.
		carryAnnotations(doc, sub.Sequence())
	}

	if href == "" {
		*rt.baseURIUsed = true
	}

	// A resource this transformation has already read may not be written to:
	// the document the stylesheet holds would stop matching what is on disk.
	// See readdocs.go.
	if err := checkReadThenWrite(rt, resolvedHref); err != nil {
		return err
	}

	// Two result documents sharing an href would mean one silently
	// overwriting the other, so the collision is reported instead.
	for _, prev := range *rt.secondary {
		if prev.Href == href {
			return fmt.Errorf(
				"XTDE1490: xsl:result-document: href %q was already produced by an earlier "+
					"result document", href)
		}
	}

	settings := sepSettings
	// The nodes carry the resolved base before they are recorded, so that
	// base-uri() inside this document answers relative to where the document
	// goes rather than to where the stylesheet was. rebase applies xml:base
	// on the way down, which is what makes <l xml:base="in/third.xml"> come
	// out at .../out/in/third.xml rather than at the bare reference.
	// The recorded sequence carries the separators too: the suite evaluates
	// its assertions against these nodes, not against the serialised text,
	// so a separator that existed only in the document node toDocument built
	// would be invisible to /text() = '+++'.
	nodes := insertItemSeparator(sub.Sequence(), settings.ItemSeparator)
	if resolvedHref != "" {
		for _, it := range nodes {
			if n, ok := it.(*xdm.Node); ok {
				rebase(n, resolvedHref)
			}
		}
	}
	cm, err := rt.sheet.flattenCharacterMaps(i.pkg, settings.UseCharacterMaps)
	if err != nil {
		return err
	}
	// A parameter document's use-character-maps spells its entries out rather
	// than naming an xsl:character-map, and it is the higher-precedence source
	// of the same parameter, so it replaces the named maps outright.
	if settings.InlineCharMap != nil {
		cm = settings.InlineCharMap
	}
	// Normalisation has already run over these nodes, so the serialiser must
	// not run it a second time and double every separator.
	settings.ItemSeparator = nil
	*rt.secondary = append(*rt.secondary, SecondaryResult{
		Href:    href,
		BaseURI: resolvedHref,
		Nodes:   nodes,
		Output:  settings,
		charMap: cm,
	})
	return nil
}

// Serialize writes the secondary document using its own output settings.
//
// A caller holding a SecondaryResult would otherwise have no way to render it:
// the serialiser is unexported, and re-deriving these settings from the
// stylesheet is exactly the duplication @format exists to avoid.
// The charMap argument overrides the table resolved from the document's own
// @use-character-maps; passing nil uses that table, which is what a caller
// almost always wants.
func (sr *SecondaryResult) Serialize(w io.Writer, charMap map[rune]string) error {
	if charMap == nil {
		charMap = sr.charMap
	}
	return serialize(w, sr.Nodes, sr.Output, charMap)
}

// String renders the secondary document using its own output settings.
func (sr *SecondaryResult) String() string {
	var sb strings.Builder
	_ = sr.Serialize(&sb, nil)
	return sb.String()
}

// destination resolves @href to the absolute URI this document is written to.
//
// Section 19.1 makes @href relative to the base output URI. When the caller
// supplied none the stylesheet's own location is the only URI this engine
// has, which is where a caller honouring the href would write the file, so
// that is what it falls back to.
func (i *resultDocumentInstr) destination(rt *runtime, href string) string {
	base := rt.baseOutputURI
	if base == "" {
		base = rt.sheet.baseURI
	}
	if resolved := resolveAgainst(base, href); resolved != "" {
		return resolved
	}
	return base
}

// buildsTree reports whether a result's raw sequence is normalised into a
// final result tree before it is delivered.
//
// Section 2.3.6: "If the effective value of the build-tree attribute is yes,
// then a final result tree is created by invoking the process of sequence
// normalization. The default for the build-tree attribute depends on the
// serialization method. For the xml, html, xhtml, and text methods the
// default value is yes. For the json and adaptive methods (available only
// with XPath 3.1) the default value is no."
//
// The distinction is not cosmetic. Normalisation wraps the raw sequence in a
// document node, and a document node may not contain an attribute or a
// function item -- so result-document-1407, which sends a map entry, a
// function reference, five integers and an attribute to method="adaptive",
// was XTDE0420 rather than the sequence the adaptive method is defined to
// write item by item.
func (o OutputSettings) buildsTree() bool {
	if o.BuildTree != nil {
		return *o.BuildTree
	}
	switch strings.ToLower(o.Method) {
	case "json", "adaptive":
		return false
	}
	return true
}
