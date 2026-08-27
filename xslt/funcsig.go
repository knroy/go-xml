package xslt

// declaredSignature is the signature of an xsl:function in the form
// xdm.FunctionItem.Signature uses: the declared return type first, then the
// declared parameter types, each in source spelling.
//
// Recording it is what lets a typed function test judge a stylesheet function
// on what it actually declares. Without one, "local:f#2 instance of
// function(item()+, item()+) as element(e)" answered true for a function
// declared (xs:long?, xs:NCName?) -- a function item with no recorded
// signature is matched on arity alone, which is every arity-2 test there is.
//
// An omitted "as" is item()*, which is what sequenceType.source already
// returns for a nil type, so a partly annotated function still gets a
// signature that says the truth about the parts it declared.
func declaredSignature(returns *sequenceType, params []*Variable) []string {
	sig := make([]string, 0, len(params)+1)
	sig = append(sig, returns.source())
	for _, p := range params {
		sig = append(sig, p.asType.source())
	}
	return sig
}
