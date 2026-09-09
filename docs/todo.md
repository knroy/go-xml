# TODO

Everything here is measured, not guessed. Each item says what it costs and what
it buys, because several of them buy less than they look like they do.

Current position:

| | schema-validity | instance |
|---|---|---|
| XSD 1.0 | 14,385 / 14,388 (99.98%) | 24,973 / 25,000 (99.89%) |
| XSD 1.1 | 15,349 / 15,354 (99.97%) | 26,196 / 26,222 (99.90%) |
| XPath 2.0 | 100.00% — 15,217 of 15,217 in scope |
| XPath 3.0 | 100.00% — 19,302 of 19,302 in scope |
| XPath 3.1 | 100.00% — 21,838 of 21,838 in scope (0 failing) |
| XQuery 3.1 | 99.96% — 29,918 of 29,930 in scope (12 failing) |
| XSLT 2.0 | 99.87% — 6,193 of 6,201 in scope (8 failing) |
| XSLT 3.0 | 98.46% — 11,348 of 11,525 in scope (177 failing); 150 of those 177 need the §19.8 streamability analysis |
| RELAX NG | 100.00% — 965 of 965 |
| Schemas wrongly refused | 7 — 6 on XSD 1.0, 1 on 1.1 |
| Tests | 1,668 `func Test` declarations, clean under `-race` |

Every one of those failures, and why it is still open, is catalogued in
[known-gaps.md](known-gaps.md). This file is the forward-looking half — what
to build next and what it would cost.

---

## 1. Features

### 1.1 XML 1.1 documents — **implemented; one layer outstanding**

XML 1.1 documents are read as XML 1.1. The declaration's version is kept in
`internal/xmlfork.Decoder`, [2] `Char` and [2a] `RestrictedChar` are enforced
per version, and §2.11 makes `NEL` and U+2028 line ends. External entities are
checked against XML §4.3.4: a 1.0 document may not include a 1.1 entity, and an
unrecognised version is refused rather than assumed compatible. See CHANGELOG.md
for the mechanism and why the version is not a caller-settable option.

Two items the old entry listed as blocking turned out not to be. XML 1.0 Fifth
Edition adopted 1.1's name productions verbatim, so `NameStartChar` and
`NameChar` are identical between the versions and the tables already in the
tokeniser are the 1.1 tables. The same holds for `\i` and `\c` in the XSD regex
translator, which had been budgeted as the widest-reaching item: `classdiff.go`'s
ranges were compared against `internal/xmlname` across the whole Unicode scalar
range and disagree nowhere, so the translator is version-independent and correct
for both.

`fn:codepoints-to-string` follows the same version. `xpath.isXMLChar` used the
XML 1.0 `[2] Char` production, whose first range starts at `#x20`, so building a
backspace or a form feed from its codepoint was `FOCH0001` on an engine that had
already chosen 1.1 everywhere else -- and `tests/xslts/deps.go` claims the
`XML_1.1` feature to the XSLT harness on our behalf. It now starts at `#x1`, as
1.1 does; `#x0` and the surrogates stay out at either version. XSLT 3.0 4.1
makes the choice ours in as many words: "Implementations may support any
version ... it is thus implementation-defined which versions and editions of XML
and XML Namespaces are supported." Saxon 9.8 reads it the same way and passes
`xml-to-json-D015`, `-D017` and `-D018`, which this now does too.

The version still decides whether such a character can be *written down*, and
that decision stays in the serializer: XDM makes no distinction between a 1.0
and a 1.1 tree (4.1), so a C0 control is an ordinary string value and only
becomes `SERE0006` when an XML 1.0 serialization is asked to spell it.

The QT3 harness records the split rather than picking a side. Both ends of the
`xml-version` dependency are out of scope, because the suite pairs them --
`K-CodepointToStringFunc-8` carries the 1.0 dependency and says why in its
description, "Codepoint 8 is invalid in XML 1.0 but valid in XML 1.1" -- and
this engine has 1.1's character model but not the rest of 1.1. Admitting the
1.1 half was measured and rejected: it takes XQuery from 29,952 passed / 12
failed to 29,936 / 26.

