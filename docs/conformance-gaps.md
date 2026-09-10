# W3C conformance: the remaining gaps

This file records **what is still failing**, and nothing else. An entry that is
no longer a gap does not belong here whatever marker it once carried: if it is
closed, it is not a gap. Three kinds of entry survive, and the file is
organised as those three sections.

1. **Open gaps** — what is failing now, per suite, read case by case.
2. **Deliberate divergences** — decisions not to conform, each with its spec
   citation and its **measured** cost. These are not gaps, and they are kept
   because without the number someone re-attempts a bad trade.
3. **Corrections** — places where a verdict recorded in this file was *wrong*.
   Not "this is fixed", but "this file said X and X was false". Kept because
   they stop the next reader re-deriving a bad conclusion.

Anything that fit none of the three has been deleted, in particular entries
whose only content was that something had been fixed.

Every figure here comes from a full run of the suite it names, with
`tests/check.sh`.

| Component | Suite | In scope | Passing | Now | Failing |
|---|---|---:|---:|---|---:|
| **xdm** | *(no external suite)* | — | — | — | — |
| **xpath** | QT3 — XPath 2.0 | 15,217 | 15,217 | 100.00% | **0** |
| **xpath** | QT3 — XPath 3.0 | 19,362 | 19,362 | 100.00% | **0** |
| **xpath** | QT3 — XPath 3.1 | 21,898 | 21,898 | 100.00% | **0** |
| **xquery** | QT3 — XQuery 3.1 | 30,346 | 30,320 | 99.91% | **26** |
| **xslt** | W3C XSLT 2.0 | 6,201 | 6,193 | 99.87% | **8** |
| **xslt** | W3C XSLT 3.0 | 11,518 | 11,456 | 99.46% | **62** |
| **xsd** | W3C xsdtests 1.0 | 39,388 | 39,358 | 99.92% | **30** |
| **xsd** | W3C xsdtests 1.1 | 41,576 | 41,545 | 99.93% | **31** |
| **relaxng** | Clark spectest | 965 | 965 | 100.00% | **0** |
| **xslt** | DocBook xslTNG *(real-world)* | 577 | 577 | 100.00% | 0 |
| **xslt** | XSpec *(real-world)* | 225 | 225 | 100.00% | 0 |
| | **Total** | | | | **178** |

The unit-test suite is 1,829 tests.

The last two rows are not W3C suites but real-world corpora — DocBook xslTNG's
577 test documents and XSpec's 225 — kept here because they are the only
measurement in this file taken against stylesheets nobody wrote for a test
harness, and because four defects the W3C suites missed were found by them.
They are not in the *Total*, which counts W3C disagreements only. Note that
they are unrelated to the `docbook-001` case read below, which belongs to the
W3C XSLT sets.

Two suites reach 100% — XPath at all three versions, and RELAX NG.

**The two largest blocks are single features, not a long tail.** 44 of the 66
XSLT 3.0 failures want an `XTSE3430` that only the unwritten remainder of the
§19.8 posture-and-sweep analysis can emit — and §19.1 says a non-streaming
processor "is not required to assess whether constructs are guaranteed-streamable" —
while 36 of the 42 XQuery failures sit in `prod-CastExpr.schema` and are the
schema-aware features [todo.md](todo.md) §1.5 deliberately leaves. Neither is a
backlog of defects.

## How to read the verdicts

**Implementable** — a defect in this engine. The spec says one thing, we do
another, and the fix is ours.

**Not implementable** — passing would require violating the specification,
reaching the network, shipping a vendor extension, contradicting a second test
in the same suite, or encoding a snapshot of Unicode that is no longer current.
These are not deferred work.

**Open question** — the correct answer is not settled by the specification text
available here. Recorded as such rather than assigned to whichever bucket
flatters the numbers.

**Costs more than it gains** — implementable, and measured to lose more cases
than it wins. A real reason to leave a case alone, and a *different claim* from
impossibility. It must never be filed as "not implementable".

**Architecture debt** — implementable, with a known design change and a stated
cost, not attempted at the current blast radius.

**Won't fix** — implementable and spec-mandated, deliberately not done because
conforming would break correct real-world stylesheets.

**Out of scope** — the suite itself declares the case inapplicable through a
dependency the harness does not read. These leave the denominator; they are not
failures at all.

---

# 1. Open gaps

## xdm — the data model

`xdm` has no external conformance suite: XDM is a data model, not a language
with a test corpus. It is measured **indirectly and continuously** — every one
of the 84,000-odd cases above builds, navigates and atomises XDM instances, so
a defect in the model surfaces as a failure in XPath, XSLT or XSD rather than
in a suite of its own. It carries 18 unit-test files of its own covering the
parts the language suites exercise thinly: type annotation, attribute-value
normalisation, character-set handling, node identity and document order.

**No known gaps.**

## xpath — 0 failures

All three XPath versions agree with the suite on every case in scope.
**XPath: 15,217 / 19,362 / 21,898, all at 100.00%.**

## relaxng — 0 failures

965 of 965 assertions in James Clark's spectest. **No known gaps.**

## xquery — 26 failures

**XQuery 3.1: 30,320 / 30,346 = 99.91%.**

The failures cluster by production, not by symptom.
**`prod-CastExpr.schema` (36) is all but the whole of it**, with six singletons
behind it: `prod-TypeswitchExpr`, `prod-FunctionCall`, `prod-ContextItemDecl`,
`op-same-key`, `misc-CombinedErrorCodes` and `app-Demos`, one case each.

The 36 are not one gap. They are separate small features that `import schema`
made *reachable* without making them present — typed *input* documents,
annotation propagation through a constructor, and substitution groups over
validated content — and they are catalogued in [todo.md](todo.md) §1.5. The
largest single shape is that a cast to a **list** type must yield items
annotated with the list's item type, which is annotation propagation reached
from the cast side.

The six singletons are read here.

