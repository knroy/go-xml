# XSD

`xsd` implements XML Schema 1.0 and 1.1. This page is the reference; if you
are deciding *whether* XSD is the right tool at all, read
[validation.md](validation.md) first — it starts with what schema validation
cannot check, which is the part most likely to save you time.

XSD is one of three schema languages here. DTD content models are validated by
the [`dtd`](../dtd) package, and RELAX NG by [`relaxng`](../relaxng); see
[validation.md](validation.md) for when each applies.

## Loading and validating

```go
schema, err := xsd.LoadFile("invoice.xsd", xsd.Options{})
if err != nil {
    return err
}

doc, err := xdm.ParseString(src, xdm.ParseOptions{})
if err != nil {
    return err
}

if err := schema.Validate(doc.Root, xsd.ValidateOptions{}); err != nil {
    return err
}
```

`Validate` returns nil for a valid document. Otherwise the error is a
`*xsd.ValidationErrors` holding one entry per failure, each carrying the
spec's own error code:

```go
var invalid *xsd.ValidationErrors
if errors.As(err, &invalid) {
    for _, e := range invalid.Errors {
        fmt.Printf("%s:%d:%d %s: %s\n", e.Path, e.Line, e.Column, e.Code, e.Message)
    }
}
```

The code — `cvc-complex-type.2.4.a`, `cvc-datatype-valid.1.2.1` — is what
lets a caller tell the kinds of failure apart without matching on message
text. Message wording is not part of the contract; codes are.

There are four ways in, differing only in where the schema comes from:

| | use when |
|---|---|
| `LoadFile(path, opts)` | one schema document on disk |
| `LoadFiles(paths, opts)` | several documents that together make one schema |
| `Load(root, baseURI, opts)` | the document is already parsed |
| `ParseSchema(root)` | one document, following no include or import |

`LoadFiles` is the one to reach for with a modular schema set. Handing it
every root document assembles them into a single schema, which is what UBL,
CII and the other large vocabularies expect.

## Choosing the version

1.1 is opt-in:

```go
xsd.Options{Version: xsd.Version11}
```

It is not the default because **1.1 changes which documents are valid**, and a
schema written for 1.0 must not acquire its relaxations by accident. Under
`Version11` you get `xs:assert`, conditional type assignment through
`xs:alternative` and inheritable attributes, `xs:openContent` and
`xs:defaultOpenContent`, `xs:override`, the `notNamespace` and `notQName`
wildcard forms, `explicitTimezone`, conditional inclusion through the
versioning attributes, and the 1.1 built-in types.

The 1.1 constructs are always *parsed*, whichever version is selected — a
schema that uses one is not rejected for it. Whether it is *honoured* is what
the version selects, and the distinction matters:

```go
// <xs:assert test="xs:int(a) gt 100"/> on the type of <r>
schema10.Validate(doc.Root, xsd.ValidateOptions{})   // nil — the assertion is not run
schema11.Validate(doc.Root, xsd.ValidateOptions{})   // cvc-assertion.3
```

So loading a 1.1 schema under the default version gives you a working 1.0
validator for it, silently missing the 1.1 constraints. **If a schema uses 1.1
features, select `Version11`.** Nothing warns you.

The exception is `notQName`, which is an error under 1.0 rather than ignored,
because it *narrows* a wildcard: ignoring it would accept documents the schema
means to exclude, where ignoring an assertion only fails to reject them. The
asymmetry is not principled — it is where the line happens to fall today.

## Checking the schema itself

Unique Particle Attribution, Element Declarations Consistent and Particle
Valid (Restriction) are all applied when the schema is loaded. Each is a
property of the schema alone, so a document violating one *is not a schema* in
the spec's terms, and it fails to load rather than validating clean.

This is not the Xerces arrangement, which gates the first two behind
`schema-full-checking`, off by default. That precedent governs whether a
*validator* pays the cost; it is the wrong analogy for a loader asked "is this
a schema?", where answering yes for a schema the spec says does not exist is a
false accept. The cost is paid once per load rather than once per document.