**What is still missing beyond `dtd` is `fn:serialize`'s `undeclare-prefixes`.**
`xslt/serialize.go` implements it (11.7); `xpath/fn_serialize.go` writes
`xmlns:p=""` unconditionally, so the 1.1-only `serialize-xml-035/036/135/136`
cases would fail if they were admitted. The `version` parameter itself now
reaches the XML declaration there, which it did not before.

The `keySep = "\x1f"` dependency is also closed. `xsd/identity.go` is now
length-prefixed and injective for any field content, so it no longer rests on
U+001F being unreachable in XML 1.0 character data.
`TestIdentityKeySeparatorUnreachableInXML10` still pins the 1.0 premise; when it
fails it is a signal to retire the test, not a key match to fix.

**What is left is the `dtd` package, which has no version notion.** `dtd.Load`
takes a DOCTYPE directive string rather than a document, so it never sees an XML
declaration and has nothing to check §4.3.4 against — its own `stripTextDecl`
discards the text declaration for the same reason `xdm`'s once did. Closing it
means giving `dtd` a way to be told the including document's version, which is a
new API surface rather than a gap closure, and it should not be invented before
a caller needs it.

Scoping the remainder properly still wants the W3C `xmlconf` suite, which this
repository does not vendor. The `XmlVersions` cases in the XSD suite are the
only 1.1 measurement currently available — xv003, xv006, xv008 and xv009 pass.

### 1.2 DTD validation — notations and entity-typed attributes outstanding

The `dtd` package validates against `<!ELEMENT>`, `<!ATTLIST>` and
`<!ENTITY>`: content models, attribute defaults, enumerations, `ID`/`IDREF`.

**The external subset now loads.** `dtd.Load` reads the half of a DTD that
`<!DOCTYPE r SYSTEM "r.dtd">` names, and validates against both halves
together. Parameter entities span the two subsets with XML 1.0 §2.8's
precedence — the internal subset is read first and its declarations bind,
later ones being ignored rather than an error — and a `%pe;` in the external
subset may expand to whole declarations, which is what makes a modular DTD
work. Conditional sections, `<![INCLUDE[` and `<![IGNORE[` (§3.4), are
resolved after expansion, since the keyword is normally itself a parameter
entity, and they nest.

Nothing is fetched without a caller-supplied `LoadOptions.Resolver`, and with
none a DOCTYPE naming an external subset is **refused** rather than validated
against half a DTD — `LoadOptions.InternalSubsetOnly` is how a caller asks for
the partial reading deliberately. Expansion across both subsets is charged to
one shared budget, so a bomb split between them meets the same limit a wholly
internal one does. See [security.md](security.md) and
[options.md](options.md).

**What is left**, and none of it is large:

* **`NOTATION` is parsed but not enforced.** An `<!ATTLIST … NOTATION (a|b)>`
  restricts the value to the listed names, which is checked, but nothing
  verifies that each name was declared by a `<!NOTATION>`. XML 1.0 §3.3.1
  makes the missing declaration a validity error.
* **`ENTITY` and `ENTITIES` attribute types are not checked** against the
  unparsed entities actually declared, for the same reason: the notion of a
  declared-but-unparsed entity lives in `xdm`, and the check would have to
  read it back out.
* **The W3C `xmlconf` suite is not vendored**, so there is no external
  measurement of any of this — `testdata/` holds the QT3, XSD, XSLT 3.0,
  RELAX NG, DocBook xslTNG and XSpec corpora and no XML conformance suite.
  Vendoring it is the one thing that would turn "the tests we wrote pass" into
  a number.

### 1.3 RELAX NG — compact syntax implemented, unverified against a suite

100% of James Clark's suite (965 of 965 assertions). Both notations are
supported: `CompileCompact` and `ParseCompact` read the compact syntax.

The compact parser translates to the XML syntax and compiles that, so it added
a parser and nothing else — the section 7 restrictions, the datatype library
and the validator are reached through the same tree, and a test asserts that
the two notations produce structurally identical trees for the same schema.

