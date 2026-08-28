# What reaching 100% would take

Measured at commit `26f12b3`. The short answer: **100% is not reachable on any
of these suites**, and roughly half the remaining disagreements are cases where
passing would mean shipping something *less* correct. What follows separates
the work that exists from the work that does not.

| | Failing | Fixable | Open | Cannot fix |
|---|---:|---:|---:|---:|
| XPath 3.1 | 4 | 1 | 0 | 3 |
| XSLT 2.0 | 9 | 1 | 1 | 7 |
| XSLT 3.0 | 52 | 33 | 1 | 18 |
| XSD 1.0 | 73 | 21 | 0 | 52 |
| XSD 1.1 | 104 | 50 | 0 | 54 |
| **Total** | **242** | **106** | **2** | **134** |

XPath 2.0, XPath 3.0 and RELAX NG are already at 100%.

---

## Part 1 — the 106 that are real work

This is the whole of what "reaching the ceiling" means. None of it is
speculative; every case has a diagnosed shape.

### XSD schema validity — 71 cases, the largest block

**21 on XSD 1.0, 50 on 1.1.** Two different problems wearing one label:

| Kind | 1.0 | 1.1 | What it means |
|---|---:|---:|---|
| `SFALSEACCEPT` | 11 | 37 | A schema-validity rule that is not checked yet. Additive: write the rule, the case passes. |
| `SFALSEREJECT` | 6 | 11 | The opposite — a rule applied too strictly. **Riskier**: loosening one can re-admit a false accept elsewhere. |
| `IFALSEACCEPT` / `IFALSEREJECT` | 4 | 2 | Instance validation, both directions. |

The false-accept half is tractable and was just demonstrated: an agent cleared
80 of these in one pass by writing seventeen rules, with no agreement count
falling. The remainder is thinner — `particlesZ001`, `Z028`, `Hb008` and
`Hb011` need the content-model restriction table *loosened*, and four more
(`simple006`, `idB005`, `over024`, `over026`) are unresolved type references
that are structurally identical to `saxonData/Missing/missing001`, which the
suite marks **valid** on the "error only if the declaration is needed for
validation" reading. Separating those needs reachability analysis over the
schema graph.

**Effort:** the additive rules are days, one rule at a time, each measured
against both versions. The over-strict four and the reachability four are
genuinely harder and may not be worth their cost.

### XSLT 3.0 long tail — 33 cases

No concentration left. Package composition was a third of the failures and is
now 5, all unreachable. What remains is one or two cases across thirty test
sets: snapshots, higher-order functions, namespace fixup on copy, two missing
static checks, `xsl:number` integer overflow, result-document base URI,
`schema-element()` in a `use-when` test.

**Effort:** each is its own small investigation with no shared cause. Steady,
unglamorous, low-risk. This is the bulk of the remaining XSLT work by count and
the least of it by difficulty.

### The two singles

- **`json-to-xml-048`** (XPath 3.1) — `\r`, `\t` and an escaped space must
  serialise as numeric character references; we emit them literally. A real
  escaping bug, one case, small.
- **`validation-0201`** (XSLT 2.0 and 3.0) — XHTML output method, `<meta
  http-equiv>` placement in `<head>`. One case, cosmetic but real.

### The two open questions

- **`sequence-0132`** — `xsl:sequence` with content and no `@select`. A fix was
  implemented and reverted: it fixed this and broke `sequence-0137`, because
  `0132` is scoped `XSLT20+` and `0137` is `XSLT20`-only and they want opposite
  answers for the same construct. Needs a rule that separates them, which may
  not exist.
- **`validation-0006`** — a parentless attribute: `XTTE1555` wanted,
  `XTTE1540` given.

### If all 109 landed

| Suite | Now | Ceiling |
|---|---|---|
| XPath 3.1 | 99.98% | **99.99%** |
| XSLT 2.0 | 99.85% | **99.89%** |
| XSLT 3.0 | 99.40% | **99.79%** |
| XSD 1.0 | 99.81% | **99.87%** |
| XSD 1.1 | 99.75% | **99.87%** |

---

## Part 2 — the 134 that are not work

Grouped by what would actually have to change.

### 106 — the W3C disputes its own expected result

XSD 1.0 (52) and 1.1 (54). The suite records a `status` on each test:
`accepted` means settled, **`queried` means the W3C has itself challenged the
expectation**, usually with a bugzilla number. 30 are `queried` in each
version, ~20 more are `stable` but carry an open bug.

**All 44 `MS-Regex` disagreements — 22 in each version — are one bug, 4113.**

To pass these we would have to agree with results the working group does not
stand behind. That is not conformance; it is bug-compatibility with a specific
processor. **Nothing to do here, and doing it would be wrong.**

*Caveat found while writing this:* six of the 106 are marginal —
`wildZ013`, `stZ007`, `stZ047`, `stZ055` are `accepted` but carry an open bug,
and `sg-abstract-edc` and `iri-001` have no status at all. `iri-001` is the
DOCTYPE refusal (deliberate, security). The other five are arguably fixable and
should be re-triaged; they would move the totals to roughly 114 fixable / 126
unfixable.

### 3 — no XQuery processor

`fn-load-xquery-module-003`, `-004`, `fn-function-lookup-764`.

`fn:load-xquery-module` compiles an XQuery library module. This engine
implements XPath and XSLT. F&O 3.1 anticipates exactly this: **FOQM0006 is
defined as "the implementation does not support the load-xquery-module
function"**, and raising it is the conforming answer.

Two of the suite's own cases cannot both pass — `-003` wants FOQM0002 ("cannot
be located") for an expression `-903` wants FOQM0006 for. Locating a module is
something only a processor could do.

**To fix: write an XQuery engine.** That is a second language implementation,
not a conformance fix.

### 3 — the suite contradicts itself

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

### 3 — suite defects

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

### 3 — features deliberately not implemented

- **`streamable-141`** — needs streamability analysis.
- **`base-uri-052`** — needs XInclude.
- **`catalog-006b`** — needs `xsl:assert`.

Of these, **`xsl:assert` is the cheapest real feature on the list** and worth
doing on its own merits.

### 3 — package composition, at a cost

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

## Part 3 — the honest bottom line

**Reaching 100% is not a goal that survives contact with the suites.** Of 242
disagreements, 134 would require agreeing with a disputed result, shipping a
second language implementation, freezing a stale Unicode table, accepting
invalid input, or weakening a security default.

What is achievable:

1. **XSD false-accept rules** — 48 cases, additive, demonstrated at scale.
2. **XSLT 3.0 long tail** — 36 cases, no shared cause, low risk.
3. **Re-triage the six marginal XSD rows** — cheap, and the totals are wrong
   without it.
4. **Two singles** — `json-to-xml-048`, `validation-0201`.
5. **XSD over-strictness** — 17 cases, riskier, each can regress a false accept.

That is **~106 cases and a ceiling of about 99.8% across the board**.

Three larger items are defensible as *features* rather than conformance work,
and should be judged that way: **`xsl:assert`** (cheapest), **XInclude**, and
**a package-aware XPath static context** (unlocks `use-package-003` and removes
a known structural limit). Streaming is the largest of all — 2,716 cases
currently out of scope, a 31% larger denominator — and would be a project in
itself.

**An XQuery engine and EXSLT are not on this list.** They are separate products.

---

## Related

[conformance-gaps.md](conformance-gaps.md) names every failing case and carries
the current figures. [known-gaps.md](known-gaps.md) is the diagnosis behind the
hard ones — attempted fixes, why they were reverted, what a rewrite would cost.
