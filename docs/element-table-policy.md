# The element table's divergences from the Recommendation

`xslt/elementtable.go` decides three of the commonest XSLT static errors —
XTSE0010 for an element this version does not define, XTSE0020 for a value
outside an attribute's permitted set, XTSE0090 for an attribute the element
does not allow. It is a hand transcription, and it does not agree with any
single document. This file says which document it is *meant* to agree with,
enumerates every place it deliberately does not, and gives the rule for
deciding the next one.

`xslt/elementtable_policy_test.go` holds the same enumeration in Go and fails
when the table gains a divergence this document does not list. Adding a
working-draft attribute, widening an enumeration past the Recommendation's, or
clearing an `avt` flag without editing this file is a test failure, not a
silent change.

## The authority

**XSLT 3.0, the W3C Recommendation of 8 June 2017**, vendored at
`testdata/xslt30-test/specs/xslt-30.html`. Its element syntax summaries — the
`<p class="element-syntax">` blocks, one per element — are the table's source
of truth for the set of elements, the set of attributes on each, which are
required, which are attribute value templates, and which have a closed set of
values.

The Last Call working draft, vendored beside it at
`testdata/xslt30-test/specs/xslt-lcwd30.xml`, is **not** an authority. It is
evidence about what a stylesheet in the wild may have been written against,
and nothing more. Where the table admits a draft spelling it is because a
stylesheet that exists writes it, not because the draft says so.

The suite's `schema-for-xslt30.xsd` is a third document and is also not an
authority. It is stale in places — it omits `xsl:accumulator-rule/@select`,
which both the Recommendation and this table carry — so it corroborates and
never overrules.

Where the two disagree and no evidence settles it, the Recommendation wins.

## What is not a divergence

Three conventions make the table look wider or narrower than it is. None of
them is a divergence and none is listed below.

**`boolean` is written `yes|no`.** The Recommendation types many attributes
`boolean`, meaning the six spellings `yes no true false 1 0`. The table lists
only `yes, no` on the attributes XSLT 2.0 also had, and `checkAttrValue`
widens to the other four through `allowsBoolAliases` when the module is 3.0.
Encoding the version gate in the check rather than the enumeration is what
lets one table serve both versions; the enumeration read literally is the 2.0
vocabulary, which is the narrower and correct answer for a 2.0 module.

**The nine standard attributes are absent from every element.** §3.5 lets
`default-collation`, `default-mode`, `default-validation`,
`exclude-result-prefixes`, `expand-text`, `extension-element-prefixes`,
`use-when`, `version` and `xpath-default-namespace` appear on *any* XSLT
element. They live in `standardAttributes` in `staticcheck.go`, not in the
per-element maps, so the summaries' listing of them on `xsl:stylesheet`,
`xsl:transform` and `xsl:package` is not something the table has to repeat.

**Bracketed attributes are real.** The Recommendation writes
`[override]? = boolean` on `xsl:function` and
`[disable-output-escaping]? = boolean` on `xsl:text` and `xsl:value-of`. The
brackets mark an attribute retained for backwards compatibility or belonging
to an optional feature (§4.8 for the latter); they are defined attributes and
the table carries them as such.

## The deliberate divergences

Eleven entries, each with the reason it exists.

### Draft spellings refused — `removed30`

A working draft proposed the name and the Recommendation dropped it. The
summary does not define it, so XTSE0090 is due anyway; listing it is what
makes the refusal survive §3.9 forwards-compatible leniency, which otherwise
ignores every attribute the summary lacks and so cannot tell a withdrawn name
from a future one.

| Element | Attribute | Evidence |
|---|---|---|
| `xsl:for-each-group` | `bind-group` | `for-each-group-002` writes `bind-group="g"` on a `version="3.0"` simplified stylesheet and expects XTSE0090 — not the XPST0008 that an unbound `$g` produces once the attribute is silently dropped. |
| `xsl:for-each-group` | `bind-grouping-key` | The sibling of the above, dropped by the same change. No suite case writes it; it is refused with its partner rather than left to be tolerated alone. |
| `xsl:function` | `identity-sensitive` | A draft spelling at `xslt-lcwd30.xml:14648`; §10.3 no longer carries it, its role having passed to `new-each-time`. |
| `xsl:accumulator` | `applies-to` | §18.2.1's summary is `name, initial-value, as, streamable` and no more. `accumulator-053`'s *description* still mentions the attribute, but no stylesheet in the suite writes it. |
| `xsl:package` | `use-package` | §3.5's `xsl:package` summary has no such attribute; a package is used through the `xsl:use-package` child element. |

