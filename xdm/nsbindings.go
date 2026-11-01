package xdm

// InScopeNamespaceBindings iterates the prefix-to-URI bindings in scope on
// an element, the same set InScopeNamespaces returns, without making a
// namespace node for any of them or a map to hold them. Other kinds have
// none.
//
// Each in-scope prefix is yielded once, with the URI the innermost
// declaration gives it, and the xml prefix is always among them. A prefix
// undeclared by xmlns:p="" (or a default undeclared by xmlns="") is not
// yielded. The order is unspecified; use NamespaceNodes for the namespace
// axis' prefix order.
//
// The cost of each binding is a scan of the ones yielded before it, so the
// iteration suits a scope of the usual size.
func (n *Node) InScopeNamespaceBindings() func(yield func(prefix, uri string) bool) {
	return func(yield func(prefix, uri string) bool) {
		if n.kind != uint8(KindElement) || n.flags&fSide != 0 {
			return
		}
		if !yield("xml", NSXML) {
			return
		}
		var buf [16]string
		seen := buf[:0] // prefixes already decided: yielded or undeclared
		for cur := n; cur != nil; cur = cur.Parent() {
			if cur.kind != uint8(KindElement) || cur.flags&fSide != 0 {
				continue
			}
			f := cur.frame()
			for i := len(f) - 1; i >= 0; i-- { // the last declaration wins
				b := f[i]
				if b.prefix == "xml" || containsString(seen, b.prefix) {
					continue
				}
				seen = append(seen, b.prefix)
				if b.uri != "" && !yield(b.prefix, b.uri) {
					return
				}
			}
		}
	}
}

func containsString(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
