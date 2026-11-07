# Known gaps

Why the hard gaps are hard. For the current figures and a case-by-case verdict
on what is fixable, see [conformance-gaps.md](conformance-gaps.md); this file
is the diagnosis behind the entries there. Nothing here is aspirational: if a
gap has no entry, it has not been measured.

The file is named for gaps, and it holds only gaps. An entry whose subject has
been fixed is not a gap any more and is deleted, however carefully it was
written; the changelog and `docs/conformance-gaps.md` are where closed work is
recorded. Three kinds of thing survive, and the file is in three sections:

- **Open gaps** — what is failing now. A diagnosed cause, and what a real fix
  would cost where the answer is a rewrite rather than a patch.
- **Deliberate divergences** — behaviour that disagrees with a test on
  purpose, each with the spec clause it rests on and the **measured** cost of
  changing it. These are not gaps, and they are kept because without the number
  someone re-attempts a bad trade.
- **Corrections** — where a verdict recorded in this file was *wrong*. Not
  "this is fixed", but "this file said X and X was false". They sit at the
  bottom, away from the open work, because their whole value is stopping the
  next reader re-deriving a conclusion that has already been disproved.

Durable design rationale that is not a gap — why the occurrence counters are a
vector, why saturation is right for the matcher and wrong for the derivation
checks, why a depth bound is not cycle detection — has moved to
[xsd.md](xsd.md#limits), where the code it constrains is documented.

A note on which direction matters. A **false reject** is valid input refused;
a **false accept** is invalid input allowed. False rejects are the more serious
kind — they break working documents — so they are listed first throughout.

## The parser's depth and chain limits report `XPDY0130` now

**Fixed 2026-09-14.** XPath 3.1 §2.3.1 says that where "limitations may exist
on the maximum numbers or sizes of various objects. An error **must** be
raised if such a limitation is exceeded [err:XPDY0130]". Five limits reported
the parse error `XPST0003` instead: `maxParseDepth` and the operator chain
bound in `xpath/parser.go`, the type-nesting twin in `xpath/parser_path.go`,
and XQuery's constructor and nesting caps. Each refuses a **well-formed**
expression that is merely deeper or longer than this processor will parse.

The recorded reason for keeping `XPST0003` was that "callers and the
conformance suites match on it". Measured, no suite case does: QT3 and both
XSLT suites are unmoved with the code changed. The messages are unchanged and
every site still wraps `xdm.ErrResourceLimit`.

## Where the numbers stand

Current conformance figures live in **[conformance-gaps.md](conformance-gaps.md)**,
which names every failing case and says whether it is fixable, and are re-measured
by `tests/check.sh`. They are deliberately not repeated here: this file explains
*why* the hard gaps are hard, and a percentage copied into two places drifts in one
of them.

For orientation only, and re-derived rather than inherited: XPath 2.0, 3.0 and
3.1 and RELAX NG are at **100%** with no failures at all; XSLT 2.0 has 8
failures of 6,201; XQuery 3.1 has 1 of 30,517; XSLT 3.0 has 17 of 11,518;
XSD 1.0 disagrees on 29 of 39,388 and XSD 1.1 on 30 of 41,598. Everything below
is an account of those 85 cases, or of a decision that produced some of them.
The 85 is the sum of the nine figures above, computed from
[tests/conformance/results.json](../tests/conformance/results.json) rather than
written: `tests/docfigures.sh` re-derives it from `tests/ratchet.txt` and fails
if this file and the generated table disagree.

What this file adds, and that one does not:

- the diagnosed cause behind a gap, rather than its error code;
- what a real fix would cost, where the answer is a rewrite rather than a patch;
- the measured price of each deliberate divergence, so it is not re-attempted;
- DTD and XDM, which have no public suite and so appear in no percentage.

---

## Open gaps

Real gaps: a genuine bug or an unimplemented rule, failing now. Ordered by how
much each costs.

### Two edges of a transform's lock (XSLT, Go API)

A transform's runtime takes one re-entrant lock wherever host Go code could
reach it from another goroutine: when a function item it made is called
after it returns, around every call into a Go-built function item or a
context-taking resolver during it, and in `fn:transform`, whose nested
transform shares its caller's lock. Two routes stay outside it:
- **Evaluating the raw expression tree.** A host that calls `Expr.Eval` on
  `Compiled.Expr()` with a context it was handed, instead of
  `Compiled.Eval`, takes no lock.
- **Items passed between separate `Transform` calls.** Two top-level
  transforms have two locks. A host that hands each one's function items to
  the other and calls them from two goroutines in opposite directions can
  take the locks in opposite order. Within one `Transform`, `fn:transform`
  included, there is one lock.

### `xs:keyref` memory grows with the square of document depth (XSD, performance)

Validating identity constraints is correct but not cheap on large documents.
In `xsd`'s identity benchmarks, a `keyref` over nested elements allocates
3.2 MB at depth 240, 11.8 MB at 480 and 45.4 MB at 960: about four times the
memory for twice the depth. A key or keyref over 40 sibling fields allocates
83–90 MB. v1 measures the same allocations, so this is not a v2 regression.
The cause has not been profiled; the audit that found it suspects the key
tables being copied per scope and target nodes being walked again. Found by
an external audit of v2.0.0; the figures are the `BenchmarkIdentity*`
benchmarks in `xsd/`.

### Memory: what the third memory audit left open (resource limits, performance)

Found by the third memory audit (2026-11-01) and not fixed, each with the reason:

- **No bound on the total across many `fn:doc` calls.** The per-document
  `MaxBytes` holds, but 40 distinct 500k-element documents were held at 846 MB
  live. A correct total needs a per-evaluation set of loaded URIs (the
  `FileResolver` cache is shared, so charging on a miss undercounts), state
  carried into nested `fn:transform`, and every reader covered. Lower
  `FileResolver.MaxBytes` or the allowed roots meanwhile.
- **`Env.MaxDigits` covers casts, constructors and binary arithmetic, not
  `CastAtomic`, `fn:sum` or `fn:avg`**, which take no context.
- **RELAX NG's derivative cap still refuses some valid documents.** The choice
  merge fixed `(a?){n} a{n}` up to about n = 30. At n = 50 and 100 the cap is
  reached again, though in 20–300 ms rather than 19.9 s. Hash-consing
  derivatives would close it.
- **`xsl:number level="any"` is quadratic in CPU** (40k items 44 s). A cached
  prefix count is not valid, since `count` and `from` patterns may use
  `current()` or per-call variables.
- **`fold-left` appending one item a step is quadratic in allocation**
  (40k items 12.4 GB, all garbage). Appending in place is unsound while other
  references may share the prefix.
- **Identity templates allocate about 3.5 KB a node of context copies**
  (`withSel`, `hostWithCurrent`, the leaf-hook copy). Built-in shallow-copy of
  a document node builds the result once in a mode with no template rules
  (v2.2.0), but still builds it and copies it into a document in a mode that
  has rules, where a rule could add an attribute that must stay `XTDE0420`.
- **Several large text values in one document cost about 1.3× their size to
  parse** (eight 3 MiB values: 1.30×). A single value is about 1.1× at every
  size since v2.1.1; with several, the first value's buffer overshoots once
  before each later value is copied exactly once.
- **An XPath expression in 1.0 compatibility mode copies its context on every
  evaluation** when the caller's context is not in that mode (96 bytes a call;
  12× of an XPath 1.0 transform's allocation in go-xmlsec). The mode can be
  set only on a `Compiled` (`WithCompatMode`), and a `*Context` is updated in
  place during evaluation, so a cached copy would be unsafe. Needs a
  context-side setter: new API, so a minor release.
- **`c14n.BytesNodeSet` on a caller's own `NodeSet` still costs about 2.1× its
  output.** Sizing it would call `Contains` twice per node, which the API
  promises not to do; the package's `Subtree` and `ExcludeSubtree` are sized.
- **XQuery inline functions capture their whole scope.** The XPath and XSLT
  closure (`InlineFunctionExpr`) keeps only what its body reads;
  `xquery/inline.go` has its own implementation.

### Security audit at `e728478a`: what the fix round left open (security, resource limits)

The audit's findings were fixed in round SA (see the changelog and the
Twenty-first pass in [security](security.md)). These remain, each with the
reason:

- **The text store's block ceiling panics under methods with no error
  return** (`xdm/record.go` `newBlock`, 1<<17 blocks, about 1 GiB of copied
  text in one tree). Every entry point that returns an error (xdm parse,
  `ParseBursts`, `ProcessXInclude`, `xpath.Compiled.Eval`, `xquery.Query.Eval`,
  `xslt.Compile`/`Transform`/`TransformReader`, xsd typed copies) returns
  `xdm.ErrResourceLimit` and no result; a host calling `Node.SetValue`,
  `AppendCopy` or `xdmbuild` methods directly still gets a panic, whose value
  is an error matching `ErrResourceLimit`.
- **Compilations in one process still run one at a time** (`compileSlot` in
  `xslt/stylesheet.go`), so one slow compile delays every other; audit run-2
  measured a trivial compile waiting 944 ms behind a slow static phase. Since
  round SE the wait is cancellable: `xslt.CompileContext` gives up with
  `ctx.Err()` while it waits for the slot, and the static phase runs under
  `ctx`. The slot stays because the compiler's remaining package globals feed
  about 60 free functions in the streaming analyser; moving them into the
  per-compilation `compileEnv` is the cure, and round SE moved the package
  serials and composition caches there first.
- **Audit run-2 (scoped, at `67d70cbc`) left three units deferred**, all
  closed since: the RELAX NG list-derivative `MaxPatternSize` check and the
  §7 restriction-walk memo in round SD, and the sibling resolvers' UNC
  handling in round SC (untested on a Windows host). Run-1's other deferred
  units, the lazy caches on concurrency-safe objects and the XPath
  constant-folding budgets, were reviewed in run-2: the caches are
  synchronised, and folding was fixed in round SC (see [security](security.md)).

### Security audit run-3 at `1ef2de62`: what round SD left open (security, resource limits)

Run-3's 46 confirmed findings were fixed in round SD, and what round SD left
open was worked in rounds SE and SF (see the changelog and the Twenty-fourth
and Twenty-fifth passes in [security](security.md)). Closed since:
`xsl:import-schema` closures are charged against `CompileOptions.MaxSchemaBytes`;
`xdm.FlattenContext` and `XIncludeOptions.Context` make both walks
cancellable, and the CLI's `-timeout` now covers `-xinclude`;
`c14n.FromXPathFilterContext` and `FromXPathContext` take a context and spend
one shared budget (go-xmlsec and other verifiers should switch to them, since
`FromXPathFilter` still runs under `context.Background()`); and small
streamed bursts share their ancestor copies, so the 16M spine-copy refusal is
a backstop that only a spine of tens of thousands of attributes over large
bursts reaches. These remain, each with the reason:

- **Other `xdm.Flatten` and `AtomizeChecked` callers take no context and are
  bounded by xdm's ceiling, not the host's `MaxItems`**: XSLT serialization
  and stream paths, general comparisons, and `xquery/eval.go`'s
  `appendSequence`. `array:flatten`, `fn:data`, `xsl:copy-of`,
  `xsl:value-of` and `xsl:attribute` flatten under the evaluation's context.
- **`scopeBindings` and `declares` in `xslt/instructions.go` are quadratic in
  an element's in-scope namespaces** (time only, no retention).
- **XSD 1.1 subsumption on a long group-ref chain still costs about 1 s per
  restriction type** after the sort fix.
- **Query-side `fn:transform` gets a fresh compile budget per top-level
  call.** Inside a transform every nested `fn:transform` shares one budget
  (round SF), but an XPath or XQuery evaluation that calls `fn:transform`
  several times starts each call's transform with its own.
- **A stylesheet that declares the exact URI a generated `xsl:original`
  namespace uses** (`http://go-xml.invalid/xslt/original/N`) has that
  namespace excluded from its results along with the generated one. The
  exclusion is now exact rather than by prefix, so only that spelling is
  affected. Relatedly, the comments on `packageVarNS` and `overriddenMarkerNS`
  call their `go-xml.invalid` URIs ones "no stylesheet can write", which is
  false: a stylesheet can write any URI, and these are merely unlikely.
- **A DTD content model of the shape `(a1*|…|an*)*` still stores about n²
  follow entries.** Shared follow pieces made a repeated choice of plain
  members linear, but not this shape; it is bounded by the 2²⁰ representation
  budget (`maxSequenceMatcherEdges`), so a wide one is refused, not slow.
- **Withheld and deferred from run-3, still open:** the NT `\??\` prefix fix
  is untested on a Windows host (string-level tests only); the run-3 deferred
  units (RELAX NG compact parser and `dtd.Parse` panic review, array walkers
  in serialize and deep-equal, UTF-16 decoding, RELAX NG XSD-datatype pattern
  recompile) need a run-4.

### Memory: what the footprint round left open (performance)

Measured on branches in 2026-11 and not adopted, each with the reason:

- **A 32-byte node record** (the tree pointer read from a chunk header):
  −11 to −22% retained, rejected because a by-value copy of an `xdm.Node`
  would then read foreign memory instead of failing safely.
- **Compact attribute records** (16 B, nodes made on demand): −10% retained on
  parse-100mb, −16% on SAML, about 0 on XMark, but +10–25% CPU on
  attribute-heavy XPath, c14n and copies (+45% worst case), and a held
  attribute would cost 62 B instead of 40. Pointer identity of `*xdm.Node`
  forces a real record per held node; denser storage needs a value-type node
  handle, which is a breaking change.
- **Template-driven shallow-copy does not share source text.** Deep copies
  (`xsl:copy-of`, `xdm.Copy` of 4,096+ records) borrow the source's text
  blocks; a template-built copy adds each value as a string.
- **Holding millions of namespace nodes costs a sweep per collection.**
  Namespace nodes are held weakly since v2.2.0; while a tree's table is not
  empty, every GC cycle sweeps it.

### Streamed execution covers `xsl:source-document` and `TransformReader`'s principal input (XSLT 3.0, performance)

An `xsl:source-document streamable="yes"` whose body has a known and verified
§19.8 verdict runs on the streamed core: the document is read through a
cursor and never built. Everything else builds the tree, as before. The core
takes literal result elements, `xsl:element`, `xsl:copy`, `xsl:attribute`,
`xsl:if` and `xsl:choose`, nested `xsl:source-document`, `xsl:for-each` and
`xsl:iterate` over a consuming path, selects split on the cursor, and
`xsl:apply-templates`, `xsl:next-match` and `xsl:apply-imports` into verified
streamable modes: rules chosen as the tree chooses them (priorities,
precedence, patterns decided on the start tag), every `on-no-match` built-in,
tunnel and ordinary parameters, `#current` and mode switches, to any depth on
one cursor. `xsl:for-each-group` with `group-adjacent`,
`group-starting-with` or `group-ending-with` over a path of child steps
(or the current group of an enclosing grouping) streams: each item's key
or pattern is decided on its start tag, each group's body runs at its
first item, and `current-group()` is read once, in order, off the cursor
by `xsl:copy-of`, `xsl:value-of`, `xsl:sequence` in an element, a
reducer (`count(current-group())`, `sum(current-group()/@v)`), an
`xsl:for-each`, `xsl:iterate` or `xsl:apply-templates` over it, or a nested
grouping. Its errors come in the tree's order (predicates, then keys, then
bodies), `TransformReader` included. A grouping over a grounded population
of values (`group-by`, `xsl:sort`) reads its select off the cursor and
groups as the tree does. `xsl:iterate` carries copies of its items
(`xsl:next-iteration` with `copy-of(.)`). A consuming attribute value
template on a literal result element (`<g n="{count(current-group())}"/>`)
streams. `xsl:try` streams when its catches read only the start tag (or
nothing of the document): the body's output is buffered as in tree mode, a
caught error skips the rest of the context, parsed and checked, and the catch
runs on the view. A parse error after a caught error is still the document's
error. Calls on stylesheet functions split as their bodies when the call is
its body exactly (one `xsl:sequence`, focus free, the arguments `.`, a path or
literals of the parameter types, a result type the body certainly has);
a call it cannot inline, recursive bodies included, runs on values, or on
each match's grounded copy when its body reads only what the copy has (since
L2, also names, attributes and existence of its ancestors' copies: a body
that climbs). `P ! R` of atomic values is the path
`P/R`. Streamable attribute sets run on the view. Accumulators applicable
to a streamed document are computed as it is read: each rule fires as the
cursor passes its node's start or end (text, comment and processing-instruction
nodes too, inside skipped and copied elements); `accumulator-before` reads the
value of an element whose start tag the plan is at, or of the document node,
and `accumulator-after` reads it once the element has been read, or reads the
element to its end when nothing else does. `use-accumulators` on
`xsl:source-document` and on the initial mode is honoured, XTDE3362 as the
tree raises it, and an accumulator's error is the tree's: since L4
(b90d54e8) both modes follow §18.2.5, so a read fails only when the
value it asks for depends on the computation that failed, and values set
before the failure stand (the tree no longer fails every read of an
accumulator whose walk failed anywhere). A cycle is the exception (§18.2.6,
39164236): once the walk meets an accumulator whose value depends on itself,
every read of it raises XTDE3400, in both modes, and a rule reading the value
it is computing is not streamed (`error-3410a`, `accumulator-080s`).
`xsl:fork` and `xsl:map` stream: with at most one prong (entry) reading the
stream, as the sequence constructor they are; with several, at the document
node of a container, each reading prong parses the document itself over one
read of its bytes (`xdmstream.Cursor.Tee`), the prongs taking turns, outputs
in prong order and the first failing prong's error the result, as the
tree's; when every reading prong is a fold (counts, sums, existence, values
off the view) one walk of the cursor runs them side by side instead. A fork or
map per record runs on a grounded copy of the record. The cursor parses in
steps on the reader's goroutine; where goroutines remain (a tee's prongs, a
merge's inputs) each hands xpath's `EvalLock` back at each switch, so a host
function item cannot hang it. `xsl:merge` streams the documents of a
`for-each-source` merge source each on a cursor, up to 64 at once (each holds
its file, cursor and coroutine until the merge ends; a merge of more runs on
the tree: `mergeStreamsMax`, a resource limit kept from rounds SC/SE; §2.1
gives "limits on the size of source documents that can be transformed" as its
example of an implementation-dependent feature; no W3C case merges more than
64), merged by key as the tree
merges (an action may call a named template of its module, write
`xsl:result-document`, or apply templates: no rule it reaches sees the
action's focus, and a rule's messages are taken back on a re-run); any error re-runs the merge on the tree,
except one met before anything is read, which is the tree's first and is
returned at once (L4). Since L4 (df063a51) both modes collect the
sources not read on a cursor first, then the cursor ones: the order across
the merge's trees is implementation-dependent (XPath 3.1 §2.4.1), and this
one lets a collected source after a streamed one stream.

Since the gap lanes (G1–G7) the core also takes:
- **selects at the document node that need their matches** (`remove`,
  `insert-before`, `subsequence`, `treat as`, `deep-equal`, a sequence of
  copies): held, capped at the records the tree would hold; past the cap,
  or when a match is the document element, the tree answers. `count`,
  `exists`, `empty`, `boolean` or `not` over such a pass-through counts
  instead of holding;
- **set operators**: `union`, `intersect` and `except` of downward paths,
  and `(A | B)/C`, as one walk, each match once in document order; with a
  variable's nodes (`count(P union $v)`, `P except $v`, `$v intersect P`, and
  `P union $v` under any holder: `copy-of`, `sum`, a filter, a map)
  rewritten or unioned by the outer, since a variable never holds a streamed
  node (but for a `doc()` of the streamed URI: the alias guard below); `A/T union B/T` with one shared tail of axis steps, and `($v, P)/T`,
  `insert-before(P, n, $v)/T`, `[A, B]?*/T` with no member a path of the
  stream (one is free-ranging by §19.8.8's operand table: `XTSE3430`,
  `sx-square-array-201`) or `($v | P)/T` as the union of the paths they make;
