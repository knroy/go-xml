#!/usr/bin/env bash
# Runs the c14n differential against xmlsec1 (TestC14NDifferentialXMLSec1) in a
# Debian container, for machines without xmlsec1: it needs only Docker. The
# W3C interop cases are compared too when tests/fetch-c14n.sh has fetched them.
#
# Usage: tests/c14n-xmlsec1.sh [bench]
#   bench   also runs TestC14NThroughputXMLSec1, the throughput comparison
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
run='TestC14NDifferentialXMLSec1'
bench=
if [ "${1:-}" = bench ]; then
	run='XMLSec1'
	bench=1
fi

docker run --rm -v "$root":/src -w /src golang:1.25 bash -c "
	apt-get update -qq && apt-get install -y -qq xmlsec1 >/dev/null &&
	xmlsec1 --version &&
	GOXML_C14N_XMLSEC1=1 GOXML_C14N_XMLSEC1_BENCH=$bench GOFLAGS=-buildvcs=false \
		go test ./tests/c14n -run '$run' -count=1 -v"
