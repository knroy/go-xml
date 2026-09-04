# TODO

Everything here is measured, not guessed. Each item says what it costs and what
it buys, because several of them buy less than they look like they do.

Current position:

| | schema-validity | instance |
|---|---|---|
| XSD 1.0 | 14,380 / 14,393 (99.91%) | 24,967 / 24,995 (99.89%) |
| XSD 1.1 | 15,343 / 15,354 (99.93%) | 26,189 / 26,204 (99.90%) |
| XPath 2.0 | 100.00% — 15,183 of 15,183 in scope |
| XPath 3.0 | 100.00% — 19,244 of 19,244 in scope |
| XPath 3.1 | 100.00% — 21,786 of 21,786 in scope (0 failing) |
| XQuery 3.1 | 99.99% — 29,800 of 29,803 in scope (3 failing) |
| XSLT 2.0 | 99.87% — 6,149 of 6,157 in scope (8 failing) |
| XSLT 3.0 | 99.85% — 8,612 of 8,625 in scope (13 failing, one deliberate); streaming out of scope, though 92% of those cases pass anyway |
| RELAX NG | 100.00% — 965 of 965 |
| Schemas that fail to load | 19, most of them correctly |
| Tests | 1,149, clean under `-race` |

Every one of those failures, and why it is still open, is catalogued in
[known-gaps.md](known-gaps.md). This file is the forward-looking half — what
to build next and what it would cost.

---

## 1. Features

### 1.1 XML 1.1 documents — **the largest single win**

`encoding/xml` refuses `version="1.1"`, so nine schemas do not parse and 38
instance tests never run. This is the only remaining item that unlocks a whole
block of the suite at once.

It is not a version-string rewrite. XML 1.1 changes what the *language* is:

* new name characters (the Dutch ligature ij, and much of the range XML 1.0
  1st edition excluded);
* C0 control characters permitted in content and attribute values;
* `NEL` (U+0085) and U+2028 as line terminators;
* `\i` and `\c` in a regex mean different sets under 1.1, so the pattern
  translator becomes version-dependent — which reaches `xpath`, not just
  `xsd`.

Cost: substantial. Buys: 38 tests and correctness for documents that really
are 1.1.

### 1.2 DTD validation — done, internal subset only

**Implemented.** The `dtd` package parses `<!ELEMENT>`, `<!ATTLIST>` and
`<!ENTITY>` and validates against them: content models, attribute defaults,
enumerations, and `ID`/`IDREF`. Content models reuse nothing from
`xsd/automaton.go` in the end — DTD's are simple enough to decide directly.

The security posture was the design rather than a footnote on it, as this
entry argued it had to be: **entity expansion is the attack surface
`AllowDOCTYPE` exists to refuse**, so expansion is bounded by count and by
total expanded size, and nothing external is fetched without a resolver the
caller supplies. Both are exercised — a billion-laughs bomb is refused in
microseconds.

What is *not* implemented is the external subset, and parameter entities only
work within the internal one. A document whose DTD lives in a separate file
validates against whatever it declares inline and no more.

### 1.3 RELAX NG — done, XML syntax only

**Implemented**, at 100% of James Clark's suite (965 of 965 assertions).
It is a separate engine, as expected: derivatives over patterns rather than a
finite automaton, because `interleave` is not a Glushkov construction. The
datatype library delegates to the XSD types, which was the reuse the earlier
estimate hoped for.

The cost estimate was roughly right for the engine and badly wrong about where
the work would go. Over half the conformance suite is schemas that must be
*rejected*, so most of the effort was the restriction rules of section 7 and
the scattered constraints of sections 4.16–4.19 — not the derivative
algorithm, which is short.

**What is left:**

* **The compact syntax** is not implemented. It is a second parser over the
  same model, so it costs a parser and nothing else — the compiler, the
  restrictions and the validator are all reached through the same tree.
* Nothing in the suite. The last failure needed markup inside an entity's
  replacement text, which was a gap in `xdm` and is fixed.

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

What is left: **nothing measurable.** Schema-validity agreement is now 99.86%
on 1.0 and 99.88% on 1.1, and none of the remaining disagreements is a fixable
defect — see [conformance-gaps.md](conformance-gaps.md). The area table that
stood here counted roughly 470 false accepts across attribute declarations,
wildcards, identity constraints and notations; all of it has since landed.

---

### 1.5 XQuery module import — the last structural gap in `xquery`

`import module` raises `XQST0059` and `import schema` leaves the in-scope
schema definitions empty, so `validate { … }` raises `XQDY0084`. Both parse
correctly and are then refused; neither is mis-parsed.

**What it buys.** Beyond the XQuery cases themselves, it is the one thing
blocking `fn:load-xquery-module`, which is why three XPath 3.1 cases are out
of scope rather than passing — see
[reaching-100.md](reaching-100.md).

**What it costs.** A module store and a resolver, plus the cycle detection
that `import` needs. It is the same shape of problem as `xsd`'s schema
assembly, and the resolver must default to nil like every other one here, or
a query gains the ability to fetch.

## 2. Bugs

### 2.1 XSD: 41 disagreements on 1.0, 38 on 1.1 — none of them a defect

Both directions are closed. The ~700 schema false accepts this entry used to
count are implemented, and the false rejects with them; schema-validity
agreement is 99.91% on 1.0 and 99.93% on 1.1.

What is left is not work: the bulk of the 79 are cases where the suite's own
`status` records that the W3C challenged the expected result, 44 of those (22
per version) being the single open bug 4113 — the regex general-category tests
written against Unicode 3.1, where passing means freezing a Unicode 3.1 table
and being wrong about modern text. Check the metadata before assuming a
disagreement is ours, and note the status is on the `<current>` element, not
on `<expected>`.

