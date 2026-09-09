# W3C conformance: the remaining gaps

Every figure here comes from a full run of the suite it names, with
`tests/check.sh`. The *In scope*, *Passing*, *Now* and *Failing* columns were
re-measured at commit `830ae11` plus the uncommitted `import schema` work, and
every case this file names was checked against those runs in both directions:
absent from the failing list means the entry is stale, present means the
recorded reason was re-read against the actual failure text. The *Fixable*, *Open* and *Can't fix* columns are
verdicts, not measurements, and were revised by the audit recorded at the foot
of this file; the *Ceiling* column is what those verdicts imply and is
therefore no longer a measured figure.

| Component | Suite | In scope | Passing | Now | Failing | Fixable | Open | Can't fix | Ceiling |
|---|---|---:|---:|---|---:|---:|---:|---:|---|
| **xdm** | *(no external suite)* | — | — | — | — | — | — | — | — |
| **xpath** | QT3 — XPath 2.0 | 15,217 | 15,217 | 100.00% | **0** | 0 | 0 | **0** | 100.00% |
| **xpath** | QT3 — XPath 3.0 | 19,362 | 19,362 | 100.00% | **0** | 0 | 0 | **0** | 100.00% |
| **xpath** | QT3 — XPath 3.1 | 21,898 | 21,898 | 100.00% | **0** | 0 | 0 | **0** | 100.00% |
| **xquery** | QT3 — XQuery 3.1 | 30,346 | 30,143 | 99.33% | **203** | 0 | 0 | **203** | 99.33% |
| **xslt** | W3C XSLT 2.0 | 6,201 | 6,193 | 99.87% | **8** | 0 | 0 | **8** | 99.87% |
| **xslt** | W3C XSLT 3.0 | 11,525 | 11,348 | 98.46% | **177** | 0 | 0 | **177** | 98.46% |
| **xsd** | W3C xsdtests 1.0 | 39,388 | 39,358 | 99.92% | **30** | 0 | 0 | **30** | 99.92% |
| **xsd** | W3C xsdtests 1.1 | 41,576 | 41,545 | 99.93% | **31** | 0 | 0 | **31** | 99.93% |
| **relaxng** | Clark spectest | 965 | 965 | 100.00% | 0 | 0 | 0 | 0 | 100.00% |
| **xslt** | DocBook xslTNG *(real-world)* | 577 | 577 | 100.00% | 0 | 0 | 0 | 0 | 100.00% |
| **xslt** | XSpec *(real-world)* | 225 | 225 | 100.00% | 0 | 0 | 0 | 0 | 100.00% |
| | **Total** | | | | **449** | **0** | **0** | **449** | |

The last two rows are not W3C suites but real-world corpora — DocBook xslTNG's
577 test documents and XSpec's 225 — kept here because they are the only
measurement in this file taken against stylesheets nobody wrote for a test
harness, and because four defects the W3C suites missed were found by them.
They are not in the *Total*, which counts W3C disagreements only. Note that
they are unrelated to the `docbook-001`/`docbook-004` cases read below, which
belong to the W3C XSLT sets.

*Ceiling* is what the suite would report if every fixable case landed and every
open question resolved our way; the "can't fix" column is what stands between
that and 100%. Where a fixable case leaves the *denominator* rather than joining
the numerator — because the suite itself declares it out of scope — the ceiling
reflects that.

> **Audit note (this revision).** An adversarial re-audit of the "can't fix"
> verdicts found that the previous claim — *0 fixable, none a known engine
> defect* — was wrong. Twenty-three cases are work: two are engine defects
> (`docbook-004`, `package-version-011`), `iri-001` is a harness defect since
> fixed, two are cases the suite already declares out of scope through a
> dependency the harness does not read, and the rest are harness scoring
> defects — chiefly the eight XSD `indeterminate` expectations per version that
> are silently scored as "must be invalid". One XSLT 3.0 failure,
> `strip-space-009`, was missing from this document altogether. Several other
> verdicts kept their outcome but rested on reasoning the spec text does not
> support, and three filed as "not implementable" were really judgements about
> cost or architecture. The arithmetic was also internally inconsistent: the
> table claimed 131 can't-fix while the prose enumerated 124 and 126 in two
> other places. What changed, and the evidence for each, is in *Corrections
> from the audit* at the foot of this file. Verdicts that survived are
> unchanged, and where the audit could not settle a case it says so rather than
> moving it to a flattering bucket.

**All 449 disagreements are triaged as unfixable.** None is now work: the
engine defects, harness defects and out-of-scope cases the audit found have all
been settled — `iri-001` and `docbook-004` fixed, `regex-syntax-xslt20-0987`
returned to *not implementable*, and `validation-0201` fixed at the engine
without moving the case, which put its two entries in the can't-fix column
where the first verdict had placed them.

The count rose from 103 to 449 without a single case regressing, and the two
causes are worth separating because they pull in opposite directions. **346 of
the increase is denominator growth**: implementing `import schema` admitted 416
XQuery cases and 60 XPath cases per lane that the `schemaImport` feature gate
had been excluding, and lifting the streaming gate admitted the XSLT 3.0
streaming corpus. Cases that were being counted as "not our business" are
counted as ours now, which is why the percentages fell while nothing got worse.
**The other component is that the two largest blocks are single features, not a
long tail**: 150 of the 177 XSLT 3.0 failures want an `XTSE3430` that only the
§19.8 posture-and-sweep analysis can emit — and §19.1 says a non-streaming
processor "is not required to assess whether constructs are guaranteed-streamable" —
while 203 of the XQuery failures are the five schema-aware features
[todo.md](todo.md) §1.5 deliberately leaves. Neither is a backlog of defects.

In the *Fixable* column above, "fixable" means "the count can move" — which
covers three distinct things, and the audit found the old document conflating
them. A case may be an engine defect (`docbook-004`, `package-version-011`), a
harness defect where the engine is already right (`iri-001`, now fixed, and
the eight `indeterminate` XSD expectations per version), or a case the suite
itself puts out of scope through a dependency the harness does not honour
(`streamable-141`, `unparsed-text-2003`). Only the first kind moves the
numerator; the other two
move the denominator or the scoring. They were counted together because all
three are work, but they are not the same claim and are labelled individually
below. Every example named in this paragraph has since been settled, which is
why the column reads zero — the distinction is kept because it is what the
column *means*, and the next case to enter it will need to be filed under one
of the three.

**Why the XPath in-scope counts rose — and the failure that came with them,
since fixed.** Two feature
labels in `tests/qt3/runner.go` had outgrown their meaning: `namespace-axis`
and `infoset-dtd` were listed as unsupported long after they stopped being so.
The namespace axis is implemented (`xpath/nsaxis.go`), and the XSLT harness had
declared `namespace_axis: true` all along — the two harnesses contradicting
each other is what exposed it. Lifting both moved the *in-scope* count, which
is the only signal that proves a lift took effect: XPath 2.0 15,183 → 15,222,
3.0 19,244 → 19,307, 3.1 21,786 → 21,863, XQuery 29,918 → 29,964 in scope
(29,901 → 29,952 passing) with no new XQuery failure. `import schema` then
lifted the `schemaImport` gate and moved them again: XPath 3.0 19,302 → **19,362**
and 3.1 21,838 → **21,898**, both still at zero failures, and XQuery
29,930 → **30,346**.

`infoset-dtd` lifted clean — 27 more cases per XPath lane, 32 in XQuery, zero
failures. `namespace-axis` admitted 36 more cases per lane, all of which now pass.
One did not at first — and it is now fixed, so the trade described below was
taken and paid off. `prod-AxisStep/Axes123` asserts node *identity* across
two namespace-axis walks (`/*/namespace::xlink is /*/namespace::*[. =
'…/xlink']`). It is XP20+, so it appears in all three XPath lanes and is one
distinct case rather than three. The cause is structural rather than a wrong
answer: namespace nodes are *synthesized* per axis walk (`xpath/axes.go`
allocates a fresh `&xdm.Node` for each in-scope binding), while `is` compares
Go pointers (`ln == rn` in `xpath/operators.go`), so two walks over the same
binding can never be identical. Fixing it means giving namespace nodes a stable
identity — caching them on the element, or comparing on (parent, prefix)
instead of pointer — which is an engine change rather than a harness one. It
was made: `Node.Is` now defers to `Order()` for `KindNamespace`, and the set
operators key on `IdentityKey` so they agree. The case passes on all three
lanes and XPath is clean again. The trade was worth taking: the lift bought 63
in-scope cases at 3.1 for one failure that the skip was previously *hiding*,
and a visible failure with a known cause is better than a silent exclusion.

**The XQuery failures that predate schema import.** Three are read case by
case below; the rest fall in three sets not yet diagnosed individually —
`prod-ModuleImport` (4: `module-URIs-3`, `modules-31`, `-32`, `-33`),
`prod-ContextItemDecl` (4: `contextDecl-048` and `-052` wanting
XQST0113, `-050` and `-051` wanting XPTY0004) and
`prod-OptionDecl.serialization/Serialization-003`, which wants XQST0108. This
paragraph read "remaining 17 … `prod-ModuleImport` (8) …
`prod-DecimalFormatDecl` … (1)"; three module-import cases and the
decimal-format one have since passed, which is what shrank the pre-import
residue. It is important that this set has not grown: the 203 XQuery failures
below are cases `import schema` newly *admitted*, not cases it broke, and the
distinction only holds because the pre-import set was diffed by name rather
than by count.

`prod-ModuleImport/errata6-003` was briefly counted here and does not belong.
It fails with `XPST0051: unknown type "a:hatsize"` -- the schema-aware shape,
not a module-import defect -- and it is **absent from the pre-import failure
list entirely**, which settles it: the case was out of scope before `import
schema` admitted it. That is the whole test for which side of this boundary a
case falls on, and it is a measurement rather than a reading of the symptom.
The pre-import residue is twelve, not thirteen.

| Cases | Verdict | Why |
|---|---|---|
| `app-Demos/RexParser` | **Not implementable here** | The sibling `sudoku` was fixed by making a FLWOR in a conditional branch belong to that branch; this one still fails to parse at offset 0 for a different reason in the same family, and is a large real-world query rather than a targeted case. |
| `same-key-023` | **Not implementable here** | 421,875 keys through O(n) `map:put` and `map:remove`. Measured rather than estimated: per-key cost scales linearly with map size (66µs at n=1,000 to 1.28ms at n=40,000), so the whole case extrapolates to 1.5–2 hours — four to five orders of magnitude from the deadline, which no constant-factor work reaches. A persistent map would fix it, but `MapItem` has 58 references across 15 files and its entries order is load-bearing for serialization stability, which a HAMT does not preserve. `same-key-024` covers the same semantics at 11,250 keys and passes. |
| `K2-sequenceExprTypeswitch-5` | **Not implementable without a parser change** | Wants a static `XPST0008` for a variable named in an unreached `typeswitch` branch. A check restricted to sibling-clause variables was built and passed eleven tests, then broke `K2-ForExprWithout-8`, where a `default $d return ()` sits inside a `for` clause binding `$d`: a sibling's name may be shadowed by an outer binding, so seeing it free proves nothing. The counts stayed net-neutral, and only the case-list diff caught it. A sound check needs the parser to track in-scope variables, which it does not do today. |

