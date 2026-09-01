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
`ordered`/`unordered`, pragmas. Verified absent: the only occurrence of
"typeswitch" in the tree is in the reserved-function-name list at
`xpath/parser_path.go:1745`.

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

Instead, follow BaseX rather than Saxon. Saxon uses a genuine mode-switching
tokeniser with an explicit state field; BaseX parses the source directly with
no separate token stream. BaseX's shape fits here because our parser is
already hand-written recursive descent:

- The `xquery` package parses constructor syntax **scannerlessly**, over the
  raw source, reusing `xpath.Token` and `xpath.TokenKind` (both exported).
- On reaching an enclosed expression `{`, it finds the matching `}` —
  tracking nesting, string literals, comments and `{{`/`}}` escapes — and
  hands that **substring** to `xpath`.

XML syntax stays confined to the constructor parser. The conformant
expression lexer is never modified. `Compiled.Expr()` is exported
(`xpath/xpath.go:137`), so an external package can compose XPath ASTs.

If that constraint has to be broken, the estimate is wrong, and Phase 1 is
designed to find that out in week one rather than month three.

### Boundary whitespace

XQuery 3.1 §3.9.1.4: within a direct constructor's content, a run of text that
is entirely whitespace and is not adjacent to a character reference, entity
reference or CDATA section is *boundary whitespace*, discarded under the
default `strip` and kept under `preserve`. `<a> {1} </a>` yields `<a>1</a>`.

This belongs at **parse time**, before `xdmbuild` sees the content, and our
own code forces that: `Builder.AppendText` merges adjacent text nodes
unconditionally, so once text reaches the builder the boundaries are gone.
`declare boundary-space` is a prolog setter and the prolog precedes every
constructor, so the policy is always known in time.

Nothing in the tree implements this today.

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