The counts here fell from 51 and 47 when two harness defects were fixed:
`indeterminate` expectations stopped being scored as "must be invalid", and
`iri-001`'s schema, which builds its RFC 3986 patterns from an internal DTD
subset, stopped being loaded with `AllowDOCTYPE` off.

The principle this entry argued for still holds and is worth keeping: **a
false reject breaks a caller outright, a false accept only fails to catch
someone else's mistake.** They are not symmetric, and a single percentage
treats them as though they were. That is why the tables above split them.

It also holds that a suite at its ceiling is not proof of exactness — see 3.1.

### 2.2 XSLT: a union's selected member was lost on every tree copy — fixed

`<xsl:template match="Date[data(.) instance of StandardDate]">` never matched,
where `StandardDate` is a simple type named by an `xsl:import-schema`. The
plain `match="Date"` won instead and copied the source text through.

Found behind `validation-0201`, whose row in
[conformance-gaps.md](conformance-gaps.md) had been filed as a harness fix.
Normalising the serializer difference that row described was implemented and
measured, and the case still failed; this is what sat behind it.

The two candidate causes this entry named — the annotation being absent, or the
type name not resolving in the pattern's static context — were **both wrong**,
and measurement was what settled it. A probe over the validated tree showed
every `Date` carrying `TypeAnnotation="DateType"` and `UnionMember="StandardDate"`,
and a trace at the `instance of` match site showed the type resolving correctly.

The real cause is one line further out. `Date` has type `DateType`, a complex
type with simple content extending a *union*; XSD §3.14.4 selects a union's
member per value, so the winning member is recorded separately on the node and
is what atomisation reads — a union's own derivation chain runs to
`xs:anySimpleType` and stops. Three copy sites carried `TypeAnnotation` and
dropped `UnionMember` beside it: `stripCopyNode` in `xslt/transform.go`,
`xdmbuild.DeepCopy`, and the parentless attribute copy in `xslt/copyfuncs.go`.
The stylesheet declares `<xsl:strip-space elements="*"/>`, so every `Date`
reaching a template had been through a copy and atomised to `xs:untypedAtomic`.

Fixed by carrying `UnionMember` at all three, which is what `xdm/xinclude.go`
already did. The output for `validation-0201` is now byte-identical to the
expected file apart from whitespace; the case still fails on the indent width,
which is implementation-defined, so it gains no suite case. Covered by
`xslt/unionmember_test.go`.

### 2.3 QName values do not resolve their prefix

An `xs:QName` value is checked lexically: prefix and local name must be
NCNames, and `xmlns` is rejected as a prefix because nothing can bind it. But a
prefix that is simply *undeclared* is accepted, because the lexical check has
no access to the element's in-scope namespaces.

Fixing it means threading the instance element's namespace context through
`validateSimpleValueVersion` and its ten call sites. Worth doing for
correctness — a QName whose prefix does not resolve has no value — but it buys
one test on the suite, so it has not been done for the number.

### 2.4 XPath: `$e-1` names a variable — retracted, not a defect

Recorded here as the one known defect the suite does not cover. It is not a
defect: the QT3 suite writes `$tz-10` and `$in-xml-1` itself and uses them as
single variables, and `prod/NameTest.xml`'s `K-NameTest-3` (`foo- foo`,
expecting `XPST0003`) states that a name takes a trailing hyphen even before
whitespace. A fix that made `$e- 1` subtraction broke that case in all four
suites. See [known-gaps.md](known-gaps.md); pinned by `xpath/hyphen_test.go`.

### 2.5 XPath: no in-scope failures

XPath 2.0, 3.0 and 3.1 are all at 100%. The last case to fall was
`fn-matches-51`, the one shape this deliberately refuses by default, and it is
worth stating why so nobody re-litigates it:

Backreferences are now resolved where doing so is exact. RE2 has none, but it
returns capture positions, and a backreference is only hard when the group it
names can match more than one width: RE2 gives a single submatch assignment —
the greedy one — so for `(a*)\1` against `"aa"` it reports the group as `"aa"`,
leaving nothing for the backreference, and a comparison answers **false** where
the truth is *true* (`"a"` + `"a"`).

When every named group has a *fixed* width there is nothing to enumerate, the
greedy assignment is the only assignment, and capture-and-compare is exact. It
runs in RE2's linear time — measured, 64,000 characters in 567 µs — so **the
default path adds no backtracking engine and the linear-time guarantee is
intact**.

The split is by what can be decided, not by what a caller asked for: an engine
that answers correctly or says it cannot is safe on always; one that guesses is
not safe at any setting. Outside the decidable subset the default raises
`FORX0002`.

The general case *is* implemented, behind `xpath.SetBacktrackingRegex(true)`
(`-backtracking-regex` on the command line) and off by default, because it has
no linear-time guarantee and patterns can come from document data. A step
budget bounds every match and exhausting it is an error, never a silent "no
match". Both XSLT harnesses and the QT3 harness now enable it for their runs, where
the suite's patterns are trusted input, so the nine XSLT
`regex`/`analyze-string` failures and `fn-matches-51` all pass and the measured
figures include them. It stays off by default in production.

An earlier version of this file argued the whole thing was not worth doing,
having reasoned about capture-and-compare in general and missed that the
fixed-width case is not a guess. See [known-gaps.md](known-gaps.md).

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
  comparison is exact, and it is on always. See 2.5.
* **`xsi:schemaLocation` in instances, by default** — honouring it lets the
  document choose its own schema. Available opt-in behind a namespace
  allowlist; see `Schema.WithInstanceLocations`.
* **Network resolution by default** — hands control of what this process
  fetches to whoever wrote the schema.
* **DOCTYPE by default** — the entry point for XXE and entity expansion.
