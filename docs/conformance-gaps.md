# W3C conformance: the remaining gaps

Measured at commit `69c53cf` with `tests/check.sh`. Every figure here comes
from a full run of the suite it names; nothing is estimated.

| Suite | In scope | Passing | Failing | |
|---|---:|---:|---:|---|
| XPath 2.0 (QT3) | 15,183 | 15,183 | 0 | 100.00% |
| XPath 3.0 (QT3) | 19,236 | 19,236 | 0 | 100.00% |
| XPath 3.1 (QT3) | 21,786 | 21,782 | 4 | 99.98% |
| **XSLT 2.0** | 6,158 | 6,149 | **9** | **99.85%** |
| **XSLT 3.0** | 8,623 | 8,548 | **75** | **99.13%** |
| XSD 1.0 | 39,404 | 39,305 | 99 | 99.75% |
| XSD 1.1 | 41,570 | 41,412 | 158 | 99.62% |
| RELAX NG | 965 | 965 | 0 | 100.00% |

The XSD and RELAX NG rows are given for context only; this document analyses
the two XSLT suites. The XSD figures count agreement with the suite's expected
verdict on each schema and instance, which is a different measure from a
pass/fail case count.

## How to read the verdicts

**Implementable** — a defect in this engine. The spec says one thing, we do
another, and the fix is ours to make.

**Not implementable** — passing would require violating the specification,
reaching the network, shipping a vendor extension, or encoding a snapshot of
Unicode that is no longer current. These are not deferred work.

**Open question** — the correct answer is not settled by the text I have.
Recorded honestly rather than assigned to whichever bucket is flattering.

---

## XSLT 2.0 — 9 failures

Six cannot be fixed; three are ours.

| Case | What happens | Verdict | Why |
|---|---|---|---|
| `format-number-070` | We raise `XTDE0040: no template named "main"` | **Not implementable** | Suite defect. The catalog invokes `<initial-template name="main"/>`; the stylesheet contains exactly one template, `match="root"`, and zero occurrences of `name="main"` (verified by grep). The spec is unambiguous: XTDE0040 is raised when the invocation "specifies a template name that does not match the expanded QName of a named template defined in the stylesheet" and says "**It is a dynamic error**" — mandatory, not optional. Passing means violating the spec. |
| `unparsed-text-2003` | First of four assertions returns `false` | **Not implementable** | Requires fetching `http://www.w3.org/Consortium/mission.html` over the network. The other three assertions (local files) all pass. The catalog declares no network dependency; it assumes live internet. Resolvers are nil by default here so an untrusted stylesheet cannot fetch what it names — a deliberate posture, not an oversight. |
| `docbook-001` | `XTDE1450: extension instruction exsl:document is not available` | **Not implementable** | The vendored DocBook XSL 1.79.1 uses the EXSLT `exsl:document` extension element — 19 times in `chunker.xsl` alone. A vendor extension outside the XSLT specification; passing means shipping an EXSLT module. |
| `regex-syntax-xslt20-0984` | `[\w]` does not match U+2308 `⌈` | **Not implementable** | Unicode drift. U+2308 LEFT CEILING is category **Ps**, and XSD Appendix F defines `\w` by subtracting `\p{P}`, so excluding it is correct. The 2012 test predates Unicode 6.1, which recategorised it from `Sm`. Go and Python's independent Unicode 14 tables agree with us. |
| `regex-syntax-xslt20-0985` | `[\d]` does not match U+1369 `፩` | **Not implementable** | Same drift. ETHIOPIC DIGIT ONE is category **No**, and `\d` is defined as `\p{Nd}`. Correct to exclude. |
| `regex-syntax-xslt20-0987` | `[\c]` matches U+0346 `͆` | **Not implementable** | Same drift, inverted: the test asserts it must *not* match, but XML NameChar explicitly includes `#x0300-#x036F` and U+0346 is inside it. |
| `sequence-0132` | `XTSE0010` where `XTTE0570` is wanted | **Open question** | `xsl:sequence` with content and no `@select`. Attempted and reverted: removing the `processorAtLeast30()` gate fixed this case and broke `sequence-0137`. The two are the version rule in miniature — `0132` is scoped `XSLT20+`, `0137` is scoped `XSLT20`, same construct, deliberately different answers. Needs a rule that separates them. |
| `import-schema-137` | `XTTE1512` where `XTTE1510` is wanted | **Open question** | Both errors are genuinely present. XSLT 2.0 §19.2 defines XTTE1512 as strict validation with "no matching top-level declaration" and XTTE1510 as "assessed and found invalid". `z:familyname` really is absent from `schema061.xsd` (only `surname` is declared), so our code is literally correct — but the enclosing `z:person` is also invalid, and the suite wants that one. A validation-**ordering** question, not a wrong-code bug. |
| `validation-0201` | Serialisation differs at offset 46 | **Implementable** | XHTML output method: a `<meta http-equiv>` element placed differently in `<head>`. Ours to fix. |

