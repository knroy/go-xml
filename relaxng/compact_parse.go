package relaxng

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// The compact syntax parser.
//
// The grammar is small enough to parse by recursive descent with no lookahead
// beyond one token, with one exception noted at topLevel: whether a schema is
// a bare pattern or a grammar cannot be known until enough of it has been read
// to see an "=" , so the decision is made by scanning ahead over a copy of the
// lexer rather than by backtracking the parse.
//
// Precedence, loosest to tightest:
//
//	p | p     choice
//	p , p     group
//	p & p     interleave
//	p? p* p+  repetition
//
// The three binary operators do NOT associate with one another: "a | b , c" is
// an error, not a reading. That is a real rule of the language and not a
// simplification here — the spec makes the infix operators non-associative
// across kinds precisely so that a reader never has to remember whether ","
// binds tighter than "&". Accepting it silently would give the schema a
// meaning its author did not choose between.

// annotation is a "##" documentation comment awaiting the construct it
// describes.
//
// It is carried rather than attached immediately because it precedes its
// subject: the parser sees the text, then the define or pattern it belongs to.
type annotation struct {
	doc string
}

// compactParser holds the parse state.
type compactParser struct {
	lex *lexer
	tok token
	b   *builder

	// namespaces maps a prefix to the URI a "namespace" declaration bound it
	// to. It is the parser's own table because the compact syntax resolves
	// prefixes at parse time: there is no xmlns in the source, so a prefixed
	// name must become an XML-syntax name= that the compiler can resolve, and
	// the binding it needs has to be written onto the tree here.
	namespaces map[string]string
	// defaultNS is the "default namespace" declaration, which becomes the
	// ns= attribute on the tree's root. Unlike the prefix table this one is
	// inherited by the compiler down the tree, which is exactly the XML
	// syntax's rule for ns=.
	defaultNS string
	// datatypes maps a prefix to a datatype library URI.
	datatypes map[string]string
	// pending holds a documentation comment read but not yet attached.
	pending *annotation
	// depth bounds recursion; see maxCompactDepth.
	depth int
}

// maxCompactDepth bounds how deeply patterns may nest.
//
// This is a resource bound on the parser and nothing else. Recursive descent
// costs a stack frame per level of parenthesis, and a schema is untrusted
// input: without a bound, "((((((..." is a stack overflow, which in Go is a
// fatal error no caller can recover from. That makes it a different failure
// from every other malformed schema, which return an error — so the bound
// exists to keep the failure mode uniform, not to express any rule of the
// language.
//
// It is deliberately far above what a real schema reaches. The deepest
// nesting in the DocBook 5.1 compact schema, at roughly 13,000 lines the
// largest in this repository's testdata, is under 30.
const maxCompactDepth = 500

