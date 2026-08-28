# W3C conformance: the remaining gaps

Every figure here comes from a full run of the suite it names, measured at
commit `69c53cf` with `tests/check.sh`. Nothing is estimated.

| Component | Suite | In scope | Passing | Failing | |
|---|---|---:|---:|---:|---|
| **xdm** | *(no external suite)* | — | — | — | see below |
| **xpath** | QT3 — XPath 2.0 | 15,183 | 15,183 | 0 | 100.00% |
| **xpath** | QT3 — XPath 3.0 | 19,236 | 19,236 | 0 | 100.00% |
| **xpath** | QT3 — XPath 3.1 | 21,786 | 21,782 | **4** | 99.98% |
| **xslt** | W3C XSLT 2.0 | 6,158 | 6,149 | **9** | 99.85% |
| **xslt** | W3C XSLT 3.0 | 8,623 | 8,548 | **75** | 99.13% |
| **xsd** | W3C xsdtests 1.0 | 39,404 | 39,305 | **99** | 99.75% |
| **xsd** | W3C xsdtests 1.1 | 41,570 | 41,412 | **158** | 99.62% |
| **relaxng** | Clark spectest | 965 | 965 | 0 | 100.00% |

**345 disagreements in total**, of which **213 are ours to fix**, **126 cannot
be fixed without shipping something less correct**, and **6 are open
questions**.

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

# xpath — 4 failures, none implementable

All four are XPath 3.1; XPath 2.0 and 3.0 are at 100%.

| Case | What happens | Verdict | Why |
|---|---|---|---|
| `fn-load-xquery-module-003` | We raise `FOQM0006`, suite wants `FOQM0002` | **Not implementable** | `fn:load-xquery-module` compiles an XQuery library module. This engine implements XPath and XSLT and has no XQuery processor. F&O 3.1 defines **FOQM0006** as "the implementation does not support the load-xquery-module function", which is the conforming answer. |
| `fn-load-xquery-module-004` | Same | **Not implementable** | Same. |
| `fn-function-lookup-764` | `FOQM0001` where `FOQM0006` is wanted | **Not implementable** | Reaches the same function through `function-lookup`. The argument check (empty module URI → FOQM0001) is a fact about the *call* and fires before the absence of a processor is reported. |
| `json-to-xml-048` | Control characters serialised literally rather than as `&#13;` | **Implementable, but disputed** | `json-to-xml('"\\\/\"\r\t "')`. We emit the raw characters; the suite wants numeric character references. A serialisation-escaping question. |

**These three cannot all pass at once.** `load-xquery-module-003` requires
`FOQM0002` ("module cannot be located") for an expression that `-903` requires
`FOQM0006` for. Locating a module is something only a processor could do, so
FOQM0006 is the honest answer and `-003`/`-004` fail rather than pretending to
have looked. That reasoning is recorded in `xpath/fn_31.go`.

**XPath ceiling: 21,783 / 21,786 = 99.99%.**

---

# xslt — 84 failures

## XSLT 2.0 — 9 failures

Six cannot be fixed; three are ours.

