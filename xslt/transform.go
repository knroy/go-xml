package xslt

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// TransformOptions configures one transform.
type TransformOptions struct {
	// Params supplies values for top-level xsl:param, keyed by Clark name
	// ("{uri}local", or just "local" for a no-namespace parameter).
	Params map[string]xdm.Sequence

	// Documents resolves fn:doc and fn:document. Nil disables them, which is
	// the default: a stylesheet that can open arbitrary URIs is an SSRF and
	// file-disclosure vector, and validation rule sets need at most the code
	// lists shipped beside them.
	Documents xpath.DocumentResolver

	// Collections resolves fn:collection. Nil disables it, which is the
	// default, and setting Documents does not set this: enabling fn:doc for
	// a known code list should not also let a stylesheet enumerate whatever
	// a collection URI happens to name.
	Collections xpath.CollectionResolver

	// Texts resolves fn:unparsed-text. Nil disables it, which is the
	// default, and setting Documents does not set this: fn:doc hands back a
	// parsed XML document, while fn:unparsed-text hands back the raw bytes
	// of whatever the resolver will open. See xpath.TextResolver.
	Texts xpath.TextResolver

	// MaxDepth bounds template recursion. Zero means DefaultMaxDepth; a
	// negative value means no limit.
	//
	// The bound catches a stylesheet that recurses without a base case,
	// which is the common authoring mistake. But it also counts the ordinary
	// descent of an identity transform, so a limit below the parser's left
	// this refusing documents it had just accepted: at the old fixed 300, a
	// legal 500-deep document could be parsed and not transformed.
	MaxDepth int

	// InitialMode names the mode for the initial apply-templates.
	InitialMode string

	// InitialMatchSelection is what the initial apply-templates selects from.
	//
	// Section 2.3.2 makes the initial match selection an arbitrary sequence,
	// not a node: a caller may start the transform at a set of nodes, or —
	// since XSLT 3.0 gave patterns the power to match one — at atomic values
	// with no source document behind them at all. Leaving it nil defaults the
	// selection to the source document, which is the ordinary invocation.
	//
	// Setting it is also what satisfies XTDE0044: naming an initial mode
	// obliges the caller to say what to apply it to, and the source document
	// is only the usual way of saying it.
	InitialMatchSelection xdm.Sequence

	// InitialTemplate names a template to invoke instead of matching the
	// document root, which is how a stylesheet with only named templates is
	// entered.
	InitialTemplate string
	// InitialTemplateURI is the namespace URI of InitialTemplate, for a
	// caller that has already resolved the prefix in its own namespace
	// context. Empty means resolve any prefix in InitialTemplate against the
	// stylesheet's own declarations instead.
	//
	// The two are not interchangeable. A caller naming the template from
	// outside the stylesheet — a test catalog, a command line with its own
	// bindings — binds the prefix itself, and resolving it a second time
	// against the stylesheet can silently select a DIFFERENT template that
	// happens to spell another namespace with the same prefix.
	InitialTemplateURI string

	// InitialTemplateParams are the values supplied for the initial
	// template's non-tunnel parameters, keyed by the parameter name in Clark
	// notation. InitialTemplateTunnelParams are its tunnel parameters, which
	// pass through to whatever the template calls in turn.
	//
	// They are separate from Params, which binds the stylesheet's global
	// parameters. Section 2.3.2 makes those two different acts of priming: a
	// global parameter belongs to the stylesheet and is set once, while these
	// are the arguments of one call, and a template parameter and a global
	// parameter may share a name without either standing for the other.
	InitialTemplateParams       map[string]xdm.Sequence
	InitialTemplateTunnelParams map[string]xdm.Sequence

	// InitialModeParams and InitialModeTunnelParams are the same thing for an
	// apply-templates invocation: section 2.3.3 gives that entry point "two
	// sets of (QName, value) pairs, one set for tunnel parameters and one for
	// non-tunnel parameters", with "the same [effect] as when a template is
	// invoked using xsl:apply-templates with an xsl:with-param child".
	//
	// They are kept apart from the initial-template pair rather than shared
	// with it because the two entry points are mutually exclusive (XTDE0047)
	// but the names would still mislead a caller reading the API.
	InitialModeParams       map[string]xdm.Sequence
	InitialModeTunnelParams map[string]xdm.Sequence

	// Now fixes the value fn:current-dateTime returns. Leave it zero to use
	// the wall clock; set it to make a transform reproducible, which is what
	// a golden-file test needs.
	Now time.Time

	// ImplicitTimezone is the offset in minutes for date values with no
	// timezone. Defaults to UTC so that results are reproducible across
	// machines.
	ImplicitTimezone int

	// BaseOutputURI is the URI the principal result tree is destined for.
	// Section 19.1 makes it implementation-defined when the caller supplies
	// none, and this engine never writes files itself, so the default is to
	// have none at all: fn:current-output-uri then answers the empty
	// sequence everywhere, and a relative @href on xsl:result-document
	// resolves against the stylesheet's own location instead.
	BaseOutputURI string
}

// Result is the outcome of a transform.
type Result struct {
	// charMap is the flattened xsl:character-map table for serialisation.
	charMap map[rune]string
	// Nodes is the result sequence, which for a typical stylesheet is a
	// single element.
	Nodes xdm.Sequence
	// Messages holds xsl:message output, in the order produced.
	Messages []string
	// Warnings holds the warnings the transform raised, in the order
	// produced. They report conditions the spec asks a processor to notice —
	// currently the two xsl:mode warning attributes — and never affect the
	// result.
	Warnings []string
	// Secondary holds the documents produced by xsl:result-document, in the
	// order produced. It is empty for the great majority of stylesheets,
	// which produce a single result.
	Secondary []SecondaryResult
	// BaseURI is the URI this result tree is identified by, which Tree()
	// puts on the document node it manufactures. It is empty for the
	// principal result, whose document node has no URI of its own; a caller
	// assembling a Result from a SecondaryResult sets it from that
	// document's BaseURI so that base-uri(/) answers inside it.
	BaseURI string
	// output carries the stylesheet's serialisation settings.
	output OutputSettings
}

