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
			named, ok := rt.sheet.namedOutputs[xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()]
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
	sub.setItemSeparator(sepSettings.ItemSeparator)
	// execSequence rather than a bare loop: the body is a sequence
	// constructor, so an xsl:variable inside it is in scope for the
	// instructions that follow, and a plain Execute loop never binds it —
	// which made every reference to such a variable XPST0008.
	if err := execSequence(i.body, rt, sub); err != nil {
		return err
	}

	// The tree is assessed as a document node before it is recorded, so that
	// an invalid result is an error rather than a file written and then
	// complained about.
	doc, derr := sub.toDocument()
	if derr != nil {
		return derr
	}
	if err := i.validation.assess(rt, doc); err != nil {
		return err
	}

	if href == "" {
		*rt.baseURIUsed = true
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
	resolvedHref := resolveAgainst(rt.sheet.baseURI, href)
	if resolvedHref == "" {
		resolvedHref = rt.sheet.baseURI
	}
	// The recorded sequence carries the separators too: the suite evaluates
	// its assertions against these nodes, not against the serialised text,
	// so a separator that existed only in the document node toDocument built
	// would be invisible to /text() = '+++'.
	nodes := insertItemSeparator(sub.sequence(), settings.ItemSeparator)
	if resolvedHref != "" {
		for _, it := range nodes {
			if n, ok := it.(*xdm.Node); ok {
				rebase(n, resolvedHref)
			}
		}
	}
	cm, err := rt.sheet.flattenCharacterMaps(settings.UseCharacterMaps)
	if err != nil {
		return err
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
