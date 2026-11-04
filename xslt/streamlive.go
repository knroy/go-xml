package xslt

import "github.com/knroy/go-xml/v2/xdm"

// A built-in rule on the parse's own record (lane J4).
//
// An element whose rule choice needs nothing but its name -- the mode's
// rule index (ruleindex.go) holds no rule that may match it or any of its
// attributes -- gets the built-in rule, whatever a view would say. Its
// view is then made for nothing: the shallow-copy, shallow-skip,
// deep-skip and text-only-copy rules read the element's name, namespaces,
// attributes and base URI, all of which the parse's record has, complete,
// at the start tag (the cursor's StartElement event), and read them only
// there, before the children are read. So the dispatch runs on that
// record, live: nothing of it is kept, the attributes are matched (to no
// rule) and copied by the output builder as the leaves already are.

// liveBuiltIn dispatches mode to el, the parse's record of the element at
// depth md, the cursor at its start tag, when no rule of mode may match el
// or its attributes and the mode's built-in rule reads only what el has;
// ok false (nothing done) otherwise. read is element's.
func (run *streamRun) liveBuiltIn(focus focuser, pos int, el *xdm.Node, md int, mode string,
	params, tunnels paramValues, out *outputBuilder) (read, ok bool, err error) {

	s := focus.disp.sheet
	switch s.modeNoMatch[mode] {
	case "shallow-copy", "copy", "shallow-skip", "deep-skip", "text-only-copy", "":
	default:
		return false, false, nil // deep-copy grounds, fail stops: the view's way
	}
	if l, ok := s.candidates(el, mode); !ok || len(l) > 0 {
		return false, false, nil
	}
	for i := range el.NumAttrs() {
		if l, ok := s.candidates(el.AttrAt(i), mode); !ok || len(l) > 0 {
			return false, false, nil
		}
	}
	rt := focus.at(el, pos)
	if err := s.checkModeTyped(el, mode); err != nil {
		return false, true, err
	}
	s.warnNoMatch(rt, el, mode)
	d, grp := run.d, run.grp
	run.d, run.used, run.grp = md, false, nil
	err = run.builtIn(rt, el, mode, params, tunnels, out)
	read = run.used
	run.d, run.used, run.grp = d, true, grp
	return read, true, err
}