**What is implemented:** grammars, `start`, `define` and the `|=` and `&=`
combine operators; `element`, `attribute`, `text`, `empty`, `notAllowed`,
`list`, `mixed`, `grammar`, `parent` and `external`; the `|`, `,` and `&`
infix operators with their non-associativity enforced, and the `?`, `*` and
`+` postfix ones; name classes including `*`, `prefix:*` and `-` except;
`namespace`, `default namespace` and `datatypes` declarations; `include` with
`inherit` and overrides; `div`; annotations in both the `[ ... ]` and the
`>> name [ ... ]` forms, and `##` documentation comments; `~` literal
concatenation, triple-quoted literals and the `\x{}` escape; datatype
parameters; `#` comments and the `\` identifier escape.

**What is left:**

* **No conformance suite.** James Clark's spectest is XML syntax only and
  carries no `.rnc` cases, and the compact syntax specification is not
  vendored here, so the grammar was implemented from the OASIS specification
  as understood rather than checked against a vendored text. The evidence that
  it is complete is the nine real-world `.rnc` files in `testdata` — about
  760KB, the largest being the DocBook 5.1 schema at 356KB — all of which
  parse, plus the round-trip property against the XML syntax. That is
  circumstantial where a suite would be decisive.
* **The built-in datatype keywords `string` and `token` are translated as
  `<ref>`.** `svrl.rnc` writes `attribute xml:* { string }`, and the compact
  parser emits `<ref name="string"/>` rather than a `<data>`, so compilation
  fails with `<ref> names "string", which no <define> provides`. Section 7.3
  used to refuse that schema first, which is why this had not been seen.
* **Annotations are parsed and discarded.** A `[ ... ]` or `>> name [ ... ]`
  annotation is checked for well-formedness and then dropped rather than
  carried onto the tree as a foreign element. Nothing downstream reads them —
  the compiler skips any element outside the RELAX NG namespace — so this
  costs nothing in validation, but a caller using `ParseCompact` to convert
  `.rnc` to `.rng` loses them. A `##` documentation comment *is* carried,
  as `a:documentation`.
* **Section 7.3 refused most real schemas — settled, and it was our rule that
  was wrong.** Seven of the nine `.rnc` files in `testdata` used to compile
  only if section 7.3 was excused. Two independent defects, both fixed:

  Section 7 opens by saying it applies to the **simplified** grammar, and
  section 4.20 rewrites `<zeroOrMore>p` as `choice(oneOrMore p, empty)`. The
  restriction pass runs *before* compilation, on the tree as written, where
  that rewrite has not happened — so it looked for a literal `<oneOrMore>`
  ancestor and refused the `<zeroOrMore>` that DocBook 5.1, XSpec and the SVRL
  schema all write. Applying a simplified-grammar rule to an unsimplified tree
  was the whole of it; the check now accepts either spelling.

  Section 4.19 expands every `<ref>` in place and discards the `<define>`, so
  a definition has no standing of its own in the simplified grammar. An
  `<attribute>` written directly in a `<define>` body was reached with nothing
  above it, which is "nothing encloses it *yet*", not "no repetition encloses
  it" — and judging the clause there refused every schema that factors an open
  name class into a definition. The standalone walk now defers it; the
  compiled-pattern check in `checkCompetition` judges those, on the simplified
  pattern, where each use site has been expanded.

  The RELAX NG specification is **not vendored** here — only `spectest.xml`
  is — so this reading is argued from section numbers quoted in the existing
  code and from the behaviour of the seven schemas, not from a vendored text.
  Jing and libxml2 were not consulted; that DocBook 5.1's own schema is not
  widely reported as invalid RELAX NG is corroboration, not proof. The suite
  does not arbitrate it either way: it passes at 965 of 965 both before and
  after, and contains only nine `zeroOrMore` occurrences in total.

