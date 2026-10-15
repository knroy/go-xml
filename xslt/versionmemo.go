package xslt

import (
	"sync"

	"github.com/knroy/go-xml/v2/xdm"
)

// versionMemo remembers, for the duration of one Compile, the answers of
// the two ancestor walks the compiler makes for nearly every element and
// expression: the nearest ancestor-or-self stating a version (versionHolder,
// behind effectiveForwards, compatModeAt, xpathVersionAt and
// declaredXSLTVersion) and the module element (moduleElement, behind
// moduleAtLeast30). Each walk reads attributes through the name table at
// every level, and DocBook's compile repeated it for tens of thousands of
// nodes.
//
// The memo is off during the static phase. That phase builds the pruned
// copies use-when and shadow attributes produce while compilation is
// reading them, so an answer remembered for a node there can be wrong by
// the time it is asked again (shadow-003 and -004 read a stale version
// when the memo stayed on). It comes back empty when the phase ends.
// Outside a compilation m is nil and every call walks. It has its own mutex
// for the reason sharedNS does: a running transform, which does not hold
// compileMu, reaches these functions too.
var versionMemo struct {
	sync.Mutex
	on      bool
	holders map[*xdm.Node]versionEntry
	modules map[*xdm.Node]*xdm.Node
}

// versionEntry is one versionHolder answer.
type versionEntry struct {
	holder *xdm.Node
	v      float64
}

// setVersionMemo turns the memo on, empty, or off.
func setVersionMemo(on bool) {
	versionMemo.Lock()
	versionMemo.on = on
	versionMemo.holders, versionMemo.modules = nil, nil
	versionMemo.Unlock()
}

// suspendVersionMemo turns the memo off for the static phase and returns
// the function that turns it back on, empty, if it was on.
func suspendVersionMemo() (resume func()) {
	versionMemo.Lock()
	was := versionMemo.on
	versionMemo.Unlock()
	setVersionMemo(false)
	return func() {
		if was {
			setVersionMemo(true)
		}
	}
}

// forgetVersionMemo drops every remembered answer, for a caller that has
// just changed a stylesheet tree's parent links during a compilation.
func forgetVersionMemo() {
	versionMemo.Lock()
	versionMemo.holders, versionMemo.modules = nil, nil
	versionMemo.Unlock()
}

// versionHolder returns the nearest ancestor-or-self element of el carrying
// a version attribute (see hasVersionAttr) and the version it states, or nil
// and 2.0 when there is none.
func versionHolder(el *xdm.Node) (*xdm.Node, float64) {
	versionMemo.Lock()
	defer versionMemo.Unlock()
	m := versionMemo.holders
	if versionMemo.on && m == nil {
		m = map[*xdm.Node]versionEntry{}
		versionMemo.holders = m
	}
	res := versionEntry{v: 2.0}
	cur := el
	for ; cur != nil; cur = cur.Parent() {
		if e, ok := m[cur]; ok {
			res = e
			break
		}
		if cur.Kind() == xdm.KindElement && hasVersionAttr(cur) {
			res = versionEntry{cur, versionAt(cur)}
			break
		}
	}
	// Every node walked shares the answer: none of them states a version.
	if m != nil {
		for a := el; a != cur; a = a.Parent() {
			m[a] = res
		}
		if cur != nil {
			m[cur] = res
		}
	}
	return res.holder, res.v
}

// moduleElement returns the module element el belongs to: the nearest
// ancestor-or-self xsl:stylesheet, xsl:transform or xsl:package, or for a
// simplified stylesheet the outermost element. It is nil when el has no
// element ancestor-or-self.
func moduleElement(el *xdm.Node) *xdm.Node {
	versionMemo.Lock()
	defer versionMemo.Unlock()
	m := versionMemo.modules
	if versionMemo.on && m == nil {
		m = map[*xdm.Node]*xdm.Node{}
		versionMemo.modules = m
	}
	var res, outermost *xdm.Node
	found := false
	cur := el
	for ; cur != nil; cur = cur.Parent() {
		if r, ok := m[cur]; ok {
			res, found = r, true
			break
		}
		if cur.Kind() != xdm.KindElement {
			continue
		}
		outermost = cur
		if cur.Name().URI == xdm.NSXSL && isStylesheetRootName(cur.Name().Local) {
			res, found = cur, true
			break
		}
	}
	if !found {
		res = outermost
	}
	// Only elements are remembered: above the outermost element the answer
	// is nil rather than the outermost element.
	if m != nil && res != nil {
		for a := el; a != nil; a = a.Parent() {
			if a.Kind() == xdm.KindElement {
				m[a] = res
			}
			if a == res || a == cur {
				break
			}
		}
	}
	return res
}
