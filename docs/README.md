# Documentation

* **[Using go-xml in a server](server.md)** — compile once, transform per
  request; timeouts, limits, and the mistakes that matter under load.
* **[Validating XML](validation.md)** — what this engine can and cannot check,
  and how to combine it with the pieces it does not provide.
* **[XSD](xsd.md)** — the schema validator in detail: versions, resolvers,
  limits, the PSVI, concurrency, and where conformance stands.
* **[Options](options.md)** — every configuration field in the four packages,
  what its zero value means, and worked examples. Start here when you want to
  know what you can change.
* **[Security](security.md)** — the threat model, what is verified safe (XXE,
  entity expansion, regex backtracking), what a caller must still do, and the
  findings from the audit that produced them.
* **[Known gaps](known-gaps.md)** — every measured failure and why it is still
  open: deliberate refusals, the two engine limits with their reverted fix
  attempts, and the unimplemented rules.
* **[TODO](todo.md)** — what is left: features, the measured bug tail, and
  the non-goals recorded so they are not proposed again as oversights.
* **[Recipes](recipes.md)** — batch validation, splitting documents, reporting
  line numbers, rendering to HTML, custom document resolvers.

Start with [validation.md](validation.md) if you are here to check documents
rather than to transform them. It begins with what this library is *not*,
which is the part most likely to save you time.
