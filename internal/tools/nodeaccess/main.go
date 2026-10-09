// Command nodeaccess rewrites uses of xdm.Node's former exported fields into
// the v2 accessor API. It is the migration tool for the field-to-method change
// and is kept so that the change can be re-applied to code written against the
// old fields, such as a branch merged after the switch.
//
//	cd internal/tools/nodeaccess && go run . [-report] [-flip] [-v] <module root>
//
// It loads every package of the module, tests included, type-checked, so a
// field is recognised by the type of the expression it is selected from and
// never by its spelling alone. It edits files in place, reloads, and repeats
// until nothing is left that it knows how to rewrite, then prints what a
// person has to finish (lines starting MANUAL) and exits 1 if there are any.
//
// The rewrites, outside package xdm:
//
//	n.Kind, n.Name, n.Value, ...     -> n.Kind(), n.Name(), n.Value(), ...
//	len(n.Children)                  -> n.NumChildren()        (Attrs, Namespaces alike)
//	n.Children[0]                    -> n.FirstChild()
//	n.Children[len(n.Children)-1]    -> n.LastChild()
//	n.Children[i]                    -> n.ChildAt(i)
//	for _, c := range n.Children     -> for c := range n.Children()
//	for i := range n.Children        -> for i := range n.NumChildren()
//	for i, c := range n.Children {   -> for i := range n.NumChildren() { c := n.ChildAt(i)
//	n.F = v                          -> n.SetF(v)
//	xdmbuild.SetF(n, v)              -> n.SetF(v)
//	&xdm.Node{Kind: k, Name: q, Value: v, Parent: p, ...}
//	                                 -> xdm.NewNode(k, q, v), then .SetParent(p)...
//
// Namespaces becomes NamespaceDecls / NumNamespaceDecls / NamespaceDeclAt /
// SetNamespaceDecls. Inside package xdm every selector and literal key is
// renamed to the unexported field instead.
//
// The indexed range differs from the loop it replaces in one way: the slice
// was read once, ChildAt reads the current children each time. A body that
// replaces the children it is iterating needs a look; none did at the switch.
//
// Without -flip, and while xdm.Node still has its exported fields, only the
// rewrites whose new spelling differs from the field's run. -flip is used
// once, at the switch: it also renames the fields in the struct declaration
// and turns every read into a call. Once the getters exist the tool detects it
// and always does everything.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

const (
	xdmPath      = "github.com/knroy/go-xml/v2/xdm"
	xdmbuildPath = "github.com/knroy/go-xml/v2/xdmbuild"
)

type field struct {
	low  string // the unexported field inside xdm
	get  string // the getter outside it; for a slice, its iterator
	set  string // the setter, "" when there is none
	num  string // slice fields: the count
	at   string // slice fields: the indexed read
	slot int    // position in NewNode(kind, name, value), or -1
}

var fields = map[string]field{
	"Kind":             {"kind", "Kind", "", "", "", 0},
	"Name":             {"name", "Name", "SetName", "", "", 1},
	"Value":            {"value", "Value", "SetValue", "", "", 2},
	"Parent":           {"parent", "Parent", "SetParent", "", "", -1},
	"Children":         {"children", "Children", "SetChildren", "NumChildren", "ChildAt", -1},
	"Attrs":            {"attrs", "Attrs", "SetAttrs", "NumAttrs", "AttrAt", -1},
	"Namespaces":       {"namespaces", "NamespaceDecls", "SetNamespaceDecls", "NumNamespaceDecls", "NamespaceDeclAt", -1},
	"BaseURI":          {"baseURI", "BaseURI", "SetBaseURI", "", "", -1},
	"DocumentURI":      {"documentURI", "DocumentURI", "SetDocumentURI", "", "", -1},
	"TypeAnnotation":   {"typeAnnotation", "TypeAnnotation", "", "", "", -1},
	"UnionMember":      {"unionMember", "UnionMember", "", "", "", -1},
	"DerivedPrimitive": {"derivedPrimitive", "DerivedPrimitive", "", "", "", -1},
	"ListItem":         {"listItem", "ListItem", "", "", "", -1},
	"IsID":             {"isID", "IsID", "", "", "", -1},
	"IsIDREFS":         {"isIDREFS", "IsIDREFS", "", "", "", -1},
	"IsNilled":         {"isNilled", "IsNilled", "", "", "", -1},
	"NoTypedValue":     {"noTypedValue", "NoTypedValue", "", "", "", -1},
	"MixedContent":     {"mixedContent", "MixedContent", "", "", "", -1},
}

