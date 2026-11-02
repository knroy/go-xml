package xslt

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	goruntime "runtime"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/internal/genid"
	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// The split's oracle is tree mode: the same expression, compiled as usual,
// evaluated on a full parse of the same document at the same context node.
// The results must be the same sequence -- nodes the same nodes of the
// document (by identity: a stream's copies carry their original's index),
// atomic values of the same type and value -- or the same error code; and
// the cursor must stand at the context's end.

// splitCase is one expression at one context of one document: k is 0 for
// the document node, else the k-th element in document order.
type splitCase struct {
	src, doc string
	k        int
	ns       xpath.NamespaceResolver
	v        xpath.Version
}

// splitResult is what checking a case found.
type splitResult struct {
	refused string // why splitStream refused
	giveUp  bool   // the fold asked for the tree
	guard   bool
	diff    string // the split disagrees with the tree
	empty   bool   // the tree's answer is ()
}

var splitCodeRe = regexp.MustCompile(`\b[A-Z]{4}[0-9]{4}\b`)

func splitCode(err error) string {
	if m := splitCodeRe.FindString(err.Error()); m != "" {
		return m
	}
	return err.Error() // a parse error: the cursor's must be xdm.Parse's own
}

func splitKey(n *xdm.Node) string {
	id := genid.Of(n)
	return id[strings.IndexByte(id, 'x'):]
}

// nthElement is the k-th element of t in document order, or the document
// node for k 0, and how many elements follow its subtree.
func nthElement(t *xdm.Tree, k int) (*xdm.Node, int) {
	if k == 0 {
		return t.Root, 0
	}
	var at *xdm.Node
	i, after := 0, 0
	for n := range t.Root.Descendants() {
		if n.Kind() != xdm.KindElement {
			continue
		}
		i++
		switch {
		case i == k:
			at = n
		case at != nil && n.Compare(at) > 0 && !isAncestorOf(at, n):
			after++
		}
	}
	return at, after
}

func isAncestorOf(a, n *xdm.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p == a {
			return true
		}
	}
	return false
}

var splitDeepEqual = xpath.MustCompile("deep-equal($a, $b)", nil)

// splitSame compares got, the split's result, with want, the tree's on t.
func splitSame(got, want xdm.Sequence, t *xdm.Tree) string {
	if len(got) != len(want) {
		return fmt.Sprintf("%d items, tree %d", len(got), len(want))
	}
	for i := range want {
		switch w := want[i].(type) {
		case *xdm.Node:
			g, ok := got[i].(*xdm.Node)
			if !ok {
				return fmt.Sprintf("item %d: %T, tree a node", i, got[i])
			}
			if w.Tree() == t {
				if g.Tree() == t || splitKey(g) != splitKey(w) || g.Kind() != w.Kind() || g.Name() != w.Name() {
					return fmt.Sprintf("item %d: node %v %s, tree %v %s", i, g.Name(), splitKey(g), w.Name(), splitKey(w))
				}
				continue
			}
			// A copy (copy-of): a new node in both modes.
			r, err := splitDeepEqual.Eval(xpath.NewContext(nil, xpath.Builtins()).
				WithVar(xdm.QName{Local: "a"}, xdm.One(g)).WithVar(xdm.QName{Local: "b"}, xdm.One(w)))
			if err != nil || len(r) != 1 || r[0].(*xdm.Atomic).String() != "true" {
				return fmt.Sprintf("item %d: copy differs", i)
			}
		case *xdm.Atomic:
			g, ok := got[i].(*xdm.Atomic)
			if !ok || g.TypeName() != w.TypeName() || g.String() != w.String() {
				return fmt.Sprintf("item %d: %v, tree %v (%s)", i, got[i], w, w.TypeName())
			}
		default:
			return fmt.Sprintf("item %d: %T in the tree's answer", i, w)
		}
	}
	return ""
}

