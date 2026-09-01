# Implementing XQuery 3.1

What it would take, what it reuses, and what is actually at risk.

Measured against the tree at `3bef1a0`. Nothing here is estimated where it
could be counted.

## Why this is smaller than it looks

XQuery 3.1 and XPath 3.1 share the entire expression language and the whole
function library. This repository has XPath 3.1 at **100%** of the QT3 suite
and ~437 functions registered, so the parts of XQuery that are also XPath are
done.

What can be reused unchanged:

| Asset | Where | State |
|---|---|---|
| Expression language | `xpath` | 100% conformant, 32.5k lines |
| Function library | `xpath/fn_*.go` | ~437 functions |
| Serializer | `xpath/fn_serialize.go` | shared |
| Data model | `xdm` | shared |
| Result-tree construction | `xdmbuild` | already parameterised for XQuery |
| Schema engine | `xsd` | 99.89%, for the schema-aware feature |

`xpath` depends only on `xdm`, so an `xquery` package sits beside `xslt`
rather than under it. No restructuring is needed.

`xdmbuild` is the largest single piece of de-risking already done: its
`Policy` enumerates XQTY0024, XQDY0025, XQDY0102 and XQTY0105 by name, and
models copy-namespaces as the two independent booleans XQuery needs.

## What is missing

Roughly 95 grammar productions, in four groups.

**FLWOR** — `ForClause` with `at $i` and `allowing empty`, `LetClause`,
`WhereClause`, `GroupByClause`, `OrderByClause`, `CountClause`,
`WindowClause`. XPath's `ForExpr`/`LetExpr` are *not* a starting point:
`Binding` in `xpath/ast.go` is `{Var, Seq}` with no slot for a positional
variable or a type, and XQuery FLWOR is a tuple-stream pipeline where XPath's
is nested iteration.

**Constructors** — direct (`<a>{$x}</a>`) and computed. This is where the
risk is; see below.

**Prolog** — version and module declarations, the setters, namespace,
variable and function declarations, imports, options.

**Other expressions** — `typeswitch`, `switch`, `try`/`catch`, `validate`,
`ordered`/`unordered`, pragmas, and the string constructor `` `[...]` ``.
Verified absent: the only occurrence of "typeswitch" in the tree is in the
reserved-function-name list at `xpath/parser_path.go:1745`.

### XQuery is not a superset of XPath

A mechanical diff of the two grammars' Appendix A.1 — 239 productions against
126 — puts **120** in XQuery that XPath lacks, and **7** the other way. Six of
those seven are XPath's cut-down `for` and `let`, which XQuery replaces with
FLWOR. The seventh is not a replacement:

**XQuery has no `namespace::` axis.** XPath 3.1 has eight forward axes and
XQuery seven. We implement it — `AxisNamespace` in `xpath/ast.go:78`,
`xpath/nsaxis.go` — so the XQuery parser has to *refuse* a construct the
expression parser beneath it accepts. That is the one place where handing a
substring to `xpath` is not enough on its own.

Nine further productions share a name and differ in content: `ExprSingle`
(gains the FLWOR and try/catch alternatives), `QuantifiedExpr` (gains type
declarations), `ValueExpr` (gains validate and extension), `PrimaryExpr`
(gains constructors and ordered/unordered), `InlineFunctionExpr` and
`FunctionTest` (gain annotations), `StringLiteral` and `BracedURILiteral`
(admit entity and character references), and `ForwardAxis` above. Everything
else with a shared name is byte-identical.

Note that `EnclosedExpr` is *not* XQuery-only: XPath has it too, as an inline
function's body.

## The risk is the lexer, and it is architectural

`parseWith` in `xpath/parser.go` lexes eagerly:

```go
toks, err := lex.Tokens()
...
p := &Parser{toks: toks, ...}
```

The whole input becomes a `[]Token` before the parser starts, and the parser
does nothing but index into it. That is incompatible with XQuery, where
`<a>{$x + 1}</a>` cannot be tokenised without parser state: whether `a` is a
tag name or a name test, and whether `+` is an operator or element text, is
not decidable in one pass.

