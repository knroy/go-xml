package xslt

import (
	"fmt"
	"io"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// SecondaryResult is one document produced by xsl:result-document.
//
// It is kept separate from the principal result rather than merged into it:
// the whole point of the instruction is that the stylesheet author wants two
// distinct documents, and folding them together would give a caller expecting
// several outputs a single plausible-looking wrong one.
type SecondaryResult struct {
	// Href is the resolved @href value, as written by the stylesheet. It is
	// the caller's choice what to do with it — this engine never writes to
	// the filesystem on a stylesheet's behalf, since a transform that can
	// create files anywhere the process can write is a hazard the caller
	// should be the one to opt into.
	Href string
	// Nodes is the result sequence for this document.
	Nodes xdm.Sequence
	// Output holds the serialisation settings that apply to this document,
	// taken from @format and any serialisation attributes on the instruction.
	Output OutputSettings
}

// resultDocumentInstr implements xsl:result-document.
type resultDocumentInstr struct {
	href *avt
	// output is the settings resolved at compile time from @format plus any
	// serialisation attributes written directly on the instruction.
	output OutputSettings
	body   []Instruction
}

func (i *resultDocumentInstr) Execute(rt *runtime, out *outputBuilder) error {
	href := ""
	if i.href != nil {
		v, err := i.href.eval(rt)
		if err != nil {
			return err
		}
		href = v
	}

	// The body builds into its own builder, so nothing it produces reaches
	// the principal result. Passing `out` here is exactly the merging bug
	// this instruction used to be rejected to avoid.
	sub := newOutputBuilder()
	for _, instr := range i.body {
		if err := instr.Execute(rt, sub); err != nil {
			return err
		}
	}

	// Two result documents sharing an href would mean one silently
	// overwriting the other, so the collision is reported instead.
	for _, prev := range *rt.secondary {
		if prev.Href == href {
			return fmt.Errorf(
				"xsl:result-document: href %q was already produced by an earlier "+
					"result document", href)
		}
	}

	*rt.secondary = append(*rt.secondary, SecondaryResult{
		Href:   href,
		Nodes:  sub.sequence(),
		Output: i.output,
	})
	return nil
}

// Serialize writes the secondary document using its own output settings.
//
// A caller holding a SecondaryResult would otherwise have no way to render it:
// the serialiser is unexported, and re-deriving these settings from the
// stylesheet is exactly the duplication @format exists to avoid.
func (sr *SecondaryResult) Serialize(w io.Writer, charMap map[rune]string) error {
	return serialize(w, sr.Nodes, sr.Output, charMap)
}

// String renders the secondary document using its own output settings.
func (sr *SecondaryResult) String() string {
	var sb strings.Builder
	_ = sr.Serialize(&sb, nil)
	return sb.String()
}
