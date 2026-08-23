package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

func main() {
	dir := "testdata/xslt30-test/tests/type/notation"
	schema := xsd.NewSchema()
	for _, f := range []string{"namespaceNotationTest.xsd", "importSchema.xsd"} {
		p := filepath.Join(dir, f)
		data, _ := os.ReadFile(p)
		tree, _ := xdm.ParseString(string(data), xdm.ParseOptions{})
		loaded, err := xsd.Load(tree.Root, p, xsd.Options{Resolver: &xsd.FileResolver{}})
		if err != nil {
			fmt.Println("load", f, err)
			continue
		}
		for n, t := range loaded.Types {
			if _, ok := schema.Types[n]; !ok {
				schema.Types[n] = t
			}
		}
		for n, d := range loaded.Elements {
			if _, ok := schema.Elements[n]; !ok {
				schema.Elements[n] = d
			}
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "notation-03.xml"))
	tree, _ := xdm.ParseString(string(data), xdm.ParseOptions{AllowDOCTYPE: true})
	fmt.Println("VALIDATE:", schema.Validate(tree.Root, xsd.ValidateOptions{Annotate: true}))
	fmt.Println("DerivedBase(not1-NOTATION-enumeration-Type) =", xdm.DerivedBase("not1-NOTATION-enumeration-Type"))
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Kind == xdm.KindAttribute && n.Name.Local == "NOTATION-attribute" {
			a := n.Atomize()
			fmt.Printf("  %q ann=%s -> atomic type=%v str=%q\n", n.StringValue(), n.TypeAnnotation, a.Type, a.String())
		}
		for _, a := range n.Attrs {
			walk(a)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree.Root)
}