### Draft spellings accepted

| Element | Attribute | Reason |
|---|---|---|
| `xsl:stream` (element) | — | §18.1's instruction in the draft; the Recommendation renamed it `xsl:source-document`. The change log says so in as many words (Bug29747): *"The `xsl:stream` instruction has been generalized to handle both streamed and unstreamed processing, and it has accordingly been renamed `xsl:source-document`, and has a `streamable` attribute."* A stylesheet written against either text is a legal one, so both names are accepted and `compileSourceDocument` serves both — it reads attributes, never the local name. The `xsl:stream` entry deliberately has no `@streamable`: that is precisely what the rename added. No suite case writes `<xsl:stream`, so this is invisible to the ratchet in both directions. |
| `xsl:merge-source` | `for-each-stream` | The two documents give the attribute different names. `for-each-source` is the Recommendation's and the suite's — its schema declares it and 56 files write it — but the draft never uses that name: it says `for-each-stream` 33 times (`xslt-lcwd30.xml:19881` types it in the summary, and XTSE3195 at 19904–19910 is written entirely in terms of it), and 6 suite files write the draft's spelling. Accepting only one rejects conforming stylesheets written against the other. `streaminstructions.go` already reads either. |
| `xsl:param` | `export` | Not in §9.2's signature; a spelling from an earlier draft, written by exactly one file in the whole suite. It is accepted and **ignored** rather than listed `removed30` because `iterate-024` writes it on an `xsl:param` while the error the case is actually about is a misplaced `xsl:on-completion` further down. Making it `removed30` reports XTSE0090 before the walk reaches the XTSE0010 the case exists to pin — measured: it cost that case and no other. Its three sibling draft attributes, which no stylesheet writes, are refused. |

### Enumerations wider than the summary

| Element | Attribute | Reason |
|---|---|---|
| `xsl:expose` | `visibility` adds `hidden` | §3.5's `xsl:expose` summary gives four values, but `hidden` is exactly the visibility `xsl:expose` exists to confer: §3.5.2 lets a component be hidden from importers, and `xsl:accept`'s own summary carries `hidden` in the same position. Rejecting it would refuse the element's principal use. This is the one entry where the Recommendation's own text argues against its summary, so the prose wins. |

### Elements not in the summaries

| Element | Reason |
|---|---|
| `xsl:original` | It has no syntax summary because it is not an element in the ordinary sense: §3.5.3 makes `xsl:original` a *symbolic reference*, the name used in `xsl:call-template/@name` or a function call within an `xsl:override` to reach the overridden component. The table carries it with no attributes so that the content-model walk of `xsl:override` does not report XTSE0010 on a legitimate reference. |
| `xsl:stream` | Above, under draft spellings accepted. |

### Required-ness that differs

| Element | Attribute | Reason |
|---|---|---|
| `xsl:sequence` | `select` is `required` plus `optional30` | XSLT 2.0 requires the attribute; 3.0 lets a sequence constructor stand in for it. The pair of flags is how one table says "required at 2.0, optional at 3.0". The Recommendation's summary is the 3.0 half. |
| `xsl:package` | `version` is not `required` | `version` is a standard attribute (§3.5), checked by `standardAttributes` rather than the per-element map, so the flag here has no reader. A 3.0 package routinely declares `version="2.0"`, describing its contents rather than itself; `compileRoot` decides whether a package is allowed at all. Marking it required in the map would be inert, and is left cleared so as not to imply a check that does not run. |

### `avt` flags that disagree with the braces

The Recommendation writes an attribute value template's type in curly
brackets. Three flags are set where the summary writes no braces.

| Element | Attribute | Reason |
|---|---|---|
| `xsl:copy-of` | `validation` | Set, though §11.9 writes the type unbraced. It is harmless: `validate.go` refuses a `"{…}"` in this position with XTSE0020 before the flag is consulted, which `TestElementTableRecommendationDrift`'s `copy-of validation braces` case pins. Clearing it would be a change with no observable behaviour. |
| `xsl:output` | `parameter-document` | Set, though §26.1 writes `uri` unbraced — only `xsl:result-document`'s attributes are braced. The flag has no reader here: the attribute has neither an enumeration nor a `qnameAttrs` entry, so `checkAttrValue` returns before reaching it. Left as written for the same reason as above. |
| `xsl:output` | `json-node-output-method` | Identical to `parameter-document`. The sibling flags on `build-tree` and `allow-duplicate-names`, which *do* have enumerations and where the flag was suppressing XTSE0020 on a `"{…}"`, were cleared; these two were not, because clearing them changes nothing. |