**Realistic XSLT 2.0 ceiling: 6,152 / 6,158 = 99.90%** if all three of the last
group land. The other six are permanently out of reach.

### The three regex cases, in context

The strongest evidence that our character classes are right is not the spec
reading above — it is the suite itself. The XSLT 3.0 `regex-syntax` set runs
**987 cases and 984 pass**, and none of its three failures involves `\w`, `\d`
or `\c` membership. Only these three 2012-era XSLT 2.0 cases disagree, and each
disagrees exactly where Unicode changed underneath them.

---

## XSLT 3.0 — 75 failures

### Package composition — 28

The largest concentration, and the most tractable: these are one feature area,
and the work this session took it from 34 to 28.

| Cases | What happens | Verdict | Why |
|---|---|---|---|
| `package-021err`, `package-022err` | We raise `XTSE3030` / `XTSE0020` where `XTSE3050` is wanted | **Not implementable** | Suite defect from a half-applied 2020 erratum (E36, "function arity must be given in accept/expose"). The editor appended `#0` to every occurrence, including `<xsl:function name="me:function1#0">` and `component="function#0"`. The spec's own syntax summary gives `xsl:function/@name` as an `eqname` and `@component` as the enumeration `"template" \| "function" \| "attribute-set" \| "variable" \| "mode"` — neither admits an arity. Passing means accepting invalid stylesheets. |
| `package-100`, `package-101` | `XPST0017: unknown function csv:preprocess-field` | **Implementable** | The function is declared at line 76 of the same package with no `visibility` attribute (so private) and called from within that package, where it must resolve. A genuine visibility bug. |
| `package-200` | `XTSE0020` where `XTSE3000` is wanted | **Implementable** | A malformed `@package-version` range should be reported as "no package matches" once the range parses as a *quoted* literal. Error-code precedence. |
| `package-001j`, `package-910`, `package-912` | Transform succeeds; an error was wanted | **Implementable** | Three missing static/dynamic checks (`XTDE0045`, `XTSE0165`, `XPDY0002`). |
| `override-f-019`, `override-f-020`, `override-t-003a`, `override-v-003`, `override-v-006`, `override-v-007`, `override-v-015`, `override-as-003`, `override-as-005`, `override-m-012` (10) | Mixed: 4 missing errors, 3 wrong codes, 3 wrong results | **Implementable** | `xsl:override` semantics: `$xsl:original` binding in overriding variables, the `XTSE3070`/`XTSE0770`/`XTSE3058` checks, and attribute-set override composition. All ours. |
| `accept-021`, `accept-022` | `XTSE3050` raised when it should not be | **Implementable** | Two packages legitimately exposing the same name; the clash test is too eager. |
| `accept-913` | `XTDE3052` where `XTDE0040` is wanted | **Implementable** | Error precedence: entry-point visibility should be checked before abstract-component invocation. |
| `expose-003`, `expose-007` | One missing error, one over-strict visibility | **Implementable** | |
| `use-package-003`, `use-package-103`, `use-package-108b`, `use-package-171` (4) | Namespace aliasing, accept-token matching, one missing `XPST0017` | **Implementable** | `use-package-103` and `-108b` are namespace-alias propagation across a package boundary; `-171` is an accept token that should match a mode two packages down. |
| `package-version-011` | `FODC0002: document access is disabled` | **Not implementable** | Reaches for a document with no resolver configured — same deliberate posture as `unparsed-text-2003`. |

### Schema-aware validation — 6

| Cases | Verdict | Why |
|---|---|---|
| `validation-0006`, `si-copy-117`, `si-copy-of-117` | **Open question** | All three report `XTTE1540`/`XTTE1555` where `XTTE1510` is wanted. The same validation-**ordering** question as `import-schema-137`: which of several genuine errors surfaces first. |
| `import-schema-137`, `validation-0201` | **Open question** / **Implementable** | See the XSLT 2.0 table. |
| `catalog-001` | **Implementable** | `schema-element()` in a `use-when` test, where no schema is imported. |