| Case | Verdict | Why |
|---|---|---|
| `app-Demos/RexParser` | **Not implementable here** | A large real-world query rather than a targeted case. The sibling `sudoku` was fixed by making a FLWOR in a conditional branch belong to that branch; this one still fails in the same family. Its symptom has been misread before: see *Corrections*, `RexParser`'s offset. |
| `op-same-key/same-key-023` | **Not implementable here** | 421,875 keys through O(n) `map:put` and `map:remove`. Measured rather than estimated: per-key cost scales linearly with map size (66µs at n=1,000 to 1.28ms at n=40,000), so the whole case extrapolates to 1.5–2 hours — four to five orders of magnitude from the deadline, which no constant-factor work reaches. A persistent map would fix it, but `MapItem` has 58 references across 15 files and its entry order is load-bearing for serialization stability, which a HAMT does not preserve. `same-key-024` covers the same semantics at 11,250 keys and passes. |
| `prod-TypeswitchExpr/K2-sequenceExprTypeswitch-5` | **Not implementable without a parser change** | Wants a static `XPST0008` for a variable named in an unreached `typeswitch` branch. A check restricted to sibling-clause variables was built and passed eleven tests, then broke `K2-ForExprWithout-8`, where a `default $d return ()` sits inside a `for` clause binding `$d`: a sibling's name may be shadowed by an outer binding, so seeing it free proves nothing. The counts stayed net-neutral, and only the case-list diff caught it. A sound check needs the parser to track in-scope variables, which it does not do today. |
| `prod-FunctionCall/FunctionCall-051` | **Open question** | Subtype comparison reads a static table of built-in spellings with no handle on an imported schema, so the answer is not reasoned. Its converse `FunctionCall-052` **passes by accident** for the same reason — see *Corrections*. |
| `prod-ContextItemDecl/contextDecl-*` | **Not diagnosed individually** | Wants `XQST0113` or `XPTY0004` on a context-item declaration. |
| `misc-CombinedErrorCodes` | **Not diagnosed individually** | One case. |

## xslt 2.0 — 8 failures

All eight are deliberate divergences, verified not ours. They are read in
§2 rather than here, because none of them is work: `format-number-070`,
`docbook-001`, `sequence-0132`, `import-schema-137`, `validation-0201` and the
three `regex-syntax-xslt20` cases.

**XSLT 2.0: 6,193 / 6,201 = 99.87%.**

## xslt 3.0 — 66 failures

**XSLT 3.0: 11,456 / 11,518 = 99.46%.**

**44 of the 66 want an `XTSE3430`** — a refusal of a stylesheet as
non-streamable, which only the §19.8 posture-and-sweep analysis can emit. Most
read literally "expected error XTSE3430, the transform succeeded": the engine
computes the right answer and the test wants it to decline. §19.1 settles
whether that is owed: a processor that does not stream "is not required to
assess whether constructs are guaranteed-streamable". These are the largest
block in this file and they are not defects. What the analysis covers and what
it does not is under *The §19.8 streamability analysis* below.

The remaining 23 divide as follows. Several are divergences and are read in §2;
what is genuinely open is read here.

### Package composition — 4

| Cases | Verdict | Why |
|---|---|---|
| `package-021err` | **Not implementable** | A half-applied 2020 erratum (E36). The defect is confined to the *used* package, which writes `<xsl:function name="me:function1#0">` where `@name` is an `eqname` — so the function has no well-formed name, nothing matches, and we raise XTSE3030 rather than the wanted XTSE3050. Passing would mean tolerating a malformed `@name`, which is a suite workaround, not a conformance fix. The suite has repaired this elsewhere: `accept-916` carries `change="Remove unintended error, missing arity on function name"`. §3.6.2 does admit an arity in `@names` — "`p:local#2`" is its own example — and we already parse `#N` correctly; see *Corrections*. |
| `package-022err` | **Not implementable** | `component="function#0"` genuinely violates the `@component` enumeration `"template" \| "function" \| "attribute-set" \| "variable" \| "mode"`. Same erratum, applied to a different attribute in each file. |
| `accept-913` | **Open question — our error is wrong either way** | §3.6.3.2 says a component matched by no `xsl:accept` keeps its visibility and only a *private* one becomes hidden, so the initial template stays public and the wanted XTDE0040 is unreachable. But we raise **XTDE3052**, which §3.6.3.2 scopes by its own parenthetical to "an abstract component accepted into a using package with `visibility="absent"`" — and `accept-913` has no `xsl:accept` at all, so nothing is absent. The defensible code is **XTSE3080** (§3.7: "It is a static error if a top-level package … contains symbolic references referring to components whose visibility is `abstract`"), because an unmatched *abstract* component stays abstract and the public initial template references it via `xsl:use-attribute-sets`. The sibling `accept-914` wants exactly XTSE3080 for the neighbouring shape. What blocks a change is a genuine tension: `accept-902`/`-910` present nearly the same structure and want the dynamic XTDE3052, and `xslt/usepackage.go` separates them today only by whether an `xsl:accept` named the component. Separating "referenced from the top level" from "merely inherited and invoked" is a reference-graph question and is unmeasured. |
| `package-200` | **Costs more than it gains** | Read in §2 — a rule separating it from `use-package-291`–`294` exists and rests on quoting, which neither §3.6.1 grammar mentions. |

### Schema-aware validation — 4

| Cases | Verdict | Why |
|---|---|---|
| `si-copy-117`, `si-copy-of-117` | **Not implementable** | Not ordering cases at all. Both write `<xsl:copy select="/*/*/@version" type="xs:date"/>` — a `type` attribute and **no `validation` attribute**. §19.2 keys the codes to which attribute was written: XTTE1510 begins "If the **validation attribute** … has the effective value `strict`", which is literally unmet, while XTTE1540 is "if an **[xsl:]type attribute** is defined … and the outcome of schema validity assessment against that type is … other than valid", which is exactly met. The suite's own description says "validate attribute **by type**". Our XTTE1540 is correct. |
| `validation-0006` | **Not implementable** | A parentless attribute: `XTTE1555` wanted, `XTTE1540` reported. XTTE1555 is scoped by its own text to "when validating a **document node**", and a parentless attribute is not one; XTTE1540, which covers the `type` attribute, is what the case actually meets. The stylesheet says so itself: "a contrived example to force **Saxon** down a particular code path". |
| `import-schema-137` | **Not implementable** | The one genuine ordering case; read in §2, since §2.9 declines to settle it. |

### Long tail