// ParseCompact parses a RELAX NG compact syntax schema and returns the
// equivalent XML syntax document.
//
// The result is exactly what an author would have written in the XML syntax,
// so it can be handed to Compile — and CompileCompact does just that. It is
// returned rather than kept private because the translation is useful on its
// own: it is how a caller converts a .rnc to a .rng, and how a reader checks
// what a compact schema actually means.
//
// The compact syntax specification is not vendored in this repository; see the
// note at the top of compact_lex.go for what that means for the rules
// implemented here.
func ParseCompact(src string) (*xdm.Node, error) {
	p := &compactParser{
		lex:        newLexer(src),
		b:          newBuilder(),
		namespaces: map[string]string{},
		datatypes:  map[string]string{},
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	root, err := p.parseTopLevel()
	if err != nil {
		return nil, err
	}
	return p.b.finish(root), nil
}

// CompileCompact compiles a schema written in the compact syntax.
//
// It is ParseCompact followed by CompileWithOptions, and exists so that the
// ordinary case is one call. A Resolver supplied in opts serves "include" and
// "external" the same way it serves <include> and <externalRef>, since by the
// time compilation happens the two syntaxes are the same tree.
//
// One asymmetry is worth naming: a Resolver returns an *xdm.Node, an XML
// syntax document. A compact schema that includes another compact schema
// therefore needs a Resolver that parses .rnc — ParseCompact is exported so
// that such a Resolver can be written in a few lines, and CompactResolver does
// it for the common case.
func CompileCompact(src string, opts Options) (*Schema, error) {
	doc, err := ParseCompact(src)
	if err != nil {
		return nil, err
	}
	return CompileWithOptions(doc, opts)
}

// advance reads the next token into p.tok.
func (p *compactParser) advance() error {
	tok, err := p.lex.next()
	if err != nil {
		return err
	}
	p.tok = tok
	return nil
}

// errorf reports a parse error at the current token.
func (p *compactParser) errorf(format string, args ...any) error {
	return fmt.Errorf("relaxng: compact syntax, line %d column %d: %s",
		p.tok.line, p.tok.col, fmt.Sprintf(format, args...))
}

// at reports whether the current token is the given punctuation.
func (p *compactParser) at(punct string) bool {
	return p.tok.kind == tokPunct && p.tok.text == punct
}

// atKeyword reports whether the current token is the given keyword.
//
// An escaped identifier never matches: "\text" is a name, and the backslash
// exists precisely so that it is not read as the keyword.
func (p *compactParser) atKeyword(kw string) bool {
	return p.tok.kind == tokIdent && p.tok.text == kw
}

// expect consumes the given punctuation or reports what was found instead.
func (p *compactParser) expect(punct string) error {
	if !p.at(punct) {
		return p.errorf("expected %q, found %s", punct, p.tok)
	}
	return p.advance()
}

// takeDoc consumes a pending documentation comment, if any.
func (p *compactParser) takeDoc() *annotation {
	d := p.pending
	p.pending = nil
	return d
}

// readDoc absorbs a documentation comment into the pending slot.
func (p *compactParser) readDoc() error {
	for p.tok.kind == tokDocumentation {
		p.pending = &annotation{doc: p.tok.text}
		if err := p.advance(); err != nil {
			return err
		}
	}
	return nil
}

// compatibilityNS is the namespace of the annotations the RELAX NG DTD
// compatibility specification defines.
//
// A "##" documentation comment maps to a:documentation in this namespace. It
// is a foreign element as far as the schema language is concerned — checkSyntax
// skips any element outside the RELAX NG namespace — so it is carried onto the
// tree for a reader's benefit and has no effect on validation.
const compatibilityNS = "http://relaxng.org/ns/compatibility/annotations/1.0"

// takesAnnotation lists the elements a documentation annotation may be added
// to.
//
// It is derived from specs rather than written out: an element accepts an
// annotation child exactly when it accepts pattern children at all, which is
// what maxPatterns != 0 says. Deriving it means the two cannot disagree — a
// hand-written list would have to be revisited every time specs changed, and
// the failure of forgetting is a schema rejected for a comment.
var takesAnnotation = func() map[string]bool {
	m := map[string]bool{}
	for name, spec := range specs {
		if spec.maxPatterns != 0 && !spec.textOnly {
			m[name] = true
		}
	}
	return m
}()

// attachDoc puts a documentation annotation onto an element.
//
// An element that takes no content does not get one. <ref>, <value>, <empty>
// and their kind are specified to hold nothing or text only, and syntax.go
// enforces that — so an annotation grafted onto one turns a schema that was
// legal into one this package rejects, which is a comment changing the
// meaning of the schema it comments on. The DocBook 5.1 and slides schemas in
// testdata both document a <value> and a <ref> this way, and both were
// rejected until the annotation was dropped here instead.
func (p *compactParser) attachDoc(n *xdm.Node, doc *annotation) {
	if doc == nil || !takesAnnotation[n.Name.Local] {
		return
	}
	a := &xdm.Node{
		Kind: xdm.KindElement,
		Name: xdm.QName{URI: compatibilityNS, Prefix: "a", Local: "documentation"},
	}
	p.b.text(a, doc.doc)
	// The annotation goes first: the compatibility spec places it before the
	// content it documents, and a <define>'s pattern children must stay in
	// their own order relative to one another.
	n.Children = append([]*xdm.Node{a}, n.Children...)
	n.Namespaces = append(n.Namespaces, &xdm.Node{
		Kind: xdm.KindNamespace, Name: xdm.QName{Local: "a"}, Value: compatibilityNS,
	})
}