* **`<ref>` expansion is not shared, and costs multiplicatively.** Fixing
  section 7.3 let DocBook 5.1 reach the compiler for the first time, which is
  how this surfaced: compilation had **not finished after 140 s**. It is not
  non-termination. `compileRefNamed` re-compiles a definition's whole body
  once per `<ref>` that names it and caches nothing, so a grammar whose
  definitions form a chain — each referring to several others, as a large
  modular schema does — costs a number of expansions that grows
  multiplicatively along the chain. Measured: XSpec, with 70 definitions,
  expands **14,140 times**, a factor of 200; DocBook has roughly 1,500.

  `maxRefExpansions` (200,000) now bounds it, in the spirit of
  `MaxPatternSize`, which bounds the same shape of blowup during validation.
  DocBook stops in about 0.2 s with a message that says what happened instead
  of hanging. **This is containment, not a fix.** The real fix is to share the
  compiled pattern between `<ref>`s naming the same definition, which the
  recursion guard makes delicate: the result depends on `inheritedNs`, and
  `elementDepth` participates in the section 4.19 self-reference check, so a
  naive cache can both reuse a pattern compiled under a different inherited
  namespace and mask a legitimate error. Caching on *exit* was tried and does
  not help — the blowup is in the first traversal, which a completed-entry
  cache never gets to serve. Until that lands, DocBook-scale schemas are
  refused rather than compiled.

* **Section 7.3's *first* clause may be over-strict for two `<zeroOrMore>`s.**
  `xspec.rnc` sequences `xml-ns-attributes` with `common-attributes`, which
  itself begins with `xml-ns-attributes`, so `attribute xml:* { text }*`
  appears twice in one group and is refused as "required twice". Two
  `<zeroOrMore>`s can each match nothing, so whether "required" is the right
  reading of that shape is a real question — but it is a different clause from
  the one settled above, and settling it needs the spec text this repository
  does not vendor.

### 1.4 Schema Component Constraints — the remaining bulk

**Largely done, and the biggest single change to the numbers so far.** Particle
Valid (Restriction) is implemented, along with the Part 2 facet constraints, a
structural check of each schema document against the schema for schemas, the
XSD regular-expression grammar, and occurrence bounds without an upper limit.
Together they took schema-validity agreement from 85.19% to 94.99% on 1.0.

The earlier entry here argued these were not worth implementing because "these
schemas are marked invalid-by-design and skipped either way". That was true of
the test driver, not of the suite: skipping them was a measurement bug, and
they are roughly 14,000 real tests. See the correction in [xsd.md](xsd.md).

What is left: **two rules, and they have now landed.** The entry that stood
here said "nothing measurable" and that none of the remaining disagreements was
a fixable defect. Both halves were wrong, and *known-gaps.md* said so at the
same time — its table marked `elemM002` and `idC019` "open" while this file
called the area finished. Two entries drifted apart because the figures were
re-measured and the prose was not.

Both are now closed, and both were false *accepts* — invalid schemas this
loaded without complaint, which is the direction that matters:

- **`MS-Element/elemM002`** — `type="foo"` naming an `<xsd:attribute
  name="foo"/>`. §3.3.2 requires `type=` to resolve to a type definition, and
  this resolves to a component of the wrong kind. What hid it was the deferral
  §3.3.3 grants an element declaration: the unprefixed name lands in the absent
  namespace, `deferrableMiss` answers true, and the reference was carried on the
  declaration instead of reported. The deferral exists because a document read
  later might supply the type — but no document can turn an attribute
  declaration into one, so here there is nothing to wait for.
- **`MS-IdentityConstraint/idC019`** — a `keyref` whose `refer=` reaches a key
  its own document never imported. §4.2.6.1 scopes an import's licence to the
  document that wrote it, which is why `doc.imports` exists beside the
  per-assembly set; the `refer=` fixup was reading the flat assembly-wide map
  and ignoring who asked. `resolveQName`'s own import check could not catch it,
  because an unprefixed name with no default namespace in scope returns early
  through `chameleonQName`.

Measured on an isolated worktree, each version gains exactly these two and
nothing else moves: 1.0 schema agreement 14,383 → **14,385** (disagreements 5 →
3), 1.1 15,347 → **15,349** (7 → 5), with both instance lanes unchanged case for
case and the vendored-schema corpus steady at 185 loaded. What remains after
them is in [conformance-gaps.md](conformance-gaps.md), and is dominated by the
22-per-version `MS-Regex` cases the W3C has itself challenged.