// Transform applies the stylesheet to a source document.
//
// The Stylesheet is not mutated, so one compiled stylesheet may be used from
// many goroutines concurrently.
func (s *Stylesheet) Transform(ctx context.Context, source *xdm.Node, opts TransformOptions) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// xsl:global-context-item constrains the item the whole transformation
	// runs against, so it is checked before anything else: every global
	// variable is evaluated from that item, and a mistyped one must not have
	// half the globals bound before it is noticed.
	var globalItem xdm.Item
	if source != nil {
		globalItem = source
	}
	if err := s.checkGlobalContextItem(globalItem); err != nil {
		return nil, err
	}
	// A nil source is legal when the transform starts from a named template.
	// XSLT 2.0 section 2.3 makes the source document optional in exactly that
	// case, and it is how a stylesheet that generates its own content is
	// invoked — so this is checked where a source is actually needed rather
	// than on the way in.
	// xsl:initial-template is the conventional default entry point: a
	// stylesheet that declares a template with that name is asking to be
	// started there when the caller names neither a source document nor a
	// template. The name is already whitelisted as the one legal XSLT-
	// namespace template name, and honouring it here is what lets a
	// source-free stylesheet run at all.
	defaultEntry := xdm.QName{URI: xdm.NSXSL, Local: "initial-template"}.Clark()
	useDefaultEntry := false
	// XTDE0044: naming an initial mode is asking for an apply-templates, and
	// an apply-templates needs something to select from. The initial match
	// selection defaults to the global context item, which this engine takes
	// from the source document, so no source means no selection at all. The
	// name is not consulted: #default and #unnamed specify a mode just as a
	// QName does, and error-0044a/aa/ac use all three.
	if source == nil && opts.InitialMode != "" && opts.InitialTemplate == "" &&
		opts.InitialMatchSelection == nil {
		return nil, fmt.Errorf(
			"XTDE0044: the invocation specifies initial mode %q but supplies "+
				"no initial match selection", opts.InitialMode)
	}
	if source == nil && opts.InitialTemplate == "" &&
		opts.InitialMatchSelection == nil {
		if _, ok := s.named[defaultEntry]; !ok {
			// An invocation that names no template starts by applying
			// templates in the initial mode, which defaults to the unnamed
			// one. That is an initial mode like any other, so supplying no
			// initial match selection is the XTDE0044 above rather than a
			// bare complaint: package-914a invokes an empty package with
			// nothing at all and accepts XTDE0040 or XTDE0044.
			return nil, fmt.Errorf(
				"XTDE0044: no initial match selection and no source " +
					"document: pass a source document, name a template to " +
					"start at, or declare a template named " +
					"xsl:initial-template")
		}
		// Being the conventional entry point does not exempt it from the
		// visibility rule. A named template of a package is a component like
		// any other and defaults to private, and 3.5.2 offers only the public
		// ones as entry points -- spec bug 30398 settled that this applies to
		// xsl:initial-template too. glob-cxt-item-006err declares it without
		// a visibility attribute and expects XTDE0040; reaching it through
		// this path rather than through opts.InitialTemplate was what let it
		// past the check the named path already made.
		if !s.eligibleInitialTemplate(xdm.QName{
			URI: xdm.NSXSL, Local: "initial-template",
		}) {
			return nil, fmt.Errorf(
				"XTDE0040: the template named xsl:initial-template is not " +
					"public, so a transform may not start at it")
		}
		useDefaultEntry = true
	}

	// Whitespace stripping is applied to a copy so that the caller's tree is
	// left as they parsed it. Stripping in place would surprise a caller that
	// reuses one parsed document across several stylesheets with different
	// strip-space declarations.
	if source != nil && len(s.strip) > 0 {
		// Stripping applies to the whole tree the initial context node belongs
		// to, not to the node itself. When the caller starts the transform at
		// an inner node -- the suite does this with <source select="..."/> --
		// stripping only that subtree left the node's ancestors unstripped,
		// and passing a text node made stripWhitespace walk a node with no
		// children and produce an empty document.
		//
		// The node is then looked up again in the stripped copy. If whitespace
		// stripping deleted it, there is no initial context item at all, and
		// every expression that needs one raises XPDY0002 -- which is exactly
		// what section 4.4 means by stripping happening before the transform
		// begins rather than as it runs.
		root := source.Root()
		stripped, found := s.stripWhitespaceFrom(root, source)
		if source == root {
			source = stripped
		} else {
			source = found
		}
	}

	// Type annotations are stripped from the same source trees whitespace
	// stripping applies to — section 3.5 says so — and after it, so that the
	// two passes compose rather than one undoing the other.
	//
	// Restricted to a document node for the same reason whitespace stripping
	// now works from the root: stripTypeAnnotationsFrom copies the children of
	// what it is handed, so handing it an inner node discarded everything
	// above and beside it.
	if source != nil && source.Kind == xdm.KindDocument && s.stripTypeAnnotations {
		source = s.stripTypeAnnotationsFrom(source)
	}

	// Documents loaded by fn:doc and fn:document are source documents too, so
	// the same whitespace declarations apply to them. The wrapper is per
	// transform because its cache holds the stripped copies, which must not
	// outlive the declarations that produced them.
	if len(s.strip) > 0 && opts.Documents != nil {
		opts.Documents = &stripSpaceResolver{sheet: s, inner: opts.Documents}
	}
	// 4.4 names the collection function in the same sentence as doc and
	// document, so the documents a collection yields are stripped too.
	if len(s.strip) > 0 && opts.Collections != nil {
		opts.Collections = &stripCollectionResolver{sheet: s, inner: opts.Collections}
	}

	// Every document the transformation reads is recorded, so that
	// xsl:result-document can refuse to write over one. See readdocs.go. The
	// wrapper goes OUTSIDE the stripping one, so that it sees the document
	// URI of the tree actually delivered to the stylesheet.
	readDocs := map[string]bool{}
	if opts.Documents != nil {
		opts.Documents = &readDocResolver{inner: opts.Documents, read: readDocs}
	}

	rt, err := newRuntime(s, ctx, source, opts)
	if err != nil {
		return nil, err
	}
	rt.readDocs = &readDocs
	// Bind the runtime so key(), current() and xsl:function can reach it.
	rt.ctx = rt.ctx.WithVar(runtimeVar,
		xdm.One(&xdm.Opaque{Label: "runtime", Value: rt}))

	// The principal result tree begins here. Global variables were evaluated
	// inside newRuntime, before this binding exists, which is what makes
	// fn:current-output-uri answer the empty sequence in one — section 24.3
	// clears the current output URI while a global is evaluated.
	rt = rt.withOutputURI(opts.BaseOutputURI)

	out := newOutputBuilder()

	if useDefaultEntry {
		t := s.named[defaultEntry]
		for _, p := range t.Params {
			if p.Required && !p.Tunnel {
				return nil, fmt.Errorf(
					"XTDE0060: the initial template xsl:initial-template "+
						"declares required parameter $%s", p.Name.Lexical())
			}
		}
		if err := runTemplate(rt, t,
			opts.InitialTemplateParams, opts.InitialTemplateTunnelParams,
			out); err != nil {
			return nil, err
		}
	} else if opts.InitialTemplate != "" {
		// XTDE0047: "it is a non-recoverable dynamic error if the invocation
		// of the stylesheet specifies both an initial mode and an initial
		// template". The two are alternative ways of saying where processing
		// starts, and honouring only one of them silently discards half of
		// what the caller asked for.
		if m := opts.InitialMode; m != "" && m != "#default" && m != "#unnamed" {
			return nil, fmt.Errorf(
				"XTDE0047: the invocation specifies both an initial mode %q "+
					"and an initial template %q", m, opts.InitialTemplate)
		}
		// The name the caller supplies is lexical, so a prefix in it is
		// resolved against the stylesheet's own namespace declarations before
		// the lookup. Treating "foo:temp" as a local name found nothing, and
		// a template declared with a prefixed name could not be invoked.
		prefix, local := xdm.SplitQName(opts.InitialTemplate)
		initName := xdm.QName{Local: opts.InitialTemplate}
		switch {
		case opts.InitialTemplateURI != "":
			initName = xdm.QName{URI: opts.InitialTemplateURI, Local: local}
		case prefix != "":
			if uri, found := s.prefixes[prefix]; found {
				initName = xdm.QName{URI: uri, Local: local}
			}
		}
		t, ok := s.named[initName.Clark()]
		if !ok {
			return nil, fmt.Errorf(
				"XTDE0040: no template named %q", opts.InitialTemplate)
		}
		if !s.eligibleInitialTemplate(initName) {
			// The template exists but the package does not offer it as an
			// entry point; see entryvisibility.go.
			return nil, fmt.Errorf(
				"XTDE0040: the template named %q is not public, so a "+
					"transform may not start at it", opts.InitialTemplate)
		}
		// XTDE0060: the initial template may not declare a required
		// parameter, because a transform started at it supplies none.
		// XSLT 2.0 has no way to supply a parameter to the initial template:
		// section 2.3 of that version admits an initial template name and
		// nothing else, so a required parameter is always unsatisfied there.
		// A 2.0 processor handed values for one must go on reporting the
		// error, which is what initial-template-002a and -003a check.
		supplyable := s.maxVersion == 0 || s.maxVersion >= 3.0
		for _, p := range t.Params {
			if !p.Required {
				continue
			}
			if !supplyable {
				if p.Tunnel {
					continue
				}
				return nil, fmt.Errorf(
					"XTDE0060: the initial template %q declares required "+
						"parameter $%s", opts.InitialTemplate, p.Name.Lexical())
			}
			// A required parameter the caller supplied is satisfied. The
			// error is for one left unset, not for the declaration itself.
			supplied := opts.InitialTemplateParams
			if p.Tunnel {
				supplied = opts.InitialTemplateTunnelParams
			}
			if _, ok := supplied[p.Name.Clark()]; ok {
				continue
			}
			if p.Tunnel {
				continue
			}
			// The code is the processor's version to choose, not the
			// module's: it names the same condition under two spellings.
			// XSLT 2.0 called this XTDE0060; XSLT 3.0 dropped that code and
			// folded the case into XTDE0700, "the initial named template ...
			// defines a template parameter that specifies required='yes' and
			// no value is supplied for that parameter".
			code := "XTDE0060"
			if s.maxVersion == 0 || s.maxVersion >= 3.0 {
				code = "XTDE0700"
			}
			return nil, fmt.Errorf(
				"%s: the initial template %q declares required "+
					"parameter $%s", code, opts.InitialTemplate, p.Name.Lexical())
		}
		if err := runTemplate(rt, t,
			opts.InitialTemplateParams, opts.InitialTemplateTunnelParams,
			out); err != nil {
			return nil, err
		}
	} else {
		// "#default" and "#unnamed" both name the unnamed mode; a caller
		// passing either means "start where a stylesheet with no @mode
		// starts", which is the empty name.
		initialMode := opts.InitialMode
		if initialMode == "#unnamed" {
			initialMode = ""
		}
		// An invocation that names no mode at all starts in the module's
		// default mode, which @default-mode on the root moves off the unnamed
		// one. The rules written without @mode moved with it, so starting in
		// the unnamed mode would find none of them; mode-1701 is exactly that
		// stylesheet. An explicit "#unnamed" is a different request and is
		// left alone, which is why this reads opts rather than initialMode.
		// "#default" is not a mode name: it asks for THE DEFAULT MODE, which
		// is what @default-mode on the top-level package names and only the
		// unnamed mode in the absence of that attribute. 5.7.1's definition
		// of the invocation puts it as "if no initial mode is supplied, then
		// the mode used is that named in the default-mode attribute of the
		// (explicit or implicit) xsl:package element of the top-level package
		// or in the absence of such an attribute, the unnamed mode", and
		// "#default" is how a caller spells "none supplied".
		//
		// "#unnamed" is the different request, and is the one the grammar
		// offers beside an EQName in @default-mode itself; it is normalised
		// above and left alone here.
		//
		// package-001p, -001q and -001r each declare default-mode="start" and
		// are invoked with initial-mode "#default". Treating that as the
		// unnamed mode ran the built-in rules and copied the text through
		// instead of firing the rule in mode "start".
		if opts.InitialMode == "" || opts.InitialMode == "#default" {
			initialMode = s.rootDefaultMode
		}
		// XTDE0045: "it is a non-recoverable dynamic error if the invocation
		// of the stylesheet specifies an initial mode (other than the default
		// mode) that does not match the expanded-QName in the mode attribute
		// of any template". mode="#all" makes a template apply in every mode
		// but does not NAME one, so it cannot satisfy the invocation: per
		// W3C bugzilla 3690 an initial mode matched only by "#all" is still
		// this error. Only tokens that are real names count as declaring.
		if initialMode != "" {
			// The caller's name is lexical, so a prefix in it is resolved
			// against the stylesheet's declarations before comparison, the
			// same way InitialTemplate is above.
			want := initialMode
			if prefix, local, found := strings.Cut(initialMode, ":"); found {
				if uri, ok := s.prefixes[prefix]; ok {
					want = xdm.QName{URI: uri, Local: local}.Clark()
				}
			}
			// The mode named by the module's own @default-mode is an entry
			// point the module nominated, so it is eligible whether or not a
			// rule mentions it and whatever its visibility; mode-1803
			// declares such a mode and no rule uses it. Tested first because
			// it excuses both of the checks below.
			if want != s.rootDefaultMode {
				if !s.declaresMode(want) {
					return nil, fmt.Errorf(
						"XTDE0045: the invocation specifies initial mode %q, "+
							"which no template declares", opts.InitialMode)
				}
				// Existing is not the same as invocable: a mode a package
				// keeps private cannot be entered from outside it. See
				// modevisibility.go.
				if !s.eligibleInitialMode(want) {
					return nil, fmt.Errorf(
						"XTDE0045: the invocation specifies initial mode %q, "+
							"which is not an eligible initial mode because "+
							"the package declares it private", opts.InitialMode)
				}
			}
			initialMode = want
		}
		if err := applyInitialSelection(rt, source, opts.InitialMatchSelection,
			initialMode, opts.InitialModeParams, opts.InitialModeTunnelParams,
			out); err != nil {
			return nil, err
		}
	}

	// The base output URI belongs to the principal result tree. An
	// xsl:result-document with an absent or empty @href names that same URI,
	// so a stylesheet that both writes there and leaves content in the
	// principal tree has produced two documents at one URI. The check is made
	// here rather than in the instruction because the ordering is not fixed:
	// the implicit content may be written either side of the instruction, and
	// only at the end is it known that both happened.
	if *rt.baseURIUsed && len(out.Sequence()) > 0 {
		return nil, fmt.Errorf(
			"XTDE1490: two result documents were written to the base output " +
				"URI: the principal result tree and an xsl:result-document " +
				"with no href")
	}

	// The principal result tree is serialized with the unnamed xsl:output,
	// and §25.1 gives parameter-document to that declaration exactly as it
	// gives it to a named one: "the parameter-document attribute allows
	// serialization parameters to be supplied in an external document", with
	// no restriction to xsl:result-document. Reading it only in the
	// instruction left an <xsl:output parameter-document="..."/> at the top
	// level with no effect at all, which is what output-0720 and output-0721
	// see - both write a character map into the parameter document and expect
	// the principal result to use it.
	//
	// The fetch happens here, after the transform, for the same reason it
	// happens at run time in the instruction: the document is located
	// relative to the deployed stylesheet, and a failure to find it leaves
	// the settings alone rather than raising.
	output := s.output
	charMap := s.activeCharMap
	if output.ParameterDocument != "" {
		base := s.baseURI
		if output.ParameterDocumentBase != "" {
			base = output.ParameterDocumentBase
		}
		if err := applyParameterDocument(rt, &output, base); err != nil {
			return nil, err
		}
		if output.InlineCharMap != nil {
			charMap = output.InlineCharMap
		}
	}

	return &Result{
		Nodes:     out.Sequence(),
		Messages:  *rt.messages,
		Warnings:  *rt.warnings,
		Secondary: *rt.secondary,
		output:    output,
		charMap:   charMap,
	}, nil
}

