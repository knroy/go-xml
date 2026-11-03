package xslt

// The stream fuzzers' grammar for kept leaves and climbing (lane G4): tails
// that climb from a match or select leaves, selects that keep leaves or
// map a match to its ancestors, leaf patterns naming ancestors, and rule
// bodies reading a leaf's or an element's ancestors.

// fuzzTailsExtra are tails beside tail()'s own.
var fuzzTailsExtra = []string{"ancestor::*", "ancestor-or-self::*/@k", "ancestor::*/name()",
	"ancestor-or-self::*/data(@v)", "parent::node()/@k", "ancestor::*[1]/@id", "ancestor::*[@k]",
	"./ancestor-or-self::*", "ancestor::*/@*", "parent::*/name()", "text()/string-length()",
	"comment()", "node()/(. || '')"}

// fuzzSelectsExtra are selects beside selectExpr()'s own; P is the path.
var fuzzSelectsExtra = []string{"head(P) ! name()", "(P, 'x') ! name()", "subsequence(P, 2) ! name()",
	"tail(P) ! name()", "count(P)", "P ! ancestor::*", "(P ! ancestor::*) ! name()",
	"(P ! ancestor-or-self::*/@k) ! string()", "(P ! ancestor::*) instance of element()*",
	"head(P//text())", "(P//text())[2]", "P/text()", "count(P/text())", "copy-of(P/text())",
	"string-join(P/text(), '|')", "(P ! ancestor::*) ! @* ! string()", "P//comment()"}

func init() {
	fuzzPatterns = append(fuzzPatterns, "REC/text()", "sub/text()", "REC//text()", "text()[parent::sub]",
		"text()[not(parent::REC)]", "wrap//comment()", "ROOT/*/text()", "/ROOT/REC/text()")
	fuzzRuleBodies = append(fuzzRuleBodies, `<a n="{count(ancestor::*)}" p="{name(..)}"><xsl:apply-templates/></a>`,
		`<xsl:value-of select="ancestor-or-self::*/@k" separator="|"/>`, `<t p="{name(..)}" k="{../@k}"><xsl:value-of select="."/></t>`)
}