- **path algebra**: `P ! Q` as `P/Q` where that is the map exactly (Q downward
  from child-step matches), `P ! R` with R making nodes or climbing as a
  per-match tail, `tail(P)`, position filters, positional step predicates
  beyond `[n]`, `F(if (C) then A else B)` with C motionless, and
  `(if (C) then A else B) ! R` as the choice lifted over the map; `P ! R` over
  text, comment or PI matches on each kept leaf, `P ! (* union $v)` (any set
  operator of the match's nodes and a variable) on each match's grounded
  copy, and a tail that is a sequence of value paths
  (`P/(@a/string(), c/string())`);
- **loop populations**: `xsl:for-each` and `xsl:iterate` over `head`,
  `tail`, `subsequence`, `remove`, `insert-before`, `unordered`,
  `one-or-more`, `($v, P)`, `P/@a`, `copy-of(P)`, `data(P/@a)`,
  `snapshot(P)`, `(P)[position() = L to H]` or a motionless choice; a loop
  whose select the analysis grounds runs its body as the tree does, `is`,
  `<<`, `>>`, `union`, `intersect` and `except` on the grounded copy included;
  `xsl:apply-templates` over windows of a bare path, and over `(P, $v)`,
  `insert-before(P, n, $v)` or `if ($c) then () else P` with no
  `xsl:with-param` or `xsl:sort` (the other items dispatched as the tree
  dispatches them);
- **kept leaves and climbing**: text, comment and processing-instruction
  nodes a streamed expression keeps are copies that are the document's nodes
  to identity, order and `generate-id()`, under copies of their ancestors; a
  tail climbing from streamed matches (`ancestor::`, `parent::`,
  `P ! ancestor::*`) takes each ancestor once in document order off the
  matches' views and reads only what a view has; leaf patterns may name
  ancestors (`chapter/text()`, `text()[not(parent::v)]`);
- **expressions**: text value templates (motionless parts, or one consuming
  part), `xsl:if`/`xsl:when` with a consuming test, array and map
  constructors, lookups and `array:`/`map:` calls over a path,
  `fold-left`, `fold-right`, `filter`, `for-each`, `distinct-values`,
  `codepoints-to-string` and `snapshot` over a path (its values collected,
  the call made once), function items, `fn:trace(P)` as P, filters over a
  focus-free base, `outermost()` of climbed nodes; `replace()` as `matches()`,
  the range operator `to`, `for`, `some` and `every` over values whose
  return reads only its variables, `generate-id()` of the context element in
  a motionless part, `accumulator-before` in a rule pattern's predicate on an
  element, and `min(.)`/`max(.)` in a rule below the document node;
- **instructions**: `xsl:copy` with a consuming select, `xsl:on-empty` and
  `xsl:on-non-empty`, `validation`/`type` on constructors (on `xsl:copy` of an
  attribute only `strip` or `preserve`), `xsl:message` (messages once and in the tree's
  order, through re-runs and fork prongs; errors in its content swallowed as
  XSLT 3.0 says), `xsl:result-document` (see below), `xsl:fallback` of a
  recognised instruction, `xsl:try` over folds that cannot fail, the
  grouping pattern `.`, groupings over copies, rules for maps
  (`.[. instance of map(*)]`) that match no node; a local `xsl:variable` with a
  consuming select (read where it is bound); a motionless `xsl:for-each` (over a
  variable's items, or the view's own nodes); text value templates with
  motionless parts in a loop body; `group-by` over a streamed population in
  `xsl:fork`, held as copies under the tree's cap and grouped by the tree's
  code; `xsl:comment` and `xsl:processing-instruction` with a consuming
  select; a consuming separator, name or namespace value template of
  `xsl:value-of`, `xsl:attribute` or `xsl:element`.

Since the completion round L (L1–L7) the core also takes:
- **several consumers of one context**: a rule body that reads its element
  twice (two fork prongs, a copy beside a read) runs on a grounded copy of
  the element, at most the tree's memory, the copy carrying the document's
  DTD; a variable's map constructor at the document node whose entries each
  read is a tee of its entries; an `xsl:fork` with several reading prongs in
  an `xsl:try`; `base-uri()`, `document-uri()`, `. instance of
  document-node()` and `unparsed-entity-uri()` at the document node (the
  stand-in has the document's URIs and DOCTYPE);
- **stylesheet functions**: bodies that climb, a body with instructions
  planned inside the caller's plan, a recursive call on the current group
  in a call frame of its own, a path given to a one-node parameter (a loop
  with the tree's cardinality), a call whose value is copied on the document
  element's grounded copy, a call given only values or a variable's copies
  (motionless), `filter(P, f#1)`, `outermost(copy-of(P))`, `xml-to-json(.)`
  written from the parse events, a used package's function verified in its
  package and called as itself, and a used package's absorbing function as a
  call frame compiled in the package's state, its modes planned per package;
- **document-level copies**: `copy-of(.)`, `snapshot(.)` and `value-of` of
  `.` at the document node, written from the events or built once (DTD
  ID-ness and typing kept); `xsl:copy` of the document node with any body;
  copies over a choice of paths; `xsl:sequence` of atomic values in a leaf
  rule; a variable with an atomic `as` atomizing its select;
  `xsl:apply-templates` over text, comment or PI matches, over fresh copies
  (`copy-of()`, `P ! snapshot(.)`, `copy-of(P)`, `(copy-of(P))[pred]`, with
  sort, parameters and `last()` as the tree), over `doc()` of a literal other
  URI, and over values read off the stream;
- **accumulators, try and merge**: rules reading an accumulator of their own
  node (mutual reads stay refused) or `base-uri(/)`; a non-streamable
  accumulator read in a container (XTDE3362 as the tree); values on copies of
  grounded matches and with `copy-accumulators="yes"`; `xsl:try` and
  `xsl:message` reading accumulators; `xsl:apply-templates` in a try when no
  rule it reaches can fail but the built-ins; merges with `last()` in the
  action, `xsl:message`, boolean or global-map keys, `use-accumulators`;
- **set operators and grouping**: `intersect`/`except` of two climbing
  paths; grouping populations that are a window, attributes, text, `node()`,
  `outermost(P)` or a crawling `(//x)[...]` under a motionless body; group-by
  over the root's children as copies made from the events; text value
  templates in a group body;
- **shapes and populations**: loops over attributes, leaves or values whose
  body reads `.`; a motionless `xsl:iterate`; `current()` as the
  instruction's item and in a rule pattern; `key()` over a global's document;
  `has-children()` read by a peek at the dispatched element's start tag;
  populations `if ($c) then P else Q`, `$v union P`, `[A, B]?*`;
  `outermost(P)/T` when P nests; `W(P) ! name(..)`; `xsl:call-template` with
  one consuming parameter the callee atomizes; `xsl:perform-sort` over copies
  or values; `xsl:copy` of an attribute against a `type`; apply-templates in
  an `xsl:map` held in a typed variable; a for-each over a union of two
  same-depth child paths (the trusting walk reads it as striding);
- **trust across modules**: a streamable mode whose rules sit in imported or
  included modules (no package among them), verified by import precedence; a
  module using a package without `xsl:override` of template rules.

Since rounds F and M (F1–F8 fixed what the first full run of round L found;
MM1–MM8 streamed the 120 W3C cases a census found still on the tree) the core
also takes:
- **positions and loops**: `last()` in a motionless `xsl:for-each` (over
  ancestors, attributes, a variable's items), in a filter over the context
  item's own values (`tokenize(., ' ')[position() * 2 gt last()]`), and in a
  for-each over fresh copies (collected first, then looped with their count);
  a for-each over `head(//X)` whose body copies content; a for-each over
  climbed ancestors whose body is motionless on their views; `outermost(D/T)`
  at the document node as `D/outermost(T)`; a loop over a tail run on each
  match's grounded copy;
- **rules and patterns**: `xsl:next-match` in a rule for text nodes; a text
  pattern reading `accumulator-before`; a rule pattern `(L except R)[q]` of
  child steps; a motionless `xsl:evaluate` and `xsl:analyze-string` in a rule;
  a rule whose group is read beside its consuming path, or with a map of two
  consuming values, on a grounded copy of its element, which carries the
  document's accumulator values; a built-in copy of a text, comment or PI
  node at the top of the principal result (`TransformReader` builds the
  document as it reads, so `Result.Nodes` holds the tree's nodes);
- **calls**: a test or predicate calling an inspection function, checked as
  its body; nested relative paths and positions among ancestors on a view; a
  leaf rule returning a stylesheet function of the leaf; an instruction-body
  function given copies and values, or given the match in a tail when its
  body sets no focus and reads only down; a module function given the
  document node, as a call frame; an unclassified function given
  `copy-of(.)`; a dynamic call given grounded copies; a used package's own
  streamable `xsl:source-document`, its accumulators observed;
- **copies**: `xsl:apply-templates` over a variable holding `xsl:copy-of`'s
  copies, an array of snapshots, a global tree with a `copy-of(.)` parameter,
  and `snapshot(@a)` into a tree mode; a select at the document node reading
  the stream only through a copy of the whole document or its element
  (`innermost(snapshot(/chapter)//section)`, `snapshot(/)`, a `snapshot(/*)`
  variable read as `$v/..`); `f(snapshot(.))` in a rule for the document
  element; `xsl:perform-sort` over `copy-of(P)`; `copy-of(/doc/chap[2])`, a
  held select with at most one match; a copy whose tail runs on a grounded
  copy of the document element; a path climbing out of `snapshot(P)` into the
  snapshots' own ancestors; `current-group()!copy-of()` and a
  `copy-of(current-group())` parameter;
- **accumulators**: values typed as maps or arrays of atomic values; a call
  on such a value with atomic arguments; `some`/`every` predicates over
  attribute values in a pattern; a rule whose value is a sequence
  constructor; `accumulator-after` in an end rule (§19.8.9.1 rule 5); a rule
  passing its own value on and calling `resolve-QName(@n, .)`;
- **the rest**: `xml-to-json()` of a path whose matches may nest;
  `min`/`max` below the document node over elements the schema types as a
  list of one atomic type; `xsl:source-document` with a `#id` fragment (read
  to the element; absent, the tree runs); a `group-by` body returning arrays
  or maps of atomic values (members copied as events); an `xsl:try` whose
  catch carries `exclude-result-prefixes`; `xsl:apply-templates` in a
  variable typed as maps or arrays; a merge of a collected path-keyed source
  with streamed string keys.

A stylesheet in which a node of the stream may meet a node of a load that may
return the streamed document by URI (`doc()` other than of a literal
different URI, `collection()`, a non-streamable `xsl:source-document`, an
`xsl:evaluate`) in a node-identity operation (`union`, `|`, `intersect`,
`except`, `is`, `<<`, `>>`, `generate-id`, an `xsl:evaluate` given the stream's
node) does not stream: in tree mode such a load returns the resolver's cached
tree, whose nodes are the stream's own, so "a variable never holds a streamed
node" does not hold. Since MM1 (90b29d95, 2de588ed) the guard follows the
parsed expressions as a flow through variables, parameters, calls and focus
changes, so a literal `separator="|"` or `is` over `doc()` nodes alone no
longer refuses the stylesheet; a stylesheet using a package keeps the
stylesheet-wide rule (L7's), since its modules may compile after the question.

A path
is child or descendant steps by name, wildcard or kind (`doc/rec`, `//rec`,
`/*/*/rec`, `*:rec`, `p:*`, names resolved through `xpath-default-namespace`),
optionally under `outermost()`/`innermost()`, with motionless or positional
predicates. `count`, `sum`, `avg`, `min`, `max`, `exists`, `empty`, `head`
and `[n]` fold as the document is read; `xsl:copy-of` writes one copy at a
time; `xsl:strip-space` is applied as the cursor reads.

`Stylesheet.TransformReader` streams the document it reads in the same way
when the transform starts by applying templates to it in a streamable mode
(§6.6.4): the document node is dispatched on a cursor over the reader, and
the rules run as above. It takes the tree path, `xdm.Parse` then `Transform`
exactly (but for the absent global context item of a declared-streamable
initial mode, below), for an initial template, function or match selection; a mode that
is not streamable, not verified, or reaches a rule or built-in the core does
not run; an `xsl:global-context-item` with a type, an applicable accumulator that is not
streamable or that the observer cannot compute, or
`input-type-annotations="strip"`; a built-in rule that would copy a node of the document to the top
of the principal result inside `xsl:fork`, or under `SourceValidation`
(elsewhere the document is built as it is read, since 5cd3316f, and
`Result.Nodes` holds the built tree's nodes); a deep-copy mode; a select at the
document node that may give up (`min`/`max` over mixed types, unless the
schema types every such element as a list of one atomic type); a stylesheet
whose other modules leave its rules unverified (since L7 imported and included
modules with no package among them are verified across modules by import
precedence, and a plain `xsl:use-package` keeps the using module trusted).

A global no longer keeps the input on the tree (L7, 6cc2247f). In a
streamable initial mode, with no initial template, function or match
selection, the global context item of `TransformReader`'s input is absent, as
XSLT 3.0 §2.3.2 requires ("cannot be nodes in a streamed document"), on the
streamed path and on its tree fallback alike: a global reading the focus
raises XPDY0002 either way (`streamable-116`). This is a deliberate change from
`Transform` of a parsed tree, which still supplies the document node. In the
same way (L4, be09e991) an `accumulator-before#1` or
`accumulator-after#1` item made at a node of a streamed input saves an absent
focus (§10.3.6), so its call raises XTDE3350 on both of `TransformReader`'s
paths (`accumulator-061`); a tree given to `Transform` keeps the focus.

`TransformOptions.Output` serializes the principal result to a writer as it
is built, for `Transform` and `TransformReader`: the builder writes each
element's finished children and takes them back, so memory holds the open
path and what is not yet written, and `MaxNodes` counts only that (a live
count; without `Output` it stays cumulative). The live heap is flat in the
output's size; a `for-each` over `1 to N` holds its N integers (about 146 B
each), in tree mode too, which is not the sink's. `Result.Nodes` and
`Result.Tree()` are nil, and on an error the writer may hold part of the
result. A nested `fn:transform` does not inherit it. Under `indent="yes"` an
element whose content may be indented is held as its serialized bytes (its
indents recorded, about 1× the output rather than a tree's 4–9×) until its end
or a text child decides, by the serializer's rule, whether it is indented: an
indented document is still written only at its end.

An `xsl:try` under `Output` writes each item its body completes provisionally:
the sink and serializer take a checkpoint and the bytes go to a chunked buffer
(about 1× the try's output), committed when the try succeeds and rolled back
when it catches an error. With `rollback-output="no"` and a catch for every
error the output is written through, and XTDE3530 is raised exactly when the
tree raises it (the body produced an item). A try that is the result's first
output (the method it decides is provisional too), a try after text in its
element, in a document node's streamed content, with leading attributes, or
around one large element (built in place, written as made) is written
provisionally as well. A try's output is still held to its end when its element is held (below:
the holder is not the try) or is the content of `xsl:document` (built whole);
when its body starts with a namespace node or a function item (a namespace
node changes bindings xdm cannot take back); when it starts with an attribute
that cannot go on to an empty element yet (the element has content, the try
writes to a sequence level, or into another try's buffer: only the end of the
try can decide); with `rollback-output="no"` and catches narrower than every
error (an outer try could recover where the tree never wrote); and when the
result never streams (`Output`'s fallbacks, a writer that cannot checkpoint).
A consuming try in the streamed core still needs `rollback-output="yes"`.

`TransformOptions.SecondaryOutput` serializes each `xsl:result-document` to the
writer the caller returns for its absolute URI, as it is made (the principal
result's sink, per result), closes it at its end, and keeps nothing in
`Result.Secondary`. A result is held, then written once final, inside an
`xsl:try` body (written when it completes), inside a streamed
`xsl:source-document` or `xsl:merge` (when that ends: a re-run must be able to
take it back), when its URI was already produced, read or is open, and when its
settings need all of it (validation or type, `build-tree="no"`,
`item-separator`, and `Output`'s own fallbacks). `MaxNodes` counts what a
secondary result holds, as under `Output`. Without the option, secondary
results are built in memory and returned in `Result.Secondary`, as in tree
mode.

A validated input streams: `validation`/`type` on `xsl:source-document`, a
merge source with validation, and a principal input with
`TransformOptions.SourceValidation`. The schema validator runs on the cursor's
events and types the records it reads as the tree's typed copy types its nodes
(defaults appended, ignorable whitespace dropped where the typed copy drops
it); an invalid document re-runs a container on the tree for the tree's error,
and gives a principal input XTTE1510 (lax XTTE1515) with the same text on both
paths. An element with an assertion is held as the subtree the assertion reads.
Identity constraints (`xs:key`, `xs:unique`, `xs:keyref`) are evaluated on the
events, keeping only their key tables; element default and fixed values, XSD
1.1 open content and defaulted attributes in a namespace (with the namespace
fixup) are as the typed copy has them; a DOCTYPE is read as the copy reads it,
without the DTD. A document the conformance harness preloads as validated and a
stylesheet reads with the default validation streams by re-reading its file
and repeating the harness's validation (the tree reads the harness's typed
copy); the harness discards validation errors, so an invalid marked document
would succeed on the tree and fail on the stream, which no W3C case does.
Refused per schema, and run on the tree: a content model
nesting bounded `maxOccurs` whose count readings could pass 4,096 on a stream,
which does not know the child count the tree's walk uses; and, for the
conformance harness only, a validated source with a DOCTYPE it has already
validated.

Figures for the core, on this machine:
- a 1 GB file totalled by `xsl:iterate`: 26 MB RSS against the tree's
  4.3 GB; 15.9 s CPU against the tree's 13.8 s;
- `count` and `sum` over 25.9M records (1 GB): 3.0 MB heap, where tree mode
  needs 773–1,284 MB at 100 MB;
- `count(//rec)` under a 900-deep spine: 2.1 s CPU and 3 MB heap against the
  tree's 1.7 s; `count(outermost(//rec))` there: 1.5 s against 10.6 s;
- the identity transform (`shallow-copy`): 100 MB in 4.0 s and 588 MB heap
  against the tree's 2.6 s and 1,011 MB; 1 GB in 46 s and 5.4 GB against 33 s
  and 9.0 GB (the heap is the output, built in memory without `Output`);
- a filter keeping 1 record in 10: 100 MB in 2.3 s and 60 MB against 1.7 s
  and 739 MB; 1 GB in 23.6 s and 624 MB against 23.4 s and 7.4 GB;
- through `TransformReader`: the identity over 100 MB in 4.1 s and 573 MB
  against Parse + Transform's 2.5 s and 1,034 MB, 1 GB in 42 s and 5.2 GB
  against 36 s and 9.1 GB; the filter over 1 GB in 23.3 s and 620 MB against
  22.2 s and 7.3 GB;
- group-adjacent over a 100 MB log of sessions: 1.24 s CPU and 17 MB heap
  against the tree's 1.04 s and 590 MB; 1 GB in 12.0 s and 158 MB against
  13.6 s and 6.0 GB. group-starting-with copying the ERROR entries of each
  run: 100 MB in 2.1 s and 82 MB against 1.4 s and 622 MB; 1 GB in 20.2 s and
  890 MB against 19.7 s and 6.2 GB (the heap is the output);
- an absorbing function per record in a sum (`sum(/doc/rec ! p:cd(.))`):
  100 MB in 2.45 s and 2.8 MB against 2.39 s and 1,045 MB; 1 GB in 30.2 s and
  3.1 MB against 39.2 s and 11.1 GB;
- `xsl:try` around a for-each writing an element per record: 100 MB in 5.5 s
  and 361 MB against 3.6 s and 1,226 MB (the loop alone is 1.8×; the try adds
  about 11%); 1 GB in 61 s and 3.4 GB against 57 s and 11.6 GB (the heap is
  the output, held twice at the end as the tree's try holds it);
- a running total (two accumulators) over a 100 MB log through
  `TransformReader`: 3.6 s CPU and 2.9 MB heap against Parse + Transform's
  2.5–2.9 s and 1.5 GB; 1 GB in 28 s and 3 MB against 42 s and 15 GB; the
  positions of the ERROR entries (`accumulator-before` in a loop): 1 GB in
  29 s and 276 MB (its output) against 58 s and 17 GB;
- four aggregates over 100 MB in one `xsl:fork`: 7.0 s CPU and 3 MB heap
  against the tree's 2.4 s and 865 MB (a parse per reading prong: 1 prong
  1.2×, 2 prongs 1.9×, 4 prongs 3× the tree's CPU, before the fused walk below); 1 GB in 70 s and 3.7 MB
  against 44 s and 7.8 GB (91 s wall, the tree compressing);
- a merge of two 50 MB logs: 12.9 s and 3.7 MB against 8.9 s and 4.2 GB; two
  512 MB logs in 115 s and 3.9 MB (the tree, about 42 GB by the 100 MB
  figure, not run on this 18 GiB machine);
- `TransformReader` with `Output`: the identity over 100 MB in 6.6 s and
  4.0 MB heap against the tree's 2.8 s and 1,002 MB, 1 GB in 64 s and 4.5 MB
  against 41 s and 9.4 GB; the filter over 1 GB in 36.6 s and 9.4 MB against
  31.0 s and 7.4 GB (CPU measured under load; `Output` adds about 8% to a
  held `TransformReader` run);
- a held select, `sum(remove(/B/I/P, 3))` over 100 MB: 3.37 s and 431 MB
  against 2.42 s and 1,147 MB; counted, `count(remove(/B/I/P, 3))`: 56–59 MB
  against 1,029–1,037 MB;
- `count(/doc/rec/sub | /doc/rec/other)` over 100 MB: 2.8 s and 3.0 MB against
  2.3 s and 1,006 MB; `for-each subsequence(/doc/rec, 1000, 100000)`: 2.3 s and
  25.5 MB against 1.6 s and 722 MB;
- `count(//PRICE/ancestor-or-self::*/@CAT)` over 119 MB: 4.07 s and 2.7 MB
  against 2.69 s and 1,117 MB; `head(//PRICE/ancestor::*) ! name()`: 1.48 s
  against 2.42 s (the stream stops early);
- `fold-left` over 100 MB of values: 3.28 s and 134 MB (the collected values)
  against 2.06 s and 1,071 MB; `xsl:try` around `avg(/doc/rec/(@v * 2))`:
  2.23 s and 2.8 MB against 1.61 s and 860 MB;
- an indented identity under `Output` over 100 MB: 4.84 s and 209 MB peak
  (the held bytes) against the tree's 3.01 s and 1,029 MB, where the held
  tree took 535 MB;
- an `xsl:try` around a record loop over 100 MB under `Output`: 40.7 MB heap
  (the provisional bytes) against 430 MB when the try was held as a tree and
  the tree's 1,310 MB;
- splitting a document per group with `SecondaryOutput` through
  `TransformReader`: 100 MB into 717 files in 2.67 s and 2.5 MB heap against
  the tree's 2.50 s and 815 MB (525 MB with the results held); 1 GB into 7,018
  files in 26.1 s and 5.6 MB;
- a validated 100 MB document summed, `validation="strict"` on
  `xsl:source-document`: 6.25 s and 147 MB against the tree's 9.24 s and
  4,195 MB (the tree makes a typed copy of the whole document); a validated
  principal input through `TransformReader`: 6.00 s and 151 MB against
  10.43 s and 4,001 MB.

CPU against the tree, at 100 MB on a loaded machine: `count` 1.1×,
`count(//e)` 1.14×, group-adjacent 1.25×, a fork of four aggregates 1.37×
(3.4× before the fused walk), a filter 1.33×, a for-each 1.28×, the identity
(`shallow-copy`) 1.27× (1.20× in instructions retired; 1.52× before J4), the
two-instruction identity 1.39× (1.18×; 1.58× before). An element no rule can
match is copied off the parse's record, with no view. What remains of the
two-instruction identity's cost is garbage collection, not dispatch work: per
record it allocates fewer objects than the tree, but its live heap of about
1 MB sits under the Go runtime's 4 MB minimum goal, so it collects about 1,970
times over 100 MB against the tree's 16. `GOGC=400` took it from 9.3 to 6.5 s
CPU; that is a process-wide setting for the application to choose, which this
library does not set.

Refused, and run on the tree: a held select past the cap, or whose match is
the document element (unless the outer only copies what it holds, L2); `xsl:apply-templates` over motionless parts or
insertions with `xsl:with-param` or `xsl:sort`, a union with a variable's
nodes that are not fresh, or in a sequence-typed variable outside an
`xsl:map`'s content; a pattern a view cannot decide
(positional, reading content, `id()`/`key()` of the streamed document); a for-each over climbed
ancestors whose body reads more than their views; a set operator with an operand that is not a downward path of the
stream (a loop over a union with a variable's nodes, `intersect` or `except`
of paths with a downward tail, which do not distribute; two climbing tails stream since L5) or whose
predicates may fail; `last()` (but in a motionless loop, a filter of the
context item's values, or a for-each or iterate over fresh copies), a path
tail reading `position()` or `last()` of the path's focus, `reverse`, `sort`;
`group-by` or `xsl:sort`
over a streamed population outside `xsl:fork` (§19.8.4.19 makes them
free-ranging), and in a fork in a rule body under `TransformReader` (no tree to
fall back to past the cap) unless its members are held as copies made from
the events; a grouping population that is not child steps beyond the shapes
rounds L and M take (a window, attributes, text, `node()`, `outermost(P)`, a crawling
`//` under a motionless body); a key or
pattern a view cannot decide; an `xsl:try` where the stream could meet another
error first than the tree (two parts of a select that may fail, a loop whose
select may fail, `xsl:apply-templates` whose rules may fail, or grouping in its body), a consuming
catch (no W3C case has one); a call on a stylesheet function that is not its
body exactly and whose body reads more than values or a grounded copy has (a
conversion that may fail, an ancestor's string value or children, `f($e/..)`, a body with
instructions the caller's plan cannot take, a node-returning category, a global variable, a cache), or in an instruction the core checks on
the view; an applicable streamable accumulator whose rules read more of a node
than its start tag has, whose values may hold a node of the document, that
reads an accumulator other than of its own node by a literal name, or that
accumulators read each other, or whose rule body is more than `xsl:variable`
instructions and one `xsl:sequence`;
`copy-accumulators="yes"` on `xsl:copy`; `accumulator-before` in a grouping's body or on a node
copied out of the stream by other than `copy-accumulators="yes"` or an
apply-templates over copies; an `xsl:fork` or `xsl:map` with several reading
prongs at an element outside a rule body (a loop body, a container's own
group body: no tee there), or in a rule body whose grounded copy would be at
the top of the principal result; a dynamic call `$v(...)` unless `$v` is provably a
map, an array, or a global bound to `accumulator-before#1`/`-after#1`, or the
call is given grounded copies of the stream and values only; two consumers in one sequence constructor outside a rule body (a consuming
`xsl:variable` beside another consumer, several consuming map entries other
than a variable's map constructor at the document node); an
`xsl:merge` source with `sort-before-merge`, a path merge
key on a validated source,
a select that is not child steps, keys of different types at one position
across sources, or an action that
mentions `trace()`, holds `xsl:evaluate`, or holds `xsl:message` or
`xsl:assert` where a key, select or `for-each-source` expression could tell
messages of its own, or calls a named template its module does not declare once
or whose module imports or includes another; an `xsl:global-context-item`.

`<xsl:apply-templates select="@*|node()"/>` in a streamable mode is refused
with `XTSE3430`: §19.8.8.4 makes a union of two striding operands crawling,
and §19.8.4.5 makes a crawling select roaming and free-ranging. `sx-union-202`
expects exactly that refusal and Saxon-EE gives it. The two-instruction form,
`select="@*"` then `select="node()"` (or no select), is guaranteed-streamable
and streams. `sx-union-315`, whose same-depth union the catalog expects to be
accepted, ran on the tree; since L7 (4f2cc85f) the trusting walk
reads a union of two child-step paths of one depth as striding, since their
nodes cannot contain one another, so it streams. The refusal walk keeps
§19.8.8.4's crawling, so no `XTSE3430` moves.

Of the 2,930 W3C cases that touch streaming (measured at 1d789fec), 2,661
stream; 262 are refused as the spec requires (`XTSE3430` 149, decided
statically 13, a static error before planning 6, no streamed input asked for
94); and 7 are proved not streamable. Every other case streams. The 7:
- `accumulator-080s`, `error-3410a`: a cyclic set of accumulators, whose only
  conformant result is XTDE3400 (§18.2.6); both modes raise it at the first
  read, and the planner refuses a rule that reads the value it is computing
  (Saxon 9.8 refuses `error-3410a` statically too);
- `merge-090`: its merge source is the principal input, already a tree;
  re-reading it could see other bytes (one URI is one document in a
  transformation), and streaming the held tree saves nothing;
- `si-group-055`: not guaranteed-streamable. The fork prong
  `xsl:apply-templates select="current-group() except ."` has two striding
  operands, which §19.8.8.4 makes crawling, and §19.8.4.5 makes an
  apply-templates over a crawling select roaming and free-ranging, so the
  mode fails §6.6.4 (the catalog expects output; the tree gives it);
- `stream-006`, `non-stream-006`: FODC0005 for `c:\my\doc\books.xml` before
  any read, in both modes: there is no document to stream;
- `use-package-150`: its input is not streamed; the case supplies its source
  as a tree.

Proofs given earlier and withdrawn, because the cases stream:
`si-apply-templates-008`, `-009` and `si-try-200` (the document is built as
read, 5cd3316f), `streamable-055` (the schema types the fold, 6930e606),
`docbook-003` (read to the `#id` element, 11c4ba52), `si-fork-815` (members
held as event copies, c5c4fa90), and the whole-document cases below.

**Whole-document memory.** These stream holding a copy of the whole document
or of the document element, which costs what the tree costs: they save no
memory, and are counted as streamed only because nothing is held above the
tree's.
- `function-5001`: `copy-of(.)` at `/` is the document built off the cursor;
- `sf-innermost-001`, `sf-reverse-001`, `sf-snapshot-0101a`,
  `sf-snapshot-0101b`: `snapshot(/chapter)`, `snapshot(/)`, `snapshot(/*)` of
  the document built;
- `sf-unparsed-entity-03`, `-04`, `use-package-152`: `snapshot(.)` of a rule's
  match that is, or may be, the document element;
- `si-perform-sort-001`, `si-group-040`, `si-group-065`: copies of every child
  of the document element when that is the list or the group;
- `function-5004`, `-5005`, `-5014`, `-5015`, `function-5006`: the document
  element grounded whole for a tail;
- `si-apply-templates-008`, `-009`, `si-try-200`, and any main-mode run whose
  built-in copy may put a leaf at the top of `Result.Nodes`: the document is
  built as it is read;
- an `xsl:fork`, or several consumers, in a rule for the document element
  (A2-8): the rule runs on a grounded copy of the element, 436 MB against the
  tree's 436 MB at 50 MB. The low-memory tee needs the fork at the document
  node; in a container it streams in 4 MB.

Memory below the tree's is the claim for the rest (measured exceptions: A2-7
and A4-F4, in the next section). Measured for the one
grouping that holds most (`si-fork-815`, `group-by` over the document
element's children, 200,000 transactions, 10 MB): peak live heap 73.5 MB
streamed against 80.1 MB on the tree with the case's body, whose result is
most of both; 37.6 MB against 46.1 MB with the body summed.

In the W3C XSLT 3.0 run 2,661 cases stream (9 before the second burst shape,
364 before `//` record paths, 450 before the core, 576 before streamable
modes, 606 before `TransformReader`, 629 before grouping, 707 before `xsl:try` and functions,
877 before accumulators, fork, merge and streamed output, 922 before the
§19.8 analysis was completed, 1,009 before the gap lanes, 2,124 before
validated inputs, 2,180 before the completion lanes J1–J4, 2,281 before rounds
L, F and M): core 2,468 runs, main 175, merge 34; 23 fall back (26 before
`P instance of T*` was counted, round SG) and 137 re-run on the tree, with 0
guard hits. Where each of those builds the tree is the next paragraph.

**Where a requested, guaranteed-streamable construct still builds the tree
(T1, §27.5: a processor claiming the feature "must use streamed processing"),
measured at round SG on the W3C run.** Each bucket, its count, why, and the
plan:
- *A select at the document node that gives up* (23 cases, all at runtime,
  the select re-read on the tree: `streamtrace` fallbacks). 13 hold their
  matches and pass the tree's record count (`streamhold.go`): the nine
  `sx-square-array-040`–`-043`, `-117`, `-140`–`-143`
  (`[$x, P/copy-of()]?*!PRICE`: a copy of every record, which is most of the
  small document), and `sx-treat-102`, `-103`, `si-try-022`, `-023`
  (`(P, $b) treat as node()*`, its matches held for the treat). 10 would ground
  the document element (`noRoot`, `streamsplit.go`): `si-copy-of-121`, `-122`,
  `-125`–`-128` (a validated copy of `*` held in a variable: the copy is the
  document, whole-document memory by the stylesheet's own demand),
  `sx-instance-of-010` and `sx-treat-010` (`(.//* | .//text())` under
  `instance of`/`treat as`), and `si-try-001`, `-010`
  (`outermost(.//node()) treat as comment()*` and the union). Plan: `treat as`
  as a per-item check on the view ahead of the outer (as `P instance of T*` is
  now: `streamsplit_instance.go` counts the items failing T's kind test, so
  `sx-instance-of-001`, `-102`, `-103` no longer fall back), and the same
  rewrite for a union, which the split does not yet take with predicates on
  its branches at the document node. The square-array and copy-of shapes keep
  memory equal to the tree's, as the whole-document cases below do.
- *Any error re-runs the container on the tree* (137 cases): the bullet below.
  Each is a container that failed (XTTE1510/1512/1540 on validated input 37,
  XPDY0050 treats 17, FORG0006 16, XPTY0004 14, FOCH0002 collations 10,
  FORG0004/0005 8, the rest one to three each, 24 codes). The memory is
  spent only on the failing path; streaming the error directly needs, per
  construct, a proof that the stream's first error is the tree's, which the
  differential checks and no construct has yet.
- *A `DocumentResolver` that is not a `FileResolver`* (0 W3C cases: the
  harness uses `FileResolver`): see the bullet below. It needs new API.
- *An unverified or unknown §19.8 verdict* (static, the analysis's figure:
  56 unverified and 100 unknown containers in the W3C streaming stylesheets,
  §19.8 below). A case counts as streamed when any of its containers streams,
  so these do not show in the runtime census; they belong to the analysis.
- *A merge of more than 64 inputs* (0 W3C cases): kept, a resource limit (above).
- *No `TransformOptions.Output`* (every case the harness runs without it): the
  result tree is the output, so it is built; inherent. With `Output` the
  held parts are listed in the bullet below.
- *Whole-document memory* (the list above): they stream, holding what the tree
  holds.
Also:

- **Without `TransformOptions.Output` the result is built in memory**, so a
  large filtered copy grows with its output and stops at the result-node cap.
  With it, `MaxNodes` counts only what is held, but some results are held
  to the end and then written exactly as `Result.Serialize` writes them: the
  json and adaptive methods, `build-tree="no"`, `item-separator`,
  `normalization-form`, `standalone`, `doctype-system`/`-public`,
  `parameter-document`, HTML5 under html or xhtml. Partly held: under
  `indent="yes"` (the html and xhtml default) an element at the top whose
  content is indented, so an indented document with no text in its document
  element is held whole, as its bytes (above); html `head` and `meta`;
  elements with `inherit-namespaces="no"` or validation; a try's output in
  the cases listed above; a variable's or function's content; secondary
  results without `SecondaryOutput`. A namespace node added to an element
  after its content raises XTDE0410 (§5.7.1) under `Output`, where the tree
  builder admits it and `Result.Serialize` succeeds: the start tag is already
  written. Temporary work per record is not
  charged cumulatively since v2.3.0: a loop item that writes nothing to the
  output hands back up to 4,096 nodes and 64 KiB it dropped
  (`TransformOptions.MaxNodesBuilt`/`MaxBytesBuilt` lift that cap). An item that
  writes output keeps all it built charged, so a stream that both writes and
  builds temporaries per record reaches the cap sooner than its output alone.
- **Growing state is bounded only per value or per expression.** A carried
  integer multiplied each record (`MaxDigits`), a sequence appended to or
  passed through `distinct-values` each record (`MaxItems`) run quadratic time
  and are stopped by the context deadline, before and after v2.3.0.
- **Any error re-runs the container on the tree** (a container: see the
  `TransformReader` bullet for a streamed principal input), so errors are the tree's,
  in the tree's order. A cancelled context is the exception: since 383ed3d4 the
  stream returns the context's error within one input read, with no re-run. The cost is a second read of the document, and the
  1 GiB string budget charges the strings both attempts built. Under
  `xsl:try rollback-output="no"`, output already written can turn a caught
  error into `XTDE3530` where the tree fails before writing; no case shows it
  (a consuming try with `rollback-output="no"` is refused, so it runs on the
  tree; under `Output` the try's own XTDE3530 is the tree's).
  A container whose result is a sequence that would hold a node of the stream
  at its top runs on the tree, as the tree keeps the source's own nodes there.
  A built-in rule's copy of a text, comment or PI node at the top of an
  `xsl:result-document` body is a new node, in a container and under
  `TransformReader`: `SecondaryResult.Nodes` holds the copy where the tree
  holds the source's node (the bytes are the same).
  Differential fuzzers (`TestStreamFuzz` with a grammar per stage and lane,
  `TestStreamFuzzMain`, `TestStreamFuzzMerge`, `TestStreamOutFuzz`,
  `TestStreamFuzzG6`, `TestSecondaryOutputFuzz`) compare streamed and tree
  runs exactly; each is run at 100,000 cases when a stage lands.
- **`TransformReader` reads its document once**, so it cannot re-run on the
  tree as a container does. Everything that would need the tree is refused
  when the stylesheet compiles (a node of the stream at the top of an
  `xsl:result-document` body, which `SecondaryResult.Nodes` would hold,
  included), and its errors are the tree's as they come. A
  malformed document or a parse limit is reported as `xdm.Parse` reports it
  even when the transform failed first: the rest of the document is read
  before the error is returned (not after a cancelled context). A defect of
  the streaming plans, which a container would hide by re-running on the
  tree, is returned as `streaming: internal error: ...`, because there is no
  tree to answer. None occurs in the W3C run or in 100,000 fuzzed cases.
  `generate-id()` of a node of a streamed document has another tree number
  than the same node parsed into a tree; the string is
  implementation-dependent and differs between any two parses.
- **`xsl:break` still reads the rest of the file**, so that a malformed tail is
  an error as in tree mode.
- **Only `FileResolver` streams**; another `DocumentResolver` builds the tree.
  The streamed document is not cached, so a later `fn:doc` of it reads it again.
  `xpath.DocumentResolver` returns a built `*xdm.Tree`, so there is nothing to
  stream through it, and the one exported interface with a reader,
  `xdm.EntityResolver`, resolves external entities, which a document is not.
  The design, which needs new exported API and the owner's approval: an
  optional `xslt.StreamingDocumentResolver` with
  `OpenDocument(uri, base string) (io.ReadCloser, docURI string, err error)`,
  tried by `executeStream` where it now asks for `burstResolver`, the stream
  parsed with `xdm.ParseOptions` the resolver supplies (or the defaults) and
  `TransformOptions`' byte and node limits; `readDocResolver` and
  `stripSpaceResolver` already wrap any resolver, so XTDE1500 and stripping
  carry over.
- **The `FileResolver` limits apply to the whole file**: raise `MaxBytes` and
  `MaxNodes` to stream a document past 64 MB or 10M nodes.

### Streaming: what audit round A left open (XSLT 3.0, performance, security)

Audit round A (four lanes at 32e31c6f: streaming correctness, memory,
profiling, security) found 25 items; fix round R closed 17 of them (see the
changelog). These are what remain, with the audit's number.

Memory above the tree, or equal to it:

- **A2-7: two strategies measure above the tree's peak.** A `group-by` in
  `xsl:fork` over event copies: heap 1.22× the tree at 50 MB and 1.07× at
  200 MB, RSS 1.21× and 1.27× (1,990 against 1,568 MB), live heap 1.02× and
  0.98×; it allocates 2.5× the tree's bytes, so GC pacing puts the process
  above it. `snapshot(/*)`: live heap 1.32× at 50 MB and 1.26× at 200 MB
  (1,742 against 1,388 MB), heap and RSS equal; the hold cap counts records,
  not bytes. The `si-fork-815` figure above is live heap with attribute-only
  members. A fix caps by bytes and compacts event members.
- **A2-8: an `xsl:fork` in a rule for the document element grounds the whole
  document**, at the tree's cost; listed under *Whole-document memory* above.
- **A4-F4: a very deep streamed document holds about 2.7× the tree.** 1M nested
  elements (7 MB): 147 MB streamed against 54 MB; depth 200,000: 27 against
  13 MB. Reachable only with `SourceParseOptions.MaxDepth` raised (the default
  1,000 makes it negligible). The cause is unverified (read from the code: a
  parse record, an open-state entry and a run frame per open element).
- **A4-F7: a deep `copy-of` is quadratic in depth, in both modes.** The tree
  takes 0.54 s at depth 10,000 and 1.65 s at 20,000, the stream 0.26 and
  0.80 s. Only with `MaxDepth` raised; not a streaming regression, and not
  profiled.
- **R3: apply-templates over `snapshot(P)` or `copy-of(P)`, other than
  `copy-of(.)`, keeps every node's accumulator values by ordinal for the run**
  (`sa.kept`, `xslt/streamaccum_l6.go`, never pruned). It grows linearly,
  still below the tree: 62.5 MB against 79.2 MB at 40,000 records.

CPU and allocation:

- **A3-2 remainder: the streamed reader is still about 1.75× `xdm.Parse`.**
  edd49236 took a 111 MB XMark drain from 786 to 713 ms and 3.33M allocations
  to 2.9k, against a parse of 408 ms; the rest of the gap is not text-store
  churn. End to end, streaming is 1.3–2.1× the CPU of Parse + Transform
  (identity 2.1 s against 1.6–1.7 s, a fork with `group-by` 0.87 against
  0.42 s).
- **A3-3: a built-in rule builds three objects per node, in both modes**
  (`withCurrent` per child whenever the mode has any rule, even for a no-op
  built-in): 15.2M of the tree's 15.77M allocations on two accumulators over
  XMark. Pre-existing, unchanged since v2.1.1.
- **A3-4: a streamed identity allocates as much as building the tree**
  (568 MB and 5.0M allocations against 591 MB and 1.68M, measured before
  edd49236 removed the text-store share): the built-in shallow-copy builds an
  element per output element and copies each text, then serializes and drops
  them. Sending the events straight to the serializer under `Output` would
  remove it.
- **A3-5: a fork's `group-by` with a count-only body copies every member**
  (342 MB of event copies; peak 217 against the tree's 281 MB, 0.87 against
  0.42 s), and `xml-to-json()` holds its whole string (106–133 MB peak for a
  34 MB result). Folding aggregates per key, and writing a whole-content
  `xml-to-json` straight to `Output`, would fix them.
- **An `xsl:iterate` carrying an unchanged 20,000-item `xs:string*` takes about
  16 s, in both modes**: the `as` conversion appears to run every iteration.

Errors and host detail:

- **R5: parse and byte-limit errors from `FileResolver` still name in-root
  absolute paths** (`parsing <path>: ...`, `<path> exceeds the N byte limit`,
  `xslt/resolver.go` `parseUncached`): they reveal where the roots are, not
  whether outside files exist. xdm parse errors may carry the base URI too, so
  dropping the prefix alone would not close it. `dtd/resolve.go:176` has the
  refusal pattern 2f825daf removed from `xslt` (`%q is outside the permitted
  directory %q`, with the resolved path and the root).
- **A4-F3: a host cannot learn the resolved path of a refusal.** Since
  2f825daf the error names the href as written and satisfies
  `errors.Is(err, fs.ErrPermission)`; the resolved path and the roots would
  need a new exported error type, which was not added.
- **R5: in tree mode the template-depth counter is not carried across an XPath
  function call.** `runtimeFrom` grafts the call's context onto the runtime
  bound at transform start, so templates applied inside a deep function nest
  start from depth about 1. The XPath call depth still bounds it.
- **R3: possibly a tree-mode bug, unverified against Saxon.** After
  `<xsl:variable name="v"><xsl:copy-of select="." copy-accumulators="yes"/></xsl:variable>`,
  `$v/rec/accumulator-before('n')` gives 1 for every `rec`, in both modes and
  before R3; §18.2.10 suggests the original's values (1, 2, 3). The row
  `copy-of-instr-variable` in `xslt/streamcopyaccum_test.go` pins only that the
  stream equals the tree.

Untested:

- **R2: `TransformReader`'s stand-in document node takes its tree id before
  the cursor opens**, so the principal input's copies order after its
  document node and before anything the transform loads. No test exercises
  that ordering through `TransformReader`; 0824c9c9's rows in `l7Rows`
  (`xslt/streaml7_test.go`) cover containers.

### `fn:replace` charges a backtracking or backreference result after building it (XPath, resource limits)

The RE2 path of `fn:replace` charges `len(input) + matches × len(replacement)`
before it builds the result, so a hostile replacement is refused without being
allocated. The two other engines, the backreference matcher and the opt-in
backtracking one (`-backtracking-regex`), still build the result and charge it
afterwards. Their step budgets bound the number of matches, but not the size
of each replacement, so a long replacement string can allocate past
`MaxBytes` before the refusal. Found by the second memory audit
(2026-10-31), which measured only the RE2 path.

### §19.8 streamability analysis is partially implemented (XSLT 3.0)

**No XSLT 3.0 failure wants `XTSE3430` any more**: every W3C case that wants
the refusal gets it, 151 of 151 (148 before M8). The block stood at 14, then
11 when the §19.8.5 streaming-parameter table was corrected (`varPosture` read
*grounded* where §19.8.5.2 and §19.8.5.3 say *striding*), then 7 when §19.8.5's
signature rule was implemented, then 3 after 2026-10-02 (`?` lookups,
§19.8.8.18; a multi-node streaming parameter, §19.8.5.2; a striding
shallow-descent body, §19.8.5.5; a climbing ascent call, §19.8.5.7). M8 closed
it on 2026-11-03: `||` is modelled (§19.8.8's `A || A`: `si-fork-902`, `-952`),
and a streamable function's body is assessed as a sequence constructor
(§19.8.5, with §19.8.4.12's higher-order note applied to singularity only:
`su-absorbing-205`). The posture-and-sweep lattice exists, and so do the rules
built on it: the §19.8.4 instruction rules (`xsl:where-populated`,
`xsl:on-empty`, `xsl:on-non-empty` among them since M8), §19.8.5 streamable
stylesheet functions, §18.2.8 accumulators, the §19.8.8 expression rules,
every `fn:`, `map:`, `array:` and `math:` row of §19.8.9's proforma, the
pattern `.` (§19.8.10), §19.6's context posture for both
`xsl:source-document`/`xsl:stream` and the template rules of a streamable mode,
and §18.1's grounded demand at both the sites it names.

**A partial analysis is safe here, and the safety is structural.** The analysis
reports whether it *modelled* every construct it met separately from what it
concluded, and an error is raised only on a fully-modelled verdict; anything
else is "no opinion". A missing rule leaves a case failing, while a wrong rule
would reject a valid stylesheet, and it is the second that the whole-corpus
scan measures at zero. Since v2.2.0 a verdict also carries an *unverified* bit:
it is set when the verdict rests on a streamable function, attribute set,
accumulator or mode whose own check gave up, or on a declaration in another
module. Refusals ignore the bit, so no XTSE3430 result moves; streamed execution
requires it clear. In the W3C streaming stylesheets the static checks accept,
56 known, grounded containers are unverified
(75 before M8): 36 apply templates into a mode that is not verified, 20 call
functions that are not. 100 containers are unknown (345 before M8): 90
schema-aware shapes (no schema is imported, so the XPath does not parse and the
cases are skipped), 6 calls on `fn:root`, 1 on `fn:fold-right`, 3 others.
Trusted containers went 2,079 → 2,355 and streamable modes not verified 90 →
46. The figures before M8 read only files containing `streamable="yes"`, those
after every `streamable="` spelling (+16 containers, +11 modes), so the
two are not on exactly the same file set.

`simpleMap` in `xslt/streamfunctions.go` took a simple map's sweep from its
right operand only, against its own comment; since 973790f6 it is the wider of
the two (§19.8.8.7), and no XTSE3430 result moved.

**Four verdicts follow the catalog rather than the Recommendation's text**,
each because catalog cases expect output where the text refuses (Saxon-EE
passes them all):
- a consuming, not free-ranging `||` in a crawling focus or over a crawling
  operand stays unknown: `sx-union-C`, 18 cases (`.+1 || ' '` in a for-each
  over `PRICE union QUANTITY`); the unwithheld rule measures 11,479;
- `xsl:copy`'s content is not counted as a consuming higher-order operand in
  the general rules (only for singularity): `si-copy-A`, 61 cases;
- §19.8.4.37, a source-document whose body calls `current-group()` or
  `current-grouping-key()` of an enclosing `xsl:for-each-group`: allowed when
  the group's select is grounded, as for any focus change over a grounded
  group (`si-group-051`, which the catalog expects to stream and Saxon 9.8
  passes; `si-group-062` streams), still `XTSE3430` over a striding select
  (`si-group-052`);
- the §19.8.8.4 union widening under grouping: `si-group-055`, below.

Not implemented: `fn:root` (§19.8.9.18 rewrites `root(X)` to `X` only when X
is statically a document node, a type this analysis does not infer; rewriting
otherwise would refuse `root()//x` at a document node), `fn:fold-right`
(§19.8.9.9), both modelled since L7 (ef9e55bf) only for a grounded
operand (`root(X)` with X grounded, `fold-right(S, Z, F)` with S grounded
and consuming), where every modelled answer is grounded and no `XTSE3430` can
follow; `fn:function-lookup` (§19.8.9.12), and §19.8.8.9's empty-axis
rule (an axis that can never select is grounded and motionless), which needs
a U-type of the context item. That rule is most of the analysis's
disagreements with the optional posture-and-sweep assessments
(`tests/sandp`, 919 cases): 437 agree, 482 disagree.

An earlier revision of this entry argued that nine `su-*` cases were
unreachable under the published text. Eight of them now pass, by the
corrections named above; the argument is kept in the history, not here.

The union-widening withholding is worth naming, because it looks like a gap and is not.
§19.8.8.4 widens a union of two striding operands to crawling by its own
admission rather than by necessity, so a rule applying templates to
`current-group() except .` is withheld rather than refused — `si-group-055`
asserts output for exactly that. The withholding is lifted where §19.8.4.19
refuses the same grouping for a reason that never consults the call — a
free-ranging `group-starting-with` pattern, or a grouping key that is not
motionless — since neither answer rests on the widening.

`system-property('xsl:supports-streaming')` reports `yes` since the block
closed (§26.5), so `system-property-012` passes. `streaming-fallback` is not
claimed: its 7 cases stay skipped. Constructs the streamed core does not
take are evaluated on the tree, correct but in memory proportional to the
document; in the W3C suite that leaves only the 7 cases proved not streamable
above.

The 17 failures are singletons or near-singletons and are catalogued in
[conformance-gaps.md](conformance-gaps.md) rather than here. Only two pairs
share anything: `merge-097`/`-097s` both fail on `FODC0002`, and the
CHANGELOG records them as not interoperable on the test set's own maintainer
comment — they rely on Saxon's `?select=` collection URIs and declare no
environment for the harness to honour. `-097sf` was read here as a third
member and is not one: it declares `<feature value="streaming-fallback"/>`,
which this engine does not claim, so it is skipped and never reaches
`FODC0002` at all. `si-copy-117`/`si-copy-of-117` both get
`XTTE1540` where `XTTE1510` is wanted. The rest — `docbook-001` (`XTMM9000`, chunking),
`validation-0201` (whitespace placement),
`strip-space-009` and a scatter of one-off error-code
disagreements — share no cause with each other at all. That is the useful fact
about them: after the streamability pass there is no second cluster waiting
behind it.

### §19.8: holes in the expression analysis (XSLT 3.0)

Verified in `xslt/stream*.go`. All but the `functionParamTypes` item return
unknown, so a required `XTSE3430` is missed; that one is a spurious refusal.
None is a wrong verdict.

- The analysis does not see text value templates (its instruction rules read
  child elements only), so a consuming part of a text value template cannot
  raise `XTSE3430`. The streamed plan judges the parts itself and refuses a
  second consumer, so such a container runs on the tree.

- `*xpath.LookupExpr` (`?`) is modelled in `expr()` (§19.8.8.18), but the
  inline-function walker `bodyMentionsStreamingParam` still lacks it, along
  with `*xpath.StringConcat` and `*xpath.ArgumentPlaceholder`. Since v2.2.0 the
  walker runs on every inline function in a streamable construct (it looks for
  `current()`), so such a body is unknown anywhere, not only in a stylesheet
  function. `||` itself is modelled in `expr()` since 1f6576df; see
  [*The `||` operator*](conformance-gaps.md#the--operator--2-cases-gained-18-lost).
- `functionParamTypes` splits a declared function type by hand: the comma in
  `function(Q{urn:a,b}t, xs:string)` makes three parameters, and
  `dynamicCallUsages` does not recognise `function (xs:string)` with a space.
  Either way a streamed argument gets navigation instead of absorption, and
  `$f(.)` in a streamable template is refused with `XTSE3430`; spelled
  `function(xs:string)` it compiles.
- A leading `/` is modelled only from a striding or grounded context, so
  inside the predicate of a crawling step it is unknown. That covers
  `key(k, v)`, which §19.8.9 defines as `key(k, v, /)`:
  `descendant::x[count(key('k', 'v'))]` gets no verdict, where outside a
  predicate the same call is refused.

### XQuery schema awareness: a tail of features `import schema` made reachable (XQuery 3.1)

**1 failure of 30,346 when written, and not a regression** (the cases measured
since, `fn:transform`'s among them, all pass; see the `fn:transform` entry). This entry exists
because the number is easy to misread. `import schema` was implemented, and
implementing it brought **416 previously-skipped cases into scope**, of which
339 now pass. The in-scope count went 29,930 → 30,346 and the passing count
29,918 → 30,343. A lift that admits failing cases raises the failure count by
construction, and quoting the failure count without the denominator beside it
would describe a gain as a loss.

The tail has fallen 203 → 113 → 101 → 93 → 42 → 26 → 10 → 3 → **1** as the features
behind it landed. What remains is a tail of separate features that
`import schema` made *reachable* without making them present. `docs/todo.md`
§1.5 names them and is the forward-looking half of this entry; what belongs
here is the measured shape, because it is what says the tail is several
features rather than one broken import.

What remains is one singleton, `prod-ContextItemDecl`; `op-same-key` and
`app-Demos` have since closed. `prod-CastExpr.schema` is at 88 / 88.

The last of the cast group was a single shape, and a narrow one: a cast to a
list type built each token from the item type's erased *code*. `xs:IDREF`
erases to `xs:string`, a union to nothing, so the sequence F&O 3.0 18.3.6 owes
— "each of which is an instance of the item type" — came back as bare strings
or, for a union over lists, as the one string handed in, because the union's
list member was looked up by item code and `xs:IDREFS` is a built-in the
schema's type table does not hold. The union-member fix had already shown the
answer — carry the NAME the code loses — and this is that answer one level
further in: the item type, and each list member of a union, is resolved as a
full cast target of its own, and every token is cast through it.

**The error-code mismatches are the interesting minority.** A group raises
`XPST0008` where `XQDY0027` is wanted. The `XPST0017`-for-`FORG0001` group that
stood beside it is closed: the constructor of an imported schema type is
registered in no library, so every DYNAMIC route to it -- `function-lookup`, and
a partial application `t(?)` -- reported the name unknown where the cast it
stands for owed `FORG0001`. Those are not missing features — they are the static-versus-dynamic
boundary being drawn one step too early, and they are the part of this tail
that is a defect rather than an absence. They are worth separating out
precisely because the rest are not defects and it would be easy to let these be
counted with them. Two groups that stood here were that same boundary drawn on
the wrong side, and each needed an existing check to look one field further
rather than a new feature — which is the evidence for reading the rest of this
group the same way rather than as absent machinery.

### XSLT 2.0: eight failures, and no cause peculiar to the lane

**8 of 6,201.** They are recorded here only because the lane has no entry
anywhere else and eight is small enough to name: `docbook-001`,
`format-number-070`, `import-schema-137`, `regex-syntax-xslt20-0984`, `-0985`,
`-0987`, `sequence-0132` and `validation-0201`.

Nothing here is a 2.0-specific gap in the engine. Three of the eight —
`docbook-001`, `import-schema-137` and `validation-0201` — fail identically in
the 3.0 lane and are covered above or in
[conformance-gaps.md](conformance-gaps.md).

`format-number-070` and `sequence-0132` fail only here, and both are recorded
in [conformance-gaps.md](conformance-gaps.md) as suite defects:
`format-number-070` invokes a template the stylesheet does not declare —
verified, zero `xsl:import`/`xsl:include` and zero `name="main"`. Neither is a
rule this engine has failed to implement.

The three `regex-syntax-xslt20` cases are the last, and they are the same
*kind* of case as the 22 `MS-Regex` schema cases under *Unicode category
drift* below; the CHANGELOG puts them under one heading. Each asserts a
single-character class against a single codepoint: `[\w]` against U+2308,
`[\d]` against U+1369, `[\c]` against U+0346. All three classes are defined by
Unicode general category, and all three codepoints sit where the assignment has
moved or is read differently than the suite assumed. Passing them means
freezing an old character database, which is the same refusal made below for
the same reason.

Worth stating, because the shape invites the opposite conclusion: three
failures in one test set normally means a cluster worth chasing. Here it is
three independent codepoints reached through three different classes, and what
they agree on is the *rule*, not a bug they share.

### Schema-validity rules not yet implemented (XSD)

**1 schema false accept in 1.0, 4 in 1.1** — invalid schemas this loads without
complaint. At this count the remaining cases are named individually; there is
no cluster left to tabulate.

| Case | Versions | Constraint |
|---|---|---|
| `MS-DataTypes/anyURI_b006_1356` | 1.0 only | RFC 2396 excluded characters in an `anyURI` enumeration |
| `Simple/simple093` | 1.1 only | `xs:NOTATION` as a union member type |
| `MS-Element/elemZ026` | 1.1 only | a 1.0 substitution-group rewrite XSD 1.1 deleted |
| `MS-Particles/particlesZ026a` | 1.1 only | same, plus a validity the W3C never settled |
| `MS-Particles/particlesZ033_g` | 1.1 only | a 1.0-era `invalid` verdict inherited by the 1.1 run |

**None of the five is a gap to be closed.** Every one is argued under
*Deliberate divergences* below: each is a case where the suite contradicts
itself or where the only rule that would reject the schema is one XSD 1.1
deliberately removed, and enforcing it costs more valid schemas than it buys.
They are listed here rather than only there because the count is what a reader
looking for open XSD work will find first, and the honest answer is that the
schema-validity column is empty of tractable work.

That is a statement about *these five*, not about the area. Adding rules here
would be the highest-yield remaining work if any were left un-argued, and it is
also the riskiest: a rule stricter than the spec starts rejecting real schemas
the suite never covers. Every change must be measured against both suite
directions *and* the production corpora (65 UBL + 427 CII), which are the
strongest guard against over-strictness — and, where those are absent, against
the 230 real-world schemas vendored in `testdata/`.

**A note on that guard when the corpora are absent.** UBL and CII are licensed
and unvendored, so a checkout without `GOXSLT_UBL`/`GOXSLT_CII` cannot run
them, and DocBook and XSpec exercise the XSLT engine rather than the schema
loader. Three things stand in when they are missing, and it is worth stating
all three, because it is easy to conclude there is no guard at all.

The first is a standing check rather than a fallback. 230 real-world `.xsd`
files ship in `testdata/` as fixtures for the XSLT and XQuery suites, and
`tests/check.sh` loads each on its own in every run, fast mode included,
ratcheted as `VendoredSchemas` — 185 load, 38 are DocBook 5.0's genuinely
invalid schema (below), 7 are excluded as fragments or deliberately invalid
test data, each named in `vendoredExclude`. A rule that starts rejecting one of
the 185 fails the build and says so. What this does **not** do is replace UBL
and CII: these are mostly test fixtures, documentation schemas and namespace
vocabularies, so they are thinner exactly where the corpora are thick — the
deep industry vocabularies with long derivation chains, large substitution
groups and heavy `xs:union`/`xs:key` use that UBL's 65 and CII's 427 exercise.
It catches the over-strict rule that breaks *any* real schema; it does not
catch the one that breaks only commercial ones.

The second is the suite population: roughly 16,000 schemas are labelled
*valid*, and a rule that over-rejects turns one of those into a false reject,
which the harness counts directly. "False rejects did not rise" over that
population is a real over-strictness signal — weaker than the corpora on the
shapes production schemas favour and no substitute for them, but far from
nothing. Pairing each new rule with a valid schema that must still load, in
`xsd/falseaccept_test.go`, is the third.

### XSD instance validation: two remaining, none addressable

Two instance cases remain, and "open" overstates both. Each is argued, and
neither is tractable work. `MS-IdentityConstraint/idZ015` was the third. Bug
4063 was settled "invalid" (`stable`, 2008-06-24), and it passes on both
versions now. See [xsd.md](xsd.md#choosing-the-version).

- `MS-Attribute/attP031` — a false *reject*, declined on purpose. The reasoning
  is under *A prohibited attribute use creates no attribute use* below.
- `saxonData/Id/id017.n01.xml` (1.1 only) — a defaulted `xs:ENTITY` in a
  document carrying no DTD. Declined on purpose: the unparsed-entity check bails
  when the instance declares no unparsed entity at all, which is what keeps
  `as-34` and the XSLT suite's `as-3401`, `match-208` and `match-209` passing.
  Read in full under *All 59 are adjudicated case by case* in
  [conformance-gaps.md](conformance-gaps.md).

### XSD particle restriction: the two-job wrapper in `recurseAsIfGroup`

**`particlesZ001` itself is a fixture defect, not a gap** — this section once
called it "the one remaining addressable XSD 1.0 schema false reject", and that
was wrong. Its `schemaTest` expects `valid` with no `version` attribute, while
its own `instanceTest` splits `invalid` under 1.0 from `valid` under 1.1, and
the group's annotation reads "Invalid restriction which becomes valid in XSD
1.1" under the `xsd1_1-RestrictionComplexTypes-IntensionalRestr` category. The
version split never reached the `schemaTest`, so under 1.0 the suite demands
`valid` for a schema it elsewhere describes as invalid. It is adjudicated as
bucket (a) in [conformance-gaps.md](conformance-gaps.md), and
`tests/conformance/results.json` records it as `fixture`.

**The wrapper limitation below is real all the same**, and is kept because it
is a live constraint on `recurseAsIfGroup` that other cases run into — the
measured cost recorded here is what stops the obvious fix being retried. The
shape is a `<sequence>` whose `<element name="element" minOccurs="0"
maxOccurs="unbounded"/>` restricts a base `<choice minOccurs="0"
maxOccurs="unbounded">` containing that element.

The cause is visible: `recurseAsIfGroup` wraps the element in a group of one
and hardcodes the wrapper at `1..1`, discarding the element's own occurrence
range. A once-only group is then compared against a repeating one, so the
repetition the base allows looks like something the restriction dropped.

**The obvious fix was measured and reverted, and the ratio is why.** Moving the
range onto the wrapper makes `particlesZ001`, `Z023` and `Z024` load, but
schema agreement falls 14,204 → 14,194 on 1.0 and 15,045 → 15,038 on 1.1 —
about **eleven invalid schemas newly accepted for each valid one recovered**.
(Those totals are the baseline of the run that measured them, not current
figures; what matters is the ratio, which is why they are left as recorded.)

The reason is that the wrapper's range is doing two jobs. For the mapping in
clause 2 it should repeat; for the *effective total range* check it should not,
because a group of one repeating N times contributes N elements where the
original particle contributed its own range. Carrying the range fixes the first
and breaks the second. **A correct fix needs the two separated rather than one
range serving both — which is a change to `effectiveTotalRange`'s contract, not
a change to this wrapper.** That is what makes this a known limitation with a
known shape rather than a patch nobody has tried; it is not counted as a
conformance gap, because the case that exposes it is a fixture defect.

Three further 1.1 cases — `particlesHb008`, `particlesHb011` and
`particlesZ028` — need XSD 1.1's §3.4.6.4 intensional restriction: genuine
language inclusion in *both* directions rather than the structural table.
`particlesHb008` restricts `choice{e1, sequence{e2,e3,e4}}` by a reordered
`choice{e1, sequence{e2, choice{e3,e4}}}` that no table can relate. That is an
automaton subsumption engine, not a rule, and two rounds declined it
deliberately rather than ship a partial one.

### `ste110`

The remaining schema false reject on both versions, `queried` against W3C bug
4957 (circular unions). `tests/conformance/results.json` records it as a
deliberate divergence, and [conformance-gaps.md](conformance-gaps.md)
spot-checked the `queried` defence: the engine disagrees in the direction the
bug points. It stays listed here because it is a false reject, the direction
that matters, and the schema has not been read beyond that check.

### `fn:transform` options F&O defines but this processor does not act on (XSLT 3.0)

`fn:transform` reads `stylesheet-*`, `package-name`/`package-version`,
`source-node`, `source-location`, `initial-*`, `function-params`,
`stylesheet-params`, `static-params`, `template-params`, `tunnel-params`,
`base-output-uri`, `delivery-format`, `serialization-params` (principal result only, as F&O
states), `requested-properties`, `xslt-version`, `global-context-item` and
`post-process`, `cache` (a bounded per-stylesheet compile cache),
`enable-assertions` (default off) and `enable-messages` (`false` skips
non-terminating messages). A requested property is
met only when it equals what `fn:system-property` reports (`xsl:version` is
ignored); an unmet one is `FOXT0001`. Every typed option is converted per F&O
§1.5.4, so a string where an `xs:QName` is declared is `XPTY0004`. These
F&O-defined options are accepted and have no effect:

- `vendor-options`; `enable-trace`, since `fn:trace` writes no diagnostics here.
- `package-location`, `package-node`, `package-text`: alone they identify no
  stylesheet (`FOXT0002`); beside a `stylesheet-*` option they are ignored.

Refusing them would be the post-process lesson applied, but it is a separate
decision. The QT3 driver now claims `fn-transform-XSLT` and
`fn-transform-XSLT30` (the `xslt` package registers a processor for XQuery
and `xpath.Eval` callers), so `fn/transform.xml` is measured: 119 cases in
scope at XPath 3.1 and 121 at XQuery, all passing. The four parameter options
(`stylesheet-params`, `static-params`, `template-params`, `tunnel-params`) are
typed `map(xs:QName, item()*)`, so a string key is `FOXT0002`, as
`fn-transform-err-18` and Saxon have it.

`xslt-version` selects the processor: up to 2.0 (1.0 included, 2.0 being the
nearest later version) the XSLT 2.0 one, `CompileOptions.MaxVersion` 2.0,
which ignores every option outside F&O's 2.0 list and reports
`xsl:version` 2.0; up to 3.0 the 3.0 one; later, `FOXT0001`. A non-decimal
value is `XPTY0004`. An absent `xslt-version` runs the 3.0 processor. F&O
takes the request from the stylesheet's own `version` and uses "a processor
that implements the requested XSLT version"; XSLT 3.0 §3.9.2 makes a 3.0
processor exactly that for 2.0 ("no differences are defined for XSLT 2.0
behavior"), and Saxon, which has only a 3.0 processor, reads it the same way.
Choosing 2.0 instead would silently drop 3.0 options such as
`global-context-item` for every `version="2.0"` stylesheet; a caller who wants
2.0 rules asks with `xslt-version: 2.0`. No QT3 case exercises the default. One
limit remains: `global-context-item` must be a node -- `Transform` takes the
global item as its source -- so an atomic or function item is `FOXT0001`.

Option names F&O does **not** define are
ignored, as F&O 3.1's option parameter conventions require: "It is not an
error if the options map contains options with names other than those
described in this specification." `source-location` (Saxon since 9.8, F&O
4.0) resolves through the `fn:doc` resolver and is never streamed.

### The function conversion rules are applied per function, not centrally

XPath 3.1 §3.1.5.2 says an argument of type `xs:untypedAtomic` supplied to a
parameter whose declared type is an atomic type is *cast* to that type. An
unvalidated document's attributes and element content atomize to
`xs:untypedAtomic`, so this is the rule that makes `format-dateTime(@date, $p)`
work at all.

`xpath/funcspec_table.go` already records every declared parameter type, and
`checkArgCardinality` already consults it at call binding — but only for
*cardinality*. Nothing applies the conversion from the declared type, so each
function that needs it implements the rule in its own body. There are around
forty-five such sites, and the next function added with a castable declared
type can get its copy wrong the same way the known ones did.

A sweep of all 80 (function, parameter) pairs whose declared type is castable
found 17 defective rows, in three classes, all since fixed; the sweep is kept
as `xpath/untyped_conversion_sweep_test.go`, which pins every one of the 80.

  - **No cast at all** (12 rows): `format-dateTime`, `format-date`,
    `format-time` at both arities and the six duration component accessors
    refused an untyped argument outright. This is the `XPTY0004` bug reported
    against v1.3.0.
  - **The cast reached, with the wrong code** (4 rows): `fn:error/1,2,3`
    parameter 1 and `fn:function-lookup/2` parameter 1 declare `xs:QName`,
    which is namespace-sensitive. §3.1.5.2 gives that case `XPTY0117` — from
    XPath 3.0; 2.0 has no such code — and all four reported `XPTY0004`.
    `argQName` could not have decided this: it had no `Context`, so it could
    not see the version, which is why it got the code wrong rather than merely
    forgetting the rule.
  - **A cast to the wrong target** (1 row): `fn:function-lookup/2` parameter 2
    declares `xs:integer`, but `$arity` was read through `argNumber`, which
    casts an untyped value to `xs:double` and then refused it for not being an
    integer. The conversion happened; the target type was wrong.

Applying the conversion centrally from the declared types was considered and
**rejected on evidence**, not deferred for effort. The declared type is not a
sufficient basis for the cast, because several functions deliberately convert
to something else:

  - `min/1`, `max/1`, `avg/1` and `sum/1` are declared `xs:anyAtomicType*`, for
    which a cast is a no-op. `fn_seq.go` casts an untyped item to `xs:double`
    instead, which is what makes `min(xs:untypedAtomic("3"))` answer 3 and
    `min(xs:untypedAtomic("three"))` raise `FORG0001` (QT3 `K-SeqMINFunc-35`).
  - `format-number/2` is declared `xs:numeric?`, but `formatnumber.go` turns a
    *failed* cast into `NaN` rather than an error, because that is what
    §4.7.4 requires of it.

A central cast obeying the manifest would change both. `funcspec.go` records
the same conclusion for the same reason — that the conversion rules are
per-signature, so an item-type check at the binding layer is the "eager check"
its design note rules out. Closing this properly means giving the manifest a
way to say *how* each parameter converts, not just what it is declared as; that
is a new field and a migration across 188 of the 255 manifest entries, and a
half-done migration leaving two conversion paths that can disagree would be
worse than the per-function duplication it replaces.

Nothing in the suites scores this, which is why the gap survived to be reported
against a release and why all three classes above were found by sweeping the
manifest rather than by a red lane. QT3 has no untyped-argument case for any of
the twelve functions that never cast. It does carry `XPTY0117` cases —
`FunctionCall-016` through `-019` in `prod/FunctionCall.xml` — but every one of
them depends on the `schemaValidation` feature and is therefore skipped, so the
four wrong-code rows were unscored too; fixing them moved no lane.

### `xsl:function` determinism: what the result cache does not yet key

A `new-each-time="no"` function is deterministic (§10.3.7), so calls with
F&O 1.7.4-*identical* arguments return identical results: the result is cached
per argument list, atomics keyed by exact type and `eq`, nodes by identity,
maps and arrays by content (issue #13). Two residuals, no suite case scoring
either: a function item is keyed by the item, so two distinct but identical
items (`concat#2` evaluated twice) recompute; and `cache="yes"` memoises
without regard to §10.3.7's default, which is *proactive*, so a node-building
function carrying only that hint returns the same nodes where fresh ones are
due. `functionDeterminism` still reads the absent attribute as `"maybe"`.

### The `dtd` package cannot enforce XML §4.3.4

`xdm` checks an external entity's declared version against the including
document's: a 1.0 document may not include a 1.1 entity, an unrecognised
version is refused rather than assumed compatible, and a table whose version
was never determined enforces the stricter 1.0 rule. `dtd` does none of this,
and the reason is structural rather than an oversight.

`dtd.Load` takes a DOCTYPE *directive string*, not a document. It therefore
never sees an XML declaration and has nothing to compare an entity's version
against — its own `stripTextDecl` discards the text declaration for exactly
the reason `xdm`'s once did. Closing it means giving `dtd` a way to be told
the including document's version, which is a new API surface, not a gap
closure. **It should wait for a caller that needs it**: inventing the
parameter now would fix the shape of something no test exercises.

Nothing in the suites scores this. The `XmlVersions` cases reach the parser
through `xdm`, which is checked; a caller reaching `dtd.Load` directly is the
uncovered path.

### `xdm` parsing cannot be cancelled

`xdm` contains no reference to `context`. A parse therefore runs to completion
or to a resource limit; a caller holding a deadline cannot interrupt one.

The quadratic **allocation** this entry used to record is fixed.
`Tree.assign` needs the *count* of namespace-axis slots to reserve at each
element, so that `generate-id()` cannot hand the same number to a namespace
node and an unrelated attribute. It obtained that count by calling
`n.InScopeNamespaces()`, which rebuilds the whole map by walking to the root —
O(*d*) per element, so O(n²) over a chain of *n* elements. `Finalize` now
threads the in-scope bindings down the pre-order walk, shadowing each
element's own declarations and restoring them once its subtree is numbered, so
an element costs only the declarations it carries itself.

Measured on darwin/arm64 with `MaxDepth` raised and `MaxNodes`/`MaxBytes`
disabled, before and after:

| Input | Depth | Time before | Time after | Allocated before | Allocated after |
|---|---|---|---|---|---|
| 14 kB | 2,000 | 0.063 s | 0.027 s | 47 MB | 0.9 MB |
| 28 kB | 4,000 | 0.190 s | 0.116 s | 209 MB | 1.7 MB |
| 56 kB | 8,000 | 0.923 s | 0.513 s | 912 MB | 3.4 MB |
| 112 kB | 16,000 | 3.59 s | 2.17 s | 4.0 GB | 6.7 MB |
| 224 kB | 32,000 | 19.9 s | 9.2 s | 17.8 GB | 13.4 MB |

Allocation now doubles for each doubling of depth rather than quadrupling: it
is linear, and 1,327x smaller at depth 32,000. A *wide* document is the
control and is unchanged at 29 MB for 256 kB of 64,000 siblings, confirming
the cost that was removed belonged to depth rather than to element count or
byte length. `TestFinalizeNotQuadraticInDepth` (`xdm/finalize_complexity_test.go`)
holds the allocation bound; reverting the fix fails it at 17,780 MB against a
200 MB budget.

Time is linear too. The ancestor-chain walks that were left on the profile
are gone: one namespace stack replaced `resolvePrefix`, and since v2.0.2 that
stack is indexed past 32 bindings, so a prefix lookup is not a scan of every
declaration in scope. The 224 kB document above now parses in 2.4 ms at depth
32,000; with a declaration at every level, 861 ms before v2.0.2 and 19 ms
after.

**`ParseOptions.MaxDepth` still defaults to 1000**, and a 1,001-level
document is refused before any work on its depth happens. Note that `MaxDepth` clamps `<= 0` to the default
(`xdm/parse.go:271`), unlike `xslt`, `relaxng` and `xsd`, where a negative
value does remove the bound: there is no way to disable the parser's depth
limit, only to raise it.

An audit reported this as 1.25 s and 1.6 GB for 1 MB of nested input. The
shape of the finding was right, but those figures never reproduced: 1 MB of
`<e>` nesting is 150,000 levels deep and is **rejected outright** under the
default limit. The cost quoted corresponded to roughly 63 kB, not 1 MB.

Adding a `context` parameter is a separate question, since it is an API change
on `Parse`, and the depth bound already prevents the unbounded case.

### XML 1.1 external entities and DTD-side rules

XML 1.1 sat outside all of this until the character layer was implemented. The
gap was never that 1.1 documents were refused — they parsed — but that
`version="1.1"` was rewritten to `1.0` and the document then read under the
wrong language's rules. The version now reaches the tokeniser, and [2] `Char`,
[2a] `RestrictedChar` and the §2.11 line ends follow it. What is still missing
is in the external-entity and DTD layers, and is described in
[todo.md](todo.md#11-xml-11-documents--character-rules-done-dtd-side-rules-outstanding).

### The tokeniser keeps two leniencies

`internal/xmltok` still carries these `parity:` behaviours from the
`encoding/xml` fork it replaced; the others were corrected against the W3C XML
Conformance Test Suite (see [testing.md](testing.md#tokeniser-differential)) or
by the parse work recorded in [CHANGELOG.md](../CHANGELOG.md).

* A PI with target `xml` is taken as the XML declaration wherever it appears,
  and the declaration's pseudo-attributes are found by substring search;
  `xdm` validates the declaration's syntax and placement itself.
* A character error is reported against the line where the run ended.

### An undeclared entity is dropped where §4.1 makes it a validity error

Where §4.1's WFC "Entity Declared" does not apply (an external subset that was
not read, or a parameter-entity reference, without `standalone="yes"`), an
undeclared general entity is a validity error, and a non-validating parse goes
on. `xdm` then drops the reference from content, attribute values and defaults
without reporting it, as libxml2 does for attribute values. libxml2 keeps an
entity-reference node in content, which the XDM cannot represent. Nothing tells
the caller a reference was dropped; a caller that must know can supply a
resolver so that the external subset is read. For the same reason, entity and
ATTLIST declarations that follow an unread parameter-entity reference are
ignored (§5.1); libxml2 2.9.13 still processes them.

---

## Deliberate divergences

Not gaps. Each is a behaviour that disagrees with a test, or with a reading
someone will propose, on purpose — and each carries the spec clause it rests on
and the **measured** cost of changing it. The number is the point of the entry:
without it, the trade gets re-attempted.

### DOCTYPE is refused by default

A DOCTYPE is the entry point for XXE and entity-expansion attacks. Refusing it
unless the caller opts in is the correct default for a library that will be
pointed at untrusted input. `xdm.ParseOptions{AllowDOCTYPE: true}` enables it
where the documents are trusted — which is what loading UBL requires, because
the W3C XML Signature schema it depends on carries one.

**Cost: none, once the distinction is drawn in the right place.** A conformance
harness pointed at a vendored suite is not the untrusted caller the default
protects, so `tests/xsdsuite` sets `AllowDOCTYPE` on the schema-load path only,
leaving external entities off. The default itself is unchanged.

### `xsi:schemaLocation` is ignored by default

Honouring it lets the document choose the schema it is validated against, which
defeats the purpose of validating. `WithInstanceLocations` opts in, with a
policy that names which namespaces may be resolved.

### An initial mode without `use-accumulators` makes no accumulator applicable (`accumulator-073`)

**Cost: one case,** `accumulator-073`; DocBook, XSpec and the XSLT 2.0 lane
are unchanged.

§18.2.2: for the document of the initial match selection "the accumulators
that are applicable are those determined by the xsl:mode declaration of the
initial mode", and of `use-accumulators` "the default value is an empty list".
A declared `<xsl:mode/>` that omits the attribute therefore makes none
applicable, and `accumulator-before` over the source document is `XTDE3362`
-- what Saxon reports for issue #16's stylesheet. `accumulator-073` asserts
the opposite for a declared mode with no list; it is the only suite case that
does, and `copy-3002` (no `xsl:mode` at all) expects the error. The engine
used to read a declared-but-silent mode as "all" to pass it.

### `fn:collection()` raises an error rather than returning empty

**Cost: zero cases.** Nothing scores against this any more; the entry is kept
because the default is what a future caller will meet, and the argument for it
is not obvious.

Returning an empty sequence for an unconfigured collection would let a
stylesheet silently process no documents and report success, which is worse
than an error, so `FODC0002` stands as the default. With no resolver configured
it is still `FODC0002` even where a hook is available, which is the point.

### Unicode category drift (bug 4113)

**22** cases in `MS-Regex2006-07-15`, identical in both versions, all flagged
`queried bug4113` by the W3C: `reJ11`, `reJ13`, `reJ19`, `reJ21`, `reJ23`,
`reJ25`, `reJ29`, `reJ31`, `reJ33`, `reJ35`, `reJ61`, `reJ69`, `reJ75`, `reJ77`,
`reL98`, `reL99`, `reM98`, `reN99`, `reS21`, `reS42`, `reT63`, `reT84`. The
list is enumerated so the next re-measurement can diff it rather than re-count.

**Cost of matching them: shipping a frozen 2001 character database.** They
assert that `\p{Lu}` rejects characters that *are* uppercase letters in current
Unicode. The suite was written against Unicode 3.1; the codepoints in question
— U+1D7A8 among them — were categorised differently then.

These are **22 of the 29 disagreements on 1.0 and 22 of the 30 on 1.1** — two
thirds of everything the suite reports against this engine, and the single
largest reason XSD cannot reach 100% and should not try. The three
`regex-syntax-xslt20` failures in the XSLT 2.0 lane are the same rule seen from
the other side.

### Two schema false accepts the suite contradicts itself on

`MS-DataTypes/anyURI_b006_1356` (1.0) and `Simple/simple093` (1.1). Both are
invalid schemas this loads, and in both cases the rule that would reject them
also rejects a schema the same suite labels valid.

**`anyURI_b006`** puts backslashes in `xs:anyURI` enumeration values. The suite
annotates it "TSTF ruled that strictly speaking, per 1.0, the schema contains
one or more invalid anyURIs", and expects `invalid` under 1.0 but `valid` under
1.1 — 1.1 relaxed `anyURI` so that any sequence of characters is in the lexical
space, which is why this case appears in the 1.0 list only and not the 1.1 one.
That asymmetry is real and `isAnyURILexical` already implements it.

The rule that would catch it is "reject the RFC 2396 excluded characters", and
`anyURI_a014`, `a015` and `a016` are why it cannot be written: `a016` puts
`foo<bar`, `foo>bar` and `foo"bar` in `anyURI` enumerations and is expected
**valid**, unqualified by version. Backslash and `<>"` are the same production
in RFC 2396, so no uniform rule separates b006 from a016. The reading the code
takes — XML Linking §5.4 percent-escapes the excluded characters, so they are
in the lexical space rather than out of it — **gains three cases where the
alternative gains one.**

There is a second, larger cost. Facet-value validation and instance validation
share one path down to `isAnyURILexical`, with no seam between them, so
tightening 1.0 `anyURI` would reject **every instance value containing a
Windows path or an unescaped space**. UBL is a 1.0 schema set and its documents
carry exactly those. The one suite case is not worth that.

**`simple093`** names `xs:NOTATION` as a union member type. §3.2.19 does forbid
using NOTATION directly, and this package enforces that for a restriction base,
a list item type, and an element or attribute type. The union arm is left
unenforced because `MS-Particles/particlesZ007` contains
`<xsd:union memberTypes="xsd:NOTATION"/>` and is labelled **valid**, with a
dependent instance test that only runs if the schema loads. Both cases carry
status `accepted`.

**Measured twice: enforcing the rule trades simple093 for particlesZ007 in 1.1
and costs 1.0 two cases outright**, because simple093 is a `saxonData` case
that never runs under 1.0 at all. The comment in `facet_check.go` records the
measurement so it is not retried.

### `particlesZ033_g` is a 1.0 verdict scored against a 1.1 run

`MS-Particles/particlesZ033_g` — 1 schema false accept, 1.1 only.

Under 1.1 this schema has **no competing pair at all**. Enumerating every state
of every content model shows the two candidates a reader will reach for do not
arise: the two `e2` positions never share a state, because the inner
`<sequence minOccurs="56">` has to be re-entered or completed, so the outer
`e2` is only ever reachable from `follow` sets the inner one is absent from;
and `m1`-against-`head` is what `particlesZ033_e` and `_f` write, both
correctly rejected in *both* versions on that pair, while `_g` is the variant
in which the author replaced it — the inner choice became `m3` against
`ref='head'`, and `m3` is a fresh name that overlaps nothing. The remaining
`ref='m1'` sits behind `<element e3 minOccurs="2">` in a sequence, so it is not
in the model's `first` set and never meets `head`.

What is left is the single pair `ref='m1'` against `<xsd:any/>`, which is what
rejects the schema under 1.0. `XSD1_1TestCategories.xml` names that relaxation
outright — "Relaxation of UPA: wildcard/element competition no longer violates
UPA" — and `s3_10_1v04s` through `s3_10_1ii09s` are written to depend on it. So
under 1.1 the model is unambiguous by the letter of the rule this suite states,
and accepting it is right.

The verdict scored against it is a 1.0 verdict. The group carries no `version`
attribute, its `documentationReference` points at the 2004 XSD 1.0 REC, and its
one bare `<expected validity="invalid"/>` is therefore inherited by the 1.1 run
under `appliesAND`. A test in the wrong bucket, not a missing rule.

**The measurement, so it is not retried. Restoring element-against-wildcard
competition under 1.1 gains this one case and costs seventeen valid schemas**,
taking the 1.1 total from 41536 to 41494 at the time it was measured. The false
rejects it creates are `addB153`, `all006`, `wild030`, `wild047`, `wild049`,
`wild050`, `wild052`, `wild072`, `wild073`, `s3_3_6v01`, `s3_3_6v04`,
`s3_8_6v01`, `s3_8_6ii01`, `s3_4_6v01`, `s3_4_6v04`, `s3_10_1v04` and `ste110`
— that is, the whole family of groups whose subject *is* the relaxation. No
narrower rule separates them: `_g`'s pair is a counted element against a
following wildcard, and `wild047`'s is the same shape. **One case for seventeen
is the wrong direction.**

Accepting it is also not a soundness hazard. The runtime in `nfa.go` is a
subset construction over the counter state, exploring every position in
parallel, so an element-against-wildcard choice UPA no longer objects to is
still resolved correctly at validation time. `particlesZ033_g` produces no
instance disagreement, only the schema one.

One negative result is worth keeping: this is **not** a budget decline. Every
give-up path in `upa.go` and `assemble.go` — the state-width cap, the pair-test
cap, `compileContentModel`, and the substitution-closure cap — returns an error
wrapping `xdm.ErrResourceLimit` rather than accepting. The huge occurrence
values here never inflate the automaton, because occurrences are runtime counts
on a counter automaton and not states. The cannot-decide invariant holds.

### The range comparison XSD 1.1 deleted (elemZ026, particlesZ026a)

`MS-Element/elemZ026` and `MS-Particles/particlesZ026a` — 2 schema false
accepts, 1.1 only. Both are **suite artifacts, not gaps**.

**What the schemas do.** elemZ026's disagreeing site is not the inner type at
all. Its `restrictedBasicBitType` narrows `maxOccurs` from `unbounded` to `1`,
and both versions accept that — narrowing is what a restriction is *for*. The
divergence is one level out, at `restrictedBasicBitContainerType`: the base
names a substitution-group head with `maxOccurs="unbounded"`, the derived names
a concrete member of that group, also unbounded.

**Why 1.0 rejects it.** Clause 2.1 of Particle Valid (Restriction) rewrites an
element particle whose declaration heads a substitution group into a *choice*
over the members. `asSubstitutionChoice` implements that faithfully: the choice
keeps the original particle's range and each member gets unit occurrence. So
the base becomes `(mem{1,1}){1,unbounded}` and Elt:Elt compares the derived
`{1,unbounded}` against a member's `{1,1}`. Occurrence Range OK fails.

**Why 1.1 accepts it, correctly.** `(a{1,1}){1,unbounded}` and `a{1,unbounded}`
are the *same language*. The 1.0 rejection is a table artifact — a pairwise
bound comparison standing in for an inclusion the table cannot compute — and it
is exactly the class of artifact the 1.1 relaxation exists to remove.
`particleSubsumes` decides this pair by language inclusion and returns
"included", which is the right answer.

**The spec settles it.** XSD 1.1 Part 1 has no Particle Valid (Restriction) and
no Occurrence Range OK. §3.9.6 retains only Particle Correct, Particle Valid
(Extension) and Particle Emptiable; Appendix B.4's constraint index lists
`cos-particle-extend` with no restriction counterpart, and `range-ok` does not
appear anywhere in the document. §3.4.6.4 (`cos-content-act-restrict`) is two
clauses, and clause 1 is the whole content-model test: "Every sequence of
element information items which is ·locally valid· with respect to R is also
·locally valid· with respect to B." The substitution-group-as-choice rewrite is
likewise absent — under 1.1 a substitution group enters restriction checking
only through ·locally valid·, because an element particle's language already
contains its substitutable members.

**The named fix, measured.** Threading each step's particle through `stepNFA`
into `declCompatible` and applying `occurrenceRangeOK` was implemented in full.
It flips elemZ026 to `invalid` as predicted — **and it also re-rejects
`particlesHa161` and `particlesZ001`, both marked `accepted` by the suite and
both valid schemas the subsumption engine was built to accept. XSD 1.1 fell
41536 → 41534: a net loss.** The fix is not narrower than the rejected
alternative; it is the same 1.0 artifact reintroduced through a different door.

The reason it fires at all is narrow and accidental. When the head is abstract,
`asSubstitutionChoice` omits it and `stepNFA` sees a single declaration on the
base side, so `declCompatible`'s clauses run. When the head is concrete,
`stepNFA` finds two declarations for the same name, sets `multiple` and returns
none, so the range would never be compared. A rule that depends on whether the
base's head happens to be abstract is not Occurrence Range OK under any
reading.

**What the suite says.** elemZ026's `<expected validity="invalid"/>` carries
`status="queried"` against W3C bug 4146, opened by Michael Kay in 2007: "the
metadata describes the schema as invalid, but it contains no obvious error. XSV
reports it as valid." The bug is still `NEW`, keyworded `disputedTest`, and its
whiteboard records an intent to fork a separate 1.1 test that was never done.
`particlesZ026a` is weaker still — the TSTF concluded its validity was
"implementation-determined" and the WG never decided, which the test's own
annotation says in as many words.

`xsd/subsume_occurs_test.go` pins this in both directions: the two
substitution-group restrictions that must keep being accepted, and the genuine
widenings — `maxOccurs` 2→3, and bounded→unbounded — that language inclusion
still rejects without ever comparing a bound to a bound.

### A prohibited attribute use creates no attribute use (attP031)

`MS-Attribute/attP031` — the one remaining addressable XSD instance false
reject, declined on purpose.

It is a suite self-contradiction rather than a defect here: it declares
`use="prohibited"` with a `fixed` value and expects the instance supplying that
value to be *valid*, while `attF001` — structurally identical but **without**
`fixed` — expects invalid, and both carry status `accepted`. §3.4.2 gives
`{attribute uses}` only the declarations whose `use` is absent, `optional` or
`required`, so a prohibited use creates no attribute use at all. Making
`attP031` pass means treating `fixed` as the discriminator, which no clause
supports.

**Measured in a clean checkout so the figure is attributable: keeping a
prohibited use that carries `fixed` takes XSD 1.0 from 39,345 to 39,346 and
leaves 1.1 unchanged**, with no schema-level change and no false accept
introduced — `attF001` still rejects. (Those are the baseline totals of that
run; both have since risen, and what matters is the delta.) So it is a clean
**+1, and it is declined anyway**: the same change makes this validator accept
`att="37"` against a declaration that prohibits the attribute, which is a
deliberate false accept bought for one suite point. A case that passes without
a clause behind it is not a fix.

### `xs:gMonth` old lexical form (bug 6901)

`gMonth002_2061`, `gMonth004_2063`, flagged `queried bug6901` — false rejects
in both versions.

These use `--03--`, the withdrawn gMonth syntax from the original XSD 1.0
release. The errata replaced it with `--03`. **Cost of accepting them:
accepting a form no current spec defines.**

### Regular expression backreferences — two engines, one default

**The default engine is RE2 and stays RE2.** RE2 has no backreference, but it
does return capture positions, and a backreference is only *hard* when the
group it names can match more than one width. RE2 returns a single submatch
assignment — the greedy one — and cannot enumerate alternatives, so for
`(a*)\1` against `"aa"` it reports the group as `"aa"`, leaving nothing for the
backreference, and a comparison against that answers **false** where the
correct answer is true (the split is `"a"` + `"a"`). The information needed was
discarded before the comparison ran.

When every group a backreference names has a *fixed* width, the greedy
assignment is the only assignment. There is nothing to enumerate, so
capture-and-compare is not an approximation — it is exact, and it runs in RE2's
linear time with one comparison pass per candidate position. Measured on
`([a-z])\1*`: 4,000 characters in 53 µs, 64,000 in 567 µs. That path is
unconditional, and it is what the default uses.

A variable-width backreference is refused with `FORX0002` rather than guessed
at, because an engine that answers correctly or says it cannot is safe to have
on always, where one that guesses is not safe at any setting.

**The general case is available, and off by default.**
`xpath.SetBacktrackingRegex(true)` — or `-backtracking-regex` on the command
line — enables a backtracking matcher that decides the rest: variable-width
groups, backreferences mid-pattern, alternation, and lazy quantifiers.

It is off by default because it has no linear-time guarantee, and a pattern is
not always the caller's own: `matches($s, $node/@pattern)` takes one from
document data, so enabling it globally would let a document being validated
choose how long the validation takes. Catastrophic backtracking is a denial of
service with a one-line payload.

Even enabled, a step budget bounds every match, and exhausting it raises
`XPDY0130` (`FORX0002` before round SF) rather than returning a silent "no match" — a budget that guessed
would do it precisely on the inputs where the answer was hardest to get. The
budget is measured from both ends: the hardest honest pattern in either
conformance suite (`regex-032`, fifteen lazy groups and a `\14` against 180
characters) answers in 525 steps, five orders of magnitude below the ceiling,
while `(a*)*\1b` against sixty `a`s exhausts the full budget in about 200 ms.

Character-class semantics are not duplicated between the two engines. The
backtracking matcher parses the *already-translated* pattern and compiles each
single-character leaf with RE2, so subtraction, `\p{IsGreek}`, `\i`, `\c` and
the Unicode-wide reading of `\d` and `\w` are owned by `translatePattern` and
applied in exactly one place.

**Measured cost of the default: one QT3 case.** `fn-matches-51` names a group
whose width can vary *and* places the backreference mid-pattern; it passes with
`xpath.SetBacktrackingRegex(true)`. The conformance harnesses set the switch,
since a suite's patterns are trusted input, so the headline figures count the
case; a run in the default configuration does not. Closing the last case by
default would cost the linear-time guarantee, which is a worse trade than the
case is worth.

The XML Schema pattern facet is unaffected: Appendix F's `atom` production has
no form for a backreference, so `xsd` still rejects `\1` under both versions.

### DocBook 5.0's XSD refuses to load — DocBook's schema is invalid

`tests/corpora walk testdata/xslt30-test` reports 39 failures, 38 of them the
DocBook 5.0 XSD under
`tests/misc/docbook/docbook-xsl-1.79.1/slides/schema/xsd/`. Every one of the 38
fails identically — the walk loads each file on its own and each includes the
same `pool.xsd`, so it is one fault counted 38 times — with 568 errors: 281
`cos-element-consistent` and 287 `cos-nonambig`.

Refusing a schema this widely deployed is strong evidence of a bug here, so it
was investigated as one. It is not. All three error clusters are genuine
violations of §3.8.6, and the W3C suite states each outright.

The whole fault reduces to seventeen lines. `db.indexterm` (`index.xsd`) is a
`<xs:choice>` of three named groups, each declaring a **local** element
`indexterm` with a **different** anonymous type; `db.firstterm`/`db._firstterm`
(`glossary.xsd`) and the five `info` declarations (`pool.xsd`) are the same
shape:

```xml
<xs:group name="a"><xs:sequence><xs:element name="x" type="xs:string"/></xs:sequence></xs:group>
<xs:group name="b"><xs:sequence><xs:element name="x" type="xs:int"/></xs:sequence></xs:group>
<xs:choice><xs:group ref="a"/><xs:group ref="b"/></xs:choice>
```

That is `msData/modelGroups/mgR022.xsd` almost verbatim, and `mgR002` is the
inlined form. All 22 of `mgR001..mgR022` carry `<expected validity="invalid"/>`
with `status="accepted"`, under the documentation *"2 particles with idendical
element declarations (different type)"* (sic). The `mgQ` series is the control:
`mgQ003` — the same model with the second `e1` given the **same** type — is
expected **valid**, and we accept it. The pair differs only in the type, which
is exactly the distinction `checkElementDeclarationsConsistent` draws.

The suspicious-looking `cos-nonambig` message that names one QName against
itself — *"element firstterm and element firstterm can both match the same
element"* — is likewise correct. `mgS002..mgS005` and `mgQ001`/`mgQ021` produce
that same message shape here and are all expected invalid; `mgQ021` is two
particles for the same name with the **same** type, still invalid under UPA.
**So `CheckOptions.LaxUPA` is not merely off by default, it would be wrong as a
default: loading DocBook with `LaxUPA` set moves 568 errors to 567.** These are
distinct `*ElementDecl`s with distinct types, not one declaration seen twice —
the genuinely-same-declaration case (`<xs:element ref=>` twice in a sequence)
already loads clean.

The nine wildcard errors — *"element abstract and wildcard ##any can both match
the same element"* — come from `db._any`, a bare `<xs:any processContents="skip"/>`
sitting in a `<xs:choice>` beside named element refs (`pool.xsd`). That is a
UPA violation in 1.0 and not in 1.1, but these files carry no `vc:minVersion`
and no `version="1.1"`: they are 1.0 schemas, and 1.0 is the right rule for
them. Loading them as 1.1 would be answering a different question.

The cause is visible in DocBook's own tree. The **normative** DocBook schema is
RELAX NG, and `relaxng/index.rng` defines `db.indexterm` as a `<choice>` of
three `<element name="indexterm">` patterns distinguished only by the value of a
required `class` attribute (`singular`/`startofrange`/`endofrange`). RELAX NG
resolves that by inspecting the attribute. §3.8.6 requires the particle be
determined *"without examining the content or attributes of that item"*, which
forbids precisely this. The XSD files are a lossy machine translation of a
construct XSD cannot express — which is why DocBook ships RELAX NG as
normative.

**Cost of relaxing either check: a false accept against `mgR002` and `mgQ021`
directly.** A search over all 11,060 expected-valid schemaTests in the suite
found no case with two same-named local declarations of differing types in one
content model, so there is no counterexample to the current behaviour, and none
of the schema-level disagreements in `xsdtests` mentions `cos-nonambig` or
`cos-element-consistent` in either direction.

### Four suite cases that should be read as disputed

Each carries status `accepted` and each is questionable on the suite's own
evidence. Only `particlesZ001` (1.0) and `simple093` (1.1) still cost anything;
the rest are kept for the argument, because each is a guard on a fix someone
will propose for a neighbouring case.

* **`simple093` contradicts `particlesZ007`.** The first declares
  `<xs:union memberTypes="xs:QName xs:NOTATION"/>` invalid; the second contains
  `<xsd:union memberTypes="xsd:NOTATION"/>` and is valid. Enforcing the rule
  trades one for the other, and loses `particlesZ007` outright under 1.0 where
  `simple093` is not even run. The list form is enforced; the union form is
  deliberately not.
* **`particlesK006` under 1.1.** L(R) ⊆ L(B) holds, so it is a valid
  restriction under §3.4.6.4, yet it is marked invalid with no version
  qualifier. Its documentation is what states the distinction that constrains
  any change to all-group restriction:

  > B's minOccurs=0, B's maxOccurs=absent, but the element has min=max=1,
  > R's minOccurs=0, R's maxOccurs=1 — expected **invalid**

* **`particlesZ001` under 1.0** is expected valid with no version attribute
  while its own annotation calls the 1.0 rule "ambiguous" and tags it as
  intensional restriction, a 1.1 feature — and its `instanceTest` carries the
  1.0/1.1 split its `schemaTest` is missing. That unpropagated split is the
  defect; the wrapper limitation it happens to expose is written up under
  *XSD particle restriction: the two-job wrapper in `recurseAsIfGroup`* above.
* **`simple004`/`005`** are self-flagged as depending on the resolution of spec
  bug 2074, and `simple006`'s own note says "one could argue for valid".

### Constraints on the 1.1 restriction relaxations

The relaxations themselves are done. What must survive is the set of guards
each one turned out to need, because every guard was found by breaking a case
that the obvious version of the change had not considered. Each is a measured
cost of the naive alternative.

**They are version-gated, not general.** `particlesT002`/`T009` (a reordered
choice), `particlesHa161` (an optional element restricting an optional choice)
and `particlesZ023`/`Z024` (a one-member choice) are all marked invalid under
1.0 and valid under 1.1. 1.0's RecurseLax really is written as an
order-preserving walk, and `stripPointless` removing a one-member choice is
*correct* for the 1.0 table. **Removing the strip unconditionally fixed the two
1.1 cases and broke the same two under 1.0, for a net loss of three.**

**A range cannot serve both `recurseAsIfGroup` and `effectiveTotalRange`.**
Moving an optional element's range onto the wrapper works only where the base
does not repeat: in `effectiveTotalRange` a group of one repeating N times
contributes N elements, so the same range means two different things. **This is
what broke `particlesV020`**, and it is the same collision recorded under
*XSD particle restriction: the two-job wrapper in `recurseAsIfGroup`* above.

**The derived minimum must already satisfy the base's.** Without that
condition, moving a `minOccurs` of 0 onto the wrapper made it violate a base
requiring 1, and **`ctF007` became a false reject for exactly one case gained.**

**The base's compositor decides whether a wrapper may be kept.** Keeping every
one-member choice under 1.1 **turned `particlesR001` into a false reject**: a
one-member choice restricting a sequence-with-wildcard is valid, and only
reaches a cell of §3.9.6's table once the wrapper is gone. The wrapper is
preserved only when *both* sides are choices, where the pair decides the cell.

**Only a group occurring exactly once may be inlined into an all group.** An
all group of all groups admits exactly the interleaving of their members, so
the nesting carries no information the flat list does not — but a *repeating*
group multiplies its members' occurrence ranges, and folding that into the
parent would compare the wrong budgets. That is the ambiguity `allSubsumes`
exists to refuse rather than guess at.

**An optional all group is a disjunction, not a scaled budget.** Scaling each
per-name budget by the base group's range is the obvious reading and it is
wrong in both directions: it makes `mgO029` load and **breaks `particlesK006`**,
whose documentation is quoted above. The base is *(empty) | (every budget met)*
— two alternatives checked separately — and the derived side is a disjunction
too. Flattening either side to `0..1` per name cannot express the coupling
between two required members, which admits this false **accept**, invisible to
the suite:

```xml
<!-- B: {} | {a1,a2} — never {a1} alone -->
<xsd:all minOccurs="0"><xsd:element name="a1"/><xsd:element name="a2"/></xsd:all>
<!-- R: admits {a1} alone, which B forbids -->
<xsd:all minOccurs="0"><xsd:element name="a1"/><xsd:element name="a2" minOccurs="0"/></xsd:all>
```

A discriminator on *shape* rather than on language — zeroing the budget floors
only when the derived side is also a group — scores both suite cases correctly
for the wrong reason and admits exactly that schema. Both halves are guarded
independently in `xsd/allgroup_disjunction_test.go`.

**A wildcard's occurrences do not split between the names it spans by a simple
count.** `all244.n` is the negative test that holds this honest, and it is
still rejected, with `the base requires a wildcard, which the restriction
omits`. A relaxation that recovered `all206`/`all218`/`all237`/`wild049`/
`wild050` by loosening the wildcard-occurrence rule would take `all244.n` with
them; `xsd/allgroup_wildcard_test.go` pins all six shapes together. Their XSD
1.0 rejections are **correct** and are not gaps: 1.0's `cos-all-limited.1`
genuinely forbids a non-element particle in an all group, and `wild049`/
`wild050` also spell `notQName`, which is 1.1-only. The 1.0 lane is pinned in
the same file.

### A union's selected member is a third fact, beside the annotation

Recorded as an invariant rather than as a gap, because the mistake it describes
is available at every site that copies a node, and one of those sites is added
whenever a new copying instruction is.

`Date` has type `DateType`, a complex type with simple content extending
`GeneralDate`, which is a union of `StandardDate` and `xs:string`. **XSD 1.0
§3.14.4 selects a union's member per *value*,** so the annotation alone cannot
say whether "29 MAY 1917" is a `StandardDate` or a plain string — the validator
records the winning member separately, in `xdm.Node.UnionMember`, and
atomisation reads it to decide what the typed value is. A union's own
derivation chain runs to `xs:anySimpleType` and stops, so **without the member
there is nothing to build a typed value from and the node atomises to
`xs:untypedAtomic`.** Any copy that carries `TypeAnnotation` and drops
`UnionMember` beside it therefore untypes the node silently.

Two things make this class of defect hard to see. The failure is **selective**:
the same pattern answers *true* on any path that has not been through a copy,
so a stylesheet could sort by a key that saw the type and then dispatch on a
pattern that did not — and `xsl:strip-space`, a declaration about whitespace
with nothing to say about types, was what untyped the document.

It was diagnosed by measurement rather than by reading, which is the
transferable part: a probe over the validated tree showed `union="StandardDate"`
present on every `Date`, and a trace at the `instance of` match site showed the
annotated value arriving 1614 times from the sort key and an *unannotated* one
arriving twice, from `apply-templates`. Reading the copy sites would not have
narrowed it; counting arrivals did.

**The invariant, which is what must survive.** The seven PSVI properties are
carried by two named operations on `xdm.Node` — `CopyTypingFrom` and
`CopyTypingStrippedFrom`/`StripTyping` — rather than by a hand-written field
list at each of ten copy sites. `xdm/typing_test.go` censuses `Node`'s exported
fields against the PSVI list and against an explicit list of the fields that
are deliberately excluded, so a new field is neither absorbed nor exempted
silently. `DocumentURI` is the one that looks like it belongs and does not — it
is the URI a document was RETRIEVED BY, and a copy was not retrieved.

The tenth site is the instructive one, and the direction of its copy is why
reading missed it. `copyAnnotationTree` in `xslt/validate.go` carries an
assessment *back*, from the document `xsl:result-document` validated onto the
nodes the result actually records. Every other site copies FROM the tree the
caller holds; this one copies from a tree the engine built and is about to
discard, so it does not look like a copy site at all until the question is
asked as "what arrives here, carrying what?".

---

## Corrections

Verdicts recorded in this file that were **wrong**. Not fixes — a fix leaves no
trace here — but readings that were believed, quoted, and disproved. They are
kept because a negative result that was believed for two revisions is more
dangerous than an open bug, and deleting one invites the same probe again.

### "An inline function declaration is grounded" — only if it mentions nothing streamed

Since v2.2.0 two more closures are unknown rather than grounded: one over a
quantified range variable bound to streamed nodes, and one calling `current()`
or `current#0`. Read literally, §19.8.8.16 grounds both; withholding the verdict
keeps execution from trusting it without refusing anything.

Three §19.8.8 expression rules were proposed with code to implement them, and
reading the Recommendation first changed two of them.

**§19.8.8.16 was proposed as an unconditional `return groundedMotionless`.**
The section says an inline function declaration "that textually contains a
variable reference bound to a streaming parameter (of some containing
stylesheet function) is roaming and free-ranging. All other inline function
declarations are grounded and motionless." Returning grounded unconditionally
is a **false negative** — a stylesheet accepted whose inline function closes
over a streamed node, which is the one failure mode this analysis exists to
avoid.

The parenthetical also decides the implementation. The streaming parameter is
the enclosing `xsl:function`'s, not one of the inline function's own, which
cannot be streaming — so the proposed variable-environment model was not
needed. One textual walk answers the question the spec actually asks.

**The walk descends into nested inline functions, deliberately.** A reference
to the outer `$element` inside `function($y) { $element }` is still within the
outer declaration's text, and the section's note says why it must count: "the
only other way an inline function could access a streamed node is by having
the streamed node in its closure, and this is prevented by the rule above." A
walker that stopped at that boundary would let exactly that closure through,
and sabotage-testing one does.

**The walk is three-valued.** The analyzer dispatches 22 expression kinds; a
walker answering "no reference" for a kind it had not been taught would ground
a function that captures a streamed node. An unrecognised kind routes to
`unknown()`, where the checker reports nothing rather than something wrong.

**§19.8.8.15 was proposed the same way and is also conditional** — roaming iff
the referenced function is focus-dependent *and* the context posture is not
grounded. Focus dependence was the missing half — nothing in the manifest
records it — and is now a table, `focusDependent` in `xslt/streamfocus.go`:
33 (name, arity) rows transcribed from the F&O 3.1 and XSLT 3.0 "Properties"
paragraphs, each citing its sentence. A built-in absent from the table is
focus-independent and grounded; a stylesheet function is grounded because
§5.3.3.1 makes its body's focus absent; an extension function is still no
opinion, as the section leaves it implementation-defined.

**`let` needed no invention at all.** §19.8.8's operand-role table gives it
directly — `let $var := N return T` — with the note "Binding of variables to
streamed nodes is not allowed." The return is an ORDINARY operand, which is
the whole difference from `for`: §19.8.8.1 calls the for expression's return
"a higher-order operand with usage transmission", and the table gives let a
bare T, because the binding is evaluated once rather than once per item.
Making it higher-order would refuse a consuming let body the spec permits.

None of the three moved a conformance count, which is expected: the
XSLT 3.0 disagreements at the time were 3 XTSE3430 cases plus 20 that are fixture
defects, deliberate divergences or implementation-defined (since M8: 19, none of them an
XTSE3430 case).

### "Remove the 1<<20 arity ceiling" — rejected twice, and it is a memory bug

An audit has twice proposed raising `maxLookupArity` (`xpath/fn_hof.go`) to
the representable `int` limit, on the reading that the `1<<20` bound is an
artificial cap on `fn:concat`. It is not: it is the only thing standing
between a one-line expression and an out-of-memory kill.

`synthesizeVariadic` (`xpath/version.go`) answers an arity by building the
signature for it — `make([]string, arity+1)`, then a write per parameter. The
arity comes straight from the caller, so with the bound at `MaxInt` a single
`fn:function-lookup(xs:QName("fn:concat"), 9223372036854775807)` asks for a
slice of nine quintillion strings. The ceiling is a resource guard, and
removing it is a denial-of-service hole rather than a conformance gain.

**The test offered with the proposal cannot fail.** It asserts that
`concat#1000001` resolves; `1,000,001 < 1,048,576`, so that arity is under the
current cap and succeeds today, before and after the change alike. Nothing
about the bound is exercised. The first arity that touches it is `1,048,577`.

**The single shared constant is itself a fix, not an accident.** The comment
at the declaration records the earlier shape: enforcing the range at
`fn:function-lookup` alone made the two acquisition routes disagree above
2^20 and left `concat#9223372036854775807` — the exact saturation value the
bound exists to refuse — reachable through the named function reference.
`maxLookupArity = maxVariadicArity` is what keeps one expression from
answering two ways.

**The prerequisite is now done.** The ceiling was a symptom of signatures
being materialised per arity, so that is what changed: `xdm.VariadicSignature`
carries a minimum arity, a result type and the one repeated parameter type,
and `functionItemMatches` reads it in place of indexing a slice.
`synthesizeVariadic` describes the signature instead of building it —
**16,806,800 bytes at arity 2²⁰ before, 2,280 after**, measured, and constant
rather than linear in the arity the caller supplies.

`MinArity` is on the descriptor rather than enforced only at construction
because it is part of the declared type: F&O 3.1 declares `fn:concat` for two
arguments or more, so an item claiming `concat#1` must fail a function test of
arity 1 however it was obtained.

**The ceiling is now gone too, and that closes the conformance gap.** F&O 3.1
declares `fn:concat` for two arguments or more and states no maximum, so a
2²⁰ cap made `concat#1048577` answer "no such function" for a function the
spec says exists. Both enforcement points went — `maxVariadicArity` /
`maxLookupArity` in `xpath`, and the `maxAvailableArity` that mirrored them in
`xslt/rtfuncs.go` — leaving only a check that the arity is REPRESENTABLE:
`int64(int(v)) != v` rather than a bare cast, because `int` is narrower than
`int64` on a 32-bit host.

The order mattered. Removing the cap first would have been the
memory-exhaustion hole the audit's version was; removing it after the
descriptor is a conformance fix, because the arity no longer sizes anything.

The old comment defended the cap as refusing "an arity no argument slice can
ever hold". Measured, that concern is handled elsewhere: applying such an item
raises `XPTY0004` from the ordinary arity check before anything is allocated,
so an item at 2⁶³−1 is inert rather than dangerous. What is still refused is
what cannot be narrowed — a bignum arity must not saturate onto `MaxInt` and
resolve, which is a real defect and is pinned separately.

### `fn:function-available` disagreed with `fn:function-lookup`, and the cap hid it

Removing the ceiling exposed a defect older than the ceiling.
`function-available('concat', 101)` was **false** while
`fn:function-lookup` at the same arity was **true**. The boundary was
`concatMaxArity`, not 2²⁰: both answered false above the cap, for different
reasons, so the disagreement was invisible until the cap went.

The cause is in `xpath.LookupDynamic`. A library implementing
`DynamicFunctionLibrary` — which both XSLT libraries do — gets to answer for
itself, and the function returned on its "not found" **before** reaching the
synthesis. Both XSLT libraries delegate to a plain `Lookup`, which knows only
the registered arities, so every variadic arity above `concatMaxArity` was
invisible to `fn:function-available`, `xsl:evaluate` and package-scoped
lookup alike.

Verified as pre-existing by stashing to `d896236` and re-probing. The fix is
to fall back to `synthesizeVariadic` at that early return rather than teach
each library to synthesize, which keeps one definition of what arities
`fn:concat` has. It cannot leak a hidden function: `synthesizeVariadic`
borrows the registered `concat#2` through `ctx.Funcs`, so a library that does
not expose that entry synthesizes nothing.

Three of the tests written for this were **vacuous on the first attempt**, and
each failed to reach the code it was about — the same trap as the `qnamedyn`
probes. One used an empty `SequenceType{}`, which spells `item()` and is
refused by the parameter check before `MinArity` is consulted. Two used
`concat#3` and `concat#101 instance of function(...)` with a single parameter:
arity 3 is *registered*, so it carries an ordinary `Signature` and never
touches the descriptor, and an arity-1 function test is settled by the arity
check. All three now fail under sabotage, and each carries a comment naming
the trap so the next reader does not re-fall into it.

### "A nilled element must not satisfy a key field" — it must, and the suite says so

An audit reported that `keySequence` (`xsd/identity.go`) lets a nilled element
contribute a key value, on the reasoning that a nilled element has no
[schema normalized value] and so no ·key-sequence· member. The fix was
specified two ways — treat it as ABSENT, or fail it outright — and **both would
have been conformance regressions.** No change was made.

`idF018` settles it. Its `<field xpath=".">` selects the nilled element itself,
over two `<uid xsi:nil="true"/>` children, and the suite expects the schema
**valid** and the instance **invalid**, `accepted` since 2006 with no bugzilla
history. Invalid is reachable only if both nilled elements DO produce a key
sequence and those sequences collide — which is the behaviour the audit called
the bug. Under the "absent" reading neither element would qualify, nothing
could collide, and `idF018.i` would be accepted. We pass it today *because of*
the code the audit asked to remove.

The "violation" reading fails separately: `idL098` and `idL102` are `xs:key`
over three `xsi:nil="true"` elements and are expected **valid**, so nilling is
not a hard failure either. It is not the `complexTyped` clause — a
complex-typed element has no simple type at all, whereas a nilled element keeps
its declared type and is still fully ·assessed· (see `xsd/validate.go:617`).

**One real weakness was found and deliberately left.** The empty key string for
a nilled element comes from `keyString`'s untyped `StringValue()` fallback, not
from a decision, so it is indistinguishable from the fallback taken by a node
that was never annotated. It gives the right verdict for `idF018` because all
nilled elements collide with each other regardless of type, but a nilled
`xs:int` silently shares a key with a nilled `xs:string`. Making that
intentional — an explicit sentinel — is a behaviour change with no oracle in
the suite, which leaves `key` + `field xpath="."` + a *single* nilled element
untested. Recorded rather than done.

### "The xs: constructors answer every function test true" — half right, and the fix was reviewed backwards

The finding was real and is fixed (above). What is recorded here is the
**reasoning that was wrong three times over**, because it is the kind that
regenerates.

The audit, the fix, and the review of the fix each illustrated the bug with
`xs:integer#1 instance of function(xs:date) as xs:integer?`, calling its
`true` answer absurd — "the integer constructor does not take an xs:date".
**That answer is correct and must stay true.** XPath 3.1 §2.5.6.2 judges
arguments with `subtype(Ba_I, Aa_I)` — the TEST's parameter against the
FUNCTION's — and notes "Function arguments are contravariant". A constructor
declared `xs:anyAtomicType?` does accept an `xs:date`, so a test naming the
narrower type is satisfied.

The genuine discriminators are the RETURN type and a *widened* parameter:
`function(xs:anyAtomicType?) as xs:date?` and `function(node()) as xs:integer?`
are both false. Had the "fix" been written to make the contravariant case
false, it would have broken conforming behaviour while appearing to close the
finding. Both directions are now pinned in
`TestConstructorFunctionItemsCarryTheirDeclaredType`.

### "The format-number exponent rule is narrow, and the suite tests every corner of it" — it tests neither half

`splitExponent` (`xpath/formatnumber.go`) carried that claim in its own doc
comment, citing `numberformat108`, `113`, `143` and `144` as the corners. Those
four cases are real and still pass, but they cover only the middle of F&O 3.1
§4.7.3. **Both edges of the exponent rule were unimplemented, and no case in
either corpus reaches them** — every exponent picture in
`testdata/qt3tests/fn/format-number.xml` and in
`testdata/xslt30-test/tests/fn/format-number/` puts digits before the separator
and digits-then-passive-text after it. The counted figures do not move with
either defect in either direction, which is why a comment asserting full
coverage survived.

**The separator was classified on what followed it alone.** §4.7.3: "A
character that matches the exponent-separator property is treated as an
exponent-separator-sign **if it is both preceded and followed** within the
sub-picture by an active character. Otherwise, it is treated as a passive
character." Only the *followed* half was tested, so a separator with nothing
active before it still split the picture and handed `parsePicture` a mantissa
that was empty or digit-free:

    format-number(1234, 'e0')    => FODF1310: picture "" contains no digit characters
    format-number(1234, 'xe0')   => FODF1310: picture "x" contains no digit characters
    format-number(1234, 'abce0') => FODF1310: picture "abc" contains no digit characters

The error message is the tell: it names a picture the caller never wrote,
because the split had already discarded the rest. Under the rule the `e` in
each is *passive*, so `'e0'` is the prefix `"e"` over the digit region `"0"`
and the answer is `e1234`.

**An active non-digit after the exponent digits was kept as a suffix.** The
same block: the sign "must be followed by one or more characters that are
members of the decimal digit family, and **it must not be followed by any
active character that is not a member of the decimal digit family**." The tail
was scanned only for a *second* separator-plus-digits (`numberformat108`);
every other active character fell through into `p.expSuffix`:

    format-number(1234, '0e0.0')  => "1e3.0"    (want FODF1310)
    format-number(1234, '0e0#')   => "1e3#"     (want FODF1310)
    format-number(1234, '0e00,0') => "1e03,0"   (want FODF1310)

Active is the spec's own list — decimal-separator, exponent-separator,
grouping-separator, digit, pattern-separator, and the decimal digit family —
and *followed* means anywhere later in the sub-picture, not immediately after,
which the spec states explicitly. So `'0e0xy#'` is an error too.

**The fix must not over-reach, and the boundary is the recursive definition.**
A passive suffix is still legal: `'0e0xyz'` is `1e3xyz`, and `'9.9999e99e'`
keeps its trailing `e` (`numberformat144`) because that `e` is followed by
nothing and so is not itself a sign — an exponent-separator in the tail counts
as active only where digits follow it, which is exactly the pre-existing
`numberformat108` check. A fix that rejected the character class outright
would have broken both.

Neither corpus can hold this in either direction, so
`TestFormatNumberExponentSeparatorNeedsPrecedingActive`,
`TestFormatNumberActiveCharacterAfterExponentDigits` and
`TestFormatNumberExponentPassiveSuffixStillFormats` are the only thing that
does. Each half was sabotage-tested independently: disabling the preceding-active
check fails exactly the four Defect-1 cases with the original leaked-mantissa
messages, and disabling the tail check fails exactly the four Defect-2 cases
with the original wrong outputs, while the other group stays green.

Two further §4.7.3 readings were checked and left alone. A **leading** grouping
separator (`',0'` gives `1,2,3,4`) is odd but conformant: the spec forbids one
adjacent to the decimal separator or at the end of the integer part, and says
nothing about the start. And the exponent/percent conflict message still names
the mantissa rather than the whole picture (`'#.#e0%'` reports `"#.#"`) — the
same cosmetic leak as above, on a path where the error code is already correct.

### The streamability comments were written against the Last Call draft

A systematic audit of the quoted spec text in `xslt/stream*.go` — 245 distinct
quotations matched against 465 section headings extracted from
`testdata/xslt30-test/specs/xslt-30.html` — found roughly **30 defective
citations**. The large majority of quotations passed; what follows is what did
not, because the pattern behind them matters more than any single line.

**The cause is a document, not carelessness.** The file family was written
against the **Last Call Working Draft**, and the Recommendation renumbered and
rewrote parts of §19. Where the LCWD wording was quoted it is often now absent
from the spec entirely, and the §18.2.x accumulator range shifted by one when
`fn:accumulator-before`/`after` were renumbered, so section numbers across
`streamaccumulators.go` are systematically LCWD-numbered.

**The element table drifted the same way.** `xslt/elementtable.go` was
transcribed from the same draft, and three classes of entry accepted what the
Recommendation forbids: `avt` flags on attributes the Recommendation writes
without braces, which suppressed the static XTSE0020 on a `"{...}"`; `hidden`
in the visibility enumerations of `xsl:template`, `xsl:mode` and friends,
though a component only ever acquires that visibility through `xsl:accept` or
`xsl:expose`; and withdrawn draft attributes tolerated in silence, now named
with `removed30`, the table's mechanism for refusing a withdrawn name where
forwards-compatible leniency would otherwise ignore it. `param/@export` was
the one withdrawn attribute left tolerated, because `iterate-024` must reach
the XTSE0010 further down its stylesheet and the attribute sweep reported
XTSE0090 first. That was a check-ordering defect rather than a reason to
accept the attribute: §8.4's placement rule for `xsl:on-completion` is now
read off the tree ahead of the grammar sweep, so the structural error wins and
`@export` is `removed30` like the rest. The table is now checked against the
Recommendation itself: `TestElementTableMatchesRecommendation` parses the
vendored summaries and fails on any divergence `docs/element-table-policy.md`
does not declare, which is how the 2026-10-07 drift audit's remaining
findings (`hidden` on `xsl:expose`, open `@method`/`@data-type` AVTs, an
optional `xsl:package/@version`) were closed.

Three classes were found, and only the first can mislead the code:

1. **Quotations stating a rule the spec does not state.** §15.4's third
   condition was quoted as "has striding posture", dropping "**or grounded**"
   and the entire sweep clause — a strictly stronger rule than the spec's.
   XTSE3195 was quoted as fixing the permitted values of `@streamable`; it
   constrains only which attributes may co-occur, and the test named as
   asserting that refusal does not exist. §19.8.9.3 was quoted in a form that
   drops two of its four branches. These are corrected.
2. **Verbatim quotations under the wrong number.** The sweep-ordering sentence
   is §19.6, cited as §19.7; the context-posture clause is §19.5, cited as
   §19.6; "is not required to assess whether constructs are
   guaranteed-streamable" is §19.10 ("Streamability Guarantees"), cited as
   §19.1 ("Determining the Static Type of a Construct"). Corrected where found.
3. **Paraphrase inside quotation marks.** Several comments put the
   implementation's own reading in quotes — `streamfunctions.go:385` renders
   §19.8.5.2 in a form that swaps the conclusion for a premise, and
   `streamaccumafter.go:57` presents a climbing clause §19.8.9.1 does not have
   (the comment below it acknowledges this as Bug 30018, but the quotation
   marks assert spec text regardless).

**Why this is recorded rather than merely fixed.** A quotation is the one thing
in a comment a reader will not re-derive — it is read as the authority the code
answers to. A stale one therefore outlives every other kind of stale comment,
and a *wrong* one silently licenses a wrong change: the §15.4 rule as quoted
would have justified rejecting a grounded merge source the spec permits.

**All three classes are now closed.** The renumbering was applied in one pass
over 63 sites, checked against 482 section headings extracted from the local
Recommendation: §19.8.8.7→.8 (path expressions), §19.8.8.6→.7 (simple map),
§18.2.8→.9 (streamability of accumulators), §19.8.4.35/.36/.37→.37/.38/.39
(source-document, text, try), §19.8.8.13→.14 (static function calls), and
§3.8.2→§3.7.2, the one cited number that names no section at all. Every §
citation in `xslt/stream*.go` now resolves to a real heading whose title
matches the code beneath it.

Four quotations that no version of the spec carries were replaced with the
Recommendation's own words: the positional-predicate note (§19.8.8.8 actually
says scanning expressions *cannot* use them), the `xsl:stream`
guaranteed-streamable sentence (§19.9, and the instruction is
`xsl:source-document`), the "expressed informally" note, and the
`use="prohibited"` grammar reference. Two more drifted in substance and were
corrected: the simple-map rule takes the **wider of the two sweeps**, not the
right operand's, and §3.7.2 says **ancestor-or-self**, not "innermost
ancestor". In both cases the code was already right and only the quotation was
wrong — `defaultModeFor` starts its walk at the element itself.

One sentence was kept but taken out of quotation marks: the "declared type
permits nodes" definition in `streamfunctions_test.go` is this
implementation's reading of §19.2's U-types, not spec text, and now says so.

**The audit itself missed one, and the repo had already found it.** Variable
references were cited as §19.8.8.11 at 25 sites; that section is *Dynamic
Function Calls*, and variable references are **§19.8.8.12**. The correction was
not new — a comment in `streamfunctions_test.go` records it verbatim, written
during the `varPosture` work: "§19.8.8.11 is *dynamic function calls*; the rule
for a variable reference is §19.8.8.12". One test was corrected and the other
24 sites were never brought along, which is the same failure mode as the
NCName transcription that existed correctly three times while `cast.go` kept
its own broken copy. The 24 are corrected; the one site that names §19.8.8.11
deliberately, to say why it is wrong, is left alone.

The check that finds these is cheap and worth repeating: extract every
`<hN>` heading from `testdata/xslt30-test/specs/xslt-30.html`, then confirm
each `§` citation resolves to one and that its title matches the code beneath.
960 sites over 98 distinct sections currently resolve, with none dangling.

Citations outside `xslt/stream*.go` were checked too and are sound: the 25
that name no XSLT 3.0 section are attributed to *other* specifications — HTML
4.01 §9.3.4 and §17.7, F&O §17.5.3, XDM §5.8.3 — or explicitly to XSLT **2.0**
(`xslt/validate.go`'s §19.2.1.3), where those numbers do exist.

### A §19.8.9.3 quotation that no longer appears in the spec

`xslt/streamaccumulators.go` quoted §19.8.9.3 as "The use of the current
function within a pattern is supported with similar restrictions. In this case
the context posture is always striding." **That sentence is not in XSLT 3.0.**
It is Last Call draft wording, superseded by Bug30033; the Recommendation says
instead that a `current()` call inside a pattern is *climbing* and motionless.

The code was checked separately from the comment, and the code is right. It
sets `postureStriding`, and for the one question this function asks — is the
predicate **motionless**? — striding and climbing are indistinguishable. Off
`current()`, every axis motionless under climbing (`self`, `parent`,
`ancestor`, `ancestor-or-self`, `attribute`, `namespace`) is motionless under
striding, and every axis that is not is non-motionless under both. No
pattern's verdict turns on the choice. The comment was replaced with the real
wording plus that argument, rather than the posture being changed to match a
citation nobody had re-read.

Recorded because a quotation is the one thing in a comment a reader will not
re-derive, so a stale one outlives every other kind of error.

### 49 `xs:` constructors answered every function test true

`xs:integer#1 instance of function(xs:date) as xs:integer?` was **true**, and
so was `xs:date#1 instance of function(xs:date) as xs:integer?`. Both are
nonsense. `functionItemMatches` falls back to judging an item on arity alone
when it carries no signature — correct for an inline function nobody declared,
wrong for a constructor whose type F&O 18.1 states.

The cause was a misread exemption. `cmd/genfunctions` builds the manifest from
the per-function proformas in the spec, and 18.1 does not write one per type:
it gives the shape **once**, `eg:TYPE($arg as xs:anyAtomicType?) as eg:TYPE?`,
for every built-in atomic type at once. So the constructors have no manifest
row, `TestRegisteredFunctionsHaveManifestMetadata` excuses the namespace, and
that excuse was read as "these have no declared type" when 18.1 declares it
uniformly.

The signature is now derived rather than listed — 49 hand-written rows saying
the same thing is 49 chances to mistype one, and a constructor added later
would silently get none. Constructors of *imported schema* types
(`lookupSchemaConstructor`) keep the permissive treatment deliberately: their
result spelling is not in the closed set `spellingSubsumes` knows, and an
unknown spelling subsumes only itself, so annotating them would turn a
permissive wrong answer into a strict one.

One constructor is not uniform, and the suite is what said so. `xs:error` is a
union type with **no member types**, so its value space is empty and the only
result it can ever have is the empty sequence: F&O 18.4 declares it
`xs:error($arg as xs:anyAtomicType?) as empty-sequence()`, and QT3
`xs-error-007` asserts that spelling exactly. Deriving `xs:error?` like the
other 48 failed that case — the first evidence the items were being judged on
their type at all, since it had passed *vacuously* for as long as every
constructor matched on arity alone. The three built-in list types of 18.3 are
the other exception, returning the **item** type repeated
(`xs:IDREFS(...) as xs:IDREF*`), because the `minLength=1` facet belongs to the
list and not to the return type.

That vacuous pass is the reason the regression guard is a unit test rather than
the suite: removing the annotation leaves QT3 green at 15217/19362/21898 and
30345, and fails `TestConstructorFunctionItemsCarryTheirDeclaredType` with five
named nonsense matches. The suites are unmoved in both directions — XSLT 3.0
stays 11490/28 and XSLT 1.0/2.0 6193/8 — so the fix is a pure tightening.

### "§19.8.9.3 costs four valid stylesheets" — it gains ten and costs none

An uncommitted verdict, carried between sessions as an oral claim and never
written down until it was re-measured and found wrong. The claim was that
implementing §19.8.9.3 — the streamability of `fn:current` — measured `+5` by
count but `9 passing / 4 regressed` by name, and that the four regressions were
spurious refusals of `stream-200`..`203`, whose accumulator rule is
`part-name/text()[$selected-parts = current()]`. On that basis the work was
said to have been reverted.

**Re-measured from the same baseline: +10 cases, zero regressions.**
`sf-current-901`..`905` as expected, and five more that came free —
`si-for-each-904`, `si-iterate-035`, `si-iterate-904`, `stream-204`,
`streamable-110` — because a `current()` call the analysis could not model used
to abandon the whole enclosing construct.

**The regression was real, and it was a bug in the implementation rather than a
cost of the rule.** §19.8.9.3 gives the call the context posture of the
*outermost* containing XPath expression; what it does not say, because §19.8.1
already does, is what happens when that call is *absorbed*. `current()` in
`text()[$parts = current()]` denotes the text node the pattern matched, and a
text node has no children, so §19.8.1's downgrade — *"If U is absorption and
the intersection of T with U{element(), document-node()} is U{} … then U′ is
inspection"* — applies exactly as it does to the equivalent `text()[$parts =
.]`. An implementation that answers §19.8.1's question for `current()` with the
blanket "assume children" default makes the predicate consuming and refuses all
four stylesheets. Answering it from the outermost context item instead — the
`currentAllowsChildren` field in `xslt/streamability.go` — refuses none of
them.

The suite pins both directions, which is why the fix is not a loosening:
`stream-200`..`203` match on `text()` and must **run**, while `stream-204` is
the same accumulator rule on an element step, where the absorption stands and
`XTSE3430` is correct. `stream-204` is one of the ten gained.

The lesson is the one this section exists for. **A revert recorded only in
conversation is a measurement that cannot be checked**: there was no revert
commit, no residual code and no entry in either gap file, so the claim survived
purely on retelling while `conformance-gaps.md` went on listing `fn:current`
among the rules absent entirely. Had the four names been written down beside
the verdict, the next reader would have seen in one step that the four share a
`text()` step and that the fifth sibling on an element step is a case the suite
wants refused — which is the whole diagnosis.

### A negative result on a bound must prove the loop it bounds actually runs

The most expensive lesson in this file, and the one most likely to be
re-learned by anyone auditing a step count. An earlier revision of this
document argued that a family of `seen > 64` and `seen > 256` counters were
*not* defects, and the argument read:

> These counters remain on *iterative* walks up a type's base chain rather than
> recursive descent through a graph; a legal restriction chain 300 links long
> was checked in both directions and the facet survived intact, so these
> counters are not truncating a real schema.

**The measurement was real and the conclusion was wrong, because the probe
drove a walk that does not iterate.** A facet chain collapses during parsing:
`SimpleType.Primitive` is filled in on every link as it is built, so
`primitiveOf` returns on its *first* iteration whatever the chain length, and
the facet is enforced from the merged `FacetSet` rather than by walking at all.
A 300-link chain exercised the loop exactly once. The probe proved nothing
about the bound and everything about the parser.

**A baseline that reads "correct" for the wrong reason is worse than no
baseline**, because it is quoted afterwards as evidence. This one was, for two
revisions. The walks that *do* iterate are the ones asking a question the
parser did not pre-answer: which built-in a type descends from, and whether one
type derives from another. Six of those truncated on a legal acyclic chain, and
two false accepts are what the superseded reasoning had licensed — a duplicate
`xs:ID` accepted once the restriction chain under `xs:ID` ran 64 links, and
`"1.5"` validating against a type descending from `xs:integer` at the same
depth. Neither schema is recursive or malformed.

So the rule for any future audit of a bound: **measure the chain length on the
built component before concluding anything**, and show the loop taking one step
per link. `TestBaseChainActuallyIterates` is written that way for exactly this
reason, and `TestDeepFacetChainCollapses` and `TestDeepUnionAndListCollapse`
pin the collapsing walks so that the superseded negative result above cannot be
re-derived from the same shape. The design rationale that replaced it — why a
visited set is the exact mechanism and a count never was — is in
[xsd.md](xsd.md#limits).

### `particlesZ033_g` was diagnosed as an element-against-element pair, and there is no competing pair at all

This file previously reasoned that since XSD 1.1 switches element-against-
wildcard competition off, the 1.1 rejection had to come from an
element-against-element pair, and named two candidates — the two `e2`
declarations at different nesting levels, and `ref='m1'` against `ref='head'` —
both of which it believed `counterForces` was suppressing, so it pointed a fix
at `exitBlocked`.

Enumerating every state of every content model in the schema shows otherwise:
under 1.1 the schema has **no competing pair, suppressed or not**.
`counterForces` is never consulted and `exitBlocked` never sees the model. The
correct reading, and the seventeen-schema cost of the fix that was pointed at
the wrong place, are under *Deliberate divergences* above. The lesson is that a
diagnosis reached by elimination — "it cannot be the wildcard, so it must be an
element pair" — is not a diagnosis until the enumeration is actually done.

### The counts in *What would move the numbers* had drifted by two orders of magnitude

A section stood here carrying an XSD table that summed to 251 and 368
disagreements against 51 and 47 actually measured, and describing twenty-six
open XPath failures on suites that report none. It also restated percentages
that this file's own opening rule says belong in
[conformance-gaps.md](conformance-gaps.md) alone, precisely because a figure
copied into two places drifts in one of them. It drifted in this one.

What was durable is the route rather than the arithmetic, and it is worth
keeping because it predicts where the next round of failures will turn out to
live. Of the seventeen XPath disagreements that remained after the ordinary
bugs were fixed, **five were the QT3 harness rather than the engine**, two
needed DTD attribute defaulting, two needed a document to be retrievable under
the URI `fn:document-uri` reports for it, and one was a lexical form that
disagreed with its own value. The adversarial audit of the XSLT and XSD
verdicts found the same shape again: of the twenty-three cases it judged
fixable, most were the harness — chiefly eight XSD `indeterminate` expectations
per version silently scored as "must be invalid" — and only four were engine
defects.

**A conformance number is only as honest as the harness producing it, and a
verdict is only as good as the last time someone re-derived it.**

### "The fixable column is empty on every suite" was scoped to one audit

That sentence was true of the population the audit covered — the XSD and XPath
disagreements standing at the time — and is still true of those: XPath is 100%
at all three versions, and the XSD remainder is argued case by case above. It
was never true of the suites as a whole. **XSLT 3.0 then carried 126 failures
of which 45 were one missing analysis, and XQuery 3.1 carried 42, and both were
eminently fixable.** A sentence scoped to one audit and left standing after the
scope changed is the same decay this file keeps recording.

### "3 (1.0) and 2 (1.1) *addressable* false rejects" counted direction as tractability

A heading here read that, and then the body argued each of the three down:
`attP031` is a suite self-contradiction and its +1 is declined on purpose, and
the two `gMonth` cases are a withdrawn lexical form under an open bug. None of
the three was addressable. The heading counted false rejects and called them
addressable **because** they were false rejects, which is the direction that
matters — but direction is not the same as tractability, and the two had been
conflated.

### A case list is a measurement, and it decays

An *Instance validation gaps* section listed 25 XSD instance false accepts by
name — `Simple/simple001`, `simple002`, `simple016`, `simple086`,
`ElemDecl/typeDef012*`, `valueConstraint007*`, `MS-ComplexType/ctZ013c`/`-d`/
`-e`, `MS-IdentityConstraint/idG006`, `idK012`, `suntest/idc006.nogen`,
`XmlVersions/xv009`, `MS-Schema/schU4`, `schU5`, `MS-Additional/isDefault070`,
`isDefault077`, `MS-SimpleType/stE054`, `MS-Regex/reK6`, `Complex/complex022`,
`CTA/cta0006`. Re-measured, **not one of them still disagreed, in either
version**. The entries had survived several rounds after the bugs behind them
were already fixed, and each round quoted the list rather than re-deriving it.

The same decay ran through the numbers: an entry saying 18 bug-4113 cases
beside a section saying nineteen, when the enumerated count is 22; a
schema-validity section quoting 195 and 305 open rules measured before the bulk
of them landed; a "13 and 12 disagreements" summary that had been 5 and 7 and
was by then 3 and 5. **Re-measure before working an entry in this file.**
`tests/check.sh` is how, and *How to re-measure* below is where.

---

## Related

[conformance-gaps.md](conformance-gaps.md) is the ledger: every failing case in
every suite, named, with the current numbers and a fixable / not-fixable
verdict. This file is the reasoning behind the hard ones.

[xsd.md](xsd.md#limits) holds the design rationale for the schema engine's
bounds — the occurrence-count vector, the saturation split between the matcher
and the derivation checks, and why every graph walk is bounded by a visited set
rather than a step count.

[todo.md](todo.md) is the forward-looking half of this file: what to build next
and what each item would cost. Several gaps here — XML 1.1 DTD-side rules, DTD
support — are entries there as features rather than bugs.

[reaching-100.md](reaching-100.md) is what buying the remainder would cost.

## How to re-measure

Neither suite is vendored — both belong to the W3C, and `testdata/` is
gitignored. Clone them where the commands below expect:

```
git clone --depth 1 https://github.com/w3c/qt3tests.git   testdata/qt3tests
git clone --depth 1 https://github.com/w3c/xsdtests.git   testdata/xsdtests
```

The figures in this file were measured against `qt3tests` at `201a6e46`
(2026-05-14) and `xsdtests` at `7bc3365c` (2026-04-01). Both are updated from
time to time, so a later checkout can move a denominator.

`tests/check.sh` runs everything below in one go, and is what to use after any
substantive change:

```
GOXSLT_UBL=<ubl-dir> GOXSLT_CII=<cii-dir> tests/check.sh
tests/check.sh fast     # build, vet, unit tests, race only
```

It reports a missing suite as skipped and a present-but-silent suite as a
failure, because a check that did not run must not look like one that
succeeded. That distinction is not theoretical: the first run of this script
caught a relative `GOXSLT_QT3` resolving against `./tests/qt3/` rather than the
repository root, which made the suite skip itself while `go test` reported
PASS.

QT3 also runs from the test suite directly:

```
GOXSLT_QT3=$PWD/testdata/qt3tests go test ./tests/qt3/ -run TestQT3 -v
```

Set `GOXSLT_QT3_VERBOSE=1` to list every failure with the expression it ran,
and `GOXSLT_QT3_SET=<substring>` to run only the matching test sets — the
percentage is then labelled as filtered rather than quoted as the suite
result.

Before accepting any change that adds a schema-validity rule, load the
production corpora — 65 UBL 2.1 entry points and 427 UN/CEFACT CII schemas.
The suite cannot catch a rule that is stricter than the spec; real schemas can.