// typing are the literal keys that go through ApplyTyping.
var typing = map[string]bool{
	"TypeAnnotation": true, "UnionMember": true, "DerivedPrimitive": true, "ListItem": true,
	"IsID": true, "IsIDREFS": true, "IsNilled": true, "NoTypedValue": true, "MixedContent": true,
}

// buildSetters are the xdmbuild functions the xdm setters replace.
var buildSetters = map[string]string{
	"SetParent": "SetParent", "SetChildren": "SetChildren", "SetAttrs": "SetAttrs",
	"SetNamespaces": "SetNamespaceDecls", "SetName": "SetName", "SetBaseURI": "SetBaseURI",
}

type edit struct {
	start, end int
	text       string
	prio       int // order of inserts at one offset: inner first
}

var (
	reportOnly = flag.Bool("report", false, "list sites, change nothing")
	flipFlag   = flag.Bool("flip", false, "rename the fields and rewrite reads too")
	verbose    = flag.Bool("v", false, "print counts per kind of rewrite")

	fset    *token.FileSet
	edits   map[string][]edit
	issues  map[string]bool
	counts  map[string]int
	flipped bool // the getters exist, or -flip was given
	root    string
)

func main() {
	flag.Parse()
	root = "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	root, _ = filepath.Abs(root)
	for pass := 1; ; pass++ {
		n := run()
		fmt.Fprintf(os.Stderr, "pass %d: %d edits\n", pass, n)
		if *reportOnly || n == 0 {
			break
		}
		if pass == 10 {
			fmt.Fprintln(os.Stderr, "no fixed point after 10 passes")
			os.Exit(2)
		}
	}
	keys := make([]string, 0, len(issues))
	for k := range issues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Println("MANUAL", k)
	}
	if len(keys) > 0 {
		os.Exit(1)
	}
}

// run makes one pass and returns how many edits it applied.
func run() int {
	edits, issues, counts = map[string][]edit{}, map[string]bool{}, map[string]int{}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:   root,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(pkgs) == 0 {
		return 0
	}
	fset = pkgs[0].Fset
	flipped = *flipFlag
	for _, p := range pkgs {
		if p.PkgPath != xdmPath || p.Types == nil {
			continue
		}
		if obj := p.Types.Scope().Lookup("Node"); obj != nil {
			if m, _, _ := types.LookupFieldOrMethod(obj.Type(), true, p.Types, "Children"); m != nil {
				if _, isFunc := m.(*types.Func); isFunc {
					flipped = true
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			name := fset.File(f.Pos()).Name()
			if seen[name] || !strings.HasPrefix(name, root) {
				continue
			}
			seen[name] = true
			visit(p, f, name)
		}
	}
	if *verbose || *reportOnly {
		ks := make([]string, 0, len(counts))
		for k := range counts {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Fprintf(os.Stderr, "%6d %s\n", counts[k], k)
		}
	}
	if *reportOnly {
		return 0
	}
	total := 0
	for name, es := range edits {
		n, err := apply(name, es)
		if err != nil {
			fmt.Fprintln(os.Stderr, name+":", err)
			os.Exit(1)
		}
		total += n
	}
	return total
}

// isNode reports whether t is xdm.Node or *xdm.Node.
func isNode(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	return ok && n.Obj().Name() == "Node" && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == xdmPath
}

func visit(p *packages.Package, f *ast.File, file string) {
	inXDM := p.PkgPath == xdmPath
	xdmName := importName(f, xdmPath)
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if fld, ok := nodeField(p.TypesInfo, x, stack); ok {
				if !inXDM {
					site(p, file, x, fld, stack)
				} else if flipped {
					counts["xdm rename"]++
					add(file, x.Sel.Pos(), x.Sel.End(), fields[fld].low, 0)
				}
			}
		case *ast.CompositeLit:
			if isNode(p.TypesInfo.TypeOf(x)) {
				if !inXDM {
					literal(p, file, xdmName, x, stack)
				} else if flipped {
					renameKeys(file, x)
				}
			}
		case *ast.CallExpr:
			if !inXDM {
				buildCall(p, file, x)
			}
		case *ast.TypeSpec:
			if inXDM && flipped && x.Name.Name == "Node" {
				if st, ok := x.Type.(*ast.StructType); ok {
					for _, fl := range st.Fields.List {
						for _, id := range fl.Names {
							if fd, ok := fields[id.Name]; ok {
								add(file, id.Pos(), id.End(), fd.low, 0)
							}
						}
					}
				}
			}
		}
		return true
	})
}