`Options.LaxUPA` relaxes Unique Particle Attribution to the permissive reading
Saxon and XSV use, in which two competing particles may be references to the
same element declaration. Without it such a schema does not load. It is off by
default because the strict reading is the conforming one, but schemas written
against either of those processors do rely on it.

`Schema.CheckConstraints` re-runs UPA and Element Declarations Consistent on a
loaded schema:

```go
if err := schema.CheckConstraints(xsd.CheckOptions{}); err != nil {
    return err
}
```

Since loading already applies them, this re-runs work the schema has passed.
It stays exported for two cases: a caller that loaded with `Options.LaxUPA`
set has no other way to ask for the strict answer afterwards, and a caller
holding a `Schema` from elsewhere may want the constraints as a list of errors
rather than as a load failure. `CheckOptions.Version` selects the UPA rule —
XSD 1.1 no longer counts an element particle competing with a wildcard as a
violation, and the zero value is 1.0, which keeps the stricter one.

## Resolving schemaLocation

`include`, `import` and `redefine` name other documents, so following them
means fetching whatever the schema asks for. Nothing is fetched unless a
resolver is configured, and the default when `Resolver` is nil follows the
grant the caller already made by choosing an entry point:

```go
xsd.Load(root, baseURI, xsd.Options{})   // refuses: no path was granted
xsd.LoadFile("main.xsd", xsd.Options{})  // &xsd.FileResolver{Root: "."} — the file's directory
```

`Load` takes a tree rather than a path, so the caller has granted nothing on
disk and a named location is refused. Only a document that actually asks is
affected: a self-contained schema, which is nearly every use of `Load`, never
resolves anything. `LoadFile` and `LoadFiles` were handed paths, so a sibling
`xs:include` still resolves — rooted at the directories named, which refuses an
absolute path elsewhere or a climb through `..`. `Schema.WithInstanceLocations`
is rooted the same way, at the directories the schema was loaded from.

To follow remote locations you have to say so, and say which hosts:

```go
xsd.Options{Resolver: &xsd.HTTPResolver{
    AllowHost: func(host string) bool { return host == "schemas.example.com" },
}}
```

`AllowHost` runs *before* the request, which makes it the place to refuse
loopback and private address ranges. Network resolution is off by default
because turning it on hands control of what this process fetches to whoever
wrote the schema.

In a server, prefer neither:

```go
xsd.Options{Resolver: &xsd.MapResolver{
    ByLocation:  map[string]string{"common.xsd": commonXSD},
    ByNamespace: map[string]string{"urn:example:common": commonXSD},
}}
```

`ByNamespace` answers an `import` that gives a namespace but no location.

`MapResolver` answers from an in-memory table and touches neither disk nor
network, so schema assembly cannot become an outbound request or a blocking
read.

### The well-known schemas, without the network

`MapResolver` matches a location literally, which is not enough for the W3C
vocabularies because one document is named several ways. The XSD 1.1 schema for
schemas is written as `http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd`, as
`http://www.w3.org/2001/XMLSchema.xsd`, as a bare relative `XMLSchema.xsd`, and
as an `xs:import` giving the namespace and no location at all. A relative
reference is also resolved against the referring document's base *before* a
resolver sees it, so what arrives is an absolute URI nobody registered.

`CatalogResolver` keys on what a reference means rather than how it is spelled:

```go
r := xsd.NewCatalogResolver()
if err := r.AddFromFS(os.DirFS("schemas"), xsd.W3CEntries()); err != nil {
    return err
}
xsd.Options{Version: xsd.Version11, Resolver: r}
```

`W3CEntries` states the aliasing as data — which file answers which namespace
and which spellings — so it is recorded once rather than rediscovered. A miss
is an error rather than a silent nil, because a catalog quietly smaller than
the caller asked for fails later and somewhere less obvious; `SetFallback`
names a resolver to consult instead.