### Regex — 3

| Cases | Verdict | Why |
|---|---|---|
| `regex-syntax-0056`, `regex-syntax-0086`, `regex-syntax-0102` | **Implementable** | Patterns like `[^a-d-b-c]` and `[a-a-x-x]+` — nested character-class subtraction with an ambiguous `-`. These must raise `FORX0002` and we accept them silently. A genuine gap, found while auditing the XSLT 2.0 cases. |

### Deliberately out of scope — 4

| Cases | Verdict | Why |
|---|---|---|
| `streamable-141` | **Not implementable** | Requires streamability analysis. Streaming is not implemented — 2,716 cases are skipped as out of scope, not counted as failures. This one is in scope only because it also depends on `backwards_compatibility`. |
| `base-uri-052` | **Not implementable** | The environment declares `xinclude="true"`; XInclude processing is not implemented. |
| `docbook-001`, `docbook-004` | **Not implementable** | EXSLT `exsl:document`, as above. |

### Long tail — 34

One or two cases each across 30 sets. All **implementable** unless noted;
none shares a cause with another, so each is its own small investigation.

| Area | Cases | Note |
|---|---|---|
| Snapshots | `snapshot-0102a`, `snapshot-0112` | `fn:snapshot` typing and identity |
| Higher-order functions | `higher-order-functions-007`, `higher-order-functions-034` | Named function references to undeclared names |
| Copy | `copy-1221`, `copy-3002` | Namespace fixup; missing `XTDE3362` |
| Errors | `error-0640e-2`, `error-3105a` | Two missing static checks |
| Base URI | `base-uri-053` | Result-document base URI |
| Catalog | `catalog-005b`, `catalog-006b`, `catalog-009` | `catalog-006b` needs `xsl:assert` (**not implemented**) |
| Numbering | `number-0111` | Integer overflow in `xsl:number` formatting |
| Functions | `function-0117`, `function-0303`, `function-lookup-005` | |
| Misc | `static-032`, `accumulator-038`, `context-item-903`, `castable-006`, `math-3701`, `accessor-064`, `collection-006`, `copy-of-009`, `current-output-uri-902`, `outermost-002`, `type-available-0151`, `for-each-group-002`, `collations-1006`, `forwards-011`, `initial-template-004`, `seqtor-017`, `notation-0002`, `unparsed-text-2003` | `unparsed-text-2003` is the network case again |

---

## What is skipped, and why that is not a gap

The XSLT 3.0 suite has 14,601 cases; 8,623 are in scope. The 5,978 skipped are
excluded by declared dependency, not by failure:

| Skipped | Reason |
|---:|---|
| 2,716 | **streaming** — not implemented, deliberately |
| 1,580 | depends on a specific Unicode version |
| 1,098 | scoped `XSLT20` only |
| 107 | numbering combinations |
| 98 | XPath 3.1 features |
| 96 | year-component values |
| 65 | XML 1.1 — the parser implements XML 1.0 |
| 38 | initial function (XSLT 3.0) |
| 33 | `disable-output-escaping` — the serializer escapes always |
| 22 | require schema-awareness to be *absent* |
| 18 | `xsl-stylesheet-processing-instruction` |

Counting these as failures would understate the engine; counting them as passes
would overstate it. They are reported separately for that reason.

## Summary

| | Total | Implementable | Not implementable | Open question |
|---|---:|---:|---:|---:|
| XSLT 2.0 | 9 | 1 | 6 | 2 |
| XSLT 3.0 | 75 | 62 | 9 | 4 |

The nine XSLT 3.0 cases that cannot be fixed are `package-021err` and
`package-022err` (the erratum defect), `package-version-011` and
`unparsed-text-2003` (no resolver by design), `streamable-141` (streaming),
`base-uri-052` (XInclude), `docbook-001` and `docbook-004` (EXSLT), and
`catalog-006b` (needs `xsl:assert`). The four open questions are the
validation-ordering group: `import-schema-137`, `validation-0006`,
`si-copy-117` and `si-copy-of-117`.

The two suites' realistic ceilings, if every implementable case lands and the
open questions resolve our way:

- **XSLT 2.0 — 99.90%** (6,152 / 6,158)
- **XSLT 3.0 — 99.90%** (8,614 / 8,623)

Neither reaches 100%, and the reason is the same in both: a small number of
cases encode a suite defect, a network fetch, a vendor extension, or a Unicode
snapshot that has since moved. Those are not deferred work — passing them would
mean shipping something less correct.
