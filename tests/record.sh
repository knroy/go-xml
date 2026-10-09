#!/bin/sh
# record.sh — record what this checkout produces for every suite and corpus,
# case by case, for tests/recdiff to compare against another checkout's
# recording. See docs/testing.md, "Output differential".
#
#   tests/record.sh <outdir>
#   go run ./tests/recdiff compare -allow tests/recdiff/allow.txt <outA> <outB>
#
# Every path handed to the engine is the testdata directory with its symlinks
# resolved, so two checkouts sharing one testdata/ record identical base URIs
# and error paths. Suite logs go to <outdir>/logs; their in-scope lines must
# match the usual gate figures, since recording may not change a result.
#
# Three streams run in parallel; GO overrides the go binary.
set -eu
cd "$(dirname "$0")/.."
GO="${GO:-go}"
[ $# -eq 1 ] || { echo "usage: tests/record.sh <outdir>" >&2; exit 2; }
mkdir -p "$1"
OUT=$(cd "$1" && pwd -P)
[ -z "$(ls -A "$OUT" | grep -v '^logs$' || true)" ] || { echo "$OUT is not empty" >&2; exit 2; }
TD=$(cd testdata && pwd -P)
L="$OUT/logs"
mkdir -p "$L"
export GOXSLT_RECORD_DIR="$OUT"
NOW=2024-01-15T09:30:00-05:00
BIN="$OUT/logs/go-xml"
RD="$OUT/logs/recdiff"
$GO build -o "$BIN" ./cmd/go-xml
$GO build -o "$RD" ./tests/recdiff
# A fixed scratch path, not mktemp: -o names the output's base URI, which a
# stylesheet can see, so a random directory would differ between recordings.
# Two recordings must therefore not run at the same time.
T="${TMPDIR:-/tmp}/goxml-record"
rm -rf "$T"
mkdir -p "$T"
trap 'rm -rf "$T"' EXIT

# runxsl <suite> <stylesheet> <dir> <pattern> [flags...]: one transform per
# input, its output and its stderr (messages, errors) recorded per file.
runxsl() {
	_s=$1 _xsl=$2 _dir=$3 _pat=$4
	shift 4
	mkdir -p "$T/$_s"
	find "$_dir" -maxdepth 1 -type f -name "$_pat" | LC_ALL=C sort | while IFS= read -r _f; do
		_b=$(basename "$_f")
		"$BIN" -timeout 120s -now "$NOW" -xsl "$_xsl" "$@" -o "$T/$_s/$_b.out" "$_f" \
			2> "$T/$_s/$_b.err" || echo "exit $?" >> "$T/$_s/$_b.err"
	done
	"$RD" ingest "$OUT" "$_s" "$T/$_s"
}

streamA() {
	GOXSLT_QT3="$TD/qt3tests" $GO test -count=1 ./tests/qt3/ \
		-run '^(TestQT3|TestQT3XQuery)$' -v -timeout 120m > "$L/qt3.log" 2>&1 || true
}

streamB() {
	for t in TestXSLTSuite TestXSLT30Suite; do
		GOXSLT_XSLTS="$TD/xslt30-test" GOXSLT_XSLTS_VERBOSE=1 $GO test -count=1 ./tests/xslts/ \
			-run "^$t\$" -v -timeout 120m > "$L/$t.log" 2>&1 || true
	done
}

streamC() {
	$GO run ./tests/xsdsuite "$TD/xsdtests" > "$L/xsd10.log" 2>&1 || true
	$GO run ./tests/xsdsuite "$TD/xsdtests" -11 > "$L/xsd11.log" 2>&1 || true
	GOXSLT_RNG="$TD/relaxng/spectest.xml" $GO test -count=1 ./relaxng/ -run '^TestSpectest$' -v \
		> "$L/rng.log" 2>&1 || true
	$GO run ./tests/corpora vendored "$TD/xslt30-test" "$TD/qt3tests" "$TD/xspec" > "$L/vendored.log" 2>&1 || true

	# The stylesheet corpora, with tests/check.sh's flags.
	X="$TD/xsltng"
	runxsl docbook "$X/src/main/xslt/docbook.xsl" "$X/src/test/resources/xml" '*.xml' \
		-allow-dir "$X" -allow-unparsed-text -allow-doctype -xinclude -compat-drop-attributes-on-document
	runxsl xspec "$TD/xspec/src/compiler/compile-xslt-tests.xsl" "$TD/xspec/test" '*.xspec' \
		-allow-dir "$TD/xspec" -allow-unparsed-text

	# The benchmark workloads (bench/workloads). DocBook's 42 are a subset of
	# the corpus above; xrechnung stage 2 reads stage 1's recorded outputs.
	B="$TD/bench"
	runxsl peppol-cen "$B/peppol/xsl/CEN-EN16931-UBL.xsl" "$B/peppol/invoices" '*.xml'
	runxsl peppol "$B/peppol/xsl/PEPPOL-EN16931-UBL.xsl" "$B/peppol/invoices" '*.xml'
	runxsl xrechnung-xr "$B/xrechnung/xsl/ubl-invoice-xr.xsl" "$B/xrechnung/invoices" '*.xml'
	mkdir -p "$T/xr"
	for _f in "$T"/xrechnung-xr/*.out; do cp "$_f" "$T/xr/$(basename "$_f" .out)"; done
	runxsl xrechnung-html "$B/xrechnung/xsl/xrechnung-html.xsl" "$T/xr" '*.xml' -allow-unparsed-text
	for _d in 0.01 0.1; do
		mkdir -p "$T/xmark-$_d"
		for _q in $(seq 1 20); do
			"$BIN" xquery -now "$NOW" -q "$B/xmark/queries/q$_q.xq" -o "$T/xmark-$_d/q$_q.out" \
				"$B/xmark/auction-$_d.xml" 2> "$T/xmark-$_d/q$_q.err" || echo "exit $?" >> "$T/xmark-$_d/q$_q.err"
		done
		"$RD" ingest "$OUT" "xmark-$_d" "$T/xmark-$_d"
	done
	"$RD" c14n "$OUT" "$B/parse/parse-1mb.xml" "$B/parse/parse-10mb.xml" "$B/parse/parse-100mb.xml"
}

start=$(date +%s)
streamA & a=$!
streamB & b=$!
streamC & c=$!
wait $a $b $c
echo "recorded in $(( $(date +%s) - start ))s: $OUT"
grep -h 'in-scope:\|spectest:\|^TOTAL\|^vendored' "$L"/*.log || true