| Case | What happens | Verdict | Why |
|---|---|---|---|
| `format-number-070` | `XTDE0040: no template named "main"` | **Not implementable** | Suite defect. The catalog invokes `<initial-template name="main"/>`; the stylesheet contains exactly one template, `match="root"`, and zero occurrences of `name="main"` (verified by grep). The spec: XTDE0040 is raised when the invocation "specifies a template name that does not match the expanded QName of a named template defined in the stylesheet" and "**It is a dynamic error**" — mandatory. Passing means violating the spec. |
| `unparsed-text-2003` | First of four assertions returns `false` | **Not implementable** | Requires fetching `http://www.w3.org/Consortium/mission.html`. The three local-file assertions pass. The catalog declares no network dependency; it assumes live internet. Resolvers are nil by default here so an untrusted stylesheet cannot fetch what it names — a deliberate posture. |
| `docbook-001` | `XTDE1450: exsl:document is not available` | **Not implementable** | The vendored DocBook XSL 1.79.1 uses the EXSLT `exsl:document` extension element — 19 times in `chunker.xsl` alone. A vendor extension outside the XSLT specification. |
| `regex-syntax-xslt20-0984` | `[\w]` does not match U+2308 `⌈` | **Not implementable** | Unicode drift. U+2308 LEFT CEILING is category **Ps**, and XSD Appendix F defines `\w` by subtracting `\p{P}`, so excluding it is correct. The 2012 test predates Unicode 6.1, which recategorised it from `Sm`. Go and Python's independent Unicode 14 tables agree with us. |
| `regex-syntax-xslt20-0985` | `[\d]` does not match U+1369 `፩` | **Not implementable** | Same drift. ETHIOPIC DIGIT ONE is category **No**; `\d` is `\p{Nd}`. |
| `regex-syntax-xslt20-0987` | `[\c]` matches U+0346 `͆` | **Not implementable** | Same drift, inverted: the test asserts it must *not* match, but XML NameChar includes `#x0300-#x036F`. |
| `sequence-0132` | `XTSE0010` where `XTTE0570` is wanted | **Open question** | `xsl:sequence` with content and no `@select`. Attempted and reverted: removing the `processorAtLeast30()` gate fixed this and broke `sequence-0137`. `0132` is scoped `XSLT20+`, `0137` is scoped `XSLT20` — same construct, deliberately different answers. Needs a rule that separates them. |
| `import-schema-137` | `XTTE1512` where `XTTE1510` is wanted | **Open question** | Both errors are genuinely present. §19.2 defines XTTE1512 as strict validation with "no matching top-level declaration", XTTE1510 as "assessed and found invalid". `z:familyname` really is absent from `schema061.xsd` (only `surname` is declared), so our code is literally correct — but the enclosing `z:person` is also invalid, and the suite wants that. A validation-**ordering** question. |
| `validation-0201` | Serialisation differs at offset 46 | **Implementable** | XHTML output method: `<meta http-equiv>` placement in `<head>`. |

**XSLT 2.0 ceiling: 6,152 / 6,158 = 99.90%.**

### Why the three regex cases are not ours

The strongest evidence is not the spec reading above — it is the suite itself.
The XSLT 3.0 `regex-syntax` set runs **987 cases and 984 pass**, and none of its
three failures involves `\w`, `\d` or `\c` membership. Only these three
2012-era XSLT 2.0 cases disagree, each exactly where Unicode moved underneath
them.

## XSLT 3.0 — 75 failures

### Package composition — 28

The largest concentration and the most tractable. This session took it from 34.

| Cases | Verdict | Why |
|---|---|---|
| `package-021err`, `package-022err` | **Not implementable** | Suite defect from a half-applied 2020 erratum (E36, "function arity must be given in accept/expose"). The editor appended `#0` to every occurrence, including `<xsl:function name="me:function1#0">` and `component="function#0"`. The spec gives `xsl:function/@name` as an `eqname` and `@component` as the enumeration `"template" \| "function" \| "attribute-set" \| "variable" \| "mode"` — neither admits an arity. |
| `package-100`, `package-101` | **Implementable** | `csv:preprocess-field` is declared at line 76 of the same package with no `visibility` (so private) and called from within that package, where it must resolve. A visibility bug. |
| `package-200` | **Implementable** | Error-code precedence: a quoted `@package-version` should reach `XTSE3000`, not `XTSE0020`. |
| `package-001j`, `package-910`, `package-912` | **Implementable** | Three missing checks: `XTDE0045`, `XTSE0165`, `XPDY0002`. |
| `package-version-011` | **Not implementable** | Reaches for a document with no resolver — same deliberate posture as `unparsed-text-2003`. |
| `override-f-019`, `override-f-020`, `override-t-003a`, `override-v-003`, `override-v-006`, `override-v-007`, `override-v-015`, `override-as-003`, `override-as-005`, `override-m-012` | **Implementable** | `xsl:override` semantics: `$xsl:original` binding in overriding variables, the `XTSE3070`/`XTSE0770`/`XTSE3058` checks, attribute-set override composition. |
| `accept-021`, `accept-022` | **Implementable** | `XTSE3050` raised when two packages legitimately expose the same name. |
| `accept-913` | **Implementable** | Precedence: entry-point visibility before abstract-component invocation. |
| `expose-003`, `expose-007` | **Implementable** | One missing error, one over-strict visibility check. |
| `use-package-003`, `use-package-103`, `use-package-108b`, `use-package-171` | **Implementable** | Namespace-alias propagation across a package boundary; an accept token that should match a mode two packages down. |

