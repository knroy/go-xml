# Changelog

Notable changes, newest first. Versions follow [semantic
versioning](https://semver.org), with the caveat that a `0.x` release may
break the API — see *Stability* below.

## Unreleased

### Backtracking regular expressions, off by default

`xpath.SetBacktrackingRegex(true)`, or `-backtracking-regex` on the command
line, enables a matcher for the backreferences RE2 cannot express:
variable-width groups, backreferences in the middle of a pattern, alternation
and lazy quantifiers.

It is off by default and should stay off for untrusted input. RE2 is linear in
the length of the input and cannot be made to backtrack; this engine has no
such guarantee, and a pattern is not always the caller's own — `matches($s,
$node/@pattern)` takes one from document data, so enabling it globally would
let a document being validated choose how long the validation takes.

Even enabled, a step budget bounds every match, and exhausting it raises
FORX0002 rather than returning a silent "no match": a budget that guessed
would do it precisely on the inputs where the answer was hardest to get. The
budget is measured from both ends — the hardest honest pattern in either
conformance suite answers in 525 steps, while `(a*)*\1b` against sixty `a`s
exhausts the whole budget in about 200 ms.

The default path is unchanged. Patterns whose backreferences are decidable by
the existing fixed-width analysis still take it, since that path is exact and
faster, and with the switch off the conformance failure set is byte-identical
to before.

With it on, nine XSLT tests and the last QT3 failure pass — XSLT 2.0 at 99.69%
and QT3 at 15,183 of 15,183. The headline figures below report the default
configuration.

### XSLT 2.0 conformance: 98.83% to 99.54%

6,024 of 6,052 in scope, up from 5,982 of 6,053. 43 tests fixed across two
rounds, no regressions. XSD 1.1 gained one instance test (26,158 of 26,209);
XSD 1.0, QT3 and RELAX NG are unchanged.

The second round added `xsl:mode/@on-no-match` (all six values of section
6.6 — the attribute had been parsed and discarded, though 335 stylesheets in
the suite use it), the `item-separator` serialization parameter, `case-order`
as a tertiary tiebreak under a language collation, and per-node base URIs for
content that arrives through an external entity, so `xml:base` inside an
entity now resolves against the entity rather than its including document.

Two further wrong answers, both in the parser:

- Attribute values were not normalized per XML 1.0 section 3.3.3, so a
  literal tab or newline inside an attribute survived into the value. This
  had to be done at the lexical layer: after `encoding/xml` decodes, a
  character reference and the character it denotes are indistinguishable, and
  section 3.3.3 requires `&#10;` to survive where a raw newline becomes a
  space.
- Whitespace in element-only content declared by a schema was not treated as
  ignorable, the schema-side counterpart to the DTD fix in the first round.
- `fn:idref` split its argument on whitespace. `fn:id` does, because its
  argument is a sequence of IDREFS values and an IDREFS value is a
  whitespace-separated list; `fn:idref` takes `xs:string` and matches it
  whole, so `idref('a c')` looks for the single name "a c" — not a valid
  `xs:IDREF`, and so matching nothing.

`xsl:variable` bindings in a sequence constructor are now skipped when the
name is never referenced later, which XSLT 2.0 section 5.2 permits: a
circularity is an error only if the variable involved is actually evaluated.
The check is deliberately one-sided, and a declaration whose own select
references its own name still binds eagerly, since XPST0008 is a static
error and is due whether or not the value is demanded.