// check runs c both ways.
func (c splitCase) check() (res splitResult) {
	base, err := xpath.CompileVersion(c.src, c.ns, c.v)
	if err != nil {
		res.refused = "does not compile standalone"
		return
	}
	e, err := xpath.ParseVersion(c.src, c.ns, c.v)
	if err != nil {
		res.refused = "does not parse standalone"
		return
	}
	s, why := splitStream(e, base, c.k == 0)
	if s == nil {
		res.refused = why
		return
	}
	tree, terr := xdm.Parse(strings.NewReader(c.doc), xdm.ParseOptions{})
	var want xdm.Sequence
	after := 0
	if terr == nil {
		var at *xdm.Node
		at, after = nthElement(tree, c.k)
		if at == nil {
			res.refused = "no such element"
			return
		}
		want, terr = base.Eval(xpath.NewContext(at, xpath.Builtins()))
		if terr != nil && strings.Contains(terr.Error(), "XPST0017") {
			// copy-of(), snapshot(), current(): the XSLT library's.
			res.refused = "needs the XSLT function library"
			return
		}
	}
	got, gerr, rest := c.stream(s)
	switch {
	case errors.Is(gerr, errSplitGiveUp):
		res.giveUp = true
	case errors.As(gerr, new(*streamtrace.PostureError)):
		res.guard = true
		res.diff = "posture guard: " + gerr.Error()
	case (gerr == nil) != (terr == nil):
		res.diff = fmt.Sprintf("error %v, tree %v", gerr, terr)
	case gerr != nil:
		if splitCode(gerr) != splitCode(terr) {
			res.diff = fmt.Sprintf("error %v, tree %v", gerr, terr)
		}
	default:
		res.diff = splitSame(got, want, tree)
		res.empty = len(want) == 0
		if res.diff == "" && rest != after {
			res.diff = fmt.Sprintf("%d elements left after the context, tree %d", rest, after)
		}
	}
	return
}

// stream evaluates s on the cursor at the case's context, and counts the
// elements the cursor still has after it.
func (c splitCase) stream(s *streamSplit) (seq xdm.Sequence, err error, rest int) {
	cur := xdmstream.Open(strings.NewReader(c.doc), xdm.ParseOptions{})
	defer cur.Close()
	d := 0
	for i := 0; i < c.k; {
		ev, err := cur.Next()
		if err != nil {
			return nil, err, 0
		}
		if ev.Kind == xdmstream.StartElement {
			i++
			d = ev.Depth
		}
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				pe, ok := p.(*streamtrace.PostureError)
				if !ok {
					panic(p)
				}
				err = pe
			}
		}()
		seq, err = s.eval(xpath.NewContext(nil, xpath.Builtins()), cur, d)
	}()
	if err != nil {
		return nil, err, 0
	}
	// The rest of the document is the caller's to read, and its error too.
	for {
		ev, err := cur.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err, 0
		}
		if ev.Kind == xdmstream.StartElement {
			rest++
		}
	}
	return seq, nil, rest
}

// splitResolver binds the prefixes the hand-written expressions use.
func splitResolver() xpath.NamespaceResolver {
	t, err := xdm.Parse(strings.NewReader(`<x xmlns:p="urn:p" xmlns:q="urn:q" xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`), xdm.ParseOptions{})
	if err != nil {
		panic(err)
	}
	return newNSResolver(t.Root.FirstChild(), "")
}

// --- Documents --------------------------------------------------------------

// splitDocs are the fixed shapes: shallow records, a 900-deep spine with
// records (self-nested) along it, wide, mixed content, namespaces and
// attributes.
func splitDocs() map[string]string {
	var b strings.Builder
	docs := map[string]string{}

	b.WriteString(`<?xml version="1.0"?><!--c0--><?pi0 x?><doc k="a" v="1">`)
	for i := range 40 {
		fmt.Fprintf(&b, `<rec k="%s" v="%s" id="r%d">%d<item v="%d"/><item v="%s">x</item></rec>`,
			fuzzKeys[i%len(fuzzKeys)], []string{"1", "2", "3.5", "-1", "10"}[i%5], i, i%7, i%3, []string{"1", "2", "4"}[i%3])
	}
	b.WriteString(`</doc><!--c1-->`)
	docs["shallow"], b = b.String(), strings.Builder{}

	b.WriteString(`<doc>`)
	for i := range 900 {
		fmt.Fprintf(&b, `<w d="%d">`, i)
		if i%150 == 3 {
			fmt.Fprintf(&b, `<rec k="a" v="%d">%d<rec k="b" v="2">5<item v="1"/></rec><item v="3"/></rec>`, i, i%10)
		}
	}
	b.WriteString(`<rec k="z" v="7"><a><b><c/></b><b/></a></rec>`)
	b.WriteString(strings.Repeat("</w>", 900) + `<rec k="end" v="1"/></doc>`)
	docs["deep"], b = b.String(), strings.Builder{}

	b.WriteString(`<doc>`)
	for i := range 3000 {
		fmt.Fprintf(&b, `<rec k="%s" v="%d"><item v="%d"/></rec>`, fuzzKeys[i%len(fuzzKeys)], i%11, i%4)
	}
	b.WriteString(`</doc>`)
	docs["wide"], b = b.String(), strings.Builder{}

	docs["mixed"] = `<doc k="m">lead<!--c--><rec k="a" v="2">one <b>two</b> three<?p q?><![CDATA[<x>]]></rec>` +
		`mid<rec k="b" v="x">4<item v="1">5</item><item/></rec><a>t<b>u<c>v</c></b><b/></a> tail</doc>`
	docs["ns"] = `<doc xmlns:p="urn:p" xmlns:q="urn:q"><p:r k="a" v="1"><q:s v="2"/></p:r><rec p:k="x" v="3">` +
		`<p:r v="4"><p:r k="n" v="5"/></p:r></rec><d xmlns="urn:d"><rec v="6"/></d><q:rec v="7"/></doc>`
	docs["attrs"] = `<doc id="d0" k="a"><rec id="1" k="a" v="1" w="2" x="3" y="4" z="5"/><rec id="2" k="b" v="NaN"/>` +
		`<rec id="3" k="" v=" 4 "/><rec id="4" k="a" v="INF"/><rec id="5" v="1e3"/></doc>`
	return docs
}