| Case | Verdict | Note |
|---|---|---|
| `accumulator-038` | **Not implementable** | Suite defect. Its stylesheet is an *explicit* `xsl:package`, so §3.6.3.1's "Otherwise, private" applies to the unannotated `main` template and XTDE0040's own text — "does not match the expanded QName of a named template defined in the stylesheet, **whose visibility is public or final**" — is met. Both 038 and 039 were converted to `xsl:package` by Bug 28410 in 2015; only 039 carries `<modified by="Michael Kay" on="2019-03-05" change="Make main template public"/>` and only 039's stylesheet has `visibility="public"`. A second, independent defence: the wanted XPTY0004 is reachable only *after* entry succeeds, and §2.9 lets an implementation report whichever error it detects first. **Re-tested against a hypothesis that failed.** The idea tried was that §3.6.1 — "Unnamed packages … cannot be the target of an `xsl:use-package` declaration" — leaves an unnamed package with no using package for anything to be private *from*. Gating `eligibleInitialTemplate` on a *named* package made this case pass and took the suite from 11,348 to **11,347**: `package-001a` is the identical construct and its description reads "initial template must be public", expecting XTDE0040. `package-001b` and `package-914a` are the same shape. The suite therefore applies the visibility default to unnamed packages deliberately. |
| `strip-space-009` | **Not implementable** | Asserts that whitespace survives `xsl:strip-space` under an element whose **ancestor**'s type carries an XSD 1.1 assertion. §4.4 grants no such exemption: it preserves whitespace only where "an element … has a type annotation that is a simple type or a complex type with simple content", and here `p` sits under `xs:any processContents="skip"`, so it has no simple-type annotation at all, while the ancestor's type is `mixed`, not simple content. We implement the §4.4 rule as written. The test's own comment says it exists "in order to exercise different paths in **Saxon**"; Saxon is the only submission that runs it, and passes. See the caveat on the spec edition below. |
| `transform-004` | **Architecture debt** | The case calls `fn:transform` from a `static="yes"` variable, so it must run during the *static phase of compilation*. Registering the real function there is a two-line change and is correct by §9.7, which gives a static expression the whole F&O library and excludes nothing. It deadlocks. `Compile` keeps `compileSchema`, `compilePackage`, `overridingDecls`, `packageParent`, `overrideXPathVersion` and `compileMaxVersion` as **package-level variables** guarded by a single non-reentrant `compileMu`, so a nested `Compile` — which is exactly what `fn:transform` must do — blocks forever on a mutex the outer call still holds. Verified by stack trace, not inferred. Making this work means moving that state onto the `compiler` value. |
| `accumulator-061` | **Costs more than it gains** | Read in §2. |
| `evaluate-045` | **Won't fix** | Read in §2. |
| `evaluate-046` | **Undiagnosed** | Fails with `XTDE3400: accumulator static-vars is defined circularly` where the case expects the transform to succeed. Not read. |
| `evaluate-048` | **Needs a network fetch** | Fails on `FODC0002: cannot retrieve "https://www.saxonica.com/welcome/welcome.xml": scheme "https" is not permitted`. Not reachable regardless. Its earlier half — `fn:function-lookup`'s dynamic visibility — was a separate reason; see *Corrections*. |
| `docbook-001` | **Not implementable** | Read in §2 — a vendor extension. |
| `merge-097`, `-097s`, `-097sf` | **Not implementable** | They call `uri-collection('.?select=merge-097-*.xml')`. The `?select=` query string is a Saxon extension, not something F&O defines, and the test set says so in a comment beside the cases: they "rely on Saxon-format collection URIs … and [are] therefore not interoperable". None of the three declares an `<environment>` or a `<collection>`, so there is nothing for the harness to honour — the harness supplies a collection resolver only where the environment declares one, precisely so that `fn:collection` keeps refusing everywhere else. The resulting `FODC0002: collections are not configured` is the engine failing closed by design: a collection URI that can name a directory is a file-disclosure vector, and returning an empty sequence instead would make "collections are switched off" indistinguishable from "the collection was empty". |
| `validation-0201` | **Implementation-defined** | Read in §2, and see *Corrections* — its recorded cause was wrong five times over. |

### The §19.8 streamability analysis

The §19 streamability rules are a type-inference pass: every construct carries
a **posture** (where the nodes it returns sit relative to the streamed input —
`grounded`, `striding`, `climbing`, `crawling`, `roaming`) and a **sweep** (how
far evaluation moves the input position — `motionless`, `consuming`,
`free-ranging`), each computed from its operands and from the **operand usage**
that says how the construct uses them (`absorption`, `inspection`,
`transmission`, `navigation`). A construct is guaranteed-streamable exactly
when it is not free-ranging; `XTSE3430` is the refusal of one that is.

**What exists.** The lattice — the part every construct shares — is complete
and tested on its own, in `xslt/streamlattice.go` and
`xslt/streamlattice_test.go`:

| §19 section | Status |
|---|---|
| 19.3 combined posture of a choice operand group | complete |
| 19.4 operand roles, all four usages | complete |
| 19.5 posture, 19.7 sweep | complete |
| 19.6 context posture | the `xsl:stream` / streamable `xsl:source-document` clauses only |
| 19.8.1 general streamability rules | complete, including the absorption downgrade, the choice-group and same-posture escapes, the higher-order rule and the singleton-builtin rule |
| 19.8.8.1 `for` expressions | complete; the "S must be grounded" rule is carried by the navigation usage §19.8.1 gives S |
| 19.8.8.2 quantified expressions | complete |
| 19.8.8.3 `if` expressions | complete |
| 19.8.8.7 path expressions | complete, both phases, including the scanning-expression reassessment that makes `//x` streamable |
| 19.8.8.8 axis steps | the posture table and the predicate rule; not the numeric-predicate narrowing |
| 19.8.8.9 filter expressions | the motionless-predicate clause; not the numeric-predicate narrowing |
| 19.8.8.11 variable references | the grounded case (correct wherever no streamable stylesheet function is declared) |
| 19.8.8.12 context item expression | complete |
| 19.8.9 built-in function operand usages | the proforma table, ~150 signatures |

Every rule the lattice implements is tested against the worked examples §19.8.2
gives, cited case by case, so a reader can check the tests against the spec
rather than against the code they test.

**What does not exist.** Of the 86 rule sections in §19, roughly 13 are covered
— call it 15%, and the covered ones are deliberately the shared ones rather
than the numerous ones. Absent entirely:

- **All 43 XSLT instruction rules of §19.8.6.** `xsl:for-each`, `xsl:iterate`,
  `xsl:for-each-group`, `xsl:fork`, `xsl:merge`, `xsl:apply-templates` and the
  rest each have their own operand roles and their own context-posture
  contribution.
- **Streamable stylesheet functions (§19.8.5) and accumulators (§19.8.4).**
  These are what the `su-*` test families exercise, and they are the largest
  single block of the remaining cases.
- **§19.8.8.10 dynamic calls, §19.8.8.14 inline functions, and `let`
  expressions.** §19.8.8.4 union/intersect/except, §19.8.8.1–2 `for` and
  quantified expressions, §19.8.8.6 simple mapping (`!`) and §19.8.8.15/16 map
  expressions are implemented.
