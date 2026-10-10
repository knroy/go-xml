# Profiling go-xml

Where go-xml v2's CPU time and memory go today, how to profile a change and
A/B it against a baseline, and which ideas were already measured and
rejected. Every figure here was measured on `a6f334f1` (version 2.0.0).
What changed and when is in [CHANGELOG.md](../CHANGELOG.md) and `git log`.
The fix-round records this file used to carry (V1–V51, with each item's
measured gain) are in `git show 026e873a:docs/profiling.md`, and the earlier
rounds (T, R and S items) in `git show aefbd8a5:docs/profiling.md`.

## Contents

- [Where go-xml stands](#where-go-xml-stands)
- [Where the time goes](#where-the-time-goes)
  - [Totals](#totals)
  - [XSLT: DocBook xslTNG](#xslt-docbook-xsltng)
  - [XSLT: Peppol and XRechnung](#xslt-peppol-and-xrechnung)
  - [XQuery: XMark](#xquery-xmark)
  - [XSD validation and typed validation](#xsd-validation-and-typed-validation)
  - [RELAX NG](#relax-ng)
  - [Parse and Canonical XML](#parse-and-canonical-xml)
- [How to profile a change](#how-to-profile-a-change)
  - [Linux](#linux)
- [Open opportunities](#open-opportunities)
- [Measured and rejected](#measured-and-rejected)

## Where go-xml stands

Warm time in a long-running process, go-xml over the reference engine:
the geometric mean over the items both engines agreed on, with each ratio
computed against the reference times from the same run. Below 1 means go-xml
is faster. v1 is the run at `f45068c` (`bench/results-v1-f45068c.json`);
v2.0.0 is the run at `a6f334f1` on 2026-10-10 (`bench/results-v2.0.0.json`).
Both ran every engine on the same machine. The cold figures, the memory and the
per-item tables are in [benchmark.md](benchmark.md).

| Workload | Reference | v1 | v2.0.0 |
|---|---|---:|---:|
| DocBook xslTNG | Saxon-HE | 0.46× | 0.20× |
| DocBook `ptoc.001` | Saxon-HE | 1.49× | 0.85× |
| Peppol Schematron | Saxon-HE | 1.99× | 0.94× |
| XRechnung stage 1 | Saxon-HE | 3.00× | 1.08× |
| XRechnung stage 2 | Saxon-HE | 1.42× | 0.64× |
| XMark q1–q20 | Saxon-HE | 1.00× | 0.77× |
| XSD catalogs | Xerces-J | 0.73× | 0.61× |
| RELAX NG DocBook 5.2 | Jing | 0.60× | 0.55× |
| Parse 1/10/100 MB | `encoding/xml` | 0.54× | 0.46× |

Read Peppol, XRechnung and RELAX NG with the reference engine's own times
beside them. Against the previous run (v2 at `4068c98c`), go-xml's time on
those four workloads did not move by more than 4%. The JVM's did:
- Saxon's geometric mean on Peppol rose from 1.29 to 1.60 ms (+24%).
- Saxon's on XRechnung stage 1 rose from 2.27 to 2.60 ms (+15%), and on
  stage 2 from 2.56 to 3.51 ms (+37%).
- Jing's on RELAX NG rose from 32.5 to 42.6 µs per document.

In that run the same four ratios were 1.16×, 1.25×, 0.89× and 0.76×.
The XMark, XSD and parse moves are go-xml's own: go-xml got 8%, 8% and 19%
faster, and the reference engines were level.

## Where the time goes

Each workload's warm loop was profiled on its own: compile once, then parse →
run → serialise to `io.Discard`, with the benchmark's parse options,
resolvers and parameters. The harness is
[the one below](#how-to-profile-a-change). Totals (getrusage CPU, exact
allocation counts) come from the macOS runs, the benchmark's own machine.
Attribution comes from the same binary cross-compiled for Linux and run in a
Docker Desktop VM (12 CPUs) on the same M3 Pro, because macOS profiles
misplace samples (see [Linux](#linux)). Every share below is of all CPU
samples in that Linux profile.

"GC" is the collector's background mark workers. "Allocator" is time
under `mallocgc` and its callers in the runtime. On this 12-core machine,
much of the marking runs on processors that would otherwise be idle. The
runtime's own estimate (`runtime/metrics`, `/cpu/classes/gc/mark/idle`) puts
that at 60% of DocBook's GC CPU, 50% of Peppol's and about 15% of
XRechnung's. Idle marking costs CPU but little wall time. On a fully loaded
server the same work competes with the transforms.

### Totals

Per pass over the benchmark's items, macOS, `GOGC=100`:

| Workload | Items | CPU | Allocations | Bytes | GC cycles | GC + allocator (Linux samples) |
|---|---:|---:|---:|---:|---:|---:|
| DocBook xslTNG | 42 | 742 ms | 6.51 M | 483 MB | 20.1 | 52% |
| Peppol (CEN + Peppol rules) | 18 | 53.7 ms | 442 k | 25.5 MB | 2.0 | 49% |
| XRechnung stage 1 | 8 | 34.0 ms | 347 k | 23.8 MB | 4.9 | 36% |
| XRechnung stage 2 | 8 | 23.2 ms | 161 k | 15.6 MB | 3.0 | 25% |
| XMark q1–q20, 1 and 11 MB | 40 | 1,018 ms | 2.29 M | 1,166 MB | 4.0 | 8% |
| XSD catalogs, `Validate` | 11 | 44.3 ms | 2.8 k | 24.6 MB | 2.0 | 6% |
| XSD catalogs, `ValidateCopy` | 11 | 66.2 ms | 25.5 k | 45.6 MB | 3.2 | 8% |
| RELAX NG DocBook 5.2 | 40 | 2.84 ms | 14.9 k | 2.73 MB | 0.33 | 51% |
| Parse + C14N, 1/10/100 MB | 3 | 885 ms | 19.6 k | 535 MB | 1.7 | 8% |

Bytes include the harness's `string(src)` copy of each input, as the
benchmark's warm loop does: 22–24% of the bytes on XMark, XSD and parse.

The workloads split into two kinds:
- **Expression-heavy** (XSLT, RELAX NG): CPU follows allocation volume.
  Half the CPU of DocBook, Peppol and RELAX NG is GC and allocator, and no
  single function outside the runtime holds more than 5%.
- **Parse-bound** (XMark, XSD, parse): most of the CPU is the tokenizer and
  tree builder. They allocate in large chunks, so GC is under 8%.

### XSLT: DocBook xslTNG

| Cost centre | Share | What it is |
|---|---:|---|
| GC mark | 38% | Background marking; `runtime/metrics` counts 22% of all CPU as idle-processor marking |
| Allocator | 14% | Small objects: atomic values, sequences, context copies |
| `xpath` (flat) | 15% | Expression evaluation: name and kind tests, sequence-type checks, calls |
| `xslt` (flat) | 12% | Template dispatch, function calls, parameter binding, lazy globals |
| `xdm` (flat) | 11% | Node accessors, `QName` comparison, atomisation |
| Source parse, serialisation | 0.4% each | The documents are small (186 B to 54 KB) |

The profile is flat. The largest single functions are `QName` equality
(2.0%), `NameTest.Matches` (1.2%), `SequenceType.matchesItem` (1.1%) and
`sequenceType.convertAs` (1.1%).

**Allocations** (155 k and 11.5 MB per document):

| Source | Objects | Bytes |
|---|---:|---:|
| `xdm.Atomize` (node values to atomics) | 9.3% | — |
| `ContextItem.Eval`, `Step.evalFrom` (result sequences) | 9.1% | 4.5% |
| `xdm.NewString`, `NewUntypedAtomic` | 7.9% | — |
| `xdmbuild.New`, `AppendNode` (temporary trees) | 3.9% | 3.4% |
| Context copies: `hostWithCurrent`, `withCurrent` (objects); `withSel` (bytes) | 6.1% | 4.5% |
| `Tree.alloc` (result-tree records) | — | 6.2% |
| `evalParams` (cumulative) | — | 6.4% |

No source exceeds a tenth of the objects. DocBook's cost is many small
allocations spread over the whole evaluator, and the collector work they
cause.

### XSLT: Peppol and XRechnung

**Peppol** (Schematron compiled to XSLT 2.0, hundreds of independent
assertions per invoice):

| Cost centre | Share | What it is |
|---|---:|---|
| GC mark | 35% | `runtime/metrics` counts 16% of all CPU as idle-processor marking |
| Allocator | 14% | Atomised values and result sequences |
| `xpath` (flat) | 24% | `NameTest.Matches` 4.9%, axis walks, general comparisons |
| `xslt` (flat) | 11% | Template-rule matching (`candidates`, `mayMatch`), serialising the SVRL (2.4%) |
| `xdm` (flat) | 9.5% | `Tree.rec` 1.8%, `QName` equality 1.6%, `Atomize` |
| Function-call resolution check | 3.1% | `FuncCall.resolve` re-validating its cache entry |
| Source parse | 2.2% | |

Allocations per (rule set, invoice) item are 24.6 k and 1.41 MB. By count:
- `Atomize` 15.5% flat and 21.8% cumulative;
- `Step.evalFrom` 13.1%;
- `NewString` 8.7%;
- `NewUntypedAtomic` 6.3%;
- `Context.WithFocus` 4.1%.

By bytes, `NewString` (7.1%), `WithFocus` (6.7%), `evalFrom` (6.6%) and
`WithVar` (5.3%) lead. Each assertion's general comparison atomises node
values into fresh strings, and that, more than evaluation, is where Peppol's
time goes.

**XRechnung stage 1** (UBL to `xr:invoice`):

| Cost centre | Share | What it is |
|---|---:|---|
| `name() = '…'` comparisons (`nameComparison`) | 20% | Cumulative. Half of it (10.5%) is `nameCallArg` proving again, on every evaluation, that `name` is the built-in |
| Allocator | 22% | `evalGeneralComparison` alone makes 17.5% of the objects |
| GC mark | 14% | |
| Serialisation | 7.9% | Text escaping (`plainText`, `escapeTextRun`) |
| Source parse | 4.6% | |

`FuncCall.resolve`'s cache check (`callResolution.current`, interface
comparisons) is 9.5% of the profile, two thirds of it from `nameCallArg`.
Allocations per invoice are 43 k and 3.0 MB.

**XRechnung stage 2** (`xr:invoice` to HTML):

| Cost centre | Share | What it is |
|---|---:|---|
| Serialisation | 23% | HTML output with the inlined CSS and JavaScript: `plainText` 3.9%, `escapeTextRun` 3.1%, `bufio` 2.6% |
| GC mark | 13% | |
| Allocator | 11% | |
| Source parse | 6.8% | |
| `unparsed-text()` | 3.9% | Three files read again on every transform: the read 1.2%, `os.OpenRoot` 0.8%, `EvalSymlinks` on the directory 0.4% |

By bytes, `Tree.alloc` (14%) and the text store (11%) lead; by count,
`Atomize` (11%) and `key()` (`fnKey`, 24% cumulative). Allocations per
invoice are 20 k and 1.95 MB.

### XQuery: XMark

| Cost centre | Share | What it is |
|---|---:|---|
| Parse | 82% | `internal/xmltok` 50% (`text` 14%, `nameBytes` 11%, `checkChars` 5.9%), tree building in `xdm` 31% |
| Evaluation and serialisation | 16% | Query evaluation, result construction, output |
| Allocator | 6.9% | `Tree.alloc`, FLWOR tuples |
| GC mark | 1.3% | |

Allocations: 42% of the bytes are node-record chunks (`Tree.alloc`), 22% the
harness's input copy and 16% the text store. By count, FLWOR tuple binding
(`tuple.bind`) is 27%, but these are small objects and about 2% of CPU. XMark
at factor 0.1 is a parse benchmark.

### XSD validation and typed validation

**`Validate`** over the 11 catalog files:

| Cost centre | Share | What it is |
|---|---:|---|
| Parse | 61% | Includes character references, 16% cumulative (`Decoder.reference`): the 2.1 MB `regex-syntax` catalog holds 184,000 of them |
| Validation | 35% | Content-model matching, attribute checks, `QName` equality 4.3% |
| GC mark | 3.6% | |

Allocations are few (255 per document) and large. By count, `matchSequence`
makes 25%, the position records (`setOffset`, `positionAt`) 34% and
`Tree.alloc` 19%. By bytes, `Tree.alloc` makes 39%.

**`ValidateCopy`** (typed validation; the typed copy is what XSLT and XQuery
validation use) costs 1.50× `Validate`'s CPU on macOS (66.2 against 44.3 ms a
pass), 1.86× its bytes and 9× its allocations:

| Cost centre | Share | What it is |
|---|---:|---|
| Parse | 43% | As above |
| Assessment | 36% | The validation itself, writing typing |
| Clone | 7.9% | The single typed copy (`cloneSubtree`) |
| Whitespace stripping | 4.8% | `stripIgnorableWhitespace` |
| GC mark | 4.1% | |

By count, defaulted attributes account for 79% of the allocations
(`applyAttributeDefault`, cumulative): each starts as a fragment tree of its
own. They are 1.7% of CPU. Typing records (`ownTyping`) are 22% of the
objects. By bytes, `Tree.alloc` makes 21%, the clone 17% and the typing
15%.

### RELAX NG

| Cost centre | Share | What it is |
|---|---:|---|
| Validation | 37% | Derivatives: `attDeriv` 3.7%, `startTagCloseDeriv` 2.3%, pattern interning and hashing |
| GC mark | 37% | |
| Parse | 22% | 40 small documents (68 KB of allocation each) |
| Allocator | 14% | `newAfterPat` alone is 5.4% |

48% of the bytes are the tokenizer's 32 KB string arena, one per document.
`newAfterPat` makes 37% of the objects and `strings.Fields` (token
normalisation) 12%. The documents are small, so that fixed arena dominates
the bytes and drives the GC share. Sizing the arena to the input was measured
and rejected because of how it moved GC pacing on DocBook (see
[rejected](#measured-and-rejected)).

### Parse and Canonical XML

| Cost centre | Share | What it is |
|---|---:|---|
| Tokenizer (`internal/xmltok`) | 37% | `text` 8.2%, `checkChars` 5.5%, `nameBytes` 4.5%, `getc` 3.4% |
| Tree building (`xdm`) | 34% | `parse` 4.1%, `Tree.alloc` 3.4%, `validateStartElement` 3.0% |
| C14N write | 22% | `writeEscaped` 6.1%, `writeQName`, `writeAttrs` |
| GC mark | 7.0% | Most of it on idle processors |
| Allocator | 1.2% | |

535 MB allocated a pass for 111 MB of input. Node-record chunks are 67% of
it, the harness's input copy 23% and the text store 7%. About 19,600
allocations a pass: the tree is a few large chunks, not an object per node.
The 1 and 10 MB C14N workload gives the same split (parse 74%, write 22%).

## How to profile a change

**1. Build a warm-loop harness.** Copy the benchmark's warm loop
(`compileGo` in the local `bench/cmd/benchrun/warm.go`) so the parse options,
resolvers and parameters match: compile once, then parse →
transform/query/validate → serialise to `io.Discard`. The figures above came
from a test file next to it (`bench/cmd/benchrun/prof_test.go`, local like
the rest of `bench/`). This is its core, which can be dropped into any
package that can build a `run` function:

```go
func TestProfile(t *testing.T) {
	runtime.MemProfileRate = 0 // CPU run: no allocation sampling
	run := compileOnce(t)      // the warm loop's compile step
	pass := func() { for _, it := range items { run(it) } }
	for i := 0; i < passes/5; i++ { pass() } // warm-up, untimed

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	r0 := rusage() // syscall.Getrusage: Utime + Stime
	f, _ := os.Create(out + "/cpu.pprof")
	pprof.StartCPUProfile(f)
	for i := 0; i < passes; i++ { pass() }
	pprof.StopCPUProfile()
	r1 := rusage()
	runtime.ReadMemStats(&m1)
	t.Logf("CPU/pass %v  allocs/pass %d  bytes/pass %d  GC/pass %.1f",
		(r1-r0)/time.Duration(passes), (m1.Mallocs-m0.Mallocs)/uint64(passes),
		(m1.TotalAlloc-m0.TotalAlloc)/uint64(passes),
		float64(m1.NumGC-m0.NumGC)/float64(passes))
}
```

For the allocation run, leave `MemProfileRate` at its default. Write
`pprof.Lookup("allocs")` to `allocs.base` before the timed passes and to
`allocs.pprof` after them. Do not take both profiles in one run: with
allocation sampling on, the same passes used 3–9% more CPU (DocBook +2.7%,
XRechnung stage 1 +7.3%, Peppol +9.2%).

**2. Run it and read the profile.**

```sh
go test -c -o /tmp/prof.test ./bench/cmd/benchrun
PROF_WORKLOAD=peppol-schematron PROF_OUT=/tmp/p PROF_PASSES=40 \
  /tmp/prof.test -test.run TestProfile -test.count=1 -test.v
go tool pprof -top -nodecount=30 /tmp/prof.test /tmp/p/cpu.pprof
go tool pprof -top -cum -nodecount=60 /tmp/prof.test /tmp/p/cpu.pprof
go tool pprof -peek 'FuncCall..resolve$' /tmp/prof.test /tmp/p/cpu.pprof
go tool pprof -list 'FuncCall..resolve$' /tmp/prof.test /tmp/p/cpu.pprof
PROF_MODE=mem PROF_WORKLOAD=peppol-schematron PROF_OUT=/tmp/p PROF_PASSES=40 \
  /tmp/prof.test -test.run TestProfile -test.count=1
go tool pprof -top -sample_index=alloc_objects \
  -base /tmp/p/allocs.base /tmp/prof.test /tmp/p/allocs.pprof
go tool pprof -top -sample_index=alloc_space \
  -base /tmp/p/allocs.base /tmp/prof.test /tmp/p/allocs.pprof
```

Run enough passes for a few seconds of samples: 15 for DocBook, 40–150 for
the invoices, 600 or more for RELAX NG. Deep XSLT recursion truncates the
stacks pprof records, so a sample's root frames (the harness, `Transform`)
are often missing. To split a profile into phases, match on leaf-side frames
(`internal/xmltok`, `xslt.(*serializer)`) instead of on the caller.
`go tool pprof -traces` prints every sample's stack for scripts that do this.

**3. A/B against a baseline.** Never prototype in the checkout. Extract the
baseline with `git archive <base> | tar -x -C /tmp/base`, put the same
harness in both trees, and alternate the two builds back to back:

```sh
for i in 1 2 3 4 5 6 7; do
  (cd /tmp/base/bench/cmd/benchrun && /tmp/base.test -test.run TestProfile ...)
  (cd ~/go-xml/bench/cmd/benchrun && /tmp/new.test -test.run TestProfile ...)
done
```

Compare paired medians of 5–30 rounds:
- The allocation and byte counts are exact and repeat to three figures. They
  are the reliable figure.
- getrusage CPU is the total to compare. A CPU difference under about 3% is
  noise on a laptop, and more so with other work running.
- Wall time is the least reliable of the three.

**4. Check the outputs are identical.** Record both trees and compare:

```sh
tests/record.sh /tmp/recA            # in the baseline tree
tests/record.sh /tmp/recB            # in the changed tree
go run ./tests/recdiff compare -allow tests/recdiff/allow.txt /tmp/recA /tmp/recB
```

Every difference must be explained, or admitted by a rule in
`tests/recdiff/allow.txt` that names the behaviour change. A performance
change should produce none.

**5. Run the gate** in the background:
`sh tests/check.sh > /tmp/gate.log 2>&1 &`. It takes long, and it fails on
any dropped passing count.

**Traps:**
- **Measure plain parse too**, not only the workload you targeted. Parsing
  sits under every workload. `smallNames` 24 (V39) won on XMark q10 but cost
  plain parsing 7%; it went back to 16 in `44cd0280`.
- **Concurrent load skews CPU.** Several builds or agents sharing the machine
  inflate getrusage CPU unevenly between the two arms. Alternate the arms,
  and trust the allocation counts over CPU when the machine is shared.
- **GC pacing.** A smaller live heap makes the collector run more often at
  the same `GOGC`. A change that cuts retained memory can cost CPU elsewhere:
  starting the string arena small cut small documents' retained heap 20–37%
  and cost DocBook 5% CPU (85 GC cycles against 77). Count GC cycles a pass
  in both arms.
- **The memory profiler costs CPU.** A binary that links `runtime/pprof`
  samples allocations unless `MemProfileRate` is 0. The CLI does not link it,
  so a harness that does measures a slightly different program.
- **macOS profiles mislead** (below). Use getrusage for totals and a Linux
  profile for attribution.
- **Warm compile figures are one-shot.** The benchmark times each compile
  once. XMark's query compile read 0.35 ms at `4068c98c` and 0.85 ms at
  `a6f334f1`. Compiled 300 times, both trees take about 16 µs a query and
  make the same 251 allocations.

### Linux

macOS profiles catch 82–90% of the CPU getrusage reports and put samples in
the wrong places. Linux profiles of the same binary, in a Docker Desktop VM on
the same machine, catch 99–100%. Shares of all samples:

| Workload | macOS: scheduler waits | macOS: `madvise` | Linux: scheduler waits | Linux: `madvise` |
|---|---:|---:|---:|---:|
| DocBook | 23.5% | 3.1% | 1.4% | 0.1% |
| Peppol | 27.8% | 2.7% | 1.4% | 0.0% |
| XRechnung stage 1 | 31.6% | 35.4% | 1.2% | 0.3% |
| XRechnung stage 2 | 26.1% | 23.2% | 1.0% | 0.3% |
| `ValidateCopy` | 8.1% | 29.8% | 0.6% | 0.6% |
| RELAX NG | 49.0% | 2.0% | 1.4% | 0.3% |
| C14N 1/10 MB | 8.4% | 22.7% | 0.0% | 0.0% |

- **Scheduler waits** (`pthread_cond_wait`, `kevent`, `usleep`) take 8–49%
  of macOS samples and 0–1.4% of Linux ones. Most of it is idle time.
- **System calls are over-weighted.** On XRechnung stage 2 the macOS profile
  puts 24% of samples on `EvalSymlinks` in `unparsed-text()`. On Linux all
  system calls together are 3.4%. A loop timing the same call on macOS gives
  7.5 µs, three calls per transform of about 2.5 ms, under 1%. `madvise` is
  the same: Go's darwin runtime calls `madvise(MADV_FREE_REUSE)` whenever it
  reuses a heap page, and the profile shows it at up to 35%. Yet stage 1 used
  less CPU on macOS than on Linux (34.0 against 36.1 ms a pass).
- **On Linux the share moves to GC marking.** Earlier measurements found cold
  CLI runs used 11–13% less CPU on Linux, and warm loops 2–20% more, with GC
  marking where macOS has `madvise`.
- **The VM adds address translation**, so read Linux CPU totals as an upper
  bound and use them for proportions only.

To profile on Linux, cross-compile the harness and mount the checkout at the
same path, so the workloads' paths resolve:

```sh
GOOS=linux GOARCH=arm64 go test -c -o /tmp/p/prof-linux.test ./bench/cmd/benchrun
docker run --rm -v "$PWD:$PWD:ro" -v /tmp/p:/tmp/p -w "$PWD/bench/cmd/benchrun" \
  -e PROF_WORKLOAD=docbook-xsltng -e PROF_OUT=/tmp/p -e PROF_PASSES=15 \
  golang:1.26 /tmp/p/prof-linux.test -test.run TestProfile -test.count=1
```

## Open opportunities

None of the fixes listed in earlier versions of this file remains open. The
candidates below are the largest cost centres in the profiles above that
look removable. Each share is a ceiling: what the workload would save if
the cost went to zero. **None has been prototyped.**

| Candidate | Workload | Ceiling | What the profile shows |
|---|---|---:|---|
| Decide once per call site that `name()` is the built-in | XRechnung stage 1 | 10.5% | `nameCallArg` calls `FuncCall.resolve` and compares names on every evaluation of `name() = '…'`. `resolve`'s cache check alone is 9.5% of stage 1, 3.1% of Peppol and 1.1% of DocBook |
| Character references without a byte-at-a-time read | XSD catalogs | 16% | `Decoder.reference` reads digits through `mustgetc` and appends them one at a time. Almost all of it is the `regex-syntax` catalog's 184,000 references |
| Fewer allocations in general comparisons | Peppol, XRechnung stage 1 | 19–22% of objects | `Atomize` (Peppol, 22% cumulative) and `evalGeneralComparison` (stage 1, 19%) allocate a fresh atomic per node value compared. GC and allocator are 36–49% of these workloads' CPU; removing these objects would save at most their share of that |
| `unparsed-text()` per call | XRechnung stage 2 | 3.9% | Each call opens an `os.Root`, evaluates the directory's symlinks and reads the file again; resolving alone is 1.2% |

Nothing else stands out. DocBook has no function above 2% outside the
runtime, and the parse-bound workloads spend their time in the tokenizer
loops; a SWAR text scan was already tried there.

## Measured and rejected

Each idea here was prototyped and measured. Reopen one only if the reason no
longer holds.

| Idea | Why not |
|---|---|
| Compile XPath to closures | Interpretation (AST dispatch, name resolution) is 2–4% of Schematron CPU. A direct child-axis loop, the largest removable piece, measured −2 to −3% at `GOGC=400` and nothing at 100. The cost is allocation, not dispatch |
| Stream the principal result into the serializer | Building the result tree is 8–10% of Schematron allocations at most. Every Schematron and XRechnung stylesheet sets `indent="yes"`, and indenting needs an element's children first, so output could not stay byte-identical without buffering |
| Pooled argument slices or arrays for leaf built-ins (v1, then V16 on v2) | On v2: CEN −11% allocations, −4.8% bytes; XRechnung and DocBook −2.2% and −1.5% allocations; CPU −2.0%, +2.5%, −0.3%, all noise. A fixed array does not help: the arguments escape through `Function.Call` |
| `.` in a pooled slot; `strSeq`/`boolSeq` in one allocation | Allocations −6 to −10% on v1, CPU within noise |
| `Compiled.scope` without a copy, by mutating the caller's context | −4 to −10% CPU, but `Compiled` is safe for concurrent use. V12 is the version that does not mutate |
| No static-base-URI copy for an expression that reads none (V12 extended) | DocBook's included modules carry their own base URI, so 56% of its evaluations still copy. Skipping those that call nothing reading it saves at most 1.3% of bytes, and needs a deny-list of every function, host ones included, that reads the base URI |
| Compile-time function resolution (T17) | A lookup allocates nothing and is about 3% of CPU. A cache that stayed correct when a library changed after first use cost more allocations than it saved; the call-site cache (S1) covers it |
| Relative-path memo (`cac:A/cac:B` across assertions) | Repeats are about 2.4% of CEN; allocations −0.3%, bytes +5%, no CPU gain |
| `fuseDescendant` without a step allocation per evaluation | −0.13% allocations on CEN, CPU flat |
| `//x[p]` fusion into `descendant::x[p]` | Not equivalent: with nested `x`, a different error can win. The per-document root-path index covers the case |
| Schema-wide RELAX NG derivative memo | 3.5× faster warm only because the benchmark re-validates the same documents. No gain on unseen documents, and twice the bytes |
| RELAX NG derivative caches keyed by pattern id (V30) | Long documents −2.2 to −2.5% over 30 rounds, corpus −0.8%, allocations unchanged; needs `unsafe` |
| Bitset NFA states in XSD 1.1 restriction checks | The checks are 6–9% of an XSD 1.1 load, so the estimated −15–20% is out of reach |
| Interned namespace URIs compared by pointer, local names first (V26) | All 229,428 equal-URI comparisons a pass became pointer-equal, but XSD validation CPU moved 1.001× (0.994× for local-first alone): the cost is comparing equal local names stored apart. The stdlib `unique` package matched nothing, its entries being freed at the next collection |
| Counting child elements lazily; testing `particleAcceptsEmpty` after the cheap checks (XSD) | Within noise |
| Interning `nodeTyping` per tree | Bytes −27%, wall +25%: hashing the key on every typing write costs more than the allocation saves |
| Typing chunks under 32 KB | No gain. The `madvise` time in the typed path is macOS heap growth, not chunk size |
| Bigger node chunks; 1,024-node chunks | No gain; +0.28 GB peak RSS at 100 MB |
| Smaller first record chunk (2 records, V15) | Most fragments hold one node, but bytes moved −0.4% to +0.2% and allocations up to +0.6%: the next chunk comes sooner for every other tree |
| Chunked result nodes | −0.9% allocations at most, +0.4–0.7% bytes |
| String arena starting small | Retained heap −20–37% on small documents, but DocBook +5% CPU: the smaller live heap ran the collector more often (85 cycles against 77). Reverted in `629cf48` |
| String arena sized to the input (V18) | 40 small RELAX NG documents: parse bytes −52%, CPU −20%, retained heap −72%. But DocBook ran about 7% more GC cycles (29.3 → 31.3 a pass), as above. Only the read windows were sized (`9ddd2bc6`) |
| Attribute values passed to xdm without the arena copy (V40) | Parse bytes −2.3 to −2.6%, but `regex-syntax` +2.6% bytes, XRechnung +26 to +53 allocations a pass and more GC cycles (7.2 → 7.6); no CPU gain. Reviving it needs the copy at `textStore.add`, since the values sit in a reused buffer |
| End tags checked against a stack of open names (V35) | After the tokenizer's name cache (V34), parse +0.8 to +1.7% CPU and 7 more allocations a parse |
| SWAR scan in the tokenizer's `text()` | No gain (round 3, and again on v2): it is already a tight table loop |
| 64 KiB file read buffer | No gain |
| A record walker for C14N and the serializer | The accessors and the `Children`/`Attrs` iterators already inline. Walking the 10 MB document: recursive accessors 3.7 ms, a range-over-func pre-order walker 5.4 ms (an indirect call per node), an exported cursor 4.1 ms. A raw chunk scan inside `xdm` takes 1.4 ms but has no enter/leave and cannot leave the package without handing out records |
| Keyed attribute sort in C14N | No gain |
| A memory limit (`GOMEMLIMIT`) instead of `GOGC=200` in the CLI | A limit under the live heap costs 10–25× CPU, and the CLI cannot know the live size |
| Pausing GC around parse and compile instead of `GOGC=200` | Cold CPU summed over four workloads: 812 ms paused against 748 ms at `GOGC=200`, and pausing needs six wrapped call sites (`5fbea36`) |
| Caching `canonCache` directory reads (V42) | Already done per assembly (`a.canon`, `3b06e4c6`). A one-document CLI schema makes no calls, a two-document include one directory read; removing that would change which names count as the same document |