---

### 1.5 XQuery schema import — the last structural gap in `xquery`

`import module` is **implemented** (§4.12) — see
[CHANGELOG.md](../CHANGELOG.md). What remains here is `import schema`, which
still parses and is then refused with `XQST0059`, leaving the in-scope schema
definitions empty so that `validate { … }` raises `XQDY0084`. It is not
mis-parsed; it is refused by name.

**What `import module` cost, for calibration.** A module store
(`Options.Modules`), a resolver (`Options.ModuleResolver`, nil by default like
every other one here), two bounds that refuse rather than truncate, and a
loader that publishes a module before following its imports — because a cycle
of module imports is *not* an error at XQuery 3.0 and later, which the suite
settles by carrying the identical module pair under two spec dependencies with
opposite expected results.

**What `import schema` costs, and why it is a bigger job than it looks.** Not
a resolver — `xsd` already has one, and the module loader's shape transfers.
The cost is that the imported components have to reach the *static context*:
§2.1.1's in-scope schema definitions are what `validate`, `instance of` and a
`SchemaElementTest` are judged against, and this package's `staticContext`
has nowhere to put them. That is PSVI plumbing between `xsd` and `xquery`
rather than another loader, and it is the reason the two halves of `import`
were separated rather than done together.

**What it buys.** The `validate` expression, the schema-aware type tests, and
the QT3 cases that depend on a typed input. It does **not** unblock
`fn:load-xquery-module`; that needed the module half, which now exists — see
[reaching-100.md](reaching-100.md) for what actually changed there.

### 1.6 A host API for JSON, and a context item that is not a node

Two separate gaps that a caller hits together, because both stand between a
JSON string and an evaluation that can see it.

**There is no `xdm.ParseJSON`.** All four XPath 3.1 JSON functions are
implemented — `fn:parse-json`, `fn:json-doc`, `fn:json-to-xml`,
`fn:xml-to-json` — but `parseJSONText` in `xpath/fn_json.go` is unexported, so
a host holding a JSON string has to reach the map by compiling and evaluating
an expression:

```go
e, _ := xpath.ParseVersion(`parse-json('{"n":42}')`, nil, xpath.XPath31)
ctx := xpath.NewContext(nil, xpath.Builtins())
ctx.Version = xpath.XPath31   // and this line, or XPST0017
seq, _ := e.Eval(ctx)         // seq[0] is an *xdm.MapItem
```

Two things make that worse than it looks. The version is set in **two** places
and both are required: `xpath.Parse` compiles 2.0, so the expression needs
`ParseVersion(…, XPath31)`, and `parse-json` is registered with
`registerFnSince(XPath31, …)`, which reads `ctx.Version` rather than the
parser's. Setting one and not the other gives `XPST0017: unknown function`,
which reads like the function is missing rather than like a version is unset.
The second is that the JSON text has to be *spliced into an expression*, so a
caller with a string in hand must escape it into a string literal.

**What it buys.** `xdm.ParseString` is the XML answer to the same question,
and the asymmetry is the whole complaint: a host can parse XML in one call and
must write an XPath expression to parse JSON. It also removes the two-version
trap from the common path.

**What it costs.** Very little — the parser exists and is exercised by the
suites. The shape is settled by prior art: Saxon added
`Processor.newJsonBuilder().parseJson(String)` in Saxon 11 for exactly this
reason, and SaxonC puts `parse_json` directly on the processor, which is the
closer match to a package-level `xdm.ParseJSON(text string) (xdm.Item, error)`.
Saxon exposes exactly one option, `liberal`, and takes `fn:parse-json`'s
defaults for everything else including `duplicates="use-first"`; there is no
reason to offer more until someone asks.

One note for whoever writes it: JSON numbers become `xs:double`. That looks
like a violation of the rule recorded in `cc17983` — an exact value must never
be routed through float64 — and is not. F&O fixes the mapping, so the double
is the specified answer rather than a shortcut, and the code should say so or
it will be "fixed" later.

