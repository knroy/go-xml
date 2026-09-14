# What reaching 100% would take

Measured at commit `23570de`, 2026-09-14. The short answer: **100% is not
reachable on any of these suites**, and the great majority of what remains are
cases where passing would mean shipping something *less* correct. What follows
separates the work that exists from the work that does not.

The verdict columns are the ones in
[conformance-gaps.md](conformance-gaps.md), which is where they are argued
case by case; this file is about what buying them would cost.

| | Failing | Fixable | Open | Cannot fix |
|---|---:|---:|---:|---:|
| XPath 3.1 | 0 | 0 | 0 | 0 |
| XQuery 3.1 | 1 | 0 | 0 | 1 |
| XSLT 2.0 | 8 | 0 | 0 | 8 |
| XSLT 3.0 | 26 | 0 | 0 | 26 |
| XSD 1.0 | 30 | 0 | 0 | 30 |
| XSD 1.1 | 31 | 0 | 0 | 31 |
| **Total** | **96** | **0** | **0** | **96** |

XPath 2.0, XPath 3.0, XPath 3.1 and RELAX NG are already at 100%.

Seven of the XSLT 3.0 cases sit in the "cannot fix" column on a weaker claim
than the rest: they want an `XTSE3430` refusal that only the unwritten
remainder of the §19.8 streamability analysis can emit, and §19.1 says a
non-streaming processor "is not required to assess whether constructs are
guaranteed-streamable". They are not owed, which is not the same as not
buildable; Part 2 files them under features deliberately not implemented.

For XSD the fixable/cannot-fix split is not a judgement call: it is the suite's
own `status` field. A case marked `accepted` is a settled expectation and so is
real work; one marked `queried` or `stable bugNNNN` is one the W3C has itself
challenged, and 56 of the 61 XSD disagreements are of that kind — 44 of them
(22 per version) are the single open bug 4113. The other five are `accepted`,
and each is adjudicated individually in
[conformance-gaps.md](conformance-gaps.md): two suite defects, one
self-contradiction, and two measured trades.

---

## Part 1 — what is left

**No case is work.** An earlier revision of this file said the fixable column
had reached zero over three rounds; an adversarial re-audit overturned that and
found twenty-three cases of work, and the detail is in
[conformance-gaps.md](conformance-gaps.md). Every one of those has since been
closed. The four it named as engine defects are gone: `docbook-004` and
`package-version-011` were fixed, `iri-001` stopped disagreeing once its
schema was loaded with `AllowDOCTYPE` on, and `regex-syntax-xslt20-0987` was
overturned to "ours" and then overturned back — it is one of the three Unicode
cases in Part 2, not a defect. The two the suite itself declared out of scope
left the denominator, and the harness scoring defects — chiefly the eight XSD
`indeterminate` expectations per version that were silently scored as "must be
invalid" — were fixed in the harness. What stands between here and 100% is
the 96 in Part 2.

The last four to fall are worth recording, because they are the shape of what
"fixable" meant:

| Case | Suite | Outcome |
|---|---|---|
| `particlesZ040` | XSD, both | Fixed by bracketing a repetition count into a low and a high reading, and since decided exactly by a set of whole count vectors. |
| `wildZ013` | XSD 1.0 | Fixed: attribute-wildcard intersection under errata E1-10. |
| `particlesK006` | XSD 1.1 | Fixed: particle derivation. |
| `catalog-005b` | XSLT 3.0 | Fixed, and `catalog-009` came with it. |
| `type-available-0151` | XSLT 3.0 | Fixed by scoping `XSD_1.1` to the version being measured, which brought three `regex-syntax` cases too. |

`attP031` left the column the other way: it is a suite defect, not work.

### The open questions

