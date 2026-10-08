# Benchmark harness

Compares go-xml with Saxon-HE, BaseX, xsltproc/xmllint (libxml2), Xerces-J
(XSD 1.1), Jing and Go's `encoding/xml`, on the workloads in `workloads/`.
Every item is checked for agreement with go-xml before it is timed; an engine
that disagrees on an item is recorded with `"agree": false` and the reason,
and gets no timing for that item.

## Prerequisites

- Go (the version in `go.mod`), run from the repository root.
- Java 17+ with `javac` on `PATH`, for the JVM engines and `java/Loop.java`.
- `xsltproc` and `xmllint` on `PATH` (system libxml2; not fetched). An engine
  that is missing is reported as skipped, not as a failure.
- `curl`, `unzip`, and `sha256sum` or `shasum`, for the fetch script.

## Fetch the engines

    sh bench/fetch-engines.sh

Downloads the pinned jars (versions, URLs and SHA-256 in `engines.json`) into
`bench/.cache/`, which is gitignored. Re-running it is a no-op; a file whose
digest does not match is refused, never overwritten.

## Run

    go run ./bench/cmd/benchrun                       # every workload -> bench/results.json
    go run ./bench/cmd/benchrun -workload selftest -out bench/.cache/selftest.json
    go run ./bench/cmd/benchrun -workload X -engine saxon-he -mode warm
    go run ./bench/cmd/benchrun -workload X -check-only

Flags: `-workload` and `-engine` (repeatable), `-mode cold|warm|both`,
`-runs` / `-warmup` (cold: timed and untimed processes, default 10 and 2),
`-warm-iters` (warm: timed passes per item, default 30, after a fifth as many
untimed), `-out`, `-check-only`. `selftest` runs the tiny fixtures in
`testfixtures/` through every engine and mode, to check the harness itself.

## The two modes

- **cold** — one OS process per run: start, compile, run, write the output to
  a file. Wall time per run and peak RSS (`getrusage` max RSS, normalised to
  bytes; not measured on Windows).
- **warm** — steady state: compile once, then time each item in-process. For
  go-xml and `encoding/xml` this is a Go loop in benchrun over the public APIs;
  for the JVM engines it is `java/Loop.java` through the engine's Java API.
  Inputs are read into memory first and output goes to a null sink. libxml2
  tools have no in-process harness, so they are cold only. BaseX recompiles a
  query per evaluation, so its warm time includes compilation.

The parse and c14n areas have no go-xml CLI command; their cold process is
`benchrun -helper ENGINE SOURCE OUT`, which parses and writes Canonical XML.

## Workload files

`workloads/<id>.json`: `id`, `area` (`xslt`, `xquery`, `xsd`, `rng`, `parse`,
`c14n`), `description`, `inputs`, `items`, `normalise` (`none`, `whitespace`,
`text`, `xml-c14n`), `engines` (must include `go-xml`), `modes`. Each item has
`name`, `stylesheet`/`query`/`schema`, `source` and optional `params`; `glob`
in place of `source` expands to one item per match; `area` on an item
overrides the workload's. Optional: `xsd_version` (go-xml's, default `1.1`)
and `args` (`{"engine": ["extra", "argv"]}`; for go-xml's warm loop only
`-allow-dir`, `-allow-doctype` and `-compat-drop-attributes-on-document` are
understood). Paths are relative to the repository root.

Validators agree on the verdict (valid, invalid, error). Other areas agree on
the normalised output, or on both engines failing — with the same error code
when both name one.
