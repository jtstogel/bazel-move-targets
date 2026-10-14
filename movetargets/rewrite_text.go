package movetargets

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/bazelbuild/buildtools/build"
	"github.com/bazelbuild/buildtools/labels"
)

// extraFiles are files outside the workspace whose labels into the workspace
// are spelled with a package prefix.
type extraFiles struct {
	paths  []string
	prefix string
}

type edit struct {
	start, end int
	text       string
}

func applyEdits(data []byte, edits []edit) []byte {
	slices.SortFunc(edits, func(a, b edit) int { return cmp.Compare(a.start, b.start) })
	var out []byte
	prev := 0
	for _, e := range edits {
		out = append(out, data[prev:e.start]...)
		out = append(out, e.text...)
		prev = e.end
	}
	return append(out, data[prev:]...)
}

// literalRewriter respells string literals of a file in place, so the rest
// of the file keeps its formatting.
type literalRewriter struct {
	data  []byte
	edits []edit
}

// rewrite records an edit replacing s with value. It fails for literals whose
// source text is not the plain quoted value (escapes, triple quotes).
func (lr *literalRewriter) rewrite(s *build.StringExpr, value string) bool {
	start, end := s.Span()
	raw := string(lr.data[start.Byte:end.Byte])
	for _, q := range []string{`"`, `'`} {
		if raw == q+s.Value+q {
			lr.edits = append(lr.edits, edit{start: start.Byte, end: end.Byte, text: q + value + q})
			return true
		}
	}
	return false
}

// finish records the edited file in out, if anything changed.
func (lr *literalRewriter) finish(p string, out map[string][]byte, rep *report) {
	if len(lr.edits) > 0 {
		out[p] = applyEdits(lr.data, lr.edits)
		rep.countRewrites(p, len(lr.edits))
	}
}

// mapSpelled parses an absolute label and maps it; false when s is not a
// label or didn't move.
func (m *labelMap) mapSpelled(s string) (labels.Label, bool) {
	l, ok := parseLabel(s, "")
	if !ok {
		return labels.Label{}, false
	}
	nl, mk := m.mapLabel(l)
	return nl, mk != unmapped
}

// mapAbsolute returns the new spelling of an absolute label that moved.
func (m *labelMap) mapAbsolute(s string) (string, bool) {
	if !isAbsolute(s) {
		return "", false
	}
	nl, ok := m.mapSpelled(s)
	if !ok {
		return "", false
	}
	return nl.Format(), true
}

// mapPrefixed returns the new spelling of a label that moved, where root
// ("//" or "//prefix") stands for the workspace root.
func (m *labelMap) mapPrefixed(s, root string) (string, bool) {
	rest, ok := strings.CutPrefix(s, root)
	if !ok || (root != "//" && rest != "" && rest[0] != '/' && rest[0] != ':') {
		return "", false
	}
	nl, ok := m.mapSpelled("//" + strings.TrimPrefix(rest, "/"))
	if !ok {
		return "", false
	}
	pkg := nl.Package
	if pkg != "" && root != "//" {
		pkg = "/" + pkg
	}
	return root + pkg + ":" + nl.Target, true
}

// rewriteBzl rewrites absolute labels of moved targets and files in .bzl
// files, and warns about relative strings that may refer to them.
func rewriteBzl(ws *workspace, m *labelMap, out map[string][]byte, rep *report) {
	for _, p := range ws.bzlFiles {
		f, data, err := ws.parse(p)
		if err != nil {
			rep.warnf("bzl-mention", position(p), "cannot parse, not scanned: %v", err)
			continue
		}
		lr := &literalRewriter{data: data}
		walkAny(f, func(s *build.StringExpr, _ strKind) {
			if v, ok := m.mapAbsolute(s.Value); ok {
				if !lr.rewrite(s, v) {
					rep.warnf("bzl-mention", posOf(p, s), "%q refers to something moved to //%s, but cannot be rewritten in place; update it by hand", s.Value, m.dst)
				}
			} else if bzlMentionsMoved(s.Value, pkgOf(p), m) {
				rep.warnf("bzl-mention", posOf(p, s), "%q refers to something moved to //%s; update it by hand", s.Value, m.dst)
			}
		})
		lr.finish(p, out, rep)
	}
}

// rewriteExtraFiles rewrites labels of moved targets and files in the extra
// files. Their labels are spelled "//<prefix>/<package>:<name>".
func rewriteExtraFiles(extra extraFiles, m *labelMap, out map[string][]byte, rep *report) error {
	root := "//" + strings.Trim(extra.prefix, "/")
	for _, p := range extra.paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := build.Parse(p, data)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", p, err)
		}
		lr := &literalRewriter{data: data}
		walkAny(f, func(s *build.StringExpr, _ strKind) {
			if v, ok := m.mapPrefixed(s.Value, root); ok && !lr.rewrite(s, v) {
				rep.warnf("extra-file-mention", posOf(p, s), "%q refers to something moved to //%s, but cannot be rewritten in place; update it by hand", s.Value, m.dst)
			}
		})
		lr.finish(p, out, rep)
	}
	return nil
}