Making the lexer pull-based is not a small refactor, and it is the wrong one.
`tryParseAxisStep` backtracks by resetting `p.pos` (`parser_path.go:236,299`),
which is cheap *because* the tokens are a materialised slice. Streaming would
need a rollback buffer at every backtrack point, in a parser that is at 100%
and carries a great deal of hard-won context-sensitivity — `Lexer.prevOperand`
is already a hand-rolled lexical state, and the `*` and `?` disambiguations
around it are delicately balanced.

**So: do not write a mode-switching lexer, and do not touch `xpath/lexer.go`.**

Instead, follow BaseX. Five implementations were read at source level, and
they divide by *where the lexical state lives* rather than by whether they
have one:

| Engine | Approach | States |
|---|---|---|
| BaseX | scannerless recursive descent, no token stream at all | 0 — the call stack is the state |
| Saxon | hand-written tokeniser, plus raw character reading for constructors | 4, plus a scannerless escape |
| Zorba | generated (Flex/Bison), explicit state stack | 20 |
| XQilla | generated (Flex/Bison), explicit state stack | 17 |
| Galax | generated (ocamllex), one lexer file per state | 26 |

The generated-parser projects reproduce the state table nearly name-for-name
because Flex start conditions map onto it directly. The hand-written ones
collapse it — and both of those escape to character-level reading for direct
constructors, because that is the part a finite state set cannot cover:
constructors nest with enclosed expressions arbitrarily deeply, which is not a
regular language. Saxon's own source carries the comment *"we may need to make
this a stack at some time"* beside its scalar state field.

BaseX's shape fits here because our parser is already hand-written recursive
descent, and because a recursive-descent parser's call stack *is* the
push-down automaton the nesting requires:

- The `xquery` package parses constructor syntax **scannerlessly**, over the
  raw source, reusing `xpath.Token` and `xpath.TokenKind` (both exported).
- On reaching an enclosed expression `{`, it finds the matching `}` —
  tracking nesting, string literals, comments and `{{`/`}}` escapes — and
  hands that **substring** to `xpath`.

XML syntax stays confined to the constructor parser. The conformant
expression lexer is never modified. `Compiled.Expr()` is exported
(`xpath/xpath.go:137`), so an external package can compose XPath ASTs.

The W3C published a note on precisely this problem — *Building a Tokenizer for
XPath or XQuery*, a 2005 Working Draft from a joint XML Query and XSL task
force — which enumerates four strategies and develops the state-driven one. It
never advanced past Working Draft, and carries its own admission that the
tables "have not... been exhaustively verified"; a 2004 comment thread records
real bugs in them. Its "scan-while-parse" strategy is what this package does.

If that constraint has to be broken, the estimate is wrong, and Phase 1 is
designed to find that out in week one rather than month three.

### Boundary whitespace

XQuery 3.1 §3.9.1.4: within a direct constructor's content, a run of text that
is entirely whitespace and delimited at each end by the start or end of the
content, another constructor, or an enclosed expression is *boundary
whitespace*, discarded under the default `strip` and kept under `preserve`.
`<a> {1} </a>` yields `<a>1</a>`.

Characters that came from a character reference or a CDATA section "are not
considered to be whitespace characters" for this purpose, so
`<a>&#x20;{"abc"}</a>` keeps its space under either policy. Both BaseX and
Saxon implement exactly that sentence as a single boolean tracked while
reading the content — BaseX's `strip &= !entity(tb)`, Saxon's
`containsEntities`. §3.9.1.3 puts stripping in step 1a, *before* reference
expansion, which is another reason it cannot wait for the builder.

This belongs at **parse time**, before `xdmbuild` sees the content, and our
own code forces that: `Builder.AppendText` merges adjacent text nodes
unconditionally, so once text reaches the builder the boundaries are gone.
`declare boundary-space` is a prolog setter and the prolog precedes every
constructor, so the policy is always known in time.