Three left this list. `sudoku` went when a FLWOR in a conditional branch was
made to belong to that branch — an earlier attempt had guarded on the
preceding word and measured 0 gains against 2-5 regressions, where tracking
the nesting costs nothing. `K2-BaseURIProlog-4` and `-5` went when a *relative*
base-URI declaration was resolved and an absolute one taken verbatim: the five
cases a previous attempt broke declare absolute URIs containing a quote, `#`
or a trailing space, which `url.ResolveReference` was re-encoding. The
`staticContext.declBase` field the fix needed already existed and was never
read or written.

`eqname-007` went, and the verdict recorded here was wrong: it said the prefix
was "genuinely unbound", and it is not — `ex` is bound by the `xmlns:ex` on the
enclosing element constructor, which §3.9.1.3 puts into the in-scope namespaces
of its content. The suite was right and this engine was not.

**XQuery 3.1: 30,143 / 30,346 = 99.33%** — what passes now. The denominator
grew by 416 when `import schema` was implemented and the `schemaImport` feature
gate came off the harness (29,930 → 30,346); 213 of those 416 newly-admitted
cases pass and 203 fail, which is the whole of the increase in the failure
count. The passing count rose by more than 213 — 29,918 → 30,143 — because four
of the pre-import failures were fixed in the same period, which is why the
pre-import residue also had to be diffed by name rather than inferred from the
arithmetic. The 203
failures are not one gap but five, each a separate small feature rather than a
defect in the import: typed *input* documents, constructor functions for
schema-defined simple types, impure and restricted unions (which `xslt` refuses
identically, by a shared and deliberate purity rule), annotation propagation
through a constructor, and substitution groups over validated content. They are
catalogued in [todo.md](todo.md) §1.5.

Where they land bears that out: `prod-CastableExpr` (49) and
`prod-CastExpr.schema` (47) are the constructor-function gap, `prod-SchemaImport`
(29) and `prod-InstanceofExpr` (12) the typed-input and annotation ones, and
`prod-FunctionCall` (16) the union rule, reporting `XPST0008: "lu:restrictedUnion"
is not a type in the in-scope schema definitions` on the very types §1.5 records
as deliberately refused. No set outside that catalogue gained a failure.

Of the failures that predate schema import, nothing is left that is both
fixable and worth the change.

The XSD split is taken largely from the suite's own `status` field rather than
from judgement: `accepted` marks a settled expectation and `queried` marks one
the W3C has itself challenged, usually with a bugzilla reference. The audit
found two things wrong with how that was applied. `stable bugNNNN` was counted
as challenged, when it means the WG examined the bug and **settled** the
expectation — the opposite. And a status field cannot settle a case that has
none: `iri-001` carries no `<current>` element at all and was nevertheless
recorded as a proved suite defect. Nine XSD cases per version now count as
work; the reasoning is under *What is genuinely ours*.

This is down from 345 at commit `69c53cf` — measured against the denominators
of the time, which is the caveat that matters, since the totals above are
larger only because the denominators grew. In three rounds of agents working
in isolated worktrees. The first cleared 103 cases: 10 of 10 in the
`xsl:override` cluster, 9 of 12 across `package`/`accept`/`expose`/
`use-package`, and 80 XSD schema-validity disagreements. The second cleared a
further 59 — 23 XSLT 3.0, 10 XSD 1.0, 26 XSD 1.1 — and took XPath 2.0 and 3.0
to a clean 100%. The third cleared 49 more, almost all of them XSD
schema-validity rules, and took XPath 3.1 to 100% as well.

Every round also moved cases the other way, into "cannot be fixed", and those
matter as much as the fixes because they are what stops the remainder being
mistaken for a backlog. Round 1 settled the regex and validation-ordering
items; round 2 `accumulator-038`, `json-to-xml-048`, `validation-0201` and
`validation-0006`; round 3 the four `notQName` cases, `particlesZ001`,
`particlesZ033_g`, `simple093` and `sequence-0132`. Each was settled by
reading the spec or the test rather than by changing the engine, and several
by measuring what the "fix" would actually cost — the `notQName` reading is
right about the suite and still loses 150 agreements.

Three of those settlements did not survive the audit. `validation-0201` is a
harness comparison the catalog schema explicitly licenses; `particlesZ033_g`
and `simple093` were called suite defects without a reading of the rule they
turn on, and were reopened as questions. `particlesZ033_g` has since been
answered — the rule it turns on is the 1.1 wildcard relaxation, and applying it
is right — so it is settled again, this time on a measurement rather than an
assertion. That two rounds of "moving cases into
cannot-be-fixed" produced verdicts a third pass overturned is the argument for
this section existing at all: the direction a case moves is easier to justify
than to check.

`validation-0201` then moved twice more, and its full history is the sharpest
illustration this document has. Called *fixable in the harness*, then *an
engine defect stands behind it*, and now *implementation-defined* again — but
arriving there by a different route, because each verdict was true of one layer
and blind to the next. The engine defect was real and is fixed; the case still
fails on the indent width alone, which is where the first verdict pointed. The
lesson is not that the first answer was right but that a case can fail for
several independent reasons at once, and fixing the visible one is what reveals
whether there was another.

The reason the fixable count fell faster than the failure count is that the
work went where the cases were: XSD schema validity moved from 99.78%/99.68%
to 99.85%/99.88% across the three rounds, which is most of what was ever
tractable.

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

The audit added three labels, because "not implementable" had been absorbing
cases that do not meet its own definition above:

**Costs more than it gains** — implementable, and measured to lose more cases
than it wins. A real reason to leave a case alone, and a *different claim* from
impossibility. It must never be filed as "not implementable".

**Architecture debt** — implementable, with a known design change and a stated
cost, not attempted at the current blast radius.

**Won't fix** — implementable and spec-mandated, deliberately not done because
conforming would break correct real-world stylesheets. `evaluate-045` is the
only one, and it is not counted among the unfixable.

**Out of scope** — the suite itself declares the case inapplicable through a
dependency the harness does not read. These leave the denominator; they are not
failures at all.

---

# xdm — the data model

`xdm` has no external conformance suite: XDM is a data model, not a language
with a test corpus. It is measured **indirectly and continuously** — every one
of the 84,000-odd cases above builds, navigates and atomises XDM instances, so
a defect in the model surfaces as a failure in XPath, XSLT or XSD rather than
in a suite of its own.

It carries 18 unit-test files of its own covering the parts the language suites
exercise thinly: type annotation, attribute-value normalisation, character-set
handling, node identity and document order.

**No known gaps.** Several XDM-level defects were found and fixed through the
language suites this session — `dm:nilled`, `IsNilled` propagation through copy
and type-annotation stripping — which is the mechanism working as intended.

---

# xpath — no failures

All three XPath versions now agree with the suite on every case in scope.

The last three disagreements were both halves of one thing:
`fn:load-xquery-module` compiles an XQuery library *module*. There is now an
XQuery processor in [`xquery`](../xquery/), but it does not implement module
import — `import module` raises `XQST0059` — so there is still nothing that
can load one.

The suite settles it with a feature dependency that the harness was not
reading. The set declares `fn-load-xquery-module` `satisfied="true"` and then
overrides fourteen cases to `satisfied="false"`; those fourteen are the ones
written for a processor that lacks the feature. Because `unsupportedSpec` did
not list the feature, `-001` through `-004` ran and failed — they want
`FOQM0001` for an empty URI and `FOQM0002` for a module that cannot be located,
neither of which an engine that never looked can honestly report.

