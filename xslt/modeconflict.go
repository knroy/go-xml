package xslt

import "fmt"

// checkModeConflicts reports XTSE0545 for a mode declared twice at the import
// precedence that wins.
//
// The rule is about the modes a package can see, and an importing module's
// xsl:mode masks the imported one entirely — so a tie among declarations that
// are all overridden is invisible and not an error. Judging it as each
// declaration was compiled reported exactly that invisible tie, because the
// module holding it had been compiled before the module that overrides it;
// and keeping only the last precedence seen per mode lost the earlier ones
// altogether, so a genuine tie could be missed as easily as one invented.
// checkAccumulatorConflicts settles XTSE3350 the same way for the same
// reason.
func (c *compiler) checkModeConflicts() error {
	for m, precs := range c.modeTies {
		best := precs[0]
		n := 0
		for _, p := range precs {
			if p > best {
				best, n = p, 0
			}
			if p == best {
				n++
			}
		}
		if n > 1 {
			name := m
			if name == "" {
				name = "#unnamed"
			}
			return fmt.Errorf(
				"XTSE0545: mode %s is declared by more than one xsl:mode at "+
					"the same import precedence", name)
		}
	}
	return nil
}
