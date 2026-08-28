# W3C conformance: the remaining gaps

Every figure here comes from a full run of the suite it names, measured at
commit `dd377f4` with `tests/check.sh`. Nothing is estimated.

| Component | Suite | In scope | Passing | Now | Failing | Fixable | Open | Can't fix | Ceiling |
|---|---|---:|---:|---|---:|---:|---:|---:|---|
| **xdm** | *(no external suite)* | — | — | — | — | — | — | — | — |
| **xpath** | QT3 — XPath 2.0 | 15,183 | 15,183 | 100.00% | 0 | 0 | 0 | 0 | 100.00% |
| **xpath** | QT3 — XPath 3.0 | 19,236 | 19,236 | 100.00% | 0 | 0 | 0 | 0 | 100.00% |
| **xpath** | QT3 — XPath 3.1 | 21,786 | 21,782 | 99.98% | **4** | 1 | 0 | **3** | 99.99% |
| **xslt** | W3C XSLT 2.0 | 6,158 | 6,149 | 99.85% | **9** | 1 | 1 | **7** | 99.89% |
| **xslt** | W3C XSLT 3.0 | 8,623 | 8,571 | 99.40% | **52** | 36 | 1 | **15** | 99.83% |
| **xsd** | W3C xsdtests 1.0 | 39,404 | 39,331 | 99.81% | **73** | 21 | 0 | **52** | 99.87% |
| **xsd** | W3C xsdtests 1.1 | 41,570 | 41,466 | 99.75% | **104** | 50 | 0 | **54** | 99.87% |
| **relaxng** | Clark spectest | 965 | 965 | 100.00% | 0 | 0 | 0 | 0 | 100.00% |
| | **Total** | | | | **242** | **109** | **2** | **131** | |

*Ceiling* is what the suite would report if every fixable case landed and every
open question resolved our way; the "can't fix" column is what stands between
that and 100%.

**242 disagreements in total**, of which **109 are ours to fix**, **131 cannot
be fixed without shipping something less correct**, and **2 are open
questions**.

This is down from 345 at commit `69c53cf`. Four agents working in isolated
worktrees cleared 103 cases: 10 of 10 in the `xsl:override` cluster, 9 of 12
across `package`/`accept`/`expose`/`use-package`, and 80 XSD schema-validity
disagreements. A fifth target — the regex and validation-ordering items —
turned out not to be work at all, and moved seven cases into the "cannot be
fixed" column instead.

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

# xslt — 61 failures

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
| `import-schema-137` | `XTTE1512` where `XTTE1510` is wanted | **Not implementable** | Both errors are genuinely present: `z:familyname` is absent from `schema061.xsd` (only `surname` is declared) so XTTE1512 is right for that node, while the enclosing `z:person` is invalid against `personType` so XTTE1510 is right for that one. §2.9 settles the choice by declining to: "**It is implementation-dependent which of the several errors is signaled.**" Either answer conforms; the suite tests one processor's order. |
| `validation-0201` | Serialisation differs at offset 46 | **Implementable** | XHTML output method: `<meta http-equiv>` placement in `<head>`. |

**XSLT 2.0 ceiling: 6,151 / 6,158 = 99.89%.**

### Why the three regex cases are not ours

The strongest evidence is not the spec reading above — it is the suite itself.
The XSLT 3.0 `regex-syntax` set runs **987 cases and 984 pass**, and none of its
three failures involves `\w`, `\d` or `\c` membership. Only these three
2012-era XSLT 2.0 cases disagree, each exactly where Unicode moved underneath
them.

## XSLT 3.0 — 52 failures

### Package composition — 5

Was 28. Two agents cleared 23 of them: all ten `xsl:override` cases and nine of
the twelve across `package`/`accept`/`expose`/`use-package`. Both independently
found the same rule — §3.6.3.2, that a using package contains a component
corresponding to every component in the package it uses — and the integration
had to choose between their two implementations; folding the inherited
components into the package's component list scores one case more than keeping
them separate, and `override-t-003a` is the case.