Nothing in the tree implements this today.

### Namespace declaration attributes force a two-pass start tag

An `xmlns` or `xmlns:p` attribute in a direct constructor's attribute list is
not an attribute node. It joins the *statically known namespaces of the
constructor*, which means it affects how the element's **own** name resolves,
and its **sibling attributes'** names, and everything nested inside it.

So the whole attribute list has to be scanned for `xmlns*` before any QName in
that start tag can be resolved — including the element name, which appears
first in the source. BaseX does this by saving the position, running the
attribute loop twice, and restoring the namespace stack afterwards.

Its value must also be a compile-time constant: an enclosed expression in a
namespace declaration's value is the static error XQST0022, which is the
reason the whole thing has to happen at parse time rather than at evaluation.

A related asymmetry to design for: a direct constructor resolves its names
statically, while a computed constructor given a *name expression* resolves
them at run time — so XQDY0074 and XQDY0096 are dynamic errors with no direct
equivalent.

## What the test suite would give

Counted from `testdata/qt3tests`, parsing every test-set with an XML parser
and inheriting test-set-level `<dependency type="spec">` onto its cases:

| | cases |
|---|---:|
| Total | 31,821 |
| **XQuery-only** | **8,907** |
| Both XQuery and XPath | 7,865 |
| XPath-only | 88 |
| No spec dependency | 14,961 |

An earlier regex-based count said 6,393. That was low: it missed dependencies
declared once at test-set level and inherited.

The harness is in better shape than expected. `TestCase.Test` already holds a
whole query rather than a bare expression, and `Modules []struct{URI, File}`
is already parsed. Assertions are language-neutral. Two changes run XQuery:

- `specInScope` (`tests/qt3/runner.go:246`) switches only on `XP*` and falls
  through for everything else; it needs `XQ10`/`XQ30`/`XQ31` arms and an
  XQuery target.
- The evaluation seam is one line — `xpath.Eval(tc.Test, ctx, ns)` at
  `tests/qt3/runner.go:551`.

XQuery cases differ in shape: a complete query, often with a prolog, usually
wrapped in `CDATA` because direct constructors contain `<`.

## Phases

| Phase | Contents | Est. |
|---|---|---:|
| **1** | Skeleton, constructors, the `{`…`}` extractor, `xdmbuild` policy, boundary whitespace, enough harness to measure | 2,500–3,500 |
| 2 | FLWOR as a tuple stream | 2,000–3,000 |
| 3 | Prolog | 1,500–2,000 |
| 4 | `typeswitch`, `switch`, `try`/`catch`, `ordered`, pragmas | 1,000–1,500 |
| 5 | QT3 wiring and the conformance grind | 500 + tail |
| 6 | Modules | 800–1,200 |
| 7 | `validate` and schema-aware, optional | 1,000+ |

Core is roughly **8,000–11,000 lines**, against `xslt`'s 42,000 — the
difference being everything `xpath` already provides. The conformance tail is
where the time actually goes, on this repository's own history.

### Phase 1 exists to kill the lexer risk

Its job is not breadth. It is to end with a measured pass rate on the
constructor test sets, which converts the one unknown that can be wrong by a
factor of two into a data point. Everything after it is ordinary
recursive-descent work over a grammar that can be read.

### The second risk, named early

`order by` and `group by` semantics exist in this tree, but inside `xslt` and
bound to XSLT types — `applySorts` and `makeSortValue` in
`xslt/instructions.go`, and `xslt/grouping.go`. Phase 2 will want them.
That is another extraction of the same shape as `xdmbuild`, and it should be
a deliberate decision rather than a discovery made halfway through.
`xpath.GroupingKey` and `GroupingEqual` are directly usable.

## Related

[conformance-gaps.md](conformance-gaps.md) for where the existing suites
stand. [known-gaps.md](known-gaps.md) for the diagnosis behind the hard ones.