## Divergences with no recorded reason

An undocumented divergence is the finding, so these are listed separately
rather than quietly explained.

| Element | Attribute | What differs |
|---|---|---|
| `xsl:global-context-item` | `streamable`, `use-accumulators` | Neither is in §3.10's summary, which gives `as?` and `use?` and no more. The table's own comment says they are "accepted and not enforced: this processor does not stream, and §19 lets one decline the analysis" — which explains why they are harmless, not why they are *defined*. No suite case writes either, and `TestElementTableRecommendationDrift` records that an `xsl:global-context-item/@use-accumulators` is in fact refused, because `contextitem.go` checks the element against the `context-item` table key, which has neither. So the two entries are unreachable as well as undeclared. They are candidates for removal; nothing yet establishes that removing them is safe, so they stand and are recorded here. |
| `xsl:evaluate` | `schema-aware` has no enumeration | §10.4 types it `{ boolean }`, a closed set once the braces are discounted. The table leaves it open. It is checked instead in `compile_instr.go`, which raises XTDE0030 — the code an AVT's effective value gets — and `evaluate-038` (`schema-aware="TRUE"`) depends on that code rather than XTSE0020. So the omission produces the right error, but nothing in the table says it is deliberate. |
| `xsl:output`, `xsl:result-document` | `standalone` lacks `true/false/1/0` | The type is `boolean \| "omit"`, so the 3.0 spellings belong beside `omit`. The `allowsBoolAliases` widening rescues `xsl:result-document`'s — `checkAttrValue` grants 3.0 aliases to every `xsl:result-document` attribute when the processor is 3.0 — but `xsl:output`'s is widened only when the *module* is 3.0. A `version="2.0"` module under a 3.0 processor writing `<xsl:output standalone="true"/>` is refused where `omit-xml-declaration="true"` beside it is accepted. No case measures this; it reads as an inconsistency rather than a decision. |

## What each annotation field means

| Field | Meaning | When it may be used |
|---|---|---|
| `elementDef.since30` | The element is new in XSLT 3.0. A 2.0 module using it gets XTSE0010, as every conforming 2.0 processor gives. | An element with no XSLT 2.0 summary **and** no 2.0 meaning. Not for a 3.0 element a 2.0 processor could execute exactly — `xsl:evaluate`, `xsl:iterate` and `xsl:mode` are deliberately unflagged, because refusing them makes a different error unreachable (`number-1004`, `system-property-022`, `collations-0128`). |
| `attrDef.since30` | The attribute is new in 3.0 on an element that existed before. Availability follows the **module's** `@version`. | When the attribute changes what the module's grammar contains. `xsl:variable/@static` is the case. |
| `attrDef.processor30` | `since30` for an attribute whose availability follows the **processor** rather than the module. | When the attribute says what the processor may do rather than what the module contains, evidenced by a suite case that writes it in a `version="2.0"` module while being scoped XSLT30+. `message-0009` (`terminate="true"`), `function-1032` (`new-each-time`), `function-1025` (`@static` on `xsl:param`), `format-number-069a` (`exponent-separator`), `result-document-0302` (`build-tree`) are the precedents. Using it without such a case is guessing. |
| `attrDef.optional30` | A 2.0-required attribute that 3.0 made optional, because 3.0 gave the element a second way to say the same thing. | Exactly one entry justifies it today: `xsl:sequence/@select`, which 3.0 lets a sequence constructor replace. |
| `attrDef.eqnameOK` | The type is a **union** of a token enumeration with an EQName, so a lexically valid namespaced name passes beyond the listed tokens. | Only where REC appendix J.1 types the attribute as such a union. `xsl:function/@streamability` (`xsl:streamability-type`) is the only current use. Not a substitute for an incomplete enumeration. |
| `attrDef.removed30` | A name a working draft proposed and the Recommendation removed. Reported XTSE0090 even under forwards-compatible leniency. | Only when the name is genuinely a *withdrawn* draft spelling, and only when no suite case depends on the attribute being tolerated. `param/@export` is the counter-example: it is a withdrawn spelling and is still not flagged, because flagging it costs `iterate-024`. |
| `attrDef.avt` | The summary writes the type in curly brackets, so the value may be `"{…}"` and cannot be checked against the enumeration at compile time. | Follow the braces. The three exceptions above are exceptions, and each is recorded because it has no reader. |

