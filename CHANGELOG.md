# Changelog

Notable changes, newest first. Versions follow [semantic
versioning](https://semver.org): from 1.0.0 the exported API is stable, and a
breaking change means 2.0 with a new module path. See *Stability* below.

## Unreleased

### XQuery conformance: 99.61% to 99.85%

29,759 of 29,803 QT3 cases in scope, up 70 across five passes. XPath 2.0/3.0/3.1 stay at 100% and
XSLT at 8,606 / 6,149.

* **A constructed element no longer inherits its parent's namespace fixup.**
  XQuery §3.9.1.3 passes down exactly one kind of binding — the ones written as
  namespace *declaration attributes* — while the bindings §3.9.1.1 adds so that
  an element's own name and its attributes' names resolve are local to that
  element. Reading in-scope namespaces by walking the XDM tree cannot tell the
  two apart, so `<e a:n1="c" b:n1="c">` handed both `a` and `b` to every child;
  the child now undeclares what it is not entitled to. K2-NameTest-30/31,
  K2-InScopePrefixesFunc-25 and cbcl-directconelem-001/002.
* **`copy-namespaces preserve` stops undeclaring the default namespace on a
  prefixed copy.** The default namespace applies to unprefixed element names
  and to nothing else, so a `<bar:b>` copied under an `<a xmlns="http://foo">`
  is not moved by that binding, and the `xmlns=""` was taking away a namespace
  §4.8's `inherit` entitles the copy to keep. An unprefixed copy in no
  namespace still gets the undeclaration it needs.
* **The XML output method honours `undeclare-prefixes`.** The parameter was
  validated and then ignored: a `xmlns:p=""` in the tree was written out
  regardless, which is XML 1.1 syntax. XSLT 3.0 §11.7 generates prefix
  undeclarations only when `undeclare-prefixes="yes"`, so they are now omitted
  by default. The default-namespace undeclaration `xmlns=""` is legal in XML
  1.0 and is unaffected.
* **The QT3 harness compares result *sequences* by infoset.** A query returning
  two elements, or text beside an element, serialises to something that is not
  a well-formed document, so the infoset comparison — the only one that ignores
  where a namespace declaration sits — could not parse it and never ran. Both
  sides are now wrapped in the same synthetic root first. This is what the
  note on K2-FilterExpr-7 had already identified as the fix worth making.

* **A range survives being counted through a comma expression.** `fn:count`,
  `fn:empty` and `fn:exists` are defined purely on the length of their
  argument, so a sequence constructor's length is now summed from its parts and
  a `lo to hi` part contributes its cardinality without being built.
  `count(((), f(()), (1 to 10000000), f(1)))` no longer trips the five-million
  item guard. Parts that *are* materialised are still charged to that budget.
* **`fn:distinct-values` compares numerics pairwise** instead of hashing them.
  F&O §14.1.7 warns that `eq` is not transitive across numeric types, and
  constrains the result only to "no two items compare equal" and "every input
  equals some output" — constraints a single hash key cannot satisfy. This also
  drops the whole-sequence scan that degraded every numeric to float precision
  whenever one `xs:float` appeared anywhere in the input.
* **`fn:deep-equal` no longer merges text across a comment or PI.** The rule for
  an untyped (mixed-content) element is that `$i1/(*|text())` be deep-equal to
  `$i2/(*|text())` — a selection over the child axis, which drops comments and
  PIs but leaves the text nodes either side of one separate. Merging them made
  `<e>te<?t d?>xt</e>` wrongly equal to `<e>text</e>`.
* **`fn:filter` applies the function conversion rules to its predicate's
  result**, so a predicate returning an element is atomised and cast rather
  than refused. This is a cast, not an effective boolean value: an `xs:string`
  result is still `XPTY0004`, as are an empty and a two-item result.
* **The date and time component accessors cast `xs:untypedAtomic`.** Content
  from an unvalidated document reached `month-from-date` and friends as
  `xs:untypedAtomic` and was rejected; the function conversion rules cast it to
  the accessor's declared type. `xs:string` is still not accepted.
* **`method="json"` is accepted in the element form of the serialization
  parameters**, which had a narrower list of methods than the map form. The two
  spellings now share one check.
* **Synthetic names survive a prolog that rebinds the `local` prefix.** The
  argument variables and step functions this package invents are written with
  that prefix and so resolve through the static context; they are now bound
  under whatever it actually points at rather than under the fixed
  local-function namespace.

### Fixed — found by real-world stylesheets

Measured against [DocBook xslTNG](https://github.com/docbook/xslt3ng) and
[XSpec](https://github.com/xspec/xspec), two XSLT 3.0 codebases large enough to
exercise combinations the W3C suites do not reach. 544 of DocBook's 593 test
documents now render, byte-identical to the Saxon reference output once the
timestamp and generator metadata are normalised, and all 225 applicable XSpec
descriptions compile. Most of the remaining 49 need a Saxon-Java extension
function for XInclude.

* **`xsl:copy` over a non-node context item** no longer raises `XTTE0945`.
  11.9.1 raises it only when the context item is *absent*; one that is present
  but is an atomic value returns that value. Conflating absent with
  not-a-node made `xsl:copy` inside `xsl:for-each` over atomics an error.
* **`fn:key` resolves its name against every binding of the prefix**, not just
  the first. The name is a lexical QName expanded at run time, so keeping one
  URI per prefix let whichever module was included last decide what it meant —
  XSpec binds `local` to 19 different URIs, one per module, and
  `key('local:scenarios', …)` failed purely by include order.
* **`xsl:evaluate` can call the stylesheet's own functions** outside an
  `xsl:package`. §10.4.1 excludes *private* functions and the default is
  private, but visibility is a property of a component of a package and a plain
  `xsl:stylesheet` is not one. Inside an `xsl:package` declared visibility is
  honoured as before. This costs W3C `evaluate-045` (XSLT 3.0: 8,607 → 8,606
  of 8,626); Saxon's own results report that case as `wrongError` too.
* **`tests/check.sh` gates on both corpora.** A new *real-world stylesheets*
  section transforms every DocBook and XSpec input and ratchets the number that
  succeeds, so a future change that breaks them fails the build rather than
  being noticed by hand. Both are skipped, not failed, when absent.
* **The CLI passes base URIs as `file:` URIs** rather than filesystem paths.
  `fn:resolve-uri` and `fn:static-base-uri` are defined over RFC 3986
  references, so `resolve-uri(rel, static-base-uri())` — the idiom a stylesheet
  uses to find a file beside itself — raised `FORG0002` on every run.

## v1.1.0 — 2026-09-01

Additive throughout: nothing exported by v1.0.0 was removed or changed shape,
so a v1.0 program compiles and behaves the same.

### Conformance

|  | v1.0.0 | v1.1.0 |
|---|---|---|
| XPath 2.0 (QT3) | 99.99% | **100%** |
| XPath 3.0 (QT3) | — | **100%** |
| XPath 3.1 (QT3) | — | **100%** |
| XSLT 2.0 | 99.63% | **99.85%** |
| XSLT 3.0 | — | **99.78%** |
| XSD 1.0 schema / instance | 99.56% / 99.88% | **99.86% / 99.88%** |
| XSD 1.1 schema / instance | 99.18% / 99.89% | **99.88% / 99.89%** |
| RELAX NG (spectest) | 100% | **100%** |

**XSLT 3.0 is the headline.** v1.0.0 shipped XSLT 2.0; this release adds
packages (`xsl:package`, `use-package`, `accept`, `expose`, `override`),
accumulators, `xsl:evaluate`, `xsl:iterate`, `xsl:merge`, `xsl:try`, maps and
arrays, higher-order functions, JSON, and the 3.0 serialization methods.
Streaming is not implemented, and its 2,716 cases are out of scope rather than
failing.

**Schema validity was the weak half and is no longer.** XSD 1.1 went from
99.18% to 99.88% — roughly a hundred and thirty missing schema-validity rules,
written one at a time and each measured against both versions so that no
agreement count ever fell.

**126 disagreements remain and none is a known defect in this engine.** Every
one is a suite defect, an expectation the W3C has itself challenged, a network
fetch, a vendor extension, or a Unicode snapshot that has moved.
[docs/conformance-gaps.md](docs/conformance-gaps.md) names each case and says
which; two open questions are recorded there as open rather than settled.

### Resolving schemaLocation without the network

Schemas name their imports as absolute URLs, and those fetches are unreliable
by design — the W3C throttles them. The W3C's own copy of the XSLT 3.0 schema
in the XSLT test suite was edited in 2021 to use a relative path, the comment
there giving the reason as "W3C web site throttling".

`xsd.CatalogResolver` answers a `schemaLocation` from memory, keyed by what a
reference *means* rather than how it is spelled: one entry answers the `TR/`
URL, the `2001/` URL, a bare relative path, and an `xs:import` that gives only
a namespace. A miss is an error rather than a request, which is what makes it
usable in a server. `xsd.W3CEntries` states the aliasing as data.

The schemas themselves are in a companion module, `w3cschemas`, because they
are W3C documents under W3C terms rather than MIT.

### Security: two nesting constructs the depth counter never saw

The v1.0.0 notes below describe an XPath depth bound "counted at the single
point every nesting construct passes through". That was an argument about the
grammar rather than a fact about it, and two constructs did not pass through
that point. Each exhausted the goroutine stack, which in Go is a *fatal error*
that `recover()` cannot catch — so an untrusted input killed the process
rather than failing the request.

* **Sequence types.** `parseSequenceType` recurses into itself for a
  parenthesised item type, for a function test's argument and return types,
  and for the member types of `map()` and `array()`. 400 KB of
  `1 instance of ((((…item()…))))` was enough at Go's default 1 GB stack, and
  it is reachable through any `@select`, `@test` or `@as`, and through
  `xs:assert/@test`.
* **XSD pattern facets.** The XSD-flavour regular-expression parser recurses
  once per group and counted nothing; a 6 MB schema was enough. Hostile-schema
  only: the XPath-flavour checker is an iterative scanner, so a pattern
  arriving as document data never reached it.

Both are now bounded at 1000 levels at their own recursion points, and each
construct has its own test rather than sharing one and an argument.

Also fixed: **a named function reference with no function library panicked**
where the equivalent call correctly raised `XPST0017`, and two schema-assembly
sites **leaked a reader** when a resolver returned one alongside an error.

### Known: nested occurrence bounds

Found by differential fuzzing against a brute-force reference, and invisible
to both W3C suites. A repeated group whose *only* child is itself repeating is
decided wrongly in both directions: for `<sequence minOccurs="5"
maxOccurs="5">` over `<element c minOccurs="2" maxOccurs="2"/>`, ten `c` is
the only valid document and is refused, while five `c` is accepted. **The
false-accept direction means a `minOccurs` floor is silently not enforced.**

A group with two or more distinct child names is decided correctly, which is
why 80,878 suite agreements step around it. Long-standing rather than new.
Diagnosed in [docs/known-gaps.md](docs/known-gaps.md); not fixed here, because
the fix is a matcher change the suites cannot defend.

### Also

* **`xsl:import-schema` can now read a schema carrying a `DOCTYPE`**, via
  `CompileOptions.SchemaParseOptions`. It still refuses one by default. The
  W3C's schema for schemas declares its entities that way, so without this the
  XSLT 3.0 schema could not be loaded at all.
* **An imported schema is read under the XSD version it declares.** A document
  carrying `vc:minVersion="1.1"` read as 1.0 is conditionally excluded in its
  entirety — silently, since that is what the attribute asks a 1.0 processor to
  do. The XSLT 3.0 schema is such a document, and all 81 of its element
  declarations were being dropped without an error.
* **`fn:load-xquery-module` reports the absent XQuery processor uniformly.**
  It has no processor to defer to, so every code it can raise describes that.

## v1.0.0 — 2026-08-24

### Security: three bounds that were not bounding

A third audit. Full detail in [docs/security.md](docs/security.md).

* **Entity expansion was charged once per entity, not once per reference.**
  Reachable with `AllowDOCTYPE: true` alone. A 70 KB document allocated 741 MB
  and was accepted. `MaxBytes` and `MaxNodes` both missed it — a reference is
  three bytes and a run of them coalesces into one text node. The bound was
  reported as working because a *different* code path charged correctly, so
  which path a document took decided whether it was bounded.
* **A nested XPath expression could kill the process rather than the request.**
  Reachable from a hostile stylesheet. A stack overflow in Go is a fatal error
  `recover()` cannot catch. Expression nesting is now bounded at 1000 levels.
* **RELAX NG nested `oneOrMore` is exponential in document width.** Reachable
  with default options from a hostile instance — a 189-byte schema and a
  63-byte instance cost over a second and a gigabyte. `MaxDepth` cannot bound
  it: the document is two levels deep however wide it grows. `MaxPatternSize`
  now holds the cost flat. This is a bound, not a cure; the structural fix is
  pattern interning.
* **`xsl:analyze-string` ignored the regex step budget**, so with the
  backtracking matcher enabled an exhausted budget was indistinguishable from a
  genuine non-match and the transform silently emitted wrong output.

One finding is left open and documented: compiling an *untrusted schema* can be
exponential in the depth of its group-reference graph.

### A constraint that never ran against the ordinary spelling

Unique Particle Attribution and Element Declarations Consistent were checked
only against a schema's *named* complex types. A type declared inline in an
element — the ordinary spelling — was never checked, so a schema with no named
types at all was checked against nothing and `(a?, a)` loaded clean.

This is a validator failing open rather than a missing conformance point, and
it is the second time the same shape has been found here: Particle Valid
(Restriction) had the identical gap earlier in this cycle. The lesson is
recorded in `docs/known-gaps.md` — when adding a schema component constraint,
verify the walk that reaches it visits anonymous types too. `xsd/upa_test.go`
now asserts both spellings, since every existing test used the named one.

Schema validity reaches **99.56%** on XSD 1.0 and **99.18%** on 1.1, both above
the ceiling this project's own analysis predicted. Also fixed: NOTATION as a
list item type, and `xs:anyAtomicType` as a restriction base, list item type or
union member.

### Circularity, and the exception that terminates every chain

Schema validity reaches 99.51% on XSD 1.0 and 99.11% on 1.1 — both at the
ceiling this project's own analysis predicted, with the remainder dominated by
tests the W3C's metadata disputes.

Four kinds of circularity were undetected, each because the structure that
would have revealed it is acyclic:

* A union whose transitive `{member type definitions}` contain the union
  itself (`st-props-correct.2`). The base-chain walk cannot see this: two
  unions naming each other have entirely acyclic base chains.
* A complex type reachable from its own `{base type definition}`
  (`ct-props-correct.3`).
* A circular substitution group (`e-props-correct.6`). The linker already
  survived cycles with a seen-set, so nothing downstream ever noticed one.
* `src-import.1.2` — an `<xs:import>` with no `namespace` requires the
  importing schema to have a `targetNamespace`.

The base-type rule needs the specification's "except for the ur-type
definition" exception, and it is not a courtesy: `xs:anyType` is its own base,
so the exception is what terminates every other chain. Omitting it rejected
**11,044 of 14,405 schemas**. That number is now recorded beside the check.

One test moves from passing to failing: `ste110`, "test circular union",
expects a schema where two unions name each other to be *valid*. Its W3C status
is `queried` under bug 4957, and it contradicts `st-props-correct.2` outright.

Also `cos-ct-extends.1.4.1`, checked on the source form rather than the
resolved content type because the extension splice rewrites the latter from the
base before any later pass could look; and the open-content half of
`cos-ct-restricts`, where a restriction declaring `{open content}` requires a
base that declares one and may not `interleave` where the base only appends.
Both open-content halves are conditioned on the derived particle admitting
something other than the empty sequence — with an empty model there is nothing
to interleave among, and `interleave` and `suffix` denote the same language.

### The harness claimed two mutually exclusive processor configurations

XSD 1.1 schema validity reaches 99.00%, and 10 of the 22 tests gained were
never a validator defect. `tests/xsdsuite` claimed support for both
`restricted-xpath-in-CTA` and `full-xpath-in-CTA`. Those name *mutually
exclusive* configurations, and a CTA test states an expectation for each:
`cta0022` is `valid` under full-xpath and `invalid` under restricted-xpath. The
later `<expected>` wins, so claiming both made the harness demand rejection of
schemas this processor correctly accepts. Only the token describing what is
implemented may be claimed.

The denominator is unchanged at 15,365 — these tests moved from failing to
passing, not from failing to skipped.

### Conditional type assignment, all-groups, and open content

Four rules, +12 on XSD 1.1 with XSD 1.0 untouched:

* `e-props-correct.5` (section 3.3.6) — every type an alternative can select
  must be validly derived from the element's declared type, `xs:error`
  excepted. Four of this project's own test fixtures declared schemas that
  violate this; they now name a common base, and what each test asserts is
  unchanged.
* A default alternative — one with no `test` — must come last (section 3.3.3).
* `cos-all-limited` (section 3.8.3) — a model group inside `xs:all` must
  itself be an all-group, and a nested all-group may not repeat.
* `cos-ct-restricts.2` (section 3.4.6.2) — an open-content wildcard must be a
  subset of the base's and may not loosen `processContents`.
* The wildcard arm of Element Declarations Consistent (section 3.8.6), where a
  strict or lax wildcard matches a global declaration whose type table differs
  from a like-named local particle's.

Three over-broad readings were caught and reverted before they shipped. A type
extending a base with open content and declaring none *inherits* it (section
3.4.2.3.3) rather than closing it, so the "extension mirror" rule rejected six
valid schemas. "`interleave` cannot restrict `suffix`" holds only when there is
something to interleave among. And the *type* half of wildcard EDC is a
validation-time check rather than a schema constraint — `wild061` says so
outright: the schema is valid though no document can satisfy it.

### XSD 1.1 wildcard attributes

Schema validity reaches 99.47% on XSD 1.0 and 98.85% on 1.1.

`namespace` and `notNamespace` on a wildcard are mutually exclusive — they are
two spellings of one `{namespace constraint}` property, and letting
`notNamespace` quietly win discarded what the schema author wrote. The check is
deliberately not version-gated: under XSD 1.0 `notNamespace` is an unrecognised
attribute in the XSD namespace, which is not a licence to ignore the conflict,
and that is what earns the two XSD 1.0 gains.

Every QName in `notQName` must lie in a namespace the wildcard's own constraint
admits (section 3.10.3). Excluding a name the wildcard could never match is a
contradiction rather than a narrowing. `##defined` and `##definedSibling` name
no namespace and stay unconstrained.

And `":stylesheet"` is not a QName. The resolver split it into an empty prefix
and a local name, then took the *no-prefix* path and accepted it as an
unqualified name; `xs:QName` requires a non-empty NCName on both sides of the
colon.

### Restricting xs:anySimpleType, and a contravariant wildcard rule

Schema validity reaches 99.45% on XSD 1.0 and 98.78% on 1.1.

`<xs:restriction base="xs:anySimpleType"/>` is now rejected. No clause forbids
it by name, which is why reading section 3.14.6 alone does not find it; the
rule is a three-step chain through the `{variety}` property. Part 2 section
4.1.1 makes `anySimpleType` the simple ur-type definition, whose variety is
*absent*; `cos-st-restricts` has a restriction inherit its base's variety; and
`st-props-correct.1` requires every simple type definition's variety to be
atomic, list or union. Absent is none of them. xmllint reports the same case as
"The variety is absent."

The rule stays narrow deliberately: naming `xs:anySimpleType` as a
*declaration's* type creates no new simple type definition and remains legal.

Wildcard Subset (section 3.10.6) clause 2 is **contravariant** in the excluded
set. The shared helper required two negated wildcards to exclude exactly the
same namespaces — correct for XSD 1.0, where a negation names one namespace. In
1.1 `notNamespace` names a *set*, and a negation of S1 is a subset of a
negation of S2 exactly when S2 is a subset of S1: excluding more admits fewer.
Equality is the special case where each set contains the other, so the fix
subsumes the 1.0 reading and needs no version gate. This also retires a
duplicate of the rule that had been added locally for attribute wildcards.

Also: derivation-ok-restriction clause 2.1.2 — where a restriction redeclares
an attribute, its type must derive from the base's, so a restriction could
previously widen an attribute's type freely. Only a simpleContent *extension*
may name a simple type as its base. And an unresolvable union member type is a
hard error rather than a deferred one, since a union cannot be built without
its members; the same treatment is deliberately *not* applied to a list's item
type, where two suite tests contradict each other and the existing deferral is
pinned by `missing006`.

### A valid document was being refused because an assertion crashed

XSD 1.1 defines the default collection in the dynamic context of an assertion
or type alternative as the empty sequence. It was left nil, so `fn:collection()`
raised FODC0002 — and because a type alternative whose test *raises* is silently
skipped, the failure was indistinguishable from a test that simply returned
false. `cta0022` fell through to its declared union and rejected a perfectly
valid `xs:date`. A crash wearing the costume of a wrong answer.

### Schema-validity: 99.40% on XSD 1.0, 98.72% on 1.1

A third round, +29 on 1.0 and +38 on 1.1 with nothing lost. Five rules:

* src-ct.1 and src-ct.2.1 (section 3.4.3) — which base shape each content form
  may sit on. `readSimpleContent` is the only path that sets a content type to
  simple, so the source form is recoverable without a new component field.
* cos-ct-extends.1.4.3.2.2.1 and derivation-ok-restriction.5.4.1.2 — mixedness
  consistency between a type and its base.
* derivation-ok-restriction.4, via Wildcard Subset (section 3.10.6), for
  attribute wildcards.
* Section 3.14.2 local simpleType form, and cos-list-of-atomic (Part 2 section
  4.1.5).

**Two over-broad readings were caught only by re-measuring**, and both would
have shipped as wins:

* **Mixedness is not symmetric.** Restricting a mixed base to element-only is a
  legitimate narrowing; only the reverse is forbidden. Extension is an "if and
  only if", restriction is one-directional. Reading them as one rule cost four
  tests.
* **cos-list-of-atomic must recurse through nested unions.** A union's members
  may themselves be unions. The test suite's own catalog schema defines a list
  of a union of eight unions, so rejecting any union-typed member rejected the
  catalog itself — and with it 91 instance tests per version.

### Instance validation reaches 99.88% on XSD 1.0 and 99.89% on 1.1

Instance disagreements fall from 48 to 31 on 1.0 and from 51 to 29 on 1.1,
with no schema-side regression. Nine rules, each measured on its own:

* cvc-elt 5.2.2.1 (section 3.3.4) — a fixed value constraint forbids element
  children. And 5.2.2.2.1: for a *mixed* content type the element's initial
  value must match the fixed value. Only the simple-content half of that
  clause, 5.2.2.2.2, had been implemented.
* cvc-type 3.1.1 on a nilled element. Clause 5.2 applies exactly when 3.2 has
  applied, and 5.2.1 still runs Element Locally Valid (Type); only 3.1.3 is
  conditioned on nilling. The whole rule was being skipped.
* Section 3.11.4 clause 3 — an identity-constraint field must select a node
  with a simple type. A complex-typed one fell back to its string value.
* Part 2 section 3.2.6.1 — duration seconds are `\d+(\.\d+)?`, so digits are
  required after the point; `PT12H30M12.S` was accepted.
* Both fixed-value checks validated the fixed literal as XSD 1.0 regardless of
  the schema's version. Under 1.1 `+INF` failed that validation, and since the
  comparison is guarded on it succeeding, the fixed check was silently skipped
  altogether.
* Section 3.14.4 — union member selection is per value: each side takes the
  first member whose lexical space contains its own literal. Trying every
  member until a pair agreed equated values of different primitive types.
* Section 3.9.6 Particle Emptiable 2.2.2 — an empty `<xs:choice/>` with
  `minOccurs="1"` admits the empty *language*, not the empty *string*. The
  vacuous-quantification answer is right for a sequence and wrong for a choice.
* The XML NameStartChar production. `isNameStartRune` accepted any rune at or
  above U+0080, which is far wider than the production; NEL and LS are among
  the characters it must exclude. This also governs `Name`, `NCName`, `ID`,
  `IDREF` and `ENTITY`.

### Schema-validity: 98.60% to 99.19% on XSD 1.0, 97.96% to 98.48% on 1.1

A second round adds src-redefine 6.2.2 and 7.2.2 (section 4.2.2): a `<group>`
or `<attributeGroup>` redefined without a self-reference must be a valid
restriction of what it replaces. The group form defers to Particle Valid
(Restriction); the attribute form applies derivation-ok-restriction clauses
2.1.1, 2.1.2, 2.1.3, 2.2 and 3.

Two things about where that check can run. It belongs in `applyRedefine` — the
only point where the original and its replacement both exist — and the
attribute half must be deferred to the post-fixup pass, because until type
fixups drain every base attribute still reads as untyped and the derivation
clause passes vacuously. Version gating came for free: threading the schema
version into the particle check makes the 1.1 all-group rule decide the one
test annotated invalid for 1.0 and valid for 1.1.

### Schema-validity, first round: the constraint that never ran

122 schema documents that were accepted despite being invalid are now
rejected, with no instance test and no other suite moving. Every one is a test
the W3C's own metadata records as `accepted`; no `queried` or bug-tied test was
targeted, which is the line this project holds — 48 of the remaining 1.0
disagreements and 50 of the 1.1 ones are suite disputes, 18 of them bug 4113
alone, where "passing" would mean freezing a Unicode 3.1 table and being wrong
about modern text.

The largest single cause was not a missing rule. `checkParticleRestriction`
walked `schema.Types`, which holds only *named* types, so Particle Valid
(Restriction) (section 3.9.6) never ran on a restriction written as an inline
`<xs:complexType>` — which is how most of them are written. The dispatch table
had been correct all along and was simply never reached for those types.

The rules that were genuinely missing:

* Clause 2.2 pointless-group inlining. `stripPointless` unwraps only the
  particle being compared, so a same-compositor wrapper among a group's
  *members* left the two sides of a Recurse at different depths.
* NameAndTypeOK clause 3.2.4 — the restricting declaration must block
  everything the base blocks. `#all` is masked to the three derivations
  `block` can name, since `#all` and "substitution extension restriction"
  denote the same set there (W3C bug 4144).
* src-include.1, src-redefine.1 and src-import.3.1/3.2: the referenced
  document's target namespace must match the referring one.
* src-redefine clauses 5, 6.1.1, 6.1.2, 6.2.1, 7.1, 7.2.1 and 2 — a redefined
  type must derive from itself, a redefined group has at most one
  self-reference with unit occurrence, and two children of one redefine may
  not redefine the same name. Answering the "defined in the redefined
  document" clauses needed a new fact: which document each include or redefine
  actually names, since the redefining document may declare the same name.
* localComplexType: `name`, `abstract`, `final` and `block` are prohibited on
  an inline complexType; `substitution` is not a legal token in a type's
  `block`; `targetNamespace=""` names no namespace; and an attribute
  declaration may not be in the schema-instance namespace.

Three XSD 1.1 particle tests move from passing to failing, and the change is
still correct: they are annotated `invalid version="1.0"` / `valid
version="1.1"` in the suite's own catalog, so they were false accepts in both
versions before and are now right in 1.0. Passing them in 1.1 needs the
section 3.4.6.4 intensional-restriction check — true language inclusion rather
than the structural table — which is not implemented.

### Entity replacement text inside an attribute value is included literally

XML 1.0 section 4.4.5, "Included in Literal": a reference inside an attribute
value has its replacement text included *as literal characters*, so a quote in
that text is data and does not end the attribute. The substitution path that
rewrites entity references into the source spliced the text in raw, so an
entity whose text contained a quote produced a malformed document.

DocBook is the case that found it — `entities.ent` declares

    <!ENTITY primary 'normalize-space(concat(primary/@sortas, " ", primary))'>

and every stylesheet using `&primary;` inside a double-quoted attribute failed
to parse. The fix tracks start-tag and attribute-quote state while scanning,
advancing it on the document's own bytes only: a quote arriving from
replacement text is data, and letting it change the state is precisely the bug.
Inside an attribute value the three characters that would be markup there are
written as character references. `&` is deliberately not among them, because
the rewrite path leaves it for the second parse to decode.

### EXSLT `node-set`

`{http://exslt.org/common}node-set` is available to stylesheets. XSLT 2.0
eliminated the result-tree-fragment type (section J.1.2), so on this processor
the conversion is the identity on its argument, and it passes the sequence
through rather than wrapping it — wrapping would change the node identity that
`generate-id()` and the union operators compare.

It is registered into the library a running stylesheet sees, not into
`xpath.Builtins()`. A plain XPath caller still gets XPST0017 for it, which is
what a processor is required to report for a function it does not have.

### Fixed: a multi-digit backreference to an unclosed group was renumbered

In the backtracking matcher, `\10` written inside the tenth group was split
into `\1` followed by a literal `0` rather than being rejected. Erratum FO.E24
makes a backreference to a group that has not yet closed a malformed pattern,
so this turned a required FORX0002 into a quiet non-match. The greedy split is
now attempted only when the number names no group at all. Single-digit
references were already correct; the bug needed two digits to reach the split.

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

### XSLT 1.0 backwards-compatible behaviour

`[xsl:]version="1.0"` now enables the behaviour XSLT 2.0 section 3.8 and XPath
2.0 section B.1 define for it, instead of raising XTDE0160. Argument coercion
takes the first item of a sequence where 2.0 raises a type error; comparison
with a node-set uses the existential 1.0 rules; arithmetic on a non-numeric
yields NaN rather than failing; `xsl:value-of` takes the first item;
`system-property('xsl:supports-backwards-compatibility')` answers "yes"; and a
call to an unavailable extension function becomes a *dynamic* XTDE1425 raised
only if it is evaluated, rather than a static XPST0017.

The flag is static, resolved at compile time and carried on the compiled
expression, so it reaches evaluation through a single write site. A stylesheet
that does not declare 1.0 is byte-identical to before; QT3 was measured five
times across the work and never moved.

One subtlety: the constant folder was baking in 2.0 answers, folding `1 + 1` to
an `xs:integer` where 1.0 requires `xs:double`. Folding is now withheld for the
operators B.1 redefines.

**This grows the measured denominator**, because 112 tests that declared the
feature as a dependency were previously skipped. 104 of the 107 newly in-scope
tests pass. The absolute count went from 6,028 passing to 6,132; the percentage
moved from 99.60% to 99.56%, because the admitted set is harder than the corpus
average. The number below is the honest one, and it is not comparable to
earlier figures measured over the smaller scope.

Two further fixes in backwards-compatible mode. Unary minus now converts its
operand with `fn:number` as B.1 rule 2 requires, so `-0` under 1.0 keeps its
sign — in 2.0 it is unary minus on an integer, which has no signed zero, and
`0` stays the right answer there. And an unprefixed atomic type name is
resolved in the default element namespace, which is inherited rather than read
from the element alone; `use-when` on a template whose sibling declares
`xpath-default-namespace` does not see that declaration.

### XSLT 2.0 conformance: 98.83% to 99.61% over a scope that grew

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
- Type annotations are namespace-qualified. They were keyed on the bare local
  name in a process-global map, so a schema defining its own type with a
  built-in's local name displaced the built-in for every schema loaded
  afterwards — permanently, and across documents. The XSLT 2.0 schema does
  exactly that with `QName`, and says in its own text why. Names in the XML
  Schema namespace stay bare so that built-in comparisons are unaffected; only
  genuinely ambiguous names change spelling.
- A node whose type was a union atomised to `xs:untypedAtomic`, because a
  union's base is `xs:anySimpleType` and the derivation chain dead-ended there.
  A union's members are *siblings* rather than ancestors, so no single name can
  answer for both: the union stays on the node's annotation and the member
  chosen for that particular value is recorded beside it. `data(x) instance of
  member` and `x instance of element(*, union)` are now both true.
- A restriction *of* a union carries no member list of its own — it inherits
  one — so union validation iterated an empty slice and admitted nothing.
- Whitespace-only text was stripped from an element whose type is a simple
  type, or a complex type with simple content, when `xsl:strip-space` named it.
  Section 4.4 exempts such an element whatever the declarations say: the text
  is its entire typed value, and stripping it leaves an annotation describing a
  value the node no longer holds.
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
| XPath 2.0 | W3C QT3 (FOTS) | 99.99% — 15,182 of 15,183 in scope |
| XSD 1.0 | W3C xsdtests | 99.88% instance · 99.56% schema-validity |
| XSD 1.1 | W3C xsdtests | 99.89% instance · 99.18% schema-validity |
| RELAX NG | James Clark's spectest | 100.00% — 965 of 965 |
| DTD | *no public suite* | content models, defaults, `ID`/`IDREF` |
| XSLT 2.0 | W3C xslt30-test, filtered | 99.63% — 6,136 of 6,159 in scope |

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

* XSD schema-validity, at 99.56% (1.0) and 99.18% (1.1). Instance validation —
  what most callers do — is above 99.7% in both. A substantial share of the
  remaining disagreements are cases the W3C's own suite marks as disputed.
* RELAX NG's compact syntax is not implemented; only the XML syntax is.
* A DTD's external subset is never fetched, so validation against one is
  partial by design. `DTD.HasExternalSubset` says when that happened.

### Stability

**This is the 1.0 release: the exported API is now stable.** Every exported
name and signature keeps its meaning, and anything that has to break goes to
2.0 with a new module path.

The surface was reviewed before freezing rather than after, which is the only
time such a review is cheap. `relaxng` had already narrowed from 27 exported
symbols to 7; the same pass over the other packages found an exported mutable
global that any importer could corrupt, a package-scope `All` that named only
derivation methods, three exported fields typed by unexported types, and one
exported function with no callers at all. Those are fixed. A handful of
further narrowings are recorded in the commit log as deliberate non-changes,
because they are judgement calls rather than defects and 1.0 can carry them.

What is *not* frozen by this: the conformance figures, which are expected to
rise; the internal representation behind every interface; and the default
values of the resource limits, which may tighten if an audit finds a bound
that does not bound — three such were found and fixed shortly before this
release.