### Schema-aware validation — 6

| Cases | Verdict | Why |
|---|---|---|
| `validation-0006`, `si-copy-117`, `si-copy-of-117`, `import-schema-137` | **Open question** | All report `XTTE1540`/`XTTE1512`/`XTTE1555` where `XTTE1510` is wanted. The same validation-**ordering** question: which of several genuine errors surfaces first. |
| `validation-0201` | **Implementable** | XHTML serialisation, as above. |
| `catalog-001` | **Implementable** | `schema-element()` in a `use-when` test with no schema imported. |

### Regex — 3

| Cases | Verdict | Why |
|---|---|---|
| `regex-syntax-0056`, `regex-syntax-0086`, `regex-syntax-0102` | **Implementable** | Patterns like `[^a-d-b-c]` and `[a-a-x-x]+` — nested character-class subtraction with an ambiguous `-`. These must raise `FORX0002` and we accept them silently. Found while auditing the XSLT 2.0 cases. |

### Deliberately out of scope — 4

| Cases | Verdict | Why |
|---|---|---|
| `streamable-141` | **Not implementable** | Requires streamability analysis. Streaming is not implemented — 2,716 cases are skipped as out of scope. This one is in scope only because it also depends on `backwards_compatibility`. |
| `base-uri-052` | **Not implementable** | The environment declares `xinclude="true"`; XInclude is not implemented. |
| `docbook-001`, `docbook-004` | **Not implementable** | EXSLT `exsl:document`, as above. |

### Long tail — 34

One or two cases each across 30 sets. All **implementable** unless noted; none
shares a cause with another, so each is its own small investigation.

| Area | Cases | Note |
|---|---|---|
| Snapshots | `snapshot-0102a`, `snapshot-0112` | `fn:snapshot` typing and identity |
| Higher-order functions | `higher-order-functions-007`, `higher-order-functions-034` | Named function references to undeclared names |
| Copy | `copy-1221`, `copy-3002` | Namespace fixup; missing `XTDE3362` |
| Errors | `error-0640e-2`, `error-3105a` | Two missing static checks |
| Base URI | `base-uri-053` | Result-document base URI |
| Catalog | `catalog-005b`, `catalog-006b`, `catalog-009` | `catalog-006b` needs `xsl:assert` — **not implementable** |
| Numbering | `number-0111` | Integer overflow in `xsl:number` |
| Functions | `function-0117`, `function-0303`, `function-lookup-005` | |
| Misc | `static-032`, `accumulator-038`, `context-item-903`, `castable-006`, `math-3701`, `accessor-064`, `collection-006`, `copy-of-009`, `current-output-uri-902`, `outermost-002`, `type-available-0151`, `for-each-group-002`, `collations-1006`, `forwards-011`, `initial-template-004`, `seqtor-017`, `notation-0002`, `unparsed-text-2003` | `unparsed-text-2003` is the network case again — **not implementable** |

**XSLT 3.0 ceiling: 8,614 / 8,623 = 99.90%.**

The nine that cannot be fixed: `package-021err`, `package-022err`,
`package-version-011`, `unparsed-text-2003`, `streamable-141`, `base-uri-052`,
`docbook-001`, `docbook-004`, `catalog-006b`.

---

# xsd — 257 disagreements

The XSD suite measures **agreement with the expected verdict** on each schema
and instance, which is a different shape from a pass/fail case count. A
disagreement is one of four kinds:

- **SFALSEACCEPT** — we accept a schema the suite says is invalid
- **SFALSEREJECT** — we reject a schema the suite says is valid
- **IFALSEACCEPT** — we accept an instance the suite says is invalid
- **IFALSEREJECT** — we reject an instance the suite says is valid

Each test also carries a W3C **status**. `accepted` means the expected result
is settled. **`queried` means the W3C has itself challenged the expectation**,
usually with a bugzilla reference; `stable bugNNN` likewise carries an open
bug. Those are a ceiling rather than outstanding work, and the runner counts
them separately for that reason.

| | Total | `accepted` (ours) | `queried`/`stable` (ceiling) |
|---|---:|---:|---:|
| XSD 1.0 | 99 | **46** | 53 |
| XSD 1.1 | 158 | **103** | 55 |

