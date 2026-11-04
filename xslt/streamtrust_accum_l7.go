package xslt

// nonStreamableAccumulator is the trust a call on fn:accumulator-before or
// -after places in an accumulator its module declares not streamable.
//
// §19.8.9.1 and §19.8.9.2 make the call grounded whatever the accumulator
// is, and nothing it does reads the stream: on a node of a document
// xsl:source-document streams, the call raises XTDE3362 (§18.2.9), which
// fnAccumulator raises in both modes alike, before the value is looked up
// (streamedTrees is set by observeAccums for the streamed run as by the
// tree's load); on any other node it reads a tree the stylesheet already
// holds. The observer never computes such an accumulator (accumRefusal skips
// it for a source-document), so its rules are never run over the stream.
// error-3362a/b are the cases.
//
// The declaration found is the one in force only when no other module can
// declare the name at a higher import precedence, as for every other trust
// point.
func (t *declTrust) nonStreamableAccumulator() bool {
	return t.local()
}
