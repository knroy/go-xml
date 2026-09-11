package xdm

import (
	"math/big"
	"strings"
	"testing"
)

// The mutation lane for op:same-key.
//
// TestMapKeyOfMatchesSameKey is green, and that on its own says nothing about
// whether its corpus is adequate. A corpus is adequate when it can tell a
// correct key encoding from an incorrect one, and the only way to measure that
// is to hand it an incorrect one and require it to object. This file does
// that: each mutant below is a plausible wrong implementation of MapKeyOf, and
// the lane asserts the corpus catches every one of them -- naming, for each,
// the pair of corpus values whose verdict changes.
//
// This is a test-quality check, not production code. There is deliberately no
// seam in maparray.go: MapKeyOf is a pure function from an *Atomic to a string,
// so a fault in it can be simulated by post-processing its output rather than
// by replacing it, and a mutant is therefore an ordinary test-local function.
// Nothing in the package under test knows this file exists, so no mutation can
// leak into a production path, and no build tag or -ldflags trick is needed:
// the lane is reproducible under a plain `go test ./xdm/ -run Mutation`.
//
// A mutant must satisfy two conditions to be counted as caught:
//
//  1. It must actually CHANGE at least one key over the corpus. A mutant that
//     rewrites nothing is dead code, and a lane that counted it as caught
//     because some other mutant's disagreement showed up would be measuring
//     the corpus against a mutation that never happened. The repository has
//     been bitten by exactly that: a sabotage that left the tests green
//     because the sabotaged line was never reached.
//  2. The mutated keys must DISAGREE with the SameKey oracle on some pair.
//
// The two are separate assertions and the diagnostics distinguish them.

// keyMutant is a plausible wrong key encoding, expressed as a rewrite of the
// correct key. rewrite is handed the value and the key production computes for
// it, and returns the key the faulty implementation would have produced.
type keyMutant struct {
	name string
	// why states which real implementation mistake the mutant stands in for,
	// so a mutant that stops being caught can be judged rather than deleted.
	why     string
	rewrite func(a *Atomic, key string) string
}