// stripWhitespace returns a copy of the tree with whitespace-only text nodes
// removed from the elements named by xsl:strip-space.
//
// xsl:preserve-space overrides xsl:strip-space, and a specific name beats a
// wildcard, so the decision is made per element by scanning both lists rather
// than precomputing a set.
func (s *Stylesheet) stripWhitespace(root *xdm.Node) *xdm.Node {
	stripped, _ := s.stripWhitespaceFrom(root, nil)
	return stripped
}

// stripWhitespaceFrom strips root and reports where want ended up in the copy.
//
// The second return is nil when want was itself a whitespace-only text node
// that stripping removed, which is the one case the caller must distinguish:
// an initial context node that no longer exists is not an error to raise here
// but an absent focus, and absence is what XPDY0002 reports at the point of
// use.
func (s *Stylesheet) stripWhitespaceFrom(root, want *xdm.Node) (*xdm.Node, *xdm.Node) {
	return s.stripWhitespaceForFrom(0, root, want)
}

// stripWhitespaceFor is stripWhitespace under one package's declarations.
func (s *Stylesheet) stripWhitespaceFor(pkg int, root *xdm.Node) *xdm.Node {
	stripped, _ := s.stripWhitespaceForFrom(pkg, root, nil)
	return stripped
}

func (s *Stylesheet) stripWhitespaceForFrom(pkg int, root, want *xdm.Node) (*xdm.Node, *xdm.Node) {
	var found *xdm.Node
	tree := xdm.NewTree()
	tree.Root.BaseURI = root.BaseURI
	// The DOCTYPE text is a property of the tree, not of the root node, and
	// it is where the unparsed-entity declarations live. Building a fresh
	// tree without it made fn:unparsed-entity-uri answer "" for every
	// document a stylesheet with xsl:strip-space was applied to — a
	// whitespace declaration silently deleting an unrelated part of the data
	// model.
	if src := root.Tree(); src != nil {
		tree.DocType = src.DocType
	}
	if want == root {
		found = tree.Root
	}
	for _, ch := range root.Children {
		if c := s.stripCopy(pkg, ch, false, want, &found); c != nil {
			tree.Root.AppendChild(c)
		}
	}
	tree.Finalize()
	return tree.Root, found
}