New in the engine: `xsl:evaluate` and `xsl:iterate` (recognised under 2.0
semantics, which is what the suite's `XSLT20+` dependency means); the XPath
3.0 `||` and `=>` operators alongside the simple map `!`; the
`http://www.w3.org/2013/collation/UCA` collation family, backed by the CLDR
tables already vendored for `xsl:sort`; and backreferences in `replace()`.

Corrections worth noting, because each was a wrong answer rather than a
missing feature:

- `matches()` returned `false` for text it matches whenever a backreference
  was separated from its group by a variable-width expression. The soundness
  check examined only the group. A wrong answer is worse than the `FORX0002`
  refusal it now gives.
- Whitespace in DTD element-only content was not ignorable, so
  `xsl:preserve-space` could preserve what XML 1.0 section 2.10 defines away.
- `xsl:result-document` ran its body without binding variables declared
  inside it, so every reference to one raised `XPST0008`.
- An expression containing a braced URI literal silently lost the schema from
  its static context, sending every schema lookup down its "no schema" branch.
- `deepCopy` and the type-annotation stripper dropped the is-id and is-idrefs
  properties they were documented to preserve.
- A value derived by restriction from `xs:boolean` atomised to nothing when
  its lexical form carried the whitespace `xs:boolean` collapses.
- An `xs:QName` with no in-scope binding for its prefix was accepted; Part 2
  section 3.2.18 makes it denote no value.

The UCA collation refuses `caseFirst`, `alternate`, `maxVariable`, `reorder`
and `backwards` even under `fallback=yes`, where the specification permits
ignoring them. Ignoring a parameter that changes the *order* yields a sort
that is quietly wrong, which is worse for the caller than a refusal.

## v0.1.0 — 2026-08-22

First tagged release. Everything before this was unversioned, so this entry
describes what the library does rather than what changed.

### What it is

XPath 2.0, XSLT 2.0 and three schema languages in pure Go. No cgo, no JVM, no
libxml2.

| | Suite | Result |
|---|---|---|
| XPath 2.0 | W3C QT3 (FOTS) | 99.99% — 15,180 of 15,181 in scope |
| XSD 1.0 | W3C xsdtests | 99.80% instance · 98.60% schema-validity |
| XSD 1.1 | W3C xsdtests | 99.81% instance · 97.96% schema-validity |
| RELAX NG | James Clark's spectest | 100.00% — 965 of 965 |
| DTD | *no public suite* | content models, defaults, `ID`/`IDREF` |
| XSLT 2.0 | W3C xslt30-test, filtered | 99.54% — 6,024 of 6,052 in scope |

DTD has no percentage because no public conformance suite exists for it.

XSLT's percentage is not comparable to the others. There is no maintained
XSLT 2.0 suite — the original froze in 2007 — so this is the XSLT 3.0 suite
filtered by each test's declared version dependency, which measures something
different from running a suite written for the version under test. It is also
young, and the first runs were dominated by harness bugs rather than engine
ones, so read it as a floor. The differential against Saxon-HE 12.4 on two
production corpora remains the stronger evidence for real stylesheets.

### Packages

`xdm` (data model and parser), `xpath`, `xslt`, `xsd`, `dtd`, `relaxng`, and a
`go-xml` command-line transformer. Each is usable on its own; the layering is
strict and one-directional.

### Security posture

Every mechanism that reaches outside the document is off by default: `DOCTYPE`
is refused, `xsi:schemaLocation` is ignored, and no schema, document or entity
is fetched without a caller-supplied resolver. Input size, node count, nesting
depth and recursion depth are all bounded.

Two security audits have been run against the library, both recorded in
[docs/security.md](docs/security.md) with the findings and their fixes. The
second audit found four defects in code added during the same session and all
four are fixed here.

The one thing a caller must still do is **sanitise URLs when rendering
transform output as HTML** — XSLT does not, and is not supposed to.

### Known gaps

Every measured failure is listed in [docs/known-gaps.md](docs/known-gaps.md),
including fix attempts that were reverted for costing more than they gained.
The largest are:

* XSD schema-validity, at 98.60% (1.0) and 97.96% (1.1). Instance validation —
  what most callers do — is above 99.7% in both. A substantial share of the
  remaining disagreements are cases the W3C's own suite marks as disputed.
* RELAX NG's compact syntax is not implemented; only the XML syntax is.
* A DTD's external subset is never fetched, so validation against one is
  partial by design. `DTD.HasExternalSubset` says when that happened.

### Stability

The API is pre-1.0. The shape is settled and the conformance figures are not
expected to move down, but names may still change — `relaxng` narrowed its
exported surface from 27 symbols to 7 shortly before this release, which is
the kind of change a `0.x` version exists to allow.
