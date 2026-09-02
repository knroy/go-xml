# What reaching 100% would take

Measured at commit `7ed5279`. The short answer: **100% is not reachable on any
of these suites**, and the great majority of what remains are cases where
passing would mean shipping something *less* correct. What follows separates
the work that exists from the work that does not.

The verdict columns are the ones in
[conformance-gaps.md](conformance-gaps.md), which is where they are argued
case by case; this file is about what buying them would cost.

| | Failing | Fixable | Open | Cannot fix |
|---|---:|---:|---:|---:|
| XPath 3.1 | 0 | 0 | 0 | 0 |
| XQuery 3.1 | 7 | 0 | 3 | 4 |
| XSLT 2.0 | 9 | 2 | 1 | 6 |
| XSLT 3.0 | 18 | 4 | 1 | 13 |
| XSD 1.0 | 51 | 8 | 0 | 43 |
| XSD 1.1 | 47 | 9 | 2 | 36 |
| **Total** | **132** | **23** | **7** | **102** |

XPath 2.0, XPath 3.0, XPath 3.1 and RELAX NG are already at 100%.

For XSD the fixable/cannot-fix split is not a judgement call: it is the suite's
own `status` field. A case marked `accepted` is a settled expectation and so is
real work; one marked `queried` or `stable bugNNNN` is one the W3C has itself
challenged, and 89 of the 98 XSD disagreements are of that kind — 44 of them
(22 per version) are the single open bug 4113.

---

## Part 1 — what is left

**Twenty-three cases are work.** An earlier revision of this file said the
fixable column had reached zero over three rounds; an adversarial re-audit
overturned that, and the detail is in
[conformance-gaps.md](conformance-gaps.md). Four are engine defects
(`docbook-004`, `package-version-011`, `regex-syntax-xslt20-0987`, `iri-001`),
two are cases the suite itself declares out of scope through a dependency the
harness does not read, and the rest are harness scoring defects — chiefly the
eight XSD `indeterminate` expectations per version that are silently scored as
"must be invalid". Seven more are open questions, settled neither way. What
stands between here and 100% is the 102 in Part 2.

The last four to fall are worth recording, because they are the shape of what
"fixable" meant:

| Case | Suite | Outcome |
|---|---|---|
| `particlesZ040` | XSD, both | Fixed by bracketing a repetition count into a low and a high reading. |
| `wildZ013` | XSD 1.0 | Fixed: attribute-wildcard intersection under errata E1-10. |
| `particlesK006` | XSD 1.1 | Fixed: particle derivation. |
| `catalog-005b` | XSLT 3.0 | Fixed, and `catalog-009` came with it. |
| `type-available-0151` | XSLT 3.0 | Fixed by scoping `XSD_1.1` to the version being measured, which brought three `regex-syntax` cases too. |

`attP031` left the column the other way: it is a suite defect, not work.

### The open questions

Seven, spread across three suites: three in XQuery, one at each XSLT target,
and two on XSD 1.1 (`simple093` and `particlesZ033_g`). Each asks for
behaviour the checked-in spec text does not settle, so none is counted as a
pass or as a defect. `validation-0006` and `strip-space-009` stood here until
the audit settled both as not implementable.

### What a zero here does not mean

It does not mean the engine is exact. It means the suites have no more to say.
The content-model matcher is provably wrong on a shape neither suite covers —
a repeated group whose only child is itself repeating is decided wrongly in
both directions, found by differential fuzzing and recorded in
[known-gaps.md](known-gaps.md). Every W3C case of that form uses two or more
distinct child names, so 80,878 agreements step around it.

A ceiling bounds what the suite asks, not what the code does.

---

## Part 2 — the 102 that are not work

Grouped by what would actually have to change.

### 89 — the W3C disputes its own expected result

XSD 1.0 (45) and 1.1 (44). The suite records a `status` on each test:
`accepted` means settled, **`queried` means the W3C has itself challenged the
expectation**, usually with a bugzilla number. 27 are `queried` in each
version, the rest are `stable` but carry an open bug.

**All 44 `MS-Regex` disagreements — 22 in each version — are one bug, 4113.**