This matters more than it looks. Schemas published by the W3C import each other
by absolute URL, and those fetches are throttled: the W3C's own copy of the
XSLT 3.0 schema in the XSLT test suite was edited in 2021 to use a relative
path, the comment there giving the reason as "W3C web site throttling". The
companion module `w3cschemas` ships those documents, kept separate because they
are under W3C rather than MIT terms.

### schemaLocation is a hint

The spec is explicit that `schemaLocation` offers a document rather than
requiring one, and this implementation follows that: an `include` or `import`
whose location cannot be resolved does not fail the schema. What that document
would have contributed is simply missing.

A reference that genuinely needed those components still fails — at the
reference, naming what is missing, and against the instance that reaches it.
That is both more useful than a load-time failure and where the spec puts it.

A `redefine` is the exception. Its children are defined in terms of what it
redefines, so an unresolvable location there is an error — unless the
`redefine` redefines nothing, in which case it asks nothing of the document
it names.

### xsi:schemaLocation is ignored by default

`xsi:schemaLocation` lives in the *instance document*. Honouring it lets
whoever supplied the document choose which schema it is judged against — a
document that fails can name a permissive schema and pass — so by default the
schema is the one the caller loaded and nothing else.

Where you do need it, it is opt-in and gated on an allowlist:

```go
extended, err := schema.WithInstanceLocations(doc.Root, xsd.InstanceLocationPolicy{
    AllowNamespace: func(ns string) bool { return ns == "urn:example:addenda" },
}, xsd.Options{Resolver: myResolver})
if err != nil {
    return err
}
err = extended.Validate(doc.Root, xsd.ValidateOptions{})
```

Three things about that shape are deliberate:

* **The zero policy grants nothing.** A nil `AllowNamespace` allows no
  namespace, so a policy that merely exists does not open the door.
  `AllowNoNamespace` is separate again, because `""` is not a namespace a
  caller thinks about and folding it in would grant it by accident.
* **A refused location is ignored, not an error.** §4.3.2 makes the attribute
  a hint, so declining to take it is not a fault in the document. A reference
  that really needed the components still fails, at the reference.
* **The receiver is not modified.** A `Schema` is immutable and shared, so
  this returns a new one. That means a fresh assembly per instance, which is
  why it is a separate call rather than something `Validate` does — a caller
  validating many documents against one schema should not pay for it.

The resolver still decides what can actually be fetched. Following untrusted
documents means pairing this with a `MapResolver`, or an `HTTPResolver` whose
`AllowHost` refuses everything you have not vouched for: the allowlist says
*which namespaces* an instance may extend, not *what it may reach*.

## The PSVI

`ValidateOptions.Annotate` writes the type of each validated node into its
`TypeAnnotation`:

```go
err := schema.Validate(doc.Root, xsd.ValidateOptions{Annotate: true})
```

That is the part of the post-schema-validation infoset the XPath and XSLT
layers consume — it is what makes `element(*, xs:date)` and typed value
comparison mean anything. It is off by default because **it mutates the tree
you passed in**, which also makes it the one option that is unsafe to use on a
tree shared between goroutines.

## Limits

Both entry points bound their work, because a schema or a document is input
like any other:

| option | default | bounds |
|---|---|---|
| `Options.MaxDocuments` | 512 | documents one assembly may read |
| `Options.MaxContentModelPositions` | 8192 | positions in one content model, ~3.3 MB |
| `Options.ParseOptions` | refuses DOCTYPE | entity expansion in schema documents |
| `ParseOptions.MaxBytes` | 64 MB | the size of one document read |
| `ParseOptions.MaxNodes` | 10,000,000 | the size of one tree, ~2 GB |
| `ParseOptions.MaxDepth` | 1000 | nesting, and so parser stack use |
| `ValidateOptions.MaxErrors` | 100 | failures collected before stopping |
| `ValidateOptions.MaxDepth` | 1000 | validation recursion, and so its stack use |
| `DefaultMaxMatchStates` | 4096 | simultaneous content-model readings per element |