## The rule for a new divergence

1. **Read the Recommendation's summary first.** Strip tags from
   `testdata/xslt30-test/specs/xslt-30.html` and find the
   `<p class="element-syntax">` block. Never fetch w3.org; the vendored copy is
   the one the tests measure against. Check appendix J.1 for the attribute's
   schema type, which resolves a union the summary abbreviates.
2. **If the table already agrees, stop.** Most apparent divergences are one of
   the three conventions under *What is not a divergence*.
3. **If the table must differ, name the evidence before writing the code.**
   Acceptable evidence is a suite case that depends on the divergence (with its
   name), a change-log entry in the Recommendation itself (with the bug
   number), or a passage of the Recommendation's prose that contradicts its own
   summary. "The draft said so" is not evidence; "six suite files write it" is.
4. **Prefer the narrower table.** Accepting a name the Recommendation does not
   define is indistinguishable, from the stylesheet's side, from ignoring it.
   Refusing a name a real stylesheet writes is at least visible. Where a
   divergence has no evidence either way, do not add it.
5. **Measure the cost.** Run the conformance gate and read the in-scope counts.
   A divergence that moves the count down is wrong however good the argument;
   `param/@export` is in the table precisely because the measurement said so.
6. **Record it here in the same commit.** Add the row with its reason, add the
   pair to `elementtable_policy_test.go`, and add a `CHANGELOG.md` entry. The
   test fails until the pair is declared, which is what keeps this document
   from drifting the way the table did.

## Could the table be generated?

`xpath/spec/function-signatures.json` is generated from the F&O
Recommendation by `cmd/genfunctions` and pinned by
`TestMigratedSignaturesMatchManifest`. The element table is not, and the
question is what it would take.

The extraction is the easy half and is already demonstrated: the 78 element
summaries parse cleanly out of `xslt-30.html` with a regex over
`<p class="element-syntax">`, a tag strip, and a line-continuation join —
under forty lines. That yields, per element, the attribute names, which are
required, which are bracketed, which are braced, and the alternation text of
each type. Resolving the type names (`boolean`, `eqname`, `tokens`,
`sequence-type`) against appendix J.1's schema, which is embedded in the same
file, gives closed enumerations for the rest. The content models parse from
the `<!-- Content: … -->` comment in the same block. Nothing here is research;
it is the same shape of job `cmd/genfunctions` already does.

The hard half is the overlay. Every row in the tables above is a decision the
Recommendation cannot supply: the six `removed30` and draft-accepted names
exist because of documents *other* than the Recommendation; `since30` versus
`processor30` is a judgement about which suite cases are scoped XSLT30+ while
writing `version="2.0"`, readable only from the suite catalogue; the
`yes|no` narrowing is a deliberate re-encoding of the version gate that a
generator reading `boolean` would undo; and the three `avt` exceptions record
that a flag has no reader, which is a fact about this codebase. So the
overlay would have to carry, keyed by (element, attribute): a set of
annotations to force, a set of enumeration edits with direction, a set of
extra entries the Recommendation does not define, and a reason string for
each — which is exactly the content of this document, in a form the build can
read. That is the real work: roughly the eleven declared divergences plus the
three undocumented ones, each needing a stable key and a reason.

What it would catch is transcription error — a mistyped enumeration value, an
attribute omitted from an element, a required flag on the wrong attribute, a
brace read as absent. Those are real: `xsl:key/@composite`,
`xsl:function/@visibility` and `@streamability`, and four `xsl:output`
serialisation attributes were all simply missing from the table, and each went
unnoticed for as long as forwards-compatible processing was swallowing the
rejection. A generator finds every one of those on the first run. What it
would *not* catch is any of the divergences in this document, because each is
an intentional entry in the overlay — a generator cannot tell a justified
override from an unjustified one. It also would not catch an overlay entry
that outlived its reason, which is the failure mode this document and its test
address directly, and more cheaply: the test costs a list of pairs and no
build step, and it fails on exactly the event a generator would also fail on —
the table gaining a divergence nobody declared.
