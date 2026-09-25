#!/usr/bin/env bash
# Downloads the W3C Canonical XML material listed in tests/c14n-corpus.txt into
# testdata/c14n/: the C14N 1.1 interop cases TestW3CC14N11Interop runs, and the
# three Recommendations for reference.
#
# It is third-party material, so like the other W3C suites it lives under the
# untracked testdata/ rather than in the repository. Unlike them it is small
# and has not changed since 2008, so it is fetched file by file from w3.org,
# and each file is checked against the digest the manifest pins. A file that is
# already present and matches is not fetched again, so running this on every
# check costs one hash per file.
#
# Usage: tests/fetch-c14n.sh [dest]   (dest defaults to testdata/c14n)
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
dest=${1:-$root/testdata/c14n}
manifest=$root/tests/c14n-corpus.txt

if command -v sha256sum >/dev/null 2>&1; then
	sha() { sha256sum "$1" | cut -d' ' -f1; }
else
	sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
fi

fetched=0 present=0
while read -r want path url; do
	case $want in '' | '#'*) continue ;; esac
	f=$dest/$path
	if [ -f "$f" ] && [ "$(sha "$f")" = "$want" ]; then
		present=$((present + 1))
		continue
	fi
	mkdir -p "$(dirname "$f")"
	curl -fsSL --retry 3 -o "$f.part" "$url"
	got=$(sha "$f.part")
	if [ "$got" != "$want" ]; then
		rm -f "$f.part"
		echo "fetch-c14n: $url: sha256 $got, manifest pins $want" >&2
		exit 1
	fi
	mv "$f.part" "$f"
	fetched=$((fetched + 1))
done < "$manifest"
echo "fetch-c14n: $fetched fetched, $present already present, in $dest"