// stripSpaceResolver applies the stylesheet's xsl:strip-space and
// xsl:preserve-space declarations to documents loaded by fn:doc and
// fn:document.
//
// Section 4.4 scopes whitespace stripping to "all source documents", not to
// the principal one: a document retrieved by fn:doc is a source document and
// is stripped by the same declarations. Transform stripped only the principal
// source, so a stylesheet declaring xsl:strip-space saw stripped whitespace in
// its input and unstripped whitespace in everything doc() returned.
//
// The stripped trees are cached by URI because fn:doc must be stable: two
// calls with the same argument return the same node, and stripping afresh each
// time would return a different tree with different node identities.
type stripSpaceResolver struct {
	sheet *Stylesheet
	inner xpath.DocumentResolver
	// done caches by the tree the inner resolver returned rather than by the
	// URI string, so that two URIs the inner resolver maps to one document
	// still map to one stripped document here. The package is part of the key
	// because 4.4 scopes the declarations to it: one file loaded from two
	// packages is two documents.
	done map[strippedKey]*xdm.Tree
}

// strippedKey identifies one stripping of one source tree.
type strippedKey struct {
	tree *xdm.Tree
	pkg  int
}

func (r *stripSpaceResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	return r.resolve(0, uri, base)
}

// ResolveDocumentIn implements xpath.ContextDocumentResolver.
//
// Section 4.4 scopes whitespace stripping to the package the call appears in
// LEXICALLY: "Declarations within a library package only affect the handling
// of documents loaded using a call on the document, doc, or collection
// functions ... appearing lexically within the same package." So which
// declarations apply is a property of the expression, and the context is what
// carries it -- compileExpr attached the package when the expression was
// compiled.
//
// document-2401 is the case. Two packages declare different stripping over one
// file, the using package calls document() and so does the package it uses,
// and the two must see different trees: 0 stripped text nodes in one and 4 in
// the other.
func (r *stripSpaceResolver) ResolveDocumentIn(
	ctx *xpath.Context, uri, base string) (*xdm.Tree, error) {

	return r.resolve(packageOf(ctx), uri, base)
}