`MaxContentModelPositions` exists because a content model can be far larger
than the text describing it. A group DAG in which each of n groups references
the next twice is valid, acyclic and a couple of kilobytes, yet expands to
2^(n-1) positions: at n=24 that is a 2.7 KB schema asking for 3.4 GB. Cost is
flat at ~400 bytes per position, so the default caps one model at about 3.3 MB.
A model over the limit is not built, and the schema is **refused** — its
Unique Particle Attribution and Element Declarations Consistent constraints
could not be checked, and an undecided constraint is never reported as
satisfied. Raising the limit grants memory in proportion, which is a decision
for the host rather than for whoever wrote the schema.

`MaxDocuments` exists because a schema that includes a generator of schemas
would otherwise be a way to spend the process. `MaxErrors` exists because a
document that is wrong in every element would otherwise produce an error for
each, which helps nobody and costs memory proportional to the document.

`MaxBytes` and `MaxNodes` are two limits rather than one because neither alone
bounds memory. A node costs a fixed ~200 bytes whatever it holds, so a megabyte
of `<a/>` elements measures 53 times the heap of a megabyte of text — a byte cap
says little about what a document will cost. `MaxBytes` bounds the read;
`MaxNodes` bounds what the read can allocate. Every limit takes `-1` to disable
it, for input this process produced itself.

`DefaultMaxMatchStates` is a constant rather than an option, because no schema
anyone has measured comes close to it. Matching an element's children against a
content model carries the *set* of readings the content admits — a position and
a whole vector of occurrence counts each — since nested occurrence bounds cannot
be decided one reading at a time; `xsd/nfa.go` has the argument. Merging
converged readings and narrowing each maximum to what the document can actually
reach keeps the set in single digits on both W3C suites, UBL 2.1 and the DocBook
corpus. A schema contrived to nest repetitions deeply enough to grow it is
refused with an error naming the limit, rather than being answered by an
approximation or allowed to allocate without a ceiling.

`ValidateOptions.MaxDepth` is deliberately separate from the parser's. The
validator recurses once per element depth, and exceeding Go's stack limit is a
`fatal error: stack overflow` that `recover()` cannot catch — it would take the
process down rather than fail the request. Raising `ParseOptions.MaxDepth` to
accept a legitimately deep document is a different decision from letting the
validator descend that far, so if you need both, set both.

[options.md](options.md) is the full field-by-field reference.

The DOCTYPE default is worth naming: a schema document has no use for one, and
it is the entry point for entity expansion attacks. Set
`ParseOptions.AllowDOCTYPE` only for a schema you control.

One consequence catches people out. UBL 2.1 ships
`UBL-xmldsig-core-schema-2.1.xsd` with a DOCTYPE, and every UBL document
schema includes it, so loading any of the 65 without the flag fails with a
cascade of

    src-resolve: element ref "ds:Signature" names no element declaration

— one refused include, reported as a dozen unresolved references rather than
as the DOCTYPE it actually is. With `AllowDOCTYPE` set, all 65 load clean.

### Why the occurrence counters are a vector and not a bracket per scope

Moved here from [known-gaps.md](known-gaps.md), which is for gaps; this is the
design rationale behind `DefaultMaxMatchStates`, and it is a standing
constraint on anyone touching occurrence handling. Four attempts to fix nested
occurrence bounds each traded one case for another, and they are summarised so
a fifth is not made along the same lines.

**The bug they were attacking.** A repeated group whose *only* child is itself
repeating was decided wrongly in both directions. For
`<sequence minOccurs="5" maxOccurs="5">` over `<element c minOccurs="2"
maxOccurs="2"/>` the only valid document is ten `c`, and it was **refused**;
five `c`, which no reading admits, was **accepted**. The false-accept direction
was the serious one: a `minOccurs` floor was silently not enforced.

