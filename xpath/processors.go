package xpath

import (
	"github.com/knroy/go-xml/xdm"
)

// fn:transform and fn:load-xquery-module each need a processor for another
// language, and this package depends on neither: xslt and xquery both import
// xpath, so xpath importing either would close a cycle, and the two
// importing each other directly would close another. Each host language
// therefore registers itself here when its package is initialised, the way a
// database/sql driver does, and a program that imports both gets both
// functions from XPath, XQuery and XSLT alike.
//
// A program that imports neither keeps the stubs' answers -- FOXT0004 and
// FOQM0006 -- which F&O 3.1 defines for exactly that case.
//
// A processor is called with the caller's Context, so it inherits the
// caller's resolvers, budgets and deadline: a query reaching a file through
// fn:transform reaches only what fn:doc could.

// TransformProcessor runs fn:transform with the given options map and
// returns the result map F&O 3.1 section 14.7.1 describes.
type TransformProcessor func(ctx *Context, options *xdm.MapItem) (xdm.Sequence, error)

// XQueryModuleLoader runs fn:load-xquery-module for the module URI and the
// options map (nil for the one-argument form), returning the map F&O 3.1
// section 14.6.1 describes.
type XQueryModuleLoader func(ctx *Context, moduleURI string, options *xdm.MapItem) (xdm.Sequence, error)

var (
	transformProcessor TransformProcessor
	xqueryModuleLoader XQueryModuleLoader
)

// RegisterTransformProcessor installs the processor behind fn:transform. It
// is meant for the xslt package's init and is not safe to call concurrently
// with evaluation.
func RegisterTransformProcessor(p TransformProcessor) { transformProcessor = p }

// RegisterXQueryModuleLoader installs the processor behind
// fn:load-xquery-module. It is meant for the xquery package's init and is not
// safe to call concurrently with evaluation.
func RegisterXQueryModuleLoader(l XQueryModuleLoader) { xqueryModuleLoader = l }
