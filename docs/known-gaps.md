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
| XPath 2.0 | W3C QT3 (FOTS) | 99.84% — 14,697 of 14,720 in scope |
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

Of those, the W3C itself flags 52 cases in 1.0 and 52 in 1.1 as `queried` or
tied to an open bug — its own suite disputes, not necessarily defects here.

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

### Particle restriction edge cases (XSD)

`addB118`, `addB183`, `particlesHa161`, `particlesT002`, `particlesT009`,
`particlesZ001`, `particlesZ023`, `particlesZ024` — 8 schema false rejects in
1.1, 2 of which (`addB183`, `particlesZ001`) also fail in 1.0.

Individually diagnosed cases in Particle Valid (Restriction) rather than one
cluster. `particlesZ001` and `addB183` failing in both versions makes them the
best entry point: they are bugs in the shared logic, not 1.1-specific gaps.

### `fn:collection()` — implemented, suite result unverified (XPath)

7 cases in QT3 `fn-collection` (`collection-001` through `-007`).

**Implemented, both halves.** `xpath.CollectionResolver` and
`Context.Collections` mirror `DocumentResolver`/`Docs`;
`xslt.TransformOptions.Collections` threads it through a transform; and the
harness parses `<collection>` environments (`Environment.Collections`) and
builds a resolver over their sources, loading through `Runner.loadDoc` so that
node identity and `fn:collection` stability hold.

**The suite number is not yet re-measured.** The QT3 checkout was not available
when this was written, so the path was verified against a synthetic suite in
the real catalog layout — three cases covering count, content and node identity
across two calls, all passing, and all failing when the wiring is removed. That
proves the mechanism, not the 7. Re-run the suite to confirm:

```
GOXSLT_QT3=/path/to/qt3tests go test ./qt3/ -run TestQT3 -v
```

Two related FOTS features stay on the skip list: `collection-stability` and
`non_empty_sequence_collection`. They are optional features with their own
semantics, and removing a skip without the suite present would count cases that
were never checked.

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

### Singleton XPath failures

`fn-doc-available-5`, `fn-in-scope-prefixes-23`, `CastableAs648`,
`K2-Literals-7` — 4 unrelated cases, each needing individual diagnosis. Not
grouped because they share no cause.

---

## Related

[todo.md](todo.md) is the forward-looking half of this file: what to build next
and what each item would cost. Several gaps here — XML 1.1 line ends, DTD
support — are entries there as features rather than bugs.

## How to re-measure

The XSD driver and the corpora runners are not in the repository; they are
rebuilt from the suite each time. QT3 runs from the test suite directly:

```
GOXSLT_QT3=/path/to/qt3tests go test ./qt3/ -run TestQT3 -v
```

Set `GOXSLT_QT3_VERBOSE=1` to list every failure rather than the summary.

Before accepting any change that adds a schema-validity rule, load the
production corpora — 65 UBL 2.1 entry points and 427 UN/CEFACT CII schemas.
The suite cannot catch a rule that is stricter than the spec; real schemas can.