**Why no suite saw it.** A group with two or more distinct child names was
decided correctly, which is why tens of thousands of XSD suite agreements never
covered it. It was found by differential fuzzing against a brute-force
reference and is invisible to both W3C suites — a standing reminder that suite
agreement is not coverage.

**Why the obvious fixes all failed.** `matchSequence` walked the automaton one
path at a time and arbitrated the nested counters with heuristics, tracking a
*low* and a *high* reading of each count independently. `counterAllows`
consulted the low count and `countersSatisfied` the high one, so a document was
admitted when *different* readings satisfied each bound though no single
consistent reading satisfied both. When a group holds one particle its FIRST
and LAST positions coincide, which makes the group's wraparound edge
indistinguishable from the inner element's own repeat edge, so the bracket
cannot be narrowed locally. **No per-edge compile-time label can resolve
this**, because the ambiguity is real: which scope repeats is only knowable
from the rest of the input. Every attempt that tried to label the edge
therefore had to trade one case for another.

**What the resolution requires.** The unit of tracking must be a *vector over
every scope at once* — a set of whole readings, not a bracket per scope — so
that counts inside one vector belong to one execution by construction and no
bound is ever met by a reading another bound is not measured against. Two
properties keep such a set small, and both are load-bearing: states agreeing on
position and counts are merged with a scope left behind reset to zero, so
converged executions are recognised as converged; and each maximum is narrowed
per document to what that document can actually reach. Without that narrowing
the suite's `particlesZ036` — a choice of 100,000 over a sequence of
100,000,000 over an unbounded element — gives each step three readings that
stay distinct forever and the set grows until the budget stops it.

**Only the counts are searched.** The walk stays deterministic on the
*positions*: Unique Particle Attribution guarantees at most one element
particle matches a name, and the one remaining ambiguity, an element against a
wildcard, is what erratum E1-29 leaves to the processor.

### The 254 cap in `encodeCounts` is not a bound on `maxOccurs`

The count vector is carried as a string of bytes, and `encodeCounts` caps each
count at 254. Read on its own that looks like a ceiling: an audit predicted
that `maxOccurs="300"` would accept a 301st child and that `minOccurs="300"`
would reject a valid 300-child document, since 255 and 256 both encode as 254.

Neither happens, and the reason is that a count never arrives at
`encodeCounts` un-narrowed. `reachable()` runs first and replaces every bound
above the document's own child count with `Unbounded` — a maximum a document
has too few children to reach cannot be broken, so it behaves exactly as
`unbounded` does. `capCount()` then clamps against that *narrowed* bound, and
once the maximum is out of reach it returns at most `min+1`. So a stored count
above 254 would require a scope with 255+ children still in play, and in that
scope the bound is already `Unbounded` and the exact value has stopped
deciding anything.

That is three functions' worth of reasoning to re-derive, which is why it is
pinned rather than argued: `xsd/occurs_boundary_test.go` walks `minOccurs` and
`maxOccurs` through 126/127/128, 253/254/255/256/257, 300, 1000 and
65535/65536, each at its bound and one either side, plus the nested-scope form
where the outer counter is the one that would saturate, and `maxOccurs` values
of 1,000,000 and 79228162514244337593543950335 which must behave as unbounded.

**A related reading, worth stating because it is not obvious.** A sweep of
2,028 combinations of outer bounds, inner bounds and child count once found 40
answers wrong, every one a false *rejection*, and every one with inner
`minOccurs="0"` and outer `minOccurs` of two or more at a small child count.

`<sequence minOccurs="2" maxOccurs="2">` over `<element c minOccurs="0"
maxOccurs="2"/>` is the witness, and its answers were self-inconsistent: zero
`c` accepted, one **refused**, two through four accepted. Accepting 0 and 2 but
not 1 is not the language of any particle, which is what makes it a bug and not
a defensible reading. That model describes exactly `c` occurring nought to four
times.