// splitGenDoc makes a random document from element names els and attribute
// names atts (QNames; a URI gets a prefix declared on the root).
func splitGenDoc(r *rand.Rand, els, atts []xdm.QName) string {
	pre := map[string]string{}
	q := func(n xdm.QName) string {
		if n.URI == "" {
			return n.Local
		}
		if _, ok := pre[n.URI]; !ok {
			pre[n.URI] = "n" + strconv.Itoa(len(pre))
		}
		return pre[n.URI] + ":" + n.Local
	}
	var body strings.Builder
	var gen func(depth int)
	gen = func(depth int) {
		for range r.Intn(4) + 1 {
			switch x := r.Intn(10); {
			case x < 6 && depth < 7:
				n := q(els[r.Intn(len(els))])
				body.WriteString("<" + n)
				for _, a := range atts {
					if r.Intn(3) > 0 {
						body.WriteString(" " + q(a) + `="` + strings.ReplaceAll(fuzzNums[r.Intn(len(fuzzNums))], `"`, "") + `"`)
					}
				}
				body.WriteString(">")
				gen(depth + 1)
				body.WriteString("</" + n + ">")
			case x < 8:
				body.WriteString(fuzzNums[r.Intn(len(fuzzNums))])
			case x < 9:
				body.WriteString("<!--c-->")
			default:
				body.WriteString("<?pi d?>")
			}
		}
	}
	gen(1)
	root := q(els[r.Intn(len(els))])
	var b strings.Builder
	b.WriteString("<" + root)
	uris := make([]string, 0, len(pre))
	for u := range pre {
		uris = append(uris, u)
	}
	sort.Strings(uris)
	for _, u := range uris {
		fmt.Fprintf(&b, ` xmlns:%s="%s"`, pre[u], u)
	}
	b.WriteString(">" + body.String() + "</" + root + ">")
	return b.String()
}

// splitNames collects the element and attribute names e tests for.
func splitNames(e xpath.Expr) (els, atts []xdm.QName) {
	seen := map[xdm.QName]bool{}
	var visit func(v reflect.Value, attr bool)
	visit = func(v reflect.Value, attr bool) {
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if st, ok := v.Interface().(*xpath.Step); ok {
				attr = st.Axis == xpath.AxisAttribute
			}
			if nt, ok := v.Interface().(*xpath.NameTest); ok && !nt.AnyLocal {
				n := nt.Name
				if !seen[n] {
					seen[n] = true
					if attr {
						atts = append(atts, n)
					} else {
						els = append(els, n)
					}
				}
				return
			}
			visit(v.Elem(), attr)
		case reflect.Slice:
			for i := range v.Len() {
				visit(v.Index(i), attr)
			}
		case reflect.Struct:
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					visit(v.Field(i), attr)
				}
			}
		}
	}
	visit(reflect.ValueOf(e), false)
	return els, atts
}

// --- The hand-written expressions --------------------------------------------