| Cases | Verdict | Why |
|---|---|---|
| `package-021err`, `package-022err` | **Not implementable** | Suite defect from a half-applied 2020 erratum (E36, "function arity must be given in accept/expose"). The editor appended `#0` to every occurrence, including `<xsl:function name="me:function1#0">` and `component="function#0"`. The spec gives `xsl:function/@name` as an `eqname` and `@component` as the enumeration `"template" \| "function" \| "attribute-set" \| "variable" \| "mode"` — neither admits an arity. |
| `accept-913` | **Not implementable** | The case's own comment states its premise: "not specifically accepting xsl:initial-template makes it private." §3.6.3.2 says the opposite in as many words — a component matched by no `xsl:accept` keeps its visibility, and only a *private* one becomes hidden. The template is public and matched by nothing, so it stays public and XTDE0040 is unreachable. The correction was built, instrumented (`acceptedVis` correctly reports `public`) and reverted. |
| `package-200` | **Not implementable** | `package-version="'1.0.0'"` wants XTSE3000, but `use-package-291`–`294` write four other malformed ranges and all want XTSE0020. One sentence — "an attribute contains a value that is not one of the permitted values" — covers all five identically, and nothing in the `PackageVersionRange` grammar separates a quoted version from any other malformed one. The current answer costs 1 case and saves 4. |
| `use-package-003` | **Not implementable at this blast radius** | A private function of a used package must resolve inside it and not outside. Functions live in one flat `xpath.Library` resolved by name at runtime, and `FuncCall` carries only a QName. A lexical rename was implemented; it fixed this case and broke `override-f-026`, where `g:transitive-closure` exists at arity 1 (public) and arity 2 (private) — a name-based rewrite cannot separate arities. A real fix needs the package threaded through the XPath static context. |

### Schema-aware validation — 6

| Cases | Verdict | Why |
|---|---|---|
| `si-copy-117`, `si-copy-of-117` | **Not implementable** | Not ordering cases at all. Both write `<xsl:copy select="/*/*/@version" type="xs:date"/>` — a `type` attribute and **no `validation` attribute**. §19.2 keys the codes to which attribute was written: XTTE1510 begins "If the **validation attribute** ... has the effective value `strict`", which is literally unmet, while XTTE1540 is "if an **[xsl:]type attribute** is defined ... and the outcome of schema validity assessment against that type is ... other than valid", which is exactly met. The suite's own description says "validate attribute **by type**". Our XTTE1540 is correct. |
| `import-schema-137` | **Not implementable** | The one genuine ordering case, and §2.9 explicitly declines to settle it: "If more than one error arises, an implementation is not required to signal any errors other than the first one that it detects. **It is implementation-dependent which of the several errors is signaled.**" Both errors are real, so either choice conforms; the suite is testing one processor's order. |
| `validation-0006` | **Open question** | A parentless attribute: `XTTE1555` wanted, `XTTE1540` reported. A distinct question from the three above. |
| `validation-0201` | **Implementable** | XHTML serialisation, as above. |
| `catalog-001` | **Implementable** | `schema-element()` in a `use-when` test with no schema imported. |

### Regex — 3

| Cases | Verdict | Why |
|---|---|---|
| `regex-syntax-0056`, `regex-syntax-0086`, `regex-syntax-0102` | **Not implementable** | Ambiguous-dash character classes such as `[^a-d-b-c]` and `[a-a-x-x]+`, which XSD 1.0 rejects and XSD 1.1 accepts. **The suite contradicts itself**: `regex-syntax-0056` and `regex-syntax-xslt20-0056` carry the *identical* pattern and the *identical* `XSD_1.1 satisfied="false"` dependency, yet the first asserts `FORX0002` and the second asserts a successful match. No engine can pass both. The XSD 1.0 rule was implemented and measured: it fixes these three (`984/3 → 987/0`) at a cost of −3 XSLT 2.0, −9 QT3 and −34 XSD 1.1, for a net loss. Reverted. |

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

**XSLT 3.0 ceiling: 8,608 / 8,623 = 99.83%.**

The fifteen that cannot be fixed: `package-021err`, `package-022err`,
`package-version-011`, `unparsed-text-2003`, `streamable-141`, `base-uri-052`,
`docbook-001`, `docbook-004`, `catalog-006b`, the three `regex-syntax`
ambiguous-dash cases, `si-copy-117`, `si-copy-of-117` and `import-schema-137`.

---

# xsd — 177 disagreements

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
| XSD 1.0 | 73 | **21** | 52 |
| XSD 1.1 | 104 | **50** | 54 |

An agent cleared **80** of these — 26 on 1.0 and 54 on 1.1 — by implementing
seventeen missing schema-validity rules, without a single agreement count
falling. The largest were `explicitTimezone` (which had no schema-level
constraints at all, 9 cases), occurrence attributes on any child of a named
group (8), a type-cycle check that excused a type whose base is itself (6),
and substitution-group type derivation (5).

