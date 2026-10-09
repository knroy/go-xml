package c14n_test

import (
	"crypto/sha256"
	"fmt"

	"github.com/knroy/go-xml/v2/c14n"
	"github.com/knroy/go-xml/v2/xdm"
)

// Exclusive canonicalization of a signed subtree, as a WS-Security
// Reference with an ec:InclusiveNamespaces PrefixList would request it. The
// Body renders only the prefixes it visibly uses (soap, wsu) plus the one
// the PrefixList names (ext); the unrelated declaration on the Envelope is
// dropped, which is what lets the subtree move between envelopes.
func ExampleOptions_prefixList() {
	tr, err := xdm.ParseString(`<soap:Envelope xmlns:soap="urn:soap" xmlns:wsu="urn:wsu" xmlns:ext="urn:ext" xmlns:other="urn:other">`+
		`<soap:Body wsu:Id="body"><m:Op xmlns:m="urn:m">hi</m:Op></soap:Body></soap:Envelope>`, xdm.ParseOptions{})
	if err != nil {
		panic(err)
	}
	body := tr.Root.Children[0].Children[0]
	out, err := c14n.Bytes(body, c14n.Options{
		Algorithm:                  c14n.Exclusive10,
		InclusiveNamespacePrefixes: c14n.ParsePrefixList("ext"),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(string(out))
	// Output:
	// <soap:Body xmlns:ext="urn:ext" xmlns:soap="urn:soap" xmlns:wsu="urn:wsu" wsu:Id="body"><m:Op xmlns:m="urn:m">hi</m:Op></soap:Body>
}

// The enveloped-signature transform: the document minus the ds:Signature
// element that holds the reference, then canonicalized and digested.
// Digest streams into the hash, so the canonical octets are never held in
// memory; Bytes is used here only to show them.
func ExampleExcludeSubtree() {
	tr, err := xdm.ParseString(`<Order xmlns="urn:o" id="42"><Item>tea</Item>`+
		`<ds:Signature xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:SignedInfo/></ds:Signature></Order>`, xdm.ParseOptions{})
	if err != nil {
		panic(err)
	}
	order := tr.Root.Children[0]
	sig := order.Children[1]
	set := c14n.ExcludeSubtree(tr.Root, sig)
	opts := c14n.Options{Algorithm: c14n.Exclusive10}

	out, err := c14n.BytesNodeSet(set, opts)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(out))

	sum, err := c14n.DigestNodeSet(sha256.New(), set, opts)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%x\n", sum)
	// Output:
	// <Order xmlns="urn:o" id="42"><Item>tea</Item></Order>
	// f657247c609c986de332beaa737e807ea02422cdd907a50e5ca393ab38a19ed4
}