None. Four stood here — three in XQuery and one on XSD 1.1 (`simple093`) —
and each has since been settled, none as a pass and none as a defect. The
XQuery three collapsed to one, `contextDecl-052`, and two independent
adjudications agree it is a fixture defect: the catalog registers a module
under a namespace the file does not declare. `simple093` is a suite
self-contradiction — `particlesZ007` declares the same `xs:NOTATION` union
valid, and both carry `status="accepted"`. `particlesZ033_g` stood here until
it was settled: the rule it turns on is the XSD 1.1 wildcard relaxation, which
the suite's own `XSD1_1TestCategories.xml` states as a feature category, and
enforcing the 1.0 reading instead costs seventeen valid schemas for the one
case. Its `invalid` verdict is a 1.0-era expectation the 1.1 run inherits; see
[known-gaps.md](known-gaps.md).
`validation-0006` and `strip-space-009` stood here until the audit settled both
as not implementable, and `validation-0201` — one at each XSLT target, so two
of the original seven — left when the engine defect behind it was fixed and the
case turned out to still fail on an implementation-defined indent width.

### What a zero here does not mean

It does not mean the engine is exact. It means the suites have no more to say.
The content-model matcher is provably wrong on a shape neither suite covers —
a repeated group whose only child is itself repeating is decided wrongly in
both directions, found by differential fuzzing and recorded in
[known-gaps.md](known-gaps.md). Every W3C case of that form uses two or more
distinct child names, so 80,925 agreements step around it.

A ceiling bounds what the suite asks, not what the code does.

---

## Part 2 — the 96 that are not work

Grouped by what would actually have to change.

### 56 — the W3C disputes its own expected result

XSD 1.0 (28 of its 30 disagreements) and 1.1 (28 of its 31). Counted from the
suite's own metadata rather than estimated: every disagreement whose `<current>`
status is not `accepted`. The remaining 2 and 3 are `accepted` and are the only
settled XSD expectations this validator disagrees with; they are filed below by
what they are.

These totals fell from 45 and 44 when two harness defects were fixed:
`indeterminate` expectations stopped being scored as "must be invalid", and
`iri-001`'s schema, which builds its RFC 3986 patterns from an internal DTD
subset, stopped being loaded with `AllowDOCTYPE` off. The suite records a
`status` on each test:
`accepted` means settled, **`queried` means the W3C has itself challenged the
expectation**, usually with a bugzilla number. 26 are `queried` on 1.0 and 27
on 1.1; the rest are `stable` but carry an open bug.

**All 44 `MS-Regex` disagreements — 22 in each version — are one bug, 4113.**

To pass these we would have to agree with results the working group does not
stand behind. That is not conformance; it is bug-compatibility with a specific
processor. **Nothing to do here, and doing it would be wrong.**

*Marginal cases:* the five `accepted` disagreements. `attP031` and
`particlesZ001` (1.0) are suite defects; `simple093` (1.1) is the suite
contradicting itself; `particlesZ033_g` and `id017.n01.xml` (1.1) are measured
trades. None is counted as fixable — the earlier revision counted its marginal
cases the less flattering way, and every one of those has since been fixed or
settled.

### 0 — `fn:load-xquery-module` (still out of scope, and now for one reason only)

`fn-load-xquery-module-003`, `-004`, `fn-function-lookup-764`.

These still do not count against anything: the set declares the feature
`satisfied="true"` and then overrides fourteen cases to `satisfied="false"`, so
the harness treats `fn-load-xquery-module` as an unsupported feature and the
cases fall out of scope. That is what took XPath 3.1 to 100%.