// splitExprs covers every construct the split admits, at the document node
// (and, where the expression does not start at the root, at the document
// element and at other elements too).
var splitExprs = []string{
	// P: child, descendant, //, wildcards, kinds, namespaces
	`count(//rec)`, `count(/doc/rec)`, `count(doc/rec)`, `count(*/rec)`, `count(//*)`, `count(//node())`,
	`count(//text())`, `count(//comment())`, `count(//processing-instruction())`, `count(descendant::item)`,
	`count(//p:r)`, `count(//*:r)`, `count(//q:*)`, `count(//p:r//p:r)`, `count(//a//b)`, `count(.//rec)`,
	`count(rec)`, `count(*)`, `count(node())`, `count(descendant::*/item)`, `count(//element())`,
	`count(//element(rec))`, `count(//rec/item/node())`,
	// motionless predicates
	`count(//rec[@k = 'a'])`, `count(//rec[@v > 2])`, `count(//rec[not(@k)])`, `count(//rec[@k][@v])`,
	`count(//rec[../@k = 'a'])`, `count(//rec[name(..) = 'doc'])`, `count(//rec[ancestor::w/@d = '5'])`,
	`count(//rec[@k = ../@k])`, `count(//rec[self::rec])`, `count(//*[local-name() = 'r'])`,
	`count(//rec[has-children(.)])`, `count(//rec[not(has-children())])`,
	`count(//text()[string-length() > 1])`,
	`count(//rec[count(ancestor::*) > 2])`, `count(//rec[base-uri(.) = ''])`,
	// positional predicates: per parent, per context, after a filter
	`count(//rec[1])`, `count(//rec[2])`, `count(//item[2])`, `count(//rec[@k][1])`, `count(//rec[1][@k])`,
	`count(descendant::rec[2])`, `count(//rec/descendant::item[1])`, `count(//rec/descendant::rec[1])`, `count(//a/descendant::b[2])`, `count(descendant::rec[1]/item)`, `count(//a/b[2]//c)`, `count(//a//b[1])`,
	`count(*[3])`, `count(//node()[2])`, `count(//rec[0])`, `count(//rec[99999999999999999999])`,
	`//rec[@v > 1][2]/@k`, `//rec[2]/@v`, `descendant::rec[3]/@k`,
	// outermost, innermost
	`count(outermost(//rec))`, `count(innermost(//rec))`, `outermost(//rec)/@v`, `innermost(//rec)/@k`,
	`sum(innermost(//rec)/@v)`, `outermost(//rec)`, `outermost(//rec[1])`, `count(innermost(descendant::rec[2]))`, `innermost(//rec)`, `count(outermost(//*))`,
	// reducers
	`sum(//rec/@v)`, `sum(doc/rec/@v)`, `sum(//item/@v, 0)`, `sum(//rec)`, `sum(//item)`, `sum(rec/@v)`,
	`avg(//rec/@v)`, `avg(//item)`, `avg(//rec[@k='zz']/@v)`, `min(//rec/@v)`, `max(//rec/@k)`, `max(//item)`,
	`min(//item/@v)`, `max(//rec/@v)`, `exists(//rec[@k='b'])`, `empty(//rec[@k='zz'])`, `boolean((true(), //rec[@k = 'zz']))`, `boolean((true(), //rec))`, `not((1, //rec/@v))`, `if ((false(), //rec)) then 1 else 2`, `boolean(//rec)`,
	`not(//rec[@k='a'])`, `boolean(//rec/@v)`, `not(//rec/string())`, `if (//rec) then 1 else 0`,
	`head(//rec)`, `(//rec)[3]`, `(//item)[2]`, `string((//rec)[2])`, `(//rec/@v)[2]`,
	`count(//rec/@v)`, `count(//@*)`, `count(.//@id)`, `.//@id`, `count(//rec/(@k, @v))`, `exists(//comment())`,
	`sum(//rec/@v) + 1`, `count(//rec) * 2 = 10`, `-sum(//item/@v)`,
	// collected: nodes (grounded), values, tails
	`//rec`, `//rec/@v`, `//rec/@*`, `doc/rec/item`, `//rec[@k='a']/item`, `//rec/string()`, `//rec/data()`,
	`//rec/name()`, `//rec/(@k, @v)`, `//rec/count(item)`, `//rec/sum(item/@v)`,
	`string-join(//rec/@k, ',')`, `string-join(//text(), '|')`, `string-join(//item, ',')`,
	`//rec ! string(@k)`, `for $r in //rec return string($r/@v)`, `some $r in //rec satisfies $r/@k = 'a'`,
	`every $r in //item satisfies $r/@v`, `data(//item/@v)`, `//text()/string()`, `//comment()/string()`,
	`//rec/@v/string()`, `//*/node-name()`, `tokenize(string(//rec[1]), ' ')`,
	`//rec[2]`, `//a/b`, `//p:r/@v`, `//rec/item[1]/@v`, `rec`, `*`, `.//*`, `.`,
	`(//rec/@v)[. > 2]`, `//rec[@v][@k]/item[@v = 1]`,
	// the context itself, the outer on the context's view, choice
	`string(.)`, `data(.)`, `string-length()`, `normalize-space()`, `number()`,
	`concat(name(), ':', count(rec))`, `count(rec) + count(@*)`, `if (@k = 'a') then sum(rec/@v) else count(rec)`,
	`if (@k) then string(.) else ()`, `if (has-children()) then count(*) else -1`,
	`if (@k = 'a') then (if (@v) then count(.//item) else 0) else sum(*/@v)`, `@k`, `name()`, `count(@*)`,
	`concat(@k, '=', sum(.//item/@v))`, `if (@nope) then 1 else 2`,
	// errors
	`sum(//rec/@k)`, `avg(//rec/@k)`, `//rec/(1 div 0)`, `count(//rec[xs:integer(@v) > 0])`,
	`exactly-one(//rec)`, `zero-or-one(//item)`,
}

