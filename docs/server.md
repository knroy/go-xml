# Using go-xml in a server

The whole design rests on one property: **`Compile` is expensive, `Transform`
is not, and a compiled `Stylesheet` is immutable.** Compile each rule set once
at startup and share it across every request and every goroutine.

Measured on the Oman PINT rule set: compilation is ~0.9 ms and a transform is
~1.0 ms. Recompiling per request roughly doubles your cost and throws away the
one thing the API is shaped to give you.

## A complete validator service

This compiles, runs, and has been load-tested at 1,200 concurrent requests.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// Validator holds one compiled rule set. It is safe for concurrent use:
// Transform does not mutate the Stylesheet.
type Validator struct{ sheet *xslt.Stylesheet }

func NewValidator(stylesheetPath, rulesDir string) (*Validator, error) {
	src, err := os.ReadFile(stylesheetPath)
	if err != nil {
		return nil, err
	}
	tree, err := xdm.ParseString(string(src), xdm.ParseOptions{})
	if err != nil {
		return nil, fmt.Errorf("parsing stylesheet: %w", err)
	}
	// The resolver confines xsl:include and document() to one directory.
	// Without it both fail closed, which breaks rule sets that load code lists.
	res, err := xslt.NewFileResolver(rulesDir)
	if err != nil {
		return nil, err
	}
	// One permitted file is still a file the stylesheet chose. MaxBytes
	// bounds each read -- 64 MB by default, lowered here because a rule set
	// and its code lists are small and anything larger is a mistake or an
	// attack. A file over the limit is refused, not truncated.
	res.MaxBytes = 8 << 20
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{Resolver: res})
	if err != nil {
		return nil, fmt.Errorf("compiling stylesheet: %w", err)
	}
	return &Validator{sheet: sheet}, nil
}

// ErrMalformed separates "your document is broken" from "our rules are broken".
var ErrMalformed = errors.New("document is not well-formed XML")

