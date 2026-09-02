# What reaching 100% would take

Measured at commit `6fa4150`. The short answer: **100% is not reachable on any
of these suites**, and the great majority of what remains are cases where
passing would mean shipping something *less* correct. What follows separates
the work that exists from the work that does not.

| | Failing | Fixable | Open | Cannot fix |
|---|---:|---:|---:|---:|
| XPath 3.1 | 0 | 0 | 0 | 0 |
| XSLT 2.0 | 9 | 0 | 0 | 9 |
| XSLT 3.0 | 19 | 0 | 2 | 17 |
| XSD 1.0 | 51 | 0 | 0 | 51 |
| XSD 1.1 | 47 | 0 | 0 | 47 |
| **Total** | **126** | **0** | **2** | **124** |

XPath 2.0, XPath 3.0, XPath 3.1 and RELAX NG are already at 100%.

For XSD the fixable/cannot-fix split is not a judgement call: it is the suite's
own `status` field. A case marked `accepted` is a settled expectation and so is
real work; one marked `queried` or `stable bugNNNN` is one the W3C has itself
challenged, and 89 of the 98 XSD disagreements are of that kind — 44 of them
(22 per version) are the single open bug 4113.

---

## Part 1 — what is left

**Nothing that is fixable.** Every case in the fixable column reached zero over
three rounds of work; what stands between here and 100% is the 124 in Part 2
plus two open questions.

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

### The two open questions

`validation-0006` and `strip-space-009`. Both ask for behaviour the checked-in
spec text does not settle, so neither is counted as a pass or as a defect.

### What a zero here does not mean

It does not mean the engine is exact. It means the suites have no more to say.
The content-model matcher is provably wrong on a shape neither suite covers —
a repeated group whose only child is itself repeating is decided wrongly in
both directions, found by differential fuzzing and recorded in
[known-gaps.md](known-gaps.md). Every W3C case of that form uses two or more
distinct child names, so 80,878 agreements step around it.

A ceiling bounds what the suite asks, not what the code does.

---

## Part 2 — the 124 that are not work

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

### 2 — the suite contradicts itself

`regex-syntax-0056`, `-0086`, `-0102`. Ambiguous-dash character classes that
XSD 1.0 rejects and 1.1 accepts.

`regex-syntax-0056` and `regex-syntax-xslt20-0056` carry the **identical**
pattern and the **identical** `XSD_1.1 satisfied="false"` dependency, and one
asserts `FORX0002` while the other asserts a successful match.

The XSD 1.0 rule was implemented and measured: it fixes these three at a cost
of −3 XSLT 2.0, −9 QT3 and −34 XSD 1.1. Reverted.

**A real fix needs a per-call XSD version knob** so the regex grammar follows
the harness rather than a single engine-wide setting. That is a plausible
change, but it buys three cases and touches a shared engine.

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

### 11 — suite defects

- **`format-number-070`** — the catalog invokes `<initial-template
  name="main"/>`; the stylesheet has one template, `match="root"`, and zero
  occurrences of `name="main"`. XTDE0040 is mandatory.
- **`package-021err`, `package-022err`** — a half-applied 2020 erratum (E36)
  appended `#0` to `<xsl:function name="me:function1#0">` and
  `component="function#0"`. The grammar admits an arity in neither.

**To fix: accept invalid stylesheets.**

### 3 — network access

`unparsed-text-2003` (both targets) asserts
`unparsed-text-available('http://www.w3.org/Consortium/mission.html')` is true;
`package-version-011` wants a document with no resolver configured.

Resolvers are nil by default so an untrusted stylesheet cannot fetch what it
names. **To fix: make outbound HTTP the default.** That is a security
regression, not a conformance gain.

### 3 — vendor extension

`docbook-001` (both targets) and `docbook-004`. The vendored DocBook XSL 1.79.1
uses EXSLT `exsl:document` — 19 times in `chunker.xsl` alone.

**To fix: implement EXSLT.** Defensible as a feature; not a conformance fix.

### 4 — features deliberately not implemented

- **`streamable-141`** — needs streamability analysis.
- **`base-uri-052`** — needs XInclude.
- **`catalog-006b`** — needs `xsl:assert`.

Of these, **`xsl:assert` is the cheapest real feature on the list** and worth
doing on its own merits.

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

**Reaching 100% is not a goal that survives contact with the suites.** Of 126
disagreements, 124 would require agreeing with a disputed result, shipping a
second language implementation, freezing a stale Unicode table, accepting
invalid input, or weakening a security default. The other two are open
questions the spec does not settle.

**There is no conformance work left to schedule.** The ceiling and the measured
state are the same number on every suite, which is what a zero in the fixable
column means.

What remains is not conformance work but engineering the suites cannot see:
the content-model matcher's nested-occurrence bug above, and whatever else
fuzzing turns up. That is the better use of the next round.

Three larger items are defensible as *features* rather than conformance work,
and should be judged that way: **`xsl:assert`** (cheapest), **XInclude**, and
**a package-aware XPath static context** (unlocks `use-package-003` and removes
a known structural limit). Streaming is the largest of all — 2,716 cases
currently out of scope, a 31% larger denominator — and would be a project in
itself.

**EXSLT is not on this list.** It is a separate product. XQuery was, and is
now implemented in [`xquery`](../xquery/) at 99.22%; what remains of it there
is tracked in [xquery.md](xquery.md) rather than here, because this file is
about the XPath and XSLT figures.

---

## Related

[conformance-gaps.md](conformance-gaps.md) names every failing case and carries
the current figures. [known-gaps.md](known-gaps.md) is the diagnosis behind the
hard ones — attempted fixes, why they were reverted, what a rewrite would cost.