// sameKeyMutants are the faults the corpus is required to detect.
//
// They are chosen to be the mistakes this encoding actually invites, not
// arbitrary perturbations: every one of them corresponds to a rule in
// MapKeyOf or typeFamilyOf that could be written the other way by someone who
// had not read the case that motivated it.
func sameKeyMutants() []keyMutant {
	return []keyMutant{
		// --- family collapses and splits (typeFamilyOf) -------------------
		{
			name: "split-string-family/anyURI",
			why: "typeFamilyOf returns a.Type.String() for every type, so " +
				"xs:anyURI stops sharing the string family. map-get-006 " +
				"and -007 turn on that grouping.",
			rewrite: func(a *Atomic, key string) string {
				if a.Type == TypeAnyURI {
					return strings.Replace(key, "str:", "anyURI:", 1)
				}
				return key
			},
		},
		{
			name: "split-string-family/untypedAtomic",
			why: "xs:untypedAtomic is rejected from the string family, which " +
				"is the reading that looks right until map-get-006 is read.",
			rewrite: func(a *Atomic, key string) string {
				if a.Type == TypeUntypedAtomic {
					return strings.Replace(key, "str:", "untypedAtomic:", 1)
				}
				return key
			},
		},
		{
			name: "merge-string-and-hexBinary",
			why: "a family table that lumps every 'lexical tail' type into " +
				"one group, so xs:hexBinary('00') keys as the string '00'.",
			rewrite: func(a *Atomic, key string) string {
				if a.Type == TypeHexBinary {
					return "str:" + a.String()
				}
				return key
			},
		},
		{
			name: "merge-untypedAtomic-into-numeric",
			why: "applying the function conversion rules as a cast to the " +
				"key's type rather than to xs:string, which would let " +
				"xs:untypedAtomic('12') find the xs:integer(12) entry. " +
				"map-get-008 requires it not to.",
			rewrite: func(a *Atomic, key string) string {
				if a.Type == TypeUntypedAtomic {
					if r, ok := new(big.Rat).SetString(a.String()); ok {
						return "num:" + r.RatString()
					}
				}
				return key
			},
		},
		{
			name: "split-duration-family",
			why: "keying a duration by its own type rather than by the " +
				"shared (months, seconds) pair, so xs:duration('P1Y') and " +
				"xs:yearMonthDuration('P12M') stop colliding (map-get-017).",
			rewrite: func(a *Atomic, key string) string {
				if isDur(a.Type) {
					return a.Type.String() + "/" + key
				}
				return key
			},
		},
		{
			name: "merge-calendar-types",
			why: "a single 'calendar' family, so xs:gYear and xs:gYearMonth " +
				"could collide. Each of the eight stands alone.",
			rewrite: func(a *Atomic, key string) string {
				if isCal(a.Type) {
					// Every calendar key is built as
					// a.Type.String()+":"+tail, and the type name itself
					// contains a colon ("xs:date"), so the type has to be
					// trimmed by name rather than at the first separator.
					return "cal:" + strings.TrimPrefix(key, a.Type.String()+":")
				}
				return key
			},
		},
		{
			name: "split-numeric-family",
			why: "keying a number under its own subtype, so xs:integer(1) " +
				"and xs:double(1) stop colliding (same-key-012). This is " +
				"what an encoder that forgot the numeric hierarchy writes.",
			rewrite: func(a *Atomic, key string) string {
				if a.Type.IsNumeric() {
					return a.Type.String() + "/" + key
				}
				return key
			},
		},

		// --- NaN ----------------------------------------------------------
		{
			name: "nan-not-equal-to-itself",
			why: "deleting the NaN carve-out and letting NaN fall through to " +
				"the 'eq' rule, under which it is equal to nothing at all. " +
				"op:same-key requires every NaN to be ONE key, so " +
				"map:get(map:entry(xs:double('NaN'),1), xs:float('NaN')) finds it.",
			rewrite: func(a *Atomic, key string) string {
				if key == "num:NaN" {
					// Distinct per value, as "eq" would have it.
					return "num:NaN/" + a.Type.String() + "/distinct"
				}
				return key
			},
		},
		{
			name: "nan-per-numeric-type",
			why: "a NaN carve-out that kept the type in the key, so " +
				"xs:float('NaN') and xs:double('NaN') are two keys. Half the " +
				"rule, which is the easier mistake than dropping it entirely.",
			rewrite: func(a *Atomic, key string) string {
				if key == "num:NaN" {
					return "num:NaN/" + a.Type.String()
				}
				return key
			},
		},
		{
			name: "infinities-per-numeric-type",
			why: "the same half-rule applied to the infinities, which take " +
				"the same early return and share it across numeric types.",
			rewrite: func(a *Atomic, key string) string {
				if key == "num:INF" || key == "num:-INF" {
					return key + "/" + a.Type.String()
				}
				return key
			},
		},

		// --- numeric canonicalisation -------------------------------------
		{
			name: "numeric-key-is-lexical",
			why: "dropping the rational canonicalisation and keying on the " +
				"spelling, so xs:decimal('1.0') and xs:decimal('1.00') are " +
				"two keys and xs:integer(1) is a third.",
			rewrite: func(a *Atomic, key string) string {
				if a.Type.IsNumeric() && !strings.HasPrefix(key, "num:NaN") &&
					key != "num:INF" && key != "num:-INF" {
					return "num:" + a.String()
				}
				return key
			},
		},
		{
			name: "numeric-key-via-float64",
			why: "keying through Float64 instead of the exact rational, " +
				"which merges 2^53 with 2^53+1 and merges map-put-023's " +
				"xs:decimal 1.0000000000100000000001 with the xs:double " +
				"1.00000000001 that it merely rounds to.",
			rewrite: func(a *Atomic, key string) string {
				if a.Type.IsNumeric() && !strings.HasPrefix(key, "num:NaN") &&
					key != "num:INF" && key != "num:-INF" {
					return "num:" + new(big.Rat).SetFloat64(a.Float64()).RatString()
				}
				return key
			},
		},
		{
			name: "signed-zero-distinct",
			why: "letting the sign of zero into the key. -0 and +0 are one " +
				"value, so they must be one key however they print.",
			rewrite: func(a *Atomic, key string) string {
				if key == "num:0" && strings.HasPrefix(a.String(), "-") {
					return "num:-0"
				}
				return key
			},
		},

		// --- calendar timezone handling -----------------------------------
		{
			name: "ignore-timezone-presence",
			why: "normalising an unzoned value through the implicit " +
				"timezone, which is what xpath.GroupingKey does and what a " +
				"map key must NOT do: same-key-013/-014/-015 build a " +
				"three-entry map from that pair and require all three to live.",
			rewrite: func(a *Atomic, key string) string {
				if dt := a.DateTimeVal(); dt != nil && !dt.HasTZ &&
					(a.Type == TypeDate || a.Type == TypeTime || a.Type == TypeDateTime) {
					// Substitute UTC, as grouping would.
					return a.Type.String() + ":tz:" + dt.ToSeconds(0).RatString()
				}
				return key
			},
		},
		{
			name: "zoned-calendar-keys-on-spelling",
			why: "keying a zoned value on its lexical form rather than on " +
				"the instant, so xs:time('17:00:00Z') and " +
				"xs:time('12:00:00-05:00') stop colliding (same-key-027).",
			rewrite: func(a *Atomic, key string) string {
				if strings.Contains(key, ":tz:") {
					return a.Type.String() + ":lex:" + a.String()
				}
				return key
			},
		},

		// --- binary -------------------------------------------------------
		{
			name: "binary-keys-on-spelling",
			why: "keying a binary value on its lexical form. The value is " +
				"the octet sequence: XSD Part 2 §3.2.15 makes hexBinary " +
				"case-insensitive and §3.2.16 admits whitespace in base64, " +
				"so a value read from a document could fail to find itself.",
			rewrite: func(a *Atomic, key string) string {
				if isBin(a.Type) {
					return a.Type.String() + ":lex:" + a.String()
				}
				return key
			},
		},
		{
			name: "merge-binary-types",
			why: "one binary family over the decoded octets, so a " +
				"hexBinary and a base64Binary denoting the same octets " +
				"collide. They are separate types under 'eq'.",
			rewrite: func(a *Atomic, key string) string {
				if isBin(a.Type) {
					if o, ok := decodeBinary(a); ok {
						return "bin:" + string(o)
					}
				}
				return key
			},
		},

		// --- QName --------------------------------------------------------
		{
			name: "qname-ignores-namespace",
			why: "keying a QName on its local part alone, which is the " +
				"shape a prefix-free key invites. The namespace URI is part " +
				"of the value.",
			rewrite: func(a *Atomic, key string) string {
				if a.Type == TypeQName {
					if q := a.QName(); q != nil {
						return "qname:" + q.Local
					}
				}
				return key
			},
		},

		// --- the encoding itself ------------------------------------------
		{
			name: "drop-type-prefix",
			why: "dropping the family prefix so the key is the bare lexical " +
				"form, which makes xs:string('1') collide with xs:integer(1) " +
				"and xs:boolean('true') with xs:string('true').",
			rewrite: func(a *Atomic, key string) string {
				if i := strings.Index(key, ":"); i >= 0 {
					return key[i+1:]
				}
				return key
			},
		},
	}
}