The rule that had been missed: XSD satisfies a particle by partitioning the
content into between `minOccurs` and `maxOccurs` consecutive parts each
matching the term, and **nothing in that rule requires a part to be
non-empty**. When the term is nullable, an empty part satisfies it. An
iteration that matches nothing is still an iteration. So the legal totals are
the union over `i` in `[oMin, oMax]` of `[i*iMin, i*iMax]`, which for
`iMin = 0` is just `[0, oMax*iMax]`.

The corollary constrains any future change here: **no maximum needs relaxing to
accommodate this.** Empty iterations are only ever added to reach a floor, and
a reading that would break a ceiling can decline to add them.

### Saturation is right for the matcher and wrong for the derivation checks

Occurrence bounds saturate at `occursHuge` = 4611686018427387903. Two bounds
that both exceeded it once compared **equal**, because both clamped to it: a
base `maxOccurs="1000000000000000000000000000000"` (1e30) restricted by three
members each at the same value has a true effective total of 3e30 against a
base of 1e30, so the restriction is invalid and was accepted — a false accept.

**The distinction is the durable part, and it is a live constraint on anyone
touching occurrence arithmetic.** The matcher compares a bound against a
*document*, where 1e30 and 3e30 genuinely are the same proposition — more
children than any document will ever have. The derivation checks compare two
bounds against *each other*, where they are emphatically not. So the exact
value is carried alongside the clamped one rather than replacing it:
`Particle` keeps `MinOccurs`/`MaxOccurs` as `int`, since the automaton, the UPA
checker and the matcher neither need exactness nor should pay for it, and gains
`*big.Int` fields that are nil unless clamping actually discarded something.

**`maxOccurs="unbounded"` stays the `Unbounded` sentinel and is never written
as a magnitude.** "No limit" and "a very large limit" are different
propositions, and conflating them is precisely how the original defect arose;
folding unbounded into the exact layer would have recreated it one level up.

**What is deliberately not exact:** everything downstream of content-model
compilation. A bound reaching the automaton is still the clamped int, because
the runtime question is "did this document supply enough children", and no
document can approach the saturation point.

### A depth bound is not cycle detection

The counterpart to the entry above, and the opposite verdict: a bound that
*does* look wrong and *is*. Twenty-four guards across `xsd/`, `relaxng/`,
`xdm/`, `xpath/` and `xslt/` stopped a graph walk at a step count — 32, 64,
256, 500, 4096 — and every one of them was a defect. They are gone; what
follows is why, because the shape is easy to reintroduce and was reintroduced
six times before it was named.

**The reason each bound was written was sound.** A model group, a union chain
or a base-type chain that reaches itself is legal to *write*; these walks run
before the content-model compiler that reports it, and would otherwise recurse
forever. The count terminated them.

**What makes it a defect is that a count cannot tell a cyclic graph from a
merely deep one.** A legal, acyclic, entirely ordinary schema — 33 user-defined
restrictions over `xs:int`, or a base declaration nested inside 32 sequences,
or 501 distinct definitions each `<ref>`ing the next — crosses the cliff and
gets the truncated answer. Nothing in such a schema is recursive or malformed.

**The dangerous part is returning a definite answer rather than a refusal.**
Almost every one of these walks answers a yes/no question, and on running out
of steps returned a `false`, a `nil` or an empty map that the caller could not
distinguish from a completed walk. The failure directions were all three kinds:

* **acceptance** — `collectElementDecls` returning an empty map skipped Element
  Declarations Consistent entirely; `nonAtomicUnionMember` returning `nil` let
  a list of lists load; a duplicate `xs:ID` was accepted once the restriction
  chain under `xs:ID` ran 64 links, and `"1.5"` validated against a type
  descending from `xs:integer` at the same depth; `checkTypeBaseCycles` giving
  up after 4096 steps meant the function that exists to catch circular types
  could not catch a large circular type. `countDigits` in `xsd/facet.go` is the
  same shape outside a graph walk: it expanded a decimal one digit at a time,
  stopped at 4096, and returned the short count, so a value with 4600 fraction
  digits satisfied `fractionDigits="4500"`. The scale now comes from factoring
  the denominator as `2^a * 5^b`, which is exact and has nothing to exhaust.