// splitRefused are expressions the split must refuse, and why.
var splitRefused = map[string]string{
	`//rec[last()]`:                                     "predicate",
	`//rec[position() = 2]`:                             "predicate",
	`//rec[item]`:                                       "predicate",
	`//rec[@v][count(item) > 1]`:                        "predicate",
	`count(//rec[@v castable as xs:double])`:            "predicate",
	`//rec[$n]`:                                         "predicate",
	`//rec/following-sibling::*`:                        "tail",
	`count(//rec) + count(//item)`:                      "second",
	`//text()`:                                          "identity",
	`//rec/text()`:                                      "identity",
	`generate-id(//rec)`:                                "function",
	`count(//rec[generate-id() = 'x'])`:                 "predicate",
	`root(.)//rec`:                                      "path",
	`outermost(descendant::rec[1])`:                     "positional",
	`count(//rec[has-children()]/item) + 0`:             "",
	`//rec[has-children()]/string()`:                    "has-children",
	`string(.) = 'x' and count(rec) > 1`:                "right",
	`true() and count(rec) > 1`:                         "right",
	`concat('a', if (@k) then sum(rec/@v) else 0)`:      "branch",
	`//rec/(item | @k)`:                                 "",
	`count(//node()[self::text()][normalize-space(.)])`: "predicate",
	`head(//text())`:                                    "identity",
	`distinct-values(//rec/@k)`:                         "function",
	`//item/..`:                                         "tail",
	`sum(//rec/@v) div count(*)`:                        "second",
	`node()`:                                            "identity",
}

func TestStreamSplitMatchesTree(t *testing.T) {
	ns := splitResolver()
	docs := splitDocs()
	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)
	var n struct{ cases, accepted, refused, nonEmpty, giveUp int }
	for _, src := range splitExprs {
		// Where the burst split takes an expression, the split must too.
		if e, err := xpath.ParseVersion(src, ns, xpath.XPath31); err == nil && splitSelect(e, false) != nil {
			if c := (splitCase{src: src, doc: docs["shallow"], ns: ns, v: xpath.XPath31}); c.check().refused != "" {
				t.Errorf("%s: the burst split takes it, the split refuses it: %s", src, c.check().refused)
			}
		}
		accepted := false
		for _, dn := range names {
			doc := docs[dn]
			for _, k := range []int{0, 1, 2, 5} {
				c := splitCase{src: src, doc: doc, k: k, ns: ns, v: xpath.XPath31}
				r := c.check()
				n.cases++
				switch {
				case r.refused != "":
					n.refused++
					continue
				case r.giveUp:
					n.giveUp++
				}
				accepted = true
				n.accepted++
				if !r.empty {
					n.nonEmpty++
				}
				if r.diff != "" {
					t.Errorf("%s on %s at element %d: %s", src, dn, k, r.diff)
				}
			}
		}
		if !accepted {
			c := splitCase{src: src, doc: docs["shallow"], k: 1, ns: ns, v: xpath.XPath31}
			why1 := c.check().refused
			c.k = 0
			t.Errorf("%s: refused everywhere: %s / at the document: %s", src, why1, c.check().refused)
		}
	}
	t.Logf("split: %d cases, %d accepted (%d with a non-empty answer, %d gave up), %d refused",
		n.cases, n.accepted, n.nonEmpty, n.giveUp, n.refused)
	for src, why := range splitRefused {
		var reasons []string
		for _, k := range []int{0, 1} {
			r := splitCase{src: src, doc: docs["shallow"], k: k, ns: ns, v: xpath.XPath31}.check()
			reasons = append(reasons, r.refused)
			switch {
			case why == "" && r.refused == "" && r.diff != "":
				t.Errorf("%s at %d: %s", src, k, r.diff)
			case why != "" && r.refused == "":
				t.Errorf("%s at %d: accepted, want refused (%s)", src, k, why)
			}
		}
		if why != "" && !strings.Contains(strings.Join(reasons, "|"), why) {
			t.Errorf("%s: refused for %q, want a reason with %q", src, reasons, why)
		}
	}
}

