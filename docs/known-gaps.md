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
| XPath 2.0 | W3C QT3 (FOTS) | 99.86% — 15,159 of 15,181 in scope |
| XSD 1.0 | W3C xsdtests | 99.80% instance · 98.60% schema-validity |
| XSD 1.1 | W3C xsdtests | 99.79% instance · 97.92% schema-validity |
| XSLT 2.0 | *no public suite* | differential against Saxon-HE 12.4 |
| XDM | *no public suite* | exercised through the three above |

XSLT and XDM have no percentage. That is not an oversight: there is no freely
redistributable W3C XSLT 2.0 conformance suite, so XSLT is verified by
comparing output against Saxon-HE 12.4 on two production corpora, and XDM is
the parser and tree layer underneath the other three. Neither figure is
comparable to a suite percentage, and neither should be quoted as one.

### Failure counts

| | XSD 1.0 | XSD 1.1 |
|---|---|---|
| schema false reject | 6 | 15 |
| schema false accept | 195 | 305 |
| instance false reject | 5 | 5 |
| instance false accept | 45 | 49 |

Of those, the W3C itself flags 49 cases in 1.0 and 48 in 1.1 as `queried` or
tied to an open bug — its own suite disputes, not necessarily defects here.
(Counted by scanning each test's `<current status=...>`; an earlier figure of
52/52 was an estimate.)

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

### Regular expression backreferences

12 cases in QT3 `fn-matches` (`fn-matches-29`, `-30`, `-36`, `-51`, `-53`,
`K2-MatchesFunc-17`, `cbcl-matches-003` and others).

All 12 fail with `FORX0002: backreference \1 is not supported`. Go's `regexp`
is RE2, which guarantees linear time by refusing constructs that need
backtracking — backreferences among them. Supporting them means a second regex
engine with its own matcher, and accepting the exponential blowup RE2 exists to
prevent. The XSD pattern facet does not permit backreferences at all, so this
affects `fn:matches` and `fn:replace` only, never schema validation.

---

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
`particlesZ001`, `particlesZ023`, `particlesZ024` — 8 schema false rejects in
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

### `xs:decimal` keeps more precision than it prints

`K2-Literals-7` — a decimal literal with 359 leading zeros after the point.

Not a formatting bug on its own, but an inconsistency between the value and its
lexical form. `decimalScale` caps rendering at 18 fractional digits, which is
what XPath 2.0 requires an implementation to support, so the literal prints as
`0`. The *value* keeps full precision, so `0.000…1 eq 0` is **false** while
`string(0.000…1)` is `"0"`. The suite offers three acceptable answers — `0`,
the full lexical form, or `FOCA0006` — and this matches none of them, because
each assertion matches a different half of the disagreement.

Making the two agree means rounding the value to the same 18 digits the lexical
form claims, at construction. That is a change to decimal semantics reaching
every arithmetic result, and it would give up exact decimal arithmetic that
works today, for one test where the spec explicitly permits a less precise
answer. Not done, and not a silent gap: the inconsistency is the part worth
knowing about.

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
| schema false reject | 6 | 15 |
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
- **4** are the test harness or the suite's own environment data rather than
  the engine — two documents whose environment declares no URI (so
  `fn:document-uri` correctly answers with a path no resolver knows), one
  needing a namespace declared through a DTD default attribute, and unused
  namespace declarations in a hand-written `assert-xml`.
- **6** are ordinary bugs across six sets, each needing its own diagnosis.

**Cost: a diagnosis each. Buys: the remaining 10, and 99.86% → ~99.93%.**

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

QT3 runs from the test suite directly:

```
GOXSLT_QT3=$PWD/testdata/qt3tests go test ./qt3/ -run TestQT3 -v
```

Set `GOXSLT_QT3_VERBOSE=1` to list every failure with the expression it ran,
and `GOXSLT_QT3_SET=<substring>` to run only the matching test sets — the
percentage is then labelled as filtered rather than quoted as the suite
result.

The XSD driver and the corpora runners are **not** in the repository; they are
rebuilt from the suite each time. See the README's *W3C xsdtests suite* section
for the three metadata rules that decide whether the resulting numbers mean
anything — each one silently inflated an earlier measurement here.

Before accepting any change that adds a schema-validity rule, load the
production corpora — 65 UBL 2.1 entry points and 427 UN/CEFACT CII schemas.
The suite cannot catch a rule that is stricter than the spec; real schemas can.