* **rejection** — `derivedFrom` refusing a legal `xsi:type`, and `relaxng`'s
  `maxRefDepth = 500` refusing a legal 501-definition grammar outright.
* **silent erasure**, the worst of the three, because nothing reports an error.
  The five walks over the derivation table (now `xdm/typeenv.go`) simply delivered the
  value untyped past 32 links, so a comparison that should have been numeric
  became a string comparison and a transform produced a wrong answer rather
  than a diagnostic. `accumulatorOrigin` in `xslt/accumulator.go` is sharper
  still: past 64 links it returned the intermediate copy it had reached — a
  node in a tree of its own, where the accumulator computes something else
  entirely. A legal-looking wrong number that nothing downstream can detect.

**Why not raise the constant.** 32 to 1024 moves the cliff without removing it
and leaves the same bug waiting at a depth nobody will test. `maxDecimalScale`
is the case that proves it: capped at 18 it printed a 360-digit decimal as `0`,
was raised to 1024, and printed `1/10^5000` as `0` for exactly the same reason.
Raising it a third time would have been the same move again — it is now gone,
the scale following the value. The arbitrariness is the argument:
`derivationMethodsTo` surfaced only because a legal schema stopped *loading*,
and its cliff sat at 65 where the validation-time walks sat at 257, because one
counted links and the other types. `relaxng`'s bound is the sharpest case — the
mechanism it was named for, `c.expanding`, sat immediately above it and already
caught every re-entry, so the count could never do the job and could only
refuse valid grammars.

**A visited set is the exact mechanism, and it must be keyed on what the
recursion revisits.** Every bound is now a set keyed on the component pointer,
or on the name string where the graph is a name-to-name registry. That
identifies a cycle exactly — the only thing the count was ever trying to
catch — and imposes no limit on a legal chain. `allDerivedDecls` is the
instructive failure: it already kept a `seen` set, but on *declarations*, which
deduplicates the result without bounding the walk, since a model group that
reaches itself revisits the same particle forever without ever repeating a
declaration.

**Convert the unreachable ones too.** Several of these walks were already
unreachable because their chains collapse during parsing. They were converted
anyway: a lone survivor of a pattern invites the next reader to copy it.

**Reachable from a schema, not from an instance.** A trusted schema with
untrusted documents cannot reach any of them, which is why none of this is a
security bound and why removing the counts costs nothing there.

**What such a test must assert.** Depths on either side of every old cliff, a
*semantic* property at each rather than that a call returned; the negative, so
that a visited set which widened the relation is caught; a genuinely cyclic
input behind a watchdog, because the regression a visited set can introduce is
a hang, which no assertion catches; and, where the registry is process-global,
type names carrying the case's own depth and walk, since `go test` runs one
process and two cases sharing a name would answer each other's questions.

