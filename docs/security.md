# Security

What this library defends against, what it does not, and what a caller has to
do. Everything below was tested rather than reasoned about: each claim of
safety names the check that enforces it, and each finding was reproduced before
it was written down.

The threat model throughout is that **the attacker controls the instance
document**. Where a finding needs more than that — a hostile schema, a hostile
stylesheet, a caller-enabled option — it says so, because that changes who is
exposed.

## How to read a finding here

Six audits have passed over this code, and the categories below are not
interchangeable. Conflating them is how a fixed bug stayed filed as live for a
release, and how an unproven suspicion was twice reported as a confirmed
vulnerability.

| category | what it means | what it needs to change category |
|---|---|---|
| **Fixed** | reproduced, then fixed, with a regression test that fails against the previous code | nothing; it stays as a record |
| **Open — performance** | reproduced, and the cost is real, but no wrong answer is produced | an algorithm that changes the complexity, not a smaller constant |
| **Deliberate limitation** | the behaviour is chosen, and the reasoning is written down | a spec citation showing the choice is wrong |
| **Withdrawn** | recorded as a defect, then measured and found not to be one | nothing; it stays so the same reading is not repeated |
| **Unverified** | a plausible reading of the code that nobody has demonstrated | a construction that produces a wrong answer, or a probe that shows it cannot |

The last row is the one that costs the most time. Three separate audits have
flagged a numeric bound in this package as a confirmed bug on the reading that
"deeper than N" implies "recursive"; two were right and the third was not, and
telling them apart took construction rather than argument every time. A guard
is only a defect once a legal, acyclic input crosses it and gets a wrong
answer. Until then it is debt, and `docs/known-gaps.md` records which of the
remaining ones have been probed and found sound.

## Current status

Seven audits have passed over this code and the record below is chronological,
so what is *live* sits eight hundred lines after what was fixed in the first
pass. This section is the index; everything it names is described in full
further down.

**Open.** Two, both cost rather than correctness, and both needing an
attacker-controlled input the threat model already accounts for:

| finding | reach | why it is still open |
|---|---|---|
| `keyref` is quadratic in scope count | hostile instance, recursive schema | inherent, not unfinished: a keyref builds no table for an ancestor to seed from, so every enclosing scope must check every target against its own key table. The field extraction is cached; the count of checks cannot be reduced by a traversal change. |
| `javascript:` URLs pass through | hostile stylesheet | an XSLT processor is not an HTML sanitiser; see *Open findings*. |

**Deliberate limits**, which are resource controls and not bugs — a request
refused here is refused loudly, and the fallback is conservative in the
rejecting direction:

`xdm.ParseOptions` `MaxBytes` / `MaxDepth` / `MaxNodes` ·
`xslt.FileResolver.MaxBytes` · `xsd.Options` `MaxDocuments` ·
`xsd.ValidateOptions` `MaxDepth` / `MaxErrors` ·
`DefaultMaxMatchStates` · `subsumeMaxStates` · `subsumeMaxProduct` ·
`branchLimit` · `TransformOptions.MaxDepth` · the RELAX NG derivative bound ·
the XPath regex step and depth budgets.

The distinction that matters: each of those bounds *work*, at the point the
work is done. A bound standing in for cycle detection is a different animal
and has been a defect every time it appeared here — six `depth > 32` guards,
two at 64, six base-chain counters, all converted to visited sets. See
[known-gaps.md](known-gaps.md).

**Withdrawn.** Two findings were reported, measured, and did not reproduce: a
CR in a text node *does* survive a round trip, and `$e-1` naming a variable is
required by the suite rather than a defect.

## The short version

If you parse untrusted XML with default options, the dangerous classes are
closed: no XXE, no entity expansion, no network access, no file access, no
stylesheet-driven writes, and no XInclude — parsing alone never performs an
inclusion; see *XInclude* below for what running one grants and what it does
not. Input size, node count, nesting depth and recursion
depth are all bounded by default. The one thing you must still do yourself is
**sanitise URLs if you render transform output as HTML**.

Two further cautions, from the third audit. If you set `AllowDOCTYPE: true` —
which some real formats require — the entity expansion bound now counts every
*reference* rather than every distinct entity; before that fix a 70 KB document
could allocate hundreds of megabytes. And if you compile *untrusted schemas*
rather than only validating untrusted documents, one exponential case remains
open and is described under *Open findings*.

---

## XInclude: a new reader on the old gate

XInclude was added after the third audit, and it is the first feature where the
**source document itself names a resource to read**. Everything else that
reads — `fn:doc`, `xsl:include`, `fn:unparsed-text`, external entities — is
named by a *stylesheet* or by a DOCTYPE. XInclude is named by an element in the
document, and the document is exactly the party this threat model treats as
hostile. That makes the confinement the whole of the safety argument rather
than part of it.

The answer is that there is no new gate. `xdm` has no filesystem and no
network; `ProcessXInclude` reads only what an `xdm.IncludeResolver` hands it,
and `xslt.FileResolver` implements that interface by calling the very same
`resolvePath` that already gates every other read: a non-`file` scheme is
rejected before the filesystem is touched, symlinks are resolved before the
containment check, and a path outside every root is refused. An inclusion
therefore reaches nothing `fn:doc` could not already reach — it is the same
files, from the same roots, with the same refusals.

Writing a second check here was considered and rejected. Two copies of a
containment rule are two things to keep correct, and the first time they drift,
one of them is the hole — which is the same reasoning that put
`ResolveEntity` and `ResolveText` through `resolvePath` rather than giving each
its own.

It is off by default in the sense that matters: nothing in this library calls
`ProcessXInclude`, so a caller who has not asked for XInclude does not have it,
and a caller who has asked has already named the roots. There is deliberately
no per-resolver `XInclude bool` beside `UnparsedText`: the switch would sit
below the one that already decides the question, and a caller who set it and
then wondered why nothing happened would be measuring the wrong thing.

Asserted by regression test rather than argued: a path escape (five spellings,
including `..` traversal and a bare absolute path), a symlink out of a root,
every network scheme against a canary HTTP server that records **zero** hits,
an end-to-end hostile document whose `xi:include` names an `http://` URL, and
that an `xi:fallback` cannot be used to launder a refusal into a read.

Two bounds hold the cost of one pass: at most 200 resources read in total, and
at most 40 levels of nesting. Neither substitutes for the other, and neither
substitutes for loop detection — a loop repeats a URI and is caught by name,
while a fan-out of a thousand distinct files repeats nothing and would
otherwise cost a thousand parses.

---

## What fuzzing has ruled out, and what it has not