**The module store now exists.** `xquery` implements `import module` (§4.12) —
a store, a resolver, transitive loading and the cycle rule — so the reason
given here previously ("there is no module store for this function to load
from") no longer holds. Measured after that work, the three cases are
**unchanged: still out of scope, neither passing nor failing.** XPath 3.1 stayed
at 21786/21786 then, and the XQuery mark rose from 29800 to 29901.

Both figures have since moved for an unrelated reason: two stale feature
labels, `namespace-axis` and `infoset-dtd`, were lifted from the same
unsupported list, putting XPath 3.1 at 21863/21863 and XQuery at 29952/29964.
`Axes123` (namespace-node identity across two axis walks) was the single XPath
failure that lift exposed, and is now fixed — see `xdm.Node.Is`. The
`fn-load-xquery-module` cases discussed here are unaffected — that label stays
on the list, for the reason the rest of this section gives.

What is left is not a missing engine but the suite's own contradiction, which
was always the second half of the reason. `-003` and `-004` want **FOQM0002**
("the module cannot be located") for `fn:load-xquery-module("http://nonexistent/module")`,
while `-903` wants **FOQM0006** ("the implementation does not support
load-xquery-module") for an expression of the same shape. A processor can
satisfy one set or the other and not both, and F&O 3.1 defines FOQM0006
precisely so that a processor without the function may say so — raising it is
the conforming answer, and it is what the fourteen overridden cases assert.

Bridging the function to the new module store would therefore trade three
out-of-scope cases for a different three, and would additionally require
deciding what a *dynamically* named module URI may fetch. That is the same
question `Options.ModuleResolver` answers statically by fetching nothing
without a resolver, and a function that resolved a URI computed at run time
would need its own answer to it. **To fix: not the engine, and not the store —
a policy for run-time module URIs, plus a choice about which half of the suite
to satisfy.**

### 3 — the suite contradicts itself

`strip-space-009` asserts a whitespace-preservation rule §4.4 does not state,
and its own comment concedes the point.

`simple093` (XSD 1.1) expects a union of `xs:QName` with `xs:NOTATION` to be
invalid, and Part 2 §3.2.19 agrees — but `particlesZ007` declares a schema
containing `<xsd:union memberTypes="xsd:NOTATION"/>` valid, and both are
`accepted`. Enforcing the rule was measured: 1.1 trades one for the other and
1.0 loses two outright. Reverted.

`system-property-012` asserts `system-property('xsl:supports-streaming')` is
`'yes'` from a processor §26.5 requires to answer `'no'`; its twin
`system-property-013` is the same stylesheet gated on the streaming feature
being *unsatisfied* and asserts `'no'`. The pair is a dependency-gated fork of
which exactly one is meant to run, and the suite does not separate the
streaming dependency from the streaming conformance the property reports on.

The three `regex-syntax` ambiguous-dash cases — `-0056`, `-0086`, `-0102` —
stood here until recently and now **pass**. They were the case where
`regex-syntax-0056` and `regex-syntax-xslt20-0056` carry the identical pattern
and the identical `XSD_1.1 satisfied="false"` dependency while one asserts
`FORX0002` and the other a successful match. An engine-wide XSD 1.0 rule fixed
them at a cost of −3 XSLT 2.0, −9 QT3 and −34 XSD 1.1, and was reverted;
scoping `XSD_1.1` to the version being measured rather than to the engine
bought all three with no such cost, and took `catalog-005b` and
`type-available-0151` with them.

### 5 — the spec declines to decide

`si-copy-117` and `si-copy-of-117` write `xsl:copy` with a `type` attribute and
**no `validation` attribute**. XTTE1510 begins "If the **validation
attribute** ... has the effective value `strict`" — literally unmet. XTTE1540
names the type attribute exactly. Our answer is right.

`validation-0006` is the same shape one clause over: it wants XTTE1555 for a
parentless attribute, and XTTE1555 is scoped by its own text to "when
validating a **document node**". XTTE1540, which covers the `type` attribute,
is what the case actually meets; the stylesheet calls itself "a contrived
example to force Saxon down a particular code path".

`import-schema-137` (both targets) has two genuine errors, and §2.9 settles the
choice by declining to: *"It is implementation-dependent which of the several
errors is signaled."*

**Nothing to do.** Passing means matching one processor's arbitrary order.

### 3 — Unicode moved

`regex-syntax-xslt20-0984`, `-0985`, `-0987` assert `\w`/`\d`/`\c` membership
for U+2308, U+1369 and U+0346. U+2308 was recategorised `Sm` → `Ps` in Unicode
6.1 (2012), after these tests were written.

The XSLT 3.0 `regex-syntax` set runs **987 cases and 984 pass**, none failing
on these classes — so the definitions are right and only these three 2012-era
cases disagree.

**To fix: freeze a 2012 Unicode table.** That would make every other case wrong.

### 9 — suite defects

- **`format-number-070`** — the catalog invokes `<initial-template
  name="main"/>`; the stylesheet has one template, `match="root"`, and zero
  occurrences of `name="main"`. XTDE0040 is mandatory.
- **`package-021err`, `package-022err`** — a half-applied 2020 erratum (E36)
  appended `#0` to `<xsl:function name="me:function1#0">` and
  `component="function#0"`. The grammar admits an arity in neither.
- **`accept-913`** — **settled: suite defect, not a cost question.** §3.6.3.2
  keeps an unmatched `public` component public (only `private` becomes
  `hidden`), so the wanted XTDE0040 — which needs a name that matches no
  public-or-final template — is unreachable. `accept-910` is the same
  stylesheet (its `xsl:accept` is a no-op under that clause, and the two used
  packages are byte-identical modulo the package name) and wants a different
  code, so the suite contradicts itself. The XTSE3080 alternative was
  measured: 11,481 → 11,468, breaking six named `accept-90x`/`-91x` cases and
  seven more without fixing `-913`. Reverted. Full evidence in
  `conformance-gaps.md` §1.
- **`accumulator-038`** — an explicit `xsl:package` whose `main` template is
  unannotated, so §3.6.3.1's "Otherwise, private" applies and XTDE0040 is met.
  Only its sibling `-039` carries the 2019 "Make main template public" repair.
- **`sequence-0132`** (XSLT 2.0) — §11.10 gives `xsl:sequence` a mandatory
  `select`, so XTSE0010 is the correct 2.0 answer; the stylesheet's own
  `<?error XTSE0010?>` and the Saxon 9.8 report agree, and the `XSLT20+` scope
  is stale metadata left behind when the expectation was edited for 3.0.
- **`contextDecl-052`** (XQuery) — registers `libmodule-3.xq` under a
  namespace the file does not declare, so XQST0059 is correct and precedes the
  wanted XQST0113.
- **`attP031`, `particlesZ001`** (XSD 1.0) — the first names its instance test
  `.i`, says the attribute appears, and still expects valid; the second never
  propagated its version split from instance test to schema test.

**To fix: accept invalid stylesheets, or broken fixtures.**

### 1 — network access

`evaluate-048` fails on
`FODC0002: cannot retrieve "https://www.saxonica.com/welcome/welcome.xml": scheme "https" is not permitted`.

Resolvers are nil by default so an untrusted stylesheet cannot fetch what it
names, and making outbound HTTP the default would be a security regression
rather than a conformance gain.

`unparsed-text-2003` was in this group and no longer needs anything:
`remoteResource` excludes it, derived from the environment's resource URIs
rather than by naming the case. `package-version-011` was in this group too and
is now **fixed** — the static phase was given the module resolver, so it no
longer wants a document with no resolver configured.

### 4 — vendor extension

`docbook-001`, on both targets. The vendored DocBook XSL 1.79.1 uses EXSLT
`exsl:document` — 19 times in `chunker.xsl` alone.

`merge-097` and `merge-097s` call `uri-collection('.?select=merge-097-*.xml')`.
The `?select=` query string is a Saxon extension, and the test set says so in
a comment beside the cases: they "rely on Saxon-format collection URIs … and
[are] therefore not interoperable".

**To fix: implement EXSLT, and Saxon's collection URI syntax.** Defensible as
features; not conformance fixes.

`docbook-004` was filed here on the strength of its neighbour's name and is
not an EXSLT case at all: its stylesheet is five lines with no extension
element, testing `xsl:source-document` with an `xml:id` fragment identifier.
It was an engine defect, and is now **fixed**: the resolver strips the
fragment before the filesystem sees it — correctly, since a fragment names a
part of a resource rather than a different one — and nothing then applied it,
so the whole document was returned. `xslt/sourcedoc.go` now resolves the
bare-name fragment against the retrieved document.

### 8 — features deliberately not implemented

Seven want an `XTSE3430` refusal that only the unwritten remainder of the
§19.8 posture-and-sweep analysis can emit: `si-fork-902`, `si-fork-952`,
`su-absorbing-205`, `su-absorbing-908`, `su-ascent-903`,
`su-shallow-descent-902` and `sx-square-array-201`. This list used to be
defined by cases that needed a feature not yet built, and these are that:
the engine computes the right answer and the test wants it to decline. §19.1
settles whether the refusal is owed — a processor that does not stream "is not
required to assess whether constructs are guaranteed-streamable" — so they are
not defects. One of the seven, `su-ascent-903`, is also argued on its own in
[conformance-gaps.md](conformance-gaps.md) because its verdict is not the
block's: there the analysis speaks and the test is wrong, since §19.8.5.7
makes an ascent function's streaming parameter climbing and permits a climbing
body. It is counted once, here.

The eighth is `sf-reverse-001`, which wants `reverse(snapshot(/chapter)//section)`
in reversed order. XPath 3.1 §3.3.1.1 returns a path's nodes in document order
however the left operand was ordered; the catalog's answer is what a
*streaming* evaluator produces, since §19.11 says sorting is incompatible with
streaming. The only route to it is streamed execution proper.

Three cases used to be on this list and are now **fixed**. `base-uri-052` went
when XInclude was implemented (`xdm.ProcessXInclude`), and the harness honours
the environment's `xinclude="true"`. `catalog-006b` went with `xsl:assert`,
which was the cheapest feature here and is done: the case reports every XSLT
element the processor recognises, so an absent one is visible in it.
`streamable-141` went last: §3.9.1 states its rule "notwithstanding anything
stated in 19 Streamability", so it never needed the streamability analysis
this file said it required. A verdict of "needs a feature we have not built"
is worth re-deriving before it is believed.

### 4 — costs more than it gains

- `package-200` — a rule separating it from `use-package-291`–`294` exists but
  rests on quoting, which neither grammar mentions, and would have exactly one
  instance in the suite.
- `accumulator-061` — §10.3.6 focus capture is implemented; what is missing is
  that nothing marks the principal input as streamed, and supplying that puts
  the 2,020 cases in `tests/strm/` at risk to win one.
- `particlesZ033_g` (XSD 1.1) — enforcing the 1.0 wildcard reading costs
  seventeen valid schemas for the one case.
- `id017.n01.xml` (XSD 1.1) — a defaulted `xs:ENTITY` in a document with no
  DTD at all. Part 2 §3.3.11 read to the letter takes it; the cost is `as-34`
  and the XSLT suite's `as-3401`, `match-208` and `match-209`, which Saxon
  validates.

`use-package-003` was once an entry, and it is now **fixed**. This file said
a real fix needed the package threaded through the XPath static context and
called it "the single largest structural change on this list". The narrow form
of that turned out to be contained: the declaring package's visibility is
carried on the function component and checked at the call site, through a
`ScopedFunctionLibrary` paralleling the existing `DynamicFunctionLibrary`. The
lexical-rename attempt that broke `override-f-026` is what made the whole
direction look expensive; a rename was the wrong shape, not the idea.
`accept-913` was an entry too, and left for the suite-defects list above once
the wanted code was shown to be unreachable.

---

### 3 — an implementation-defined difference, and the engine defect that hid behind it (fixed)

`validation-0201`, on both targets, and `streamable-116`. **Nothing is left to
do here.**

`streamable-116` wants XPDY0002 because a global variable reads the context
item while the initial mode streams. §3.6.6 offers two options to the API, one
of which is "building the corresponding tree in memory and supplying the global
context item as an unstreamed node" — which is what this engine does — and the
catalog's own keywords mark the case `_WRONG:wrong-error-code`.

`validation-0201` does assert Saxon's three-space indent byte-for-byte, and
indentation is implementation-defined by §10 of the serialization spec — the
suite's own `validation-0202` was rewritten in 2013 "to avoid serialization
dependencies" and `0201` was not. That much of the earlier reading holds.

What it missed is that the indentation is only the first of three differences,
and normalising it does not pass the case. Behind it: an expected file
declaring `iso-8859-1` with no `@encoding` on the assertion for the harness to
read, since fixed; and then a real defect, `29 MAY 1917` where `29 May 1917`
is wanted, because `match="Date[data(.) instance of StandardDate]"` never
matched and the plain `match="Date"` copied the source text through.

That defect is now fixed, and it was not the one this section predicted. An
imported schema's simple type *was* visible to `instance of`, and the element
*did* carry its annotation. `Date`'s type is a complex type with simple content
extending a union, and XSD §3.14.4 selects a union's member per value — so the
member that accepted the text is recorded separately on the node, and is what
atomisation reads. Three tree-copy sites carried the annotation and dropped the
member; the stylesheet's `<xsl:strip-space elements="*"/>` put every `Date`
through one of them.

**The case still fails**, on the indent width and nothing else — the output is
now byte-identical to the expected file apart from whitespace. So the fix gains
no suite case, and the original implementation-defined verdict is where this
lands after all, by a route that had to be walked to be believed. See
[known-gaps.md](known-gaps.md).

---

## Part 3 — the honest bottom line

**Reaching 100% is not a goal that survives contact with the suites.** All 96
remaining disagreements would require agreeing with a disputed result, shipping
a second language implementation, freezing a stale Unicode table, accepting
invalid input, weakening a security default, finishing an analysis §19.1 says
a non-streaming processor need not perform, or reproducing another
implementation's choice where the spec declines to make one. None is now an
open question about our own correctness — the last two, `validation-0201` on
both targets, were settled by fixing the defect behind them, which turned out
not to move the case.

**Most of that work has since landed, and what it left behind is engine work
rather than harness work.** The XSD `indeterminate` expectations are no longer
scored as "must be invalid", `iri-001`'s schema is loaded with `AllowDOCTYPE`
on, `docbook-004`, `package-version-011` and `use-package-003` are fixed, and
and both the encoding half and the engine half of `validation-0201` are fixed.
**The fixable column is now empty on every suite, and so is the open column.**
`streamable-141` was fixed rather than excluded, and
`unparsed-text-2003` needed nothing: `remoteResource` already excludes it,
derived from the environment's resource URIs rather than by naming the case.

Two revisions of this file have now been overturned by re-derivation. The
first claimed the fixable column had reached zero; the audit recorded in
[conformance-gaps.md](conformance-gaps.md) found twenty-three cases of work.
The second called `validation-0201` a harness fix; implementing it showed the
serialisation difference was hiding an engine defect. The third — this one —
named the wrong engine defect: it predicted an imported type invisible to
`instance of`, and the cause was a union's selected member dropped on every
tree copy. All three are worth remembering as the kind of claim a document
makes when its verdicts stop being re-derived, and the third especially: a
diagnosis reached by elimination is a hypothesis, not a finding.

Beyond that is engineering the suites cannot see:
the content-model matcher's nested-occurrence bug above, and whatever else
fuzzing turns up. That is the better use of the next round.

The larger items on this list are now all done. **A package-aware XPath static
context** was the last, and `use-package-003` fell to a contained form of it --
visibility carried on the function component and checked at the call site.
`xsl:assert` and **XInclude** were the other two; XInclude took DocBook xslTNG
from 549 to 577 of 593.

Streaming has the largest denominator, 2,646 cases out of scope, but it is not
the project it looks like. Measured with the gate lifted and nothing else
changed, 2,424 of those pass already: §19.1 lets a processor answer a request
for streamed evaluation by building the tree, and this engine does. Of the 222
that fail, 150 want XTSE3430 -- a *refusal* of a non-streamable stylesheet,
which needs the §19.8 posture and sweep analysis and no runtime change at all.
Those three gate-lifted figures were not re-measured at `23570de`; the
in-scope residue at this commit is the block of seven in Part 2. Streamed
execution proper would buy almost none of it. See
[conformance-gaps.md](conformance-gaps.md) for the breakdown.

**EXSLT is not on this list.** It is a separate product. XQuery was, and is
now implemented in [`xquery`](../xquery/) at 30,345 of 30,346; what remains of
it there is tracked in [xquery.md](xquery.md) rather than here, because this
file is about the XPath and XSLT figures.

---

## Related

[conformance-gaps.md](conformance-gaps.md) names every failing case and carries
the current figures. [known-gaps.md](known-gaps.md) is the diagnosis behind the
hard ones — attempted fixes, why they were reverted, what a rewrite would cost.
