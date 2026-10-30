package movetargets

import (
	"bytes"
	"strings"

	"github.com/bazelbuild/buildtools/build"
	"github.com/bazelbuild/buildtools/labels"
)

// rewriteRefs rewrites the references in a BUILD file of package pkg and
// records the number of changed strings. In the source package, top-level
// assignments are only reported, because their values may be used anywhere.
func rewriteRefs(f *build.File, filePath, pkg string, m *labelMap, a attrKinds, rep *report) {
	n := 0
	for _, st := range f.Stmt {
		if as, ok := st.(*build.AssignExpr); ok && pkg == m.src {
			warnAssignment(as, filePath, m, rep)
			continue
		}
		a.forEachString(st, func(s *build.StringExpr, kind strKind) {
			v, mk := m.rewriteString(s.Value, kind, pkg, pkg)
			if mk == mappedFile && pkg == m.src {
				rep.warnf("leftover-reference", posOf(filePath, s), "%q now belongs to //%s; rewritten to %q, which needs an exports_files there", s.Value, m.dst, v)
			}
			if v != s.Value {
				s.Value = v
				n++
			}
		})
	}
	rep.countRewrites(filePath, n)
}

func warnAssignment(as *build.AssignExpr, filePath string, m *labelMap, rep *report) {
	walkAny(as.RHS, func(s *build.StringExpr, _ strKind) {
		if l, ok := resolve(s.Value, labelString, m.src); ok {
			if _, mk := m.mapLabel(l); mk != unmapped {
				rep.warnf("assignment-mentions-moved-file", posOf(filePath, s), "%s mentions %q, which moves to //%s; update it by hand", identName(as.LHS), s.Value, m.dst)
			}
		}
	})
}

// rewriteWorkspace rewrites references in every BUILD file other than the
// source and destination, adding changed files to out.
func rewriteWorkspace(ws *workspace, m *labelMap, a attrKinds, out map[string][]byte, rep *report) error {
	needle := []byte("//" + m.src)
	for _, pkg := range ws.packages() {
		if pkg == m.src || pkg == m.dst {
			continue
		}
		p := ws.buildFiles[pkg]
		data, err := ws.read(p)
		if err != nil {
			return err
		}
		if !bytes.Contains(data, needle) {
			continue
		}
		f, err := build.ParseBuild(p, data)
		if err != nil {
			return err
		}
		rewriteRefs(f, p, pkg, m, a, rep)
		addIfChanged(out, p, data, f)
	}
	return nil
}

func addIfChanged(out map[string][]byte, filePath string, old []byte, f *build.File) {
	if data := build.Format(f); old == nil || !bytes.Equal(data, old) {
		out[filePath] = data
	}
}

// bzlMentionsMoved reports whether a string in a .bzl file of package pkg
// names something that moves. Unlike in BUILD files, ":x" may also mean the
// source package, because macros resolve plain strings against their caller.
func bzlMentionsMoved(s, pkg string, m *labelMap) bool {
	if l, ok := parseLabel(s, pkg); ok {
		if _, mk := m.mapLabel(l); mk != unmapped {
			return true
		}
	}
	if name, ok := strings.CutPrefix(s, ":"); ok {
		_, mk := m.mapLabel(labels.Label{Package: m.src, Target: name})
		return mk != unmapped
	}
	return false
}
