# Changelog

Notable changes, newest first. Versions follow [semantic
versioning](https://semver.org): from 1.0.0 the exported API is stable, and a
breaking change means 2.0 with a new module path. See *Stability* below.

## Unreleased

### Added

| Change | What it does |
|---|---|
| **The foundation of the XSLT 3.0 §19.8 streamability analysis** | §19 decides whether a construct is *guaranteed-streamable* by inferring a **posture** (`grounded`, `striding`, `climbing`, `crawling`, `roaming`) and a **sweep** (`motionless`, `consuming`, `free-ranging`) for every construct from those of its operands and the **operand usage** (`absorption`, `inspection`, `transmission`, `navigation`) each operand has in it. `XTSE3430` is the refusal of a construct that comes out free-ranging. The **lattice** — §19.3, §19.4, §19.5, §19.7 and the whole of §19.8.1's general rules — is implemented and tested on its own, against the worked examples §19.8.2 gives, in `xslt/streamlattice.go`. On top of it sit the XPath rules of §19.8.8 for paths (both phases, including the scanning-expression reassessment that makes `//x` streamable), axis steps, filters, conditionals, the context item and variable references, plus the §19.8.9 operand-usage table for ~150 built-in function signatures. **The 43 XSLT instruction rules of §19.8.6, streamable stylesheet functions and accumulators, and the union, `for`, quantified, simple-mapping, dynamic-call, inline-function and map rules are not implemented** — roughly 13 of §19's 86 rule sections are covered, deliberately the shared ones. The analysis therefore reports whether it *modelled* every construct it met separately from what it concluded, and `xslt/streamcheck.go` raises `XTSE3430` only when the verdict came entirely from modelled constructs, so an unmodelled construct is "no opinion" rather than a rejection — a missing error costs a failing test, a spurious one rejects a valid stylesheet. Measured against the catalog's own expectations over all 359 `strm` cases naming a stylesheet: 18 now raise the `XTSE3430` they want, 92 still do not, and **0 cases that do not want it now get it**; the whole 456-stylesheet streaming corpus compiles with no spurious refusal. XSLT 2.0 unmoved at 6,193 / 8. The narrow §3.9.1 rule already in `staticerrors.go` is untouched and still independent of this analysis. See [docs/conformance-gaps.md](docs/conformance-gaps.md) § *The §19.8 streamability analysis* for the section-by-section coverage table and what completing it would take. |

### Fixed — engine

| Change | Problem → solution | Commit |
|---|---|---|
| A pattern facet was tested against the source value, not the canonical result | F&O 3.0 §18.3.3 says that where a cast crosses a branch of the type hierarchy the pattern is tested against "the canonical lexical representation of the value", and W3C bug 26865 — which revised `CastableAs653`–`658` twice — settled that this means the **cast result**, in the target's own primitive. The cases carry the rule in their own titles: "Pattern must match canonical representation (not the result of `string()`)". The engine handed the schema the *source* value's `fn:string` form, so `12 castable as d:canonicalDecimal` tested `"12"` against `-?[0-9]+\.[0-9]+` and answered false where the canonical `"12.0"` matches. `fn:string` is XPath's serialization and is **not** XSD's canonical form, and the two disagree exactly here: `string(xs:double(93.7))` is `"93.7"` where the canonical double is `"9.37E1"`, which is why the pattern demanding an `E` refused every one of these. A new `canonicalLexical` in `xpath/cast.go` answers for the numeric primitives alone — `xs:decimal` always carrying a point, `xs:double`/`xs:float` always an uppercase-`E` exponent including for zero — so every other type keeps the lexical form it already had. `xs:integer` is deliberately excluded: F&O §18.3.1 names it a primitive in its own right and §3.3.13.1 makes its canonical form the bare digits, so a pattern written for integers must keep seeing `"12"` and not `"12.0"`. `docs/todo.md` and `docs/conformance-gaps.md` had both predicted this was a facet-application rule inside `xsd`; it is not, and both now say so — validation is handed a lexical form, and the defect was in which form the cast chose to hand it. Fixes `CastableAs653`–`658`. | — |
| A cast to a union's list member returned one item instead of a sequence | F&O 3.0 §18.3.6 makes a cast to a list type a **sequence** — one value per whitespace-separated token, "the effect … is the same as … constructing an element or attribute node whose string value is `S`, validating it using `L` as the governing type, and atomizing the resulting node" — and its own example returns two `xs:integer` values from `my:coordinates("2 -1")`. A union holding a list member is *impure*, so a cast to it is decided by the schema through `SchemaSimpleType` rather than by the pure-union member walk, and that branch returned the operand **unchanged**. So the constructor `s:impureUnionType("1 2 3")` yielded one `xs:string` where it owes three `xs:decimal` values. The count is what the suite pins: a three-item sequence is castable to nothing, which is why `cbcl-castable-impure-010` wants false — and why the `?` on `-020` changes nothing, since neither cardinality admits three items. `SchemaUnionListMemberType` reports the item type of a union's list member, the mirror of the atomic-member interface added beside it, and the cast builds the sequence when a string-like source reaches the list. The atomic members are tried **first**, because a union's members are tried in declaration order: `s:impureUnionType("2001-01-01")` matches the `xs:date` member and stays one item. The source-type rule this depends on is untouched, so `cbcl-castable-impure-005` (`xs:untypedAtomic`, true) and `-009` (`xs:decimal`, false) still split the same way. A different bug from the canonical-form row above despite sharing the cast path; the two failed in opposite directions. Fixes `cbcl-castable-impure-010` and `-020`, taking `prod-CastableExpr` to 946 / 946. | — |
| A schema-defined list or impure union was accepted as an item type | The mirror image of the cast-target row below, and the reason the two belong together: having made an impure union a legal *cast* target, nothing made it an illegal **ItemType**. XPath 3.1 §2.5.4 admits only a *generalized atomic* type — an atomic type or a **pure** union — in `instance of`, `treat as` and a function signature, and §2.5.5 writes union membership as a clause of derives-from for a pure union alone. The purity check covered only the three built-in list types (`xs:NMTOKENS` and friends), so a schema-defined list or an impure or restricted union reached those positions unchallenged and failed later, at the value binding, as `XPTY0004` — a dynamic error for a static defect, and one whose code invites the caller to pass a different value when no value would have helped. The check now also refuses `SchemaListType` and `SchemaSimpleType`, giving `XPST0051`. A **pure** union in the same position still works, which is what makes this a rule about purity rather than about schema types at large; and the cast-target check is deliberately left alone, because §3.14.2 admits any simple type there. Fixes `FunctionCall-032`, `-033`, `-034`, `-039` and five `prod-InstanceofExpr` cases. | — |
| A pure union declared as a return type refused an `xs:untypedAtomic` | §3.1.5 casts an `xs:untypedAtomic` to whatever type is declared, and for a union §3.14.2 defines that cast as trying the member types in order — the machinery for which already existed and was never reached. A union carries no atomic type code of its own, having no single primitive to erase to, so it failed the "is this an atomic type" guard that stands in front of the conversion and was refused before the cast owed to it could be attempted. `declare function local:makeDate($in as xs:string) as lu:unionOfUnionType { … xs:untypedAtomic($in) … }` was `XPTY0004` on a value the rules exist to convert. Fixes `FunctionCall-037` and `-038`, both of which assert the result is an `xs:date`. | — |
| A namespace-sensitive union converted where §3.1.5 forbids it | Casting an `xs:untypedAtomic` to `xs:QName` means resolving whatever prefix its string carries, and the only bindings in scope at a call are the callee's — which have nothing to do with where the value was written. §3.1.5 therefore excludes the case outright and gives it `XPTY0117`, a distinct code precisely because no value the caller could have written would have succeeded. The test for it asked only whether the declared type *was* `xs:QName`, which a union hides: it has no atomic type code, so a union over `xs:date`, `xs:QName` and a numeric union looked insensitive and was converted. The check now walks the union's members. Fixes `FunctionCall-041`. | — |
| A C0 control was delivered into a text node under XML 1.1's rules (XPath) | `fn:json-to-xml` decided whether a codepoint could be carried literally with `isXMLChar`, the XML **1.1** `Char` production, where every C0 control but NUL is legal — because 1.1 lets a document write it as a character reference. A JSON string has no such escape hatch: the string *is* the text node's value, so XML 1.0 is the applicable production. `escape=true` emitted a raw U+0001 where `\u0001` was owed, and `escape=false` a raw U+0008 where U+FFFD was. A JSON-local predicate now decides; the shared `isXMLChar` is untouched, since its other callers concern documents, where 1.1 does admit these. Fixes `json-to-xml-045`. | — |
| The built-in schema for the XML representation of JSON was a paraphrase (XSD) | `xsd/jsonschema.go` was commented "§C.2 verbatim" and was not: `j:boolean` was declared `type="xs:boolean"` where F&O 3.1 §C.2 gives it a named `j:booleanType`, so a validated boolean came back annotated with the built-in rather than the schema's own type. Replaced with a faithful copy, restoring `j:finiteNumberType`, the six named `*WithinMapType` types and the `xs:anyAttribute` wildcards with it. The package's own test had pinned the wrong annotation as expected behaviour, with a comment rationalising it, which is why the defect survived being tested. Fixes `json-to-xml-047`. | — |
| A simple-content type's derivation skipped its named base (XSD) | An element test matches a node whose type is *derived from* the named one (XPath 3.1 §2.5.5.3), which the assembler records as a chain. For complex content it registered the step to the named complex base; for simple content it registered the type the value *atomises* as, jumping straight past the schema's own type to the built-in. `j:stringWithinMapType` → `j:stringType` → `xs:string` was recorded only as `→ xs:string`, so a validated map's child answered false to `element(fn:string, fn:stringType)` while its complex-content siblings answered true. The named base is now registered when there is one, and its own registration continues to the built-in, so atomisation still resolves one link later. Fixes `json-to-xml-046`. | — |
| `fn:json-to-xml` with `validate:true()` could not validate (XQuery) | `xsd.SchemaForJSON` has carried the F&O 3.1 §C.2 schema all along, and `xslt` has installed it as an `xpath.TreeValidator` since `xslt/jsonvalidate.go` — the tree has to go out to the schema layer because `xpath` cannot import `xsd`, the dependency running the other way for the XPath inside assertions and selectors. `xquery` never wired that hook, so every query asking for validation got the `FOJS0004` §17.5.3 reserves for a processor that *cannot* validate. It is installed in `prepare` the way `xslt` installs it in `runtime.go`, unconditionally — whether the processor can validate is a property of the processor — and a caller that supplied its own validator keeps it. Fixes `json-to-xml-017b`, `-037b`, `-038b`, `-044` and both `xml-to-json` cases; `fn-xml-to-json` reaches 100%. | — |
| The QT3 harness could not supply a schema named only by an `at` hint | `qischema041` and `qischema083` import a namespace no `<environment>` declares, naming its documents inline. The harness reads those files itself and assembles them with `xsd.LoadFiles`; `Options.SchemaResolver` stays nil, so the engine still never opens an `at` hint and the run remains evidence that nothing is fetched on a query's say-so. Two details only the cases show: one namespace may span two documents, and `Options.Schemas` keeps the *first* registration per namespace, so separate entries silently drop one; and one document may `xs:import` the other, which only an assembly with a base can follow. The driver also now recognises `http://www.w3.org/2005/xpath-functions` from the copy the suite ships, which its own catalog comments as something "either the test driver or the product under test is expected to recognize". | — |
| Casting to a schema-defined union raised instead of answering | The purity rule of XPath 3.1 §2.5 was gating cast targets as well as item types. It belongs to the ItemType question alone: `instance of` and a function signature must answer from a value's own annotation, and a member of a faceted union does not necessarily satisfy that union's facets — the XSD 1.0 error XSD 1.1 §3.16.6.3 corrected. §3.14.2 admits **any** simple type in the in-scope schema types as a cast target, and a cast has the lexical form in hand, so the union's facets can actually be checked. `castable as s:impureUnionType` was `XPST0003` where `cbcl-castable-impure-001` asserts `true`. `SchemaSimpleType` now marks a target the schema decides; the ItemType positions refuse an impure union exactly as before. `docs/todo.md` §1.5 had recorded this as a *deliberate* joint refusal shared with `xslt` — that reasoning was wrong, and the entry now says so. | [`6654bac`][6654bac] |
| A schema import was installed too late for the prolog's own declarations | The imports were followed at the *end* of the prolog, which is early enough for the query body but not for a function signature — a signature is parsed where it stands, so `declare function local:f($a as s:dateOrDateTime)` one line below the import defining that type was `XPST0051`. Each import is now followed where it is read, keeping one loader for the prolog so each import consults the resolver exactly once. §4.11 requires imports to precede variable and function declarations, so no declaration can see an import it should not. Fixes `Castable-UnionType-36`–`38`. | [`6654bac`][6654bac] |
| A cast to a union validated the operand rather than the result | A cast *converts*; validation asks whether a value is already in a type's lexical space. `castToUnion` put the operand's `"123.12"` to the schema after the member cast had already produced `"123"`, so `123.12 cast as s:myUnionType1` raised `FORG0001` where `CastAs-UnionType-3` expects the integer `123`. | [`6654bac`][6654bac] |
| A union member's own facets were skipped by a shortcut | An item whose type code already matched a member's was returned untouched, which is right only when the schema has nothing further to say. A member that is a *restriction* carries facets its type code cannot express, so `"AD123456789"` — a perfectly good `xs:string` — cast to a union over a pattern-restricted `xs:string` succeeded where `CastAs-UnionType-5a` requires `FORG0001`. The shortcut is now taken only when no schema validation is attached. | [`6654bac`][6654bac] |
| A union's list member was reachable from a non-string source | F&O 3.0 §18.3 defines the cast to a list type from `xs:string` and `xs:untypedAtomic` only, so a source that is neither may reach a union's **atomic** members and no others. Validation alone cannot see this: it is handed a lexical form, and `"1"` is a valid one-item list of decimals whatever produced it. This is the difference between `cbcl-castable-impure-005` (`xs:untypedAtomic("1 2 3")`, true) and `-009` (`xs:decimal("1")`, false) against the *same* union. `SchemaImpureUnionTypes` reports a union's atomic members without requiring purity, and the source's own type decides. | [`6654bac`][6654bac] |
| `validate` in tail position evaluated to the empty sequence | `validateExpr.eval` computed its result and threw it away, so `validate lax {…}` as the last expression of a query yielded nothing. Pre-existing and invisible, because `validate strict` always raised before reaching it. | [`73d547b`][73d547b] |
| `type=` naming an attribute declaration was accepted (XSD) | `<xsd:element name="myElem" type="foo"/>` beside `<xsd:attribute name="foo"/>` loaded clean. §3.3.2 requires `type=` to resolve to a *type definition*, and this resolves to a component of the wrong kind. It was hidden by the deferral §3.3.3 grants an element declaration: the unprefixed name lands in the absent namespace, `deferrableMiss` answers true, and the reference was carried on the declaration instead of reported. That latitude exists because a document read later might supply the type — but no document can turn an attribute declaration into one, so the miss is final when it is made. `resolveTypeRefLazy` now reports a name the assembly defines as a non-type before consulting the deferral; `saxonData Missing/missing001`, whose `type="absent"` names nothing at all, keeps its deferral and still loads. Fixes `MS-Element/elemM002` on both versions. | [`830ae11`][830ae11] |
| `keyref refer=` reached a key its document never imported (XSD) | `schema.identityConstraints` is one flat map over the whole assembly, so the `refer=` fixup could resolve against a key the asking document has no licence to see. §4.2.6.1 `src-resolve` scopes an `<xs:import>`'s licence to the document that wrote it, which is why `doc.imports` already existed beside the per-assembly set — the fixup was not consulting it. `idC017a.xsd` targets `diffNS` and writes `refer="keyName"` unprefixed with no default namespace in scope, so §3.11.2 resolves it to the *absent* namespace; the only such key belongs to the importing document, which `idC017a.xsd` does not import. `resolveQName`'s own import check cannot catch it, because an unprefixed name with no default namespace returns early through `chameleonQName`. The fixup now captures its declaring document and requires the key's namespace to be that document's own or one it imports; a key in the referrer's own namespace, or in an imported one, still resolves. Fixes `MS-IdentityConstraint/idC019` on both versions. | [`830ae11`][830ae11] |
| Typed mode unchecked under a built-in rule | XTTE3100/XTTE3110 were applied where an `xsl:apply-templates` selected a node, and nowhere else. §6.7.3 writes the shallow-copy built-in rule out as a template whose body is literally `<xsl:apply-templates select="@*"/>` and `<xsl:apply-templates select="node()"/>`, and §2.3.3 defines the initial match selection as "the processing then corresponds to the effect of the `xsl:apply-templates` instruction" — so both are selections the error reaches. The check moved into `applyToNode`, which every selection path funnels through, covering the initial selection, the built-in descents and the array-member unwrapping at once. `mode-1438` is a stylesheet that is nothing but `<xsl:mode typed="yes" on-no-match="shallow-copy"/>`: it has no `xsl:apply-templates` of its own, and the document node it starts from carries no annotation to object to, so only the descent into the first element reaches the error. Fixes `mode-1438`. | [`bc72bed`][bc72bed] |
| `typed="false"`, `"0"` and `"unspecified"` read as their opposite | `@typed` is `boolean \| "strict" \| "lax" \| "unspecified"`, and a boolean in this language is any of yes/no, true/false, 1/0. `checkModeTyped` tested only the literal `"no"`, so every other way of writing it fell to the affirmative branch and asserted the reverse of what it said; `"unspecified"`, which asserts nothing, did the same. The bug was unreachable while the check ran only on explicit `xsl:apply-templates` — `mode-1445` (`typed=" false "`), `mode-1446` (`typed="0"`) and the `unspecified` cases all reach a mode through the built-in rules alone — so widening the check's reach in the row above is what exposed it. The capitalised `"No"` stays an error: `mode-1447` writes it and expects `XTSE0020`. | [`bc72bed`][bc72bed] |
| JSON-nested HTML wrote the wrong content-type meta | `json-node-output-method="html"` serializes nested nodes through the minimal serializer in `xpath`, which declared the encoding as the HTML5 `<meta charset="UTF-8">` on the belief that the suite accepts either spelling. It does not: `output-0716` requires the serialization to match `<head>…<meta http-equiv=\"Content-Type\"` and `output-0702` the same with a `content` attribute, and neither regex admits a `charset` attribute in its place. The full serializer in `xslt` had always written the `http-equiv` form, so the two were also disagreeing about the same element. The namespace test was widened to match the full serializer's as well — under the html method every element is HTML by definition, and `output-0702` builds its `<head>` under a default `xmlns` of the XHTML namespace — while still leaving a `<head>` of some other vocabulary alone. Fixes `output-0702` and `output-0716`. | [`bc72bed`][bc72bed] |
| `fn:function-lookup` hidden from `xsl:evaluate` | §10.4.1 keeps the XSLT-defined functions out of the target expression's **static** context, and `restrictedLibrary` applied that to every lookup. But §10.4.2 says of the dynamic context that "all other aspects […] are the same as the dynamic context for the `xsl:evaluate` instruction itself", and its own note takes for granted that `fn:document` is reachable there: "a processor may disallow access using the `doc` or `document` functions to documents in local filestore" would have nothing to disallow otherwise. F&O 16.1.1 resolves `fn:function-lookup` against "the named functions component of the dynamic context" and leaves the outcome implementation-defined where the static context lacks the name. `restrictedLibrary` now implements `LookupDynamic` without the static hiding, so a dynamic lookup finds `fn:document` while a call written out in the expression stays `XTDE3160` — the split `evaluate-047` and `evaluate-048` assert either side of. The stylesheet's own private functions stay hidden either way. This is the library defect `evaluate-048` turns on, but the case still fails: its assertion fetches `https://www.saxonica.com/...`, and nothing is fetched here without a resolver. Fixed, not evidenced. | [`bc72bed`][bc72bed] |
| C0 controls serialized raw | `#x1`–`#x1F` fell past every arm of the escaper into a plain rune write, producing output the serializer could not parse back. The version now decides: 1.1 writes character references, 1.0 raises `SERE0006`. | [`a45c3a6`][a45c3a6] |
| XML declaration hardcoded `1.0` | `xsl:output/@version="1.1"` was parsed and never reached the declaration. Also mapped `xsl:result-document/@output-version`, which §3.5 renames and nothing read. | [`a45c3a6`][a45c3a6] |
| Arrays dropped from constructed content | An `*xdm.ArrayItem` matched neither arm of a two-arm type switch, so `('a',[1,2],'b')` yielded `"a b"` — members lost from mid-sequence. `xdm.Flatten` was already correct and simply never called. | [`be2938e`][be2938e] |
| Arrays rejected by `xsl:sequence` and `xsl:apply-templates` | The same family as the row above, on the two paths it missed. `xsl:sequence` handed the array to the builder, which raised `XTDE0450` for every non-node non-atomic item under an open element; `xsl:apply-templates` dispatched the array as one opaque item, whose built-in rule produced nothing. `XTDE0450` is worded against a *function item* (§5.7.1), and an array is not one: content construction flattens it. The builder now flattens an array under an open element only, so a top-level `as="array(*)"` still holds an array; the array's built-in template rule applies templates to its members, leaving an explicit rule matching the array itself (`square-array-019`) to win. Fixes `arrays-301/302/304/305` and `output-0713/0714/0715`. | [`f536984`][f536984] |
| Constructed trees sorted before parsed ones | `Node.Compare` read a treeless node's tree id as the zero value, so every node a sequence constructor built sorted ahead of every parsed document: `(/BOOKLIST/BOOKS/ITEM/PRICE union $insertion)` came back with the variable's elements first. `Order()` already biased constructed roots above tree ids by a fixed `1<<20`, but a fixed offset is only right until a run overruns it — the xslt30 suite reaches tree id 1272658, which is why the same cases passed when their test-set ran alone and failed in a full run. Detached roots now draw from the counter the parser draws tree ids from, so the two kinds are ordered by when each was made, with no offset to overrun. Fixes `sx-union-012/017/022/035` and `si-fork-118`. | [`f536984`][f536984] |
| Current group survived a streamed invocation | §14.4 says an invocation construct written inside a declared-streamable construct sets the current group and the current grouping key to absent; only the merge group was being cleared. `xsl:call-template` and `xsl:apply-templates` now clear the grouping scope when they are lexically inside `xsl:source-document`/`xsl:stream` or an `xsl:attribute-set`, `xsl:function`, `xsl:merge`, `xsl:accumulator` or `xsl:global-context-item` declaring `streamable="yes"` — after the select expression and the parameters are evaluated, which still see it. Fixes `si-fork-113/114/115`. | [`f536984`][f536984] |
| `match="."` rejected below version 3.0 | The XSLT 3.0 `PredicatePattern` was gated on the module's version, so a `version="2.0"` module writing `match="."` got `XTSE0340`. It now follows the processor's version, as the `$v` pattern form already did for the same reason; the placement rule follows it, so `".|a"` and `"(.)"` are `XTSE0340` at both versions rather than quietly compiling to the other alternative. Fixes `si-fork-115`. | [`f536984`][f536984] |
| Namespace-node identity | The axis synthesizes a node per walk, so `is` compared two fresh pointers and answered false for one binding. `Node.Is` now defers to `Order()`; set operators key on `IdentityKey` to agree. Only `KindNamespace`, since parentless nodes of other kinds share order zero. | [`d15b6df`][d15b6df] |
| Library-module variable scope | Bodies were checked against a flat pool of every loaded module's globals, so a module could read `$foo:test` having imported no `foo` (§4.12). | [`d15b6df`][d15b6df] |
| Decimal formats crossed modules | `fn:format-number` in a library module resolved format names against the importing query's prolog rather than its own (§4.4). | [`d15b6df`][d15b6df] |
| One namespace, several modules | `Options.Modules` keyed on namespace alone, so a second registration replaced the first instead of contributing alongside it. | [`d15b6df`][d15b6df] |
| DTD typos disabled validation | A misspelt `#REQUIRED` was read as the default value `"#REQUIRE"`, silently demoting a required attribute to optional. Now recorded as invalid and reported. | [`96171c5`][96171c5] |
| `namespace=""` matched everything | An empty wildcard spelling was defaulted to `##any`, admitting every element (Part 1 §3.10.2). | [`39f7174`][39f7174] |
| Annotations lost on copy | `xsl:result-document` carried a validation's annotation back by name only, dropping `UnionMember`, `DerivedPrimitive` and `ListItem`. | [`39f7174`][39f7174] |
| Optional `<all>` read as a range | Reading a disjunction as a range let an invalid instance validate. | [`39f7174`][39f7174] |
| XML 1.1 documents | Read as 1.1: `[2] Char` and `[2a] RestrictedChar` per version, `NEL` and U+2028 as line ends, and an external entity's version checked against the including document (§4.3.4). | [`2cc633e`][2cc633e] |
| `fn:transform` entry points | `initial-function` and `function-params` were unimplemented and silently dropped, leaving the inner stylesheet with no entry point — the cause of issue #4's intermittent `XTDE0044`. | [`e049991`][e049991] |
| Included-document errors | An error from an included schema did not say which document it came from. | [`9a41bea`][9a41bea] |
| `fn:current-output-uri()` | Reported the stylesheet's own location instead of the output's. | [`bd0aaf5`][bd0aaf5] |
| `xsl:result-document` with no `href` | Produced no output at all from the CLI; it names the principal output. | [`6c8405c`][6c8405c] |
| `validation="lax"` demanded a schema | XTSE1660 names what a non-schema-aware processor must refuse and lax is not among it — "a value other than strip, preserve, or lax". Lax with nothing to validate against leaves the node untyped; strict still errors. | [`f536984`][f536984] |
| `xsl:inherit-namespaces` ignored on a literal result element | Implemented for `xsl:element` and `xsl:copy` only, so the blocking pass never ran on an LRE and no namespace undeclaration was ever owed. | [`f536984`][f536984] |
| `xsl:copy` with `copy-namespaces="no"` broke an XDM invariant | The copied element carried no namespace node for its own prefix, so `in-scope-prefixes()` answered `("xml")` where `("gml","xml")` was required. §5.8.3 fixup applies whether or not the source's namespace nodes were copied; `xsl:element` and `xsl:copy-of` already did it. Invisible in the output — the serialiser writes the declaration a name needs regardless — and visible only on the namespace axis. | [`ea4681f`][ea4681f] |
| `xsl:function` accepted a streamability it cannot have | XTSE3155 was unimplemented: a function with no `xsl:param` children may only declare `streamability="unclassified"`, since the other classifications describe how a function consumes its streamed argument and one taking none cannot be any of them. The rule holds with no §19 analysis behind it, which is why a non-streaming processor can enforce it. | [`9113ac4`][9113ac4] |
| A result-document href absolute on another platform | `filepath.IsAbs` answers for the host, so `C:/out.xml` was refused on Windows and, on Unix, quietly made a directory named `C:` inside the result directory — the same stylesheet writing somewhere on one operating system and erroring on the other. Now refused textually on every platform: leading slash, drive letter, or UNC. | [`28699a9`][28699a9] |
| `xsl:source-document` ignored `xml:base` | The href resolved against the stylesheet module rather than the expression's static base URI. | [`f536984`][f536984] |
| A Windows filename parsed as a URI scheme | `c:\my\doc\books.xml` was read as scheme `c`; backslashes are not legal URI characters on any platform, so it is FODC0005. | [`f536984`][f536984] |
| `fn:transform` codes and options | FOXT0001 where the options simply identify no stylesheet (QT3 reserves it for a requested *product* being unavailable); an option written as element content refused as a text node; an href-less `xsl:result-document` treated as secondary. | [`f536984`][f536984] |
| Four unchecked or unread attributes | `exponent-separator` accepted but never mapped into the format, XTSE0730 unimplemented, `xsl:attribute-set/@streamable` absent from the table, and `xsl:` attribute *values* on a literal result element never checked. | [`f536984`][f536984] |

### Fixed — bounds and budgets

The governing rule: *a resource budget may decline to answer, but must never
turn "I could not prove the constraint" into "the constraint holds."*

| Change | Problem → solution | Commit |
|---|---|---|
| Uncompilable content models skipped every constraint on them | A model that would not compile passed silently rather than declining. | [`b6fb5ab`][b6fb5ab] |
| A budget answered "valid" | Exhausting the budget was reported as success instead of as an inability to decide. | [`2c461c7`][2c461c7] |
| Budgets counted the wrong thing | A bound over the wrong quantity is not a bound. | [`8dcc4dc`][8dcc4dc] |
| Last unbudgeted load-time algorithm | Now bounded; `Options.MaxContentModelPositions` makes the position budget host-tunable. | [`81e6ee5`][81e6ee5] |
| Substitution closure unbounded | Bounded, along with the pairwise overlap test it fed. | [`1b027e5`][1b027e5] |

### Fixed — test harness

A harness defect and an engine defect are not the same finding: these changed
what the suites *measured*, not what the library does.

| Change | Problem → solution | Commit |
|---|---|---|
| Four stale feature labels | `streaming`, `streaming-fallback`, `XPath_3.1` and `XML_1.1` sat in `unsupportedFeatures` long after they were implemented, hiding 2,862 XSLT cases. Deleting an entry is not enough — it must *move* to `supportedFeatures` or fall through to "unknown feature". | [`be2938e`][be2938e] |
| Two more stale labels | `namespace-axis` and `infoset-dtd`; the two harnesses had been contradicting each other. | [`a820213`][a820213] |
| Dependencies merged additively | A set's `satisfied="true"` outlived a case's `"false"`, so all fourteen `fn-load-xquery-module-901..914` were excluded by the declaration they exist to contradict. Now per (type, value) — the per-kind alternative was measured and is worse (0/0/0/17 → 1/2/2/23 failures). | [`d15b6df`][d15b6df] |
| `SerializeAsXML` forced 1.0 | Tree assertions compared against the truncated prefix of a discarded error. The version, unlike method and indentation, decides whether a character can be written at all. | [`a45c3a6`][a45c3a6] |
| Ratchet read a shrinking corpus as a regression | A count taken over fewer roots is not comparable; it is now skipped, not passed, when a root is absent. | [`3b6e685`][3b6e685] |
| Cases never scored went uncounted | A case that is never scored must still appear in the denominator. | [`c3a52be`][c3a52be] |

### Investigated — not defects

| Finding | Verdict | Commit |
|---|---|---|
| `system-property('xsl:supports-streaming')` answers "no" | Correct, and must stay: §26.5 says a processor that does not conform "must return the value no". The engine builds a tree, and reporting "yes" would lie to stylesheets that branch on it to pick a fallback. | [`f536984`][f536984] |
| `merge-097`, `-097s`, `-097sf` | Not interoperable, per the test set's own maintainer comment — they rely on Saxon's `?select=` collection URIs and declare no environment for the harness to honour. | [`f536984`][f536984] |
| `transform-004` | Needs `fn:transform` during the static phase, which deadlocks on a non-reentrant compile mutex — confirmed from a stack trace, not inferred. | [`f536984`][f536984] |
| Unrecognised `fn:transform` options | Ignoring them is correct: `fn-transform-48` is titled "…unrecognised option which is ignored" and asserts success. | [`e049991`][e049991] |
| Two XSD 1.1 "false accepts" | The suite's, not ours. | [`7c4bef2`][7c4bef2] |
| DocBook 5.0's XSD refused | The schema is genuinely invalid. | [`c8fc839`][c8fc839] |
| 22 MS-Regex false accepts | One rule — the same one the XSLT suite exercises. | [`7c4bef2`][7c4bef2] |
| Three "gaps" in XML 1.0 5th-edition names | Not gaps: 1.0 5e adopted 1.1's name productions verbatim. | [`83148b7`][83148b7] |
| `op:same-key` canonical key | Two canonical keys disagree by design; the recurring Gregorian "fix" is wrong. | [`b6fb5ab`][b6fb5ab] |

### Added

| Feature | Notes | Commit |
|---|---|---|
| XQuery `import schema` | Schema import per XQuery 3.1 §4.11 — the last structural gap in `xquery`, which until now parsed the declaration and refused it by name with `XQST0059`. All three prefix forms including `default element namespace`. Components reach the static context *before the body parses*, because XQuery resolves type names while parsing whereas the module loader runs after: `cast`/`castable` apply the schema's facets rather than the base type alone, `instance of`, `element(*,T)` and `schema-element(E)` resolve, and `validate` strict/lax/`type T` is assessed and annotated. `Options.SchemaResolver` is nil by default and nothing is read without one, sharing the budget and the resolver with the schema's own `xs:include`/`xs:import`. QT3 XQuery in scope 29,930 → 30,346, passing 29,918 → 30,143; XPath 3.0 and 3.1 each gained 60 in-scope cases at 0 failures. The 203 failures are newly *admitted* cases, not lost ones — the original 12 are still exactly those 12 — and they are the five features §1.5 of the todo records as deliberately left. | [`73d547b`][73d547b] |
| DTD external subsets | `dtd.Load` reads the second half of a DTD. Nothing is fetched by default and the refusal is loud; bounds are shared across subsets, not per-subset. | [`b6fb5ab`][b6fb5ab] |
| RELAX NG compact syntax | `relaxng.CompileCompact`. | [`b6fb5ab`][b6fb5ab] |
| XQuery `import module` | Module import per XQuery 3.1. A cycle of imports is not an error. Nothing is fetched by default — `Options.ModuleResolver` is nil in the zero value — and `MaxModules` refuses rather than truncates. | [`b6fb5ab`][b6fb5ab] |
| RELAX NG §7.3 | Was refusing seven of nine real-world schemas; `<ref>` expansion is no longer shared between references. | [`7c4bef2`][7c4bef2] |

## v1.2.2 — 2026-09-05

Security and the honesty of the numbers. Measured with `tests/check.sh`:
XPath 2.0, 3.0 and 3.1 all at 100%, XQuery 3.1 at 99.99% (29,800 of 29,803),
XSLT 2.0 at 99.87% (6,149 of 6,157), XSLT 3.0 at 99.85% (8,612 of 8,625),
XSD 1.0 at 99.90% (39,347 agreeing) and 1.1 at 99.91% (41,532), RELAX NG at
100% (965 of 965). DocBook xslTNG 577 of 593 and XSpec 225, with 1,435 unit
tests and 6 fuzz targets clean under `-race`. No conformance figure moved in
either direction: every change here was made without spending a case.

### Headline

| Change | Detail | Commit |
|---|---|---|
| Four silent wrong answers are closed | A schema loaded with a zero-value `xsd.Options` read whatever an `xs:include` named, `/etc/hosts` included. INF and NaN passed every facet a schema could write | — |
| Two bounds did not bound | The regular-expression caches were bounded against a single goroutine and not against several, peaking at 2,984 entries against a limit of 1,024. `FileResolver.Preload` wrote past its own bound | — |
| The documentation is now checked by the gate | Nine conformance marks were already machine-enforced and all nine were correct; the unit test count, which nothing checked, was wrong in four files at once | — |
| One limitation is now written down rather than absent | `checkUPA` is the only load-time algorithm with no budget: its cost is cubic in the size of a content model, and 116 KB of legal schema takes 12 seconds | — |

### Fixed — CI

| Change | Detail | Commit |
|---|---|---|
| The `w3cschemas` module was testing a published release, not this tree | A separate module — the W3C documents it bundles are under W3C terms rather than MIT — and it was in no gate at all: absent from `ci.yml`, absent from `tests/check.sh`, and out of reach of `go list ./...` at the root | — |
| Investigated, not changed: the `XSpec 225` ratchet mark is correct | Reported as stale on the grounds that the corpus measured 224. Three runs — twice at `HEAD`, once at [`e51ed3f`][e51ed3f] — each transformed 225 of 284 inputs with a byte-identical set of 59 failures, so the mark stands | — |
| A unit test that cost minutes broke both CI jobs | `TestMaxPositionsRealBoundary`, added in [`84735c8`][84735c8], compiled three content models of ~8,192 particles to drive `maxPositions` at its edges | — |
| `TestQT3` and the RELAX NG spectest are now ratcheted | Both were run and printed but not recorded, so an XPath 2.0 or RELAX NG count could fall without `check.sh` saying anything | — |

### Security

| Change | Detail | Commit |
|---|---|---|
| Two caches with no bound | A bound on a cache is a property of the cache, so every path that writes to one has to obey it; these two did not | — |
| Those cache bounds held only on one goroutine | The fix above, and the two bounded caches it left in place, shared an idiom that is not atomic as a group: read an atomic size counter, clear the `sync.Map` wholesale if it is full, then `LoadOrStore` the new entry | [`6567f8e`][6567f8e] |
| `xsd` no longer installs an unconfined file resolver by default | `Load`, `LoadFile`, `LoadFiles` and `WithInstanceLocations` each defaulted a nil `Options.Resolver` to a `FileResolver` with no `Root` — "any readable path", by that field's own comment | — |

### Documentation

| Change | Detail | Commit |
|---|---|---|
| `validation-0201` was failing for a reason no document named | Five places across three documents said the case's last remaining difference was the indent width — Saxon's 3 spaces against this serializer's 2 — and §20's implementation-defined latitude. Measurement says otherwise: setting the width to 3 gains **nothing** (6,193 passed / 8 failed either way; the reported offset merely moves from 46 to 134). Strip all whitespace and the two sides are byte-identical, so the difference really is whitespace placement, but it is a *second*, independent one — inside `<head>` we write a newline before `<style>` and Saxon does not, because Saxon declines to indent before an element whose own content is significant text. Ours is the parent's call (`hasTextChild(<head>)` is false, so `indentChildren` stays on), and it is permitted: Serialization 3.1 §5 constrains where a serialiser *may not* indent, not where it must. The verdict — implementation-defined, not ours — is unchanged; the reason for it is now the true one, and pinned by a test. The case is the fourth time this one has failed for a reason underneath the reason last recorded, and this time the paragraph warning about exactly that had itself fallen into it | — |
| Four claims that the code had already outgrown | Each described behaviour that changed under it, and three of the four understated the risk or overstated what is refused | — |
| A retention claim overstated what was measured | `docs/security.md` read "2,000 distinct schema loads ... show **0.00 MB** heap growth after GC". That holds only when the schemas reuse type names | — |
| Numbers and counts that had drifted apart between documents | An audit compared every stated conformance figure against `tests/ratchet.txt`, which `tests/check.sh` enforces, and every stated count against the list it introduces | — |

### Fixed

| Change | Detail | Commit |
|---|---|---|
| `INF`, `-INF` and `NaN` no longer satisfy every bound facet | `checkBounds` compared with `big.Rat` and read a lexical with no rational form as "no opinion", returning success: with `xs:double` constrained to `0..100`, `101` was correctly refused while `INF`, `-INF` and `NaN` were all accepted | — |
| A derived type could widen a bound to `INF` and the schema still loaded | `compareBoundValues` returned "unordered" for a bound with no rational form and `checkBoundOrder` reads unordered as satisfied, so a restriction that widened its base went unnoticed | [`a883c0a`][a883c0a] |
| A validated `xs:double` that overflowed stopped being a double | Building a typed value from a schema annotation parsed the lexical form with `strconv.ParseFloat` and treated any error as "not a lexical form of this type" | — |
| A processing instruction in an internal DTD subset broke the subset scan | XML 1.0 §2.8 admits a PI to `intSubset` — `markupdecl` is "elementdecl \| AttlistDecl \| EntityDecl \| NotationDecl \| PI \| Comment" — and §2.6 makes its content text rather than markup | — |

### Changed

| Change | Detail | Commit |
|---|---|---|
| A resource refusal can be told apart from a malformed expression | New `xdm.ErrResourceLimit` is a sentinel that resource-exhaustion errors wrap with `%w`, so a caller can ask `errors.Is(err, xdm.ErrResourceLimit)` | — |

### Fixed

| Change | Detail | Commit |
|---|---|---|
| A grammar reached through `<include>` was not checked against section 7 | `collectInclude` (`relaxng/compile.go`) ran `checkSyntax` on the included document but not `checkRestrictions`, unlike the top-level and `<externalRef>` paths, which run both | — |
| Four silent numeric narrowings: a wrong value, with no error raised | `big.Int.Int64` is *undefined* out of range rather than saturating, so an unbounded `xs:integer` argument arrived as its low 64 bits and the result was computed from that — and in each case the wrapped value was itself plausible, so nothing failed | — |
| `fn:round-half-to-even` clamped its precision, which changed answers | The precision argument was clamped to ±4096 before use, which silently changed the answer rather than refusing the request | — |
| A backreference pattern was refused for having too many groups | The backreference path rejected any pattern declaring more than 64 capturing groups with `FORX0002`, reachable from `fn:matches`, `fn:replace`, `fn:tokenize` and `fn:analyze-string` | — |
| `keyref` rediscovered its targets once per enclosing scope | A `keyref` on a self-embedding element walked the whole remaining subtree once per level: nodes visited grew 3.92x, 3.96x, 3.98x as depth doubled, so a hostile instance against a recursive schema cost quadratic work | [`e125888`][e125888] |
| Thirteen derivation walks stopped at 32 or 64 steps | Twelve stopped at 32 and one at 64, each walking a type's derivation chain or a node's copy lineage: five in `xdm`, five in `xpath`, two in `xslt` | [`eb5ea72`][eb5ea72] |
| Nine node-copy sites each hand-picked which type properties to carry | `xdm.Node` records seven PSVI properties — `TypeAnnotation`, `UnionMember`, `DerivedPrimitive`, `ListItem`, `IsID`, `IsIDREFS`, `IsNilled` — and every place that copied a node wrote its own field list | — |
| A second schema silently retyped a document the first had validated | `xdm` keyed `derivedPrimitives`, `listItems` and `unionMembers` by QName alone, process-wide. Mutexes make that race-free, not isolated: the last schema to register a name wins | [`eb5ea72`][eb5ea72] |
| A circular type longer than 4096 links loaded clean | Reachable from a hostile *schema*, not from an instance. `checkTypeBaseCycles` walks a global type's base chain looking for a return to itself, stopped at `steps < 4096`, and appended **no** error on running out — the permissive verdict | [`ad2c3dc`][ad2c3dc] |
| RELAX NG refused a legal chain of 501 definitions | The mirror image of the same mistake, failing in the other direction. `maxRefDepth = 500` was a hard refusal, so a legal chain of 501 definitions became uncompilable with "recurses more than 500 deep" when nothing recursed | — |
| A permitted file was read whole with no byte limit | `readConfined` ended in a bare `io.ReadAll`, so every path through the filesystem resolver read a file entirely into memory before anything could refuse it: `fn:doc`, external entities, `fn:unparsed-text`, and XInclude `parse="text"` | — |
| An ambiguous key came back at three siblings | `mergeTables` dropped a key sequence that two children both defined, because an ancestor's keyref cannot say which of them it resolves to | [`28e455a`][28e455a] |
| The language-inclusion procedure declined any bound above 64 | The subsumption procedure fell back to the structural XSD 1.0 rules above `maxOccurs="64"`, which can refuse a restriction whose language really is a subset | — |
| Six base-chain counters were defects, two of them false accepts | Reachable from a schema rather than from an instance. Eleven remaining `seen > 64` and `seen > 256` counters had been recorded as sound on evidence that did not cover them | — |
| Occurrence arithmetic is exact, not saturating | Saturating arithmetic fixed an earlier wrap and left two bounds above `occursHuge` comparing equal, so a base of 1e30 restricted by three members of 1e30 was accepted | [`f88747b`][f88747b] |
| Identity constraints are linear in the document, not quadratic | Doubling the depth now doubles the work rather than quadrupling it, measured at 2.00x across 240, 480 and 960 where it was 3.98, 3.99 and 4.00. The fix is not a cheaper traversal | [`277599e`][277599e] |
| A 3 KB schema took 35 seconds to load, in two places | Reachable from a hostile schema. A group referencing the next one twice, 29 times over, is acyclic and valid and fits in 3.0 KB; loading it took 35.8 seconds | — |
| Occurrence arithmetic wrapped negative | `occursHuge` is a quarter of the int range, so it survives doubling but not tripling; the derivation checks multiply and sum bounds until they wrap negative | [`145d0d1`][145d0d1] |
| An assertion rejected a valid document 33 elements deep | `maxAnnotateDepth = 32` bounded the walk that types an element and its descendants before an XSD 1.1 assertion runs | [`145d0d1`][145d0d1] |
| Six walks stopped at depth 32 and accepted documents the schema forbids | Reachable from a schema, not from an instance: a deployment with a trusted schema and untrusted documents cannot reach it | [`3f3cce3`][3f3cce3] |
| An iteration that matches nothing is still an iteration | A sweep of 2,028 combinations of outer bounds, inner bounds and child count found 40 still wrong after the count-vector rewrite — every one a false rejection | [`0048fde`][0048fde] |
| A negative `xsd.ValidateOptions.MaxErrors` approved invalid documents | `fail()` stopped recording once `len(v.errs) >= opts.MaxErrors`, with no guard on the limit being positive; at `MaxErrors = -1` the first comparison already held, so nothing was ever recorded | — |
| Filesystem confinement is enforced when the file is opened | `resolvePath` called `EvalSymlinks`, compared against the roots, and the file was opened later — a window an attacker who can write to the filesystem could use | [`5964c0a`][5964c0a] |
| The resolver no longer serialises cache misses | `loadTracked` held its mutex across `os.ReadFile` and the parse, so concurrent transforms sharing one resolver loaded modules one at a time whenever the cache was cold | — |
| The largest byte limit a caller can name is not a refusal | `ParseOptions.MaxBytes` and `xsd.HTTPResolver.MaxBytes` wrap the reader in `io.LimitReader(r, max+1)`, one byte over so that hitting the limit is distinguishable from a document exactly at it | [`f0ffb5b`][f0ffb5b] |
| A schema-aware stylesheet panicked on every run | `ValidateContext` gave `validator` a `ctx` field and a cancellation check on the validation walk; `validateNodeAgainstType` builds the same struct without one, so every schema-aware run dereferenced nil | — |
| An XInclude copy dropped the union member on attributes | `copySubtree` carried `UnionMember` on the element and dropped it on the element's attributes — an inconsistency inside one function, and the same half-omission that silently untyped a validated document at three copy sites elsewhere | [`30dc68d`][30dc68d] |
| XSD: nested occurrence bounds are decided exactly | A repeated group whose only child is itself repeating was decided wrongly in *both* directions — false accepts and false rejections from the same arithmetic | [`17ce36c`][17ce36c] |
| A timezone name was read off a saturated instant | `applyPlace` (`xpath/fn_misc.go`) has to put a value on the timeline before it can ask an Olson zone which offset applied at that moment, and it took the second count with `new(big.Float).SetRat(utc).Int64()` | — |
| The `[0,60)` second invariant is written down instead of re-derived | The `[s]` and `[f]` components of `fn:format-dateTime` each split `dt.Second` into a whole part and a fraction, narrowing the whole part to `int64` | — |

### Changed

| Change | Detail | Commit |
|---|---|---|
| Every configurable limit is now tested at its edges | An off-by-one or an overflow at the boundary of a caller-settable limit is precisely what a unit test should catch before an auditor does, and nothing covered the edges of any limit before | [`2ef8dba`][2ef8dba] |
| `endOfInternalSubset` read comment text as structure | It tracked quotes and brackets but had no comment state, and XML 1.0 §2.8 permits comments in the internal subset while §2.5 says their content is not markup | — |
| `xdm`'s own limits did not carry `xdm.ErrResourceLimit` | The package that *defines* the sentinel, and documents it as how a caller tells "the processor declined" from "your input is wrong", applied it to none of its own limits | — |
| `MaxBytes` did not bound a UTF-16 document at all | `ParseOptions.MaxBytes` is documented as bounding the source document, and the code says the limit "wraps the reader, so it bounds what is read rather than what a caller remembered to check" | — |

## v1.2.1 — 2026-09-03

Conformance, correctness, and the honesty of the numbers reporting them.
Measured with `tests/check.sh`: XPath 2.0, 3.0 and 3.1 all at 100%, XQuery
3.1 at 99.99% (29,800 of 29,803), XSLT 2.0 at 99.87% (6,149 of 6,157), XSLT
3.0 at 99.85% (8,612 of 8,625), XSD 1.0 and 1.1 at 99.90% and 99.91%
agreeing, RELAX NG at 100%. DocBook xslTNG 577 of 593 and XSpec 225, with
1,095 unit tests clean under `-race`.

**The Go floor is lowered to 1.25**, from 1.26, so this builds on one more
toolchain than v1.2.0 did. It is measured rather than nominal: `regexp`
learned the Unicode category `Cn` in 1.25, and building on 1.24 costs four
conformance cases -- the dependency is on standard-library behaviour, not on
a symbol, which is why a local run with a newer toolchain installed could not
see it.

| Change | Detail | Commit |
|---|---|---|
| A private function of a used package is not callable from outside it | `use-package-003` asked for it and this file had recorded it as needing "the package threaded through the XPath static context", the single largest structural change on the list | — |
| An instruction in 1.0 compatibility mode inside a streamable mode | `XTSE3430`. Section 3.9.1 states the rule "notwithstanding anything stated in 19 Streamability": an instruction processed with XSLT 1.0 behavior *is* roaming and free-ranging | [`7b0562a`][7b0562a] |
| The QT3 per-case deadline was measuring the runner | CI reported XQuery 29,799 passing in one run and 29,798 in another, for the same commit, minutes apart, and the ratchet correctly called the second a regression | — |
| Verdicts that were re-derived and found wrong | `validation-0201` was filed as fixable in the harness -- the suite does license a driver to "ignore differences in the serialization that are known to be irrelevant", and the case is not a serializer test | — |
| Also in this release | Two ratchet marks go **down** and neither is a regression. XSD 1.0 fell from 39,353 to 39,347 and XSD 1.1 rose from 41,525 to 41,532: `indeterminate` expectations stopped being scored as "must be invalid" | — |
| system-property('xsl:product-version') was answering 0.1 | It was a constant nobody edited, so it said `0.1` through the 1.0, 1.1 and 1.2 releases -- and a stylesheet dispatching on it, which is the only reason section 18.2 defines the property, got an answer three tags out of date | — |
| -allow-dir says where, not what | The flag's help named only `xsl:include` and `document()`. It also governs `xsl:import`, `fn:doc`, `fn:unparsed-text`, external entities and XInclude -- one root list for every reader, each of the riskier ones gated by its own flag on top | — |
| An indeterminate expectation is not a demand to reject | `<expected validity="indeterminate"/>` prescribes no result. The W3C uses it where the working group left an area underspecified: `schZ012_a`'s annotation says "The WG decided the spec. is underspecified in this area | [`704222f`][704222f] |
| An XSLT 3.0 static expression can read a document | The static phase built its evaluation context with no document resolver, so `fn:doc` in a `use-when`, a `static="yes"` variable or a shadow attribute always failed with `FODC0002: document access is disabled` | [`a3ec25e`][a3ec25e] |
| The gate no longer runs the conformance suites twice | `tests/check.sh` ran its `unit tests` and `race` steps without `GOXSLT_NO_SUITES`, so in an environment that has `testdata/` on disk — which is exactly the conformance job | — |
| A base URI is a URI, not a path | `fn:static-base-uri` and `fn:base-uri` returned the filesystem path the file was read from — `C:\Users\m\s.xsl` on Windows | — |
| XInclude | `xdm.ProcessXInclude` implements XML Inclusions (XInclude) 1.0, Second Edition, as a pass over an already-parsed tree — which is what the specification says it is: §4 defines XInclude as a transformation from one infoset to another | — |
| One scanner for what is not syntax (internal) | XQuery's parser decides which sub-parser reads an expression by scanning ahead over raw source, and that scan has to step over the regions whose bytes look like grammar but are not: string literals, comments (which nest) | — |
| Internal: a compiled XQuery expression can no longer be evaluated unsafely | No behaviour changes and no conformance movement; this removes the shape of a bug rather than an instance of one | [`b21f5eb`][b21f5eb] |
| The declared XQuery version is recorded | `parseVersionDecl` used to read `xquery version "1.0";`, check the literal against a list of three, and throw it away | [`78f70d5`][78f70d5] |
| XQuery conformance: 99.61% to 99.98% | 29,796 of 29,803 QT3 cases in scope, up 107 across seven passes. XPath 2.0/3.0/3.1 stay at 100% and XSLT at 8,606 / 6,149 | — |
| Fixed — found by real-world stylesheets | Measured against [DocBook xslTNG](https://github.com/docbook/xslt3ng) and [XSpec](https://github.com/xspec/xspec), two XSLT 3.0 codebases large enough to exercise combinations the W3C suites do not reach. 577 of DocBook's 593 test documents now | — |

## v1.2.0 — 2026-09-02

XQuery 3.1 from 99.07% to 99.61% of the QT3 suite, and the second host
language reached parity: constructors, FLWOR, the prolog, try/catch, switch,
typeswitch and windows. `fn:transform` was added, so a stylesheet can run a
stylesheet. Three bugs that DocBook xslTNG and XSpec found and the W3C suites
did not were fixed with them.

### generate-id() must tell apart the nodes of a built tree

Reported from the field: a Schematron schema transpiled by SchXslt2 raised
`XTDE3365` on a duplicate map key, in this engine *and* in Saxon, from a
stylesheet this engine had generated. Both failing on the same generated file
is what said the error was correct and the stylesheet producing it was not.

`generate-id()` is built on `Node.Order()`, which combines a tree identity
with the node's document-order index. A tree assembled by a sequence
constructor is never finalized, so every node under one root still carried
the index `0` it was built with: the identity told two trees apart, and
nothing told the nodes within one tree apart. SchXslt2 keys a map on
`generate-id()` of each `sch:assert` and `sch:report`, so two distinct nodes
collided. `TestOrderDistinguishesUnfinalizedNodes` guards it.

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
| XSLT 3.0 | — | **99.79%** |
| XSD 1.0 schema / instance | 99.56% / 99.88% | **99.86% / 99.88%** |
| XSD 1.1 schema / instance | 99.18% / 99.89% | **99.88% / 99.89%** |
| RELAX NG (spectest) | 100% | **100%** |

**XSLT 3.0 is the headline.** v1.0.0 shipped XSLT 2.0; this release adds
packages (`xsl:package`, `use-package`, `accept`, `expose`, `override`),
accumulators, `xsl:evaluate`, `xsl:iterate`, `xsl:merge`, `xsl:try`, maps and
arrays, higher-order functions, JSON, and the 3.0 serialization methods.
Streaming is not implemented, and its 2,646 cases are out of scope rather than
failing — though measured with that gate lifted, 92% of them pass anyway,
because §19.1 lets a processor answer a request for streamed evaluation by
building the tree.

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

| Change | Detail | Commit |
|---|---|---|
| Security: three bounds that were not bounding | A third audit. Full detail in [docs/security.md](docs/security.md) | — |
| A constraint that never ran against the ordinary spelling | Unique Particle Attribution and Element Declarations Consistent were checked only against a schema's *named* complex types | [`e967628`][e967628] |
| Circularity, and the exception that terminates every chain | Schema validity reaches 99.51% on XSD 1.0 and 99.11% on 1.1 — both at the ceiling this project's own analysis predicted, with the remainder dominated by tests the W3C's metadata disputes | — |
| The harness claimed two mutually exclusive processor configurations | XSD 1.1 schema validity reaches 99.00%, and 10 of the 22 tests gained were never a validator defect. `tests/xsdsuite` claimed support for both `restricted-xpath-in-CTA` and `full-xpath-in-CTA` | [`176ce57`][176ce57] |
| Conditional type assignment, all-groups, and open content | Four rules, +12 on XSD 1.1 with XSD 1.0 untouched | [`176ce57`][176ce57] |
| XSD 1.1 wildcard attributes | Schema validity reaches 99.47% on XSD 1.0 and 98.85% on 1.1. `namespace` and `notNamespace` on a wildcard are mutually exclusive — they are two spellings of one `{namespace constraint}` property | — |
| Restricting xs:anySimpleType, and a contravariant wildcard rule | Schema validity reaches 99.45% on XSD 1.0 and 98.78% on 1.1. `<xs:restriction base="xs:anySimpleType"/>` is now rejected | [`01b91ba`][01b91ba] |
| A valid document was being refused because an assertion crashed | XSD 1.1 defines the default collection in the dynamic context of an assertion or type alternative as the empty sequence | [`da1cde6`][da1cde6] |
| Schema-validity: 99.40% on XSD 1.0, 98.72% on 1.1 | A third round, +29 on 1.0 and +38 on 1.1 with nothing lost. Five rules | — |
| Instance validation reaches 99.88% on XSD 1.0 and 99.89% on 1.1 | Instance disagreements fall from 48 to 31 on 1.0 and from 51 to 29 on 1.1, with no schema-side regression. Nine rules, each measured on its own | [`bb803d5`][bb803d5] |
| Schema-validity: 98.60% to 99.19% on XSD 1.0, 97.96% to 98.48% on 1.1 | A second round adds src-redefine 6.2.2 and 7.2.2 (section 4.2.2): a `<group>` or `<attributeGroup>` redefined without a self-reference must be a valid restriction of what it replaces | — |
| Schema-validity, first round: the constraint that never ran | 122 schema documents that were accepted despite being invalid are now rejected, with no instance test and no other suite moving | — |
| Entity replacement text inside an attribute value is included literally | XML 1.0 section 4.4.5, "Included in Literal": a reference inside an attribute value has its replacement text included *as literal characters*, so a quote in that text is data and does not end the attribute | [`e2606cc`][e2606cc] |
| EXSLT `node-set` | `{http://exslt.org/common}node-set` is available to stylesheets. XSLT 2.0 eliminated the result-tree-fragment type (section J.1.2), so on this processor the conversion is the identity on its argument | — |
| Fixed: a multi-digit backreference to an unclosed group was renumbered | In the backtracking matcher, `\10` written inside the tenth group was split into `\1` followed by a literal `0` rather than being rejected | — |
| Backtracking regular expressions, off by default | `xpath.SetBacktrackingRegex(true)`, or `-backtracking-regex` on the command line, enables a matcher for the backreferences RE2 cannot express: variable-width groups, backreferences in the middle of a pattern, alternation and lazy quantifiers | — |
| XSLT 1.0 backwards-compatible behaviour | `[xsl:]version="1.0"` now enables the behaviour XSLT 2.0 section 3.8 and XPath 2.0 section B.1 define for it, instead of raising XTDE0160. Argument coercion takes the first item of a sequence where 2.0 raises a type error | [`22d2d64`][22d2d64] |
| XSLT 2.0 conformance: 98.83% to 99.61% over a scope that grew | 6,024 of 6,052 in scope, up from 5,982 of 6,053. 43 tests fixed across two rounds, no regressions. XSD 1.1 gained one instance test (26,158 of 26,209); XSD 1.0, QT3 and RELAX NG are unchanged | [`600e7c0`][600e7c0] |

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

## Before v0.1.0

Unversioned work, recorded at the time under headings of its own. Filed
here so every entry in this file sits under a release.

| Change | Detail |
|---|---|
| Four more exact values narrowed without a range proof | The audit that produced the nine fixes in [`cc17983`][cc17983] found four further sites of the same family in the date, duration, and cast paths |
| fn:format-date no longer claims the Julian calendar | `supportedCalendar` accepted `OS` alongside `AD`, `ISO` and the default. `OS` is Old Style — the Julian calendar — not a third spelling of the Gregorian one |
| xslt: a lossy grouping key merged distinct integers into one group | `xsl:for-each-group group-by=` found a group by hashing the key value to a string and looking it up in a map. For an `xs:integer` or `xs:decimal` that string is `xpath.GroupingKey`, which formats the value through a `float64` |
| xslt: system-property and its neighbours truncated a sequence argument | `stringArg` read `atoms[0]` of whatever it was given, so `system-property(('xsl:version','xsl:vendor'))` answered `"3.0"` as though the second item had not been written |
| xpath: [Y] on a BCE year is correct, and is now pinned | `format-date`/`format-dateTime` with `[Y]` renders `xs:dateTime( "-1000000-06-15T12:00:00Z")` as `1000000`, with no minus |
| xsd: an identity field typed as a union compared spellings, not values | Identity-constraint equality is defined on values. `keyString` already builds a type-tagged canonical form for every field before the sequence is joined, so `3.0` and `3` collide as one `xs:decimal`, `007` and `7` as one `xs:integer` |


[0048fde]: https://github.com/knroy/go-xml/commit/0048fde
[01b91ba]: https://github.com/knroy/go-xml/commit/01b91ba
[145d0d1]: https://github.com/knroy/go-xml/commit/145d0d1
[176ce57]: https://github.com/knroy/go-xml/commit/176ce57
[17ce36c]: https://github.com/knroy/go-xml/commit/17ce36c
[1b027e5]: https://github.com/knroy/go-xml/commit/1b027e5
[22d2d64]: https://github.com/knroy/go-xml/commit/22d2d64
[277599e]: https://github.com/knroy/go-xml/commit/277599e
[28699a9]: https://github.com/knroy/go-xml/commit/28699a9
[28e455a]: https://github.com/knroy/go-xml/commit/28e455a
[2c461c7]: https://github.com/knroy/go-xml/commit/2c461c7
[2cc633e]: https://github.com/knroy/go-xml/commit/2cc633e
[2ef8dba]: https://github.com/knroy/go-xml/commit/2ef8dba
[30dc68d]: https://github.com/knroy/go-xml/commit/30dc68d
[39f7174]: https://github.com/knroy/go-xml/commit/39f7174
[3b6e685]: https://github.com/knroy/go-xml/commit/3b6e685
[3f3cce3]: https://github.com/knroy/go-xml/commit/3f3cce3
[5964c0a]: https://github.com/knroy/go-xml/commit/5964c0a
[600e7c0]: https://github.com/knroy/go-xml/commit/600e7c0
[6567f8e]: https://github.com/knroy/go-xml/commit/6567f8e
[6c8405c]: https://github.com/knroy/go-xml/commit/6c8405c
[704222f]: https://github.com/knroy/go-xml/commit/704222f
[78f70d5]: https://github.com/knroy/go-xml/commit/78f70d5
[7b0562a]: https://github.com/knroy/go-xml/commit/7b0562a
[7c4bef2]: https://github.com/knroy/go-xml/commit/7c4bef2
[81e6ee5]: https://github.com/knroy/go-xml/commit/81e6ee5
[830ae11]: https://github.com/knroy/go-xml/commit/830ae11
[83148b7]: https://github.com/knroy/go-xml/commit/83148b7
[84735c8]: https://github.com/knroy/go-xml/commit/84735c8
[8dcc4dc]: https://github.com/knroy/go-xml/commit/8dcc4dc
[9113ac4]: https://github.com/knroy/go-xml/commit/9113ac4
[96171c5]: https://github.com/knroy/go-xml/commit/96171c5
[9a41bea]: https://github.com/knroy/go-xml/commit/9a41bea
[a3ec25e]: https://github.com/knroy/go-xml/commit/a3ec25e
[a45c3a6]: https://github.com/knroy/go-xml/commit/a45c3a6
[a820213]: https://github.com/knroy/go-xml/commit/a820213
[a883c0a]: https://github.com/knroy/go-xml/commit/a883c0a
[ad2c3dc]: https://github.com/knroy/go-xml/commit/ad2c3dc
[b21f5eb]: https://github.com/knroy/go-xml/commit/b21f5eb
[b6fb5ab]: https://github.com/knroy/go-xml/commit/b6fb5ab
[bb803d5]: https://github.com/knroy/go-xml/commit/bb803d5
[bc72bed]: https://github.com/knroy/go-xml/commit/bc72bed
[bd0aaf5]: https://github.com/knroy/go-xml/commit/bd0aaf5
[be2938e]: https://github.com/knroy/go-xml/commit/be2938e
[c3a52be]: https://github.com/knroy/go-xml/commit/c3a52be
[c8fc839]: https://github.com/knroy/go-xml/commit/c8fc839
[cc17983]: https://github.com/knroy/go-xml/commit/cc17983
[d15b6df]: https://github.com/knroy/go-xml/commit/d15b6df
[da1cde6]: https://github.com/knroy/go-xml/commit/da1cde6
[e049991]: https://github.com/knroy/go-xml/commit/e049991
[e125888]: https://github.com/knroy/go-xml/commit/e125888
[e2606cc]: https://github.com/knroy/go-xml/commit/e2606cc
[e51ed3f]: https://github.com/knroy/go-xml/commit/e51ed3f
[e967628]: https://github.com/knroy/go-xml/commit/e967628
[ea4681f]: https://github.com/knroy/go-xml/commit/ea4681f
[eb5ea72]: https://github.com/knroy/go-xml/commit/eb5ea72
[f0ffb5b]: https://github.com/knroy/go-xml/commit/f0ffb5b
[f536984]: https://github.com/knroy/go-xml/commit/f536984
[f88747b]: https://github.com/knroy/go-xml/commit/f88747b
[6654bac]: https://github.com/knroy/go-xml/commit/6654bac
[73d547b]: https://github.com/knroy/go-xml/commit/73d547b