Every audit finding below was reasoned about and then asserted by a regression
test. Fuzzing is the complement: it searches for the input nobody reasoned
about. Five targets now do that — over the XML parser, the schema assembler and
its content-model compiler, the stylesheet compiler, the XPath expression
compiler, and a parse → serialise → parse round trip. See
[testing.md](testing.md#fuzzing) for how to run one.

Each was run for 150 seconds and none found a crash. The parser alone took
about 20 million executions, the schema assembler 4.4 million, and the round
trip 3.6 million. What that buys, stated precisely:

- **`xdm.ParseString` did not panic**, and every refusal came back as an error
  value with no tree beside it. A panic on parse is a denial of service for any
  host that parses input it did not write, which is every host that accepts a
  document over the wire.
- **`xsd.Load` did not panic** at either XSD version, and every content model
  it accepted compiled to an automaton that answered every query. A `.xsd` is
  as untrusted as a `.xml` when both arrive over the wire, and the assembler is
  the larger and more recursive body of code.
- **Serialisation is faithful.** A document that parses, serialises to
  something that parses back to the same document — same kinds, same expanded
  names, same string values. A serialiser that could emit text which reparses
  differently is an injection primitive wherever output is handed to another
  processor, and no input was found that does it.

What this does **not** establish. A coverage-guided search finds what it
reaches in the time it is given, and 150 seconds is a floor rather than a
ceiling; these runs are evidence of absence only in proportion to their length.
None of the targets asserts that an *answer* is correct, only that a refusal is
a refusal — the one content-model defect that fuzzing did find here was found
by comparing against an independent oracle, a technique still applied by hand
rather than by a standing target ([todo.md](todo.md)).

Nor did any of it bear on the two **cost** findings — the quadratic identity
constraints and the exponential group-reference cycle check. Both are fixed
now, and neither was found by fuzzing: they are unbounded time with flat
memory, and a fuzzer is nearly blind to that shape: Go reports a hang only
after ten seconds in a single execution, so an input merely expensive rather
than non-terminating is recorded as slow
and dropped. That the schema target ran 4.4 million executions without tripping
the hang detector says the search did not happen to generate a deep enough
reference DAG — not that one is hard to write by hand, because it is not.

---

## Fixed in the sixth audit

### A permitted file was read whole with no byte limit

`FileResolver.readConfined` ended in a bare `io.ReadAll`. Every read the
resolver performs goes through it — `fn:doc` and `xsl:import`, external
entities, `fn:unparsed-text`, and XInclude `parse="text"` — so none of the four
had a size bound.

`xdm.ParseOptions.MaxBytes` did not cover this. It bounds the *parse*, and
`readConfined` has the whole file in memory before the parser is handed
anything; `fn:unparsed-text` and XInclude `parse="text"` never reach a parser at
all, so for those two there was no limit anywhere. A 40 MB file was read
whole through all four paths with a nil error.

**Reachable from hostile input wherever a resolver is enabled.** The
confinement decides *which* files may be read, not how large they may be, and
the stylesheet chooses among them — so one large permitted file, a log or a
data dump beside the code lists, is a memory-exhaustion primitive, and a
stylesheet that reads it in a loop multiplies it.

Fixed with `FileResolver.MaxBytes`, defaulting to `DefaultMaxResourceBytes` =
64 MB — the same figure as `xdm.DefaultMaxBytes`, so that a document is refused
at the read rather than after the bytes are spent, with the parser's identical
limit left as the backstop. One field covers all four paths rather than one per
path: every root is readable by every path, so per-path limits would only mean
the effective limit is the largest of them.

The over-read is the saturating form the entry below exists for — `lim := max;
if lim < math.MaxInt64 { lim++ }` — so `math.MaxInt64` does not overflow to a
limit that silently reads nothing. An oversized file is **refused, naming the
limit**, never truncated: a half-read stylesheet is a different, smaller
stylesheet that may well parse, and a half-read text resource is a string the
stylesheet computes with. Both are silently wrong output.

Pinned by `xslt/resolver_limits_test.go`, which runs the six-point boundary
battery — zero, negative, one, exactly at the limit, one over, `math.MaxInt64`
— against each of the four paths independently, and asserts the at-limit case
returns the whole file rather than merely not erroring.

### A key ambiguous across three siblings became resolvable again

`mergeTables` folds each child's key table into the ancestor's, and dropped a
sequence two children both defined — an ancestor's keyref cannot say which of
them it resolves to. Deleting the entry was the whole record of that, and it is
not enough: a key is absent both before it is first seen and after it has been
dropped, and the merge could not tell those apart. A third sibling found
nothing there and put it back.

The result oscillated with the sibling count — resolvable at one, ambiguous at
two, **resolvable again at three**, ambiguous at four — and the wrong direction
was acceptance. An outer keyref resolved against a key that three separate
subtrees defined.

`nodeTable.ambiguous` makes the third state explicit and `mergeEntry` is the
single path every fold goes through, so ambiguity is terminal. The regression
test walks one through seven siblings, and asserts the invariant directly as
well as through a verdict: if two distinct nodes produced the same sequence,
that sequence cannot still be resolvable. Checking the table rather than the
answer is what makes the next violation cheap to find.

Found by an external audit reading the merge, not by the oracle — the
generated corpora put targets under one scope at a time, so a three-sibling
ancestor never arose. The corpus has that shape now.

### keyref could not be pruned, so its key sequences are cached instead (cost still open)

The pruning that made `key` and `unique` linear does not transfer. A `key`
builds a table its ancestor seeds from, so a pruned subtree is still accounted
for; a `keyref` produces nothing, and a node under a nested keyref scope is a
target of that scope *and* of every enclosing one, each resolving against its
own key table. Both checks are required, so the count of checks really is
nodes times enclosing scopes and no traversal change removes it.

What was repeated needlessly is the field extraction, which is the expensive
half — a node's key sequence does not depend on which scope is asking.
Memoising it per (node, constraint) leaves the quadratic count of map lookups
and removes the quadratic count of `selectNodes` calls: at depth 960, 221 ms
and 5,164,375 allocations become 158 ms and 2,863,754; at width 40, 324 ms and
5,269,468 become 217 ms and 1,297,551.

**The curve is unchanged, and this entry is therefore only half a fix.** The
count of checks is nodes times enclosing keyref scopes, and that is what the
semantics require rather than what the implementation happens to do — so it is
listed under *Current status* as open. What was removed is the repeated field
extraction inside each check. A caller exposed to hostile instances against a
recursive schema carrying a keyref should bound the request with
`ValidateContext`.

### The language-inclusion procedure declined any bound above 64

XSD 1.1's semantic subsumption check unrolls a repetition into one state per
copy and refused outright above `maxOccurs="64"`, falling back to the
structural 1.0 rules — which can refuse a restriction whose language really is
a subset. The bound was a second cliff in front of the real one:
`subsumeMaxStates` is checked on every iteration of the unroll, so the state
budget already stops a bound too large to unroll, and stops it where the cost
is incurred rather than at a number chosen in advance. Removed. Bounds of
1,000,000 and `unbounded` still load in milliseconds, through the budget.

### Six base-chain counters accepted a duplicate xs:ID and rejected legal schemas

The counterpart to the `depth > 32` and `depth > 64` findings below, and the
one that took three audits to settle because a measurement recorded here
cleared it wrongly.

Eleven `seen > 64` and `seen > 256` counters remained on walks up a type's base
chain. An earlier revision of `docs/known-gaps.md` recorded a legal 300-link
restriction chain as evidence that they truncated nothing, and an external
reviewer reasonably withdrew their claim on the strength of it. The measurement
was real; it cleared the wrong walk. The probe drove a *facet* chain, and
`SimpleType.Primitive` is filled in eagerly as each link is built — set on the
deepest link at depth 1, 64 and 300 alike — so `primitiveOf` returns on its
first iteration and a 300-link chain exercised the loop exactly once.

The walks that do iterate are the ones asking what the parser did not
pre-answer. Six truncated on a legal, acyclic chain:

| walk | truncated to | consequence |
|---|---|---|
| `idKind` | `""`, not an ID | **a duplicate `xs:ID` was accepted** at 64 links |
| `descendsFromInteger` | `false` | **`"1.5"` validated** as an integer descendant at 64 |
| `derivedFrom` | `false` | `cvc-elt.4.3` refused a legal `xsi:type` at 257 |
| `typeDerivedFrom` | `false` | schema-time derivation denied at 300 |
| `derivationMethodsTo` | `(_, false)` | a legal schema failed to **load** at 65 |
| `substitutionMemberBlocked` | `false` | reachable once the load above succeeded |

The two false accepts are the ones that matter, and neither schema is recursive
or malformed. **Reachable from a schema rather than an instance alone**, so a
trusted schema with untrusted documents cannot reach it.

That the cliffs land at 65, 257 and 300 rather than at one number is the
argument against raising a constant: one walk counts links, another counts
types, and no single bound is correct for both. All twelve counters are visited
sets now, including the six that were already unreachable — a lone survivor of
a pattern this one invites the next reader to copy it.

`xsd/deep_derivation_test.go` pins all twelve at 1, 2, 31, 32, 63, 64, 65, 128,
255, 256, 257, 300 and 512, asserting a semantic property at each — a deep type
is still an ID, still derives for `xsi:type`, still honours `block=` — rather
than that a call returned something. It also pins the collapsing walks, so the
superseded negative result cannot be re-derived from the same shape.

### Occurrence arithmetic wrapped negative, and a bound was compared against garbage

`occursHuge` — `int(^uint(0) >> 2)`, roughly a quarter of the int range — is
what an occurrence bound too large for an `int` saturates to, so a schema may
write `minOccurs="79228162514244337593543950335"` and get a workable number.
It survives being doubled but not tripled: `occursHuge*3` is negative, and the
Go compiler rejects it outright as a constant expression.

The derivation checks in `restrict.go` multiply a particle's bounds by a model
group's length and sum bounds across its members, with no guard. A sequence of
three members at a saturated `minOccurs`, restricting a choice, produced

    minOccurs -4611686018427387907 is below the base's 0

A wrapped bound in a diagnostic is the visible half. The dangerous half is that
a negative bound can satisfy an inequality it should fail, which would admit an
invalid restriction.

Sixteen sites were the problem. The reported multiplication was three of them;
the other thirteen were found by sweeping for the pattern, and the sums in
`effectiveTotalRange` are the worse find — they overflow at *two* saturated
members rather than three, and they feed the wildcard-restriction path
independently. `xsd/occurs_overflow_test.go` asserts positively on the
computed bound rather than merely on the absence of a minus sign, and it
caught a second distinct wrap the reported case never reaches.

Saturating those sites closed the wrap and left a second defect, since fixed:
two bounds *both* past `occursHuge` clamped to the same value and so compared
equal, which let a derived total of 3e30 pass against a base of 1e30. The
inequality is now evaluated exactly. `Particle` keeps its `int` bounds for the
runtime matcher and carries the true value in two `*big.Int` fields that stay
nil unless clamping actually lost something, so `addExact`/`mulExact`/
`cmpExactOccurs` take an int fast path on every schema that is not
pathological — the allocation count in `BenchmarkValidateInstance` is
unchanged. `maxOccurs="unbounded"` deliberately stays a sentinel rather than a
magnitude: "no limit" and "a very large limit" are different propositions, and
treating them alike is what produced the defect in the first place.
`xsd/occurs_exact_test.go` pins the ordering in both directions, so a fix that
merely rejected everything large would not pass it.

### Identity constraints were quadratic on recursive elements

Reachable from a hostile instance with default settings, but it needs a schema
where a **recursive** element carries an identity constraint with a `.//`
selector. Independently reproduced:

| depth | input | time | allocated |
|---|---|---|---|
| 60 | 6.9 KB | 9 ms | 5 MB |
| 120 | 14.0 KB | 25 ms | 20 MB |
| 240 | 28.9 KB | 79 ms | 82 MB |
| 480 | 58.7 KB | 304 ms | 332 MB |

Doubling the depth quadruples both.

The figures below are what the shape cost *before* the fix above, kept because
they are what the amplification looked like and because the width factor is the
part every earlier account of this got wrong.

**`MaxDepth` bounds one of the two factors, not the cost.** This section used
to claim the default `MaxDepth` of 1000 bounded the whole thing, at 111 KB
costing 1.2 s and 1.2 GB. That is the worst case for a *chain*. The cost is
depth times subtree size, and **width is the factor `MaxDepth` does not touch**.
Re-measured at depth 990, varying the number of children per level:

| width | nodes | input | time | churn | live heap |
|---|---|---|---|---|---|
| 0 | 990 | 16.3 KB | 291 ms | 151 MB | 2.4 MB |
| 10 | 10,890 | 169.9 KB | 2.41 s | 2.19 GB | 9.7 MB |
| 40 | 40,590 | 659.8 KB | 12.6 s | 8.71 GB | 43.8 MB |
| 80 | 80,190 | 1.31 MB | **26.2 s** | **17.7 GB** | 73.8 MB |

`DefaultMaxNodes` is 10,000,000, so the same shape goes further within default
parse limits. What the old text got right is the *kind* of failure: live heap
stays at 74 MB against 17.7 GB of churn, so this starves a service of CPU and
hammers the collector rather than OOM-killing it.

The per-depth figures in the table above are also stale — re-measured on the
current tree, depth 480 costs 47 ms and 35 MB rather than 304 ms and 332 MB.
The shape is right and the constants are an order of magnitude out; whether the
code got faster or the original run was on slower hardware could not be
established from the history.

**The trigger is narrower than "a `.//` selector".** A control with the same
document and the same selector, but the constraint declared on a
*non-recursive* wrapper, is perfectly linear — 117 µs at depth 60, 1.72 ms at
960. What costs is the constraint sitting on an element that is its own
descendant, so `buildNodeTable` runs once per level and each run walks the whole
remaining subtree. The recursion is the load-bearing half.

**Fixed.** The evaluator now visits each node a bounded number of times, and
the quadratic is gone: doubling the depth doubles the work rather than
quadrupling it, measured at 2.00x across 240, 480 and 960 where it was 3.98,
3.99 and 4.00. `xsd/identity_stats_test.go` asserts that ratio, so a return to
the old shape fails the build rather than being noticed in a profile later.

What changed is which subtree gets walked. A descendant element that declares
the same constraint is itself a scope, and its own table already holds every
target beneath it — so the walk stops there instead of descending again. On the
recursive shape that turns a whole-subtree walk per level into a walk of the
gap between one scope and the next. The child tables now carry every target
they selected, not only the entries that survived merging: an ancestor decides
duplicates from those, and a sequence two siblings share has to survive here
even though `entries` drops it for keyref resolution.

| | before | after |
|---|---:|---:|
| depth 960 | 23.6 ms, 1,400,001 allocations | **1.2 ms, 15,423** |
| depth 200 width 40 | 214 ms, 1,223,930 | **121 ms, 104,340** |

The first attempt at this was wrong in the direction that matters, and the
oracle caught it: seeding the subtree's targets into the table only when the
walk revisited them meant a target from below was never seen at all, so a key
at depth 1 and the same key at depth 2 stopped colliding. 771 disagreements
across the two generated corpora, and the four hand-written cross-level cases
failed with it. Seeding before the walk rather than during it fixes it. That is
precisely the mistake the review predicted a bottom-up merge would make.

**The four earlier attempts, all reverted.** They are kept because each one is
a plausible idea that measurement refuted, and because the reason they failed
is the reason the fix above works: they all tried to make the same traversal
cheaper, and the traversal was not the thing to change.

**A fifth was tried and kept.** The four below all attacked the traversal.
This one leaves the traversal alone and removes the *recomputation*: the child
tables already flowing up through `mergeTables` carry the key sequences for
everything below, so `buildNodeTable` seeds from them and derives a sequence
only for a target it has not seen. The duplicate scan still runs at every
scope, because a collision that exists only in an outer scope cannot have been
reported by an inner one. Allocations on the wide shape fall 3.8x at width 20
(2,703,986 to 718,996) and 4.2x at width 40, with time down 25% and 15%; the
deep-and-narrow shape is unchanged, and the W3C XSD corpus is unmoved at 35.1 s
against a 35.6 s baseline with identical agreement counts. The curve is still
quadratic — this lowers the constant on the factor that produced the 17.7 GB of
churn rather than changing the shape.

What made it safe to attempt is `xsd/identity_oracle_test.go`, which decides
validity from the spec's definition over a generated tree and never calls
`selectNodes`, `buildNodeTable` or `mergeTables`. It agrees with the engine on
6,000 documents, and — the part that matters — it was checked against a
deliberately broken `buildNodeTable` that only scans direct children, which is
the mistake an incremental rewrite invites. It caught 416 disagreements, every
one a false accept.

The list:

- A narrower `selectNodes`, walking descendants once for a single-step `.//a`
  rather than re-walking from every descendant: cut allocations ~11% and left
  the curve quadratic.
- Memoising selector evaluation per (element, constraint): no effect at all,
  because each level of the recursion is a *different* element, so the cache
  never hits.
- Matching each descendant *upward* against the step list — one subtree
  traversal instead of a forward walk from every start. Correct, and the suites
  agreed, but it only moves the cost: it halved the deep-chain case (27 ms to
  13 ms at depth 960) and made the wide cases **1.6× slower** (177 ms to 284 ms
  at depth 200, width 20), because re-walking a candidate's ancestors costs
  more than the forward walk it replaced whenever the tree is broad.
- Replacing `descendantsOrSelf` with a callback walk over child slices, to stop
  materialising the subtree. An allocation profile had said `descendantsOrSelf`
  was 90% of the churn, which made this look like the obvious win. It measured
  **2.5× worse** — 35 MB per operation against 14 — because the per-level
  slices the walk pushes cost more than the single flat slice they replace.
  Being 90% of the allocation does not mean a cheaper way to allocate it
  exists.

The reason it resists a local fix is that **cross-level duplicate detection
needs the whole-subtree walk**. A key at depth 1 and the same key at depth 2 must
collide, and only the ancestor's full walk sees both — verified: that document
is correctly rejected today. `mergeTables` cannot be reused for this, because it
*drops* conflicting entries by design (the spec's rule for tables merged from
below) where `buildNodeTable` must *report* them. A bottom-up rewrite would have
to reproduce that difference exactly, along with per-target error reporting.
That is a redesign of identity-constraint evaluation, not an optimisation.

**A caller can now bound it.** `Schema.ValidateContext(ctx, root, opts)` takes a
context and stops when it ends, returning `context.DeadlineExceeded` or
`context.Canceled` rather than a `*ValidationErrors`. The check sits in the
selected-node loop of `buildNodeTable` and `checkKeyref` — inside the walk whose
cost this finding is about, not merely around it — so a deadline is honoured
mid-run rather than reported afterwards. The quadratic curve is unchanged; what
changes is that a service can put a ceiling on it that does not depend on
`MaxDepth`. `Validate` without a context still runs to completion.

### A 3 KB schema took 35 seconds to load, in two places

A group referencing the next one twice, for 29 definitions, is acyclic and
valid and has 2^28 distinct root-to-leaf paths in a 3.0 KB file. Loading it
took 35.8 seconds. A CPU profile put 86% of that in `cycleFrom` and 8% in
`badNestedAll` — not in group expansion, not in automaton construction, and not
in UPA checking, none of which appear in the profile at all. A `<group ref>`
resolves to the definition's own `ModelGroup` pointer, so the component graph
is a genuine DAG with flat memory; validation against the same schema is
linear. The entire cost was two graph walks at load enumerating paths.

`cycleFrom` kept only the current descent, on the reasoning that a group
reachable by two disjoint routes is not a cycle and marking it visited would
misreport. That objection is correct against two-colour marking and does not
apply to the three-colour form: a group explored to the bottom without a cycle
stays acyclic whatever route reaches it next, so it can be pruned, while only a
group on the *current* path is a back edge. `badNestedAll` had no memo at all.

Both memoise now, and the shared `done` set spans every root so the pass is
linear in the graph rather than per root. The same shape at n=40 — over five
hundred billion paths — loads in 0.01 s, where before the fix n=32 did not
finish in 90 seconds. `xsd/group_dag_test.go` pins that alongside three cycle
shapes that must still be caught.

Fixing only `cycleFrom` would have left an 8%-of-a-huge-number exponential
behind, reaching the same wall a few groups later. That is why the profile
mattered more than the documentation's account of the cause.

### An assertion rejected a valid document 33 elements deep

`maxAnnotateDepth = 32` bounded the walk that labels an element and its
descendants with their types before an XSD 1.1 assertion is evaluated. Past it
the descendants went unannotated, and an unannotated node atomises as
`xs:untypedAtomic` — precisely what the annotation exists to prevent. Since
XSD 1.1 makes an XPath evaluation error a *false* assertion result rather than
a distinct outcome, the truncation did not degrade to "unknown": it produced a
definite `cvc-assertion.3` violation. A schema whose assertion holds was
**valid at nesting 32 and invalid at 33**, on documents differing in nothing
but depth.

The bound's own comment claimed the walk "follows declarations rather than
nodes and would otherwise not terminate". That was wrong, and checking it is
what settled the fix: the recursion descends `el.ChildElements()`, so it enters
a child only where the *instance* has one. A recursive type is legal but its
instance is finite, and document nesting is already bounded by
`xdm.ParseOptions.MaxDepth`. The bound never prevented a loop — it could only
truncate. It is gone rather than raised, and a self-referential type still
terminates at instance depth 900.

Worth recording for anyone calibrating severity: a `sum(.//n) ge 1` variant
stayed valid at every depth, because `untypedAtomic` casts to double and the
comparison survives. The bug needs a type that will not silently coerce —
`xs:date` comparison, or `instance of`. That is likely why no suite case caught
it.

## Fixed in the fifth audit

### A depth bound on four schema-graph walks accepted documents the schema forbids

Four walks over the schema graph stopped at `depth > 32`. The bound was there
for a real reason — a model group or union chain that reaches itself is legal
to write, and these walks run before the compiler that reports it — but it
conflated a graph that is *cyclic* with one that is merely *deep*, and three of
the four returned a **definite answer** on running out of depth rather than a
refusal. In each case the answer was the permissive one.

| walk | truncated answer | what it decided |
|---|---|---|
| `collectElementDecls` | empty declaration map | Element Declarations Consistent skipped entirely |
| `nonAtomicUnionMember` | `nil`, the same value as a clean result | `cos-list-of-atomic` passed; a list of lists loaded |
| `particleMatchesOnlyEmpty` | `false` | a type with `appliesToEmpty="false"` was opened anyway |
| `SchemaUnionMemberTypes` | `(nil, false)` | `1 instance of t:U` went false on a legal union chain |

The first is the sharpest. Taking the suite's own `saxonData/wild068` — a base
declaring `<e>` as a date/time union, a derived type replacing it with a lax
wildcard, and a global `<e>` of type `xs:duration` matched through it — and
nesting the base's declaration inside 32 sequences turns a document XSD 1.1
requires rejecting into one this engine accepted. Nothing about that schema is
recursive or malformed.

**Reachable only from a schema, not from an instance.** For a deployment with a
trusted schema and untrusted documents — the common shape, and the one
[server.md](server.md) describes — the schema is fixed and this cannot be
reached. It matters where schemas are themselves accepted from callers, and for
machine-generated schemas, which reach nesting depths hand-written ones do not.

Fixed by replacing each bound with a visited set keyed on the component
pointer, which stops a cycle exactly and does not limit a legal chain. The
XSLT-side `SchemaUnionMemberNames` already had a `seen` map by name, so its
bound was pure truncation and simply went. Pinned by
`xsd/depth_acyclic_test.go`, which checks both shapes at nesting 0, 31, 32, 33
and 64, and separately that a genuinely cyclic union still terminates. Both
regression tests fail against the previous code at exactly 32.

The audit that found this reported it as *unproven* — "a high-value target for
differential testing, not a confirmed vulnerability" — and it was right to.
Two of my own first attempts to reproduce it showed no difference, because the
rule is XSD 1.1 only and `Options{}` defaults to 1.0, which silently no-ops it.

**The same confusion at 64.** The follow-up audit noted two walks left at
`depth > 64` — `walkParticleElements` and `allDerivedDecls` — and again filed
them as unproven, "my highest-value XSD correctness probe". Again correct:
`walkParticleElements` feeds `checkTypeTables`, and a declaration the walk does
not reach is one whose type alternatives are never checked, so a schema
violating `src-type-alternative` — a default alternative that is not last —
loaded clean with its declaration 64 groups deep. `allDerivedDecls` feeds three
restriction checks and dropped declarations the same way; its existing `seen`
set deduplicates results but cannot terminate the walk, since a model group
reaching itself revisits a particle without repeating a declaration. Both are
visited sets now, and `TestDepthAcyclicTypeAlternatives` pins the first at
nesting 0, 63, 64, 65 and 96.

## Fixed in the fourth audit

### A negative xsd.ValidateOptions.MaxErrors approved invalid documents

`schema.Validate(root, xsd.ValidateOptions{MaxErrors: -1})` returned `nil` for
a document that is flagrantly invalid. Minimised: a schema declaring `<r>` with
empty content, validated against `<r><nope/></r>`, passed.

The stop check in `xsd/validate.go` had no `> 0` guard, so at a negative limit
`0 >= -1` held on the very first failure: validation stopped before recording
anything and the caller was told the document was valid.

**Not reachable from hostile input** — it needed the *caller* to pass a
negative value. But it was reachable by a caller following the documented
idiom: `-1` means "no limit" in `xdm.ParseOptions.MaxBytes` and in
`dtd.Options.MaxErrors`, whose validator implements it correctly with a
`v.max > 0 &&` guard. A caller copying that idiom across got a validator that
approved everything.

The failure shape was the dangerous one — **a silent pass, not an error** — the
same shape as the `HTTPResolver` overflow below. Fixed with the
`v.opts.MaxErrors > 0 &&` guard matching `dtd`, so a negative value now means
no limit as the idiom implies. Pinned by `xsd/limits_boundary_test.go`, which
is where the bug was found: the boundary table was written first and recorded
this as a skipped expectation.

### The largest byte limit a caller could name refused every document

`ParseOptions.MaxBytes` and `HTTPResolver.MaxBytes` wrap the reader in
`io.LimitReader(r, max+1)`, one byte over so that hitting the limit is
distinguishable from a document exactly at it. At `math.MaxInt64` that addition
overflows to `math.MinInt64`, and `io.LimitReader` reads a negative limit as
"nothing left".

So the setting a caller would pick to mean *do not limit me* was the setting
that broke. `xdm.ParseString("<r/>", ParseOptions{MaxBytes: math.MaxInt64})`
failed with "no root element"; the HTTP resolver was worse, returning an empty
body with a **nil error** — a schema that silently loaded as empty rather than
one that refused to load. Both saturate now, and
`TestMaxBytesAtMaxInt64` pins the boundary along with the small-limit case that
must still be refused.

A third `max+1` at `xdm/dtd_external.go` is not affected: its budget is clamped
to an internal constant that no caller can raise.

### `AllowHost` is a name check, and said otherwise

`HTTPResolver.AllowHost` was documented as "the place to refuse loopback,
link-local and private ranges — the addresses an SSRF is usually aimed at". It
cannot do that: it receives a hostname, and a permitted name may resolve to any
of those ranges, or to a different address by the time the connection is made.
The advice invited exactly the mental model that gets an allowlist bypassed.
The field now says it is an allowlist of names, that DNS rebinding defeats a
name check by construction, and that the boundary belongs in a `Transport`
whose `DialContext` sees the resolved address.

### Filesystem confinement is enforced at open time, not before it

`resolvePath` called `filepath.EvalSymlinks`, compared the result against the
roots, and the file was opened later. Those are two moments, and an attacker
able to write to the filesystem between them can replace a checked path with a
link pointing out of the root — the opened file is then not the checked one.

Reads now go through `os.Root`, added in Go 1.24 and available because this
module requires 1.25. Each path component is resolved against the root's own
descriptor and traversal out of it is refused by the kernel, so containment
holds at the moment of the open rather than resting on a string comparison
taken earlier. The prior check is kept: it decides which root a path belongs to
and produces the error naming the permitted directories. It is the diagnosis;
`os.Root` is the enforcement.

One subtlety in the implementation: the *parent* directory of a path is
symlink-resolved so it can be compared with a resolved root — on macOS `/var`
is itself a link to `/private/var`, and comparing a resolved root against an
unresolved path matches nothing. The final component is deliberately left
unresolved, because resolving it is precisely the check-then-open gap being
closed.

This needs filesystem write access and so was outside the threat model a
hostile document reaches. It is fixed because a multi-tenant deployment can
hand exactly that access to an untrusted caller.
`TestReadConfinedRefusesSwappedSymlink` swaps a file for a link out of the root
and asserts the read is refused.

### The resolver no longer serialises cache misses

`loadTracked` held its mutex across `os.ReadFile` and the parse, so concurrent
transforms sharing one resolver loaded modules one at a time on a cold cache —
the lock was protecting cache correctness but covering I/O as well.

Simply releasing it would have been wrong. `fn:doc` is defined to return the
same node for the same URI within one execution, so the cache is correctness
rather than speed, and two goroutines that each parsed and each published would
hand out two document nodes for one document. The lock now covers the cache and
an in-flight table only: a path already being read is announced, and a second
caller waits for that parse instead of starting its own.
`TestFileResolverConcurrentLoadIdentity` runs sixteen readers against each of
eight documents and asserts every reader gets the identical tree.

## Fixed in the third audit

Four issues, each with a regression test. Two of the three high-severity ones
were reachable without any opt-in beyond what this document already tells real
callers to set.

### Entity expansion was charged once per entity, not once per reference

**Reachable with `AllowDOCTYPE: true` alone** — no external entities, no
resolver, no hostile schema. `maxTotalEntityBytes` was charged where an
entity's replacement text is first memoised, which happens once per *distinct
name*. The decoder then substituted it any number of times with no further
accounting. Neither `MaxBytes` nor `MaxNodes` caught it: a reference is three
bytes, and a run of them coalesces into a single text node.

A 70 KB document allocated 741 MB and was accepted; a 356 KB one reached 14 GB.
The bound existed and was reported as working because the *rewrite* path
charged per reference — so which of two code paths a document happened to take
decided whether it was bounded at all.

The charge now happens as the document streams past, before the decoder expands
anything. Checking after the parse was tried first and rejected: `encoding/xml`
coalesces a run of references into one token, so the refusal arrived only after
the memory had been allocated.

### A nested expression could kill the process, not the request

**Reachable from a hostile stylesheet or `xs:assert/@test`**, not from a
document. The XPath parser had no depth limit, so deeply nested parentheses
exhausted the goroutine stack. In Go that is a *fatal error*: `recover()`
cannot catch it, so a server does not fail the request, it dies. The XML
parser's element-nesting limit is no help, because the whole expression lives
inside one attribute value. Under a hardened `SetMaxStack`, 14 KB was enough.

Expression nesting is now bounded at 1000 levels and reported as `XPST0003`.

That bound was first described here as "counted at the single point every
nesting construct passes through". It was not: two constructs did not pass
through it, and a fourth audit found both still crashing the process.

- **Sequence types.** `parseSequenceType` recurses into itself for a
  parenthesised item type, for a function test's argument and return types,
  and for the member types of `map()` and `array()` — none through
  `parseExprSingle`. `1 instance of ((((…item()…))))` at 400 KB was enough at
  Go's default 1 GB stack. Reachable through any `@select`, `@test` or `@as`,
  and through `xs:assert/@test`.
- **XSD pattern facets.** The XSD-flavour regex parser recurses once per
  group and counted nothing; a 6 MB schema was enough. Hostile-schema only —
  the XPath-flavour checker is an iterative scanner, so a pattern arriving as
  document data never reached it.

Both are now bounded at 1000 levels, counted at their own recursion points.
The lesson is that "every construct passes through here" is a claim about a
grammar, and it needs a test per construct rather than one test and an
argument: `TestDeepTypeIsRefusedNotFatal` and
`TestDeepSchemaPatternIsRefusedNotFatal` now stand alongside the original.

### RELAX NG: nested `oneOrMore` is exponential in document *width*

**Reachable with default options from a hostile instance** — the most exposed
of the three. A 189-byte schema and a 63-byte instance cost over a second and
more than a gigabyte, growing several times over for every two children added.

`MaxDepth` provably cannot bound this: the document is two levels deep however
wide it grows. The derivative pattern is now bounded by size
(`MaxPatternSize`, default 100,000 nodes), which holds the cost flat.

**This is a bound, not a cure.** The structural fix is interning the pattern
so that shared sub-patterns are not duplicated, which is a redesign of the
derivative engine. A legitimately very wide document validated against a
`oneOrMore` nested in a `oneOrMore` will hit the limit and get an error naming
the cause rather than a verdict. The spec suite passes unchanged.

### `xsl:analyze-string` ignored the regex step budget

**Only when the backtracking matcher is explicitly enabled**, which is a
documented opt-in. The grouping code called the matcher without checking
whether the budget had been exhausted, which the interface's own contract
requires. An exhausted budget was indistinguishable from a genuine non-match,
so the transform silently produced wrong output on exactly the inputs where the
answer was hardest to compute — the guess this package refuses to make
everywhere else. It now raises `XTDE1140`.

### XSD group references were exponential at schema load — fixed

**Hostile schema only**, so materially lower severity than the above: a caller
compiling an untrusted schema has already accepted more than a caller
validating an untrusted document. A 3.0 KB acyclic schema took 35.8 seconds.
The cycle check enumerated paths rather than traversing the graph, so cost was
exponential in the depth of the reference DAG while memory stayed flat —
invisible to a memory limit.

Fixed by the mark-acyclic memoisation this entry proposed, in the sixth audit,
along with a second exponential walk the diagnosis here had missed:
`badNestedAll` carried no memo at all and accounted for 8% of the load against
`cycleFrom`'s 86%, so fixing only the one named here would have moved the wall
a few groups later rather than removing it. See *A 3 KB schema took 35 seconds
to load, in two places* above. n=40 — over five hundred billion paths — now
loads in 0.01 s.

---

## Fixed in the second audit

Four issues, all in code added after the first audit: the entity-markup
rewrite in `xdm`, and the RELAX NG validator. Each has a regression test.

### Entity references were expanded inside CDATA, comments and PIs

**High.** The rewrite that lets an entity hold markup was a flat byte scan for
`&` with no lexical state, so it expanded references in the three regions XML
defines as *not* recognising one. That was wrong twice over: it expanded a
reference the document meant literally, and it let replacement text close the
region and open a new one.

```xml
<!DOCTYPE r [<!ENTITY e "]]><evil/><![CDATA[">]><r><![CDATA[&e;]]></r>
```

produced a real `<evil/>` element. Entity text became document structure,
silently, and it moved validation verdicts in both directions — a document
valid per spec was rejected, and structure could be smuggled past a validator
whose downstream consumer parses CDATA correctly. The trigger was cheap: any
one entity containing `<` switched the whole document onto that path.

Fixed by giving the scanner the three regions to skip. Verified against
libxml2, which agrees the reference stays literal.

### Replacement text was decoded twice

**Medium.** Expansion decoded `&amp;` because `dec.Entity` substitutes without
re-scanning — but on the rewrite path the text *is* scanned again, so
`&amp;lt;evil/&amp;gt;` became `<evil/>`, manufacturing markup from data the
document had escaped. The same entity gave different results depending on
whether an unrelated entity happened to contain `<`.

A character reference is the opposite case and is still decoded on both paths:
it may form part of a *name*, and a name is not a place a reference survives to
be decoded later.

### Unused entity declarations consumed the expansion budget

**Low.** Testing whether any entity held markup resolved every declaration,
charging unused ones against the shared cap — so a subset full of large unused
entities made a legitimate reference fail with an error about something else.
It also made the result depend on map iteration order, so the same document
parsed differently from run to run. The check now reads the raw text and
resolves nothing.

### RELAX NG validation was quadratic in depth with no bound of its own

**Medium.** Each level of nesting carries the pattern remaining at every level
above it, so cost grows with the square of the depth: 8000 levels cost 487ms
and 911MB, and doubling the depth quadrupled both. `xdm`'s `MaxDepth` capped it
by accident. `relaxng.ValidateOptions.MaxDepth` now bounds it deliberately,
matching what `xsd` already had — a caller who raises the parser's limit, or
builds a tree by transform rather than parsing, has not agreed to spend a
gigabyte validating it.

### Checked and clean

XXE stays closed through the new path — file, HTTP, `PUBLIC`, parameter
entities and external entities reached indirectly through an internal markup
entity were all refused, with a canary HTTP server recording zero hits. Entity
bombs remain bounded through the rewrite, including the many-small-references
case. `MaxBytes`, `MaxNodes` and `MaxDepth` are re-applied to the expanded
text, which was the suspected bypass and is not one. The re-parse cannot
recurse. RELAX NG `Compile` with no resolver refuses every `href`.

---

## Fixed in the first audit

Nine issues, each with a regression test that was verified to fail without its
fix.

### Only the five predefined entities expand

`xdm/parse.go` set the decoder's entity map to `xml.HTMLEntity`, which defines
252 HTML entities, so `&nbsp;` and `&copy;` expanded in a document declaring no
DTD at all. A conforming XML parser must reject an undeclared entity.

Not an injection vector — the map is a fixed table and a numeric reference that
decodes to `&` does not start a second round of expansion (verified: `&#38;#60;`
yields the literal `&#60;`). The risk was divergence: this validator accepted
documents the next consumer in the chain would reject.

### `AllowHost` is checked on every redirect hop

`HTTPResolver.AllowHost` ran once, before the first request, and the default
`http.Client` follows redirects. A schema on a permitted host answering `302`
had the redirect followed and the body returned — the SSRF the field exists to
prevent, reachable through any open redirector on an allowed host.

The returned path was the *original* URL too, so a caller logging it never
learned where the bytes actually came from.

```
named=http://127.0.0.1:60317 (ALLOWED)
redirected to=http://localhost:60315/secret.xsd (DENIED by policy)
result err=<nil> data="INTERNAL-SECRET-SCHEMA"
AllowHost was consulted for: [127.0.0.1]
```

Now checked per hop via `CheckRedirect`, installed on a copy of the client so a
caller's own client is not mutated, and `Resolve` returns the document's real
origin.

### `FileResolver.Root` refuses symlinks

`Root` refused `..`, absolute paths and `file:` URLs, but not symlinks: a link
planted inside the root passed the containment check and `os.Open` then followed
it out. The doc comment already claimed symlinks were refused, and `xslt`'s
resolver had always done it correctly.

Needs an attacker-planted symlink in the schema directory, so it is a
defence-in-depth failure rather than a remote hole.

### Computed names are validated

A name computed by `xsl:element`, `xsl:attribute` or
`xsl:processing-instruction` was checked only for a non-empty local part, and is
written to the output verbatim:

```
name="a><evil/><x"  ->  <r><a><evil/><x>t</a><evil/><x></r>
name="a b"          ->  <r><a b>t</a b></r>
name="1abc"         ->  <r><1abc>t</1abc></r>
```

The processing-instruction target was not checked at all, and its worst case is
quieter than malformed output — a target of `a?><evil/><?b` closed the
instruction, opened an element, and the result **reparsed cleanly as a
different tree**, which nothing downstream would notice.

Both halves of a computed QName must now be NCNames, and a PI target may not be
the reserved name `xml`.

Reachability: needs a stylesheet that computes a name from document data. That
is a normal stylesheet pattern, but it is not the default path.

### Raw text may not end its element

The HTML output method writes `<script>` and `<style>` content unescaped, which
is correct — escaping `&` and `>` there would corrupt a JavaScript comparison.
The rule the spec pairs with that one was missing: content containing `</` ends
the element early and everything after it is markup.

```
<script>var u = "</script><img src=x onerror=alert(1)>";</script>
```

That is the standard XSS primitive, and it cannot be escaped away, so the spec
makes it a serialization error — as `--` in a comment and `?>` in a PI already
were here. Ordinary JavaScript containing `<`, `>` and `&&` still passes through
unescaped, which is the point of the raw-text rule.

### A nil document is an error, not a panic

`Schema.Validate`, `ParseSchema` and `xslt.Compile` dereferenced their document
argument immediately. A caller's mistake rather than an attack — but in a
server, a nil arriving from a failed parse upstream takes down every other
request in the process, not just the one that caused it.

### Input size and node count are bounded

`xdm.ParseOptions` had `MaxDepth` but no byte or node cap, and a node costs a
fixed ~200 bytes whatever it holds — so the heap a document needs follows its
node count, not its length:

| document | input | heap | amplification |
|---|---|---|---|
| `<a/>` repeated | 0.8 MB | 40.7 MB | **53.3x** |
| invoice-like | 11.9 MB | 284.7 MB | 23.9x |
| text-heavy | 39.5 MB | 74.2 MB | 1.9x |

`MaxBytes` (default 64 MB) bounds the read; `MaxNodes` (default 10 million,
about 2 GB of tree) bounds what the read can allocate. Neither alone is a memory
bound, which is why there are two: across a 1.9x–53x spread, a byte cap says
little about the heap. Attributes and namespaces count, because a document of
few elements carrying many attributes allocates most of its memory in those.

The byte limit wraps the reader rather than trusting the caller to check, and
reads one byte past the cap so hitting it is distinguishable from a document of
exactly the maximum size. A negative value disables either check.

**Micro-optimisation was tried first and did not work.** Slab-allocating nodes
cut allocation *count* 13% but raised total bytes, because slabs over-allocate;
interning names gave nothing, because Go already shares the decoder's string
storage. The `Node` struct is 39.7 MB of the 41.6 MB a 200,000-element document
costs, so the limits are the defence. `Node` did lose 8 bytes — `order` narrowed
to `int32` and moved beside `offset` so the two share a word — for about 3%.

### Validation depth is bounded separately from parsing

The XSD validator recurses once per element depth at roughly 3 kB of stack a
level. Exceeding Go's stack limit is `fatal error: stack overflow`, which
**`recover()` cannot catch** — it kills the process, not the request.

`ValidateOptions.MaxDepth` (default 1000) makes that an ordinary validation
error. It is deliberately a separate knob from the parser's: a caller who raises
`xdm.ParseOptions.MaxDepth` to accept a legitimately deep document has not
thereby agreed to arm a crash.

The reported error path is elided in the middle as well — a failure at depth
50,000 produced fifty thousand `/r` segments, which is unreadable and costs more
memory than the error it decorates.

### The transform bound no longer refuses legal documents

XSLT recursion was capped at a fixed 300, below the parser's 1000 — and that
bound counts the ordinary descent of an identity transform, not only a template
calling itself. So a legal 500-deep document could be parsed and then not
transformed.

`TransformOptions.MaxDepth` now defaults to 1000, matching the parser. A
stylesheet with no base case is still caught.

---

## Open findings

### INFO — `javascript:` URLs pass through

`<a href="{/d/u}"/>` yields `href="javascript:alert(document.domain)"`. This is
spec-conformant — XSLT does not sanitise URLs, and the value *is* correctly
`&`-escaped. **If you render transform output as HTML, sanitise URL-valued
attributes yourself.**

### Withdrawn — a CR in a text node *does* survive a round trip

Recorded here as an open defect on the reading that `escapeText` handles `&`,
`<` and `>` but not `\r`. That missed the branch above the switch: a CR,
U+2028 and the whole C1 range are written as numeric references before the
named escapes are reached, which is what `K2-Serialization-5`, `-10` and `-11`
assert, with `-6` covering the attribute case. Measured across a bare CR, a CR
between characters, a doubled CR, a CRLF pair and an attribute value: every one
round-trips byte-identical. Pinned by `xslt/cr_roundtrip_test.go`.

The XSLT `html` method is deliberately excluded from that branch, and correctly
— HTML has no line-ending normalisation, so a CR there is an ordinary
character.

---

## Verified safe

Each of these was demonstrated by execution, not inferred from reading the code.

### XXE is absent, even with `AllowDOCTYPE: true`

This is the important result, and it is stronger than the code comments claimed.
`encoding/xml` never parses the DTD internal subset — it hands the whole
DOCTYPE over as one opaque `Directive` token. No DTD-declared entity ever
exists, so every reference to one is a hard syntax error.

Tested across external general entities (`file://`, bare paths, `/etc/passwd`,
`http://`), external parameter entities, PUBLIC identifiers, external DTD
subsets, entities in attribute values, and NDATA/NOTATION: **zero file reads,
zero network requests**, with a canary HTTP server recording `hits=0`. Even
*internal* entities fail.

This matters because real callers must set `AllowDOCTYPE: true` — UBL depends on
the W3C XML Signature schema, which carries a DOCTYPE. **That escape hatch does
not reopen XXE.**

### A content model cannot make the matcher allocate without a ceiling

Deciding whether an element's children match a content model needs the *set* of
readings the children admit, because nested occurrence bounds cannot be settled
one reading at a time — see *Nested occurrence bounds were wrong in both
directions* in [known-gaps.md](known-gaps.md). A set is a thing a schema could
try to grow, and a schema is untrusted input: a `.xsd` arriving over the wire is
as hostile as a `.xml`.

Two things bound it. Each occurrence maximum is narrowed per document to what
that document can actually reach, so a scope cannot contribute distinguishable
readings it has no children to fill — `maxOccurs="100000000"` against a thousand
children behaves as `unbounded` does, and readings past the minimum merge
instead of multiplying. That alone holds both W3C suites, UBL 2.1 and the
DocBook corpus to single-digit set sizes. Above it sits `DefaultMaxMatchStates`,
a hard ceiling of 4,096 readings per element, and crossing it fails the element
with an error naming the limit rather than continuing to allocate.

The limit refuses rather than approximates, deliberately. A matcher that fell
back to a heuristic on a large set would be least exact precisely on the inputs
constructed to make it so, which is a validator that can be talked out of
validating.

### Billion laughs is impossible

Same cause. A 9-level, fan-10 entity bomb fails in 10 µs with `invalid character
entity &e9;` — the expansion is never attempted, with or without
`AllowDOCTYPE`.

### Regular expressions cannot backtrack catastrophically

Go's `regexp` is RE2. `matches('aaaa…!', '^(a+)+$')` is flat at 2–6 µs from
n=24 to n=40. Go also *rejects* repeat counts over 1000, so `{1,1000000}` cannot
be used to force an expansion; the XSD pattern translator allocates nothing on
nested quantifiers or 200-deep groups.

**Backreferences do not change this, by default.** XPath 2.0 has them and RE2
does not, and the usual way to bridge that is a backtracking engine — which is
exactly the denial-of-service vector RE2 exists to remove. The default engine
does not use one. A backreference is resolved only when every group it names,
*and the text between the group and the reference*, has a fixed width, where
RE2's single submatch assignment is the only assignment and one comparison
decides the answer; the whole match stays linear in the input. Measured on
`([a-z])\1*`: 4,000 characters in 53 µs, 64,000 in 567 µs.

Outside that subset — `(a*)\1` — the default refuses with `FORX0002` rather
than answering, because deciding it needs alternatives RE2 cannot enumerate. An
engine that answers correctly or says it cannot is safe to expose to untrusted
patterns; one that guesses is not safe at any setting.

**A backtracking matcher is available, and is off by default.**
`xpath.SetBacktrackingRegex(true)`, or `-backtracking-regex` on the command
line, decides the general case. Leave it off for untrusted input. The reason is
the one above: `fn:matches` takes its pattern from the stylesheet, a stylesheet
may be caller-supplied, and `matches($s, $node/@pattern)` takes one from
*document data* — so enabling it globally lets a document being validated
choose how long the validation takes.

Even enabled it is bounded. Every match attempt is counted against a step
budget, and exhausting the budget raises `FORX0002` rather than returning a
silent "no match" — a budget that guessed would do it precisely on the inputs
where the answer was hardest to get. Measured from both ends: the hardest
honest pattern in either conformance suite answers in 525 steps, while
`(a*)*\1b` against sixty `a`s exhausts the whole budget in about 200 ms. So the
worst case is a fifth of a second of wasted work, not a hang — but it is still
work an attacker can ask for, which is why the default stands.

### Internal entities expand; external ones never do

`AllowDOCTYPE` now also enables **internal general entities** — the
`<!ENTITY name "text">` form declared in a document's own subset. Some schemas
need them: the W3C's RFC 3986 type library composes its URI regexes out of
fifty entities named after the grammar's productions, and without expansion the
document cannot be parsed at all.

The line that does not move is **external** entities. One declared `SYSTEM` or
`PUBLIC` names something outside the document, and fetching it is XXE. Those
are recorded as refused rather than resolved, so a reference to one is an error
and never a fetch — including when reached indirectly through an internal
entity. Parameter entities are not read either.

Expansion is bounded three ways, because nesting is exactly how billion-laughs
works:

| bound | value | why |
|---|---|---|
| depth | 100 levels | past anything hand-written, far short of a bomb |
| one entity | 64 KB | the largest legitimate expansion measured is 9,569 bytes |
| all entities | 1 MB | a bomb split across many entities cannot add up |

A cycle — direct or mutual — is detected and refused rather than recursed.

The per-entity figure is measured rather than chosen: a first attempt used
1 MB, and a five-level billion-laughs reaching 100,000 bytes parsed cleanly
through it. The regression test that caught that is `TestEntityExpansionBlowupIsRefused`.

### All resolution defaults are closed

`doc()`, `document()`, `collection()`, `xsl:include` and `xsl:import` all refuse
when no resolver is configured. `collection()` has its own switch —
`Collections`, separate from `Documents` — so enabling `fn:doc` for a known
code list does not also let a stylesheet enumerate whatever a collection URI
names; a resolver that accepts one should validate the URI it is handed, which
arrives from the stylesheet. `unparsed-text()` has its own switch too —
`FileResolver.UnparsedText`, off by default and implied by nothing else. It is
separate because it is the widest of them: `ResolveDocument` hands back a
parsed XML document, so a file that is not well-formed XML discloses nothing,
while `unparsed-text` hands back the raw bytes of any file inside `Roots`. A
root holding one XML data file and one private key leaks the key. An earlier
revision of this document said the function was disabled *unconditionally*;
that was true when written and is no longer, and the distinction matters to
anyone deciding what a root may contain. `xsl:result-document` never writes to
disk; the engine returns secondary results to the caller as data. XInclude is
off unless a caller runs `xdm.ProcessXInclude` and names the roots it may read
— see *XInclude: a new reader on the old gate* above.

`AllowHost` resists spoofing: it uses `u.Hostname()`, so userinfo tricks
(`http://good.example@127.0.0.1/`) and ports do not fool it, and it is
re-checked after base-URI resolution.

### Escaping and serialisation

- Text and attribute escaping is correct in the xml, html and text methods.
- **`disable-output-escaping` is ignored** — the most common XSLT XSS primitive
  is simply absent.
- Comment breakout (`--`) and PI *content* breakout (`?>`) are both errors.
- `]]>` in text is escaped and reparses identically.
- NUL and control characters are rejected at parse and never reach the
  serialiser; a lone surrogate becomes U+FFFD.
- Namespace round-tripping is stable: prefix rebinding, default-namespace
  undeclaration, two prefixes for one URI and attribute-prefix shadowing all
  reserialise to identical expanded-name trees.

### No XPath expression injection

Every `xpath.Compile` call site takes stylesheet or schema source. Attribute
value templates compile at compile time from stylesheet text; document data only
ever supplies *values*. There is no `evaluate()`-style extension, so a document
cannot influence which expression is compiled.

### `xsi:type` is not a type-confusion vector

All five attacks were rejected: an unrelated type, a sibling type, an undeclared
prefix, and both directions of facet escape. Prefix rebinding resolves by URI,
and facets still apply to the substituted type.

### Concurrency and retention

- No `go func` anywhere in non-test code; goroutine count is stable.
- 20,000 parse-and-validate cycles and 2,000 distinct schema loads each show
  **0.00 MB** heap growth after GC.
- The `xpath` regex cache is bounded at 1024; the `xsd` model cache is keyed by
  complex type, which is schema-controlled rather than attacker-controlled.
- A compiled `Schema` and `Stylesheet` are safe for concurrent use, verified
  under `-race`.

### No unsafe code

No `unsafe`, no `cgo`, no `reflect` in any non-test file.

---

## What a caller must do

1. **Consider the defaults deliberately.** `MaxBytes` (64 MB), `MaxNodes` (10
   million), `MaxDepth` (1000, separately in `xdm`, `xsd`, `relaxng` and
   `xslt`) are set for a general-purpose service. If you know your documents
   are smaller, lower them: they are the bound on what one request can cost
   you.
2. **Leave `AllowDOCTYPE` off** unless a schema you control needs it. Turning it
   on does not reopen XXE, but it is still the wider setting.
3. **Sanitise URLs** if you serve transform output as HTML. XSLT does not, and
   is not supposed to.
4. **Set a `Root`** on `FileResolver`, and an `AllowHost` on `HTTPResolver`, if
   either resolves locations an attacker can influence. A `relaxng.Resolver` is
   your own code and has no such field: it receives the href with `..` intact
   and the scheme filled in, so it must do its own containment check. See the
   interface's documentation for measured examples.
5. **Set a timeout** on the request, and pass the context in. The
   identity-constraint finding above is CPU exhaustion; the depth limit caps it,
   but a `context` deadline is what bounds the general case. Use
   `xsd.Schema.ValidateContext` rather than `Validate`, and
   `xslt.Stylesheet.Transform`, which already takes one — a deadline the
   library never looks at bounds nothing.
6. **Raise `MaxDepth` only deliberately.** Past a few hundred thousand levels
   the XSD validator trades a clean error for an uncatchable stack overflow,
   and raising it also removes the ceiling on the identity-constraint cost. In
   `relaxng` the cost of depth is *quadratic*, so raising it there is the most
   expensive of the four.

## Re-running the audit

The probes are not checked in — they are written against a specific version and
would rot. The method that found these: build a document or stylesheet that
*tries* the attack, run it, and read the actual output rather than the code.
Every finding above was reproduced that way, including two that turned out to be
wrong on first framing.
