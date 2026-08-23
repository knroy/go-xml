# Known gaps

Everything this implementation is measured to get wrong, and why each one is
still open. Figures come from the W3C suites, re-run at the commit that added
this file. Nothing here is aspirational: if a gap has no entry, it has not been
measured.

Three categories run through the list:

- **Won't fix** — the behaviour is deliberate, and the test disagrees with a
  choice made on purpose. Changing it would be a regression in something that
  matters more.
- **Needs an engine change** — the cause is understood and the fix is real, but
  it is a rewrite of a component rather than a patch. Attempted patches are
  recorded so they are not retried.
- **Open** — a genuine bug or unimplemented rule with no work done yet.

A note on which direction matters. A **false reject** is valid input refused;
a **false accept** is invalid input allowed. False rejects are the more serious
kind — they break working documents — so they are listed first throughout.

## Where the numbers stand

| | Suite | Result |
|---|---|---|
| XPath 2.0 | W3C QT3 (FOTS) | 99.99% — 15,180 of 15,181 in scope |
| XSD 1.0 | W3C xsdtests | 99.80% instance · 98.60% schema-validity |
| XSD 1.1 | W3C xsdtests | 99.79% instance · 97.96% schema-validity |
| RELAX NG | James Clark's spectest | 100% — 965 of 965 assertions |
| DTD | *no public suite* | unit tests only; see below |
| XSLT 2.0 | W3C xslt30-test, filtered | 75.90% — 4,075 of 5,369 in scope |
| XDM | *no public suite* | exercised through the three above |

XSLT and XDM have no percentage. That is not an oversight: there is no freely
redistributable W3C XSLT 2.0 conformance suite, so XSLT is verified by
comparing output against Saxon-HE 12.4 on two production corpora, and XDM is
the parser and tree layer underneath the other three. Neither figure is
comparable to a suite percentage, and neither should be quoted as one.

### Failure counts

| | XSD 1.0 | XSD 1.1 |
|---|---|---|
| schema false reject | 6 | 9 |
| schema false accept | 195 | 305 |
| instance false reject | 5 | 5 |
| instance false accept | 45 | 49 |

Of those, the W3C itself flags 48 cases in 1.0 and 47 in 1.1 as `queried` or
tied to an open bug — its own suite disputes, not necessarily defects here.
Counted by scanning each test's `<current status=...>`; an earlier figure of
52/52 was an estimate. See *What 100% would take* below for why this sets the
reachable ceiling below 100%.

### RELAX NG