**Prior art is thinner than it first appears.** Saxon is the *only* processor
with this API. Five others implement real XDM 3.1 maps and arrays — Altova
RaptorXML, XmlPrime, FontoXPath, elementpath, BaseX — and every one of them
exposes a map only as something read *out of* an evaluation, with no
constructor that ingests JSON. BaseX's `JsonW3Converter` is Java-public but
undocumented; eXist-db's parser lives inside the function implementation. The
structural reason is that the only standard host API, XQJ (JSR 225), froze at
XQuery 1.0 in 2009, before maps, arrays and JSON existed in the data model,
and no successor was written. So this is not a convention to conform to. It is
one implementation's good idea, and the ergonomics are ours to choose.

**The XSLT context item is narrower than the specification's.**
`Stylesheet.Transform` takes a `*xdm.Node`, so a map cannot be the principal
source of a transformation even though the language allows it. XSLT 3.0 gives
`xsl:global-context-item` the default `as="item()"`, and the error that
polices non-node items is narrow rather than general: XTTE0510 fires only for
`xsl:apply-templates` with no `select`, which would be pointless if a non-node
context item were forbidden outright. The specification goes further and names
this signature as the older one — a single node "is likely to be found for
compatibility reasons in a transformation API designed to work with earlier
versions of this specification". Saxon's `Xslt30Transformer` takes
`setGlobalContextItem(XdmItem)`.

Nothing internal blocks it: `checkGlobalContextItem` already takes an
`xdm.Item`, and the narrowing happens only at the public signature. The cost
is an API decision rather than an implementation one — `Transform`'s signature
is covered by the 1.x stability promise, so widening it means a sibling entry
point rather than a change in place. Until then the workarounds are real but
indirect: pass the map through `TransformOptions.Params` to a top-level
`xsl:param`, or call `parse-json()` inside the stylesheet and bind it to an
`xsl:variable`, passing only the JSON string in.

## 2. Bugs

None open. Every entry this section used to carry is fixed or was retracted as
not a defect; the reasoning that is still worth keeping lives in
[known-gaps.md](known-gaps.md) and the CHANGELOG, not here. A todo list that
records its own successes stops being a todo list.

---

## 3. Verification gaps

These are places where the tests are thinner than the claims.

### 3.1 Fuzzing beyond the parser

**Largely done.** There are now five targets, not one. Alongside
`FuzzCompileNoPanic` over the XPath expression compiler:

- `FuzzParseNoPanic` (`xdm`) over `ParseString`, the front door for every
  untrusted document the engine reads;
- `FuzzLoadSchemaNoPanic` (`xsd`) over `Load`, at both XSD versions, which
  reaches the content-model compiler through every complexType it accepts and
  then compiles each one to an automaton;
- `FuzzSerializeRoundTrip` (`xslt`), which asserts that parse → serialise →
  parse yields the same document compared semantically rather than
  byte-for-byte;
- `FuzzCompileStylesheetNoPanic` (`xslt`) over the stylesheet compiler.