func (r *stripSpaceResolver) resolve(pkg int, uri, base string) (*xdm.Tree, error) {
	t, err := r.inner.ResolveDocument(uri, base)
	if err != nil || t == nil || t.Root == nil {
		return t, err
	}
	// Keyed by package as well as by tree: the same file loaded from two
	// packages is two differently stripped documents, and caching on the
	// source tree alone returned whichever package asked first to both.
	k := strippedKey{tree: t, pkg: pkg}
	if c, ok := r.done[k]; ok {
		return c, nil
	}
	root := r.sheet.stripWhitespaceFor(pkg, t.Root)
	out := root.Tree()
	if out == nil {
		return t, nil
	}
	if r.done == nil {
		r.done = map[strippedKey]*xdm.Tree{}
	}
	r.done[k] = out
	return out, nil
}

// stripCollectionResolver applies the stylesheet's xsl:strip-space and
// xsl:preserve-space declarations to the documents fn:collection returns.
//
// Section 4.4 names the collection function alongside document and doc: "The
// effect of xsl:strip-space and xsl:preserve-space is local to the package in
// which they appear. Declarations within a library package only affect the
// handling of documents loaded using a call on the document, doc, or
// collection functions ... appearing lexically within the same package."
// Collections were passed through unwrapped, so a stylesheet that stripped
// whitespace saw it stripped in its input and in doc(), but not in
// collection().
//
// It mirrors stripSpaceResolver in every respect, including the per-package
// cache: collection-006 loads one file from two packages that declare
// different stripping, and each must see its own tree.
type stripCollectionResolver struct {
	sheet *Stylesheet
	inner xpath.CollectionResolver
	done  map[strippedKey]*xdm.Tree
}

func (r *stripCollectionResolver) ResolveCollection(
	uri, base string) (xdm.Sequence, error) {

	return r.resolve(0, uri, base)
}

// ResolveCollectionIn implements xpath.ContextCollectionResolver.
func (r *stripCollectionResolver) ResolveCollectionIn(
	ctx *xpath.Context, uri, base string) (xdm.Sequence, error) {

	return r.resolve(packageOf(ctx), uri, base)
}

func (r *stripCollectionResolver) resolve(
	pkg int, uri, base string) (xdm.Sequence, error) {

	seq, err := r.inner.ResolveCollection(uri, base)
	if err != nil {
		return seq, err
	}
	out := make(xdm.Sequence, 0, len(seq))
	for _, it := range seq {
		// A collection may hold items that are not document nodes, and only
		// a document is a source document to be stripped.
		n, ok := it.(*xdm.Node)
		if !ok || n.Kind != xdm.KindDocument {
			out = append(out, it)
			continue
		}
		t := n.Tree()
		if t == nil {
			out = append(out, it)
			continue
		}
		k := strippedKey{tree: t, pkg: pkg}
		if c, hit := r.done[k]; hit {
			out = append(out, c.Root)
			continue
		}
		root := r.sheet.stripWhitespaceFor(pkg, n)
		st := root.Tree()
		if st == nil {
			out = append(out, it)
			continue
		}
		if r.done == nil {
			r.done = map[strippedKey]*xdm.Tree{}
		}
		r.done[k] = st
		out = append(out, st.Root)
	}
	return out, nil
}

