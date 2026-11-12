package xslt

// The global context item of a streamed principal input (lane L7,
// streamable-116).
//
// XSLT 3.0 §2.3.2 (Priming a Stylesheet; specs/xslt-30.html): "The value
// given to the global context item ... cannot be nodes in a streamed
// document. This rule ensures that all global variables can freely navigate
// within the relevant tree" and "Since the global context item cannot be a
// streamed node, in cases where the transformation is to proceed by applying
// streamable templates to a streamed input document, the global context
// item must either be absent, or must be something that differs from the
// initial match selection." TransformReader is the API that supplies the
// principal input in streamable form, so when its initial mode is declared
// streamable the global context item is absent -- whether the run then
// streams or falls back to a tree (TransformReader's own Parse + Transform),
// so the two are byte-identical. A global reading the focus raises XPDY0002
// (streamable-116 expects it). Transform, given a tree, is unchanged.

// streamedSourceMode reports whether TransformReader's input is the initial
// match selection of a mode declared streamable.
func (s *Stylesheet) streamedSourceMode(opts TransformOptions) bool {
	if opts.InitialTemplate != "" || opts.InitialFunction.Local != "" || opts.InitialMatchSelection != nil {
		return false
	}
	m, err := s.initialModeOf(opts)
	return err == nil && s.modeStreamable[m]
}

// noteModeStreamable records an xsl:mode's @streamable for name: a higher
// import precedence wins, an equal one is the later declaration, and a used
// package's never displaces the using package's (as modeNoMatch).
func (c *compiler) noteModeStreamable(name string, yes bool, precedence int) {
	if c.sheet.modeStreamable == nil {
		c.sheet.modeStreamable = map[string]bool{}
		c.sheet.modeStreamablePrec = map[string]int{}
	}
	rank := precedence
	if c.usedPackageDepth > 0 {
		rank = -1 - c.usedPackageDepth
	}
	if prev, seen := c.sheet.modeStreamablePrec[name]; !seen || rank >= prev {
		c.sheet.modeStreamable[name] = yes
		c.sheet.modeStreamablePrec[name] = rank
	}
}