Each was run for 150 seconds and found nothing: roughly 20 million executions
against the parser alone with no crash. See [testing.md](testing.md#fuzzing)
for how to run one. That the parser survives that is a real result — it is the
component with the largest untrusted surface — but "found nothing in 150
seconds" is a floor, not a ceiling, and the targets are worth running for hours
rather than minutes when there is a machine to spare.

**The differential technique is now a standing test, for occurrence bounds.**
The fuzz targets above assert that nothing crashes; none of them asserts that
the answer is *right*. Generating a content model, generating documents, and
comparing against an independent reference is what turned up the content-model
bug in [known-gaps.md](known-gaps.md), and it was a one-off in a scratch
directory. It is now `xsd/occurs_oracle_test.go`: 8,397 documents over six
shapes — a repeating sequence over one element, an emptiable inner particle, two
children per iteration checked as *name sequences* rather than counts, a
repeating two-branch choice, three levels of nesting, and `maxOccurs="0"` — each
compared against a count derived from interval arithmetic over the bounds, never
from the engine. It runs in 0.4s as part of `go test ./...`, and
`GOXSLT_OCCURS_WIDE=1` widens every sweep to about 2s. Against the code before
either occurrence fix it reports 1,474 wrong answers, 165 of them false accepts.

**What remains** is that this covers occurrence arithmetic only. The oracle is
stateable because the language of these shapes is a set of integers; it says
nothing about wildcard weighting, substitution-group closure, type derivation,
or a choice whose branches interleave — for those, an independent oracle would
have to reimplement the matcher, and one that reasons the same way would inherit
the same mistakes. Those regions still rest on the suites and on hand-written
cases.

The earlier round, before these targets existed, found three defects the suites
cannot see:

- a nil dereference on a named function reference with no function library,
  where the equivalent call correctly raised `XPST0017`;
- two unbounded recursions — sequence types and XSD pattern facets — each of
  which killed the *process*, since a stack overflow is fatal in Go and
  `recover()` cannot catch it;
- and, by differential fuzzing against a brute-force reference, a
  content-model bug that decided a whole class of schemas wrongly in both
  directions ([known-gaps.md](known-gaps.md)).

All four are fixed. The last needed the matcher's counter runtime replaced —
a set of whole count vectors rather than a bracketed reading per scope, so that
every occurrence bound is answered from one execution. Both suites came through
unmoved, case for case. The differential technique — generate a model, generate
documents, compare against an independent oracle — is what found the one that
mattered most, and it is the only method that reached a bug 80,879 suite
agreements could not, which is why it is now committed rather than rerun by
hand. It earned its keep twice: a second sweep over the same family found a surviving
region — an emptiable inner particle, whose outer scope has to be credited for
an iteration that consumed nothing — that the first fix had left rejecting valid
documents, and again no suite case moved.

### 3.2 Deep-nesting and pathological schemas

Limits exist for documents (`MaxDocuments`, `MaxErrors`, depth), and a content
model with deeply nested counters now has one too: `DefaultMaxMatchStates`
bounds the readings the matcher will carry at once, and exceeding it is an error
naming the limit rather than an unbounded allocation. Still less certain: a
substitution group closure over thousands of declarations, a union of unions.
Worth a benchmark that fails loudly rather than an assumption.

### 3.3 Production corpora as a fixture

UBL, CII, Peppol and Factur-X found more bugs per hour than any other method,
but they live in a scratch directory and are re-fetched by hand. They should
be a documented, opt-in fixture like `GOXSLT_QT3` — otherwise the highest-yield
test is the one most likely to stop being run.

**This is now the main safeguard against over-strict schema checks, which
raises its priority.** Every Schema Component Constraint added is a chance to
reject a schema real systems depend on, and the conformance suite cannot catch
that: it scores agreement with the W3C's labels, so a rule that is merely *too
strict* shows up only if the suite happens to contain a valid schema
exercising it. Re-loading the corpora does catch it. As of the constraint work
above, 65 UBL 2.1 and 427 CII/EN16931 schemas load clean.

Note the corpora need `ParseOptions{AllowDOCTYPE: true}`: UBL's
`UBL-xmldsig-core-schema-2.1.xsd` carries a DOCTYPE, and without the flag all
65 fail with a cascade of unresolved `ds:` element references from the one
refused include.

---

## 4. Deliberate non-goals

Recorded so they are not proposed again as oversights:

* **Backreferences to a variable-width group, *by default*** — resolving one
  means enumerating submatch assignments RE2 does not offer, so it needs a
  backtracking engine and gives up the linear-time guarantee. That engine now
  exists behind `xpath.SetBacktrackingRegex(true)`; what remains a non-goal is
  turning it on by default, since patterns can come from document data. The
  fixed-width case is *not* in this list: it has one possible assignment, so
  comparison is exact, and it is on always. See [known-gaps.md](known-gaps.md).
* **`xsi:schemaLocation` in instances, by default** — honouring it lets the
  document choose its own schema. Available opt-in behind a namespace
  allowlist; see `Schema.WithInstanceLocations`.
* **Network resolution by default** — hands control of what this process
  fetches to whoever wrote the schema.
* **DOCTYPE by default** — the entry point for XXE and entity expansion.