// SourceDocumentResolver wraps a document resolver so that the trees it hands
// back are whitespace-stripped by this stylesheet's xsl:strip-space and
// xsl:preserve-space declarations, exactly as Transform strips the ones fn:doc
// returns during the transformation.
//
// It exists for the one caller that has to evaluate an expression over the
// source documents *before* the transform begins: the initial match selection
// is supplied to Transform as a sequence, so whoever computes that sequence
// computes it outside the transform and would otherwise see unstripped trees
// the transform itself never sees. Section 4.4 scopes stripping to "all source
// documents", and a node reaches the transform through the initial match
// selection as surely as through fn:doc. mode-1802 selects
// doc('mode-14.xml')//v[position() = 1 to 5] and then indexes the source by
// position, which counts the whitespace text nodes if they are still there.
//
// Passing nil returns nil, so a caller with no resolver to wrap is unchanged.
func (s *Stylesheet) SourceDocumentResolver(inner xpath.DocumentResolver) xpath.DocumentResolver {
	if inner == nil {
		return nil
	}
	return &stripSpaceResolver{sheet: s, inner: inner}
}

// stripCopy copies n, dropping whitespace-only text where stripping applies.
//
// preserving carries xml:space="preserve" down the subtree, and *only* that:
// whether an element's own whitespace is stripped is decided from the
// strip-space and preserve-space declarations matching its name.
func (s *Stylesheet) stripCopy(pkg int, n *xdm.Node, preserving bool, want *xdm.Node, found **xdm.Node) *xdm.Node {
	c := s.stripCopyNode(pkg, n, preserving, want, found)
	if c != nil && n == want {
		*found = c
	}
	return c
}

func (s *Stylesheet) stripCopyNode(pkg int, n *xdm.Node, preserving bool, want *xdm.Node, found **xdm.Node) *xdm.Node {
	switch n.Kind {
	case xdm.KindText:
		// Whitespace-only text is dropped unless xml:space preserves it or
		// its parent element is outside the strip-space list. The parent is
		// what the declarations are matched against, which is why the test
		// happens here rather than being decided by the parent and passed in.
		// A parent whose type is a simple type, or a complex type with
		// simple content, preserves its whitespace whatever the
		// declarations say: section 4.4 exempts it, because the text is
		// that element's entire typed value and stripping it would leave
		// an annotation describing a value the node no longer holds.
		// The exemption is read off the annotation the schema wrote, so
		// an unvalidated document is unaffected and this stays inside the
		// existing gate on there being a strip-space declaration at all.
		if !preserving && xdm.IsXMLWhitespace(n.Value) &&
			n.Parent != nil && s.stripsElement(pkg, n.Parent.Name) &&
			!xdm.HasSimpleTypeAnnotation(n.Parent.TypeAnnotation) {
			return nil
		}
		return &xdm.Node{Kind: xdm.KindText, Value: n.Value}

	case xdm.KindElement:
		// The type annotation travels with the copy. Whitespace stripping is
		// defined over which text nodes survive, not over what the surviving
		// nodes are: section 4.4 says nothing about types, and dropping them
		// here would mean declaring xsl:strip-space silently untyped a
		// document the caller had validated. This pass is gated on there
		// being a strip-space declaration at all, which is the only reason
		// the loss was not visible — removing that gate cost 115 tests.
		c := &xdm.Node{Kind: xdm.KindElement, Name: n.Name, BaseURI: n.BaseURI,
			TypeAnnotation: n.TypeAnnotation,
			IsID:           n.IsID, IsIDREFS: n.IsIDREFS,
			IsNilled:       n.IsNilled}
		for _, ns := range n.Namespaces {
			c.AddNamespace(ns.Name.Local, ns.Value)
		}
		for _, a := range n.Attrs {
			ac := &xdm.Node{Kind: xdm.KindAttribute, Name: a.Name, Value: a.Value,
				TypeAnnotation: a.TypeAnnotation,
				IsID:           a.IsID, IsIDREFS: a.IsIDREFS}
			c.AddAttr(ac)
			if a == want {
				*found = ac
			}
		}

		// Section 4.4 decides stripping per element, from the strip-space and
		// preserve-space declarations matching *that* element's name. What
		// inherits down the tree is xml:space, not the outcome: an ancestor
		// outside the strip-space list says nothing about its descendants.
		//
		// Threading the outcome down made it latch. The document element is
		// outside the list in the ordinary case, so it set "preserve", and
		// every element beneath it then preserved whitespace whatever the
		// declarations said — which is to say xsl:strip-space did nothing at
		// all unless it also named the document element.
		childPreserving := preserving
		if a := n.Attr(xdm.NSXML, "space"); a != nil {
			childPreserving = a.Value == "preserve"
		}

		for _, ch := range n.Children {
			if cc := s.stripCopy(pkg, ch, childPreserving, want, found); cc != nil {
				c.AppendChild(cc)
			}
		}
		return c

	default:
		return &xdm.Node{Kind: n.Kind, Name: n.Name, Value: n.Value}
	}
}

// stripsElement reports whether whitespace inside the named element is
// stripped. A specific preserve-space entry wins over a wildcard strip-space
// entry, matching the spec's import-precedence rule for the common case.
func (s *Stylesheet) stripsElement(pkg int, name xdm.QName) bool {
	best, strip := -1, false
	rank := func(q xdm.QName) int {
		switch {
		case q.Local == "*" && q.URI == "":
			return 0 // "*"
		case q.Local == "*", q.URI == "*":
			return 1 // "prefix:*" or "*:local"
		default:
			return 2 // a specific name
		}
	}
	consider := func(q xdm.QName, isStrip bool) {
		// "*:local" matches that local name in any namespace, which is
		// recorded with URI "*" because a namespace URI cannot be one.
		if q.URI == "*" {
			if q.Local != name.Local {
				return
			}
			if r := rank(q); r >= best {
				best, strip = r, isStrip
			}
			return
		}
		if q.Local != "*" && (q.Local != name.Local || q.URI != name.URI) {
			return
		}
		if q.Local == "*" && q.URI != "" && q.URI != name.URI {
			return
		}
		if r := rank(q); r >= best {
			best, strip = r, isStrip
		}
	}
	strips, preserves := s.spaceDeclsFor(pkg)
	for _, q := range strips {
		consider(q, true)
	}
	for _, q := range preserves {
		consider(q, false)
	}
	return strip
}