// TestStreamSplitMalformed: a parse error after the matches is the parse's,
// whatever the split has decided by then.
func TestStreamSplitMalformed(t *testing.T) {
	ns := splitResolver()
	for _, doc := range []string{
		`<doc><rec v="1"/><rec v="2"/><bad></doc>`,
		`<doc><rec v="1"/><rec v="2"/>&undefined;</doc>`,
		`<doc><rec v="1"><rec v="2"></rec></doc>`,
		`<doc><rec v="1"/></doc><trailing/>`,
	} {
		for _, src := range []string{`count(.//rec)`, `exists(.//rec)`, `head(.//rec)`, `sum(.//rec/@v)`, `.//rec/@v`, `.//rec`} {
			for _, k := range []int{0, 1} {
				r := splitCase{src: src, doc: doc, k: k, ns: ns, v: xpath.XPath31}.check()
				if r.refused != "" || r.diff != "" {
					t.Errorf("%s on %q at %d: refused %q, %s", src, doc, k, r.refused, r.diff)
				}
			}
		}
	}
}

// --- The random differential -------------------------------------------------

type splitGen struct{ r *rand.Rand }

func (g splitGen) pick(xs ...string) string { return xs[g.r.Intn(len(xs))] }

func (g splitGen) pred() string {
	switch g.r.Intn(12) {
	case 0:
		return "[" + strconv.Itoa(g.r.Intn(3)+1) + "]"
	case 1:
		return "[@k = '" + g.pick(fuzzKeys...) + "']"
	case 2:
		return "[@v > " + g.pick("0", "1", "2.5") + "]"
	case 3:
		return "[not(@k)]"
	case 4:
		return "[has-children()]"
	case 5:
		return "[name(..) = '" + g.pick("doc", "rec", "a") + "']"
	case 6:
		return "[@v][" + strconv.Itoa(g.r.Intn(2)+1) + "]"
	case 7:
		return "[ancestor::rec]"
	}
	return ""
}

func (g splitGen) path() string {
	var b strings.Builder
	if g.r.Intn(4) == 0 {
		b.WriteString(g.pick("/", "//", ".//"))
	} else if g.r.Intn(3) == 0 {
		b.WriteString("descendant::")
	}
	for i := range g.r.Intn(3) + 1 {
		if i > 0 {
			b.WriteString(g.pick("/", "/", "//", "/descendant::"))
		}
		b.WriteString(g.pick("rec", "rec", "item", "a", "b", "*", "p:r", "node()", "text()", "w"))
		b.WriteString(g.pred())
	}
	return b.String()
}

func (g splitGen) expr() string {
	p := g.path()
	if g.r.Intn(6) == 0 {
		p = g.pick("outermost", "innermost") + "(" + p + ")"
	}
	p += g.pick("", "", "", "/@v", "/@*", "/name()", "/string()", "/count(item)", "/@k/string()", "/(@k, name())")
	switch g.r.Intn(16) {
	case 0, 1:
		return "count(" + p + ")"
	case 2:
		return "sum(" + p + ")"
	case 3:
		return g.pick("min", "max", "avg") + "(" + p + ")"
	case 4:
		return g.pick("exists", "empty", "boolean", "not") + "(" + p + ")"
	case 5:
		return "head(" + p + ")"
	case 6:
		return "(" + p + ")[" + strconv.Itoa(g.r.Intn(4)+1) + "]"
	case 7:
		return "string-join(" + p + ", ',')"
	case 8:
		return "if (@k = 'a') then count(" + p + ") else sum(" + g.path() + "/@v)"
	case 9:
		return "data(" + p + ")"
	}
	return p
}