func (v *Validator) Validate(ctx context.Context, doc []byte) (*xslt.Result, error) {
	tree, err := xdm.ParseString(string(doc), xdm.ParseOptions{
		TrackPositions: true, // so the report can name a line
		// The body is already bounded below, but MaxNodes is the limit
		// that matters: a node costs a fixed ~200 bytes whatever it
		// holds, so a megabyte of "<a/>" is fifty times the heap of a
		// megabyte of text. A byte cap alone does not bound memory.
		MaxBytes: 4 << 20,
		MaxNodes: 200_000,
		MaxDepth: 200,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return v.sheet.Transform(ctx, tree.Root, xslt.TransformOptions{
		Documents: nil, // doc()/document() disabled unless a resolver is set
	})
}

func main() {
	v, err := NewValidator("rules/rules.xslt", "rules")
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("POST /validate", func(w http.ResponseWriter, r *http.Request) {
		// Bound the body. An unbounded read is the easiest way to be OOM'd.
		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}

		// Bound the transform. A pathological document can otherwise occupy
		// a goroutine indefinitely; cancellation is checked at every loop.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		res, err := v.Validate(ctx, body)
		switch {
		case errors.Is(err, ErrMalformed):
			// The client's fault: 400, and say what was wrong.
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		case errors.Is(err, context.DeadlineExceeded):
			http.Error(w, "validation timed out", http.StatusGatewayTimeout)
			return
		case err != nil:
			// Our fault: the rules failed to run. Log it and page someone.
			log.Printf("rule set failed: %v", err)
			http.Error(w, "internal validation error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/xml")
		_ = res.Serialize(w)
	})

	srv := &http.Server{
		Addr:              ":8080",
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
```

## What matters under load

### Compile once — really once

```go
// ✅ at startup
v, _ := NewValidator("rules/rules.xslt", "rules")

// ❌ per request: ~0.9 ms and a large allocation, thrown away every time
func handler(w http.ResponseWriter, r *http.Request) {
    sheet, _ := xslt.Compile(tree.Root, opts)   // don't
}
```

Concurrency is tested rather than asserted: the suite shares one compiled
stylesheet *and one parsed source tree* across goroutines under `-race`.
Sharing the tree is the stronger claim, since it means evaluation never writes
back into the document.

### Always set a timeout

`Transform` takes a `context.Context` and checks it at every loop boundary. A
2-second deadline interrupts a 9-million-iteration expression at 2.0 s. Without
one, a stylesheet with an accidental cross-product occupies a goroutine until
the process dies.

### Bound the request body

`io.LimitReader` is the fix. Parsing is roughly 61 MB/s and allocates about
2.6× the document size as a node tree, so a 100 MB upload is ~260 MB resident
before any rules run.

### Reuse a parsed tree when validating against several rule sets

Parsing dominates when rule sets are small:

```go
tree, err := xdm.ParseString(src, xdm.ParseOptions{TrackPositions: true})
if err != nil { return err }

for _, sheet := range v.sheets {          // parse once
    res, err := sheet.Transform(ctx, tree.Root, opts)
    ...
}
```

This is safe for the same reason concurrency is: `Transform` treats the source
as read-only.

### Hot-reloading rule sets

A `Stylesheet` is immutable, so swapping one is a pointer store. Guard it with
`atomic.Pointer` rather than a mutex — readers then never block:

```go
type Validator struct{ sheet atomic.Pointer[xslt.Stylesheet] }

func (v *Validator) Reload(path, rulesDir string) error {
    s, err := compile(path, rulesDir)   // build the new one first
    if err != nil {
        return err                      // keep serving the old one
    }
    v.sheet.Store(s)                    // atomic swap
    return nil
}
```

Compile the replacement *before* storing it. A rule set that fails to compile
should leave the service running on the previous version, not take it down.

---

## Validating XML against a schema in a server

The XSLT service above transforms; this one **validates**. It is the shape to
copy when you accept XML from outside and need to know whether it conforms to a
schema you control.

Everything below is compiled and exercised — including against XXE and
billion-laughs — before being written here.

### The rule

**Every default in this library is already the safe one.** You are not
hardening a permissive parser; you are choosing limits that fit your documents.
The two settings that matter most are the ones you do *not* change:
`AllowDOCTYPE` stays off, and no `HTTPResolver` is configured.

### Load the schema once, at startup

```go
// A *Schema is immutable and safe to share across every request and every
// goroutine. Loading per request would dominate the cost of validating.
//
// FileResolver.Root confines every schemaLocation to one directory, so an
// import cannot reach elsewhere on disk. No HTTPResolver, so nothing is
// fetched over the network — the schema graph is exactly what you shipped.
schema, err := xsd.LoadFile("schemas/main.xsd", xsd.Options{
    Version:      xsd.Version11,
    Resolver:     &xsd.FileResolver{Root: "schemas"},
    MaxDocuments: 64,
    // Needed only if your schema graph contains a DOCTYPE, as UBL's does.
    // This is a file you ship, not caller input; see the note below.
    ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true},
})
```

`AllowDOCTYPE` here applies to **your schema files**, which you control. It
must not be set on the parse of an incoming document.

### Size the limits to your documents

```go
const (
    maxRequestBytes = 4 << 20 // 4 MB of XML
    maxNodes        = 200_000 // an invoice is thousands of nodes, not millions
    maxDepth        = 200     // real business documents nest tens deep
    maxErrors       = 50      // enough to fix a document, not a DoS amplifier
    requestTimeout  = 10 * time.Second
)
```

The defaults (64 MB, 10M nodes, depth 1000) are sized for a general-purpose
library. If you know you are receiving invoices rather than archives, these are
far tighter and cost you nothing.

`maxNodes` is the one that actually bounds memory. A node costs ~200 bytes
whatever it holds, so heap follows node count, not byte count — and the ratio
between them ranges from 1.9x to 53x depending on document shape. A byte cap
alone does not bound your memory.

### The handler

```go
func (s *server) handle(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "POST an XML document", http.StatusMethodNotAllowed)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
    defer cancel()

    // Bound the read itself. MaxBytesReader makes the limit the server's,
    // not the client's Content-Length claim.
    body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
    if err != nil {
        http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
        return
    }

    // Parse with every default left closed: no DOCTYPE, so no entity
    // expansion and no external entity; explicit caps on size, nodes, depth.
    tree, err := xdm.ParseString(string(body), xdm.ParseOptions{
        MaxBytes: maxRequestBytes,
        MaxNodes: maxNodes,
        MaxDepth: maxDepth,
    })
    if err != nil {
        writeResult(w, http.StatusBadRequest, "not well-formed", []string{err.Error()})
        return
    }

    // Annotate is deliberately off: it writes type annotations into the tree,
    // which would make this handler mutate state shared across goroutines.
    err = s.schema.Validate(tree.Root, xsd.ValidateOptions{
        MaxErrors: maxErrors,
        MaxDepth:  maxDepth,
    })
    if err == nil {
        writeResult(w, http.StatusOK, "valid", nil)
        return
    }

    // ValidationErrors is the collection; ValidationError is one failure.
    var verr *xsd.ValidationErrors
    if errors.As(err, &verr) {
        msgs := make([]string, 0, len(verr.Errors))
        for _, e := range verr.Errors {
            msgs = append(msgs, e.Error())
        }
        writeResult(w, http.StatusUnprocessableEntity, "invalid", msgs)
        return
    }
    writeResult(w, http.StatusInternalServerError, "error", []string{err.Error()})
}
```

Note the status codes. **400 is "not XML", 422 is "XML but not conforming".** A
caller can act on that difference: the first is a bug in their serialiser, the
second is a bug in their data.

### What this refuses, measured

Each of these was run against the handler above:

| input | result |
|---|---|
| valid invoice | `200 {"status":"valid","errors":[]}` |
| `<total>NOT</total>` | `422` with `cvc-datatype-valid.1` and the path |
| truncated document | `400 not well-formed` |
| **XXE** — `<!ENTITY x SYSTEM "file:///etc/passwd">` | `400 DOCTYPE declaration rejected` |
| **billion laughs** — nested entity expansion | `400 DOCTYPE declaration rejected` |
| 5,000-deep nesting | `400 nesting exceeds 200 levels` |
| 500,000 nodes | `400 document exceeds 200000 nodes` |

Both entity attacks are stopped at the same place, before any expansion is
attempted, because the DOCTYPE never gets parsed. That is why `AllowDOCTYPE`
defaults off and why it must stay off for caller input.

### Checklist

- [ ] Schema loaded **once** at startup, shared across goroutines
- [ ] `FileResolver{Root: ...}` set — never a bare `FileResolver{}` for
      caller-influenced locations
- [ ] No `HTTPResolver` unless you need one, and then with `AllowHost`
- [ ] `AllowDOCTYPE` **off** for request bodies (on for your own schemas only
      if they need it)
- [ ] `MaxBytes`, `MaxNodes`, `MaxDepth` sized to your documents
- [ ] `MaxBytesReader` bounding the read, not just the parse
- [ ] `Annotate` off, or a fresh tree per goroutine if on
- [ ] Server-level `ReadTimeout`/`WriteTimeout`/`MaxHeaderBytes` set
- [ ] 400 and 422 distinguished in the response

## A document from another timezone

A service in Amsterdam validating a document written in Riyadh is the case that
breaks quietly, because nothing errors: a date with no timezone is a *different
value* from the same reading with one, and the two are compared through an
**implicit timezone** the processor supplies. Get it wrong and a document is
accepted or rejected for a reason no one can see in the payload.

Measured against this library, with a schema whose facet is
`minInclusive="2026-01-01T00:00:00Z"`:

| Instance value | Verdict |
|---|---|
| `2026-01-01T02:00:00` (no timezone) | **valid** |
| `2026-01-01T02:00:00+03:00` (Riyadh) | **invalid** |
| `2025-12-31T23:00:00Z` (the same instant) | **invalid** |

The first and second rows are the same wall-clock reading and get opposite
answers; the second and third are the same *instant* and agree. That is correct
— a value without a timezone is not a point on the world's timeline until one is
chosen — but it means an unzoned `02:00` from Riyadh is silently read as 02:00
UTC, three hours earlier than it was written.

### The rule

**Require the timezone rather than inferring it.** XSD 1.1 has a facet for
exactly this, and it is the only approach that does not depend on a server
setting:

```xml
<xs:simpleType name="Timestamp">
  <xs:restriction base="xs:dateTime">
    <!-- explicitTimezone="required" rejects "2026-01-01T02:00:00" outright,
         so no implicit timezone is ever consulted. XSD 1.1 only; the
         built-in xs:dateTimeStamp is xs:dateTime with this facet already
         applied, and is the shorter way to say the same thing. -->
    <xs:explicitTimezone value="required"/>
  </xs:restriction>
</xs:simpleType>
```

```go
schema, err := xsd.LoadFile("schemas/invoice.xsd", xsd.Options{
    Version: xsd.Version11, // explicitTimezone is a 1.1 facet
})
```

A document that omits the offset is then refused with `cvc-datatype-valid.1`
naming the facet, which is a diagnosable error rather than a wrong answer. Where
you control the schema, stop here — the rest of this section is for when you
do not.

### Where the implicit timezone actually applies

It matters that this is **not** an `xsd.ValidateOptions` field, and that is
deliberate rather than an omission:

* **Facet comparison** (`minInclusive`, `maxExclusive`, `enumeration`) resolves
  an unzoned value against **UTC**, fixed. It is not configurable, because a
  schema is a contract: the same document and schema must produce the same
  verdict on every machine, and a validator whose answer depended on the host's
  locale would not be one.
* **XSD 1.1 assertions and conditional type alternatives** evaluate XPath, and
  that XPath context also uses UTC.
* **XSLT and XPath** *are* configurable, because a transform is a computation
  rather than a contract — `fn:current-date()`, `xsl:sort` over dates and
  `xsl:for-each-group` on a date key all consult it:

```go
// Riyadh is UTC+03:00. Offsets are in MINUTES east of UTC, so +3h is 180.
res, err := sheet.Transform(ctx, tree.Root, xslt.TransformOptions{
    ImplicitTimezone: 3 * 60,
})
```

Never derive that from the server's clock. `time.Local` on the Amsterdam host
is the *host's* zone, it moves twice a year with daylight saving, and it makes
the same input produce different output on two machines in a pool. Take it from
the document, the tenant, or the request — never from the environment:

```go
// The offset belongs to the data, not to the process. A tenant record or an
// explicit request field is auditable; time.Local is not.
tz, ok := tenantOffsetMinutes[req.TenantID]
if !ok {
    http.Error(w, "unknown tenant", http.StatusBadRequest)
    return
}
```

### Checklist

* Prefer `xs:dateTimeStamp`, or `explicitTimezone="required"`, and never rely on
  an implicit timezone for data that crosses a border.
* Leave XSD facet comparison alone. It is UTC everywhere, on purpose.
* Set `ImplicitTimezone` explicitly on every `TransformOptions` where dates are
  sorted, grouped or compared — the zero value is UTC, which is a *choice* worth
  making deliberately rather than inheriting.
* Do not use `time.Local`. A pool of servers must not disagree, and daylight
  saving must not change a verdict.
* If you accept unzoned values, log the offset you applied. A verdict that
  depended on an assumption should say which assumption.

---

## Security posture

Every remote-reference mechanism is off unless you turn it on, but the
defaults deserve stating explicitly for a server:

| | default | to enable |
|---|---|---|
| `DOCTYPE` in input | **rejected** — the XXE and entity-expansion vector | `ParseOptions.AllowDOCTYPE` |
| `fn:doc` / `document()` | **refused** — every URI | `TransformOptions.Documents` |
| `xsl:include` / `import` | **refused** | `CompileOptions.Resolver` |
| network access | **none, at all** | not available |
| `xsl:result-document` | returned on `Result.Secondary`, never written to disk | you write them |

Resource limits are on by default too, and a server should tighten them to what
its documents actually look like rather than leave the general-purpose numbers:

| | default | why |
|---|---|---|
| `ParseOptions.MaxBytes` | 64 MB | bounds one read |
| `ParseOptions.MaxNodes` | 10,000,000 | bounds the tree, ~2 GB — the limit that actually binds |
| `ParseOptions.MaxDepth` | 1000 | bounds parser stack use |
| `ValidateOptions.MaxDepth` | 1000 | bounds validator stack use, separately |
| `TransformOptions.MaxDepth` | 1000 | catches a template with no base case |
| `ValidateOptions.MaxErrors` | 100 | bounds the report, not the work |

`MaxNodes` is the one to think about. A node costs a fixed ~200 bytes whatever
it holds, so a byte cap is a loose bound on memory: a megabyte of `<a/>`
elements is fifty times the heap of a megabyte of text. Set both, and set them
from your own corpus — [options.md](options.md) has the measurements.

**Treat a stylesheet as code, not data.** Inside the permitted roots it can
read any file, and it can spend your whole timeout doing it. Never compile a
stylesheet uploaded by a user; ship rule sets with the service. If you must
accept one, run it in a separate process with its own filesystem view.

`xsd.FileResolver`, `xslt.FileResolver` and `dtd.FileResolver` all enforce
their roots the same way: they open through `os.OpenRoot`, so each path
component is resolved against the root's own descriptor at open time and a
symlink swapped in after the check is refused rather than followed. Nothing is
resolved and then re-opened by name. The earlier containment check remains as
the diagnosis — it names the permitted directory in the error — and is
explained in [docs/security.md](security.md). Traversal vectors are covered by
tests, including ones that need no symlink privilege and so run on Windows.

## Observability

```go
res, err := v.Validate(ctx, body)

// xsl:message output, collected rather than printed to stderr.
for _, m := range res.Messages {
    log.Printf("rules: %s", m)
}

// Error codes are structured, so you can branch on them rather than
// string-match. XPTY0004 is a type error, FODC0002 an unretrievable document.
if code := xdm.ErrorCode(err); code != "" {
    metrics.ValidationErrors.WithLabelValues(code).Inc()
}
```

Counting `failed-assert` elements per rule id is usually the metric worth
having: it tells you which rule is rejecting real traffic, which is nearly
always a rule-set bug or a client integration problem rather than fraud.