// spaceDeclsFor returns the whitespace declarations in force for a package.
//
// Section 4.4 makes them local to the package they appear in, so a document
// loaded by a call written in a library package is stripped by that package's
// declarations alone -- not by the ones its user declared, and not by the flat
// union of every module's.
//
// A package with no declarations of its own strips nothing, which is the whole
// point: document-2401a declares <xsl:strip-space elements=""/> and must see
// the four whitespace text nodes its user's elements="*" would have removed.
// The flat lists remain the answer only for the top-level package, which also
// strips the principal source document.
func (s *Stylesheet) spaceDeclsFor(pkg int) (strip, preserve []xdm.QName) {
	if s.pkgSpace == nil {
		return s.strip, s.preserve
	}
	ps := s.pkgSpace[pkg]
	if ps == nil {
		// The top-level package is the one that may legitimately have no
		// entry: a stylesheet with no xsl:strip-space at all records none,
		// and the flat list is then the same thing. A LIBRARY package with no
		// entry declared nothing, and 4.4 makes that mean nothing is
		// stripped -- not that its user's declarations apply.
		if pkg == 0 {
			return s.strip, s.preserve
		}
		return nil, nil
	}
	return ps.strip, ps.preserve
}

// String renders the result using the stylesheet's output settings.
func (r *Result) String() string {
	var sb strings.Builder
	_ = r.Serialize(&sb)
	return sb.String()
}

// Serialize writes the result using the stylesheet's xsl:output settings.
//
// Deliberately not named WriteTo: that name implies io.WriterTo, whose
// contract returns a byte count this would have to fabricate.
func (r *Result) Serialize(w io.Writer) error {
	return serialize(w, r.Nodes, r.output, r.charMap)
}

// Tree returns the result as a document node, for callers that want to keep
// navigating it rather than serialise it — which is what a Schematron driver
// does with an SVRL report.
func (r *Result) Tree() *xdm.Node {
	tree := xdm.NewTree()
	// The document node is manufactured here, so it is the only place the
	// result's own URI can be put on it. Without this base-uri(/) answered
	// "" even when every element below it had a base URI.
	tree.Root.BaseURI = r.BaseURI
	// Sequence normalisation runs here for the same reason the serialiser
	// runs it: a run of adjacent atomic values is one text node with a single
	// space between each, and a caller navigating the result must see the
	// same document the serialiser would have written. Appending each value
	// as its own text node lost the separators and broke the XDM invariant
	// that no two text nodes are adjacent.
	for _, it := range joinAdjacentAtomics(insertItemSeparator(r.Nodes, r.output.ItemSeparator)) {
		switch v := it.(type) {
		case *xdm.Node:
			// 5.7.1: "Any document node within the result sequence is
			// replaced by a sequence containing each of its children, in
			// document order." A result tree may not hold a document node
			// below its root, and appending one anyway put the content out
			// of reach of the tree's own string value -- xsl:document at the
			// top of a template returns exactly such a node, and its text
			// vanished from the result.
			//
			// The raw sequence in r.Nodes keeps the wrapper, because a
			// caller reading the sequence must still see the document node
			// it asked for; the flattening belongs to building the tree,
			// which is the step this rule describes.
			if v.Kind == xdm.KindDocument {
				for _, ch := range append([]*xdm.Node(nil), v.Children...) {
					tree.Root.AppendChild(ch)
				}
				continue
			}
			tree.Root.AppendChild(v)
		case *xdm.Atomic:
			tree.Root.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: v.String()})
		}
	}
	tree.Finalize()
	return tree.Root
}

// stripTypeAnnotations returns a copy of the tree with every type annotation
// removed, as input-type-annotations="strip" requires.
//
// Section 3.5 states the effect exactly: the annotation of every element
// becomes xs:untyped and of every attribute xs:untypedAtomic, the typed value
// of both becomes the string value as xs:untypedAtomic, and the is-nilled
// property of every element becomes false. The is-id and is-idrefs properties
// are explicitly *not* changed, which is why xsi:nil is the only attribute
// dropped here and the ID-bearing ones are copied through untouched.
//
// The work is done on a copy for the same reason whitespace stripping is: a
// caller may reuse one parsed document across several stylesheets, and only
// some of them ask for the annotations to go.
func (s *Stylesheet) stripTypeAnnotationsFrom(root *xdm.Node) *xdm.Node {
	tree := xdm.NewTree()
	tree.Root.BaseURI = root.BaseURI
	for _, ch := range root.Children {
		if c := stripAnnotationCopy(ch); c != nil {
			tree.Root.AppendChild(c)
		}
	}
	tree.Finalize()
	return tree.Root
}