// nodeField reports whether sel is a use of a former field of xdm.Node: by
// the field itself before the switch, or by its name on a node expression
// that is not being called after it.
func nodeField(info *types.Info, sel *ast.SelectorExpr, stack []ast.Node) (string, bool) {
	name := sel.Sel.Name
	if _, ok := fields[name]; !ok {
		return "", false
	}
	if s := info.Selections[sel]; s != nil && s.Kind() == types.FieldVal {
		v := s.Obj().(*types.Var)
		if v.Pkg() != nil && v.Pkg().Path() == xdmPath && isNode(fieldOwner(s)) {
			return name, true
		}
		return "", false
	}
	if t := info.TypeOf(sel.X); t == nil || !isNode(t) {
		return "", false
	}
	if c, ok := stack[len(stack)-2].(*ast.CallExpr); ok && c.Fun == sel {
		return "", false // already a getter call
	}
	return name, true
}

// fieldOwner returns the struct type that declares the selected field,
// following embedding.
func fieldOwner(s *types.Selection) types.Type {
	t := s.Recv()
	idx := s.Index()
	for _, i := range idx[:len(idx)-1] {
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		t = t.Underlying().(*types.Struct).Field(i).Type()
	}
	return t
}

func importName(f *ast.File, path string) string {
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == path {
			if im.Name != nil {
				return im.Name.Name
			}
			return path[strings.LastIndex(path, "/")+1:]
		}
	}
	return ""
}

func pos(n ast.Node) string {
	p := fset.Position(n.Pos())
	rel, err := filepath.Rel(root, p.Filename)
	if err != nil {
		rel = p.Filename
	}
	return fmt.Sprintf("%s:%d:%d", rel, p.Line, p.Column)
}

func manual(n ast.Node, what string) {
	issues[pos(n)+": "+what] = true
	counts["manual"]++
}

func off(p token.Pos) int { return fset.Position(p).Offset }

func add(file string, from, to token.Pos, text string, prio int) {
	edits[file] = append(edits[file], edit{off(from), off(to), text, prio})
}

func src(e ast.Node) string {
	var b bytes.Buffer
	format.Node(&b, fset, e)
	return b.String()
}

func renameKeys(file string, cl *ast.CompositeLit) {
	for _, el := range cl.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok {
				if f, ok := fields[id.Name]; ok {
					counts["xdm literal key"]++
					add(file, id.Pos(), id.End(), f.low, 0)
				}
			}
		}
	}
}

func isLHS(as *ast.AssignStmt, e ast.Expr) bool {
	for _, l := range as.Lhs {
		if l == e {
			return true
		}
	}
	return false
}

