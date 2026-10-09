# Profiling: what is left to fix

This document lists the performance work that is still open on v2, the
ideas that were measured and rejected, and how to measure. It was measured on
the `v2` branch at `ef76ae2c`; the Go code has not changed since apart from
the version string.

Fixes that have landed are not repeated here. Each is in
[CHANGELOG.md](../CHANGELOG.md) and the git history. The six earlier
profiling rounds (T1–T23, R1–R13, S1–S11, the round 4–6 changes, the v2
context split and node records, and the v2 allocation cuts) are in the
previous version of this file: `git show aefbd8a5:docs/profiling.md`.

## Where go-xml stands

Warm time, go-xml over the reference engine (geometric mean). Each version is
compared with the reference times from its own benchmark run
([benchmark](benchmark.md)). "v2, pre-fix" is the run before the fix waves;
"v2 now" is `377452c0`, after waves 1 and 2 (the fixes under
[Landed](#landed-in-the-v2-fix-round)). The [open fixes](#open-fixes) (V17,
V21–V51) are not projected.

| Workload | Reference | v1 | v2, pre-fix | v2 now |
|---|---|---:|---:|---:|
| DocBook xslTNG | Saxon-HE | 0.46× | 0.37× | 0.24× |
| DocBook `ptoc.001` | Saxon-HE | 1.49× | 1.17× | 0.98× |
| Peppol Schematron | Saxon-HE | 1.99× | 1.31× | 1.22× |
| XRechnung stage 1 | Saxon-HE | 3.00× | 1.77× | 1.46× |
| XRechnung stage 2 | Saxon-HE | 1.42× | 1.23× | 0.75× |
| XMark q1–q20 | Saxon-HE | 1.00× | 0.97× | 0.86× |
| XSD catalogs | Xerces-J | 0.73× | 0.69× | 0.73× |
| RELAX NG DocBook 5.2 | Jing | 0.60× | 0.75× | 0.91× |
| Parse 1/10/100 MB | `encoding/xml` | 0.54× | 0.57× | 0.53× |

The XSD and RELAX NG ratios follow the JVM validators more than go-xml.
go-xml's geometric mean per document went 1.07 → 1.05 → 1.02 ms on XSD and
26.8 → 28.4 → 27.6 µs on RELAX NG, while Xerces ran at 1.47, 1.53 and 1.39 ms
and Jing at 44.5, 37.9 and 30.4 µs in the same three runs. XRechnung stage 2
crosses 1× partly because Saxon's median there was 3.4 ms in the last run
against 2.8 ms in the pre-fix one; go-xml's went 3.3 → 2.6 ms.

On XSLT, most of the time left goes to the garbage collector and the
allocator: 26–47% GC marking and 14–28% `mallocgc`, from Linux profiles. CPU
follows allocation volume. At factor 0.1, XMark is parse-bound: the parse
takes 48–50 ms of every query except q10–q12.

### The three apparent regressions

| Benchmark figure | Finding |
|---|---|
| DocBook compile 55 → 77 ms | **Real.** Bisected to `a81dff28` (the 40-byte records). The stylesheet checks walk ancestors for the version attribute (`effectiveForwards`, `moduleAtLeast30`, `xpathVersionAt` …). Each `Attr` lookup now goes through the name table instead of an inlined field read, which costs about 5 ms. The static-phase copies add about 2 ms, and the smaller heap runs more GC cycles (115 against 84 per 20 compiles). Fixed by V7 (landed, see [Landed](#landed-in-the-v2-fix-round)) |
| RELAX NG warm 0.60× → 0.75× Jing | **Mostly Jing.** The same Jing jar ran 15% faster in this run, which accounts for about 73% of the change. go-xml's own share is +4–6% on small documents, from per-document parse set-up (V18). Long documents are 18–40% faster than v1 |
| Parse warm 0.54× → 0.57× `encoding/xml` | **Not the parse.** Parse wall time is level with v1 and its CPU is 39% lower. The C14N write that the item includes was 14% slower. V11 (landed, see [Landed](#landed-in-the-v2-fix-round)) cut the write 10–12% and the item 3.7%, from the output buffer rather than the accessors |
| RELAX NG warm 0.60× → 0.75× Jing | **Mostly Jing.** The same Jing jar ran 15% faster in this run, which accounts for about 73% of the change. go-xml's own share is +4–6% on small documents, from per-document parse set-up (V18, read windows landed, see [Landed](#landed-in-the-v2-fix-round)). Long documents are 18–40% faster than v1 |
| Parse warm 0.54× → 0.57× `encoding/xml` | **Not the parse.** Parse wall time is level with v1 and its CPU is 39% lower. The C14N write that the item includes is 14% slower, from the node accessors (V11) |

### Two regressions the ratios do not show

- **Typed validation** (`ValidateCopy`) costs 2.5–3.4× what v1's in-place
  annotation did. It makes two full per-node copies: the first to
  validate, the second to drop whitespace and add defaults. On top of that,
  three callers still copy the input first, as v1 had to. CLI
  `-validate strict` on a 2.5 MB catalog takes 11 ms on v1 and 38 ms on v2.
  Fixed by V2 (landed; see [Landed](#landed-in-the-v2-fix-round)).
- **XMark q10** evaluation is 55% slower. XQuery element content is copied
  twice per node (`xdm.Copy`, then `AppendNode`'s own copy), and
  `limitInherited` builds three maps per constructed element. Fixed by V1
  (`484cb4e8`, see [Landed](#landed-in-the-v2-fix-round)).

## Open fixes

From the second v2 profile at `377452c0`, after fix waves 1 and 2. Gains are
measured on prototypes (getrusage CPU, exact allocation counts, alternated
A/B) unless marked *estimate*; none has been through the full conformance
gate yet. Code layout alone moves timings on this machine by up to ±8%, so a
single CPU gain under about 5% is unconfirmed; byte counts are exact.

Two regressions against v1 that the benchmark ratios hide:
- **XSD validation itself** is 15–23% slower than v1 (the record accessors);
  faster parsing keeps the whole XSD item level. V21 + V26 bring validation
  back to about v1 (17.4 → 15.3 ms, v1 14.7).
- **Typed validation** (`ValidateCopy`) costs about 2× v1's validate phase:
  the per-node typing table is 80 B a node, twice the record. CLI
  `-validate strict` on a 2.5 MB catalog is 14% slower than v1 end to end.
- **Warm compile** is slower (CEN +18%, DocBook +10%): the smaller live heap
  runs the collector about twice as often during compile; DocBook keeps +7%
  with GC off (name-table attribute lookups). CLI cold runs are unaffected.

### Validation (XSD, typed validation, RELAX NG)

| ID | Fix | Where | Measured | Risk / API |
|---|---|---|---|---|
| V21 | Skip the facet checks for a simple type with no facets (one flag on the cached chain facts) | `xsd/validate_simple.go:225`, `xsd/component.go:585` | XSD validate −10% CPU | Low / none |
| V22 | RELAX NG name memos: typed copy-on-write maps keyed by namespace and local name instead of `sync.Map` keyed by `QName` | `relaxng/pattern.go:153,175`, `derive.go:348,449` | validate −9.5% small, −7% long | Low / none |
| V23 | RELAX NG reuses one child and one attribute buffer; a single text node skips the builder | `relaxng/validate.go:297,326,376` | with V22: corpus validate −15.5%, long documents −19%, allocations −69% | Low / none |
| V24 | The clone keeps a dense index of default-attribute insertions instead of three map lookups per record | `xdm/clone.go:47,56,141,184` | typed validate −8% wall | Low / none |
| V25 | XSD caches the annotation name and its resolution per type and writes typing once | `xsd/validate.go:2236`, `typevalidate.go:495`, `validate_attr.go:205` | typed allocations −25%, CPU −4% | Low / one xdm setter |
| V26 | Intern namespace URIs (cloned) so equal URIs compare by pointer; compare local names first | `xdm/parse.go:745`, `xsd/automaton.go:480,504` and others | XSD validate −5.7% | Medium-low / none |
| V27 | Typing as a pointer to a shared per-tree profile plus flags (16 B instead of 80 B) | `xdm/record.go:221`, `clone.go:195` | typed bytes −26%, CPU −3% beyond V25 | Medium / none |
| V28 | Record character-reference spans only for XML 1.1 | `internal/xmltok/xmltok.go:1213` | `regex-syntax` parse bytes −38%, fewer GC cycles | Low / none |
| V29 | `ParseString` uses its own string as the position source when decoding changes nothing | `xdm/parse.go:217–235` | XSD warm bytes −10% | Low-medium / none |
| V30 | RELAX NG derivative caches keyed by pattern id instead of an interface | `relaxng/derive.go:44,57,118` | *estimate* −8 to −10% long-document validate | Medium / none |
| V31 | Validate the original tree read-only and clone once instead of twice | `xsd/validate_copy.go:60` | *ceiling* the first clone (7%) and its bytes | High / none |
| V32 | Lazy package init for xpath, xslt, xdm and collate | init functions | *estimate* ≤5% of a cold small XSD run | Low-medium / none |
| V33 | Hand-written XML declaration check instead of a regexp | `xdm/wellformed.go:28` | 0.29 µs a document (~2.4% of a small parse) | Low / none |

### Parse and XQuery

| ID | Fix | Where | Measured | Risk / API |
|---|---|---|---|---|
| V34 | Small direct-mapped cache in front of the tokenizer's name map | `internal/xmltok/xmltok.go:841` | parse −3.6 to −5.3% | Low / none |
| V35 | Check an end tag against a stack of open names instead of a second lookup | `xmltok.go:378,406` | −1 to −4% | Low / none |
| V36 | One namespace-binding stack shared by `validateStartElement` and `buildElement`; name cache keyed on the tokenizer's interned strings | `xdm/parse.go:740,818`, `wellformed.go:91,235`, `record.go:288` | −1.5 to −4.5% each | Low-medium / none |
| V37 | Text runs stored straight into the text store | `xdm/parse.go:844–881` | 14–20 fewer allocations a parse | Low / none |
| V38 | `Rebase` stops at a subtree whose base did not change and holds no own bases | `xdmbuild/builder.go:245` | XMark q10 eval −6.6%; XSLT copies benefit too | Low; the prototype's Tree field must fit the 320 B class / none |
| V39 | Raise `smallNames` from 8 to 24 | `xdm/record.go:344` | q10 eval −8.8% CPU, −18% bytes | Needs a DocBook/Schematron A/B / none |
| V40 | Pass attribute values to xdm without the tokenizer's arena copy | `xmltok.go:457` | *estimate* ~1% CPU | GC-pacing caveat (see rejected) / none |
| V41 | Build the large package-level tables on first use | `xslt/elementtable.go:110,942,1143`, xpath tables | *estimate* −0.2 to −0.3 ms a cold CLI run | Low / none |
| V42 | Cache `canonCache` directory reads and stats | `xsd/assemble.go:556,572` | *estimate* ~0.1 ms cold | Low / none |

V34–V38 together: parse CPU −7 to −9%, XMark −8.4 to −9.2% wall (about 0.79×
Saxon from 0.86×), outputs byte-identical.

### XSLT

| ID | Fix | Where | Measured | Risk / API |
|---|---|---|---|---|
| V43 | Mark merge, grouping and regex absent at the root runtime (the rest of V20, which is otherwise already in the code) | `xslt/runtime.go:902` | XRechnung stage 2 −13% bytes, −7 to −8% CPU; stage 1 −4% bytes | Low / none |
| V44 | An absent bit for the output URI, so calls and pattern matches stop rebinding it | `xslt/apply.go:767`, `pattern.go:475`, `pattern30.go:498`, `outputfuncs.go:34` | with V43: DocBook −4.5%, CEN −5.3%, Peppol −6.6%, stage 2 −15.6% bytes | Low-medium / none |
| V45 | Install the module's base URI once on template and function entry, so body expressions stop copying the context | `xslt/apply.go:471,740`, `compile.go:754,1629` | DocBook −3.3%, stage 1 −4.4% bytes; `Compiled.scope` 4.9% → 0.6% of DocBook bytes | Low-medium / none |
| V46 | int64 fast path for integer `+ - *` (overflow-checked), integer positional predicates, shared small-integer constants | `xpath/operators.go:1077`, `xpath/eval.go:569`, `xdm/atomic.go:295` | Peppol −13.8% allocations, −5.7% CPU | Low; document `Atomic.Rat()` as read-only / none |
| V47 | Cache the whitespace-stripped copy of a `doc()` tree per stylesheet instead of per transform | `xslt/transform.go:840` | DocBook −3% bytes, about −7% CPU | Medium: bounded or weak-keyed cache / none |
| V48 | Grow the tokenizer scratch once to the remaining input when a long text run crosses a read window | `internal/xmltok/xmltok.go:1011`, `xdm/parse.go:254` | XRechnung stage 1 −7.3%, stage 2 −9.3% bytes | Low / none |
| V49 | Count recursion depth in place for non-leaf calls (as leaf calls already do) | `xpath/eval.go:833` | DocBook −4% bytes, −2.8% CPU | Medium / none |
| V50 | The text store keeps a string of 8 KB or more as its own block | `xdm/record.go:1404` | stage 1 −6.2%, stage 2 −4.3% bytes | Medium: audit the `unsafe.String` producers / none |
| V51 | `xpath.Context` 112 → 96 B | `xpath/context.go:38` | *estimate* about −3% DocBook bytes | Changes exported `Position`, `Size`, `Depth`, `Funcs` / v2 API |
| V17 | Slot-indexed frames for local variables (Saxon's design) instead of the `WithVar` scope chain | After V4, on all 42 DocBook items, `WithVar` is 13.0% of bytes and 6.6% of allocations, the runtime copies `withVar` makes another 3.7% and 5.1%; `lookupVarPlain` is 2.4% of CPU. Of the `withVar` allocations, 30% are locals in a sequence constructor, 21% function parameters, 16% template parameters, 18% the grouping and regex clearing bindings a function call makes | `xpath/context.go`, `xslt/runtime.go:367` | Measured ceiling, not a gain: a frame prototype that binds all of one sequence constructor's locals in one allocation saved 0.5% of allocations and no CPU (most constructors bind one variable). Frames across a whole template or function, parameters included, would remove at most about 10% of allocations and 15% of bytes, so the −10 to −15% CPU estimate is the ceiling. The clearing bindings (18%) can be skipped when the component is already absent, without frames | High effort: XPath has no compile-time scope for XSLT locals, so slots need one threaded through `xpath.Compile`, for/let/quantified, inline functions and closures that capture a frame | Host variable binding through the context must keep working |

V43–V49 together: DocBook −13.3% CPU, −15.1% bytes; Peppol −10.8%, −14.2%;
XRechnung stage 1 −9.1%, −18.5%; stage 2 −15.8%, −29.5%; CEN −4.1%, −6.3%.
`ptoc.001` goes from Saxon parity to about 0.94×. XSLT 2.0/3.0 unchanged on
the combined prototype. V17's ceiling is unchanged (locals 36%, function
params 26%, template params 18% of the remaining `WithVar` bytes).

Measured and not worth it this round: an `Atomize` fast path for all-atomic
input (allocations −5 to −9%, no CPU), reading a schema in one system call (no
gain), a `checkChars` table loop for short text, a byte table for the C14N
escape test. `xdm.ErrorCode` allocating its `errors.As` target on a nil error
is a trivial fix worth folding into any of the above.

## Landed in the v2 fix round

A/B against `0ff09c65`, getrusage CPU and allocation counts, medians of three
alternated runs (four for parse).

| ID | Commit | Measured |
|---|---|---|
| V1 | `484cb4e8` | XMark q10 eval −38% at 0.1 (−32% at 0.01), allocations −43%; q13 eval −37%, allocations −27%; q1–q20 allocations −12.7% |
| V3 | `7186eb0e` | Parse 1 MB −2.2%, 10 MB −2.4%, `xp-striding` catalog −3.2%; XMark parse phase −6.0% at 0.1. Allocations +0.3% (one cache per indexed tree) |
| V6 | `ae4f4d1b` | `table-cals.049` validate 18.8 → 12.7 ms CPU (−33%); 589-document corpus −18% (−5% with parse); 40-document set flat. Verdicts and messages identical on all 589 documents; pattern bytes +2–24% |
| V8 | `5509745a` | `HTTPResolver` is `xsdnet.HTTPResolver`; `goxml_nohttp` removed. CLI binary −2.0 MB (−11%); small XSD validation 4.2 → 2.9 ms CPU, RSS 12.5 → 7.7 MB. Only `xsd/xsdnet` links `net/http` |
| V9 | `c5184cb8` | `default.pgo` regenerated on v2: warm CPU −1 to −6% (DocBook −1 to −2%, CEN −3 to −6%, RELAX NG −3 to −7%, XSD level); cold CLI within ±1.5%. `MemProfileRate = 0` was not added: the CLI does not link `runtime/pprof`, so the linker already turns sampling off (forcing it on costs up to 10% on DocBook). The −8% in the V9 row came from a harness that links `runtime/pprof` |
| V2 | `18c35f6`, `b48adaf` | XSD catalog warm `ValidateCopy` 102.2 → 53.5 ms CPU per pass, 74.8 → 53.5 MB; `xp-striding` 38–45 → 18–19 ms (v1 22), unicode-90 20.9 → 11.7 ms. 2.5 MB catalog: CLI `-validate strict` 63.0 → 43.2 ms CPU, `xsl:source-document validation="strict"` 77.9 → 54.2 ms, XQuery `validate strict {}` 68.5 → 44.9 ms. The clone is behind `internal/xdmclone`; no exported API. Still per node: a copy whose assessment added namespace declarations, and the ancestors-only first copy of a node below the document element. The source-document and merge pre-copy is kept for modes other than strict-with-schema and for trees with a DTD or positions; XQuery `validate` keeps its copy (made in bulk), since dropping it would change the result for an operand with a parent or a document-node operand |
| V10 | `e48af61` | XSD catalog warm `Validate` 18.9 → 18.1 ms CPU, unicode-90 5.29 → 4.80 ms, 508 → 19 KB and 387 → 316 allocations per pass |
| V5 | `462ee9a2` `NameTest.Matches` local name first; `35cd4d60` per-transform fields to `transformState` (runtime copy 176 → 112 B); `16f3d909` one focus context per predicate, behind a static capture check; `f2c08b2e` strip-space answer remembered per package and name (cap 4,096); `db7e1ba3` no position stamp on `next-iteration`/`break`; `40dbca21` `stripAnnotations` skips a never-typed tree (`Node.TreeHasTyping`) | XRechnung stage 1 −12% allocations, −10% CPU (focus reuse); stage 2 −9% CPU (untyped skip), −6% bytes; DocBook items −5% CPU and −8% bytes from the field move, −6% CPU and −6% allocations from the focus reuse; the strip pass 450 → 170 µs per DocBook document; an included `xsl:iterate` −10% per iteration; `Matches` −9% per call (within noise on the workloads) |
| V7 | `23ace644` version-attribute walks remembered per Compile, off during the static phase; `be9eb58e` `FileResolver` remembers `EvalSymlinks` per path (cap 4,096; `os.Root` still confines at open) | DocBook compile −5% CPU from the walks, −7% CPU and −4% allocations from `EvalSymlinks` (32% → 6% of the compile profile on macOS); CEN compile −3.5% |
| V11 | `63155c73` C14N appends to its own 64 KiB buffer instead of calling a `bufio.Writer` per token; `da27a7cd` the serializer's `element` has no defers (they forced the runtime's deferred-call path on every element) and calls `WriteString` directly | Against `257ade0d`, 3 alternated rounds: C14N 1 MB −9.6%, 10 MB −11.9% CPU (exclusive −10.8%, −10.5%); parse + C14N −3.7% at 1 and 10 MB; one allocation fewer, +16 KB buffer per call. Serializing a finished result: XMark q10 −22%, q2 −20%, XRechnung stage 2 −8%, DocBook −18%; allocations unchanged. No xdm API was added: a record walker measured slower than the accessors (see rejected) |
| V4 | `7c5121ca` globals evaluated on first use, bound in one map scope (`xpath.Context.WithLazyVars`); `03adcab8` the errors and their wording stay those of eager evaluation: a global naming itself, reaching a cycle, mentioning `key(`, or with `xsl:message`, `xsl:assert`, `xsl:result-document` or `fn:trace` in its own body is still evaluated at the start, in declaration order. A/B against `257ade0d`, 5 rounds | All 42 DocBook items −9.7% CPU, −11.6% allocations; `ptoc.001`/`indexterm.001`/`chapter.003` −5.2%, −2.8%; XRechnung stage 1 −7.2%, −4.5%; stage 2 −2.4%, −0.3%; Peppol −5.5%, −3.5%; CEN and both compiles within noise. DocBook runs several transforms per document, with 300–950 globals each, and evaluates few of them. The prototype's −13% was against a base before V5, V7 and V13. Output differential: zero unexplained |
| V13 | `e09f1060` the runtime holds its selection by pointer, allocated with the copy that selects it (runtime copy 112 → 64 B) | DocBook items −5.5% bytes, −3.8% CPU; XRechnung stage 2 −4.4% bytes, −2% CPU; allocation counts unchanged. Allocating the selection separately added 1.8% allocations and was dropped |
| V14 | `a785e1a4` `xsl:sequence` and `xsl:copy-of` copy into an open element once, through `Builder.AppendCopyOf`; the namespace work of the old detached copy is done on the attached one. `copy-namespaces="no"` and the validating modes keep the detached copy | Copy micro case −49% CPU, −74% allocations; CEN −3.8% allocations, −6.1% bytes, −2.3% CPU; Peppol −2.2%, bytes −4.1%; DocBook −0.3%, bytes −0.7%; XRechnung unchanged (no such copies) |
| V15 | `e62d96db` the parse-only `Tree` fields (external subset, source text, line index, offsets, foreign positions) behind `Tree.source`: `Tree` 424 → 320 B; `b22d90cc` `xsl:attribute` makes no node when strip or preserve leaves it untyped (that node was a fragment per attribute) | Tree: bytes CEN −1.3%, Peppol −0.8%, XRechnung 1 −1.6%, DocBook −0.6%. Attribute: XRechnung 1 −3.8% allocations, −5.6% bytes; CEN −2.0%, −4.3%; Peppol −1.4%, −3.3%. Both: XRechnung 1 bytes −7.1%, CEN −5.5%, Peppol −4.1%. CPU within noise. A smaller first chunk was measured and rejected |
| V18 | `9ddd2bc6` a reader of known length gets read windows (encoding sniff, line-end scratch, tokeniser buffer) of its own length within 512–4,096 B instead of 4 KB each | 40 RELAX NG documents, warm parse: bytes −11.4%, CPU −5.7% (paired median), allocations and retained heap unchanged. Sizing the string arena too was measured and rejected |

Wave 2, A/B against `257ade0d` (V12) and `d5a980b6` (V19), getrusage CPU and
allocations, medians of 5–7 alternated runs:

| ID | Commit | Measured |
|---|---|---|
| V12 | `d5a980b6` the transform's context carries the common version (3.1 under a 3.0 processor) and package (`Context.WithStaticHost`); an expression's namespaces are installed only where it reads them (format-date family, `function-lookup`) | CEN −5.1% CPU, −5.5% allocations, −9.2% bytes (the upper bound); Peppol CPU flat, −2.1% allocations, −3.6% bytes; DocBook flat: its included modules carry their own static base URI, which still copies (371,546 of 663,331 evaluations per pass) |
| V19 | `babd534f` a parsed document indexes its elements by expanded name on the first `descendant::name` lookup (4 B per element, built in one pass; `internal/xdmindex`) | Parse and query each iteration: XMark q7 −7.8% CPU at 0.1, −5.9% at 0.01; q6 −2.8%; q14, q19 within noise (q19's single whole-document lookup: eval +4%). Bytes +5–7% per pass: the index is 0.67 MB (1.6% of an 11 MB document's tree), the rest the build's scratch. One document queried repeatedly: q6 −98.9%, q7 −98.2%, q14 −29.6% CPU. DocBook and the Schematron workloads unaffected: they reach `//name` 0–673 times per pass (the root-path index and keys answer the rest) |

V1 and V3 together: XMark q1–q20 CPU −8.8% at 0.1 and −7.7% at 0.01; q10
−33%, q13 −10%. The prototype's −40% q10 eval holds; its parse gains
(−5.5 to −7%) measured smaller here, with four lanes sharing the machine.

V14, V15 and V18 were measured against `257ade0d` and each other in turn
(5–7 alternated rounds); all 77 workload outputs stayed byte-identical.

V5, V7 and V13 together, against `0ff09c65` (7 alternated rounds, CPU and allocations per
pass): CEN −4.0% CPU, −1.0% allocations; Peppol −6.8%, −2.4%; XRechnung
stage 1 −11.0%, −11.7%; stage 2 −14.6%, −1.4%; DocBook `ptoc.001`,
`indexterm.001` and `chapter.003` −17.1%, −7.5% (bytes −21%); all 42 DocBook
items −18.3%, −4.5%; DocBook compile −11.0%, −4.0%; CEN compile −1.9%.

## Measured and rejected

Each idea here was prototyped and measured, so there is no need to redo it.
Reopen one only if the reason no longer holds.

| Idea | Why not |
|---|---|
| Compile XPath to closures | Interpretation (AST dispatch, name resolution) is 2–4% of Schematron CPU. A direct child-axis loop, the largest removable piece, measured −2 to −3% at `GOGC=400` and nothing at 100. The cost is allocation, not dispatch |
| Stream the principal result into the serializer | Building the result tree is ≤8–10% of allocations on Schematron. Every Schematron and XRechnung stylesheet sets `indent="yes"`, and indenting needs an element's children first, so the output could not stay byte-identical without buffering |
| Pooled argument slices for leaf built-ins; `.` in a pooled slot; `strSeq`/`boolSeq` in one allocation | Allocations −6 to −10%, CPU within noise on v1. Small objects did not turn into CPU; re-measured on v2 (V16, below) |
| `Compiled.scope` without a copy, by mutating the caller's context | −4 to −10% CPU, but `Compiled` is safe for concurrent use. V12 is the version that does not mutate |
| `Compiled.scope` with the runtime carrying the package's version and static host (v1) | Skipped 0 copies: a top-level evaluation is recognised by `StaticNamespaces == nil`, and CEN alone has 1,238 distinct namespace resolvers. V12 removed that trigger |
| Pooled argument arrays for leaf built-ins (V16, on v2) | CEN −11% allocations, −4.8% bytes; XRechnung −2.2%, DocBook −1.5% allocations; CPU −2.0%, +2.5%, −0.3%: noise. A fixed array is no alternative: the arguments escape through `Function.Call`, so it is heap-allocated anyway |
| V12 for the static base URI: no copy for an expression that reads none (DocBook) | DocBook's included modules carry their own base URI, so 56% of its evaluations still copy. Skipping those that call nothing reading it saves at most 1.3% of bytes, and needs a deny-list of every function (host ones included) that reads the base URI to keep correct |
| Compile-time function resolution (T17) | After struct keys, a lookup allocates nothing and is about 3% of CPU. A cache that stayed correct when a library changed after first use cost more allocations than it saved. The call-site cache (S1) covers it |
| Relative-path memo (`cac:A/cac:B` across assertions) | Repeats are about 2.4% of CEN; allocations −0.3%, bytes +5%, no CPU gain |
| `fuseDescendant` without a step allocation per evaluation | −0.13% allocations on CEN, CPU flat |
| `//x[p]` fusion into `descendant::x[p]` | Not equivalent: with nested `x`, a different error can win. The per-document root-path index covers the case |
| Schema-wide RELAX NG derivative memo | 3.5× faster warm only because the benchmark re-validates the same documents. No gain on unseen documents, and twice the bytes |
| Bitset NFA states in XSD 1.1 restriction checks | The checks are 6–9% of an XSD 1.1 load, so the −15–20% estimate is out of reach |
| Bigger node chunks; 1,024-node chunks | No gain; +0.28 GB peak RSS at 100 MB |
| String arena starting small | Retained heap −20–37% on small documents, but DocBook +5% CPU: the smaller live heap makes the collector run more often at the same `GOGC` (85 cycles against 77). Reverted in `629cf48` |
| String arena sized to the input (V18) | 40 small RELAX NG documents: parse bytes −52%, CPU −20%, retained heap −72%. But DocBook ran about 7% more GC cycles (29.3 → 31.3 a pass) from the smaller live heap, as with the small start above. Only the read windows were sized (`9ddd2bc6`) |
| Smaller first record chunk (2 records, V15) | Most fragments hold one node, but bytes moved −0.4% to +0.2% and allocations up to +0.6%: the next chunk comes sooner for every other tree |
| Chunked result nodes | −0.9% allocations at most, +0.4–0.7% bytes |
| A memory limit (`GOMEMLIMIT`) instead of `GOGC=200` in the CLI | A limit under the live heap costs 10–25× CPU, and the CLI cannot know the live size |
| Pausing GC around parse and compile instead of `GOGC=200` | Cold CPU summed over four workloads: 812 ms paused against 748 ms at `GOGC=200`, and pausing needs six wrapped call sites (`5fbea36`) |
| SWAR scan in the tokeniser's `text()` | No gain (round 3, and again on v2): it is already a tight table loop |
| 64 KiB file read buffer | No gain |
| A record walker for C14N and the serializer (V11 as proposed) | The accessors and the `Children`/`Attrs` iterators already inline. Walking the 10 MB document reading every name and value: recursive accessors 3.7 ms, a pre-order walker as a range-over-func iterator 5.4 ms (its yield is an indirect call per node), an exported cursor 4.1 ms. A raw chunk scan inside `xdm` takes 1.4 ms but has no enter/leave and is not reachable from another package without handing out records. The C14N gap to v1 was the per-token `bufio.Writer` calls instead |
| Interning `nodeTyping` per tree | Bytes −27%, wall +25%: hashing the key on every typing write costs more than the allocation it saves |
| Typing chunks under 32 KB | No gain. The `madvise` time in the typed path is macOS heap growth, not chunk size |
| Counting child elements lazily; testing `particleAcceptsEmpty` after the cheap checks (XSD) | Within noise |
| Keyed attribute sort in C14N | No gain |

## Correctness

One is open (below). Every other bug found while profiling has been fixed
and is listed in [CHANGELOG.md](../CHANGELOG.md). The v2 profile found no new bug: XSD and
RELAX NG verdicts and messages are identical to v1 (11/11 catalogs, 589/589
RELAX NG documents), and every prototype kept its outputs byte-identical.

One deliberate difference remains: inside `xsl:merge-action`, go-xml clears
the current template rule as XSLT 3.0 §6.8 requires, while Saxon 12.10 still
runs the next rule.

**Open:** a function item that escapes a transform and is then called from
several goroutines is not race-safe when it reaches per-transform runtime
state other than global variables, such as the `key()` index (`keyIndex`):
building an index entry writes a map another goroutine may read. Lazy global
variables are safe in that situation (V4). This race predates v2's fix round.

## Method notes

- **Harnesses** copy the benchmark's warm loop: compile once, then
  parse → transform/query/validate → serialise to `io.Discard`. They use the
  same parse options, resolvers and parameters as the benchmark items. The
  `benchrun -helper` used for parse and C14N runs as the CLI does (`GOGC=200`,
  streamed parse). The warm loops parse with `ParseString` at the harness's own
  `GOGC`.
- **Allocation counts are exact** and are the most reliable figure. CPU comes
  from getrusage. Wall times are noisy when lanes run together, so treat
  differences under about 3% as noise.
- **A/B runs alternate** the two builds, back to back, with medians of several
  runs. A prototype is built on a copy of the tree (`git archive` or a
  worktree), never in the checkout. It is checked by diffing every workload
  output against the unmodified build. Run the affected unit tests and the full
  conformance gate before landing.
- **Retained heap** is measured after `runtime.GC` with the input dropped. A
  cheaper measurement that keeps the input under-counts by about the input
  size.
- **macOS profiles under-count.** They catch about 80% of the CPU that
  getrusage reports, and put up to half their samples on scheduler waits
  (`pthread_cond_wait`, `kevent`). On v2 they also put 25–40% of XSLT samples
  on `EvalSymlinks` system calls, which an A/B showed cost no warm time. Use
  getrusage for totals, and Linux profiles for attribution.

### Linux

Measured in a 4-CPU Docker Desktop VM on the same M3 Pro, alternating with
macOS, with both binaries from one compiler:

- `runtime.madvise` is 0–1.6% of samples on Linux, against 13–26% on macOS for
  parse, XMark and XSD. On macOS, Go's darwin runtime calls
  `madvise(MADV_FREE_REUSE)` every time it reuses a heap page.
- Cold CLI runs use 11–13% less CPU on Linux.
- Warm in-process loops use 2–20% more CPU on Linux, because GC marking takes
  the share that `madvise` takes on macOS.
- The VM adds a layer of address translation, so read the Linux figures as
  an upper bound.