// stripAnnotationCopy copies n with its type annotation cleared.
//
// An empty TypeAnnotation is how this data model spells xs:untyped for an
// element and xs:untypedAtomic for an attribute: Atomize returns an
// untypedAtomic for a node carrying none, which is precisely the typed value
// the specification asks for here.
func stripAnnotationCopy(n *xdm.Node) *xdm.Node {
	switch n.Kind {
	case xdm.KindElement:
		// IsID and IsIDREFS are carried over for the same reason they are on
		// the attributes below: section 3.5 changes the annotation and the
		// typed value but leaves is-id and is-idrefs alone. An element whose
		// schema type derives from xs:ID holds the identity in its CONTENT,
		// so dropping the property here made fn:id miss it — id('id1') found
		// nothing for an <id-elem> of type xs:ID once the annotations went.
		c := &xdm.Node{
			Kind: xdm.KindElement, Name: n.Name, BaseURI: n.BaseURI,
			IsID: n.IsID, IsIDREFS: n.IsIDREFS,
		}
		for _, ns := range n.Namespaces {
			c.AddNamespace(ns.Name.Local, ns.Value)
		}
		for _, a := range n.Attrs {
			// xsi:nil is dropped rather than copied: stripping makes
			// the is-nilled property of every element false, and the
			// attribute would otherwise remain as a claim the stripped
			// tree no longer supports. The property itself is simply
			// not carried onto the copy above. Every other attribute is
			// kept, which is what leaves is-id and is-idrefs unchanged.
			if a.Name.URI == xdm.NSXSI && a.Name.Local == "nil" {
				continue
			}
			c.AddAttr(&xdm.Node{
				Kind: xdm.KindAttribute, Name: a.Name, Value: a.Value,
				IsID: a.IsID, IsIDREFS: a.IsIDREFS,
			})
		}
		for _, ch := range n.Children {
			if cc := stripAnnotationCopy(ch); cc != nil {
				c.AppendChild(cc)
			}
		}
		return c
	case xdm.KindText:
		return &xdm.Node{Kind: xdm.KindText, Value: n.Value}
	case xdm.KindComment:
		return &xdm.Node{Kind: xdm.KindComment, Value: n.Value}
	case xdm.KindPI:
		return &xdm.Node{Kind: xdm.KindPI, Name: n.Name, Value: n.Value}
	}
	return nil
}

// SerializeAsXML renders a result with the xml output method, ignoring the
// stylesheet's own xsl:output.
//
// It exists for a caller making a *tree* assertion about a result — the W3C
// conformance harness is the one in this repository. The stylesheet's method
// is part of what serialisation means, not part of what the tree is: the html
// method injects a content-type meta into <head> and writes void elements
// unclosed, so a result asserted as a tree would be compared against markup
// the stylesheet never produced, and would not parse back as XML at all.
//
// Indentation and the other settings are deliberately left at their defaults
// rather than inherited, for the same reason.
func SerializeAsXML(r *Result) string {
	var sb strings.Builder
	_ = serialize(&sb, r.Nodes, OutputSettings{Method: "xml"}, r.charMap)
	return sb.String()
}

// applyInitialSelection runs the initial apply-templates over the initial
// match selection, section 2.3.2.
//
// The selection is a sequence rather than a node because a caller may name
// several starting nodes, or — in XSLT 3.0, where a pattern can match an
// atomic value — supply values that never belonged to a source tree. The
// position and size of each item have to be set the way xsl:apply-templates
// sets them, since a template rule reached this way may call fn:position().
func applyInitialSelection(rt *runtime, source *xdm.Node, sel xdm.Sequence,
	mode string, params, tunnels map[string]xdm.Sequence,
	out *outputBuilder) error {

	if sel == nil {
		noteInitialAccumulators(rt, mode, source)
		return applyToNode(rt, source, mode, params, tunnels, out)
	}
	size := len(sel)
	for _, it := range sel {
		if node, ok := it.(*xdm.Node); ok {
			noteInitialAccumulators(rt, mode, node)
		}
	}
	for idx, it := range sel {
		node, ok := it.(*xdm.Node)
		if !ok {
			sub := rt.withCurrent(it, idx+1, size)
			if err := applyToAtomic(sub, it, mode, params, tunnels, out); err != nil {
				return err
			}
			continue
		}
		if err := rt.sheet.checkModeTyped(node, mode); err != nil {
			return err
		}
		sub := rt.withCurrent(node, idx+1, size)
		if err := applyToNode(sub, node, mode, params, tunnels, out); err != nil {
			return err
		}
	}
	return nil
}

// emptyModeAccumulators is the applicable set for a mode that declares no
// use-accumulators list: nothing. See noteInitialAccumulators.
var emptyModeAccumulators = &modeAccumulators{names: map[string]bool{}}

// noteInitialAccumulators records which accumulators are applicable to a tree
// reached through the initial match selection.
//
// 18.2.2: "For a document containing nodes supplied in the initial match
// selection, the accumulators that are applicable are those determined by the
// xsl:mode declaration of the initial mode." Reading one the list leaves out
// is XTDE3362, the same code and the same rule xsl:source-document and
// xsl:merge-source already answer to through rt.treeAccums -- this is the
// third source of the same per-tree restriction, so it is recorded the same
// way rather than checked separately.
//
// What a mode does say is honoured exactly: mode-1106b starts in a mode
// declared use-accumulators="" and expects accumulator-after('counter') to
// fail, while mode-1106c starts in one declared use-accumulators="#all" and
// expects it to succeed.
//
// A mode that says nothing is the interesting case, and the suite splits it
// on whether an xsl:mode declaration exists at all. 18.2.2's "in the absence
// of an xsl:mode declaration, no accumulators are applicable" is taken at its
// word: copy-3002 declares no mode, copies from the initial match selection
// with copy-accumulators="yes", and expects XTDE3362 -- and copy-3003, which
// is the same stylesheet with <xsl:mode use-accumulators="latest-pick"/>
// added, expects it to work. But a mode that IS declared and merely omits
// @use-accumulators is left permissive, because the suite requires that too:
// accumulator-073 declares <xsl:mode on-no-match="shallow-copy"/> with no
// accumulator list, copies from the initial match selection the same way, and
// asserts the copied accumulator values. Reading the absent attribute as an
// empty list would fail it.
func noteInitialAccumulators(rt *runtime, mode string, node *xdm.Node) {
	if node == nil {
		return
	}
	set, ok := rt.sheet.modeAccums[mode]
	if !ok {
		// The mode says nothing about accumulators. Whether that means
		// "all" or "none" turns on whether the mode was declared at all;
		// see the comment above.
		if rt.sheet.declaredModeNames[mode] {
			return
		}
		set = emptyModeAccumulators
	}
	root := node.Root()
	if root == nil {
		return
	}
	// An entry already there was put by a nearer rule -- a copy carrying its
	// origin's applicability, say -- and is not displaced by this one.
	if _, seen := rt.treeAccums[root]; !seen {
		rt.treeAccums[root] = set
	}
}