- **The 18 per-function sections of §19.8.9** that do not follow the general
  rules: `fn:current`, `fn:last`, `fn:position`, `fn:root`, `fn:reverse`,
  `fn:innermost`, `fn:outermost`, `fn:fold-right`, `fn:function-lookup`, the
  accumulator pair and the merge pair.
- **Static type inference.** §19.2's U-types are approximated syntactically.
  Where the approximation is uncertain it answers in the direction that widens
  the sweep, which loses precision and never gains a false rejection.

**Why a partial analysis is safe.** A missing rule and a wrong rule fail in
opposite directions, and only one of them is tolerable. A missing `XTSE3430`
leaves a test failing; a spurious one rejects a valid stylesheet at compile
time, where the user has no way around it. The analysis therefore reports
whether it *modelled* every construct it met, separately from what it
concluded, and `xslt/streamcheck.go` raises `XTSE3430` only when the answer was
derived entirely from modelled constructs. An unmodelled construct — a union
expression, a `for` expression, a map constructor, a stylesheet function with a
streamability category — is "no opinion", not a failure. The check is further
confined to `xsl:sequence`, `xsl:copy-of` and `xsl:value-of` whose select is
evaluated with the streamable container's own focus, and it refuses to descend
past any instruction that changes the focus, because assessing an inner
expression against the wrong context posture is exactly how a spurious error
would arise.

**Measured.** Against the `strm` cases that name a stylesheet, checked directly
against the catalog's own expectations: **0 cases that do not want `XTSE3430`
now get it**, and the whole 456-stylesheet streaming corpus compiles with no
spurious refusal. XSLT 2.0 is unmoved, which the structure guarantees as well
as the measurement — no XSLT 2.0 stylesheet in the suite uses a streamable
container at all.

**What completing it would take.** The remaining work is wide rather than
deep: the lattice is the part that had to be right, and the per-construct rules
are mostly mechanical transcription against it. The 43 instruction rules are
the bulk, and `xsl:for-each-group`, `xsl:iterate`, `xsl:fork` and `xsl:merge`
are the four that are genuinely intricate, because each defines its own
context-posture contribution rather than deferring to the general rules.
Streamable stylesheet functions need the streaming-parameter posture table of
§19.8.8.11 and a per-function streamability category, and would unlock the
`su-*` families. A realistic estimate for the remainder is several times the
work already done, and it should be taken construct family by construct family,
each with the negative arm the existing tests establish as the pattern.

Streamed *execution* — an incremental parser and pull evaluator — is a separate
and much larger project, and it would buy almost no conformance, because the
cases it would serve already pass. §19.1 lets a processor answer a request for
streamed evaluation by building the tree instead, and this engine does exactly
that: `xsl:source-document`, `xsl:merge`, `xsl:fork`, `xsl:accumulator` and the
streamable forms are all implemented; what is absent is streaming, not the
vocabulary. Flipping the feature flag on without §19.8 would claim streaming
while never refusing a non-streamable stylesheet, which is why the flag stays
off.

## xsd — 61 disagreements

**XSD 1.0 — 39,358 / 39,388 = 99.92%. XSD 1.1 — 41,545 / 41,576 = 99.93%.**

The XSD suite measures **agreement with the expected verdict** on each schema
and instance, which is a different shape from a pass/fail case count. A
disagreement is one of four kinds:

- **SFALSEACCEPT** — we accept a schema the suite says is invalid
- **SFALSEREJECT** — we reject a schema the suite says is valid
- **IFALSEACCEPT** — we accept an instance the suite says is invalid
- **IFALSEREJECT** — we reject an instance the suite says is valid

Each test also carries a W3C **status**. `accepted` means the expected result
is settled. **`queried` means the W3C has itself challenged the expectation**,
usually with a bugzilla reference. `stable bugNNNN` does **not** mean the same
thing — it means the WG looked at that bug and settled the expectation, so it
is the opposite of challenged.

| | Total | `accepted` | `queried` | `stable` | no status |
|---|---:|---:|---:|---:|---:|
| XSD 1.0 | 30 | **2** | 26 | 2 | 0 |
| XSD 1.1 | 31 | **2** | 27 | 2 | 0 |

Those totals are counted from the `<current>` status of each disagreeing case.

| Set | Cases | Status | Why |
|---|---:|---|---|
| `MS-Regex2006-07-15` | 22 per version, 44 in all | `queried bug4113` | Every single MS-Regex disagreement is the *same* open W3C bug. The expected results are challenged upstream; agreeing with them would mean agreeing with something the working group does not stand behind. |
| `MS-Element`, `MS-DataTypes`, `MS-IdentityConstraint`, `MS-Particles`, others | 11 (1.0), 12 (1.1) | `queried`/`stable` + bug | Assorted challenged expectations, almost all across the Microsoft-contributed sets. |

The `queried` defence was spot-checked on four cases and **held** in each —
`ste110` (bug 4957, circular unions), `gMonth002`/`004` (bug 6901, withdrawn
gMonth lexical forms), `anyURI_a004_1339.i` (bug 4126, whose own annotation
sides with us) and the `MS-Regex` cases, 22 on each version (bug 4113). In
every one we disagree in the direction the filed bug points, which is what
makes the status a defence rather than a label.

Of the 30 on 1.0, 28 carry a `queried` or `stable` bugzilla reference and 2
carry `accepted` — `attP031` and `particlesZ001`. Of the 31 on 1.1, 29 are
`queried` or `stable` and 2 are `accepted`, here `simple093` and
`particlesZ033_g`. Those four are read individually.

| Case | Version | Verdict |
|---|---|---|
| `MS-Attribute2006-07-15/attP031` | 1.0 | **Not implementable — suite defect.** It names its instance test `.i`, says in its own prose that the attribute *does* appear, and still expects valid; its sibling `attP029`, byte-identical but for the instance, is consistent. |
| `particlesZ001` | 1.0 | **Not implementable — suite defect.** It never propagated its instanceTest's version split to its schemaTest. |
| `simple093` | 1.1 | **Not implementable — the suite contradicts itself.** Expected invalid; the schema unions `xs:QName` with `xs:NOTATION`, and Part 2 §3.2.19 does forbid NOTATION being "used directly in a schema", so the case is a correct reading. But `msData particlesZ007` declares a schema containing `<xsd:union memberTypes="xsd:NOTATION"/>` **valid**, and both carry `status="accepted"`. The rule was implemented and measured: 1.1 trades one for the other (agree 41,519 → 41,518) and 1.0 loses two outright (39,347 → 39,345), because particlesZ007 has a dependent instance test and simple093 is not run under 1.0 at all. Reverted; `xsd/facet_check.go` enforces §3.2.19 in the three places the suite is consistent about. |
| `particlesZ033_g` | 1.1 | **Costs more than it gains** — read in §2, with its measured cost. |