// TestStreamSplitFuzz is the random differential: generated documents and
// expressions, at random contexts.
//
//	GOXSLT_SPLIT_FUZZ=50000 GOXSLT_SPLIT_FUZZ_SEED=1 go test ./xslt -run 'TestStreamSplitFuzz$' -v
func TestStreamSplitFuzz(t *testing.T) {
	count, seed := 2000, int64(1)
	if v, err := strconv.Atoi(os.Getenv("GOXSLT_SPLIT_FUZZ")); err == nil {
		count = v
	}
	if v, err := strconv.ParseInt(os.Getenv("GOXSLT_SPLIT_FUZZ_SEED"), 10, 64); err == nil {
		seed = v
	}
	if testing.Short() {
		count = min(count, 300)
	}
	ns := splitResolver()
	els := []xdm.QName{{Local: "rec"}, {Local: "item"}, {Local: "a"}, {Local: "b"}, {Local: "w"}, {URI: "urn:p", Local: "r"}}
	atts := []xdm.QName{{Local: "k"}, {Local: "v"}}
	var n struct{ accepted, nonEmpty, refused, giveUp int }
	refusals := map[string]int{}
	for i := range count {
		s := seed + int64(i)
		r := rand.New(rand.NewSource(s))
		g := splitGen{r}
		c := splitCase{src: g.expr(), doc: splitGenDoc(r, els, atts), k: r.Intn(4), ns: ns, v: xpath.XPath31}
		res := c.check()
		switch {
		case res.refused != "":
			n.refused++
			refusals[splitReason(res.refused)]++
			continue
		case res.giveUp:
			n.giveUp++
		case !res.empty:
			n.nonEmpty++
		}
		n.accepted++
		if res.diff != "" {
			t.Errorf("seed %d: %s at element %d: %s\n%s", s, c.src, c.k, res.diff, c.doc)
		}
	}
	t.Logf("split fuzz: %d cases from seed %d: %d accepted (%d non-empty, %d gave up), %d refused %v",
		count, seed, n.accepted, n.nonEmpty, n.giveUp, n.refused, refusals)
}

// splitReason is a refusal without the expression text it names.
func splitReason(why string) string {
	for _, p := range []string{"predicate ", "tail ", "filter ", "simple map ", "quantified test ",
		"for return ", "path ", "expression ", "operator ", "function "} {
		if strings.HasPrefix(why, p) {
			if i := strings.Index(why, ": "); i > 0 && p != "function " && p != "operator " {
				return p + "..." + why[i:]
			}
			if p == "function " || p == "operator " {
				return why
			}
			return strings.TrimSpace(p)
		}
	}
	return why
}

// --- The suite's consuming expressions ---------------------------------------

// splitSuiteSite is one consuming XPath site of a trusted container.
type splitSuiteSite struct {
	el  *xdm.Node
	src string
	doc bool
}

// splitSuiteSites collects, from every streamable container of the W3C
// XSLT 3.0 suite the analysis trusts, the XPath sites it records as
// consuming; distinct by source text and context kind.
func splitSuiteSites(t *testing.T) []splitSuiteSite {
	root := filepath.Join(corpusBase(t), "xslt30-test", "tests")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no xslt30-test under testdata")
	}
	seen := map[string]bool{}
	var out []splitSuiteSite
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".xsl") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(data), `streamable="yes"`) {
			return nil
		}
		tree, err := xdm.Parse(strings.NewReader(string(data)), xdm.ParseOptions{})
		if err != nil {
			return nil
		}
		for _, sd := range streamableContainers(tree.Root) {
			pl := buildStreamPlan(sd, false)
			if !pl.trusted() {
				continue
			}
			for s := range pl.sites {
				r := pl.rec(pl.root(s))
				if r == nil || r.props.sweep != sweepConsuming || !r.known || r.unverified {
					continue
				}
				doc := splitSiteAtDocument(s.el, sd)
				key := fmt.Sprintf("%v|%s", doc, strings.Join(strings.Fields(s.src), " "))
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, splitSuiteSite{s.el, s.src, doc})
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].src < out[j].src || out[i].src == out[j].src && out[i].doc })
	return out
}

// splitSiteAtDocument reports whether an XPath site on el has the
// container's document node for its context: no instruction between them
// sets another focus.
func splitSiteAtDocument(el, sd *xdm.Node) bool {
	for n := el.Parent(); n != nil && n != sd; n = n.Parent() {
		for _, f := range []string{"for-each", "iterate", "for-each-group", "template", "function",
			"merge", "accumulator", "attribute-set", "key"} {
			if isXSL(n, f) {
				return false
			}
		}
	}
	return true
}