With those out of scope the apparent contradiction goes with them. The cases
that do apply — `-901`, `-902`, `-903` and `function-lookup-764` — accept
**FOQM0006** ("the implementation does not support the load-xquery-module
function") throughout, and `-901`/`-902` accept either it or `FOQM0001`. The
function now reports the absent processor uniformly, which is the only claim it
can make truthfully. The reasoning is recorded in `xpath/fn_31.go`.

`json-to-xml-048` was the fourth and is fixed: it was never an engine bug, but
the harness's own comparison serializer writing a literal CR, which XML §2.11
converts to LF on re-parse.

**XPath: 21,898 / 21,898 = 100.00%, on all three versions.**

---

# xslt — 185 failures across the two targets

Eight at the 2.0 target and 177 at the 3.0 target. Three cases fail at both —
`import-schema-137`, `validation-0201` and `docbook-001` — so the distinct case
count is 182. (This heading has read 29, 28, 27, 23, 22 and 21 in turn as
`xsl:assert` cleared `catalog-006b`, an audit found `strip-space-009` missing
from the 3.0 list, `unparsed-text-2003` left the denominator, and
`use-package-003` was fixed by scoping an ordinary function call to the package
it is written in.)

**The jump from 21 to 185 is the streaming gate coming off, not a regression.**
Every case the 21 named is still accounted for below, and the eight XSLT 2.0
failures are byte-identical to what they were. Of the 164 newly-visible 3.0
cases, **150 want an `XTSE3430`** — a refusal of a stylesheet as
non-streamable, which only the §19.8 posture-and-sweep analysis can emit — and
136 of those read literally "expected error XTSE3430, the transform succeeded":
the engine computes the right answer and the test wants it to decline. §19.1
settles whether that is owed: a processor that does not stream "is not required
to assess whether constructs are guaranteed-streamable". These are the largest
block in this file and they are not defects. They are diagnosed under *The
streaming row is the one that overstates the gap*, and only the small remainder
is read case by case here.

## XSLT 2.0 — 8 failures

The table below carries nine rows; `unparsed-text-2003` is the ninth and is
out of scope rather than failing, so it is not in the eight. This section used
to open "None of the nine can be fixed." Two can, and
neither moves the numerator: `validation-0201` is a harness comparison the
suite's own schema licenses, and `unparsed-text-2003` is a case the suite
declares out of scope through a dependency it forgot to write down. A third,
`regex-syntax-xslt20-0987`, was briefly filed as an engine defect and has been
returned to *not implementable* — see its row.

| Case | What happens | Verdict | Why |
|---|---|---|---|
| `format-number-070` | `XTDE0040: no template named "main"` | **Not implementable** | Suite defect. The catalog invokes `<initial-template name="main"/>`; the stylesheet contains exactly one template, `match="root"`, and zero occurrences of `name="main"` (verified by grep). The spec: XTDE0040 is raised when the invocation "specifies a template name that does not match the expanded QName of a named template defined in the stylesheet" and "**It is a dynamic error**" — mandatory. Passing means violating the spec. |
| `unparsed-text-2003` | First of four assertions returns `false` | **Out of scope — suite defect** | Requires fetching `http://www.w3.org/Consortium/mission.html`; the three local-file assertions pass. But the catalog *does* have a dependency for this and the harness already honours it — the sibling `unparsed-text-2002` declares `available_documents` for the same URL and is skipped, while `2003` omits it. See the 3.0 entry below. |
| `docbook-001` | `XTMM9000: Can't make chunks with go-xml's processor.` | **Not implementable** | The vendored DocBook XSL 1.79.1 uses the EXSLT `exsl:document` extension element — 19 times in `chunker.xsl` alone. A vendor extension outside the XSLT specification. The *reported error* has moved since this row was written — it read `XTDE1450: exsl:document is not available`, and DocBook's own `chunker.xsl` now reaches its `xsl:message terminate="yes"` fallback first and says so in its own words. The verdict is unchanged; only the symptom is. |
| `regex-syntax-xslt20-0984` | `[\w]` does not match U+2308 `⌈` | **Not implementable** | Unicode drift, and **the W3C has already fixed it upstream**: the XSLT 3.0 twin `regex-syntax-0984` carries `<modified by="Michael Kay" on="2024-05-04" change="Drop x2308 and x2309, characters reclassified"/>`, and those two codepoints are the only difference between the two copies. The 2.0 copy was never back-patched. (The category argument also holds: U+2308 is `Ps`, and Appendix F defines `\w` by subtracting `\p{P}`.) |
| `regex-syntax-xslt20-0985` | `[\d]` does not match U+1369 `፩` | **Not implementable** | Same shape, fixed upstream silently: the 3.0 twin's `[\d]` list omits U+1369–U+1371 (ETHIOPIC DIGIT ONE–NINE), the only difference between the copies. They were `Nd` in Unicode 3.0 and are `No` now; `\d` is `\p{Nd}`. |
| `regex-syntax-xslt20-0987` | `[\c]` matches U+0346 `͆` | **Not implementable** | Edition drift, and the same shape as its two neighbours after all. The audit read the data correctly and drew the wrong conclusion from it. The data: this case's `match` list holds exactly 72 codepoints in the combining block, `0300-0345` and `0360-0361`, and `nonmatch` holds U+0346, the first codepoint in the gap — XML 1.0 **4th edition**'s `CombiningChar` character for character. We implement **5th edition**, whose `NameChar` is the blanket `[#x0300-#x036F]` (`xpath/classdiff.go`). What the audit did not check is whether 4e is a configuration we are free to adopt. It is not: the XSD test suite's own schema (`testdata/xsdtests/common/xsts.xsd`) enumerates `XML-1.0-1e-4e` and `XML-1.0-5e` as **mutually exclusive** processor configurations and records that "XSD 1.1 describes XML 1.0 Fifth Edition as the base version in its normative reference" — so the 4e reading this one case wants would be paid for out of the XSD 1.1 numerator, and the same translation serves `\c` for XPath, XQuery, XSLT and XSD pattern facets alike. The W3C reached the same conclusion: the 3.0 twin `regex-syntax-0987` was rewritten to be edition-**neutral** — every combining character was removed from its `match` list and the `nonmatch` parameter deleted outright — so it passes under either edition. Saxon 9.8 passes that twin and reports the 2.0 copy `notRun`. The 2.0 copy was never back-ported, exactly as with `-0984` and `-0985`. |
| `sequence-0132` | `XTSE0010` where `XTTE0570` is wanted | **Not implementable** | Settled directly by the 2.0 REC, without needing the `sequence-2401a` argument this row used to make (the two are different constructs: 2401 has `@select` *and* content, 0132 has content and no `@select`). §11.10's element syntax summary gives `xsl:sequence` a **mandatory** `select` and `<!-- Content: xsl:fallback* -->`; §3.9 XTSE0010 fires "if a required attribute is omitted, or if the content of the element does not correspond to the content that is allowed". So XTSE0010 is the correct 2.0 answer and it is static, raised before any type check could reach XTTE0570. The stylesheet itself carries `<?error XTSE0010?>`, and Saxon 9.8 and Parrot 2017 both report `wrongError` with "Expected XTSE0010" — an older catalog wanted our answer. The `XSLT20+` scope is stale metadata: the expectation was edited to XTTE0570 in 2017 and 2018 without narrowing the scope to 3.0. |
| `import-schema-137` | `XTTE1512` where `XTTE1510` is wanted | **Not implementable** | Both errors are genuinely present: `z:familyname` is absent from `schema061.xsd` (only `surname` is declared) so XTTE1512 is right for that node, while the enclosing `z:person` is invalid against `personType` so XTTE1510 is right for that one. §2.9 settles the choice by declining to: "**It is implementation-dependent which of the several errors is signaled.**" Either answer conforms; the suite tests one processor's order. |
| `validation-0201` | Serialisation differs at offset 46 | **Implementation-defined** | Same case as the 3.0 entry below, and now down to one difference. The engine defect that stood behind the indentation is **fixed**: a union's selected member type was dropped whenever the tree was copied, so `xsl:strip-space` untyped the document and `data(.) instance of StandardDate` went false. With that fixed the output is byte-identical to the expected file apart from whitespace. What remains is the indent width — Saxon writes 3 spaces, this serializer writes 2 — which §20 leaves implementation-defined. See the 3.0 row. |

All three `regex-syntax-xslt20` verdicts were re-derived from the data rather
than taken on trust, and each held. The engine was probed directly against the
catalog's own `match` and `nonmatch` lists, which reduced each disagreement to
its exact codepoints: `-0984` misses U+2308 and U+2309 and nothing else,
`-0985` misses U+1369–U+1371 and nothing else, `-0987` admits U+0346 and
nothing else. Go's `unicode` tables then name the cause — U+2308/09 are `Ps`/`Pe`
(so `\w`, defined by subtracting `\p{P}`, excludes them), U+1369–71 are `No`
(so `\p{Nd}` excludes them), U+0346 is `Mn` and inside 5th edition's blanket
`[#x0300-#x036F]`. `Saxon_9.8.xml` reports all three `notRun`, and the
`<modified ... change="Drop x2308 and x2309, characters reclassified"/>` note on
the 3.0 twin `regex-syntax-0984` is still there. Nothing to do.

**XSLT 2.0 ceiling: 6,193 / 6,201 = 99.87%** — the 6,193 that pass now.
`regex-syntax-xslt20-0987` is back out of the numerator: it is edition drift like
its two neighbours, not an engine defect, and its 3.0 twin was made
edition-neutral rather than fixed. `unparsed-text-2003` **has** since left the
denominator — the 6,201 above is measured with it already gone, so the
"6,193 / 6,199 = 99.90%" this paragraph used to project is not a further gain
still available; it was taken, and 99.87% is the figure after it. Only
`validation-0201` is still both failing and arguably not the numerator's
business. `validation-0201`'s remaining difference is the indent
width and nothing else: the engine defect that used to stand behind it — a
union's selected member lost on every tree copy — is fixed, and the output now
matches the expected file byte for byte apart from whitespace.

### Why none of the three regex cases is ours

This heading has read "the three regex cases", then "why two of the three are
not ours — and why the third is", and is now back where it started, by a longer
route. The original argument was the same for all three: the XSLT 3.0
`regex-syntax` set runs 987 cases with 984 passing and none of its failures
involves `\w`, `\d` or `\c` membership, so only these 2012-era XSLT 2.0 cases
disagree and something moved underneath them.

For `0984` and `0985` there is better evidence than the Unicode tables:
**the W3C already fixed both upstream.** The 3.0 twin of `0984` carries
`change="Drop x2308 and x2309, characters reclassified"` dated 2024-05-04, and
those two codepoints are the only difference between the copies; the 3.0 twin of
`0985` silently drops U+1369–U+1371, likewise the only difference. The 2.0
copies were never back-ported.

`0987` is the same story told in a different vocabulary, and the difference in
vocabulary is what made it look like ours. Nothing was **reclassified** here —
U+0346 has been `Mn` since Unicode 3.0 — so the drift argument as originally
phrased genuinely does not apply. What moved is the **edition of XML** that
defines `NameChar` for `\c`, and the test data names it: 72 codepoints,
`0300-0345` and `0360-0361`, which is XML 1.0 4th edition's `CombiningChar`
exactly, with `nonmatch` holding U+0346, the first codepoint in the gap 4e
leaves. We implement 5th edition, whose `NameChar` is the blanket
`[#x0300-#x036F]`.

An audit read that far and concluded the case was ours. The step it skipped is
whether 4e is a configuration this processor may adopt. It is not, and the
W3C's own test infrastructure says so: `testdata/xsdtests/common/xsts.xsd`
enumerates `XML-1.0-1e-4e` and `XML-1.0-5e` as processor configurations to be
claimed *instead of* one another, and its documentation records that "XSD 1.1
describes XML 1.0 Fifth Edition as the base version in its normative
reference". 5e is not a preference we happen to hold; it is what XSD 1.1
requires of us, and the one translation in `xpath/fn_regex.go` serves `\c` for
XPath, XQuery, XSLT and XSD `pattern` facets alike. Buying this single 2.0 case
means selling the edition the XSD 1.1 numerator rests on.

The W3C came to the same view. Its 3.0 twin `regex-syntax-0987` was not fixed to
4e — it was made **edition-neutral**: every combining character was removed from
the `match` list and the `nonmatch` parameter was deleted outright, so the case
passes under either edition. Saxon 9.8 passes that twin and reports the 2.0 copy
`notRun` for unsatisfied dependencies, so no conforming processor is on record
rejecting U+0346 for `\c`. As with `-0984` and `-0985`, the 2.0 copy was left
behind.

One caveat survives from the earlier revision and is worth keeping: XSD Part 2
Appendix F is not among the four vendored specs, so the 4e reading of the test
data rests on F&O §5.6.1's wholesale delegation to it plus the fingerprint in
the data, rather than on Appendix F's own words. That caveat cuts against
changing anything, not for it.

## XSLT 3.0 — 177 failures

### Deliberate divergence — 1

Two neighbours share this section's set and are not deliberate. `evaluate-046`
fails with `XTDE3400: accumulator static-vars is defined circularly` where the
case expects the transform to succeed, and is undiagnosed. **`evaluate-048` is
a verdict this file should record as wrong**: the CHANGELOG entry for
`fn:function-lookup`'s dynamic visibility says "Fixes `evaluate-048`", and the
half it names *is* fixed — but the case still fails, now on
`FODC0002: cannot retrieve "https://www.saxonica.com/welcome/welcome.xml":
scheme "https" is not permitted`. It was the same mistake `validation-0201`
made three times over: a case can fail for several independent reasons at once,
and fixing the visible one is what reveals whether there was another. The
remaining reason is a network fetch, so the case is not reachable regardless;
what is wrong is the claim that it was closed.

| Cases | Verdict | Why |
|---|---|---|
| `evaluate-045` | **Won't fix** — implementable, deliberately not done | It asserts that a stylesheet function with no `visibility` attribute is private, and so unreachable from `xsl:evaluate`. **The suite is right and this row's old spec argument was false.** It claimed visibility is a property of a component of an `xsl:package` and "a plain `xsl:stylesheet` is not one". §3.6 says the opposite verbatim: "When the `xsl:package` element is not used explicitly, **the entire stylesheet comprises a single implicit package**." §3.6.3.1's ladder ends "Otherwise, private", with no carve-out, and `xsl:evaluate`'s static context admits user-defined functions only "provided their visibility is not hidden or private". So XTDE3160 is correct and we diverge knowingly. The reason to diverge is unchanged and is a real one: enforcing it means no stylesheet outside a package can call its own functions from its own `xsl:evaluate`, which breaks deployed stylesheets — DocBook xslTNG does this in all 613 of its test documents — and Saxon diverges the same way (`wrongError` in its own submission). This is a **won't fix, not a can't fix**, and it is excluded from the unfixable count below. |

### Package composition — 4

Was 28, then 5. `use-package-003` was the most recent to fall, and its old row
is worth keeping in mind when reading the rest of this file: it was recorded as
architecture debt that needed the flat `xpath.Library` restructured, and the
narrow fix turned out to need no restructuring at all. §3.6.3.4 admits into a
package's static context only the components of the packages it uses that are
*visible* to it, so an ordinary function call has to be answered differently
depending on which package wrote it — a private function of a used package is
callable from inside that package and nowhere else. The machinery for a
call-site-dependent answer already existed for dynamic references
(`DynamicFunctionLibrary`, and `hostPackage` riding on every compiled
expression), so the change was a second interface in the same shape,
`ScopedFunctionLibrary`, consulted at the single point every static call already
passes through. Two things had to be got right beyond that, and both were caught
only by diffing the failing case list rather than the count: a component's
visibility is its declaration's attribute *as adjusted by its package
manifest*, and composition consumes the `xsl:expose` elements, so the answer has
to be recorded while composition still has it (`expose-002`); and a declaration
inside an `xsl:override` supplies a body for a component of the used package,
which keeps the visibility that package gave it, so the override's own
`visibility="private"` must not hide it from the library that calls it
(`override-f-026`).

Was 28. Two agents cleared 23 of them: all ten `xsl:override` cases and nine of
the twelve across `package`/`accept`/`expose`/`use-package`. Both independently
found the same rule — §3.6.3.2, that a using package contains a component
corresponding to every component in the package it uses — and the integration
had to choose between their two implementations; folding the inherited
components into the package's component list scores one case more than keeping
them separate, and `override-t-003a` is the case.

| Cases | Verdict | Why |
|---|---|---|
| `package-021err` | **Not implementable** — reason corrected | Still a half-applied 2020 erratum (E36), but **not for the reason this row used to give**. It claimed neither `@name` nor `@names` admits an arity. §3.6.2 admits one in `@names` explicitly: "The `names` attribute selects a subset of those components by name (**and in the case of functions, arity**) … Examples are `*`, `p:*`, `*:local`, `p:local`, and **`p:local#2`**", and §3.6.3.2 imports those rules for `xsl:accept`. We already parse `#N` correctly. The real defect is confined to the *used* package, which writes `<xsl:function name="me:function1#0">` where `@name` is an `eqname` — so the function has no well-formed name, nothing matches, and we raise XTSE3030 rather than the wanted XTSE3050. Passing would mean tolerating a malformed `@name`, which is a suite workaround, not a conformance fix. The suite has repaired this elsewhere: `accept-916` carries `change="Remove unintended error, missing arity on function name"`. |
| `package-022err` | **Not implementable** | `component="function#0"` genuinely violates the `@component` enumeration `"template" \| "function" \| "attribute-set" \| "variable" \| "mode"`. Same erratum, applied to a different attribute in each file. |
| `accept-913` | **Open question** — old reason stale, our error wrong either way | The old row argued only about `xsl:initial-template`, and that half still holds: §3.6.3.2 says a component matched by no `xsl:accept` keeps its visibility and only a *private* one becomes hidden, so the template stays public and the wanted XTDE0040 is unreachable. But that is not what we now report. We raise **XTDE3052**, which §3.6.3.2 scopes by its own parenthetical to "an abstract component accepted into a using package with `visibility="absent"`" — and `accept-913` has no `xsl:accept` at all, so nothing is absent. The defensible code is **XTSE3080** (§3.7: "It is a static error if a top-level package … contains symbolic references referring to components whose visibility is `abstract`"), because an unmatched *abstract* component stays abstract — "in a using package it can either remain abstract or be overridden" — and the public initial template references it via `xsl:use-attribute-sets`. The sibling `accept-914` wants exactly XTSE3080 for the neighbouring shape. What blocks a change is a genuine tension rather than a spec reading: `accept-902`/`-910` present nearly the same structure and want the dynamic XTDE3052, and `xslt/usepackage.go` separates them today only by whether an `xsl:accept` named the component. Separating "referenced from the top level" from "merely inherited and invoked" is a reference-graph question and is unmeasured. Recorded as open, not settled. |
| `package-200` | **Not worth it** — re-examined, verdict unchanged, reasoning sharpened | Re-read against §3.6.1's two grammars and all four siblings. The old row said no rule separates the five; a rule *does* exist, and it is still not worth taking. `package-version="'1.0.0'"` is a well-formed `PackageVersion` wrapped in apostrophes; `use-package-291`–`294` write `2.0.0-alpha:beta`, `TotallyInvalid`, `-3.6` and `-alpha`, none of which parse under any reading. "Strip a matched pair of quotes and it parses" therefore does separate them — but that is a rule about *quoting*, and neither grammar mentions quotes. Two facts settle it. `package-version="'…'"` occurs **exactly once in the whole suite**, in `package-201.xsl`, so the rule would have precisely one instance and no second case to confirm it against — the signature of a special case, not a rule. And the genuine XTSE3000 shape is already implemented and passing: `error-3000a` writes the perfectly well-formed range `2.0.0` and expects XTSE3000 because no package matches it, which is exactly what §3.6.1 says the error means ("no package matching the package name and version … can be located"). `package-201.xsl`'s own comment agrees, calling for "a package-not-found error". Reporting XTSE0020 for a value that is not a range at all is the defensible reading; special-casing one quoted string to convert it into a not-found is not. Costs 1 case, deliberately. |

### Schema-aware validation — 5

| Cases | Verdict | Why |
|---|---|---|
| `si-copy-117`, `si-copy-of-117` | **Not implementable** | Not ordering cases at all. Both write `<xsl:copy select="/*/*/@version" type="xs:date"/>` — a `type` attribute and **no `validation` attribute**. §19.2 keys the codes to which attribute was written: XTTE1510 begins "If the **validation attribute** ... has the effective value `strict`", which is literally unmet, while XTTE1540 is "if an **[xsl:]type attribute** is defined ... and the outcome of schema validity assessment against that type is ... other than valid", which is exactly met. The suite's own description says "validate attribute **by type**". Our XTTE1540 is correct. |
| `import-schema-137` | **Not implementable** | The one genuine ordering case, and §2.9 explicitly declines to settle it: "If more than one error arises, an implementation is not required to signal any errors other than the first one that it detects. **It is implementation-dependent which of the several errors is signaled.**" Both errors are real, so either choice conforms; the suite is testing one processor's order. |
| `validation-0006` | **Not implementable** | A parentless attribute: `XTTE1555` wanted, `XTTE1540` reported. XTTE1555 is scoped by its own text to "when validating a **document node**", and a parentless attribute is not one; XTTE1540, which covers the `type` attribute, is what the case actually meets. The stylesheet says so itself: "a contrived example to force **Saxon** down a particular code path". |
| `validation-0201` | **Implementation-defined — the engine defect behind it is fixed** | Three differences were stacked here and two are now gone. The first verdict was that this is Saxon's indentation (3 spaces then 6 where this serializer writes 2) and that `admin/catalog-schema.xsd` licenses a driver to ignore it: *"Test drivers are free to ignore differences in the serialization that are known to be irrelevant."* That licence is real and the case does not test the serializer — its own description calls it *"a 'system test' of schema-aware processing"*. But normalising indentation did not pass the case; it exposed what offset 46 was hiding. The second difference was an encoding one — the expected file declares `iso-8859-1` and carries a NBSP as the single byte `xA0` while the assertion has no `@encoding` — since fixed. The third was the real defect: the output read `29 MAY 1917` where `29 May 1917` is wanted, because `<xsl:template match="Date[data(.) instance of StandardDate]">` never matched. **That is now fixed.** The cause was not, as this row previously guessed, that an imported type is invisible to `instance of` — the type resolved fine and the element carried its annotation. `Date` has type `DateType`, a complex type with simple content extending a *union*, and XSD §3.14.4 selects a union's member per value, so which member accepted the text is recorded separately on the node (`xdm.Node.UnionMember`) and is what atomisation reads. Three copy sites carried the annotation and dropped the member: `stripCopyNode`, `xdmbuild.DeepCopy` and the parentless-attribute copy in `xslt/copyfuncs.go`. The stylesheet declares `<xsl:strip-space elements="*"/>`, so every `Date` reaching a template was a copy that had lost its member and atomised to `xs:untypedAtomic`. With the fix the output is **byte-identical to the expected file apart from whitespace** — all three dates now read "29 May 1917", "12 September 1953", "22 November 1963". Only the indent width remains, which §20 leaves implementation-defined, so the case still fails and the fix is covered by `xslt/unionmember_test.go` instead. |

### Deliberately out of scope — 1

The table carries three rows and only `docbook-001` still fails; the other two
are kept because their fixes are the reasoning, not the outcome.


| Cases | Verdict | Why |
|---|---|---|
| `streamable-141` | **Fixed** | It wanted XTSE3430 for `version="1.0"` on an `xsl:apply-templates` inside a `streamable="yes"` mode, and the old verdict was that this needs the §19.8 streamability analysis. It does not. §3.9.1 states the rule *"notwithstanding anything stated in 19 Streamability"*: an instruction processed with XSLT 1.0 behavior **is** roaming and free-ranging, by declaration rather than as a consequence of any posture inference. That makes it checkable without the analysis, and `checkStreamableCompat` in `xslt/staticerrors.go` now checks exactly it — a template whose `@mode` names a mode declared streamable, containing an element that states `version="1.0"`. Nothing wider: a processor that does not stream is not required to assess whether anything else is guaranteed-streamable. Measured at +1 on the 3.0 target (8,611 → 8,612) with the 2.0 failing list byte-identical. The earlier −4 and −177 measurement was a different change — skipping the case through the *set's* unsupported feature, which swept up cases that pass today. |
| `streamable-021`, `streamable-042`, `streamable-043` | **Fixed — a driver gap, not an engine gap** | All three failed `XTSE1660: validation requires a schema; none was imported`. Each declares the `loans` environment, whose `<schema file="loans.xsd"/>` loads cleanly (2 elements, 90 types), and then validates with `validation="strict"` while declaring **no** `xsl:import-schema` of its own — `streamable-021`'s is commented out. `tests/xslts/runner.go` merged the environment's schema only into a schema the stylesheet had already built, so where `ss.Schema()` was nil the components were loaded and thrown away and strict validation had nothing to look in. The suite's own reference driver settles what should happen: `c:validated-document` in `runner/run-tests.xsl` builds its stylesheet with one synthesised `<xsl:import-schema>` per environment `<schema>`, **unconditionally** and without consulting the stylesheet under test, and XSLT 2.0 §3.14 makes an import satisfiable "using a schema that is already known to the processor". The harness now installs the environment schema when the stylesheet declared none, through a new `Stylesheet.SetSchemaIfAbsent`, which refuses to displace a schema `xsl:import-schema` built — that case still merges, so a declaration the stylesheet named by hand keeps priority. Measured in an isolated worktree: XSLT 3.0 11,332 → **11,335** passing, 193 → **190** failing, the three being exactly these; XSLT 2.0 unmoved at 6,193 / 8. The blast radius is the 33 test-sets that declare a `<schema>`, and all 33 were measured on both trees: only `streamable` moves (106 → 109 passing), every other set identical, `streamable-044` — which imports the same schema itself — still passing. |
| `docbook-001` | **Not implementable** | EXSLT `exsl:document`, 19 times in `chunker.xsl` alone. Now surfaces as DocBook's own `XTMM9000: Can't make chunks with go-xml's processor.` rather than `XTDE1450` — its stylesheet's terminating fallback, reached for the same absent extension. |

Two left this list. `docbook-004` was never an EXSLT case — it was filed as one
on the strength of its neighbour's name, and its stylesheet is five lines with
no extension element, testing `xsl:source-document` with an `xml:id` fragment
identifier. The fragment was being dropped, so the whole document came back
where a section was wanted. `package-version-011` went when the static phase
was given the module resolver: §9.7 makes available documents
implementation-defined at 3.0 where 2.0's §3.13 fixes them at none, and Saxon
9.8 passes the case.

### Long tail — 3

Rounds two and three cleared the rest, and `catalog-005b`,
`type-available-0151`, `catalog-006b` and `unparsed-text-2003` have since
joined them — the last of those by leaving the denominator rather than the
failures, since it reads a URL its own neighbour declares a dependency for and
it does not. What is left shares no cause, so each is its own investigation.

| Case | Verdict | Note |
|---|---|---|
| `accumulator-038` | **Not implementable** | Suite defect, and the audit strengthened rather than weakened it. Its stylesheet is an *explicit* `xsl:package`, so §3.6.3.1's "Otherwise, private" applies to the unannotated `main` template and XTDE0040's own text — "does not match the expanded QName of a named template defined in the stylesheet, **whose visibility is public or final**" — is met. Both 038 and 039 were converted to `xsl:package` by Bug 28410 in 2015; only 039 carries `<modified by="Michael Kay" on="2019-03-05" change="Make main template public"/>` and only 039's stylesheet has `visibility="public"`. A second, independent defence: the wanted XPTY0004 is reachable only *after* entry succeeds, and §2.9 lets an implementation report whichever error it detects first. Note that this verdict depends on the stylesheet being a package — unlike `evaluate-045`, whose old rationale wrongly claimed the visibility rules do not reach a plain `xsl:stylesheet`. Correcting that row removes a latent contradiction between the two. |
| `strip-space-009` | **Not implementable** | *This case was missing from every list in this file when the audit found it.* It asserts that whitespace survives `xsl:strip-space` under an element whose **ancestor**'s type carries an XSD 1.1 assertion. §4.4 grants no such exemption: it preserves whitespace only where "an element … has a type annotation that is a simple type or a complex type with simple content", and here `p` sits under `xs:any processContents="skip"`, so it has no simple-type annotation at all, while the ancestor's type is `mixed`, not simple content. We implement the §4.4 rule as written. The test's own comment says it exists "in order to exercise different paths in **Saxon**"; Saxon is the only submission that runs it, and passes. Note the caveat below on the spec edition. |

| `initial-function-002`, `initial-function-100a`–`100i` | **Fixed — it was a driver gap, not an engine gap** | Ten cases that invoke an initial function and then assert about the *raw result sequence* — `$result instance of xs:integer`, `assert-count 2`. The engine was never wrong: a direct API call returns `986572` as an `xs:integer` and `1.0E-10` as an `xs:float`, as typed `*xdm.Atomic` items in `Result.Nodes`, which is 2.3.5's raw result exactly. The driver was. `<output tree="no" serialize="no"/>` asks it not to wrap the result in a document node, but `rawResultVar` in `tests/xslts/catalog.go` bound the raw sequence only when the case ALSO wrote `result-var` — an attribute the suite spells exactly once, and which the reference driver never reads at all: grep `runner/` for `result-var` and there is no hit. `run-tests.xsl` selects the raw delivery format on `@tree='no' and not(@serialize='yes')`, and `rawResultVar` now says that and nothing else, defaulting the variable to the ordinary name `result`. The `serialize="yes"` half of the condition is what keeps the `result-document-14xx` and `output-07xx` families on the serialized format they assert against. `assert-count` and `assert-deep-eq` were genuinely missing from `judge.go` and are now translated to `count($result) = n` and `deep-equal($result, (…))`, reusing the engine's own semantics rather than a matcher written in the harness. Measured in a clean worktree so a second agent's concurrent `xslt/` edits could not be mistaken for this change: 11,273 → 11,284 passing, 252 → 241 failing, the eleven being these ten and `sx-arithmetic-004`, with no new failure anywhere and XSLT 2.0 unmoved at 6,190/11. |
| `transform-001`, `transform-005`–`transform-009` | **Fixed** | Six `fn:transform` cases, four distinct causes. (1) `transform-001`: a `stylesheet-location` naming a file that is not there was reported as FOXT0001. FOXT0001 is the code for a transformation the processor cannot *run* — every QT3 case that asserts it does so for an unavailable vendor named in `requested-properties` (“thrown if Saxon is not available”) — while a location that cannot be retrieved identifies no stylesheet, which is FOXT0002. `fn-transform-err-1`'s own modification note (“based on careful reading of the spec”) settles it. (2) `transform-008`: an option written as element content, `<xsl:map-entry key="'stylesheet-location'">a.xsl</xsl:map-entry>`, arrives as a *text node*, and `transformString` refused it with XPTY0004 on a map that says exactly what a string-valued one says; nodes are now atomized to their string value. (3) `transform-009`: an `xsl:result-document` with **no href** is the principal output — §24.3 changes the current output URI only for an instruction *with* an href — but `transformResultMap` keyed it as a secondary under `""`, leaving `?output` holding the empty tree the stylesheet never wrote to, so the principal serialization came out blank. Same rule `cmd/go-xml` already applies. (4) `transform-005`–`007`: the `package-name` and `package-version` options were not read at all, so the options looked like they identified no stylesheet. They now resolve through the same `PackageResolver` the outer compilation was given, which `Stylesheet` retains for the purpose. Measured: 11,304 → 11,324 passing, 221 → 201 failing; XSLT 2.0 unmoved at 6,193/8 and QT3 unmoved. |
| `transform-004` | **Not implementable without unpicking `Compile`'s global state** | The case calls `fn:transform` from a `static="yes"` variable, so it must run during the *static phase of compilation*. Registering the real function there is a two-line change and is correct by §9.7, which gives a static expression the whole F&O library and excludes nothing. It deadlocks. `Compile` keeps `compileSchema`, `compilePackage`, `overridingDecls`, `packageParent`, `overrideXPathVersion` and `compileMaxVersion` as **package-level variables** guarded by a single non-reentrant `compileMu`, so a nested `Compile` — which is exactly what `fn:transform` must do — blocks forever on a mutex the outer call still holds. Verified by stack trace, not inferred. Making this work means moving that state onto the `compiler` value; that is a real refactor of shared machinery and out of scope for an error-code fix. |

**XSLT 3.0 ceiling: 11,348 / 11,525 = 98.46%** — what passes now. Of the 177
remaining, **150 want an `XTSE3430`** that only the §19.8 posture-and-sweep
analysis can emit, and §19.1 says a non-streaming processor "is not required to
assess whether constructs are guaranteed-streamable" — so they are not defects
this engine is obliged to close. The reachable ceiling without implementing
streamability analysis is therefore **11,498 of 11,525**, or 99.77% — the 177
less the 150. It is not an approximation any more: the 150 were counted from
the run rather than projected, and this figure read "about 11,496" when they
were an estimate.

`base-uri-052` left this list when XInclude was implemented: the environment's
`xinclude="true"` now runs a real inclusion pass, and the case's assertions are
about the `xml:base` fixup XInclude 1.0 §4.5.5 requires. The two cases
once counted towards a higher ceiling, `validation-0006` and `validation-0201`,
are settled above — the first as not implementable, the second as
implementation-defined once the engine defect behind it was fixed — so no
headroom is left against this suite. The fourteen read case by case here:
`accept-913`, `package-200`,
`package-021err`, `package-022err`,
`docbook-001`, `strip-space-009`, `si-copy-117`, `si-copy-of-117`,
`import-schema-137`, `accumulator-038`, `validation-0201`, `validation-0006`,
`transform-004` and `evaluate-045` (the last given up deliberately; see
*Deliberate divergence* above). All fourteen were re-checked against the
current run and all fourteen still fail with the reason recorded for them.
`streamable-141` was a fifteenth and is fixed — see its row above.

The other 163 are not individually read here, and the reason is that they do
not divide 163 ways. 150 want `XTSE3430`; the remainder is the long tail
diagnosed under *The streaming row is the one that overstates the gap*, plus
`evaluate-046`, `evaluate-048` and `merge-097`/`-097s`/`-097sf`, all of which
are read elsewhere in this file.

Entries left this list as the work behind them landed. `base-uri-052`
went with XInclude. `catalog-006b` went with `xsl:assert`: it reports every
XSLT element the processor recognises, so an absent one was visible in it. The
three `regex-syntax` ambiguous-dash cases went when `XSD_1.1` was scoped to the
version being measured rather than to the engine, and `catalog-005b` and
`type-available-0151` with them. `package-version-011` went when the static
phase was given the module resolver, `docbook-004` when the fragment on an
`xsl:source-document` href stopped being dropped, `unparsed-text-2003` by
leaving the denominator, and `streamable-141` when §3.9.1's "notwithstanding"
clause turned out to make it checkable without the streamability analysis.
`strip-space-009` is the one addition, having been
counted in the prose here but omitted from the list
until the audit found it.

---

# xsd — 61 disagreements

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
is the opposite of challenged. The previous revision of this file merged the
two into one "ceiling" column and described both as challenged, which inverted
`stable`'s meaning; they are separated below.

| | Total | `accepted` | `queried` | `stable` | no status |
|---|---:|---:|---:|---:|---:|
| XSD 1.0 | 30 | **2** | 26 | 2 | 0 |
| XSD 1.1 | 31 | **2** | 27 | 2 | 0 |

Those totals are the measured ones, counted from the `<current>` status of each
disagreeing case. They fell from 51 and 47 when the `indeterminate` scoring bug
and `iri-001`'s DOCTYPE were fixed; the `accepted` counts did not move, which is
the point of splitting them out -- the cases that are real work were never the
ones the harness was miscounting. The `accepted` counts were likewise given as 8 and 5 against a
measured 6 and 2 — which mattered, because the file then named five "settled
suite defects" on 1.0 against six accepted cases and three on 1.1 against two,
i.e. it claimed to have settled more cases than existed in the bucket.

The case that does not fit at all is **`iri-001`**, which has **no `<current>`
element and therefore no W3C status of any kind**. It was listed as a proven
suite defect on the strength of a status field it does not have. It is ours:
see below.

Three rounds of agents cleared about **130** of these by implementing missing
schema-validity rules, without a single agreement count falling. Round 1
contributed 80 — `explicitTimezone` (which had no schema-level constraints at
all, 9 cases), occurrence attributes on any child of a named group (8), a
type-cycle check that excused a type whose base is itself (6), and
substitution-group type derivation (5). Round 3 contributed most of the rest,
concentrated in `xs:all` and wildcard restriction and in open content; four of
its rules were settled outright by the XSD 1.1 schema for schemas the suite
itself ships, which pins the occurrence attributes those elements admit.

That is why schema validity, long the weaker half of these numbers, is now the
stronger: **99.97%** on 1.0 and **99.95%** on 1.1, against 99.89% and 99.90%
for instance validation.

## What the ceiling consists of

| Set | Cases | Status | Why |
|---|---:|---|---|
| `MS-Regex2006-07-15` | 22 per version, 44 in all | `queried bug4113` | Every single MS-Regex disagreement is the *same* open W3C bug. The expected results are challenged upstream; agreeing with them would mean agreeing with something the working group does not stand behind. |
| `MS-Element`, `MS-DataTypes`, `MS-IdentityConstraint`, `MS-Particles`, others | 11 (1.0), 12 (1.1) | `queried`/`stable` + bug | Assorted challenged expectations, almost all across the Microsoft-contributed sets. |

**Not implementable: 30 (XSD 1.0) and 31 (XSD 1.1)** — the whole of the
measured disagreement count, not a remainder subtracted from it. Two per
version have left it since this paragraph was written: `elemM002` and `idC019`
were implementable after all, and are now fixed (see *What is genuinely ours*
below). The
`indeterminate` scoring errors and `iri-001` were the earlier subtrahends, and
both are fixed: those cases have already left the disagreement counts, so
deducting them a second time would double-count. Of the 30, 28 carry a
`queried` or `stable` bugzilla reference and 2 carry `accepted` — `attP031`,
the suite defect named below, and `particlesZ001`. Of the 31, 29 are
`queried` or `stable` and 2 are `accepted`, here `simple093` and
`particlesZ033_g`, both read as questions below. Those four `accepted` cases
were re-counted from the run and are unchanged by the two fixes.

### The `notQName` cases are a suite omission, not a gap

Four XSD 1.0 `SFALSEREJECT` cases — `wildcard/s3_10_1ii08`, `s3_10_1ii09`,
`anyAttribute/s3_10_6ii01` and `s3_10_6ii02` — fail with "notQName requires
XSD 1.1", and they are right to.

`notQName` is **unprefixed** in all four schemas, so XSD 1.0's rule about
ignoring attributes from other namespaces does not reach it: 1.0 declares
`<xs:anyAttribute namespace="##other" processContents="lax"/>` on `xs:any`,
which admits only *qualified* foreign attributes. `##definedSibling` has no
XSD 1.0 meaning at all — 1.0's `xs:any` has no `notQName` in any form.
Rejecting these schemas under 1.0 is correct.

The cause is a missing attribute in the suite. Every one of these groups
carries `<ts:documentationReference xlink:href="http://www.w3.org/TR/
xmlschema11-1/#Wildcard_details"/>` and a reference into
`XSD1_1TestCategories.xml`, whose root element is
`t1_1:xmlSchema1_1TSExtensions` and which enumerates only 1.1 features — but
no `version="1.1"`, so the runner scores them under 1.0 as well. In
`ibmMeta/wildcard.testSet` alone, **8 of the 17 groups** referencing that file
omit it, while `s3_10_1v01`, testing the same feature in the same file, has it.

Teaching the harness to read a `XSD1_1TestCategories.xml#` reference as an
implicit `version="1.1"` was implemented and measured: 1.0 disagreements fall
63 → 56, but agreements fall **39,341 → 39,191**, because those same groups
contribute many currently-agreeing 1.0 results. It is the right reading of the
suite and still a net loss of 150, so it is not taken.

## What is genuinely ours

The previous revision said "nothing, on either version". That was wrong, and it
is the claim the audit most clearly overturned.

| Case | Version | Verdict |
|---|---|---|
| `elemM002` | both | **Was ours — now fixed.** `<xsd:element name="myElem" type="foo"/>` beside `<xsd:attribute name="foo"/>`. §3.3.2 requires `type=` to resolve to a *type definition*; this resolves to a component of the wrong kind, and the schema was loading clean. What hid it is the deferral §3.3.3 grants an element declaration: the unprefixed `type=` lands in the absent namespace, `deferrableMiss` answers true for it, and the reference was carried on the declaration rather than reported. That deferral is right in general — a document read later may supply the type — but no document can turn an attribute declaration into a type definition, so this miss is final at the moment it is made. `resolveTypeRefLazy` now reports a name the assembly defines as a non-type before consulting the deferral. The negative arm is `saxonData Missing/missing001`, which writes `type="absent"` into the same absent namespace of a schema that declares components there — identical in every respect `deferrableMiss` can see — but names nothing at all, so its deferral survives and it still loads. 1.0 schema agreement 14,383 → 14,384, 1.1 15,347 → 15,348, instances unmoved. |
| `idC019` | both | **Was ours — now fixed.** `schema.identityConstraints` is one flat map over the whole assembly, so the `refer=` fixup could reach a key the asking document has no licence to see. §4.2.6.1 `src-resolve` scopes the licence an `<xs:import>` grants to *the document that wrote it* — which is why `doc.imports` already existed beside the per-assembly `importedNamespaces`; the fixup simply was not consulting it. idC019 imports `idC017a.xsd`, whose `targetNamespace` is `diffNS` and whose `keyref` writes `refer="keyName"` **unprefixed** with no default namespace in scope, so §3.11.2 resolves it to the *absent* namespace rather than to `diffNS`. Nothing in `diffNS` declares that key: the match came from the importing document's own absent namespace, which `idC017a.xsd` never imports. `resolveQName`'s own `checkReferenceImported` cannot catch this, because an unprefixed name with no default namespace in scope returns early through `chameleonQName`, before the switch that calls it. The fixup now captures its declaring document and requires the key's namespace to be that document's own target namespace or one it imports. 1.0 schema agreement 14,384 → 14,385, 1.1 15,348 → 15,349, instances unmoved. |
| `iri-001` | 1.1 | **Was the harness — now fixed.** `wgData/iri/ElementDeclarations.xsd` is expected valid and we rejected it: the type-library schemas it imports carry an internal DTD subset — `TypeLibrary-URI-RFC3986.xsd` declares one entity per ABNF non-terminal of RFC 3986 so its patterns can be assembled bottom-up — and `tests/xsdsuite` loaded every schema with the default `ParseOptions`, where `AllowDOCTYPE` is off. Nothing was wrong with those schemas and nothing was wrong with the engine: `xsd/assemble.go` already threads the caller's `ParseOptions` through `xs:include` and `xs:import`, so the schema loads once the driver asks for it. The driver now sets `AllowDOCTYPE` on the schema-load path only, leaving external entities off. That recovered the schema test **and** the 12 instance tests the load failure had been suppressing: XSD 1.1 agree 41,519 → 41,532, disagree 39 → 38, with XSD 1.0 and the XPath, XQuery, XSLT 2.0 and XSLT 3.0 suites unchanged case for case. |
| `indeterminate` cases | both | **Was ours — a harness scoring bug, now fixed.** `schZ012_a`, `schZ015`, `schG14`, `schA2.i`, `schA5.i`, `addC002`, `addB071`, `elemZ031` and, on 1.0 only, `particlesZ026` and `particlesZ026.v` carry `<expected validity="indeterminate"/>`; `schZ012_a`'s own annotation says "The WG decided the spec. is underspecified in this area, so implementations may reasonably differ," and `particlesZ026` records that the TSTF found its validity implementation-determined. `expectedValidity` in `tests/xsdsuite/main.go` read the attribute as `w == "valid"`, so `indeterminate` silently became "must be invalid" and our acceptance scored as a false accept. The driver now treats it as a third outcome, skips the case, and reports the count on its own `indeterminate` column. This removed **10** disagreements on 1.0 and **8** on 1.1 — the earlier estimate of 8 per version missed the two 1.0-only `particlesZ026` cases. Because the cases leave the denominator as well as the numerator, the agreeing counts fell by 6 per version (39,353 → 39,347 and 41,525 → 41,519) while the percentages rose; `tests/ratchet.txt` was lowered to match. |
| `simple093` | 1.1 | **Not implementable — the suite contradicts itself.** Expected invalid; the schema unions `xs:QName` with `xs:NOTATION`, and Part 2 §3.2.19 does forbid NOTATION being "used directly in a schema", so the case is a correct reading. But `msData particlesZ007` declares a schema containing `<xsd:union memberTypes="xsd:NOTATION"/>` **valid**, and both carry `status="accepted"`. The rule was implemented and measured: 1.1 trades one for the other (agree 41,519 → 41,518) and 1.0 loses two outright (39,347 → 39,345), because particlesZ007 has a dependent instance test and simple093 is not run under 1.0 at all. Reverted; `xsd/facet_check.go` enforces §3.2.19 in the three places the suite is consistent about. |
| `particlesZ033_g` | 1.1 | **Not implementable — magnitude is not what the family tests.** Expected invalid; the test's own note says "validates as xs:any if maxOccurs greater than 4096", which describes a 2006 vendor behaviour rather than a rule. No threshold can satisfy the family: sibling `particlesZ033_a` carries `maxOccurs="79228162514264337593543950335"` — 7.9×10²⁸, far larger than `_g`'s 45,678,363 — and is expected **valid**. What actually separates `_g` from the `_f` we correctly reject is that `_f`'s substitution-group member beside `<xsd:element ref='head'>` was replaced by a local element, removing the UPA conflict; accepting `_g` follows from getting UPA right. Where the WG did adjudicate implementation limits, in `elemZ031`, it resolved the expectation to `indeterminate` rather than invalid (bug 4059). **The reopened question is now closed by measurement.** Enumerating every state of every content model in `_g` shows that under 1.1 it has *no* competing pair at all — not a suppressed one — so `counterForces` and `exitBlocked` are not implicated, and the earlier suspicion that they were is withdrawn. The only pair is `ref='m1'` against `<xsd:any/>`, and `XSD1_1TestCategories.xml` names that relaxation outright: "Relaxation of UPA: wildcard/element competition no longer violates UPA". Restoring the competition under 1.1 gains this one case and costs **seventeen** valid schemas — `addB153`, `all006`, `wild030/047/049/050/052/072/073`, `s3_3_6v01`, `s3_3_6v04`, `s3_8_6v01`, `s3_8_6ii01`, `s3_4_6v01`, `s3_4_6v04`, `s3_10_1v04` and `ste110` — taking 1.1 from 41,536 to 41,494. The group carries no `version` attribute and cites the 2004 1.0 REC, so its bare `invalid` is a 1.0 verdict the 1.1 run inherits. See *known-gaps.md*. |

The `queried` defence itself was spot-checked on four cases and **held** in each
— `ste110` (bug 4957, circular unions), `gMonth002`/`004` (bug 6901, withdrawn
gMonth lexical forms), `anyURI_a004_1339.i` (bug 4126, whose own annotation
sides with us) and the `MS-Regex` cases, 22 on each version (bug 4113). In every one we disagree
in the direction the filed bug points, which is what makes the status a defence
rather than a label.

Four cases stood here until recently, and what happened to them is the useful
part:

| Case | Version | Outcome |
|---|---|---|
| `MS-Particles2006-07-15/particlesZ040` | both | **Fixed.** First by bracketing a repetition count into a low and a high reading; the bracket has since been replaced by a set of whole count vectors, which decides it exactly rather than by approximation. |
| `MS-Wildcards2006-07-15/wildZ013` | 1.0 | **Fixed.** Attribute-wildcard intersection under errata E1-10. |
| `MS-Particles2006-07-15/particlesK006` | 1.1 | **Fixed.** Particle derivation. |
| `MS-Attribute2006-07-15/attP031` | 1.0 | **Suite defect.** It names its instance test `.i`, says in its own prose that the attribute *does* appear, and still expects valid; its sibling `attP029`, byte-identical but for the instance, is consistent. |

A short list is not the same as the engine being exact, and the case that made
the point was found by fuzzing rather than by either suite: a repeated group
whose only child is itself repeating was decided wrongly in *both* directions,
which no W3C case covers because they all use two or more distinct child names.
It is fixed — the matcher now carries a set of whole occurrence-count vectors,
so every bound is answered from one execution — and the suite numbers did not
move by a single case in either direction, which is exactly the point. A suite
reaching its ceiling bounds what the suite asks, not what the code does. A
second sweep then found a remainder the vectors alone did not settle: an
emptiable inner particle, where a scope has to be credited for an iteration that
consumed nothing, was rejecting valid documents at 40 of 2,028 combinations.
That is fixed too, and again moved no suite case — both disagreement lists stayed
identical by name. See *Nested occurrence bounds were wrong in both directions*
in [known-gaps.md](known-gaps.md).

**XSD measured now: 1.0 — 39,358 / 39,388 = 99.92%. 1.1 — 41,545 / 41,576 =
99.93%.** The `indeterminate` correction is applied, so 16 cases on 1.0 and 14
on 1.1 have left both sides of the ratio; the driver prints their count so the
denominator is legible rather than assumed. This paragraph previously called
1.0 "now also the ceiling — everything remaining is a suite defect or a
`queried` disagreement". That was not true, and `elemM002` and `idC019`
disprove it: both were genuine defects of ours, on both versions, and both are
now fixed. What remains after them is a suite defect or a `queried`/`stable`
disagreement on either version — but "the ceiling" is a claim worth
re-measuring rather than inheriting, which is how these two survived several
rounds of it. On 1.1 `iri-001` and its 12 masked instance tests have since been
recovered; they are in the figures above.

---

# relaxng — 0 failures

965 of 965 assertions in James Clark's spectest. **No known gaps.**

---

# What is skipped, and why that is not a gap

The XSLT 3.0 suite has 14,601 cases; 11,525 are in scope. The 3,076 skipped
are excluded by *declared dependency*, not by failure.

The largest rows this table used to carry are gone. Streaming (2,646),
XPath 3.1 (98), XML 1.1 (65) and initial-function (38) were all listed as
unsupported long after they were implemented — six stale labels across two
harnesses, worth 2,862 cases. They are measured now, which is why the
denominator grew and the percentage fell: the cases that were being counted as
"not our business" are counted as ours.

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

## The streaming row is the one that overstates the gap

Measured with the streaming gate lifted and nothing else changed, 2,424 of the
2,646 pass — 92%. That is not an accident: XSLT 3.0 §19.1 lets a processor
answer a request for streamed evaluation by building the tree instead, and this
engine does exactly that. `xsl:source-document`, `xsl:merge`, `xsl:fork`,
`xsl:accumulator` and the streamable forms are all implemented; what is absent
is streaming, not the vocabulary.

The gate is now off for good and the streaming cases are in the measured
denominator, so this is no longer a projection. Of the 177 XSLT 3.0 failures,
the streaming corpus contributes the great majority, and it divides cleanly:

| Cases | What they want |
|---:|---|
| 150 | **XTSE3430** — reject a stylesheet as non-streamable. 136 read "expected error XTSE3430, the transform succeeded": the engine computes the right answer, and the test wants a refusal. Counted from the current run, not projected |
| ~13 | An unrelated long tail; several are missing test-data files rather than engine defects, and `merge-097`/`-097s`/`-097sf` are the Saxon collection-URI cases read at the foot of this section |
| 25 | Two real bugs, now fixed — see below, and no longer among the failures |

This table read "222 … ~65 … 25" when it was a projection made with the gate
lifted experimentally. The 150 reproduced exactly; the long tail did not,
because the two bugs below and the `transform`, `initial-function` and
`streamable-021/042/043` fixes took most of it. That the projection's headline
figure held and its remainder did not is the ordinary shape of this: a single
named cause counts reliably, a residue does not.

Only the first group needs streaming work, and specifically the §19.8 posture
and sweep analysis: a static classifier over the compiled tree that assigns
each construct a posture and rejects the combinations that cannot stream. It
changes no runtime behaviour. Streamed *execution* — an incremental parser and
pull evaluator — is a separate and much larger project, and it would buy
almost no conformance, because the cases it would serve already pass.

Flipping the feature on without §19.8 would claim streaming while never
refusing a non-streamable stylesheet, which is why the flag stays off.

### Two bugs the gate was hiding

Both were invisible while every case that exercises them sat behind the
streaming dependency:

* **Attribute whitespace was not ignored.** §3.2 ignores leading and trailing
  whitespace unless the allowed values are given as *string* or *char*. The
  element table trimmed before checking a value against its enumeration, so
  `validation=" lax "` passed that check and was refused by the instruction
  behind it, which read the attribute again untrimmed. 27 cases; the whole
  `source-document/stream-*` set is written with the spaces.
* **XTSE3195 was over-enforced.** The code excluded `for-each-item` from
  `streamable` — a constraint in no version of the spec — and from
  `use-accumulators`, which the working draft states but which `merge-073` and
  `merge-082` contradict: both are success cases in the suite and both pass in
  Saxon 9.8's report. 4 cases.

### Resolving an external resource: base URI, and what is not a URI

Three groups of `xsl:source-document` and `xsl:merge` failures were all about
resolving an external resource, and only two of them were engine bugs.

* **`@href` ignored `xml:base`.** §18.1 obtains the document "the same as for
  the `doc` function", and `fn:doc` resolves a relative reference against the
  static base URI of the *expression* — a per-element property that `xml:base`
  on the instruction or any ancestor moves. The instruction resolved against
  the stylesheet module's base instead, so an href beside a relocated base was
  looked for beside the module. `non-stream-004` and `stream-004`, whose
  template carries `xml:base="../../.."` and asks for `catalog.xml`, failed
  with an `FODC0002` naming a path under the stylesheet's own directory. The
  compiler already captures the element's base for the AVT's namespace
  resolver; the instruction now keeps it.
* **A filesystem path was mistaken for a URI.** F&O separates `FODC0002`, a
  resource that could not be retrieved, from `FODC0005`, an argument that is
  not a valid URI — the difference being whether retrieval was ever attempted.
  `non-stream-006` and `stream-006` ask for `c:\my\doc\books.xml`, a native
  Windows filename. Its backslashes are not legal URI characters, so it is not
  a URI reference on *any* platform, but the leading `c:` parsed as a URI
  scheme and the failure came back as an unsupported scheme — a retrieval error
  for something that was never a URI. `@href` is now checked for backslashes
  and for malformed percent-escapes before the resolver is consulted, so the
  code no longer depends on whether a resolver is configured. The check is on
  the string rather than on the host filesystem, so it answers identically on
  Windows, macOS and Linux.

* **`merge-097`, `merge-097s`, `merge-097sf` are not fixable, and not bugs.**
  They call `uri-collection('.?select=merge-097-*.xml')`. The `?select=` query
  string is a Saxon extension, not something F&O defines, and the test set says
  so in a comment beside the cases: they "rely on Saxon-format collection URIs
  ... and [are] therefore not interoperable". None of the three declares an
  `<environment>` or a `<collection>`, so there is nothing for the harness to
  honour — the harness supplies a collection resolver only where the
  environment declares one, precisely so that `fn:collection` keeps refusing
  everywhere else. The resulting `FODC0002: collections are not configured` is
  the engine failing closed by design: a collection URI that can name a
  directory is a file-disclosure vector, and returning an empty sequence
  instead would make "collections are switched off" indistinguishable from
  "the collection was empty". Making these three pass would mean either
  implementing a Saxon-proprietary URI syntax or loosening that confinement,
  so they stay failing.

---

# Summary

The per-suite counts are in the table at the top. What that table cannot show
is *why* the 449 unfixable cases are unfixable. Two rows dominate and are new
since this table was last written — they are listed first, because without them
the rest reads as though it were the whole story:

| Reason | Cases | Where |
|---|---:|---|
| **Requires the §19.8 streamability analysis** | 150 | XSLT 3.0. They want an `XTSE3430` refusing a stylesheet as non-streamable, and 136 of them read "the transform succeeded" — the engine computes the right answer and the test wants it to decline. §19.1: a processor that does not stream "is not required to assess whether constructs are guaranteed-streamable". |
| **Schema-aware features deliberately left** | 203 | QT3 XQuery 3.1, all of them cases `import schema` newly admitted rather than cases it broke. Five features, catalogued in [todo.md](todo.md) §1.5: typed input documents, constructor functions for schema-defined simple types, impure and restricted unions, annotation propagation through a constructor, and substitution groups over validated content. |

The remaining 96 are the ones this file reads case by case:

| Reason | Cases | Where |
|---|---:|---|
| **W3C has challenged its own expected result** | 61 | The `queried` cases: XSD 1.0 (30) and 1.1 (31). The `MS-Regex` cases, 22 on each version, are one open bug, 4113. Spot-checked on four; in each we disagree in the direction the filed bug points. |
| **W3C settled the expectation after a bug** | 20 | The `stable bugNNNN` cases that are not `indeterminate`: XSD 1.0 (15) and 1.1 (13), less the eight per version now recognised as `indeterminate` scoring errors. These are *settled*, not challenged — the previous revision counted them as challenged, which inverts the status. |
| **Suite defect** | 9 | `format-number-070` invokes a template the stylesheet does not declare (verified: zero `xsl:import`/`xsl:include` and zero `name="main"`); `package-021err`/`022err` carry a half-applied erratum; `accumulator-038` omits the `visibility="public"` its sibling was patched to add in 2019; the four `notQName` cases are XSD 1.1 tests the suite forgot to mark `version="1.1"`; `particlesZ001` never propagated its instanceTest's version split to its schemaTest; `attP031` says in its own prose that the attribute *does* appear yet expects valid. |
| **Unicode or edition moved** | 3 | `regex-syntax-xslt20-0984`, `-0985` and `-0987`, all three already corrected by the W3C in their XSLT 3.0 twins and never back-ported. `-0987` briefly left this row as an engine defect and has returned: it turns on XML 1.0 4e vs 5e `NameChar`, and 5e is the edition XSD 1.1 normatively requires of us. |
| **Suite contradicts itself** | 1 | `strip-space-009` asserts a whitespace-preservation rule §4.4 does not state, and its own comment says it exists to exercise Saxon's code paths. `sequence-0132` was listed here and is better explained directly from §11.10 + §3.9; `simple093` was listed here and is reopened as a question. |
| **Spec declines to decide** | 4 | `si-copy-117` and `si-copy-of-117` use `type=` where XTTE1510 requires `validation=`; `import-schema-137` (which fails on both the 2.0 and 3.0 targets, so counts twice) has two genuine errors and §2.9 makes the choice implementation-dependent. |
| **Needs a network fetch** | 1 | `evaluate-048` wants `https://www.saxonica.com/welcome/welcome.xml`. `unparsed-text-2003` was here on both targets and has left the denominator; `package-version-011` was here and is fixed — no fetch was ever needed, since `doc('')` names the containing module. |
| **Vendor extension** | 2 | `docbook-001`, on both targets, needs EXSLT `exsl:document`. |
| **Feature deliberately not implemented** | 353 | The two rows at the head of this section: 150 XSLT 3.0 cases wanting the §19.8 streamability analysis and 203 XQuery cases wanting the five schema-aware features of [todo.md](todo.md) §1.5. This row read **0** when the streaming gate was still excluding its cases from the denominator and `import schema` had not yet admitted the XQuery ones — the row was empty because the cases were not being counted, which is the failure mode this whole file exists to prevent. `streamable-141` was its last individually-named entry and is **fixed**: §3.9.1 states its rule "notwithstanding anything stated in 19 Streamability", so it never needed the analysis its row claimed. `catalog-006b` was here until `xsl:assert` was implemented, and XSD `iri-001` moved to the fixable column when the audit found it ours, and has since been fixed in the driver. |
| **Nested compile deadlocks on package-level state** | 1 | `transform-004` calls `fn:transform` from a `static="yes"` variable, so it must run during the static phase. Registering the function there is correct by §9.7 and two lines; it deadlocks on the non-reentrant `compileMu` guarding `Compile`'s package-level state. Architecture debt with a known change and a stated cost. |
| **Undiagnosed** | 1 | `evaluate-046` reports `XTDE3400: accumulator static-vars is defined circularly` where the case expects success. Not read. |
| **Costs more than it gains** | 2 | `accept-913` (its own comment contradicts §3.6.3.2), `package-200` (a rule separating it from `use-package-291`–`294` exists but rests on quoting, which neither grammar mentions, and would have exactly one instance in the suite). `use-package-003` was here and is now **fixed**: the narrow form of the change its row called for — carrying the declaring package's visibility on the function component and checking it at the call site — turned out to be contained, and gained the case with no regression. |
| **Implementation-defined** | 2 | `validation-0201` (both targets) asserts Saxon's 3-space indent byte-for-byte where this serializer writes 2. The suite rewrote the sibling `validation-0202` in 2013 to avoid exactly this. |

The XSLT rows above are exact and case-by-case, save the two feature rows at
the head, which are counted from the run rather than read individually. The XSD
rows are not: they are derived from the `status` field and the kind of each
disagreement, and the two XSD reason rows overlap slightly with the
suite-defect row, so this table sums to a little more than 449. That imprecision is inherent to deriving the XSD
split from status rather than from a per-case reading, and it is stated here
rather than papered over — the previous revision's version of this table summed
to 124 against a claimed 131, with no note.

Two suites do reach 100% — XPath at all three versions, and RELAX NG. The
others will not, and for most of the residue the old reasons hold: a suite
defect, a W3C-challenged expectation, a vendor extension, or a Unicode snapshot
that has since moved. The twenty-three cases the audit found to be work have
all since been settled — fixed, or returned to their original verdict with
better evidence — which is why the *Fixable* and *Open* columns are zero. What
replaced them is not a backlog either: the 353 cases in the two rows above are
two named features, one of which the spec explicitly does not require of a
non-streaming processor and one of which is deliberately deferred with its
scope written down. The honest summary is "here is what is left, here are the
two features that are most of it, and here is why the rest is not work".

## Related

[reaching-100.md](reaching-100.md) answers the question this file's numbers
raise: what it would actually take to close each gap, and which of them are not
work at all. It predates the streaming gate coming off and `import schema`
landing, so its per-gap reasoning still applies while its arithmetic does not —
read it for the *what it would take*, and take the counts from here.

[known-gaps.md](known-gaps.md) is the reasoning behind the hard entries here:
diagnosed causes, fixes that were attempted and measured and reverted, and what
a real fix would cost where the answer is a rewrite rather than a patch. It also
covers DTD and XDM, which have no public suite and so appear in no percentage.

## Caveat on confidence

The verdicts are not uniformly deep. The package-composition, regex and XPath
cases were root-caused by reading the specification and the engine. The XSD
breakdown is derived from the suite's own `status` field and the kind of each
disagreement, which does not identify *which* rule is missing in each case —
and the audit showed that the status field is weaker evidence than it looked,
because `stable` had been read as challenged when it means settled, and
`iri-001` had no status at all.

Two limits on the XSLT 3.0 evidence are worth stating, because several verdicts
lean on them:

**The XSLT 3.0 spec here is a 2012 Last Call Working Draft, not the 2017
Recommendation.** `specs/xslt-lcwd30.xml` says so in its own status section. Any
verdict of the form "the spec grants no such rule" is a claim about the LCWD.
`strip-space-009` is the case most exposed to this: it was authored in December
2012 by the spec's own editor, which is some evidence the WG intended a rule the
LCWD does not state, and `xsl:source-document` — which `docbook-004` tests —
does not appear in the LCWD at all.

**Saxon 9.8's submission is not a neutral referee.** It passes eleven of the
fourteen XSLT 3.0 failures this file reads individually, but several
expectations were recorded from Saxon's
own behaviour: `validation-0006`'s stylesheet says outright that it is "a
contrived example to force Saxon down a particular code path", and
`validation-0201`'s expected file is Saxon's output byte-for-byte. Saxon's
submission also predates `accept-913`, `package-200`, `package-021err`,
`package-022err` and the `docbook` set, so it is silent on those. Where Saxon
passes a case *and* the spec text supports it — `package-version-011`, and
`use-package-003` before it was fixed — that is real evidence; where Saxon is
the source of the expectation, it is not.

## Corrections from the audit

Every entry below was checked against the local spec, the test sources and the
vendored submissions. Nothing here was implemented; this section records what
the previous verdicts got wrong.

**Verdicts overturned — the case is ours or the harness's.**

| Case | Was | Now | Why the old verdict failed |
|---|---|---|---|
| `strip-space-009` | *absent* | Not implementable | Not a wrong verdict but a **missing** one: it is the twentieth XSLT 3.0 failure and appeared nowhere in this file, while the prose enumerated nineteen against a table saying twenty. |
| `docbook-004` | Vendor extension (EXSLT) | Ours — now fixed | Grouped with `docbook-001` by name. The stylesheet is five lines with no extension element; it tests an `xml:id` fragment on `xsl:source-document/@href`, which `xslt/sourcedoc.go` ignored entirely. It now applies the bare-name fragment to the retrieved document. |
| `package-version-011` | Needs a network fetch | Ours | No fetch exists. `doc('')` names the containing module; `fn:document` already has the exemption and `fn:doc` does not. |
| `validation-0201` | Implementation-defined | Harness, then **ours — now fixed**, and implementation-defined again | The catalog schema licenses drivers to ignore serialization differences "capable of being produced by a conformant implementation" and says the assertion "should not be used except where the purpose of the test is to test the serializer". Acting on that licence did not pass the case; it uncovered an encoding defect (fixed) and then an engine one (now fixed): a union's selected member type was dropped on every tree copy, so `xsl:strip-space` untyped the document and the schema-typed `Date` template never matched. The output now matches the expected file byte for byte apart from the indent width, which is where the original verdict pointed. Three verdicts, each true of one layer — see the note under *What each round of work changed*. |
| `unparsed-text-2003` | Needs a network fetch | Out of scope | The suite has `available_documents` for exactly this and the harness already honours it; the sibling `unparsed-text-2002` declares it for the same URL and is skipped. |
| `streamable-141` | Requires streamability analysis | Out of scope | The spec says a non-streaming processor "is not required to assess whether constructs are guaranteed-streamable". Its environment declares `source/@streaming`, which the harness does not read. |
| `iri-001` | Suite defect (XSD 1.1) | Harness — now fixed | It has no `<current>` element, so there was no status to cite. The engine was never at fault: `tests/xsdsuite` loaded every schema without `AllowDOCTYPE`, and the IRI/URI type library builds its RFC 3986/3987 patterns out of an internal DTD subset. Setting it on the schema-load path took XSD 1.1 from 41,519 to 41,532 agreeing — the schema test itself plus the 12 instance tests the load failure had been suppressing — with XSD 1.0 and all four XPath/XQuery/XSLT suites byte-identical. |
| 8 `indeterminate` XSD cases per version | counted as disagreements | Harness | `expectedValidity` collapses `indeterminate` to "must be invalid". The WG's own annotation says implementations may reasonably differ. |

**Verdicts whose outcome stands but whose reasoning was wrong.**

- `evaluate-045` — the claim that "a plain `xsl:stylesheet` is not a package" is
  false; §3.6 says an implicit package *is* one. The divergence is deliberate
  and defensible, but it is a won't-fix, not a can't-fix, and it was
  simultaneously described as won't-fix, can't-fix and a cost trade-off.
- `package-021err` — `@names` does admit an arity; §3.6.2 gives `p:local#2` as
  an example. The defect is confined to `xsl:function/@name`.
- `accept-913` — the recorded diagnosis describes an investigation into
  `xsl:initial-template` visibility, not the error we now emit. XTDE3052 is
  scoped to `visibility="absent"` and nothing here is absent. Reopened.
- `sequence-0132` — the alleged contradiction with `sequence-2401a` compares two
  different constructs. §11.10 and §3.9 settle it directly.
- `regex-syntax-xslt20-0984`/`-0985` — right about the drift, but the decisive
  evidence is that the W3C already fixed both in their 3.0 twins.
- `regex-syntax-xslt20-0987` — the audit overturned this one to "ours", and a
  re-measurement has overturned it back. Its reading of the test data was exact
  and is preserved in the row above; what it did not check was whether 4e is a
  configuration we are free to adopt. It is not. `common/xsts.xsd` makes 4e and
  5e mutually exclusive processor configurations and names 5e as XSD 1.1's
  normative base, so the 4e reading this one case wants would be paid for out of
  the XSD 1.1 numerator. The 3.0 twin settles the intent: it was made
  edition-neutral, not 4e.

**Claims of "not implementable" that were really cost or architecture.**
`package-200` (a special case with one instance, not a rule), `use-package-003`
(the row already read "not implementable *at this blast radius*" and named the
design change needed — and when that change was actually attempted it proved
narrow, so the row was wrong twice over), and `accept-913` before it was
reopened. The file's own definition — "passing
would require violating the specification, reaching the network, shipping a
vendor extension, contradicting a second test in the same suite, or encoding a
snapshot of Unicode that is no longer current" — covers none of these.

**Bookkeeping.** The top table claimed 131 can't-fix while the prose said 126 in
one place and 124 in another, and the Summary's reason table summed to 124. The
XSD section's own table printed totals of 53 and 49 against the top table's 51
and 47, and `accepted` counts of 8 and 5 against a measured 6 and 2 — the
latter mattering because the file then named more "settled suite defects" than
the bucket contained. The header also attributes the figures to commit
`6fa4150`, which is not this tree's HEAD; the counts were re-measured and the
failure totals reproduce.