No assertion fails. The last one to do so needed markup inside an entity's
replacement text, which was a gap in `xdm`'s parser rather than in the
validator; it is fixed, and the fix is described in
[security.md](security.md#fixed-in-the-second-audit) because getting it wrong
was a vulnerability.

Two limits remain, neither measured by the suite:

* **The compact syntax is not implemented.** Only the XML syntax is parsed. It
  would be a second parser over the same model and nothing else, since the
  compiler, restrictions and validator all work from the tree.
* **A schema's names follow XML 1.0 fourth edition**, which is what RELAX NG
  specifies, while `xdm` follows the fifth, which is what XML now is. The two
  differ deliberately; `relaxng/ncname.go` says why.

### XSLT 2.0

The weakest of the measured numbers, at 75.90%, and the newest — so it is a
floor rather than a settled figure. Two things about how it is obtained matter
before the failures are read.

There is no maintained XSLT 2.0 suite: the original XSLTS froze at 1.1.0 in
2007 behind a click-through licence, with no repository. This runs the XSLT
3.0 suite filtered by each test's declared version dependency, which measures
something different from running a suite written for the version under test.
5,369 of 14,601 cases are in scope; the largest exclusions are 6,136 needing
XSLT 3.0, 1,580 depending on a Unicode version, and 347 on `xsl:package`.

`GOXSLT_XSLTS_BYSET=1` prints the result of each test-set separately, which is
what shows where the work is. A set failing nearly everything is an
unimplemented feature; one failing a handful is edge cases. The largest gaps
by that measure:

| set | passing | what is missing |
|---|---|---|
| `error` | 245/398 | the remaining error conditions of Appendix E |
| `import-schema` | 93/189 | schema-aware assessment in the harder cases |
| `output` | 45/138 | serialisation errors, CDATA sections, encodings |
| `match` | 93/179 | patterns over schema components |
| `result-document` | 25/79 | the base output URI, and output-state rules |
| `namespace` | 140/189 | namespace fixup on constructed elements |
| `number` | 91/136 | languages other than English |
| `collations` | 17/42 | collations beyond codepoint and ASCII-case-insensitive |
| `base-uri` | 24/48 | remaining base-URI edge cases |

Roughly 240 failures are errors the suite expects that the engine does not
raise. That is the same shape as the XSD schema-validity gap: the engine
accepts a stylesheet the specification says to reject. It is the lower-risk
direction — a wrong stylesheet runs rather than being reported — but it is a
real gap, and the largest single thing standing between this number and the
others.

Appendix E of the specification lists 79 static errors. The three commonest —
XTSE0010, XTSE0020 and XTSE0090 — are now decided by the element grammar in
`xslt/elementtable.go`, which was extracted mechanically from the
specification's own element syntax summaries rather than typed: 49 elements
and 170 attributes, with the required/optional flags and the closed value sets
it states. Forwards-compatible processing (section 3.9) rides on the same
table, since an unknown element or attribute is ignored rather than rejected
wherever a version greater than 2.0 is in force.

Appendix E defines 154 error codes. 81 can now be raised; 73 cannot. All 154
definitions were extracted from the specification's own markup rather than
transcribed, which is also how the element grammar in `xslt/elementtable.go`
and the content models behind XTSE0260 were built — a hand-written list of
"elements that must be empty" wrongly included `xsl:value-of` and refused 65
valid stylesheets before it was replaced with the specification's own.

**A ceiling below 100% is structural, not a matter of effort.** Three tests
expect `XTDE3160` and `XTTE1535`, which appear nowhere in the XSLT 2.0
Recommendation; fifteen more assert a *serialization error*, and this
serialiser has no error path. Those eighteen cannot pass against a conforming
XSLT 2.0 processor. The reachable maximum is about 99.7%.

The `FORX0002` group is not a defect. Those tests expect a backreference to be
refused; this engine resolves the fixed-width ones exactly, which is more than
the suite expects rather than less. See the backreference note in the README.

### DTD

There is no public DTD conformance suite comparable to the others, so the DTD
validator's evidence is its own unit tests. It checks content models,
attribute presence and defaults, enumerated values, and `ID`/`IDREF`, over the
internal subset only — an external subset is never fetched, which is the
attack `AllowDOCTYPE` exists to gate. `DTD.HasExternalSubset` records that one
was named, so a caller knows a check was partial rather than clean.

---

## Won't fix

### DOCTYPE is refused by default

`IRI/iri-001` (schema), and any instance carrying a DOCTYPE.

A DOCTYPE is the entry point for XXE and entity-expansion attacks. Refusing it
unless the caller opts in is the correct default for a library that will be
pointed at untrusted input. `xdm.ParseOptions{AllowDOCTYPE: true}` enables it
where the documents are trusted — which is what loading UBL requires, because
the W3C XML Signature schema it depends on carries one.

### `xsi:schemaLocation` is ignored by default

Honouring it lets the document choose the schema it is validated against, which
defeats the purpose of validating. `WithInstanceLocations` opts in, with a
policy that names which namespaces may be resolved.

### `fn:collection()` raises an error rather than returning empty

`CTA/cta0022` (XSD), 7 cases in QT3 `fn-collection`.

`cta0022` wants `empty(collection())` to be true. Returning an empty sequence
for an unconfigured collection would let a stylesheet silently process no
documents and report success, which is worse than an error. `FODC0002` stands.

The 7 QT3 cases are a different matter and are listed under *Open* below: they
supply real documents through a `<collection>` environment, so they are a
capability gap rather than a disagreement. That gap is now closed, in the
engine and in the harness.

Note that `cta0022` is unaffected by the hook. With no resolver configured the
default is still `FODC0002`, which is the point.

### Unicode category drift (bug 4113)

18 cases in `MS-Regex2006-07-15` (`reJ11`, `reJ13`, `reJ19`, …), all flagged
`queried bug4113` by the W3C.

These assert that `\p{Lu}` rejects characters that *are* uppercase letters in
current Unicode. The suite was written against Unicode 3.1; the codepoints in
question — U+1D7A8 among them — were categorised differently then. Matching the
suite would mean shipping a frozen 2001 character database.

### `ibmMeta/wildcard.testSet` is mislabelled

4 cases in 1.0 (`s3_10_6v02s`, `s3_10_1ii08s`, `s3_10_1ii09s`, and one in
`anyAttribute`).

The set is tagged `version="1.0"`, but every group in it cites the 1.1 spec and
four use `notQName`, which is 1.1-only. Rejecting `notQName` under 1.0 is
correct; the tests are in the wrong bucket.

### `xs:gMonth` old lexical form (bug 6901)

`gMonth002_2061`, `gMonth004_2063`, flagged `queried bug6901`.

These use `--03--`, the withdrawn gMonth syntax from the original XSD 1.0
release. The errata replaced it with `--03`. Accepting both would mean
accepting a form no current spec defines.

---

## Needs an engine change

Each of these has a diagnosed cause and at least one attempted fix that was
measured and reverted. The attempts are recorded because the obvious patch is
wrong in a way that is not obvious.

### Content model matching is greedy, not backtracking

`MS-Particles/particlesZ040` (instance, both versions).

`matchSequence` walks the Glushkov automaton one path at a time and arbitrates
nested occurrence counters with heuristics. It decides every content model in
the suite and both production corpora except this one: a
`<sequence maxOccurs="3">` holding an optional element, an unbounded wildcard
and another optional element. Because the neighbours are optional, the wildcard
is both a *first* and a *last* position of the outer scope, so every
wildcard-to-wildcard step reads as a restart of the sequence. Twenty-three
children drove the outer count to 14 against a maximum of 3.

Two fixes were tried and both reverted:

1. Skipping the outer increment when an inner counter accounts for the step —
   restoring symmetry with `counterAllows`, which already excuses such a step
   from the outer *bound*. Unit tests stayed green; the suite lost **6 cases on
   both versions**.
2. Additionally recording, per scope, the wrap-around follow edges the compiler
   lays to make a scope repeatable, and treating only those as restarts. That
   fixes `particlesZ040` and breaks `TestInnerBoundIsNotATotal`, where an outer
   choice and an inner element are self-loops on the *same* position: the edge
   serves both scopes and excluding it from either reading is wrong.

The two pull in opposite directions because the ambiguity is real. Which scope
repeats is only knowable from the rest of the input, which is a backtracking or
subset-construction question, not something a per-edge compile-time label can
answer. Fixing it properly means replacing the matcher — a deliberate project
weighed against the 99.8% that already holds.

### An optional all group is a disjunction, not a scaled budget

`MS-ModelGroups/mgO029` (schema, 1.1).

`allSubsumes` turns a base all group into a per-name occurrence budget and
ignores the *group's* own `minOccurs`, while the derived side folds its group's
range into its branch counts. That asymmetry rejects `mgO029`, whose base and
derived are spelled identically — both `<all minOccurs="0">` around a required
element — so a type is refused as an invalid restriction of itself.

Multiplying each budget by the base group's range fixes `mgO029` and breaks
`particlesK006`, whose own documentation states the distinction:

> B's minOccurs=0, B's maxOccurs=absent, but the element has min=max=1,
> R's minOccurs=0, R's maxOccurs=1 — expected **invalid**

`<all minOccurs="0">` around a required `a1` means *either the group is skipped
entirely, or a1 appears exactly once*. It does not mean `a1` is independently
optional. Scaling to `0..1` flattens a disjunction into a range and loses the
all-or-nothing coupling that `particlesK006` exists to catch. Net effect of the
attempt was zero: one false reject fixed, one false accept introduced.

Deciding both needs the base read as *(empty) | (every budget met)* — two
alternatives checked separately.

### Regular expression backreferences — the variable-width case

One case remains: `fn-matches-51`,
`fn:matches("ab()cd()ef()gh", "^(ab)([()]*)(cd)([)(]*)ef\4gh$")`.

**The rest are implemented.** RE2 has no backreference, but it does return
capture positions, and a backreference is only *hard* when the group it names
can match more than one width. RE2 returns a single submatch assignment — the
greedy one — and cannot enumerate alternatives, so for `(a*)\1` against `"aa"`
it reports the group as `"aa"`, leaving nothing for the backreference, and a
comparison against that answers **false** where the correct answer is true (the
split is `"a"` + `"a"`). The information needed was discarded before the
comparison ran.

When every group a backreference names has a *fixed* width, the greedy
assignment is the only assignment. There is nothing to enumerate, so
capture-and-compare is not an approximation — it is exact, and it runs in RE2's
linear time with one comparison pass per candidate position. Measured on
`([a-z])\1*`: 4,000 characters in 53 µs, 64,000 in 567 µs.

So the split is by what can be *decided*, not by what a caller asked for. A
fixed-width backreference is resolved; a variable-width one still raises
`FORX0002`. That is why this needs no option to enable — an engine that answers
correctly or says it cannot is safe to have on always, where one that guesses
is not safe at any setting. The linear-time guarantee is intact, and no
backtracking engine was added.

`fn-matches-51` names `([)(]*)`, a variable-width group, *and* puts the
backreference mid-pattern, which would need the comparison to feed back into
the automaton. Both are refused.

The XML Schema pattern facet is unaffected: Appendix F's `atom` production has
no form for a backreference, so `xsd` still rejects `\1` under both versions.

## Open

Real gaps with no work done. Ordered by how much they cost.

### Schema-validity rules not yet implemented (XSD)

195 false accepts in 1.0, 305 in 1.1 — invalid schemas this loads without
complaint. 25 of the 1.0 cases and 26 of the 1.1 cases are W3C-flagged. The
rest concentrate in:

| Cluster | 1.1 count | What is missing |
|---|---|---|
| `MS-Particles` | 46 | Particle-level Schema Component Constraints |
| `MS-Schema` | 44 | schema-document structural rules |
| `MS-SimpleType` | 21 | simple type derivation constraints |
| `MS-ComplexType` | 18 | complex type derivation constraints |
| `Wild` | 17 | 1.1 wildcard rules (`notQName`, `notNamespace`) |
| `Simple` | 16 | 1.1 simple type rules |
| `CTA` | 16 | conditional type assignment constraints |
| `Open` / `PopenContent` | 15 | open content and interleave |
| `Override` | 7 | `xs:override` semantics |

These are unwritten rules rather than broken ones: each rejects a schema that
should be rejected but currently loads. None of them affects a *valid* schema,
which is why the false-reject count is two orders of magnitude smaller.

Adding rules here is the highest-yield remaining work and also the riskiest: a
rule stricter than the spec starts rejecting real schemas the suite never
covers. Every change must be measured against both suite directions *and* the
production corpora (65 UBL + 427 CII), which is the only guard against
over-strictness.

### Restriction of an all group by a wildcard or a named group (XSD 1.1)

`All/all206`, `all218`, `all237`, `Wild/wild049`, `wild050` — 5 schema false
rejects.

XSD 1.1 permits derivations the 1.0 table calls Forbidden: a sequence or a
wildcard restricting an all group, and a named model group merged into one.
`allSubsumes` decides the case where every base particle is an element
declaration, and falls back to the 1.0 table otherwise — sound, but
conservative, so these five valid schemas are refused. Extending it to cover
wildcards means deciding how a wildcard's occurrences split between the names
it spans, which `all244` shows is not a simple count.

### A choice is unordered under 1.1 (fixed)

`particlesT002`, `particlesT009`: the derived choice offers the base's
alternatives swapped. A choice imposes no order on what it admits, so the
language is identical — but `recurseLax` walked the base list left to right and
could not go back.

1.0's RecurseLax really is written as an order-preserving walk, and the suite
marks both cases invalid under 1.0 and valid under 1.1, so the relaxation is
version-gated. Under 1.1 the assignment is a matching instead: each derived
alternative must restrict *some* unused base alternative. Each base alternative
backs at most one, since merging two would let the restriction admit a sequence
twice where the base admits it once.

### An optional element may restrict an optional choice (fixed)

`particlesHa161`: `<element name="a" minOccurs="0"/>` restricting
`<choice minOccurs="0">` whose branches are `1..1`. `recurseAsIfGroup` wrapped
the element at a fixed `1..1`, so its optionality was compared against a
branch's `1..1` and rejected — but the optionality belongs to the choice, not
to the alternative inside it.

Three conditions, and the third was learned the hard way:

* **Version.** Marked invalid under 1.0, valid under 1.1, like the reorder.
* **A non-repeating base.** Where the base repeats, moving the range is what
  broke `particlesV020`: the wrapper's range also feeds `effectiveTotalRange`,
  where a group of one repeating N times contributes N elements. One range
  cannot serve both uses.
* **The derived minimum must already satisfy the base's.** Without it, moving a
  `minOccurs` of 0 onto the wrapper made it violate a base requiring 1, and
  `ctF007` became a false reject for exactly one case gained.

1.1 schema agreement 15,048 → 15,051 across these two entries, with 1.0 and
both instance figures unchanged.

### A nested all group is flattened before budgeting (fixed)

`all206`: a base `<all>` holding `<group ref>` and an element, restricted by an
`<all>` holding that element and a narrower group.

`allSubsumes` gave up on any base particle that was not an element
declaration, so a nested group sent the derivation to the 1.0 table, which
calls it Forbidden. XSD 1.1 requires a group reference inside an all group to
name a group whose model is itself an all group, and an all group of all groups
admits exactly the interleaving of their members — so the nesting carries no
information the flat list does not.

Only a group occurring **exactly once** is inlined. A repeating one multiplies
its members' occurrence ranges, and folding that into the parent would compare
the wrong budgets — the ambiguity `allSubsumes` exists to refuse rather than
guess at.

1.1 schema agreement 15,047 → 15,048, with 1.0 and both instance figures
unchanged.

### A one-member choice is not pointless under 1.1 (fixed)

`particlesZ023` and `particlesZ024`: a derived `<choice>` holding one
three-element sequence, restricting a base `<choice>` of two such sequences —
a valid dropping of one alternative.

`stripPointless` removed *any* one-member group wrapper, choices included. That
turned a choice-restricting-choice derivation into a sequence restricting a
choice, a different cell of §3.9.6's table with a different rule, and it was
rejected for "maxOccurs 3 exceeds the base's 1" — the three elements summed
against a choice that admits one branch.

Two conditions, both learned by measuring:

* **Version.** The suite marks these invalid under 1.0 and valid under 1.1, so
  the strip is *correct* for the 1.0 table and wrong only for 1.1's language
  inclusion. Removing it unconditionally fixed two 1.1 cases and broke the same
  two under 1.0, for a net loss of 3.
* **The base's compositor.** Keeping every one-member choice under 1.1 then
  turned `particlesR001` into a false reject: a one-member choice restricting a
  sequence-with-wildcard is valid, and only reaches a cell once the wrapper is
  gone. The wrapper is preserved only when *both* sides are choices, where the
  pair decides the cell.

1.1 schema agreement 15,045 → 15,047, with 1.0 unchanged.

### Particle restriction: the occurrence-carrying wrapper (attempted, reverted)

`particlesZ001` is a `<sequence>` whose `<element name="element" minOccurs="0"
maxOccurs="unbounded"/>` restricts a base `<choice minOccurs="0"
maxOccurs="unbounded">` containing that element. It is valid under both
versions and is refused.

The cause is visible: `recurseAsIfGroup` wraps the element in a group of one
and hardcodes the wrapper at `1..1`, discarding the element's own occurrence
range. A once-only group is then compared against a repeating one, so the
repetition the base allows looks like something the restriction dropped.

**Moving the range onto the wrapper fixes the case and loses ground overall.**
Measured: `particlesZ001` and `particlesZ023`/`Z024` start loading, but schema
agreement falls 14,204 → 14,194 on 1.0 and 15,045 → 15,038 on 1.1 — about
eleven invalid schemas newly accepted for each valid one recovered. Reverted.

The reason is that the wrapper's range is doing two jobs. For the mapping in
clause 2 it should repeat; for the *effective total range* check it should not,
because a group of one repeating N times contributes N elements where the
original particle contributed its own range. Carrying the range fixes the first
and breaks the second. A correct fix needs the two separated rather than one
range serving both — which is a change to `effectiveTotalRange`'s contract, not
a change to this wrapper.

### Particle restriction edge cases (XSD)

`addB118`, `addB183`, `particlesHa161`, `particlesT002`, `particlesT009`,
`particlesZ001` — 6 schema false rejects in
1.1, 2 of which (`addB183`, `particlesZ001`) also fail in 1.0.

Individually diagnosed cases in Particle Valid (Restriction) rather than one
cluster. `particlesZ001` and `addB183` failing in both versions makes them the
best entry point: they are bugs in the shared logic, not 1.1-specific gaps.

### `fn:collection()` — fixed (XPath)

All 7 QT3 `fn-collection` failures are closed; the set is 17 of 17.

`xpath.CollectionResolver` and `Context.Collections` mirror
`DocumentResolver`/`Docs`; `xslt.TransformOptions.Collections` threads it
through a transform; and the harness parses `<collection>` environments,
loading through `Runner.loadDoc` so node identity and collection stability hold
across calls.

The last two were a relative collection URI. `fn:collection` passed the
*context item's* base URI to the resolver, so `collection("collection1")` asked
about whichever document was in focus rather than what the expression named;
the spec resolves the argument against the **static** base URI. The item's base
remains the fallback for a caller who set no static base, and resolving stays
the resolver's job — the engine hands over the base and does not guess what a
URI means to the caller.

Measured against the real suite, not inferred: 7 failures before, 0 after.

`cta0022` is unaffected. With no resolver configured the default is still
`FODC0002`, which is the point.

### Harness source paths were resolved against the wrong directory (fixed)

Not an engine bug, but it was suppressing 461 cases, so it belongs in the
record.

A `<source file="...">` path is relative to the document that names it. The
catalog writes `docs/atomic.xml` from the suite root; a test-set writes
`../docs/bib.xml` from its own directory. The runner joined every path against
the root, so each test-set-relative path escaped above it, the document was not
found, and the case was skipped as "source unavailable" rather than counted.

Resolution now happens during the environment merge, where the origin is still
known — after the merge a source no longer records which document named it.
In-scope cases went from 14,720 to 15,181.

Two consequences worth noting. `fn:doc` needed a resolver in the harness for
the same reason `fn:collection` did — environments name documents by URI, and
without one those cases failed closed. And two genuine `fn-doc` serialisation
bugs are now visible that were never previously exercised: an empty element
with a non-ASCII name, and namespace declarations on a document read through
`fn:doc`.

### Instance validation gaps (XSD)

25 real instance false accepts in 1.1 after removing the W3C-flagged ones.
Diagnosed individually rather than by cluster:

- `Simple/simple001`, `simple002`, `simple016`, `simple086` — keyref `@ref`,
  and union member substitutability through a restricted union.
- `ElemDecl/typeDef012*`, `valueConstraint007*` — element declaration value
  constraints.
- `MS-ComplexType/ctZ013c`, `-d`, `-e` — complex type edge cases.
- `MS-IdentityConstraint/idG006`, `idK012` — identity constraint scoping.
- `suntest/idc006.nogen` — keyref resolution across a subtree boundary
  (a false *reject*, so more serious than the rest of this list).
- `XmlVersions/xv009` — XML 1.1 line-end normalisation, which requires the
  parser to distinguish XML 1.0 from 1.1 document declarations.
- `MS-Schema/schU4`, `schU5`, `MS-Additional/isDefault070`, `isDefault077`,
  `MS-SimpleType/stE054`, `MS-Regex/reK6`, `Complex/complex022`,
  `CTA/cta0006` — one-off cases, each needing its own diagnosis.

### `xs:decimal` printed fewer digits than it kept (fixed)

`K2-Literals-7` — a decimal literal with 359 leading zeros after the point.

`decimalScale` capped rendering at 18 fractional digits, so the literal printed
as `0` while the value kept full precision: `0.000…1 eq 0` was **false** and
`string(0.000…1)` was `"0"`. Whichever answer a caller trusted, the other
contradicted it.

A terminating decimal is now rendered in full. The bound moved rather than
disappearing — a rational that does not terminate, which is what division
produces, is still rendered at the 18 digits XPath 2.0 requires, and so is one
needing more than 1,024 digits, so formatting cannot be made to allocate
without limit. The *value* did not move; only its lexical form now says what it
is.

### Singleton XPath failures

Six remain, one per set, each needing its own diagnosis: `fn-doc-29`
(namespace declarations dropped on a document read through `fn:doc`),
`op-concatenate-mix-args-019`, `fn-union-node-args-003`, `ForExpr013`,
`CondExpr017`, `K2-Literals-7` (a decimal literal with 79 leading zeros).

Two of the four listed here previously are fixed:

* **`fn-in-scope-prefixes-23`** — `in-scope-prefixes(/)` answered with the root
  element's prefixes. The parameter is `element()`, so a document node is
  `XPTY0004`; answering a different question hid the mistake.
* **`CastableAs648`** — `for $var in "ABC" return $var castable as xs:QName`
  answered true. Casting to `xs:QName` is defined only from a *literal* string,
  because the namespace comes from the static context and only a literal is
  folded where the prefix bindings are in scope. This is a static property of
  the operand, so it is decided in `CastExpr.Eval` rather than in
  `CastToDerived`, which sees a value and cannot tell a literal from a variable
  holding one. A value that is already an `xs:QName` is exempt — it carries its
  own binding — which `K-SeqExprCastable-18` pins.

`fn-doc-available-5` and `functx-fn-doc-available-1` are **not** engine bugs:
their environment declares no `uri` for the source, so `fn:document-uri`
correctly answers with a filesystem path that no resolver knows.
`fn-in-scope-prefixes-25` needs a namespace declared through a DTD default
attribute, which `encoding/xml` never parses.

---

## What would move the numbers

Measured on 2026-08-21 with both suites present. Ordered by cases-per-unit-work,
not by cluster size — several of the largest clusters are the least worth doing.

### The shape of what is left

| | XSD 1.0 | XSD 1.1 |
|---|---:|---:|
| schema false accept | 195 | 305 |
| schema false reject | 6 | 9 |
| instance false accept | 45 | 49 |
| instance false reject | 5 | 5 |
| *of those, W3C-disputed* | *49* | *48* |

Roughly a sixth of every disagreement is a test the W3C's own metadata marks
`queried` or ties to an open bug. Those are not defects to fix.

### XSD: the 21 schema false rejects are the whole story

A false reject breaks a working caller; a false accept only fails to catch
someone else's mistake. They are not symmetric, and a single percentage hides
that. **Every one of the 21 is Particle Valid (Restriction)** — one subsystem:

| cause | cases |
|---|---:|
| a group has no corresponding particle in the base | 5 |
| the base requires a group the restriction omits | 4 |
| all-group restricted by a wildcard or a sequence | 5 |
| `notQName` needs XSD 1.1 (correct under 1.0) | 3 |
| occurrence-budget disagreements | 2 |
| other | 2 |

`particlesZ001` and `addB183` fail under **both** versions, which makes them
the best entry point: they are bugs in shared logic rather than 1.1-specific
gaps. The all-group cases (`all206`, `all218`, `all237`, `wild049`, `wild050`)
need `allSubsumes` extended to wildcards, where deciding how a wildcard's
occurrences split across the names it spans is the hard part — `all244` shows
it is not a simple count.

**Cost: small, one subsystem. Buys: 21 cases and, more importantly, correctness
for valid schemas this refuses today.** Do this first.

### XSD: the 500 schema false accepts are the largest number and the worst ratio

They cluster in `MS-Particles` (47/46), `MS-Schema` (45/44), `MS-SimpleType`,
`MS-ComplexType`, and for 1.1 also `Wild`, `CTA` and `Open`. Each is an
unwritten Schema Component Constraint: a schema that should be rejected loads.

Two reasons this is not the obvious next move despite being the biggest number.

First, **no valid schema is affected**, which is why the false-reject count is
two orders of magnitude smaller.

Second, **each rule added is a chance to reject a schema real systems depend
on**, and the conformance suite cannot catch that — it scores agreement with
W3C labels, so a rule that is merely *too strict* only shows up if the suite
happens to contain a valid schema exercising it. The production corpora are the
only guard. Re-load them after every rule:

```
65 of 65 UBL 2.1 and 427 of 427 CII/EN16931 schemas must still load clean.
```

**Cost: high and open-ended, one rule at a time. Buys: the percentage, and
little else.**

### XSD instance: 10 false rejects, individually diagnosed

`idc006.nogen` is keyref resolution across a subtree boundary. `gMonth002_2061`
and `gMonth004_2063` are the old `--MM--` lexical form (W3C bug 6901).
`particlesZ040` is the greedy content-model matcher, already documented as
resisting a targeted fix. `attP031` and `cta0022` are one-offs.

**Cost: five separate diagnoses. Buys: 10 cases, all of them false rejects.**

### XPath: 12 of the 26 are architectural, 4 are the harness

Of what remains:

- **12** are regex backreferences, which RE2 does not have. Not fixable without
  a second engine; see §2.3 of [todo.md](todo.md) for why capture groups plus
  an explicit comparison does not work.
That is the whole list. Every other disagreement has been fixed, and the route
there is worth recording: of the seventeen that remained after the ordinary
bugs, **five were the QT3 harness rather than the engine**, two needed DTD
attribute defaulting, two needed a document to be retrievable under the URI
`fn:document-uri` reports for it, and one was a lexical form that disagreed
with its own value. A conformance number is only as honest as the harness
producing it.

**Cost: a diagnosis each. Buys: little — 12 of the 17 are backreferences and 4 more are outside the engine.**

### Recommended order

1. **XSD particle restriction** — 21 false rejects, one subsystem, real callers
   affected today.
2. **XPath singletons** — 10 cases, independent, each small.
3. **XSD instance false rejects** — 10 cases, five diagnoses.
4. **Schema Component Constraints** — the 500, one rule at a time, corpora
   re-loaded after each.

XML 1.1 support sits outside this list and unlocks 38 instance tests plus nine
schemas that do not parse today; it is a larger piece of work and is described
in [todo.md](todo.md#11-xml-11-documents--the-largest-single-win).

---

## What 100% would take

Measured 2026-08-21 with both suites present. The short answer: **XPath can
reach 100% only by leaving RE2, and XSD cannot reach 100% at all** — part of
the remaining gap is the suite disagreeing with itself.

### The ceiling that is not ours

| | XSD 1.0 | XSD 1.1 |
|---|---:|---:|
| disagreements | 251 | 374 |
| of those, W3C-flagged `queried` or tied to an open bug | 48 | 47 |

Those are cases where the W3C's own metadata records a dispute about the
expected result. Nineteen of them are one cause: bug 4113, the `\p{Lu}`,
`\p{Ll}` and `\p{Lo}` tests, written against Unicode 3.1 before characters such
as U+1D7A8 moved between general categories. Passing them means freezing a
Unicode 3.1 table and being wrong about modern text. **They are a reason to
stop short of 100%, not a defect to fix.**

So the reachable ceiling is roughly **99.5% on 1.0 and 99.1% on 1.1**, not 100.

### XPath: one case, refused on purpose

`fn-matches-51` names a group whose width can vary *and* places the
backreference mid-pattern. Both are refused: RE2 returns a single submatch
assignment, so for a variable-width group the split it reports may not be the
one that matches, and a comparison against it would answer confidently and
wrongly.

Eleven of the twelve backreference cases that used to sit here are fixed. When
every named group has a **fixed** width the greedy assignment is the only
assignment, so comparison is exact and stays linear — no backtracking engine,
and the DoS class [security.md](security.md) keeps out stays out. The full
reasoning is under *Regular expression backreferences* above.

**Closing the last one would cost the linear-time guarantee**, which is a worse
trade than the case is worth.

### XSD schema-validity: 174 (1.0) and 285 (1.1) that are ours

All false *accepts* — invalid schemas that load. They cluster in
`MS-Particles` (46), `MS-Schema` (44), `MS-SimpleType` (21), `MS-ComplexType`
(18), and for 1.1 also `Wild` (17), `Simple` (16) and `CTA` (16).

Each is an unwritten Schema Component Constraint. There is no single change
here: it is one rule at a time, indefinitely, and **every rule added is a
chance to reject a schema real systems depend on**. The conformance suite
cannot catch that — it scores agreement with W3C labels, so an over-strict rule
shows up only if the suite happens to contain a valid schema exercising it.
`tests/check.sh` re-loads the corpora for exactly this reason.

### XSD schema-validity: 21 false rejects, one subsystem

The ones that matter, because a false reject breaks a working caller. **Every
one is Particle Valid (Restriction)** apart from `iri-001`, which needs a
DOCTYPE and is refused by design.

One attempt is recorded above as reverted: carrying the element's occurrence
range onto `recurseAsIfGroup`'s wrapper fixes `particlesZ001`, `Z023` and
`Z024` and costs about eleven false accepts for each — the wrapper's range
serves two jobs that want opposite answers. A correct fix separates them, which
is a change to `effectiveTotalRange`'s contract.

### XSD instance: 5 false rejects

`idc006.nogen` (keyref across a subtree boundary), `gMonth002_2061` and
`gMonth004_2063` (the old `--MM--` form, W3C bug 6901), `particlesZ040` (the
greedy content-model matcher, two reverted attempts recorded above), and
`cta0022`.

### Honest summary

| | now | reachable | what stands in the way |
|---|---|---|---|
| XPath 2.0 | **99.99%** | 99.99% | the last case is refused on purpose |
| XSD 1.0 instance | 99.80% | ~99.9% | 3 diagnoses; 2 of the 5 are disputed |
| XSD 1.1 instance | 99.79% | ~99.9% | same |
| XSD 1.0 schema | 98.60% | ~99.9% | 174 constraints, one at a time |
| XSD 1.1 schema | 97.96% | ~99.9% | 285 constraints, one at a time |

Nothing here is blocked on a missing idea. XPath's last case is a deliberate
refusal, the XSD false accepts are volume rather than difficulty, and the false
rejects are one subsystem that needs its occurrence handling reworked rather
than patched.

**Note that reaching 100% on XSD is not possible and not desirable.** 48 of the
1.0 disagreements and 47 of the 1.1 ones are cases the W3C's own metadata
records a dispute about; nineteen are the bug 4113 general-category tests,
where passing means freezing a Unicode 3.1 table and being wrong about modern
text.

## Related

[todo.md](todo.md) is the forward-looking half of this file: what to build next
and what each item would cost. Several gaps here — XML 1.1 line ends, DTD
support — are entries there as features rather than bugs.

## How to re-measure

Neither suite is vendored — both belong to the W3C, and `testdata/` is
gitignored. Clone them where the commands below expect:

```
git clone --depth 1 https://github.com/w3c/qt3tests.git   testdata/qt3tests
git clone --depth 1 https://github.com/w3c/xsdtests.git   testdata/xsdtests
```

The figures in this file were measured against `qt3tests` at `201a6e46`
(2026-05-14) and `xsdtests` at `7bc3365c` (2026-04-01). Both are updated from
time to time, so a later checkout can move a denominator.

`tests/check.sh` runs everything below in one go, and is what to use after any
substantive change:

```
GOXSLT_UBL=<ubl-dir> GOXSLT_CII=<cii-dir> tests/check.sh
tests/check.sh fast     # build, vet, unit tests, race only
```

It reports a missing suite as skipped and a present-but-silent suite as a
failure, because a check that did not run must not look like one that
succeeded. That distinction is not theoretical: the first run of this script
caught a relative `GOXSLT_QT3` resolving against `./tests/qt3/` rather than the
repository root, which made the suite skip itself while `go test` reported
PASS.

QT3 also runs from the test suite directly:

```
GOXSLT_QT3=$PWD/testdata/qt3tests go test ./tests/qt3/ -run TestQT3 -v
```

Set `GOXSLT_QT3_VERBOSE=1` to list every failure with the expression it ran,
and `GOXSLT_QT3_SET=<substring>` to run only the matching test sets — the
percentage is then labelled as filtered rather than quoted as the suite
result.

The XSD driver and the corpora runners live in [`tests/`](../tests): they were
rebuilt from scratch each time before that, which is how three metadata rules
came to silently inflate earlier measurements. See the README's *W3C xsdtests
suite* section for what those rules are.

Before accepting any change that adds a schema-validity rule, load the
production corpora — 65 UBL 2.1 entry points and 427 UN/CEFACT CII schemas.
The suite cannot catch a rule that is stricter than the spec; real schemas can.