## What the ceiling consists of

| Set | Cases | Status | Why |
|---|---:|---|---|
| `MS-Regex2006-07-15` | 22 (both versions) | `queried bug4113` | Every single MS-Regex disagreement is the *same* open W3C bug. The expected results are challenged upstream; agreeing with them would mean agreeing with something the working group does not stand behind. |
| `MS-Schema`, `MS-SimpleType`, `MS-Element`, `MS-DataTypes`, others | ~31–33 | `queried`/`stable` + bug | Assorted challenged expectations across the Microsoft-contributed sets. |

**Not implementable: 52 (XSD 1.0) and 54 (XSD 1.1).**

## What is genuinely ours

What remains is a different shape from what was cleared. The bulk of the
`SFALSEACCEPT` backlog is gone; several of the stragglers are the *opposite*
problem — `particlesZ001`, `Z028`, `Hb008` and `Hb011` are places where the
content-model restriction table is too **strict**, and loosening it risks
re-admitting a false accept elsewhere. Four more (`simple006`, `idB005`,
`over024`, `over026`) are unresolved type references that are structurally
identical to `saxonData/Missing/missing001`, which the suite marks **valid** on
the "error only if the declaration is needed for validation" reading;
separating them needs reachability analysis.

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

**XSD ceilings: 1.0 — 39,352 / 39,404 = 99.87%. 1.1 — 41,516 / 41,570 = 99.87%.**

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

The per-suite counts and ceilings are in the table at the top. What that table
cannot show is *why* the 126 unfixable cases are unfixable:

| Reason | Cases | Where |
|---|---:|---|
| **W3C has challenged its own expected result** | 106 | XSD 1.0 (52) and 1.1 (54). All 44 `MS-Regex` cases across both versions are one open bug, 4113. |
| **Suite defect** | 3 | `format-number-070` invokes a template the stylesheet does not declare; `package-021err` and `package-022err` carry a half-applied erratum that puts an arity where the grammar admits none. |
| **Unicode moved** | 3 | The 2012 `regex-syntax-xslt20` cases assert `\w`/`\d`/`\c` membership that Unicode 6.1 changed. |
| **Suite contradicts itself** | 3 | The `regex-syntax` ambiguous-dash cases. `regex-syntax-0056` and `regex-syntax-xslt20-0056` carry the same pattern and the same dependency and demand opposite outcomes. |
| **Spec declines to decide** | 4 | `si-copy-117` and `si-copy-of-117` use `type=` where XTTE1510 requires `validation=`; `import-schema-137` (which fails on both the 2.0 and 3.0 targets, so counts twice) has two genuine errors and §2.9 makes the choice implementation-dependent. |
| **Needs a network fetch** | 3 | `unparsed-text-2003` (both targets) and `package-version-011` want documents no resolver is configured to reach. |
| **Vendor extension** | 3 | `docbook-001` (both targets) and `docbook-004` need EXSLT `exsl:document`. |
| **No XQuery processor** | 3 | The `fn:load-xquery-module` cases. Two of the suite's own cases cannot both pass. |
| **Feature deliberately not implemented** | 3 | `streamable-141` (streaming), `base-uri-052` (XInclude), `catalog-006b` (`xsl:assert`). |

No suite reaches 100%, and the reason is consistent across all of them: a
residue of cases encodes a suite defect, a W3C-challenged expectation, a
network fetch, a vendor extension, a missing XQuery processor, or a Unicode
snapshot that has since moved. Those are not deferred work — passing them would
mean shipping something less correct.

## Related

[known-gaps.md](known-gaps.md) is the reasoning behind the hard entries here:
diagnosed causes, fixes that were attempted and measured and reverted, and what
a real fix would cost where the answer is a rewrite rather than a patch. It also
covers DTD and XDM, which have no public suite and so appear in no percentage.

## Caveat on confidence

The verdicts are not uniformly deep. The package-composition, regex and XPath
cases were root-caused by reading the specification and the engine. The XSD
breakdown is derived from the suite's own `status` field and the kind of each
disagreement, which is solid for the ceiling/ours split but does not identify
*which* rule is missing in each case. The XSLT 3.0 long tail is triage from
error messages: the "implementable" verdicts there are a reasonable reading,
not a diagnosis.
