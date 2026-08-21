# TODO

Everything here is measured, not guessed. Each item says what it costs and what
it buys, because several of them buy less than they look like they do.

Current position:

| | |
|---|---|
| XSD 1.0 | 99.51% — 24,863 of 24,986 |
| XSD 1.1 | 100% — 1,083 of 1,083 |
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

### 1.4 Particle Valid (Restriction)

Deliberately unimplemented. A schema whose restriction is unsound in this
specific way is accepted rather than reported. The company this keeps is
reasonable — libxml2 omits it entirely, Xerces leaves it off by default — and
it affects schema authors, not document validators.

Cost: moderate and fiddly. Buys: nothing measurable on the suite, since these
schemas are marked invalid-by-design and skipped either way.

---

## 2. Bugs

### 2.1 XSD 1.0: 123 disagreements

No cluster above three cases, so this is individual spec-reading rather than
one fix. Split by direction, because they are different kinds of work:

**83 false accepts** — a document the suite calls invalid is accepted. These
are missing checks, and each is a constraint not being applied.

**40 false rejects** — a valid document is refused. These have diagnosable
error codes:

| cases | code | area |
|---:|---|---|
| 12 | `cvc-complex-type.2` | content models |
| 8 | `cvc-identity-constraint.4` | key/keyref/unique field selection |
| 8 | `cvc-datatype-valid.1` | simple type values |
| 2 | `cvc-id.2` | ID uniqueness |
| 2 | `cvc-elt.5.2.2.2.1` | element default vs content |
| 2 | `cvc-attribute.3`, `cvc-attribute.4` | attribute values and fixed |
| ~4 | one each | the tail |

Start with the false rejects: an error message naming a code and a path is a
much shorter path to the cause than "this should have failed and did not".

**At least one is a suite defect, not a bug here.** `anyURI_a004` is marked
`status="queried"` against an open W3C bug, and its own group annotation
contradicts the expectation recorded for it. Expect a few more like it — check
the `.testSet` metadata before assuming a disagreement is ours.

### 2.2 XPath: 28 in-scope failures

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

### 2.3 Remaining load failures: 20

Most are correct behaviour rather than bugs — nine XML 1.1 documents (item
1.1), five using 1.1 constructs under 1.0 and *meant* to fail, two needing a
DOCTYPE that is refused by default, several naming deliberately absent files.

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

---

## 4. Deliberate non-goals

Recorded so they are not proposed again as oversights:

* **Regex backreferences** — leaving RE2 costs the linear-time guarantee.
* **`xsi:schemaLocation` in instances** — honouring it lets the document
  choose its own schema.
* **Network resolution by default** — hands control of what this process
  fetches to whoever wrote the schema.
* **DOCTYPE by default** — the entry point for XXE and entity expansion.
