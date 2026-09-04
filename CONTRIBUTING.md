# Contributing

This document collects the rules that were learned the expensive way — each
one is here because something shipped without it and gave a wrong answer.
`tests/check.sh` is the gate; a change is not done until it prints `OK`.

---

## Never narrow an exact XDM value

`xs:integer` and `xs:decimal` are unbounded. They are backed by `math/big`
precisely so that a value larger than an `int64` survives a round trip. Every
time one of them has been narrowed to a Go `int`, `int64` or `float64` in
order to be used as an index, a count, an arity, a precision or a codepoint,
the narrowing has silently wrapped or saturated and the function has returned
a **wrong value with no error** — which is worse than a crash, because nothing
in the test suite notices a plausible-looking answer.

Three forms are forbidden.

**XDM → `float64` → `int` for any exact value.** `float64` has 53 bits of
mantissa, so the conversion saturates at `math.MaxInt` long before the value
is out of range, and rounds well before that. `fn:function-lookup` did
`int(arity.Float64())` and an absurd arity therefore looked up a function
rather than returning the empty sequence.

**Unproven `.Int64()`.** `Atomic.Int64` truncates the big value to 64 bits; it
does not report that it did. Concretely, from the four sites fixed in
`cc17983`:

```
round(1.55, 2^64+1)          → 1.6    (should be 1.55; the precision wrapped to 1)
codepoints-to-string(2^64+65) → "A"   (should be FOCH0001; the codepoint wrapped to 65)
remove((1,2,3), 2^64+2)      → (1,3)  (should be unchanged; the position wrapped to 2)
```

None of those raised an error. Each is a `.Int64()` with no preceding
`FitsInt64()` or explicit `big.Int` comparison — and the duration accessors'
`ratTrunc` had the same shape.

**`sequence[0]` for a singleton parameter.** A parameter declared `xs:integer`
(no occurrence indicator) is not satisfied by a sequence of zero or two items,
and indexing element zero either panics or quietly acts on the first of
several. Check the cardinality and raise `XPTY0004`.

### The sanctioned patterns

Use one of these rather than inventing a fourth:

* **`clampPosition`** (`xpath/fn_seq.go`) — for a position that a caller's own
  bounds check will then reject. It proves the value with `FitsInt64()` and an
  explicit `int` range test, and clamps to just past the end instead of
  wrapping.
* **`integerPosition`** (`xpath/fn_array.go`) — for a position that must be in
  range. It compares the `big.Int` against explicit bounds and raises
  `FOAY0001` when it is outside them.
* **`integerValueOf`** (`xpath/fn_formatinteger.go`) — best of all: it does not
  narrow at all. `fn:format-integer` needs the decimal digits, so it works on
  the exact digit string and formats an arbitrarily large value correctly.

The question to ask at every one of these sites is not "will this value ever
be that big?" — a conformance suite will hand you `2^64+1` specifically to
find out — but "what does the specification say happens when it is?" Usually
the answer is a named error code, and raising it is less code than the wrong
narrowing was.

`tests/narrowing-check.sh` greps for the three forms. It is advisory: it
always exits 0, and it reports enough false positives that it is a reading
list for review, not a build gate.