Four XSD 1.0 `SFALSEREJECT` cases in the `queried`/`stable` remainder are a
suite omission with a measured cost of their own, and are read in §2: the
`notQName` cases.

---

# 2. Deliberate divergences

These are not gaps. Each is a decision not to conform, with the spec citation
that says what conforming would mean and the **measured** cost of doing it. The
numbers are the point: without them, someone re-attempts a bad trade.

## `xsl:fork` — the Last Call draft and the test set disagree

**Deliberate divergence from a normative draft, decided by the tests.**
§19.8.4.20 of the Last Call draft rejects an `xsl:fork` any of whose branches
is not grounded. The test set does not agree with its own spec: `si-fork-A`'s
`f-006` pairs a grounded branch with a striding one and must run, while the
example the spec itself gives — two `xsl:copy-of` branches — must be
streamable with both branches consuming.

No reading of "reject a non-grounded branch" admits both. The implemented rule
counts *non-grounded* branches and rejects more than one, which admits all
three. The cost of following the draft literally is `f-006` plus the spec's own
example; the cost of the implemented rule is nothing measured.

This is recorded because it is the shape of thing a later reader corrects back
to the letter of the draft. It is a divergence taken knowingly, not an
oversight — see the rule and its reasoning in `xslt/streaminstructions.go`.

## The `||` operator — 2 cases gained, 18 lost

**Measured and reverted.** §19.8.8's proforma operand-usage table lists
`StringConcatExpr [19]` as `A || A`: both operands absorb, the same entry it
gives `AdditiveExpr`. Adding that one case to the expression dispatch in
`xslt/streamability.go` is a faithful transcription, and it does what it should
— `si-fork-952`, whose `current-group()/(AUTHOR||TITLE)` is two down-selections
in one step, is refused, and so is `si-fork-902`.

