package xslt

import "github.com/knroy/go-xml/v2/xdm"

// A collected (not streamed) merge source whose merge key is a path
// (merge-081: select="name" over doc('weather-SE.xml')). How the key is
// written does not tell its type: the documents are whatever the anchors
// are, a validated one's nodes typed. But the source is collected before
// anything is read off a cursor (runStream, pass 0), so its key values are
// known then: taken for strings at compile time, the merge goes to the tree
// unless every one is an xs:untypedAtomic or an xs:string.

// collectedNodeKey is the class of key k of source s when s is collected
// and the key a path to elements or attributes: "string", recorded in
// i.nodeKeys for the check at run time; "" otherwise.
func (i *mergeInstr) collectedNodeKey(s, k int) string {
	src := i.sources[s]
	el := src.keyElems[k]
	if src.streamed && src.sourceURIs != nil || el.Attr("", "select") == nil || el.Attr("", "data-type") != nil ||
		!nodesOnly(parseAt(el, el.AttrValue("select"))) {
		return ""
	}
	i.nodeKeys = append(i.nodeKeys, [2]int{s, k})
	return "string"
}

// nodeKeysUntyped reports whether the entries collected for source s have,
// at each of its key positions in i.nodeKeys, a string or untyped value.
func (i *mergeInstr) nodeKeysUntyped(s int, got []mergeEntry) bool {
	for _, sk := range i.nodeKeys {
		if sk[0] != s {
			continue
		}
		for _, e := range got {
			if a := e.atoms[sk[1]]; a != nil && a.Type != xdm.TypeUntypedAtomic && a.Type != xdm.TypeString {
				return false
			}
		}
	}
	return true
}
