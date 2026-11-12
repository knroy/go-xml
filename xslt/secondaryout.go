package xslt

// Secondary results written as they are made (TransformOptions.SecondaryOutput;
// pre-release lane H3).
//
// With SecondaryOutput set, each xsl:result-document is serialized to the
// writer the caller's function returns for its URI, and is not added to
// Result.Secondary. Where it can, the instruction streams: its body's builder
// has the principal result's sink (streamout.go) attached, writing to that
// writer, so the result is held only up to what is not yet written. Where a
// written byte could not be taken back, the result is built as tree mode
// builds it and written once it is final:
//
//   - inside a streamed xsl:source-document or xsl:merge (hold > 0), which a
//     failure re-runs on the tree after taking back the secondary results of
//     the failed run (effectsMark): such a result waits in rt.secondary,
//     where the take-back finds it, and is written when no run that may
//     re-run is left (flush). The principal input of TransformReader is never
//     re-run, so a result-document under it streams;
//   - inside the body of an xsl:try (inTry): a caught error would leave a
//     result that was being made half written, where the tree has none. A
//     result-document that completes inside a try is kept by the tree, caught
//     error or not, so it is written when it completes;
//   - beyond maxOpenSecondary writers open at once: a result-document nested
//     deeper than that in others being written is built and written when it
//     completes, so a host's writers (files, sockets) open at once stay few
//     however deep the stylesheet nests them;
//   - when the output needs the whole result (validation, build-tree="no",
//     an item-separator; and streamOut's own fallbacks, which it holds to
//     its end), or the URI is already produced, read, or being written
//     (XTDE1490 or XTDE1500 will follow, as in tree mode, after the body).

import (
	"fmt"
	"io"

	"github.com/knroy/go-xml/v2/internal/xdmsink"
)

// maxOpenSecondary bounds the SecondaryOutput writers a transform holds open
// at once. Only nesting opens more than one, and a result-document nested
// deeper is built as tree mode builds it and written once complete, which
// gives the same bytes (XSLT 3.0 section 25.1 places no limit on nesting).
const maxOpenSecondary = 16

// secondaryOutput is SecondaryOutput's state for one transform.
type secondaryOutput struct {
	open func(href string) (io.WriteCloser, error)
	// opened are the URIs whose writer is open now.
	opened map[string]bool
	// hold counts the streamed runs in progress that may re-run on the tree.
	hold int
}

// holdSecondary marks a streamed run that may re-run on the tree, until the
// func it returns is called.
func (rt *runtime) holdSecondary() func() {
	so := rt.secondaryOut
	if so == nil {
		return func() {}
	}
	so.hold++
	return func() { so.hold-- }
}

// inTryBody is rt for the body of an xsl:try.
func (rt *runtime) inTryBody() *runtime {
	if rt.secondaryOut == nil || rt.inTry {
		return rt
	}
	sub := *rt
	sub.inTry = true
	return &sub
}

// resultKey is the URI a secondary result is written to: its absolute URI,
// or the href as written when no base URI resolves it.
func resultKey(href, resolved string) string {
	if resolved != "" {
		return resolved
	}
	return href
}

// flush writes the secondary results built so far, and takes them out of
// rt.secondary, unless a run that may re-run is in progress.
func (so *secondaryOutput) flush(rt *runtime) error {
	if so.hold > 0 {
		return nil
	}
	for len(*rt.secondary) > 0 {
		sr := (*rt.secondary)[0]
		*rt.secondary = (*rt.secondary)[1:]
		key := resultKey(sr.Href, sr.BaseURI)
		if so.opened[key] {
			// The result-document writing this URI now encloses this one
			// and is in no xsl:try and no run that re-runs: it ends in
			// XTDE1490, and the transform with it.
			continue
		}
		w, err := so.openWriter(key)
		if err != nil {
			return err
		}
		err = sr.Serialize(w, nil)
		if err = closeWriter(w, key, err); err != nil {
			return err
		}
	}
	return nil
}

func (so *secondaryOutput) openWriter(key string) (io.WriteCloser, error) {
	w, err := so.open(key)
	if err != nil {
		return nil, fmt.Errorf("xsl:result-document: SecondaryOutput(%q): %w", key, err)
	}
	return w, nil
}

// closeWriter closes w, whose result ended with err; Close's error is the
// result's when err is nil.
func closeWriter(w io.WriteCloser, key string, err error) error {
	if cerr := w.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("xsl:result-document: closing the SecondaryOutput writer for %q: %w", key, cerr)
	}
	return err
}

// stream runs i to the writer SecondaryOutput returns, as it is made, when
// it can (done); otherwise nothing has been done and the instruction builds
// the result as tree mode does.
func (i *resultDocumentInstr) stream(rt *runtime, href, resolved string, settings OutputSettings) (done bool, err error) {
	so := rt.secondaryOut
	key := resultKey(href, resolved)
	if so.hold > 0 || rt.inTry || !i.validation.isDefault() || !settings.buildsTree() ||
		settings.ItemSeparator != nil || so.opened[key] || len(so.opened) >= maxOpenSecondary ||
		(*rt.resultHrefs)[href] ||
		resolved != "" && (rt.writtenDocs != nil && (*rt.writtenDocs)[resolved] ||
			rt.readDocs != nil && hasDoc(*rt.readDocs, resolved)) {
		return false, nil
	}
	cm, err := rt.sheet.flattenCharacterMaps(i.pkg, settings.UseCharacterMaps)
	if err != nil {
		return false, nil // raised after the body, as the tree does
	}
	if settings.InlineCharMap != nil {
		cm = settings.InlineCharMap
	}
	if err := so.flush(rt); err != nil {
		return true, err
	}
	w, err := so.openWriter(key)
	if err != nil {
		return true, err
	}
	so.opened[key] = true
	defer delete(so.opened, key)
	sub := newOutputBuilder(rt)
	out := newStreamOut(rt, w, settings, cm)
	xdmsink.Attach(sub, out)
	err = execSequence(i.body, rt.withOutputURI(resolved), sub)
	if err == nil {
		err = keptStreamNode(rt, sub.Sequence())
	}
	if err == nil {
		if href == "" {
			*rt.baseURIUsed = true
		}
		err = checkResultURI(rt, href, resolved)
	}
	if err == nil {
		err = xdmsink.Finish(sub, sub.Sequence())
	}
	if ferr := out.flush(); err == nil {
		err = ferr
	}
	if err = closeWriter(w, key, err); err != nil {
		return true, err
	}
	recordResultURI(rt, href, resolved)
	return true, nil
}
