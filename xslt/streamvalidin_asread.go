package xslt

import "github.com/knroy/go-xml/v2/internal/xsdstream"

// asRead is how a document the harness preloaded as validated (MarkSource)
// streams when it is read with the default validation. The tree reads the
// harness's typed tree as it is (load, mergeDoc's tree); the stream reads
// the file again and repeats the harness's validation, pre, and keeps the
// document URI, as that tree does. nil for a document not marked, which the
// resolver streams or not as before (wantsBursts); ok false when the stream
// cannot be exact:
//   - the stylesheet strips the annotations (strips): the tree reads a
//     stripped copy, whose properties are not the parse's;
//   - the prolog may hold a DTD, which the harness's parse read;
//   - the harness's tree is not cached under the URI the stream gives the
//     document (a source whose file is not its uri, or evicted: a re-read
//     on the tree would be untyped);
//   - the stream refuses the harness's schema.
//
// An invalid document fails the stream at its end, and the container re-runs
// on the tree, which reads the harness's tree as it does now.
func asRead(rt *runtime, br burstResolver, href, base string, strips bool) (*streamValidation, bool) {
	fr, ok := br.(*FileResolver)
	if !ok {
		return nil, true
	}
	pre := fr.preValidation(href, base)
	if pre == nil {
		return nil, true
	}
	if strips && rt.sheet.stripTypeAnnotations || !fr.cachedAs(href, base) ||
		fr.prologMayHaveDTD(href, base) || xsdstream.Refusal(pre) != "" {
		return nil, false
	}
	return &streamValidation{pre: pre, marked: true, asRead: true}, true
}

// cachedAs reports whether the tree cached for href is there and has the
// document URI a stream of the file gives it.
func (r *FileResolver) cachedAs(href, base string) bool {
	path, err := r.resolvePath(href, base)
	if err != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.cache[path]
	return t != nil && t.Root.DocumentURI() == fileURIOf(path)
}