Above all, **a probe must establish that the loop it measures actually runs**.
That correction is recorded in
[known-gaps.md](known-gaps.md#a-negative-result-on-a-bound-must-prove-the-loop-it-bounds-actually-runs),
because a believed-and-wrong measurement is the thing most likely to be
re-derived.


## Concurrency

A loaded `Schema` is immutable and safe to validate from any number of
goroutines:

```go
schema, err := xsd.LoadFile("invoice.xsd", xsd.Options{})   // once, at startup
// ...then, per request, from many goroutines:
err = schema.Validate(doc.Root, xsd.ValidateOptions{})
```

Its one piece of lazily-built state, the content-model cache, is synchronised
for that reason: content models are compiled on first use, so the cache is
written by whichever goroutine reaches a type first and read by the rest.

Two things are *not* shared:

* **A schema still being assembled.** Finish loading before publishing it.
* **A document tree.** Parse one per goroutine. `Annotate: true` writes into
  the tree, and even without it the tree is the one mutable thing in play.

This is tested rather than asserted. The suite runs validation from many
goroutines against both warm and deliberately cold schemas, validates
documents carrying identical `xs:ID` and key values concurrently — the state
most likely to have been hung off the schema by mistake — and loads schemas
in parallel, all under `-race`.

### Determinism

Loading the same schema twice gives the same answer. That is worth stating
because it was once untrue: `{substitution group}` membership was built by
ranging a Go map, whose iteration order is deliberately randomised, and
Particle Valid (Restriction) maps a derived choice onto a base choice with an
*order-preserving* mapping. So when a substitution-group head was expanded into
a choice, the order of that choice decided the answer — one suite schema,
loaded forty times from the same file, was accepted five times and rejected
thirty-five.

Membership is now ordered by qualified name. The spec does not order
`{substitution group}`, so any stable order conforms; what does not conform is
a different one each run. The regression test loads the schema many times,
because a single load used to pass by luck about one time in eight.

Error *reporting* is stabilised the same way: where several independent types
each fail, the messages are sorted before the error is built, so the one
message a caller sees under `MaxErrors: 1` does not vary between runs.

## Conformance

Measured against the W3C xsdtests suite:

| | schema-validity | instance |
|---|---|---|
| **XSD 1.0** | 14,385 / 14,388 (99.98%) | 24,973 / 25,000 (99.89%) |
| **XSD 1.1** | 15,349 / 15,354 (99.97%) | 26,217 / 26,222 (99.98%) |

**Earlier revisions reported 99.56% and "1.1: 100%". Both were measured
wrongly.** Two bugs in the test driver, both of which flattered the result:

*Schema-validity tests were not scored at all.* A group whose schema the suite
marks invalid by design was treated as a skip, on the reasoning that Schema
Component Constraints are a separate concern from instance validation. They are
not a separate concern from *conformance*: the schema is meant to be rejected,
and accepting it is a failure. Scoring them exposed roughly 2,200 of them.

*The 1.1 run scored about six per cent of its tests.* `common/xsts.xsd` defines
`version` as a **list of tokens** — OR-joined on `testSet`, `testGroup`,
`schemaTest` and `instanceTest`, AND-joined on `expected`, and **absent means
the test applies to every processor**. Comparing the attribute for equality
with `"1.1"` restricted the 1.1 run to the explicitly-marked groups: 888 schema
tests instead of 15,365.

The dominant remaining gap is schema false-accepts — Schema Component
Constraints not yet enforced — across facet consistency, regular-expression
syntax, particles and model groups, complex-type derivation and identity
constraints. Instance false-rejects and false-accepts together are in the low
hundreds.

Some disagreements are suite defects rather than bugs here: `anyURI_a004` is
marked `status="queried"` against an open W3C bug, and its own group annotation
contradicts the expectation recorded for it. Twenty-seven such queried cases
sit in the 1.0 instance tail.

A further 20 test groups are skipped because their schema does not load, and
most of those are correct behaviour rather than gaps: five use 1.1 constructs
under 1.0 and are *meant* to fail; two need a DOCTYPE, refused by default; and
several name a document that is deliberately absent. The `XmlVersions` group is
not among them — those schemas carry `version="1.1"` and every one of them
parses and loads, because the parser accepts the declaration and then applies
XML 1.0 rules. They are measured, but what they measure is not what they
test.

Beyond the suite, the validator is run against production schema sets — UBL
2.1, UN/CEFACT CII, Peppol BIS Billing 3.0, Factur-X/ZUGFeRD. Those found bugs
25,000 suite cases had not, because a diamond in the import graph is the normal
shape of a large modular schema set and the suite has no such thing. See the
testing section of the [main README](../README.md) for what each method
catches.
