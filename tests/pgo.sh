#!/bin/sh
# Regenerates cmd/go-xml/default.pgo, the CPU profile `go build` uses for
# profile-guided optimisation of the CLI (Go 1.21+; -pgo=auto is the default).
#
#   sh tests/pgo.sh
#
# Needs the local, untracked corpora: testdata/bench (bench/fetch-inputs.sh),
# testdata/xsltng, testdata/xslt30-test and testdata/qt3tests. It does not use
# the bench/ harness itself.
#
# It builds the CLI with an overlay whose main() writes a CPU profile to
# $GOXML_CPUPROFILE, runs each benchmark workload family cold (one process per
# item, as a user would) and merges the profiles. The reps counts below repeat
# a family so each contributes roughly the same CPU samples (about 1 s, printed
# per family), so no single workload dominates. Takes about two minutes: each
# profiled process spends ~0.2 s flushing its profile on exit.
# Regenerate after a change that moves the hot paths; a stale profile only
# costs optimisation, never correctness.
set -eu
cd "$(dirname "$0")/.."
R=$PWD
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

awk '{print} /^func main\(\) \{$/ {print "\tdefer startProfile()()"}' cmd/go-xml/main.go >"$T/main.go"
cat >"$T/prof.go" <<'EOF'
package main

import (
	"os"
	"runtime/pprof"
)

func startProfile() func() {
	f, err := os.Create(os.Getenv("GOXML_CPUPROFILE"))
	if err != nil {
		panic(err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		panic(err)
	}
	return func() { pprof.StopCPUProfile(); f.Close() }
}
EOF
cat >"$T/overlay.json" <<EOF
{"Replace": {"$R/cmd/go-xml/main.go": "$T/main.go", "$R/cmd/go-xml/zz_pgo_prof.go": "$T/prof.go"}}
EOF
go build -pgo=off -overlay "$T/overlay.json" -o "$T/go-xml" ./cmd/go-xml

cat >"$T/identity.xsl" <<'EOF'
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:mode on-no-match="shallow-copy"/>
</xsl:stylesheet>
EOF

n=0
run() { # family args...
	fam=$1
	shift
	n=$((n + 1))
	GOXML_CPUPROFILE="$T/$fam.$n.pprof" "$T/go-xml" "$@" >/dev/null
}
reps() { # count family-function
	i=0
	while [ $i -lt "$1" ]; do $2; i=$((i + 1)); done
}

B=testdata/bench
peppol() {
	for s in CEN-EN16931-UBL PEPPOL-EN16931-UBL; do
		for f in $B/peppol/invoices/*.xml; do run peppol -xsl $B/peppol/xsl/$s.xsl "$f"; done
	done
}
xrechnung() {
	for f in $B/xrechnung/invoices/*.xml; do
		run xrechnung -xsl $B/xrechnung/xsl/ubl-invoice-xr.xsl "$f"
	done
}
docbook() {
	for d in indexterm.001 chapter.003 ptoc.001 book.001 blocks.002 table-cals.001 refentry.001 glossary.001; do
		run docbook -xsl testdata/xsltng/src/main/xslt/docbook.xsl -allow-dir testdata/xsltng \
			-allow-unparsed-text -allow-doctype -compat-drop-attributes-on-document \
			-p dc-metadata=false -p generator-metadata=false \
			testdata/xsltng/src/test/resources/xml/$d.xml
	done
}
xmark() {
	for q in $B/xmark/queries/q*.xq; do run xmark xquery -q "$q" $B/xmark/auction-0.1.xml; done
}
xsd() {
	for f in tests/fn/unparsed-text-lines/_unparsed-text-lines-test-set.xml \
		tests/decl/attribute-set/_attribute-set-test-set.xml \
		tests/fn/xml-to-json/_xml-to-json-test-set.xml \
		tests/misc/regex-syntax/_regex-syntax-test-set.xml; do
		run xsd validate -xsd testdata/xslt30-test/admin/catalog-schema.xsd -xsd-version 1.0 -quiet \
			testdata/xslt30-test/$f
	done
	run xsd validate -xsd testdata/qt3tests/catalog-schema.xsd -xsd-version 1.0 -quiet \
		testdata/qt3tests/prod/AxisStep.xml
}
rng() {
	for d in programlisting.004 book.022 bibliography.006 table-cals.021 table-html.011 reference.001 glossary.007 section.004; do
		run rng validate -rng $B/docbook/rng/docbook.rng -quiet testdata/xsltng/src/test/resources/xml/$d.xml
	done
}
parse() {
	run parse -xsl "$T/identity.xsl" $B/parse/parse-10mb.xml
}

reps 2 peppol
reps 10 xrechnung
reps 1 docbook
reps 1 xmark
reps 50 xsd
reps 7 rng
reps 1 parse

for fam in peppol xrechnung docbook xmark xsd rng parse; do
	go tool pprof -proto "$T"/$fam.*.pprof >"$T/$fam.merged" 2>/dev/null
	printf '%-10s %s\n' $fam "$(go tool pprof -top "$T/$fam.merged" 2>/dev/null | grep -m1 'Total samples')"
done
go tool pprof -proto "$T"/*.merged >cmd/go-xml/default.pgo 2>/dev/null
ls -l cmd/go-xml/default.pgo