// site rewrites one use of a former field outside package xdm.
func site(p *packages.Package, file string, sel *ast.SelectorExpr, name string, stack []ast.Node) {
	f := fields[name]
	parent := stack[len(stack)-2]
	var grand ast.Node
	if len(stack) >= 3 {
		grand = stack[len(stack)-3]
	}

	switch q := parent.(type) {
	case *ast.AssignStmt:
		if isLHS(q, sel) {
			if q.Tok != token.ASSIGN || len(q.Lhs) != 1 || len(q.Rhs) != 1 || f.set == "" {
				manual(sel, "write to "+name)
				return
			}
			counts["write "+name]++
			add(file, sel.X.End(), q.Rhs[0].Pos(), "."+f.set+"(", 0)
			add(file, q.Rhs[0].End(), q.Rhs[0].End(), ")", 1)
			return
		}
	case *ast.IncDecStmt:
		manual(sel, "inc/dec of "+name)
		return
	case *ast.UnaryExpr:
		if q.Op == token.AND {
			manual(sel, "address of "+name)
			return
		}
	case *ast.SelectorExpr:
		if as, ok := grand.(*ast.AssignStmt); ok && isLHS(as, q) {
			manual(sel, "write through "+name+"; read it, change it, Set"+name)
			return
		}
		if u, ok := grand.(*ast.UnaryExpr); ok && u.Op == token.AND {
			manual(sel, "address through "+name)
			return
		}
	}

	if f.num == "" {
		if flipped {
			counts["read "+name]++
			add(file, sel.End(), sel.End(), "()", 0)
		}
		return
	}

	switch q := parent.(type) {
	case *ast.CallExpr:
		if id, ok := q.Fun.(*ast.Ident); ok && id.Name == "len" && len(q.Args) == 1 && q.Args[0] == sel {
			if _, ok := p.TypesInfo.Uses[id].(*types.Builtin); ok {
				counts["len "+name]++
				add(file, q.Pos(), sel.X.Pos(), "", 0)
				add(file, sel.X.End(), q.End(), "."+f.num+"()", 0)
				return
			}
		}
	case *ast.IndexExpr:
		if q.X != sel {
			break
		}
		if as, ok := grand.(*ast.AssignStmt); ok && isLHS(as, q) {
			manual(sel, "element write to "+name)
			return
		}
		if u, ok := grand.(*ast.UnaryExpr); ok && u.Op == token.AND {
			manual(sel, "address of an element of "+name)
			return
		}
		if name == "Children" {
			if lit, ok := q.Index.(*ast.BasicLit); ok && lit.Value == "0" {
				counts["first "+name]++
				add(file, sel.X.End(), q.End(), ".FirstChild()", 0)
				return
			}
			if b, ok := q.Index.(*ast.BinaryExpr); ok && b.Op == token.SUB && src(b.Y) == "1" {
				if c, ok := b.X.(*ast.CallExpr); ok && len(c.Args) == 1 && src(c.Fun) == "len" && src(c.Args[0]) == src(sel) {
					counts["last "+name]++
					add(file, sel.X.End(), q.End(), ".LastChild()", 0)
					return
				}
			}
		}
		counts["index "+name]++
		add(file, sel.X.End(), q.Index.Pos(), "."+f.at+"(", 0)
		add(file, q.Index.End(), q.End(), ")", 1)
		return
	case *ast.RangeStmt:
		if q.X != sel {
			break
		}
		key, _ := q.Key.(*ast.Ident)
		val, _ := q.Value.(*ast.Ident)
		if (q.Key != nil && key == nil) || (q.Value != nil && val == nil) {
			manual(sel, "range over "+name+" with a non-identifier variable")
			return
		}
		if val != nil && val.Name == "_" {
			val = nil
		}
		if val == nil {
			counts["range-count "+name]++
			if q.Value != nil {
				add(file, q.Key.End(), q.Value.End(), "", 0)
			}
			add(file, sel.Sel.Pos(), sel.End(), f.num+"()", 0)
			return
		}
		if key == nil || key.Name == "_" {
			if !flipped {
				return
			}
			counts["range-iter "+name]++
			if q.Key != nil {
				add(file, q.Key.Pos(), q.Value.Pos(), "", 0)
			}
			add(file, sel.Sel.Pos(), sel.End(), f.get+"()", 0)
			return
		}
		counts["range-indexed "+name]++
		add(file, q.Key.End(), q.Value.End(), "", 0)
		add(file, sel.Sel.Pos(), sel.End(), f.num+"()", 0)
		add(file, q.Body.Lbrace+1, q.Body.Lbrace+1,
			"\n"+val.Name+" "+q.Tok.String()+" "+src(sel.X)+"."+f.at+"("+key.Name+")", 1)
		return
	}
	manual(sel, "slice value of "+name+" ("+describe(parent)+")")
}

func describe(n ast.Node) string {
	switch q := n.(type) {
	case *ast.CallExpr:
		return "argument to " + src(q.Fun)
	case *ast.SliceExpr:
		return "sliced"
	case *ast.AssignStmt:
		return "assigned"
	case *ast.ReturnStmt:
		return "returned"
	case *ast.BinaryExpr:
		return "compared with " + q.Op.String()
	}
	return fmt.Sprintf("%T", n)
}

// buildCall rewrites xdmbuild.SetX(n, v) to n.SetX(v).
func buildCall(p *packages.Package, file string, c *ast.CallExpr) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || len(c.Args) != 2 {
		return
	}
	obj, ok := p.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok || obj.Pkg() == nil || obj.Pkg().Path() != xdmbuildPath {
		return
	}
	m, ok := buildSetters[obj.Name()]
	if !ok {
		return
	}
	recv := c.Args[0]
	switch recv.(type) {
	case *ast.Ident, *ast.SelectorExpr, *ast.CallExpr, *ast.IndexExpr, *ast.ParenExpr:
	default:
		manual(c, "xdmbuild."+obj.Name()+" on a compound receiver")
		return
	}
	counts["xdmbuild."+obj.Name()]++
	add(file, c.Pos(), recv.Pos(), "", 0)
	add(file, recv.End(), c.Args[1].Pos(), "."+m+"(", 0)
}

