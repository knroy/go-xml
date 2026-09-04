# Documentation

* **[Using go-xml in a server](server.md)** — compile once, transform or
  validate per request; a hardened XSD validation endpoint, timeouts, limits,
  and the mistakes that matter under load.
* **[XQuery](xquery.md)** — XQuery 3.1: the two entry points, getting output,
  external variables, options, and what is not implemented.
* **[XQuery plan](xquery-plan.md)** — *historical*: the plan written before
  the work, kept for the design rationale, in particular why the parser reads
  source rather than a token stream.
* **[Validating XML](validation.md)** — what this engine can and cannot check,
  and how to combine it with the pieces it does not provide.
* **[XSD](xsd.md)** — the schema validator in detail: versions, resolvers,
  limits, the PSVI, concurrency, and where conformance stands.
* **[Options](options.md)** — every configuration field in the four packages,
  what its zero value means, and worked examples. Start here when you want to
  know what you can change.
* **[CHANGELOG.md](../CHANGELOG.md)** — every fixed finding, with what was
  wrong, why it mattered, how it was found and what it cost. The other
  documents describe the engine as it is now; this one is where the record of
  getting there lives, so neither has to carry both.
* **[Security](security.md)** — the threat model, what is open today, the
  deliberate resource limits, what is verified safe (XXE, entity expansion,
  regex backtracking), and what a caller must still do. Opens with the current
  status rather than the history; the fixed findings are one line each, with
  the detail in [CHANGELOG.md](../CHANGELOG.md).
* **[Testing](testing.md)** — how the engine is tested, how to run any layer of
  it, what the ratchet is for, and how to read a result without being misled by
  a count that did not move.
* **[Conformance gaps](conformance-gaps.md)** — the current figures, and a
  case-by-case verdict on every failure: fixable, open, or unreachable, with
  the reason. This is where the numbers live.
* **[Known gaps](known-gaps.md)** — why the hard gaps are hard: deliberate
  refusals, the reverted fix attempts and what each one measured, the
  unimplemented rules, and the methodological lessons that cost the most to
  learn. The diagnosis behind the verdicts above. A finding that is merely
  *fixed* is not here — it is in [CHANGELOG.md](../CHANGELOG.md); what stays is
  the reasoning that still guides a decision.
* **[Reaching 100%](reaching-100.md)** — what the remaining distance to a
  perfect score actually consists of, and which parts of it are worth buying.
* **[TODO](todo.md)** — what is left: features, the measured bug tail, and
  the non-goals recorded so they are not proposed again as oversights.
* **[Recipes](recipes.md)** — batch validation, splitting documents, reporting
  line numbers, rendering to HTML, custom document resolvers.

Start with [validation.md](validation.md) if you are here to check documents
rather than to transform them. It begins with what this library is *not*,
which is the part most likely to save you time.