## What the ceiling consists of

| Set | Cases | Status | Why |
|---|---:|---|---|
| `MS-Regex2006-07-15` | 22 (both versions) | `queried bug4113` | Every single MS-Regex disagreement is the *same* open W3C bug. The expected results are challenged upstream; agreeing with them would mean agreeing with something the working group does not stand behind. |
| `MS-Schema`, `MS-SimpleType`, `MS-Element`, `MS-DataTypes`, others | ~31–33 | `queried`/`stable` + bug | Assorted challenged expectations across the Microsoft-contributed sets. |

**Not implementable: 53 (XSD 1.0) and 55 (XSD 1.1).**

## What is genuinely ours

| Kind | XSD 1.0 | XSD 1.1 | Reading |
|---|---:|---:|---|
| `SFALSEACCEPT` | 36 | 90 | **Missing schema-validity rules** — the dominant gap in both versions. We accept schemas that violate a constraint we do not yet check. |
| `SFALSEREJECT` | 6 | 11 | Over-strict: a rule applied where it should not be. |
| `IFALSEACCEPT` | 2 | 1 | Missing instance-validation rule. |
| `IFALSEREJECT` | 2 | 1 | Over-strict instance validation. |

Concentrated by area:

| Area | XSD 1.0 | XSD 1.1 |
|---|---:|---:|
| `MS-Particles` — particle/content-model derivation | 10 | 16 |
| `MS-Additional` | 7 | 7 |
| `Simple` — simple-type facets | — | 9 |
| `Zone`, `All`, `Override`, `Open`, `Wild` — 1.1-specific | — | 26 |
| `MS-SimpleType`, `MS-IdentityConstraint`, `MS-Attribute` | 9 | — |

All **implementable**. The 1.1-specific clusters (`Override`, `Open`, `Wild`,
`All`) are the newer features — open content, wildcards, `xs:override`,
relaxed `xs:all` — where the rules are both newer and less exercised.

**XSD ceilings: 1.0 — 39,351 / 39,404 = 99.87%. 1.1 — 41,515 / 41,570 = 99.87%.**

---

# relaxng — 0 failures

965 of 965 assertions in James Clark's spectest. **No known gaps.**

---

# What is skipped, and why that is not a gap

The XSLT 3.0 suite has 14,601 cases; 8,623 are in scope. The 5,978 skipped are
excluded by *declared dependency*, not by failure:

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

---

# Summary

| Component | Failing | Implementable | Not implementable | Open question |
|---|---:|---:|---:|---:|
| xdm | 0 | — | — | — |
| xpath | 4 | 1 | 3 | 0 |
| xslt 2.0 | 9 | 1 | 6 | 2 |
| xslt 3.0 | 75 | 62 | 9 | 4 |
| xsd 1.0 | 99 | 46 | 53 | 0 |
| xsd 1.1 | 158 | 103 | 55 | 0 |
| relaxng | 0 | — | — | — |
| **Total** | **345** | **213** | **126** | **6** |

Ceilings if every implementable case lands and the open questions resolve our
way:

| Component | Now | Ceiling |
|---|---|---|
| XPath 3.1 | 99.98% | **99.99%** |
| XSLT 2.0 | 99.85% | **99.90%** |
| XSLT 3.0 | 99.13% | **99.90%** |
| XSD 1.0 | 99.75% | **99.87%** |
| XSD 1.1 | 99.62% | **99.87%** |
| RELAX NG | 100.00% | 100.00% |

No suite reaches 100%, and the reason is consistent across all of them: a
residue of cases encodes a suite defect, a W3C-challenged expectation, a
network fetch, a vendor extension, a missing XQuery processor, or a Unicode
snapshot that has since moved. Those are not deferred work — passing them would
mean shipping something less correct.

## Caveat on confidence

The verdicts are not uniformly deep. The package-composition, regex and XPath
cases were root-caused by reading the specification and the engine. The XSD
breakdown is derived from the suite's own `status` field and the kind of each
disagreement, which is solid for the ceiling/ours split but does not identify
*which* rule is missing in each case. The XSLT 3.0 long tail is triage from
error messages: the "implementable" verdicts there are a reasonable reading,
not a diagnosis.
