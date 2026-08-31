# What reaching 100% would take

Measured at commit `56543f9`. The short answer: **100% is not reachable on any
of these suites**, and the great majority of what remains are cases where
passing would mean shipping something *less* correct. What follows separates
the work that exists from the work that does not.

| | Failing | Fixable | Open | Cannot fix |
|---|---:|---:|---:|---:|
| XPath 3.1 | 0 | 0 | 0 | 0 |
| XSLT 2.0 | 9 | 0 | 0 | 9 |
| XSLT 3.0 | 27 | 6 | 1 | 20 |
| XSD 1.0 | 53 | 4 | 0 | 49 |
| XSD 1.1 | 61 | 16 | 0 | 45 |
| **Total** | **150** | **26** | **1** | **123** |

XPath 2.0, XPath 3.0, XPath 3.1 and RELAX NG are already at 100%.

For XSD the fixable/cannot-fix split is not a judgement call: it is the suite's
own `status` field. A case marked `accepted` is a settled expectation and so is
real work; one marked `queried` or `stable bugNNNN` is one the W3C has itself
challenged, and 99 of the 142 XSD disagreements are of that kind — 44 of them
(22 per version) are the single open bug 4113.

---

## Part 1 — the 26 that are real work

This is the whole of what "reaching the ceiling" means. None of it is
speculative; every case has a diagnosed shape.

### XSD schema validity — 39 cases, the largest block

**9 on XSD 1.0, 30 on 1.1**, counting only what the suite marks `accepted`
and setting aside four `notQName` cases the suite omitted a `version="1.1"` on.
Two different problems wearing one label:

| Kind | 1.0 | 1.1 | What it means |
|---|---:|---:|---|
| `SFALSEACCEPT` | 5 | 22 | A schema-validity rule that is not checked yet. Additive: write the rule, the case passes. |
| `SFALSEREJECT` | 6 | 7 | The opposite — a rule applied too strictly. **Riskier**: loosening one can re-admit a false accept elsewhere. |
| `IFALSEREJECT` | 2 | 1 | Instance validation. |

The false-accept half is the tractable one, and two agent rounds have now
demonstrated it: ~90 of these cleared by writing rules one at a time, with no
agreement count falling. What is left is thinner and concentrated in 1.1 —
the `All` group (`all009`, `all218`, `all237`, `all308`, `all313`), open
content (`open036`, `open046`, `open048`) and wildcards (`wild049`, `wild050`,
`wild057`, `wild069`).

The `SFALSEREJECT` side is where the risk sits: `particlesZ001`,
`s3_10_6ii01`/`ii02` and `s3_10_1ii08`/`ii09` need the content-model and
wildcard restriction tables *loosened*, and each loosening has to be measured
against both versions to show it has not re-admitted a false accept.

**Effort:** the additive rules are days, one rule at a time, each measured
against both versions. The over-strict four and the reachability four are
genuinely harder and may not be worth their cost.

### XSLT 3.0 long tail — 8 cases

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

### The one open question

- **`validation-0006`** — a parentless attribute: `XTTE1555` wanted,
  `XTTE1540` given.

`sequence-0132` was the other, and has been settled as not implementable. It
is scoped `XSLT20+` so it must pass at both versions; it passes at 3.0 and
fails at 2.0. Reaching the type check at 2.0 requires the content model to
accept content on `xsl:sequence`, and `sequence-2401a` — scoped `XSLT20` —
requires it to reject exactly that with `XTSE0010`. Measured: removing the
gate takes 2.0 from 6149 to 6148, trading one case for the other, and `0132`
still fails because the code reported then is `XTSE3185`, which does not exist
before 3.0.

### If all 55 landed

| Suite | Now | Ceiling |
|---|---|---|
| XPath 3.1 | 100.00% | **100.00%** |
| XSLT 2.0 | 99.85% | **99.85%** |
| XSLT 3.0 | 99.69% | **99.76%** |
| XSD 1.0 | 99.87% | **99.88%** |
| XSD 1.1 | 99.85% | **99.89%** |

---

## Part 2 — the 123 that are not work

Grouped by what would actually have to change.

### 99 — the W3C disputes its own expected result

XSD 1.0 (50) and 1.1 (49). The suite records a `status` on each test:
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

**Reaching 100% is not a goal that survives contact with the suites.** Of 183
disagreements, 123 would require agreeing with a disputed result, shipping a
second language implementation, freezing a stale Unicode table, accepting
invalid input, or weakening a security default.

What is achievable:

1. **XSD false-accept rules** — 48 cases, additive, demonstrated at scale.
2. **XSLT 3.0 long tail** — 36 cases, no shared cause, low risk.
3. **Re-triage the six marginal XSD rows** — cheap, and the totals are wrong
   without it.
4. **Two singles** — `json-to-xml-048`, `validation-0201`.
5. **XSD over-strictness** — 17 cases, riskier, each can regress a false accept.

That is **~26 cases and a ceiling of about 99.9% across the board**.

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