// TestStreamSplitSuite reports which of the suite's consuming expressions
// the split accepts, why it refuses the rest, and checks every accepted one
// that needs no variable against the tree on documents made from its names.
// It also checks the split accepts what the burst split (splitSelect)
// accepts at the document node.
func TestStreamSplitSuite(t *testing.T) {
	sites := splitSuiteSites(t)
	var n struct{ sites, accepted, checked, nonEmpty, giveUp, burst, burstRefused int }
	refusals := map[string]int{}
	examples := map[string]string{}
	for _, st := range sites {
		n.sites++
		ns := newNSResolver(st.el, xpathDefaultNamespace(st.el))
		v := xpathVersionAt(st.el)
		k := 0
		if !st.doc {
			k = 1
		}
		c := splitCase{src: st.src, k: k, ns: ns, v: v}
		base, err := xpath.CompileVersion(st.src, ns, v)
		if err != nil {
			refusals["does not compile standalone"]++
			continue
		}
		e, _ := xpath.ParseVersion(st.src, ns, v)
		if st.doc {
			if b := splitSelect(parseAt(st.el, st.src), false); b != nil {
				n.burst++
				e2, _ := xpath.ParseVersion(st.src, ns, v)
				if s, why := splitStream(e2, base, true); s == nil {
					n.burstRefused++
					t.Errorf("burst split accepts %q, the split refuses it: %s", st.src, why)
				}
			}
		}
		if s, why := splitStream(e, base, st.doc); s == nil {
			reason := splitReason(why)
			refusals[reason]++
			if examples[reason] == "" {
				examples[reason] = st.src
			}
			continue
		}
		n.accepted++
		if len(base.FreeVariables()) > 0 || strings.Contains(st.src, "current()") {
			continue
		}
		els, atts := splitNames(e)
		if len(els) == 0 {
			els = []xdm.QName{{Local: "a"}}
		}
		r := rand.New(rand.NewSource(int64(len(st.src))))
		for i := range 4 {
			c.doc = splitGenDoc(r, els, atts)
			if i == 3 {
				c.doc = splitDocs()["shallow"]
			}
			res := c.check()
			n.checked++
			if res.giveUp {
				n.giveUp++
			}
			if !res.empty {
				n.nonEmpty++
			}
			if res.diff != "" {
				t.Errorf("suite %q at %d: %s\n%s", st.src, c.k, res.diff, c.doc)
			}
		}
	}
	keys := make([]string, 0, len(refusals))
	for k := range refusals {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return refusals[keys[i]] > refusals[keys[j]] })
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "\n  %5d  %s   e.g. %q", refusals[k], k, examples[k])
	}
	t.Logf("split suite: %d distinct consuming expressions, %d accepted, %d refused; %d checks (%d non-empty, %d gave up); "+
		"burst split accepts %d, of which the split refuses %d; refusals:%s",
		n.sites, n.accepted, n.sites-n.accepted, n.checked, n.nonEmpty, n.giveUp, n.burst, n.burstRefused, b.String())
}

// TestStreamSplitMemory: a split over a large document holds what its
// strategy class says, not the document: folds and counts a few MB however
// many records there are, where the tree holds every one.
func TestStreamSplitMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("memory test")
	}
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 150000 {
		fmt.Fprintf(&b, `<rec k="%c" v="%d">%d<item v="%d"/><item v="1">x</item></rec>`, 'a'+i%5, i%100, i%10, i%7)
	}
	b.WriteString("</doc>")
	doc := b.String()
	ns := splitResolver()
	for _, src := range []string{`count(//rec)`, `sum(//rec/@v)`, `count(//rec/item[@v])`, `sum(//rec/count(item))`,
		`count(/doc/rec/item[2]/string())`, `exists(//rec[@v = 99])`} {
		base, err := xpath.CompileVersion(src, ns, xpath.XPath31)
		if err != nil {
			t.Fatal(err)
		}
		e, _ := xpath.ParseVersion(src, ns, xpath.XPath31)
		s, why := splitStream(e, base, true)
		if s == nil {
			t.Fatalf("%s: refused: %s", src, why)
		}
		peak := splitPeak(func() {
			c := xdmstream.Open(strings.NewReader(doc), xdm.ParseOptions{})
			defer c.Close()
			if _, err := s.eval(xpath.NewContext(nil, xpath.Builtins()), c, 0); err != nil {
				t.Fatal(err)
			}
		})
		tree := splitPeak(func() {
			tr, err := xdm.Parse(strings.NewReader(doc), xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := base.Eval(xpath.NewContext(tr.Root, xpath.Builtins())); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("%s: peak %.1f MB, tree %.1f MB", src, float64(peak)/1e6, float64(tree)/1e6)
		if peak > 24<<20 || peak > tree/3 {
			t.Errorf("%s: peak live heap %.1f MB (tree %.1f MB)", src, float64(peak)/1e6, float64(tree)/1e6)
		}
	}
}

// splitPeak is the most heap fn's objects took above what was live before.
func splitPeak(fn func()) uint64 {
	goruntime.GC()
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	base, top := s[0].Value.Uint64(), uint64(0)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			metrics.Read(s)
			top = max(top, s[0].Value.Uint64())
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Microsecond):
			}
		}
	}()
	fn()
	close(stop)
	<-done
	if top < base {
		return 0
	}
	return top - base
}