// TestSameKeyMutationLane is the adequacy measurement.
//
// For each mutant it re-runs exactly the comparison TestMapKeyOfMatchesSameKey
// makes -- production keys against the SameKey oracle over the shared corpus --
// with the mutant's keys substituted, and requires a disagreement to appear.
// A mutant that survives is a hole in the corpus, reported with the mutant's
// rationale so the missing values can be added.
func TestSameKeyMutationLane(t *testing.T) {
	vals := sameKeyCorpus(t)
	keys := make([]string, len(vals))
	for i, v := range vals {
		k, err := MapKeyOf(v)
		if err != nil {
			t.Fatalf("MapKeyOf(%s %q): %v", v.Type, v.String(), err)
		}
		keys[i] = k
	}

	mutants := sameKeyMutants()
	if len(mutants) < 15 {
		t.Fatalf("the lane is meant to be a spread of plausible faults, not a sample; got %d", len(mutants))
	}

	for _, m := range mutants {
		t.Run(m.name, func(t *testing.T) {
			mutated := make([]string, len(vals))
			touched := 0
			for i, v := range vals {
				mutated[i] = m.rewrite(v, keys[i])
				if mutated[i] != keys[i] {
					touched++
				}
			}
			// Condition 1: the mutation must be live. A mutant that rewrote
			// nothing is dead code, and would otherwise be reported as
			// "caught" by whatever the corpus already disagrees about.
			if touched == 0 {
				t.Fatalf("the mutation is dead: it rewrote none of the %d corpus keys, "+
					"so this run measured the unmutated encoding.\n"+
					"  fault modelled: %s\n"+
					"Either the corpus holds no value that reaches the mutated rule, "+
					"or the rewrite no longer matches the key shape MapKeyOf builds.",
					len(vals), m.why)
			}

			// Condition 2: the corpus must notice.
			for i := range vals {
				for j := range vals {
					want, ok := SameKey(vals[i], vals[j])
					if !ok {
						// The oracle fails closed. An unjudged pair cannot
						// witness a mutant either way, and silently skipping
						// it would let a mutant be reported as surviving (or
						// as caught) on a comparison that never happened.
						t.Fatalf("unsupported oracle representation: no op:same-key model "+
							"for %s %q vs %s %q; the mutation lane cannot measure the "+
							"corpus over a pair the oracle has no opinion about",
							vals[i].Type, vals[i].String(), vals[j].Type, vals[j].String())
					}
					if want != (mutated[i] == mutated[j]) {
						// Caught, and the pair is named so a future
						// narrowing of the corpus can be judged.
						t.Logf("caught by %s %q vs %s %q (%d of %d keys rewritten)",
							vals[i].Type, vals[i].String(),
							vals[j].Type, vals[j].String(), touched, len(vals))
						return
					}
				}
			}
			t.Errorf("MUTANT SURVIVED: the corpus cannot tell this fault from the correct encoding.\n"+
				"  fault modelled: %s\n"+
				"  %d of %d corpus keys were rewritten, and no pair changed its verdict.\n"+
				"This is a finding about the corpus, not about the mutant: add values to "+
				"sameKeyCorpus that distinguish the two encodings.", m.why, touched, len(vals))
		})
	}
}