It also **loses 18 cases**, measured: 11,451/67 → 11,435/83. `||` was the last
unmodelled construct standing between the analysis and
`xslt/streamexprs.go`'s §19.8.8.4 widening, which turns a union of two
*striding* operands into crawling. The spec admits that widening is a choice
rather than a necessity ("there are cases where an implementation could
determine that the result is also striding: for example `(author | editor)`").
`sx-union-C.xsl` hosts 18 cases that all assert output, one of which writes
`.+1 || ' '` inside a `for-each` over `PRICE union QUANTITY`; the widened
posture makes the body roaming and the whole stylesheet is refused at compile
time, taking all 18 with it. That stylesheet's own comment concedes the point —
"Streamable in Saxon but perhaps not in the W3C spec".

Withholding the verdict on the striding+striding widening rescues those 18 and
costs `sx-union-202`, which wants `XTSE3430` for `(/BOOKLIST/ITEM |
/BOOKLIST/MAGAZINE)/PRICE` and is annotated "The union of two striding
expressions is crawling". Both stylesheets union two striding operands, so no
rule reading the union alone separates them. The distinction that would is
whether the widened posture feeds a following *step* — real in both cases, but
it means threading "this posture was widened" through the two-phase path fold
of §19.8.8.7, which is a change to shared analysis plumbing rather than a
transcription.

So `||` is correct, currently unmodelled, and cannot be landed on its own. It
should go in together with the U-type inference that would let §19.8.8.4 keep
`(author | editor)` striding — the same missing inference already recorded
against `si-group-055` in `bodyCallsCurrentGroup`.

## `evaluate-045` — 1 case gained, 510 real documents lost

**Won't fix.** It asserts that a stylesheet function with no `visibility`
attribute is private, and so unreachable from `xsl:evaluate`. **The suite is
right.** §3.6 says verbatim: "When the `xsl:package` element is not used
explicitly, **the entire stylesheet comprises a single implicit package**."
§3.6.3.1's ladder ends "Otherwise, private", with no carve-out, and
`xsl:evaluate`'s static context admits user-defined functions only "provided
their visibility is not hidden or private". So XTDE3160 is correct and we
diverge knowingly.

The reason to diverge is real: enforcing it means no stylesheet outside a
package can call its own functions from its own `xsl:evaluate`, which breaks
deployed stylesheets, and Saxon diverges the same way (its XSLT 3.0 submission
records no result for the case at all, while its sibling `evaluate-006` — the
same stylesheet with `visibility="public"` written on the declaration — passes).

**Cost, measured:** removing the `isPackage` guard from `evaluateMayCall` and
re-running the DocBook xslTNG corpus takes it from **577 documents to 67**.
Conforming here would break 510 real documents to gain one suite case.

## `particlesZ033_g` — 1 case gained, 17 valid schemas lost

**Costs more than it gains.** Expected invalid; the test's own note says "validates
as xs:any if maxOccurs greater than 4096", which describes a 2006 vendor
behaviour rather than a rule. No threshold can satisfy the family: sibling
`particlesZ033_a` carries `maxOccurs="79228162514264337593543950335"` — 7.9×10²⁸,
far larger than `_g`'s 45,678,363 — and is expected **valid**. Where the WG did
adjudicate implementation limits, in `elemZ031`, it resolved the expectation to
`indeterminate` rather than invalid (bug 4059).

Enumerating every state of every content model in `_g` shows that under 1.1 it
has *no* competing pair at all. The only pair is `ref='m1'` against
`<xsd:any/>`, and `XSD1_1TestCategories.xml` names that relaxation outright:
"Relaxation of UPA: wildcard/element competition no longer violates UPA".

**Cost, measured:** restoring the competition under 1.1 gains this one case and
costs **seventeen** valid schemas — `addB153`, `all006`,
`wild030/047/049/050/052/072/073`, `s3_3_6v01`, `s3_3_6v04`, `s3_8_6v01`,
`s3_8_6ii01`, `s3_4_6v01`, `s3_4_6v04`, `s3_10_1v04` and `ste110` — taking 1.1
from 41,536 to 41,494. The group carries no `version` attribute and cites the
2004 1.0 REC, so its bare `invalid` is a 1.0 verdict the 1.1 run inherits.

## The `notQName` cases — 7 disagreements gained, 150 agreements lost

**Costs more than it gains.** Four XSD 1.0 `SFALSEREJECT` cases —
`wildcard/s3_10_1ii08`, `s3_10_1ii09`, `anyAttribute/s3_10_6ii01` and
`s3_10_6ii02` — fail with "notQName requires XSD 1.1", and they are right to.
`notQName` is **unprefixed** in all four schemas, so XSD 1.0's rule about
ignoring attributes from other namespaces does not reach it: 1.0 declares
`<xs:anyAttribute namespace="##other" processContents="lax"/>` on `xs:any`,
which admits only *qualified* foreign attributes. `##definedSibling` has no XSD
1.0 meaning at all. Rejecting these schemas under 1.0 is correct.

The cause is a missing attribute in the suite. Every one of these groups carries
a `documentationReference` into the 1.1 wildcard section and a reference into
`XSD1_1TestCategories.xml`, which enumerates only 1.1 features — but no
`version="1.1"`, so the runner scores them under 1.0 as well. In
`ibmMeta/wildcard.testSet` alone, **8 of the 17 groups** referencing that file
omit it, while `s3_10_1v01`, testing the same feature in the same file, has it.

**Cost, measured:** teaching the harness to read a `XSD1_1TestCategories.xml#`
reference as an implicit `version="1.1"` takes 1.0 disagreements 63 → 56, but
agreements fall **39,341 → 39,191**, because those same groups contribute many
currently-agreeing 1.0 results. It is the right reading of the suite and still
a net loss of 150.

## `accumulator-061` — 1 case gained, 2,020 cases risked

**Costs more than it gains.** The blocker is not what this file once recorded
(see *Corrections*): §10.3.6 focus capture is already implemented and cites the
clause. What is missing is that nothing marks the principal input as streamed.

**Cost:** supplying that would put the 2,020 cases in `tests/strm/` at risk to
win one. It stays a divergence rather than work.

## `package-200` — 1 case, deliberately

**Costs more than it gains.** Re-read against §3.6.1's two grammars and all four
siblings. `package-version="'1.0.0'"` is a well-formed `PackageVersion` wrapped
in apostrophes; `use-package-291`–`294` write `2.0.0-alpha:beta`,
`TotallyInvalid`, `-3.6` and `-alpha`, none of which parse under any reading.
"Strip a matched pair of quotes and it parses" therefore does separate them —
but that is a rule about *quoting*, and neither grammar mentions quotes.

Two facts settle it. `package-version="'…'"` occurs **exactly once in the whole
suite**, in `package-201.xsl`, so the rule would have precisely one instance and
no second case to confirm it against — the signature of a special case, not a
rule. And the genuine XTSE3000 shape is already implemented and passing:
`error-3000a` writes the perfectly well-formed range `2.0.0` and expects
XTSE3000 because no package matches it, which is exactly what §3.6.1 says the
error means. `package-201.xsl`'s own comment agrees, calling for "a
package-not-found error". Reporting XTSE0020 for a value that is not a range at
all is the defensible reading. **Cost: 1 case.**

## `validation-0201` — implementation-defined, on both targets

Whitespace placement and nothing else: strip all whitespace from both sides and
the output is byte-identical to the expected file. All three dates read "29 May
1917", "12 September 1953", "22 November 1963".

**It is not the indent width**, which five places in three documents said it was.
Three-space indentation was measured across the whole lane and gained nothing —
6,193/8 either way, the reported offset merely moving from 46 to 134. See
*Corrections*.

The surviving difference is a newline we write before `<style>` inside `<head>`
and Saxon does not, because Saxon declines to indent before an element whose own
content is significant text. Ours is the parent's decision —
`hasTextChild(<head>)` is false, so `indentChildren` stays on — and it is
permitted: **Serialization 3.1 §5** licenses added whitespace "only where the
effect is not significant", constraining where a serialiser *may not* indent
rather than where it must, while **XSLT 2.0 §20** makes "the amount of
indentation to be used when `indent="yes"` is specified" implementation-defined
outright. `<style>` is deliberately absent from
`htmlPreserveWhitespaceElement`, whose set is HTML 4.01 §9.3.4's and HTML5's:
`pre`, `listing`, `plaintext`, `textarea`, `xmp`. Pinned by
`TestXHTMLIndentsBeforeStyleWithTextContent`.

The catalog schema also licenses ignoring this outright — *"Test drivers are
free to ignore differences in the serialization that are known to be
irrelevant"* — and the case does not test the serializer: its own description
calls it *"a 'system test' of schema-aware processing"*.

## `import-schema-137` — §2.9 declines to decide, on both targets

**Not implementable.** Both errors are genuinely present: `z:familyname` is
absent from `schema061.xsd` (only `surname` is declared) so XTTE1512 is right
for that node, while the enclosing `z:person` is invalid against `personType` so
XTTE1510 is right for that one. §2.9 settles the choice by declining to:
"**It is implementation-dependent which of the several errors is signaled.** This
applies both to static errors and to dynamic errors." Either answer conforms;
the suite tests one processor's order — `Saxon_9.8.xml` records `result="pass"`,
which is what a catalog written against one implementation's detection order
looks like from the other side.

## `format-number-070` — the stylesheet declares no such template

**Not implementable — suite defect.** The catalog invokes
`<initial-template name="main"/>`; the stylesheet contains exactly one template,
`match="root"`, and **zero occurrences of `name="main"`** (`grep -c` = 0, and
zero `xsl:import`/`xsl:include`). The spec: XTDE0040 is raised when the
invocation "specifies a template name that does not match the expanded-QName of
a named template defined in the stylesheet", and §2.9 makes it a
**non-recoverable** dynamic error — mandatory, not discretionary. Passing means
violating the spec. No third-party result settles it either way: the case was
created in 2020 and does not appear in `Saxon_9.8.xml` at all.

## `docbook-001` — a vendor extension, on both targets

**Not implementable.** The vendored DocBook XSL 1.79.1 writes its chunks through
vendor extension elements, and `chunker.xsl` offers a choice of **three**:
`element-available('saxon:output')`, `element-available('exsl:document')` and
`element-available('redirect:write')`, each tested at two sites. All three are
outside the XSLT specification — the `saxon:output` branch even sets
`saxon:character-representation` — so passing means implementing Saxon's or
Xalan's proprietary output elements, i.e. impersonating another vendor.

When no branch matches, an `xsl:otherwise` reaches
`<xsl:message terminate="yes">` that concatenates the literal text
`Can't make chunks with `, `system-property('xsl:vendor')` and `'s processor.`
**The error string `XTMM9000: Can't make chunks with go-xml's processor.` is the
stylesheet's own words about us, not a diagnostic of ours** — which is why the
symptom moved (it once read `XTDE1450: exsl:document is not available`) while
the verdict did not.

## `sequence-0132` — §11.10 makes XTSE0010 the correct 2.0 answer

**Not implementable.** §11.10's element syntax summary gives `xsl:sequence` a
**mandatory** `select` and `<!-- Content: xsl:fallback* -->`; §3.9 XTSE0010
fires "if a required attribute is omitted, or if the content of the element does
not correspond to the content that is allowed". So XTSE0010 is the correct 2.0
answer and it is static, raised before any type check could reach XTTE0570. The
stylesheet itself carries `<?error XTSE0010?>`, and **Saxon 9.8 and Parrot 2017
both report `wrongError` with "Expected XTSE0010"** — an older catalog wanted our
answer. The `XSLT20+` scope is stale metadata: the expectation was edited to
XTTE0570 in 2017 and 2018 without narrowing the scope to 3.0.

## The three `regex-syntax-xslt20` cases — 2012 Unicode, corrected upstream

All three are 2012-era XSLT 2.0 cases whose XSLT 3.0 twins the W3C has since
corrected or made edition-neutral, and never back-ported. The engine was probed
directly against the catalog's own `match` and `nonmatch` lists, which reduced
each disagreement to its exact codepoints. `Saxon_9.8.xml` reports all three
`notRun`.

| Case | Exact disagreement | Why it is not ours |
|---|---|---|
| `regex-syntax-xslt20-0984` | `[\w]` misses U+2308 and U+2309 and nothing else | **Fixed upstream.** The 3.0 twin `regex-syntax-0984` carries `<modified by="Michael Kay" on="2024-05-04" change="Drop x2308 and x2309, characters reclassified"/>`, and those two codepoints are the only difference between the copies. The category argument also holds: U+2308/09 are `Ps`/`Pe`, and Appendix F defines `\w` by subtracting `\p{P}`. |
| `regex-syntax-xslt20-0985` | `[\d]` misses U+1369–U+1371 and nothing else | **Fixed upstream silently.** The 3.0 twin's `[\d]` list omits U+1369–U+1371 (ETHIOPIC DIGIT ONE–NINE), the only difference between the copies. They were `Nd` in Unicode 3.0 and are `No` now; `\d` is `\p{Nd}`. |
| `regex-syntax-xslt20-0987` | `[\c]` admits U+0346 and nothing else | **Edition drift, and 4e is not ours to adopt.** The case's `match` list holds exactly 72 codepoints, `0300-0345` and `0360-0361` — XML 1.0 **4th edition**'s `CombiningChar` character for character — with `nonmatch` holding U+0346, the first codepoint in the gap 4e leaves. We implement **5th edition**, whose `NameChar` is the blanket `[#x0300-#x036F]` (`xpath/classdiff.go`). **Cost:** `testdata/xsdtests/common/xsts.xsd` enumerates `XML-1.0-1e-4e` and `XML-1.0-5e` as **mutually exclusive** processor configurations and records that "XSD 1.1 describes XML 1.0 Fifth Edition as the base version in its normative reference", so the 4e reading this one case wants would be paid for out of the XSD 1.1 numerator — and the one translation in `xpath/fn_regex.go` serves `\c` for XPath, XQuery, XSLT and XSD `pattern` facets alike. The W3C reached the same conclusion: the 3.0 twin was rewritten **edition-neutral** — every combining character removed from `match`, the `nonmatch` parameter deleted outright — so it passes under either edition. |

One caveat is worth keeping: XSD Part 2 Appendix F is not among the four
vendored specs, so the 4e reading of `-0987`'s test data rests on F&O §5.6.1's
wholesale delegation to it plus the fingerprint in the data, rather than on
Appendix F's own words. That caveat cuts against changing anything, not for it.

---

# 3. Corrections

Places where a verdict recorded in this file, or in `known-gaps.md`, was
**wrong**. Not a record of fixed work — a record of false claims, kept so the
next reader does not re-derive them.

## `validation-0201` does not fail on the indent width

**Five places across three documents said it did.** Measured: setting the indent
to three spaces gains nothing — 6,193/8 either way, the reported offset merely
moving from 46 to 134. The width was never what fails. The real cause is a
newline before `<style>` inside `<head>`, which Serialization 3.1 §5 permits
either way. The correct reading is in §2.

The case earned this correction four times over, each verdict true of one layer
and blind to the next: called *fixable in the harness*, then *an engine defect
stands behind it*, then *implementation-defined on the indent width*, and now
*implementation-defined on whitespace placement*. The lesson is not that any one
answer was right but that **a case can fail for several independent reasons at
once, and fixing the visible one is what reveals whether there was another**.

## `merge-077` — both recorded blockers were false

It was recorded as needing an AST-walk facility `xpath` lacks, and as being
blocked by a broken `lang="sv"` collation. **Neither was true.** `walkCalls` and
`StaticCalls()` already existed and needed only argument values; and `merge-076`
is the same stylesheet with the same collation, and passes. The subject was a bad
source name.

## `accumulator-061` — the recorded blocker was the wrong one

It was recorded as needing changed function-item semantics. §10.3.6 focus capture
is **already implemented** and cites the clause. The real blocker is that nothing
marks the principal input as streamed, which puts it in §2 as a divergence with
a measured risk rather than in §1 as work.

## `FunctionCall-052` passes by accident

Subtype comparison reads a static table of built-in spellings with no handle on
an imported schema. Neither its answer nor the answer of its failing converse
`FunctionCall-051` is reasoned. A passing case is not evidence of a correct rule.

## A `known-gaps.md` entry recorded an XSpec regression that does not exist

The entry named a regression at 224. **It was fabricated.** The gate and a
rebuild both score **225**. The entry has been removed.

## `RexParser`'s "fails at offset 0" was misleading

`parseNestedExpr` resets the cursor before refusing, so the offset names the
reset, not the construct. The actual construct is a multi-clause FLWOR in a
conditional branch.

## `evaluate-048` was recorded as closed and is not

The CHANGELOG entry for `fn:function-lookup`'s dynamic visibility says "Fixes
`evaluate-048`", and the half it names *is* fixed — but the case still fails, now
on a network fetch. What is wrong is the claim that it was closed. Same shape as
`validation-0201`: two independent reasons, one fix.

## `evaluate-045` — the spec argument was false

The row claimed visibility is a property of a component of an `xsl:package` and
"a plain `xsl:stylesheet` is not one". **§3.6 says the opposite verbatim**: "When
the `xsl:package` element is not used explicitly, the entire stylesheet comprises
a single implicit package." The divergence stands and is defensible; it is a
won't-fix, not a can't-fix, and it was simultaneously described as won't-fix,
can't-fix and a cost trade-off. Correcting it also removes a latent contradiction
with `accumulator-038`, whose verdict *depends* on its stylesheet being a package.

## `package-021err` — the recorded defect was in the wrong attribute

The row claimed neither `@name` nor `@names` admits an arity. **§3.6.2 admits one
in `@names` explicitly**: "The `names` attribute selects a subset of those
components by name (**and in the case of functions, arity**) … Examples are `*`,
`p:*`, `*:local`, `p:local`, and **`p:local#2`**", and §3.6.3.2 imports those
rules for `xsl:accept`. We already parse `#N` correctly. The real defect is in
`xsl:function/@name`.

## `accept-913` — the recorded diagnosis describes a different error

It describes an investigation into `xsl:initial-template` visibility, not the
error we now emit. XTDE3052 is scoped to `visibility="absent"` and nothing here
is absent. Reopened as an open question in §1.

## `sequence-0132` — the alleged contradiction compared two different constructs

The row argued from `sequence-2401a`. The two are different constructs: 2401 has
`@select` *and* content, 0132 has content and no `@select`. §11.10 and §3.9
settle it directly, without the comparison.

## `regex-syntax-xslt20-0987` — overturned to "ours", then overturned back

An audit read the test data exactly — it is XML 1.0 4e `CombiningChar` character
for character — and concluded the case was ours. **The step it skipped is whether
4e is a configuration we are free to adopt.** It is not: `common/xsts.xsd` makes
4e and 5e mutually exclusive processor configurations and names 5e as XSD 1.1's
normative base. The W3C's 3.0 twin was made edition-*neutral*, not 4e, which
settles the intent.

## `stable bugNNNN` was read as "challenged"; it means "settled"

A previous revision merged `queried` and `stable` into one column and described
both as challenged, which inverts `stable`'s meaning: it means the WG examined
the bug and **settled** the expectation. The two are separated in §1.

## A status field cannot settle a case that has none

`iri-001` was recorded as a proved suite defect on the strength of a status field
**it does not have** — it carries no `<current>` element at all. It was ours (a
driver defect), not the suite's.

## `docbook-004` and `package-version-011` were filed under false reasons

`docbook-004` was filed as a vendor-extension case on the strength of its
neighbour's name; its stylesheet is five lines with **no extension element**.
`package-version-011` was filed as needing a network fetch; **no fetch exists** —
`doc('')` names the containing module.

## `streamable-141` did not need the streamability analysis

It was recorded as requiring §19.8. §3.9.1 states its rule "*notwithstanding
anything stated in 19 Streamability*": an instruction processed with XSLT 1.0
behavior **is** roaming and free-ranging, by declaration rather than as a
consequence of any posture inference. Checkable without the analysis.

## The arithmetic was internally inconsistent

A previous revision's top table claimed 131 can't-fix while the prose said 126 in
one place and 124 in another, and the Summary's reason table summed to 124. The
XSD section's own table printed totals of 53 and 49 against the top table's 51
and 47, and `accepted` counts of 8 and 5 against a measured 6 and 2 — the latter
mattering because the file then named more "settled suite defects" than the
bucket contained. The header attributed its figures to a commit that was not the
tree's HEAD. **This is what closed entries sitting in live tables do to the
counts**, and it is the reason this file now carries only open gaps, divergences
and corrections.

---

# What is skipped, and why that is not a gap

The XSLT 3.0 suite has 14,601 cases; 11,518 are in scope. The 3,083 skipped are
excluded by *declared dependency*, not by failure.

| Skipped | Reason |
|---:|---|
| 1,580 | depends on a specific Unicode version |
| 1,098 | scoped `XSLT20` only |
| 107 | numbering combinations |
| 96 | year-component values |
| 33 | `disable-output-escaping` — the serializer escapes always |
| 24 | `enable_assertions` — unmodelled dependency |
| 22 | require schema-awareness to be *absent* |
| 21 | scoped `XSLT10 XSLT20` |
| 18 | `xsl-stylesheet-processing-instruction` |
| 12 | `package_version_resolution` — unmodelled |
| 12 | `additional_normalization_form` — unmodelled |
| 7 | `maximum_number_of_decimal_digits` — unmodelled |
| **3,030** | **listed above; the remaining 46 are single-case reasons** |

Counting these as failures would understate the engine; counting them as passes
would overstate it. They are reported separately for that reason.

---

# Caveat on confidence

The verdicts are not uniformly deep. The package-composition, regex and XPath
cases were root-caused by reading the specification and the engine. The XSD
breakdown is derived from the suite's own `status` field and the kind of each
disagreement, which does not identify *which* rule is missing in each case — and
the status field is weaker evidence than it looks, for the two reasons recorded
in §3.

Two limits on the XSLT 3.0 evidence are worth stating, because several verdicts
lean on them:

**The XSLT 3.0 spec here is a 2012 Last Call Working Draft, not the 2017
Recommendation.** `specs/xslt-lcwd30.xml` says so in its own status section. Any
verdict of the form "the spec grants no such rule" is a claim about the LCWD.
`strip-space-009` is the case most exposed to this: it was authored in December
2012 by the spec's own editor, which is some evidence the WG intended a rule the
LCWD does not state.

**Saxon 9.8's submission is not a neutral referee.** It passes most of the XSLT
3.0 failures this file reads individually, but several expectations were recorded
from Saxon's own behaviour: `validation-0006`'s stylesheet says outright that it
is "a contrived example to force Saxon down a particular code path", and
`validation-0201`'s expected file is Saxon's output byte-for-byte. Saxon's
submission also predates `accept-913`, `package-200`, `package-021err`,
`package-022err` and the `docbook` set, so it is silent on those. Where Saxon
passes a case *and* the spec text supports it, that is real evidence; where Saxon
is the source of the expectation, it is not.

---

# Related

[reaching-100.md](reaching-100.md) answers the question this file's numbers
raise: what it would actually take to close each gap, and which of them are not
work at all. Its per-gap reasoning still applies while its arithmetic predates
the streaming gate coming off and `import schema` landing — read it for the
*what it would take*, and take the counts from here.

[known-gaps.md](known-gaps.md) is the reasoning behind the hard entries here:
diagnosed causes, fixes that were attempted and measured and reverted, and what
a real fix would cost where the answer is a rewrite rather than a patch. It also
covers DTD and XDM, which have no public suite and so appear in no percentage.
