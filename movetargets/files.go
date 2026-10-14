package movetargets

import (
	"path"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

// ruleFiles returns the source-package files named in a rule's file attributes
// and the include patterns of its globs.
func ruleFiles(call *build.CallExpr, a attrKinds, m *labelMap) (files, patterns []string) {
	for _, arg := range call.List {
		kw, ok := arg.(*build.AssignExpr)
		if !ok || a.kindOf(identName(kw.LHS)) != fileString {
			continue
		}
		walkValue(kw.RHS, fileString, func(s *build.StringExpr, kind strKind) {
			if f, ok := fileName(s.Value, kind, m); ok {
				files = append(files, f)
			}
		})
	}
	for _, p := range includePatterns(call) {
		patterns = append(patterns, p.Value)
	}
	return files, patterns
}

// fileName returns the package-relative path if s names a file in the source package.
func fileName(s string, kind strKind, m *labelMap) (string, bool) {
	l, ok := resolve(s, kind, m.src)
	if !ok || l.Package != m.src || !m.isLocal(l.Repository) || m.srcRules[l.Target] {
		return "", false
	}
	if !strings.ContainsAny(l.Target, "/.") {
		return "", false // Probably a rule created by a macro or comprehension.
	}
	return l.Target, true
}

func globs(e build.Expr) []*build.CallExpr {
	var gs []*build.CallExpr
	build.Walk(e, func(x build.Expr, _ []build.Expr) {
		if c, ok := x.(*build.CallExpr); ok && callName(c) == "glob" {
			gs = append(gs, c)
		}
	})
	return gs
}

// includePatterns returns the include patterns of every glob() call below e.
func includePatterns(e build.Expr) []*build.StringExpr {
	var patterns []*build.StringExpr
	for _, g := range globs(e) {
		include, _ := globPatterns(g)
		patterns = append(patterns, include...)
	}
	return patterns
}

// globPatterns returns the literal include and exclude patterns of a glob() call.
func globPatterns(g *build.CallExpr) (include, exclude []*build.StringExpr) {
	for i, arg := range g.List {
		key := ""
		if kw, ok := arg.(*build.AssignExpr); ok {
			key, arg = identName(kw.LHS), kw.RHS
		}
		strs := listStrings(arg)
		switch {
		case key == "include" || (key == "" && i == 0):
			include = append(include, strs...)
		case key == "exclude" || (key == "" && i == 1):
			exclude = append(exclude, strs...)
		}
	}
	return include, exclude
}

// rerootGlobs strips the destination directory from the glob patterns in e.
func rerootGlobs(e build.Expr, rel string) {
	for _, g := range globs(e) {
		include, exclude := globPatterns(g)
		for _, p := range append(include, exclude...) {
			p.Value = strings.TrimPrefix(p.Value, rel+"/")
		}
	}
}

type placement int

const (
	noFiles   placement = iota
	outside             // no file is under the destination directory
	inside              // every file is under the destination directory
	nested              // strict mode: some file is in a subdirectory of it
	straddles           // some files are under it and some are not
)

// place classifies paths (relative to the source package) against the
// destination directory rel.
func place(paths []string, rel string, strict bool) placement {
	if len(paths) == 0 {
		return noFiles
	}
	in, deep := 0, false
	for _, p := range paths {
		if rest, ok := strings.CutPrefix(p, rel+"/"); ok {
			in++
			deep = deep || strings.Contains(rest, "/") || strings.Contains(rest, "**")
		}
	}
	switch {
	case in == 0:
		return outside
	case in < len(paths):
		return straddles
	case strict && deep:
		return nested
	}
	return inside
}

// globCanReach conservatively reports whether pattern could match a path under dir.
// It works for literal paths too.
func globCanReach(pattern, dir string) bool {
	ps, ds := strings.Split(pattern, "/"), strings.Split(dir, "/")
	for i, d := range ds {
		if i >= len(ps) {
			return false
		}
		if ps[i] == "**" {
			return true
		}
		if ok, _ := path.Match(ps[i], d); !ok {
			return false
		}
	}
	return len(ps) > len(ds)
}