To pass these we would have to agree with results the working group does not
stand behind. That is not conformance; it is bug-compatibility with a specific
processor. **Nothing to do here, and doing it would be wrong.**

*Marginal cases:* six sit on the boundary. `wildZ013`, `stZ007`, `stZ047` and
`stZ055` are `accepted` but carry an open bug, and `sg-abstract-edc` and
`iri-001` have no status at all. `iri-001` is the DOCTYPE refusal (deliberate,
security). They are counted as fixable here, which is the less flattering
reading.

### 0 — no XQuery module loader (now out of scope, not failing)

`fn-load-xquery-module-003`, `-004`, `fn-function-lookup-764`.

These no longer count against anything: the set declares the feature
`satisfied="true"` and then overrides fourteen cases to `satisfied="false"`, so
the harness treats `fn-load-xquery-module` as an unsupported feature and the
cases fall out of scope. That is what took XPath 3.1 to 100%. The reasoning
below is why they are out of scope rather than a gap.

`fn:load-xquery-module` compiles an XQuery library module. This engine now
implements XQuery too, in [`xquery`](../xquery/), but not *module import*:
`import module` raises `XQST0059`, so there is no module store for this
function to load from. F&O 3.1 anticipates exactly this: **FOQM0006 is defined
as "the implementation does not support the load-xquery-module function"**, and
raising it is the conforming answer.

Two of the suite's own cases cannot both pass — `-003` wants FOQM0002 ("cannot
be located") for an expression `-903` wants FOQM0006 for. Locating a module is
something only a processor could do.

**To fix: implement module import in `xquery`,** then bridge the function to
it. The engine is no longer the missing piece; the module store is.

### 1 — the suite contradicts itself

`strip-space-009` asserts a whitespace-preservation rule §4.4 does not state,
and its own comment concedes the point.

The three `regex-syntax` ambiguous-dash cases — `-0056`, `-0086`, `-0102` —
stood here until recently and now **pass**. They were the case where
`regex-syntax-0056` and `regex-syntax-xslt20-0056` carry the identical pattern
and the identical `XSD_1.1 satisfied="false"` dependency while one asserts
`FORX0002` and the other a successful match. An engine-wide XSD 1.0 rule fixed
them at a cost of −3 XSLT 2.0, −9 QT3 and −34 XSD 1.1, and was reverted;
scoping `XSD_1.1` to the version being measured rather than to the engine
bought all three with no such cost, and took `catalog-005b` and
`type-available-0151` with them.

### 4 — the spec declines to decide

`si-copy-117` and `si-copy-of-117` write `xsl:copy` with a `type` attribute and
**no `validation` attribute**. XTTE1510 begins "If the **validation
attribute** ... has the effective value `strict`" — literally unmet. XTTE1540
names the type attribute exactly. Our answer is right.

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

### 3 — suite defects

- **`format-number-070`** — the catalog invokes `<initial-template
  name="main"/>`; the stylesheet has one template, `match="root"`, and zero
  occurrences of `name="main"`. XTDE0040 is mandatory.
- **`package-021err`, `package-022err`** — a half-applied 2020 erratum (E36)
  appended `#0` to `<xsl:function name="me:function1#0">` and
  `component="function#0"`. The grammar admits an arity in neither.

**To fix: accept invalid stylesheets.**

### 2 — network access

`unparsed-text-2003` (both targets) asserts
`unparsed-text-available('http://www.w3.org/Consortium/mission.html')` is true;
`package-version-011` wants a document with no resolver configured.

Resolvers are nil by default so an untrusted stylesheet cannot fetch what it
names, and making outbound HTTP the default would be a security regression
rather than a conformance gain. Neither case needs that, though:
`unparsed-text-2003` omits the `available_documents` dependency its own
neighbour `unparsed-text-2002` declares and the harness already honours, and
`package-version-011` is triaged as an engine defect. Both sit in the fixable
column for those reasons.

### 2 — vendor extension

`docbook-001`, on both targets. The vendored DocBook XSL 1.79.1 uses EXSLT
`exsl:document` — 19 times in `chunker.xsl` alone.

**To fix: implement EXSLT.** Defensible as a feature; not a conformance fix.

