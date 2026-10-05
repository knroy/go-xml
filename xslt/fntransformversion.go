package xslt

import (
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// transformXSLTVersion reads fn:transform's xslt-version option and returns
// the CompileOptions.MaxVersion of the processor F&O 3.1 14.7.1 selects for
// it: "If a processor that implements the requested XSLT version is
// available, then it is used. Otherwise, if a processor that implements a
// version later than the requested version is available, then it is used.
// Otherwise, the function fails" with FOXT0001.
//
// This engine is two processors: XSLT 2.0 (MaxVersion 2.0, the processor the
// XSLT 2.0 suite runs) and XSLT 3.0 (zero). So 2.0 selects the 2.0 one, and
// so does 1.0, for which 2.0 is the nearest later version; anything above 2.0
// up to 3.0 selects 3.0, and anything later has no processor.
//
// The option is declared xs:decimal, and the option parameter conventions
// convert the value by the function conversion rules: an xs:integer is a
// decimal already, an untyped value -- or a node, atomized -- is cast, and
// anything else is XPTY0004. fn-transform-err-4 passes the string "2.0".
//
// An absent option leaves the processor at 3.0. F&O defaults the request to
// the stylesheet's own version, which for a version="2.0" stylesheet would
// select the 2.0 processor; that default is not followed (docs/known-gaps.md).
func transformXSLTVersion(opts *xdm.MapItem) (float64, error) {
	seq, ok := transformOption(opts, "xslt-version")
	if !ok {
		return 0, nil
	}
	it, err := seq.Single()
	if err != nil {
		return 0, xdm.ErrType("fn:transform: xslt-version must be a single xs:decimal")
	}
	var a *xdm.Atomic
	switch v := it.(type) {
	case *xdm.Node:
		a = xdm.NewUntypedAtomic(v.StringValue())
	case *xdm.Atomic:
		a = v
	default:
		return 0, xdm.ErrType(
			"fn:transform: xslt-version must be an xs:decimal, got %s", it.TypeName())
	}
	if a.Type == xdm.TypeUntypedAtomic {
		if a, err = xpath.CastAtomic(a, xdm.TypeDecimal); err != nil {
			return 0, err
		}
	}
	if a.Type != xdm.TypeDecimal && a.Type != xdm.TypeInteger {
		return 0, xdm.ErrType(
			"fn:transform: xslt-version must be an xs:decimal, got %s", a.TypeName())
	}
	v, _ := a.Rat().Float64()
	switch {
	case v <= 2.0:
		return 2.0, nil
	case v <= 3.0:
		return 0, nil
	}
	return 0, xdm.Errorf("FOXT0001",
		"fn:transform: no XSLT processor implementing version %s is available", a.String())
}

// xslt20Options is the option set F&O 3.1 lists for invoking an XSLT 2.0
// processor, xslt-version aside. Of anything else it says "if anything else
// is present, it is ignored" -- global-context-item and initial-function
// among it, which fn-transform-82e passes to a 2.0 processor.
var xslt20Options = map[string]bool{
	"stylesheet-location": true, "stylesheet-node": true, "stylesheet-text": true,
	"source-node": true, "initial-mode": true, "initial-template": true,
	"stylesheet-base-uri": true, "stylesheet-params": true, "base-output-uri": true,
	"delivery-format": true, "serialization-params": true, "enable-messages": true,
	"enable-trace": true, "requested-properties": true, "vendor-options": true,
	"cache": true, "xslt-version": true,
}

// optionsFor20 drops from opts every entry an XSLT 2.0 processor ignores.
func optionsFor20(opts *xdm.MapItem) (*xdm.MapItem, error) {
	b := xdm.NewMapBuilder()
	err := opts.Entries(func(key *xdm.Atomic, value xdm.Sequence) error {
		if key.QName() != nil || !xslt20Options[key.String()] {
			return nil
		}
		return b.Set(key, value)
	})
	if err != nil {
		return nil, err
	}
	return b.Build(), nil
}
