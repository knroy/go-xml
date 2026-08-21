# TODO

Everything here is measured, not guessed. Each item says what it costs and what
it buys, because several of them buy less than they look like they do.

Current position:

| | schema-validity | instance |
|---|---|---|
| XSD 1.0 | 14,204 / 14,405 (98.60%) | 24,953 / 25,002 (99.80%) |
| XSD 1.1 | 15,045 / 15,365 (97.92%) | 26,155 / 26,208 (99.79%) |
| XPath 2.0 | 99.81% — 14,692 of 14,720 in scope (28 failing) |
| Schemas that fail to load | 20, most of them correctly |
| Tests | 526, clean under `-race` |

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

### 1.2 DTD validation

Today a `DOCTYPE` is parsed past rather than applied — see
[validation.md](validation.md#dtd-and-relax-ng). Making it real means a DTD
syntax parser (`<!ELEMENT>`, `<!ATTLIST>`, `<!ENTITY>`, parameter entities)
and entity expansion.

The content models are the easy half: DTD's `(a, b*, (c|d)?)` is a strict
subset of what the Glushkov automaton in `xsd/automaton.go` already compiles,
and ID/IDREF and attribute defaulting are already there for XSD.

The hard half is that **entity expansion is the attack surface `AllowDOCTYPE`
exists to refuse**. Billion-laughs and XXE both enter here, so this has to be
built with expansion limits and no external fetches by default — the security
posture is the design, not a footnote on it.

Cost: ~1,500–2,500 lines plus a corpus. Buys: a format still common in the
wild, with real reuse of what is here.

### 1.3 RELAX NG

A different validation model, not another DTD: derivatives over patterns
rather than a finite automaton, so it is a separate engine rather than a use
of the existing one. `interleave` is where implementations get slow or wrong.
Two syntaxes to parse — XML and compact — though the datatype library can
delegate to the XSD types already here.

Cost: ~4,000–6,000 lines, and it needs James Clark's suite to be trustworthy.
Buys: breadth. Recommend only after 1.1 and 1.2.

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

What is left, in rough order of size:

| area | schema false-accepts (1.0) |
|---|---:|
| attribute declarations and attribute groups | ~106 |
| wildcards, element declarations, model group definitions | ~215 |
| identity constraints | ~38 |
| notations | ~21 |
| schema-level and assorted | ~90 |

---

## 2. Bugs

### 2.1 XSD 1.0: 786 disagreements, 27 of them disputed

Split by direction, because they are different kinds of work:

**~700 schema false accepts** — an invalid schema loads without complaint.
These are Schema Component Constraints not yet applied; see 1.4 for where they
sit.

**~25 schema false rejects and ~15 instance false rejects** — valid input we
refuse. **Work these first.** A false reject breaks a caller outright, while a
false accept only fails to catch someone else's mistake; the two are not
symmetric, and a single percentage treats them as though they were. An error
naming a code and a path is also a far shorter route to a cause.

**27 are disputed** — `status="queried"` against an open W3C bug. Nineteen of
those are one cause: bug 4113, the regex general-category tests, written
against Unicode 3.1 before characters moved between categories. Passing them
would mean freezing a Unicode 3.1 table and being wrong about modern text.
Check the metadata before assuming a disagreement is ours — and note the status
is on the `<current>` element, not on `<expected>`.

### 2.2 QName values do not resolve their prefix

An `xs:QName` value is checked lexically: prefix and local name must be
NCNames, and `xmlns` is rejected as a prefix because nothing can bind it. But a
prefix that is simply *undeclared* is accepted, because the lexical check has
no access to the element's in-scope namespaces.

Fixing it means threading the instance element's namespace context through
`validateSimpleValueVersion` and its ten call sites. Worth doing for
correctness — a QName whose prefix does not resolve has no value — but it buys
one test on the suite, so it has not been done for the number.

### 2.3 XPath: 28 in-scope failures

Mostly not fixable, and worth stating why so nobody re-litigates it:

| cases | cause | fixable |
|---:|---|---|
| 12 | regex backreferences (`\1`) | **no** — RE2 has none by design |
| 7 | `fn:collection` | no — the harness configures no collection |
| 5 | `xs:dateTimeStamp` | no — an XSLT 3.0 type this engine does not claim |
| 4 | ordinary bugs, one per set | yes |

Only those 4 are work. The backreference 12 are the one genuine architectural
ceiling in the project: supporting them means leaving RE2, and RE2's linear
time guarantee is worth more than twelve tests.

### 2.4 Remaining load failures

Most are correct behaviour rather than bugs — nine XML 1.1 documents (item
1.1), two needing a DOCTYPE that is refused by default, several naming
deliberately absent files. The chameleon-include and huge-occurrence cases that
used to sit here have been fixed.

The genuinely open one: `common/xsts.xsd`, the suite's own harness file, needs
a remote XLink schema. Not a bug — network resolution is off by default — but
it could be resolved by shipping a `MapResolver` for the well-known W3C
namespaces.

---

## 3. Verification gaps

These are places where the tests are thinner than the claims.

### 3.1 Fuzzing beyond the parser

There is one fuzz target in the tree, `FuzzCompileNoPanic` over the XPath
expression compiler. Neither the XML parser, the schema assembler, nor the
content-model compiler has one, and all three consume adversarial input — a
`.xsd` is as untrusted as a `.xml` when it arrives over the wire.

### 3.2 Deep-nesting and pathological schemas

Limits exist for documents (`MaxDocuments`, `MaxErrors`, depth). Less certain:
a content model with deeply nested counters, a substitution group closure over
thousands of declarations, a union of unions. Worth a benchmark that fails
loudly rather than an assumption.

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

* **Regex backreferences** — leaving RE2 costs the linear-time guarantee.
* **`xsi:schemaLocation` in instances, by default** — honouring it lets the
  document choose its own schema. Available opt-in behind a namespace
  allowlist; see `Schema.WithInstanceLocations`.
* **Network resolution by default** — hands control of what this process
  fetches to whoever wrote the schema.
* **DOCTYPE by default** — the entry point for XXE and entity expansion.