// TestSameKeyMutationLaneBaselineIsClean measures the lane itself.
//
// The lane above measures the corpus. This one measures the lane: it asserts
// that the unmutated encoding survives the same double loop with no
// disagreement, so that a "caught" verdict above is attributable to the
// mutation rather than to a corpus that disagrees with the oracle already.
// Without it, a broken MapKeyOf would make every mutant look caught and the
// lane would report a clean sheet while measuring nothing.
func TestSameKeyMutationLaneBaselineIsClean(t *testing.T) {
	vals := sameKeyCorpus(t)
	keys := make([]string, len(vals))
	for i, v := range vals {
		k, err := MapKeyOf(v)
		if err != nil {
			t.Fatalf("MapKeyOf(%s %q): %v", v.Type, v.String(), err)
		}
		keys[i] = k
	}
	for i := range vals {
		for j := range vals {
			want, ok := SameKey(vals[i], vals[j])
			if !ok {
				t.Fatalf("unsupported oracle representation: no op:same-key model for "+
					"%s %q vs %s %q; the baseline cannot be called clean over a pair "+
					"the oracle cannot judge",
					vals[i].Type, vals[i].String(), vals[j].Type, vals[j].String())
			}
			if want != (keys[i] == keys[j]) {
				t.Fatalf("the unmutated baseline already disagrees at %s %q vs %s %q; "+
					"every mutant above would report as caught for the wrong reason",
					vals[i].Type, vals[i].String(), vals[j].Type, vals[j].String())
			}
		}
	}
}