// literal turns an xdm.Node composite literal into xdm.NewNode plus setters.
func literal(p *packages.Package, file, xdmName string, cl *ast.CompositeLit, stack []ast.Node) {
	if xdmName == "" || xdmName == "." || xdmName == "_" {
		manual(cl, "xdm.Node literal in a file that does not import xdm by name")
		return
	}
	args := []string{xdmName + ".KindDocument", xdmName + ".QName{}", `""`}
	var sets, typ []string
	used := map[string]bool{}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			manual(cl, "unkeyed xdm.Node literal")
			return
		}
		id := kv.Key.(*ast.Ident)
		f := fields[id.Name]
		ast.Inspect(kv.Value, func(n ast.Node) bool {
			if i, ok := n.(*ast.Ident); ok {
				used[i.Name] = true
			}
			return true
		})
		v := src(kv.Value)
		switch {
		case f.slot >= 0:
			args[f.slot] = v
		case typing[id.Name]:
			typ = append(typ, id.Name+": "+v)
		case f.set != "":
			sets = append(sets, f.set+"("+v+")")
		default:
			manual(cl, "xdm.Node literal key "+id.Name)
			return
		}
	}
	if len(typ) > 0 {
		sets = append(sets, "ApplyTyping("+xdmName+".Typing{"+strings.Join(typ, ", ")+"})")
	}
	call := xdmName + ".NewNode(" + strings.Join(args, ", ") + ")"

	// The expression the literal stands for: &xdm.Node{...}, or the literal
	// itself where an elided element type is *xdm.Node.
	var whole ast.Node = cl
	pointer := false
	if u, ok := stack[len(stack)-2].(*ast.UnaryExpr); ok && u.Op == token.AND {
		whole, pointer = u, true
	} else if cl.Type == nil {
		if outer, ok := stack[len(stack)-2].(*ast.CompositeLit); ok {
			if sl, ok := p.TypesInfo.TypeOf(outer).Underlying().(*types.Slice); ok {
				_, pointer = sl.Elem().(*types.Pointer)
			}
		}
	}
	if !pointer {
		if len(sets) > 0 {
			manual(cl, "xdm.Node value literal with fields beyond kind, name and value")
			return
		}
		counts["literal value"]++
		add(file, whole.Pos(), whole.End(), "*"+call, 0)
		return
	}
	if len(sets) == 0 {
		counts["literal"]++
		add(file, whole.Pos(), whole.End(), call, 0)
		return
	}
	// v := &xdm.Node{...} as a statement: set the rest in statements after it.
	n := len(stack)
	if as, ok := stack[n-3].(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 && as.Rhs[0] == whole {
		// Not when a value names the variable: x = &xdm.Node{Parent: x}
		// reads the old x, x.SetParent(x) after the assignment the new one.
		if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name != "_" && !used[id.Name] {
			switch stack[n-4].(type) {
			case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
				counts["literal+stmts"]++
				add(file, whole.Pos(), whole.End(), call, 0)
				var b strings.Builder
				for _, s := range sets {
					b.WriteString("\n" + id.Name + "." + s)
				}
				add(file, as.End(), as.End(), b.String(), 1)
				return
			}
		}
	}
	tmp := ""
	for _, c := range []string{"n", "nd", "node", "lit", "nodeLit"} {
		if !used[c] {
			tmp = c
			break
		}
	}
	var b strings.Builder
	b.WriteString("func() *" + xdmName + ".Node {\n" + tmp + " := " + call + "\n")
	for _, s := range sets {
		b.WriteString(tmp + "." + s + "\n")
	}
	b.WriteString("return " + tmp + "\n}()")
	counts["literal+func"]++
	add(file, whole.Pos(), whole.End(), b.String(), 0)
}

// apply writes the non-overlapping edits of one file and returns how many it
// applied. An edit inside a range another edit replaces is left for the next
// pass, which sees the file as rewritten.
func apply(name string, es []edit) (int, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return 0, err
	}
	sort.SliceStable(es, func(i, j int) bool {
		a, c := es[i], es[j]
		if a.start != c.start {
			return a.start < c.start
		}
		az, cz := a.start == a.end, c.start == c.end
		if az != cz {
			return az // an insert goes before a replacement at its offset
		}
		if a.end != c.end {
			return a.end > c.end // the wider replacement wins
		}
		return a.prio < c.prio
	})
	var keep []edit
	lastEnd := 0
	for _, e := range es {
		if len(keep) > 0 {
			if keep[len(keep)-1] == e {
				continue // one edit found twice
			}
			if e.start < lastEnd {
				continue // inside a replacement: next pass
			}
		}
		keep = append(keep, e)
		if e.end > lastEnd {
			lastEnd = e.end
		}
	}
	var out strings.Builder
	last := 0
	for _, e := range keep {
		out.Write(b[last:e.start])
		out.WriteString(e.text)
		last = e.end
	}
	out.Write(b[last:])
	res, err := imports.Process(name, []byte(out.String()), &imports.Options{Comments: true, TabIndent: true, TabWidth: 8})
	if err != nil {
		return 0, fmt.Errorf("gofmt after rewrite: %v", err)
	}
	return len(keep), os.WriteFile(name, res, 0o644)
}
