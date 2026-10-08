#!/bin/sh
# Downloads the pinned reference engines into bench/.cache/ and verifies each
# file's SHA-256. Idempotent: a file already present with the right digest is
# kept; a file present with the wrong digest is refused, never overwritten.
# The pins below must match bench/engines.json (a test checks they do).
set -eu

cd "$(dirname "$0")"
CACHE=.cache
M=https://repo1.maven.org/maven2

if command -v sha256sum >/dev/null 2>&1; then
	sha() { sha256sum "$1" | cut -d' ' -f1; }
else
	sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
fi

# fetch DEST SHA256 URL
fetch() {
	dest=$CACHE/$1
	want=$2
	url=$3
	if [ -f "$dest" ]; then
		got=$(sha "$dest")
		if [ "$got" != "$want" ]; then
			echo "fetch-engines: $dest: sha256 $got, want $want; refusing (delete it to re-fetch)" >&2
			exit 1
		fi
		echo "ok      $dest"
		return
	fi
	mkdir -p "$(dirname "$dest")"
	curl -fsSL -o "$dest.part" "$url"
	got=$(sha "$dest.part")
	if [ "$got" != "$want" ]; then
		rm -f "$dest.part"
		echo "fetch-engines: $url: sha256 $got, want $want; refusing" >&2
		exit 1
	fi
	mv "$dest.part" "$dest"
	echo "fetched $dest"
}

# pin lines: DEST SHA256 URL
fetch saxon-he/Saxon-HE-12.10.jar b571af282f25d7301059f788b9a149aab8b5cdc14ef3d212dc5425d3dcbb9a97 $M/net/sf/saxon/Saxon-HE/12.10/Saxon-HE-12.10.jar
fetch saxon-he/xmlresolver-5.3.3.jar 1fe4d5b92f708dcdb82dbce12919e0171e6b5ca62c6dca6220483625098feb5f $M/org/xmlresolver/xmlresolver/5.3.3/xmlresolver-5.3.3.jar
fetch saxon-he/xmlresolver-5.3.3-data.jar b0c487ad2f3e558be8d829c916d2458d10aca6a5bafa7a4d0524b70845e48a5c $M/org/xmlresolver/xmlresolver/5.3.3/xmlresolver-5.3.3-data.jar
fetch basex/basex-12.4.jar 3975bc91acfadd1c139872dd9fc154f2fc7a166792509c7665501089edf5e301 $M/org/basex/basex/12.4/basex-12.4.jar
fetch jing/jing-20241231.jar ea5e9026244d977e607d8b52212d6871498ece51939f9c49d0e7a77aad91133a $M/org/relaxng/jing/20241231/jing-20241231.jar
fetch xerces-j-xsd11/Xerces-J-bin.2.12.2-xml-schema-1.1.zip 610d77b0e1a3d23a5224d528266ba1ec21eb7d671f595211e01c2303b8db7cb2 https://archive.apache.org/dist/xerces/j/binaries/Xerces-J-bin.2.12.2-xml-schema-1.1.zip

# The Xerces XSD 1.1 build ships only as a distribution archive.
X=$CACHE/xerces-j-xsd11
if [ ! -f "$X/xerces-2_12_2-xml-schema-1.1/xercesImpl.jar" ]; then
	unzip -qo "$X/Xerces-J-bin.2.12.2-xml-schema-1.1.zip" 'xerces-2_12_2-xml-schema-1.1/*.jar' -d "$X"
	echo "unpacked $X/xerces-2_12_2-xml-schema-1.1"
fi

# BaseX writes its configuration into its home directory; give it one here
# rather than letting it create ~/basex.
mkdir -p "$CACHE/basex/home"