`docbook-004` was filed here on the strength of its neighbour's name and is
not an EXSLT case at all: its stylesheet is five lines with no extension
element, testing `xsl:source-document` with an `xml:id` fragment identifier.
It was an engine defect, and is now **fixed**: the resolver strips the
fragment before the filesystem sees it — correctly, since a fragment names a
part of a resource rather than a different one — and nothing then applied it,
so the whole document was returned. `xslt/sourcedoc.go` now resolves the
bare-name fragment against the retrieved document.

### 1 — features deliberately not implemented

- **`streamable-141`** — needs the §19.8 streamability analysis.

Two cases used to be on this list and are now **fixed**. `base-uri-052` went
when XInclude was implemented (`xdm.ProcessXInclude`), and the harness honours
the environment's `xinclude="true"`. `catalog-006b` went with `xsl:assert`,
which was the cheapest feature here and is done: the case reports every XSLT
element the processor recognises, so an absent one is visible in it.

### 3 — costs more than it gains

- `accept-913` — the case's own comment states a premise §3.6.3.2 contradicts.
  Built, instrumented, reverted.
- `package-200` — would cost 4 cases to gain 1.
- `use-package-003` — a private function must resolve inside its package and
  not outside. Functions live in one flat `xpath.Library` resolved by name;
  `FuncCall` carries only a QName. A lexical rename fixed this and broke
  `override-f-026`, where one name exists at two arities. **A real fix needs
  the package threaded through the XPath static context** — the single largest
  structural change on this list.

---

### 2 — implementation-defined

`validation-0201`, on both targets.

It asserts Saxon's three-space indent byte-for-byte. Indentation is
implementation-defined by §10 of the serialization spec, and this serializer
writes a different amount of it. The suite's own `validation-0202` was
rewritten in 2013 "to avoid serialization dependencies"; `0201` was not.

**To fix: match another processor's whitespace.** That is not conformance.

---

## Part 3 — the honest bottom line

**Reaching 100% is not a goal that survives contact with the suites.** Of 132
disagreements, 102 would require agreeing with a disputed result, shipping a
second language implementation, freezing a stale Unicode table, accepting
invalid input, or weakening a security default. Seven more are open questions
the spec does not settle.

**Twenty-three are work, and most of that work is in the harness rather than
the engine** — the eight XSD `indeterminate` expectations per version scored as
"must be invalid", a Saxon-byte-exact serialisation comparison, and two cases
the suite puts out of scope through a dependency the harness does not read.
Four are engine defects. An earlier revision of this file claimed the fixable
column had reached zero; the audit recorded in
[conformance-gaps.md](conformance-gaps.md) overturned that, and the claim is
worth remembering as the kind a document makes when its verdicts stop being
re-derived.

Beyond that is engineering the suites cannot see:
the content-model matcher's nested-occurrence bug above, and whatever else
fuzzing turns up. That is the better use of the next round.

One larger item is defensible as a *feature* rather than conformance work and
should be judged that way: **a package-aware XPath static context** (unlocks
`use-package-003` and removes a known structural limit). `xsl:assert` and
**XInclude** were the other two and are both done -- XInclude took DocBook
xslTNG from 549 to 577 of 593.

Streaming has the largest denominator, 2,646 cases out of scope, but it is not
the project it looks like. Measured with the gate lifted and nothing else
changed, 2,424 of those pass already: §19.1 lets a processor answer a request
for streamed evaluation by building the tree, and this engine does. Of the 222
that fail, 150 want XTSE3430 -- a *refusal* of a non-streamable stylesheet,
which needs the §19.8 posture and sweep analysis and no runtime change at all.
Streamed execution proper would buy almost none of it. See
[conformance-gaps.md](conformance-gaps.md) for the breakdown.

**EXSLT is not on this list.** It is a separate product. XQuery was, and is
now implemented in [`xquery`](../xquery/) at 99.98%; what remains of it there
is tracked in [xquery.md](xquery.md) rather than here, because this file is
about the XPath and XSLT figures.

---

## Related

[conformance-gaps.md](conformance-gaps.md) names every failing case and carries
the current figures. [known-gaps.md](known-gaps.md) is the diagnosis behind the
hard ones — attempted fixes, why they were reverted, what a rewrite would cost.
